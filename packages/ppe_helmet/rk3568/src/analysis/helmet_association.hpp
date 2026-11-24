/**
 * @file helmet_association.hpp
 * @brief 将头部安全帽检测框过滤并解决同一头部的跨类冲突，输出研判结论
 *
 * DAMO-YOLO-S 只检头部（head_helmet / head_nohelmet），不依赖人体框：
 *   - 过滤掉过小的远景头部框（height < min_head_height_ratio）；
 *   - 同一头部同时命中戴帽/未戴帽时，按高分优胜（平票偏 no_helmet）去重。
 */

#pragma once

#include <zhulong/algo.hpp>

#include <vector>

#include "detection/yolov8_ppe_decode.hpp"

namespace zhulong::ppe {

/**
 * @brief 单个头部目标的安全帽研判结果
 */
struct HelmetDecision {
    float box[4]{0.0f, 0.0f, 0.0f, 0.0f}; /**< 头部框（归一化） */
    float score{0.0f};
    bool violation{false}; /**< true=head_nohelmet，false=head_helmet */
};

/**
 * @brief 过滤过小头部并解决同一头部的戴帽/未戴帽冲突
 *
 * @param detections 解码后的检测框（head_helmet / head_nohelmet）
 * @param min_head_height_ratio 头部框最小高度（归一化），过小目标不做研判
 * @param decisions 输出缓冲：入口清空，出口每个有效头部一条
 */
void associate_helmets(const std::vector<zhulong::Detection>& detections,
                       float min_head_height_ratio,
                       std::vector<HelmetDecision>& decisions);

} // namespace zhulong::ppe
