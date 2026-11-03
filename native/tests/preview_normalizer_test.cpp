#include "pipeline/preview_normalizer.hpp"

#include <algorithm>
#include <cstdint>
#include <cstdlib>
#include <iostream>
#include <iterator>
#include <vector>

namespace {

void Check(bool condition, const char *message) {
    if (!condition) {
        std::cerr << "FAILED: " << message << '\n';
        std::exit(1);
    }
}

bool StartsWith(const std::vector<uint8_t> &data, const std::vector<uint8_t> &prefix) {
    return data.size() >= prefix.size() &&
           std::equal(prefix.begin(), prefix.end(), data.begin());
}

Zhulong_packet_view Packet(int64_t pts, int64_t dts, bool keyframe = false) {
    static const uint8_t payload[] = {0, 0, 0, 1, 0x41, 0x88};
    Zhulong_packet_view packet{};
    packet.codec = Zhulong_CODEC_H264;
    packet.pts = pts;
    packet.dts = dts;
    packet.has_pts = 1;
    packet.has_dts = 1;
    packet.time_base_num = 1;
    packet.time_base_den = 25;
    packet.key_frame = keyframe ? 1 : 0;
    packet.data = payload;
    packet.size = sizeof(payload);
    return packet;
}

void TestAvcConfigurationRecord() {
    const uint8_t avcc[] = {
        1, 0x64, 0, 0x1f, 0xff, 0xe1,
        0, 2, 0x67, 0x64,
        1, 0, 2, 0x68, 0xee,
    };
    const auto parameter_sets = zhulong::to_annex_b(
        Zhulong_CODEC_H264, avcc, sizeof(avcc));
    Check(StartsWith(parameter_sets, {0, 0, 0, 1}),
          "avcC parameters should be prefixed by an Annex B start code");
    Check(zhulong::has_complete_parameter_sets(
              Zhulong_CODEC_H264, parameter_sets.data(), parameter_sets.size()),
          "avcC SPS/PPS should be extracted into Annex B");
    // 兼容别名验证
    Check(zhulong::ToAnnexB(Zhulong_CODEC_H264, avcc, sizeof(avcc)) == parameter_sets,
          "ToAnnexB alias must match to_annex_b");
    Check(zhulong::HasCompleteParameterSets(
              Zhulong_CODEC_H264, parameter_sets.data(), parameter_sets.size()),
          "HasCompleteParameterSets alias must match has_complete_parameter_sets");

    zhulong::PreviewNormalizer normalizer;
    normalizer.update_metadata(Zhulong_CODEC_H264, avcc, sizeof(avcc), 1, 25);
    auto keyframe = Packet(0, 0, true);
    keyframe.data = keyframe.data + 4;
    keyframe.size -= 4;
    const auto output = normalizer.normalize(keyframe);
    Check((output.pkt_flags & ZHULONG_PKT_FLAG_PARAM_SETS_INJECTED) != 0,
          "avcC parameter sets should be injected into an IDR");
    Check(output.size > keyframe.size && output.data[0] == 0 && output.data[3] == 1,
          "injected avcC parameter sets should use Annex B start codes");
}

void TestHevcConfigurationRecord() {
    std::vector<uint8_t> hvcc(23, 0);
    hvcc[0] = 1;
    hvcc[22] = 3;
    const uint8_t arrays[][7] = {
        {0x80 | 32, 0, 1, 0, 2, 0x40, 0x01},
        {0x80 | 33, 0, 1, 0, 2, 0x42, 0x01},
        {0x80 | 34, 0, 1, 0, 2, 0x44, 0x01},
    };
    for (const auto &array : arrays) {
        hvcc.insert(hvcc.end(), std::begin(array), std::end(array));
    }

    const auto parameter_sets = zhulong::to_annex_b(
        Zhulong_CODEC_H265, hvcc.data(), hvcc.size());
    Check(StartsWith(parameter_sets, {0, 0, 0, 1}),
          "hvcC parameters should be prefixed by an Annex B start code");
    Check(zhulong::has_complete_parameter_sets(
              Zhulong_CODEC_H265, parameter_sets.data(), parameter_sets.size()),
          "hvcC VPS/SPS/PPS should be extracted into Annex B");
}

void TestBFrameTimestampsAndDiscontinuity() {
    zhulong::PreviewNormalizer normalizer;
    normalizer.update_metadata(Zhulong_CODEC_H264, nullptr, 0, 1, 25);

    const int64_t pts[] = {0, 3, 1, 2};
    const int64_t dts[] = {0, 1, 2, 3};
    const int64_t expected_pts[] = {0, 120000, 40000, 80000};
    for (size_t i = 0; i < 4; ++i) {
        auto packet = Packet(pts[i], dts[i]);
        const auto output = normalizer.normalize(packet);
        Check(output.pts_us == expected_pts[i], "presentation timestamp must preserve B-frame order");
        Check(output.dts_us == static_cast<int64_t>(i) * 40000,
              "decode timestamp must stay monotonic in packet order");
        Check((output.pkt_flags & ZHULONG_PKT_FLAG_DISCONTINUITY) == 0,
              "normal B-frame PTS reordering must not signal discontinuity");
    }

    auto restarted = Packet(0, 0);
    const auto output = normalizer.normalize(restarted);
    Check((output.pkt_flags & ZHULONG_PKT_FLAG_DISCONTINUITY) != 0,
          "DTS reset should signal a source discontinuity");
    Check(output.dts_us == 120001,
          "normalized DTS after discontinuity should continue after prior decode time");
}

void TestDtsAvailabilityTransitionDoesNotSignalDiscontinuity() {
    zhulong::PreviewNormalizer normalizer;
    normalizer.update_metadata(Zhulong_CODEC_H264, nullptr, 0, 1, 25);

    auto first = Packet(3, 3, true);
    first.has_dts = 0;
    const auto first_output = normalizer.normalize(first);
    Check((first_output.pkt_flags & ZHULONG_PKT_FLAG_DTS_SYNTHETIC) != 0,
          "missing first DTS should use a synthetic decode clock");

    const auto second_output = normalizer.normalize(Packet(1, 1));
    Check((second_output.pkt_flags & ZHULONG_PKT_FLAG_DISCONTINUITY) == 0,
          "switching from PTS fallback to real DTS must not signal discontinuity");
    Check(second_output.gop_id == first_output.gop_id,
          "a timestamp source transition must preserve the current GOP");
    Check(second_output.dts_us > first_output.dts_us,
          "normalized DTS must remain monotonic across a timestamp source transition");

    const auto third_output = normalizer.normalize(Packet(2, 2));
    Check((third_output.pkt_flags & ZHULONG_PKT_FLAG_DISCONTINUITY) == 0,
          "monotonic DTS after the source transition must remain continuous");

    const auto restarted_output = normalizer.normalize(Packet(0, 0));
    Check((restarted_output.pkt_flags & ZHULONG_PKT_FLAG_DISCONTINUITY) != 0,
          "a DTS reset after comparable DTS packets must still be detected");

    zhulong::PreviewNormalizer mixed_clock_restart;
    mixed_clock_restart.update_metadata(Zhulong_CODEC_H264, nullptr, 0, 1, 25);
    auto pts_fallback = Packet(1000, 1000, true);
    pts_fallback.has_dts = 0;
    mixed_clock_restart.normalize(pts_fallback);
    const auto large_restart = mixed_clock_restart.normalize(Packet(0, 0));
    Check((large_restart.pkt_flags & ZHULONG_PKT_FLAG_DISCONTINUITY) != 0,
          "a large reset must still be detected when DTS availability changes");
}

void TestMissingDtsGetsSyntheticDecodeClock() {
    zhulong::PreviewNormalizer normalizer;
    normalizer.update_metadata(Zhulong_CODEC_H264, nullptr, 0, 1, 25);
    auto first = Packet(0, 0);
    first.has_dts = 0;
    auto second = Packet(0, 0);
    second.has_dts = 0;

    const auto first_output = normalizer.normalize(first);
    const auto second_output = normalizer.normalize(second);
    Check((first_output.pkt_flags & ZHULONG_PKT_FLAG_DTS_SYNTHETIC) != 0,
          "missing DTS must be marked synthetic");
    Check(second_output.dts_us > first_output.dts_us,
          "synthetic DTS must increase in packet order");
}

void TestClockStateAndDiscontinuityGeneration() {
    zhulong::PreviewNormalizer normalizer;
    Check(!normalizer.origin_ready(), "origin must not be ready initially");
    Check(normalizer.discontinuity_generation() == 0, "discontinuity generation must be 0 initially");

    normalizer.update_metadata(Zhulong_CODEC_H264, nullptr, 0, 1, 25);
    normalizer.normalize(Packet(0, 0, true));
    Check(normalizer.origin_ready(), "origin must be ready after first normalized packet");
    Check(normalizer.discontinuity_generation() == 0, "no discontinuity yet");

    // 触发跳变（PTS 回退）
    normalizer.normalize(Packet(100, 100));
    normalizer.normalize(Packet(0, 0));
    Check(normalizer.discontinuity_generation() == 1, "generation must increment after discontinuity");
}

} // namespace

int main() {
    TestAvcConfigurationRecord();
    TestHevcConfigurationRecord();
    TestBFrameTimestampsAndDiscontinuity();
    TestDtsAvailabilityTransitionDoesNotSignalDiscontinuity();
    TestMissingDtsGetsSyntheticDecodeClock();
    TestClockStateAndDiscontinuityGeneration();
    std::cout << "Preview normalizer tests passed\n";
    return 0;
}
