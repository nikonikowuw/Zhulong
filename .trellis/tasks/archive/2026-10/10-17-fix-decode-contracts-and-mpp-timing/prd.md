# 修复原生解码节点合约与 MPP 硬件时序缺陷

## Goal

消除 `native/src/nodes/decode` 跨层解码节点存在的 PTS 逻辑判定倒置、硬件队列满时跨层背压错误码击穿导致清空参考帧、`receive_frame` 异常时 fd 与显存泄漏隐患，以及探测权限与显存清理等健壮性缺陷，确保软硬件解码在全链路时序、错误码契约与异常安全上达到工业级一致性。

## Requirements

1. **MPP 时间戳 `has_pts` 与 PTS 保真修正**：
   - 严格遵循 `(raw_pts != AV_NOPTS_VALUE)` 契约判断时间戳有效性；
   - 修复 PTS = 0（首帧常见场景）被误判为无效时间戳的缺陷；
   - 修复上游缺失 PTS（`AV_NOPTS_VALUE` 即 `INT64_MIN`）被误判为有效时间戳并透传出负数时间戳的缺陷，无 PTS 时回退为 0 且 `has_pts = false`。
2. **跨层背压错误码契约归一化**：
   - `IDecodeNode::send_packet` 明确背压契约，当底层输入队列满或超时时统一返回 `AVERROR(EAGAIN)`（与 FFmpeg 标准一致）；
   - 在 `MppDecodeNode::send_packet` 中将 `MPP_ERR_BUFFER_FULL` (-1012) 与 `MPP_ERR_TIMEOUT` (-8) 映射为 `AVERROR(EAGAIN)`，避免上游 `stream.cpp` 误判为严重解码损坏而触发 `flush()` 与关键帧重同步；
   - 在 `stream.cpp` 中兼容 MPP 错误码或依靠归一化后的 `AVERROR(EAGAIN)` 执行 `drain_frames` 并重发。
3. **`MppDecodeNode::receive_frame` 异常安全与 RAII 防御**：
   - 为 `owned_fd`（`::dup(raw_fd)`）与 `mpp_buf` 引用计数引入栈上清理守卫（ScopeGuard）；
   - 确保在 `std::make_shared<HardwareFrame>()` 内存不足抛出异常或后续装配失败时，`owned_fd` 被及时关闭，`mpp_buf` 引用计数被回退，且外层 `mpp_frame_deinit(&frame)` 必然执行。
4. **`MppLoader` 设备访问权限严格判定**：
   - 将对 `/dev/mpp_service` 的存在性检查 `access(..., F_OK)` 改为读写权限检查 `access(..., R_OK | W_OK)`，杜绝容器或非特权组用户环境下的假阳性。
5. **动态分辨率重构时 BufferGroup 清理**：
   - 在 `MppDecodeNode::handle_info_change` 中重置 `buf_group_` 前，显式调用 `mpp_buffer_group_clear(buf_group_)` 释放已绑定的物理缓冲块，再调用 `mpp_buffer_group_put`。
6. **软解 `FFmpegDecodeNode` 扩展兼容 YUVJ420P**：
   - 将 `AV_PIX_FMT_YUVJ420P` 格式正确映射为 `FRAME_FORMAT_YUV420P`，兼容安防 RTSP 摄像头常见 Full-range 码流。

## Acceptance Criteria

- [x] `MppDecodeNode` 对 PTS=0 的输入帧装配出 `has_pts=true` 且 `pts=0`；
- [x] `MppDecodeNode` 对 `AV_NOPTS_VALUE` 的输入帧装配出 `has_pts=false` 且 `pts=0`；
- [x] `MppDecodeNode::send_packet` 在满载时返回标准背压码，`stream.cpp` 正确排空后重试，不再误触发 `flush()` 与丢包重同步；
- [x] `MppDecodeNode::receive_frame` 在异常路径下无句柄泄漏与显存引用泄漏；
- [x] `mpp_loader.cpp` 探针在无读写权限时判定为不支持，平滑回退至软解；
- [x] `ffmpeg_decoder.cpp` 成功识别并装配 `AV_PIX_FMT_YUVJ420P` 为 `FRAME_FORMAT_YUV420P`；
- [x] `native/tests/decoder_test.cpp` 新增覆盖上述边界（PTS=0、无 PTS、软硬件契约）的测试用例并全部通过；
- [x] `make native-test` 全部通过，无数据竞争与内存泄漏。
