# 修复 FrameBroker 共享内存与 IPC 协议并发与内存安全缺陷 (PRD)

## 1. 目标与背景

在 `8ef1177` 提交交付跨进程 FrameBroker 与共享内存租赁分发基础能力后，经系统工程代码审查，发现在极端负载、网络抖动、异常进程以及非连续内存场景下，存在 6 处关键并发、内存安全与协议健壮性缺陷：
1. 协议结构体未自然 8 字节对齐，在 ARM64/嵌入式异构平台上存在性能损耗与 SIGBUS 风险；
2. Socket 读超时/半包处理不当引发协议字节流永久脱节（Desync）；
3. IPC 载荷无上限与子进程工作线程缺乏异常防爆门，畸变包可能导致主进程 `std::terminate()` 崩溃；
4. 阻塞式 Socket 发送可能因慢/卡死 Worker 反向阻塞主媒体抓流/解码线程；
5. 推帧失败路径上在途帧计数器未回滚导致配额永久泄漏；
6. 软解多平面 YUV420P 帧错误假定物理连续，导致直接 `memcpy` 产生越界读踩踏。

本任务旨在彻底修复上述所有并发与内存安全缺陷，补齐安全契约与回归测试，达到生产级高可靠性标准。

---

## 2. 需求清单 (Requirements)

### R1. 协议结构体 8 字节自然对齐 (ARM64 硬件对齐保证)
- 调整 `FrameDispatchMsg` 与 `FrameAckMsg` 的字段顺序与填充，保证所有 64 位整数（`generation`、`pts`、`stream_id`、`frame_id`）严格位于 8 字节对齐边界上；
- 消除编译器 `-Waddress-of-packed-member` 警告，杜绝 ARM64 / AArch64 上的非对齐访存异常。

### R2. Socket 读操作状态机与防流脱节 (Desync-Free Socket I/O)
- 重构 `read_all` 为状态机模式，显式区分 `SUCCESS`、`IDLE_TIMEOUT`（0 字节到达时超时）、`CLOSED`（对端正常关闭）、`DESYNC_ERROR`（已读入部分数据后超时或错误）；
- 若读入部分字节后发生超时或错误，视为不可恢复的流破坏，严禁执行 `continue` 跳过，必须断开连接并回收客户端；
- 正确识别 `POLLIN` + `recv() == 0` 的对端正常关闭（EOF），杜绝忙轮询。

### R3. 载荷大小硬上限与顶层线程异常防爆门 (Crash Prevention)
- 对 `IpcHeader.payload_size` 施加硬上限（如 $\le 64\text{KB}$），超出上限的异常包立即熔断拒绝并断开连接；
- 在 `UnixSocketServer::client_read_loop` 等独立分离线程中加入完整的 `try ... catch` 异常防爆门，拦截一切 C++ 异常与 `std::bad_alloc`，绝不触发 `std::terminate()` 拖垮主进程。

### R4. 非阻塞/带超时发送保护 (Worker Stalls Isolation)
- 在接收或连接 UDS Socket 时配置 `SO_SNDTIMEO` 发送超时（如 50ms），防止由于异常 Worker 卡死导致 `FrameBroker::publish_frame` 同步阻塞主视频流解码/采集流水线。

### R5. 失败推帧的原子状态回滚 (In-Flight Counter Leak Prevention)
- 在 `FrameBroker::publish_frame` 中，若后续槽位写入指针无效或下发失败，必须原子回退所有已递增的客户端 `in_flight_count`，杜绝配额泄漏。

### R6. 多平面内存布局感知与安全拷贝 (Multi-Planar Memory Safety)
- 增强 `HardwareFrame` 对独立平面的寻址能力（`host_planes[4]` 与 `plane_strides[4]`）；
- `FrameBroker::publish_frame` 在处理多平面格式（如 YUV420P、独立 NV12）时，按各平面独立指针与步长逐行/逐平面拷贝到槽位对应 offset，严禁跨越 plane0 缓冲区做连续 `memcpy`。

### R7. 自动化测试与断言验证 (Regression Coverage)
- 针对半包超时重试、畸变超大 Payload 拒绝、ARM64 对齐断言、慢 Worker 阻塞超时隔离、多平面 YUV420P 正确拷贝等场景编写完备的自动化回归测试；
- 确保 CTest、ASan/UBSan 与 TSan 全绿。

---

## 3. 验收标准 (Acceptance Criteria)

- [ ] **AC-1**：`static_assert` 确认 `FrameDispatchMsg` 与 `FrameAckMsg` 中所有 `uint64_t`/`int64_t` 字段偏移量均为 8 的整数倍，且开启 `-Waddress-of-packed-member` 0 警告。
- [ ] **AC-2**：单次 `read_all` 模拟半包（仅传输 4 字节）并触发超时后，服务端立即安全断开并清理该通道，无字节错位、流脱节或 100% CPU 忙轮询。
- [ ] **AC-3**：模拟畸变 `payload_size = 0xFFFFFFFF` 的非法包，服务端安全拒绝并断开，无 `std::bad_alloc` 或 `std::terminate()` 崩溃。
- [ ] **AC-4**：配置 `SO_SNDTIMEO` 后，模拟对端彻底挂起不读 Socket，`publish_frame` 达到超时上限后平稳丢帧返回，耗时不超过指定阈值，不永久阻塞主线程。
- [ ] **AC-5**：模拟 `slot_write_ptr` 异常返回 nullptr，各客户端 `in_flight_count` 保持为 0，无任何状态计数泄漏。
- [ ] **AC-6**：输入来自 `ffmpeg_decoder` 的真实三平面 `YUV420P` 帧，各分量正确逐平面写入共享内存槽位对应偏移，ASan 零越界告警。
- [ ] **AC-7**：全量 CTest (15/15 以上)、ASan/UBSan、TSan 100% 通过。
