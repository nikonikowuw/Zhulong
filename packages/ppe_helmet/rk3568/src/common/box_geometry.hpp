/**
 * @file box_geometry.hpp
 * @brief 跨 Stage 共享的归一化矩形原语
 *
 * 解码（按类 NMS）、关联（人体/头部归属）、跟踪（轨迹匹配）三个 Stage 都要
 * 「复制 4 个 float」和「算 IoU」。各写一份的结果是「零面积框算不算重叠」这类
 * 细节迟早分叉，因此按 algo-plugin-contract §4 收进 `common/`。
 */

#pragma once

#include <algorithm>

namespace zhulong::ppe {

/** @brief 复制归一化 xyxy 框（4 个 float） */
inline void copy_box(float* destination, const float* source) noexcept {
    destination[0] = source[0];
    destination[1] = source[1];
    destination[2] = source[2];
    destination[3] = source[3];
}

/** @brief 两个归一化 xyxy 框的 IoU；无重叠或并集面积为 0 时返回 0 */
inline float box_iou(const float* a, const float* b) noexcept {
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

} // namespace zhulong::ppe
