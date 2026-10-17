# 技术方案设计: 修复原生解码节点合约与 MPP 硬件时序缺陷

## 1. 架构与边界约定

```txt
Capture / Demux (RTSP/V4L2)
       │
       ▼ [Packet: pts, dts, data]
DecodeSession::decode_loop()
       │
       ├── IDecodeNode::send_packet()  ──► [统一返回 0, AVERROR(EAGAIN), <0 错误]
       │         │
       │         ├── FFmpegDecodeNode (返回 0 / AVERROR(EAGAIN))
       │         └── MppDecodeNode (将 MPP_ERR_BUFFER_FULL / TIMEOUT 映射为 AVERROR(EAGAIN))
       │
       └── IDecodeNode::receive_frame() ──► [统一填充 HardwareFrame]
                 │
                 ├── FFmpegDecodeNode: 保持 has_pts = (pts != AV_NOPTS_VALUE)
                 └── MppDecodeNode: 保持 has_pts = (pts != AV_NOPTS_VALUE)，以 RAII Guard 看护 fd/显存
```

## 2. 核心修改技术细节

### 2.1 时间戳契约保真 (`MppDecodeNode::receive_frame`)
- 上游 `stream.cpp` 与采集侧以 `AV_NOPTS_VALUE`（`INT64_MIN`）标定缺失时间戳。
- MPP 硬件解码器在 `mpp_packet_set_pts` 后会保真输出在 `mpp_frame_get_pts`。
- 修正判定逻辑：
  ```cpp
  int64_t raw_pts = loader_.mpp_frame_get_pts(frame);
  out_frame->has_pts = (raw_pts != AV_NOPTS_VALUE);
  out_frame->pts = out_frame->has_pts ? raw_pts : 0;
  ```
- 从而杜绝首帧 `pts = 0` 被标为 `has_pts = false`，且上游无 PTS 时不会误生成 `-9223372036854775808LL`。

### 2.2 跨层背压错误码统一 (`MppDecodeNode::send_packet` & `stream.cpp`)
- `IDecodeNode::send_packet` 明确文档规范：
  - 成功返回 `0`；
  - 缓冲区满/背压重试返回 `AVERROR(EAGAIN)`（调用方需排空帧队列并重试当前包）；
  - 遇到其他严重错误返回 `< 0`（调用方冲刷状态并重同步关键帧）。
- 在 `MppDecodeNode::send_packet` 中：
  ```cpp
  if (put_ret == mpp::MPP_OK) {
      return 0;
  }
  if (put_ret == mpp::MPP_ERR_BUFFER_FULL || put_ret == mpp::MPP_ERR_TIMEOUT) {
      return AVERROR(EAGAIN);
  }
  return put_ret;
  ```
- 在 `stream.cpp` 中保持对 `AVERROR(EAGAIN)` 的先排空再重发机制，同时对 `ret != AVERROR_EOF && ret != AVERROR(EAGAIN)` 的严重错误进行状态冲刷。

### 2.3 RAII 资源防泄漏守护 (`MppDecodeNode::receive_frame`)
- 在提取 `raw_fd`、`::dup(raw_fd)` 和 `buffer_inc_ref(mpp_buf)` 后，构建栈上 `ScopeGuard`：
  ```cpp
  struct MppFrameGuard {
      mpp::MppLoader &loader;
      mpp::MppBuffer buf{nullptr};
      int fd{-1};
      bool active{true};
      ~MppFrameGuard() {
          if (active) {
              if (fd >= 0) ::close(fd);
              if (buf) loader.buffer_put(buf);
          }
      }
  } guard{loader_, mpp_buf, owned_fd, true};
  ```
- 组装成功并注入 `out_frame->release_fn` 之后，执行 `guard.active = false`。
- 如果其间任何内存分配或操作失败抛出异常，析构函数自动安全释放 `owned_fd` 并回退 `buffer_inc_ref`。

### 2.4 设备节点权限探测 (`MppLoader::load_symbols`)
- `access("/dev/mpp_service", R_OK | W_OK) != 0`，只有当前进程拥有读写权限时才激活探针，否则安全判定为不支持并透明回退至 CPU 软解。

### 2.5 缓冲区清理 (`handle_info_change` 与 `destroy_mpp`)
- 在重置或销毁 `buf_group_` 前增加 `loader_.mpp_buffer_group_clear(buf_group_)` 检查与调用，释放已提交的物理缓冲块。

### 2.6 格式兼容 (`FFmpegDecodeNode::receive_frame`)
- `if (av_frame->format == AV_PIX_FMT_YUV420P || av_frame->format == AV_PIX_FMT_YUVJ420P)` 统一装配为 `FRAME_FORMAT_YUV420P`。

## 3. 测试与验证策略

1. 单元测试 `native/tests/decoder_test.cpp`：
   - 增加 `pts = 0` 测试用例，断言 `frame->has_pts == true` 且 `frame->pts == 0`；
   - 增加缺失 PTS（传入 `AV_NOPTS_VALUE`）测试用例，断言 `frame->has_pts == false` 且 `frame->pts == 0`；
   - 增加 `AV_PIX_FMT_YUVJ420P` 格式映射断言测试；
   - 验证 `MppDecodeNode::send_packet` 错误码对齐；
2. 执行 `python3 native/scripts/build.py go test -race ./...` 与 native 编译验证。
