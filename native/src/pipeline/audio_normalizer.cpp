/**
 * @file audio_normalizer.cpp
 * @brief 音频时间戳归一化器实现
 */

#include "pipeline/audio_normalizer.hpp"

#include <algorithm>

namespace zhulong {

void AudioNormalizer::reset() {
    codec_ = 0;
    sample_rate_ = 8000;
    channels_ = 1;
    time_base_num_ = 1;
    time_base_den_ = 8000;

    anchor_established_ = false;
    anchor_abs_us_ = 0;
    base_offset_us_ = 0;

    has_last_abs_ = false;
    last_abs_us_ = 0;
    wrap_offset_us_ = 0;

    last_video_gen_ = 0;
    has_last_out_ = false;
    last_out_us_ = 0;

    dropped_before_origin_ = 0;
}

void AudioNormalizer::update_metadata(int32_t codec, int32_t sample_rate, int32_t channels,
                                     int32_t time_base_num, int32_t time_base_den) {
    codec_ = codec;
    sample_rate_ = sample_rate > 0 ? sample_rate : 8000;
    channels_ = channels > 0 ? channels : 1;
    time_base_num_ = time_base_num > 0 ? time_base_num : 1;
    time_base_den_ = time_base_den > 0 ? time_base_den : 8000;
}

int64_t AudioNormalizer::rescale_to_microseconds(int64_t ticks, int32_t num, int32_t den) {
    if (den <= 0) return 0;
    const __int128_t scaled = static_cast<__int128_t>(ticks) * 1000000 * num;
    return static_cast<int64_t>(scaled / den);
}

Zhulong_packet_view AudioNormalizer::normalize(const Zhulong_packet_view &in,
                                              bool video_origin_ready,
                                              int64_t video_origin_arrival_us,
                                              int64_t video_shift_us,
                                              uint64_t video_discontinuity_gen,
                                              int64_t audio_arrival_us) {
    // 1. 若视频原点尚未建立，丢弃音频包并计数，杜绝音频先行构造悬空基准
    if (!video_origin_ready) {
        ++dropped_before_origin_;
        Zhulong_packet_view empty{};
        empty.size = 0;
        return empty;
    }

    uint32_t flags = 0;
    int64_t audio_abs_us = 0;

    // 2. 提取或合成音频媒体绝对时间戳
    if (in.has_pts) {
        audio_abs_us = rescale_to_microseconds(in.pts, time_base_num_, time_base_den_);

        // 32 位 RTP 滴答回绕检测与展开 (约 2^31 滴答突降)
        if (has_last_abs_) {
            const int64_t diff = audio_abs_us - (last_abs_us_ - wrap_offset_us_);
            const int64_t half_period_us = rescale_to_microseconds(1LL << 31, time_base_num_, time_base_den_);
            const int64_t full_period_us = rescale_to_microseconds(1LL << 32, time_base_num_, time_base_den_);
            if (half_period_us > 0 && diff < -half_period_us) {
                wrap_offset_us_ += full_period_us;
            }
        }
        audio_abs_us += wrap_offset_us_;
    } else {
        flags |= ZHULONG_PKT_FLAG_TIMESTAMP_SYNTHETIC;
        // 缺失 PTS 时按格式步长递推
        int64_t step_us = 0;
        if (codec_ == Zhulong_CODEC_AAC) {
            step_us = 1024LL * 1000000 / sample_rate_;
        } else {
            // PCMA / PCMU: 每采样 1 字节
            step_us = static_cast<int64_t>(in.size) * 1000000 / sample_rate_;
        }
        if (step_us <= 0) step_us = 20000; // 默认 20ms

        if (has_last_abs_) {
            audio_abs_us = last_abs_us_ + step_us;
        } else {
            audio_abs_us = 0;
        }
    }

    // 3. 首次进入时建立对齐锚点
    if (!anchor_established_) {
        anchor_established_ = true;
        anchor_abs_us_ = audio_abs_us;
        // base 允许为负：音频包可能比视频原点更早到达（交织顺序），此时它本来就该输出在 0 之前。
        // 夹到 0 会变成一个最多半帧的系统性滞后，不如保留真实值——老的包由前端按 `due` 丢弃。
        base_offset_us_ = audio_arrival_us - video_origin_arrival_us;
        last_video_gen_ = video_discontinuity_gen;
    }

    // 4. 检测视频时间轴跳变（discontinuity generation 递增）
    if (video_discontinuity_gen != last_video_gen_) {
        flags |= ZHULONG_PKT_FLAG_DISCONTINUITY;
        last_video_gen_ = video_discontinuity_gen;
    }

    // 5. 映射至视频输出时间轴
    int64_t audio_out_us = (audio_abs_us - anchor_abs_us_) + base_offset_us_ + video_shift_us;

    // 6. 单调性保护：绝不静默向后回拨
    if (has_last_out_) {
        if (audio_out_us < last_out_us_) {
            flags |= ZHULONG_PKT_FLAG_DISCONTINUITY;
            audio_out_us = last_out_us_;
        }
    }

    // 7. 更新历史记录
    has_last_abs_ = true;
    last_abs_us_ = audio_abs_us;
    has_last_out_ = true;
    last_out_us_ = audio_out_us;

    // 8. 组装输出包视图
    Zhulong_packet_view out = in;
    out.track_type = ZHULONG_TRACK_AUDIO;
    out.pts_us = audio_out_us;
    out.dts_us = audio_out_us;
    out.key_frame = 0;
    out.frame_class = Zhulong_FRAME_UNKNOWN;
    out.pkt_flags = flags;
    return out;
}

} // namespace zhulong
