/**
 * @file face_track_identity.hpp
 * @brief 单条跨帧人脸轨迹的身份投票、确认与锁定（纯数据逻辑 Header-only，无模型依赖）
 *
 * 为什么要有轨迹级决策：逐帧独立比对时，同一个人走近/转头的过程里 `matched` 会在
 * 正脸帧与侧脸帧之间 1/0 抖动；而远距/模糊帧的单帧分数常常落在阈值下方一点点。
 * 行业通行做法是把证据按轨迹累积（k-of-n 投票 + 身份锁定），而不是降低单帧阈值。
 *
 * 规则（只在一条已由空间 + 特征关联起来的轨迹内生效）：
 *   1. **严格确认**：本帧（融合特征）比对按 `threshold` + 分差门禁命中 → 立即确认该身份；
 *   2. **投票确认**：最近 `kTrackVoteWindow` 帧里，至少 `track_vote_min_hits` 帧的**单帧**特征
 *      top-1 是同一身份、分数 `>= threshold - track_vote_relax` 且分差门禁通过，并且**本帧**
 *      也满足 → 确认。计票用单帧特征而不是融合特征：融合特征逐帧高度相关，用它计票等于
 *      把同一份证据数两次；
 *   3. **锁定**：确认后，侧脸/模糊帧（本帧没命中任何人、或 top-1 只是近似）继续继承该身份；
 *   4. **切换**：连续 `kTrackIdentitySwitchFrames` 帧**严格命中**另一身份才改判 —— 单帧冲突
 *      视为噪声。中间夹一帧非冲突帧即清零。
 *
 * 线程安全：无。由 `StreamFeatureFusionPool` 在每路流的互斥锁内持有与调用。
 */

#pragma once

#include "match_config.hpp"

#include <algorithm>
#include <array>
#include <cmath>
#include <cstddef>
#include <cstdint>
#include <string>

namespace zhulong::face {

/** @brief 投票滑动窗口长度（最近多少帧参与计票） */
inline constexpr size_t kTrackVoteWindow = 5;
/** @brief 已确认身份被改判所需的连续严格冲突帧数 */
inline constexpr uint32_t kTrackIdentitySwitchFrames = 2;

/**
 * @brief 一帧比对给出的身份证据（取自 `MatchResult`：top-1 候选 + 门禁结论）
 *
 * `person_id` 为空表示本帧没有任何候选（底库为空 / 特征无效）。
 */
struct IdentityEvidence {
    bool matched{false};   /**< 严格命中：`score >= threshold` 且分差门禁通过 */
    bool margin_ok{true};  /**< 分差门禁是否通过（与阈值无关） */
    float score{0.0f};     /**< top-1 身份的相似度 */
    std::string person_id;
    std::string person_name;
    std::string face_id;
};

/** @brief 轨迹级身份决策结果 */
struct TrackIdentityDecision {
    bool confirmed{false};   /**< 该轨迹当前是否有已确认身份 */
    float similarity{0.0f};  /**< 轨迹内该身份观测到的最高相似度（单帧或融合） */
    std::string person_id;
    std::string person_name;
    std::string face_id;     /**< 产生最高相似度的底库模板 */
};

class TrackIdentityState {
public:
    /**
     * @brief 推进一帧身份证据并给出当前决策
     *
     * @param strict 本帧**融合特征**的比对证据（决定严格确认与冲突切换）
     * @param vote   本帧**单帧特征**的比对证据（进入投票窗口；未融合时与 strict 相同）
     * @param params 生效参数（threshold / track_vote_min_hits / track_vote_relax）
     */
    TrackIdentityDecision observe(const IdentityEvidence& strict,
                                  const IdentityEvidence& vote,
                                  const MatchParams& params) {
        push_vote(vote);

        const bool strict_ok = strict.matched && !strict.person_id.empty() && std::isfinite(strict.score);

        if (!confirmed_.confirmed) {
            if (strict_ok) {
                confirm(strict, strict.score);
            } else {
                try_confirm_by_vote(vote, params);
            }
            return confirmed_;
        }

        if (strict_ok && strict.person_id != confirmed_.person_id) {
            if (conflict_person_id_ == strict.person_id) {
                ++conflict_frames_;
            } else {
                conflict_person_id_ = strict.person_id;
                conflict_frames_ = 1;
            }
            if (conflict_frames_ >= kTrackIdentitySwitchFrames) {
                confirm(strict, strict.score);
            }
            return confirmed_;
        }

        clear_conflict();
        if (strict_ok) {
            raise_similarity(strict);
        }
        if (vote.margin_ok && !vote.person_id.empty() && vote.person_id == confirmed_.person_id &&
            std::isfinite(vote.score)) {
            raise_similarity(vote);
        }
        return confirmed_;
    }

    /** @brief 当前是否已有确认身份（单测与诊断用） */
    [[nodiscard]] bool confirmed() const noexcept { return confirmed_.confirmed; }

    /** @brief 清空已确认身份、历史投票与冲突状态（底库删人或关闭轨迹身份时调用） */
    void reset() noexcept {
        for (Vote& v : votes_) {
            v.valid = false;
            v.margin_ok = false;
            v.score = 0.0f;
            v.person_id.clear();
            v.person_name.clear();
            v.face_id.clear();
        }
        vote_next_ = 0;
        confirmed_.confirmed = false;
        confirmed_.similarity = 0.0f;
        confirmed_.person_id.clear();
        confirmed_.person_name.clear();
        confirmed_.face_id.clear();
        clear_conflict();
    }

private:
    struct Vote {
        bool valid{false};
        bool margin_ok{false};
        float score{0.0f};
        std::string person_id;
        std::string person_name;
        std::string face_id;
    };

    static float vote_floor(const MatchParams& params) noexcept {
        const float threshold = std::isfinite(params.threshold) ? params.threshold : kDefaultMatchThreshold;
        const float relax = std::isfinite(params.track_vote_relax)
                                ? std::clamp(params.track_vote_relax, kMinTrackVoteRelax, kMaxTrackVoteRelax)
                                : kDefaultTrackVoteRelax;
        return std::max(0.0f, threshold - relax);
    }

    static size_t min_hits(const MatchParams& params) noexcept {
        return static_cast<size_t>(std::clamp(params.track_vote_min_hits, kMinTrackVoteMinHits, kMaxTrackVoteMinHits));
    }

    /** 每帧都占一个窗口位（无证据帧记为无效票），窗口语义恒为「最近 N 帧」 */
    void push_vote(const IdentityEvidence& vote) {
        Vote& slot = votes_[vote_next_];
        slot.valid = !vote.person_id.empty() && std::isfinite(vote.score);
        slot.margin_ok = vote.margin_ok;
        slot.score = slot.valid ? vote.score : 0.0f;
        slot.person_id = slot.valid ? vote.person_id : std::string();
        slot.person_name = slot.valid ? vote.person_name : std::string();
        slot.face_id = slot.valid ? vote.face_id : std::string();
        vote_next_ = (vote_next_ + 1) % kTrackVoteWindow;
    }

    void try_confirm_by_vote(const IdentityEvidence& vote, const MatchParams& params) {
        const float floor = vote_floor(params);
        if (vote.person_id.empty() || !std::isfinite(vote.score) || !vote.margin_ok || vote.score < floor) {
            return; // 本帧自己不合格：不允许靠历史票「隔空」确认
        }
        size_t hits = 0;
        const Vote* best_vote = nullptr;
        for (const Vote& v : votes_) {
            if (v.valid && v.margin_ok && v.score >= floor && v.person_id == vote.person_id) {
                ++hits;
                if (best_vote == nullptr || v.score > best_vote->score ||
                    (v.score == best_vote->score && !v.face_id.empty() &&
                     (best_vote->face_id.empty() || v.face_id < best_vote->face_id))) {
                    best_vote = &v;
                }
            }
        }
        if (hits >= min_hits(params) && best_vote != nullptr) {
            IdentityEvidence winner = vote;
            winner.score = best_vote->score;
            winner.face_id = best_vote->face_id;
            if (winner.person_name.empty()) {
                winner.person_name = best_vote->person_name;
            }
            confirm(winner, best_vote->score);
        }
    }

    void confirm(const IdentityEvidence& evidence, float similarity) {
        confirmed_.confirmed = true;
        confirmed_.person_id = evidence.person_id;
        confirmed_.person_name = evidence.person_name;
        confirmed_.face_id = evidence.face_id;
        confirmed_.similarity = similarity;
        clear_conflict();
    }

    void raise_similarity(const IdentityEvidence& evidence) {
        if (evidence.score > confirmed_.similarity) {
            confirmed_.similarity = evidence.score;
            confirmed_.face_id = evidence.face_id;
            if (!evidence.person_name.empty()) {
                confirmed_.person_name = evidence.person_name;
            }
        }
    }

    void clear_conflict() noexcept {
        conflict_person_id_.clear();
        conflict_frames_ = 0;
    }

    std::array<Vote, kTrackVoteWindow> votes_{};
    size_t vote_next_{0};
    TrackIdentityDecision confirmed_{};
    std::string conflict_person_id_;
    uint32_t conflict_frames_{0};
};

} // namespace zhulong::face
