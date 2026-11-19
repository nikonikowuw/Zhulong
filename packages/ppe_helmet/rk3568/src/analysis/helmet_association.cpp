#include "analysis/helmet_association.hpp"

#include "common/box_geometry.hpp"

#include <algorithm>
#include <array>
#include <cstddef>
#include <cstdint>

namespace zhulong::ppe {
namespace {

constexpr float kHeadTopExpandRatio = 0.05f;   /**< 人体框顶部向上放宽 */
constexpr float kHeadSideExpandRatio = 0.02f;  /**< 人体框左右放宽 */
constexpr float kHeadBottomRatio = 0.55f;      /**< 头中心必须落在人体框上 55% 区域内 */
constexpr float kMinHeadHeightRatio = 0.06f;   /**< 头高 / 人身高 */
constexpr float kMaxHeadHeightRatio = 0.45f;
constexpr float kMinHeadWidthRatio = 0.10f;    /**< 头宽 / 人身宽 */
constexpr float kMaxHeadWidthRatio = 0.90f;

/**
 * @brief 头部框分数「打平」的容差
 *
 * 检测分数是连续浮点，两帧/两框恰好相等几乎不会发生；用严格 `==` 判定平票会让
 * 「平票偏 no_helmet」这条安全侧规则永远不生效。
 */
constexpr float kScoreTieEpsilon = 1e-4f;

float width(const float* box) noexcept { return box[2] - box[0]; }
float height(const float* box) noexcept { return box[3] - box[1]; }
float area(const float* box) noexcept {
    const float w = width(box);
    const float h = height(box);
    return w > 0.0f && h > 0.0f ? w * h : 0.0f;
}

/** @brief 头部框被人体框覆盖的比例（分母是头框面积） */
float containment(const float* head, const float* person) noexcept {
    const float inter_w = std::min(head[2], person[2]) - std::max(head[0], person[0]);
    const float inter_h = std::min(head[3], person[3]) - std::max(head[1], person[1]);
    const float head_area = area(head);
    if (inter_w <= 0.0f || inter_h <= 0.0f || head_area <= 0.0f) {
        return 0.0f;
    }
    return (inter_w * inter_h) / head_area;
}

bool head_belongs_to_person(const zhulong::Detection& head, const zhulong::Detection& person,
                            float min_person_height_ratio) noexcept {
    const float person_w = width(person.box);
    const float person_h = height(person.box);
    if (person_w <= 0.0f || person_h < min_person_height_ratio) {
        return false;
    }
    const float head_w = width(head.box);
    const float head_h = height(head.box);
    if (head_w <= 0.0f || head_h <= 0.0f) {
        return false;
    }

    const float head_center_x = (head.box[0] + head.box[2]) * 0.5f;
    const float head_center_y = (head.box[1] + head.box[3]) * 0.5f;
    const float left = person.box[0] - kHeadSideExpandRatio * person_w;
    const float right = person.box[2] + kHeadSideExpandRatio * person_w;
    const float top = person.box[1] - kHeadTopExpandRatio * person_h;
    const float bottom = person.box[1] + kHeadBottomRatio * person_h;
    if (head_center_x < left || head_center_x > right || head_center_y < top ||
        head_center_y > bottom) {
        return false;
    }

    const float height_ratio = head_h / person_h;
    if (height_ratio < kMinHeadHeightRatio || height_ratio > kMaxHeadHeightRatio) {
        return false;
    }
    const float width_ratio = head_w / person_w;
    if (width_ratio < kMinHeadWidthRatio || width_ratio > kMaxHeadWidthRatio) {
        return false;
    }
    return true;
}

} // namespace

void associate_helmets(const std::vector<zhulong::Detection>& detections,
                       float min_person_height_ratio,
                       std::vector<HelmetDecision>& decisions) {
    decisions.clear();

    // 解码端已把输出限制在 kPpeMaxDetections 以内，这里用栈上定长数组收集下标，
    // 避免每帧三条 vector 堆分配。超出上限说明上游违约，截断即可。
    const size_t inspected = std::min(detections.size(), kPpeMaxDetections);
    std::array<size_t, kPpeMaxDetections> persons{};
    std::array<size_t, kPpeMaxDetections> heads{};
    size_t person_count = 0;
    size_t head_count = 0;
    for (size_t i = 0; i < inspected; ++i) {
        const int32_t class_id = detections[i].class_id;
        if (class_id == kClassPerson) {
            persons[person_count++] = i;
        } else if (class_id == kClassHeadHelmet || class_id == kClassHeadNoHelmet) {
            heads[head_count++] = i;
        }
    }
    if (person_count == 0 || head_count == 0) {
        return;
    }

    // head -> 包含度最高且几何合理的人体（-1 表示无主）
    std::array<int16_t, kPpeMaxDetections> owner{};
    owner.fill(-1);
    for (size_t hi = 0; hi < head_count; ++hi) {
        const auto& head = detections[heads[hi]];
        int16_t best_person = -1;
        float best_containment = 0.0f;
        for (size_t pi = 0; pi < person_count; ++pi) {
            const auto& person = detections[persons[pi]];
            if (!head_belongs_to_person(head, person, min_person_height_ratio)) {
                continue;
            }
            const float score = containment(head.box, person.box);
            if (score > best_containment) {
                best_containment = score;
                best_person = static_cast<int16_t>(pi);
            }
        }
        owner[hi] = best_person;
    }

    for (size_t pi = 0; pi < person_count; ++pi) {
        const auto& person = detections[persons[pi]];
        size_t chosen_head = 0;
        bool has_head = false;
        for (size_t hi = 0; hi < head_count; ++hi) {
            if (owner[hi] != static_cast<int16_t>(pi)) {
                continue;
            }
            if (!has_head) {
                chosen_head = hi;
                has_head = true;
                continue;
            }
            const auto& candidate = detections[heads[hi]];
            const auto& current = detections[heads[chosen_head]];
            // 同一人多个头部框：取分数最高者；分数打平时 nohelmet 优先（宁可多报不可漏报）。
            const bool clearly_better =
                candidate.confidence > current.confidence + kScoreTieEpsilon;
            const bool tied_but_violation =
                candidate.confidence >= current.confidence - kScoreTieEpsilon &&
                candidate.class_id == kClassHeadNoHelmet && current.class_id != kClassHeadNoHelmet;
            if (clearly_better || tied_but_violation) {
                chosen_head = hi;
            }
        }
        if (!has_head) {
            continue;
        }

        const auto& head = detections[heads[chosen_head]];
        HelmetDecision decision;
        copy_box(decision.box, person.box);
        decision.person_score = person.confidence;
        decision.head_score = head.confidence;
        decision.violation = head.class_id == kClassHeadNoHelmet;
        decisions.push_back(decision);
    }
}

} // namespace zhulong::ppe
