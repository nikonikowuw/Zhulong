# 扩展结构化结果 C ABI 执行计划

## 1. 目标与范围

在不改变既有 `zhulong_plugin_api_v1` 函数表的前提下，扩展 `plugin.h` 与 `algo.hpp`，支持 `ZHULONG_RESULT_TYPE_PERSON_STRUCTURE = 3` 及嵌套 POD 结构体；提供连续 Arena 内存打包与释放机制；同步修正 Go 侧 `verifier.go` 门禁与跨语言契约测试。

---

## 2. 实施步骤

### 步骤 1：C ABI 定义扩展（`native/sdk/include/zhulong/plugin.h`）
- [ ] 增加 `ZHULONG_RESULT_TYPE_PERSON_STRUCTURE = 3` 枚举。
- [ ] 增加长度宏：`ZHULONG_PERSON_ID_MAX_LEN 64`, `ZHULONG_PERSON_NAME_MAX_LEN 64`, `ZHULONG_FACE_LANDMARK_COUNT 5`。
- [ ] 增加 POD 结构体：
  - `zhulong_face_candidate`
  - `zhulong_face_item`
  - `zhulong_person_item`
- [ ] 编写 64 位平台下的 `static_assert`（大小与字段对齐验证）。
- **验证**：编译原生 SDK 校验头文件语法：
  ```bash
  python3 native/scripts/build.py test
  ```

### 步骤 2：SDK 适配器与宏扩展（`native/sdk/include/zhulong/algo.hpp`）
- [ ] 扩展 `PluginApi`，使其能够支持自定义释放（或统一使用 Arena 单点释放）。
- [ ] 实现 `PersonStructureResultAdapter`：
  - 提供单块连续 Arena 内存打包辅助工具。
  - 实现基于单一 `internal_token` 的 `result_release`。
- [ ] 提供插件注册宏 `ZHULONG_REGISTER_PERSON_STRUCTURE_PLUGIN(PluginClass)`。
- **验证**：头文件无语法或模板推导错误。

### 步骤 3：C ABI 单元测试与 Mock 插件验证（`native/tests/`）
- [ ] 在 `native/tests/plugin_c_abi_test.c` 中增加对结构化结果的纯 C 读取与断言。
- [ ] 新建或扩展 Mock 结构化插件，并在 CTest 中验证：
  - 端到端 `process` 调用返回 `ZHULONG_RESULT_TYPE_PERSON_STRUCTURE`；
  - 能够正确读取 1:N 人脸及 TopK 候选人数据；
  - 坐标及 Landmarks 范围符合 [0.0, 1.0]；
  - `result_release` 释放后 ASan 检查无泄漏（Clean）。
- **验证**：
  ```bash
  python3 native/scripts/build.py test
  ```

### 步骤 4：Go Verifier 门禁更新（`internal/algorithm/verifier.go`）
- [ ] 在 `internal/algorithm/verifier.go:249` 中，将 `TaskTypePersonStructure` 期望的 `ResultType` 改为 `3`。
- [ ] 调整/增加对应的单元测试，确保 `person_structure` 要求 `ResultType == 3`，若返回 `1` 则明确报错。
- **验证**：
  ```bash
  go test -race ./internal/algorithm/... -run TestVerifier
  ```

### 步骤 5：跨语言契约测试护栏（`internal/algorithm/`）
- [ ] 在 `internal/algorithm/ipc_contract_test.go` 中解析 `plugin.h`，增加对 `ZHULONG_RESULT_TYPE_PERSON_STRUCTURE` 常量值的强校验断言。
- **验证**：
  ```bash
  go test -race ./internal/algorithm/... -run TestIpcContract
  ```

### 步骤 6：契约规范文档同步
- [ ] 更新 `.trellis/spec/native/algo-plugin-contract.md` 中的数据结构与内存管理章节。
- [ ] 全套质量回归：
  ```bash
  make native-test
  make go-check
  ```

---

## 3. 回滚方案

- 本任务所有改动集中于新增结果类型和新增适配器，既有 `ZHULONG_RESULT_TYPE_DETECTION` (1) 和 `ZHULONG_RESULT_TYPE_CLASSIFICATION` (2) 的数据结构与运行路径完全不受影响。
- 若需回滚，只需 `git checkout` 还原 `plugin.h`、`algo.hpp` 和 `verifier.go`，既有检测/分类功能不受任何破坏。
