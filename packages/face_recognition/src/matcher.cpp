#include "matcher.hpp"
#include <algorithm>
#include <cmath>

namespace zhulong::face {

Matcher::Matcher(float threshold, size_t top_k)
    : threshold_(threshold), top_k_(top_k) {}

MatchResult Matcher::match(
    const float* probe_features,
    size_t feature_dim,
    const std::vector<FaceTemplate>& gallery
) const {
    MatchResult result;
    if (probe_features == nullptr || feature_dim == 0 || gallery.empty()) {
        return result;
    }

    struct ScoredCandidate {
        std::string person_id;
        std::string person_name;
        std::string face_id;
        float similarity{0.0f};
    };

    std::vector<ScoredCandidate> scored;
    scored.reserve(gallery.size());

    for (const auto& item : gallery) {
        if (item.features.size() != feature_dim) {
            continue;
        }

        // 计算单位向量点积（余弦相似度）
        float dot = 0.0f;
        for (size_t i = 0; i < feature_dim; ++i) {
            dot += probe_features[i] * item.features[i];
        }

        // 截断至 [0.0, 1.0]
        float sim = std::max(0.0f, std::min(1.0f, dot));
        scored.push_back({item.person_id, item.person_name, item.face_id, sim});
    }

    if (scored.empty()) {
        return result;
    }

    // 按相似度降序排列
    std::sort(scored.begin(), scored.end(), [](const ScoredCandidate& a, const ScoredCandidate& b) {
        return a.similarity > b.similarity;
    });

    // 提取 Top-K
    size_t count = std::min(top_k_, scored.size());
    result.candidates.reserve(count);
    for (size_t i = 0; i < count; ++i) {
        FaceCandidate cand;
        cand.person_id = scored[i].person_id;
        cand.person_name = scored[i].person_name;
        cand.face_id = scored[i].face_id;
        cand.similarity = scored[i].similarity;
        result.candidates.push_back(std::move(cand));
    }

    // Top-1 决策
    const auto& best = scored.front();
    result.similarity = best.similarity;

    if (best.similarity >= threshold_) {
        result.matched = true;
        result.person_id = best.person_id;
        result.person_name = best.person_name;
        result.matched_face_id = best.face_id;
    } else {
        result.matched = false;
        result.person_id = "";
        result.person_name = "";
        result.matched_face_id = "";
    }

    return result;
}

} // namespace zhulong::face
