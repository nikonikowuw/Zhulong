/**
 * @file temporal_tracker.cpp
 * @brief 时序状态机与驻留积分跟踪实现
 */

#include "temporal_tracker.hpp"

#include <algorithm>
#include <cmath>

namespace zhulong::crowd {

namespace {

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

std::vector<ConfirmedAlarm> TaskStreamTracker::update(
    const std::vector<ClusterCandidate>& candidates,
    float duration_seconds,
    std::chrono::steady_clock::time_point now
) {
    const size_t num_tracks = tracks_.size();
    const size_t num_cands = candidates.size();

    std::vector<bool> cand_matched(num_cands, false);
    std::vector<bool> track_matched(num_tracks, false);

    // 1. 空间与质心匹配（优先高 IoU，次选质心邻近）
    for (size_t c = 0; c < num_cands; ++c) {
        int best_t = -1;
        float best_score = -1.0f;

        for (size_t t = 0; t < num_tracks; ++t) {
            if (track_matched[t]) {
                continue;
            }
            const float iou = box_iou(candidates[c].enclosing_box, tracks_[t].box);
            const float dist = centroid_dist(candidates[c].centroid_x, candidates[c].centroid_y,
                                             tracks_[t].centroid_x, tracks_[t].centroid_y);

            // 判据：IoU ≥ 0.15 或 质心归一化距离 ≤ 0.15
            if (iou >= 0.15f || dist <= 0.15f) {
                const float match_metric = iou + std::max(0.0f, 0.15f - dist);
                if (match_metric > best_score) {
                    best_score = match_metric;
                    best_t = static_cast<int>(t);
                }
            }
        }

        if (best_t >= 0) {
            track_matched[best_t] = true;
            cand_matched[c] = true;

            auto& trk = tracks_[best_t];
            trk.box = candidates[c].enclosing_box;
            trk.centroid_x = candidates[c].centroid_x;
            trk.centroid_y = candidates[c].centroid_y;
            trk.person_count = candidates[c].person_count;
            trk.score = candidates[c].score;
            trk.last_seen = now;

            const float elapsed = std::chrono::duration<float>(now - trk.first_seen).count();
            if (elapsed >= duration_seconds) {
                trk.alarmed = true;
            }
        }
    }

    // 2. 未匹配候选簇入列新生 Track
    for (size_t c = 0; c < num_cands; ++c) {
        if (!cand_matched[c]) {
            TrackedCluster new_trk;
            new_trk.track_id = next_track_id_++;
            new_trk.box = candidates[c].enclosing_box;
            new_trk.centroid_x = candidates[c].centroid_x;
            new_trk.centroid_y = candidates[c].centroid_y;
            new_trk.person_count = candidates[c].person_count;
            new_trk.score = candidates[c].score;
            new_trk.first_seen = now;
            new_trk.last_seen = now;
            // 若持续时长配置为 0，单帧直接告警
            new_trk.alarmed = (duration_seconds <= 0.05f);

            tracks_.push_back(new_trk);
        }
    }

    // 3. 淘汰超时未出现的簇（超过宽限保持期）
    tracks_.erase(
        std::remove_if(tracks_.begin(), tracks_.end(), [&](const TrackedCluster& trk) {
            const float inactive_sec = std::chrono::duration<float>(now - trk.last_seen).count();
            return inactive_sec > kDefaultGracePeriodSeconds;
        }),
        tracks_.end()
    );

    // 4. 搜集并输出触发告警的簇
    std::vector<ConfirmedAlarm> alarms;
    for (const auto& trk : tracks_) {
        if (trk.alarmed) {
            ConfirmedAlarm alm;
            alm.box = trk.box;
            alm.score = trk.score;
            alm.person_count = trk.person_count;
            alm.dwell_seconds = std::chrono::duration<float>(now - trk.first_seen).count();
            alarms.push_back(alm);
        }
    }

    return alarms;
}

std::vector<ConfirmedAlarm> TemporalTracker::process(
    uint64_t task_id,
    const std::vector<ClusterCandidate>& candidates,
    float duration_seconds
) {
    const auto now = std::chrono::steady_clock::now();
    std::lock_guard<std::mutex> lock(mutex_);
    return stream_trackers_[task_id].update(candidates, duration_seconds, now);
}

void TemporalTracker::reset_task(uint64_t task_id) {
    std::lock_guard<std::mutex> lock(mutex_);
    stream_trackers_.erase(task_id);
}

} // namespace zhulong::crowd
