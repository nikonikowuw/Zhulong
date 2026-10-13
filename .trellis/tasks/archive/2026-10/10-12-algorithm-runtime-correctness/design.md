# 算法插件与 CPU 预处理修复：技术设计

## 1. 边界与数据流

本任务只改动算法插件 SDK、CPU runtime、Runner、native 构建目标和相关测试。C ABI v1 的公开结构布局保持不变。

```text
zhulong_frame_view
  -> CpuVisionBackend::letterbox
     -> 检查格式/尺寸/stride
     -> packed RGB/BGR 双线性采样并按 target_format 写入 HWC Tensor
  -> plugin adapter
     -> detector: zhulong_bbox[] / DETECTION
     -> classifier: zhulong_classification_item[] / CLASSIFICATION
  -> Runner JSON report (每个字符串字段 JSON 转义)
```

## 2. 插件结果适配

- 在 `algo.hpp` 内部以结果 traits/adapter 共享 model、worker、task 生命周期与 C ABI 防异常胶水。
- detector traits 调用 `PluginClass::detect(Session&, Image)`，编码为 `zhulong_bbox[]` 和 `ZHULONG_RESULT_TYPE_DETECTION`。
- 新增 classifier traits 调用 `PluginClass::classify(Session&, Image)`，编码为 `zhulong_classification_item[]` 和 `ZHULONG_RESULT_TYPE_CLASSIFICATION`。
- 保留 `ZHULONG_REGISTER_DETECTOR_PLUGIN` 的源码接口；新增 `ZHULONG_REGISTER_CLASSIFIER_PLUGIN`。
- 句柄仍是不透明的 C handle；adapter 的具体上下文类型放在模板内部，不再为每个插件定义相同的全局 `zhulong_*_ctx_t` 具体布局。
- 结果分配与释放在同一插件 adapter 内完成，`internal_token` 指向对应的具体结果数组。

## 3. CPU 视觉前处理

- 仅接受 RGB24/BGR24 输入和 RGB24/BGR24 目标。NV12、YUV420P、RGBA32、UNKNOWN 等格式返回 `NOT_SUPPORTED`，不读取图像数据。
- `strides[0] == 0` 按紧密排列兼容；正 stride 必须至少为 `width * 3`；负值或过小值返回 `INVALID_ARGUMENT`。
- `resize` 接口接收源 stride、目标 stride 与源/目标格式，避免把行跨度隐含为 `width * 3`。
- 双线性采样采用像素中心映射，边界坐标 clamp；通道交换在采样写出时完成。
- Letterbox 创建/填充最终 HWC Tensor，再把缩放区域直接写到输出子区域；不再创建整帧 `resized_temp`。这只消除额外中间缓冲分配；最终 `dst` Tensor 仍按现有 API 创建，本任务不新增 per-Session workspace 或改变 `forward` 输入所有权。
- 任何格式、尺寸或 stride 校验失败都在触碰源像素前返回。

## 4. Tensor 固定尺寸

- 默认构造的空 Tensor 继续允许 `element_count() == 0`。
- 非空 shape 遇到任一 `dim <= 0` 时，`element_count()` 明确抛出 `std::invalid_argument`；移除与该行为冲突的 `noexcept`。
- 正维度构造和大小查询保持原语义；本任务不实现动态维度解析。

## 5. Runner JSON 与符号边界

- 添加 Runner 内部 JSON string quoting helper，转义双引号、反斜杠、控制字符以及标准短转义字符。
- Runner 的 plugin path、error、plugin name/version、sample path 全部通过同一个 helper 输出。
- `plugin.h` 定义可移植的插件导出宏，并将 `zhulong_plugin_get_api` 标记为 default/export。
- Mock plugin shared targets 使用 hidden 默认可见性与 hidden inline symbols；通过既有 `dlopen`/`dlsym` 集成测试验证 ABI 入口仍可发现。Linux 下额外检查定义的动态符号集合。

## 6. 注释与兼容性

- NMS 算法保持不变，仅将注释修正为排序后标量贪心抑制、最坏 O(n²)。
- 不改变 `zhulong_plugin_api_v1` / `zhulong_frame_view` 字段、函数表顺序或 ABI 版本。
- 新分类宏是增量能力；既有 detector 类和注册调用继续编译。
- 对不支持像素格式的调用从静默错读变为明确错误，调用端若使用 YUV/RGBA 必须在上游转换。

## 7. 验证与回滚

- 单元测试分别覆盖 stride、颜色顺序、格式拒绝、双线性像素值、Tensor 无效维度、JSON 字符串转义和分类结果类型。
- 集成测试覆盖 detector/classifier 插件加载、结果数据布局和入口符号发现。
- 最终执行 `make native-test`；如静态 FFmpeg 前缀不可用，记录阻塞并运行独立算法 CMake 测试。
- 每个改动集中在列明的 native 算法文件和对应测试；若验证失败，按文件回滚本任务改动，不触碰其他 Trellis 任务或工作区既有修改。
