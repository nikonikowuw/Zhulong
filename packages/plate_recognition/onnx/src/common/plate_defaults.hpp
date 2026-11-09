/**
 * @file plate_defaults.hpp
 * @brief 检测阈值默认值与可接受区间的唯一来源
 *
 * 同一组数字曾经写了三处：`config.schema.json`（宿主/UI 校验）、`PlateDetector` 的成员
 * 初值、`pipeline` 的回退常量。改一处忘一处的表现是「宿主按 schema 发 0.5，算法回落到
 * 0.35」，而且从任何一侧都看不出不一致。
 *
 * ⚠️ schema 仍然是给宿主与前端看的**权威声明**，本文件是代码侧的同一份事实：
 *    改这里必须同步 `config.schema.json`（两边数值与区间都要对得上）。
 */

#pragma once

namespace zhulong::plate {

/** @brief 检测置信度阈值默认值（= config.schema.json:confidence_threshold.default） */
inline constexpr float kDefaultConfidenceThreshold = 0.35f;
/** @brief NMS 重叠抑制阈值默认值（= config.schema.json:nms_threshold.default） */
inline constexpr float kDefaultNmsThreshold = 0.45f;

/**
 * @brief 阈值可接受区间 (kThresholdMinExclusive, kThresholdMaxInclusive]
 *
 * 越界一律回落默认值而不是报错：宿主与包分开交付，版本偏斜真实存在
 * （见 .trellis/spec/native/algo-plugin-contract.md「两层配置通道」）。
 */
inline constexpr double kThresholdMinExclusive = 0.0;
inline constexpr double kThresholdMaxInclusive = 1.0;

} // namespace zhulong::plate
