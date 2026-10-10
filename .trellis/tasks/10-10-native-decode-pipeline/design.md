# 技术设计：Native 节点化解码器接入与流水线架构重构

## 1. 架构拓扑与目录重构设计

### 1.1 现状与重构目标
- **现状**：`native/src/pipeline/engine.hpp` 与 `engine.cpp` 包含所有的 `Subscription`、`Stream`、`Engine`、`Reaper`，职责过于耦合，无法自然插入后续的 `DecodeNode`、`PreprocessNode` 与帧有界队列。
- **重构后结构**：

```txt
native/src/
├── abi/
│   └── engine.cpp                  # 纯 C ABI 转换门面 (调用 Engine 接口)
├── nodes/
│   ├── capture/
│   │   ├── rtsp_input.hpp          # RTSP 连接建立与拉流 Worker
│   │   └── rtsp_input.cpp
│   └── decode/
│       ├── decoder.hpp             # IDecodeNode 纯虚抽象契约
│       ├── ffmpeg_decoder.hpp      # FFmpeg 静态软解节点定义
│       └── ffmpeg_decoder.cpp      # FFmpeg 解码实现与 HardwareFrame 构造
└── pipeline/
    ├── bounded_queue.hpp           # 具备 Drop-Oldest 与停机唤醒的通用有界队列
    ├── subscription.hpp            # 订阅通道状态机与同步排空 (Drain)
    ├── subscription.cpp
    ├── stream.hpp                  # 单物理流生命周期与节点流水线拓扑
    ├── stream.cpp
    ├── engine.hpp                  # 顶层物理流池 (Stream Pool)、Reaper 与 Probe
    └── engine.cpp
```

---

## 2. 核心组件详细设计

### 2.1 通用有界队列 `BoundedQueue<T>` (`native/src/pipeline/bounded_queue.hpp`)
```cpp
namespace zhulong {

enum class OverflowStrategy {
    BLOCK = 0,       // 阻塞等待空闲 (用于关键数据/录像)
    DROP_OLDEST = 1, // 丢弃最旧数据 (用于实时 AI 帧队列)
    DROP_NEWEST = 2  // 丢弃当前新数据
};

template <typename T>
class BoundedQueue {
public:
    explicit BoundedQueue(size_t capacity, OverflowStrategy strategy = OverflowStrategy::BLOCK);
    
    // 线程安全入队，根据溢出策略处理；如果已取消返回 false
    bool push(T item);

    // 线程安全出队；队列空时阻塞，支持超时或被 cancel 唤醒
    bool pop(T& item, std::chrono::milliseconds timeout = std::chrono::milliseconds(100));

    // 协作式取消：清空队列并唤醒所有等待的条件变量 (优雅停机核心)
    void cancel();

    size_t size() const;
    bool is_cancelled() const;
};

}
```

### 2.2 解码节点抽象 `IDecodeNode` (`native/src/nodes/decode/decoder.hpp`)
```cpp
namespace zhulong {

class IDecodeNode {
public:
    using Ptr = std::unique_ptr<IDecodeNode>;
    virtual ~IDecodeNode() = default;

    /** @brief 异步送入压缩包 NALU / Packet */
    virtual int send_packet(const uint8_t* data, size_t size, int64_t pts, int64_t dts) = 0;

    /** @brief 拉取解出的硬件帧 (非阻塞) */
    virtual int receive_frame(HardwareFrame::Ptr& out_frame) = 0;

    /** @brief 清空参考帧队列与内部缓冲 */
    virtual void flush() = 0;
};

}
```

### 2.3 FFmpeg 软解节点 `FFmpegDecodeNode` (`native/src/nodes/decode/ffmpeg_decoder.hpp/.cpp`)
- 基于现有裁剪版 FFmpeg (`libavcodec.a` + `libavutil.a`)；
- 使用 `avcodec_find_decoder`、`avcodec_alloc_context3`、`avcodec_open2`；
- 支持 H.264 (`AV_CODEC_ID_H264`) 与 H.265 (`AV_CODEC_ID_HEVC`)；
- 调用 `avcodec_send_packet` 和 `avcodec_receive_frame`，将 `AVFrame` 包装为 `HardwareFrame`：
  - `host_ptr = av_frame->data[0]`
  - `stride = av_frame->linesize[0]`
  - `vstride = av_frame->height`
  - 析构闭包：`frame->release_fn = [av_frame](HardwareFrame*) { AVFrame* f = av_frame; av_frame_free(&f); };`

---

## 3. 流水线数据流与按需激活机制

```txt
Capture Worker 线程
       │
       ▼ (提取 AVPacket)
   dispatch(packet) 
       │
       ├─► 1. 广播给预览消费者 (直接借用 packet_view 回调 Go，零解码)
       ├─► 2. 广播给录像消费者 (写入录像有界队列)
       │
       └─► 3. 若当前流存在 AI 消费者 (kind == Zhulong_CONSUMER_AI):
                 │
                 ▼ (放入 Packet 队列)
           [Decode Worker 线程]
                 │
                 ▼ avcodec_send_packet / receive_frame
           [生产 HardwareFrame::Ptr]
                 │
                 ▼ (写入 AI FrameQueue，容量 2~3，Drop Oldest)
           [AI 分发 / 前处理管线]
```

**按需解码原则**：
- 当该流没有任何 AI 消费者时，不启动 Decode Worker 线程，不分配解码器上下文，CPU 占用保持极低；
- 当首个 AI 消费者进入时，动态拉起解码节点；最后一个 AI 消费者离开时，优雅停机并释放解码器。

---

## 4. 停机与并发安全约束

1. **锁层次不变**：
   - `Engine::lifecycle` 串行化全局启动/停止；
   - `Engine::control` 保护流池增删；
   - `Stream::mutex` 保护订阅者与节点状态。
2. **优雅停机时序**：
   - 调用 `Stream::request_stop()` 时，先 `cancel()` 所有的 PacketQueue 与 FrameQueue，唤醒可能阻塞的 Decode Worker；
   - 然后 `join()` Capture Worker 和 Decode Worker；
   - 最后释放解码器上下文与硬件资源。
