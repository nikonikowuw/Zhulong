/**
 * @file ffmpeg_decoder.cpp
 * @brief 基于静态 FFmpeg 的 CPU 软解节点实现
 *
 * 核心实现规范：
 * 1. 参数集注入：为 extradata 补充 AV_INPUT_BUFFER_PADDING_SIZE 字节的安全填充，杜绝 FFmpeg 汇编优化越界读取；
 * 2. 借用指针防护：在 send_packet 调用后立即解除 AVPacket 对外部数据指针的挂接，防止 unref 误释放借用内存；
 * 3. 多平面装配：软解标准输出 YUV420P 多平面格式，准确回填 host_planes 与 plane_strides；
 * 4. 延迟回收闭包：通过 release_fn lambda 捕获 AVFrame 指针，确保当所有外部消费者完成帧借用后，才由最后一个持有方安全释放底层帧缓冲。
 */

#include "nodes/decode/ffmpeg_decoder.hpp"
#include "nodes/decode/mpp_decoder.hpp"

#include <cstring>
#include <stdexcept>

namespace zhulong {

IDecodeNode::Ptr IDecodeNode::create(int32_t codec, const uint8_t *extradata, size_t extradata_size,
                                     int32_t time_base_num, int32_t time_base_den,
                                     DecoderBackend backend) {
    if (backend == DecoderBackend::AUTO || backend == DecoderBackend::MPP_HW) {
        if (MppDecodeNode::is_supported()) {
            try {
                return std::make_unique<MppDecodeNode>(codec, extradata, extradata_size, time_base_num, time_base_den);
            } catch (...) {
                if (backend == DecoderBackend::MPP_HW) {
                    throw;
                }
            }
        } else if (backend == DecoderBackend::MPP_HW) {
            throw std::runtime_error("MPP hardware decoder requested but not supported on this host");
        }
    }
    return std::make_unique<FFmpegDecodeNode>(codec, extradata, extradata_size, time_base_num, time_base_den);
}

FFmpegDecodeNode::FFmpegDecodeNode(int32_t codec, const uint8_t *extradata, size_t extradata_size,
                                   int32_t time_base_num, int32_t time_base_den)
    : time_base_num_(time_base_num > 0 ? time_base_num : 1),
      time_base_den_(time_base_den > 0 ? time_base_den : 90000) {
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

    // 配置 extradata (SPS/PPS/VPS 参数集)，补充尾部填充字节以防止 FFmpeg SIMD 优化越界读取
    if (extradata && extradata_size > 0) {
        codec_ctx_->extradata = static_cast<uint8_t *>(av_mallocz(extradata_size + AV_INPUT_BUFFER_PADDING_SIZE));
        if (!codec_ctx_->extradata) {
            avcodec_free_context(&codec_ctx_);
            throw std::bad_alloc();
        }
        std::memcpy(codec_ctx_->extradata, extradata, extradata_size);
        codec_ctx_->extradata_size = static_cast<int>(extradata_size);
    }

    // 单流软解固定采用单线程，避免每路流创建多个线程引起系统级线程爆炸，同时消除内部竞争
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
        // 发送 NULL 包以 flush/drain 解码器内部驻留帧
        return avcodec_send_packet(codec_ctx_, nullptr);
    }

    av_packet_unref(packet_);
    packet_->data = const_cast<uint8_t *>(data);
    packet_->size = static_cast<int>(size);
    packet_->pts = pts;
    packet_->dts = dts;

    int ret = avcodec_send_packet(codec_ctx_, packet_);
    // ⚠️ 立即解除借用指针挂接：防止后续 av_packet_unref 误尝试释放借用的外部只读内存
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
        return 1; // 暂无输出，需继续喂入下一个数据包
    }
    if (ret < 0) {
        av_frame_free(&av_frame);
        return ret; // 解码器内部严重错误
    }

    struct FrameGuard {
        AVFrame *f;
        ~FrameGuard() {
            if (f) {
                av_frame_free(&f);
            }
        }
    } guard{av_frame};

    // 成功解出一帧，装配至跨层统一 HardwareFrame 对象
    auto frame = std::make_shared<HardwareFrame>();
    frame->width = static_cast<uint32_t>(av_frame->width);
    frame->height = static_cast<uint32_t>(av_frame->height);
    frame->stride = static_cast<uint32_t>(av_frame->linesize[0]);
    frame->vstride = static_cast<uint32_t>(av_frame->height);
    // ⚠️ 明确标记多平面格式：软解输出标准为 YUV420P（含 Full Range YUVJ420P），严禁外部当作单平面连续 NV12 解析
    if (av_frame->format == AV_PIX_FMT_YUV420P || av_frame->format == AV_PIX_FMT_YUVJ420P) {
        frame->format = FRAME_FORMAT_YUV420P;
    } else if (av_frame->format == AV_PIX_FMT_NV12) {
        frame->format = FRAME_FORMAT_NV12;
    } else {
        frame->format = FRAME_FORMAT_UNKNOWN;
    }
    frame->host_ptr = av_frame->data[0];
    for (int i = 0; i < 4; ++i) {
        if (av_frame->data[i]) {
            frame->host_planes[i] = av_frame->data[i];
            frame->plane_strides[i] = static_cast<uint32_t>(av_frame->linesize[i]);
        }
    }
    // ⚠️ 时间戳保真透传：严禁将没有 PTS 的帧直接当作 PTS=0 处理
    frame->has_pts = (av_frame->pts != AV_NOPTS_VALUE);
    frame->pts = frame->has_pts ? av_frame->pts : 0;
    frame->time_base_num = time_base_num_;
    frame->time_base_den = time_base_den_;

    // ⚠️ 绑定专属 release_fn：当所有持有方（shared_ptr）析构时安全归还底层 AVFrame 缓冲，彻底杜绝悬挂指针
    frame->release_fn = [av_frame](HardwareFrame *) {
        AVFrame *f = av_frame;
        av_frame_free(&f);
    };

    guard.f = nullptr; // 成功转移所有权给 release_fn
    out_frame = std::move(frame);
    return 0;
}

void FFmpegDecodeNode::flush() {
    if (codec_ctx_) {
        // 清空 DPB 参考帧缓冲
        avcodec_flush_buffers(codec_ctx_);
    }
}

} // namespace zhulong
