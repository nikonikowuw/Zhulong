# Rockchip MPP 硬件解码节点与端到端零拷贝贯通 (10-15-rk3568-mpp-hardware-decoder)

## Goal

在 Zhulong 原生引擎中引入基于 Rockchip MPP (`librkmpp`) 的硬件解码器节点 `MppDecodeNode`，替换单流 CPU 软解。解码后帧直接由 MPP 缓冲区（`MppBuffer`）导出内核 DMA-BUF 文件描述符挂载到 `HardwareFrame::dma_fd`，通过 `FrameBroker` 零拷贝穿透下发至算法插件，打通视频流从 RTSP 解码到 NPU 推理全链路无 `memcpy` 的端到端闭环。

## Parent Task & Ordering

- **Parent Task**: `10-15-rk3568-true-zero-copy-pipeline`
- **Execution Ordering**: 在 Child Task 1 (`10-15-rk3568-rga-algo-pipeline`) 交付之后执行。Child 1 已经为 `FrameBroker` 和算法消费端铺平了对 `dma_fd >= 0` 的零拷贝直通支持；本任务专注于生产端的硬件解码输出与全链路集成贯通。

## Requirements

1. **Rockchip MPP 依赖与编译隔离**：
   - 引入 MPP 头文件契约（`rk_mpi.h`, `mpp_buffer.h`, `mpp_frame.h`, `mpp_packet.h`）；
   - 在 Linux/RK3568 target profile 中配置 MPP 动态链接或运行时 `dlopen`；在 macOS/x86_64 平台保持编译隔离与软解回退。
2. **MppDecodeNode 硬件解码节点**：
   - 实现 `IDecodeNode` 接口的 MPP 派生类 `MppDecodeNode`；
   - 支持 H.264 / H.265 码流解码，管理 MPP 解码上下文（`MppCtx`, `MppApi`）；
   - 采用标准 send_packet / receive_frame 异步状态机，适配非阻塞 drain 与 flush。
3. **MppBuffer 导出 DMA-BUF 与生命周期管理**：
   - 从解码输出的 `MppFrame` 获取底层 `MppBuffer`；
   - 通过 `mpp_buffer_get_fd` 提取内核 DMA-BUF 文件描述符，回填至 `HardwareFrame::dma_fd`；
   - 绑定专属 `release_fn` 闭包，引用计数归零时安全释放 `MppFrame` / `mpp_buffer_put`，严防显存与句柄泄漏。
4. **全链路端到端贯通与无 memcpy 验证**：
   - 将解码出来的 `HardwareFrame` 送入 `FrameBroker`，触发零拷贝穿透分支；
   - 验证 RTSP ➔ MPP 解码 ➔ Broker ➔ RGA ➔ RKNN 推理全流程无 CPU 数据拷贝，CPU 占用率达到边缘最佳指标。

## Acceptance Criteria

- [ ] `MppDecodeNode` 在 RK3568 上能正常硬解 1080P/4K H.264 与 H.265 RTSP 视频流。
- [ ] 解码帧成功提取有效 `dma_fd >= 0` 并携带物理 stride 与尺寸属性。
- [ ] `HardwareFrame` 析构时底层 MPP 帧缓冲安全归还，长时运行无 DMA-BUF fd 泄露与显存耗尽。
- [ ] 非 RK 平台或环境初始化失败时，透明回退至 `FFmpegDecodeNode` 软解。
- [ ] 端到端视频分析链路无崩溃，CPU 占用率较软解大幅降低 60% 以上。
