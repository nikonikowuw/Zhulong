#include "analysis/violation_tracker.hpp"

#include <algorithm>
#include <cstddef>
#include <utility>

namespace zhulong::ppe {
namespace {

float box_iou(const float* a, const float* b) noexcept {
    const float inter_w = std::min(a[2], b[2]) - std::max(a[0], b[0]);
    const float inter_h = std::min(a[3], b[3]) - std::max(a[1], b[1]);
    if (inter_w <= 0.0f || inter_h <= 0.0f) {
        return 0.0f;
    }
    const float intersection = inter_w * inter_h;
    const float area_a = (a[2] - a[0]) * (a[3] - a[1]);
    const float area_b = (b[2] - b[0]) * (b[3] - b[1]);
    const float uni = area_a + area_b - intersection;
    return uni > 0.0f ? intersection / uni : 0.0f;
}

void copy_box(float* destination, const float* source) noexcept {
    destination[0] = source[0];
    destination[1] = source[1];
    destination[2] = source[2];
    destination[3] = source[3];
}

} // namespace

std::vector<zhulong::Detection> ViolationTracker::confirm(
    uint64_t task_id, const std::vector<HelmetDecision>& decisions, int confirm_frames) {
    const int required = std::clamp(confirm_frames, 1, 10);
    std::lock_guard<std::mutex> lock(mutex_);
    TaskStream& stream = tasks_[task_id];

    std::vector<uint8_t> track_used(stream.tracks.size(), 0);
    std::vector<zhulong::Detection> confirmed;

    for (const HelmetDecision& decision : decisions) {
        int best_track = -1;
        float best_iou = kTrackIouThreshold;
        for (size_t i = 0; i < stream.tracks.size(); ++i) {
            if (track_used[i] != 0) {
                continue;
            }
            const float iou = box_iou(decision.box, stream.tracks[i].box);
            if (iou > best_iou) {
                best_iou = iou;
                best_track = static_cast<int>(i);
            }
        }

        if (best_track < 0) {
            Track track;
            track.id = stream.next_id++;
            copy_box(track.box, decision.box);
            track.streak = decision.violation ? 1 : 0;
            track.missed = 0;
            stream.tracks.push_back(track);
            track_used.push_back(1);
            if (decision.violation && track.streak >= required) {
                zhulong::Detection detection;
                copy_box(detection.box, decision.box);
                detection.confidence = std::min(decision.person_score, decision.head_score);
                detection.class_id = kClassHeadNoHelmet;
                detection.label = "no_helmet";
                confirmed.push_back(std::move(detection));
            }
            continue;
        }

        Track& track = stream.tracks[static_cast<size_t>(best_track)];
        copy_box(track.box, decision.box);
        track.missed = 0;
        if (decision.violation) {
            if (track.streak < required) {
                ++track.streak;
            }
        } else {
            track.streak = 0;
        }
        if (decision.violation && track.streak >= required) {
            zhulong::Detection detection;
            copy_box(detection.box, decision.box);
            detection.confidence = std::min(decision.person_score, decision.head_score);
            detection.class_id = kClassHeadNoHelmet;
            detection.label = "no_helmet";
            confirmed.push_back(std::move(detection));
        }
    }

    for (size_t i = 0; i < stream.tracks.size(); ++i) {
        if (track_used[i] == 0) {
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
