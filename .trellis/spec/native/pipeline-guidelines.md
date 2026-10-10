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

- **按需激活解码 (On-Demand Activation)**：仅在存在 `Zhulong_CONSUMER_AI` 接入时拉起 `Stream::decode_loop` 与解码器实例；全 AI 消费者注销后自动休眠解码管线并清空帧队列，避免空转消耗 CPU/NPU 资源。
- **单流确定性线程模型**：解码器采用 `codec_ctx_->thread_count = 1` 单线程解码，并发由多流模型（多 stream）承载，杜绝底层多线程竞争与线程爆炸。
- **零拷贝帧封装**：通过 `HardwareFrame` 封装解码输出，`release_fn` 闭包安全回收底层 `AVFrame` 引用，由下游算法只读借用。

---

## 2. 有界队列容量与背压策略 (Bounded Queue)

严禁无界队列，避免网络爆流或推理拥塞导致 OOM：

| 传输路径 | 目标场景 | 容量上限 | 拥塞溢出策略 |
| --- | --- | --- | --- |
| **Capture ➔ Decode** | 原始压缩码流包 (H.264/H.265) | 30~60 包 | **丢弃非关键帧 (B/P 帧)**；优先保障 I 帧连续性 |
| **Decode ➔ Storage (录像)** | 录像切片持久化 | 60~120 帧 | **背压阻塞唤醒**；保障录像完整，不随意丢弃 |
| **Decode ➔ Inference (AI)** | 实时视觉检测分析 | **极小 (2~3 帧)** | **直接丢弃旧帧 (Drop Oldest)**！保障视觉分析始终针对最新画面 |

---

## 3. 零拷贝与内存所有权 (HardwareFrame)

统一硬件帧定义位于 `native/include/Zhulong/frame.hpp` (`zhulong::HardwareFrame`)：

1. **不可变帧与多算法只读共享**：节点间传递使用 `std::shared_ptr<const HardwareFrame>`，解码后底层物理内存（`dma_fd`、`dev_ptr`、`host_ptr`）只读，下游 N 个算法并发只读借用，严禁就地修改原像素。
2. **硬件级零拷贝 (DMA-BUF / Device Ptr)**：
   - Rockchip / Jetson: 通过传递 `dma_fd` 由 RGA / CUDA 直接完成 CSC/缩放并写入算法私有 Tensor 缓冲区；
   - 华为昇腾: 通过传递 `dev_ptr` 由 DVPP VPC 硬件完成切图缩放并绑定至 ACL 输入；
   - 严格包含 `stride` 与 `vstride`（垂直对齐步长），NV12 UV 平面偏移计算必须遵循 `y_plane_size() = stride * (vstride > 0 ? vstride : height)`，防止色度错位。
3. **安全 Deleter 守护与生命周期解耦**：
   - 帧对象 RAII 析构时触发 `release_fn` 归还底层解码器缓冲池；
   - ⚠️ **防慢算法拖垮解码器准则**：各算法必须在**硬件预处理任务派发/完成后立即 drop 掉 `HardwareFrame` 引用**，严禁将原帧持有至 NPU 推理阶段，确保解码缓冲池在毫秒级内回归。

---

## 4. 五步优雅停机时序 (Graceful Shutdown)

```txt
1. 发出停止信号 (stop_requested = true)
   │
2. 唤醒并取消所有队列 (queue.cancel() 解除所有读写条件变量阻塞)
   │
3. 停止上游采集源 (关闭 RTSP Socket / V4L2 设备)
   │
4. 汇合工作线程 (调用 worker_thread.join()，等待工作循环跳出)
   │
5. 析构底层硬件资源 (释放 NPU 上下文、关闭解码器、销毁引擎)
```

⚠️ **禁令**：严禁在持有队列互斥锁时调用耗时操作；严禁在工作线程尚未 `join()` 时销毁其正在访问的资源。
