/**
 * @file mpp_decoder.cpp
 * @brief Rockchip MPP 硬件解码节点实现
 */

#include "mpp_decoder.hpp"

extern "C" {
#include <libavutil/avutil.h>
#include <libavutil/error.h>
}

#include <iostream>
#include <stdexcept>
#include <string>
#include <unistd.h>

namespace zhulong {

bool MppDecodeNode::is_supported() noexcept {
    return mpp::MppLoader::instance().is_supported();
}

MppDecodeNode::MppDecodeNode(int32_t codec, const uint8_t *extradata, size_t extradata_size,
                             int32_t time_base_num, int32_t time_base_den)
    : loader_(mpp::MppLoader::instance()),
      time_base_num_(time_base_num > 0 ? time_base_num : 1),
      time_base_den_(time_base_den > 0 ? time_base_den : 90000) {
    if (!loader_.is_supported()) {
        throw std::runtime_error("Rockchip MPP hardware decoding is not supported on this host");
    }
    init_mpp(codec, extradata, extradata_size);
}

MppDecodeNode::~MppDecodeNode() {
    destroy_mpp();
}

void MppDecodeNode::init_mpp(int32_t codec, const uint8_t *extradata, size_t extradata_size) {
    mpp::MppCodingType coding = mpp::MPP_VIDEO_CodingAVC;
    if (codec == Zhulong_CODEC_H264) {
        coding = mpp::MPP_VIDEO_CodingAVC;
    } else if (codec == Zhulong_CODEC_H265) {
        coding = mpp::MPP_VIDEO_CodingHEVC;
    } else {
        throw std::invalid_argument("Unsupported codec for MppDecodeNode: " + std::to_string(codec));
    }

    int ret = loader_.mpp_create(&ctx_, &mpi_);
    if (ret != mpp::MPP_OK || !ctx_ || !mpi_) {
        throw std::runtime_error("mpp_create failed with code " + std::to_string(ret));
    }

    // ⚠️ 开启内部 Parser Split Mode 与 Fast Mode，必须在 mpp_init 之前设置 (Need to setup before init)
    uint32_t split_mode = 1;
    if (mpi_->control) {
        mpi_->control(ctx_, mpp::MPP_DEC_SET_PARSER_SPLIT_MODE, &split_mode);
        uint32_t fast_mode = 1;
        mpi_->control(ctx_, mpp::MPP_DEC_SET_PARSER_FAST_MODE, &fast_mode);
    }

    ret = loader_.mpp_init(ctx_, mpp::MPP_CTX_DEC, coding);
    if (ret != mpp::MPP_OK) {
        destroy_mpp();
        throw std::runtime_error("mpp_init failed with code " + std::to_string(ret));
    }

    if (mpi_->control) {
        // 设置非阻塞超时 (与 IDecodeNode 的两段式非阻塞流式契约保持一致)
        int64_t timeout_ms = 0;
        mpi_->control(ctx_, mpp::MPP_SET_INPUT_TIMEOUT, &timeout_ms);
        mpi_->control(ctx_, mpp::MPP_SET_OUTPUT_TIMEOUT, &timeout_ms);

        // 开启即时输出与快速播放，使 IDR 关键帧能即时完成解码输出
        uint32_t fast_play = 1;
        mpi_->control(ctx_, mpp::MPP_DEC_SET_ENABLE_FAST_PLAY, &fast_play);
        uint32_t imm_out = 1;
        mpi_->control(ctx_, mpp::MPP_DEC_SET_IMMEDIATE_OUT, &imm_out);
    }

    // 若调用方提供了 extradata（SPS/PPS 参数集），作为初始化包预先送入
    if (extradata && extradata_size > 0 && mpi_->decode_put_packet) {
        mpp::MppPacket packet = nullptr;
        if (loader_.mpp_packet_init(&packet, const_cast<uint8_t*>(extradata), extradata_size) == mpp::MPP_OK) {
            mpi_->decode_put_packet(ctx_, packet);
            loader_.mpp_packet_deinit(&packet);
        }
    }

    initialized_ = true;
}

void MppDecodeNode::destroy_mpp() noexcept {
    if (ctx_ && loader_.mpp_destroy) {
        loader_.mpp_destroy(ctx_);
        ctx_ = nullptr;
        mpi_ = nullptr;
    }
    if (buf_group_ && loader_.mpp_buffer_group_put) {
        if (loader_.mpp_buffer_group_clear) {
            loader_.mpp_buffer_group_clear(buf_group_);
        }
        loader_.mpp_buffer_group_put(buf_group_);
        buf_group_ = nullptr;
    }
    initialized_ = false;
}

int MppDecodeNode::send_packet(const uint8_t *data, size_t size, int64_t pts, int64_t dts) {
    (void)dts;
    if (!initialized_ || !mpi_ || !mpi_->decode_put_packet) {
        return -1;
    }

    // 排空 (Drain) 信号：data 为 nullptr 或 size 为 0
    if (!data || size == 0) {
        if (eos_sent_) {
            return 0;
        }
        mpp::MppPacket packet = nullptr;
        int ret = loader_.mpp_packet_init(&packet, nullptr, 0);
        if (ret == mpp::MPP_OK && packet) {
            loader_.mpp_packet_set_eos(packet);
            mpi_->decode_put_packet(ctx_, packet);
            loader_.mpp_packet_deinit(&packet);
            eos_sent_ = true;
        }
        return 0;
    }

    eos_sent_ = false;
    mpp::MppPacket packet = nullptr;
    // 零拷贝借用入参缓冲区构造 MppPacket
    int ret = loader_.mpp_packet_init(&packet, const_cast<uint8_t*>(data), size);
    if (ret != mpp::MPP_OK || !packet) {
        return -1;
    }
    loader_.mpp_packet_set_pts(packet, pts);

    int put_ret = mpi_->decode_put_packet(ctx_, packet);
    loader_.mpp_packet_deinit(&packet);

    if (put_ret == mpp::MPP_OK) {
        return 0;
    }
    // ⚠️ 契约归一化：将硬件队列满或超时背压映射为 AVERROR(EAGAIN)，驱动调用方排空已解帧后重试，防止误判为损坏清空 DPB
    if (put_ret == mpp::MPP_ERR_BUFFER_FULL || put_ret == mpp::MPP_ERR_TIMEOUT) {
        return AVERROR(EAGAIN);
    }
    return put_ret;
}

int MppDecodeNode::receive_frame(HardwareFrame::Ptr &out_frame) {
    out_frame.reset();
    if (!initialized_ || !mpi_ || !mpi_->decode_get_frame) {
        return -1;
    }

    while (true) {
        mpp::MppFrame frame = nullptr;
        int ret = mpi_->decode_get_frame(ctx_, &frame);
        if (ret == mpp::MPP_ERR_TIMEOUT || (ret == 0 && !frame)) {
            return 1; // EAGAIN：暂无输出，需继续喂包
        }
        if (ret != mpp::MPP_OK || !frame) {
            return -1;
        }

        // 1. 处理 info_change 事件 (流参数更新或首次解析出 SPS/PPS)
        if (loader_.mpp_frame_get_info_change(frame)) {
            int info_ret = handle_info_change(frame);
            loader_.mpp_frame_deinit(&frame);
            if (info_ret != 0) {
                return info_ret;
            }
            // info_change 帧不携带图像数据，继续拉取真正的画面帧
            continue;
        }

        // 2. 坏帧与丢弃帧过滤
        uint32_t err = loader_.mpp_frame_get_errinfo(frame);
        uint32_t discard = loader_.mpp_frame_get_discard(frame);
        if (err != 0 || discard != 0) {
            loader_.mpp_frame_deinit(&frame);
            continue;
        }

        // 3. 提取底层物理 MppBuffer 与内核 DMA-BUF 文件描述符
        mpp::MppBuffer mpp_buf = loader_.mpp_frame_get_buffer(frame);
        if (!mpp_buf) {
            loader_.mpp_frame_deinit(&frame);
            continue;
        }

        int raw_fd = loader_.buffer_get_fd(mpp_buf);
        if (raw_fd < 0) {
            loader_.mpp_frame_deinit(&frame);
            return -1;
        }

        // dup 独立文件描述符以与 HardwareFrame 生命周期解耦
        int owned_fd = ::dup(raw_fd);
        if (owned_fd < 0) {
            loader_.mpp_frame_deinit(&frame);
            return -1;
        }

        // 递增底层 MppBuffer 引用计数，保证跨线程流转期间显存不被复用
        loader_.buffer_inc_ref(mpp_buf);

        // RAII 保护守卫：防止 make_shared 抛出 std::bad_alloc 或后续装配异常导致 owned_fd 与 buffer 引用计数泄露
        struct ScopeGuard {
            int fd{-1};
            mpp::MppBuffer buf{nullptr};
            mpp::MppLoader &ldr;
            bool active{true};
            ~ScopeGuard() {
                if (active) {
                    if (fd >= 0) {
                        ::close(fd);
                    }
                    if (buf) {
                        ldr.buffer_put(buf);
                    }
                }
            }
        } guard{owned_fd, mpp_buf, loader_, true};

        // 4. 组装硬件帧
        out_frame = std::make_shared<HardwareFrame>();
        out_frame->width = loader_.mpp_frame_get_width(frame);
        out_frame->height = loader_.mpp_frame_get_height(frame);
        out_frame->stride = loader_.mpp_frame_get_hor_stride(frame);
        out_frame->vstride = loader_.mpp_frame_get_ver_stride(frame);
        out_frame->format = FRAME_FORMAT_NV12;
        out_frame->dma_fd = owned_fd;

        // ⚠️ 时间戳保真透传：严格遵循 (raw_pts != AV_NOPTS_VALUE) 契约判定有效性
        const int64_t raw_pts = loader_.mpp_frame_get_pts(frame);
        out_frame->has_pts = (raw_pts != AV_NOPTS_VALUE);
        out_frame->pts = out_frame->has_pts ? raw_pts : 0;
        out_frame->time_base_num = time_base_num_;
        out_frame->time_base_den = time_base_den_;

        // RAII 回收闭包：当 HardwareFrame 最后一个智能指针析构时安全归还显存并关闭 fd
        auto &loader_ref = loader_;
        out_frame->release_fn = [&loader_ref, mpp_buf, owned_fd](HardwareFrame*) {
            if (owned_fd >= 0) {
                ::close(owned_fd);
            }
            loader_ref.buffer_put(mpp_buf);
        };

        // 成功交接所有权给 HardwareFrame，解除栈上守卫
        guard.active = false;

        // 立即归还外层 MppFrame 容器，防止解码器帧池耗尽
        loader_.mpp_frame_deinit(&frame);
        return 0;
    }
}

int MppDecodeNode::handle_info_change(mpp::MppFrame frame) {
    uint32_t w = loader_.mpp_frame_get_width(frame);
    uint32_t h = loader_.mpp_frame_get_height(frame);
    uint32_t hor_s = loader_.mpp_frame_get_hor_stride(frame);
    uint32_t ver_s = loader_.mpp_frame_get_ver_stride(frame);
    size_t raw_buf_size = loader_.mpp_frame_get_buf_size(frame);

    width_ = w;
    height_ = h;
    hor_stride_ = (hor_s > 0) ? hor_s : ((w + 15) & ~15);
    ver_stride_ = (ver_s > 0) ? ver_s : ((h + 15) & ~15);

    size_t buf_size = (raw_buf_size > 0) ? raw_buf_size : (static_cast<size_t>(hor_stride_) * ver_stride_ * 3 / 2);

    // 根据分辨率计算自适应缓冲池容量 (720P: 10, 1080P: 16, 4K: 20)
    int count = 16;
    uint64_t pixels = static_cast<uint64_t>(w) * h;
    if (pixels <= 1280 * 720) {
        count = 10;
    } else if (pixels <= 1920 * 1080) {
        count = 16;
    } else {
        count = 20;
    }

    if (buf_group_) {
        if (loader_.mpp_buffer_group_clear) {
            loader_.mpp_buffer_group_clear(buf_group_);
        }
        loader_.mpp_buffer_group_put(buf_group_);
        buf_group_ = nullptr;
    }

    // 依次探测 DMA_HEAP -> DRM -> ION 分配器
    static const mpp::MppBufferType kTypes[] = {
        mpp::MPP_BUFFER_TYPE_DMA_HEAP,
        mpp::MPP_BUFFER_TYPE_DRM,
        mpp::MPP_BUFFER_TYPE_ION
    };

    int ret = mpp::MPP_NOK;
    for (auto type : kTypes) {
        ret = loader_.mpp_buffer_group_get(&buf_group_, type, mpp::MPP_BUFFER_INTERNAL, "ZhulongBufGrp", "info_change");
        if (ret == mpp::MPP_OK && buf_group_) {
            break;
        }
    }

    if (ret != mpp::MPP_OK || !buf_group_) {
        return -1;
    }

    loader_.mpp_buffer_group_limit_config(buf_group_, buf_size, count);

    // 绑定外部缓冲池并通知解除挂起
    if (mpi_->control) {
        mpi_->control(ctx_, mpp::MPP_DEC_SET_EXT_BUF_GROUP, buf_group_);
        mpi_->control(ctx_, mpp::MPP_DEC_SET_INFO_CHANGE_READY, nullptr);
        uint32_t fast_play = 1;
        mpi_->control(ctx_, mpp::MPP_DEC_SET_ENABLE_FAST_PLAY, &fast_play);
    }
    return 0;
}

void MppDecodeNode::flush() {
    if (initialized_ && mpi_ && mpi_->reset) {
        mpi_->reset(ctx_);
        eos_sent_ = false;
    }
}

} // namespace zhulong
