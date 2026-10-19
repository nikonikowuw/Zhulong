# Technical Design: ROI/MASK 区域策略与宿主统一过滤

## 1. 设计原则

**区域是宿主策略（policy），不是算法参数（parameter）。** 因此声明、编辑、校验、执行四件事全部由宿主承担，算法包**零参与**——避免每个算法包重复实现同一套区域过滤规则。

推论：

- 区域**不放进** `config_json`（那是算法参数，且会被算法包 schema 的 `additionalProperties: false` 拒绝），而是独立的 `region_config_json` 列。
- 区域**不依赖**算法包 schema 声明：任何算法任务都能配置区域。
- 执行**只在宿主唯一的推理结果汇聚点**做一次。

---

## 2. 分层拓扑

```txt
┌──────────────────────────────────────────────────────────────────────────┐
│ 前端                                                                      │
│  ChannelTaskDialog                                                       │
│    └─► RegionEditorDialog（按「相机+算法」粒度）                            │
│          ├─ CameraFrameSurface  ← 经 @/features/live 门面导出              │
│          │     └─ useStreamConnection(main 流) + WebCodecs → 背景 canvas   │
│          ├─ RegionCanvas（叠加 canvas：绘制/拖动/缩放/删除矩形）             │
│          └─ RegionRectList（数值读数 + 删除 + 冻结画面）                    │
└──────────────────────────────────────────────────────────────────────────┘
                                   │ regionConfigJson
┌──────────────────────────────────────────────────────────────────────────┐
│ Go 宿主                                                                   │
│  internal/task                                                           │
│    ├─ validateRegionConfig → internal/region（纯几何校验）                 │
│    ├─ analysis_tasks.region_config_json（迁移 000012）                     │
│    └─ RegionConfigCache（原子快照，按 camera+algorithm 索引）              │
│                                   │ RegionConfigProvider（消费者侧接口）    │
│  internal/camera                                                         │
│    └─ StreamHub.consumeAIResults ← 唯一汇聚点，两路分发前统一过滤           │
│          ├─ aiConsumer.ProcessInferenceResult（落库：抓拍/告警/识别）       │
│          └─ eventHub.BroadcastAIResult（前端实时叠加）                      │
└──────────────────────────────────────────────────────────────────────────┘
                                   ▲ algorithm_id
┌──────────────────────────────────────────────────────────────────────────┐
│ Native                                                                    │
│  zhulong_algo_runner --algorithm-id <id> → MSG_RESULT 载荷含 algorithm_id  │
└──────────────────────────────────────────────────────────────────────────┘
```

---

## 3. 数据契约

### 3.1 持久化

迁移 `000012_add_region_config_to_analysis_tasks.{up,down}.sql`：

```sql
-- up
ALTER TABLE analysis_tasks ADD COLUMN region_config_json TEXT NOT NULL DEFAULT '{}';
-- down
ALTER TABLE analysis_tasks DROP COLUMN region_config_json;
```

⚠️ 迁移为**只追加**，不修改既有 `000007`。SQLite 3.35+ 支持 `DROP COLUMN`；若构建的 SQLite 版本不足，down 需走「建新表 → 拷贝 → 换名」路径，实现时以实际驱动版本为准验证。

### 3.2 区域 JSON 结构

```json
{
  "roi":  [[0.10, 0.20, 0.50, 0.90], [0.55, 0.20, 0.95, 0.90]],
  "mask": [[0.30, 0.60, 0.40, 0.75]]
}
```

- 矩形为 `[x1, y1, x2, y2]`，与既有检测框契约（`algo-plugin-contract.md` §2「全系统统一采用 `[x1,y1,x2,y2]` 严格归一化两点坐标」）一致，而非自定义对象键，避免引入第二套坐标表示。
- `x1 < x2`、`y1 < y2`，全部落在 `[0,1]`。
- `{}` / `""` 均解析为空配置（无限制），**默认行为与改动前完全一致**——这是上线安全性的关键：功能上线但无人配置区域时，过滤是 no-op。

### 3.3 校验规则（`internal/region`）

| 规则 | 阈值 | 依据 |
| :--- | :--- | :--- |
| 坐标范围 | 每个分量 ∈ `[0,1]` | 归一化契约 |
| 非退化 | `x1 < x2` 且 `y1 < y2` | 否则 `Contains` 恒假 |
| 最小边长 | 每边 ≥ `0.01`（画面 1%） | 拒绝误触产生的极小矩形 |
| 数量上限 | 每类 ≤ `8` | 行业锚点：Axis exclude ≤ 5、Hikvision 区域 ≤ 4；矩形比多边形便宜，取 8 留余量 |

阈值集中为 `internal/region` 的具名常量，便于后续调整与前端复用。

---

## 4. 判定与过滤语义

### 4.1 归属判定：目标框底部中心

```txt
bottom-center = ((x1 + x2) / 2, y2)
```

**只取这一个点，重叠面积不参与判定**——与 DeepStream（官方规定且不可改）、Frigate（文档明示「与重叠面积无关」）、Axis（人的脚部）三家一致。

理由：底部中心对入侵/区域类场景语义最准（人站在区域内即算进入），不受目标框高度影响；用框中心会在近景/高大目标时漏报。

### 4.2 过滤顺序

```txt
keep(obj):
  if len(roi) > 0 and obj.bottom_center not in any roi:  drop
  if obj.bottom_center in any mask:                       drop
  keep
```

- ROI 为空 → 不做 include 限制；MASK 为空 → 不做排除。
- **MASK 优先**：ROI 与 MASK 重叠时排除胜出（与 Axis「exclude area 在 include area 内部挖掉」语义一致）。

### 4.3 适用对象

| 结果类型 | 判定用框 | 说明 |
| :--- | :--- | :--- |
| `detections` | `DetectionItem.Box` | 逐个判定 |
| `persons` | `PersonItem.BodyBox` | 逐个判定；保留则其嵌套 `faces` 一并保留 |
| `classifications` | 无 | 无框，不受影响 |

⚠️ **空结果信标必须原样透传**：`MSG_RESULT` 的「目标消失退出信标」（`algo-plugin-contract.md` §9.3）是空列表，过滤后仍为空，前端才能清空滞留画框。过滤不得把空结果丢弃或改写。

---

## 5. 关键设计点

### 5.1 为什么必须先补「算法归属」

`consumeAIResults` 的订阅是**按流（相机）**建立的，同一相机上多个算法的结果混在同一条订阅上，而载荷与回调都没有算法标识（`RawInferencePayload` 无该字段；`stream_hub.go:540` 传空串）。没有归属就无法查该任务的区域配置。

方案：runner 增加 `--algorithm-id`，在结果 JSON 中输出 `algorithm_id`。

- `native/src/algorithm/runner/runner.cpp`：`main` 参数解析处（`:778-802`）增加 `--algorithm-id`；`serialize_inference_result`（`:102`，**唯一调用点** `:635`）增加 `algorithm_id` 参数并输出该字段。
- 不复用 `client_name`：它是 32 字节定长缓冲、且 `--name` 传的是 `algo.Name`（人类可读、可能被截断），无法可靠映射回 `algo.ID`。
- 附带收益：修复 F5 —— 落库的 `algorithm_id` 不再恒为空。

### 5.2 热路径不能查库：原子快照

过滤发生在**每帧**，绝不能逐帧查数据库。

`RegionConfigCache`（`internal/task`）持有 `atomic.Pointer[map[RegionKey]region.Config]`：

- `Reload(ctx)`：`ListTasks` → 过滤 `Enabled` → 构建全新 map → 原子换指针（**copy-on-write，读者零锁**）。与仓库既有 `GalleryRegistry` 快照思路一致。
- `ConfigFor(cameraID, algorithmID)`：原子读 + map 查找，命中失败返回 `(零值, false)`，调用方按「无区域限制」处理。
- 触发时机：启动时（生命周期钩子，**不在构造函数**——构造函数必须纯粹）、以及任务创建/更新/删除后。
- 同步 reload 而非异步：任务 CRUD 是低频管理操作，一次 `SELECT` 开销可忽略；避免在 CRUD 路径派生带 `ctx` 的 goroutine（`ctx` 会随请求结束失效）。

### 5.3 单一监听者需要扇出

`internal/task/service.go` 原先只接受**一个** `TaskChangeListener`，由 `internal/algorithm.Service` 实现（`internal/algorithm/service.go:325`），而区域配置缓存也必须响应任务变更。

**实际交付方案（优于原计划的适配器）**：把 `NewService` 的形参改为 `[]TaskChangeListener`，`RegionConfigCache` 直接实现 `OnTaskChanged`（内部调用 `Reload` 并自行记录错误）。`internal/app` 只需把两个监听者一起传入，不必引入只做转发的扇出适配器，错误日志也不再重复。

### 5.4 依赖方向：消费者侧接口

`internal/camera` **不**导入 `internal/task`（避免耦合与潜在环）。沿用该包既有 `AIResultConsumer` 模式，在 `internal/camera` 定义消费者侧接口：

```go
type RegionConfigProvider interface {
    ConfigFor(cameraID, algorithmID string) (region.Config, bool)
}
```

由 `internal/app` 注入 `RegionConfigCache` 实现。`internal/region` 为纯几何包（仅依赖 `internal/aitypes` 用于过滤适配），`internal/task` 与 `internal/camera` 均可安全引用。

### 5.5 前端：跨特性必须走门面

`frontend/directory-structure.md` §4.2 明确「跨业务特性引用必须通过对应特性的门面入口」，§2.2 允许 `index.tsx` 导出「跨模块共享组件」。因此：

- 在 `web/src/features/live/` 内实现 `CameraFrameSurface`，并由 `web/src/features/live/index.tsx` 导出。
- `tasks` 只从 `@/features/live` 导入，**不穿透** `@/features/live/components/...`。

`CameraFrameSurface` 契约：

```ts
interface CameraFrameSurfaceProps {
  cameraId: string | null
  paused?: boolean          // 冻结画面：停止绘制新帧，叠加层保留
  overlay?: (imageRect: ImageRect) => React.ReactNode  // 容器已按绘制区定位，回调内直接用相对坐标
  onStateChange?: (state: FrameSurfaceState) => void
}
type FrameSurfaceState = 'no-camera' | 'loading' | 'live' | 'unavailable'
```

**把叠加层放在同一个组件内部**（而非由 `tasks` 自行定位）是为了让「叠加层与画面绘制区严格重合」这个不变量只由一处负责——避免 letterbox 留白被误算进坐标空间（PRD R6 / 验收 AC8）。

### 5.6 降级态与可编辑门禁

`StreamStatus` 为 `'idle' | 'connecting' | 'connected' | 'reconnecting' | 'error' | 'closed'`，映射为：

| StreamStatus | FrameSurfaceState | 可编辑区域 |
| :--- | :--- | :--- |
| `idle` / `connecting` / `reconnecting` | `loading` | 否 |
| `connected` 且**已渲染首帧** | `live` | 是 |
| `error` / `closed` | `unavailable` | 否（显式降级提示） |
| 无 `cameraId` | `no-camera` | 否 |

门禁依据是「**已渲染首帧**」而非「已连接」：连接成功但首帧未到时空画布上绘制会产出错误坐标。

`unavailable` 时：禁用绘制与保存，明确提示无参考画面；**保留已存区域的只读展示**，允许用户切到 JSON/数值方式查看，但不得保存新坐标（PRD R5 / 验收 AC8）。

### 5.7 编辑器交互

- 工具：新增 ROI / 新增 MASK / 选择 / 删除；矩形可拖动与四角缩放。
- 拖拽过程实时做 `[0,1]` 钳制，松手后若退化（边长 < 最小阈值）则丢弃该矩形并提示，而不是保存非法值。
- 数值读数随拖拽实时更新，便于精确核对。
- 底部中心判定点可在选中矩形时以标记显示，帮助用户理解判定语义。
- 撤销：以「一次用户操作 = 一条历史」的粒度维护快照栈（上限 50 条），可连续回退；快照在操作**真正产生变更之前**压入，因此一次连续拖拽只占一条历史，不会堆满中间态。整段撤销历史只存在于编辑会话内，不落库。

### 5.8 生效时效

区域变更 → 任务更新成功 → 扇出 `Reload` → 原子换快照，下一帧即生效，无需重启（验收 AC9）。前端实时叠加与落库共用同一过滤结果，因此两侧口径必然一致。

---

## 6. 兼容性与迁移

- **默认安全**：新列默认 `'{}'`，未配置区域时 `Config.Filter` 走 `IsEmpty()` 快速返回原结果，行为与改动前逐字节一致。
- **API 兼容**：新增 `regionConfigJson` 字段为可选；沿用既有 DTO 的「camelCase 主字段 + snake_case 兼容字段」惯例，避免破坏既有调用方。
- **Native 兼容**：`--algorithm-id` 为可选参数，缺省时载荷 `algorithm_id` 输出空串，Go 侧回退到调用点传入值——老版本 runner 与新宿主、新 runner 与老宿主均可工作。
- **API 文档**：新增字段需同步 `make api-docs`。

---

## 7. 取舍与已知限制

| 取舍 | 说明 |
| :--- | :--- |
| **结果级过滤，不省算力** | 模型仍在全画面推理，ROI 只决定「上报什么」。精确等价于「只上报 ROI 内的目标」，**不等价于**「只在 ROI 内推理」。真正的算力节省需要 per-task 配置下发 + ROI 预裁剪，已明确列为 Out of Scope。 |
| **单点判定有抖动** | 底部中心可能因框略偏而误判。基础防抖由既有 track 冷却覆盖；更强的时间防抖（zone inertia）列为 Out of Scope。 |
| **MASK ≠ 隐私掩码** | 本设计只丢弃结果，**不修改画面**。若需像素级遮挡（隐私合规），是另一个特性。命名与文案须避免混淆。 |
| **每类 8 个矩形上限** | 取行业锚点（Axis ≤ 5、Hikvision ≤ 4）之上的宽松值；超限由校验拒绝。 |
| **区域策略按任务存储** | 同一相机上不同算法可配不同区域，符合 Axis「每个 scenario 独立区域」模型；代价是无法跨任务复用区域模板（Out of Scope）。 |

---

## 8. 回滚

- **Go/Native**：回滚二进制即可。老 runner 不输出 `algorithm_id` 时，Go 侧回退路径保证不 panic、不误判（归属为空 → 无区域限制 → 不过滤）。
- **前端**：回滚静态资源；新增字段为可选，老后端不返回时按空配置处理。
- **数据库**：`region_config_json` 列可保留不删（无消费者即无影响）；必要时执行 down 迁移。
- **紧急止血**：无需改代码——清空所有任务的 `region_config_json` 即为完全关闭过滤。

---

## 9. 涉及文件清单

**Native**

- `native/src/algorithm/runner/runner.cpp`（参数解析 + 序列化）
- 如有 runner 参数相关 native 测试，同步补充

**Go**

- `internal/database/migrations/000012_add_region_config_to_analysis_tasks.{up,down}.sql`（新增）
- `internal/region/{region.go,filter.go,region_test.go,filter_test.go}`（新增）
- `internal/task/{model.go,service.go,store.go,region_config.go}`（列、校验、快照缓存）
- `internal/aitypes/model.go`（载荷字段 + 解析回退）
- `internal/algorunner/{process.go,supervisor.go}`（`--algorithm-id` 下发）
- `internal/camera/stream_hub.go`（归属传入 + 汇聚点过滤 + 消费者侧接口）
- `internal/app/*`（扇出监听者、生命周期预热、依赖注入）
- `internal/apidocs/*`（`make api-docs` 生成）

**前端**

- `web/src/features/live/components/camera-frame-surface.tsx`（新增）、`web/src/features/live/index.tsx`（门面导出）
- `web/src/features/live/hooks/use-webcodecs-render-loop.ts`（新增，与 `live-player` 共用的解码 + rAF 渲染循环）
- `web/src/features/live/services/frame-geometry.ts`（新增，`object-contain` 内容区几何）
- `web/src/features/tasks/components/{region-editor-dialog.tsx,region-editor-toolbar.tsx,region-canvas.tsx,region-rect-list.tsx}`（新增）
- `web/src/features/tasks/components/{use-region-drag.ts,region-drag-types.ts,region-geometry.ts}`（新增，拖拽状态机 / 交互契约 / 纯几何换算）
- `web/src/features/tasks/hooks/use-region-editor.ts`（新增，草稿 + 撤销历史 + 稳定 id）
- `web/src/features/tasks/components/channel-task-dialog.tsx`（入口集成）
- `web/src/features/tasks/data/{region-config.ts,schema.ts}`、`api/tasks-api.ts`
- `web/src/lib/category-palette.ts`（新增画布图元/预览/独立文字三张分类色表）
- `web/src/features/live/locales/{en,zh-Hans,zh-Hant}.json`、`web/src/features/tasks/locales/{en,zh-Hans,zh-Hant}.json`
- 对应 `*.test.ts(x)`

**模块拆分理由**（`development-guidelines.md` 要求单文件不超 300 行）：拖拽状态机、交互契约、
纯几何换算三者拆开，几何换算因此可脱离 DOM 单测；渲染循环抽出后
`camera-frame-surface` 与 `live-player` 不再各写一份解码器生命周期。
