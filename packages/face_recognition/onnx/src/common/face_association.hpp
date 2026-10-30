/**
 * @file face_association.hpp
 * @brief 人脸 → 人体的空间挂载判据（纯几何，无模型依赖）
 *
 * 与 `.trellis/spec/native/algo-plugin-contract.md` §2「空间挂载准则」一致：
 * 人脸中心落在人体框上半身（含容差）且人脸在人体框内的覆盖比 IoF ≥ 0.50。
 *
 * 抽出成独立头文件的理由：这是**纯几何**判据，`make unit-test` 就能覆盖到 ——
 * 它同时也是除零/NaN 的入口（零尺寸框），不能只靠需要模型的端到端测试发现。
 */

#pragma once

#include <algorithm>
#include <array>

namespace zhulong::face {

/** @brief 人脸中心允许落在人体框上部多少比例以内（下半身的人脸不挂载） */
inline constexpr float kPersonUpperBodyRatio = 0.45f;
/** @brief 人体框左右/上方的外扩容差（人歪头、框抖动时仍能挂上） */
inline constexpr float kPersonBodyMarginRatio = 0.15f;
/** @brief 人脸被人体框覆盖的最低比例 IoF = 交集面积 / 人脸面积 */
inline constexpr float kMinimumFaceInsidePersonRatio = 0.50f;
/** @brief 框尺寸下限，避免零/负尺寸框把 IoF 除成 NaN 或 inf */
inline constexpr float kMinimumBoxExtent = 0.001f;

/**
 * @brief 判断该人脸是否挂载到该人体框
 *
 * @param face_box 归一化人脸框 [x1, y1, x2, y2]
 * @param body_box 归一化人体框 [x1, y1, x2, y2]
 * @return true 表示两个几何条件同时满足
 *
 * @note 全部为 float 计算且对零尺寸框做了下限保护，输入退化时返回 false 而不是 NaN 比较。
 */
inline bool face_belongs_to_person(const std::array<float, 4>& face_box,
                                   const std::array<float, 4>& body_box) {
    const float body_w = std::max(kMinimumBoxExtent, body_box[2] - body_box[0]);
    const float body_h = std::max(kMinimumBoxExtent, body_box[3] - body_box[1]);
    const float face_center_x = (face_box[0] + face_box[2]) * 0.5f;
    const float face_center_y = (face_box[1] + face_box[3]) * 0.5f;

    // 几何条件 1：人脸中心落在人体框上半身（含左右/上方容差）
    const bool center_in_upper_body =
        face_center_x >= body_box[0] - body_w * kPersonBodyMarginRatio &&
        face_center_x <= body_box[2] + body_w * kPersonBodyMarginRatio &&
        face_center_y >= body_box[1] - body_h * kPersonBodyMarginRatio &&
        face_center_y <= body_box[1] + body_h * kPersonUpperBodyRatio;

    // 几何条件 2：人脸在人体框内的覆盖比 IoF = 交集面积 / 人脸面积
    const float inter_w = std::max(0.0f, std::min(body_box[2], face_box[2]) - std::max(body_box[0], face_box[0]));
    const float inter_h = std::max(0.0f, std::min(body_box[3], face_box[3]) - std::max(body_box[1], face_box[1]));
    const float face_w = std::max(kMinimumBoxExtent, face_box[2] - face_box[0]);
    const float face_h = std::max(kMinimumBoxExtent, face_box[3] - face_box[1]);
    const float inside_ratio = (inter_w * inter_h) / (face_w * face_h);

    return center_in_upper_body && inside_ratio >= kMinimumFaceInsidePersonRatio;
}

} // namespace zhulong::face
