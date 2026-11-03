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

    // 视频发生跳变：discontinuity generation 从 0 变到 1，shift 增加了 500ms
    auto pkt2 = AudioPacket(160);
    auto out2 = normalizer.normalize(pkt2, true, v_arr, 500000, 1, v_arr + 20000);

    Check((out2.pkt_flags & ZHULONG_PKT_FLAG_DISCONTINUITY) != 0,
          "discontinuity flag must be set when video generation changes");
    // 20ms + 500ms = 520,000 us
    Check(out2.pts_us == 520000, "pts_us must include video shift");
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

} // namespace

int main() {
    TestDroppedBeforeOrigin();
    TestAnchorAndAlignment();
    TestNegativeBasePreserved();
    TestVideoDiscontinuityPropagation();
    TestSyntheticPts();
    TestMonotonicityProtection();
    TestTimestampWrapAround();
    std::cout << "AudioNormalizer tests passed.\n";
    return 0;
}
