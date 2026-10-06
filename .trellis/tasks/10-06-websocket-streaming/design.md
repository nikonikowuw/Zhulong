# WebSocket 媒体流分发与按需订阅 (Technical Design)

## 1. 架构总览与模块边界

本项目在 Go 业务层与前端 Web 播放器之间建立高吞吐、低延迟的 WebSocket 媒体分发中心（`StreamHub`），承接 CGO 原生引擎借用与 Web 端按需消费：

```txt
┌──────────────┐
│ React 前端   │ (WebCodecs / MSE / jmuxer)
└──────▲───────┘
       │ WebSocket (Binary: ZLM1 Header + Annex B NALU)
┌──────┴────────────────────────────────────────────────┐
│ internal/camera (StreamHub & WS Handler)              │
│                                                       │
│ ┌───────────────────────────────────────────────────┐ │
│ │ StreamDispatcher (Per Camera Stream: main / sub)  │ │
│ │  ├─ 扇出广播器 (Fan-out to N Web Clients)          │ │
│ │  ├─ GOP 秒开缓存 (Last KeyFrame + SPS/PPS)        │ │
│ │  └─ 慢客户端背压淘汰 (64-slot Ring Buffer)         │ │
│ └───────▲───────────────────────────────────────────┘ │
└─────────┼─────────────────────────────────────────────┘
          │ engine.Acquire(Preview) & stream.Subscribe()
┌─────────┴─────────────────────────────────────────────┐
│ internal/engine (CGO Native Bridge)                   │
│  └─ FFmpeg RTSP demux -> AVPackets -> Deep Copied Go  │
└───────────────────────────────────────────────────────┘
```

### 核心设计原则
1. **零转码透传**：服务端仅做解复用与 NALU 提取，直接封包二进制帧头后推送到 WebSocket，服务端 CPU 开销维持在微秒级。
2. **多客户端单例拉流**：同一路流无论有多少 Web 客户端观看，底层仅向 Native Engine 注册一个 `ConsumerPreview` 订阅，共享同一个物理连接与解复用器。
3. **按需激活与防抖休眠**：首个客户端连入时唤醒拉流；所有客户端断开后注销拉流，利用 Native 8s Grace Period 避免频繁拉流/断流。
4. **正交状态联动**：流激活/断开时同步更新 `StateRegistry` 的 `SessionState`（`running` / `idle` / `error`），通过 SSE 实时上报。

---

## 2. 二进制 Wire Protocol 规约 (ZLM1)

所有推流数据帧使用统一的 24 字节大端序固定头：

```
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|       'Z'     |      'L'      |      'M'      |      '1'      | (Magic: 0x5A4C4D31)
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|     Codec     |     Flags     |            Reserved           |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                                                               |
+                         PTS (int64)                           +
|                                                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                                                               |
+                         DTS (int64)                           +
|                                                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                   Payload (Annex B NALU ...)                  |
|                              ...                              |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

### 字段定义与取值
- **Magic**：4 字节 ASCII `"ZLM1"`（`0x5A, 0x4C, 0x4D, 0x31`）。前端解析前校验，不匹配直接丢弃。
- **Codec**：1 字节。`0x01` = H.264 (AVC)，`0x02` = H.265 (HEVC)。
- **Flags**：1 字节位掩码：
  - `0x01`（Bit 0）：`KeyFrame`（1 = IDR/关键帧，包含 SPS/PPS/VPS）。
  - `0x02`（Bit 1）：`HasPTS`（1 = PTS 有效，0 = 缺失）。
  - `0x04`（Bit 2）：`HasDTS`（1 = DTS 有效，0 = 缺失）。
- **Reserved**：2 字节。固定填 `0x0000`。
- **PTS / DTS**：各 8 字节（`int64` 大端序）。时钟基数为 90kHz（RTSP 视频标准时钟基），前端可直接用于渲染定时。
- **Payload**：以起始码（`00 00 00 01` 或 `00 00 01`）分隔的完整 NALU 单元。

---

## 3. 核心组件与数据结构设计

### 3.1 `StreamHub`
全局单例，负责管理所有摄像机流的分发器生命周期：
```go
type StreamHub struct {
    engine    *engine.Engine
    store     *CameraStore
    cipher    *LazyCipher
    registry  *StateRegistry
    logger    *zap.Logger

    mu          sync.Mutex
    dispatchers map[string]*StreamDispatcher // key: "camID:role"
    closed      bool
}
```

### 3.2 `StreamDispatcher`
单路流的分发核心，管理底层 Native 订阅与多个 Web 客户端：
```go
type StreamDispatcher struct {
    hub         *StreamHub
    cameraID    string
    role        string
    rawURI      string
    logger      *zap.Logger

    mu          sync.Mutex
    subscribers map[*Client]struct{}
    cachedKey   []byte // 最近一个打包好的关键帧（含 ZLM1 头）
    
    stream      *engine.Stream
    sub         *engine.Subscription
    cancelPump  context.CancelFunc
    closed      bool
}
```

### 3.3 `Client` (WebSocket 客户端会话)
```go
type Client struct {
    conn       *websocket.Conn
    sendChan   chan []byte // 容量 64，有界缓冲
    dispatcher *StreamDispatcher
    done       chan struct{}
    closeOnce  sync.Once
}
```

---

## 4. 关键流程时序与状态机

### 4.1 客户端连入与按需激活 (0 -> 1)
```txt
Client               Handler             StreamHub         Dispatcher          Native Engine
  │                     │                    │                 │                     │
  ├── WS Upgrade ──────>│                    │                 │                     │
  │                     ├── GetOrCreate ────>│                 │                     │
  │                     │   (camID:role)     ├── (0->1) New ──>│                     │
  │                     │                    │                 ├── Acquire(Preview)->│
  │                     │                    │                 ├── Subscribe() ----->│
  │                     │                    │                 ├── go pumpLoop()     │
  │                     │                    │                 │   (Session=Running) │
  │                     ├── RegisterClient ───────────────────>│                     │
  │<── Cached KeyFrame ─┼──────────────────────────────────────┤ (若有缓存立即推送)  │
  │<── Live Frames ─────┼──────────────────────────────────────┤ (持续广播)          │
```

### 4.2 客户端断开与延时释放 (1 -> 0)
```txt
Client               Handler             Dispatcher             Native Engine
  │                     │                    │                        │
  ├── WS Close/EOF ────>│                    │                        │
  │                     ├── Unregister ─────>│                        │
  │                     │                    ├── (Count == 0)         │
  │                     │                    │   Close Sub & Stream ->│ (进入 8s Grace)
  │                     │                    │   Cancel pumpLoop()    │
  │                     │                    │   (Session=Idle)       │
```

### 4.3 背压流控与慢客户端淘汰
每个客户端分配 64-slot `sendChan`。
在 `dispatcher.broadcast(packedFrame)` 时：
```go
for client := range d.subscribers {
    select {
    case client.sendChan <- packedFrame:
    default:
        // 队列满：若为非关键帧，优先丢帧以维持实时性
        if !isKeyFrame {
            continue
        }
        // 若关键帧也无法入队，判定该客户端为极慢僵死连接，主动断开
        go client.CloseWithReason(websocket.ClosePolicyViolation, "slow consumer evicted")
    }
}
```

---

## 5. 错误处理与安全性考量

1. **鉴权安全**：WS 路由受 `auth.RequireAuth` 保护。握手请求必须携带合法的 `zhulong_session` Cookie。
2. **CGO 指针安全**：严格复用 `engine.Subscription` 提供的深拷贝 Go-owned `Packet`，不保留任何 C 原生内存。
3. **并发安全与清理**：
   - 使用 `closeOnce` 保护各层 Close 操作；
   - 写泵（`writePump`）设置每次写超时 `SetWriteDeadline(time.Now().Add(writeWait))`；
   - 读泵（`readPump`）监听对端关闭事件与 Ping/Pong 心跳。
4. **优雅停机**：在 Fx `OnStop` 阶段排空关闭 `StreamHub`，所有活跃 WebSocket 发送 `websocket.CloseGoingAway` 后关闭底层 Native 引用。
