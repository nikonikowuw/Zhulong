/**
 * @file stream.cpp
 * @brief 物理流上下文与节点处理流水线实现
 */

#include "pipeline/stream.hpp"

extern "C" {
#include <libavcodec/avcodec.h>
#include <libavutil/error.h>
}

#include <algorithm>
#include <chrono>
#include <utility>

namespace zhulong {

// ============================================================================
//                               DecodeSession 实现
// ============================================================================

namespace {
/** 缓冲池最多保留的缓冲数量与单个缓冲容量上限 (防止异常大包长期占用内存) */
constexpr size_t kMaxPooledBuffers = 16;
constexpr size_t kMaxPooledCapacity = 4u * 1024u * 1024u;
}

DecodeSession::DecodeSession(uint64_t stream_id, Video video_meta)
    : stream_id_(stream_id), video_meta_(std::move(video_meta)) {
    time_base_num_.store(video_meta_.view.time_base_num, std::memory_order_relaxed);
    time_base_den_.store(video_meta_.view.time_base_den, std::memory_order_relaxed);
    worker_ = std::thread(&DecodeSession::decode_loop, this);
}

std::vector<uint8_t> DecodeSession::acquire_buffer(size_t size) {
    std::vector<uint8_t> buffer;
    {
        std::lock_guard<std::mutex> lock(pool_mutex_);
        if (!buffer_pool_.empty()) {
            buffer = std::move(buffer_pool_.back());
            buffer_pool_.pop_back();
        }
    }
    buffer.reserve(size);
    return buffer;
}

void DecodeSession::recycle_buffer(std::vector<uint8_t> &&buffer) {
    if (buffer.capacity() == 0 || buffer.capacity() > kMaxPooledCapacity) return;
    buffer.clear();
    std::lock_guard<std::mutex> lock(pool_mutex_);
    if (buffer_pool_.size() < kMaxPooledBuffers) {
        buffer_pool_.push_back(std::move(buffer));
    }
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
            keyframe_gate_drops_.fetch_add(1, std::memory_order_relaxed);
            return false;
        }
        waiting_keyframe_.store(false, std::memory_order_relaxed);
        // 新的关键帧到达：清空此前积压的旧包队列 (仅清元素，不触碰取消标记)
        packet_queue_.clear();
    }

    QueuedPacket qp;
    // 预留 AV_INPUT_BUFFER_PADDING_SIZE 零填充空间，防止底层优化解码器读越界；
    // 缓冲来自复用池，先拷贝有效载荷，再仅对尾部 padding 清零，避免整块重复写入
    qp.data = acquire_buffer(packet.size + AV_INPUT_BUFFER_PADDING_SIZE);
    qp.data.assign(packet.data, packet.data + packet.size);
    qp.data.resize(packet.size + AV_INPUT_BUFFER_PADDING_SIZE, 0);
    qp.pts = packet.has_pts ? packet.pts : 0;
    qp.dts = packet.has_dts ? packet.dts : 0;
    qp.has_pts = (packet.has_pts != 0);
    qp.has_dts = (packet.has_dts != 0);

    qp.time_base_num = packet.time_base_num > 0 ? packet.time_base_num : time_base_num_.load(std::memory_order_relaxed);
    qp.time_base_den = packet.time_base_den > 0 ? packet.time_base_den : time_base_den_.load(std::memory_order_relaxed);
    qp.codec = packet.codec;
    qp.key_frame = is_key;

    if (!packet_queue_.push(std::move(qp))) {
        // 队列已满溢出：立即丢弃全部积压并等待下一个关键帧重同步。
        // 否则 AI 将长期消费一条落后约一个队列深度 (60 包) 的陈旧画面，延迟无法自愈。
        waiting_keyframe_.store(true, std::memory_order_relaxed);
        packet_queue_.clear();
        // 仅在队列未被取消时计数：停机竞态下的 push 失败不是拥塞，不污染统计
        if (!packet_queue_.is_cancelled()) {
            congestion_resyncs_.fetch_add(1, std::memory_order_relaxed);
        }
        return false;
    }
    return true;
}

bool DecodeSession::pop_frame(HardwareFrame::ConstPtr &out_frame, std::chrono::milliseconds timeout) {
    return frame_queue_.pop(out_frame, timeout);
}

void DecodeSession::update_metadata(const Video &video_meta) {
    std::lock_guard<std::mutex> lock(meta_mutex_);
    // 仅当编码类型或参数集真正变化时才触发解码器重建，避免就绪回调的重复通知打断解码
    const bool decoder_relevant_change =
        video_meta_.view.codec != video_meta.view.codec || video_meta_.extradata != video_meta.extradata;
    video_meta_ = video_meta;
    time_base_num_.store(video_meta_.view.time_base_num, std::memory_order_relaxed);
    time_base_den_.store(video_meta_.view.time_base_den, std::memory_order_relaxed);
    if (decoder_relevant_change) {
        meta_generation_.fetch_add(1, std::memory_order_release);
    }
}

size_t DecodeSession::frame_queue_size() const {
    return frame_queue_.size();
}

size_t DecodeSession::packet_queue_size() const {
    return packet_queue_.size();
}

DecodeCounters DecodeSession::counters() const {
    DecodeCounters snapshot;
    snapshot.decoder_unavailable_drops = decoder_unavailable_drops_.load(std::memory_order_relaxed);
    snapshot.keyframe_gate_drops = keyframe_gate_drops_.load(std::memory_order_relaxed);
    snapshot.congestion_resyncs = congestion_resyncs_.load(std::memory_order_relaxed);
    return snapshot;
}

bool DecodeSession::is_active() const {
    return active_.load(std::memory_order_relaxed) && !packet_queue_.is_cancelled();
}

void DecodeSession::decode_loop() noexcept {
    using SteadyClock = std::chrono::steady_clock;
    constexpr auto kInitialRetryDelay = std::chrono::milliseconds(250);
    constexpr auto kMaxRetryDelay = std::chrono::milliseconds(5000);

    IDecodeNode::Ptr decoder;
    uint64_t decoder_generation = 0;
    // 解码线程侧的关键帧门控：启动、解码出错、解码器重建之后，
    // 必须等到关键帧才能继续送入解码器，避免孤立的 P 帧产生报错与花屏帧
    bool need_keyframe = true;
    // 解码器创建失败退避：硬件/软件解码器初始化昂贵，失败时不能每包重试
    auto next_create_attempt = SteadyClock::time_point::min();
    auto retry_delay = kInitialRetryDelay;

    auto create_or_get_decoder = [&](int32_t codec_hint, int32_t tb_num, int32_t tb_den) -> IDecodeNode * {
        if (decoder) return decoder.get();
        if (SteadyClock::now() < next_create_attempt) return nullptr;
        try {
            Video meta;
            uint64_t generation = 0;
            {
                std::lock_guard<std::mutex> lock(meta_mutex_);
                generation = meta_generation_.load(std::memory_order_acquire);
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
                if (decoder) {
                    decoder_generation = generation;
                    retry_delay = kInitialRetryDelay;
                    need_keyframe = true;
                } else {
                    next_create_attempt = SteadyClock::now() + retry_delay;
                    retry_delay = std::min(retry_delay * 2, kMaxRetryDelay);
                }
            }
        } catch (...) {
            decoder.reset();
            next_create_attempt = SteadyClock::now() + retry_delay;
            retry_delay = std::min(retry_delay * 2, kMaxRetryDelay);
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

        // 编码类型或参数集变化：旧解码器不再适用，丢弃并按新元数据重建
        if (decoder && decoder_generation != meta_generation_.load(std::memory_order_acquire)) {
            decoder.reset();
            need_keyframe = true;
        }

        if (need_keyframe && !pkt.key_frame) {
            keyframe_gate_drops_.fetch_add(1, std::memory_order_relaxed);
            recycle_buffer(std::move(pkt.data));
            continue;
        }

        try {
            auto *dec = create_or_get_decoder(pkt.codec, pkt.time_base_num, pkt.time_base_den);
            if (dec) {
                // 解码器可能刚在上面被重建 (need_keyframe 被重置)，非关键帧此时仍须丢弃
                if (need_keyframe && !pkt.key_frame) {
                    keyframe_gate_drops_.fetch_add(1, std::memory_order_relaxed);
                    recycle_buffer(std::move(pkt.data));
                    continue;
                }
                need_keyframe = false;

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
                    // 解码单包遇到错误（如弱网残损包），执行 flush 清空 DPB，
                    // 并等待下一个关键帧，避免参考错误持续污染后续画面
                    dec->flush();
                    need_keyframe = true;
                }
            } else {
                // 解码器不可用 (codec 无效 / 创建退避窗口内 / 创建失败)：本包被丢弃
                decoder_unavailable_drops_.fetch_add(1, std::memory_order_relaxed);
            }
        } catch (...) {
            if (decoder) {
                try {
                    decoder->flush();
                } catch (...) {}
            }
            need_keyframe = true;
        }
        recycle_buffer(std::move(pkt.data));
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

uint64_t Stream::callback_exception_count() const {
    return callback_exceptions_.load(std::memory_order_relaxed);
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

void Stream::rebuild_subscribers() {
    auto list = std::make_shared<SubscriberList>();
    list->reserve(subscriptions.size());
    for (const auto &entry : subscriptions) {
        list->push_back(entry.second);
    }
    subscriber_snapshot_ = std::move(list);
}

void Stream::dispatch(const Zhulong_packet_view &packet) {
    // 单次加锁同时取得解码会话与订阅者快照 (仅拷贝 shared_ptr，热路径无堆分配)
    std::shared_ptr<DecodeSession> session;
    std::shared_ptr<const SubscriberList> subscribers;
    {
        std::lock_guard<std::mutex> lock(mutex);
        session = decode_session_;
        subscribers = subscriber_snapshot_;
    }

    // 1. 优先推送至 AI 解码队列 (先于 Preview 回调，避免慢回调延迟 AI 处理)
    if (session && !cancel.stopped.load()) {
        session->push_packet(packet);
    }

    // 2. 在锁外逐个调用订阅回调
    //    逐订阅者隔离异常：抛异常是调用方违约 (engine.h)，不得中断同流其他订阅者的数据供给
    for (const auto &subscription : *subscribers) {
        if (cancel.stopped.load()) break;
        try {
            subscription->invoke(packet);
        } catch (...) {
            // invoke 已在抛出前恢复 in_callback 并置 enabled=false，此处仅计数：
            // 违约订阅者静默失效，流状态保持 RUNNING，其他订阅者不受影响
            callback_exceptions_.fetch_add(1, std::memory_order_relaxed);
        }
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
