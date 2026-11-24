#include "analysis/violation_tracker.hpp"

#include "common/box_geometry.hpp"
#include "common/ppe_config.hpp"

#include <algorithm>
#include <cstddef>
#include <utility>

namespace zhulong::ppe {
namespace {

/**
 * @brief 把一条已确认的违规结论落成输出框
 *
 * 新建轨迹与命中轨迹两条路径必须给出完全一致的输出；各写一份时，任何一处漏改
 * 都会让「首帧即告警」与「累计后才告警」的语义悄悄分叉。
 */
zhulong::Detection make_confirmed(const HelmetDecision& decision) {
    zhulong::Detection detection;
    copy_box(detection.box, decision.box);
    detection.confidence = decision.score;
    detection.class_id = kClassHeadNoHelmet;
    detection.label = "no_helmet";
    return detection;
}

} // namespace

std::vector<zhulong::Detection> ViolationTracker::confirm(
    uint64_t task_id, const std::vector<HelmetDecision>& decisions, int confirm_frames) {
    const int required = std::clamp(confirm_frames, kMinConfirmFrames, kMaxConfirmFrames);
    std::lock_guard<std::mutex> lock(mutex_);
    TaskStream& stream = tasks_[task_id];

    // 逐帧复用：assign 只在容量不足时分配，避免每帧一条 vector
    std::vector<uint8_t>& used = stream.used_flags;
    used.assign(stream.tracks.size(), 0);

    std::vector<zhulong::Detection> confirmed;

    for (const HelmetDecision& decision : decisions) {
        int best_track = -1;
        float best_iou = kTrackIouThreshold;
        for (size_t i = 0; i < stream.tracks.size(); ++i) {
            if (used[i] != 0) {
                continue;
            }
            const float iou = box_iou(decision.box, stream.tracks[i].box);
            if (iou > best_iou) {
                best_iou = iou;
                best_track = static_cast<int>(i);
            }
        }

        size_t index = 0;
        if (best_track < 0) {
            Track track;
            copy_box(track.box, decision.box);
            track.streak = decision.violation ? 1 : 0;
            track.missed = 0;
            stream.tracks.push_back(track);
            used.push_back(1);
            index = stream.tracks.size() - 1;
        } else {
            // 认领后必须置位。漏掉这一行时，同一帧的第二个人会复用这条轨迹已经
            // 累计的 streak：confirm_frames 的防抖对重叠目标直接失效，首帧就告警。
            index = static_cast<size_t>(best_track);
            used[index] = 1;
            Track& track = stream.tracks[index];
            copy_box(track.box, decision.box);
            track.missed = 0;
            if (decision.violation) {
                if (track.streak < required) {
                    ++track.streak;
                }
            } else {
                track.streak = 0;
            }
        }

        if (decision.violation && stream.tracks[index].streak >= required) {
            confirmed.push_back(make_confirmed(decision));
        }
    }

    for (size_t i = 0; i < stream.tracks.size(); ++i) {
        if (used[i] == 0) {
            ++stream.tracks[i].missed;
        }
    }
    stream.tracks.erase(std::remove_if(stream.tracks.begin(), stream.tracks.end(),
                                       [](const Track& track) {
                                           return track.missed > kMaxMissedFrames;
                                       }),
                        stream.tracks.end());

    if (stream.tracks.size() > kMaxTracks) {
        std::sort(stream.tracks.begin(), stream.tracks.end(), [](const Track& a, const Track& b) {
            if (a.streak != b.streak) {
                return a.streak > b.streak;
            }
            return a.missed < b.missed;
        });
        stream.tracks.resize(kMaxTracks);
    }

    // 空闲任务不留在表里：否则同一 task_id 重启后旧 streak 仍在（首帧就可能告警），
    // 且任务表随 task_id 单调增长。`stream` 与 `used` 到此为止不再使用。
    if (stream.tracks.empty()) {
        tasks_.erase(task_id);
    }

    return confirmed;
}

size_t ViolationTracker::track_count(uint64_t task_id) const {
    std::lock_guard<std::mutex> lock(mutex_);
    const auto it = tasks_.find(task_id);
    return it == tasks_.end() ? 0 : it->second.tracks.size();
}

void ViolationTracker::reset(uint64_t task_id) {
    std::lock_guard<std::mutex> lock(mutex_);
    tasks_.erase(task_id);
}

} // namespace zhulong::ppe
