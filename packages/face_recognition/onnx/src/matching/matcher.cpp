#include "matcher.hpp"

#include <algorithm>
#include <cstddef>
#include <utility>
#include <vector>

namespace zhulong::face {

MatchResult Matcher::match(
    const float* probe_features,
    size_t feature_dim,
    const std::vector<FaceTemplate>& gallery,
    float threshold,
    size_t top_k
) const {
    MatchResult result;
    if (probe_features == nullptr || feature_dim == 0 || gallery.empty()) {
        return result;
    }

    // 只保留 (底库下标, 相似度)：候选人的人名/人员 ID 等字段等 Top-K 定下来再取。
    // 为每个底库项拷贝 3 个 std::string 是这轮比对里最贵的一步（万级底库 × 每帧每脸）。
    std::vector<std::pair<size_t, float>> scored;
    scored.reserve(gallery.size());

    for (size_t index = 0; index < gallery.size(); ++index) {
        const auto& item = gallery[index];
        if (item.features.size() != feature_dim) {
            continue;
        }

        // 单位向量点积（余弦相似度）
        float dot = 0.0f;
        for (size_t i = 0; i < feature_dim; ++i) {
            dot += probe_features[i] * item.features[i];
        }
        // 截断至 [0.0, 1.0]：负相关按「不相似」处理
        scored.emplace_back(index, std::clamp(dot, 0.0f, 1.0f));
    }

    if (scored.empty()) {
        return result;
    }

    // 保证即使 top_k == 0，也至少对前 1 个元素做堆排序，确保 scored.front() 恒为全局最高分
    const size_t sort_count = std::min(std::max(top_k, size_t{1}), scored.size());
    std::partial_sort(scored.begin(), scored.begin() + static_cast<std::ptrdiff_t>(sort_count), scored.end(),
                      [](const std::pair<size_t, float>& a, const std::pair<size_t, float>& b) {
                          return a.second > b.second;
                      });

    const size_t count = std::min(top_k, scored.size());
    result.candidates.reserve(count);
    for (size_t i = 0; i < count; ++i) {
        const auto& item = gallery[scored[i].first];
        FaceCandidate candidate;
        candidate.person_id = item.person_id;
        candidate.person_name = item.person_name;
        candidate.face_id = item.face_id;
        candidate.similarity = scored[i].second;
        result.candidates.push_back(std::move(candidate));
    }

    // Top-1 决策（经至少 partial_sort(1) 排序后，scored.front() 必然是全局最高分）
    const auto& best = gallery[scored.front().first];
    result.similarity = scored.front().second;
    if (result.similarity >= threshold) {
        result.matched = true;
        result.person_id = best.person_id;
        result.person_name = best.person_name;
        result.matched_face_id = best.face_id;
    }

    return result;
}

} // namespace zhulong::face
