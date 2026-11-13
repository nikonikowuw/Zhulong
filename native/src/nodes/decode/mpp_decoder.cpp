/**
 * @file mpp_decoder.cpp
 * @brief Rockchip MPP 硬件解码节点实现
 *
 * 关键时序（详见 .trellis/spec/native/hardware-backends.md §5）：
 * 1. Parser Split / Fast Mode 必须在 `mpp_init` 之前下发，否则单包输入无法识别 Access Unit 边界；
 * 2. `mpp_buffer_inc_ref` 之后立即 `mpp_frame_deinit` 外层 MppFrame，避免占满 MPP 固定槽位导致 VPU 死锁；
 * 3. 帧的归还（`mpp_buffer_put` + `close(dma_fd)`）绑定在 `HardwareFrame::release_fn` 上；
 * 4. 外部缓冲组的销毁时机由最后一张引用它的帧决定（见 `BufferGroup`），绝不在解码器析构或分辨率
 *    切换时释放仍有在途帧的显存。
 */

#include "nodes/decode/mpp_decoder.hpp"
#include "common/log.hpp"

extern "C" {
#include <libavutil/avutil.h>
#include <libavutil/error.h>
}

#include <cerrno>
#include <climits>
#include <stdexcept>
#include <string>
#include <unistd.h>

namespace zhulong {

namespace {

constexpr const char *log_module = "nodes.decode";

/** @brief 外部缓冲组分配器探测顺序：DMA_HEAP -> DRM -> ION */
constexpr mpp::MppBufferType buffer_types[] = {
    mpp::MPP_BUFFER_TYPE_DMA_HEAP,
    mpp::MPP_BUFFER_TYPE_DRM,
    mpp::MPP_BUFFER_TYPE_ION,
};

/** @brief 缓冲池容量分级（720P 及以下 / 1080P 及以下 / 4K） */
constexpr int32_t buffer_count_sd = 10;
constexpr int32_t buffer_count_hd = 16;
constexpr int32_t buffer_count_uhd = 20;
constexpr uint64_t sd_pixel_limit = 1280ULL * 720ULL;
constexpr uint64_t hd_pixel_limit = 1920ULL * 1080ULL;

/** @brief 硬件步长对齐粒度：水平 16 字节、垂直 16 行 */
constexpr uint32_t stride_align = 16;

int32_t buffer_count_for(uint32_t width, uint32_t height) {
    const uint64_t pixels = static_cast<uint64_t>(width) * height;
    if (pixels <= sd_pixel_limit) return buffer_count_sd;
    if (pixels <= hd_pixel_limit) return buffer_count_hd;
    return buffer_count_uhd;
}

uint32_t align_up(uint32_t value, uint32_t alignment) {
    return alignment > 0 ? ((value + alignment - 1) / alignment) * alignment : value;
}

} // namespace

bool MppDecodeNode::is_supported() noexcept {
    return mpp::MppLoader::instance().is_supported();
}

MppDecodeNode::BufferGroup::~BufferGroup() {
    if (!raw || !loader) return;

    const auto &api = loader->api();
    if (api.buffer_group_clear && api.buffer_group_clear(raw) != mpp::MPP_OK) {
        ZL_LOGW(stream_id, log_module, "mpp_buffer_group_clear_failed");
    }
    if (api.buffer_group_put && api.buffer_group_put(raw) != mpp::MPP_OK) {
        ZL_LOGW(stream_id, log_module, "mpp_buffer_group_put_failed");
    }
    raw = nullptr;
}

MppDecodeNode::MppDecodeNode(int32_t codec, const uint8_t *extradata, size_t extradata_size,
                             int32_t time_base_num, int32_t time_base_den, uint64_t stream_id)
    : loader_(mpp::MppLoader::instance()),
      stream_id_(stream_id),
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
    try {
        setup_mpp(codec, extradata, extradata_size);
    } catch (...) {
        // ⚠️ 构造函数抛出异常时本对象的析构函数不会运行，必须在此回收半初始化的 MPP 上下文与缓冲组
        destroy_mpp();
        throw;
    }
}

void MppDecodeNode::setup_mpp(int32_t codec, const uint8_t *extradata, size_t extradata_size) {
    mpp::MppCodingType coding = mpp::MPP_VIDEO_CodingAVC;
    if (codec == Zhulong_CODEC_H264) {
        coding = mpp::MPP_VIDEO_CodingAVC;
    } else if (codec == Zhulong_CODEC_H265) {
        coding = mpp::MPP_VIDEO_CodingHEVC;
    } else {
        throw std::invalid_argument("Unsupported codec for MppDecodeNode: " + std::to_string(codec));
    }

    const auto &api = loader_.api();
    int ret = api.create ? api.create(&ctx_, &mpi_) : mpp::MPP_NOK;
    if (ret != mpp::MPP_OK || !ctx_ || !mpi_) {
        throw std::runtime_error("mpp_create failed with code " + std::to_string(ret));
    }

    // ⚠️ Parser 命令字必须在 mpp_init 之前设置完成，否则单包输入无法识别 AU 边界并造成首帧滞留
    uint32_t split_mode = 1;
    if (control(mpp::MPP_DEC_SET_PARSER_SPLIT_MODE, &split_mode, "set_parser_split_mode") != decode_ok) {
        throw std::runtime_error("mpp parser split mode setup failed");
    }
    // 快速解析仅为性能开关：失败已由 control() 记录告警，不阻断初始化
    uint32_t fast_mode = 1;
    (void)control(mpp::MPP_DEC_SET_PARSER_FAST_MODE, &fast_mode, "set_parser_fast_mode");

    ret = api.init ? api.init(ctx_, mpp::MPP_CTX_DEC, coding) : mpp::MPP_NOK;
    if (ret != mpp::MPP_OK) {
        throw std::runtime_error("mpp_init failed with code " + std::to_string(ret));
    }

    // 非阻塞超时（0ms）：与 IDecodeNode 的两段式非阻塞流式契约保持一致
    int64_t timeout_ms = 0;
    (void)control(mpp::MPP_SET_INPUT_TIMEOUT, &timeout_ms, "set_input_timeout");
    (void)control(mpp::MPP_SET_OUTPUT_TIMEOUT, &timeout_ms, "set_output_timeout");

    // 即时输出与快速播放：使 IDR 关键帧无需积攒后续 P 帧即可立即解算输出
    uint32_t fast_play = 1;
    (void)control(mpp::MPP_DEC_SET_ENABLE_FAST_PLAY, &fast_play, "set_fast_play");
    uint32_t imm_out = 1;
    (void)control(mpp::MPP_DEC_SET_IMMEDIATE_OUT, &imm_out, "set_immediate_out");

    // 若调用方提供了 extradata（SPS/PPS 参数集），作为初始化包预先送入
    if (extradata && extradata_size > 0) {
        if (extradata_size > static_cast<size_t>(INT_MAX)) {
            throw std::invalid_argument("Extradata size exceeds INT_MAX");
        }
        mpp::MppPacket packet = nullptr;
        if (!api.packet_init || !api.packet_deinit ||
            api.packet_init(&packet, const_cast<uint8_t *>(extradata), extradata_size) != mpp::MPP_OK ||
            !packet) {
            throw std::runtime_error("mpp_packet_init for extradata failed");
        }
        const int put_ret = mpi_->decode_put_packet ? mpi_->decode_put_packet(ctx_, packet) : mpp::MPP_NOK;
        api.packet_deinit(&packet);
        if (put_ret != mpp::MPP_OK) {
            // ⚠️ 不因参数集入队失败而 fail closed：Annex-B 码流的首个 IDR 自带 SPS/PPS，
            //    MPP 的切分器（PARSER_SPLIT_MODE）会从码流里重新取到参数集。但两种失败的
            //    严重性必须区分开，否则「硬解整条流解不出图」会被一条 WARN 淹没：
            //      - 背压/超时：初始化阶段队列异常，属于可恢复情形，WARN；
            //      - 其他错误：包格式或上下文状态不可用，ERROR（调用方据此可判后端异常）。
            if (put_ret == mpp::MPP_ERR_BUFFER_FULL || put_ret == mpp::MPP_ERR_TIMEOUT) {
                ZL_LOGW(stream_id_, log_module, "mpp_extradata_put_backpressure ret=%d", put_ret);
            } else {
                ZL_LOGE(stream_id_, log_module, "mpp_extradata_put_failed ret=%d", put_ret);
            }
        }
    }

    initialized_ = true;
}

void MppDecodeNode::destroy_mpp() noexcept {
    if (ctx_ && loader_.api().destroy) {
        const int ret = loader_.api().destroy(ctx_);
        if (ret != mpp::MPP_OK) {
            ZL_LOGW(stream_id_, log_module, "mpp_destroy_failed ret=%d", ret);
        }
    }
    ctx_ = nullptr;
    mpi_ = nullptr;
    initialized_ = false;
    eos_sent_ = false;
    // ⚠️ 只解除本地强引用：仍在途的 HardwareFrame 持有同一缓冲组，最后一帧释放时才真正销毁
    buf_group_.reset();
}

int MppDecodeNode::control(int32_t cmd, void *param, const char *what) noexcept {
    if (!mpi_ || !mpi_->control) {
        ZL_LOGE(stream_id_, log_module, "mpp_control_unavailable what=%s", what);
        return decode_backend_failure;
    }
    const int ret = mpi_->control(ctx_, cmd, param);
    if (ret != mpp::MPP_OK) {
        ZL_LOGW(stream_id_, log_module, "mpp_control_failed what=%s cmd=0x%08x ret=%d", what,
                static_cast<unsigned>(cmd), ret);
        return decode_backend_failure;
    }
    return decode_ok;
}

int MppDecodeNode::configure_buffer_group(mpp::MppFrame frame) {
    const auto &api = loader_.api();

    const uint32_t width = api.frame_get_width(frame);
    const uint32_t height = api.frame_get_height(frame);
    if (width == 0 || height == 0) {
        ZL_LOGE(stream_id_, log_module, "mpp_info_change_invalid_dimensions width=%u height=%u", width, height);
        return decode_backend_failure;
    }
    const uint32_t hor_stride_in = api.frame_get_hor_stride(frame);
    const uint32_t ver_stride_in = api.frame_get_ver_stride(frame);
    const size_t raw_buf_size = api.frame_get_buf_size ? api.frame_get_buf_size(frame) : 0;

    const uint32_t hor_stride = hor_stride_in >= width ? hor_stride_in : align_up(width, stride_align);
    const uint32_t ver_stride = ver_stride_in >= height ? ver_stride_in : align_up(height, stride_align);
    const size_t buf_size =
        raw_buf_size > 0 ? raw_buf_size : static_cast<size_t>(hor_stride) * ver_stride * 3 / 2;
    const int32_t count = buffer_count_for(width, height);

    // 先创建持有者、再由持有者接收 MppBufferGroup：make_shared 抛出 bad_alloc 时尚未分配任何组，
    // 异常路径零泄漏（无需额外的栈上临时守卫）
    auto holder = std::make_shared<BufferGroup>();
    holder->loader = &loader_;
    holder->stream_id = stream_id_;

    int ret = mpp::MPP_NOK;
    for (const auto type : buffer_types) {
        holder->raw = nullptr;
        ret = api.buffer_group_get(&holder->raw, type, mpp::MPP_BUFFER_INTERNAL, "ZhulongBufGrp", "info_change");
        if (ret == mpp::MPP_OK && holder->raw) {
            break;
        }
    }
    if (ret != mpp::MPP_OK || !holder->raw) {
        ZL_LOGE(stream_id_, log_module, "mpp_buffer_group_get_failed ret=%d", ret);
        return decode_backend_failure;
    }

    if (api.buffer_group_limit_config && api.buffer_group_limit_config(holder->raw, buf_size, count) != mpp::MPP_OK) {
        ZL_LOGW(stream_id_, log_module, "mpp_buffer_group_limit_failed size=%zu count=%d", buf_size, count);
    }

    // 绑定外部缓冲池并通知解除挂起
    if (control(mpp::MPP_DEC_SET_EXT_BUF_GROUP, holder->raw, "set_ext_buf_group") != decode_ok) {
        return decode_backend_failure;
    }
    // ⚠️ ctx_ 已引用该外部缓冲组：即使后续 info_change_ready 失败，也不能让 holder 在此提前销毁，
    //    必须由 buf_group_ 保活直至 destroy_mpp() 销毁 ctx_ 之后。
    //    旧组不在此销毁：仍持有它的在途帧会在释放时把引用计数降为 0。
    buf_group_ = std::move(holder);
    if (control(mpp::MPP_DEC_SET_INFO_CHANGE_READY, nullptr, "info_change_ready") != decode_ok) {
        return decode_backend_failure;
    }

    ZL_LOGI(stream_id_, log_module, "mpp_info_change width=%u height=%u stride=%u vstride=%u buffers=%d", width,
            height, hor_stride, ver_stride, count);

    return decode_ok;
}

int MppDecodeNode::send_packet(const uint8_t *data, size_t size, int64_t pts, int64_t dts) {
    (void)dts; // MPP 硬解只透传显示时间戳，无独立解码时间戳语义
    if (!initialized_ || !mpi_ || !mpi_->decode_put_packet) {
        return decode_backend_failure;
    }
    // ⚠️ 排空状态契约：若已发送 EOS，继续送包无效，必须先 flush() 复位 draining 状态（与软解行为对齐）
    if (eos_sent_) {
        return AVERROR_EOF;
    }
    const auto &api = loader_.api();

    // 排空 (Drain) 信号：data 为 nullptr 或 size 为 0
    if (!data || size == 0) {
        mpp::MppPacket packet = nullptr;
        if (!api.packet_init || !api.packet_deinit || api.packet_init(&packet, nullptr, 0) != mpp::MPP_OK ||
            !packet) {
            ZL_LOGE(stream_id_, log_module, "mpp_eos_packet_init_failed");
            return decode_backend_failure;
        }
        if (api.packet_set_eos) {
            api.packet_set_eos(packet);
        }
        const int put_ret = mpi_->decode_put_packet(ctx_, packet);
        api.packet_deinit(&packet);
        // ⚠️ EOS 未真正入队时不得标记 eos_sent_，调用方排空后可重试
        if (put_ret == mpp::MPP_ERR_BUFFER_FULL || put_ret == mpp::MPP_ERR_TIMEOUT) {
            return decode_again;
        }
        if (put_ret != mpp::MPP_OK) {
            ZL_LOGW(stream_id_, log_module, "mpp_eos_put_failed ret=%d", put_ret);
            return decode_backend_failure;
        }
        eos_sent_ = true;
        return decode_ok;
    }

    if (size > static_cast<size_t>(INT_MAX)) {
        ZL_LOGE(stream_id_, log_module, "mpp_packet_too_large size=%zu", size);
        return AVERROR(EINVAL);
    }

    mpp::MppPacket packet = nullptr;
    // 借用入参缓冲区构造 MppPacket：仅在 decode_put_packet 同步调用期间有效，返回后立即 deinit
    if (!api.packet_init || !api.packet_deinit ||
        api.packet_init(&packet, const_cast<uint8_t *>(data), size) != mpp::MPP_OK || !packet) {
        ZL_LOGE(stream_id_, log_module, "mpp_packet_init_failed size=%zu", size);
        return decode_backend_failure;
    }
    if (api.packet_set_pts) {
        api.packet_set_pts(packet, pts);
    }

    const int put_ret = mpi_->decode_put_packet(ctx_, packet);
    api.packet_deinit(&packet);

    if (put_ret == mpp::MPP_OK) {
        return decode_ok;
    }
    // ⚠️ 契约归一化：硬件队列满或超时属于背压，映射为 decode_again，避免上层误判为损坏而清空 DPB
    if (put_ret == mpp::MPP_ERR_BUFFER_FULL || put_ret == mpp::MPP_ERR_TIMEOUT) {
        return decode_again;
    }
    ZL_LOGW(stream_id_, log_module, "mpp_put_packet_failed ret=%d", put_ret);
    return decode_backend_failure;
}

int MppDecodeNode::receive_frame(HardwareFrame::Ptr &out_frame) {
    out_frame.reset();
    if (!initialized_ || !mpi_ || !mpi_->decode_get_frame) {
        return decode_backend_failure;
    }
    const auto &api = loader_.api();
    mpp::MppLoader *const loader_ptr = &loader_;

    while (true) {
        mpp::MppFrame frame = nullptr;
        const int ret = mpi_->decode_get_frame(ctx_, &frame);
        if (ret == mpp::MPP_ERR_TIMEOUT || (ret == mpp::MPP_OK && !frame)) {
            return decode_again; // 暂无输出，需继续喂包
        }
        if (ret != mpp::MPP_OK || !frame) {
            // EOS 之后的终止态不是故障：排空已完成，调用方须先 flush() 才可继续喂包
            if (eos_sent_) {
                return decode_again;
            }
            ZL_LOGW(stream_id_, log_module, "mpp_decode_get_frame_failed ret=%d", ret);
            return decode_backend_failure;
        }

        // RAII 保护守卫：无论通过 continue 还是 return 离开当前轮次，均自动归还 MppFrame 容器。
        // ⚠️ 必须在取出 MppBuffer 并 inc_ref 后立即释放，否则会耗尽 MPP 固定槽位导致 VPU 死锁。
        struct FrameGuard {
            mpp::MppFrame frame{nullptr};
            mpp::MppLoader *loader{nullptr};
            ~FrameGuard() {
                if (frame && loader && loader->api().frame_deinit) {
                    loader->api().frame_deinit(&frame);
                }
            }
        } frame_guard{frame, loader_ptr};

        // 0. 处理 EOS 标记（排空完成信号）
        if (api.frame_get_eos && api.frame_get_eos(frame)) {
            // EOS 帧若不携带物理缓冲，说明排空已彻底完成，直接返回 decode_again
            if (!api.frame_get_buffer || !api.frame_get_buffer(frame)) {
                return decode_again;
            }
        }

        // 1. 处理 info_change 事件 (流参数更新或首次解析出 SPS/PPS)
        if (api.frame_get_info_change(frame)) {
            const int info_ret = configure_buffer_group(frame);
            if (info_ret != decode_ok) {
                return info_ret;
            }
            // info_change 帧不携带图像数据，继续拉取真正的画面帧
            continue;
        }

        // 2. 坏帧与丢弃帧过滤
        if (api.frame_get_errinfo(frame) != 0 || api.frame_get_discard(frame) != 0) {
            continue;
        }

        // 3. 输出格式校验：MPP 可能输出 10bit 半平面（MPP_FMT_YUV420SP_10BIT）或其他格式，
        //    ⚠️ 严禁一律硬编码为 NV12，否则下游 RGA/RKNN 会按 8bit 解析造成静默错帧
        const mpp::MppFrameFormat format =
            api.frame_get_fmt ? api.frame_get_fmt(frame) : mpp::MPP_FMT_YUV420SP;
        if (format != mpp::MPP_FMT_YUV420SP) {
            if (!unsupported_format_logged_) {
                unsupported_format_logged_ = true;
                ZL_LOGW(stream_id_, log_module, "mpp_unsupported_frame_format fmt=%u",
                        static_cast<unsigned>(format));
            }
            continue;
        }

        // 3.5 几何尺寸校验：丢弃宽高为 0 的异常帧，步长缺失时按 16 对齐补齐（与 configure_buffer_group 口径一致）
        const uint32_t width = api.frame_get_width(frame);
        const uint32_t height = api.frame_get_height(frame);
        if (width == 0 || height == 0) {
            continue;
        }
        const uint32_t hor_stride_in = api.frame_get_hor_stride(frame);
        const uint32_t ver_stride_in = api.frame_get_ver_stride(frame);
        const uint32_t hor_stride = hor_stride_in >= width ? hor_stride_in : align_up(width, stride_align);
        const uint32_t ver_stride = ver_stride_in >= height ? ver_stride_in : align_up(height, stride_align);

        // 4. 提取底层物理 MppBuffer 与内核 DMA-BUF 文件描述符
        mpp::MppBuffer mpp_buf = api.frame_get_buffer(frame);
        if (!mpp_buf) {
            continue;
        }

        const int raw_fd = loader_.fd(mpp_buf);
        if (raw_fd < 0) {
            ZL_LOGE(stream_id_, log_module, "mpp_buffer_get_fd_failed");
            return decode_backend_failure;
        }

        // dup 独立文件描述符以与 HardwareFrame 生命周期解耦
        const int owned_fd = ::dup(raw_fd);
        if (owned_fd < 0) {
            ZL_LOGE(stream_id_, log_module, "mpp_dma_fd_dup_failed errno=%d", errno);
            return decode_backend_failure;
        }

        // ⚠️ 引用计数必须校验：加引用失败时立刻回滚 fd，绝不进入装配流程；
        //    否则 release_fn 中的 mpp_buffer_put 会造成净减引用并提前回收显存
        if (loader_.inc_ref(mpp_buf) != mpp::MPP_OK) {
            ::close(owned_fd);
            ZL_LOGE(stream_id_, log_module, "mpp_buffer_inc_ref_failed");
            return decode_backend_failure;
        }

        // RAII 保护守卫：防止 make_shared 抛出 std::bad_alloc 或装配期间异常导致 fd 与引用计数泄漏
        struct ScopeGuard {
            int fd{-1};
            mpp::MppBuffer buffer{nullptr};
            mpp::MppLoader *loader{nullptr};
            bool active{true};
            ~ScopeGuard() {
                if (!active) return;
                if (fd >= 0) {
                    ::close(fd);
                }
                if (buffer && loader) {
                    loader->put(buffer);
                }
            }
        } guard{owned_fd, mpp_buf, loader_ptr, true};

        // 5. 组装硬件帧。
        // ⚠️ 先在局部装配、最后一步才写入 out_frame：任何一步抛出（如 std::function 分配失败）
        // 都不会让调用方看到半成品输出，契约「失败时 out_frame 被置空」始终成立。
        auto frame_obj = std::make_shared<HardwareFrame>();
        frame_obj->width = width;
        frame_obj->height = height;
        frame_obj->stride = hor_stride;
        frame_obj->vstride = ver_stride;
        frame_obj->format = FRAME_FORMAT_NV12;
        frame_obj->dma_fd = owned_fd;

        // ⚠️ 时间戳保真透传：严格遵循 (raw_pts != AV_NOPTS_VALUE) 契约判定有效性
        const int64_t raw_pts = api.frame_get_pts ? api.frame_get_pts(frame) : AV_NOPTS_VALUE;
        frame_obj->has_pts = (raw_pts != AV_NOPTS_VALUE);
        frame_obj->pts = frame_obj->has_pts ? raw_pts : 0;
        frame_obj->time_base_num = time_base_num_;
        frame_obj->time_base_den = time_base_den_;

        // RAII 回收闭包：当 HardwareFrame 最后一个智能指针析构时安全归还显存并关闭 fd。
        // ⚠️ group_keepalive 是必须的：它保证本次帧所属的缓冲组存活到本帧归还之后。
        const std::shared_ptr<BufferGroup> group_keepalive = buf_group_;
        frame_obj->release_fn = [loader_ptr, mpp_buf, owned_fd, group_keepalive](HardwareFrame *) {
            if (owned_fd >= 0) {
                ::close(owned_fd);
            }
            loader_ptr->put(mpp_buf);
            (void)group_keepalive;
        };

        // 成功交接所有权给 HardwareFrame，解除栈上守卫
        guard.active = false;
        out_frame = std::move(frame_obj);
        return decode_ok;
    }
}

void MppDecodeNode::flush() {
    if (!initialized_ || !mpi_ || !mpi_->reset) {
        return;
    }
    if (mpi_->reset(ctx_) != mpp::MPP_OK) {
        ZL_LOGW(stream_id_, log_module, "mpp_reset_failed");
    }
    eos_sent_ = false;
}

} // namespace zhulong
