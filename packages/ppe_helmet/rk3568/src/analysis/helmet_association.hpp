/**
 * @file helmet_association.hpp
 * @brief 把 person 框与 head_helmet / head_nohelmet 框配对，得到每人的穿戴结论
 *
 * 只做纯几何/分数计算，不依赖推理会话，便于单测覆盖。配对是 head→person 单向的：
 * 一个头部框只属于包含关系最好的那个人，避免一人多框/两人共框的重复告警。
 */

#pragma once

#include <zhulong/algo.hpp>

#include <vector>

#include "detection/yolov8_ppe_decode.hpp"

namespace zhulong::ppe {

/**
 * @brief 单个人员的穿戴研判结果
 */
struct HelmetDecision {
    float box[4]{0.0f, 0.0f, 0.0f, 0.0f}; /**< 人员人体框（归一化） */
    float person_score{0.0f};
    float head_score{0.0f};
    bool violation{false}; /**< true=head_nohelmet，false=head_helmet */
};

/**
 * @brief 关联并给出**有头部证据**的人员结论；无人头配对的人不会出现在结果里
 *
 * @param detections 解码后的检测框（person / head_helmet / head_nohelmet）
 * @param min_person_height_ratio 人体框最小高度（归一化），过小的人不做研判
 * @return 每个被判定人员一条；顺序与 person 检测顺序一致
 */
std::vector<HelmetDecision> associate_helmets(const std::vector<zhulong::Detection>& detections,
                                              float min_person_height_ratio);

} // namespace zhulong::ppe
