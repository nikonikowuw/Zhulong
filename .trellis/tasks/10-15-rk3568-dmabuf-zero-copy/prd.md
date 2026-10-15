# RK3568 全链路 DMA-BUF 硬件零拷贝（第一期：AI 跨进程与硬件推理）(10-15-rk3568-dmabuf-zero-copy)

## Goal

在 Rockchip RK3568 平台上打通从 FrameBroker 共享内存槽位、跨进程 UNIX 域套接字 `SCM_RIGHTS` 描述符传递、RGA 视觉前处理到 RKNPU2 神经网络推理的**全链路 DMA-BUF 物理内存硬件零拷贝**闭环，彻底消除 AI 视频流分析链路中用户态与驱动层的所有数据拷贝（`memcpy`）与 CPU Cache 刷新开销。

## Hardware & Architecture Constraints

- **目标平台**：Rockchip RK3568（Quad A55 @ 2.0GHz, 单核 NPU 1.0 TOPS @ INT8）。
- **运行环境**：Debian 11 arm64, Linux 5.10.226, RKNPU2 Runtime 2.3.2 (`librknnrt.so`), RGA 2.1.0 (`librga.so`)。
- **底层硬件分配器**：
  - 首选 `/dev/dma_heap/cma` 或 `/dev/dma_heap/system`（内核已验证挂载 rockchip-iommu，支持物理/散布页面连续映射）；
  - 备选 DRM Dumb Buffer（`/dev/dri/card0` 或 `renderD128`）；
  - 开发机/CI 环境：透明回退至现有 POSIX SHM (`/dev/shm`) 虚拟内存，保障 x86_64 CPU 桩 100% 稳定运行。
- **跨进程文件描述符传递契约**：
  - UNIX Domain Socket + `sendmsg`/`recvmsg` + `SCM_RIGHTS`；
  - 严格 8 字节自然对齐的定长二进制控制包（`FrameDispatchMsg` 的 `reserved_fd` 承载，配合控制消息发送）；
  - 接收端建立 RAII 文件描述符引用与生命期管理，严防跨进程 fd 泄露与悬挂句柄。
- **硬件加速器接口规范**：
  - RGA 2.1.0：统一采用 `importbuffer_fd` / `wrapbuffer_fd`，输入与输出双端绑定 DMA-BUF fd；
  - RKNPU2 2.3.2：采用 `rknn_create_mem_from_fd` + `rknn_set_io_mem`，消灭驱动内显式内存拷贝。

## Requirements

- **R1 物理连续 DMA-BUF 槽位分配器 (DmaBufAllocator)**：
  - 在 Native 端实现 `DmaBufAllocator`，封装 `/dev/dma_heap/`（`cma` / `system`）及 DRM Dumb Buffer ioctl；
  - 提供 `DmaBuffer` RAII 封装（包含 `fd`、`size`、`mmap_addr`），支持成对称的 CPU Cache 同步（`DMA_BUF_IOCTL_SYNC` 带 `DMA_BUF_SYNC_START` / `END` 以及 `EINTR`/`EAGAIN` 重试机制）；
  - 在非 Linux 或缺少 `/dev/dma_heap` 环境下优雅回退至普通堆内存/SHM。
- **R2 FrameBroker 槽位 DMA-BUF 升级与 SCM_RIGHTS 传输**：
  - `FrameBroker` 的槽位池升级支持 DMA-BUF 模式（若底层支持 DMA-BUF，则为每个 Slot 预分配物理 DMA-BUF）；
  - 改造点对点 IPC 发送链路：当分发 `MSG_FRAME_DISPATCH` 时，通过 `sendmsg` 附带 `SCM_RIGHTS` 控制消息将该槽位的 `dma_buf_fd` 投递给客户端；
  - 改造 Runner 接收链路：`recvmsg` 提取附属控制消息中的 `dma_buf_fd`，装配至 `zhulong_frame_view` 与 `zhulong::Image`；
  - 租约生命周期（`MSG_FRAME_ACK`）归还时，Runner 关闭该接收端 `dup` 出的局部 fd，Broker 维持 Slot 代次与安全复用。
- **R3 C ABI 与 SDK 零拷贝帧视图增强 (`zhulong_frame_view` & `zhulong::Image`)**：
  - 在 `zhulong_frame_view` 中明确声明 `dma_buf_fd` 字段（保持 8 字节对齐）；
  - `zhulong::Image` 提供 `int dma_buf_fd() const noexcept` 访问器；
  - `Tensor` 容器支持绑定外部 DMA-BUF（`dma_buf_fd` 与 `dma_buf_va`），提供零拷贝张量构造器。
- **R4 RGA 硬件前处理全 fd 零拷贝化 (`RgaVisionBackend`)**：
  - 重构 `RgaVisionBackend::resize` 与 Letterbox 等算子：
    - 若输入图像具有 `dma_buf_fd >= 0` 且目标 Tensor 具有 `dma_buf_fd >= 0`，直接调用 `wrapbuffer_fd` / `importbuffer_fd` 执行纯硬件加速 CSC/缩放；
    - 维持现有虚拟地址处理作为兜底保障。
- **R5 RKNPU2 驱动零拷贝绑定 (`RknnInferenceBackend`)**：
  - 重构 `RknnInferenceBackend::forward`：
    - 若输入 `Tensor` 拥有有效 `dma_buf_fd`，调用 `rknn_create_mem_from_fd` 并通过 `rknn_set_io_mem` 绑定到 NPU 输入流；
    - 执行 `rknn_run` 时 NPU 直接读取 DMA-BUF 物理显存，消除驱动内显式内存拷贝；
    - 若输入为普通内存，平滑降级走 `rknn_inputs_set`。
- **R6 人脸/人体结构化算法包接入与真机性能标定**：
  - 更新 `packages/face_recognition` 算法包前处理与推理流水线，测试端到端通过 DMA-BUF 流转；
  - 在 RK3568 真机上开展性能标定与压力测试，记录 CPU 占用率与耗时变化。

## Acceptance Criteria

- [ ] `DmaBufAllocator` 在 RK3568 上能成功从 `/dev/dma_heap` 分配有效 fd 并映射物理连续内存。
- [ ] FrameBroker 能通过 `SCM_RIGHTS` 将 `dma_buf_fd` 跨进程安全传给 Runner，Runner 端能正常读取像素且无 fd 泄漏。
- [ ] `RgaVisionBackend` 在全 fd 模式下成功执行双线性缩放与格式转换，输出正确的图像特征。
- [ ] `RknnInferenceBackend` 通过 `rknn_create_mem_from_fd` 成功完成模型推理，输出数值与基准完全一致。
- [ ] 在 RK3568 真机上运行 `packages/face_recognition`，全链路无崩溃、无内存泄漏、CPU 占用显著降低。
- [ ] 本地开发机（x86_64）`make check` 与单元测试保持 100% 通过（优雅回退）。
