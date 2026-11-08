/**
 * @file preview_normalizer.cpp
 * @brief 预览契约整形层实现
 *
 * 设计取舍：
 * 1. 热路径零分配：只有关键帧才可能触发参数集拼接，且缓冲在成员变量中复用；
 *    非关键帧走纯算术路径。
 * 2. 热路径零加锁：本层状态只被单一拉流 Worker 线程读写，加锁只会把
 *    「本可以忽略的竞争」变成「每帧一次 futex」。
 * 3. 保守优先：任何无法确定的判定（参数集是否齐全、是否非参考帧）都退回到
 *    「当作需要注入 / 当作参考帧」，宁可多传几 KB 也不让画面黑掉。
 */

#include "pipeline/preview_normalizer.hpp"

#include <algorithm>
#include <array>
#include <cstring>
#include <limits>
#include <utility>

namespace zhulong {

namespace {

/** 每秒微秒数 */
constexpr int64_t microseconds_per_second = 1000000;

/** 前向时间轴跳变容忍上限：超过即视为不连续（相机重启 / RTSP 会话重建） */
constexpr int64_t max_forward_jump_us = 5LL * microseconds_per_second;

constexpr std::array<uint8_t, 4> annex_b_start_code{0, 0, 0, 1};

// H.264 (AVC) NAL 单元类型
constexpr int32_t h264_nal_non_idr_slice = 1;
constexpr int32_t h264_nal_idr_slice = 5;
constexpr uint8_t h264_nal_sps = 7;
constexpr uint8_t h264_nal_pps = 8;

// H.265 (HEVC) NAL 单元类型
constexpr int32_t hevc_nal_radl_r = 9;
constexpr int32_t hevc_nal_cra_nut = 21;
constexpr uint8_t hevc_nal_vps = 32;
constexpr uint8_t hevc_nal_sps = 33;
constexpr uint8_t hevc_nal_pps = 34;

#if defined(__SIZEOF_INT128__)
// __int128 是 GCC/Clang 扩展而非 ISO C++ 类型，-Wpedantic 会为此逐行告警。
// __extension__ 在这里显式声明「扩展是有意为之」，让 -Wpedantic 继续对其它
// 真正可疑的写法发声，而不是被这一处噪声淹没。
__extension__ using wide_time_delta = __int128;
#endif

constexpr int64_t saturating_add(int64_t left, int64_t right) noexcept {
    if (right > 0 && left > std::numeric_limits<int64_t>::max() - right) {
        return std::numeric_limits<int64_t>::max();
    }
    if (right < 0 && left < std::numeric_limits<int64_t>::min() - right) {
        return std::numeric_limits<int64_t>::min();
    }
    return left + right;
}

constexpr int64_t next_timestamp(int64_t value) noexcept {
    return value == std::numeric_limits<int64_t>::max() ? value : value + 1;
}

constexpr bool is_forward_jump(int64_t current, int64_t previous) noexcept {
    return previous <= std::numeric_limits<int64_t>::max() - max_forward_jump_us &&
           current > previous + max_forward_jump_us;
}

constexpr bool is_backward_jump(int64_t current, int64_t previous) noexcept {
    return previous >= std::numeric_limits<int64_t>::min() + max_forward_jump_us &&
           current < previous - max_forward_jump_us;
}

void append_nal(const uint8_t *nal, size_t length, std::vector<uint8_t> &out) {
    out.insert(out.end(), annex_b_start_code.begin(), annex_b_start_code.end());
    out.insert(out.end(), nal, nal + length);
}

constexpr uint16_t read_u16(const uint8_t *data) noexcept {
    return static_cast<uint16_t>((static_cast<uint16_t>(data[0]) << 8) | data[1]);
}

bool append_length_prefixed_nals(const uint8_t *data, size_t size, size_t &offset,
                                uint32_t count, size_t length_size,
                                std::vector<uint8_t> &out) {
    for (uint32_t index = 0; index < count; ++index) {
        if (offset > size || length_size > size - offset) return false;
        uint32_t nal_length = 0;
        for (size_t i = 0; i < length_size; ++i) {
            nal_length = (nal_length << 8) | data[offset++];
        }
        if (nal_length == 0 || nal_length > size - offset) return false;
        append_nal(data + offset, nal_length, out);
        offset += nal_length;
    }
    return true;
}

bool parse_avc_configuration(const uint8_t *data, size_t size,
                             std::vector<uint8_t> &out) {
    if (size < 7 || data[0] != 1 || (data[4] & 0xfc) != 0xfc ||
        (data[5] & 0xe0) != 0xe0) {
        return false;
    }

    size_t offset = 6;
    const uint32_t sps_count = data[5] & 0x1f;
    if (!append_length_prefixed_nals(data, size, offset, sps_count, 2, out) ||
        offset >= size) {
        return false;
    }
    const uint32_t pps_count = data[offset++];
    if (!append_length_prefixed_nals(data, size, offset, pps_count, 2, out)) {
        return false;
    }
    return !out.empty();
}

bool parse_hevc_configuration(const uint8_t *data, size_t size,
                              std::vector<uint8_t> &out) {
    if (size < 23 || data[0] != 1) return false;

    size_t offset = 23;
    const uint32_t array_count = data[22];
    for (uint32_t array = 0; array < array_count; ++array) {
        if (offset > size || size - offset < 3) return false;
        const uint8_t nal_type = data[offset++] & 0x3f;
        const uint32_t nal_count = read_u16(data + offset);
        offset += 2;
        for (uint32_t index = 0; index < nal_count; ++index) {
            if (offset > size || size - offset < 2) return false;
            const uint32_t nal_length = read_u16(data + offset);
            offset += 2;
            if (nal_length == 0 || nal_length > size - offset) return false;
            if (nal_type >= hevc_nal_vps && nal_type <= hevc_nal_pps) {
                append_nal(data + offset, nal_length, out);
            }
            offset += nal_length;
        }
    }
    return offset == size && !out.empty();
}

bool append_four_byte_length_prefixed_nals(const uint8_t *data, size_t size,
                                        std::vector<uint8_t> &out) {
    size_t offset = 0;
    while (offset < size) {
        if (size - offset < 4) return false;
        const uint32_t nal_length =
            (static_cast<uint32_t>(data[offset]) << 24) |
            (static_cast<uint32_t>(data[offset + 1]) << 16) |
            (static_cast<uint32_t>(data[offset + 2]) << 8) |
            static_cast<uint32_t>(data[offset + 3]);
        offset += 4;
        if (nal_length == 0 || nal_length > size - offset) return false;
        append_nal(data + offset, nal_length, out);
        offset += nal_length;
    }
    return !out.empty();
}

/**
 * @brief 遍历 Annex B 载荷中的每个 NAL 单元
 *
 * 依赖 H.26x 的防竞争字节机制（0x000003 转义）保证载荷内部不会出现裸起始码，
 * 因此直接线性扫描起始码是安全的，无需解析 RBSP。
 *
 * @param visit 回调接收 (NAL 起始偏移, NAL 长度)；返回 false 可提前终止遍历
 */
template <typename Visitor>
void for_each_nal(const uint8_t *data, size_t size, Visitor &&visit) {
    size_t i = 0;
    while (i + 3 <= size) {
        size_t start_code_len = 0;
        if (data[i] == 0 && data[i + 1] == 0) {
            if (data[i + 2] == 1) {
                start_code_len = 3;
            } else if (i + 4 <= size && data[i + 2] == 0 && data[i + 3] == 1) {
                start_code_len = 4;
            }
        }
        if (start_code_len == 0) {
            ++i;
            continue;
        }

        const size_t nal_start = i + start_code_len;
        if (nal_start >= size) return;

        size_t next = size;
        for (size_t j = nal_start; j + 3 <= size; ++j) {
            if (data[j] == 0 && data[j + 1] == 0 &&
                (data[j + 2] == 1 ||
                 (j + 4 <= size && data[j + 2] == 0 && data[j + 3] == 1))) {
                next = j;
                break;
            }
        }

        if (!visit(nal_start, next - nal_start)) return;
        i = next;
    }
}

} // namespace

std::vector<uint8_t> to_annex_b(int32_t codec, const uint8_t *extradata, size_t size) {
    if (extradata == nullptr || size == 0) return {};

    // Annex B already has the representation expected by the preview path.
    if (size >= 3 && extradata[0] == 0 && extradata[1] == 0 &&
        (extradata[2] == 1 || (size >= 4 && extradata[2] == 0 && extradata[3] == 1))) {
        return std::vector<uint8_t>(extradata, extradata + size);
    }

    std::vector<uint8_t> out;
    if (codec == Zhulong_CODEC_H264 && extradata[0] == 1) {
        return parse_avc_configuration(extradata, size, out) ? std::move(out)
                                                             : std::vector<uint8_t>{};
    }
    if (codec == Zhulong_CODEC_H265 && extradata[0] == 1) {
        return parse_hevc_configuration(extradata, size, out) ? std::move(out)
                                                              : std::vector<uint8_t>{};
    }

    // Some demuxers provide a bare 4-byte length-prefixed NAL sequence instead
    // of a codec configuration record.
    return append_four_byte_length_prefixed_nals(extradata, size, out) ? std::move(out)
                                                                   : std::vector<uint8_t>{};
}

bool has_complete_parameter_sets(int32_t codec, const uint8_t *data, size_t size) noexcept {
    if (data == nullptr || size == 0) return false;

    bool vps = false;
    bool sps = false;
    bool pps = false;

    for_each_nal(data, size, [&](size_t offset, size_t length) {
        if (length < 1) return true;
        const uint8_t header = data[offset];
        if (codec == Zhulong_CODEC_H265) {
            switch ((header >> 1) & 0x3f) {
                case hevc_nal_vps: vps = true; break;
                case hevc_nal_sps: sps = true; break;
                case hevc_nal_pps: pps = true; break;
                default: break;
            }
        } else {
            switch (header & 0x1f) {
                case h264_nal_sps: sps = true; break;
                case h264_nal_pps: pps = true; break;
                default: break;
            }
        }
        return !(sps && pps && (codec != Zhulong_CODEC_H265 || vps));
    });

    if (codec == Zhulong_CODEC_H265) return vps && sps && pps;
    return sps && pps;
}

bool is_non_reference_frame(int32_t codec, const uint8_t *data, size_t size) noexcept {
    if (data == nullptr || size == 0) return false;

    bool saw_slice = false;
    bool saw_reference = false;

    for_each_nal(data, size, [&](size_t offset, size_t length) {
        if (length < 1) return true;
        const uint8_t header = data[offset];
        if (codec == Zhulong_CODEC_H265) {
            const int32_t nal_type = (header >> 1) & 0x3f;
            if (nal_type > hevc_nal_cra_nut) return true;   // 参数集 / SEI / 分片头，不是 slice
            saw_slice = true;
            // TRAIL_N(0) TSA_N(2) STSA_N(4) RADL_N(6) RASL_N(8) 为非参考；
            // 1/3/5/7/9 及 BLA/CRA/IDR 全部计入参考。
            const bool non_reference = nal_type <= hevc_nal_radl_r && (nal_type % 2 == 0);
            if (!non_reference) saw_reference = true;
        } else {
            const int32_t nal_type = header & 0x1f;
            if (nal_type != h264_nal_non_idr_slice && nal_type != h264_nal_idr_slice) return true;   // 非 slice NAL
            saw_slice = true;
            if (((header >> 5) & 0x03) != 0) saw_reference = true;   // nal_ref_idc != 0
        }
        return !saw_reference;
    });

    return saw_slice && !saw_reference;
}

void PreviewNormalizer::update_metadata(int32_t codec, const uint8_t *extradata,
                                        size_t extradata_size, int32_t time_base_num,
                                        int32_t time_base_den) {
    codec_ = codec;
    time_base_num_ = time_base_num > 0 ? time_base_num : default_time_base_num;
    time_base_den_ = time_base_den > 0 ? time_base_den : default_time_base_den;
    parameter_sets_ = to_annex_b(codec, extradata, extradata_size);
}

int64_t PreviewNormalizer::rescale_delta_to_microseconds(int64_t ticks, int64_t origin,
                                                           int32_t num, int32_t den) noexcept {
    if (den <= 0) den = default_time_base_den;
    if (num <= 0) num = default_time_base_num;
#if defined(__SIZEOF_INT128__)
    const wide_time_delta delta = static_cast<wide_time_delta>(ticks) - origin;
    const wide_time_delta scaled = delta * num * microseconds_per_second / den;
    if (scaled > std::numeric_limits<int64_t>::max()) {
        return std::numeric_limits<int64_t>::max();
    }
    if (scaled < std::numeric_limits<int64_t>::min()) {
        return std::numeric_limits<int64_t>::min();
    }
    return static_cast<int64_t>(scaled);
#else
    const long double delta = static_cast<long double>(ticks) - origin;
    const long double scaled =
        delta * num * static_cast<long double>(microseconds_per_second) / den;
    if (scaled > std::numeric_limits<int64_t>::max()) {
        return std::numeric_limits<int64_t>::max();
    }
    if (scaled < std::numeric_limits<int64_t>::min()) {
        return std::numeric_limits<int64_t>::min();
    }
    return static_cast<int64_t>(scaled);
#endif
}

Zhulong_packet_view PreviewNormalizer::normalize(const Zhulong_packet_view &in) {
    Zhulong_packet_view out = in;
    out.pts_us = 0;
    out.dts_us = 0;
    out.gop_id = gop_id_;
    out.pkt_flags = 0;
    out.frame_class = Zhulong_FRAME_UNKNOWN;

    const int32_t tb_num = in.time_base_num > 0 ? in.time_base_num : time_base_num_;
    const int32_t tb_den = in.time_base_den > 0 ? in.time_base_den : time_base_den_;

    // ---------------------------------------------------------------- 1) 时间轴
    const bool has_pts = in.has_pts != 0;
    const bool has_dts = in.has_dts != 0;
    const bool has_source_timestamp = has_dts || has_pts;
    const int64_t decode_ticks = has_dts ? in.dts : (has_pts ? in.pts : 0);

    if (!origin_set_) {
        origin_ticks_ = has_pts && has_dts ? std::min(in.pts, in.dts) : decode_ticks;
        origin_set_ = true;
    }

    int64_t source_dts_us = has_source_timestamp
                                ? rescale_delta_to_microseconds(decode_ticks, origin_ticks_,
                                                                tb_num, tb_den)
                                : (has_last_source_clock_ ? next_timestamp(last_source_clock_us_) : 0);
    int64_t source_pts_us = has_pts
                                ? rescale_delta_to_microseconds(in.pts, origin_ticks_, tb_num,
                                                                tb_den)
                                : source_dts_us;

    // DTS is reliable in decode order. When both packets have DTS, any rollback
    // is a discontinuity. If either packet falls back to PTS, B-frame reordering
    // can move the clock slightly, so only detect large jumps across that boundary.
    const int64_t source_clock_us = has_dts ? source_dts_us : source_pts_us;
    const bool comparable_clock = has_last_source_clock_;
    const bool both_have_dts = has_dts && last_source_clock_has_dts_;
    const bool went_backwards = comparable_clock &&
                                (both_have_dts
                                     ? source_clock_us < last_source_clock_us_
                                     : is_backward_jump(source_clock_us, last_source_clock_us_));
    const bool jumped_forward = comparable_clock &&
                                is_forward_jump(source_clock_us, last_source_clock_us_);
    if (went_backwards || jumped_forward) {
        origin_ticks_ = decode_ticks;
        source_dts_us = 0;
        source_pts_us = has_pts
                            ? rescale_delta_to_microseconds(in.pts, origin_ticks_, tb_num,
                                                            tb_den)
                            : 0;
        timestamp_shift_us_ = has_last_dts_ ? next_timestamp(last_dts_us_) : 0;
        out.pkt_flags |= ZHULONG_PKT_FLAG_DISCONTINUITY;
        ++gop_id_;
        ++discontinuity_count_;
    } else if (!has_dts && has_last_source_clock_ &&
               source_dts_us <= last_source_clock_us_) {
        // Without source DTS, keep a synthetic decode clock monotonic without
        // rewriting the presentation timestamp of a reordered frame.
        source_dts_us = next_timestamp(last_source_clock_us_);
    }

    int64_t dts_us = saturating_add(source_dts_us, timestamp_shift_us_);
    int64_t pts_us = has_pts ? saturating_add(source_pts_us, timestamp_shift_us_) : dts_us;
    if (has_last_dts_ && dts_us <= last_dts_us_) {
        const int64_t next_dts_us = next_timestamp(last_dts_us_);
        if (has_dts) {
            const int64_t adjustment = next_dts_us - dts_us;
            pts_us = saturating_add(pts_us, adjustment);
        }
        dts_us = next_dts_us;
    }

    has_last_source_clock_ = true;
    last_source_clock_us_ = source_dts_us;
    last_source_clock_has_dts_ = has_dts;
    has_last_dts_ = true;
    last_dts_us_ = dts_us;
    out.pts_us = pts_us;
    out.dts_us = dts_us;
    if (!has_pts) out.pkt_flags |= ZHULONG_PKT_FLAG_TIMESTAMP_SYNTHETIC;
    if (!has_dts) out.pkt_flags |= ZHULONG_PKT_FLAG_DTS_SYNTHETIC;

    // ------------------------------------------------- 2) 帧分类 + 参数集注入
    const bool is_idr = in.key_frame != 0;
    if (is_idr) {
        out.frame_class = Zhulong_FRAME_IDR;
        out.gop_id = ++gop_id_;
        if (!parameter_sets_.empty() &&
            !has_complete_parameter_sets(codec_, in.data, in.size)) {
            // 复用 stitch_ 缓冲：只在关键帧触发，容量一次长到最大帧后不再增长
            stitch_.resize(parameter_sets_.size() + in.size);
            std::memcpy(stitch_.data(), parameter_sets_.data(), parameter_sets_.size());
            if (in.size > 0 && in.data != nullptr) {
                std::memcpy(stitch_.data() + parameter_sets_.size(), in.data, in.size);
            }
            out.data = stitch_.data();
            out.size = stitch_.size();
            out.pkt_flags |= ZHULONG_PKT_FLAG_PARAM_SETS_INJECTED;
            ++injected_count_;
        }
    } else {
        out.frame_class = is_non_reference_frame(in.codec, in.data, in.size)
                              ? Zhulong_FRAME_NON_REFERENCE
                              : Zhulong_FRAME_PREDICTIVE;
        out.gop_id = gop_id_;
    }

    return out;
}

} // namespace zhulong
