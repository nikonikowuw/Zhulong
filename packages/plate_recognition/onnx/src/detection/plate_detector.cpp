#include "plate_detector.hpp"

#include "common/runner_log.hpp"

#include <algorithm>
#include <array>
#include <cmath>
#include <cstddef>
#include <cstdio>
#include <string_view>
#include <utility>
#include <vector>

namespace zhulong::plate {

namespace {

/// 检测模型输入尺寸：16:9 CCTV 档位固定 640×384（anchors 数由输出形状反推，不写死 5040）
constexpr int kModelInputWidth = 640;
constexpr int kModelInputHeight = 384;

/// 输出通道数上限：4 个框分量 + 类别分；超出即判定该张量不是检测头输出
constexpr int64_t kMaxOutputChannels = 256;

/** @brief 256 项归一化查表：编译期生成，消除每帧 73 万次浮点除法并保证常驻 L1 缓存 */
inline constexpr auto kNormLut = []() constexpr {
    std::array<float, 256> lut{};
    for (std::size_t i = 0; i < 256; ++i) {
        lut[i] = static_cast<float>(i) / 255.0f;
    }
    return lut;
}();

/**
 * @brief NMS 前的候选上限
 *
 * `zhulong::ops::nms` 是逐框贪心的 O(N²)（`native/src/algorithm/runtime/detection_postprocess.cpp`）。
 * schema 允许把置信度阈值调到 0.1，而 640×384 输入有 5040 个锚点 —— 低阈值下活下来的候选
 * 足以让单帧 NMS 从微秒级涨到几十毫秒。留 top-K 与 ultralytics 的 `max_nms` 同构：
 * 只截候选池，不改变「被截掉的框本来也进不了结果」这一事实（final NMS 只留极少数）。
 */
constexpr std::size_t kMaxCandidatesBeforeNms = 300;

/**
 * @brief YOLOv8 检测头输出的形状解释
 */
struct OutputLayout {
    bool valid{false};
    int channels{0};          /**< 4 + 类别数 */
    int num_anchors{0};       /**< 候选框数（640×384 输入为 5040，随输入尺寸变化） */
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

    std::array<int64_t, 4> dims{};
    std::size_t num_dims = 0;
    for (const int64_t dim : shape) {
        if (dim != 1) {
            if (num_dims >= dims.size()) {
                return layout;
            }
            dims[num_dims++] = dim;
        }
    }
    if (num_dims != 2 || dims[0] <= 0 || dims[1] <= 0) {
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

/** @brief 把超限的类别数写成一次告警（宿主日志可解析，见 common/runner_log.hpp） */
void report_head_mismatch(int channels) noexcept {
    char detail[96];
    const int written = std::snprintf(detail, sizeof(detail), "channels=%d classes=%d", channels, channels - 4);
    log_warning("plate_recognition", "detection output is not a plate head",
                written > 0 ? std::string_view(detail) : std::string_view{});
}

} // namespace

std::vector<PlateBoxDetection> PlateDetector::detect(zhulong::Session& session,
                                                     const zhulong::Image& image) const {
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
    params.target_width = kModelInputWidth;
    params.target_height = kModelInputHeight;
    params.target_format = ZHULONG_PIXEL_FORMAT_RGB24;

    if (zhulong::ops::letterbox(image, letterboxed, params) != ZHULONG_PLUGIN_OK) {
        return {};
    }
    constexpr std::size_t kExpectedElements =
        static_cast<std::size_t>(kModelInputWidth) * static_cast<std::size_t>(kModelInputHeight) * 3;
    if (letterboxed.element_count() < kExpectedElements) {
        return {};
    }

    // 2. 前向推理：本 target 只有 ONNX 一条路径，模型吃 [1, 3, H, W] FLOAT32 NCHW 归一化输入。
    //    （另一个 target 的 RKNN 实现直接喂 letterbox 的 HWC 原始像素张量——两份实现
    //      互不引用，因此这里没有、也不允许出现运行期布局分支。）
    zhulong::Tensor model_input({1, 3, kModelInputHeight, kModelInputWidth}, zhulong::DataType::FLOAT32);
    const uint8_t* source = letterboxed.data<uint8_t>();
    float* destination = model_input.data<float>();
    if (source == nullptr || destination == nullptr) {
        return {};
    }
    // ⚠️ 每帧分配 ~2.95MB（640×384×3×4B）后做一次 HWC→CHW 重排。检测器可被多个
    //    Worker 线程并发调用，因此这些缓冲必须是调用栈局部量：一旦提成成员变量
    //    就会在多 Worker 下互相覆盖（Session 才是线程独占的最小单位）。
    constexpr int kPlane = kModelInputWidth * kModelInputHeight;
    float* plane_r = destination;
    float* plane_g = destination + kPlane;
    float* plane_b = destination + kPlane * 2;
    for (int i = 0; i < kPlane; ++i) {
        const std::size_t offset = static_cast<std::size_t>(i) * 3;
        plane_r[i] = kNormLut[source[offset + 0]];
        plane_g[i] = kNormLut[source[offset + 1]];
        plane_b[i] = kNormLut[source[offset + 2]];
    }
    std::vector<zhulong::Tensor> outputs = session.forward({model_input});

    if (outputs.empty() || outputs[0].element_count() == 0) {
        return {};
    }

    // 3. 解析检测头输出：布局与 anchors 数全部来自形状，类别数 = channels - 4
    const auto& output = outputs[0];
    const OutputLayout layout = resolve_output_layout(output.shape());
    if (!layout.valid || layout.channels < 5) {
        return {}; // 不是 YOLOv8 检测头输出（例如推理后端返回了别的张量）
    }
    // 类别数越界 = 加载的不是车牌检测头（如 80 类 COCO 权重）。继续按 (cx, cy, w, h) 解析
    // 会产出位置离谱、置信度却「看着正常」的框，比返回空危险得多，因此就地失败。
    if (layout.channels - 4 > kMaxPlateClasses) {
        if (!head_mismatch_reported_.exchange(true, std::memory_order_relaxed)) {
            report_head_mismatch(layout.channels);
        }
        return {};
    }
    if (output.element_count() < static_cast<std::size_t>(layout.channels) * static_cast<std::size_t>(layout.num_anchors)) {
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
    candidates.reserve(64);

    for (int i = 0; i < layout.num_anchors; ++i) {
        const auto at = [&](int channel) -> float {
            return layout.channel_first
                       ? data[static_cast<std::size_t>(channel) * static_cast<std::size_t>(layout.num_anchors) +
                              static_cast<std::size_t>(i)]
                       : data[static_cast<std::size_t>(i) * static_cast<std::size_t>(layout.channels) +
                              static_cast<std::size_t>(channel)];
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
        detection.class_id = best_channel - 4;
        candidates.push_back(std::move(detection));
    }

    if (candidates.empty()) {
        return {};
    }

    // 3.5 候选池截断：见 kMaxCandidatesBeforeNms。被截掉的框在 NMS 后本来也留不下
    //     （同一目标的高分框必然先被保留），这里只是不让 O(N²) 吃到全部 8400 个锚点。
    if (candidates.size() > kMaxCandidatesBeforeNms) {
        const auto keep = static_cast<std::ptrdiff_t>(kMaxCandidatesBeforeNms);
        std::partial_sort(candidates.begin(), candidates.begin() + keep, candidates.end(),
                          [](const zhulong::Detection& lhs, const zhulong::Detection& rhs) {
                              return lhs.confidence > rhs.confidence;
                          });
        candidates.resize(kMaxCandidatesBeforeNms);
    }

    // 4. NMS：复用平台算子，避免在算法包里维护第二份 IoU/抑制实现（语义与原实现一致：
    //    按置信度降序、跨类别抑制、O(n^2)）。
    const std::vector<zhulong::Detection> kept = zhulong::ops::nms(candidates, nms_threshold_);

    std::vector<PlateBoxDetection> results;
    results.reserve(kept.size());
    for (const auto& detection : kept) {
        PlateBoxDetection box;
        std::copy_n(detection.box, box.box.size(), box.box.begin());
        box.score = detection.confidence;
        box.kind = plate_kind_from_index(detection.class_id);
        results.push_back(box);
    }
    return results;
}

} // namespace zhulong::plate
