# RK3568 平台端到端真·硬件零拷贝重构 (10-15-rk3568-true-zero-copy-pipeline)

## Goal

彻底消除 Rockchip (RK3568) 平台上视频分析流水线的两处核心 CPU 性能瓶颈（FrameBroker 中的全分辨率 `std::memcpy`、算法前处理中的 CPU 逐像素采样循环），打通从「视频解码 ➔ IPC 跨进程分发 ➔ RGA 视觉硬件前处理 ➔ RKNN NPU 硬件推理」的真正端到端硬件零拷贝。

## Architecture & Subtask Decomposition

本项目拆分为两个可独立交付与验证的子任务：

```
10-15-rk3568-true-zero-copy-pipeline (Parent Task)
├── 10-15-rk3568-rga-algo-pipeline (Child Task 1: 算法端 RGA 硬件化与 Broker 穿透)
└── 10-15-rk3568-mpp-hardware-decoder (Child Task 2: MPP 硬件解码节点与端到端贯通)
```

### Child Task 1: 算法前处理 RGA 硬件加速与 FrameBroker 零拷贝穿透 (`10-15-rk3568-rga-algo-pipeline`)
- **定位**：算法消费端硬件化改造（零新外部 SDK 依赖，仓库内已有 RGA 和 RKNN 后端）。
- **交付内容**：
  1. 重构 `packages/face_recognition/src/detector.cpp`，消除手写 CPU 双重 `for` 循环，接入 `zhulong::ops::letterbox` (RGA 2D 硬件缩放与 CSC)，输出直写 `dma_input_buf_`。
  2. 重构 `packages/face_recognition/src/aligner.cpp` 及 `extractor.cpp`，人脸对齐与裁剪适配硬件加速，支持 DMA-BUF 绑定与 RKNN 直绑。
  3. 改造 `FrameBroker`，当且仅当输入帧有有效 `dma_fd >= 0` 时，支持零拷贝直传；保持软解帧时的优雅降级。
  4. 跨平台 x86_64 CPU 桩 100% 绿色回退保障。

### Child Task 2: Rockchip MPP 硬件解码节点与端到端零拷贝贯通 (`10-15-rk3568-mpp-hardware-decoder`)
- **定位**：视频生产端硬解改造与全链路贯通。
- **交付内容**：
  1. 引入 Rockchip MPP (`libmpp.so` / `rk_mpi.h`) 硬件解码支持与编译配置（profiles / CMake）。
  2. 实现 `MppDecodeNode` 继承 `IDecodeNode`，支持 H.264/H.265 硬解，将解码出的 `MppFrame` / `MppBuffer` 导出为内核 DMA-BUF fd，挂载到 `HardwareFrame::dma_fd`。
  3. 将 MPP 解码出的 `dma_fd` 通过 `FramePublisher` 送入 `FrameBroker`，触发 Child 1 的零拷贝穿透分支，彻底干掉 `FrameBroker` 的 `std::memcpy`。
  4. 端到端实测验证：从 RTSP 码流 ➔ MPP 硬解 ➔ Broker 零拷贝 IPC ➔ RGA 前处理 ➔ RKNN 推理全流程无 CPU 内存拷贝。

## Cross-Child Acceptance Criteria

- [ ] **算法前处理无 CPU 循环**：在 RK3568 上，人脸检测与对齐前处理 100% 由 RGA 硬件单元完成，消除 CPU 密集采样。
- [ ] **分发链路无内存复制**：输入硬件解码帧时，`FrameBroker::publish_frame` 不调用 `std::memcpy`，仅靠内核 fd 传递与生命周期借用流转。
- [ ] **硬解生产连续物理页**：MPP 解码节点解出帧直接由 `MppBuffer` 导出有效 `dma_buf_fd`。
- [ ] **精度与一致性保真**：端到端推理结果置信度与坐标框同 CPU baseline 保持一致（允许 RGA 双线性滤波极值容差）。
- [ ] **全平台保底与优雅降级**：非 Linux/RK 平台或软解场景自动安全降级，现有单测与 `make check` 100% 通过。
