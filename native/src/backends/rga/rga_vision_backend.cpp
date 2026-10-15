/**
 * @file rga_vision_backend.cpp
 * @brief RGA 视觉前处理后端实现（运行期能力探测、O(1) 参数适配与 CPU 透明回退）
 *
 * ⚠️ R12（最重要）：目标缓冲区指针**绝不偏移** —— 内容区偏移必须由 `improcess`
 * 的 `drect` 表达。若把 dst 指针直接偏移到内容区起点，驱动会按完整 `wstride × hstride`
 * 校验，访问超出 Tensor 边界（实测约 242KB 越界），引发 RGA 陷入 D 状态死锁
 * 并触发看门狗复位（整机重启，`uptime -s` 可对照）。
 *
 * ⚠️ 不得声称零拷贝：当前走 wrapbuffer_virtualaddr 虚拟地址映射，
 * 存在用户态到 DMA 映射开销（DMA-BUF 属后续任务）。
 *
 * ⚠️ 滤波差异：与 CPU 软解的核函数不同，下采样时单像素极差可达 119，
 * 因此测试容差为 worst <= 128。
 */

#include "rga_vision_backend.hpp"

#include "rga_error_map.hpp"

#include <algorithm/runtime/cpu_vision_backend.hpp>
#include <algorithm/runtime/letterbox_geometry.hpp>

#include <cstddef>
#include <rga/im2d.h>

#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstring>
#include <limits>

namespace zhulong {
namespace {

// ── 格式映射 ──────────────────────────────────────────────────────────────
// RGA 用 RK_FORMAT_* 表达通道顺序（rga.h 实测值）。
//
// ⚠️ 通道顺序语义：zhulong 的 RGB24 表示内存里 R,G,B 顺序，对应 RK_FORMAT_RGB_888
// （rga.h:32「[0:23] R:G:B 8:8:8 little endian」）；BGR24 对应 RK_FORMAT_BGR_888。
//
// ⚠️ 多平面与 32bit 格式经真机实测（见 algo-plugin-contract.md §9）：
//   RGBA32  → RK_FORMAT_RGBA_8888 (0x0 << 8)
//   NV12    → RK_FORMAT_YCbCr_420_SP (0xa << 8) （2 plane: Y + 交错 Cb:Cr，Cb 在低字节）
//   YUV420P → RK_FORMAT_YCbCr_420_P  (0xb << 8) （3 plane: Y + Cb + Cr）
/**
 * @brief 源像素格式到 RGA 格式描述符的映射结果
 */
struct SourceFormatSpec {
    int rga_format{0};    /**< 对应的 RK_FORMAT_* 值 */
    int plane0_bpp{0};    /**< 平面 0 的每像素字节数（用于推导 wstride） */
    int plane_count{1};   /**< 平面数量（1 = 单平面，2 = NV12，3 = YUV420P） */
    bool valid{false};    /**< 是否为 RGA 支持的格式 */
};

/**
 * @brief 将 zhulong 像素格式映射为 RGA 格式描述符
 *
 * @param format zhulong 像素格式
 * @return SourceFormatSpec 描述符（不支持的格式 valid == false）
 */
SourceFormatSpec describe_source_format(zhulong_pixel_format_t format) {
    switch (format) {
        case ZHULONG_PIXEL_FORMAT_RGB24:
            return {RK_FORMAT_RGB_888, 3, 1, true};
        case ZHULONG_PIXEL_FORMAT_BGR24:
            return {RK_FORMAT_BGR_888, 3, 1, true};
        case ZHULONG_PIXEL_FORMAT_RGBA32:
            return {RK_FORMAT_RGBA_8888, 4, 1, true};
        case ZHULONG_PIXEL_FORMAT_NV12:
            return {RK_FORMAT_YCbCr_420_SP, 1, 2, true};
        case ZHULONG_PIXEL_FORMAT_YUV420P:
            return {RK_FORMAT_YCbCr_420_P, 1, 3, true};
        default:
            return {0, 0, 0, false};
    }
}

/**
 * @brief 目标侧 Tensor 依据合同恒定为 HWC 3 通道 UINT8，因此目标格式只支持 packed 24bit。
 *
 * @param format 目标像素格式
 * @return int 对应的 RK_FORMAT_*；不支持时返回 0
 */
int rga_format_for_target_24bit(zhulong_pixel_format_t format) {
    switch (format) {
        case ZHULONG_PIXEL_FORMAT_RGB24:
            return RK_FORMAT_RGB_888;
        case ZHULONG_PIXEL_FORMAT_BGR24:
            return RK_FORMAT_BGR_888;
        default:
            return 0;
    }
}

/**
 * @brief 判断 imcheck_t 返回状态是否为「通过」
 */
bool imcheck_is_ok(IM_STATUS status) {
    return status == IM_STATUS_NOERROR || status == IM_STATUS_SUCCESS;
}

/**
 * @brief 判断矩形是否完全落在 w × h 范围内（越界防御，避免驱动陷入死锁）
 */
bool rect_within(const im_rect& r, int w, int h) {
    return r.x >= 0 && r.y >= 0 && r.width > 0 && r.height > 0 &&
           r.x + r.width <= w && r.y + r.height <= h;
}

// 统一的源侧 buffer 解析与连续性校验
/**
 * @brief 源侧 buffer 解析结果
 */
struct ResolvedSource {
    rga_buffer_t buffer{};          /**< 包装后的 RGA 源缓冲区 */
    bool is_stride_adapted{false};  /**< 是否使用了非紧密（实际 linesize）行跨度 */
    bool valid{false};              /**< 是否解析成功（false 表示应回退 CPU） */
};

/**
 * @brief 解析源帧视图并校验多平面连续性与硬件对齐要求
 *
 * ⚠️ 多平面格式（NV12 / YUV420P）要求 plane 在内存中**严格连续**（偏移由 y_stride
 * 与 height 推导）；不连续时返回 valid == false，由调用方回退 CPU（重排是 O(N)，不做）。
 *
 * @param src 源帧视图（借用）
 * @param stride_align 运行期探测到的行跨度字节对齐值（0 表示未取到）
 * @return ResolvedSource 解析结果
 */
ResolvedSource resolve_source(const zhulong_frame_view& src, uint32_t stride_align) {
    ResolvedSource res{};
    if (!src.planes[0] || src.width <= 0 || src.height <= 0) {
        return res;
    }

    const SourceFormatSpec spec = describe_source_format(src.format);
    if (!spec.valid) {
        return res;
    }

    const int64_t min_stride = static_cast<int64_t>(src.width) * spec.plane0_bpp;
    if (min_stride > std::numeric_limits<int>::max()) {
        return res;
    }

    int y_stride = src.strides[0];
    if (y_stride == 0) {
        y_stride = static_cast<int>(min_stride);
    }
    if (y_stride < min_stride) {
        return res;
    }
    if (y_stride % spec.plane0_bpp != 0) {
        return res;
    }

    // 运行期探测到的行字节对齐校验
    if (stride_align > 0 && static_cast<uint32_t>(y_stride) % stride_align != 0u) {
        return res;
    }

    const int wstride_px = y_stride / spec.plane0_bpp;
    const int hstride = src.height;

    // 多平面连续性与对齐校验
    if (spec.plane_count == 2) {
        // NV12: 要求 plane1 连续放置在 planes[0] + y_stride * height
        if (!src.planes[1]) {
            return res;
        }
        const uintptr_t p0 = reinterpret_cast<uintptr_t>(src.planes[0]);
        const uintptr_t p1 = reinterpret_cast<uintptr_t>(src.planes[1]);
        const size_t expected_offset = static_cast<size_t>(y_stride) * src.height;
        if (p1 != p0 + expected_offset) {
            return res;
        }
        if (src.strides[1] > 0 && src.strides[1] != y_stride) {
            return res;
        }
    } else if (spec.plane_count == 3) {
        // YUV420P: 要求 plane1 在 planes[0] + y_stride * height,
        // plane2 在 plane1 + (y_stride/2) * (height/2)
        if (!src.planes[1] || !src.planes[2] || src.width % 2 != 0 || src.height % 2 != 0 || y_stride % 2 != 0) {
            return res;
        }
        const uintptr_t p0 = reinterpret_cast<uintptr_t>(src.planes[0]);
        const uintptr_t p1 = reinterpret_cast<uintptr_t>(src.planes[1]);
        const uintptr_t p2 = reinterpret_cast<uintptr_t>(src.planes[2]);
        const size_t expected_u_offset = static_cast<size_t>(y_stride) * src.height;
        const size_t uv_w = static_cast<size_t>(y_stride) / 2;
        const size_t uv_h = static_cast<size_t>(src.height) / 2;
        const size_t expected_v_offset = expected_u_offset + uv_w * uv_h;
        if (p1 != p0 + expected_u_offset || p2 != p0 + expected_v_offset) {
            return res;
        }
    }

    if (src.dma_buf_fd >= 0) {
        res.buffer = wrapbuffer_fd(
            src.dma_buf_fd, src.width, src.height, spec.rga_format,
            wstride_px, hstride);
    } else {
        res.buffer = wrapbuffer_virtualaddr(
            const_cast<uint8_t*>(src.planes[0]), src.width, src.height, spec.rga_format,
            wstride_px, hstride);
    }
    if (res.buffer.width <= 0) {
        return res;
    }

    // 实际 linesize 与紧密排列不一致时，走「非紧密 stride 适配」路径（AC9 计数）
    res.is_stride_adapted = (y_stride != min_stride);
    res.valid = true;
    return res;
}

} // namespace

RgaVisionBackend::RgaVisionBackend() {
    probe();
}

/**
 * @brief 探测 RGA 可用性与硬件能力参数
 *
 * ⚠️ 对齐值严禁硬编码：byte_stride_align 从 /sys/kernel/debug/rkrga/hardware 读取
 * （RK3568 实测 4）；offset_align 通过 imcheck_t 在合成缓冲上动态探测（实测 2）。
 * 任一探测失败（取不到 stride_align 或 /dev/rga 不可访问）即置 rga_usable_ = false，
 * 由上层透明回退 CPU。
 */
void RgaVisionBackend::probe() {
    rga_usable_ = false;
    stride_align_ = 0;
    offset_align_ = 0;

    // 1) 头文件与运行库版本一致性检查。失败即视为不可用（回退 CPU）。
    if (imcheckHeader() != IM_STATUS_SUCCESS) {
        return;
    }

    // 2) 记录库版本（querystring 是 librga 导出符号，实测存在）。
    const char* version = querystring(RGA_VERSION);
    if (version) {
        std::snprintf(api_version_, sizeof(api_version_), "%s", version);
    }

    // 3) 探测 byte_stride_align。
    //    优先读内核 debugfs 的 byte_stride_align（RK3568 实测 4）；
    //    ⚠️ 取不到就**不使用 RGA**（回退 CPU），绝不硬编码 4 或 16。
    {
        std::FILE* hardware = std::fopen("/sys/kernel/debug/rkrga/hardware", "r");
        if (hardware) {
            char line[256];
            while (std::fgets(line, sizeof(line), hardware)) {
                unsigned value = 0;
                if (std::sscanf(line, "byte_stride_align: %u", &value) == 1 && value > 0) {
                    stride_align_ = value;
                    break;
                }
            }
            std::fclose(hardware);
        }
    }

    if (stride_align_ == 0) {
        return;
    }

    // 4) 驱动版本（尽力而为，读不到不影响可用性）。
    {
        std::FILE* version_node = std::fopen("/sys/kernel/debug/rkrga/version", "r");
        if (version_node) {
            char line[sizeof(driver_version_)];
            if (std::fgets(line, sizeof(line), version_node)) {
                std::snprintf(driver_version_, sizeof(driver_version_), "%s", line);
            }
            std::fclose(version_node);
        }
    }

    // 5) /dev/rga 可访问性：权限为 crw-rw---- root:video，进程须属 video 组。
    {
        std::FILE* device = std::fopen("/dev/rga", "rb");
        if (!device) {
            return;
        }
        std::fclose(device);
    }

    // 6) 运行期探测 srect/drect 偏移对齐 (offset_align_)：
    //    利用 imcheck_t 在合成缓冲上测试不同偏移，避免硬编码 2 (RGA2) 或 64 (RGA3)。
    {
        const int tw = 64, th = 64;
        std::vector<uint8_t> dummy_buf(static_cast<size_t>(tw) * th * 3, 0);
        rga_buffer_t s = wrapbuffer_virtualaddr(dummy_buf.data(), tw, th, RK_FORMAT_RGB_888, tw, th);
        rga_buffer_t d = wrapbuffer_virtualaddr(dummy_buf.data(), tw, th, RK_FORMAT_RGB_888, tw, th);
        const im_rect prect{0, 0, 0, 0};

        uint32_t detected = 0;
        for (uint32_t a = 1; a <= 64; ++a) {
            im_rect srect{static_cast<int>(a), 0, 32, 32};
            im_rect drect{0, 0, 32, 32};
            if (imcheck_is_ok(imcheck_t(s, d, rga_buffer_t{}, srect, drect, prect, 0))) {
                detected = a;
                break;
            }
        }
        offset_align_ = (detected > 0) ? detected : 2;
    }

    rga_usable_ = true;
}

/**
 * @brief 尝试走 RGA 路径完成 Letterbox；返回 false 表示应回退 CPU
 *
 * ⚠️ R12：目标 buffer 描述的是**完整 Tensor**（含 padding），内容区 ROI 仅通过
 * `drect` 表达。切勿将 dst_buf 指针偏移到内容区起点 —— 那会让驱动按完整
 * wstride × hstride 校验并越界（实测约 242KB），引发 RGA D 状态死锁与看门狗复位。
 */
bool RgaVisionBackend::try_rga_letterbox(const zhulong_frame_view& src, Tensor& dst,
                                         LetterboxParams& inout_params) {
    const int dst_format = rga_format_for_target_24bit(inout_params.target_format);
    if (dst_format == 0) {
        return false;
    }

    const ResolvedSource resolved = resolve_source(src, stride_align_);
    if (!resolved.valid) {
        return false;
    }

    const int target_w = inout_params.target_width > 0 ? inout_params.target_width : 640;
    const int target_h = inout_params.target_height > 0 ? inout_params.target_height : 640;

    // 目标 buffer 的行跨度固定为 target_w * 3（Tensor 紧密排列）。
    const int dst_wstride_bytes = target_w * 3;
    if (!stride_aligned(dst_wstride_bytes)) {
        return false;
    }

    // 几何计算与 CPU 共用同一纯函数（design §6，不重复实现）。
    const LetterboxGeometry geometry =
        compute_letterbox_geometry(src.width, src.height, target_w, target_h);

    // 若目标 Tensor 具备外部 DMA-BUF 且尺寸匹配，直接原地复用；否则分配普通 Tensor
    if (dst.dma_buf_fd() < 0 || dst.byte_size() < static_cast<size_t>(target_h * target_w * 3)) {
        dst = Tensor({target_h, target_w, 3}, DataType::UINT8);
    }
    uint8_t* dst_buf = dst.data<uint8_t>();
    if (dst_buf) {
        std::memset(dst_buf, 114, target_h * target_w * 3);
    }

    // ⚠️ 描述完整的 dst buffer（含 padding）；内容区偏移只能通过 drect 传递，
    // 绝不可把 dst_buf 指针偏移到内容区起点（R12）。
    rga_buffer_t src_buffer = resolved.buffer;
    rga_buffer_t dst_buffer;
    if (dst.dma_buf_fd() >= 0) {
        dst_buffer = wrapbuffer_fd(
            dst.dma_buf_fd(), target_w, target_h, dst_format, target_w, target_h);
    } else {
        dst_buffer = wrapbuffer_virtualaddr(
            dst_buf, target_w, target_h, dst_format, target_w, target_h);
    }

    if (dst_buffer.width <= 0) {
        return false;
    }

    const im_rect srect{0, 0, src.width, src.height};
    const im_rect drect{geometry.pad_left, geometry.pad_top, geometry.unpad_w, geometry.unpad_h};
    const im_rect prect{0, 0, 0, 0};

    // 严格范围校验与 imcheck_t 预检
    if (!rect_within(srect, src.width, src.height) || !rect_within(drect, target_w, target_h)) {
        return false;
    }
    if (!imcheck_is_ok(imcheck_t(src_buffer, dst_buffer, rga_buffer_t{}, srect, drect, prect, 0))) {
        return false;
    }

    // improcess 执行缩放 + CSC
    const IM_STATUS status = improcess(src_buffer, dst_buffer, rga_buffer_t{},
                                       srect, drect, prect, -1, nullptr, nullptr, 0);
    if (status != IM_STATUS_SUCCESS) {
        return false;
    }

    inout_params.scale = geometry.scale;
    inout_params.pad_left = geometry.pad_left;
    inout_params.pad_top = geometry.pad_top;

    if (resolved.is_stride_adapted) {
        stride_adapted_count_.fetch_add(1, std::memory_order_relaxed);
    }
    return true;
}

/**
 * @brief 尝试走 RGA 路径完成缩放；返回 false 表示应回退 CPU
 *
 * ⚠️ resize 接口使用平坦指针，只支持单平面格式（RGB24 / BGR24 / RGBA32）；
 * NV12 / YUV420P 等非连续内存格式安全回退 CPU 并返回 NOT_SUPPORTED。
 */
bool RgaVisionBackend::try_rga_resize(const uint8_t* src, int src_w, int src_h, int src_stride,
                                      zhulong_pixel_format_t src_format, uint8_t* dst, int dst_stride,
                                      int dst_w, int dst_h, zhulong_pixel_format_t dst_format) {
    const SourceFormatSpec src_spec = describe_source_format(src_format);
    const int rga_dst_format = rga_format_for_target_24bit(dst_format);
    // resize 接口使用平坦指针，只支持单平面格式（RGB24 / BGR24 / RGBA32）
    if (!src_spec.valid || src_spec.plane_count != 1 || rga_dst_format == 0) {
        return false;
    }

    const int64_t min_src_stride = static_cast<int64_t>(src_w) * src_spec.plane0_bpp;
    const int64_t min_dst_stride = static_cast<int64_t>(dst_w) * 3;
    if (min_src_stride > std::numeric_limits<int>::max() ||
        min_dst_stride > std::numeric_limits<int>::max()) {
        return false;
    }
    if (src_stride == 0) src_stride = static_cast<int>(min_src_stride);
    if (dst_stride == 0) dst_stride = static_cast<int>(min_dst_stride);
    if (src_stride < min_src_stride || dst_stride < min_dst_stride) {
        return false;
    }
    if (src_stride % src_spec.plane0_bpp != 0 || dst_stride % 3 != 0) {
        return false;
    }

    if (!stride_aligned(src_stride) || !stride_aligned(dst_stride)) {
        return false;
    }

    const int src_wstride_px = src_stride / src_spec.plane0_bpp;
    const int dst_wstride_px = dst_stride / 3;

    rga_buffer_t src_buffer = wrapbuffer_virtualaddr(
        const_cast<uint8_t*>(src), src_w, src_h, src_spec.rga_format,
        src_wstride_px, src_h);
    rga_buffer_t dst_buffer = wrapbuffer_virtualaddr(
        dst, dst_w, dst_h, rga_dst_format,
        dst_wstride_px, dst_h);

    if (src_buffer.width <= 0 || dst_buffer.width <= 0) {
        return false;
    }

    const im_rect srect{0, 0, src_w, src_h};
    const im_rect drect{0, 0, dst_w, dst_h};
    const im_rect prect{0, 0, 0, 0};

    if (!rect_within(srect, src_w, src_h) || !rect_within(drect, dst_w, dst_h)) {
        return false;
    }
    if (!imcheck_is_ok(imcheck_t(src_buffer, dst_buffer, rga_buffer_t{}, srect, drect, prect, 0))) {
        return false;
    }

    const IM_STATUS status = improcess(src_buffer, dst_buffer, rga_buffer_t{},
                                       srect, drect, prect, -1, nullptr, nullptr, 0);
    if (status != IM_STATUS_SUCCESS) {
        return false;
    }

    if (src_stride != min_src_stride) {
        stride_adapted_count_.fetch_add(1, std::memory_order_relaxed);
    }
    return true;
}

/**
 * @brief 尝试走 RGA 路径完成「裁剪 + 缩放 + CSC」；返回 false 表示应回退 CPU
 *
 * ⚠️ 两阶段裁剪（形式 1）：ROI 坐标不满足 2 像素对齐时，**只改参数向外扩取整**
 * （srect 是参数，不是内存），避免 O(N) 内存搬移。
 */
bool RgaVisionBackend::try_rga_crop_scale(const zhulong_frame_view& src, const CropRect& roi,
                                          uint8_t* dst, int dst_stride, int dst_w, int dst_h,
                                          zhulong_pixel_format_t dst_format) {
    const int rga_dst_format = rga_format_for_target_24bit(dst_format);
    if (rga_dst_format == 0) {
        return false;
    }

    const ResolvedSource resolved = resolve_source(src, stride_align_);
    if (!resolved.valid) {
        return false;
    }

    const int64_t min_dst_stride = static_cast<int64_t>(dst_w) * 3;
    if (dst_stride == 0) dst_stride = static_cast<int>(min_dst_stride);
    if (dst_stride < min_dst_stride || dst_stride % 3 != 0 || !stride_aligned(dst_stride)) {
        return false;
    }

    // srect 坐标按 offset_align_ 向外扩取整（形式 1：改参数，零内存重排）
    const int align = static_cast<int>(offset_align_ > 0 ? offset_align_ : 2);
    int x0 = (roi.x / align) * align;
    int y0 = (roi.y / align) * align;
    int x1 = ((roi.x + roi.width + align - 1) / align) * align;
    int y1 = ((roi.y + roi.height + align - 1) / align) * align;
    if (x1 > src.width) x1 = src.width;
    if (y1 > src.height) y1 = src.height;
    if (x1 <= x0 || y1 <= y0) {
        return false;
    }

    const bool rect_adapted = (x0 != roi.x || y0 != roi.y || (x1 - x0) != roi.width || (y1 - y0) != roi.height);

    rga_buffer_t src_buffer = resolved.buffer;
    rga_buffer_t dst_buffer = wrapbuffer_virtualaddr(
        dst, dst_w, dst_h, rga_dst_format,
        dst_stride / 3, dst_h);

    if (src_buffer.width <= 0 || dst_buffer.width <= 0) {
        return false;
    }

    const im_rect srect{x0, y0, x1 - x0, y1 - y0};
    const im_rect drect{0, 0, dst_w, dst_h};
    const im_rect prect{0, 0, 0, 0};

    if (!rect_within(srect, src.width, src.height) || !rect_within(drect, dst_w, dst_h)) {
        return false;
    }
    if (!imcheck_is_ok(imcheck_t(src_buffer, dst_buffer, rga_buffer_t{}, srect, drect, prect, 0))) {
        return false;
    }

    const IM_STATUS status = improcess(src_buffer, dst_buffer, rga_buffer_t{},
                                       srect, drect, prect, -1, nullptr, nullptr, 0);
    if (status != IM_STATUS_SUCCESS) {
        return false;
    }

    if (rect_adapted) {
        rect_adapted_count_.fetch_add(1, std::memory_order_relaxed);
    }
    if (resolved.is_stride_adapted) {
        stride_adapted_count_.fetch_add(1, std::memory_order_relaxed);
    }
    return true;
}

/**
 * @brief 「裁剪 + 缩放 + CSC」公开入口：RGA 可用则走硬件，否则透明回退 CPU
 */
zhulong_status_t RgaVisionBackend::crop_scale(const zhulong_frame_view& src, const CropRect& roi,
                                              uint8_t* dst, int dst_stride, int dst_w, int dst_h,
                                              zhulong_pixel_format_t dst_format) {
    if (!dst || dst_w <= 0 || dst_h <= 0) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }
    if (!src.planes[0] || src.width <= 0 || src.height <= 0) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }
    if (roi.width <= 0 || roi.height <= 0 || roi.x < 0 || roi.y < 0 ||
        roi.x + roi.width > src.width || roi.y + roi.height > src.height) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }

    if (rga_usable_) {
        try {
            if (try_rga_crop_scale(src, roi, dst, dst_stride, dst_w, dst_h, dst_format)) {
                return ZHULONG_PLUGIN_OK;
            }
        } catch (...) {
            // 异常不穿越 ABI
        }
    }

    fallback_count_.fetch_add(1, std::memory_order_relaxed);
    // ⚠️ IVisionBackend 无裁剪入口。当输入为单平面 24bit 格式时，可以通过指针偏移
    // 裁剪后透明交给 fallback_.resize 执行；不支持的格式返回 NOT_SUPPORTED。
    if (src.format == ZHULONG_PIXEL_FORMAT_RGB24 || src.format == ZHULONG_PIXEL_FORMAT_BGR24) {
        const int src_stride = src.strides[0] > 0 ? src.strides[0] : src.width * 3;
        const uint8_t* roi_ptr = src.planes[0] + static_cast<size_t>(roi.y) * src_stride + roi.x * 3;
        return fallback_.resize(roi_ptr, roi.width, roi.height, src_stride, src.format,
                                dst, dst_stride, dst_w, dst_h, dst_format);
    }
    return ZHULONG_PLUGIN_ERR_NOT_SUPPORTED;
}

/**
 * @brief Letterbox 公开入口：RGA 可用则走硬件，否则透明回退 CPU
 */
zhulong_status_t RgaVisionBackend::letterbox(const zhulong_frame_view& src,
                                             Tensor& dst,
                                             LetterboxParams& inout_params) {
    if (rga_usable_) {
        try {
            if (try_rga_letterbox(src, dst, inout_params)) {
                return ZHULONG_PLUGIN_OK;
            }
        } catch (...) {
            // RGA 路径异常不向上传播：回退 CPU
        }
    }

    fallback_count_.fetch_add(1, std::memory_order_relaxed);
    return fallback_.letterbox(src, dst, inout_params);
}

/**
 * @brief 缩放公开入口：RGA 可用则走硬件，否则透明回退 CPU
 */
zhulong_status_t RgaVisionBackend::resize(const uint8_t* src, int src_w, int src_h, int src_stride,
                                          zhulong_pixel_format_t src_format, uint8_t* dst, int dst_stride,
                                          int dst_w, int dst_h, zhulong_pixel_format_t dst_format) {
    if (rga_usable_) {
        try {
            if (try_rga_resize(src, src_w, src_h, src_stride, src_format,
                               dst, dst_stride, dst_w, dst_h, dst_format)) {
                return ZHULONG_PLUGIN_OK;
            }
        } catch (...) {
            // 同上：异常不穿越
        }
    }

    fallback_count_.fetch_add(1, std::memory_order_relaxed);
    return fallback_.resize(src, src_w, src_h, src_stride, src_format,
                            dst, dst_stride, dst_w, dst_h, dst_format);
}

} // namespace zhulong
