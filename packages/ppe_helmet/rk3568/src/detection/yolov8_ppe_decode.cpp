#include "detection/yolov8_ppe_decode.hpp"

#include <algorithm>
#include <cmath>
#include <cstddef>

namespace zhulong::ppe {
namespace {

/** @brief DFL 每个方向的分箱数（64 通道 ÷ 4 边） */
constexpr int kDflBins = 16;
/** @brief 候选上限：避免异常模型输出把内存顶穿，超出后按置信度截断 */
constexpr size_t kMaxCandidates = 2048;
/** @brief 单帧输出上限（按类 NMS 之后） */
constexpr size_t kMaxDetections = 128;

float sigmoid(float value) noexcept {
    return 1.0f / (1.0f + std::exp(-value));
}

/** @brief 对 16 个 DFL logits 做 softmax 期望，得到该方向到框边的离散距离 */
float dfl_expectation(const float* logits, int count) noexcept {
    float max_logit = logits[0];
    for (int k = 1; k < count; ++k) {
        max_logit = std::max(max_logit, logits[k]);
    }
    float weight_sum = 0.0f;
    float weighted_sum = 0.0f;
    for (int k = 0; k < count; ++k) {
        const float weight = std::exp(logits[k] - max_logit);
        weight_sum += weight;
        weighted_sum += weight * static_cast<float>(k);
    }
    return weight_sum > 0.0f ? weighted_sum / weight_sum : 0.0f;
}

float box_iou(const float* a, const float* b) noexcept {
    const float inter_w = std::min(a[2], b[2]) - std::max(a[0], b[0]);
    const float inter_h = std::min(a[3], b[3]) - std::max(a[1], b[1]);
    if (inter_w <= 0.0f || inter_h <= 0.0f) {
        return 0.0f;
    }
    const float intersection = inter_w * inter_h;
    const float area_a = (a[2] - a[0]) * (a[3] - a[1]);
    const float area_b = (b[2] - b[0]) * (b[3] - b[1]);
    const float uni = area_a + area_b - intersection;
    return uni > 0.0f ? intersection / uni : 0.0f;
}

} // namespace

const char* ppe_class_label(int32_t class_id) noexcept {
    switch (class_id) {
        case kClassHeadHelmet: return "head_helmet";
        case kClassHeadNoHelmet: return "head_nohelmet";
        case kClassPerson: return "person";
        default: return "object";
    }
}

std::vector<zhulong::Detection> decode_yolov8_ppe(
    const std::vector<Yolov8PpeHeadView>& heads,
    const Yolov8PpeGeometry& geometry,
    float score_threshold,
    float nms_threshold,
    std::vector<zhulong::Detection>& scratch) {
    scratch.clear();
    if (!(geometry.scale > 0.0f) || geometry.image_width <= 0 || geometry.image_height <= 0) {
        return {};
    }

    const float image_width = static_cast<float>(geometry.image_width);
    const float image_height = static_cast<float>(geometry.image_height);
    const float inv_scale = 1.0f / geometry.scale;

    for (const auto& head : heads) {
        if (head.box == nullptr || head.score == nullptr || head.grid_h <= 0 || head.grid_w <= 0 ||
            head.stride_h <= 0 || head.stride_w <= 0) {
            continue;
        }
        const size_t plane = static_cast<size_t>(head.grid_h) * static_cast<size_t>(head.grid_w);
        for (int row = 0; row < head.grid_h; ++row) {
            for (int col = 0; col < head.grid_w; ++col) {
                const size_t anchor = static_cast<size_t>(row) * static_cast<size_t>(head.grid_w) +
                                      static_cast<size_t>(col);

                // 单标签解码：先取 17 类最大 logit，只有赢家属于目标三类才继续。
                // 否则一个 anchor 会同时吐出 person 和同位置的 head 框，平白增加关联歧义。
                int32_t best_class = 0;
                float best_logit = head.score[anchor];
                for (int32_t class_id = 1; class_id < kClassCount; ++class_id) {
                    const float logit =
                        head.score[static_cast<size_t>(class_id) * plane + anchor];
                    if (logit > best_logit) {
                        best_logit = logit;
                        best_class = class_id;
                    }
                }
                if (best_class != kClassPerson && best_class != kClassHeadHelmet &&
                    best_class != kClassHeadNoHelmet) {
                    continue;
                }
                if (!std::isfinite(best_logit)) {
                    continue;
                }
                const float score = sigmoid(best_logit);
                if (score < score_threshold) {
                    continue;
                }

                float dfl[4] = {0.0f, 0.0f, 0.0f, 0.0f};
                bool finite_dfl = true;
                for (int side = 0; side < 4; ++side) {
                    float bins[kDflBins] = {};
                    for (int k = 0; k < kDflBins; ++k) {
                        bins[k] = head.box[static_cast<size_t>(side * kDflBins + k) * plane + anchor];
                    }
                    dfl[side] = dfl_expectation(bins, kDflBins);
                    if (!std::isfinite(dfl[side])) {
                        finite_dfl = false;
                        break;
                    }
                }
                if (!finite_dfl) {
                    continue;
                }

                // 与 python demo 一致：stride_w / stride_h 独立计算，不能用模型高/宽混算。
                const float center_x = static_cast<float>(col) + 0.5f;
                const float center_y = static_cast<float>(row) + 0.5f;
                float x1 = (center_x - dfl[0]) * static_cast<float>(head.stride_w);
                float y1 = (center_y - dfl[1]) * static_cast<float>(head.stride_h);
                float x2 = (center_x + dfl[2]) * static_cast<float>(head.stride_w);
                float y2 = (center_y + dfl[3]) * static_cast<float>(head.stride_h);

                // letterbox -> 原图；pad/scale 来自 ops::letterbox 的回填值
                x1 = (x1 - static_cast<float>(geometry.pad_left)) * inv_scale;
                y1 = (y1 - static_cast<float>(geometry.pad_top)) * inv_scale;
                x2 = (x2 - static_cast<float>(geometry.pad_left)) * inv_scale;
                y2 = (y2 - static_cast<float>(geometry.pad_top)) * inv_scale;
                if (!std::isfinite(x1) || !std::isfinite(y1) || !std::isfinite(x2) ||
                    !std::isfinite(y2)) {
                    continue;
                }
                x1 = std::clamp(x1, 0.0f, image_width);
                y1 = std::clamp(y1, 0.0f, image_height);
                x2 = std::clamp(x2, 0.0f, image_width);
                y2 = std::clamp(y2, 0.0f, image_height);
                if (x2 - x1 < 1.0f || y2 - y1 < 1.0f) {
                    continue;
                }

                zhulong::Detection detection;
                detection.box[0] = x1 / image_width;
                detection.box[1] = y1 / image_height;
                detection.box[2] = x2 / image_width;
                detection.box[3] = y2 / image_height;
                detection.confidence = score;
                detection.class_id = best_class;
                detection.label = ppe_class_label(best_class);
                scratch.push_back(std::move(detection));
            }
        }
    }

    if (scratch.size() > kMaxCandidates) {
        std::nth_element(scratch.begin(), scratch.begin() + static_cast<std::ptrdiff_t>(kMaxCandidates),
                         scratch.end(), [](const zhulong::Detection& a, const zhulong::Detection& b) {
                             return a.confidence > b.confidence;
                         });
        scratch.resize(kMaxCandidates);
    }

    // 按类分组 NMS：SDK 的 ops::nms 是全局跨类抑制，会把框内的头部框一起压掉。
    std::stable_sort(scratch.begin(), scratch.end(),
                     [](const zhulong::Detection& a, const zhulong::Detection& b) {
                         return a.confidence > b.confidence;
                     });
    std::vector<zhulong::Detection> kept;
    kept.reserve(std::min(scratch.size(), kMaxDetections));
    std::vector<uint8_t> suppressed(scratch.size(), 0);
    for (size_t i = 0; i < scratch.size() && kept.size() < kMaxDetections; ++i) {
        if (suppressed[i] != 0) {
            continue;
        }
        kept.push_back(scratch[i]);
        for (size_t j = i + 1; j < scratch.size(); ++j) {
            if (suppressed[j] != 0 || scratch[j].class_id != scratch[i].class_id) {
                continue;
            }
            if (box_iou(scratch[i].box, scratch[j].box) > nms_threshold) {
                suppressed[j] = 1;
            }
        }
    }
    return kept;
}

} // namespace zhulong::ppe
