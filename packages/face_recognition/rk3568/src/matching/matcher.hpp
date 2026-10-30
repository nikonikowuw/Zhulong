/**
 * @file matcher.hpp
 * @brief 1:N 人脸特征比对与 Top-K 候选人检索模块
 */

#pragma once

#include <zhulong/algo.hpp>

#include <cstddef>
#include <string>
#include <vector>

namespace zhulong::face {

/** @brief 比对命中阈值默认值（config.schema.json 的 threshold 默认值，两处必须同步） */
inline constexpr float kDefaultMatchThreshold = 0.40f;
/** @brief Top-K 默认值（config.schema.json 的 top_k 默认值，两处必须同步） */
inline constexpr size_t kDefaultMatchTopK = 3;
/** @brief Top-K 上限（config.schema.json 的 top_k maximum，两处必须同步） */
inline constexpr size_t kMaxMatchTopK = 20;

struct MatchResult {
    bool matched{false};
    float similarity{0.0f};
    std::string person_id;
    std::string person_name;
    std::string matched_face_id;
    std::vector<FaceCandidate> candidates;
};

class Matcher {
public:
    explicit Matcher(float threshold = kDefaultMatchThreshold, size_t top_k = kDefaultMatchTopK);

    void set_threshold(float threshold) noexcept { threshold_ = threshold; }
    void set_top_k(size_t top_k) noexcept { top_k_ = top_k; }

    [[nodiscard]] float threshold() const noexcept { return threshold_; }
    [[nodiscard]] size_t top_k() const noexcept { return top_k_; }

    /**
     * @brief 对探针特征做 1:N 比对
     *
     * @param probe_features 探针特征（L2 归一化单位向量）
     * @param feature_dim 探针维度；底库中维度不符的条目会被跳过
     * @param gallery 底库快照（调用方持有，本函数只读）
     * @return 比对结果；底库为空或探针非法时返回 matched=false 的空结果
     */
    [[nodiscard]] MatchResult match(
        const float* probe_features,
        size_t feature_dim,
        const std::vector<FaceTemplate>& gallery
    ) const;

private:
    float threshold_{kDefaultMatchThreshold};
    size_t top_k_{kDefaultMatchTopK};
};

} // namespace zhulong::face
