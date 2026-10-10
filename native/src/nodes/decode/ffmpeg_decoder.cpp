/**
 * @file ffmpeg_decoder.cpp
 * @brief 基于静态 FFmpeg 的 CPU 软解节点实现
 */

#include "nodes/decode/ffmpeg_decoder.hpp"

#include <cstring>
#include <stdexcept>

namespace zhulong {

IDecodeNode::Ptr IDecodeNode::create(int32_t codec, const uint8_t *extradata, size_t extradata_size,
                                     DecoderBackend backend) {
    (void)backend;
    return std::make_unique<FFmpegDecodeNode>(codec, extradata, extradata_size);
}

FFmpegDecodeNode::FFmpegDecodeNode(int32_t codec, const uint8_t *extradata, size_t extradata_size) {
    AVCodecID codec_id = AV_CODEC_ID_NONE;
    if (codec == Zhulong_CODEC_H264) {
        codec_id = AV_CODEC_ID_H264;
    } else if (codec == Zhulong_CODEC_H265) {
        codec_id = AV_CODEC_ID_HEVC;
    } else {
        throw std::invalid_argument("Unsupported codec for FFmpegDecodeNode");
    }

    const AVCodec *decoder = avcodec_find_decoder(codec_id);
    if (!decoder) {
        throw std::runtime_error("Codec decoder not found in static FFmpeg");
    }

    codec_ctx_ = avcodec_alloc_context3(decoder);
    if (!codec_ctx_) {
        throw std::bad_alloc();
    }

    // 配置 extradata (SPS/PPS/VPS 参数集)
    if (extradata && extradata_size > 0) {
        codec_ctx_->extradata = static_cast<uint8_t *>(av_mallocz(extradata_size + AV_INPUT_BUFFER_PADDING_SIZE));
        if (!codec_ctx_->extradata) {
            avcodec_free_context(&codec_ctx_);
            throw std::bad_alloc();
        }
        std::memcpy(codec_ctx_->extradata, extradata, extradata_size);
        codec_ctx_->extradata_size = static_cast<int>(extradata_size);
    }

    // 单流使用单线程解码，避免线程爆炸与内部未同步线程竞争
    codec_ctx_->thread_count = 1;

    int ret = avcodec_open2(codec_ctx_, decoder, nullptr);
    if (ret < 0) {
        avcodec_free_context(&codec_ctx_);
        throw std::runtime_error("Failed to open codec context");
    }

    packet_ = av_packet_alloc();
    if (!packet_) {
        avcodec_free_context(&codec_ctx_);
        throw std::bad_alloc();
    }
}

FFmpegDecodeNode::~FFmpegDecodeNode() {
    if (packet_) {
        av_packet_free(&packet_);
    }
    if (codec_ctx_) {
        avcodec_free_context(&codec_ctx_);
    }
}

int FFmpegDecodeNode::send_packet(const uint8_t *data, size_t size, int64_t pts, int64_t dts) {
    if (!codec_ctx_) return -1;

    if (!data || size == 0) {
        // 发送 NULL 包以 flush/drain 解码器
        return avcodec_send_packet(codec_ctx_, nullptr);
    }

    av_packet_unref(packet_);
    packet_->data = const_cast<uint8_t *>(data);
    packet_->size = static_cast<int>(size);
    packet_->pts = pts;
    packet_->dts = dts;

    int ret = avcodec_send_packet(codec_ctx_, packet_);
    // 重置借用指针，防止 unref 尝试释放借用的外部内存
    packet_->data = nullptr;
    packet_->size = 0;
    return ret;
}

int FFmpegDecodeNode::receive_frame(HardwareFrame::Ptr &out_frame) {
    out_frame = nullptr;
    if (!codec_ctx_) return -1;

    AVFrame *av_frame = av_frame_alloc();
    if (!av_frame) return -1;

    int ret = avcodec_receive_frame(codec_ctx_, av_frame);
    if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) {
        av_frame_free(&av_frame);
        return 1; // 暂无输出，需继续喂入数据包
    }
    if (ret < 0) {
        av_frame_free(&av_frame);
        return ret; // 解码器内部错误
    }

    // 成功解出一帧，装配至统一 HardwareFrame 对象
    auto frame = std::make_shared<HardwareFrame>();
    frame->width = static_cast<uint32_t>(av_frame->width);
    frame->height = static_cast<uint32_t>(av_frame->height);
    frame->stride = static_cast<uint32_t>(av_frame->linesize[0]);
    frame->vstride = static_cast<uint32_t>(av_frame->height);
    frame->host_ptr = av_frame->data[0];
    frame->pts = av_frame->pts;

    // 绑定专属 release_fn：当所有持有方析构时安全归还底层 AVFrame 缓冲
    frame->release_fn = [av_frame](HardwareFrame *) {
        AVFrame *f = av_frame;
        av_frame_free(&f);
    };

    out_frame = std::move(frame);
    return 0;
}

void FFmpegDecodeNode::flush() {
    if (codec_ctx_) {
        avcodec_flush_buffers(codec_ctx_);
    }
}

} // namespace zhulong
