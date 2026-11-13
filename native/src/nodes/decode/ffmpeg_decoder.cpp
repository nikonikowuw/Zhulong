/**
 * @file ffmpeg_decoder.cpp
 * @brief 基于静态 FFmpeg 的 CPU 软解节点实现
 *
 * 核心实现规范：
 * 1. 参数集注入：为 extradata 补充 AV_INPUT_BUFFER_PADDING_SIZE 字节的安全填充，杜绝 FFmpeg 汇编优化越界读取；
 * 2. 借用指针防护：在 send_packet 调用后立即解除 AVPacket 对外部数据指针的挂接，防止 unref 误释放借用内存；
 *    注意 libavcodec 内部会通过 `av_packet_ref()` 对 `buf == nullptr` 的包做一次受控拷贝（见 FFmpeg 7.1.5
 *    `libavcodec/decode.c` 与 `libavcodec/packet.c`），因此这里是「安全借用」而**不是**零拷贝；
 * 3. 多平面装配：软解标准输出 YUV420P 多平面格式，准确回填 host_planes 与 plane_strides；
 * 4. 延迟回收闭包：通过 release_fn lambda 捕获 AVFrame 指针，确保当所有外部消费者完成帧借用后，
 *    才由最后一个持有方安全释放底层帧缓冲。
 */

#include "nodes/decode/ffmpeg_decoder.hpp"
#include "common/log.hpp"

#include <cerrno>
#include <climits>
#include <cstring>
#include <stdexcept>

namespace zhulong {

FFmpegDecodeNode::FFmpegDecodeNode(int32_t codec, const uint8_t *extradata, size_t extradata_size,
                                   int32_t time_base_num, int32_t time_base_den, uint64_t stream_id)
    : time_base_num_(time_base_num > 0 ? time_base_num : 1),
      time_base_den_(time_base_den > 0 ? time_base_den : 90000),
      stream_id_(stream_id) {
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

    codec_ctx_.reset(avcodec_alloc_context3(decoder));
    if (!codec_ctx_) {
        throw std::bad_alloc();
    }

    // 配置 extradata (SPS/PPS/VPS 参数集)，补充尾部填充字节以防止 FFmpeg SIMD 优化越界读取
    if (extradata && extradata_size > 0) {
        if (extradata_size > static_cast<size_t>(INT_MAX) - AV_INPUT_BUFFER_PADDING_SIZE) {
            throw std::invalid_argument("Extradata size exceeds INT_MAX");
        }
        codec_ctx_->extradata = static_cast<uint8_t *>(av_mallocz(extradata_size + AV_INPUT_BUFFER_PADDING_SIZE));
        if (!codec_ctx_->extradata) {
            throw std::bad_alloc();
        }
        std::memcpy(codec_ctx_->extradata, extradata, extradata_size);
        codec_ctx_->extradata_size = static_cast<int>(extradata_size);
    }

    // 单流软解固定采用单线程，避免每路流创建多个线程引起系统级线程爆炸，同时消除内部竞争
    codec_ctx_->thread_count = 1;

    int ret = avcodec_open2(codec_ctx_.get(), decoder, nullptr);
    if (ret < 0) {
        throw std::runtime_error("Failed to open codec context");
    }

    packet_.reset(av_packet_alloc());
    if (!packet_) {
        throw std::bad_alloc();
    }
}

int FFmpegDecodeNode::send_packet(const uint8_t *data, size_t size, int64_t pts, int64_t dts) {
    if (!codec_ctx_ || !packet_) return decode_backend_failure;

    if (!data || size == 0) {
        // 发送 NULL 包以 flush/drain 解码器内部驻留帧
        return avcodec_send_packet(codec_ctx_.get(), nullptr);
    }

    if (size > static_cast<size_t>(INT_MAX)) {
        ZL_LOGE(stream_id_, "nodes.decode", "ffmpeg_packet_too_large size=%zu", size);
        return AVERROR(EINVAL);
    }

    av_packet_unref(packet_.get());
    packet_->data = const_cast<uint8_t *>(data);
    packet_->size = static_cast<int>(size);
    packet_->pts = pts;
    packet_->dts = dts;

    int ret = avcodec_send_packet(codec_ctx_.get(), packet_.get());
    // ⚠️ 立即解除借用指针挂接：防止后续 av_packet_unref 误尝试释放借用的外部只读内存
    packet_->data = nullptr;
    packet_->size = 0;
    return ret;
}

int FFmpegDecodeNode::receive_frame(HardwareFrame::Ptr &out_frame) {
    out_frame = nullptr;
    if (!codec_ctx_) return decode_backend_failure;

    if (!cached_frame_) {
        cached_frame_.reset(av_frame_alloc());
        if (!cached_frame_) return AVERROR(ENOMEM);
    }

    while (true) {
        int ret = avcodec_receive_frame(codec_ctx_.get(), cached_frame_.get());
        if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) {
            // ⚠️ EAGAIN（暂无输出）与 AVERROR_EOF（drain 已排空）共用 decode_again：
            //    receive 侧不区分两者，语义见 decoder.hpp 的契约。drain 之后要继续送包，
            //    必须先 flush() 复位 draining 状态，否则 send_packet 恒返 AVERROR_EOF。
            //    此时 cached_frame_ 保留复用，杜绝轮询中每帧产生无谓的 alloc/free 堆颠簸。
            return decode_again;
        }
        if (ret < 0) {
            av_frame_unref(cached_frame_.get());
            return ret; // 解码器内部严重错误（已是 AVERROR 命名空间）
        }

        // 跳过宽高无效的异常损坏帧，避免 static_cast<uint32_t> 将负值变为超大尺寸传给下游
        if (cached_frame_->width <= 0 || cached_frame_->height <= 0) {
            av_frame_unref(cached_frame_.get());
            continue;
        }

        // 成功取出有效帧：转移给局部 guard 管理，cached_frame_ 置空在下一轮按需补充分配
        std::unique_ptr<AVFrame, detail::AvFrameDeleter> guard(cached_frame_.release());

        // ⚠️ 负 linesize 表示垂直翻转输出（data[i] 指向该平面最后一行），或 linesize[0] 小于图像宽度。
        //    HardwareFrame 没有翻转语义，把非法步长传下去会让下游读到错行或越界——宁可显式判为 UNKNOWN，
        //    也不能静默错帧。仅在真正出现的平面上判定负值，避开未使用平面的 -1 哨兵。
        bool invalid_stride = guard->linesize[0] < guard->width;
        for (int i = 0; i < frame_plane_count; ++i) {
            if (guard->data[i] != nullptr && guard->linesize[i] < 0) {
                invalid_stride = true;
            }
        }

        // 成功解出一帧，装配至跨层统一 HardwareFrame 对象
        auto frame = std::make_shared<HardwareFrame>();
        frame->width = static_cast<uint32_t>(guard->width);
        frame->height = static_cast<uint32_t>(guard->height);
        frame->stride = (!invalid_stride && guard->linesize[0] > 0)
                            ? static_cast<uint32_t>(guard->linesize[0])
                            : 0;
        frame->vstride = static_cast<uint32_t>(guard->height);
        // ⚠️ 明确标记多平面格式：软解输出标准为 YUV420P（含 Full Range YUVJ420P），严禁外部当作单平面连续 NV12 解析
        if (invalid_stride) {
            // 不填任何平面指针：下游按 format 拒绝的同时，也拿不到「指针有效但步长非法」的半成品
            frame->format = FRAME_FORMAT_UNKNOWN;
            if (!invalid_stride_logged_) {
                invalid_stride_logged_ = true;
                ZL_LOGW(stream_id_, "nodes.decode",
                        "ffmpeg_invalid_linesize width=%d luma=%d chroma_u=%d chroma_v=%d",
                        guard->width, guard->linesize[0], guard->linesize[1], guard->linesize[2]);
            }
        } else if (guard->format == AV_PIX_FMT_YUV420P || guard->format == AV_PIX_FMT_YUVJ420P) {
            frame->format = FRAME_FORMAT_YUV420P;
        } else if (guard->format == AV_PIX_FMT_NV12) {
            frame->format = FRAME_FORMAT_NV12;
        } else {
            // 10bit 等未支持格式标记为 UNKNOWN：下游（jpeg_encoder / snapshot / frame_broker）会显式拒绝，
            // 绝不猜测成 NV12 造成静默错帧
            frame->format = FRAME_FORMAT_UNKNOWN;
            if (!unsupported_format_logged_) {
                unsupported_format_logged_ = true;
                ZL_LOGW(stream_id_, "nodes.decode", "ffmpeg_unsupported_pix_fmt fmt=%d", guard->format);
            }
        }
        if (!invalid_stride) {
            frame->host_ptr = guard->data[0];
            for (int i = 0; i < frame_plane_count; ++i) {
                if (guard->data[i]) {
                    frame->host_planes[i] = guard->data[i];
                    frame->plane_strides[i] = guard->linesize[i] > 0
                                                  ? static_cast<uint32_t>(guard->linesize[i])
                                                  : 0;
                }
            }
        }
        // ⚠️ 时间戳保真透传：严禁将没有 PTS 的帧直接当作 PTS=0 处理
        frame->has_pts = (guard->pts != AV_NOPTS_VALUE);
        frame->pts = frame->has_pts ? guard->pts : 0;
        frame->time_base_num = time_base_num_;
        frame->time_base_den = time_base_den_;

        // ⚠️ 绑定专属 release_fn：当所有持有方（shared_ptr）析构时安全归还底层 AVFrame 缓冲，彻底杜绝悬挂指针
        AVFrame *av_frame = guard.get();
        frame->release_fn = [av_frame](HardwareFrame *) {
            AVFrame *f = av_frame;
            av_frame_free(&f);
        };

        guard.release(); // 成功转移所有权给 release_fn
        out_frame = std::move(frame);
        return decode_ok;
    }
}

void FFmpegDecodeNode::flush() {
    if (cached_frame_) {
        av_frame_unref(cached_frame_.get());
    }
    if (codec_ctx_) {
        // 清空 DPB 参考帧缓冲，同时复位内部 draining 状态（后续仍可继续送包）
        avcodec_flush_buffers(codec_ctx_.get());
    }
}

} // namespace zhulong
