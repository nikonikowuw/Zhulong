/**
 * @file face_feature_fusion.hpp
 * @brief 单路视频流有界人脸轨迹池：空间优先关联、Top-K 高质量帧融合与轨迹级身份（纯数据逻辑 Header-only）
 *
 * 设计要点：
 *   1. 按任务上下文（`TaskContext::task_id`，一路 broker 一路流）隔离状态池，每路独立互斥锁，
 *      多相机并发推理零锁争用；`shared_ptr<TaskEntry>` 保证桶淘汰期间在途调用绝不悬垂。
 *   2. **空间优先 + 特征否决**的关联：观测框与状态上一帧框空间连续（IoU / 中心位移 + 尺度比，
 *      且距上次观测不超过 `kSpatialMaxGapSeconds`）时，特征只需 `>= fusion_spatial_similarity_threshold`
 *      （防串人否决）；否则回落纯特征门槛 `fusion_similarity_threshold`。两种门槛都同时校验
 *      「融合特征」与「最高质量锚点」，阻断顺着均值渐变漂移串人。
 *   3. 整帧一对一贪心配对：按 `(空间一致 desc, sim_fused desc, sim_anchor desc, obs_idx asc, state_idx asc)`
 *      全局排序，同帧多张脸绝不串入同一状态，空间连续的真主优先夺得原状态。
 *   4. **Top-K 帧库 + 相对质量 × 锚点一致性加权**：每状态最多保留 `kFusionTopFrames` 帧单位特征，
 *      第 k 帧的融合权重 `w_k = exp((q_k - q_anchor) / τ) · g(cos(f_k, anchor))`，融合 = 加权和 +
 *      严格 L2 归一化：
 *        - **相对质量**（softmax 温度 τ = `kFusionQualityTemperature`）：只看与锚点的质量差，
 *          与质量分的绝对标定无关（onnx FP32 与 rk3568 INT8 的 eDifFIQA 分数存在整体偏移）；
 *          旧的线性权重 `max(q, 0.05)` 让 0.35 的侧脸帧拿到 0.70 正脸帧一半的权重，早期
 *          （库未满）融合特征会被首批困难帧明显拉偏。
 *        - **锚点一致性** `g`：与锚点余弦从 `kFusionConsistencyLow` 到 `kFusionConsistencyHigh`
 *          线性升到满权。空间低门槛（默认 0.40）放进来的困难帧只负责维持轨迹连续，
 *          不再以满权污染模板。
 *      满库时按同一效用淘汰（锚点除外，等效用先淘汰更早的帧）；新帧效用低于最差帧时只维持
 *      轨迹、不进入特征。锚点恒为库内最高质量帧，且永不被挤出。
 *   5. 每状态挂一个 `TrackIdentityState`（见 face_track_identity.hpp），由调用方在比对后经
 *      `observe_identity` 推进；轨迹键 `track_key` **只在包内使用**，不得写入 IPC 的 `track_id`
 *      （spec：`track_id` 归宿主独占）。
 *
 * 内存：单个状态约 12.6 KiB（6 组 512 维 float + 身份与元数据），单路满额 32 状态 ≈ 400 KiB；
 *       状态按需分配，20 路、每路 5 人的典型场景约 1.3 MiB。
 */

#pragma once

#include "face_track_identity.hpp"
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
#include <optional>
#include <unordered_map>
#include <vector>

namespace zhulong::face {

/** @brief 人脸特征向量固定维度（与人脸特征提取模型输出一致） */
inline constexpr size_t kFusionFeatureDim = 512;
/** @brief 单路视频流同时维护的活跃人脸状态上限（满额时优先淘汰本帧未命中的状态） */
inline constexpr size_t kMaxActiveStatesPerStream = 32;
/** @brief 进程内最多保留的任务流分桶数上限（防止异常动态 task_id 造成空桶累积） */
inline constexpr size_t kMaxTrackedStreams = 256;
/** @brief 每状态参与融合的最高质量帧数（Top-K 帧库容量） */
inline constexpr size_t kFusionTopFrames = 5;
/**
 * @brief 相对质量权重的 softmax 温度：质量比锚点低 Δq 的帧权重为 `exp(-Δq / τ)`
 *
 * τ = 0.10 时 Δq = 0.05 → 0.61，0.10 → 0.37，0.20 → 0.14，0.30 → 0.05：同一姿态帧间
 * ±0.03 级的质量噪声几乎不影响权重，而明显更差的帧迅速失去话语权。锚点权重恒为 1，
 * 因此权重和恒 `>= 1`，不存在旧线性权重在 `min_face_quality = 0` 时的零权重退化。
 */
inline constexpr double kFusionQualityTemperature = 0.10;
/** @brief 锚点一致性权重：与锚点余弦 `<=` 该值的帧不参与融合 */
inline constexpr float kFusionConsistencyLow = 0.30f;
/** @brief 锚点一致性权重：与锚点余弦 `>=` 该值的帧满权参与融合（同人正常帧间余弦通常更高） */
inline constexpr float kFusionConsistencyHigh = 0.70f;
/** @brief L2 模长有效下界 */
inline constexpr double kFusionNormEpsilon = 1e-6;

/** @brief 空间一致判据：框 IoU 达到该值即视为同一位置 */
inline constexpr float kSpatialMinIoU = 0.10f;
/** @brief 空间一致判据：中心位移（按两框平均宽/高分轴归一化后的欧氏距离）上限 */
inline constexpr float kSpatialMaxCenterShift = 1.5f;
/** @brief 空间一致判据：两框面积开方比的允许区间（走近/远离时人脸尺度逐帧变化） */
inline constexpr float kSpatialMinScaleRatio = 0.5f;
inline constexpr float kSpatialMaxScaleRatio = 2.0f;
/**
 * @brief 空间证据的时效：距状态上次观测超过该秒数后不再承认空间连续
 *
 * 门口场景里 A 离开、B 在同一位置出现是常态；间隔一长，「位置相同」就不再说明「是同一个人」，
 * 此时必须回到纯特征的高门槛。
 */
inline constexpr double kSpatialMaxGapSeconds = 1.0;

/**
 * @brief 单帧单张已通过质量门禁的人脸特征观测
 *
 * 特征是**定长数组指针**而不是 `const float*`：长度由类型固定，池内不可能是短读或错读。
 * `has_box=false`（旧调用形态 / 单测）时只走纯特征关联，行为与引入空间关联前一致。
 */
struct FusionFaceObservation {
    const std::array<float, kFusionFeatureDim>* features{nullptr}; /**< 512 维 L2 单位特征视图 */
    float quality{0.0f};                                           /**< eDifFIQA(T) 质量分 [0.0, 1.0] */
    std::array<float, 4> box{0.0f, 0.0f, 0.0f, 0.0f};              /**< 归一化人脸框 [x1, y1, x2, y2] */
    bool has_box{false};                                           /**< box 是否有效 */
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
    /**
     * @brief 包内轨迹键（0 = 本帧没有归属任何状态，例如池满或融合关闭）
     *
     * ⚠️ 只用于回调 `observe_identity`；**不得**写入结果的 `track_id`（宿主独占）。
     */
    uint64_t track_key{0};
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

inline float sanitize_spatial_fusion_threshold(float raw) noexcept {
    if (!std::isfinite(raw)) {
        return kDefaultFusionSpatialSimilarityThreshold;
    }
    return std::clamp(raw, kMinFusionSpatialSimilarityThreshold, kMaxFusionSpatialSimilarityThreshold);
}

inline double sanitize_fusion_ttl_seconds(float raw) noexcept {
    if (!std::isfinite(raw)) {
        return static_cast<double>(kDefaultFusionTtlSeconds);
    }
    const float clamped = std::clamp(raw, kMinFusionTtlSeconds, kMaxFusionTtlSeconds);
    return static_cast<double>(clamped);
}

/**
 * @brief 帧库内单帧的融合效用（即融合权重）：相对锚点质量的 softmax × 锚点一致性
 *
 * @param quality        该帧质量（已 sanitize 到 [0, 1]）
 * @param anchor_quality 锚点质量（已 sanitize）
 * @param cos_to_anchor  该帧与锚点的余弦；非有限按 0 处理
 * @return `[0, +inf)`；锚点自身恒为 1。质量高于锚点（仅在评估「新帧能否入库」时出现）返回 `> 1`
 */
inline double fusion_utility(float quality, float anchor_quality, float cos_to_anchor) noexcept {
    if (!std::isfinite(cos_to_anchor) || cos_to_anchor <= kFusionConsistencyLow) {
        return 0.0;
    }
    const double consistency =
        cos_to_anchor >= kFusionConsistencyHigh
            ? 1.0
            : static_cast<double>(cos_to_anchor - kFusionConsistencyLow) /
                  static_cast<double>(kFusionConsistencyHigh - kFusionConsistencyLow);
    const double delta_q = static_cast<double>(quality) - static_cast<double>(anchor_quality);
    return std::exp(delta_q / kFusionQualityTemperature) * consistency;
}

inline const float* observation_view(const FusionFaceObservation& observation) noexcept {
    return observation.features == nullptr ? nullptr : observation.features->data();
}

inline float sanitize_observation_quality(float quality) noexcept {
    return std::isfinite(quality) ? std::clamp(quality, 0.0f, 1.0f) : 0.0f;
}

/** @brief 归一化框是否可用作空间证据（有限、正宽高） */
inline bool box_is_usable(const std::array<float, 4>& box) noexcept {
    for (const float v : box) {
        if (!std::isfinite(v)) {
            return false;
        }
    }
    return box[2] > box[0] && box[3] > box[1];
}

/**
 * @brief 两个归一化人脸框是否空间连续
 *
 * 中心位移按两框**平均宽/高分轴**归一化：归一化坐标在 16:9 画面里横纵尺度不同，
 * 分轴相对化之后判据与画面长宽比、分辨率无关。
 */
inline bool boxes_spatially_consistent(const std::array<float, 4>& a, const std::array<float, 4>& b) noexcept {
    if (!box_is_usable(a) || !box_is_usable(b)) {
        return false;
    }
    const float wa = a[2] - a[0];
    const float ha = a[3] - a[1];
    const float wb = b[2] - b[0];
    const float hb = b[3] - b[1];
    const float area_a = wa * ha;
    const float area_b = wb * hb;
    if (!(area_a > 0.0f) || !(area_b > 0.0f)) {
        return false;
    }

    const float scale_ratio = std::sqrt(area_a / area_b);
    if (!std::isfinite(scale_ratio) || scale_ratio < kSpatialMinScaleRatio || scale_ratio > kSpatialMaxScaleRatio) {
        return false;
    }

    const float ix = std::max(0.0f, std::min(a[2], b[2]) - std::max(a[0], b[0]));
    const float iy = std::max(0.0f, std::min(a[3], b[3]) - std::max(a[1], b[1]));
    const float inter = ix * iy;
    const float uni = area_a + area_b - inter;
    if (uni > 0.0f && inter / uni >= kSpatialMinIoU) {
        return true;
    }

    const float dx = ((a[0] + a[2]) - (b[0] + b[2])) * 0.5f / ((wa + wb) * 0.5f);
    const float dy = ((a[1] + a[3]) - (b[1] + b[3])) * 0.5f / ((ha + hb) * 0.5f);
    const float shift = std::hypot(dx, dy);
    return std::isfinite(shift) && shift <= kSpatialMaxCenterShift;
}

} // namespace detail

/**
 * @brief 单路视频流的有界轨迹池（非线程安全，并发保护在 `TaskFeatureFusionManager` 每路锁）
 */
class StreamFeatureFusionPool {
public:
    StreamFeatureFusionPool() = default;

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
            outputs[i].track_key = 0;
        }
        return outputs;
    }

    /**
     * @brief 推进一帧合格人脸观测并产出用于 1:N 比对的特征
     *
     * @param observations 当前帧通过质量门禁的人脸特征列表
     * @param params 当前帧生效的运行参数（含开关、融合阈值、TTL）
     * @param now 当前单调时钟时间点（单测可显式注入确定性时间）
     * @return 与 `observations` 等长且一一对应的输出特征、`is_fused`、`valid` 与 `track_key`
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
        const float feature_threshold = detail::sanitize_fusion_threshold(params.fusion_similarity_threshold);
        const float spatial_threshold =
            detail::sanitize_spatial_fusion_threshold(params.fusion_spatial_similarity_threshold);
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

        // 枚举通过门禁的候选配对边：空间连续走低门槛否决，否则走纯特征高门槛
        struct CandidateEdge {
            size_t obs_idx{0};
            size_t state_idx{0};
            float sim_fused{0.0f};
            float sim_anchor{0.0f};
            bool spatial{false};
        };

        std::vector<CandidateEdge> edges;
        edges.reserve(observations.size() * states_.size());
        for (size_t i = 0; i < observations.size(); ++i) {
            if (!obs_valid[i]) {
                continue;
            }
            for (size_t j = 0; j < states_.size(); ++j) {
                const FusionTrackState& state = states_[j];
                float sim_fused = 0.0f;
                float sim_anchor = 0.0f;
                if (!detail::cosine_similarity_512(unit_obs[i], state.fused_features, sim_fused) ||
                    !detail::cosine_similarity_512(unit_obs[i], state.bank[state.anchor_index].features,
                                                   sim_anchor)) {
                    continue;
                }
                const bool spatial = spatially_continuous(observations[i], state, now);
                const float gate = spatial ? spatial_threshold : feature_threshold;
                if (sim_fused < gate || sim_anchor < gate) {
                    continue;
                }
                edges.push_back(CandidateEdge{i, j, sim_fused, sim_anchor, spatial});
            }
        }

        std::sort(edges.begin(), edges.end(), [](const CandidateEdge& a, const CandidateEdge& b) {
            if (a.spatial != b.spatial) {
                return a.spatial;
            }
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
                update_matched_state(state, unit_obs[i], quality, observations[i], now);
                outputs[i].features = state.fused_features;
                outputs[i].is_fused = (state.observation_count >= 2);
                outputs[i].track_key = state.key;
                obs_paired[i] = true;
            }
        }

        // 第二趟：为未配对观测补建状态（新状态不参与同帧其他观测的二次融合）
        for (size_t i = 0; i < observations.size(); ++i) {
            if (!obs_valid[i] || obs_paired[i]) {
                continue;
            }
            const float quality = detail::sanitize_observation_quality(observations[i].quality);

            // 池满时只驱逐**本帧未被命中**的状态；全部在用则不建状态，本帧直出原始特征。
            // 代价是「同帧人数 > 容量」时最后进场的脸要等一轮才有状态，好处是绝不在刚更新完
            // 就把某个状态拆掉（否则该脸的 is_fused 会每帧 1/0 抖动，状态永远攒不起来）。
            if (states_.size() >= kMaxActiveStatesPerStream && !evict_one_stale_state(now)) {
                outputs[i].features = unit_obs[i];
                outputs[i].is_fused = false;
                outputs[i].track_key = 0;
                continue;
            }

            states_.push_back(make_initial_state(unit_obs[i], quality, observations[i], now));
            outputs[i].features = unit_obs[i];
            outputs[i].is_fused = false;
            outputs[i].track_key = states_.back().key;
        }

        return outputs;
    }

    /**
     * @brief 用本帧比对证据推进某条轨迹的身份决策
     *
     * @return 轨迹已不存在（被淘汰 / 键为 0）时返回空，调用方回落逐帧决策
     */
    std::optional<TrackIdentityDecision> observe_identity(uint64_t track_key,
                                                          const IdentityEvidence& strict,
                                                          const IdentityEvidence& vote,
                                                          const MatchParams& params) {
        if (track_key == 0) {
            return std::nullopt;
        }
        for (auto& state : states_) {
            if (state.key == track_key) {
                return state.identity.observe(strict, vote, params);
            }
        }
        return std::nullopt;
    }

    /** @brief 当前池内活跃状态数量（供单测与诊断使用） */
    size_t state_count() const noexcept { return states_.size(); }

    /** @brief 显式清空池内所有状态 */
    void clear() noexcept { states_.clear(); }

private:
    struct BankEntry {
        std::array<float, kFusionFeatureDim> features{};
        float quality{0.0f};
        uint64_t seq{0}; /**< 入库序号：等质量淘汰时先淘汰更早的帧 */
    };

    struct FusionTrackState {
        uint64_t key{0};
        std::array<BankEntry, kFusionTopFrames> bank{};
        size_t bank_size{0};
        size_t anchor_index{0}; /**< 库内最高质量帧下标（等质量取更早入库者） */
        uint64_t next_seq{0};
        std::array<float, kFusionFeatureDim> fused_features{};
        std::array<float, 4> last_box{0.0f, 0.0f, 0.0f, 0.0f};
        bool has_box{false};
        uint32_t observation_count{0};
        std::chrono::steady_clock::time_point last_seen{};
        TrackIdentityState identity{};

        float anchor_quality() const noexcept { return bank[anchor_index].quality; }
    };

    static bool spatially_continuous(const FusionFaceObservation& observation,
                                     const FusionTrackState& state,
                                     std::chrono::steady_clock::time_point now) noexcept {
        if (!observation.has_box || !state.has_box) {
            return false;
        }
        const double gap = std::chrono::duration<double>(now - state.last_seen).count();
        if (!(gap <= kSpatialMaxGapSeconds)) {
            return false;
        }
        return detail::boxes_spatially_consistent(observation.box, state.last_box);
    }

    static void record_box(FusionTrackState& state, const FusionFaceObservation& observation) noexcept {
        if (observation.has_box && detail::box_is_usable(observation.box)) {
            state.last_box = observation.box;
            state.has_box = true;
        }
        // 本帧无可用框时保留旧框：它仍是该轨迹最近一次可信的位置
    }

    FusionTrackState make_initial_state(
        const std::array<float, kFusionFeatureDim>& unit_feat,
        float quality,
        const FusionFaceObservation& observation,
        std::chrono::steady_clock::time_point now) {
        FusionTrackState state;
        state.key = next_key_++;
        state.bank[0].features = unit_feat;
        state.bank[0].quality = quality;
        state.bank[0].seq = state.next_seq++;
        state.bank_size = 1;
        state.anchor_index = 0;
        state.fused_features = unit_feat;
        state.observation_count = 1;
        state.last_seen = now;
        record_box(state, observation);
        return state;
    }

    /**
     * @brief 帧库内某帧相对当前锚点的融合效用（锚点自身恒为 1，免受余弦舍入影响）
     */
    static double entry_utility(const FusionTrackState& state, size_t k) noexcept {
        if (k == state.anchor_index) {
            return 1.0;
        }
        return candidate_utility(state, state.bank[k].features, state.bank[k].quality);
    }

    /** @brief 任意单位特征（库内帧或待入库新帧）相对当前锚点的融合效用 */
    static double candidate_utility(const FusionTrackState& state,
                                    const std::array<float, kFusionFeatureDim>& unit_feat,
                                    float quality) noexcept {
        const BankEntry& anchor = state.bank[state.anchor_index];
        float cos_to_anchor = 0.0f;
        if (!detail::cosine_similarity_512(unit_feat, anchor.features, cos_to_anchor)) {
            return 0.0;
        }
        return detail::fusion_utility(quality, anchor.quality, cos_to_anchor);
    }

    /**
     * @brief 把新帧放进 Top-K 帧库
     *
     * 未满直接入库；满库时在**非锚点**帧里找效用最低者（等效用先淘汰更早入库的帧），新帧效用
     * 不低于它才替换。按效用而不是按原始质量淘汰：锚点更换后与新锚点不一致的「死帧」权重
     * 已经趋零，若仍按质量保留，就会长期占着名额却不贡献特征。
     */
    static void insert_into_bank(FusionTrackState& state,
                                 const std::array<float, kFusionFeatureDim>& unit_feat,
                                 float quality) noexcept {
        static_assert(kFusionTopFrames >= 2, "满库淘汰要求锚点之外至少还有一个候选槽位");
        size_t slot = state.bank_size;
        if (state.bank_size >= kFusionTopFrames) {
            size_t victim = state.bank_size;
            double victim_utility = 0.0;
            for (size_t k = 0; k < state.bank_size; ++k) {
                if (k == state.anchor_index) {
                    continue;
                }
                const double u = entry_utility(state, k);
                if (victim == state.bank_size || u < victim_utility ||
                    (u == victim_utility && state.bank[k].seq < state.bank[victim].seq)) {
                    victim = k;
                    victim_utility = u;
                }
            }
            if (candidate_utility(state, unit_feat, quality) < victim_utility) {
                return; // 效用不及库里最差的帧：只维持轨迹，不进入特征
            }
            slot = victim;
        } else {
            ++state.bank_size;
        }
        state.bank[slot].features = unit_feat;
        state.bank[slot].quality = quality;
        state.bank[slot].seq = state.next_seq++;
    }

    static void refresh_anchor(FusionTrackState& state) noexcept {
        size_t best = 0;
        for (size_t k = 1; k < state.bank_size; ++k) {
            const BankEntry& cur = state.bank[k];
            const BankEntry& top = state.bank[best];
            if (cur.quality > top.quality || (cur.quality == top.quality && cur.seq < top.seq)) {
                best = k;
            }
        }
        state.anchor_index = best;
    }

    /**
     * @brief 以「相对质量 softmax × 锚点一致性」加权求和并 L2 归一化；数值相消退化时退回锚点帧
     *
     * 锚点权重恒为 1，所以权重和恒 `>= 1`；退化分支只防御理论上的数值异常。
     */
    static void refresh_fused_features(FusionTrackState& state) noexcept {
        std::array<double, kFusionFeatureDim> acc{};
        for (size_t k = 0; k < state.bank_size; ++k) {
            const double w = entry_utility(state, k);
            if (!(w > 0.0) || !std::isfinite(w)) {
                continue;
            }
            const auto& f = state.bank[k].features;
            for (size_t d = 0; d < kFusionFeatureDim; ++d) {
                acc[d] += w * static_cast<double>(f[d]);
            }
        }
        double sum_sq = 0.0;
        for (const double v : acc) {
            sum_sq += v * v;
        }
        if (!std::isfinite(sum_sq) || sum_sq <= kFusionNormEpsilon * kFusionNormEpsilon) {
            state.fused_features = state.bank[state.anchor_index].features;
            return;
        }
        const double inv_norm = 1.0 / std::sqrt(sum_sq);
        for (size_t d = 0; d < kFusionFeatureDim; ++d) {
            state.fused_features[d] = static_cast<float>(acc[d] * inv_norm);
        }
    }

    static void update_matched_state(
        FusionTrackState& state,
        const std::array<float, kFusionFeatureDim>& unit_feat,
        float quality,
        const FusionFaceObservation& observation,
        std::chrono::steady_clock::time_point now) noexcept {
        insert_into_bank(state, unit_feat, quality);
        refresh_anchor(state);
        refresh_fused_features(state);
        record_box(state, observation);
        if (state.observation_count < std::numeric_limits<uint32_t>::max()) {
            ++state.observation_count;
        }
        state.last_seen = now;
    }

    /** @brief 淘汰优先级：`last_seen` 更早 → 锚点质量更低 → 下标更小（严格全序，结果确定） */
    static bool is_eviction_prior(const FusionTrackState& a, const FusionTrackState& b,
                                  size_t index_a, size_t index_b) noexcept {
        if (a.last_seen != b.last_seen) {
            return a.last_seen < b.last_seen;
        }
        if (a.anchor_quality() != b.anchor_quality()) {
            return a.anchor_quality() < b.anchor_quality();
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
    /** @brief 本路单调递增的轨迹键（0 保留为「无轨迹」） */
    uint64_t next_key_{1};
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

    /**
     * @brief 推进某路流某条轨迹的身份决策（不创建分桶）
     * @return 分桶或轨迹不存在时返回空，调用方回落逐帧决策
     */
    std::optional<TrackIdentityDecision> observe_identity(uint64_t task_id,
                                                          uint64_t track_key,
                                                          const IdentityEvidence& strict,
                                                          const IdentityEvidence& vote,
                                                          const MatchParams& params) {
        if (track_key == 0) {
            return std::nullopt;
        }
        const std::shared_ptr<TaskEntry> entry = find_entry(task_id);
        if (!entry) {
            return std::nullopt;
        }
        std::lock_guard<std::mutex> lock(entry->mutex);
        return entry->pool.observe_identity(track_key, strict, vote, params);
    }

    void clear_stream_if_present(uint64_t task_id) {
        const std::shared_ptr<TaskEntry> entry = find_entry(task_id);
        if (!entry) {
            return;
        }
        std::lock_guard<std::mutex> lock(entry->mutex);
        entry->pool.clear();
    }

    size_t state_count_for(uint64_t task_id) {
        const std::shared_ptr<TaskEntry> entry = find_entry(task_id);
        if (!entry) {
            return 0;
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

    std::shared_ptr<TaskEntry> find_entry(uint64_t task_id) const {
        std::lock_guard<std::mutex> lock(entries_mutex_);
        const auto it = entries_.find(task_id);
        return it == entries_.end() ? nullptr : it->second;
    }

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
