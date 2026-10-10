# 需求文档：Native 异构硬件解码帧对象与多算法零拷贝管线设计

## 1. 目标 (Goal)

为 Zhulong 原生引擎设计并实现统一的**异构硬件解码帧对象（HardwareFrame）**与**多算法零拷贝前处理分发机制**。
针对边缘主流异构芯片（Rockchip MPP/RGA/RKNN、华为昇腾 CANN/DVPP/ACL、NVIDIA Jetson NVDECODE/CUDA、CPU 软解回退），建立极简、高性能的帧抽象，保障解码后的单一视频帧可被多个并发 AI 算法以零拷贝形式并行处理，同时杜绝慢算法拖垮底层硬件解码缓冲池的风险。

---

## 2. 核心需求 (Requirements)

### 2.1 极简硬件帧数据抽象 (Minimal Hardware Frame Abstraction)
- **R1.1 精准几何与双向步长**：必须包含有效显示宽高（`width`, `height`）、水平行跨度（`stride` / pitch）以及垂直对齐步长（`vstride` / height stride），确保各算法在切图与定位 YUV/NV12 平面（尤其是 UV 色度分量偏移）时分毫不差。
- **R1.2 统一多后端物理寻址**：
  - 支持 Linux 标准 DMA-BUF 文件描述符（`dma_fd`），适配 Rockchip 与 Jetson 平台；
  - 支持异构设备显存指针（`dev_ptr`），适配华为昇腾 DVPP；
  - 支持 CPU 用户态虚拟地址（`host_ptr`），适配通用 x86/ARM 软解及调试回退。
- **R1.3 格式与时序标识**：支持标准格式码（如 DRM FourCC `DRM_FORMAT_NV12`）与原流时间戳 `pts`，用于 AI 检测框结果与画面时序对齐。
- **R1.4 强生命周期闭环**：通过 RAII 与自定义回收回调（`release_fn`），在帧对象析构时自动触发并归还硬件解码器的内部缓冲池（Buffer Pool）。

### 2.2 1:N 多算法并发零拷贝分发契约 (1-to-N Zero-Copy Dispatch)
- **R2.1 只读共享机制**：多算法消费同一帧时，通过 `std::shared_ptr<HardwareFrame>` 实现并发安全共享，底层物理内存（`dma_fd` 或 `dev_ptr`）保持只读。
- **R2.2 硬件预处理直通**：各算法前处理单元（RGA、VPC、CUDA 核函数）直接以原始帧的物理句柄作为硬件输入，硬件 DMA 直写到算法私有的输入张量（Tensor），CPU 零内存拷贝。
- **R2.3 缓冲池防死锁与解耦隔离**：
  - 严格规范流转生命周期：各算法必须在**硬件预处理任务派发/完成后立即 drop 掉 `HardwareFrame` 引用**，严禁将原帧持有至 NPU 推理结束；
  - 确保解码器输出缓冲池在毫秒级内回收，慢算法（大模型/长推理）绝不阻塞快算法与上游解码器。

### 2.3 原生代码结构与测试覆盖
- **R3.1 头文件组织**：置于 `native/include/Zhulong/frame.hpp`，提供自包含、零外部沉重依赖的 C++17 结构。
- **R3.2 完备测试**：在 `native/tests/` 中编写单元测试，验证：
  - 1:N 多算法多线程并发持有与释放的引用计数正确性；
  - `release_fn` 保证且仅保证调用一次；
  - NV12 等典型格式的行对齐与 UV 平面地址计算逻辑。

---

## 3. 非功能性约束 (Non-Functional Requirements)

1. **零冗余、无虚函数与运行时包袱**：采用朴素结构体（POD-like struct）与极简智能指针，无复杂类继承层次，实例尺寸小于 64 字节。
2. **纯 Native 高内聚**：数据流仅在 C++ 内部流转，不向 Go/CGO 上浮每一帧原始裸数据。
3. **平台中立性**：抽象不硬绑定特定厂商头文件（通过整型 fd / void* dev_ptr 隔离具体驱动 SDK 依赖）。

---

## 4. 验收标准 (Acceptance Criteria)

- [x] **AC-1 (数据结构完备性)**：`HardwareFrame` 包含 `width`, `height`, `stride`, `vstride`, `format`, `dma_fd`, `dev_ptr`, `host_ptr`, `pts`, `release_fn`，各字段语义清晰且编译通过。
- [x] **AC-2 (1:N 并发安全与引用回收)**：模拟 1 解码 ➔ 3 算法并行前处理场景，各算法分别持有 `std::shared_ptr<HardwareFrame>`；验证最后一个算法释放引用时，底层 `release_fn` 精准触发且仅触发 1 次。
- [x] **AC-3 (UV 平面偏移计算准确性)**：编写测试用例验证 1080P（1920x1080, stride=1920, vstride=1088）的 NV12 UV 起始偏移计算（必须为 `stride * vstride` = 2,088,960 字节，而非 `1920 * 1080`），防止色度错位。
- [x] **AC-4 (单元测试与工程构建通过)**：Native 构建与测试套件（`make native-build` / `make native-test`）全项通过。
