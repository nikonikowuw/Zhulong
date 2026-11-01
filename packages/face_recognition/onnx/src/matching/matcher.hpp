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
 * @brief 无状态 1:N 比对器
 *
 * 阈值与 top_k **逐次传入**而不是存成成员：一个包进程会同时服务多路流，每路流的
 * `task_algorithms.config_json` 各不相同。把阈值存进共享成员会让两路流互相覆盖
 * （后写者赢），这类竞争不会崩、只会让识别结果对不上配置 —— 最难查的一类。
 */
class Matcher {
public:
    Matcher() = default;

    /**
     * @brief 对探针特征做 1:N 比对
     *
     * @param probe_features 探针特征（L2 归一化单位向量）
     * @param feature_dim 探针维度；底库中维度不符的条目会被跳过
     * @param gallery 底库快照（调用方持有，本函数只读）
     * @param threshold 命中阈值（余弦相似度），调用方负责范围校验
     * @param top_k 候选人返回上限
     * @return 比对结果；底库为空或探针非法时返回 matched=false 的空结果
     */
    [[nodiscard]] MatchResult match(
        const float* probe_features,
        size_t feature_dim,
        const std::vector<FaceTemplate>& gallery,
        float threshold,
        size_t top_k
    ) const;
};

} // namespace zhulong::face
