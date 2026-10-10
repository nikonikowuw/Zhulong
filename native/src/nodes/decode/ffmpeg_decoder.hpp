/**
 * @file ffmpeg_decoder.hpp
 * @brief 基于静态 FFmpeg 的 CPU 软解节点定义
 */

#pragma once

#include "nodes/decode/decoder.hpp"

extern "C" {
#include <libavcodec/avcodec.h>
#include <libavutil/error.h>
}

namespace zhulong {

class FFmpegDecodeNode final : public IDecodeNode {
public:
    FFmpegDecodeNode(int32_t codec, const uint8_t *extradata, size_t extradata_size);
    ~FFmpegDecodeNode() override;

    int send_packet(const uint8_t *data, size_t size, int64_t pts, int64_t dts) override;
    int receive_frame(HardwareFrame::Ptr &out_frame) override;
    void flush() override;

private:
    AVCodecContext *codec_ctx_{nullptr};
    AVPacket *packet_{nullptr};
};

} // namespace zhulong
