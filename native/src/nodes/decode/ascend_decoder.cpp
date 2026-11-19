/**
 * @file ascend_decoder.cpp
 * @brief 昇腾 VDEC 解码节点实现（不依赖 CANN 头文件）
 *
 * 帧装配契约（与 `mpp_decoder.cpp` 对齐）：
 * 1. 设备帧只是「借出」，回拷完成后立即归还通道，`HardwareFrame` 只持有主机内存；
 * 2. `release_fn` 必须自带池的 keepalive（`shared_ptr`），否则节点先于在途帧析构时会悬垂；
 * 3. 任何一步失败都要先归还设备帧，绝不让 VDEC 的 frame buffer 泄漏（那是通道级不可恢复错误）。
 */

#include "nodes/decode/ascend_decoder.hpp"
#include "common/log.hpp"

extern "C" {
#include <libavutil/avutil.h>
#include <libavutil/error.h>
}

#include <algorithm>
#include <cerrno>
#include <mutex>
#include <stdexcept>
#include <string>
#include <utility>
#include <vector>

namespace zhulong {

namespace {

constexpr const char *log_module = "nodes.decode";

/** @brief 宽 stride 对齐粒度（DVPP 要求 16 字节） */
constexpr uint32_t stride_align_x = 16;
/** @brief 高 stride 对齐粒度（YUV420SP 只需 2 行对齐） */
constexpr uint32_t stride_align_y = 2;
/** @brief DVPP 要求的最小宽 stride */
constexpr uint32_t min_width_stride = 32;

uint32_t align_up(uint32_t value, uint32_t alignment) noexcept {
    return alignment > 0 ? ((value + alignment - 1) / alignment) * alignment : value;
}

} // namespace

namespace ascend {

VdecGeometry compute_vdec_geometry(uint32_t width, uint32_t height) noexcept {
    VdecGeometry geometry;
    if (width == 0 || height == 0) {
        return geometry;
    }
    const uint32_t width_stride = align_up(std::max(width, min_width_stride), stride_align_x);
    const uint32_t height_stride = align_up(height, stride_align_y);
    geometry.width = width;
    geometry.height = height;
    geometry.width_stride = width_stride;
    geometry.height_stride = height_stride;
    geometry.buffer_size = static_cast<size_t>(width_stride) * height_stride * 3 / 2;
    return geometry;
}

} // namespace ascend

/**
 * @brief 主机 NV12 帧池
 *
 * 用裸 `new[]` 而不是 `std::vector`：缓冲区地址在 `HardwareFrame::host_ptr` 里裸传，
 * 池的存活期由 `release_fn` 里的 `shared_ptr` 决定，与节点生命周期解耦。
 */
struct AscendDecodeNode::HostBufferPool {
    struct PooledBuffer {
        uint8_t *ptr{nullptr};
        size_t capacity{0};
    };

    static constexpr size_t max_pooled_buffers = 16;

    std::mutex mutex;
    std::vector<PooledBuffer> free_buffers;

    ~HostBufferPool() {
        for (const auto &buffer : free_buffers) {
            delete[] buffer.ptr;
        }
    }

    [[nodiscard]] uint8_t *acquire(size_t size) {
        {
            std::lock_guard<std::mutex> lock(mutex);
            while (!free_buffers.empty()) {
                auto buffer = free_buffers.back();
                free_buffers.pop_back();
                if (buffer.capacity >= size) {
                    return buffer.ptr;
                }
                delete[] buffer.ptr;
            }
        }
        return new (std::nothrow) uint8_t[size];
    }

    void release(uint8_t *buffer, size_t capacity) noexcept {
        if (buffer == nullptr) {
            return;
        }
        std::lock_guard<std::mutex> lock(mutex);
        if (free_buffers.size() >= max_pooled_buffers) {
            delete[] buffer;
            return;
        }
        free_buffers.push_back({buffer, capacity});
    }
};

bool AscendDecodeNode::is_supported() noexcept { return ascend::ascend_vdec_available(); }

AscendDecodeNode::AscendDecodeNode(int32_t codec, const uint8_t *extradata, size_t extradata_size,
                                   int32_t time_base_num, int32_t time_base_den, uint64_t stream_id,
                                   int32_t width, int32_t height)
    : pool_(std::make_shared<HostBufferPool>()), stream_id_(stream_id), time_base_num_(time_base_num),
      time_base_den_(time_base_den) {
    if (codec != Zhulong_CODEC_H264 && codec != Zhulong_CODEC_H265) {
        throw std::invalid_argument("ascend vdec: unsupported codec " + std::to_string(codec));
    }
    if (width <= 0 || height <= 0) {
        // VDEC 通道的 pic_width/pic_height 在创建时固定，事后无法调整：
        // 拿不到编码宽高就宁可 fail closed，也不要建一个注定解错尺寸的通道。
        throw std::invalid_argument("ascend vdec: coded width/height required, got " + std::to_string(width) + "x" +
                                    std::to_string(height));
    }

    ascend::VdecChannelConfig config;
    config.codec = codec == Zhulong_CODEC_H265 ? ascend::VdecCodec::h265 : ascend::VdecCodec::h264;
    config.width = static_cast<uint32_t>(width);
    config.height = static_cast<uint32_t>(height);
    config.stream_id = stream_id;

    // extradata（SPS/PPS）不需要单独下发：RTSP 解复用产出的是 Annex-B，参数集随 IDR 一起到达。
    // 但 AVCC/HVCC 封装（MP4 等）的参数集不在码流里，VDEC 会解不出画面且不报错——
    // 这里至少留下可检索的告警，避免「黑屏但一切正常」的排查黑洞。
    if (extradata != nullptr && extradata_size >= 4 && extradata[0] == 1) {
        ZL_LOGW(stream_id_, log_module, "ascend_vdec_extradata_avcc_ignored size=%zu", extradata_size);
    }

    channel_ = ascend::create_vdec_channel(config);
    if (!channel_) {
        throw std::runtime_error("ascend vdec: channel unavailable");
    }
    ZL_LOGI(stream_id_, log_module, "ascend_vdec_channel_ready codec=%d width=%d height=%d", codec, width, height);
}

AscendDecodeNode::AscendDecodeNode(ascend::IVdecChannel::Ptr channel, uint64_t stream_id, int32_t time_base_num,
                                   int32_t time_base_den)
    : pool_(std::make_shared<HostBufferPool>()), channel_(std::move(channel)), stream_id_(stream_id),
      time_base_num_(time_base_num), time_base_den_(time_base_den) {}

AscendDecodeNode::~AscendDecodeNode() = default;

int AscendDecodeNode::send_packet(const uint8_t *data, size_t size, int64_t pts, int64_t dts) {
    if (!channel_) {
        return decode_backend_failure;
    }
    // EOS 之后继续送包必须失败：契约要求调用方先 flush() 复位 draining 状态。
    if (eos_sent_) {
        return AVERROR_EOF;
    }

    if (data == nullptr || size == 0) {
        const int eos_result = channel_->send_eos();
        if (eos_result == decode_again) {
            return decode_again;
        }
        if (eos_result != decode_ok) {
            ZL_LOGE(stream_id_, log_module, "ascend_vdec_eos_failed");
            return decode_backend_failure;
        }
        eos_sent_ = true;
        return decode_ok;
    }

    const int result = channel_->send(data, size, pts, dts);
    if (result == decode_again) {
        return decode_again;
    }
    if (result != decode_ok) {
        // 日志纪律：稳定 token + 数值上下文，不回写后端错误文本
        ZL_LOGE(stream_id_, log_module, "ascend_vdec_send_failed size=%zu", size);
        return decode_backend_failure;
    }
    return decode_ok;
}

int AscendDecodeNode::receive_frame(HardwareFrame::Ptr &out_frame) {
    out_frame.reset();
    if (!channel_) {
        return decode_backend_failure;
    }

    ascend::DecodedFrame decoded;
    const int result = channel_->receive(decoded);
    if (result == decode_again) {
        return decode_again;
    }
    if (result != decode_ok) {
        ZL_LOGE(stream_id_, log_module, "ascend_vdec_receive_failed");
        return decode_backend_failure;
    }
    if (decoded.dev_ptr == nullptr) {
        // 通道契约违反（decode_ok 必须带有效帧），按不可恢复错误处理，避免上层死循环。
        ZL_LOGE(stream_id_, log_module, "ascend_vdec_empty_frame");
        return decode_backend_failure;
    }

    const ascend::VdecGeometry &geometry = decoded.geometry;
    if (geometry.width == 0 || geometry.height == 0 || geometry.buffer_size == 0) {
        channel_->release(decoded.dev_ptr);
        ZL_LOGE(stream_id_, log_module, "ascend_vdec_bad_geometry width=%u height=%u", geometry.width,
                geometry.height);
        return decode_backend_failure;
    }

    const size_t buffer_size = geometry.buffer_size;
    uint8_t *host = pool_->acquire(buffer_size);
    if (host == nullptr) {
        // ⚠️ 分配失败也必须归还设备帧：VDEC 的 frame buffer 是通道级稀缺资源
        channel_->release(decoded.dev_ptr);
        ZL_LOGE(stream_id_, log_module, "ascend_vdec_host_alloc_failed size=%zu", buffer_size);
        return decode_backend_failure;
    }

    const int copy_result = channel_->copy_to_host(host, decoded.dev_ptr, buffer_size);
    // 回拷完成（无论成败）立即归还设备帧：主机内存才是本节点的输出载体
    channel_->release(decoded.dev_ptr);
    if (copy_result != decode_ok) {
        pool_->release(host, buffer_size);
        ZL_LOGE(stream_id_, log_module, "ascend_vdec_d2h_failed size=%zu", buffer_size);
        return decode_backend_failure;
    }

    const size_t y_size = static_cast<size_t>(geometry.width_stride) * geometry.height_stride;
    std::shared_ptr<HardwareFrame> frame;
    try {
        frame = std::make_shared<HardwareFrame>();
    } catch (...) {
        pool_->release(host, buffer_size);
        throw;
    }

    frame->width = geometry.width;
    frame->height = geometry.height;
    frame->stride = geometry.width_stride;
    frame->vstride = geometry.height_stride;
    frame->format = FRAME_FORMAT_NV12;
    frame->host_ptr = host;
    frame->host_planes[0] = host;
    frame->host_planes[1] = host + y_size;
    frame->plane_strides[0] = geometry.width_stride;
    frame->plane_strides[1] = geometry.width_stride;

    // ⚠️ 时间戳保真：VDEC 原样回传 send 时的 pts，用 0 表示「无时间戳」是驱动侧约定
    frame->has_pts = decoded.has_pts;
    frame->pts = decoded.has_pts ? decoded.pts : 0;
    frame->time_base_num = time_base_num_;
    frame->time_base_den = time_base_den_;
    frame->stream_id = stream_id_;

    // 池的 keepalive 与缓冲区地址一起捕获：即使节点先析构，在途帧仍能安全归还
    const std::shared_ptr<HostBufferPool> pool = pool_;
    frame->release_fn = [pool, host, buffer_size](HardwareFrame *) { pool->release(host, buffer_size); };

    out_frame = std::move(frame);
    return decode_ok;
}

void AscendDecodeNode::flush() {
    if (!channel_) {
        return;
    }
    channel_->reset();
    eos_sent_ = false;
}

} // namespace zhulong
