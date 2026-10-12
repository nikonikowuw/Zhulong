#include "cpu_vision_backend.hpp"
#include <algorithm>
#include <cmath>
#include <cstring>

namespace zhulong {

zhulong_status_t CpuVisionBackend::resize(const uint8_t* src, int src_w, int src_h, int channels,
                                          uint8_t* dst, int dst_w, int dst_h) {
    if (!src || !dst || src_w <= 0 || src_h <= 0 || dst_w <= 0 || dst_h <= 0 || channels <= 0) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }

    float x_ratio = static_cast<float>(src_w) / static_cast<float>(dst_w);
    float y_ratio = static_cast<float>(src_h) / static_cast<float>(dst_h);

    for (int y = 0; y < dst_h; ++y) {
        int src_y = std::min(static_cast<int>(y * y_ratio), src_h - 1);
        for (int x = 0; x < dst_w; ++x) {
            int src_x = std::min(static_cast<int>(x * x_ratio), src_w - 1);
            const uint8_t* src_pixel = src + (src_y * src_w + src_x) * channels;
            uint8_t* dst_pixel = dst + (y * dst_w + x) * channels;
            for (int c = 0; c < channels; ++c) {
                dst_pixel[c] = src_pixel[c];
            }
        }
    }
    return ZHULONG_PLUGIN_OK;
}

zhulong_status_t CpuVisionBackend::letterbox(const zhulong_frame_view& src,
                                             Tensor& dst,
                                             LetterboxParams& inout_params) {
    if (!src.planes[0] || src.width <= 0 || src.height <= 0) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }

    int target_w = inout_params.target_width > 0 ? inout_params.target_width : 640;
    int target_h = inout_params.target_height > 0 ? inout_params.target_height : 640;

    float r = std::min(static_cast<float>(target_w) / static_cast<float>(src.width),
                       static_cast<float>(target_h) / static_cast<float>(src.height));

    int unpad_w = static_cast<int>(std::round(src.width * r));
    int unpad_h = static_cast<int>(std::round(src.height * r));

    int dw = target_w - unpad_w;
    int dh = target_h - unpad_h;

    int pad_left = dw / 2;
    int pad_top = dh / 2;

    inout_params.scale = r;
    inout_params.pad_left = pad_left;
    inout_params.pad_top = pad_top;

    // 分配输出张量 [1, 3, target_h, target_w] 或 [target_h, target_w, 3]
    dst = Tensor({target_h, target_w, 3}, DataType::UINT8);
    uint8_t* dst_buf = dst.data<uint8_t>();
    // 默认填充 114 (YOLO 经典灰色)
    std::memset(dst_buf, 114, target_w * target_h * 3);

    // 双线性缩放有效区域并复制
    std::vector<uint8_t> resized_temp(unpad_w * unpad_h * 3);
    zhulong_status_t rc = resize(src.planes[0], src.width, src.height, 3,
                                 resized_temp.data(), unpad_w, unpad_h);
    if (rc != ZHULONG_PLUGIN_OK) {
        return rc;
    }

    // 拷贝至居中位置
    for (int y = 0; y < unpad_h; ++y) {
        const uint8_t* src_row = resized_temp.data() + y * unpad_w * 3;
        uint8_t* dst_row = dst_buf + ((y + pad_top) * target_w + pad_left) * 3;
        std::memcpy(dst_row, src_row, unpad_w * 3);
    }

    return ZHULONG_PLUGIN_OK;
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
