#pragma once

#include <cstdint>
#include <cstddef>
#include <zhulong/plugin.h>

namespace zhulong::ipc {

constexpr uint32_t IPC_MAGIC = 0x5A484C47; // 'ZHLG'
constexpr uint16_t IPC_VERSION = 1;
constexpr uint32_t MAX_IPC_PAYLOAD_SIZE = 64 * 1024; // 64KB 防御上限

enum MessageType : uint16_t {
    MSG_UNKNOWN         = 0,
    MSG_HANDSHAKE_REQ   = 1,
    MSG_HANDSHAKE_RESP  = 2,
    MSG_FRAME_DISPATCH  = 3,
    MSG_FRAME_ACK       = 4,
    MSG_HEARTBEAT       = 5,
    MSG_DISCONNECT      = 6,
    MSG_RESULT          = 7
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
    uint64_t generation{0};      // 前置确保 8 字节自然对齐
    int64_t  pts{0};
    uint32_t slot_index{0};
    int32_t  width{0};
    int32_t  height{0};
    int32_t  format{ZHULONG_PIXEL_FORMAT_UNKNOWN};
    int32_t  strides[4]{0};
    uint32_t offsets[4]{0};      // 相对于对应槽位起始地址的偏移
    int32_t  reserved_fd{-1};    // 预留平台 dma_fd (>=0 表示有效)
    uint32_t reserved_pad{0};    // 填充确保总大小 8 字节整除
};

// MSG_FRAME_ACK (Client -> Broker)
struct FrameAckMsg {
    uint64_t stream_id{0};
    uint64_t frame_id{0};
    uint64_t generation{0};      // 前置确保 8 字节自然对齐
    uint32_t slot_index{0};
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
static_assert(sizeof(FrameDispatchMsg) == 88, "FrameDispatchMsg size mismatch");
static_assert(sizeof(FrameAckMsg) == 32, "FrameAckMsg size mismatch");

static_assert(offsetof(FrameDispatchMsg, stream_id) % 8 == 0, "stream_id unaligned");
static_assert(offsetof(FrameDispatchMsg, frame_id) % 8 == 0, "frame_id unaligned");
static_assert(offsetof(FrameDispatchMsg, generation) % 8 == 0, "generation unaligned");
static_assert(offsetof(FrameDispatchMsg, pts) % 8 == 0, "pts unaligned");

static_assert(offsetof(FrameAckMsg, stream_id) % 8 == 0, "stream_id unaligned");
static_assert(offsetof(FrameAckMsg, frame_id) % 8 == 0, "frame_id unaligned");
static_assert(offsetof(FrameAckMsg, generation) % 8 == 0, "generation unaligned");

} // namespace zhulong::ipc
