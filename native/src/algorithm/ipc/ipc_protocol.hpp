#pragma once

#include <cstdint>
#include <cstddef>
#include <zhulong/plugin.h>

namespace zhulong::ipc {

constexpr uint32_t IPC_MAGIC = 0x5A484C47; // 'ZHLG'
constexpr uint16_t IPC_VERSION = 1;

enum MessageType : uint16_t {
    MSG_UNKNOWN         = 0,
    MSG_HANDSHAKE_REQ   = 1,
    MSG_HANDSHAKE_RESP  = 2,
    MSG_FRAME_DISPATCH  = 3,
    MSG_FRAME_ACK       = 4,
    MSG_HEARTBEAT       = 5,
    MSG_DISCONNECT      = 6
};

#pragma pack(push, 1)

struct IpcHeader {
    uint32_t magic{IPC_MAGIC};
    uint16_t msg_type{MSG_UNKNOWN};
    uint16_t version{IPC_VERSION};
    uint32_t payload_size{0};
    uint32_t sequence{0};
};

// MSG_HANDSHAKE_REQ (Client -> Broker)
struct HandshakeReq {
    uint32_t client_id{0};       // 0: 由 Broker 分配
    uint32_t api_version{1};
    char     client_name[32]{0};
};

// MSG_HANDSHAKE_RESP (Broker -> Client)
struct HandshakeResp {
    uint32_t client_id{0};       // Broker 分配的唯一客户端 ID
    uint32_t status{0};          // 0: 成功
    char     shm_name[64]{0};    // 关联的共享内存名称
    uint32_t slot_count{0};      // 槽位总数
    uint32_t slot_size{0};       // 单个槽位大小 (字节)
};

// MSG_FRAME_DISPATCH (Broker -> Client)
struct FrameDispatchMsg {
    uint64_t stream_id{0};
    uint64_t frame_id{0};
    int64_t  pts{0};
    uint32_t slot_index{0};
    uint64_t generation{0};
    int32_t  width{0};
    int32_t  height{0};
    int32_t  format{ZHULONG_PIXEL_FORMAT_UNKNOWN};
    int32_t  strides[4]{0};
    uint32_t offsets[4]{0};      // 相对于对应槽位起始地址的偏移
    int32_t  reserved_fd{-1};    // 预留平台 dma_fd (>=0 表示有效)
};

// MSG_FRAME_ACK (Client -> Broker)
struct FrameAckMsg {
    uint64_t stream_id{0};
    uint64_t frame_id{0};
    uint32_t slot_index{0};
    uint64_t generation{0};
    uint32_t client_id{0};
};

// MSG_HEARTBEAT (Bidirectional)
struct HeartbeatMsg {
    uint64_t timestamp_ms{0};
};

#pragma pack(pop)

static_assert(sizeof(IpcHeader) == 16, "IpcHeader must be 16 bytes");
static_assert(sizeof(HandshakeReq) == 40, "HandshakeReq size mismatch");
static_assert(sizeof(HandshakeResp) == 80, "HandshakeResp size mismatch");
static_assert(sizeof(FrameDispatchMsg) == 84, "FrameDispatchMsg size mismatch");
static_assert(sizeof(FrameAckMsg) == 32, "FrameAckMsg size mismatch");

} // namespace zhulong::ipc
