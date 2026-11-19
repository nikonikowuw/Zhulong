/**
 * @file violation_tracker.hpp
 * @brief 安全帽违规的时序确认（按 task_id 隔离）
 *
 * 单帧 head_nohelmet 可能来自侧脸、逆光、遮挡，直接告警会产生大量瞬时误报。
 * 这里按人体框 IoU 把跨帧结论串成轨迹：违规连续 `confirm_frames` 次观测才上报，
 * 任一次合规观测把计数清零；超过 15 帧未匹配的轨迹淘汰。
 *
 * 线程模型：一个 task_id 同时只会有一个 Worker 调用；内部仍用互斥量保护任务表，
 * 以支持多路流并发。
 */

#pragma once

#include <zhulong/algo.hpp>

#include <cstdint>
#include <mutex>
#include <unordered_map>
#include <vector>

#include "analysis/helmet_association.hpp"

namespace zhulong::ppe {

class ViolationTracker {
public:
    /** @brief 轨迹匹配 IoU 下限 */
    static constexpr float kTrackIouThreshold = 0.30f;
    /** @brief 轨迹未匹配多少帧后淘汰 */
    static constexpr int kMaxMissedFrames = 15;
    /** @brief 单任务轨迹上限，防止异常输入无限增长 */
    static constexpr size_t kMaxTracks = 128;

    /**
     * @brief 输入本帧人员结论，返回已确认的违规人员框
     *
     * @param task_id 任务身份（一路流一个）
     * @param decisions 本帧有头部证据的人员结论（可能为空）
     * @param confirm_frames 连续违规观测次数（内部 clamp 到 1..10）
     * @return 已确认的 `no_helmet` 检测框，置信度取 person/head 的较小值
     */
    std::vector<zhulong::Detection> confirm(uint64_t task_id,
                                            const std::vector<HelmetDecision>& decisions,
                                            int confirm_frames);

    /** @brief 当前任务存活轨迹数（诊断/单测用） */
    size_t track_count(uint64_t task_id) const;

    /** @brief 清空指定任务的轨迹 */
    void reset(uint64_t task_id);

private:
    struct Track {
        uint64_t id{0};
        float box[4]{0.0f, 0.0f, 0.0f, 0.0f};
        int streak{0};
        int missed{0};
    };
    struct TaskStream {
        std::vector<Track> tracks;
        uint64_t next_id{1};
    };

    mutable std::mutex mutex_;
    std::unordered_map<uint64_t, TaskStream> tasks_;
};

} // namespace zhulong::ppe
