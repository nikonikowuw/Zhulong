# 扩展结构化结果 C ABI

## Goal

为 `person_structure`（人脸/人体结构化）结果在插件 C ABI 上提供**正式的落脚点**，消除「把 `person_structure` 复用为 `result_type == 1`」这一临时约定，使插件能够输出 `track_id` / `body_box` / 嵌套 `faces[]` / `candidates[]`。

## 父任务

`10-14-face-recognition-e2e`（人脸识别端到端接入）

## 依赖

- **前置**：父任务「待决问题 1/3」已回答（平台、特征提取归属）。
  - ⚠️ 若特征提取归属选择**插件内**，结构化结果需携带特征字段——本任务的范围会扩大，需重新评审。
- **后继**：`10-14-face-algo-package` 硬依赖本任务。**ABI 未冻结前不得开始插件编码。**

### ✅ 已解冻（G1 已答）

父任务决策：**开发机 ONNX 为主环境**、**特征提取在插件内**。因此：

- ⚠️ **本任务范围确认扩大**：结构化结果需能承载插件内提取的特征（若走 1:N 本地索引，
  则结果只需回传 `person_id` / `similarity` / `candidates`，不必回传向量）。
- ⚠️ **必须承载 5 点 landmarks**（实测：`det_500m` 的 kps 输出 10 维 = 5×2），
  因为前端需要展示、且对齐发生在本包内。
- 详细模型契约见父任务 `design.md` §2.0 与 `research/face-model-baseline.md`。

## 问题证据

1. **承载结构缺失**：`native/sdk/include/zhulong/plugin.h` 的 `zhulong_bbox` 为扁平单层结构
   （`{x, y, width, height, confidence, class_id, label[64]}`），**无法表达**嵌套的
   `PersonItem { track_id, body_box, body_score, faces[] }` 与
   `FaceSubItem { box, quality, landmarks, matched, person_id, person_name, similarity, is_fused, candidates[] }`
   （见 `internal/algorithm/result.go:20-45`）。
2. **Adapter 缺失**：`native/sdk/include/zhulong/algo.hpp` 仅提供
   `DetectionResultAdapter` 与 `ClassificationResultAdapter`，注册宏也只有
   `ZHULONG_REGISTER_DETECTOR_PLUGIN` / `ZHULONG_REGISTER_CLASSIFIER_PLUGIN`。
3. **临时约定**：`internal/algorithm/verifier.go:249` 将 `TaskTypePersonStructure` 与
   `TaskTypeDetection` 一同映射为 `result_type == 1`。这是占位，不是设计。

## Requirements

- **R1 方案定案**：在 `design.md` 中定案 ABI 扩展方案，并说明理由与兼容性影响。
  已定案采纳**方案 D（数据层多态扩展，保持 v1 表不变）**，理由是容器 `zhulong_inference_result`
  天生多态且 `struct_size` 协商在 runner 侧未生效，无需新建 v2 表或追加函数指针。
- **R2 结构化结果类型**：在 `plugin.h` 中定义可表达 1:N 人脸挂载的结构化结果 item 与结果类型枚举值。
  ⚠️ **必须承载 5 点 landmarks**（实测 `det_500m` 的 kps 输出 10 维 = 5×2），
  因为前端需要展示且对齐发生在插件内。
  ⚠️ 人脸框**必须落在人体框拓扑几何内部**（`10-12-algo-package-lifecycle/design.md` 的
  验包门禁要求）——若本期不做人体检测，则 `body_box` 的语义需在 `design.md` 中明确
  （例如：人脸框自身，或标记为可选）。
  ⚠️ 坐标**必须归一化到 [0,1]**（同上的「强制归一化」铁律）。
- **R3 SDK 门面**：在 `algo.hpp` 中提供对应的结果适配器与注册宏，第三方可像写 detector 一样写结构化插件。
- **R4 内存与释放**：结构化结果的分配与释放遵循现有 `internal_token` + `result_release` 模式；
  嵌套数组的生命周期归属必须在头文件中写清。
- **R5 verifier 映射更新**：`verifier.go` 的 `task_type` ↔ `result_type` 映射更新为正式值；
  若存在兼容期，需明确旧包（`result_type == 1` 的 person_structure）的处理策略。
- **R6 契约文档**：`.trellis/spec/native/algo-plugin-contract.md` 与 `plugin.h` 注释同步更新。
- **R7 跨语言护栏**：新增的枚举/常量纳入 `internal/algorithm/ipc_contract_test.go` 式的跨语言一致性测试
  （若涉及 IPC 常量）。

## Acceptance Criteria

- [ ] ABI 方案在 `design.md` 中定案并给出兼容性分析（旧插件在新 runner / 新插件在旧 runner 的行为）。
- [ ] `plugin.h` 中的新结构有 `static_assert` 校验尺寸与对齐（沿用现有风格）。
- [ ] `algo.hpp` 提供结构化适配器 + 注册宏，并有至少一个可编译的示例（可复用 mock 插件模式）。
- [ ] 原生侧 CTest 覆盖：结构化结果能通过 C ABI 往返，字段完整、无内存泄漏（ASan 干净）。
- [ ] `verifier.go` 不再把 `person_structure` 与 `detection` 混为一谈；映射有测试。
- [ ] 旧 v1 路径**未被破坏**：既有 `mock_detector` / `mock_classifier` 测试仍通过。
- [ ] `make native-test` 与 `make go-check` 通过。

## 范围外

- 人脸算法包的实现（属 `10-14-face-algo-package`）。
- 结果上报通道（属 `10-14-inference-result-egress`）。
- 人体跟踪算法本身（`track_id` 由插件产生，宿主不实现跟踪）。
