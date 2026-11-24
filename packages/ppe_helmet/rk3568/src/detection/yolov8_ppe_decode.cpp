#include "detection/yolov8_ppe_decode.hpp"

#include "common/box_geometry.hpp"

#include <algorithm>
#include <cmath>
#include <cstddef>

namespace zhulong::ppe {
namespace {

/** @brief 对 17 个 DFL logits 做 softmax 期望，得到该方向到框边的离散距离 */
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

} // namespace

const char* ppe_class_label(int32_t class_id) noexcept {
    switch (class_id) {
        case kClassHeadHelmet: return "head_helmet";
        case kClassHeadNoHelmet: return "head_nohelmet";
        default: return "object";
    }
}

void decode_yolov8_ppe(const std::vector<Yolov8PpeHeadView>& heads,
                       const Yolov8PpeGeometry& geometry,
                       float score_threshold,
                       float nms_threshold,
                       PpeDecodeBuffers& buffers) {
    std::vector<zhulong::Detection>& candidates = buffers.candidates;
    std::vector<zhulong::Detection>& detections = buffers.detections;
    candidates.clear();
    detections.clear();
    if (!(geometry.scale > 0.0f) || geometry.image_width <= 0 || geometry.image_height <= 0) {
        return;
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

                // DAMO-YOLO ZeroHead 导出头 cls_* 已经过 sigmoid，直接取两类最大概率。
                int32_t best_class = 0;
                float best_score = head.score[anchor];
                for (int32_t class_id = 1; class_id < kClassCount; ++class_id) {
                    const float candidate_score =
                        head.score[static_cast<size_t>(class_id) * plane + anchor];
                    if (candidate_score > best_score) {
                        best_score = candidate_score;
                        best_class = class_id;
                    }
                }
                if (!std::isfinite(best_score) || best_score < score_threshold) {
                    continue;
                }
                const float score = best_score;

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

                // DAMO-YOLO ZeroHead 直接以网格点 (col, row) * stride 为锚点（无 +0.5 偏移）。
                const float center_x = static_cast<float>(col);
                const float center_y = static_cast<float>(row);
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
                candidates.push_back(std::move(detection));
            }
        }
    }

    if (candidates.size() > kPpeMaxCandidates) {
        std::nth_element(candidates.begin(),
                         candidates.begin() + static_cast<std::ptrdiff_t>(kPpeMaxCandidates),
                         candidates.end(),
                         [](const zhulong::Detection& a, const zhulong::Detection& b) {
                             return a.confidence > b.confidence;
                         });
        candidates.resize(kPpeMaxCandidates);
    }

    // 按类分组 NMS：SDK 的 ops::nms 是全局跨类抑制，会把框内的头部框一起压掉。
    std::stable_sort(candidates.begin(), candidates.end(),
                     [](const zhulong::Detection& a, const zhulong::Detection& b) {
                         return a.confidence > b.confidence;
                     });
    std::vector<uint8_t>& suppressed = buffers.suppressed;
    suppressed.assign(candidates.size(), 0);
    detections.reserve(std::min(candidates.size(), kPpeMaxDetections));
    for (size_t i = 0; i < candidates.size() && detections.size() < kPpeMaxDetections; ++i) {
        if (suppressed[i] != 0) {
            continue;
        }
        // 标签都短于 std::string 的 SSO 上限，逐条拷贝不产生堆分配。
        detections.push_back(candidates[i]);
        for (size_t j = i + 1; j < candidates.size(); ++j) {
            if (suppressed[j] != 0 || candidates[j].class_id != candidates[i].class_id) {
                continue;
            }
            if (box_iou(candidates[i].box, candidates[j].box) > nms_threshold) {
                suppressed[j] = 1;
            }
        }
    }
}

} // namespace zhulong::ppe
