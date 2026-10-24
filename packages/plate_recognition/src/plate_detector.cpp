#include "plate_detector.hpp"

#include <algorithm>
#include <cmath>
#include <cstddef>
#include <vector>

namespace zhulong::plate {

namespace {

/// 检测模型输入边长：该权重导出固定 640×640（anchors 数由输出形状反推，不写死 8400）
constexpr int kModelInputSize = 640;

/// 输出通道数上限：4 个框分量 + 类别分；超出即判定该张量不是检测头输出
constexpr int64_t kMaxOutputChannels = 256;

/**
 * @brief YOLOv8 检测头输出的形状解释
 */
struct OutputLayout {
    bool valid{false};
    int channels{0};          /**< 4 + 类别数 */
    int num_anchors{0};       /**< 候选框数（640 输入通常是 8400，但随输入尺寸变化） */
    bool channel_first{true}; /**< true: [C, N]；false: [N, C] */
};

/**
 * @brief 从输出形状推断布局，并区分「检测头输出」与「其他张量」
 *
 * 支持 [1, C, N] / [1, N, C] / [C, N] / [N, C]（忽略大小为 1 的 batch/squeeze 维）。
 * 通道数必然是较小的那一维（anchor 数在 640 输入下 ≥ 2100），因此两维都小于等于
 * kMaxOutputChannels（例如桩后端的 [1, 6]）或都大于它时一律判为非法。
 */
OutputLayout resolve_output_layout(const std::vector<int64_t>& shape) {
    OutputLayout layout;

    std::vector<int64_t> dims;
    dims.reserve(shape.size());
    for (const int64_t dim : shape) {
        if (dim != 1) dims.push_back(dim);
    }
    if (dims.size() != 2 || dims[0] <= 0 || dims[1] <= 0) {
        return layout;
    }

    const int64_t first = dims[0];
    const int64_t second = dims[1];
    if (first <= kMaxOutputChannels && second > kMaxOutputChannels) {
        layout.channels = static_cast<int>(first);
        layout.num_anchors = static_cast<int>(second);
        layout.channel_first = true;
    } else if (second <= kMaxOutputChannels && first > kMaxOutputChannels) {
        layout.channels = static_cast<int>(second);
        layout.num_anchors = static_cast<int>(first);
        layout.channel_first = false;
    } else {
        return layout;
    }
    layout.valid = true;
    return layout;
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

    // 1. 前处理：等比缩放 + 居中补边，统一到 RGB24（与训练侧的 BGR→RGB 翻转一致）
    zhulong::Tensor letterboxed;
    zhulong::LetterboxParams params;
    params.target_width = kModelInputSize;
    params.target_height = kModelInputSize;
    params.target_format = ZHULONG_PIXEL_FORMAT_RGB24;

    if (zhulong::ops::letterbox(image, letterboxed, params) != ZHULONG_PLUGIN_OK) {
        return {};
    }
    if (letterboxed.element_count() < static_cast<size_t>(kModelInputSize) * kModelInputSize * 3) {
        return {};
    }

    // 2. 前向推理：ONNX 导出模型吃 [1, 3, H, W] FLOAT32 NCHW 归一化输入；
    //    NPU 后端吃 letterbox 的 HWC 原始像素张量。
    std::vector<zhulong::Tensor> outputs;
    if (is_onnx_) {
        zhulong::Tensor model_input({1, 3, kModelInputSize, kModelInputSize}, zhulong::DataType::FLOAT32);
        const uint8_t* source = letterboxed.data<uint8_t>();
        float* destination = model_input.data<float>();
        if (source == nullptr || destination == nullptr) {
            return {};
        }
        // ⚠️ 每帧分配 ~4.9MB（640×640×3×4B）后做一次 HWC→CHW 重排。检测器可被多个
        //    Worker 线程并发调用，因此这些缓冲必须是调用栈局部量：一旦提成成员变量
        //    就会在多 Worker 下互相覆盖（Session 才是线程独占的最小单位）。
        constexpr int kPlane = kModelInputSize * kModelInputSize;
        float* plane_r = destination;
        float* plane_g = destination + kPlane;
        float* plane_b = destination + kPlane * 2;
        for (int i = 0; i < kPlane; ++i) {
            plane_r[i] = static_cast<float>(source[i * 3 + 0]) / 255.0f;
            plane_g[i] = static_cast<float>(source[i * 3 + 1]) / 255.0f;
            plane_b[i] = static_cast<float>(source[i * 3 + 2]) / 255.0f;
        }
        outputs = session.forward({model_input});
    } else {
        outputs = session.forward({letterboxed});
    }

    if (outputs.empty() || outputs[0].element_count() == 0) {
        return {};
    }

    // 3. 解析检测头输出：布局与 anchors 数全部来自形状，类别数 = channels - 4
    const auto& output = outputs[0];
    const OutputLayout layout = resolve_output_layout(output.shape());
    if (!layout.valid || layout.channels < 5) {
        return {}; // 不是 YOLOv8 检测头输出（例如推理后端返回了别的张量）
    }
    if (output.element_count() < static_cast<size_t>(layout.channels) * layout.num_anchors) {
        return {};
    }
    const float* data = output.data<float>();
    if (data == nullptr) {
        return {};
    }

    const float scale = params.scale > 0.0f ? params.scale : 1.0f;
    const float pad_left = params.pad_left;
    const float pad_top = params.pad_top;
    const float orig_w = static_cast<float>(raw.width);
    const float orig_h = static_cast<float>(raw.height);

    std::vector<zhulong::Detection> candidates;
    candidates.reserve(32);

    for (int i = 0; i < layout.num_anchors; ++i) {
        const auto at = [&](int channel) -> float {
            return layout.channel_first
                       ? data[static_cast<size_t>(channel) * layout.num_anchors + i]
                       : data[static_cast<size_t>(i) * layout.channels + channel];
        };

        float score = at(4);
        int best_channel = 4;
        for (int channel = 5; channel < layout.channels; ++channel) {
            const float value = at(channel);
            if (value > score) {
                score = value;
                best_channel = channel;
            }
        }
        // 写成 !(x >= t) 以让 NaN 落进「丢弃」分支，而不是静默通过阈值
        if (!(score >= conf_threshold_)) {
            continue;
        }

        const float cx = at(0);
        const float cy = at(1);
        const float w = at(2);
        const float h = at(3);

        zhulong::Detection detection;
        detection.box[0] = std::clamp((cx - w * 0.5f - pad_left) / scale / orig_w, 0.0f, 1.0f);
        detection.box[1] = std::clamp((cy - h * 0.5f - pad_top) / scale / orig_h, 0.0f, 1.0f);
        detection.box[2] = std::clamp((cx + w * 0.5f - pad_left) / scale / orig_w, 0.0f, 1.0f);
        detection.box[3] = std::clamp((cy + h * 0.5f - pad_top) / scale / orig_h, 0.0f, 1.0f);
        if (!(detection.box[2] > detection.box[0]) || !(detection.box[3] > detection.box[1])) {
            continue;
        }
        detection.confidence = score;
        detection.class_id = best_channel - 4; // 0=单层, 1=双层, 2=新能源（由模型类别决定）
        candidates.push_back(std::move(detection));
    }

    if (candidates.empty()) {
        return {};
    }

    // 4. NMS：复用平台算子，避免在算法包里维护第二份 IoU/抑制实现（语义与原实现一致：
    //    按置信度降序、跨类别抑制、O(n^2)）。
    const std::vector<zhulong::Detection> kept = zhulong::ops::nms(candidates, nms_threshold_);

    std::vector<PlateBoxDetection> results;
    results.reserve(kept.size());
    for (const auto& detection : kept) {
        PlateBoxDetection box;
        for (int k = 0; k < 4; ++k) {
            box.box[k] = detection.box[k];
        }
        box.score = detection.confidence;
        box.class_id = detection.class_id;
        results.push_back(box);
    }
    return results;
}

} // namespace zhulong::plate
