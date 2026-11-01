/**
 * @file cpu_vision_backend.cpp
 * @brief CPU 视觉前处理后端实现（stride 契约、双线性缩放与 Letterbox）
 */

#include "cpu_vision_backend.hpp"
#include "letterbox_geometry.hpp"
#include "letterbox_target.hpp"
#include <zhulong/runtime/yuv_sampling.hpp>
#include <algorithm>
#include <cmath>
#include <cstring>
#include <limits>

namespace zhulong {
namespace {

/**
 * @brief 判断像素格式是否为 packed 24 位 RGB/BGR
 */
bool is_packed_24bit(zhulong_pixel_format_t format) {
    return format == ZHULONG_PIXEL_FORMAT_RGB24 || format == ZHULONG_PIXEL_FORMAT_BGR24;
}

/**
 * @brief YUV420P 与 NV12 的平面布局差异，仅此一项不同
 */
struct YuvLayout {
    bool is_nv12;
    int src_w;
    int src_h;
    int y_stride;
    int chroma_stride_0;   /**< YUV420P: U 平面 stride；NV12: 唯一的交错 UV 平面 stride */
    int chroma_stride_1;   /**< 仅 YUV420P 使用（V 平面） */
};

/**
 * @brief 把半平面 YUV 源缩放到 packed RGB24/BGR24 目标
 *
 * YUV420P 与 NV12 只有色度平面布局不同（分离平面 vs 交错半平面），
 * 亮度采样、色度坐标映射与色彩转换完全一致，因此共用一套循环。
 */
zhulong_status_t resize_yuv(const zhulong_frame_view& src, const YuvLayout& layout,
                            uint8_t* dst, int dst_stride, int dst_w, int dst_h,
                            zhulong_pixel_format_t dst_format) {
    const int uv_w = (layout.src_w + 1) / 2;
    const int uv_h = (layout.src_h + 1) / 2;
    const bool write_bgr = (dst_format == ZHULONG_PIXEL_FORMAT_BGR24);

    for (int y = 0; y < dst_h; ++y) {
        const float src_y = std::clamp((static_cast<float>(y) + 0.5f) * layout.src_h / dst_h - 0.5f,
                                       0.0f, static_cast<float>(layout.src_h - 1));
        const float uv_y = std::clamp((static_cast<float>(y) + 0.5f) * uv_h / dst_h - 0.5f,
                                      0.0f, static_cast<float>(uv_h - 1));
        uint8_t* dst_row = dst + static_cast<size_t>(y) * dst_stride;

        for (int x = 0; x < dst_w; ++x) {
            const float src_x = std::clamp((static_cast<float>(x) + 0.5f) * layout.src_w / dst_w - 0.5f,
                                           0.0f, static_cast<float>(layout.src_w - 1));
            const float uv_x = std::clamp((static_cast<float>(x) + 0.5f) * uv_w / dst_w - 0.5f,
                                          0.0f, static_cast<float>(uv_w - 1));

            const float y_val = sample_plane_bilinear(src.planes[0], layout.src_w, layout.src_h,
                                                      layout.y_stride, src_x, src_y);
            float u_val = 128.0f;
            float v_val = 128.0f;
            if (layout.is_nv12) {
                sample_nv12_uv_bilinear(src.planes[1], uv_w, uv_h, layout.chroma_stride_0,
                                        uv_x, uv_y, u_val, v_val);
            } else {
                u_val = sample_plane_bilinear(src.planes[1], uv_w, uv_h, layout.chroma_stride_0, uv_x, uv_y);
                v_val = sample_plane_bilinear(src.planes[2], uv_w, uv_h, layout.chroma_stride_1, uv_x, uv_y);
            }

            uint8_t r = 0;
            uint8_t g = 0;
            uint8_t b = 0;
            yuv_to_rgb_u8(y_val, u_val, v_val, r, g, b);

            dst_row[x * 3 + 0] = write_bgr ? b : r;
            dst_row[x * 3 + 1] = g;
            dst_row[x * 3 + 2] = write_bgr ? r : b;
        }
    }
    return ZHULONG_PLUGIN_OK;
}

/**
 * @brief 校验 YUV 源平面与 stride，并映到统一布局
 *
 * 未提供的平面（NV12 的 planes[2]）必须在读取像素前就拒绝。
 */
zhulong_status_t describe_yuv_layout(const zhulong_frame_view& src, YuvLayout& layout) {
    // ⚠️ 负 stride 表示「底朝上的缓冲区」，本后端不支持。必须显式拒绝，**不能**当成
    //    「未提供 stride」回落到紧凑布局：那会把像素按 top-down 读出来（图像上下翻转、
    //    无任何错误码），且与 RGA 后端（显式拒负）语义分叉，破坏「后端可替换」前提。
    if (src.strides[0] < 0 || src.strides[1] < 0 || src.strides[2] < 0) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }

    layout.is_nv12 = (src.format == ZHULONG_PIXEL_FORMAT_NV12);
    layout.src_w = src.width;
    layout.src_h = src.height;

    // ⚠️ 用 int64 计算 (width + 1) / 2：width 逼近 INT_MAX 时 int 的 +1 是有符号溢出
    //    （UBSan 下 -fno-sanitize-recover 会直接 abort）。顺手校验色度行宽可被 int 表示。
    const int64_t chroma_min_width_64 = (static_cast<int64_t>(src.width) + 1) / 2;
    if (chroma_min_width_64 * 2 > std::numeric_limits<int>::max()) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }
    const int chroma_min_width = static_cast<int>(chroma_min_width_64);

    if (layout.is_nv12) {
        if (!src.planes[0] || !src.planes[1]) return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
        layout.y_stride = src.strides[0] > 0 ? src.strides[0] : src.width;
        // ⚠️ NV12 的 UV 是交错平面，每行占 2 * ceil(width/2) 字节，**不是** width。
        //    奇数宽时按 width 回落会把紧凑布局误判成 stride 不足而拒绝。
        layout.chroma_stride_0 = src.strides[1] > 0 ? src.strides[1] : chroma_min_width * 2;
        layout.chroma_stride_1 = layout.chroma_stride_0;
        if (layout.y_stride < src.width || layout.chroma_stride_0 < chroma_min_width * 2) {
            return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
        }
        return ZHULONG_PLUGIN_OK;
    }

    if (!src.planes[0] || !src.planes[1] || !src.planes[2]) return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    layout.y_stride = src.strides[0] > 0 ? src.strides[0] : src.width;
    layout.chroma_stride_0 = src.strides[1] > 0 ? src.strides[1] : chroma_min_width;
    layout.chroma_stride_1 = src.strides[2] > 0 ? src.strides[2] : chroma_min_width;
    if (layout.y_stride < src.width || layout.chroma_stride_0 < chroma_min_width ||
        layout.chroma_stride_1 < chroma_min_width) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }
    return ZHULONG_PLUGIN_OK;
}

} // namespace

zhulong_status_t CpuVisionBackend::resize(const uint8_t* src, int src_w, int src_h, int src_stride,
                                          zhulong_pixel_format_t src_format, uint8_t* dst, int dst_stride,
                                          int dst_w, int dst_h, zhulong_pixel_format_t dst_format) {
    if (!src || !dst || src_w <= 0 || src_h <= 0 || dst_w <= 0 || dst_h <= 0) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }
    // ⚠️ 未实现的格式必须在读取源像素之前拒绝，不得把 NV12 plane0 当三通道 RGB 读取
    if (!is_packed_24bit(src_format) || !is_packed_24bit(dst_format)) {
        return ZHULONG_PLUGIN_ERR_NOT_SUPPORTED;
    }

    const int64_t min_src_stride = static_cast<int64_t>(src_w) * 3;
    const int64_t min_dst_stride = static_cast<int64_t>(dst_w) * 3;
    if (min_src_stride > std::numeric_limits<int>::max() || min_dst_stride > std::numeric_limits<int>::max()) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }
    // stride 契约：0 表示 packed（按 width*3 兼容处理）；正 stride 必须至少为 width*3
    if (src_stride == 0) src_stride = static_cast<int>(min_src_stride);
    if (dst_stride == 0) dst_stride = static_cast<int>(min_dst_stride);
    if (src_stride < min_src_stride || dst_stride < min_dst_stride) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }

    // RGB/BGR 目标顺序不一致时必须交换 R/B 通道。
    // ⚠️ 必须显式列出两个方向：写成「格式不同就交换」只在「唯一合法组合是
    //    RGB24<->BGR24」时成立，将来一旦支持第三种 24 位布局就会静默错位。
    const bool swap_channels = (src_format == ZHULONG_PIXEL_FORMAT_RGB24 &&
                                dst_format == ZHULONG_PIXEL_FORMAT_BGR24) ||
                               (src_format == ZHULONG_PIXEL_FORMAT_BGR24 &&
                                dst_format == ZHULONG_PIXEL_FORMAT_RGB24);
    // 逐行以 src + y*stride 定位，严禁假定 src 为 packed 连续缓冲
    for (int y = 0; y < dst_h; ++y) {
        float source_y = (static_cast<float>(y) + 0.5f) * src_h / dst_h - 0.5f;
        source_y = std::clamp(source_y, 0.0f, static_cast<float>(src_h - 1));
        const int y0 = static_cast<int>(std::floor(source_y));
        const int y1 = std::min(y0 + 1, src_h - 1);
        const float y_weight = source_y - y0;
        const uint8_t* row0 = src + static_cast<size_t>(y0) * src_stride;
        const uint8_t* row1 = src + static_cast<size_t>(y1) * src_stride;
        uint8_t* dst_row = dst + static_cast<size_t>(y) * dst_stride;

        for (int x = 0; x < dst_w; ++x) {
            // 像素中心坐标映射 + 边界 clamp，避免边缘越界采样
            float source_x = (static_cast<float>(x) + 0.5f) * src_w / dst_w - 0.5f;
            source_x = std::clamp(source_x, 0.0f, static_cast<float>(src_w - 1));
            const int x0 = static_cast<int>(std::floor(source_x));
            const int x1 = std::min(x0 + 1, src_w - 1);
            const float x_weight = source_x - x0;

            for (int channel = 0; channel < 3; ++channel) {
                const int src_channel = swap_channels ? 2 - channel : channel;
                const float top = row0[x0 * 3 + src_channel] * (1.0f - x_weight) +
                                  row0[x1 * 3 + src_channel] * x_weight;
                const float bottom = row1[x0 * 3 + src_channel] * (1.0f - x_weight) +
                                     row1[x1 * 3 + src_channel] * x_weight;
                const float value = top * (1.0f - y_weight) + bottom * y_weight;
                dst_row[x * 3 + channel] = static_cast<uint8_t>(std::clamp(std::lround(value), 0L, 255L));
            }
        }
    }
    return ZHULONG_PLUGIN_OK;
}

zhulong_status_t CpuVisionBackend::letterbox(const zhulong_frame_view& src,
                                             Tensor& dst,
                                             LetterboxParams& inout_params) {
    const bool src_is_packed = is_packed_24bit(src.format);
    const bool src_is_yuv = (src.format == ZHULONG_PIXEL_FORMAT_YUV420P ||
                             src.format == ZHULONG_PIXEL_FORMAT_NV12);

    if ((!src_is_packed && !src_is_yuv) || !is_packed_24bit(inout_params.target_format)) {
        return ZHULONG_PLUGIN_ERR_NOT_SUPPORTED;
    }
    if (!src.planes[0] || src.width <= 0 || src.height <= 0) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }

    // ⚠️ target 尺寸必须由调用方显式给出正数。LetterboxParams 已自带 640 默认值，此处再
    //    兜一次会把「传 0」静默变成 640×640，且**不回写** inout_params —— 调用方读到的
    //    target_* 与实际张量尺寸不一致，据此换算的后处理会错位。
    const int target_w = inout_params.target_width;
    const int target_h = inout_params.target_height;
    if (target_w <= 0 || target_h <= 0) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }
    // 行偏移按 int 计算，target_w * 3 必须能用 int 表示。
    if (target_w > std::numeric_limits<int>::max() / 3) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }

    // 仅 packed 路径直接使用 src_stride；YUV 各平面的 stride 由 describe_yuv_layout 校验。
    int src_stride = src.strides[0];
    if (src_is_packed) {
        const int64_t min_src_stride = static_cast<int64_t>(src.width) * 3;
        if (min_src_stride > std::numeric_limits<int>::max()) {
            return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
        }
        if (src_stride == 0) src_stride = static_cast<int>(min_src_stride);
        if (src_stride < min_src_stride) {
            return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
        }
    }

    // ⚠️ 全部入参校验（含 YUV 平面布局）必须在写入 dst / 回填几何**之前**完成：
    //    失败路径不得改写调用方的 out 参数，否则调用方会拿到「新张量 + 已回填几何 + 错误码」
    //    这种半成品状态，后续任何一次误用都是静默错图。
    YuvLayout layout{};
    if (src_is_yuv) {
        const zhulong_status_t layout_status = describe_yuv_layout(src, layout);
        if (layout_status != ZHULONG_PLUGIN_OK) {
            return layout_status;
        }
    }

    // 几何计算提取到 compute_letterbox_geometry()，与 RGA 后端共用同一套语义。
    const LetterboxGeometry geometry = compute_letterbox_geometry(src.width, src.height, target_w, target_h);

    // target_h * target_w * 3 必须能用 size_t 表示（只校验 target_w 在 32 位 host 上不够）。
    const size_t row_bytes = static_cast<size_t>(target_w) * 3;
    if (static_cast<size_t>(target_h) > std::numeric_limits<size_t>::max() / row_bytes) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }
    const size_t required_bytes = static_cast<size_t>(target_h) * row_bytes;

    const std::vector<int64_t> target_shape{target_h, target_w, 3};

    // 复用/重建决策与 RGA 后端共用同一份实现（letterbox_target.hpp）：两个后端对
    // 「什么算可复用」必须给出同一答案，否则同一调用在 RGA 与 CPU 下的缓冲区所有权不同。
    const zhulong_status_t prepare_status =
        prepare_letterbox_target(dst, target_shape, required_bytes);
    if (prepare_status != ZHULONG_PLUGIN_OK) {
        return prepare_status;
    }

    uint8_t* dst_buf = dst.data<uint8_t>();
    // Letterbox 补边值为 letterbox_pad_value（非 0），与训练预处理保持一致
    std::memset(dst_buf, letterbox_pad_value, required_bytes);

    // 缩放直接写到最终 Tensor ROI（内容区），不分配中间 resized_temp
    const int target_stride = target_w * 3;
    uint8_t* resized_region =
        dst_buf + (static_cast<size_t>(geometry.pad_top) * target_w + geometry.pad_left) * 3;

    const zhulong_status_t rc =
        src_is_packed
            ? resize(src.planes[0], src.width, src.height, src_stride, src.format, resized_region,
                     target_stride, geometry.unpad_w, geometry.unpad_h, inout_params.target_format)
            : resize_yuv(src, layout, resized_region, target_stride, geometry.unpad_w,
                         geometry.unpad_h, inout_params.target_format);
    if (rc != ZHULONG_PLUGIN_OK) {
        return rc;
    }

    // ⚠️ 几何回填放在最后：后处理必须用这些值把归一化坐标反算回原图，
    //    任何失败路径都不得让调用方读到「看似已回填」的 scale/pad。
    inout_params.scale = geometry.scale;
    inout_params.pad_left = geometry.pad_left;
    inout_params.pad_top = geometry.pad_top;
    return ZHULONG_PLUGIN_OK;
}

} // namespace zhulong
