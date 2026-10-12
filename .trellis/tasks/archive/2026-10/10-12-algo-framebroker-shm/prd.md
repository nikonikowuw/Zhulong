# 跨进程 FrameBroker 与共享内存帧租赁分发 (PRD)

## 1. 目标与背景

本任务为算法插件架构父任务（`.trellis/tasks/10-10-algorithm-plugin-architecture/`）的**阶段二**交付物。
在阶段一（`10-12-algo-plugin-sdk-runner`）中，我们已成功交付纯 C ABI、现代 C++17 SDK、公共运行库 HAL 与独立 Runner/验包 CLI。
阶段二的核心目标是**打通主媒体进程（ZhulongEngine / Go 宿主）与多个独立算法 Runner 进程之间的跨进程高性能数据流**：
基于 POSIX 共享内存环形槽位池（SHM Ring Buffer）与代次保护租约（Leasing），实现多算法包零拷贝/低拷贝广播扇出、慢包丢帧背压隔离与子进程崩溃自愈回收。

## 2. 核心需求 (Requirements)

### R1. 原生 `HardwareFrame` 导出与多 Plane 序列化契约
- 严格对接 `native/include/Zhulong/frame.hpp`（`zhulong::HardwareFrame`）；
- 规范化导出帧元数据：`stream_id`, `frame_id`, `pts`, `time_base`, 像素格式（NV12, YUV420P, BGR24 等）, `width`, `height`, `stride`, `vstride`；
- 明确多 Plane 内存布局与对齐计算（如 NV12 的 Y 与 UV 步长，YUV420P 的 3 Plane 偏移），确保写入 SHM 后的物理布局能被子进程无缝包装为 `zhulong_frame_view`。

### R2. POSIX 共享内存环形槽位池 (SHM Ring Buffer)
- 主进程创建并维护有界共享内存环形缓冲（默认 8~16 个槽位），支持文件描述符传递或命名空间（`/zhulong_shm_<stream_id>`）；
- 兼容 Linux 4.19+ / Debian 11 GLIBC 2.31，使用标准 `shm_open` + `ftruncate` + `mmap`；
- 主进程负责独占写入（Write），各 Runner 子进程以只读权限（`PROT_READ`）映射共享内存，物理防止算法进程篡改原始画面。

### R3. 多包有界租赁 (Leasing) 与代次保护 (Generation)
- **多租户借用机制**：主进程每派发一帧，为每个订阅该流的算法包增加该槽位（Slot）的租赁引用；
- **代次单调递增 (Generation)**：每个 Slot 维护 64 位单调递增 Generation，防止网络延迟或慢处理导致的 ABA 悬挂确认与提前释放；
- **槽位状态机**：`FREE` ➔ `WRITING` ➔ `LEASED` ➔ `RECLAIMING` ➔ `FREE`；仅当所有租约完成（或超时回收）后，Slot 重新进入可用队列。

### R4. Unix Domain Socket 控制面协议 (IPC Channel)
- 主进程作为 IPC Server 监听私有 Unix Domain Socket（如 `/tmp/zhulong_broker_<stream_id>.sock`），Runner 作为 Client 连接；
- 采用轻量定长二进制帧通知（`FrameNotification`）与确认包（`FrameAck`），支持心跳保活与就绪握手（`Handshake`）；
- 保证无多余的序列化/反序列化开销，单次 IPC 消息在微秒级完成。

### R5. 慢包丢帧背压隔离与崩溃自愈安全门 (Fault Isolation)
- **慢消费者不拖垮主链路**：若某个算法 Runner 处理过慢（超过队列阈值或租约未归还），Broker 对该包直接丢弃新帧（Drop Latest），绝不阻塞主视频采集、解码或其他正常算法包；
- **全槽位枯竭保底**：当所有 SHM Slot 均被占用时，Broker 强制驱逐超时最久或未确认的 Slot，保障主视频流绝对不停顿；
- **崩溃自愈回收**：当某个 Runner 异常退出（Socket 收到 EOF / 断连）时，Broker 立即扫描并回收该进程占用的所有未归还租约，防止槽位泄漏导致系统瘫痪。

## 3. 验收标准 (Acceptance Criteria)

- [x] **AC1 (HardwareFrame 导出与对齐保真)**：从 `HardwareFrame` 正确导出元数据并写入 SHM，跨进程映射后生成的 `zhulong_frame_view` 尺寸、Plane 偏移、Stride 与 PTS 与原始解码帧 100% 一致。
- [x] **AC2 (只读共享内存安全隔离)**：Runner 子进程以只读模式映射共享内存，尝试写入将触发段错误（通过保护机制隔离），主进程单向写入不受污染。
- [x] **AC3 (代次保护与租约释放)**：多个消费者并发持有同一 Slot 租约，单消费者归还或延迟归还不会引发提前释放或重复释放；过时的旧 Generation ACK 被安全忽略。
- [x] **AC4 (慢消费者背压自愈验证)**：在测试夹具中模拟一个慢速 Runner（故意 sleep）和一个高速 Runner，高速 Runner 保持满帧率接收，慢速 Runner 经历平滑丢帧，主媒体源与解码器无阻塞。
- [x] **AC5 (子进程异常崩溃回收验证)**：在多流并发分发期间，对某个正在借用帧的 Runner 进程强行发送 `SIGKILL`，Broker 必须在 100ms 内检测到断连并瞬间释放其持有的所有租约，后续帧正常流转。
- [x] **AC6 (构建集成与 Sanitizer 门禁)**：新增模块与测试接入 `native/CMakeLists.txt`，`make native-test` 全部通过；ASan/UBSan 与 TSan 检验零内存泄漏与数据竞争。

## 4. 边界与范围外
- 本任务使用 POSIX 共享内存，暂不引入真实物理 NPU 的专用 DMA-BUF fd 跨进程导出（留给阶段四针对 RKNN/RGA 硬件优化）；
- 本任务不实现完整的 Go 业务持久化与管理 API（留给阶段三进行 Go 包装与进程编排）。
