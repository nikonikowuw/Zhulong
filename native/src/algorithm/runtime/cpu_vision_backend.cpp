/**
 * @file cpu_vision_backend.cpp
 * @brief CPU 视觉前处理后端实现（stride 契约、双线性缩放与 Letterbox）
 */

#include "cpu_vision_backend.hpp"
#include "letterbox_geometry.hpp"
#include "yuv_sampling.hpp"
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
    layout.is_nv12 = (src.format == ZHULONG_PIXEL_FORMAT_NV12);
    layout.src_w = src.width;
    layout.src_h = src.height;

    const int chroma_min_width = (src.width + 1) / 2;
    if (layout.is_nv12) {
        if (!src.planes[0] || !src.planes[1]) return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
        layout.y_stride = src.strides[0] > 0 ? src.strides[0] : src.width;
        layout.chroma_stride_0 = src.strides[1] > 0 ? src.strides[1] : src.width;
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

    // RGB/BGR 目标顺序不一致时必须交换 R/B 通道
    const bool swap_channels = src_format != dst_format;
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

    int target_w = inout_params.target_width > 0 ? inout_params.target_width : 640;
    int target_h = inout_params.target_height > 0 ? inout_params.target_height : 640;
    if (target_w > std::numeric_limits<int>::max() / 3) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }

    // 几何计算提取到 compute_letterbox_geometry()，与 RGA 后端共用同一套语义。
    const LetterboxGeometry geometry = compute_letterbox_geometry(src.width, src.height, target_w, target_h);
    const int unpad_w = geometry.unpad_w;
    const int unpad_h = geometry.unpad_h;
    const int pad_left = geometry.pad_left;
    const int pad_top = geometry.pad_top;

    // ⚠️ 几何回填：后处理必须使用这些值将归一化坐标反算回原图
    inout_params.scale = geometry.scale;
    inout_params.pad_left = pad_left;
    inout_params.pad_top = pad_top;

    const size_t required_bytes = static_cast<size_t>(target_h) * target_w * 3;
    const std::vector<int64_t> target_shape{target_h, target_w, 3};
    const bool dma_reusable = (dst.dma_buf_fd() >= 0 && dst.raw_data() != nullptr &&
                               dst.byte_size() >= required_bytes);
    const bool cpu_reusable = (dst.dma_buf_fd() < 0 && dst.byte_size() >= required_bytes &&
                               dst.dtype() == DataType::UINT8 && dst.shape() == target_shape);

    if (dma_reusable) {
        if (dst.dtype() != DataType::UINT8 || dst.shape() != target_shape) {
            dst = Tensor::from_dma_buf(dst.dma_buf_fd(), target_shape, DataType::UINT8, dst.raw_data());
        }
    } else if (!cpu_reusable) {
        dst = Tensor(target_shape, DataType::UINT8);
    }
    uint8_t* dst_buf = dst.data<uint8_t>();
    if (!dst_buf) {
        return ZHULONG_PLUGIN_ERR_OUT_OF_MEMORY;
    }
    // Letterbox 补边值为 114（非 0），与训练预处理保持一致
    std::memset(dst_buf, 114, required_bytes);

    // 缩放直接写到最终 Tensor ROI（内容区），不分配中间 resized_temp
    const int target_stride = target_w * 3;
    uint8_t* resized_region = dst_buf + (static_cast<size_t>(pad_top) * target_w + pad_left) * 3;

    if (src_is_packed) {
        return resize(src.planes[0], src.width, src.height, src_stride, src.format,
                      resized_region, target_stride, unpad_w, unpad_h, inout_params.target_format);
    }

    YuvLayout layout{};
    const zhulong_status_t layout_status = describe_yuv_layout(src, layout);
    if (layout_status != ZHULONG_PLUGIN_OK) {
        return layout_status;
    }
    return resize_yuv(src, layout, resized_region, target_stride, unpad_w, unpad_h,
                      inout_params.target_format);
}

/**
 * @brief 基于归一化两点坐标 [x1,y1,x2,y2] 计算交并比（IoU）
 *
 * 使用 std::max(0.0f, ...) 防御越界或坐标倒置导致的负面积。
 */
static float calc_iou(const zhulong_bbox& a, const zhulong_bbox& b) {
    float x1 = std::max(a.box[0], b.box[0]);
    float y1 = std::max(a.box[1], b.box[1]);
    float x2 = std::min(a.box[2], b.box[2]);
    float y2 = std::min(a.box[3], b.box[3]);

    float inter_w = std::max(0.0f, x2 - x1);
    float inter_h = std::max(0.0f, y2 - y1);
    float inter_area = inter_w * inter_h;

    float area_a = std::max(0.0f, a.box[2] - a.box[0]) * std::max(0.0f, a.box[3] - a.box[1]);
    float area_b = std::max(0.0f, b.box[2] - b.box[0]) * std::max(0.0f, b.box[3] - b.box[1]);
    float union_area = area_a + area_b - inter_area;

    if (union_area <= 0.0f) return 0.0f;
    return inter_area / union_area;
}

/**
 * @brief 按置信度降序排序后逐框抑制的标量 NMS（非极大值抑制），最坏复杂度 O(n^2)
 *
 * ⚠️ 输入框必须为已归一化坐标 [x1, y1, x2, y2] ∈ [0.0, 1.0]。
 */
std::vector<zhulong_bbox> nms(const std::vector<zhulong_bbox>& boxes, float iou_threshold) {
    if (boxes.empty()) return {};

    std::vector<zhulong_bbox> sorted_boxes = boxes;
    std::sort(sorted_boxes.begin(), sorted_boxes.end(), [](const zhulong_bbox& a, const zhulong_bbox& b) {
        return a.confidence > b.confidence;
    });

    std::vector<bool> suppressed(sorted_boxes.size(), false);
    std::vector<zhulong_bbox> keep;

    for (size_t i = 0; i < sorted_boxes.size(); ++i) {
        if (suppressed[i]) continue;
        keep.push_back(sorted_boxes[i]);

        for (size_t j = i + 1; j < sorted_boxes.size(); ++j) {
            if (suppressed[j]) continue;
            if (calc_iou(sorted_boxes[i], sorted_boxes[j]) > iou_threshold) {
                suppressed[j] = true;
            }
        }
    }
    return keep;
}

} // namespace zhulong
