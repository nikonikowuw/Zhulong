/**
 * @file face_feature_fusion.hpp
 * @brief 单路视频流有界人脸特征状态池与跨帧防漂移质量加权融合（纯数据逻辑 Header-only）
 *
 * 设计要点：
 *   1. 按任务上下文（`TaskContext::task_id`，一路 broker 一路流）隔离状态池，每路独立互斥锁，
 *      多相机并发推理零锁争用；`shared_ptr<TaskEntry>` 保证桶淘汰期间在途调用绝不悬垂。
 *   2. 整帧一对一贪心二分匹配：按 `(sim_fused desc, sim_anchor desc, obs_idx asc, state_idx asc)`
 *      全局排序配对，既保证同帧多张脸绝不串入同一状态，又保证高相似真主优先夺得原状态。
 *   3. 双重同人门禁：当前帧特征必须同时与「状态融合均值特征」及「历史最高质量锚点特征」的
 *      余弦相似度 >= `fusion_similarity_threshold`，阻断擦肩而过或渐变遮挡引发的特征漂移。
 *   4. 质量加权累加 + 软上限衰减（`kMaxAccumulatedWeight`）+ 严格 L2 重归一化：既抑制早期低质帧
 *      噪声，又防止长驻目标被过往帧锁死，输出恒为单位向量，可直接送入 `Matcher::match`。
 *
 * 内存：单个状态实测 6168 B（3 组 512 维 float + 元数据），单路满额 32 状态 ≈ 192.8 KiB，
 *       进程内 `kMaxTrackedStreams` 上限对应最坏 48.2 MiB（20 路典型场景约 3.8 MiB）。
 */

#pragma once

#include "match_config.hpp"

#include <algorithm>
#include <array>
#include <chrono>
#include <cmath>
#include <cstddef>
#include <cstdint>
#include <limits>
#include <memory>
#include <mutex>
#include <unordered_map>
#include <vector>

namespace zhulong::face {

/** @brief 人脸特征向量固定维度（与 FaceLiVTv2-L 输出一致） */
inline constexpr size_t kFusionFeatureDim = 512;
/** @brief 单路视频流同时维护的活跃人脸状态上限（满额时优先淘汰本帧未命中的状态） */
inline constexpr size_t kMaxActiveStatesPerStream = 32;
/** @brief 进程内最多保留的任务流分桶数上限（防止异常动态 task_id 造成空桶累积） */
inline constexpr size_t kMaxTrackedStreams = 256;
/** @brief 单状态累计质量权重软上限（超出时按比例指数衰减旧累加器，保持长驻目标响应性） */
inline constexpr float kMaxAccumulatedWeight = 6.0f;
/** @brief 单次观测最小有效融合权重下界（防止 min_face_quality=0 时出现零权重除零退化） */
inline constexpr float kMinFusionWeightFloor = 0.05f;
/** @brief L2 模长有效下界 */
inline constexpr double kFusionNormEpsilon = 1e-6;

/**
 * @brief 单帧单张已通过质量门禁的人脸特征观测
 *
 * 特征是**定长数组指针**而不是 `const float*`：长度由类型固定，池内不可能是短读或错读，
 * 调用方也不再需要额外约定「必须恰好 512 个 float」。
 */
struct FusionFaceObservation {
    const std::array<float, kFusionFeatureDim>* features{nullptr}; /**< 512 维 L2 单位特征视图 */
    float quality{0.0f};                                           /**< 几何拓扑与姿态质量分 [0.0, 1.0] */
};

/** @brief 经跨帧状态池处理后的单张人脸输出 */
struct FusedFaceOutput {
    std::array<float, kFusionFeatureDim> features{}; /**< 送入底库比对的 512 维特征向量 */
    bool is_fused{false};                            /**< 是否融合了 >=2 帧同人特征 */
    /**
     * @brief 输入特征是否可用（非空、全有限、模长有效）
     *
     * `false` 时 `features` 恒为零向量 —— 调用方**必须丢弃该脸**：零向量送进 `Matcher::match`
     * 只会得到相似度 0 的「伪候选人证据」，看起来像一次正常但没认出来的比对。
     */
    bool valid{false};
};

namespace detail {

/**
 * @brief 校验 512 维特征有限性并累加平方和
 * @param src 特征视图；空指针按不可用处理
 * @param out_sum_sq 成功时写入二范数平方（`double` 累加，避免 512 项单精度相消）
 * @return false 表示含非有限分量或指针为空
 */
inline bool feature_norm_sq_512(const float* src, double& out_sum_sq) noexcept {
    if (src == nullptr) {
        return false;
    }
    double sum_sq = 0.0;
    for (size_t d = 0; d < kFusionFeatureDim; ++d) {
        const float v = src[d];
        if (!std::isfinite(v)) {
            return false;
        }
        sum_sq += static_cast<double>(v) * static_cast<double>(v);
    }
    out_sum_sq = sum_sq;
    return std::isfinite(sum_sq);
}

/** @brief 只校验不写入（融合关闭时的直通路径用，避免为判定可用性多算一遍归一化因子） */
inline bool feature_is_usable_512(const float* src) noexcept {
    double sum_sq = 0.0;
    return feature_norm_sq_512(src, sum_sq) && sum_sq > kFusionNormEpsilon * kFusionNormEpsilon;
}

/** @brief 校验并 L2 单位化 512 维特征；返回 false 时 `dst` 保持不变 */
inline bool normalize_feature_512(const float* src,
                                 std::array<float, kFusionFeatureDim>& dst) noexcept {
    double sum_sq = 0.0;
    if (!feature_norm_sq_512(src, sum_sq) || sum_sq <= kFusionNormEpsilon * kFusionNormEpsilon) {
        return false;
    }
    const double inv_norm = 1.0 / std::sqrt(sum_sq);
    for (size_t d = 0; d < kFusionFeatureDim; ++d) {
        dst[d] = static_cast<float>(static_cast<double>(src[d]) * inv_norm);
    }
    return true;
}

/**
 * @brief 512 维余弦相似度（`double` 累加）
 * @param out_similarity 成功时写入 [-1.0, 1.0] 点积
 * @return false 表示结果非有限；调用方必须按「不相似」跳过，**不要**用哨兵值比较
 */
inline bool cosine_similarity_512(const std::array<float, kFusionFeatureDim>& a,
                                  const std::array<float, kFusionFeatureDim>& b,
                                  float& out_similarity) noexcept {
    double dot = 0.0;
    for (size_t d = 0; d < kFusionFeatureDim; ++d) {
        dot += static_cast<double>(a[d]) * static_cast<double>(b[d]);
    }
    if (!std::isfinite(dot)) {
        return false;
    }
    out_similarity = static_cast<float>(dot);
    return true;
}

inline float sanitize_fusion_threshold(float raw) noexcept {
    if (!std::isfinite(raw)) {
        return kDefaultFusionSimilarityThreshold;
    }
    return std::clamp(raw, kMinFusionSimilarityThreshold, kMaxFusionSimilarityThreshold);
}

inline double sanitize_fusion_ttl_seconds(float raw) noexcept {
    if (!std::isfinite(raw)) {
        return static_cast<double>(kDefaultFusionTtlSeconds);
    }
    const float clamped = std::clamp(raw, kMinFusionTtlSeconds, kMaxFusionTtlSeconds);
    return static_cast<double>(clamped);
}

inline float sanitize_observation_weight(float quality) noexcept {
    if (!std::isfinite(quality)) {
        return kMinFusionWeightFloor;
    }
    const float clamped = std::clamp(quality, 0.0f, 1.0f);
    return std::max(clamped, kMinFusionWeightFloor);
}

inline const float* observation_view(const FusionFaceObservation& observation) noexcept {
    return observation.features == nullptr ? nullptr : observation.features->data();
}

inline float sanitize_observation_quality(float quality) noexcept {
    return std::isfinite(quality) ? std::clamp(quality, 0.0f, 1.0f) : 0.0f;
}

} // namespace detail

/**
 * @brief 单路视频流的有界特征融合状态池（非线程安全，并发保护在 `TaskFeatureFusionManager` 每路锁）
 */
class StreamFeatureFusionPool {
public:
    StreamFeatureFusionPool() {
        // 状态 6168 B/个：不做 reserve 的话池子扩张要反复搬 6 KB 对象（32 次扩容 ≈ 1.5 MB memcpy）
        states_.reserve(kMaxActiveStatesPerStream);
    }

    /**
     * @brief 融合关闭时的直通：原样拷贝单帧特征、`is_fused=false`，只做可用性判定。
     *
     * 与 `fuse_frame` 的关闭分支共用同一实现，避免「关融合」语义在两处各写一遍。
     */
    [[nodiscard]] static std::vector<FusedFaceOutput> passthrough(
        const std::vector<FusionFaceObservation>& observations) {
        std::vector<FusedFaceOutput> outputs(observations.size());
        for (size_t i = 0; i < observations.size(); ++i) {
            const float* src = detail::observation_view(observations[i]);
            outputs[i].valid = detail::feature_is_usable_512(src);
            if (outputs[i].valid) {
                std::copy_n(src, kFusionFeatureDim, outputs[i].features.begin());
            }
            outputs[i].is_fused = false;
        }
        return outputs;
    }

    /**
     * @brief 推进一帧合格人脸观测并产出用于 1:N 比对的特征
     *
     * @param observations 当前帧通过质量门禁的人脸特征列表
     * @param params 当前帧生效的运行参数（含开关、融合阈值、TTL）
     * @param now 当前单调时钟时间点（单测可显式注入确定性时间）
     * @return 与 `observations` 等长且一一对应的输出特征、`is_fused` 与 `valid` 标记
     */
    std::vector<FusedFaceOutput> fuse_frame(
        const std::vector<FusionFaceObservation>& observations,
        const MatchParams& params,
        std::chrono::steady_clock::time_point now = std::chrono::steady_clock::now()) {
        // 关闭融合：立即清空本路残余状态并保持单帧原始特征直通
        if (!params.enable_face_feature_fusion) {
            states_.clear();
            return passthrough(observations);
        }

        std::vector<FusedFaceOutput> outputs(observations.size());
        const float sim_threshold = detail::sanitize_fusion_threshold(params.fusion_similarity_threshold);
        const double ttl_seconds = detail::sanitize_fusion_ttl_seconds(params.fusion_ttl_seconds);

        evict_expired_states(now, ttl_seconds);
        if (observations.empty()) {
            return outputs;
        }

        // 归一化校验当帧观测（防止异常非有限向量污染状态池），同时把可用性写进输出
        std::vector<std::array<float, kFusionFeatureDim>> unit_obs(observations.size());
        std::vector<bool> obs_valid(observations.size(), false);
        for (size_t i = 0; i < observations.size(); ++i) {
            obs_valid[i] =
                detail::normalize_feature_512(detail::observation_view(observations[i]), unit_obs[i]);
            outputs[i].valid = obs_valid[i];
        }

        // 枚举通过「融合特征 + 锚点特征」双重门禁的候选配对边
        struct CandidateEdge {
            size_t obs_idx{0};
            size_t state_idx{0};
            float sim_fused{0.0f};
            float sim_anchor{0.0f};
        };

        std::vector<CandidateEdge> edges;
        edges.reserve(observations.size() * states_.size());
        for (size_t i = 0; i < observations.size(); ++i) {
            if (!obs_valid[i]) {
                continue;
            }
            for (size_t j = 0; j < states_.size(); ++j) {
                float sim_fused = 0.0f;
                if (!detail::cosine_similarity_512(unit_obs[i], states_[j].fused_features, sim_fused) ||
                    sim_fused < sim_threshold) {
                    continue;
                }
                float sim_anchor = 0.0f;
                if (!detail::cosine_similarity_512(unit_obs[i], states_[j].anchor_features, sim_anchor) ||
                    sim_anchor < sim_threshold) {
                    continue;
                }
                edges.push_back(CandidateEdge{i, j, sim_fused, sim_anchor});
            }
        }

        std::sort(edges.begin(), edges.end(), [](const CandidateEdge& a, const CandidateEdge& b) {
            if (a.sim_fused != b.sim_fused) {
                return a.sim_fused > b.sim_fused;
            }
            if (a.sim_anchor != b.sim_anchor) {
                return a.sim_anchor > b.sim_anchor;
            }
            if (a.obs_idx != b.obs_idx) {
                return a.obs_idx < b.obs_idx;
            }
            return a.state_idx < b.state_idx;
        });

        // 第一趟：只更新**已配对**状态。配对下标（`states_` 位置）严格活在这一块作用域内 ——
        // 第二趟会 erase 状态，任何跨趟保留的位置下标都会立刻失效。
        std::vector<bool> obs_paired(observations.size(), false);
        {
            std::vector<int> obs_to_state(observations.size(), -1);
            std::vector<bool> state_claimed(states_.size(), false);
            for (const auto& edge : edges) {
                if (obs_to_state[edge.obs_idx] >= 0 || state_claimed[edge.state_idx]) {
                    continue;
                }
                obs_to_state[edge.obs_idx] = static_cast<int>(edge.state_idx);
                state_claimed[edge.state_idx] = true;
            }

            for (size_t i = 0; i < observations.size(); ++i) {
                if (!obs_valid[i]) {
                    continue;
                }
                const int matched_idx = obs_to_state[i];
                if (matched_idx < 0) {
                    continue;
                }
                FusionTrackState& state = states_[static_cast<size_t>(matched_idx)];
                const float quality = detail::sanitize_observation_quality(observations[i].quality);
                update_matched_state(state, unit_obs[i], quality,
                                     detail::sanitize_observation_weight(quality), now);
                outputs[i].features = state.fused_features;
                outputs[i].is_fused = (state.observation_count >= 2);
                obs_paired[i] = true;
            }
        }

        // 第二趟：为未配对观测补建状态（新状态不参与同帧其他观测的二次融合）
        for (size_t i = 0; i < observations.size(); ++i) {
            if (!obs_valid[i] || obs_paired[i]) {
                continue;
            }
            const float quality = detail::sanitize_observation_quality(observations[i].quality);
            const float weight = detail::sanitize_observation_weight(quality);

            // 池满时只驱逐**本帧未被命中**的状态；全部在用则不建状态，本帧直出原始特征。
            // 代价是「同帧人数 > 容量」时最后进场的脸要等一轮才有状态，好处是绝不在刚更新完
            // 就把某个状态拆掉（否则该脸的 is_fused 会每帧 1/0 抖动，状态永远攒不起来）。
            if (states_.size() >= kMaxActiveStatesPerStream && !evict_one_stale_state(now)) {
                outputs[i].features = unit_obs[i];
                outputs[i].is_fused = false;
                continue;
            }

            states_.push_back(make_initial_state(unit_obs[i], quality, weight, now));
            outputs[i].features = unit_obs[i];
            outputs[i].is_fused = false;
        }

        return outputs;
    }

    /** @brief 当前池内活跃状态数量（供单测与诊断使用） */
    size_t state_count() const noexcept { return states_.size(); }

    /** @brief 显式清空池内所有状态 */
    void clear() noexcept { states_.clear(); }

private:
    struct FusionTrackState {
        std::array<float, kFusionFeatureDim> weighted_sum{};
        std::array<float, kFusionFeatureDim> fused_features{};
        std::array<float, kFusionFeatureDim> anchor_features{};
        float accum_weight{0.0f};
        float anchor_quality{0.0f};
        uint32_t observation_count{0};
        std::chrono::steady_clock::time_point last_seen{};
    };

    static FusionTrackState make_initial_state(
        const std::array<float, kFusionFeatureDim>& unit_feat,
        float quality,
        float weight,
        std::chrono::steady_clock::time_point now) noexcept {
        FusionTrackState state;
        for (size_t d = 0; d < kFusionFeatureDim; ++d) {
            state.weighted_sum[d] = weight * unit_feat[d];
        }
        state.fused_features = unit_feat;
        state.anchor_features = unit_feat;
        state.accum_weight = weight;
        state.anchor_quality = quality;
        state.observation_count = 1;
        state.last_seen = now;
        return state;
    }

    static void update_matched_state(
        FusionTrackState& state,
        const std::array<float, kFusionFeatureDim>& unit_feat,
        float quality,
        float weight,
        std::chrono::steady_clock::time_point now) noexcept {
        if (state.accum_weight + weight > kMaxAccumulatedWeight && state.accum_weight > 0.0f) {
            const float target_prev = std::max(0.0f, kMaxAccumulatedWeight - weight);
            const float decay = target_prev / state.accum_weight;
            for (size_t d = 0; d < kFusionFeatureDim; ++d) {
                state.weighted_sum[d] *= decay;
            }
            state.accum_weight *= decay;
        }

        for (size_t d = 0; d < kFusionFeatureDim; ++d) {
            state.weighted_sum[d] += weight * unit_feat[d];
        }
        state.accum_weight += weight;

        std::array<float, kFusionFeatureDim> normalized{};
        if (detail::normalize_feature_512(state.weighted_sum.data(), normalized)) {
            state.fused_features = normalized;
        } else {
            // 极端数值抵消保护：退回当前帧有效单位特征重锚定
            for (size_t d = 0; d < kFusionFeatureDim; ++d) {
                state.weighted_sum[d] = weight * unit_feat[d];
            }
            state.fused_features = unit_feat;
            state.accum_weight = weight;
        }

        if (quality > state.anchor_quality) {
            state.anchor_features = unit_feat;
            state.anchor_quality = quality;
        }
        if (state.observation_count < std::numeric_limits<uint32_t>::max()) {
            ++state.observation_count;
        }
        state.last_seen = now;
    }

    /** @brief 淘汰优先级：`last_seen` 更早 → `anchor_quality` 更低 → 下标更小（严格全序，结果确定） */
    static bool is_eviction_prior(const FusionTrackState& a, const FusionTrackState& b,
                                  size_t index_a, size_t index_b) noexcept {
        if (a.last_seen != b.last_seen) {
            return a.last_seen < b.last_seen;
        }
        if (a.anchor_quality != b.anchor_quality) {
            return a.anchor_quality < b.anchor_quality;
        }
        return index_a < index_b;
    }

    void evict_expired_states(std::chrono::steady_clock::time_point now, double ttl_seconds) {
        states_.erase(
            std::remove_if(states_.begin(), states_.end(),
                           [&](FusionTrackState& s) {
                               if (now < s.last_seen) {
                                   s.last_seen = now;
                                   return false;
                               }
                               const double age = std::chrono::duration<double>(now - s.last_seen).count();
                               return age > ttl_seconds;
                           }),
            states_.end());
    }

    /**
     * @brief 为新手腾位：只驱逐本帧**未被命中**的状态
     *
     * 「本帧命中过」用 `last_seen == now` 精确判定（`update_matched_state` 直接写入同一个
     * `time_point`）。全部在用（同帧人数超过容量）时返回 false，由调用方放弃建状态。
     */
    bool evict_one_stale_state(std::chrono::steady_clock::time_point now) {
        size_t victim = states_.size();
        for (size_t i = 0; i < states_.size(); ++i) {
            if (states_[i].last_seen == now) {
                continue;
            }
            if (victim == states_.size() ||
                is_eviction_prior(states_[i], states_[victim], i, victim)) {
                victim = i;
            }
        }
        if (victim == states_.size()) {
            return false;
        }
        states_.erase(states_.begin() + static_cast<std::ptrdiff_t>(victim));
        return true;
    }

    std::vector<FusionTrackState> states_;
};

/**
 * @brief 多路视频流跨帧特征融合管理器（按 `task_id` 分桶 + 每路独立互斥锁）
 */
class TaskFeatureFusionManager {
public:
    TaskFeatureFusionManager() = default;

    /**
     * @brief 推进指定任务流的跨帧融合
     *
     * 融合关闭时同样走这里：先清掉该流的残余状态再直通，语义只有 `StreamFeatureFusionPool` 一份实现。
     */
    std::vector<FusedFaceOutput> fuse_frame(
        uint64_t task_id,
        const std::vector<FusionFaceObservation>& observations,
        const MatchParams& params,
        std::chrono::steady_clock::time_point now = std::chrono::steady_clock::now()) {
        if (!params.enable_face_feature_fusion) {
            clear_stream_if_present(task_id);
            return StreamFeatureFusionPool::passthrough(observations);
        }
        const std::shared_ptr<TaskEntry> entry = entry_for(task_id, now);
        std::lock_guard<std::mutex> lock(entry->mutex);
        return entry->pool.fuse_frame(observations, params, now);
    }

    void clear_stream_if_present(uint64_t task_id) {
        std::shared_ptr<TaskEntry> entry;
        {
            std::lock_guard<std::mutex> lock(entries_mutex_);
            const auto it = entries_.find(task_id);
            if (it == entries_.end()) {
                return;
            }
            entry = it->second;
        }
        std::lock_guard<std::mutex> lock(entry->mutex);
        entry->pool.clear();
    }

    size_t state_count_for(uint64_t task_id) {
        std::shared_ptr<TaskEntry> entry;
        {
            std::lock_guard<std::mutex> lock(entries_mutex_);
            const auto it = entries_.find(task_id);
            if (it == entries_.end()) {
                return 0;
            }
            entry = it->second;
        }
        std::lock_guard<std::mutex> lock(entry->mutex);
        return entry->pool.state_count();
    }

    size_t stream_count() const {
        std::lock_guard<std::mutex> lock(entries_mutex_);
        return entries_.size();
    }

private:
    struct TaskEntry {
        std::mutex mutex;
        StreamFeatureFusionPool pool;
        std::chrono::steady_clock::time_point last_access{};
    };

    std::shared_ptr<TaskEntry> entry_for(uint64_t task_id,
                                         std::chrono::steady_clock::time_point now) {
        std::lock_guard<std::mutex> lock(entries_mutex_);
        const auto it = entries_.find(task_id);
        if (it != entries_.end()) {
            if (now >= it->second->last_access) {
                it->second->last_access = now;
            }
            return it->second;
        }

        if (entries_.size() >= kMaxTrackedStreams) {
            auto oldest_it = entries_.begin();
            for (auto cur = entries_.begin(); cur != entries_.end(); ++cur) {
                if (cur->second->last_access < oldest_it->second->last_access ||
                    (cur->second->last_access == oldest_it->second->last_access &&
                     cur->first < oldest_it->first)) {
                    oldest_it = cur;
                }
            }
            entries_.erase(oldest_it);
        }

        auto created = std::make_shared<TaskEntry>();
        created->last_access = now;
        entries_.emplace(task_id, created);
        return created;
    }

    mutable std::mutex entries_mutex_;
    std::unordered_map<uint64_t, std::shared_ptr<TaskEntry>> entries_;
};

} // namespace zhulong::face
