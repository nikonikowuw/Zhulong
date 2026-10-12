# 修复算法插件与视觉预处理问题

## Goal

修复算法插件 SDK、CPU 视觉预处理、Tensor 与 Runner 中审查确认的问题，保持 v1 插件 ABI 兼容，并以回归测试防止错误结果类型、错位图像和无效 JSON 再次出现。

## Background

- 插件 SDK 只有 detector 注册宏；MockClassifier 也以检测框序列化结果（`native/tests/plugins/mock_classifier/mock_classifier.cpp:18-49`，`native/sdk/include/zhulong/algo.hpp:224-249`）。
- CPU letterbox 忽略帧 stride/像素格式并固定按三通道读（`native/src/algorithm/runtime/cpu_vision_backend.cpp:31-78`）；resize 实际最近邻（同文件 `:14-28`），而既有插件设计要求双线性缩放和颜色转换（`.trellis/tasks/archive/2026-10/10-12-algo-plugin-sdk-runner/design.md:118`）。
- NV12/YUV420P/RGBA32 在 ABI 枚举中存在但没有相应处理（`native/sdk/include/zhulong/plugin.h:26-36`）；letterbox 每次创建 `resized_temp`（`native/src/algorithm/runtime/cpu_vision_backend.cpp:57-75`）。
- Tensor 对 `dim <= 0` 静默跳过（`native/src/algorithm/runtime/tensor.hpp:43-53`）；Runner 多处直接拼接 JSON 字符串（`native/src/algorithm/runner/runner.cpp:68-80,148-150`）。
- Mock plugin targets 未设 hidden visibility（`native/CMakeLists.txt:181-189`）；入口由 `dlsym` 查询（`native/src/algorithm/runner/runner.cpp:29-35`），需在隐藏默认符号时显式保留入口可见性。

## Requirements

- **R1 插件结果类型**：增加分类插件注册/序列化路径；分类器调用 `classify()` 并产出 `ZHULONG_RESULT_TYPE_CLASSIFICATION` 与 `zhulong_classification_item[]`。保留 detector 宏及其现有行为。
- **R2 帧 stride**：packed RGB/BGR 输入按 `strides[0]` 逐行读取；零 stride 为兼容旧调用按紧密排列处理；非法 stride 返回明确错误，不越行读取。
- **R3 像素格式与通道顺序**：实现 RGB24/BGR24 输入到 RGB24/BGR24 目标格式的转换。NV12、YUV420P、RGBA32、UNKNOWN 及其他不支持格式显式返回 `ZHULONG_PLUGIN_ERR_NOT_SUPPORTED`，不得按三通道读取。
- **R4 缩放契约**：CPU resize 实现双线性插值，letterbox 以目标格式写入 HWC 三通道 Tensor；同步修正文档注释。
- **R5 中间缓冲**：去除 `resized_temp` 全帧临时分配，直接将缩放结果写入 letterbox 输出区域；本项仅移除额外中间缓冲，不承诺整个推理路径零分配，也不改变插件 ABI。
- **R6 Tensor 维度**：固定尺寸 Tensor 遇到零或负维度显式失败，不再静默忽略维度并分配错误大小。
- **R7 Runner JSON**：所有输出 JSON 字符串字段统一转义，包括插件路径、错误、插件元数据和样本路径。
- **R8 插件符号可见性**：示例插件采用 hidden 默认可见性，只导出 `zhulong_plugin_get_api`；为该入口提供跨平台显式导出标记。
- **R9 NMS 文档**：注释准确说明当前为排序加标量贪心 O(n²) 抑制，不宣称向量化；本任务不要求改 NMS 算法。

## Constraints

- 保持 `zhulong_plugin_api_v1` 与 `zhulong_frame_view` 的 C ABI 布局不变；分类支持通过新增 SDK 宏/内部实现提供。
- 不实现 YUV/RGBA 转换；这些格式必须安全、显式拒绝。
- 保持现有 HWC Tensor 布局和 detector 插件源码兼容。
- 不修改当前未提交的其他 Trellis 任务文件。

## Acceptance Criteria

- [x] **AC1 分类结果**：MockClassifier 走分类接口；测试断言结果类型为 CLASSIFICATION，并将 data 解读为 `zhulong_classification_item[]`；MockDetector 仍返回 DETECTION/`zhulong_bbox[]`。
- [x] **AC2 stride 与颜色**：对带行尾填充的 RGB/BGR 帧得到正确像素；RGB↔BGR 转换结果正确；负 stride/不足行宽被拒绝，零 stride 的紧密排列兼容路径通过。
- [x] **AC3 格式防护**：NV12、YUV420P、RGBA32 和 UNKNOWN 均返回 NOT_SUPPORTED，不触发非法内存读取。
- [x] **AC4 缩放**：双线性插值由已知图样测试验证；letterbox 的尺寸、padding、HWC 布局与目标格式正确。
- [x] **AC5 缓冲**：letterbox 不再创建 `resized_temp` 全帧缓冲；已有预处理行为回归测试通过。
- [x] **AC6 Tensor**：零维和动态/负维度构造或计数明确失败；正维度和 byte size 结果正确。
- [x] **AC7 JSON**：含引号、反斜杠、换行和控制字符的输入字段输出可被标准 JSON parser 解析，且字符串值往返一致。
- [x] **AC8 可见性**：插件仍可通过 `dlopen`/`dlsym` 加载唯一 ABI 入口；动态符号检查确认插件不导出非入口实现符号。
- [x] **AC9 工程验证**：`make native-test` 通过；ASan native suite 也通过（14/14）。

## Out of Scope

- 支持 YUV/RGBA 输入转换、动态形状张量分配、真实 NPU 后端或 NMS SIMD 优化。
- 修改 `zhulong_plugin_api_v1` / `zhulong_frame_view` ABI 版本或进行与本修复无关的 Runner 重构。
