# 技术设计：实现底库热更新通道 (10-14-gallery-hot-sync)

## 1. 架构目标与拓扑

打通人员底库从 Go 宿主到 Native 算法 Runner 子进程的**下行链路**：
- **冷启动全量同步（SYNC）**：当算法 Runner 启动接入或由宿主触发同步时，Go 从 SQLite 加载全量人员特征，经分片保护（每片 < 64KB）下行广播至 Runner，Runner 聚合后建立本地快速索引。
- **运行期增量同步（UPSERT / DELETE）**：当人员/人脸在 Go 业务端新增、更新、删除时，立即向活跃 Runner 发送增量消息，Runner 实时就地更新索引，无需重启进程或中断视频流。
- **IPC 消息类型统一**：补齐 Native 与 Go 两端的枚举定义（10/11/12/13）并纳入跨语言护栏守护。

```txt
┌──────────────── Go 宿主 ────────────────┐
│  person.Service                         │
│    ├─ AddFaceWithReader ──→ UpsertMsg ──┤
│    ├─ DeleteFace/Person ──→ DeleteMsg ──┤
│    └─ GetGallerySyncData ─→ SyncShards ─┤
│                                         │
│  algorithm.GalleryBroadcaster           │
│    └─ 分片打包 (<= 64KB / 片)           │
└───────────────────┬─────────────────────┘
                    │ CGO: Zhulong_engine_broadcast_ai_message
                    ▼
┌────────── Native Engine (Broker 宿主) ──────────┐
│  Engine::broadcast_ai_message                  │
│    └─ FrameBroker::broadcast_message           │
│         └─ UnixSocketServer::broadcast         │
│              (复用流专属 UDS 连接)               │
└───────────────────┬────────────────────────────┘
                    │ IPC: MSG_GALLERY_SYNC / UPSERT / DELETE
                    ▼
┌───────── 算法 Runner (Subprocess) ──────────────┐
│  UnixSocketClient 接收下行控制消息               │
│    ├─ ShardedSyncContext (分片聚合与超时保护)     │
│    ├─ GalleryIndex (并发安全本地索引 face_id ─→ 特征)
│    └─ 回传 MSG_GALLERY_ACK (确认全量同步完成)     │
└────────────────────────────────────────────────┘
```

---

## 2. 协议与消息定义

### 2.1 消息类型枚举 (`native/src/algorithm/ipc/ipc_protocol.hpp`)

```cpp
enum MessageType : uint16_t {
    MSG_UNKNOWN         = 0,
    MSG_HANDSHAKE_REQ   = 1,
    MSG_HANDSHAKE_RESP  = 2,
    MSG_FRAME_DISPATCH  = 3,
    MSG_FRAME_ACK       = 4,
    MSG_HEARTBEAT       = 5,
    MSG_DISCONNECT      = 6,
    MSG_RESULT          = 7,
    MSG_GALLERY_SYNC    = 10,
    MSG_GALLERY_UPSERT  = 11,
    MSG_GALLERY_DELETE  = 12,
    MSG_GALLERY_ACK     = 13
};
```

与 Go 侧 `internal/algorithm/process.go` 常量严格一致：
- `MsgGallerySync   = 10`
- `MsgGalleryUpsert = 11`
- `MsgGalleryDelete = 12`
- `MsgGalleryAck    = 13`

并在 `internal/algorithm/ipc_contract_test.go` 中加入静态断言防护。

### 2.2 64KB 分片协议（针对 `MSG_GALLERY_SYNC`）

单个 512 维 `float32` 向量序列化为 JSON 数组占用约 4KB 文本，单条 IPC Payload 上限为 64KB（`MAX_IPC_PAYLOAD_SIZE = 65536`）。当底库包含超过 12 张人脸时必须分片。

**分片载荷结构 (`GallerySyncShardPayload`)**：
```json
{
  "sync_id": "sync_1729000000_abcd",
  "shard_index": 0,
  "shard_total": 3,
  "items": [
    {
      "face_id": "face_1",
      "person_id": "EMP_001",
      "person_name": "张三",
      "image_path": "EMP_001_face_1.jpg",
      "features": [0.012, -0.045, ...]
    }
  ]
}
```

- **分片策略**：Go 侧每 10 个 `GallerySyncItem` 切分为一个 Shard（约 40KB ~ 50KB JSON），严格保证单片不触碰 64KB 边界。
- **空底库语义**：底库为空时，发送 1 片（`shard_index=0, shard_total=1, items=[]`），Runner 接收后清空本地底库并回复 ACK。
- **组装与超时机制**：Runner 侧针对每个 `sync_id` 维护未决组装上下文，所有 `0..shard_total-1` 分片收齐后一次性原子提交并清空旧底库；若 5 秒内未收齐或接收期间出现新的 `sync_id`，则丢弃前序未收齐碎片并报错。

### 2.3 增量消息 (`MSG_GALLERY_UPSERT`)

```json
{
  "face_id": "face_2",
  "person_id": "EMP_002",
  "person_name": "李四",
  "image_path": "EMP_002_face_2.jpg",
  "features": [0.055, 0.088, ...]
}
```
- 若 `face_id` 已存在则原地覆盖更新；若不存在则追加至本地底库索引。

### 2.4 删除消息 (`MSG_GALLERY_DELETE`)

```json
{
  "face_id": "face_2",
  "person_id": "EMP_002"
}
```
- 若提供 `face_id`，精确删除对应单张人脸；
- 若仅提供 `person_id`（`face_id` 为空），则级联删除该人员下的全部人脸特征。

### 2.5 确认消息 (`MSG_GALLERY_ACK`)

```json
{
  "sync_id": "sync_1729000000_abcd",
  "success": true,
  "total_faces": 42,
  "error_msg": ""
}
```

---

## 3. 跨层通道与 CGO 契约

### 3.1 传输通道复用

遵循架构决策 **D1**：`MSG_GALLERY_*` 与 `FrameBroker` **复用同一条 UDS**（无需为底库开设独立 Socket）。
1. `FrameBroker` 作为 UDS Server，已通过 `server_.broadcast()` 支持向所有连接的 Runner 客户端广播下行报文。
2. 在 `FrameBroker` 中新增 `broadcast_message(uint16_t msg_type, const void* payload, size_t size)`。
3. 在 `Engine` 中新增广播接口：
   ```cpp
   Zhulong_status_t Engine::broadcast_ai_message(Zhulong_stream_id stream_id, uint16_t msg_type, const void* payload, size_t size);
   ```
   当 `stream_id == 0` 时，向 Engine 管理的所有活跃流的 FrameBroker 广播。
4. 导出 C ABI：
   ```c
   Zhulong_status_t Zhulong_engine_broadcast_ai_message(Zhulong_engine_h engine,
       Zhulong_stream_id stream, uint16_t msg_type, const void *payload, size_t payload_size);
   ```
5. Go `internal/engine` 封装：
   ```go
   func (e *Engine) BroadcastAIMessage(ctx context.Context, streamID uint64, msgType uint16, payload []byte) error
   ```

---

## 4. Runner 进程内底库索引 (`GalleryIndex`)

### 4.1 索引结构与并发安全
Runner 包含主分发线程与推理工作循环：
```cpp
struct FaceTemplate {
    std::string face_id;
    std::string person_id;
    std::string person_name;
    std::vector<float> features; // 512 floats
};

class GalleryIndex {
public:
    void reset(std::vector<FaceTemplate> templates);
    void upsert(FaceTemplate tmpl);
    void remove_face(const std::string& face_id);
    void remove_person(const std::string& person_id);
    std::vector<FaceTemplate> snapshot() const;
    size_t size() const;
private:
    mutable std::mutex mutex_;
    std::unordered_map<std::string, FaceTemplate> faces_; // face_id -> template
};
```
- `snapshot()` 产生只读快照供单帧 1:N 检索使用，避免长时锁争用；
- 增量 `upsert` 和 `remove` 操作持有细粒度锁快速完成。

---

## 5. Go 业务层接线与事件通知

### 5.1 抽象通知器 `GalleryNotifier`
在 `internal/algorithm` 中定义：
```go
type GalleryBroadcaster interface {
    BroadcastSync(ctx context.Context, items []GallerySyncItem) error
    BroadcastUpsert(ctx context.Context, item GalleryUpsertPayload) error
    BroadcastDelete(ctx context.Context, faceID, personID string) error
}
```
并在 `internal/person` 中注入。当：
- `AddFaceWithReader` 成功录入特征：触发 `BroadcastUpsert`；
- `DeleteFace` 成功删除人脸：触发 `BroadcastDelete(faceID, personID)`；
- `DeletePerson` 成功删除人员：触发 `BroadcastDelete("", personID)`；
- `UpdatePerson` 成功修改人员姓名：遍历其人脸触发 `BroadcastUpsert`。

### 5.2 冷启动同步触发时机
- 当新 Runner 启动就绪握手后，或调用 `SyncAllActiveRunners` 时，调用 `person.Service.GetGallerySyncData(ctx)` 并通过 `BroadcastSync` 分片下发。

---

## 6. 验证计划

1. **协议守卫测试**：`internal/algorithm/ipc_contract_test.go` 验证 10/11/12/13 枚举常量。
2. **分片与边界测试**：
   - 构造 50 人底库（>64KB），验证分片生成、逐片分发、Runner 聚合重组正确性；
   - 验证异常丢片或超时时正确抛错并保持旧底库完整。
3. **增量热更测试**：
   - 启动测试 Runner 监听底库；
   - 发送 `UPSERT` 验证索引新增；
   - 发送 `DELETE` 验证索引移除；
   - 验证既有视频帧 `MSG_FRAME_DISPATCH` 绝不发生交织破坏或丢包。
4. **门禁验证**：
   - `make native-test` 全部通过；
   - `make go-check` 全部通过。
