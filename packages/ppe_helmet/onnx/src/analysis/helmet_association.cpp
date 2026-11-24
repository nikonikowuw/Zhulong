#include "analysis/helmet_association.hpp"

#include "common/box_geometry.hpp"

#include <algorithm>
#include <array>
#include <cstddef>
#include <cstdint>

namespace zhulong::ppe {
namespace {

/** @brief 同一头部同时出现戴帽与未戴帽框时，判定为同一目标的 IoU 阈值 */
constexpr float kCrossClassConflictIou = 0.50f;

/**
 * @brief 头部框分数「打平」的容差
 *
 * 检测分数是连续浮点，两框恰好相等几乎不会发生；用严格 `==` 判定平票会让
 * 「平票偏 no_helmet」这条安全侧规则永远不生效。
 */
constexpr float kScoreTieEpsilon = 1e-4f;

} // namespace

void associate_helmets(const std::vector<zhulong::Detection>& detections,
                       float min_head_height_ratio,
                       std::vector<HelmetDecision>& decisions) {
    decisions.clear();

    const size_t inspected = std::min(detections.size(), kPpeMaxDetections);
    std::array<size_t, kPpeMaxDetections> heads{};
    size_t head_count = 0;
    for (size_t i = 0; i < inspected; ++i) {
        const auto& det = detections[i];
        if (det.class_id != kClassHeadHelmet && det.class_id != kClassHeadNoHelmet) {
            continue;
        }
        const float w = det.box[2] - det.box[0];
        const float h = det.box[3] - det.box[1];
        if (w <= 0.0f || h < min_head_height_ratio) {
            continue;
        }
        heads[head_count++] = i;
    }
    if (head_count == 0) {
        return;
    }

    std::array<uint8_t, kPpeMaxDetections> suppressed{};
    decisions.reserve(head_count);
    for (size_t i = 0; i < head_count; ++i) {
        if (suppressed[i] != 0) {
            continue;
        }
        suppressed[i] = 1;
        size_t chosen = i;
        for (size_t j = i + 1; j < head_count; ++j) {
            if (suppressed[j] != 0) {
                continue;
            }
            const auto& current = detections[heads[chosen]];
            const auto& candidate = detections[heads[j]];
            if (current.class_id == candidate.class_id) {
                continue;
            }
            if (box_iou(current.box, candidate.box) < kCrossClassConflictIou) {
                continue;
            }

            // 同一头部同时检出戴帽与未戴帽：取高分；平票时优先 no_helmet（安全侧）。
            suppressed[j] = 1;
            const bool clearly_better =
                candidate.confidence > current.confidence + kScoreTieEpsilon;
            const bool tied_but_violation =
                candidate.confidence >= current.confidence - kScoreTieEpsilon &&
                candidate.class_id == kClassHeadNoHelmet && current.class_id != kClassHeadNoHelmet;
            if (clearly_better || tied_but_violation) {
                chosen = j;
            }
        }

        const auto& winner = detections[heads[chosen]];
        HelmetDecision decision;
        copy_box(decision.box, winner.box);
        decision.score = winner.confidence;
        decision.violation = (winner.class_id == kClassHeadNoHelmet);
        decisions.push_back(decision);
    }
}

} // namespace zhulong::ppe
