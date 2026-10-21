/**
 * @file dmabuf_allocator.cpp
 * @brief Linux DMA-BUF 物理内存分配器与 CPU 缓存一致性同步实现
 */

#include "dmabuf_allocator.hpp"

#if defined(ZHULONG_IPC_LOGGING)
#include "common/log.hpp"
#include <atomic>
#endif

#include <unistd.h>
#include <fcntl.h>
#include <sys/mman.h>
#include <cstring>
#include <cerrno>
#include <utility>
#include <thread>
#include <chrono>
#include <vector>
#include <algorithm>
#include <cctype>

#if defined(__linux__)
#include <linux/types.h>
#include <linux/ioctl.h>
#include <sys/ioctl.h>
#include <dirent.h>

#ifndef DMA_HEAP_IOC_MAGIC
#define DMA_HEAP_IOC_MAGIC 'H'
struct dma_heap_allocation_data {
    __u64 len;
    __u32 fd;
    __u32 fd_flags;
    __u64 heap_flags;
};
#define DMA_HEAP_IOCTL_ALLOC _IOWR(DMA_HEAP_IOC_MAGIC, 0x0, struct dma_heap_allocation_data)
#endif

#ifndef DMA_BUF_SYNC_READ
struct dma_buf_sync {
    __u64 flags;
};
#define DMA_BUF_SYNC_READ (1 << 0)
#define DMA_BUF_SYNC_WRITE (2 << 0)
#define DMA_BUF_SYNC_RW (DMA_BUF_SYNC_READ | DMA_BUF_SYNC_WRITE)
#define DMA_BUF_SYNC_START (0 << 2)
#define DMA_BUF_SYNC_END (1 << 2)
#define DMA_BUF_BASE 'b'
#define DMA_BUF_IOCTL_SYNC _IOW(DMA_BUF_BASE, 0, struct dma_buf_sync)
#endif

#ifndef DRM_IOCTL_MODE_CREATE_DUMB
struct drm_mode_create_dumb {
    __u32 height;
    __u32 width;
    __u32 bpp;
    __u32 flags;
    __u32 handle;
    __u32 pitch;
    __u64 size;
};
struct drm_mode_destroy_dumb {
    __u32 handle;
};
struct drm_prime_handle {
    __u32 handle;
    __u32 flags;
    __s32 fd;
};
#define DRM_IOCTL_BASE 'd'
#define DRM_IOCTL_MODE_CREATE_DUMB _IOWR(DRM_IOCTL_BASE, 0xB2, struct drm_mode_create_dumb)
#define DRM_IOCTL_MODE_DESTROY_DUMB _IOWR(DRM_IOCTL_BASE, 0xB4, struct drm_mode_destroy_dumb)
#define DRM_IOCTL_PRIME_HANDLE_TO_FD _IOWR(DRM_IOCTL_BASE, 0x2D, struct drm_prime_handle)
#ifndef DRM_CLOEXEC
#define DRM_CLOEXEC O_CLOEXEC
#endif
#ifndef DRM_RDWR
#define DRM_RDWR O_RDWR
#endif
#endif // DRM_IOCTL_MODE_CREATE_DUMB

#endif // __linux__

namespace zhulong::platform {

#if defined(ZHULONG_IPC_LOGGING)
namespace {
std::atomic<bool> allocation_failure_reported{false};

void report_allocation_failure(const char* reason, int error_number, size_t size) noexcept {
    if (allocation_failure_reported.exchange(true, std::memory_order_relaxed)) {
        return;
    }
    ZL_LOGE(0, "platform.dmabuf", "allocation_failed reason=%s errno=%d size_bytes=%zu",
            reason, error_number, size);
}
}
#endif

#if defined(__linux__)
static void scan_dma_heaps(std::vector<std::string>& cma_candidates,
                           std::vector<std::string>& sys_candidates) {
    DIR* dir = ::opendir("/dev/dma_heap");
    if (!dir) {
        return;
    }

    struct dirent* entry = nullptr;
    while ((entry = ::readdir(dir)) != nullptr) {
        if (entry->d_name[0] == '.') {
            continue;
        }
        std::string name = entry->d_name;
        std::string lower_name = name;
        std::transform(lower_name.begin(), lower_name.end(), lower_name.begin(),
                       [](unsigned char c) { return static_cast<char>(std::tolower(c)); });

        std::string full_path = std::string("/dev/dma_heap/") + name;
        if (lower_name.find("cma") != std::string::npos) {
            if (lower_name == "cma") {
                cma_candidates.insert(cma_candidates.begin(), full_path);
            } else {
                cma_candidates.push_back(full_path);
            }
        } else if (lower_name.find("system") != std::string::npos ||
                   lower_name.find("sys") != std::string::npos ||
                   lower_name.find("uncached") != std::string::npos) {
            if (lower_name == "system") {
                sys_candidates.insert(sys_candidates.begin(), full_path);
            } else {
                sys_candidates.push_back(full_path);
            }
        } else {
            sys_candidates.push_back(full_path);
        }
    }
    ::closedir(dir);
}
#endif

// =============================================================================
// DmaBuffer Implementation
// =============================================================================

DmaBuffer::DmaBuffer(int fd, size_t size, void* mmap_addr, int drm_fd, uint32_t dumb_handle, size_t mmap_size)
    : fd_(fd), size_(size), mmap_size_(mmap_size > 0 ? mmap_size : size),
      mmap_addr_(mmap_addr), dumb_handle_(dumb_handle) {
#if defined(__linux__)
    if (drm_fd >= 0) {
        drm_fd_ = ::dup(drm_fd);
    } else {
        drm_fd_ = -1;
    }
#else
    (void)drm_fd;
    drm_fd_ = -1;
#endif
}

DmaBuffer::~DmaBuffer() {
    if (mmap_addr_ && mmap_addr_ != MAP_FAILED) {
        ::munmap(mmap_addr_, mmap_size_);
        mmap_addr_ = nullptr;
    }
    if (fd_ >= 0) {
        ::close(fd_);
        fd_ = -1;
    }
#if defined(__linux__)
    if (drm_fd_ >= 0 && dumb_handle_ > 0) {
        struct drm_mode_destroy_dumb destroy_req{};
        destroy_req.handle = dumb_handle_;
        ioctl(drm_fd_, DRM_IOCTL_MODE_DESTROY_DUMB, &destroy_req);
        dumb_handle_ = 0;
    }
    if (drm_fd_ >= 0) {
        ::close(drm_fd_);
        drm_fd_ = -1;
    }
#endif
}

DmaBuffer::DmaBuffer(DmaBuffer&& other) noexcept
    : fd_(other.fd_), size_(other.size_), mmap_size_(other.mmap_size_),
      mmap_addr_(other.mmap_addr_), drm_fd_(other.drm_fd_), dumb_handle_(other.dumb_handle_) {
    other.fd_ = -1;
    other.size_ = 0;
    other.mmap_size_ = 0;
    other.mmap_addr_ = nullptr;
    other.drm_fd_ = -1;
    other.dumb_handle_ = 0;
}

DmaBuffer& DmaBuffer::operator=(DmaBuffer&& other) noexcept {
    if (this != &other) {
        if (mmap_addr_ && mmap_addr_ != MAP_FAILED) {
            ::munmap(mmap_addr_, mmap_size_);
        }
        if (fd_ >= 0) {
            ::close(fd_);
        }
#if defined(__linux__)
        if (drm_fd_ >= 0 && dumb_handle_ > 0) {
            struct drm_mode_destroy_dumb destroy_req{};
            destroy_req.handle = dumb_handle_;
            ioctl(drm_fd_, DRM_IOCTL_MODE_DESTROY_DUMB, &destroy_req);
        }
        if (drm_fd_ >= 0) {
            ::close(drm_fd_);
        }
#endif
        fd_ = other.fd_;
        size_ = other.size_;
        mmap_size_ = other.mmap_size_;
        mmap_addr_ = other.mmap_addr_;
        drm_fd_ = other.drm_fd_;
        dumb_handle_ = other.dumb_handle_;

        other.fd_ = -1;
        other.size_ = 0;
        other.mmap_size_ = 0;
        other.mmap_addr_ = nullptr;
        other.drm_fd_ = -1;
        other.dumb_handle_ = 0;
    }
    return *this;
}

bool DmaBuffer::sync_start(uint64_t flags) {
    // 空对象必须先拒绝：没有 fd 就没有可同步的缓冲。这个检查必须留在条件编译
    // 之外 —— 非 Linux 平台没有 ioctl 可用，但「不能对自己不拥有的 fd 报告成功」
    // 与平台无关。迁移到 sync_end 同理。
    if (fd_ < 0) return false;
#if defined(__linux__)
    struct dma_buf_sync sync{};
    sync.flags = DMA_BUF_SYNC_START | (flags & DMA_BUF_SYNC_RW);
    int ret = 0;
    int retries = 0;
    constexpr int MAX_RETRIES = 50;
    constexpr auto BACKOFF_TIME = std::chrono::microseconds(100);

    do {
        ret = ioctl(fd_, DMA_BUF_IOCTL_SYNC, &sync);
        if (ret == -1 && errno == EAGAIN) {
            if (++retries > MAX_RETRIES) {
                return false;
            }
            std::this_thread::sleep_for(BACKOFF_TIME);
        }
    } while (ret == -1 && (errno == EINTR || errno == EAGAIN));
    return (ret == 0);
#else
    (void)flags;
    return true;
#endif
}

bool DmaBuffer::sync_end(uint64_t flags) {
    if (fd_ < 0) return false;
#if defined(__linux__)
    struct dma_buf_sync sync{};
    sync.flags = DMA_BUF_SYNC_END | (flags & DMA_BUF_SYNC_RW);
    int ret = 0;
    int retries = 0;
    constexpr int MAX_RETRIES = 50;
    constexpr auto BACKOFF_TIME = std::chrono::microseconds(100);

    do {
        ret = ioctl(fd_, DMA_BUF_IOCTL_SYNC, &sync);
        if (ret == -1 && errno == EAGAIN) {
            if (++retries > MAX_RETRIES) {
                return false;
            }
            std::this_thread::sleep_for(BACKOFF_TIME);
        }
    } while (ret == -1 && (errno == EINTR || errno == EAGAIN));
    return (ret == 0);
#else
    (void)flags;
    return true;
#endif
}

// =============================================================================
// DmaBufAllocator Implementation
// =============================================================================

DmaBufAllocator& DmaBufAllocator::instance() {
    static DmaBufAllocator inst;
    return inst;
}

DmaBufAllocator::DmaBufAllocator() {
    probe();
}

DmaBufAllocator::~DmaBufAllocator() {
    if (heap_fd_ >= 0) {
        ::close(heap_fd_);
        heap_fd_ = -1;
    }
    if (drm_fd_ >= 0) {
        ::close(drm_fd_);
        drm_fd_ = -1;
    }
}

void DmaBufAllocator::set_error(std::string err) {
    std::lock_guard<std::mutex> lock(error_mutex_);
    last_error_ = std::move(err);
}

std::string DmaBufAllocator::last_error() const {
    std::lock_guard<std::mutex> lock(error_mutex_);
    return last_error_;
}

void DmaBufAllocator::probe() {
    type_ = DmaAllocatorType::NONE;
    device_path_.clear();

#if defined(__linux__)
    std::vector<std::string> cma_candidates;
    std::vector<std::string> sys_candidates;
    scan_dma_heaps(cma_candidates, sys_candidates);

    // 1. 优先尝试连续物理内存堆 CMA（满足 VPU/NPU 硬性约束）
    for (const auto& path : cma_candidates) {
        heap_fd_ = ::open(path.c_str(), O_RDWR | O_CLOEXEC);
        if (heap_fd_ >= 0) {
            type_ = DmaAllocatorType::DMA_HEAP_CMA;
            device_path_ = path;
            return;
        }
    }

    // 2. 次选系统散布内存堆 system
    for (const auto& path : sys_candidates) {
        heap_fd_ = ::open(path.c_str(), O_RDWR | O_CLOEXEC);
        if (heap_fd_ >= 0) {
            type_ = DmaAllocatorType::DMA_HEAP_SYS;
            device_path_ = path;
            return;
        }
    }

    // 3. 备选 DRM Dumb Buffer PRIME FD（使用 2D 几何步长真实探测）
    drm_fd_ = ::open("/dev/dri/card0", O_RDWR | O_CLOEXEC);
    if (drm_fd_ >= 0) {
        // 动态探针校验：使用标准二维步长（4096 字节宽，1 行高）测试驱动真实支持情况
        struct drm_mode_create_dumb test_req{};
        test_req.width = 4096;
        test_req.height = 1;
        test_req.bpp = 8;
        if (ioctl(drm_fd_, DRM_IOCTL_MODE_CREATE_DUMB, &test_req) == 0) {
            struct drm_mode_destroy_dumb destroy_req{};
            destroy_req.handle = test_req.handle;
            ioctl(drm_fd_, DRM_IOCTL_MODE_DESTROY_DUMB, &destroy_req);
            type_ = DmaAllocatorType::DRM_DUMB;
            device_path_ = "/dev/dri/card0";
            return;
        }
        ::close(drm_fd_);
        drm_fd_ = -1;
    }
#endif
}

const char* DmaBufAllocator::type_name() const noexcept {
    switch (type_) {
        case DmaAllocatorType::DMA_HEAP_CMA: return "dma_heap/cma";
        case DmaAllocatorType::DMA_HEAP_SYS: return "dma_heap/system";
        case DmaAllocatorType::DRM_DUMB:     return "drm_dumb";
        case DmaAllocatorType::NONE:
        default:                             return "none";
    }
}

std::unique_ptr<DmaBuffer> DmaBufAllocator::allocate(size_t size) {
    if (size == 0 || !is_supported()) {
        return nullptr;
    }

#if defined(__linux__)
    if (type_ == DmaAllocatorType::DMA_HEAP_CMA || type_ == DmaAllocatorType::DMA_HEAP_SYS) {
        struct dma_heap_allocation_data alloc_data{};
        alloc_data.len = size;
        alloc_data.fd_flags = O_CLOEXEC | O_RDWR;
        alloc_data.heap_flags = 0;

        if (ioctl(heap_fd_, DMA_HEAP_IOCTL_ALLOC, &alloc_data) < 0) {
            int err = errno;
#if defined(ZHULONG_IPC_LOGGING)
            report_allocation_failure("dma_heap_ioctl", err, size);
#endif
            if (err == ENOMEM) {
                set_error("DMA_HEAP_IOCTL_ALLOC out of memory (CMA/system pool exhausted)");
            } else if (err == EMFILE || err == ENFILE) {
                set_error("DMA_HEAP_IOCTL_ALLOC fd table limit reached");
            } else {
                set_error("DMA_HEAP_IOCTL_ALLOC failed with errno " + std::to_string(err));
            }
            return nullptr;
        }

        int dma_fd = static_cast<int>(alloc_data.fd);
        void* ptr = ::mmap(nullptr, size, PROT_READ | PROT_WRITE, MAP_SHARED, dma_fd, 0);
        if (ptr == MAP_FAILED) {
            int err = errno;
#if defined(ZHULONG_IPC_LOGGING)
            report_allocation_failure("dma_heap_mmap", err, size);
#endif
            set_error("mmap dma_buf fd failed: " + std::string(std::strerror(err)));
            ::close(dma_fd);
            return nullptr;
        }

#if defined(ZHULONG_IPC_LOGGING)
        allocation_failure_reported.store(false, std::memory_order_relaxed);
#endif
        return std::make_unique<DmaBuffer>(dma_fd, size, ptr);
    }

    if (type_ == DmaAllocatorType::DRM_DUMB) {
        // 2D 尺寸二维化计算：固定 4096 步长宽度（对齐 4KB 页面与 max_width 约束），折算高度
        constexpr __u32 DUMB_STRIDE_WIDTH = 4096;
        __u32 calculated_height = static_cast<__u32>((size + DUMB_STRIDE_WIDTH - 1) / DUMB_STRIDE_WIDTH);
        if (calculated_height == 0) {
            calculated_height = 1;
        }

        struct drm_mode_create_dumb create_req{};
        create_req.width = DUMB_STRIDE_WIDTH;
        create_req.height = calculated_height;
        create_req.bpp = 8;

        if (ioctl(drm_fd_, DRM_IOCTL_MODE_CREATE_DUMB, &create_req) < 0) {
            int err = errno;
#if defined(ZHULONG_IPC_LOGGING)
            report_allocation_failure("drm_create_dumb", err, size);
#endif
            set_error("DRM_IOCTL_MODE_CREATE_DUMB failed with errno " + std::to_string(err));
            return nullptr;
        }

        struct drm_prime_handle prime_req{};
        prime_req.handle = create_req.handle;
        prime_req.flags = DRM_CLOEXEC | DRM_RDWR;
        prime_req.fd = -1;

        if (ioctl(drm_fd_, DRM_IOCTL_PRIME_HANDLE_TO_FD, &prime_req) < 0) {
            int err = errno;
#if defined(ZHULONG_IPC_LOGGING)
            report_allocation_failure("drm_prime_handle_to_fd", err, size);
#endif
            set_error("DRM_IOCTL_PRIME_HANDLE_TO_FD failed with errno " + std::to_string(err));
            struct drm_mode_destroy_dumb destroy_req{};
            destroy_req.handle = create_req.handle;
            ioctl(drm_fd_, DRM_IOCTL_MODE_DESTROY_DUMB, &destroy_req);
            return nullptr;
        }

        size_t actual_mmap_size = static_cast<size_t>(create_req.size);
        if (actual_mmap_size < size) {
            actual_mmap_size = size;
        }

        void* ptr = ::mmap(nullptr, actual_mmap_size, PROT_READ | PROT_WRITE, MAP_SHARED, prime_req.fd, 0);
        if (ptr == MAP_FAILED) {
            int err = errno;
#if defined(ZHULONG_IPC_LOGGING)
            report_allocation_failure("drm_mmap", err, size);
#endif
            set_error("mmap DRM dumb PRIME fd failed: " + std::string(std::strerror(err)));
            ::close(prime_req.fd);
            struct drm_mode_destroy_dumb destroy_req{};
            destroy_req.handle = create_req.handle;
            ioctl(drm_fd_, DRM_IOCTL_MODE_DESTROY_DUMB, &destroy_req);
            return nullptr;
        }

#if defined(ZHULONG_IPC_LOGGING)
        allocation_failure_reported.store(false, std::memory_order_relaxed);
#endif
        return std::make_unique<DmaBuffer>(prime_req.fd, size, ptr, drm_fd_, create_req.handle, actual_mmap_size);
    }
#endif

    return nullptr;
}

} // namespace zhulong::platform
