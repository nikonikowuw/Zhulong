#include "analysis/helmet_association.hpp"

#include <algorithm>
#include <cstddef>

namespace zhulong::ppe {
namespace {

constexpr float kHeadTopExpandRatio = 0.05f;   /**< 人体框顶部向上放宽 */
constexpr float kHeadSideExpandRatio = 0.02f;  /**< 人体框左右放宽 */
constexpr float kHeadBottomRatio = 0.55f;      /**< 头中心必须落在人体框上 55% 区域内 */
constexpr float kMinHeadHeightRatio = 0.06f;   /**< 头高 / 人身高 */
constexpr float kMaxHeadHeightRatio = 0.45f;
constexpr float kMinHeadWidthRatio = 0.10f;    /**< 头宽 / 人身宽 */
constexpr float kMaxHeadWidthRatio = 0.90f;

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

std::vector<HelmetDecision> associate_helmets(const std::vector<zhulong::Detection>& detections,
                                              float min_person_height_ratio) {
    std::vector<size_t> persons;
    std::vector<size_t> heads;
    for (size_t i = 0; i < detections.size(); ++i) {
        const int32_t class_id = detections[i].class_id;
        if (class_id == kClassPerson) {
            persons.push_back(i);
        } else if (class_id == kClassHeadHelmet || class_id == kClassHeadNoHelmet) {
            heads.push_back(i);
        }
    }
    if (persons.empty() || heads.empty()) {
        return {};
    }

    // head -> 包含度最高且几何合理的人体
    std::vector<int> owner(heads.size(), -1);
    for (size_t hi = 0; hi < heads.size(); ++hi) {
        const auto& head = detections[heads[hi]];
        int best_person = -1;
        float best_containment = 0.0f;
        for (size_t pi = 0; pi < persons.size(); ++pi) {
            const auto& person = detections[persons[pi]];
            if (!head_belongs_to_person(head, person, min_person_height_ratio)) {
                continue;
            }
            const float score = containment(head.box, person.box);
            if (score > best_containment) {
                best_containment = score;
                best_person = static_cast<int>(pi);
            }
        }
        owner[hi] = best_person;
    }

    std::vector<HelmetDecision> decisions;
    decisions.reserve(persons.size());
    for (size_t pi = 0; pi < persons.size(); ++pi) {
        const auto& person = detections[persons[pi]];
        int chosen_head = -1;
        for (size_t hi = 0; hi < heads.size(); ++hi) {
            if (owner[hi] != static_cast<int>(pi)) {
                continue;
            }
            if (chosen_head < 0) {
                chosen_head = static_cast<int>(hi);
                continue;
            }
            const auto& candidate = detections[heads[hi]];
            const auto& current = detections[heads[static_cast<size_t>(chosen_head)]];
            // 同一人多个头部框：取分数最高者；平票时 nohelmet 优先（宁可多报不可漏报）。
            if (candidate.confidence > current.confidence ||
                (candidate.confidence == current.confidence &&
                 candidate.class_id == kClassHeadNoHelmet)) {
                chosen_head = static_cast<int>(hi);
            }
        }
        if (chosen_head < 0) {
            continue;
        }

        const auto& head = detections[heads[static_cast<size_t>(chosen_head)]];
        HelmetDecision decision;
        decision.box[0] = person.box[0];
        decision.box[1] = person.box[1];
        decision.box[2] = person.box[2];
        decision.box[3] = person.box[3];
        decision.person_score = person.confidence;
        decision.head_score = head.confidence;
        decision.violation = head.class_id == kClassHeadNoHelmet;
        decisions.push_back(decision);
    }
    return decisions;
}

} // namespace zhulong::ppe
