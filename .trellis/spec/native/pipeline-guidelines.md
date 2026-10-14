# 原生节点流水线设计规范 (Node Pipeline)

> C++ 处理节点契约、有界队列背压、零拷贝传递与 5 步优雅停机时序。

---

## 1. 核心链路与节点职责

当前已实现 `src/nodes/capture/rtsp_input.*` 视频采集、`src/nodes/decode/` 解码节点抽象与 FFmpeg 软解实现，以及 `src/pipeline/` 解耦后的流拓扑与有界队列流转。

```txt
Capture Node (RTSP/V4L2) ➔ Decode Node (HW/SW) ➔ Preprocess Node (Resize/CSC) ➔ Inference Node (NPU/GPU)
         │                         │                          │                          │
    [Packet 队列]             [Frame 队列]               [Tensor 队列]              [Result 事件]
```

- **按需激活解码会话 (`DecodeSession`)**：
  - 仅在存在 `Zhulong_CONSUMER_AI` 接入时动态拉起独立的 `DecodeSession`；全 AI 消费者注销后将该会话优雅停止并置空，彻底解耦解码工作线程与流生命周期。
  - **防止取消状态残留**：流生命周期内若经历“AI 接入 ➔ 注销 ➔ 再次接入”，每次均创建全新的 `DecodeSession`，其内部 `packet_queue_` 与 `frame_queue_` 拥有初始干净的非取消状态，杜绝已取消队列引发的 100% CPU 空转与产帧中断。
- **原生 C++ 帧消费通道**：
  - `Stream::acquire_frame(HardwareFrame::ConstPtr &out_frame, timeout)` 提供线程安全的原生帧借用通道，下游模块以只读共享所有权并发消费，由底层闭包安全保活。
- **单流确定性线程模型**：
  - 解码器采用 `codec_ctx_->thread_count = 1` 单线程解码，并发由多流模型（多 stream）承载，杜绝底层多线程竞争与线程爆炸。
- **零拷贝帧封装**：
  - 通过 `HardwareFrame` 封装解码输出，`release_fn` 闭包安全回收底层 `AVFrame` 引用，由下游算法只读借用。
- **订阅通道逐订阅者异常隔离**：
  - `Stream::dispatch` 的扇出循环必须对每个 `subscription->invoke()` 单独 `try/catch`。回调抛 C++ 异常是调用方违约（`engine.h` 明文禁止），**不得**中断同流其他订阅者的数据供给，**也不得**将流置为 `FAILED`。
  - `Subscription::invoke` 在重抛前已恢复 `in_callback` 并置 `enabled=false`，故违约订阅者自动静默失效（后续 `invoke` 首行直接返回），无需从快照移除。
  - ⚠️ 不得改为在 `Subscription::invoke` 内吞掉异常：`native/tests/capture_unit_test.cpp` 依赖其重抛语义来验证 `in_callback` TLS 恢复。
- **解码丢包与重同步可观测性**：
  - `DecodeSession::counters()` 返回 `DecodeCounters` 快照（`decoder_unavailable_drops` / `keyframe_gate_drops` / `congestion_resyncs`），`Stream::callback_exception_count()` 返回被隔离的回调异常数。
  - 全部为 `memory_order_relaxed` 原子自增，无锁、无同步依赖；仅供测试与运维观测，**不跨越 C ABI**（`engine.h` 无对应符号）。

---

## 2. 有界队列容量与背压自愈策略 (Bounded Queue)

严禁无界队列，避免网络爆流或推理拥塞导致 OOM：

| 传输路径 | 目标场景 | 容量上限 | 拥塞溢出策略 |
| --- | --- | --- | --- |
| **Capture ➔ Decode** | 原始压缩码流包 (H.264/H.265) | 30~60 包 | **关键帧自愈重同步 (Keyframe Resync)**：满载丢包后进入等待关键帧状态；新关键帧到达时自动清空残存损坏包队列并恢复解码 |
| **Decode ➔ Storage (录像)** | 录像切片持久化（规划中，未实现） | 60~120 帧 | **背压阻塞唤醒**：保障录像完整，不随意丢弃 |
| **Decode ➔ Inference (AI)** | 实时视觉检测分析 | **极小 (2~3 帧)** | **直接丢弃旧帧 (Drop Oldest)**：保障视觉分析始终针对最新画面 |

### 关键帧自愈状态机 (Keyframe Resync)
1. 压缩包队列满载时，丢弃无法入队的包并置位 `waiting_keyframe_ = true`；
2. 在等待关键帧期间，所有新到达的 P/B 帧均被立即丢弃，避免损坏的参考帧喂入解码器造成花屏；
3. 当收到完整关键帧（IDR）时，重置标志位并清空旧包队列（`packet_queue_.clear()`，仅清元素、不触碰取消标记），确保解码器从完整关键帧开始干净解算；
4. 解码器遇到单包解码错误时，调用 `dec->flush()` 清空 DPB 状态。

---

## 3. 零拷贝与硬件帧契约 (HardwareFrame)

统一硬件帧定义位于 `native/include/Zhulong/frame.hpp` (`zhulong::HardwareFrame`)：

1. **不可变帧与多算法只读共享**：
   - 节点间传递使用 `std::shared_ptr<const HardwareFrame>`（或 `HardwareFrame::ConstPtr`），解码后底层物理内存（`dma_fd`、`dev_ptr`、`host_ptr`）只读，下游 N 个算法并发只读借用，严禁就地修改原像素。
2. **硬件级零拷贝与格式/时间戳保真**：
   - 包含标准格式常量（`FRAME_FORMAT_YUV420P`、`FRAME_FORMAT_NV12` 等），与 Linux DRM / V4L2 FourCC 语义对齐；
   - 包含显式 `has_pts` 标志与 `time_base_num / time_base_den`，真实反映上游流时间基；
   - 严格包含 `stride` 与 `vstride`（垂直对齐步长），NV12 UV 平面偏移计算必须遵循 `y_plane_size() = stride * (vstride > 0 ? vstride : height)`，防止色度错位。
3. **安全 Deleter 守护与生命周期解耦**：
   - 帧对象 RAII 析构时触发 `release_fn` 归还底层解码器缓冲池；移动构造与移动赋值均正确转移所有权并置空原指针，防止 Double-Free；
   - 队列淘汰（Drop Oldest）或取消/重置时，丢弃对象的析构必须在**队列互斥锁外**执行，防止触发外部闭包时形成潜在死锁；
   - ⚠️ **防慢算法拖垮解码器准则**：各算法必须在**硬件预处理任务派发/完成后立即 drop 掉 `HardwareFrame` 引用**，严禁将原帧持有至 NPU 推理阶段，确保解码缓冲池在毫秒级内回归。

---

## 4. 无锁汇合与五步优雅停机时序 (Graceful Shutdown)

为消除跨流锁争用与死锁，所有工作线程的 `join()` 必须遵循**无锁汇合原则（Lock-free Join）**：

```txt
1. 持锁提取待停止对象指针，并将其从流/引擎主结构中解绑移出
   │
2. 退出流与引擎全局互斥锁 (释放 Stream::mutex 与 Engine::control)
   │
3. 锁外发起停止信号 (request_stop()，唤醒并取消队列)
   │
4. 锁外汇合工作线程 (调用 worker_thread.join()，等待工作循环跳出)
   │
5. 锁外析构底层资源 (释放解码器与硬件上下文)
```

⚠️ **禁令**：
- 严禁在持有 `Engine::control` 或 `Stream::mutex` 期间调用任何 `thread.join()`；
- 严禁在工作线程尚未 `join()` 时销毁其正在访问的内存资源。

---

## 5. AI 分析帧通道与 FramePublisher 契约

1. **AI 消费不走 Subscribe 铁律**：
   - `Zhulong_CONSUMER_PREVIEW` 走 Packet 订阅与回调通道；`Zhulong_CONSUMER_AI` 接入激活 `DecodeSession`，帧数据仅通过 `Stream::acquire_frame` 内部帧队列消费。
   - `Stream::Subscribe` 严禁被 AI 消费者调用（硬校验 `kind == ConsumerPreview`）。
2. **发布侧节流与共享内存投递**：
   - 每路 AI 流由独占的 `FramePublisher` 线程拉取解码帧，并依据 `max(sample_fps)`（由 Go 经 C ABI `Zhulong_stream_set_publish_rate` 动态设置）执行发布侧节流；
   - 未到发布周期的帧就地丢弃释放，杜绝无意义的共享内存拷贝与 socket 通知；
   - 推送至 `FrameBroker` 后立即 reset `HardwareFrame` 句柄，杜绝慢算法跨进程反压卡死上游解码会话。
3. **宿主生命周期绑定与资源隔离**：
   - `FrameBroker` 与 `FramePublisher` 由 `Engine` 按需生命周期管理，当首个 `ConsumerAI` 接入时拉起，最后一个 `ConsumerAI` 注销时销毁；
   - 停机时遵循第 4 节无锁汇合与五步优雅停机，先于工作线程 join 前完成停机与资源解绑。
4. **AI 流保活由订阅者集合决定，不由「谁先 acquire」决定**：
   - `Engine` 侧只认 `ConsumerAI` 计数（首个接入建 broker、最后一个注销销毁）；Go 侧另有一层**按算法包**的订阅者集合（`StreamHub` 的 `aiSubscribers`），用于回答「这一路 AI 流现在还该不该活着」。
   - 解析路径与同步订阅必须是**同一次操作**（`BrokerSubscriptionResolver.SyncBrokerSubscriptions`）：只解析不登记，下一轮同步就会把该流回收；只登记不解析，则会留下无 broker 的孤儿流。
   - ⚠️ **解析失败不得提交订阅变更**，也不得遗留已 acquire 的流：实现必须先完成全部路径解析，再进入提交阶段。
   - ⚠️ **发布率跨包取 max**：一个 broker 只有一个发布率，因此该 stream 上的发布率是其**所有** enabled 分析任务 `max(sample_fps)`，而不是单个算法包内的 max。
5. **跨层停机顺序**（Go 侧，硬约束）：
   - `camera.Stop` → `algorithm.Stop` → `native.Close`。理由：AI 消费者与 StreamHub 先撤，runner 子进程再排空，最后才销毁 broker 宿主。
   - 该顺序由 `internal/app/runtime_test.go` 的 `TestAIShutdownOrderPrecedesNativeClose` 守护；改坏顺序会让测试失败，而不是静默产生 use-after-free。
6. **共享内存预算（板端实测）**：
   - 每路 AI 流一份 broker 共享内存：`DEFAULT_SLOT_COUNT=8` × `DEFAULT_SLOT_SIZE=4MB` = **32MB/路**，且槽位在 `start` 时**一次性分配**（发布侧节流不改变静态预算）。
   - ⚠️ RK3568 实测 `/dev/shm` 总容量仅 **976MB**，因此默认参数下约 **30 路** AI 流即打满。若需提高路数上限，应通过 `BrokerOptions` 调小 `slot_count`（`frame_queue_` 只有 3 帧，8 槽位偏保守）。
   - 预算核算请按**最坏情况**（`slot_count` 全部在途）而非稳态估算。
