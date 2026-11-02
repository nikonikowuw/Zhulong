# Implementation Plan: ROI/MASK 区域策略与宿主统一过滤

## 前置条件

1. **⚠️ 先处理在途改动**：`web/src/features/tasks/components/channel-task-dialog.tsx`、`web/src/features/tasks/locales/{en,zh-Hans,zh-Hant}.json` 当前**已有未提交改动**（来自进行中的 `10-17-algo-config-schema-dynamic-form`）。本任务要改同一批文件，必须先把那批改动提交或明确隔离，否则 diff 会互相污染、且无法区分回归来源。
2. 开工前跑 `trellis-before-dev` 加载 backend / frontend / native 三层 spec 清单（本任务跨三层）。
3. 确认基线绿：`make check`。

## 执行顺序

顺序由依赖决定：纯几何包最先（零依赖、可独立验证），native 归属缺口先于宿主过滤（否则过滤无法定位任务），前端最后（依赖后端契约冻结）。

### 步骤 1 — `internal/region` 纯几何包

- 新增 `internal/region/region.go`：`Rect [4]float64`（`BottomCenter()` / `Contains()` / `Validate()`）、`Config{ROI, Mask []Rect}`、`ParseConfig`、`Validate`、`IsEmpty`，阈值具名常量（每类上限 8、最小边长 0.01）。
- 新增 `internal/region/filter.go`：`Config.Filter(aitypes.InferenceResult)`，实现 §4.2 顺序与 §4.3 适用对象；`IsEmpty()` 走快速返回。
- 表驱动单测覆盖：空配置透传、ROI include、MASK exclude、两者重叠时 MASK 优先、空结果信标透传、退化矩形拒绝、数量上限、`[0,1]` 边界值。
- 验证：`python3 native/scripts/build.py go test -race ./internal/region`

### 步骤 2 — 迁移与任务模型

- 新增 `internal/database/migrations/000012_add_region_config_to_analysis_tasks.{up,down}.sql`（只追加）。
- `internal/task/model.go`：`RegionConfigJSON` 列（`gorm:"column:region_config_json;not null;default:'{}'"`）+ DTO 字段（camelCase 主 + snake_case 兼容），对齐既有 `ConfigJSON` / `ConfigJSONAlt` 惯例。
- `internal/task/service.go`：在 `CreateTask`（`:99` 附近）与 `UpdateTask`（`:160` 附近）调用区域校验，失败返回稳定机器码 `apperr.Invalid`。
- 验证：`python3 native/scripts/build.py go test -race ./internal/task`
- 手动验证迁移可 up/down：`make check` 的 go-check 会跑迁移相关用例；另需确认 SQLite 驱动版本支持 `DROP COLUMN`，否则改走建新表路径。

### 步骤 3 — Native：结果算法归属

- `native/src/algorithm/runner/runner.cpp`：
  - `main` 参数解析（`:778-802`）新增 `--algorithm-id <id>`，并更新 usage 文本（`:817`）。
  - `serialize_inference_result`（`:102`）增加 `algorithm_id` 参数并输出 `"algorithm_id":"..."`；同步更新**唯一调用点** `:635`。
  - 所有分支（空结果、detection、classification、person_structure、custom）都必须输出该字段，保证 Go 侧解析一致。
- ⚠️ 保持既有约束：不向 C ABI 传 Go 指针、异常不穿越边界；本改动仅在 runner 内部，不触碰 `zhulong_plugin_api_v1` 布局。
- 验证：`make native-build`，随后 `make native-test`。

### 步骤 4 — Go 侧归属贯通

- `internal/algorunner/process.go`：`ProcessConfig` 增加 `AlgorithmID`，在 `:129-141` 的参数装配中传 `--algorithm-id`。
- `internal/algorunner/supervisor.go`：三处 `AlgorithmName: algo.Name`（`:207,376,434,707`）旁补 `AlgorithmID: algo.ID`。
- `internal/aitypes/model.go`：`RawInferencePayload` 增加 `algorithm_id`；`ParseInferenceResult`（`:145`）**优先取载荷字段，缺省回退到传入参数**（保持签名不变，兼容 `internal/algorithm/result.go:44` 的别名与既有测试）。
- `internal/camera/stream_hub.go:554`：**保持传 `""`（有意为之，已加注释说明）**。该 AI 流是「按相机」的，同一相机的所有算法 runner 共享同一条订阅，hub 无法把某条结果归属到某个算法；归属只能由 runner 经载荷回传。传一个猜测的非空值反而会在载荷缺字段时把其他算法的结果错误归属，因此空值更安全。
- 单测：载荷含/不含 `algorithm_id` 两条路径；落库 `algorithm_id` 非空（覆盖 F5 缺陷）。
- 验证：`python3 native/scripts/build.py go test -race ./internal/aitypes ./internal/algorunner ./internal/camera ./internal/record`

### 步骤 5 — 区域策略快照与扇出

- 新增 `internal/task/region_config.go`：`RegionConfigCache`（`atomic.Pointer` copy-on-write 快照）、`NewRegionConfigCache`（**构造函数纯粹，不查库**）、`Reload(ctx)`、`ConfigFor(cameraID, algorithmID)`。
- `internal/app`：`task.NewService` 接受 `[]TaskChangeListener`，把 `algorithm.Service` 与 `RegionConfigCache` 一起注册（`RegionConfigCache` 自行实现 `OnTaskChanged` 并在内部记录错误，无需额外扇出适配器）；生命周期 `OnStart` 钩子做一次预热 `Reload`。
- 单测：快照重建后旧读者不受影响；未命中返回 `(零值, false)`；禁用任务不进入快照。
- 验证：`python3 native/scripts/build.py go test -race ./internal/task ./internal/app`

### 步骤 6 — 汇聚点过滤

- `internal/camera/stream_hub.go`：定义消费者侧接口 `RegionConfigProvider`（沿用 `AIResultConsumer` 模式，**不导入 `internal/task`**），新增 `SetRegionConfigProvider`。
- 在 `consumeAIResults`（`:528-565`）中，于 `aiConsumer` 与 `eventHub` **两路分发之前**调用 `region.Config.Filter`。
- 单测：ROI/MASK 生效后两路输出一致；无区域配置时结果与改动前一致；分类结果不受影响。
- 验证：`python3 native/scripts/build.py go test -race ./internal/camera`

### 步骤 7 — 前端：实时画面门面

- 新增 `web/src/features/live/components/camera-frame-surface.tsx`：复用 `useStreamConnection`（**main 流**）+ WebCodecs 解码 + canvas 绘制；支持 `paused` 冻结、`overlay(imageRect)` 同坐标系叠加、`onStateChange` 状态回调（§5.6 映射表）。
- 解码器 + rAF 渲染循环抽到 `hooks/use-webcodecs-render-loop.ts`，与既有 `live-player.tsx` 共用一份实现（两处各写一份时清理顺序与丢帧策略极易走偏，例如 canvas 已卸载却忘记 `frame.close()`）。
- canvas 用 `absolute inset-0 h-full w-full object-contain` 撑满容器：作为 flex item 时 `min-width: auto` 会把元素钉在画面内在尺寸上，`w-full` 无法收缩，画面会被 `overflow-hidden` 裁掉。元素盒恒等于容器盒后，叠加层坐标空间才可精确计算。
- 内容区几何（`object-contain` 的居中与留白）抽到 `services/frame-geometry.ts` 并单测；叠加层容器按内容区尺寸与偏移定位，letterbox 留白不计入坐标空间。
- `web/src/features/live/index.tsx` 增加门面导出（§5.5）。
- 单测：首帧前为 `loading` 不可编辑；`error`/`closed` → `unavailable`；`paused` 后不再解码新帧；首帧绘制后才挂载 `overlay`；`measureContentRect` 对宽画面/高画面/比例一致/小画面四种情形的留白与偏移。
- 验证：`cd web && pnpm test -- src/features/live`

### 步骤 8 — 前端：区域编辑器与集成

- 新增 `web/src/features/tasks/data/region-config.ts`：Zod 解析/序列化/校验，与 Go 契约逐字段对齐（含上限与最小边长常量）；另含编辑期草稿模型（`RegionItem`/`RegionDraft`、`toDraft`/`fromDraft`、分类→分类色相映射）。
- 草稿携带稳定 id（由 `use-region-editor` 内的自增 ref 生成，不引入模块级可变状态），仅供 React key 与撤销历史使用，序列化时丢弃；用数组下标当 key 会在删除/重排时复用错位的 DOM。
- 新增 `region-editor-dialog.tsx`（弹窗骨架）/ `region-editor-toolbar.tsx`（工具条、冻结、撤销、重置）/ `region-canvas.tsx`（渲染）/ `region-rect-list.tsx`（清单）/ `use-region-drag.ts`（拖拽状态机）/ `region-drag-types.ts`（交互契约）/ `region-geometry.ts`（纯几何换算）。
- 绘制、拖动、四角缩放、删除、`[0,1]` 钳制、退化矩形丢弃、底部中心标记、撤销最近一次操作（快照在操作真正变更前压入，一次拖拽只压一次）。
- 画布图元与清单配色一律走 `@/lib/category-palette` 的分类色板（ROI→emerald、MASK→violet），不得手写色值或占用状态色相。
- `channel-task-dialog.tsx`：每个已勾选算法卡片增加区域配置入口（显示 `ROI n · MASK m` 计数）。
- `data/schema.ts` + `api/tasks-api.ts`：增加可选 `regionConfigJson`。
- `locales/{en,zh-Hans,zh-Hant}.json`：补齐文案，**注意与像素级「隐私掩码」区分用词**；`live` 命名空间新增 `frame.*` 状态文案，不得在组件里硬编码中文。
- 单测：绘制/缩放/删除后序列化正确；归一化 ↔ 像素往返映射在非 16:9 与缩放容器下正确；`unavailable` 时保存被禁止；几何换算的边界（贴边回推、最小边长、越界钳制）；画布拖拽产生归一化矩形。
- 验证：`cd web && pnpm test -- src/features/tasks`

### 步骤 9 — 收口

- 生成 API 文档：`make api-docs`（新增字段）。
- 全量质量门禁：`make check`（前端 lint/type-check/test + native-test + api-docs + go-check）。
- 手工验收：真机/真流下绘制区域 → 保存 → 观察实时叠加与告警是否按区域过滤；断流场景确认降级提示且无法保存。

## 验证命令速查

```bash
# Go（凡(间接)依赖 internal/engine 的包一律走 wrapper）
python3 native/scripts/build.py go test -race ./internal/region ./internal/task
python3 native/scripts/build.py go test -race ./internal/aitypes ./internal/algorunner ./internal/camera ./internal/record

# Native（改完 C++ 必须重建，Air 只监听 Go/TOML）
make native-build && make native-test

# 前端（真实无头浏览器，不是 JSDOM）
cd web && pnpm test -- src/features/live
cd web && pnpm test -- src/features/tasks

# 收工门禁
make check
```

## 风险文件与回滚点

| 文件/区域 | 风险 | 应对 |
| :--- | :--- | :--- |
| `internal/camera/stream_hub.go` | 每帧热路径；误改会造成丢帧或内存增长 | 过滤走原子快照 + `IsEmpty()` 快速返回；`-race` 必过 |
| `native/src/algorithm/runner/runner.cpp` | 触及结果上报契约，影响所有算法包 | 只加字段不改 ABI 布局；所有分支都输出字段；`make native-test` |
| `internal/database/migrations/000012_*` | 迁移不可回改 | 只追加；down 需实测可回滚 |
| `channel-task-dialog.tsx` | 与在途任务改动冲突 | 见「前置条件 1」，先隔离/提交在途改动 |
| `internal/task/service.go` 监听者 | 改签名会波及算法热重启与既有调用点 | 形参改为 `[]TaskChangeListener`，逐个 nil 判空转发；`algorithm.Service` 行为不变 |
| 区域策略缓存 | 陈旧快照导致过滤不生效 | 任务 CRUD 后同步 `Reload`；未命中按「无限制」处理（安全方向） |

**紧急止血**：清空所有任务的 `region_config_json` 即完全关闭过滤，无需改代码或回滚。

## `task.py start` 前检查

- [ ] 在途算法配置任务的改动已提交或明确隔离
- [ ] 三层 spec 已加载（`trellis-before-dev`）
- [ ] `design.md` 中的阈值常量（每类上限 8、最小边长 0.01）已确认
- [ ] `implement.jsonl` / `check.jsonl` 已按本任务实际规范来源整理
