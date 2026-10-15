# 实现识别结果前端叠加与记录

## Goal

在实时预览画面上叠加人脸检测框与识别标签，并提供识别记录查看界面，消费推理结果出口链路产出的结构化结果。

## 父任务

`10-14-face-recognition-e2e`（人脸识别端到端接入）

## 依赖

- **前置**：`10-14-inference-result-egress`（没有结果出口就没有数据源）。
- **可并行**：与 `10-14-face-algo-package` 并行（前者依赖数据出口，后者依赖 ABI/通道）。
- ⚠️ 若结果传输采用 WebSocket/SSE，其协议形态由 `10-14-inference-result-egress` 定案，本任务需对齐。

## 现状

- `web/src/features/live/` 已有实时预览（WebCodecs 解码 + Canvas 渲染），但**无任何检测框叠加层**。
- 后端 `InferenceResult` 已定义完整结构（`internal/algorithm/result.go`），但无消费方。

## Requirements

- **R1 框叠加**：在实时预览上叠加人脸框，坐标来自后端归一化值（[0,1]），
  按显示尺寸缩放，**不得假设像素坐标**。
- **R2 时序同步**：框与画面的同步按 `pts` 对齐（`result.go` 的 PTS 时钟保真要求），
  避免框与画面错位。
- **R3 识别标签**：`matched=true` 时显示姓名与相似度；`matched=false` 时按设计显示未识别态。
- **R4 多脸支持**：单人体可挂载 0~N 张人脸，单帧可含多个人体——叠加层必须支持多框。
- **R5 渲染性能**：叠加层不得拖慢解码渲染。
  ⚠️ 遵循现有 `requestAnimationFrame` / Canvas 路径；避免每帧触发 React 重渲染
  （参考 `.trellis/spec/frontend/development-guidelines.md`）。
- **R6 识别记录界面**：提供识别记录查看（列表 / 筛选），数据源由 `inference-result-egress` 的落库决定。
- **R7 特性切片结构**：遵循 `src/features/<feature>/{api,hooks,components,data,locales}` 与
  `index.tsx` 入口约定；`src/routes/**` 只做路径声明与守卫。
- **R8 三语 i18n**：`zh-Hans` / `zh-Hant` / `en` 齐全，**严禁 JSX 硬编码文本**。
- **R9 Zod 解析**：不可信输入用 Zod 解析而非类型断言。
- **R10 异步三态**：加载骨架屏 / 错误重试 / 空态完备。
- **R11 实时数据连接**：订阅方式（WebSocket / SSE / 轮询）需定案；断线重连与节流/批量更新必须处理。
- **R12 可关闭**：叠加层需有开关（性能或隐私场景可关闭）。

## Acceptance Criteria

- [ ] 检测框跟随画面运动，无明显延迟或错位。
- [ ] 归一化坐标在**不同显示尺寸**下都正确缩放（窗口缩放后框仍对齐）。
- [ ] 多脸场景（≥2 人）框与标签正确对应，不串位。
- [ ] 已登记人员显示姓名与相似度；未登记显示未识别态。
- [ ] 识别记录界面可查询，分页/筛选可用。
- [ ] 三语文案无缺失。
- [ ] 严格零 `any`；Zod Schema 有测试。
- [ ] 渲染性能实测：开启叠加层后帧率下降在可接受范围内（给出数据）。
- [ ] 叠加层开关生效。
- [ ] `pnpm lint`、`pnpm build`、`pnpm test` 通过。
- [ ] 测试跑在**真实无头浏览器**中（非 JSDOM）。

## 范围外

- 推理结果的产生与落库（属 `10-14-inference-result-egress`）。
- 视频结构化中的人体框（本任务只做人脸框，尽管后端模型预留了 body/face 结构）。
- 识别记录的长期留存策略。
