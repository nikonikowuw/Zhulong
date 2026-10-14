# 统一 BBox 边界框为归一化 [x1, y1, x2, y2] 坐标

## Goal

重构 `zhulong_bbox` 与 `Detection` 结构体为 `float box[4]`，消除原生层通用目标检测与结构化人脸之间的坐标系双轨制，全栈（C ABI ⇄ IPC ⇄ Go ⇄ 前端）统一为 `[x1, y1, x2, y2]` 严格归一化坐标 `[0.0, 1.0]`。

## Requirements

1. **C ABI 结构体统一（`native/sdk/include/zhulong/plugin.h`）**：
   - 将 `zhulong_bbox` 的 `float x, y, width, height` 重构为 `float box[4]`；
   - 明确注释其语义为 `[x1, y1, x2, y2]` 且必须严格归一化到 `[0.0, 1.0]`；
   - 维持结构体总大小 88 字节不变，增加 `_Static_assert` 大小与 8 字节对齐校验。

2. **C++ SDK 与后处理适配（`native/sdk/include/zhulong/algo.hpp`）**：
   - 将 `struct Detection` 的字段重构为 `float box[4]{0.0f, 0.0f, 0.0f, 0.0f}`；
   - 调整 `DetectionResultAdapter::serialize`，直接序列化 4 个 float；
   - 调整 `zhulong::nms` 适配器。

3. **视觉运行时 IoU 计算重构（`native/src/algorithm/runtime/cpu_vision_backend.cpp`）**：
   - `calc_iou` 重构为基于两点坐标的 `[x1, y1, x2, y2]` 交并比算法，使用 `std::max(0.0f, ...)` 防御越界或倒置导致的负面积。

4. **测试用例与 Mock 插件适配**：
   - `native/tests/plugins/mock_detector/mock_detector.cpp`：使用归一化坐标构造测试目标；
   - `native/tests/algo_runtime_test.cpp`：更新 NMS 单元测试用例为归一化两点坐标；
   - `native/tests/plugin_c_abi_test.c`：更新对 `zhulong_bbox` 的尺寸、对齐与字段断言。

5. **契约规范同步**：
   - 更新 `.trellis/spec/native/algo-plugin-contract.md`。

## Acceptance Criteria

- [x] `zhulong_bbox` 与 `Detection` 字段统一为 `box[4]`，语义固定为 `[x1, y1, x2, y2]` 归一化。
- [x] 64 位平台下 `_Static_assert` 尺寸（88 字节）与 8 字节对齐全部通过。
- [x] `cpu_vision_backend.cpp` 的 `calc_iou` 与 `nms` 逻辑正确，无负面积风险。
- [x] 原生单元测试与 ASan 检查通过（`python3 native/scripts/build.py test` 及 `--sanitizer address`）。
- [x] Go 侧集成检查通过（`TMPDIR=/home/niko/tmp make go-check`）。
- [x] 规范文档 `.trellis/spec/native/algo-plugin-contract.md` 同步更新。

## Out of Scope

- 不修改 Go 侧 `DetectionItem`（已是 `[4]float64` 且为 `[x1, y1, x2, y2]`）。
- 不涉及 IPC 序列化协议（属于后续 `10-14-inference-result-egress` 任务）。
