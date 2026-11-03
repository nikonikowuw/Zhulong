/**
 * @file temporal_tracker.cpp
 * @brief 时序状态机与驻留积分跟踪实现
 */

#include "temporal_tracker.hpp"

#include <algorithm>
#include <cmath>

namespace zhulong::crowd {

namespace {

/** @brief 判为同一簇的 IoU 下界 */
constexpr float kTrackMatchIouThreshold = 0.15f;
/** @brief 判为同一簇的质心归一化距离上界 */
constexpr float kTrackMatchCentroidDistance = 0.15f;
/** @brief 时长为 0 时「单帧命中即告警」的判定容差（秒） */
constexpr float kImmediateAlarmDurationSeconds = 0.05f;

float box_iou(const std::array<float, 4>& a, const std::array<float, 4>& b) noexcept {
    const float x1 = std::max(a[0], b[0]);
    const float y1 = std::max(a[1], b[1]);
    const float x2 = std::min(a[2], b[2]);
    const float y2 = std::min(a[3], b[3]);
    const float inter_w = std::max(0.0f, x2 - x1);
    const float inter_h = std::max(0.0f, y2 - y1);
    const float inter = inter_w * inter_h;
    const float area_a = std::max(0.0f, a[2] - a[0]) * std::max(0.0f, a[3] - a[1]);
    const float area_b = std::max(0.0f, b[2] - b[0]) * std::max(0.0f, b[3] - b[1]);
    const float u = area_a + area_b - inter;
    return (u <= 0.0f) ? 0.0f : (inter / u);
}

float centroid_dist(float x1, float y1, float x2, float y2) noexcept {
    const float dx = x1 - x2;
    const float dy = y1 - y2;
    return std::sqrt(dx * dx + dy * dy);
}

} // namespace

float TaskStreamTracker::match_score(const ClusterCandidate& candidate, const TrackedCluster& track) const noexcept {
    const float iou = box_iou(candidate.enclosing_box, track.box);
    const float dist = centroid_dist(candidate.centroid_x, candidate.centroid_y,
                                     track.centroid_x, track.centroid_y);
    if (iou < kTrackMatchIouThreshold && dist > kTrackMatchCentroidDistance) {
        return -1.0f;
    }
    // IoU 主导，质心邻近作为同分时的加权：两者都随目标靠近而单调上升
    return iou + std::max(0.0f, kTrackMatchCentroidDistance - dist);
}

std::vector<bool> TaskStreamTracker::match_existing_tracks(
    const std::vector<ClusterCandidate>& candidates,
    float duration_seconds,
    std::chrono::steady_clock::time_point now
) {
    std::vector<bool> cand_matched(candidates.size(), false);
    std::vector<bool> track_matched(tracks_.size(), false);

    for (size_t c = 0; c < candidates.size(); ++c) {
        size_t best_track = tracks_.size();
        float best_score = -1.0f;

        for (size_t t = 0; t < tracks_.size(); ++t) {
            if (track_matched[t]) {
                continue;
            }
            const float score = match_score(candidates[c], tracks_[t]);
            if (score > best_score) {
                best_score = score;
                best_track = t;
            }
        }

        if (best_score < 0.0f || best_track >= tracks_.size()) {
            continue;
        }

        track_matched[best_track] = true;
        cand_matched[c] = true;

        auto& track = tracks_[best_track];
        track.box = candidates[c].enclosing_box;
        track.centroid_x = candidates[c].centroid_x;
        track.centroid_y = candidates[c].centroid_y;
        track.person_count = candidates[c].person_count;
        track.score = candidates[c].score;
        track.last_seen = now;

        // 告警是一次性闩锁：跨过阈值后一直上报，由宿主的告警冷却去重
        const float elapsed = std::chrono::duration<float>(now - track.first_seen).count();
        if (elapsed >= duration_seconds) {
            track.alarmed = true;
        }
    }

    return cand_matched;
}

void TaskStreamTracker::append_new_tracks(
    const std::vector<ClusterCandidate>& candidates,
    const std::vector<bool>& cand_matched,
    float duration_seconds,
    std::chrono::steady_clock::time_point now
) {
    for (size_t c = 0; c < candidates.size(); ++c) {
        if (cand_matched[c]) {
            continue;
        }
        TrackedCluster track;
        track.box = candidates[c].enclosing_box;
        track.centroid_x = candidates[c].centroid_x;
        track.centroid_y = candidates[c].centroid_y;
        track.person_count = candidates[c].person_count;
        track.score = candidates[c].score;
        track.first_seen = now;
        track.last_seen = now;
        // 时长为 0 表示「单帧命中即告警」
        track.alarmed = (duration_seconds <= kImmediateAlarmDurationSeconds);

        tracks_.push_back(track);
    }
}

void TaskStreamTracker::evict_stale_tracks(
    float duration_seconds,
    std::chrono::steady_clock::time_point now
) {
    const float grace = grace_period_seconds(duration_seconds);
    tracks_.erase(
        std::remove_if(tracks_.begin(), tracks_.end(), [now, grace](const TrackedCluster& track) {
            const float inactive_sec = std::chrono::duration<float>(now - track.last_seen).count();
            return inactive_sec > grace;
        }),
        tracks_.end()
    );
}

std::vector<ConfirmedAlarm> TaskStreamTracker::collect_alarms(std::chrono::steady_clock::time_point now) const {
    std::vector<ConfirmedAlarm> alarms;
    alarms.reserve(tracks_.size());
    for (const auto& track : tracks_) {
        if (!track.alarmed) {
            continue;
        }
        ConfirmedAlarm alarm;
        alarm.box = track.box;
        alarm.score = track.score;
        alarm.person_count = track.person_count;
        alarm.dwell_seconds = std::chrono::duration<float>(now - track.first_seen).count();
        alarms.push_back(alarm);
    }
    return alarms;
}

std::vector<ConfirmedAlarm> TaskStreamTracker::update(
    const std::vector<ClusterCandidate>& candidates,
    float duration_seconds,
    std::chrono::steady_clock::time_point now
) {
    const std::vector<bool> cand_matched = match_existing_tracks(candidates, duration_seconds, now);
    append_new_tracks(candidates, cand_matched, duration_seconds, now);
    evict_stale_tracks(duration_seconds, now);
    return collect_alarms(now);
}

TemporalTracker::TaskEntry& TemporalTracker::entry_for(uint64_t task_id) {
    std::lock_guard<std::mutex> lock(entries_mutex_);
    auto& slot = entries_[task_id];
    if (!slot) {
        slot = std::make_unique<TaskEntry>();
    }
    return *slot;
}

std::vector<ConfirmedAlarm> TemporalTracker::process(
    uint64_t task_id,
    const std::vector<ClusterCandidate>& candidates,
    float duration_seconds
) {
    const auto now = std::chrono::steady_clock::now();
    TaskEntry& entry = entry_for(task_id);
    std::lock_guard<std::mutex> lock(entry.mutex);
    return entry.tracker.update(candidates, duration_seconds, now);
}

} // namespace zhulong::crowd
