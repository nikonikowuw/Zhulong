# 修复 FrameBroker 共享内存与 IPC 协议并发与内存安全缺陷 (Execution Plan)

## 阶段一：协议结构体自然对齐与编译断言

- [x] 调整 `native/src/algorithm/ipc/ipc_protocol.hpp` 中 `FrameDispatchMsg` 与 `FrameAckMsg` 字段顺序：
  - 将 `generation` 提前至 8 字节对齐位置；
  - 在 `FrameDispatchMsg` 尾部增加 `reserved_pad` 确保整体 8 字节整除；
- [x] 增加 `static_assert` 检查字段偏移（`offsetof`）与总大小；
- [x] 验证包含该头文件在开启 `-Waddress-of-packed-member` 时零警告。

## 阶段二：UDS Socket 四态状态机、异常防爆门与发送超时

- [x] 在 `native/src/algorithm/ipc/unix_socket.cpp` 中重构 `read_all`：
  - 实现 `ReadStatus` 四态枚举（`SUCCESS` / `IDLE_TIMEOUT` / `CLOSED` / `DESYNC_ERROR`）；
  - 区分 0 字节到达与部分字节到达后的超时；
- [x] 在 `UnixSocketServer::client_read_loop` 中重构读取逻辑：
  - 遇到 `IDLE_TIMEOUT` 安全 `continue`；
  - 遇到 `CLOSED` 或 `DESYNC_ERROR` 立即 `break` 退出并回收客户端；
  - 增加 `header.payload_size > MAX_IPC_PAYLOAD_SIZE`（64KB）熔断拦截；
  - 在线程循环外层包裹 `try ... catch` 防爆门；
- [x] 在 `UnixSocketServer::listen_loop` 中设置客户端套接字 `SO_SNDTIMEO` 发送超时（50ms）。

## 阶段三：在途配额原子回滚、多平面内存安全与 Runner 同步

- [x] 增强 `native/include/Zhulong/frame.hpp` 中 `HardwareFrame`：
  - 添加 `host_planes[4]` 与 `plane_strides[4]` 数组并保证移动语义正确传递；
- [x] 在 `native/src/nodes/decode/ffmpeg_decoder.cpp` 中解出 `AV_PIX_FMT_YUV420P` 时填充 `host_planes[0..2]` 与 `plane_strides[0..2]`；
- [x] 在 `native/src/algorithm/ipc/frame_broker.cpp` 中：
  - `publish_frame` 失败路径上添加客户端 `in_flight_count`、`delivered_frames` 与 `dropped_frames` 的原子回滚；
  - 增强 `FRAME_FORMAT_YUV420P` 分平面安全拷贝逻辑，防止对 `data[0]` 做单缓冲连续 `memcpy` 产生越界读；
- [x] 在 `native/src/algorithm/runner/runner.cpp` 中同步更新 `FrameDispatchMsg` 的字段映射与 payload 尺寸校验。

## 阶段四：回归测试与全量质量门禁

- [x] 在 `native/tests/frame_broker_test.cpp` 中新增/完善测试用例：
  - 测试用例 1：半包超时（仅发 4 字节）不发生死循环与字节流脱节；
  - 测试用例 2：超大畸变 Payload（65KB / 0xFFFFFFFF）安全拒绝；
  - 测试用例 3：卡死 Worker 触发 `SO_SNDTIMEO` 隔离且不永久阻塞主线程；
  - 测试用例 4：`slot_write_ptr` 异常时在途配额零泄漏校验；
  - 测试用例 5：真实多平面 YUV420P 帧分发与内存完整性断言；
- [x] 运行全量验证：
  - `make native-test` (15/15 以上全绿)；
  - ASan/UBSan 内存与越界检查；
  - TSan 数据竞争检查；
  - `make go-check` Go 桥接回归。
