#include "matcher.hpp"

#include <algorithm>
#include <cmath>
#include <cstddef>
#include <utility>
#include <vector>

namespace zhulong::face {

namespace {

struct ScoredTemplate {
    size_t index{0};
    float score{0.0f};
};

/**
 * @brief 严格全序比较器：判断候选 `a` 是否优于 `b`
 *
 * 与宿主 `person.SearchByImage` 对齐两层 tie-break：
 *   1. 相似度更高者优先；
 *   2. 跨身份等分时 `person_id` 字典序更小者优先；
 *   3. 同身份多模板等分时 `face_id` 字典序更小者优先（代表照片确定性）；
 *   4. 底库槽位下标兜底构成严格全序。
 *
 * 性质：按该全序依次弹出元素并只保留每个 `person_id` 的首次出现时，
 * 首次出现必然是该人员的最高分（及等分最小 `face_id`）代表模板，
 * 且不同人员之间的产出顺序严格等价于「先按人聚合再按 `(best_score desc, person_id asc)` 排序」。
 */
bool is_better_template(
    const ScoredTemplate& a,
    const ScoredTemplate& b,
    const std::vector<FaceTemplate>& gallery
) noexcept {
    if (a.score != b.score) {
        return a.score > b.score;
    }
    const auto& ta = gallery[a.index];
    const auto& tb = gallery[b.index];
    if (ta.person_id != tb.person_id) {
        return ta.person_id < tb.person_id;
    }
    if (ta.face_id != tb.face_id) {
        return ta.face_id < tb.face_id;
    }
    return a.index < b.index;
}

} // namespace

MatchResult Matcher::match(
    const float* probe_features,
    size_t feature_dim,
    const std::vector<FaceTemplate>& gallery,
    const MatchLimits& limits
) const {
    MatchResult result;
    const float threshold = limits.threshold;
    const float min_match_margin = limits.min_match_margin;
    if (probe_features == nullptr || feature_dim == 0 || gallery.empty() ||
        !std::isfinite(threshold) || !std::isfinite(min_match_margin)) {
        return result;
    }

    // 探针只校验一次有限性（O(D)），避免在底库循环内重复检查探针分量
    for (size_t i = 0; i < feature_dim; ++i) {
        if (!std::isfinite(probe_features[i])) {
            return result;
        }
    }

    // 只保留 (底库下标, 相似度)：不为每个底库项拷贝字符串，也不用 unordered_map 触发逐人节点分配
    std::vector<ScoredTemplate> scored;
    scored.reserve(gallery.size());

    for (size_t index = 0; index < gallery.size(); ++index) {
        const auto& item = gallery[index];
        if (item.features.size() != feature_dim || item.person_id.empty()) {
            continue;
        }

        float dot = 0.0f;
        for (size_t i = 0; i < feature_dim; ++i) {
            dot += probe_features[i] * item.features[i];
        }
        // 一次判据拦截模板中的 NaN 与 ±Inf（含 0 * Inf = NaN），保护比较器严格弱序
        if (!std::isfinite(dot)) {
            continue;
        }
        scored.push_back(ScoredTemplate{index, std::clamp(dot, 0.0f, 1.0f)});
    }

    if (scored.empty()) {
        return result;
    }

    // std::make_heap 将「最大」元素置于 front()，因此比较器返回 true 表示 a 排在 b 后面（即 b 优于 a）
    const auto heap_less = [&gallery](const ScoredTemplate& a, const ScoredTemplate& b) noexcept {
        return is_better_template(b, a, gallery);
    };
    std::make_heap(scored.begin(), scored.end(), heap_less);

    // 至少取前 2 个不同身份以独立支撑 min_match_margin，不受 top_k=0/1 截断影响。
    // 去重只保留一条线性路径：`top_k` 被 config.schema.json 钉在 kMaxMatchTopK(=20)，
    // 「唯一身份数超过 64 才切换算法」的分支永远走不到，留着只会是没人执行的第二种实现。
    const size_t target_unique = std::min(std::max(limits.top_k, size_t{2}), scored.size());

    std::vector<ScoredTemplate> unique_persons;
    unique_persons.reserve(target_unique);

    size_t heap_end = scored.size();
    while (heap_end > 0 && unique_persons.size() < target_unique) {
        std::pop_heap(scored.begin(), scored.begin() + static_cast<std::ptrdiff_t>(heap_end), heap_less);
        --heap_end;
        const ScoredTemplate current = scored[heap_end];

        bool duplicate = false;
        for (const auto& existing : unique_persons) {
            if (gallery[existing.index].person_id == gallery[current.index].person_id) {
                duplicate = true;
                break;
            }
        }
        if (duplicate) {
            continue;
        }

        unique_persons.push_back(current);
    }

    if (unique_persons.empty()) {
        return result;
    }

    // 候选列表保持「抓拍证据」语义：无论最终是否达到 threshold / min_match_margin，都返回最相似的唯一人员
    const size_t emit_count = std::min(limits.top_k, unique_persons.size());
    result.candidates.reserve(emit_count);
    for (size_t i = 0; i < emit_count; ++i) {
        const auto& item = gallery[unique_persons[i].index];
        FaceCandidate candidate;
        candidate.person_id = item.person_id;
        candidate.person_name = item.person_name;
        candidate.face_id = item.face_id;
        candidate.similarity = unique_persons[i].score;
        result.candidates.push_back(std::move(candidate));
    }

    const auto& best = gallery[unique_persons.front().index];
    result.similarity = unique_persons.front().score;

    bool margin_ok = true;
    if (min_match_margin > 0.0f && unique_persons.size() >= 2) {
        const float margin = unique_persons[0].score - unique_persons[1].score;
        margin_ok = (margin >= min_match_margin);
    }

    if (result.similarity >= threshold && margin_ok) {
        result.matched = true;
        result.person_id = best.person_id;
        result.person_name = best.person_name;
        result.matched_face_id = best.face_id;
    }

    return result;
}

} // namespace zhulong::face
