/**
 * @file detection_postprocess.cpp
 * @brief 后端无关的检测后处理算子（IoU 与 NMS）实现
 *
 * ⚠️ 本文件是从 cpu_vision_backend.cpp 拆出来的：`nms` 声明在公共头
 *    `zhulong/runtime/vision_backend.hpp`，是「任何视觉后端都能用」的通用算子。
 *    定义寄生在 CPU 后端的翻译单元里会造成隐性链接耦合 —— 任何不链接 CPU 后端的
 *    构建（例如只含 RGA 的产物，或第三方只取 SDK 头 + 自家后端）都会 undefined symbol。
 *    算子归属放在 backend 之外，`nms` 才真正与后端正交。
 */

#include <zhulong/runtime/vision_backend.hpp>

#include <algorithm>
#include <cmath>
#include <vector>

namespace zhulong {
namespace {

/**
 * @brief 基于归一化两点坐标 [x1,y1,x2,y2] 计算交并比（IoU）
 *
 * 使用 std::max(0.0f, ...) 防御越界或坐标倒置导致的负面积。
 */
float calc_iou(const zhulong_bbox& a, const zhulong_bbox& b) {
    const float x1 = std::max(a.box[0], b.box[0]);
    const float y1 = std::max(a.box[1], b.box[1]);
    const float x2 = std::min(a.box[2], b.box[2]);
    const float y2 = std::min(a.box[3], b.box[3]);

    const float inter_w = std::max(0.0f, x2 - x1);
    const float inter_h = std::max(0.0f, y2 - y1);
    const float inter_area = inter_w * inter_h;

    const float area_a = std::max(0.0f, a.box[2] - a.box[0]) * std::max(0.0f, a.box[3] - a.box[1]);
    const float area_b = std::max(0.0f, b.box[2] - b.box[0]) * std::max(0.0f, b.box[3] - b.box[1]);
    const float union_area = area_a + area_b - inter_area;

    if (union_area <= 0.0f) return 0.0f;
    return inter_area / union_area;
}

} // namespace

/**
 * @brief 按置信度降序排序后逐框抑制的标量 NMS（非极大值抑制），最坏复杂度 O(n^2)
 *
 * ⚠️ 输入框必须为已归一化坐标 [x1, y1, x2, y2] ∈ [0.0, 1.0]。
 */
std::vector<zhulong_bbox> nms(const std::vector<zhulong_bbox>& boxes, float iou_threshold) {
    if (boxes.empty()) return {};

    // ⚠️ 必须先剔除非有限的置信度或坐标（NaN / ±Inf）。
    //    NaN 会让 `a.confidence > b.confidence` 不满足**严格弱序**，而 std::sort 在该
    //    谓词下是未定义行为（libstdc++ 的 introsort 可能越界读）；即便侥幸不崩，NaN 框
    //    也会被保留并落在排序结果的任意位置，破坏「按置信度降序」这条下游依赖的不变量。
    //    量化溢出、上游数据损坏都会产出 NaN，且这类框本就不是有效检测，丢弃即可。
    std::vector<zhulong_bbox> sorted_boxes;
    sorted_boxes.reserve(boxes.size());
    for (const auto& box : boxes) {
        if (!std::isfinite(box.confidence) ||
            !std::isfinite(box.box[0]) || !std::isfinite(box.box[1]) ||
            !std::isfinite(box.box[2]) || !std::isfinite(box.box[3])) {
            continue;
        }
        sorted_boxes.push_back(box);
    }
    if (sorted_boxes.empty()) return {};

    std::sort(sorted_boxes.begin(), sorted_boxes.end(),
              [](const zhulong_bbox& a, const zhulong_bbox& b) {
                  return a.confidence > b.confidence;
              });

    std::vector<bool> suppressed(sorted_boxes.size(), false);
    std::vector<zhulong_bbox> keep;
    keep.reserve(sorted_boxes.size());

    for (size_t i = 0; i < sorted_boxes.size(); ++i) {
        if (suppressed[i]) continue;
        keep.push_back(sorted_boxes[i]);

        for (size_t j = i + 1; j < sorted_boxes.size(); ++j) {
            if (suppressed[j]) continue;
            if (calc_iou(sorted_boxes[i], sorted_boxes[j]) > iou_threshold) {
                suppressed[j] = true;
            }
        }
    }
    return keep;
}

} // namespace zhulong
