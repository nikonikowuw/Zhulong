# 修复 FrameBroker 共享内存与 IPC 协议并发与内存安全缺陷 (Technical Design)

## 1. 架构总览与加固边界

本次加固覆盖 IPC 协议层、UDS Socket 网络层、FrameBroker 调度层及硬件帧内存层：

```txt
┌────────────────────────────────────────────────────────────────────────┐
│                        FrameBroker / Host 进程                         │
│                                                                        │
│  [HardwareFrame] (支持 host_planes[4] 独立平面寻址)                     │
│         │                                                              │
│         ▼                                                              │
│  [publish_frame] ── 安全分平面逐行拷贝 ──► [ShmPool (64B 对齐槽位)]     │
│         │                                                              │
│         ├─ 事务性在途配额管理 (失败自动原子回滚)                          │
│         │                                                              │
│         ▼ (SO_SNDTIMEO 超时保护，防 Worker 卡死拖垮主线程)                │
│  [UnixSocketServer] ── 8B 自然对齐二进制 IPC 报文 ──► [UDS 管道]       │
│         │                                                 │            │
│         └─ read_all 四态状态机 (防半包流脱节)                │            │
│         └─ try...catch 防爆门 + 64KB Payload 上限           ▼            │
└────────────────────────────────────────────────────────────────────────┘
                                                    [Runner 子进程]
```

---

## 2. 详细技术方案

### 2.1 IPC 协议 8 字节自然对齐 (`ipc_protocol.hpp`)

调整 `FrameDispatchMsg` 与 `FrameAckMsg` 的字段顺序，将 64 位整数统一前置，并添加尾部填充，使整体大小与字段偏移均满足 8 字节倍数：

```cpp
#pragma pack(push, 1)

// MSG_FRAME_DISPATCH (Broker -> Client)
struct FrameDispatchMsg {
    uint64_t stream_id{0};    // offset 0  (8B)
    uint64_t frame_id{0};     // offset 8  (8B)
    uint64_t generation{0};   // offset 16 (8B) [前置，8字节对齐]
    int64_t  pts{0};          // offset 24 (8B) [8字节对齐]
    uint32_t slot_index{0};   // offset 32 (4B)
    int32_t  width{0};        // offset 36 (4B)
    int32_t  height{0};       // offset 40 (4B)
    int32_t  format{ZHULONG_PIXEL_FORMAT_UNKNOWN}; // offset 44 (4B)
    int32_t  strides[4]{0};   // offset 48 (16B)
    uint32_t offsets[4]{0};   // offset 64 (16B)
    int32_t  reserved_fd{-1}; // offset 80 (4B)
    uint32_t reserved_pad{0}; // offset 84 (4B) [填充至 8 字节对齐总长]
};

// MSG_FRAME_ACK (Client -> Broker)
struct FrameAckMsg {
    uint64_t stream_id{0};    // offset 0  (8B)
    uint64_t frame_id{0};     // offset 8  (8B)
    uint64_t generation{0};   // offset 16 (8B) [前置，8字节对齐]
    uint32_t slot_index{0};   // offset 24 (4B)
    uint32_t client_id{0};    // offset 28 (4B)
};

#pragma pack(pop)

static_assert(sizeof(FrameDispatchMsg) == 88, "FrameDispatchMsg must be 88 bytes");
static_assert(sizeof(FrameAckMsg) == 32, "FrameAckMsg must be 32 bytes");
static_assert(offsetof(FrameDispatchMsg, generation) % 8 == 0, "generation must be 8-byte aligned");
static_assert(offsetof(FrameAckMsg, generation) % 8 == 0, "generation must be 8-byte aligned");
```

### 2.2 UDS Socket 四态状态机与防脱节 (`unix_socket.cpp`)

重构底层 `read_all` 为状态机枚举：

```cpp
enum class ReadStatus {
    SUCCESS,        // 完整读入请求的所有字节
    IDLE_TIMEOUT,   // 在读取 0 字节时发生 poll 超时 (安全的空闲等待)
    CLOSED,         // 在读取 0 字节时对端优雅关闭 (EOF, recv == 0)
    DESYNC_ERROR    // 已读取部分字节后发生超时或 I/O 错误 (流已破坏，不可恢复)
};
```

**处理逻辑**：
1. 若返回 `IDLE_TIMEOUT`：且主服务仍在运行，允许安全的 `continue;` 进入下一次检测；
2. 若返回 `CLOSED` 或 `DESYNC_ERROR`：立即 `break` 退出循环并调用 `remove_client(client_id)` 触发租约清算，绝不在错位的字节流上继续读取。

### 2.3 防御性安全边界与异常防爆门

1. **Payload 上限检查**：
   ```cpp
   constexpr uint32_t MAX_IPC_PAYLOAD_SIZE = 64 * 1024; // 64KB
   if (header.magic != IPC_MAGIC || header.payload_size > MAX_IPC_PAYLOAD_SIZE) {
       break; // 拒绝异常畸变包
   }
   ```
2. **顶层异常捕获**：
   在 `UnixSocketServer::client_read_loop` 与 `listen_loop` 线程顶层包裹：
   ```cpp
   try {
       // I/O 处理循环
   } catch (const std::exception& e) {
       // 记录异常，优雅关闭本连接
   } catch (...) {
       // 未知异常保护
   }
   ```
3. **Socket 发送超时**：
   在 `UnixSocketServer::listen_loop` 接受客户端后，配置发送超时：
   ```cpp
   struct timeval tv{ .tv_sec = 0, .tv_usec = 50000 }; // 50ms
   setsockopt(client_fd, SOL_SOCKET, SO_SNDTIMEO, &tv, sizeof(tv));
   ```

### 2.4 在途计数器事务性回滚 (`frame_broker.cpp`)

在 `publish_frame` 中，如果获取槽位写入指针失败或向各客户端分发全部失败：
```cpp
if (!slot_data) {
    std::lock_guard<std::mutex> lock(mutex_);
    for (uint32_t cid : target_clients) {
        auto it = clients_.find(cid);
        if (it != clients_.end() && it->second.in_flight_count > 0) {
            it->second.in_flight_count--;
            it->second.delivered_frames--;
            it->second.dropped_frames++;
        }
    }
    slots_[slot_idx].state = SlotState::FREE;
    slots_[slot_idx].active_leases.clear();
    return false;
}
```

### 2.5 多平面独立内存感知与跨步长安全拷贝

1. **`HardwareFrame` 增强**：
   保留原有 `host_ptr`（兼容单一连续缓冲），新增 `host_planes[4]` 与 `plane_strides[4]`；
   在 `ffmpeg_decoder.cpp` 解出 `AV_PIX_FMT_YUV420P` 时，分别填充：
   - `host_planes[0] = av_frame->data[0]`, `plane_strides[0] = av_frame->linesize[0]`
   - `host_planes[1] = av_frame->data[1]`, `plane_strides[1] = av_frame->linesize[1]`
   - `host_planes[2] = av_frame->data[2]`, `plane_strides[2] = av_frame->linesize[2]`
2. **`FrameBroker::publish_frame` 安全拷贝**：
   若 `host_planes[1]` 有效，按各平面行步长和有效宽度逐行/逐平面拷贝到共享内存槽位对应的 `offsets[i]` 区域，防止 `data[0]` 读越界；若为连续 packed 单缓冲，保持紧凑内存单次拷贝。

---

## 3. 风险与向下兼容考量

- **IPC 协议兼容性**：调整字段顺序后，`FrameDispatchMsg` 由 84 字节扩充为 88 字节（尾部 4 字节 padding），`zhulong_algo_runner` 与 `FrameBroker` 必须同步重新编译。
- **性能开销**：多平面逐行拷贝仅在 CPU 软解 YUV420P 时生效；NV12 和 BGR24 仍走连续高速内存拷贝，对 1080P/4K 零拷贝主路径无性能衰退。
