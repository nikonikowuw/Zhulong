/**
 * @file window_crop.cpp
 * @brief 窗口物化的 CPU 实现：分平面逐行拷贝到紧打包目标
 *
 * 关键点：
 *  1. NV12 的 UV 起点用 `HardwareFrame::y_plane_size()`（含 vstride），不是 stride*height；
 *  2. 源行跨度优先 `plane_strides[i]`，为 0 才回退 `stride`；
 *  3. dmabuf 源 mmap + `ScopedFdSync(READ)`，DMA 目标 `ScopedFdSync(WRITE)`；
 *  4. 任何越界/格式不支持都返回 false，由 broker fail_publish，绝不发半截帧。
 */

#include "nodes/preprocess/window_crop.hpp"

#include "platform/dmabuf/dmabuf_sync.hpp"

#include <cstring>
#include <memory>
#include <optional>

#if defined(__linux__)
#include <sys/mman.h>
#endif

namespace zhulong {

namespace {

/** @brief 解析出的源平面视图（只借用，不拥有）。 */
struct SourcePlanes {
    const uint8_t* y{nullptr};
    const uint8_t* u{nullptr};
    const uint8_t* v{nullptr};
    uint32_t y_stride{0};
    uint32_t u_stride{0};
    uint32_t v_stride{0};
};

#if defined(__linux__)
/** @brief 只读 DMA-BUF 映射的 RAII；只借用 fd，绝不 close。 */
class ScopedMmap {
public:
    ScopedMmap() = default;
    ~ScopedMmap() { reset(); }
    ScopedMmap(const ScopedMmap&) = delete;
    ScopedMmap& operator=(const ScopedMmap&) = delete;

    const uint8_t* open(int fd, size_t size) noexcept {
        if (fd < 0 || size == 0) {
            return nullptr;
        }
        void* addr = ::mmap(nullptr, size, PROT_READ, MAP_SHARED, fd, 0);
        if (addr == MAP_FAILED || addr == nullptr) {
            return nullptr;
        }
        addr_ = addr;
        size_ = size;
        return static_cast<const uint8_t*>(addr_);
    }

    void reset() noexcept {
        if (addr_ != nullptr) {
            ::munmap(addr_, size_);
            addr_ = nullptr;
            size_ = 0;
        }
    }

private:
    void* addr_{nullptr};
    size_t size_{0};
};
#endif

/** @brief 为分平面/连续布局计算需要 mmap 的源字节数。 */
size_t source_map_size(const HardwareFrame& frame) noexcept {
    if (frame.format == FRAME_FORMAT_NV12) {
        const size_t uv_stride = frame.plane_strides[1] > 0 ? frame.plane_strides[1] : frame.stride;
        return frame.y_plane_size() + uv_stride * ((static_cast<size_t>(frame.height) + 1) / 2);
    }
    if (frame.format == FRAME_FORMAT_YUV420P) {
        const size_t u_stride = frame.plane_strides[1] > 0 ? frame.plane_strides[1] : (frame.stride + 1) / 2;
        const size_t v_stride = frame.plane_strides[2] > 0 ? frame.plane_strides[2] : (frame.stride + 1) / 2;
        const size_t chroma_rows = (static_cast<size_t>(frame.height) + 1) / 2;
        return static_cast<size_t>(frame.stride) * frame.height + u_stride * chroma_rows +
               v_stride * chroma_rows;
    }
    return 0;
}

/**
 * @brief 解析源平面：优先 host_planes（各自独立），其次 host_ptr / dmabuf 连续基址。
 *
 * 连续基址的平面偏移与 FrameBroker 的既有 memcpy 布局保持一致（Y 用 stride，
 * chroma 用 (stride+1)/2；NV12 的 UV 用 y_plane_size 定位）。
 */
bool resolve_source(const HardwareFrame& frame, const uint8_t* base,
                    SourcePlanes& out) noexcept {
    if (frame.format != FRAME_FORMAT_NV12 && frame.format != FRAME_FORMAT_YUV420P) {
        return false;
    }
    if (frame.width == 0 || frame.height == 0 || frame.stride < frame.width) {
        return false;
    }

    if (frame.host_planes[0] != nullptr) {
        out.y = frame.host_planes[0];
        out.y_stride = frame.plane_strides[0] > 0 ? frame.plane_strides[0] : frame.stride;
        if (out.y_stride < frame.width) {
            return false;
        }
        if (frame.format == FRAME_FORMAT_NV12) {
            if (frame.host_planes[1] == nullptr) {
                return false;
            }
            out.u = frame.host_planes[1];
            out.u_stride = frame.plane_strides[1] > 0 ? frame.plane_strides[1] : frame.stride;
            return out.u_stride >= frame.width;
        }
        if (frame.host_planes[1] == nullptr || frame.host_planes[2] == nullptr) {
            return false;
        }
        out.u = frame.host_planes[1];
        out.v = frame.host_planes[2];
        out.u_stride = frame.plane_strides[1] > 0 ? frame.plane_strides[1] : (frame.stride + 1) / 2;
        out.v_stride = frame.plane_strides[2] > 0 ? frame.plane_strides[2] : (frame.stride + 1) / 2;
        return out.u_stride >= (frame.width + 1) / 2 && out.v_stride >= (frame.width + 1) / 2;
    }

    if (base == nullptr) {
        return false;
    }
    out.y = base;
    out.y_stride = frame.stride;
    if (frame.format == FRAME_FORMAT_NV12) {
        out.u = base + frame.y_plane_size();
        out.u_stride = frame.stride;
        return true;
    }
    const size_t y_size = static_cast<size_t>(frame.stride) * frame.height;
    const size_t chroma_stride = (frame.stride + 1) / 2;
    const size_t chroma_rows = (static_cast<size_t>(frame.height) + 1) / 2;
    out.u = base + y_size;
    out.v = out.u + chroma_stride * chroma_rows;
    out.u_stride = static_cast<uint32_t>(chroma_stride);
    out.v_stride = static_cast<uint32_t>(chroma_stride);
    return true;
}

} // namespace

bool WindowCrop::copy(const HardwareFrame& frame, const ViewWindow& view, uint8_t* dst,
                      size_t dst_size, int dst_stride, int dst_dma_fd) noexcept {
    const PackedLayout layout = packed_layout_for(view, frame.format);
    if (!layout.valid || dst == nullptr || dst_size < layout.bytes ||
        dst_stride != static_cast<int>(layout.stride)) {
        return false;
    }
    if (view.x < 0 || view.y < 0) {
        return false;
    }
    if (static_cast<uint64_t>(view.x) + static_cast<uint64_t>(view.w) > frame.width ||
        static_cast<uint64_t>(view.y) + static_cast<uint64_t>(view.h) > frame.height) {
        return false;
    }
    // 窗口几何保证四个分量都是偶数（YUV420 的 UV 采样前提）。
    if ((view.x | view.y | view.w | view.h) & 1) {
        return false;
    }

    // 目标为 DMA 槽位时覆盖整个 CPU 写入窗口；shm（-1）不做。
    platform::ScopedFdSync dst_sync(dst_dma_fd, platform::kDmaSyncWrite);

    std::optional<platform::ScopedFdSync> src_sync;
#if defined(__linux__)
    ScopedMmap mapped;
#endif
    const uint8_t* base = nullptr;

    if (frame.host_planes[0] == nullptr) {
        if (frame.host_ptr != nullptr) {
            base = frame.host_ptr;
        } else if (frame.dma_fd >= 0) {
            const size_t map_size = source_map_size(frame);
            if (map_size == 0) {
                return false;
            }
            src_sync.emplace(frame.dma_fd, platform::kDmaSyncRead);
#if defined(__linux__)
            base = mapped.open(frame.dma_fd, map_size);
#endif
            if (base == nullptr) {
                return false;
            }
        }
    }

    SourcePlanes planes;
    if (!resolve_source(frame, base, planes)) {
        return false;
    }

    const int w = view.w;
    const int h = view.h;
    const size_t src_y_offset = static_cast<size_t>(view.y) * planes.y_stride + static_cast<size_t>(view.x);
    const uint8_t* src_y = planes.y + src_y_offset;
    uint8_t* dst_y = dst + layout.offsets[0];
    for (int r = 0; r < h; ++r) {
        std::memcpy(dst_y + static_cast<size_t>(r) * layout.stride,
                    src_y + static_cast<size_t>(r) * planes.y_stride, static_cast<size_t>(w));
    }

    const int chroma_rows = h / 2;
    const size_t chroma_y = static_cast<size_t>(view.y / 2);
    if (frame.format == FRAME_FORMAT_NV12) {
        const uint8_t* src_u = planes.u + chroma_y * planes.u_stride + static_cast<size_t>(view.x);
        uint8_t* dst_u = dst + layout.offsets[1];
        for (int r = 0; r < chroma_rows; ++r) {
            std::memcpy(dst_u + static_cast<size_t>(r) * layout.stride,
                        src_u + static_cast<size_t>(r) * planes.u_stride, static_cast<size_t>(w));
        }
    } else {
        const size_t chroma_x = static_cast<size_t>(view.x / 2);
        const size_t chroma_w = static_cast<size_t>(w / 2);
        const size_t dst_chroma_stride = static_cast<size_t>(layout.strides[1]);
        const uint8_t* src_u = planes.u + chroma_y * planes.u_stride + chroma_x;
        uint8_t* dst_u = dst + layout.offsets[1];
        for (int r = 0; r < chroma_rows; ++r) {
            std::memcpy(dst_u + static_cast<size_t>(r) * dst_chroma_stride,
                        src_u + static_cast<size_t>(r) * planes.u_stride, chroma_w);
        }
        const uint8_t* src_v = planes.v + chroma_y * planes.v_stride + chroma_x;
        uint8_t* dst_v = dst + layout.offsets[2];
        for (int r = 0; r < chroma_rows; ++r) {
            std::memcpy(dst_v + static_cast<size_t>(r) * dst_chroma_stride,
                        src_v + static_cast<size_t>(r) * planes.v_stride, chroma_w);
        }
    }

    cpu_count_.fetch_add(1, std::memory_order_relaxed);
    return true;
}

} // namespace zhulong
