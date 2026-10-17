# 实施计划：RKNN 推理后端状态事务化与输出直写

## 执行步骤

### 阶段 1：类型定义与 Rule of 5 规范 (`rknn_types.hpp`)
- [x] 1.1 为 `RknnModelAsset` 显式禁用拷贝与移动操作（Rule of 5）。
- [x] 1.2 为 `CachedDmaMem` 增加幂等复位函数 `reset(rknn_context ctx)`。
- [x] 1.3 为 `RknnSessionContext` 显式禁用拷贝操作（Rule of 5），析构函数复用 `reset()`。
- 验证项：`python3 native/scripts/build.py compile ZhulongAlgoRuntime` 编译通过，无类型与析构告警。

### 阶段 2：输入门禁与缓存事务化改造 (`rknn_inference_backend.cpp`)
- [x] 2.1 强化 `file_basename`，消除末尾斜杠导致的空字符串问题。
- [x] 2.2 在 `forward` 头部增加全量 DMA 输入计数与混合输入安全拦截（部分 DMA 输入直接返回 `ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT`）。
- [x] 2.3 增加 DMA 模式到常规 Host 内存模式切换时的缓存清理。
- [x] 2.4 实现 DMA-BUF 缓存的两阶段事务提交（创建 -> 绑定 -> 失败回滚销毁 -> 成功提交凭据）。
- [x] 2.5 补齐 `io_attr.pass_through` 与 `w_stride` 约束核查。
- 验证项：编译通过，静态检查逻辑无遗漏。

### 阶段 3：输出零拷贝直写与安全校验 (`rknn_inference_backend.cpp`)
- [x] 3.1 改造输出阶段逻辑：在 `rknn_outputs_get` 之前直接按 `output_attrs` 构造 `outputs` 里的 `Tensor` 实例。
- [x] 3.2 设置 `rknn_output.is_prealloc = 1`，将 `buf` 与 `size` 指向预分配的 `Tensor`。
- [x] 3.3 保持 `OutputsReleaseGuard` 进行句柄清理。
- [x] 3.4 增加 `rknn_outputs[i].size == tensor.byte_size()` 的一致性断言，杜绝 Stride 导致的截断错位。
- 验证项：`python3 native/scripts/build.py compile ZhulongAlgoRuntime` 顺利完成。

### 阶段 4：测试套件补齐与全面回归 (`tests/rknn_backend_test.cpp`)
- [x] 4.1 在 `tests/rknn_backend_test.cpp` 的 `case_faults` 中补充：
  - 路径末尾带斜杠的模型路径测试；
  - 混合输入（1 个 DMA-BUF Tensor + 1 个普通 Tensor）必须返回 `ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT`；
- [x] 4.2 验证 `case1` ~ `case6` 各阶段在 `is_prealloc = 1` 下输出逐元素的一致性与正确性。
- [x] 4.3 运行工程检查 `make go-check` / 测试构建，确保整体无回归。
- 验证项：本地测试编译通过，真机运行测试输出完整 PASS 证据。
