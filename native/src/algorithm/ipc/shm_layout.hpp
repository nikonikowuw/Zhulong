#pragma once

#include <cstdint>
#include <cstddef>

namespace zhulong::ipc {

constexpr uint32_t SHM_MAGIC = 0x5A485348; // 'ZHSH'
constexpr uint32_t SHM_VERSION = 1;
constexpr size_t   DEFAULT_SLOT_COUNT = 8;
constexpr size_t   DEFAULT_SLOT_SIZE  = 4 * 1024 * 1024; // 4MB/槽位 (满足 1080P NV12/RGB/YUV420P)

#pragma pack(push, 1)

struct ShmHeader {
    uint32_t magic{SHM_MAGIC};
    uint32_t version{SHM_VERSION};
    uint32_t slot_count{0};
    uint32_t slot_size{0};
    uint64_t total_size{0};
    uint8_t  reserved[40]{0};
};

#pragma pack(pop)

static_assert(sizeof(ShmHeader) == 64, "ShmHeader must be 64 bytes");

} // namespace zhulong::ipc
