# 算法运行时问题核查记录

本记录来自对当前 native 代码与已归档算法 SDK 设计的只读核对，作为实施与检查依据。

- 分类器：`native/tests/plugins/mock_classifier/mock_classifier.cpp:18-49` 实现 `detect()` 并注册 detector；`native/sdk/include/zhulong/algo.hpp:224-249` 固定调用 detect 并序列化为 `DETECTION`/`zhulong_bbox[]`。
- stride：`native/src/algorithm/runtime/cpu_vision_backend.cpp:65` 调用 resize 时未传 `src.strides[0]`；`resize()` 第 14-24 行以 `src_w * channels` 连续寻址。
- 格式/颜色：`native/src/algorithm/runtime/cpu_vision_backend.cpp:34-78` 不检查输入 `format` 或 `target_format`，固定 3 通道；`native/sdk/include/zhulong/plugin.h:26-36` 枚举包含 BGR24、RGB24、NV12、YUV420P、RGBA32。
- 插值：`native/src/algorithm/runtime/cpu_vision_backend.cpp:14-28` 用整数坐标最近邻采样；`native/src/algorithm/runtime/vision_backend.hpp:28-30` 和 `cpu_vision_backend.cpp:63` 声称双线性。归档设计 `.trellis/tasks/archive/2026-10/10-12-algo-plugin-sdk-runner/design.md:118` 明确 CPU 预处理应提供双线性和颜色转换。
- 临时缓冲：`native/src/algorithm/runtime/cpu_vision_backend.cpp:57-75` 分配输出 Tensor 和 `resized_temp`，后者再逐行复制到输出。
- NMS：`native/src/algorithm/runtime/vision_backend.hpp:33` 声称高性能向量化；`cpu_vision_backend.cpp:99-120` 使用排序和标量嵌套循环。
- Tensor：`native/src/algorithm/runtime/tensor.hpp:43-53` 对 `dim <= 0` 不乘入元素数，动态维度会造成错误大小；默认空 Tensor 当前返回 0，应保留。
- JSON：`native/src/algorithm/runner/runner.cpp:68-80,148-150` 直接拼接路径、错误、插件元数据与样本路径，没有字符串转义。
- 可见性：`native/CMakeLists.txt:181-189` 未设 Mock plugin hidden visibility；`runner.cpp:29-35` 使用 `dlsym` 获取 ABI 入口。句柄结构在注册宏中重复定义，但 C opaque handle 具体类型本身不等于 ELF 导出符号。

静态核查已由实现回归测试验证：`make native-test` 与 `python3 native/scripts/build.py --sanitizer address test` 均通过（14/14）。Linux MockDetector/MockClassifier 的定义动态符号均仅有 `zhulong_plugin_get_api`。ASan 首轮检查发现 hidden visibility 单独使用仍会泄露弱 RTTI 符号，现由 Linux export map 收敛导出集合并有自动测试覆盖。
