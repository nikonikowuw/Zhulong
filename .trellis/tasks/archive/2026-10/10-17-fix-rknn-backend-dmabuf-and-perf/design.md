# 技术设计：RKNN 推理后端状态事务化与输出直写

## 1. 背景与现状

当前 `RknnInferenceBackend` 实现了基础的模型加载（含 R10 缺陷规避）、Worker 会话派生与张量推演，但在深入异构硬件边界时存在如下设计缺陷：
1. **DMA-BUF 缓存非事务性更新**：在 `rknn_set_io_mem` 成功前就提交了缓存凭据（fd/va/size），一旦底层设置失败，下一次调用将产生虚假命中。
2. **混合模式未受控**：同一模型的输入若既包含 DMA-BUF 又包含常规 Host 指针，底层 `librknnrt` 会因为上下文模式冲突报错；若在多帧间模式切换，残留缓存句柄会导致冲突。
3. **输出阶段内存开销高昂**：`want_float = 1, is_prealloc = 0` 模式导致每帧必须经历运行时 `malloc -> Tensor malloc -> memcpy -> free` 的沉重开销，严重影响嵌入式流水线吞吐。

## 2. 核心架构与设计变更

### 2.1 DMA-BUF 缓存事务提交协议

在 `RknnSessionContext` 中维护 `cached_input_mems`：
```cpp
struct CachedDmaMem {
    int fd{-1};
    void* va{nullptr};
    size_t size{0};
    rknn_tensor_mem* mem{nullptr};

    void reset(rknn_context ctx) {
        if (mem && ctx) {
            rknn_destroy_mem(ctx, mem);
        }
        mem = nullptr;
        fd = -1;
        va = nullptr;
        size = 0;
    }
};
```

更新流程变更为两阶段提交：
1. **准备阶段**：检查缓存是否匹配 `(cached.mem != nullptr && cached.fd == in.fd && cached.va == in.va && cached.size == in.size)`。
   - 若不匹配，先释放旧 `cached.reset(state->ctx)`。
   - 调用 `rknn_create_mem_from_fd` 生成新句柄 `new_mem`。若失败，直接返回 `ERR_INTERNAL`。
2. **校验与绑定阶段**：
   - 构造 `io_attr`，配置 `w_stride` 校验、`h_stride` 设定以及根据数据类型是否一致配置 `pass_through`。
   - 调用 `rknn_set_io_mem(state->ctx, new_mem, &io_attr)`。
   - **若失败**：立即调用 `rknn_destroy_mem(state->ctx, new_mem)`，不修改 `cached` 任何字段，并返回映射后的错误码。
   - **若成功**：正式将 `(new_mem, in.fd, in.va, in.size)` 提交到 `cached`。

### 2.2 混合输入与模式切换门禁

在 `forward` 输入校验阶段进行全量门禁检查：
```cpp
uint32_t dma_count = 0;
for (const auto& in_tensor : inputs) {
    if (in_tensor.dma_buf_fd() >= 0) {
        ++dma_count;
    }
}

// 严禁混合模式：必须全部为 DMA-BUF 或全部为常规内存
if (dma_count > 0 && dma_count != asset.io_num.n_input) {
    return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
}
```
当 `dma_count == 0`（全常规模式）时：
- 若之前该 Session 缓存过 DMA 句柄，执行清空并释放：
  ```cpp
  for (auto& item : state->cached_input_mems) {
      item.reset(state->ctx);
  }
  state->cached_input_mems.clear();
  ```
- 然后安全执行全量 `rknn_inputs_set`。

### 2.3 输出阶段直写 (`is_prealloc = 1`)

#### 内存所有权契约
- 由 `Tensor` 自身管理连续内存存储。
- 在调用 `rknn_outputs_get` 之前，一次性分配所有输出 `Tensor` 并加入 `outputs`：
  ```cpp
  outputs.reserve(asset.io_num.n_output);
  for (uint32_t i = 0; i < asset.io_num.n_output; ++i) {
      const rknn_tensor_attr& attr = asset.output_attrs[i];
      // 提取 shape...
      outputs.emplace_back(std::move(shape), DataType::FLOAT32);
  }
  ```
- 配置 `rknn_output`：
  ```cpp
  std::vector<rknn_output> rknn_outputs(asset.io_num.n_output);
  for (uint32_t i = 0; i < asset.io_num.n_output; ++i) {
      rknn_output& out = rknn_outputs[i];
      out.index = i;
      out.want_float = 1;
      out.is_prealloc = 1;
      out.buf = outputs[i].raw_data();
      out.size = static_cast<uint32_t>(outputs[i].byte_size());
  }
  ```
- 执行 `rknn_outputs_get`。如果成功，`librknnrt` 会直接将 FP32 反量化结果写入 `outputs[i].raw_data()`。
- `OutputsReleaseGuard` 依旧生效，但因为 `is_prealloc = 1`，`rknn_outputs_release` 只释放 Runtime 内部句柄，不会释放外部 Tensor 的 `raw_data()` 内存。
- 校验实际输出大小：如果 `rknn_outputs[i].size != outputs[i].byte_size()`，清空输出并返回 `ERR_INTERNAL`。

### 2.4 C++ 规范与鲁棒性

1. **Rule of 5**：
   在 `RknnModelAsset` 与 `RknnSessionContext` 中显式添加：
   ```cpp
   RknnModelAsset(const RknnModelAsset&) = delete;
   RknnModelAsset& operator=(const RknnModelAsset&) = delete;
   RknnModelAsset(RknnModelAsset&&) = delete;
   RknnModelAsset& operator=(RknnModelAsset&&) = delete;
   ```
   `RknnSessionContext` 同样禁用拷贝。
2. **`file_basename` 防御**：
   处理路径末尾带连续斜杠的情况，剥除尾部斜杠后再提取 basename。

## 3. 风险与权衡 (Tradeoffs)

| 方案 / 变更 | 收益 | 潜在风险 | 应对策略 |
| :--- | :--- | :--- | :--- |
| `is_prealloc = 1` 直写 | 彻底消除中间动态堆分配与 memcpy | 依赖 `librknnrt` 对 prealloc 缓冲大小的严格校验 | 严格按 `shape` 的元素数乘以 `sizeof(float)` 分配，并在 get 后比对 size |
| 全量 DMA-BUF 门禁 | 杜绝混合输入导致底层驱动未定义状态 | 不支持单模型异构输入（部分 DMA 部分常规） | Rockchip 硬件驱动本身不支持同 Context 混用，严格报错是 Fail-closed 防御 |
| 事务性回滚 | 避免下帧产生幽灵缓存命中 | 异常时多一次销毁调用 | `reset()` 封装幂等销毁，确保无双重释放 |
