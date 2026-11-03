/**
 * @file temporal_tracker.hpp
 * @brief 基于簇级质心/包络框跟踪与驻留积分防抖的时序状态机
 */

#pragma once

#include "spatial_clustering.hpp"

#include <array>
#include <chrono>
#include <cstdint>
#include <mutex>
#include <unordered_map>
#include <vector>

namespace zhulong::crowd {

/** @brief 偶发漏检或跳帧的宽限保持期（秒） */
inline constexpr float kDefaultGracePeriodSeconds = 1.5f;

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
    uint64_t track_id{0};
    std::array<float, 4> box{0.0f, 0.0f, 0.0f, 0.0f};
    float centroid_x{0.0f};
    float centroid_y{0.0f};
    int person_count{0};
    float score{0.0f};
    std::chrono::steady_clock::time_point first_seen;
    std::chrono::steady_clock::time_point last_seen;
    bool alarmed{false};
};

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

private:
    uint64_t next_track_id_{1};
    std::vector<TrackedCluster> tracks_;
};

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

    /**
     * @brief 清除指定任务的时序状态
     */
    void reset_task(uint64_t task_id);

private:
    std::mutex mutex_;
    std::unordered_map<uint64_t, TaskStreamTracker> stream_trackers_;
};

} // namespace zhulong::crowd
