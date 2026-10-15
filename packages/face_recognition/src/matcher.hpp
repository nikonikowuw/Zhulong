/**
 * @file matcher.hpp
 * @brief 1:N 人脸特征比对与 Top-K 候选人检索模块
 */

#pragma once

#include <zhulong/algo.hpp>
#include <vector>
#include <string>

namespace zhulong::face {

struct MatchResult {
    bool matched{false};
    float similarity{0.0f};
    std::string person_id;
    std::string person_name;
    std::vector<FaceCandidate> candidates;
};

class Matcher {
public:
    explicit Matcher(float threshold = 0.40f, size_t top_k = 3);

    void set_threshold(float threshold) noexcept { threshold_ = threshold; }
    void set_top_k(size_t top_k) noexcept { top_k_ = top_k; }

    [[nodiscard]] float threshold() const noexcept { return threshold_; }
    [[nodiscard]] size_t top_k() const noexcept { return top_k_; }

    [[nodiscard]] MatchResult match(
        const float* probe_features,
        size_t feature_dim,
        const std::vector<FaceTemplate>& gallery
    ) const;

private:
    float threshold_{0.40f};
    size_t top_k_{3};
};

} // namespace zhulong::face
