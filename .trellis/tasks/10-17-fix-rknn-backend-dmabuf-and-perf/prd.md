# 修复 RKNN 推理后端的 DMA-BUF 状态管理与输出直写优化

## Goal

修复 `RknnInferenceBackend` 中的 DMA-BUF 缓存脏状态、多输入混合类型约束、输出热路径高频分配与拷贝瓶颈，并补齐 Rule of 5 与边缘防御，确保 NPU 硬件推理的内存安全性与高性能。

## Requirements

### 1. DMA-BUF 缓存状态事务性提交与回滚
- 在调用 `rknn_set_io_mem` 成功之前，不得提前将 `inputs[i]` 的 `fd`、`va`、`size` 写入 `cached` 状态。
- 若 `rknn_set_io_mem` 失败，必须立即销毁已创建的 `rknn_tensor_mem`，将缓存项重置为无效，并返回对应错误码。
- 严禁产生「底层绑定失败但缓存命中导致后续帧跳过绑定」的幽灵命中。

### 2. 混合输入（DMA-BUF 与常规 Host 内存）安全门禁
- 检查全部输入是否一致：严禁同一模型的部分输入使用 DMA-BUF（`rknn_set_io_mem`）而另一部分输入使用常规 Host 内存（`rknn_inputs_set`）。
- 若存在部分输入提供 `dma_buf_fd >= 0` 而另一部分 `< 0`，返回 `ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT`（或者当且仅当所有输入均为有效 DMA-BUF 时启用 IO MEM 零拷贝路径）。
- 当会话从 DMA-BUF 模式切换回常规 Host 模式时，必须先清理已缓存的 `CachedDmaMem`，避免底层 Context 状态冲突。

### 3. DMA-BUF 输入属性契约补齐
- DMA-BUF 输入绑定时，显式配置 `io_attr.pass_through`（类型与模型输入一致时设为 1，避免 NPU 不必要的格式转换）。
- 校验输入的字节跨度是否满足硬件要求的 `attr.w_stride` 与 `attr.size_with_stride`。

### 4. 输出阶段零拷贝直写优化 (`is_prealloc = 1`)
- 废弃 `is_prealloc = 0` 导致的每帧 `librknnrt malloc` ➔ `Tensor malloc` ➔ `memcpy` ➔ `free` 高频抖动链路。
- 改为在 `rknn_outputs_get` 调用前，预先在 `outputs` 中构造目标 `Tensor` 实例。
- 将 `rknn_outputs[i].is_prealloc = 1`，并将 `rknn_outputs[i].buf` 直接指向 `outputs[i].raw_data()`，`size = outputs[i].byte_size()`。
- `rknn_outputs_get` 直接将 float32 结果写入 `Tensor`，彻底消除中间临时堆分配与 `memcpy` 拷贝开销。
- 保留 `OutputsReleaseGuard`（在 `is_prealloc = 1` 时调用 `rknn_outputs_release` 仅释放内部句柄，不会释放外部传入的 buf）。

### 5. 输出跨度安全校验
- 校验 `rknn_outputs[i].size == tensor.byte_size()`。若输出存在未处理的额外 Stride Padding，返回 `ZHULONG_PLUGIN_ERR_INTERNAL`，严禁通过 `std::min` 截断拷贝引入张量数据错位。

### 6. 现代 C++ 规范与边界鲁棒性
- `RknnSessionContext` 与 `RknnModelAsset` 遵循 Rule of 5，显式声明 `= delete` 拷贝构造函数与拷贝赋值运算符。
- `file_basename` 防御末尾带有 `/` 或 `\` 的模型路径输入。

## Acceptance Criteria

- [x] **AC-1**: 当 `rknn_set_io_mem` 模拟失败或返回错误时，`cached_input_mems` 不残留脏元数据，下一帧再次调用能正确重新触发创建与绑定。
- [x] **AC-2**: 当传入混合输入（如 Input 0 为 DMA-BUF，Input 1 为普通 Tensor）时，被明确拦截并返回 `ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT`。
- [x] **AC-3**: 当所有输入均为合法 DMA-BUF 时，正常进入零拷贝绑定路径，且正确配置 `io_attr`。
- [x] **AC-4**: 输出阶段采用 `is_prealloc = 1`，推理结果逐元素与原本 `is_prealloc = 0` 完全一致，且热路径单帧堆分配次数降为 0（复用输出或单次分配）。
- [x] **AC-5**: `RknnSessionContext` 与 `RknnModelAsset` 禁止拷贝，编译期通过类型检查。
- [x] **AC-6**: `tests/rknn_backend_test.cpp` 覆盖上述故障注入、混合输入拦截、预分配输出与路径边界，且现有用例无回归（`case1` ~ `case6` 全部 PASS）。
