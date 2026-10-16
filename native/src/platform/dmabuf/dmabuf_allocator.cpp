/**
 * @file dmabuf_allocator.cpp
 * @brief Linux DMA-BUF 物理内存分配器与 CPU 缓存一致性同步实现
 */

#include "dmabuf_allocator.hpp"

#include <unistd.h>
#include <fcntl.h>
#include <sys/mman.h>
#include <cstring>
#include <cerrno>
#include <utility>

#if defined(__linux__)
#include <linux/types.h>
#include <linux/ioctl.h>
#include <sys/ioctl.h>

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

// =============================================================================
// DmaBuffer Implementation
// =============================================================================

DmaBuffer::DmaBuffer(int fd, size_t size, void* mmap_addr, int drm_fd, uint32_t dumb_handle)
    : fd_(fd), size_(size), mmap_addr_(mmap_addr), drm_fd_(drm_fd), dumb_handle_(dumb_handle) {}

DmaBuffer::~DmaBuffer() {
    if (mmap_addr_ && mmap_addr_ != MAP_FAILED) {
        ::munmap(mmap_addr_, size_);
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
#endif
}

DmaBuffer::DmaBuffer(DmaBuffer&& other) noexcept
    : fd_(other.fd_), size_(other.size_), mmap_addr_(other.mmap_addr_),
      drm_fd_(other.drm_fd_), dumb_handle_(other.dumb_handle_) {
    other.fd_ = -1;
    other.size_ = 0;
    other.mmap_addr_ = nullptr;
    other.drm_fd_ = -1;
    other.dumb_handle_ = 0;
}

DmaBuffer& DmaBuffer::operator=(DmaBuffer&& other) noexcept {
    if (this != &other) {
        if (mmap_addr_ && mmap_addr_ != MAP_FAILED) {
            ::munmap(mmap_addr_, size_);
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
#endif
        fd_ = other.fd_;
        size_ = other.size_;
        mmap_addr_ = other.mmap_addr_;
        drm_fd_ = other.drm_fd_;
        dumb_handle_ = other.dumb_handle_;

        other.fd_ = -1;
        other.size_ = 0;
        other.mmap_addr_ = nullptr;
        other.drm_fd_ = -1;
        other.dumb_handle_ = 0;
    }
    return *this;
}

bool DmaBuffer::sync_start(uint64_t flags) {
#if defined(__linux__)
    if (fd_ < 0) return false;
    struct dma_buf_sync sync{};
    sync.flags = DMA_BUF_SYNC_START | (flags & DMA_BUF_SYNC_RW);
    int ret = 0;
    do {
        ret = ioctl(fd_, DMA_BUF_IOCTL_SYNC, &sync);
    } while (ret == -1 && (errno == EINTR || errno == EAGAIN));
    return (ret == 0);
#else
    (void)flags;
    return true;
#endif
}

bool DmaBuffer::sync_end(uint64_t flags) {
#if defined(__linux__)
    if (fd_ < 0) return false;
    struct dma_buf_sync sync{};
    sync.flags = DMA_BUF_SYNC_END | (flags & DMA_BUF_SYNC_RW);
    int ret = 0;
    do {
        ret = ioctl(fd_, DMA_BUF_IOCTL_SYNC, &sync);
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

void DmaBufAllocator::probe() {
    type_ = DmaAllocatorType::NONE;
#if defined(__linux__)
    // 1. 首选连续物理内存堆 CMA
    heap_fd_ = ::open("/dev/dma_heap/cma", O_RDWR | O_CLOEXEC);
    if (heap_fd_ >= 0) {
        type_ = DmaAllocatorType::DMA_HEAP_CMA;
        device_path_ = "/dev/dma_heap/cma";
        return;
    }

    // 2. 次选散布内存堆 system
    heap_fd_ = ::open("/dev/dma_heap/system", O_RDWR | O_CLOEXEC);
    if (heap_fd_ >= 0) {
        type_ = DmaAllocatorType::DMA_HEAP_SYS;
        device_path_ = "/dev/dma_heap/system";
        return;
    }

    // 3. 备选 DRM Dumb Buffer PRIME FD (⚠️ KMS Dumb Buffer 属于显示管理 ioctl，仅允许在主节点 card* 创建)
    drm_fd_ = ::open("/dev/dri/card0", O_RDWR | O_CLOEXEC);
    if (drm_fd_ >= 0) {
        // 动态探针校验：真实测试驱动是否支持 CREATE_DUMB，防止不可用设备假阳性
        struct drm_mode_create_dumb test_req{};
        test_req.width = 64;
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
            return nullptr;
        }

        int dma_fd = static_cast<int>(alloc_data.fd);
        void* ptr = ::mmap(nullptr, size, PROT_READ | PROT_WRITE, MAP_SHARED, dma_fd, 0);
        if (ptr == MAP_FAILED) {
            ::close(dma_fd);
            return nullptr;
        }

        return std::make_unique<DmaBuffer>(dma_fd, size, ptr);
    }

    if (type_ == DmaAllocatorType::DRM_DUMB) {
        struct drm_mode_create_dumb create_req{};
        create_req.width = static_cast<__u32>(size);
        create_req.height = 1;
        create_req.bpp = 8;

        if (ioctl(drm_fd_, DRM_IOCTL_MODE_CREATE_DUMB, &create_req) < 0) {
            return nullptr;
        }

        struct drm_prime_handle prime_req{};
        prime_req.handle = create_req.handle;
        prime_req.flags = DRM_CLOEXEC | DRM_RDWR;
        prime_req.fd = -1;

        if (ioctl(drm_fd_, DRM_IOCTL_PRIME_HANDLE_TO_FD, &prime_req) < 0) {
            struct drm_mode_destroy_dumb destroy_req{};
            destroy_req.handle = create_req.handle;
            ioctl(drm_fd_, DRM_IOCTL_MODE_DESTROY_DUMB, &destroy_req);
            return nullptr;
        }

        void* ptr = ::mmap(nullptr, size, PROT_READ | PROT_WRITE, MAP_SHARED, prime_req.fd, 0);
        if (ptr == MAP_FAILED) {
            ::close(prime_req.fd);
            struct drm_mode_destroy_dumb destroy_req{};
            destroy_req.handle = create_req.handle;
            ioctl(drm_fd_, DRM_IOCTL_MODE_DESTROY_DUMB, &destroy_req);
            return nullptr;
        }

        return std::make_unique<DmaBuffer>(prime_req.fd, size, ptr, drm_fd_, create_req.handle);
    }
#endif

    return nullptr;
}

} // namespace zhulong::platform
