# 跨进程 FrameBroker 与共享内存帧租赁分发 (Technical Design)

## 1. 架构总览

```txt
  +--------------------------------------------------------------------------+
  |                   主媒体服务进程 (Host: ZhulongEngine / Go)                |
  |                                                                          |
  |  [RtspCapture] ➔ [DecodeNode]                                            |
  |                        │ (HardwareFrame)                                 |
  |                        ▼                                                 |
  |               +------------------+                                       |
  |               |   FrameBroker    |                                       |
  |               +--------+---------+                                       |
  |                        |                                                 |
  |       写入 (Write)     |                  控制通知 (IPC Dispatch)         |
  |       PROT_READ|WRITE  |                  Unix Domain Socket             |
  |                        ▼                               ▼                 |
  |    +------------------------+             +-------------------------+    |
  |    |  POSIX SHM Ring Buffer |             |    IPC Server (UDS)     |    |
  |    |  (/dev/shm/zhulong_*)  |             +------------+------------+    |
  +----+-----------+------------+--------------------------+-----------------+
                   |                                       |
                   | 只读映射 (PROT_READ)                   |
                   | (Zero-copy read)                      |
                   |                                       |
       +-----------+--------------------+                  |
       |                                |                  |
       ▼                                ▼                  ▼
+-----------------------------+  +-------------------------------+
|  Runner 进程 A (Plugin A)   |  |   Runner 进程 B (Plugin B)     |
|                             |  |                               |
|  [IPC Client] (Read Msg)    |  |   [IPC Client] (Read Msg)     |
|         │                   |  |          │                    |
|         ▼                   |  |          ▼                    |
|  [zhulong_frame_view]       |  |   [zhulong_frame_view]        |
|         │                   |  |          │                    |
|         ▼                   |  |          ▼                    |
|  api.process(task, frame)   |  |   api.process(task, frame)    |
|         │                   |  |          │                    |
|         ▼                   |  |          ▼                    |
|  [IPC ACK / Result]         |  |   [IPC ACK / Result]          |
+-----------------------------+  +-------------------------------+
```

---

## 2. 共享内存环形槽位池设计 (SHM Ring Buffer)

### 2.1 内存布局规范
共享内存段名称采用 `/zhulong_shm_<stream_id>`，总容量由 `slot_count * slot_size` 决定。
每个槽位的大小满足最大分辨率要求（例如 1080P NV12: 1920 * 1080 * 1.5 = ~3.1MB，加上对齐填充预留 4MB/槽位）。

```txt
+-----------------------------------------------------------------------+
| SHM Header (64B)                                                      |
|   magic: 0x5A484C47 ('ZHLG')                                          |
|   version: 1                                                          |
|   slot_count: 8                                                       |
|   slot_size: 4194304 (4MB)                                            |
+-----------------------------------------------------------------------+
| Slot 0 (4MB)                                                          |
|   [Plane 0: Y 数据 (stride * height)]                                  |
|   [Plane 1: UV 数据 (stride * height / 2)]                             |
+-----------------------------------------------------------------------+
| Slot 1 (4MB) ...                                                      |
+-----------------------------------------------------------------------+
| Slot N-1 (4MB)                                                        |
+-----------------------------------------------------------------------+
```

### 2.2 槽位生命周期与代次保护 (Generation)
每个槽位在 Broker 内存中由 `SlotTracker` 管理：
- `slot_index`：[0, slot_count - 1]；
- `generation`：64 位单调递增代次计数器，每次写入新帧时 `generation++`；
- `active_leases`：当前持有该槽位租约的客户端集合（`std::unordered_set<uint32_t>` 或位图）；
- `state`：
  - `FREE`：无租约，可分配给新帧；
  - `WRITING`：Broker 正在向其拷贝解码数据；
  - `LEASED`：已完成写入，已向客户端派发租约，等待客户端 ACK；
  - `RECLAIMING`：租约已清空或超时强制回收。

**防 ABA 问题**：
客户端 ACK 时必须携带派发时下发的 `generation`。若客户端返回的 `generation` 小于槽位当前的 `generation`，说明该 ACK 属于被超期回收的历史旧帧，Broker 静默丢弃，绝不影响当前正在进行的租约。

---

## 3. Unix Domain Socket 控制面协议 (IPC Protocol)

通信采用定长二进制包头 + 定长 Payload，杜绝任何动态内存分配与序列化反序列化开销。

### 3.1 协议数据结构
```cpp
#pragma pack(push, 1)

struct IpcHeader {
    uint32_t magic;         // 0x5A484C47 ('ZHLG')
    uint16_t msg_type;      // 1: HANDSHAKE, 2: DISPATCH, 3: ACK, 4: HEARTBEAT, 5: RESULT
    uint16_t version;       // 1
    uint32_t payload_size;
    uint32_t sequence;
};

// MSG_HANDSHAKE: 握手注册
struct HandshakeMsg {
    uint32_t client_id;
    uint32_t api_version;
    char client_name[32];
};

// MSG_DISPATCH: 帧分发
struct FrameDispatchMsg {
    uint64_t stream_id;
    uint64_t frame_id;
    int64_t  pts;
    uint32_t slot_index;
    uint64_t generation;
    uint32_t width;
    uint32_t height;
    uint32_t format;        // ZHULONG_PIXEL_FORMAT_*
    uint32_t strides[4];
    uint32_t offsets[4];    // 各 Plane 相对槽位起始的字节偏移
};

// MSG_ACK: 租约归还
struct FrameAckMsg {
    uint64_t stream_id;
    uint64_t frame_id;
    uint32_t slot_index;
    uint64_t generation;
    uint32_t client_id;
};

#pragma pack(pop)
```

---

## 4. 容错、背压与自愈策略

### 4.1 慢客户端丢帧背压 (Drop Latest)
Broker 为每个连接的客户端维护一个有界在途计数器（`in_flight_count`，上限通常为 2）。
当某客户端 `in_flight_count >= max_in_flight` 时：
1. Broker **不向该客户端发送该帧的 `MSG_DISPATCH`**；
2. 不将该客户端计入该槽位的 `active_leases`；
3. 统计计数器 `dropped_frames_count++`；
4. 保证媒体主链路（采集与解码）以全帧率继续运行，其他高速算法不受慢算法任何拖累。

### 4.2 客户端异常断开与崩溃恢复 (Crash Isolation)
当 Socket 检测到 `read() <= 0` 或客户端断开时：
1. 立即触发 `Broker::on_client_disconnected(client_id)`；
2. 遍历所有槽位，将该 `client_id` 从所有槽位的 `active_leases` 集合中清除；
3. 若某槽位在移除后 `active_leases` 归零，立即将其状态重置为 `FREE`，回收共享内存；
4. 杜绝子进程崩溃导致槽位死锁泄露。

### 4.3 槽位枯竭保底机制 (Slot Starvation Fallback)
若所有槽位均处于 `LEASED` 状态且无可用槽位：
1. Broker 检查最老槽位的持有时长（`elapsed_ms`）；
2. 若超时阈值（如 > 500ms），强制回收最老槽位（清除其所有租赁并置位 `generation++`），供新帧写入；
3. 若未超时，丢弃当前新解码帧，确保解码器有界队列不发生背压死锁。
