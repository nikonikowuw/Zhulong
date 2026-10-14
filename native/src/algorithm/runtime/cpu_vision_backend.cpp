#include "cpu_vision_backend.hpp"
#include "letterbox_geometry.hpp"
#include <algorithm>
#include <cmath>
#include <cstring>
#include <limits>

namespace zhulong {
namespace {

bool is_packed_24bit(zhulong_pixel_format_t format) {
    return format == ZHULONG_PIXEL_FORMAT_RGB24 || format == ZHULONG_PIXEL_FORMAT_BGR24;
}

} // namespace

zhulong_status_t CpuVisionBackend::resize(const uint8_t* src, int src_w, int src_h, int src_stride,
                                          zhulong_pixel_format_t src_format, uint8_t* dst, int dst_stride,
                                          int dst_w, int dst_h, zhulong_pixel_format_t dst_format) {
    if (!src || !dst || src_w <= 0 || src_h <= 0 || dst_w <= 0 || dst_h <= 0) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }
    if (!is_packed_24bit(src_format) || !is_packed_24bit(dst_format)) {
        return ZHULONG_PLUGIN_ERR_NOT_SUPPORTED;
    }

    const int64_t min_src_stride = static_cast<int64_t>(src_w) * 3;
    const int64_t min_dst_stride = static_cast<int64_t>(dst_w) * 3;
    if (min_src_stride > std::numeric_limits<int>::max() || min_dst_stride > std::numeric_limits<int>::max()) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }
    if (src_stride == 0) src_stride = static_cast<int>(min_src_stride);
    if (dst_stride == 0) dst_stride = static_cast<int>(min_dst_stride);
    if (src_stride < min_src_stride || dst_stride < min_dst_stride) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }

    const bool swap_channels = src_format != dst_format;
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
    if (!is_packed_24bit(src.format) || !is_packed_24bit(inout_params.target_format)) {
        return ZHULONG_PLUGIN_ERR_NOT_SUPPORTED;
    }
    if (!src.planes[0] || src.width <= 0 || src.height <= 0) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }

    const int64_t min_src_stride = static_cast<int64_t>(src.width) * 3;
    if (min_src_stride > std::numeric_limits<int>::max()) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }
    int src_stride = src.strides[0];
    if (src_stride == 0) src_stride = static_cast<int>(min_src_stride);
    if (src_stride < min_src_stride) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
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

    inout_params.scale = geometry.scale;
    inout_params.pad_left = pad_left;
    inout_params.pad_top = pad_top;

    dst = Tensor({target_h, target_w, 3}, DataType::UINT8);
    uint8_t* dst_buf = dst.data<uint8_t>();
    std::memset(dst_buf, 114, dst.byte_size());

    const int target_stride = target_w * 3;
    uint8_t* resized_region = dst_buf + (static_cast<size_t>(pad_top) * target_w + pad_left) * 3;
    return resize(src.planes[0], src.width, src.height, src_stride, src.format,
                  resized_region, target_stride, unpad_w, unpad_h, inout_params.target_format);
}

static float calc_iou(const zhulong_bbox& a, const zhulong_bbox& b) {
    float x1 = std::max(a.x, b.x);
    float y1 = std::max(a.y, b.y);
    float x2 = std::min(a.x + a.width, b.x + b.width);
    float y2 = std::min(a.y + a.height, b.y + b.height);

    float inter_w = std::max(0.0f, x2 - x1);
    float inter_h = std::max(0.0f, y2 - y1);
    float inter_area = inter_w * inter_h;

    float area_a = a.width * a.height;
    float area_b = b.width * b.height;
    float union_area = area_a + area_b - inter_area;

    if (union_area <= 0.0f) return 0.0f;
    return inter_area / union_area;
}

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
