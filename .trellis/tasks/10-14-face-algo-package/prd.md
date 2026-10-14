# 实现人脸识别算法包

## Goal

交付一个**可安装、可验包、可运行**的人脸识别算法包，完成 detect → align → embed → 1:N 检索的完整链路，基于扩展后的结构化 ABI 与底库热更新通道，产出可被前端消费的结构化识别结果。

## 父任务

`10-14-face-recognition-e2e`（人脸识别端到端接入）

## 依赖

⚠️ **硬前置，缺一不可**：

- `10-14-algo-structure-abi` —— ABI 未冻结前**不得开始插件编码**，否则必然返工。
- `10-14-gallery-hot-sync` —— 没有底库就无法做 1:N。
- `10-14-inference-result-egress` —— 没有出口就无法验证结果正确性。
- `10-14-face-feature-extraction` 的**归属决策** —— 决定特征提取写在本包内还是宿主内。

## Requirements

- **R1 包结构**：遵循 `.trellis/spec/backend/algorithm-guidelines.md` 的 manifest 契约与
  `.trellis/spec/native/algo-plugin-contract.md` 的目录约定（`manifest.json`、`plugin.so`、`sample.jpg`、`models/`）。
- **R2 task_type**：使用 `person_structure`（`internal/algorithm/model.go:37`）。
- **R3 完整流水线**：detect（人脸检测）→ align（关键点对齐）→ embed（特征提取）→ 1:N 检索。
- **R4 结构化输出**：按 `result.go` 的 `PersonItem` / `FaceSubItem` / `FaceCandidate` 语义输出，
  包含 `track_id` / `body_box` / `faces[]`（含 `landmarks`、`matched`、`person_id`、`person_name`、
  `similarity`、`is_fused`、`candidates[]` Top-K）。
  ⚠️ 坐标**必须归一化到 [0,1]**（见 `10-12-algo-package-lifecycle/design.md` 的「强制归一化」铁律）。
- **R5 身份字段**：输出的 `person_id` 必须是底库载荷中的**业务键原值**（`persons.person_id`）。
- **R6 时序融合**：多帧特征融合与轨迹平滑（`is_fused` 标记），阈值可配置。
- **R7 阈值可配置**：识别阈值随包声明或经 config schema 传入，不硬编码在代码里。
- **R8 验包门禁**：通过 `verifier.go` 的自测样本验证，包含坐标与置信度的越界检查
  （`10-12-algo-package-lifecycle/design.md` 要求：数值越界即验包失败）。
- **R9 推理后端**：目标平台按父任务「待决问题 1」的答案（CPU / RKNN）。
- **R10 精度验证**：给出人脸检测与识别的实测精度数据（至少 FAR/FRR 或等价指标），
  并说明测试数据集与阈值。
- **R11 性能数据**：给出单帧推理耗时（含 detect + align + embed + 检索），在目标硬件上实测。
- **R12 文档**：包的 README 说明模型来源、许可证、输入输出规格、阈值含义。

## Acceptance Criteria

- [ ] 包能通过验包门禁被成功安装（`InstallAlgorithm` 全流程）。
- [ ] 自测样本产出正确的结构化结果，坐标归一化、字段完整。
- [ ] 冷启动后能从底库检索到已登记人脸；运行期新增/删除人脸**不重启**即可生效。
- [ ] 父任务 **AC1**（同人不同照 cosine 高于阈值）在本包的 embed 阶段有直接验证。
- [ ] 父任务 **AC3**：对准已登记人员时 `matched=true` 且 `person_id` 为业务键原值。
- [ ] 父任务 **AC5**：未登记人员在同一阈值下 `matched=false`，**无误报**。
- [ ] 精度与性能数据在目标硬件上实测并记录（不接受 CPU 模拟数据冒充目标板数据）。
- [ ] 原生侧 ASan/UBSan 干净。
- [ ] 模型文件许可证清晰，不引入许可冲突。

## 范围外

- 宿主侧的特征提取（若归属插件内，则属本包；否则属 `10-14-face-feature-extraction`）。
- 前端展示。
- 人体（非人脸）检测与跟踪。
