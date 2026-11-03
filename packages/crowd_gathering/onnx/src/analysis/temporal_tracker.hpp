/**
 * @file temporal_tracker.hpp
 * @brief 基于簇级质心/包络框跟踪与驻留积分防抖的时序状态机
 */

#pragma once

#include "spatial_clustering.hpp"

#include <array>
#include <chrono>
#include <cstddef>
#include <cstdint>
#include <memory>
#include <mutex>
#include <unordered_map>
#include <vector>

namespace zhulong::crowd {

/** @brief 偶发漏检或跳帧的宽限保持期（秒），是「时长比例宽限」的下界 */
inline constexpr float kDefaultGracePeriodSeconds = 1.5f;
/**
 * @brief 宽限期随告警时长伸展的比例
 *
 * 固定 1.5s 的宽限期与 `gather_duration_seconds` 是耦合的：配置成 30s/60s 时，
 * 任何一次 >1.5s 的漏检都会清掉 track、把 `first_seen` 归零，滞留积分永远攒不满 ——
 * 表现是「阈值调长了反而不报警」。宽限期必须随目标时长一起放宽。
 */
inline constexpr float kGracePeriodDurationRatio = 0.25f;

/**
 * @brief 按告警时长换算宽限期
 */
inline float grace_period_seconds(float duration_seconds) noexcept {
    const float scaled = (duration_seconds > 0.0f) ? duration_seconds * kGracePeriodDurationRatio : 0.0f;
    return (scaled > kDefaultGracePeriodSeconds) ? scaled : kDefaultGracePeriodSeconds;
}

/**
 * @brief 确认触发告警的聚集事件
 */
struct ConfirmedAlarm {
    std::array<float, 4> box{0.0f, 0.0f, 0.0f, 0.0f}; /**< 聚集簇外接大框 [x1, y1, x2, y2] ∈ [0, 1] */
    float score{0.0f};                                /**< 告警置信度 */
    int person_count{0};                              /**< 簇内人数 */
    float dwell_seconds{0.0f};                         /**< 滞留时间（秒） */
};

struct TrackedCluster {
    std::array<float, 4> box{0.0f, 0.0f, 0.0f, 0.0f};
    float centroid_x{0.0f};
    float centroid_y{0.0f};
    int person_count{0};
    float score{0.0f};
    std::chrono::steady_clock::time_point first_seen;
    std::chrono::steady_clock::time_point last_seen;
    bool alarmed{false};
};

/**
 * @brief 单路视频流的簇跟踪状态（由 `TemporalTracker` 按 task 隔离持有）
 *
 * 非线程安全：并发保护在 `TemporalTracker` 的每路锁上，本类只做纯时序推进。
 */
class TaskStreamTracker {
public:
    TaskStreamTracker() = default;

    /**
     * @brief 更新当前帧候选簇并输出达到驻留时长的告警
     *
     * @param candidates 当前帧几何聚类候选簇
     * @param duration_seconds 聚集持续告警时长阈值
     * @param now 当前时间戳
     * @return 触发告警的聚集簇列表
     */
    std::vector<ConfirmedAlarm> update(
        const std::vector<ClusterCandidate>& candidates,
        float duration_seconds,
        std::chrono::steady_clock::time_point now
    );

    /** @brief 当前仍在跟踪的簇数量（自测与诊断用） */
    std::size_t track_count() const noexcept { return tracks_.size(); }

private:
    /** @brief 高 IoU 或质心邻近即视为同一簇；返回匹配得分，<0 表示不匹配 */
    float match_score(const ClusterCandidate& candidate, const TrackedCluster& track) const noexcept;

    /** @brief 阶段 1：把候选簇匹配到已有 track 上；返回逐候选的「已匹配」掩码 */
    std::vector<bool> match_existing_tracks(
        const std::vector<ClusterCandidate>& candidates,
        float duration_seconds,
        std::chrono::steady_clock::time_point now
    );

    /** @brief 阶段 2：未匹配的候选簇入列新生 track */
    void append_new_tracks(
        const std::vector<ClusterCandidate>& candidates,
        const std::vector<bool>& cand_matched,
        float duration_seconds,
        std::chrono::steady_clock::time_point now
    );

    /** @brief 阶段 3：淘汰超过宽限期未出现的 track */
    void evict_stale_tracks(float duration_seconds, std::chrono::steady_clock::time_point now);

    /** @brief 阶段 4：收集本帧应上报的告警 */
    std::vector<ConfirmedAlarm> collect_alarms(std::chrono::steady_clock::time_point now) const;

    std::vector<TrackedCluster> tracks_;
};

/**
 * @brief 多路任务时序状态机（每路独立跟踪 + 每路独立加锁）
 *
 * ⚠️ 为什么不用一把全局锁：包进程按「一路 broker 一个 Worker」并发跑多路，全局锁会把
 *    互不相干的相机串行化，而时序更新本身只有微秒级 —— 锁竞争比计算本身还贵。
 */
class TemporalTracker {
public:
    TemporalTracker() = default;

    /**
     * @brief 逐路视频流时序状态推进与告警研判
     *
     * @param task_id 任务 ID（一路 broker 一个，等于 stream_id）
     * @param candidates 当前帧检测到的聚集簇候选
     * @param duration_seconds 聚集滞留时长阈值（秒）
     * @return 达到滞留时长的告警结果列表
     */
    std::vector<ConfirmedAlarm> process(
        uint64_t task_id,
        const std::vector<ClusterCandidate>& candidates,
        float duration_seconds
    );

private:
    /** @brief 单路状态容器：锁随数据走（CP.50），地址稳定（unique_ptr，不随 rehash 搬家） */
    struct TaskEntry {
        std::mutex mutex;
        TaskStreamTracker tracker;
    };

    /** @brief 取（必要时创建）指定任务的状态容器 */
    TaskEntry& entry_for(uint64_t task_id);

    std::mutex entries_mutex_;
    std::unordered_map<uint64_t, std::unique_ptr<TaskEntry>> entries_;
};

} // namespace zhulong::crowd
