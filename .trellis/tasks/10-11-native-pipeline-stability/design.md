# Native pipeline 稳定性修复 技术设计 (Technical Design)

## 1. 架构与会话模型 (Architecture & Session Model)

### 1.1 核心问题定位与解耦原则
当前 `Stream` 结构体直接内嵌了 `packet_queue`、`frame_queue`、`decode_worker` 与 `decode_active` 原子标志。这种“平铺静态成员”模式导致当最后一个 AI 消费者离开时调用 `cancel()` 清空并使队列永久失效，后续新 AI 消费者接入时无法“复活”已取消的队列，从而产生不可恢复状态与 CPU 忙循环。

为了从根本上解决生命周期失配问题，将解码流水线抽象为可重入的**独立解码会话 (`DecodeSession`)**：

```txt
┌─────────────────────────────────────────────────────────────────┐
│ Stream (物理流生命周期，长久存在于 Engine 流池)                        │
│                                                                 │
│   ├── Capture Worker (RTSP/FFmpeg 持续收包)                      │
│   │     │                                                       │
│   │     ├──► dispatch: 优先拷贝送入当前活跃的 DecodeSession       │
│   │     └──► 锁外同步调用 Preview 订阅回调 (保持原有 ABI 契约)      │
│   │                                                             │
│   ├── VideoMetadata 缓存 (codec, extradata, time_base, 分辨率)   │
│   │                                                             │
│   └── std::unique_ptr<DecodeSession> decode_session (按需动态创建) │
│         │                                                       │
│         ├── packet_queue (容量 60，独立生命周期)                   │
│         ├── frame_queue (容量 3，Drop-Oldest，独立生命周期)        │
│         ├── decode_worker (独立解码工作线程)                     │
│         └── IDecodeNode::Ptr (完整参数集初始化的解码器实例)        │
└─────────────────────────────────────────────────────────────────┘
```

- **按需激活**：首个 AI 消费者注册时，创建全新 `DecodeSession` 并拉起工作线程。
- **优雅休眠**：最后一个 AI 消费者注销时，将 `decode_session` 转移给局部变量，在锁外执行 `stop()` 与 `join()`，彻底释放解码器与队列内存。
- **再次激活**：新 AI 消费者接入时，重新构造干净的 `DecodeSession`，队列初始未取消，完全恢复正常解码产帧。

---

## 2. 详细组件设计

### 2.1 解码会话 `DecodeSession`
```cpp
namespace zhulong {

class DecodeSession {
public:
    DecodeSession(uint64_t stream_id, const Video &video_meta);
    ~DecodeSession();

    // 协作式停止并唤醒队列
    void request_stop();

    // 汇合解码线程（必须在锁外调用）
    void join();

    // 向解码会话推送压缩包（由 Capture Worker 调用）
    bool push_packet(const Zhulong_packet_view &packet);

    // 从会话拉取解码出的硬件帧（由 AI 消费者借用）
    bool pop_frame(HardwareFrame::ConstPtr &out_frame, std::chrono::milliseconds timeout);

    // 获取当前帧队列大小
    size_t frame_queue_size() const;

private:
    void decode_loop() noexcept;

    const uint64_t stream_id_;
    const Video video_meta_;

    std::atomic<bool> active_{true};
    Cancellation cancel_;

    BoundedQueue<QueuedPacket> packet_queue_{60, OverflowStrategy::DROP_NEWEST};
    BoundedQueue<HardwareFrame::ConstPtr> frame_queue_{3, OverflowStrategy::DROP_OLDEST};

    std::thread worker_;
    std::atomic<bool> waiting_keyframe_{false}; // 拥塞重同步标记
};

}
```

### 2.2 压缩包拥塞恢复与丢帧重同步 (Congestion & Keyframe Resync)
- **原因**：视频编码存在严格的 P/B 帧参考依赖，若向解码器喂入缺失前序参考帧的 P 帧，会导致花屏、崩溃或持续解码失败。
- **重同步机制**：
  1. `packet_queue_` 采用定制策略或入队过滤：当队列积压达到高水位阈值或 push 失败（满载）时，触发 `waiting_keyframe_ = true`；
  2. 丢弃当前积压的非关键帧包，仅放行后续收到的关键帧（IDR 帧，`key_frame == 1`）；
  3. 当关键帧到达并成功送入队列后，重置 `waiting_keyframe_ = false`；
  4. 解码工作线程在检测到帧序列不连续或跨越丢弃时，调用 `decoder->flush()` 清空参考帧 DPB，从新的关键帧无瑕疵重新起解。

### 2.3 流元数据透传与解码器初始化
- `read_rtsp` 在探测到视频轨道并解析完 `stream_info` 后，`Video` 对象已包含：
  - `codec`: `Zhulong_CODEC_H264` 或 `Zhulong_CODEC_H265`；
  - `extradata`: SPS/PPS/VPS 字节切片；
  - `time_base_num`, `time_base_den`；
  - `width`, `height`。
- `Stream` 在 capture 线程初始化期间通过 `ready` 回调或内部变量保存 `Video` 元数据副本。
- `DecodeSession` 实例化时直接传入该元数据，并在启动解码器时调用：
  ```cpp
  decoder = IDecodeNode::create(video_meta_.view.codec,
                                video_meta_.extradata.data(),
                                video_meta_.extradata.size());
  ```
- 彻底解决依赖 SDP 参数集时解码器因缺少 SPS/PPS 拒绝解码的问题。

### 2.4 无锁 Join 与并发安全 (Lock-free Join)
为避免一路流的解码停止等待阻塞全局 `Engine::control` 或单流 `Stream::mutex`：
- **状态修改与线程汇合分离**：
  ```cpp
  // 在 Stream 内部
  std::unique_ptr<DecodeSession> session_to_stop;
  {
      std::lock_guard<std::mutex> lock(mutex);
      if (!has_ai_consumer() && decode_session_) {
          session_to_stop = std::move(decode_session_);
      }
  }
  // 锁外执行停止与等待
  if (session_to_stop) {
      session_to_stop->request_stop();
      session_to_stop->join();
  }
  ```
- `Engine::release` 内部调用时，先在锁内更新 consumer 映射，若需关闭解码会话，提取出需释放的 session，在释放 `Engine::control` 锁后（或锁外）执行 session 的 join。
- 这样其他线程对 `status()`、`acquire()` 或其它流的操作毫秒级完成，杜绝线程饥饿与卡死。

### 2.5 异常隔离与安全退出
- `decode_loop` 顶层使用统一 `try-catch` 守卫：
  ```cpp
  void DecodeSession::decode_loop() noexcept {
      try {
          // 主解码循环
      } catch (const std::exception &ex) {
          // 记录日志或流状态异常，清理局部状态
      } catch (...) {
          // 兜底防御，禁止异常逃出 noexcept 函数导致 std::terminate
      }
  }
  ```
- 解码器 `send_packet` 与 `receive_frame` 严格遵循 FFmpeg 规范：
  - 若 `send_packet` 返回 `EAGAIN`，持续调用 `receive_frame` 排空输出帧，然后再重试发送该数据包；
  - 遇到严重解码错误时，调用 `flush()` 重置内部缓冲并等待下一个关键帧。

### 2.6 时间戳保真与硬件帧契约完善
- `QueuedPacket` 结构补充：
  ```cpp
  struct QueuedPacket {
      std::vector<uint8_t> data;
      int64_t pts{0};
      int64_t dts{0};
      bool has_pts{false};
      bool has_dts{false};
      int32_t time_base_num{1};
      int32_t time_base_den{90000};
      int32_t codec{0};
      bool key_frame{false};
  };
  ```
- `HardwareFrame` 补充/规范化字段：
  - `time_base_num` 与 `time_base_den`（原生保留来源时基）；
  - `has_pts` 标识（区分“没有 PTS”与“有效 PTS 为 0”）；
  - `format`：软解产出的帧确切标明为 YUV420P（而不是空的或误填为 NV12）。

### 2.7 原生帧消费契约
- 在 `Stream` 上提供供内部节点（如未来的推理管线）安全消费的 C++ 接口：
  - `bool acquire_frame(HardwareFrame::ConstPtr &out_frame, std::chrono::milliseconds timeout = 50ms);`
- 消费端通过 `std::shared_ptr<const HardwareFrame>` 取得只读借用所有权；当 `DecodeSession` 销毁时，由于 `release_fn` 闭包与 `HardwareFrame` 自身所有权绑定，外部仍持有的帧引用依然能安全访问并最终由 shared_ptr 析构释放底层的 `AVFrame`，不会发生悬挂指针或野指针。

---

## 3. 回滚与兼容性矩阵

1. **C ABI 兼容**：现有 `Zhulong_engine_*` 和 `Zhulong_stream_*` 函数签名、参数以及导出的结构体完全不变。
2. **Go 桥接兼容**：Go 侧 `internal/engine` 包测试与行为 100% 保持一致。
3. **回滚方案**：若改动出现未预期的回归，可通过 git 单次提交 revert 回退，不影响持久化数据或配置。
