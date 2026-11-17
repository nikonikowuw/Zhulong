/**
 * @file clip_recorder_test.cpp
 * @brief 事件录像片段：环形缓冲淘汰、关键帧对齐、合并/切分、写队列溢出截断、
 *        时间轴不连续、停机收尾，以及 H.264 / HEVC fMP4 写出后用 mov demuxer 读回。
 *
 * 码流夹具取自 tests/fixtures/rtp_frames.json（64x48 单帧 intra，libx264/libx265 生成）。
 */

#include "nodes/capture/rtsp_input.hpp"
#include "nodes/record/fmp4_writer.hpp"
#include "pipeline/clip_recorder.hpp"

#include <atomic>
#include <chrono>
#include <condition_variable>
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <iostream>
#include <memory>
#include <mutex>
#include <string>
#include <sys/stat.h>
#include <thread>
#include <unistd.h>
#include <vector>

extern "C" {
#include <libavformat/avformat.h>
}

namespace {

using zhulong::ClipEvent;
using zhulong::ClipEventQueue;
using zhulong::ClipRecorder;
using zhulong::ClipTriggerOutcome;
using zhulong::ClipTriggerParams;
using zhulong::RecorderSettings;

void Check(bool condition, const char *message) {
    if (!condition) {
        std::cerr << "FAILED: " << message << '\n';
        std::exit(1);
    }
}

// ---------------------------------------------------------------- 码流夹具
const std::vector<uint8_t> h264_sps = {0x67, 0x42, 0xc0, 0x0a, 0xdc, 0x47, 0xb0, 0x11, 0x00, 0x00, 0x03,
                                       0x00, 0x01, 0x00, 0x00, 0x03, 0x00, 0x32, 0x8f, 0x12, 0x27, 0x80};
const std::vector<uint8_t> h264_pps = {0x68, 0xce, 0x0f, 0xc8};
const std::vector<uint8_t> h264_idr = {0x65, 0x88, 0x84, 0x3a, 0x26, 0x28, 0x00, 0x09, 0x02, 0xc9,
                                       0xc9, 0xc9, 0xd7, 0x5d, 0x75, 0xd7, 0x5d, 0x75, 0xe0};
const std::vector<uint8_t> h265_vps = {0x40, 0x01, 0x0c, 0x01, 0xff, 0xff, 0x04, 0x08, 0x00, 0x00, 0x03, 0x00,
                                       0x9f, 0xa8, 0x00, 0x00, 0x03, 0x00, 0x00, 0x1e, 0xba, 0x02, 0x40};
const std::vector<uint8_t> h265_sps = {0x42, 0x01, 0x01, 0x04, 0x08, 0x00, 0x00, 0x03, 0x00, 0x9f, 0xa8, 0x00, 0x00,
                                       0x03, 0x00, 0x00, 0x1e, 0xa0, 0x20, 0x83, 0x16, 0x5b, 0xa4, 0xa4, 0xc2, 0xf0,
                                       0x16, 0x80, 0x80, 0x00, 0x00, 0x03, 0x00, 0x80, 0x00, 0x00, 0x0c, 0x84};
const std::vector<uint8_t> h265_pps = {0x44, 0x01, 0xc0, 0x71, 0x81, 0x12};
const std::vector<uint8_t> h265_idr = {0x28, 0x01, 0xad, 0xe0, 0xd1, 0x17, 0xff,
                                       0xd3, 0x91, 0x81, 0x87, 0xd8, 0xc9, 0xd8};

void AppendNal(std::vector<uint8_t> &out, const std::vector<uint8_t> &nal) {
    static const uint8_t start_code[4] = {0, 0, 0, 1};
    out.insert(out.end(), start_code, start_code + 4);
    out.insert(out.end(), nal.begin(), nal.end());
}

/** @brief 整形后的关键帧形态：参数集前置 + IDR */
std::vector<uint8_t> KeyFramePayload(int32_t codec) {
    std::vector<uint8_t> out;
    if (codec == Zhulong_CODEC_H264) {
        AppendNal(out, h264_sps);
        AppendNal(out, h264_pps);
        AppendNal(out, h264_idr);
    } else {
        AppendNal(out, h265_vps);
        AppendNal(out, h265_sps);
        AppendNal(out, h265_pps);
        AppendNal(out, h265_idr);
    }
    return out;
}

/** @brief 非关键帧：把 IDR 的 slice 载荷改成非 IDR NAL 类型（muxer 只 remux，不解码） */
std::vector<uint8_t> PredictivePayload(int32_t codec) {
    std::vector<uint8_t> nal = codec == Zhulong_CODEC_H264 ? h264_idr : h265_idr;
    if (codec == Zhulong_CODEC_H264) {
        nal[0] = 0x41; // nal_ref_idc=2, type=1 (non-IDR slice)
    } else {
        nal[0] = 0x02; // type=1 (TRAIL_R)
        nal[1] = 0x01;
    }
    std::vector<uint8_t> out;
    AppendNal(out, nal);
    return out;
}

/** @brief 以固定帧率合成整形后的包序列（pts_us 与 dts_us 相同，无 B 帧） */
class PacketFeeder {
public:
    PacketFeeder(int32_t codec, int64_t frame_us, int frames_per_gop, size_t filler_bytes = 0)
        : codec_(codec), frame_us_(frame_us), frames_per_gop_(frames_per_gop),
          key_(KeyFramePayload(codec)), predictive_(PredictivePayload(codec)) {
        if (filler_bytes > 0) {
            key_.resize(key_.size() + filler_bytes, 0x11);
            predictive_.resize(predictive_.size() + filler_bytes, 0x22);
        }
    }

    /** @brief 送入一帧；返回其 pts_us */
    int64_t Feed(ClipRecorder &recorder, uint32_t extra_flags = 0) {
        const bool key = (index_ % frames_per_gop_) == 0;
        if (key) ++gop_id_;
        Zhulong_packet_view packet{};
        packet.codec = codec_;
        packet.key_frame = key ? 1 : 0;
        packet.pts_us = pts_us_;
        packet.dts_us = pts_us_;
        packet.gop_id = gop_id_;
        packet.track_type = ZHULONG_TRACK_VIDEO;
        packet.pkt_flags = extra_flags;
        const auto &payload = key ? key_ : predictive_;
        packet.data = payload.data();
        packet.size = payload.size();
        recorder.on_packet(packet);
        const int64_t fed = pts_us_;
        pts_us_ += frame_us_;
        ++index_;
        return fed;
    }

    /** @brief 送到（不含）给定 pts */
    void FeedUntil(ClipRecorder &recorder, int64_t pts_us) {
        while (pts_us_ < pts_us) Feed(recorder);
    }

    /** @brief 模拟时间轴跳变：下一个 GOP 号跳一格并带 DISCONTINUITY（不一定是关键帧） */
    void JumpGop() { ++gop_id_; }

    int64_t next_pts_us() const { return pts_us_; }
    size_t key_size() const { return key_.size(); }
    size_t predictive_size() const { return predictive_.size(); }

private:
    int32_t codec_;
    int64_t frame_us_;
    int frames_per_gop_;
    std::vector<uint8_t> key_;
    std::vector<uint8_t> predictive_;
    int64_t pts_us_{0};
    int64_t index_{0};
    uint32_t gop_id_{0};
};

// ---------------------------------------------------------------- 桩写出器
struct FakeClip {
    std::string path;
    std::vector<int64_t> pts;
    std::vector<bool> keys;
    bool finished{false};
    bool aborted{false};
};

struct FakeWriterState {
    std::mutex mutex;
    std::condition_variable cv;
    std::vector<std::shared_ptr<FakeClip>> clips;
    bool gate_closed{false};
    std::atomic<int> blocked_writes{0};

    void Release() {
        {
            std::lock_guard<std::mutex> lock(mutex);
            gate_closed = false;
        }
        cv.notify_all();
    }
};

class FakeWriter final : public zhulong::ClipFileWriter {
public:
    FakeWriter(std::shared_ptr<FakeWriterState> state, std::shared_ptr<FakeClip> clip)
        : state_(std::move(state)), clip_(std::move(clip)) {}

    bool write(const zhulong::ClipSample &sample) noexcept override {
        std::unique_lock<std::mutex> lock(state_->mutex);
        if (state_->gate_closed) {
            ++state_->blocked_writes;
            state_->cv.wait(lock, [this] { return !state_->gate_closed; });
        }
        clip_->pts.push_back(sample.pts_us);
        clip_->keys.push_back(sample.key_frame);
        return true;
    }
    bool finish(int64_t *out_size_bytes) noexcept override {
        std::lock_guard<std::mutex> lock(state_->mutex);
        clip_->finished = true;
        if (out_size_bytes) *out_size_bytes = static_cast<int64_t>(clip_->pts.size());
        return true;
    }
    void abort() noexcept override {
        std::lock_guard<std::mutex> lock(state_->mutex);
        clip_->aborted = true;
    }

private:
    std::shared_ptr<FakeWriterState> state_;
    std::shared_ptr<FakeClip> clip_;
};

zhulong::ClipWriterFactory FakeFactory(const std::shared_ptr<FakeWriterState> &state) {
    return [state](const zhulong::ClipWriteRequest &request) -> std::unique_ptr<zhulong::ClipFileWriter> {
        auto clip = std::make_shared<FakeClip>();
        clip->path = request.path;
        {
            std::lock_guard<std::mutex> lock(state->mutex);
            state->clips.push_back(clip);
        }
        return std::make_unique<FakeWriter>(state, clip);
    };
}

RecorderSettings Settings(int64_t pre_ms, int64_t byte_cap = 0, int64_t queue_byte_cap = 0) {
    RecorderSettings settings{};
    settings.pre_ms = pre_ms;
    settings.byte_cap = byte_cap;
    settings.queue_byte_cap = queue_byte_cap;
    return settings;
}

ClipTriggerParams Trigger(int64_t event_pts_us, int64_t post_ms, const std::string &path, int64_t max_ms = 0) {
    ClipTriggerParams params{};
    params.event_pts_us = event_pts_us;
    params.post_ms = post_ms;
    params.max_ms = max_ms;
    params.path = path;
    return params;
}

/** @brief 轮询完成队列直到拿到 count 条事件或超时 */
std::vector<ClipEvent> WaitEvents(ClipEventQueue &queue, size_t count,
                                  std::chrono::milliseconds timeout = std::chrono::milliseconds(3000)) {
    std::vector<ClipEvent> events;
    const auto deadline = std::chrono::steady_clock::now() + timeout;
    while (events.size() < count && std::chrono::steady_clock::now() < deadline) {
        ClipEvent batch[8];
        const size_t got = queue.poll(batch, 8);
        events.insert(events.end(), batch, batch + got);
        if (got == 0) std::this_thread::sleep_for(std::chrono::milliseconds(5));
    }
    return events;
}

constexpr int64_t frame_us = 40000; // 25 fps
constexpr int gop_frames = 25;      // 1s GOP
constexpr int64_t second_us = 1000000;

// ---------------------------------------------------------------- 测试
void TestEvictionByTime() {
    auto events = std::make_shared<ClipEventQueue>();
    auto state = std::make_shared<FakeWriterState>();
    ClipRecorder recorder(1, Settings(2000), events, FakeFactory(state));
    PacketFeeder feeder(Zhulong_CODEC_H264, frame_us, gop_frames);
    feeder.FeedUntil(recorder, 10 * second_us + 5 * frame_us);

    const auto snapshot = recorder.snapshot();
    const int64_t newest = snapshot.newest_pts_us;
    Check(newest == 10 * second_us + 4 * frame_us, "newest pts should track the last packet");
    // 最少 GOP 覆盖 pre + 事件延迟余量（2s+2s）：最老 GOP 起点 <= newest - 4s，且下一个 GOP 起点 > newest - 4s
    Check(snapshot.oldest_pts_us <= newest - 2 * second_us, "buffer must cover pre_ms");
    Check(snapshot.oldest_pts_us == 6 * second_us, "buffer must keep exactly the GOP covering pre_ms + slack");
    Check(snapshot.gop_count == 5, "6s..10s GOPs remain");
    Check(snapshot.oldest_pts_us % second_us == 0, "buffer must start on a keyframe");
    recorder.shutdown();
}

void TestEvictionByBytes() {
    auto events = std::make_shared<ClipEventQueue>();
    auto state = std::make_shared<FakeWriterState>();
    // 每帧约 1 KiB、每 GOP 25 帧 ≈ 25 KiB；上限 60 KiB → 最多保留 2 个完整 GOP
    PacketFeeder feeder(Zhulong_CODEC_H264, frame_us, gop_frames, 1000);
    const int64_t cap = 60 * 1024;
    ClipRecorder recorder(2, Settings(15000, cap), events, FakeFactory(state));
    feeder.FeedUntil(recorder, 10 * second_us + frame_us);

    const auto snapshot = recorder.snapshot();
    Check(snapshot.buffered_bytes <= cap, "byte cap must be enforced");
    Check(snapshot.gop_count >= 1 && snapshot.gop_count <= 3, "eviction is by whole GOP");
    Check(snapshot.oldest_pts_us % second_us == 0, "byte eviction must keep a keyframe first");
    Check(snapshot.oldest_pts_us > 5 * second_us, "15s pre must be cut down by the byte cap");
    recorder.shutdown();
}

void TestKeyframeAlignedStartAndCompletion() {
    auto events = std::make_shared<ClipEventQueue>();
    auto state = std::make_shared<FakeWriterState>();
    ClipRecorder recorder(3, Settings(3000), events, FakeFactory(state));
    PacketFeeder feeder(Zhulong_CODEC_H264, frame_us, gop_frames);
    feeder.FeedUntil(recorder, 10 * second_us);

    const int64_t event_us = 8 * second_us + 500000;
    const ClipTriggerOutcome outcome = recorder.trigger(Trigger(event_us, 1000, "/tmp/clip-a.mp4"));
    Check(outcome.created, "first trigger creates a clip");
    Check(outcome.seq >= 1, "sequence starts at 1");
    Check(outcome.codec == Zhulong_CODEC_H264, "codec is reported");
    Check(outcome.start_pts_us == 5 * second_us, "start aligns to last keyframe <= event - pre");
    Check(outcome.offset_ms == 3500, "offset = event - clip start");

    feeder.FeedUntil(recorder, 11 * second_us);
    const auto got = WaitEvents(*events, 1);
    Check(got.size() == 1, "clip completion event is published");
    Check(got[0].seq == outcome.seq, "event carries the clip seq");
    Check(got[0].status == ZHULONG_CLIP_COMPLETED, "clip completed");
    Check(got[0].first_pts_us == 5 * second_us, "first sample is the start keyframe");
    Check(got[0].last_pts_us < event_us + second_us, "clip ends at the post boundary");
    Check(got[0].last_pts_us >= event_us + second_us - frame_us, "clip covers post_ms");
    Check(got[0].duration_ms >= 4500 && got[0].duration_ms <= 4500 + frame_us / 1000,
          "duration covers pre+post from the keyframe");

    std::lock_guard<std::mutex> lock(state->mutex);
    Check(state->clips.size() == 1, "one clip written");
    Check(state->clips[0]->path == "/tmp/clip-a.mp4", "path is passed through");
    Check(!state->clips[0]->keys.empty() && state->clips[0]->keys.front(), "first written sample is key");
    Check(state->clips[0]->finished, "writer finished");
    recorder.shutdown();
}

void TestMergeExtendWithIncreasingOffsets() {
    auto events = std::make_shared<ClipEventQueue>();
    auto state = std::make_shared<FakeWriterState>();
    ClipRecorder recorder(4, Settings(2000), events, FakeFactory(state));
    PacketFeeder feeder(Zhulong_CODEC_H264, frame_us, gop_frames);
    feeder.FeedUntil(recorder, 5 * second_us);

    const auto first = recorder.trigger(Trigger(4 * second_us, 2000, "/tmp/clip-m1.mp4"));
    feeder.FeedUntil(recorder, 6 * second_us);
    const auto second = recorder.trigger(Trigger(5 * second_us + 200000, 2000, "/tmp/clip-m2.mp4"));
    feeder.FeedUntil(recorder, 7 * second_us);
    // event_pts_us <= 0：退回最新包的 pts
    const auto third = recorder.trigger(Trigger(0, 2000, "/tmp/clip-m3.mp4"));

    Check(first.created && !second.created && !third.created, "events within the window merge");
    Check(first.seq == second.seq && second.seq == third.seq, "merged events share one clip");
    Check(first.offset_ms < second.offset_ms && second.offset_ms < third.offset_ms, "offsets increase");
    Check(third.offset_ms == (7 * second_us - frame_us - first.start_pts_us) / 1000,
          "latest-packet fallback uses the newest pts");

    feeder.FeedUntil(recorder, 10 * second_us);
    const auto got = WaitEvents(*events, 1);
    Check(got.size() == 1 && got[0].status == ZHULONG_CLIP_COMPLETED, "merged clip completes once");
    Check(got[0].last_pts_us >= 8 * second_us + 900000, "end was extended by the last event");
    std::lock_guard<std::mutex> lock(state->mutex);
    Check(state->clips.size() == 1, "only one file for merged events");
    Check(state->clips[0]->path == "/tmp/clip-m1.mp4", "extensions never use the new path");
    recorder.shutdown();
}

void TestMaxLengthSplit() {
    auto events = std::make_shared<ClipEventQueue>();
    auto state = std::make_shared<FakeWriterState>();
    ClipRecorder recorder(5, Settings(1000), events, FakeFactory(state));
    PacketFeeder feeder(Zhulong_CODEC_H264, frame_us, gop_frames);
    feeder.FeedUntil(recorder, 3 * second_us);

    const auto first = recorder.trigger(Trigger(2 * second_us + 500000, 3000, "/tmp/clip-s1.mp4", 5000));
    Check(first.created && first.start_pts_us == second_us, "first clip starts at 1s keyframe");
    feeder.FeedUntil(recorder, 5 * second_us);
    // 1s 起点 + 5s 上限：4.5s + 3s 后段 = 6.5s 跨度 > 5s → 切新片段
    const auto second = recorder.trigger(Trigger(4 * second_us + 500000, 3000, "/tmp/clip-s2.mp4", 5000));
    Check(second.created, "exceeding max_ms starts a new clip");
    Check(second.seq != first.seq, "split clip gets a new seq");
    Check(second.start_pts_us == 3 * second_us, "new clip starts from the buffered keyframe");

    feeder.FeedUntil(recorder, 8 * second_us);
    const auto got = WaitEvents(*events, 2);
    Check(got.size() == 2, "both clips finish");
    Check(got[0].seq == first.seq && got[0].status == ZHULONG_CLIP_COMPLETED, "old clip completes at split");
    Check(got[1].seq == second.seq && got[1].status == ZHULONG_CLIP_COMPLETED, "new clip completes");
    std::lock_guard<std::mutex> lock(state->mutex);
    Check(state->clips.size() == 2, "two files after split");
    recorder.shutdown();
}

void TestQueueOverflowTruncatesWithoutBlocking() {
    auto events = std::make_shared<ClipEventQueue>();
    auto state = std::make_shared<FakeWriterState>();
    state->gate_closed = true; // 写线程卡住（模拟慢盘）
    PacketFeeder feeder(Zhulong_CODEC_H264, frame_us, gop_frames, 1000);
    ClipRecorder recorder(6, Settings(1000, 0, 20 * 1024), events, FakeFactory(state));
    feeder.FeedUntil(recorder, 2 * second_us);

    const auto outcome = recorder.trigger(Trigger(0, 30000, "/tmp/clip-q.mp4"));
    Check(outcome.created, "clip created while writer is stuck");

    const auto started = std::chrono::steady_clock::now();
    feeder.FeedUntil(recorder, 6 * second_us);
    const auto elapsed = std::chrono::steady_clock::now() - started;
    Check(elapsed < std::chrono::milliseconds(500), "capture thread must never wait for the writer");
    const auto snapshot = recorder.snapshot();
    Check(!snapshot.active, "overflowed clip is no longer active");
    Check(snapshot.truncations == 1, "overflow is counted once");
    Check(snapshot.gop_count >= 1, "ring buffer keeps buffering after truncation");

    state->Release();
    const auto got = WaitEvents(*events, 1);
    Check(got.size() == 1 && got[0].seq == outcome.seq, "truncated clip still publishes an event");
    Check(got[0].status == ZHULONG_CLIP_TRUNCATED, "queue overflow marks the clip truncated");
    std::lock_guard<std::mutex> lock(state->mutex);
    Check(state->clips.size() == 1 && state->clips[0]->finished, "truncated file is still finalized");
    recorder.shutdown();
}

void TestGopDiscontinuityEndsClip() {
    auto events = std::make_shared<ClipEventQueue>();
    auto state = std::make_shared<FakeWriterState>();
    ClipRecorder recorder(7, Settings(2000), events, FakeFactory(state));
    PacketFeeder feeder(Zhulong_CODEC_H264, frame_us, gop_frames);
    feeder.FeedUntil(recorder, 3 * second_us + 10 * frame_us);
    const auto outcome = recorder.trigger(Trigger(0, 10000, "/tmp/clip-d.mp4"));
    Check(outcome.created, "clip created");

    // 非关键帧上 gop_id 跳变（重连 / 时间轴不连续）
    feeder.JumpGop();
    feeder.Feed(recorder, ZHULONG_PKT_FLAG_DISCONTINUITY);
    auto snapshot = recorder.snapshot();
    Check(!snapshot.active, "discontinuity ends the active clip");
    Check(snapshot.gop_count == 0, "discontinuity clears the buffer");

    bool not_ready = false;
    try {
        recorder.trigger(Trigger(0, 1000, "/tmp/clip-d2.mp4"));
    } catch (const zhulong::Failure &failure) {
        not_ready = failure.status == Zhulong_ERR_NOT_READY;
    }
    Check(not_ready, "empty buffer after discontinuity → NOT_READY");

    // 下一个关键帧前的包都不进缓冲
    feeder.FeedUntil(recorder, 4 * second_us + frame_us);
    snapshot = recorder.snapshot();
    Check(snapshot.gop_count == 1 && snapshot.oldest_pts_us == 4 * second_us, "buffer restarts on a keyframe");

    const auto got = WaitEvents(*events, 1);
    Check(got.size() == 1 && got[0].status == ZHULONG_CLIP_TRUNCATED, "discontinuity marks the clip truncated");
    recorder.shutdown();
}

void TestNotReadyAndShutdownFinalizes() {
    auto events = std::make_shared<ClipEventQueue>();
    auto state = std::make_shared<FakeWriterState>();
    ClipRecorder recorder(8, Settings(2000), events, FakeFactory(state));
    bool not_ready = false;
    try {
        recorder.trigger(Trigger(0, 1000, "/tmp/clip-n.mp4"));
    } catch (const zhulong::Failure &failure) {
        not_ready = failure.status == Zhulong_ERR_NOT_READY;
    }
    Check(not_ready, "empty buffer → NOT_READY");

    PacketFeeder feeder(Zhulong_CODEC_H265, frame_us, gop_frames);
    feeder.FeedUntil(recorder, 2 * second_us);
    const auto outcome = recorder.trigger(Trigger(0, 60000, "/tmp/clip-n.mp4"));
    Check(outcome.codec == Zhulong_CODEC_H265, "hevc codec is reported");
    recorder.shutdown();
    const auto got = WaitEvents(*events, 1);
    Check(got.size() == 1 && got[0].seq == outcome.seq, "shutdown flushes the active clip");
    Check(got[0].status == ZHULONG_CLIP_COMPLETED, "shutdown flush is a completed (partial) clip");

    // 关闭后不再接收包、不再触发
    feeder.Feed(recorder);
    not_ready = false;
    try {
        recorder.trigger(Trigger(0, 1000, "/tmp/clip-n2.mp4"));
    } catch (const zhulong::Failure &failure) {
        not_ready = failure.status == Zhulong_ERR_NOT_READY;
    }
    Check(not_ready, "trigger after shutdown → NOT_READY");
}

void TestShutdownBudgetAbortsStuckWriter() {
    auto events = std::make_shared<ClipEventQueue>();
    auto state = std::make_shared<FakeWriterState>();
    state->gate_closed = true;
    ClipRecorder recorder(9, Settings(2000), events, FakeFactory(state));
    PacketFeeder feeder(Zhulong_CODEC_H264, frame_us, gop_frames);
    feeder.FeedUntil(recorder, 2 * second_us);
    recorder.trigger(Trigger(0, 60000, "/tmp/clip-b.mp4"));

    std::thread releaser([state] {
        std::this_thread::sleep_for(std::chrono::milliseconds(300));
        state->Release();
    });
    const auto started = std::chrono::steady_clock::now();
    recorder.shutdown(std::chrono::milliseconds(100));
    const auto elapsed = std::chrono::steady_clock::now() - started;
    releaser.join();
    Check(elapsed < std::chrono::milliseconds(2000), "shutdown is bounded");
    const auto got = WaitEvents(*events, 1);
    Check(got.size() == 1 && got[0].status == ZHULONG_CLIP_TRUNCATED, "budget overrun marks the clip truncated");
}

void TestEventQueueDropsOldest() {
    ClipEventQueue queue(2);
    const uint64_t first = queue.next_sequence();
    Check(first == 1 && queue.next_sequence() == 2, "sequence is monotonic from 1");
    for (uint64_t seq = 1; seq <= 3; ++seq) {
        ClipEvent event{};
        event.seq = seq;
        queue.push(event);
    }
    ClipEvent out[4];
    Check(queue.poll(out, 4) == 2, "queue is bounded");
    Check(out[0].seq == 2 && out[1].seq == 3, "overflow drops the oldest event");
    Check(queue.dropped() == 1, "dropped events are counted");
}

// ---------------------------------------------------------------- fMP4 读回
std::string TempDir() {
    char pattern[] = "/tmp/zhulong-clip-test-XXXXXX";
    const char *dir = ::mkdtemp(pattern);
    Check(dir != nullptr, "mkdtemp");
    return dir;
}

bool FileExists(const std::string &path) {
    struct stat info {};
    return ::stat(path.c_str(), &info) == 0;
}

void VerifyReadBack(const std::string &path, int32_t codec, int expected_frames, int64_t expected_span_us) {
    AVFormatContext *context = nullptr;
    Check(avformat_open_input(&context, path.c_str(), nullptr, nullptr) == 0, "mov demuxer opens the clip");
    Check(context->nb_streams == 1, "clip has exactly one stream");
    const AVStream *stream = context->streams[0];
    const AVCodecParameters *par = stream->codecpar;
    Check(par->codec_id == (codec == Zhulong_CODEC_H264 ? AV_CODEC_ID_H264 : AV_CODEC_ID_HEVC), "codec id");
    Check(par->width == 64 && par->height == 48, "dimensions parsed from SPS");
    // avcC / hvcC 的 configurationVersion 恒为 1：证明 Annex B 参数集已被转成配置记录
    Check(par->extradata_size > 4 && par->extradata[0] == 1, "extradata is avcC/hvcC, not Annex B");
    if (codec == Zhulong_CODEC_H265) {
        Check(par->codec_tag == MKTAG('h', 'v', 'c', '1'), "hevc uses the hvc1 sample entry");
    }

    AVPacket *packet = av_packet_alloc();
    int frames = 0;
    int64_t first_pts = AV_NOPTS_VALUE;
    int64_t last_pts = AV_NOPTS_VALUE;
    bool first_key = false;
    bool length_prefixed = false;
    while (av_read_frame(context, packet) >= 0) {
        if (frames == 0) {
            first_key = (packet->flags & AV_PKT_FLAG_KEY) != 0;
            first_pts = packet->pts;
            // 样本必须是长度前缀（mov muxer 已把起始码改写掉）
            length_prefixed = packet->size > 4 &&
                              !(packet->data[0] == 0 && packet->data[1] == 0 &&
                                (packet->data[2] == 1 || (packet->data[2] == 0 && packet->data[3] == 1)));
        }
        last_pts = packet->pts;
        ++frames;
        av_packet_unref(packet);
    }
    av_packet_free(&packet);
    Check(first_key, "first sample is a keyframe");
    Check(length_prefixed, "samples are length-prefixed (Annex B converted)");
    Check(frames == expected_frames, "all samples are read back");
    const int64_t span_us = av_rescale_q(last_pts - first_pts, stream->time_base, AVRational{1, 1000000});
    Check(span_us == expected_span_us, "sample timestamps survive the remux");
    avformat_close_input(&context);
}

void TestFmp4ReadBack(int32_t codec) {
    const std::string dir = TempDir();
    const std::string path = dir + "/evt-" + (codec == Zhulong_CODEC_H264 ? "h264" : "hevc") + ".mp4";
    auto events = std::make_shared<ClipEventQueue>();
    {
        ClipRecorder recorder(10, Settings(2000), events);
        PacketFeeder feeder(codec, frame_us, gop_frames);
        feeder.FeedUntil(recorder, 3 * second_us);
        const auto outcome = recorder.trigger(Trigger(2 * second_us + 500000, 1500, path));
        Check(outcome.created && outcome.start_pts_us == 0, "clip starts at the first keyframe");
        feeder.FeedUntil(recorder, 5 * second_us);
        const auto got = WaitEvents(*events, 1);
        Check(got.size() == 1 && got[0].status == ZHULONG_CLIP_COMPLETED, "fMP4 clip completes");
        Check(got[0].size_bytes > 0, "size is reported");
        // 0 .. 4s（不含）共 100 帧
        Check(got[0].duration_ms == 4000, "duration_ms matches the sample span");
        recorder.shutdown();
    }
    Check(FileExists(path), "final file exists");
    Check(!FileExists(path + ".part"), ".part is renamed away");
    VerifyReadBack(path, codec, 100, 4 * second_us - frame_us);
    std::remove(path.c_str());
    ::rmdir(dir.c_str());
}

void TestFmp4FailureRemovesPart() {
    auto events = std::make_shared<ClipEventQueue>();
    const std::string path = "/tmp/zhulong-clip-missing-dir-xyz/evt-x.mp4";
    ClipRecorder recorder(11, Settings(1000), events);
    PacketFeeder feeder(Zhulong_CODEC_H264, frame_us, gop_frames);
    feeder.FeedUntil(recorder, 2 * second_us);
    recorder.trigger(Trigger(0, 100, path));
    feeder.FeedUntil(recorder, 3 * second_us);
    const auto got = WaitEvents(*events, 1);
    Check(got.size() == 1 && got[0].status == ZHULONG_CLIP_FAILED, "unwritable path → FAILED");
    Check(!FileExists(path) && !FileExists(path + ".part"), "failed clip leaves no file");
    recorder.shutdown();
}

void TestExtractParameterSets() {
    const auto key = KeyFramePayload(Zhulong_CODEC_H265);
    const auto sets = zhulong::extract_annex_b_parameter_sets(Zhulong_CODEC_H265, key.data(), key.size());
    Check(sets.size() == 4 * 3 + h265_vps.size() + h265_sps.size() + h265_pps.size(),
          "VPS/SPS/PPS are extracted, IDR is not");
    const auto predictive = PredictivePayload(Zhulong_CODEC_H264);
    Check(zhulong::extract_annex_b_parameter_sets(Zhulong_CODEC_H264, predictive.data(), predictive.size()).empty(),
          "slices are not parameter sets");
}

void TestBFrameOrderAndPartialParamSetFallback() {
    const std::string dir = TempDir();
    const std::string path = dir + "/evt-bframe.mp4";
    std::vector<uint8_t> hint;
    AppendNal(hint, h264_sps);
    AppendNal(hint, h264_pps);

    zhulong::Fmp4Writer writer(zhulong::Fmp4WriterConfig{
        path, Zhulong_CODEC_H264, 0, 0, hint, zhulong::default_bframe_pts_reorder_offset_us,
    });

    // 首包只带 PPS + IDR（缺 SPS），验证能回退到 hint 而不写出空 avcC
    std::vector<uint8_t> partial_key;
    AppendNal(partial_key, h264_pps);
    AppendNal(partial_key, h264_idr);
    const auto p_frame = PredictivePayload(Zhulong_CODEC_H264);

    // 模拟 B 帧解码序：I0(pts=0,dts=0) -> P3(pts=120ms,dts=40ms) -> B1(pts=40ms,dts=40.001ms) -> B2(pts=80ms,dts=40.002ms)
    Check(writer.write(0, 0, true, partial_key.data(), partial_key.size()), "write I0");
    Check(writer.write(120000, 40000, false, p_frame.data(), p_frame.size()), "write P3");
    Check(writer.write(40000, 40001, false, p_frame.data(), p_frame.size()), "write B1");
    Check(writer.write(80000, 40002, false, p_frame.data(), p_frame.size()), "write B2");
    Check(writer.finish(), "finish bframe clip");

    AVFormatContext *context = nullptr;
    Check(avformat_open_input(&context, path.c_str(), nullptr, nullptr) == 0, "open bframe clip");
    Check(context->streams[0]->codecpar->width == 64 && context->streams[0]->codecpar->height == 48,
          "dimensions probed via parameter_sets_hint");
    AVPacket *pkt = av_packet_alloc();
    std::vector<int64_t> pts_order;
    while (av_read_frame(context, pkt) >= 0) {
        pts_order.push_back(pkt->pts);
        av_packet_unref(pkt);
    }
    av_packet_free(&pkt);
    avformat_close_input(&context);
    Check(pts_order.size() == 4, "4 bframe samples read back");
    // 解码序中第 2 包是 P3，第 3/4 包是 B1/B2，因此 P3.pts > B2.pts > B1.pts > I0.pts
    Check(pts_order[0] < pts_order[2] && pts_order[2] < pts_order[3] && pts_order[3] < pts_order[1],
          "B-frame presentation order preserved");

    std::remove(path.c_str());
    ::rmdir(dir.c_str());
}

void TestLaggedBatchMergeAndZeroSampleReject() {
    auto events = std::make_shared<ClipEventQueue>();
    auto state = std::make_shared<FakeWriterState>();
    ClipRecorder recorder(12, Settings(2000), events, FakeFactory(state));
    PacketFeeder feeder(Zhulong_CODEC_H264, frame_us, gop_frames);
    feeder.FeedUntil(recorder, 5 * second_us);

    // 滞后事件：event=2s, post=1s -> end=3s <= newest(5s)，在缓冲内直接收尾
    const auto first = recorder.trigger(Trigger(2 * second_us, 1000, "/tmp/clip-lag1.mp4"));
    const auto second = recorder.trigger(Trigger(2 * second_us, 1000, "/tmp/clip-lag2.mp4"));
    Check(first.created && !second.created && first.seq == second.seq,
          "lagged same-frame batch merges even after backfill reached_end");

    // 完全滑出缓冲的过旧事件（end < gops_[0].start_pts_us）必须拒绝
    feeder.FeedUntil(recorder, 12 * second_us);
    bool rejected = false;
    try {
        recorder.trigger(Trigger(1 * second_us, 1000, "/tmp/clip-old.mp4"));
    } catch (const zhulong::Failure &failure) {
        rejected = failure.status == Zhulong_ERR_NOT_READY;
    }
    Check(rejected, "completely evicted event window is rejected with NOT_READY");
    recorder.shutdown();
}

} // namespace

int main() {
    TestEventQueueDropsOldest();
    TestExtractParameterSets();
    TestEvictionByTime();
    TestEvictionByBytes();
    TestKeyframeAlignedStartAndCompletion();
    TestMergeExtendWithIncreasingOffsets();
    TestMaxLengthSplit();
    TestQueueOverflowTruncatesWithoutBlocking();
    TestGopDiscontinuityEndsClip();
    TestNotReadyAndShutdownFinalizes();
    TestShutdownBudgetAbortsStuckWriter();
    TestFmp4ReadBack(Zhulong_CODEC_H264);
    TestFmp4ReadBack(Zhulong_CODEC_H265);
    TestFmp4FailureRemovesPart();
    TestBFrameOrderAndPartialParamSetFallback();
    TestLaggedBatchMergeAndZeroSampleReject();
    std::cout << "Clip recorder tests passed\n";
    return 0;
}
