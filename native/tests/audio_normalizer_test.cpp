/**
 * @file audio_normalizer_test.cpp
 * @brief 音频归一化器单元测试（锚点对齐、PTS 递推、视频跳变传播、单调性与回绕展开）
 */

#include "pipeline/audio_normalizer.hpp"

#include <cstdint>
#include <cstdlib>
#include <iostream>
#include <vector>

namespace {

void Check(bool condition, const char *message) {
    if (!condition) {
        std::cerr << "FAILED: " << message << '\n';
        std::exit(1);
    }
}

Zhulong_packet_view AudioPacket(int64_t pts, bool has_pts = true, size_t size = 160) {
    static const uint8_t payload[160] = {0xd5};
    Zhulong_packet_view packet{};
    packet.track_type = ZHULONG_TRACK_AUDIO;
    packet.codec = Zhulong_CODEC_PCMA;
    packet.pts = pts;
    packet.dts = pts;
    packet.has_pts = has_pts ? 1 : 0;
    packet.has_dts = has_pts ? 1 : 0;
    packet.time_base_num = 1;
    packet.time_base_den = 8000;
    packet.key_frame = 0;
    packet.data = payload;
    packet.size = size;
    return packet;
}

void TestDroppedBeforeOrigin() {
    zhulong::AudioNormalizer normalizer;
    normalizer.update_metadata(Zhulong_CODEC_PCMA, 8000, 1, 1, 8000);

    auto pkt = AudioPacket(100);
    auto out = normalizer.normalize(pkt, false, 0, 0, 0, 1000);
    Check(out.size == 0, "packet before video origin must be dropped");
    Check(normalizer.dropped_before_origin() == 1, "dropped counter must be incremented");
    Check(!normalizer.anchor_established(), "anchor must not be established before video origin");
}

void TestAnchorAndAlignment() {
    zhulong::AudioNormalizer normalizer;
    normalizer.update_metadata(Zhulong_CODEC_PCMA, 8000, 1, 1, 8000);

    const int64_t video_origin_arrival_us = 1000000; // 视频首包 1.0s 到达
    const int64_t audio_first_arrival_us = 1025000;  // 音频首包 1.025s 到达 (延迟 25ms)

    // 音频首包 PTS = 8000 ticks (在 1/8000 下对应 1,000,000 us)
    auto pkt1 = AudioPacket(8000);
    auto out1 = normalizer.normalize(pkt1, true, video_origin_arrival_us, 0, 0, audio_first_arrival_us);

    Check(out1.size == 160, "valid audio packet must not be dropped");
    Check(out1.track_type == ZHULONG_TRACK_AUDIO, "track_type must be AUDIO");
    Check(normalizer.anchor_established(), "anchor must be established");
    Check(normalizer.base_offset_us() == 25000, "base offset must match audio arrival delay (25ms)");
    // 首包输出微秒： (1000000 - 1000000) + 25000 + 0 = 25000
    Check(out1.pts_us == 25000, "first audio out pts_us must equal base offset");
    Check(out1.dts_us == 25000, "dts_us must equal pts_us");
    Check(out1.key_frame == 0, "audio key_frame must be 0");
    Check(out1.frame_class == Zhulong_FRAME_UNKNOWN, "audio frame_class must be UNKNOWN");

    // 第二包 PTS = 8160 ticks (20ms 之后，对应 1,020,000 us)
    auto pkt2 = AudioPacket(8160);
    auto out2 = normalizer.normalize(pkt2, true, video_origin_arrival_us, 0, 0, audio_first_arrival_us + 20000);
    Check(out2.pts_us == 45000, "second audio packet must step exactly 20ms");
}

void TestNegativeBasePreserved() {
    zhulong::AudioNormalizer normalizer;
    normalizer.update_metadata(Zhulong_CODEC_PCMA, 8000, 1, 1, 8000);

    const int64_t v_arr = 1000000;
    // 音频比视频原点更早到达 20ms：base 应为负（它本来就该输出在 0 之前），
    // 夹到 0 会变成最多半帧的系统性滞后。
    auto pkt1 = AudioPacket(0);
    auto out1 = normalizer.normalize(pkt1, true, v_arr, 0, 0, v_arr - 20000);
    Check(normalizer.base_offset_us() == -20000, "negative base must be preserved");
    Check(out1.pts_us == -20000, "audio output may precede video time zero");

    auto pkt2 = AudioPacket(160);
    auto out2 = normalizer.normalize(pkt2, true, v_arr, 0, 0, v_arr);
    Check(out2.pts_us == 0, "next packet lands on video time zero");
}

void TestVideoDiscontinuityPropagation() {
    zhulong::AudioNormalizer normalizer;
    normalizer.update_metadata(Zhulong_CODEC_PCMA, 8000, 1, 1, 8000);

    const int64_t v_arr = 1000000;
    auto pkt1 = AudioPacket(0);
    normalizer.normalize(pkt1, true, v_arr, 0, 0, v_arr);

    // 视频发生跳变：discontinuity generation 从 0 变到 1，跳变包与本音频包同时到达，
    // 跳变时刻的视频绝对输出位置（shift）为 500ms
    auto pkt2 = AudioPacket(160);
    auto out2 = normalizer.normalize(pkt2, true, v_arr + 20000, 500000, 1, v_arr + 20000);

    Check((out2.pkt_flags & ZHULONG_PKT_FLAG_DISCONTINUITY) != 0,
          "discontinuity flag must be set when video generation changes");
    // 重锚到新纪元：shift + (a - d) = 500ms + 0
    Check(out2.pts_us == 500000, "audio must re-anchor onto the new video epoch");

    // 重锚后正常前进 20ms
    auto out3 = normalizer.normalize(AudioPacket(320), true, v_arr + 20000, 500000, 1, v_arr + 40000);
    Check(out3.pts_us == 520000, "timeline must advance normally after video discontinuity");
}

/**
 * @brief 只有视频跳变、音频时钟连续时，不得把跳变前的时长重复叠加
 *
 * 视频的 shift 是跳变时刻的**绝对**输出位置（不是增量）。若音频沿用旧锚点，
 * (audio_abs - A) 已包含跳变前的 600s，再加 600s 的 shift 会输出约 1200s。
 */
void TestVideoOnlyDiscontinuityNoDoubleCount() {
    zhulong::AudioNormalizer normalizer;
    normalizer.update_metadata(Zhulong_CODEC_PCMA, 8000, 1, 1, 8000);

    const int64_t v_arr = 1000000;
    const int64_t elapsed_us = 600LL * 1000000; // 600s
    auto out1 = normalizer.normalize(AudioPacket(0), true, v_arr, 0, 0, v_arr);
    Check(out1.pts_us == 0, "anchor out is 0");

    // 600s 后视频 DTS 回退触发不连续：shift = 600s，跳变包比本音频包早 10ms 到达
    const int64_t jump_arrival = v_arr + elapsed_us - 10000;
    auto out2 = normalizer.normalize(AudioPacket(600LL * 8000), true, jump_arrival,
                                     elapsed_us, 1, v_arr + elapsed_us);
    Check(out2.pts_us == elapsed_us + 10000,
          "video-only discontinuity must not double count elapsed audio time");

    auto out3 = normalizer.normalize(AudioPacket(600LL * 8000 + 160), true, jump_arrival,
                                     elapsed_us, 1, v_arr + elapsed_us + 20000);
    Check(out3.pts_us == elapsed_us + 30000, "timeline must keep advancing 20ms per packet");
}

void TestSyntheticPts() {
    zhulong::AudioNormalizer normalizer;
    normalizer.update_metadata(Zhulong_CODEC_PCMA, 8000, 1, 1, 8000);

    const int64_t v_arr = 1000000;
    // 首包有 PTS = 0
    auto pkt1 = AudioPacket(0);
    auto out1 = normalizer.normalize(pkt1, true, v_arr, 0, 0, v_arr);
    Check((out1.pkt_flags & ZHULONG_PKT_FLAG_TIMESTAMP_SYNTHETIC) == 0, "pkt1 has real pts");

    // 第二包无 PTS，160 字节 PCMA 在 8000Hz 下应递增 20ms (20,000 us)
    auto pkt2 = AudioPacket(0, false, 160);
    auto out2 = normalizer.normalize(pkt2, true, v_arr, 0, 0, v_arr + 20000);
    Check((out2.pkt_flags & ZHULONG_PKT_FLAG_TIMESTAMP_SYNTHETIC) != 0,
          "synthetic flag must be set when pts is missing");
    Check(out2.pts_us == 20000, "synthetic pts must advance by frame duration");
}

void TestMonotonicityProtection() {
    zhulong::AudioNormalizer normalizer;
    normalizer.update_metadata(Zhulong_CODEC_PCMA, 8000, 1, 1, 8000);

    const int64_t v_arr = 1000000;
    auto pkt1 = AudioPacket(800); // 100ms
    auto out1 = normalizer.normalize(pkt1, true, v_arr, 0, 0, v_arr);
    Check(out1.pts_us == 0, "anchor out is 0");

    // 第二包时间戳回退 (例如 PTS 变为 400 = 50ms)
    auto pkt2 = AudioPacket(400);
    auto out2 = normalizer.normalize(pkt2, true, v_arr, 0, 0, v_arr + 20000);

    Check((out2.pkt_flags & ZHULONG_PKT_FLAG_DISCONTINUITY) != 0,
          "discontinuity flag must be set on backward timestamp jump");
    Check(out2.pts_us >= out1.pts_us, "pts_us must never move backward");
}

void TestTimestampWrapAround() {
    zhulong::AudioNormalizer normalizer;
    normalizer.update_metadata(Zhulong_CODEC_PCMA, 8000, 1, 1, 8000);

    const int64_t v_arr = 1000000;
    // 接近 32 位上限：2^32 - 160
    const int64_t near_wrap = (1LL << 32) - 160;
    auto pkt1 = AudioPacket(near_wrap);
    auto out1 = normalizer.normalize(pkt1, true, v_arr, 0, 0, v_arr);

    // 发生 32 位无符号回绕，PTS 回绕到 0
    auto pkt2 = AudioPacket(0);
    auto out2 = normalizer.normalize(pkt2, true, v_arr, 0, 0, v_arr + 20000);

    // 差值应正好是 160 ticks = 20,000 us
    Check(out2.pts_us - out1.pts_us == 20000,
          "pts_us must seamlessly unwrap across 32-bit boundary");
}

/** @brief 源流缺 DTS 时必须打 DTS_SYNTHETIC，与视频路径同构 */
void TestDtsSyntheticFlag() {
    zhulong::AudioNormalizer normalizer;
    normalizer.update_metadata(Zhulong_CODEC_PCMA, 8000, 1, 1, 8000);

    const int64_t v_arr = 1000000;
    auto out1 = normalizer.normalize(AudioPacket(0), true, v_arr, 0, 0, v_arr);
    Check((out1.pkt_flags & ZHULONG_PKT_FLAG_DTS_SYNTHETIC) == 0,
          "real dts must not be flagged synthetic");

    Zhulong_packet_view no_dts = AudioPacket(160);
    no_dts.has_dts = 0;
    no_dts.dts = 0;
    auto out2 = normalizer.normalize(no_dts, true, v_arr, 0, 0, v_arr + 20000);
    Check((out2.pkt_flags & ZHULONG_PKT_FLAG_DTS_SYNTHETIC) != 0,
          "missing dts must set ZHULONG_PKT_FLAG_DTS_SYNTHETIC");
    Check(out2.dts_us == out2.pts_us, "audio dts_us is derived from pts");
}

/**
 * @brief 首包缺 PTS 时锚点建在合成轴上，首枚真实 PTS 必须重锚而不是跳到绝对值
 *
 * 不重锚的后果：输出直接变成「真实 PTS + base」（用例里是 200s），
 * 而单调保护只拦回拨、拦不住前跳，音频整场不再响。
 */
void TestReanchorOnFirstRealPts() {
    zhulong::AudioNormalizer normalizer;
    normalizer.update_metadata(Zhulong_CODEC_PCMA, 8000, 1, 1, 8000);

    const int64_t v_arr = 1000000;
    auto out1 = normalizer.normalize(AudioPacket(0, false, 160), true, v_arr, 0, 0, v_arr);
    Check(out1.pts_us == 0, "synthetic anchor must start at zero");

    // 1600000 ticks @ 1/8000 = 200s
    auto out2 = normalizer.normalize(AudioPacket(1600000), true, v_arr, 0, 0, v_arr + 20000);
    Check(out2.pts_us == 20000, "re-anchor must rebase on the first real pts");
    Check((out2.pkt_flags & ZHULONG_PKT_FLAG_DISCONTINUITY) != 0,
          "re-anchor must set ZHULONG_PKT_FLAG_DISCONTINUITY");

    // 1600160 ticks = 200.02s，重锚后应只前进 20ms
    auto out3 = normalizer.normalize(AudioPacket(1600160), true, v_arr, 0, 0, v_arr + 40000);
    Check(out3.pts_us == 40000, "timeline must advance normally after re-anchor");
}

} // namespace

int main() {
    TestDroppedBeforeOrigin();
    TestAnchorAndAlignment();
    TestNegativeBasePreserved();
    TestVideoDiscontinuityPropagation();
    TestVideoOnlyDiscontinuityNoDoubleCount();
    TestSyntheticPts();
    TestMonotonicityProtection();
    TestTimestampWrapAround();
    TestDtsSyntheticFlag();
    TestReanchorOnFirstRealPts();
    std::cout << "AudioNormalizer tests passed.\n";
    return 0;
}
