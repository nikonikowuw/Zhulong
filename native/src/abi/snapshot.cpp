/**
 * @file snapshot.cpp
 * @brief C ABI 抓拍接口的句柄管理、DMA-BUF 映射与像素拷贝实现
 *
 * 分层职责（⚠️ 本文件不参与 engine.cpp 的控制流，只被其调用）：
 * 1. 把 HardwareFrame 的硬件引用（DMA-BUF / 裸虚拟地址）统一转换成 CPU 可见视图；
 * 2. 用 jpeg_encoder 产出全幅图与各裁剪框的 JPEG；
 * 3. 把所有 JPEG 聚合成一个不透明结果句柄，交由 Zhulong_snapshot_destroy 释放。
 *
 * 内存所有权：
 *   - 结果句柄由 `new` 分配，`Zhulong_snapshot_destroy` 负责 `delete`；
 *   - 每张 JPEG 存放在 std::vector 里，其 data() 借用给 C 结构，随句柄一起释放。
 *     Native 侧从未把其中任何内存的所有权交给调用方。
 */

#include "Zhulong/engine.h"
#include "Zhulong/frame.hpp"
#include "nodes/snapshot/jpeg_encoder.hpp"

#include <algorithm>
#include <array>
#include <cstdlib>
#include <cstring>
#include <memory>
#include <mutex>
#include <new>
#include <vector>

#if defined(__linux__)
#include <cerrno>
#include <sys/ioctl.h>
#include <sys/mman.h>
#include <unistd.h>
#if defined(__has_include)
#if __has_include(<linux/dma-buf.h>)
#include <linux/dma-buf.h>
#define ZHULONG_HAVE_DMA_BUF_H 1
#endif
#endif
// 兜底定义：交叉编译 sysroot 缺少 <linux/dma-buf.h> 时仍需正确的 ioctl 编码。
// 数值来自 Linux UAPI，与内核 ABI 一一对应，故意硬编码而不是自己发明。
#ifndef ZHULONG_HAVE_DMA_BUF_H
struct dma_buf_sync {
    uint64_t flags;
};
#ifndef DMA_BUF_SYNC_READ
#define DMA_BUF_SYNC_READ (1 << 0)
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
#endif
#endif // __linux__

namespace zhulong {

/**
 * @brief 一张抓拍图的完整生命周期载体
 */
struct SnapshotImage {
    int32_t box_index{-1};
    int32_t width{0};
    int32_t height{0};
    std::vector<uint8_t> jpeg;
    Zhulong_snapshot_image view{-1, nullptr, 0, 0, 0};
};

} // namespace zhulong

/**
 * @brief 抓拍结果的不透明句柄定义
 *
 * ⚠️ 这里不使用继承（`struct ... : zhulong::SnapshotResult`）：
 * C 侧只拿到基类指针，而基类没有虚析构，`delete` 派生对象是未定义行为。
 * 直接定义结构体，让 C 句柄与分配对象类型完全一致，`delete result` 才是安全的。
 */
struct Zhulong_snapshot_result_t {
    std::vector<std::unique_ptr<zhulong::SnapshotImage>> images;
};

namespace zhulong {

namespace {

/**
 * @brief DMA-BUF 的 CPU 侧映射 + cache 同步 RAII
 *
 * ⚠️ 为什么必须自己映射：HardwareFrame 只携带 dma_fd，解码器与 RGA 之间是纯硬件
 * 通路，从未建立 CPU 映射。抓拍要拷像素，必须 mmap 且成对做 DMA_BUF_IOCTL_SYNC，
 * 否则读到的可能是 CPU cache 中的陈旧数据。
 *
 * ⚠️ 为什么用 PROT_READ：抓拍只读，向内核声明只读可避免驱动把页面标记为脏。
 */
class MappedDmaBuffer {
public:
    MappedDmaBuffer() = default;
    ~MappedDmaBuffer() {
        if (addr_ && addr_ != MAP_FAILED) {
            if (sync_started_) {
                dma_buf_sync sync{};
                sync.flags = DMA_BUF_SYNC_END | DMA_BUF_SYNC_READ;
                (void)::ioctl(fd_, DMA_BUF_IOCTL_SYNC, &sync);
            }
            ::munmap(addr_, size_);
        }
    }

    MappedDmaBuffer(const MappedDmaBuffer &) = delete;
    MappedDmaBuffer &operator=(const MappedDmaBuffer &) = delete;

    /**
     * @brief 映射并开始 CPU 读同步
     * @param fd DMA-BUF fd（调用方持有，本类不接管所有权）
     * @param size 映射长度（byte）
     * @return true 成功
     */
    bool open(int fd, size_t size) {
        if (fd < 0 || size == 0) return false;
        fd_ = fd;
        size_ = size;
        addr_ = ::mmap(nullptr, size, PROT_READ, MAP_SHARED, fd, 0);
        if (addr_ == MAP_FAILED || addr_ == nullptr) {
            addr_ = nullptr;
            return false;
        }
#if defined(__linux__)
        dma_buf_sync sync{};
        sync.flags = DMA_BUF_SYNC_START | DMA_BUF_SYNC_READ;
        // 驱动不支持 sync ioctl 时不算致命：连续内存（CMA）通常无需显式同步，
        // 而 DRM 堆的 cache 一致性由内核在 mmap 时保证。因此失败不在这里报错。
        sync_started_ = (::ioctl(fd_, DMA_BUF_IOCTL_SYNC, &sync) == 0);
#endif
        return true;
    }

    [[nodiscard]] const uint8_t *data() const { return static_cast<const uint8_t *>(addr_); }

private:
    int fd_{-1};
    void *addr_{nullptr};
    size_t size_{0};
    bool sync_started_{false};
};

/**
 * @brief 从 HardwareFrame 构造 CPU 可见视图，需要时自动映射 DMA-BUF
 *
 * 三种可能来源：
 *   1. 软解（FFmpeg）：host_planes 已有效，直接借用；
 *   2. 硬解（MPP）：只有 dma_fd，需 mmap；NV12 的 UV 平面按 stride × vstride 定位；
 *   3. 未来 V4L2/Jetson：同样走 dma_fd 分支。
 */
class CpuFrameAccessor {
public:
    explicit CpuFrameAccessor(const HardwareFrame &frame) : frame_(frame) {}

    /**
     * @brief 准备 CPU 可读视图
     * @param[out] out 视图
     * @return true 成功；false 格式不支持或映射失败
     */
    bool prepare(CpuFrameView &out) {
        out = CpuFrameView{};
        out.width = frame_.width;
        out.height = frame_.height;
        out.vstride = frame_.vstride;
        out.format = frame_.format;

        if (frame_.format != FRAME_FORMAT_NV12 && frame_.format != FRAME_FORMAT_YUV420P) {
            return false;
        }
        if (frame_.width == 0 || frame_.height == 0) {
            return false;
        }

        // 分支 1：解码器已经给了 CPU 虚拟地址（软解路径）
        if (frame_.host_planes[0] != nullptr) {
            for (int i = 0; i < 4; ++i) {
                out.planes[i] = frame_.host_planes[i];
                out.strides[i] = frame_.plane_strides[i];
            }
            // 软解时 host_planes[0] 即 Y 平面，plane_strides[0] 即 Y stride
            if (out.strides[0] == 0) {
                out.strides[0] = frame_.stride;
            }
            return out.planes[0] != nullptr;
        }

        // 分支 2：只有 DMA-BUF fd（硬解路径）
        if (frame_.dma_fd < 0) {
            return false;
        }
        const uint32_t vstride = frame_.vstride != 0 ? frame_.vstride : frame_.height;
        // NV12 需要的总长度 = stride × vstride × 1.5；YUV420P 同样为 1.5 倍但三平面独立。
        // 取上界做映射，避免因 vstride 小于 height 的极端流导致越界。
        const size_t map_size = static_cast<size_t>(frame_.stride) * vstride * 3 / 2;
        if (map_size == 0) {
            return false;
        }
        if (!mapping_.open(frame_.dma_fd, map_size)) {
            return false;
        }

        const uint8_t *base = mapping_.data();
        out.strides[0] = frame_.stride;
        out.planes[0] = base;
        if (frame_.format == FRAME_FORMAT_NV12) {
            // NV12：UV 交错平面紧跟在 Y 平面之后，偏移 = stride × vstride
            out.planes[1] = base + static_cast<size_t>(frame_.stride) * vstride;
            out.strides[1] = frame_.stride;
        } else {
            // YUV420P：三个平面连续存放，U 与 V 各占 (stride/2) × (vstride/2)
            const size_t chroma_offset = static_cast<size_t>(frame_.stride) * vstride;
            const size_t chroma_stride = frame_.stride / 2;
            out.planes[1] = base + chroma_offset;
            out.strides[1] = static_cast<uint32_t>(chroma_stride);
            out.planes[2] = base + chroma_offset + chroma_stride * (vstride / 2);
            out.strides[2] = static_cast<uint32_t>(chroma_stride);
        }
        return true;
    }

private:
    const HardwareFrame &frame_;
    MappedDmaBuffer mapping_;
};

/**
 * @brief 追加一张编码好的图片到结果集
 * @return false 表示内存分配失败
 */
bool append_image(Zhulong_snapshot_result_t &result, int32_t box_index, int32_t width, int32_t height,
                  std::vector<uint8_t> &&jpeg) {
    auto image = std::make_unique<SnapshotImage>();
    image->box_index = box_index;
    image->width = width;
    image->height = height;
    image->jpeg = std::move(jpeg);
    // ⚠️ view 指向 image->jpeg 的内部缓冲：必须先完成 vector 赋值再取 data()，
    //    且此后不得再对 vector 做任何会重新分配的修改。
    image->view.box_index = box_index;
    image->view.data = image->jpeg.data();
    image->view.size = image->jpeg.size();
    image->view.width = width;
    image->view.height = height;
    result.images.push_back(std::move(image));
    return true;
}

} // namespace

/**
 * @brief 对给定帧执行「全幅 + 各裁剪框」抓拍
 *
 * 本函数不做任何流查找与锁操作，纯粹做像素处理与编码，便于在 engine.cpp 中
 * 于锁外调用（JPEG 编码耗时 35~60ms，绝不能挂在 Engine::control 上）。
 *
 * @param frame 已取到的只读帧引用
 * @param quality 已收敛到 [1,100] 的质量
 * @param boxes 归一化框数组（可为空）
 * @param[out] out 结果句柄；失败时不分配
 * @return Zhulong_status_t
 */
Zhulong_status_t encode_snapshot(const HardwareFrame &frame, int32_t quality,
                                 const std::vector<std::array<float, 4>> &boxes,
                                 Zhulong_snapshot_h *out) {
    // 先探编码器能力：静态 FFmpeg 若未编译 mjpeg，应立刻返回 UNSUPPORTED，
    // 而不是让调用方误以为「这一帧有问题，下次再试」。
    if (!jpeg_encoder_available()) {
        return Zhulong_ERR_UNSUPPORTED;
    }

    CpuFrameView view{};
    CpuFrameAccessor accessor(frame);
    if (!accessor.prepare(view)) {
        return Zhulong_ERR_UNSUPPORTED;
    }

    auto result = std::make_unique<Zhulong_snapshot_result_t>();
    YuvFrame full{};
    if (!extract_yuv_region(view, 0, 0, static_cast<int>(view.width), static_cast<int>(view.height), full)) {
        return Zhulong_ERR_UNSUPPORTED;
    }

    std::vector<uint8_t> jpeg;
    if (!encode_jpeg(full, quality, jpeg)) {
        return Zhulong_ERR_INTERNAL;
    }
    append_image(*result, -1, static_cast<int32_t>(full.width), static_cast<int32_t>(full.height),
                 std::move(jpeg));

    // 裁剪图：某个框越界/退化时静默跳过，不使整次抓拍失败。
    // 调用方按 box_index 索引，因此这里不需要回填占位。
    for (size_t i = 0; i < boxes.size(); ++i) {
        int px = 0;
        int py = 0;
        int pw = 0;
        int ph = 0;
        if (!normalized_box_to_pixels(boxes[i].data(), view.width, view.height, px, py, pw, ph)) {
            continue;
        }
        YuvFrame crop{};
        if (!extract_yuv_region(view, px, py, pw, ph, crop)) {
            continue;
        }
        std::vector<uint8_t> crop_jpeg;
        if (!encode_jpeg(crop, quality, crop_jpeg)) {
            // 单张裁剪图编码失败不影响其他图：抓拍的核心价值是全幅图。
            continue;
        }
        append_image(*result, static_cast<int32_t>(i),
                     static_cast<int32_t>(crop.width), static_cast<int32_t>(crop.height),
                     std::move(crop_jpeg));
    }

    *out = result.release();
    return Zhulong_OK;
}

} // namespace zhulong

// ============================================================================
//                              C ABI 抓拍句柄接口
// ============================================================================

extern "C" int32_t Zhulong_snapshot_count(Zhulong_snapshot_h result) {
    if (!result) {
        return 0;
    }
    return static_cast<int32_t>(result->images.size());
}

extern "C" const Zhulong_snapshot_image *Zhulong_snapshot_at(Zhulong_snapshot_h result, int32_t index) {
    if (!result || index < 0 || static_cast<size_t>(index) >= result->images.size()) {
        return nullptr;
    }
    return &result->images[static_cast<size_t>(index)]->view;
}

extern "C" void Zhulong_snapshot_destroy(Zhulong_snapshot_h result) {
    // ⚠️ 谁分配谁释放：本函数释放所有 JPEG 缓冲与句柄本身。传入 NULL 安全。
    delete result;
}
