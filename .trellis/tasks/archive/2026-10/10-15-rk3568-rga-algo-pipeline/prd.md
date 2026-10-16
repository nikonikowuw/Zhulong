# 算法前处理 RGA 硬件加速与 FrameBroker 零拷贝穿透 (10-15-rk3568-rga-algo-pipeline)

## Goal

将 `packages/face_recognition` 生产人脸/人体分析算法包全面接入 `IVisionBackend` (RGA 硬件加速) 与 DMA-BUF 零拷贝流转，消除手写 CPU 像素循环与通道重排；并在 `FrameBroker` 建立针对上游硬件解码帧的零拷贝直通路径，打通算法侧与分发侧的真·硬件零拷贝流转。

## Background & Problem Statement

在当前系统中：
1. `packages/face_recognition/src/detector.cpp` 虽然为输入张量分配了 DMA-BUF 缓冲（`dma_input_buf_`），但像素写入却使用手写 CPU 双重 `for` 循环（逐行近邻采样与 BGR->RGB 交换），在 1080P/4K 输入下吃掉大量 CPU 算力。
2. `native/src/backends/rga/rga_vision_backend.cpp` 已具备完整的 `wrapbuffer_fd` 2D 硬件缩放与格式转换能力，但业务算法包从未调用 `zhulong::ops::letterbox`。
3. `native/src/algorithm/ipc/frame_broker.cpp` 在分发帧时无差别执行 `std::memcpy`，未对具备有效 `dma_fd` 的硬件帧提供零拷贝穿透支持。

## Requirements

- **R1: 检测前处理接入 RGA 硬件 Letterbox 算子**：
  - 重构 `FaceDetector::detect`，接入 `zhulong::ops::letterbox(image, input_tensor, params)`；
  - 若 `dma_input_buf_` 有效，将其挂接至目标 `input_tensor`，使 RGA 硬件直接将画面等比缩放并转换色彩写入该 DMA-BUF；
  - 保留 CPU 优雅降级：若 RGA 不可用或返回非 OK，平滑回退至现有 CPU 逻辑。
- **R2: 统一 Letterbox 居中几何后处理反算**：
  - `compute_letterbox_geometry` 采用居中补边（`pad_left = dw / 2`, `pad_top = dh / 2`）；
  - 重构 `FaceDetector::decode_scrfd`，使用 `params.scale`、`params.pad_left`、`params.pad_top` 统一反算目标框与 5 点关键点的高清原图坐标，确保 CPU/RGA 几何行为一致。
- **R3: FrameBroker 零拷贝穿透分支**：
  - 改造 `FrameBroker::publish_frame`：当传入的 `HardwareFrame` 自身已携带有效 `dma_fd >= 0` 时，直接传递/dup 该描述符，跳过对 `dma_slots_` 或 `shm_pool_` 的全尺寸 `std::memcpy`。
  - 对于无 `dma_fd`（软解）的输入帧，保持现有的安全拷贝至 slot 槽位行为。
- **R4: 跨平台一致性与回归保护**：
  - 确保 x86_64 / macOS 开发环境在 `ZHULONG_VISION_BACKEND=cpu` 配置下所有单测（`algo_runtime_test`, `frame_broker_test`, `rtsp_integration_test`）无损通过。
  - 算法包前处理与推理输出与基准数值保持一致（在 RGA 双线性滤波硬件极值容差范围内）。

## Acceptance Criteria

- [x] `FaceDetector::detect` 在硬件加速环境下使用 `zhulong::ops::letterbox` 完成缩放与通道重排，彻底消除 CPU 双重逐像素采样循环。
- [x] `decode_scrfd` 在居中 Letterbox 几何参数下正确还原原图坐标，人脸检测框与 5 点关键点位置与基准对齐。
- [x] 当 `HardwareFrame::dma_fd >= 0` 时，`FrameBroker::publish_frame` 走零拷贝穿透路径，`std::memcpy` 调用次数降为 0。
- [x] 本地开发机测试命令 `python3 native/scripts/build.py test` 100% 通过；`make go-check` 100% 正常。
- [x] 算法插件打包脚本 `build_package.py` 可正常构建出包含新前处理逻辑的算法包。
