/**
 * @file dmabuf_sync.hpp
 * @brief DMA-BUF CPU 访问所需的 UAPI 兜底定义与裸 fd 同步 RAII（仓库内单一来源）
 *
 * 为什么需要这个头：
 * 1. `<linux/dma-buf.h>` 在部分交叉 sysroot 里缺失，但 `DMA_BUF_IOCTL_SYNC` 的 ioctl 编码
 *    必须与内核 ABI 一一对应，不能省略也不能自己发明；
 * 2. 该兜底块此前在 `algo_main.cpp`、`dmabuf_allocator.cpp`、`snapshot.cpp` 各抄了一份，
 *    新增消费者（运动检测器 / 窗口物化）不该再抄。
 *
 * 使用纪律：
 * - 只有「按裸 fd 做一次 CPU 访问」的场景才用它；持有完整 RAII 的调用方请直接用
 *   `<zhulong/platform/dmabuf_allocator.hpp>` 的 `DmaBuffer` / `ScopedCpuSync`。
 * - `START` 与 `END` 必须成对，且覆盖整个 CPU 访问窗口
 *   （见 .trellis/spec/native/algo-plugin-contract.md · CPU Cache 同步守则）。
 */

#pragma once

#include <cstdint>

#if defined(__linux__)
#include <linux/ioctl.h>
#include <sys/ioctl.h>

#if defined(__has_include)
#if __has_include(<linux/dma-buf.h>)
#include <linux/dma-buf.h>
#define ZHULONG_HAVE_DMA_BUF_H 1
#endif
#endif

#ifndef ZHULONG_HAVE_DMA_BUF_H
// 数值来自 Linux UAPI（include/uapi/linux/dma-buf.h），故意硬编码。
struct dma_buf_sync {
    uint64_t flags;
};
#ifndef DMA_BUF_SYNC_READ
#define DMA_BUF_SYNC_READ (1 << 0)
#endif
#ifndef DMA_BUF_SYNC_WRITE
#define DMA_BUF_SYNC_WRITE (2 << 0)
#endif
#ifndef DMA_BUF_SYNC_RW
#define DMA_BUF_SYNC_RW (DMA_BUF_SYNC_READ | DMA_BUF_SYNC_WRITE)
#endif
#ifndef DMA_BUF_SYNC_START
#define DMA_BUF_SYNC_START (0 << 2)
#endif
#ifndef DMA_BUF_SYNC_END
#define DMA_BUF_SYNC_END (1 << 2)
#endif
#ifndef DMA_BUF_BASE
#define DMA_BUF_BASE 'b'
#endif
#ifndef DMA_BUF_IOCTL_SYNC
#define DMA_BUF_IOCTL_SYNC _IOW(DMA_BUF_BASE, 0, struct dma_buf_sync)
#endif
#endif // ZHULONG_HAVE_DMA_BUF_H
#endif // __linux__

namespace zhulong::platform {

// 与 Linux UAPI 的 DMA_BUF_SYNC_* 数值一致；独立命名以免与 dmabuf_allocator.hpp 的
// DMA_SYNC_READ/WRITE（供 ScopedCpuSync 使用）混淆。
constexpr uint64_t kDmaSyncRead = 1ULL << 0;
constexpr uint64_t kDmaSyncWrite = 2ULL << 0;
constexpr uint64_t kDmaSyncReadWrite = kDmaSyncRead | kDmaSyncWrite;

/**
 * @brief 裸 fd 的 DMA-BUF CPU 访问窗口（START/END 成对，失败不抛）。
 *
 * 与 `dmabuf_allocator.hpp` 的 `ScopedCpuSync` 分工：后者绑定**拥有型** `DmaBuffer&`，
 * 而窗口物化 / 运动检测手里只有借用的 fd（所有权属于 HardwareFrame 或槽位池）。
 * 非 Linux 平台上是空操作，调用方无需条件编译。
 */
class ScopedFdSync {
public:
    ScopedFdSync(int fd, uint64_t flags) noexcept : fd_(fd), flags_(flags) {
#if defined(__linux__)
        if (fd_ < 0) {
            return;
        }
        struct dma_buf_sync sync{};
        sync.flags = DMA_BUF_SYNC_START | flags_;
        active_ = ::ioctl(fd_, DMA_BUF_IOCTL_SYNC, &sync) == 0;
#else
        (void)fd_;
        (void)flags_;
#endif
    }

    ~ScopedFdSync() { reset(); }

    ScopedFdSync(const ScopedFdSync&) = delete;
    ScopedFdSync& operator=(const ScopedFdSync&) = delete;
    ScopedFdSync(ScopedFdSync&&) = delete;
    ScopedFdSync& operator=(ScopedFdSync&&) = delete;

    [[nodiscard]] bool active() const noexcept { return active_; }

private:
    void reset() noexcept {
#if defined(__linux__)
        if (!active_) {
            return;
        }
        struct dma_buf_sync sync{};
        sync.flags = DMA_BUF_SYNC_END | flags_;
        (void)::ioctl(fd_, DMA_BUF_IOCTL_SYNC, &sync);
        active_ = false;
#endif
    }

    int fd_{-1};
    uint64_t flags_{0};
    bool active_{false};
};

} // namespace zhulong::platform
