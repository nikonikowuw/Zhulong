/**
 * @file shm_layout.hpp
 * @brief 进程间共享内存（SHM）固定头部与槽位布局契约
 *
 * 核心设计原则：
 * 1. 物理安全隔离：宿主进程作为生产者独占以读写权限创建共享内存，各独立算法 Runner 子进程以只读权限（PROT_READ）映射，杜绝算法异常篡改原始画面。
 * 2. 紧密单字节对齐（#pragma pack(push, 1)）：ShmHeader 严格固定为 64 字节，通过 static_assert 静态断言确保跨编译器与架构 ABI 严格一致。
 * 3. 槽位预分配拓扑：共享内存由 64 字节头部 + N 个固定大小（默认 8 槽位 × 4MB = 32MB/路）连续槽位组成。
 */

#pragma once

#include <cstdint>
#include <cstddef>

namespace zhulong::ipc {

constexpr uint32_t SHM_MAGIC = 0x5A485348; /**< 共享内存段魔数：ASCII 'ZHSH' (0x5A485348) */
constexpr uint32_t SHM_VERSION = 1;        /**< 共享内存协议主版本号 */
constexpr size_t   DEFAULT_SLOT_COUNT = 8; /**< 默认共享内存槽位总数（满足 8 帧并发与缓冲池循环） */
constexpr size_t   DEFAULT_SLOT_SIZE  = 4 * 1024 * 1024; /**< 默认单个槽位字节容量（4MB，满足 1080P NV12/RGB/YUV420P 及 4K 压缩格式） */

#pragma pack(push, 1)

/**
 * @brief 共享内存段起始处的固定 64 字节元数据头部
 *
 * 映射于共享内存基地址起始处（偏移 0），描述槽位数量、单个槽位容量及总内存尺寸。
 * 各子进程只读打开共享内存后首先校验 magic 与 version，随后按 slot_count 与 slot_size 计算槽位偏移。
 */
struct ShmHeader {
    uint32_t magic{SHM_MAGIC};      /**< 校验魔数，必须恒等于 SHM_MAGIC */
    uint32_t version{SHM_VERSION};  /**< 协议版本号，必须恒等于 SHM_VERSION */
    uint32_t slot_count{0};         /**< 槽位总数（默认 8） */
    uint32_t slot_size{0};          /**< 单个槽位字节数（默认 4MB = 4194304 字节） */
    uint64_t total_size{0};         /**< 共享内存文件总映射字节数（sizeof(ShmHeader) + slot_count * slot_size） */
    uint8_t  reserved[40]{0};       /**< 预留填充字段（全零），保证头部精确占据 64 字节 */
};

#pragma pack(pop)

static_assert(sizeof(ShmHeader) == 64, "ShmHeader must be 64 bytes");

} // namespace zhulong::ipc

