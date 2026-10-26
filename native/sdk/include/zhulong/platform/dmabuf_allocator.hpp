/**
 * @file dmabuf_allocator.hpp
 * @brief Linux DMA-BUF 物理连续/散布内存分配器与 CPU 缓存一致性同步 (RAII)
 *
 * 核心设计原则：
 * 1. 硬件级零拷贝基石：首选 /dev/dma_heap 目录中的物理连续内存堆 (CMA)，次选系统散布堆 (system/sys/uncached)，
 *    备选 DRM Dumb Buffer (/dev/dri/card0 二维化步长映射)。
 * 2. CPU Cache 同步保护：成对的 DMA_BUF_IOCTL_SYNC 调用（DMA_BUF_SYNC_START 与 DMA_BUF_SYNC_END），
 *    自动处理 -EINTR 与带退避重试的 -EAGAIN，杜绝紧凑忙轮询死循环。
 * 3. 完善的 RAII 语义：DmaBuffer 与 ScopedCpuSync 拥有独立且安全的资源描述符生命周期，杜绝 UAF。
 * 4. 优雅降级与跨平台：非 Linux 或无可用硬件 heap 时，is_supported() 为 false，
 *    allocate() 返回 nullptr，上层透明回退至常规堆/SHM 内存。
 */

#pragma once

#include <cstdint>
#include <cstddef>
#include <memory>
#include <string>
#include <mutex>

namespace zhulong::platform {

/**
 * @brief DMA-BUF CPU 访问读写标志（对齐 Linux <linux/dma-buf.h>）
 */
constexpr uint64_t DMA_SYNC_READ  = (1ULL << 0);
constexpr uint64_t DMA_SYNC_WRITE = (2ULL << 0);
constexpr uint64_t DMA_SYNC_RW    = (DMA_SYNC_READ | DMA_SYNC_WRITE);

/**
 * @brief 底层硬件分配器类型枚举
 */
enum class DmaAllocatorType {
    NONE = 0,     /**< 未检测到可用 DMA 分配器（透明回退 CPU/SHM） */
    DMA_HEAP_CMA, /**< Linux /dev/dma_heap/cma 等连续物理内存堆（最高性能，满足 VPU/NPU 硬性约束） */
    DMA_HEAP_SYS, /**< Linux /dev/dma_heap/system 等 IOMMU 散布内存堆 */
    DRM_DUMB      /**< DRM Dumb Buffer PRIME FD (/dev/dri/card0 二维步长安全映射) */
};

/**
 * @brief 单个 DMA-BUF 缓冲区的 RAII 封装
 *
 * 管理 dma_buf fd、mmap 虚拟地址以及 CPU Cache 同步生命周期。
 * 拥有独立的 DRM 描述符生命周期，杜绝跨单元生命周期悬空。
 */
class DmaBuffer {
public:
    DmaBuffer(int fd, size_t size, void* mmap_addr, int drm_fd = -1, uint32_t dumb_handle = 0, size_t mmap_size = 0);
    ~DmaBuffer();

    // 禁用拷贝，允许移动
    DmaBuffer(const DmaBuffer&) = delete;
    DmaBuffer& operator=(const DmaBuffer&) = delete;
    DmaBuffer(DmaBuffer&& other) noexcept;
    DmaBuffer& operator=(DmaBuffer&& other) noexcept;

    /** @brief 获取 DMA-BUF 文件描述符 */
    [[nodiscard]] int fd() const noexcept { return fd_; }

    /** @brief 获取请求的缓冲区字节大小 */
    [[nodiscard]] size_t size() const noexcept { return size_; }

    /** @brief 获取底层真实映射的字节大小（向上取整至页面或 pitch * height） */
    [[nodiscard]] size_t mmap_size() const noexcept { return mmap_size_; }

    /** @brief 获取 CPU 可访问的虚拟地址映射指针 */
    [[nodiscard]] void* mmap_addr() noexcept { return mmap_addr_; }
    [[nodiscard]] const void* mmap_addr() const noexcept { return mmap_addr_; }

    /**
     * @brief 开始 CPU 访问（刷新/失效 CPU Cache）
     * @param flags DMA_SYNC_READ, DMA_SYNC_WRITE 或 DMA_SYNC_RW
     * @return true 同步成功；false 失败
     */
    bool sync_start(uint64_t flags = DMA_SYNC_RW);

    /**
     * @brief 结束 CPU 访问（写回/同步 CPU Cache）
     * @param flags 与 sync_start 使用的标志严格对称
     * @return true 同步成功；false 失败
     */
    bool sync_end(uint64_t flags = DMA_SYNC_RW);

private:
    int fd_{-1};
    size_t size_{0};
    size_t mmap_size_{0};
    void* mmap_addr_{nullptr};
    int drm_fd_{-1};
    uint32_t dumb_handle_{0};
};

/**
 * @brief RAII CPU 缓存同步作用域守卫
 *
 * 在进入作用域时自动调用 sync_start()，离开作用域（包括异常与提前 return）时
 * 自动调用 sync_end()，杜绝 Cache 脏行残留导致的硬件抽帧或花屏。
 */
class ScopedCpuSync {
public:
    explicit ScopedCpuSync(DmaBuffer& buffer, uint64_t flags = DMA_SYNC_RW)
        : buffer_(&buffer), flags_(flags), active_(buffer_->sync_start(flags)) {}

    ~ScopedCpuSync() {
        if (active_ && buffer_ != nullptr) {
            buffer_->sync_end(flags_);
            active_ = false;
        }
    }

    // 禁用拷贝，允许移动
    ScopedCpuSync(const ScopedCpuSync&) = delete;
    ScopedCpuSync& operator=(const ScopedCpuSync&) = delete;

    ScopedCpuSync(ScopedCpuSync&& other) noexcept
        : buffer_(other.buffer_), flags_(other.flags_), active_(other.active_) {
        other.buffer_ = nullptr;
        other.active_ = false;
    }

    ScopedCpuSync& operator=(ScopedCpuSync&& other) noexcept {
        if (this != &other) {
            if (active_ && buffer_ != nullptr) {
                buffer_->sync_end(flags_);
            }
            buffer_ = other.buffer_;
            flags_ = other.flags_;
            active_ = other.active_;
            other.buffer_ = nullptr;
            other.active_ = false;
        }
        return *this;
    }

    /** @brief 检查 CPU 同步是否成功上锁/失效 Cache */
    [[nodiscard]] bool is_locked() const noexcept { return active_; }

private:
    DmaBuffer* buffer_{nullptr};
    uint64_t flags_{DMA_SYNC_RW};
    bool active_{false};
};

/**
 * @brief DMA-BUF 物理分配器单例
 */
class DmaBufAllocator {
public:
    static DmaBufAllocator& instance();

    /**
     * @brief 分配物理 DMA-BUF 缓冲区
     * @param size 请求字节大小
     * @return 成功返回 DmaBuffer RAII 智能指针；若平台不支持或分配失败返回 nullptr
     */
    std::unique_ptr<DmaBuffer> allocate(size_t size);

    /** @brief 当前平台与环境是否支持硬件 DMA-BUF 分配 */
    [[nodiscard]] bool is_supported() const noexcept { return type_ != DmaAllocatorType::NONE; }

    /** @brief 获取当前探测命中的分配器类型 */
    [[nodiscard]] DmaAllocatorType type() const noexcept { return type_; }

    /** @brief 获取分配器类型可读名称 */
    [[nodiscard]] const char* type_name() const noexcept;

    /** @brief 获取底层绑定的设备路径（如 /dev/dma_heap/cma 或 /dev/dri/card0） */
    [[nodiscard]] const std::string& device_path() const noexcept { return device_path_; }

    /** @brief 获取最近一次错误描述信息（线程安全） */
    [[nodiscard]] std::string last_error() const;

private:
    DmaBufAllocator();
    ~DmaBufAllocator();

    void probe();
    void set_error(std::string err);

    DmaAllocatorType type_{DmaAllocatorType::NONE};
    std::string device_path_;
    int heap_fd_{-1};
    int drm_fd_{-1};

    mutable std::mutex error_mutex_;
    std::string last_error_;
};

} // namespace zhulong::platform
