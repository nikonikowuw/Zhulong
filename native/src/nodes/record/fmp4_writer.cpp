/**
 * @file fmp4_writer.cpp
 * @brief 事件录像片段的 fMP4 写出节点实现
 */

#include "nodes/record/fmp4_writer.hpp"

#include "Zhulong/engine.h"
#include "common/log.hpp"
#include "pipeline/preview_normalizer.hpp"

#include <algorithm>
#include <cerrno>
#include <cstdio>
#include <cstring>
#include <sys/stat.h>
#include <utility>

extern "C" {
#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include <libavutil/dict.h>
#include <libavutil/mem.h>
}

namespace zhulong {

namespace {

constexpr const char *log_module = "nodes.record";
constexpr const char *fragment_movflags = "frag_keyframe+empty_moov+default_base_moof";

/** @brief 定位下一个 Annex B 起始码；返回起始码首字节偏移，*code_size 写入 3 或 4 */
size_t find_start_code(const uint8_t *data, size_t size, size_t from, size_t *code_size) noexcept {
    for (size_t i = from; i + 3 <= size; ++i) {
        if (data[i] != 0 || data[i + 1] != 0) continue;
        if (data[i + 2] == 1) {
            *code_size = 3;
            // 4 字节起始码 00 00 00 01：把前导 0 计入，避免它被当成上一个 NAL 的尾字节
            if (i > from && data[i - 1] == 0) {
                *code_size = 4;
                return i - 1;
            }
            return i;
        }
    }
    *code_size = 0;
    return size;
}

bool is_parameter_set(int32_t codec, uint8_t header) noexcept {
    if (codec == Zhulong_CODEC_H264) {
        const int type = header & 0x1F;
        return type == 7 || type == 8;
    }
    if (codec == Zhulong_CODEC_H265) {
        const int type = (header >> 1) & 0x3F;
        return type == 32 || type == 33 || type == 34;
    }
    return false;
}

AVCodecID to_av_codec(int32_t codec) noexcept {
    if (codec == Zhulong_CODEC_H264) return AV_CODEC_ID_H264;
    if (codec == Zhulong_CODEC_H265) return AV_CODEC_ID_HEVC;
    return AV_CODEC_ID_NONE;
}

/** @brief 用 FFmpeg 码流解析器从关键帧的 SPS 里取宽高（不需要解码器） */
void probe_dimensions(AVCodecID codec_id, const uint8_t *data, size_t size, int *width, int *height) noexcept {
    AVCodecParserContext *parser = av_parser_init(codec_id);
    if (parser == nullptr) return;
    AVCodecContext *context = avcodec_alloc_context3(nullptr);
    if (context == nullptr) {
        av_parser_close(parser);
        return;
    }
    context->codec_id = codec_id;
    context->codec_type = AVMEDIA_TYPE_VIDEO;
    parser->flags |= PARSER_FLAG_COMPLETE_FRAMES;

    // 解析器要求输入尾部有 AV_INPUT_BUFFER_PADDING_SIZE 字节的零填充
    uint8_t *padded = static_cast<uint8_t *>(av_mallocz(size + AV_INPUT_BUFFER_PADDING_SIZE));
    if (padded != nullptr) {
        std::memcpy(padded, data, size);
        uint8_t *out = nullptr;
        int out_size = 0;
        av_parser_parse2(parser, context, &out, &out_size, padded, static_cast<int>(size),
                         AV_NOPTS_VALUE, AV_NOPTS_VALUE, 0);
        if (parser->width > 0 && parser->height > 0) {
            *width = parser->width;
            *height = parser->height;
        } else if (context->width > 0 && context->height > 0) {
            *width = context->width;
            *height = context->height;
        }
        av_free(padded);
    }
    avcodec_free_context(&context);
    av_parser_close(parser);
}

} // namespace

std::vector<uint8_t> extract_annex_b_parameter_sets(int32_t codec, const uint8_t *data, size_t size) {
    std::vector<uint8_t> out;
    if (data == nullptr || size < 4) return out;
    size_t code_size = 0;
    size_t start = find_start_code(data, size, 0, &code_size);
    while (start < size) {
        const size_t nal_begin = start + code_size;
        size_t next_code = 0;
        const size_t next = find_start_code(data, size, nal_begin, &next_code);
        size_t nal_end = next;
        // 去掉 NAL 尾部的 trailing zero（属于下一个起始码之前的填充）
        while (nal_end > nal_begin && data[nal_end - 1] == 0) --nal_end;
        if (nal_end > nal_begin && is_parameter_set(codec, data[nal_begin])) {
            static const uint8_t start_code[4] = {0, 0, 0, 1};
            out.insert(out.end(), start_code, start_code + 4);
            out.insert(out.end(), data + nal_begin, data + nal_end);
        }
        start = next;
        code_size = next_code;
    }
    return out;
}

Fmp4Writer::Fmp4Writer(Fmp4WriterConfig config)
    : config_(std::move(config)),
      pts_reorder_offset_us_(std::max<int64_t>(0, config_.pts_reorder_offset_us)) {
    part_path_ = config_.path + ".part";
}

Fmp4Writer::~Fmp4Writer() {
    if (!finished_) abort();
    if (packet_ != nullptr) av_packet_free(&packet_);
}

void Fmp4Writer::close_context() noexcept {
    if (context_ == nullptr) return;
    if (context_->pb != nullptr) avio_closep(&context_->pb);
    avformat_free_context(context_);
    context_ = nullptr;
}

void Fmp4Writer::remove_part() noexcept {
    if (!opened_) return;
    if (std::remove(part_path_.c_str()) != 0 && errno != ENOENT) {
        ZL_LOGW(0, log_module, "clip_part_remove_failed errno=%d", errno);
    }
}

void Fmp4Writer::abort() noexcept {
    if (finished_) return;
    close_context();
    remove_part();
    opened_ = false;
    failed_ = true;
    finished_ = true;
}

bool Fmp4Writer::open(const uint8_t *first_key_frame, size_t size) noexcept {
    const AVCodecID codec_id = to_av_codec(config_.codec);
    if (codec_id == AV_CODEC_ID_NONE) {
        ZL_LOGE(0, log_module, "clip_open_failed reason=unsupported_codec codec=%d", config_.codec);
        return false;
    }

    std::vector<uint8_t> parameter_sets;
    bool used_hint = false;
    try {
        parameter_sets = extract_annex_b_parameter_sets(config_.codec, first_key_frame, size);
        // 仅含部分参数集（如只有 PPS 缺 SPS/VPS）会令 movenc.c 静默写出空 avcC/hvcC：
        // 必须校验完整参数集，否则回退到流元数据里的 parameter_sets_hint。
        if (!has_complete_parameter_sets(config_.codec, parameter_sets.data(), parameter_sets.size())) {
            if (has_complete_parameter_sets(config_.codec, config_.parameter_sets_hint.data(),
                                            config_.parameter_sets_hint.size())) {
                parameter_sets = config_.parameter_sets_hint;
                used_hint = true;
            } else {
                parameter_sets.clear();
            }
        }
    } catch (...) {
        ZL_LOGE(0, log_module, "clip_open_failed reason=out_of_memory");
        return false;
    }
    if (parameter_sets.empty()) {
        ZL_LOGE(0, log_module, "clip_open_failed reason=missing_parameter_sets codec=%d", config_.codec);
        return false;
    }

    int width = 0;
    int height = 0;
    probe_dimensions(codec_id, first_key_frame, size, &width, &height);
    if ((width <= 0 || height <= 0) && used_hint) {
        try {
            std::vector<uint8_t> combined;
            combined.reserve(parameter_sets.size() + size);
            combined.insert(combined.end(), parameter_sets.begin(), parameter_sets.end());
            combined.insert(combined.end(), first_key_frame, first_key_frame + size);
            probe_dimensions(codec_id, combined.data(), combined.size(), &width, &height);
        } catch (...) {
            // 维度探测 OOM 时继续走 width_hint / height_hint 兜底
        }
    }
    if (width <= 0 || height <= 0) {
        width = config_.width_hint;
        height = config_.height_hint;
    }
    if (width <= 0 || height <= 0) {
        ZL_LOGE(0, log_module, "clip_open_failed reason=unknown_dimensions codec=%d", config_.codec);
        return false;
    }

    packet_ = av_packet_alloc();
    if (packet_ == nullptr) return false;

    int rc = avformat_alloc_output_context2(&context_, nullptr, "mp4", part_path_.c_str());
    if (rc < 0 || context_ == nullptr) {
        ZL_LOGE(0, log_module, "clip_open_failed reason=alloc_output rc=%d", rc);
        return false;
    }
    AVStream *stream = avformat_new_stream(context_, nullptr);
    if (stream == nullptr) return false;
    stream->time_base = AVRational{1, 1000000};
    AVCodecParameters *par = stream->codecpar;
    par->codec_type = AVMEDIA_TYPE_VIDEO;
    par->codec_id = codec_id;
    par->width = width;
    par->height = height;
    // HEVC 用 hvc1：Safari / MSE 的 `hvc1.*` 探测只认这个样本入口
    if (codec_id == AV_CODEC_ID_HEVC) par->codec_tag = MKTAG('h', 'v', 'c', '1');
    par->extradata = static_cast<uint8_t *>(av_mallocz(parameter_sets.size() + AV_INPUT_BUFFER_PADDING_SIZE));
    if (par->extradata == nullptr) return false;
    std::memcpy(par->extradata, parameter_sets.data(), parameter_sets.size());
    par->extradata_size = static_cast<int>(parameter_sets.size());

    rc = avio_open(&context_->pb, part_path_.c_str(), AVIO_FLAG_WRITE);
    if (rc < 0) {
        ZL_LOGE(0, log_module, "clip_open_failed reason=avio_open rc=%d", rc);
        return false;
    }
    opened_ = true;

    AVDictionary *options = nullptr;
    av_dict_set(&options, "movflags", fragment_movflags, 0);
    rc = avformat_write_header(context_, &options);
    av_dict_free(&options);
    if (rc < 0) {
        ZL_LOGE(0, log_module, "clip_open_failed reason=write_header rc=%d", rc);
        return false;
    }
    header_written_ = true;
    return true;
}

bool Fmp4Writer::write(int64_t pts_us, int64_t dts_us, bool key_frame, const uint8_t *data, size_t size) noexcept {
    if (failed_ || finished_) return false;
    if (data == nullptr || size == 0 || size > static_cast<size_t>(INT32_MAX - AV_INPUT_BUFFER_PADDING_SIZE)) {
        abort();
        return false;
    }
    if (!header_written_) {
        if (!key_frame) {
            ZL_LOGE(0, log_module, "clip_open_failed reason=first_sample_not_key");
            abort();
            return false;
        }
        if (!open(data, size)) {
            abort();
            return false;
        }
        stats_.first_pts_us = pts_us;
        stats_.first_dts_us = dts_us;
    }

    // 以首包 dts 为 0 点；存在 B 帧时加上 pts_reorder_offset_us_ 保持呈现序且满足 pts >= dts。
    const int64_t dts = dts_us - stats_.first_dts_us;
    int64_t pts = (pts_us - stats_.first_dts_us) + pts_reorder_offset_us_;
    if (pts < dts) pts = dts;

    if (av_new_packet(packet_, static_cast<int>(size)) < 0) {
        abort();
        return false;
    }
    std::memcpy(packet_->data, data, size);
    packet_->pts = pts;
    packet_->dts = dts;
    packet_->stream_index = 0;
    packet_->time_base = AVRational{1, 1000000};
    if (key_frame) packet_->flags |= AV_PKT_FLAG_KEY;

    const int rc = av_write_frame(context_, packet_);
    av_packet_unref(packet_);
    if (rc < 0) {
        ZL_LOGE(0, log_module, "clip_write_failed rc=%d", rc);
        abort();
        return false;
    }
    ++stats_.packets;
    stats_.last_pts_us = pts_us;
    stats_.last_dts_us = dts_us;
    return true;
}

bool Fmp4Writer::finish() noexcept {
    if (finished_) return !failed_;
    if (failed_ || !header_written_) {
        abort();
        return false;
    }
    int rc = av_write_trailer(context_);
    if (rc < 0) {
        ZL_LOGE(0, log_module, "clip_finish_failed reason=write_trailer rc=%d", rc);
        abort();
        return false;
    }
    rc = avio_closep(&context_->pb);
    if (rc < 0) {
        ZL_LOGE(0, log_module, "clip_finish_failed reason=avio_close rc=%d", rc);
        abort();
        return false;
    }
    avformat_free_context(context_);
    context_ = nullptr;

    struct stat info {};
    if (::stat(part_path_.c_str(), &info) != 0) {
        ZL_LOGE(0, log_module, "clip_finish_failed reason=stat errno=%d", errno);
        abort();
        return false;
    }
    if (std::rename(part_path_.c_str(), config_.path.c_str()) != 0) {
        ZL_LOGE(0, log_module, "clip_finish_failed reason=rename errno=%d", errno);
        abort();
        return false;
    }
    stats_.size_bytes = static_cast<int64_t>(info.st_size);
    finished_ = true;
    return true;
}

} // namespace zhulong
