# 原生节点流水线设计规范 (Node Pipeline)

> C++ 处理节点契约、有界队列背压、零拷贝传递与 5 步优雅停机时序。

---

## 1. 核心链路与节点职责

```txt
Capture Node (RTSP/V4L2) ➔ Decode Node (HW/SW) ➔ Preprocess Node (Resize/CSC) ➔ Inference Node (NPU/GPU)
         │                         │                          │                          │
    [Packet 队列]             [Frame 队列]               [Tensor 队列]              [Result 事件]
```

---

## 2. 有界队列容量与背压策略 (Bounded Queue)

严禁无界队列，避免网络爆流或推理拥塞导致 OOM：

| 传输路径 | 目标场景 | 容量上限 | 拥塞溢出策略 |
| --- | --- | --- | --- |
| **Capture ➔ Decode** | 原始压缩码流包 (H.264/H.265) | 30~60 包 | **丢弃非关键帧 (B/P 帧)**；优先保障 I 帧连续性 |
| **Decode ➔ Storage (录像)** | 录像切片持久化 | 60~120 帧 | **背压阻塞唤醒**；保障录像完整，不随意丢弃 |
| **Decode ➔ Inference (AI)** | 实时视觉检测分析 | **极小 (2~3 帧)** | **直接丢弃旧帧 (Drop Oldest)**！保障视觉分析始终针对最新画面 |

---

## 3. 零拷贝与内存所有权

1. **不可变帧包裹**：节点间传递使用 `std::shared_ptr<const Frame>`，解码后像素为只读，下游并行读取，严禁就地修改原像素缓冲区。
2. **硬件级零拷贝 (DMA-BUF)**：在支持的 SoC 上优先分配 DMA-BUF，通过传递文件描述符（`fd`）实现硬解 ➔ 图像预处理 ➔ NPU 推理零内存拷贝。
3. **安全 Deleter 守护**：底层硬件缓冲池在最后一个下游节点借用方析构前保持有效。

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
