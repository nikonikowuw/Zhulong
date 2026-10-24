#include "plate_detector.hpp"
#include <algorithm>
#include <cmath>
#include <cstring>

namespace zhulong::plate {

namespace {

float compute_iou(const std::array<float, 4>& a, const std::array<float, 4>& b) {
    float x1 = std::max(a[0], b[0]);
    float y1 = std::max(a[1], b[1]);
    float x2 = std::min(a[2], b[2]);
    float y2 = std::min(a[3], b[3]);
    float w = std::max(0.0f, x2 - x1);
    float h = std::max(0.0f, y2 - y1);
    float inter = w * h;
    float area_a = std::max(0.0f, a[2] - a[0]) * std::max(0.0f, a[3] - a[1]);
    float area_b = std::max(0.0f, b[2] - b[0]) * std::max(0.0f, b[3] - b[1]);
    float union_area = area_a + area_b - inter;
    return (union_area <= 0.0f) ? 0.0f : (inter / union_area);
}

} // namespace

std::vector<PlateBoxDetection> PlateDetector::detect(zhulong::Session& session, const zhulong::Image& image) {
    const auto& raw = image.raw_view();
    if (raw.planes[0] == nullptr || raw.width <= 0 || raw.height <= 0) {
        return {};
    }

    if (!session.valid()) {
        return {};
    }

    // 1. 前处理 Letterbox
    zhulong::Tensor input_tensor;
    zhulong::LetterboxParams params;
    params.target_width = 640;
    params.target_height = 640;
    params.target_format = ZHULONG_PIXEL_FORMAT_RGB24;

    zhulong_status_t rc = zhulong::ops::letterbox(image, input_tensor, params);
    if (rc != ZHULONG_PLUGIN_OK) {
        return {};
    }

    // 2. 前向推理（ONNX 模型需要归一化为 [1, 3, 640, 640] FLOAT32 NCHW）
    std::vector<zhulong::Tensor> outputs;
    if (is_onnx_) {
        constexpr int model_w = 640;
        constexpr int model_h = 640;
        zhulong::Tensor onnx_input({1, 3, model_h, model_w}, zhulong::DataType::FLOAT32);
        const uint8_t* src_hwc = input_tensor.data<uint8_t>();
        float* dst_nchw = onnx_input.data<float>();
        if (!src_hwc || !dst_nchw) {
            return {};
        }
        constexpr int plane_size = model_w * model_h;
        float* dst_r = dst_nchw;
        float* dst_g = dst_nchw + plane_size;
        float* dst_b = dst_nchw + plane_size * 2;
        for (int i = 0; i < plane_size; ++i) {
            dst_r[i] = static_cast<float>(src_hwc[i * 3 + 0]) / 255.0f;
            dst_g[i] = static_cast<float>(src_hwc[i * 3 + 1]) / 255.0f;
            dst_b[i] = static_cast<float>(src_hwc[i * 3 + 2]) / 255.0f;
        }
        outputs = session.forward({onnx_input});
    } else {
        outputs = session.forward({input_tensor});
    }

    if (outputs.empty() || outputs[0].element_count() == 0) {
        return {};
    }

    // 3. 解析 YOLOv8 输出 (预期形状为 [1, 6, 8400] 或 [1, 8400, 6])
    const auto& out_tensor = outputs[0];
    if (out_tensor.element_count() < 8400 * 6) {
        return {};
    }

    const float* data = out_tensor.data<float>();
    if (!data) {
        return {};
    }

    const auto& shape = out_tensor.shape();
    int num_anchors = 8400;
    int channels = 6;
    bool is_chw = true;

    if (shape.size() >= 3) {
        if (shape[1] == 6 && shape[2] > 0) {
            channels = 6;
            num_anchors = shape[2];
            is_chw = true;
        } else if (shape[1] > 0 && shape[2] == 6) {
            num_anchors = shape[1];
            channels = 6;
            is_chw = false;
        }
    }

    std::vector<PlateBoxDetection> candidates;
    candidates.reserve(32);

    const float orig_w = static_cast<float>(raw.width);
    const float orig_h = static_cast<float>(raw.height);

    for (int i = 0; i < num_anchors; ++i) {
        float cx = is_chw ? data[0 * num_anchors + i] : data[i * channels + 0];
        float cy = is_chw ? data[1 * num_anchors + i] : data[i * channels + 1];
        float w  = is_chw ? data[2 * num_anchors + i] : data[i * channels + 2];
        float h  = is_chw ? data[3 * num_anchors + i] : data[i * channels + 3];
        float s0 = is_chw ? data[4 * num_anchors + i] : data[i * channels + 4];
        float s1 = is_chw ? data[5 * num_anchors + i] : data[i * channels + 5];

        float score = std::max(s0, s1);
        int class_id = (s1 > s0) ? 1 : 0;

        if (score < conf_threshold_) {
            continue;
        }

        // 还原 letterbox 坐标
        float x1 = (cx - w * 0.5f - params.pad_left) / params.scale;
        float y1 = (cy - h * 0.5f - params.pad_top)  / params.scale;
        float x2 = (cx + w * 0.5f - params.pad_left) / params.scale;
        float y2 = (cy + h * 0.5f - params.pad_top)  / params.scale;

        PlateBoxDetection d;
        d.box[0] = std::clamp(x1 / orig_w, 0.0f, 1.0f);
        d.box[1] = std::clamp(y1 / orig_h, 0.0f, 1.0f);
        d.box[2] = std::clamp(x2 / orig_w, 0.0f, 1.0f);
        d.box[3] = std::clamp(y2 / orig_h, 0.0f, 1.0f);
        d.score = score;
        d.class_id = class_id;

        if (d.box[2] > d.box[0] && d.box[3] > d.box[1]) {
            candidates.push_back(d);
        }
    }

    if (candidates.empty()) {
        return {};
    }

    // 4. 按置信度降序排序
    std::sort(candidates.begin(), candidates.end(), [](const PlateBoxDetection& a, const PlateBoxDetection& b) {
        return a.score > b.score;
    });

    // 5. NMS 过滤
    std::vector<PlateBoxDetection> kept;
    std::vector<bool> suppressed(candidates.size(), false);

    for (size_t i = 0; i < candidates.size(); ++i) {
        if (suppressed[i]) continue;
        kept.push_back(candidates[i]);
        for (size_t j = i + 1; j < candidates.size(); ++j) {
            if (suppressed[j]) continue;
            if (compute_iou(candidates[i].box, candidates[j].box) > nms_threshold_) {
                suppressed[j] = true;
            }
        }
    }

    return kept;
}


} // namespace zhulong::plate
