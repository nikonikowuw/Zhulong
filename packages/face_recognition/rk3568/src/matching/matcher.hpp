/**
 * @file matcher.hpp
 * @brief 1:N 人脸特征比对与 Top-K 候选人检索模块
 */

#pragma once

#include "common/match_config.hpp"

#include <zhulong/algo.hpp>

#include <cstddef>
#include <string>
#include <vector>

namespace zhulong::face {

struct MatchResult {
    bool matched{false};
    float similarity{0.0f};
    std::string person_id;
    std::string person_name;
    std::string matched_face_id;
    std::vector<FaceCandidate> candidates;
};

/**
 * @brief 一次 1:N 比对的三个门限
 *
 * 收成聚合体而不是三个位置参数：`threshold` / `top_k` / `min_match_margin` 里的
 * `float, size_t, float` 混排极易在补参数时错位，而错位不会崩——只会让阈值变成别的量，
 * 表现为「某天开始全部认不出」。
 */
struct MatchLimits {
    float threshold{kDefaultMatchThreshold};        /**< 命中阈值（余弦相似度） */
    size_t top_k{kDefaultMatchTopK};                /**< 唯一身份候选人返回上限 */
    float min_match_margin{kDefaultMinMatchMargin}; /**< 第一、二名最小分差（<=0 关闭） */
};

/** @brief 从任务级参数里取出比对门限（`Matcher` 只消费这三个字段） */
[[nodiscard]] inline MatchLimits match_limits_from(const MatchParams& params) noexcept {
    return MatchLimits{params.threshold, params.top_k, params.min_match_margin};
}

/**
 * @brief 无状态 1:N 比对器
 *
 * 门限与会话 **逐次传入**而不是存成成员：一个包进程会同时服务多路流，每路流的
 * `task_algorithms.config_json` 各不相同。把阈值存进共享成员会让两路流互相覆盖
 * （后写者赢），这类竞争不会崩、只会让识别结果对不上配置 —— 最难查的一类。
 */
class Matcher {
public:
    Matcher() = default;

    /**
     * @brief 对探针特征做按身份聚合的 1:N 比对
     *
     * @param probe_features 探针特征（L2 归一化单位向量）
     * @param feature_dim 探针维度；底库中维度不符、person_id 为空或点积非有限的条目会被跳过
     * @param gallery 底库快照（调用方持有，本函数只读）
     * @param limits 命中阈值 / 唯一身份候选上限 / 第一二名最小分差
     * @return 比对结果；底库为空或探针/门限非法时返回 matched=false 的空结果
     */
    [[nodiscard]] MatchResult match(
        const float* probe_features,
        size_t feature_dim,
        const std::vector<FaceTemplate>& gallery,
        const MatchLimits& limits
    ) const;
};

} // namespace zhulong::face
