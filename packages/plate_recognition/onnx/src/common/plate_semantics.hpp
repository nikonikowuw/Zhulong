/**
 * @file plate_semantics.hpp
 * @brief 车牌检测头的类别语义（单层 / 双层 / 新能源）+ 类别数上限（唯一来源）
 *
 * 为什么必须放在 `common/`：检测 Stage 产出它、识别 Stage 消费它、管线把两边拼成
 * SDK 结构体。曾经检测侧有 `PlateLayerClass`、识别侧有 `PlateLineLayout`，两个 enum
 * 靠「数值恰好相同」对齐，Stage 之间传的还是裸 `int32_t` —— 谁给其中一个加类目都不会
 * 编译失败，只会让双层车牌按单层采样（表现是「号码首字对、后半段乱」）。
 *
 * ⚠️ 枚举值 = 模型 class 索引，顺序由权重决定，不能重排。
 */

#pragma once

#include <cstdint>

namespace zhulong::plate {

/**
 * @brief 车牌层数/类型语义（对应检测模型 `class_id`）
 *
 * 类别数由模型决定（`channels - 4`）；本包只固定**已约定**的三个语义。
 * 模型若只有 2 类，则新能源分支永远不会产出。
 */
enum class PlateKind : int32_t {
    SingleLine = 0, /**< 单层车牌 */
    DoubleLine = 1, /**< 双层车牌（识别前需上下半拼成一行） */
    NewEnergy = 2,  /**< 新能源 8 位车牌（前 2 字仍按单层采样） */
};

/**
 * @brief 本包认识的类别数上限：三个已约定语义 + 1 个预留位
 *
 * 超过它说明加载的不是车牌检测头（例如 80 类 COCO 权重）。此时按 (cx, cy, w, h) 继续
 * 解析会产出一堆位置离谱、置信度却「看起来正常」的框，比直接返回空危险得多。
 */
inline constexpr int kMaxPlateClasses = 4;

/**
 * @brief 模型类别索引 → PlateKind
 *
 * 未约定的索引（预留位、或超出上限的模型）一律按单层采样：这是唯一不会产生
 * 结构性错位的降级（双层按单层采样的后果是号码错，而把单层当双层采样会拿到
 * 上下半各一半的乱码）。检测 Stage 已在上游用 kMaxPlateClasses 挡住越界模型。
 */
inline PlateKind plate_kind_from_index(int32_t class_id) noexcept {
    switch (class_id) {
    case static_cast<int32_t>(PlateKind::DoubleLine):
        return PlateKind::DoubleLine;
    case static_cast<int32_t>(PlateKind::NewEnergy):
        return PlateKind::NewEnergy;
    default:
        return PlateKind::SingleLine;
    }
}

} // namespace zhulong::plate
