/**
 * @file stream.cpp
 * @brief 物理流上下文与节点处理流水线实现
 */

#include "pipeline/stream.hpp"

extern "C" {
#include <libavcodec/avcodec.h>
#include <libavutil/error.h>
}

#include <chrono>
#include <cstring>
#include <utility>

namespace zhulong {

// ============================================================================
//                               DecodeSession 实现
// ============================================================================

DecodeSession::DecodeSession(uint64_t stream_id, Video video_meta)
    : stream_id_(stream_id), video_meta_(std::move(video_meta)) {
    worker_ = std::thread(&DecodeSession::decode_loop, this);
}

DecodeSession::~DecodeSession() {
    request_stop();
    join();
}

void DecodeSession::request_stop() {
    active_.store(false, std::memory_order_relaxed);
    packet_queue_.cancel();
    frame_queue_.cancel();
}

void DecodeSession::join() {
    if (worker_.joinable()) {
        worker_.join();
    }
}

bool DecodeSession::push_packet(const Zhulong_packet_view &packet) {
    if (!active_.load(std::memory_order_relaxed) || !packet.data || packet.size == 0) {
        return false;
    }

    const bool is_key = (packet.key_frame != 0);

    // 拥塞重同步机制：若正处于等待关键帧状态，丢弃一切非关键帧直至收到完整 IDR/关键帧
    if (waiting_keyframe_.load(std::memory_order_relaxed)) {
        if (!is_key) {
            return false;
        }
        waiting_keyframe_.store(false, std::memory_order_relaxed);
        // 新的关键帧到达：清空此前积压的旧包队列，让解码器从全新关键帧起解
        packet_queue_.reset();
    }

    QueuedPacket qp;
    // 预留 AV_INPUT_BUFFER_PADDING_SIZE 零填充空间，防止底层优化解码器读越界
    qp.data.resize(packet.size + AV_INPUT_BUFFER_PADDING_SIZE, 0);
    std::memcpy(qp.data.data(), packet.data, packet.size);
    qp.pts = packet.has_pts ? packet.pts : 0;
    qp.dts = packet.has_dts ? packet.dts : 0;
    qp.has_pts = (packet.has_pts != 0);
    qp.has_dts = (packet.has_dts != 0);

    {
        std::lock_guard<std::mutex> lock(meta_mutex_);
        qp.time_base_num = packet.time_base_num > 0 ? packet.time_base_num : video_meta_.view.time_base_num;
        qp.time_base_den = packet.time_base_den > 0 ? packet.time_base_den : video_meta_.view.time_base_den;
    }
    qp.codec = packet.codec;
    qp.key_frame = is_key;

    if (!packet_queue_.push(std::move(qp))) {
        // 队列已满溢出：开启等待下一个关键帧进行自愈重同步，避免损坏的参考链喂入解码器
        waiting_keyframe_.store(true, std::memory_order_relaxed);
        return false;
    }
    return true;
}

bool DecodeSession::pop_frame(HardwareFrame::ConstPtr &out_frame, std::chrono::milliseconds timeout) {
    return frame_queue_.pop(out_frame, timeout);
}

void DecodeSession::update_metadata(const Video &video_meta) {
    std::lock_guard<std::mutex> lock(meta_mutex_);
    video_meta_ = video_meta;
}

size_t DecodeSession::frame_queue_size() const {
    return frame_queue_.size();
}

size_t DecodeSession::packet_queue_size() const {
    return packet_queue_.size();
}

bool DecodeSession::is_active() const {
    return active_.load(std::memory_order_relaxed) && !packet_queue_.is_cancelled();
}

void DecodeSession::decode_loop() noexcept {
    IDecodeNode::Ptr decoder;

    auto create_or_get_decoder = [&](int32_t codec_hint, int32_t tb_num, int32_t tb_den) -> IDecodeNode * {
        if (decoder) return decoder.get();
        try {
            Video meta;
            {
                std::lock_guard<std::mutex> lock(meta_mutex_);
                meta = video_meta_;
            }
            const int32_t selected_codec = meta.view.codec > 0 ? meta.view.codec : codec_hint;
            const int32_t final_tb_num = tb_num > 0 ? tb_num : (meta.view.time_base_num > 0 ? meta.view.time_base_num : 1);
            const int32_t final_tb_den = tb_den > 0 ? tb_den : (meta.view.time_base_den > 0 ? meta.view.time_base_den : 90000);

            if (selected_codec > 0) {
                decoder = IDecodeNode::create(
                    selected_codec,
                    meta.extradata.empty() ? nullptr : meta.extradata.data(),
                    meta.extradata.size(),
                    final_tb_num,
                    final_tb_den
                );
            }
        } catch (...) {
            decoder.reset();
        }
        return decoder.get();
    };

    auto drain_frames = [&](IDecodeNode *dec) {
        if (!dec) return;
        HardwareFrame::Ptr frame;
        while (dec->receive_frame(frame) == 0 && frame != nullptr) {
            frame->stream_id = stream_id_;
            frame_queue_.push(std::move(frame));
        }
    };

    // 尝试前置初始化解码器（若此时已有 extradata）
    create_or_get_decoder(0, 1, 90000);

    while (active_.load(std::memory_order_relaxed) && !packet_queue_.is_cancelled()) {
        QueuedPacket pkt;
        if (!packet_queue_.pop(pkt, std::chrono::milliseconds(50))) {
            continue;
        }

        if (!active_.load(std::memory_order_relaxed) || packet_queue_.is_cancelled()) {
            break;
        }

        try {
            auto *dec = create_or_get_decoder(pkt.codec, pkt.time_base_num, pkt.time_base_den);
            if (dec) {
                const size_t actual_size = pkt.data.size() >= AV_INPUT_BUFFER_PADDING_SIZE
                                               ? pkt.data.size() - AV_INPUT_BUFFER_PADDING_SIZE
                                               : pkt.data.size();
                const int64_t pts = pkt.has_pts ? pkt.pts : AV_NOPTS_VALUE;
                const int64_t dts = pkt.has_dts ? pkt.dts : AV_NOPTS_VALUE;

                int ret = dec->send_packet(pkt.data.data(), actual_size, pts, dts);

                // 若底层缓冲已满导致 EAGAIN，先排空帧输出，再重试当前包
                if (ret == AVERROR(EAGAIN)) {
                    drain_frames(dec);
                    ret = dec->send_packet(pkt.data.data(), actual_size, pts, dts);
                }

                if (ret >= 0) {
                    drain_frames(dec);
                } else if (ret != AVERROR_EOF && ret != AVERROR(EAGAIN)) {
                    // 解码单包遇到错误（如弱网残损包），执行 flush 清空 DPB，避免参考错误持续污染后续画面
                    dec->flush();
                }
            }
        } catch (...) {
            if (decoder) {
                try {
                    decoder->flush();
                } catch (...) {}
            }
        }
    }

    // 退出前排空残留帧
    if (decoder) {
        try {
            decoder->send_packet(nullptr, 0, 0, 0);
            drain_frames(decoder.get());
        } catch (...) {}
    }
}

// ============================================================================
//                               Stream 实现
// ============================================================================

Stream::Stream(Zhulong_stream_id stream_id, std::string normalized_url, Options stream_options)
    : id(stream_id), url(std::move(normalized_url)), options(stream_options) {}

Stream::~Stream() {
    request_stop();
    join();
}

void Stream::start() {
    // 检查并按需初始化解码节点
    stop_decode_session(sync_decode_pipeline());
    // 启动拉流 Worker 工作线程
    worker = std::thread(&Stream::capture, this);
}

void Stream::request_stop() {
    cancel.stopped.store(true);
    std::shared_ptr<DecodeSession> session;
    {
        std::lock_guard<std::mutex> lock(mutex);
        session = decode_session_;
    }
    if (session) {
        session->request_stop();
    }
}

void Stream::join() {
    std::shared_ptr<DecodeSession> session;
    {
        std::lock_guard<std::mutex> lock(mutex);
        session = std::move(decode_session_);
    }
    if (session) {
        session->join();
    }
    if (worker.joinable()) {
        worker.join();
    }
}

bool Stream::has_ai_consumer() const {
    for (const auto &entry : consumers) {
        if (entry.second == Zhulong_CONSUMER_AI) {
            return true;
        }
    }
    return false;
}

std::shared_ptr<DecodeSession> Stream::sync_decode_pipeline() {
    std::lock_guard<std::mutex> lock(mutex);
    const bool should_decode = has_ai_consumer();

    if (should_decode && !decode_session_) {
        // 创建全新独立会话并启动工作线程，队列初始状态未取消
        decode_session_ = std::make_shared<DecodeSession>(id, video_meta_);
        return nullptr;
    } else if (!should_decode && decode_session_) {
        // 移出会话指针，以便调用方在锁外调用 stop 与 join
        auto retiring = std::move(decode_session_);
        return retiring;
    }
    return nullptr;
}

void Stream::stop_decode_session(std::shared_ptr<DecodeSession> session) {
    if (session) {
        session->request_stop();
        session->join();
    }
}

bool Stream::acquire_frame(HardwareFrame::ConstPtr &out_frame, std::chrono::milliseconds timeout) {
    std::shared_ptr<DecodeSession> session;
    {
        std::lock_guard<std::mutex> lock(mutex);
        session = decode_session_;
    }
    if (!session) return false;
    return session->pop_frame(out_frame, timeout);
}

size_t Stream::frame_queue_size() const {
    std::shared_ptr<DecodeSession> session;
    {
        std::lock_guard<std::mutex> lock(mutex);
        session = decode_session_;
    }
    return session ? session->frame_queue_size() : 0;
}

bool Stream::has_active_decoder() const {
    std::shared_ptr<DecodeSession> session;
    {
        std::lock_guard<std::mutex> lock(mutex);
        session = decode_session_;
    }
    return session && session->is_active();
}

void Stream::on_ready(const Video &video) {
    std::shared_ptr<DecodeSession> session;
    {
        std::lock_guard<std::mutex> lock(mutex);
        video_meta_ = video;
        status.state = Zhulong_STREAM_RUNNING;
        session = decode_session_;
    }
    if (session) {
        session->update_metadata(video);
    }
}

void Stream::dispatch(const Zhulong_packet_view &packet) {
    // 1. 优先推送至 AI 解码队列 (先于 Preview 回调，避免慢回调延迟 AI 处理)
    if (!cancel.stopped.load()) {
        std::shared_ptr<DecodeSession> session;
        {
            std::lock_guard<std::mutex> lock(mutex);
            session = decode_session_;
        }
        if (session) {
            session->push_packet(packet);
        }
    }

    // 2. 快照机制：在锁保护下复制当前的订阅者指针列表，在锁外逐个调用
    std::vector<std::shared_ptr<Subscription>> snapshot;
    {
        std::lock_guard<std::mutex> lock(mutex);
        for (const auto &entry : subscriptions) {
            snapshot.push_back(entry.second);
        }
    }

    for (const auto &subscription : snapshot) {
        if (cancel.stopped.load()) break;
        subscription->invoke(packet);
    }
}

void Stream::capture() noexcept {
    Zhulong_status_t error = Zhulong_OK;
    try {
        read_rtsp(url, options, cancel,
            [this](const Zhulong_packet_view &packet) {
                dispatch(packet);
            },
            [this](const Video &video) {
                on_ready(video);
            });
    } catch (const Failure &failure) {
        error = failure.status;
    } catch (const std::bad_alloc &) {
        error = Zhulong_ERR_OUT_OF_MEMORY;
    } catch (...) {
        error = Zhulong_ERR_INTERNAL;
    }

    std::lock_guard<std::mutex> lock(mutex);
    status = {Zhulong_STREAM_FAILED, error == Zhulong_OK ? Zhulong_ERR_CANCELLED : error};
}

} // namespace zhulong
