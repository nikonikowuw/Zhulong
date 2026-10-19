# PRD: ROI/MASK 区域策略与宿主统一过滤

## Goal

让 ROI（保留区域）与 MASK（排除区域）成为**宿主拥有的任务级策略**：用户可在任务配置弹窗中以该相机实时主码流画面为底图绘制多个归一化矩形，配置独立于算法包持久化与校验，并由宿主在推理结果汇聚点统一执行过滤，使实时叠加、抓拍、告警与录像元数据一致生效。

核心价值：区域语义**只实现一次**，所有算法包零改动、零重复；同时补齐缺失的推理结果算法归属。

---

## Background：已确认的仓库事实

| 编号 | 事实 | 证据 |
| :--- | :--- | :--- |
| F1 | `analysis_tasks` 含 `UNIQUE(camera_id, algorithm_id)`，故「相机+算法」唯一确定一个任务 | `internal/database/migrations/000007_create_algorithm_tables.up.sql:23-33`；最新迁移为 `000011` |
| F2 | 算法参数 `config_json` 仅被 JSON Schema 校验消费，无其他读取方 | `internal/task/config_schema.go:25`、`internal/task/service.go:99,160` |
| F3 | 算法进程**永远**收到空配置，`task_open` 无配置参数 | `native/src/algorithm/runner/runner.cpp:316`（`model_create("{}")`）、`:487` |
| F4 | 推理结果**无算法归属**：载荷无算法标识字段，且调用点传空字符串 | `internal/aitypes/model.go:133-142`、`internal/camera/stream_hub.go:540` |
| F5 | 由此产生**现存缺陷**：识别/抓拍/告警记录的 `algorithm_id` 实际恒为空 | 落库点 `internal/record/service.go:105,190,254` |
| F6 | 所有 AI 结果存在**唯一汇聚点**，同时供给落库与前端广播 | `internal/camera/stream_hub.go:528-565` |
| F7 | AI 分析流**固定 main 角色**，绘制底图必须用 main 流坐标才一致 | `internal/camera/stream_hub.go:305` |
| F8 | 前端已具备实时流订阅与解码能力，相机模型含 `streams[].width/height` | `web/src/features/live/services/stream-pool.ts:36`、`hooks/use-stream-connection.ts:19-40`、`web/src/features/cameras/data/schema.ts` |
| F9 | 已有按 track 的冷却去重，可作为基础时间防抖 | `internal/record/service.go:42` 及其 `recKey` 构造 |
| F10 | 行业实践一致：区域归属取**目标框底部中心**，重叠面积不参与 | 见本任务 `research/` 目录（DeepStream / Frigate / Axis 原文） |

### 行业实践要点（决定本 PRD 的语义选择）

- **判定点**：DeepStream 官方文档规定所有分析计算一律使用目标框底部中心，且**不允许自定义**；Frigate 明确「与重叠面积无关」；Axis 用人的脚部（即底部中心）。三家一致。
- **ROI 与 MASK 的关系**：Axis 采用「1 个 include area + 最多 5 个 exclude areas」，exclude area 是在 include area 内部挖掉的区域——与本需求最吻合。DeepStream 仅「1 个 ROI 列表 + 取反布尔」，表达力更弱。
- **术语陷阱**：行业中 "mask" 有两种含义——像素级隐私/运动掩码（改画面或运动检测）与结果级排除区（丢弃结果）。本需求是**后者**，命名与文案须避免与隐私掩码混淆。
- **单点判定需配时间防抖**：Frigate 的 zone inertia 与 Hikvision 的 Time Threshold 都是为此存在；本系统已由 F9 的 track 冷却覆盖基础防抖。

---

## Requirements

### R1. 任务级区域配置的数据契约与持久化

- `analysis_tasks` 新增列 `region_config_json TEXT NOT NULL DEFAULT '{}'`（新迁移 `000012`，成对 up/down，只追加不改历史迁移）。
- 与算法参数 `config_json` **并列而非混入**：算法包 schema 可能声明 `additionalProperties: false`，混入会被 `jsonschema-go` 严格拒绝；且宿主策略不应伪装成算法参数。
- 结构：`{"roi": [[x1,y1,x2,y2], ...], "mask": [[x1,y1,x2,y2], ...]}`，坐标严格归一化 `[0.0, 1.0]`，与既有检测框契约一致。
- 空 `roi` 表示不限制（全画面保留）；空 `mask` 表示不排除；两者皆空为默认值，**行为与现状完全一致**（保证上线即安全）。

### R2. 区域配置校验

- Go 侧独立校验（不复用算法 schema）：坐标须在 `[0,1]`、`x1 < x2`、`y1 < y2`、边长不小于最小值、每类数量不超过上限。
- 在创建与更新任务时校验，失败返回稳定的 `apperr` 机器码。

### R3. 推理结果算法归属（前置缺口修复）

- runner 新增 `--algorithm-id` 参数，并在 `MSG_RESULT` 载荷中携带 `algorithm_id`。
- Go 侧解析该字段并用于任务定位；同时修复 F5 记录的 `algorithm_id` 恒为空缺陷。

### R4. 宿主统一过滤

- 在 `consumeAIResults` 于**两路分发之前**执行过滤，使落库与前端广播口径一致。
- ROI 非空时，仅保留底部中心落在任一 ROI 矩形内的目标；MASK 内的目标一律丢弃。
- 过滤须发生在 record 服务的冷却与持久化**之前**。
- 分类结果无检测框，不受区域影响。

### R5. 前端区域编辑器

- 位于任务配置弹窗内，按「相机 + 算法」粒度配置（每个已勾选算法卡片提供入口）。
- 以该相机 **main 流**实时画面为底图，canvas 叠加绘制、拖动、缩放多个矩形；提供冻结画面以便精细绘制。
- 实时流不可用（无流 / 解码失败 / 相机离线）时进入**显式降级态**：明确提示无参考画面，并**禁止保存区域坐标**，绝不静默保存错误坐标。
- 与算法参数表单分离，是独立编辑入口。

### R6. 坐标一致性与生效时效

- 归一化坐标在任意显示尺寸下映射正确；叠加层必须与画面绘制区严格重合（不得把 letterbox 留白算入坐标空间）。
- 区域配置变更后，过滤行为须在可接受时间内生效，且不因逐帧查询数据库而拖慢热路径。

### R7. 质量与测试

- Go：`internal/region` 几何与校验、归属解析、过滤逻辑均为表驱动单测。
- 前端：编辑器交互、降级态、序列化与往返映射用 Vitest 覆盖（真实无头浏览器）。
- 三语文案齐备，无硬编码。

---

## Acceptance Criteria

- [ ] **AC1**：迁移 `000012` 可正常 up/down，`analysis_tasks.region_config_json` 默认 `'{}'`；未配置区域的任务过滤行为与改动前完全一致。
- [ ] **AC2**：创建/更新任务时非法区域配置被拒绝并返回稳定机器码（越界坐标、`x1 >= x2`、超数量上限、退化矩形）。
- [ ] **AC3**：`MSG_RESULT` 载荷含 `algorithm_id`，且识别/抓拍/告警记录落库的 `algorithm_id` 不再为空。
- [ ] **AC4**：配置 ROI 后，底部中心在 ROI 外的检测目标不出现在前端实时叠加、抓拍与告警中；ROI 内的目标正常出现。
- [ ] **AC5**：配置 MASK 后，底部中心在 MASK 内的目标被丢弃；ROI 与 MASK 同时配置时，MASK 优先排除。
- [ ] **AC6**：分类任务（无检测框）不受区域配置影响。
- [ ] **AC7**：任务弹窗内可绘制、拖动、删除多个 ROI/MASK 矩形并保存；重新打开后坐标正确回显。
- [ ] **AC8**：实时流不可用时编辑器显示明确降级提示且无法保存区域坐标（不产生静默错误坐标）。
- [ ] **AC9**：区域配置变更后，无需重启服务即生效。
- [ ] **AC10**：`make check` 全绿（前端 lint/type-check/test + native-test + api-docs + go-check）。

---

## Out of Scope

- **算法包感知与 per-task 配置下发**（`task_open` 扩展 / 新 IPC 消息 / C ABI 变更）。ROI 预裁剪等省算力优化不在本次范围。
- **区域惯性 / 连续帧确认**（仿 Frigate zone inertia）：基础防抖已由现有 track 冷却覆盖（F9）。
- **目标尺寸门限**（仿 Hikvision max/min size）。
- **多边形区域**：本次仅矩形。
- **越线（Line）与方向判定**、**像素级隐私/运动掩码**。
- 区域配置的独立管理页面（本次仅在任务弹窗内配置）。
