/**
 * @file stream.cpp
 * @brief 物理流上下文与节点处理流水线实现
 *
 * 核心机制实现说明：
 * 1. 独立解码会话生命周期与锁外汇合：
 *    - 当物理流首个 AI 消费者注册时，动态实例化 DecodeSession，拉起独立解码线程；
 *    - 当最后一个 AI 消费者释放时，sync_decode_pipeline 在锁内转移出 session 指针，
 *      交由调用方在锁外调用 stop_decode_session 执行 request_stop() 与 join()，杜绝持锁汇合阻塞全局流控制；
 * 2. 拥塞重同步与关键帧门控（R16）：
 *    - 面对网络抖动或丢包，当 packet_queue_ 满载时触发 waiting_keyframe_=true 并清空积压旧包；
 *    - 拒绝一切非关键帧，直到完整关键帧到达并清空旧队列后才恢复解码，解码器调用 flush() 清空 DPB；
 * 3. 背压完整排空（R19）：
 *    - 当 send_packet 返回 decode_again 时，立即循环 receive_frame 排空当前就绪帧，
 *      随后再次重试送入该包；二次仍背压则记录告警，杜绝静默吞包丢帧；
 * 4. 优先级分发与逐订阅者异常隔离：
 *    - 数据包优先拷入 AI 解码队列，确保分析低延迟；
 *    - 锁外通过不可变快照遍历 Preview 订阅者，单个订阅者回调抛错不影响其他订阅者与主采集流程。
 */

#include "pipeline/stream.hpp"
#include "common/log.hpp"

#include <algorithm>
#include <chrono>

extern "C" {
#include <libavcodec/avcodec.h>
#include <libavutil/error.h>
}

#include <utility>

namespace zhulong {

// ============================================================================
//                               DecodeSession 实现
// ============================================================================

namespace {
/**
 * 缓冲池上限：块数、单块容量、池总字节数。
 *
 * 总字节上限是主约束：只限块数时，16 块 × 4MB = 64MB 会被一路流长期占住。
 * 8MB 足以覆盖 60 包队列里绝大多数复用，又不至于让板端内存被空池吃掉。
 */
constexpr size_t max_pooled_buffers = 16;
constexpr size_t max_pooled_capacity = 4u * 1024u * 1024u;
constexpr size_t max_pooled_bytes = 8u * 1024u * 1024u;
constexpr int32_t default_video_time_base_num = 1;
constexpr int32_t default_video_time_base_den = 90000;
}

DecodeSession::DecodeSession(uint64_t stream_id, Video video_meta,
                             Zhulong_decode_policy_t policy,
                             std::function<void(Zhulong_status_t)> on_fatal_error)
    : stream_id_(stream_id), video_meta_(std::move(video_meta)),
      policy_(policy), on_fatal_error_(std::move(on_fatal_error)) {
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
            pooled_bytes_ -= buffer.capacity();
        }
    }
    buffer.reserve(size);
    return buffer;
}

void DecodeSession::recycle_buffer(std::vector<uint8_t> &&buffer) noexcept {
    const size_t capacity = buffer.capacity();
    if (capacity == 0 || capacity > max_pooled_capacity) return;
    buffer.clear();
    std::lock_guard<std::mutex> lock(pool_mutex_);
    // ⚠️ 形参 buffer 在函数体局部对象（含上面的 lock）之后析构，因此这里提前 return
    //    释放的是锁外的内存，不会持锁触发堆分配器。改成移动进局部变量再 return 就会破掉这点。
    if (buffer_pool_.size() >= max_pooled_buffers || pooled_bytes_ + capacity > max_pooled_bytes) {
        return;
    }
    // ⚠️ 本函数在解码线程入口的热路径上且是 noexcept：池扩容失败只放弃这一次复用
    //    （缓冲在锁外析构），绝不让 bad_alloc 穿出 noexcept 触发 std::terminate。
    try {
        buffer_pool_.push_back(std::move(buffer));
    } catch (...) {
        return;
    }
    pooled_bytes_ += capacity;
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
        // 新的关键帧到达：清空此前积压的旧包队列 (仅清空元素，不改变取消标记)
        packet_queue_.clear();
    }

    QueuedPacket qp;
    // 预留 AV_INPUT_BUFFER_PADDING_SIZE 零填充空间，防止底层优化解码器读越界；
    // 缓冲来自复用池，先拷贝有效载荷，再仅对尾部 padding 清零，避免整块重复写入
    qp.data = acquire_buffer(packet.size + AV_INPUT_BUFFER_PADDING_SIZE);
    qp.data.assign(packet.data, packet.data + packet.size);
    qp.data.resize(packet.size + AV_INPUT_BUFFER_PADDING_SIZE, 0);
    // ⚠️ 时间戳保真透传：严格根据 has_pts / has_dts 记录有效性，禁止把缺失时间戳当作有效 0
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
        const bool already_waiting = waiting_keyframe_.exchange(true, std::memory_order_relaxed);
        packet_queue_.clear();
        // 仅在队列未被取消时计数：停机竞态下的 push 失败不是拥塞，不污染统计
        if (!packet_queue_.is_cancelled()) {
            congestion_resyncs_.fetch_add(1, std::memory_order_relaxed);
            if (!already_waiting) {
                ZL_LOGW(stream_id_, "pipeline.stream", "decode_queue_resync");
            }
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

DecodeCounters DecodeSession::counters() const noexcept {
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
    // ⚠️ 线程入口必须自行收口异常：本函数是 noexcept，任何逃出的异常都会直接
    //    std::terminate 掉整个宿主进程（同进程里还有其他流与 Go 侧调用在跑）。
    //    收口后按「解码会话不可用」受控降级，并把故障上报给 Stream 熔断。
    auto fail_closed = [this](Zhulong_status_t status) noexcept {
        active_.store(false, std::memory_order_relaxed);
        // 取消队列与熔断回调都可能分配内存（`std::deque` 默认构造、Stream 侧容器），
        // 而本 lambda 已在异常收口路径上：二次异常穿出 noexcept 就是 std::terminate。
        // 二者都不是恢复正确性所必需——`active_` 已置 false，发布线程最迟 50ms 超时退出。
        try {
            packet_queue_.cancel();
            frame_queue_.cancel();
            if (on_fatal_error_) {
                on_fatal_error_(status);
            }
        } catch (...) {
        }
    };

    try {
        decode_loop_body();
    } catch (const std::bad_alloc &) {
        ZL_LOGE(stream_id_, "pipeline.stream", "decode_loop_out_of_memory");
        fail_closed(Zhulong_ERR_OUT_OF_MEMORY);
    } catch (...) {
        ZL_LOGE(stream_id_, "pipeline.stream", "decode_loop_exception");
        fail_closed(Zhulong_ERR_INTERNAL);
    }
}

void DecodeSession::decode_loop_body() {
    using SteadyClock = std::chrono::steady_clock;
    constexpr auto initial_retry_delay = std::chrono::milliseconds(250);
    constexpr auto max_retry_delay = std::chrono::milliseconds(5000);

    IDecodeNode::Ptr decoder;
    uint64_t decoder_generation = 0;
    // 解码线程侧的关键帧门控：启动、解码出错、解码器重建之后，
    // 必须等到关键帧才能继续送入解码器，避免孤立的 P 帧产生报错与花屏帧
    bool need_keyframe = true;
    // 解码器创建失败退避：硬件/软件解码器初始化昂贵，失败时不能每包重试
    auto next_create_attempt = SteadyClock::time_point::min();
    auto retry_delay = initial_retry_delay;

    auto create_or_get_decoder = [&](int32_t codec_hint, int32_t tb_num, int32_t tb_den) -> IDecodeNode * {
        if (decoder) return decoder.get();
        if (SteadyClock::now() < next_create_attempt) return nullptr;

        DecoderBackend target_backend = DecoderBackend::automatic;
        if (policy_.backend == Zhulong_DECODE_BACKEND_MPP) {
            target_backend = DecoderBackend::mpp_hw;
        } else if (policy_.backend == Zhulong_DECODE_BACKEND_FFMPEG) {
            target_backend = DecoderBackend::ffmpeg_sw;
        } else {
            if (policy_.allow_software_fallback == 0) {
                target_backend = DecoderBackend::mpp_hw;
            } else {
                target_backend = DecoderBackend::automatic;
            }
        }

        int32_t selected_codec = codec_hint;
        try {
            Video meta;
            uint64_t generation = 0;
            {
                std::lock_guard<std::mutex> lock(meta_mutex_);
                generation = meta_generation_.load(std::memory_order_acquire);
                meta = video_meta_;
            }
            selected_codec = meta.view.codec > 0 ? meta.view.codec : codec_hint;
            const int32_t final_tb_num =
                tb_num > 0 ? tb_num : (meta.view.time_base_num > 0 ? meta.view.time_base_num : default_video_time_base_num);
            const int32_t final_tb_den =
                tb_den > 0 ? tb_den : (meta.view.time_base_den > 0 ? meta.view.time_base_den : default_video_time_base_den);

            if (selected_codec > 0) {
                decoder = IDecodeNode::create(
                    selected_codec,
                    meta.extradata.empty() ? nullptr : meta.extradata.data(),
                    meta.extradata.size(),
                    final_tb_num,
                    final_tb_den,
                    target_backend,
                    stream_id_
                );
                if (decoder) {
                    decoder_generation = generation;
                    retry_delay = initial_retry_delay;
                    need_keyframe = true;
                } else {
                    next_create_attempt = SteadyClock::now() + retry_delay;
                    retry_delay = std::min(retry_delay * 2, max_retry_delay);
                    ZL_LOGW(stream_id_, "pipeline.stream", "decoder_create_failed retry_delay_ms=%lld",
                        static_cast<long long>(retry_delay.count()));
                }
            }
        } catch (const std::exception &) {
            decoder.reset();
            if (target_backend == DecoderBackend::mpp_hw) {
                ZL_LOGE(stream_id_, "pipeline.stream", "hardware_decode_fail_closed codec=%d", selected_codec);
                if (on_fatal_error_) {
                    on_fatal_error_(Zhulong_ERR_HARDWARE_UNAVAILABLE);
                }
                active_.store(false, std::memory_order_relaxed);
                return nullptr;
            }
            next_create_attempt = SteadyClock::now() + retry_delay;
            retry_delay = std::min(retry_delay * 2, max_retry_delay);
            ZL_LOGW(stream_id_, "pipeline.stream", "decoder_create_failed retry_delay_ms=%lld",
                static_cast<long long>(retry_delay.count()));
        } catch (...) {
            decoder.reset();
            if (target_backend == DecoderBackend::mpp_hw) {
                ZL_LOGE(stream_id_, "pipeline.stream", "hardware_decode_fail_closed codec=%d", selected_codec);
                if (on_fatal_error_) {
                    on_fatal_error_(Zhulong_ERR_HARDWARE_UNAVAILABLE);
                }
                active_.store(false, std::memory_order_relaxed);
                return nullptr;
            }
            next_create_attempt = SteadyClock::now() + retry_delay;
            retry_delay = std::min(retry_delay * 2, max_retry_delay);
            ZL_LOGW(stream_id_, "pipeline.stream", "decoder_create_failed retry_delay_ms=%lld",
                static_cast<long long>(retry_delay.count()));
        }
        return decoder.get();
    };

    // ⚠️ 本 lambda 自己吞掉异常：decode_loop_body() 允许抛出（由 decode_loop 收口），
    //    但排空帧失败只该丢帧并按解码错误重同步关键帧，不值得终止整个解码会话。
    auto drain_frames = [&](IDecodeNode *dec) {
        if (!dec) return;
        try {
            HardwareFrame::Ptr frame;
            while (dec->receive_frame(frame) == decode_ok && frame != nullptr) {
                frame->stream_id = stream_id_;
                frame_queue_.push(std::move(frame));
            }
        } catch (...) {
            // 排空中断：丢弃已解出的帧比崩掉整个宿主便宜，随后按解码错误重同步关键帧
            need_keyframe = true;
            ZL_LOGW(stream_id_, "pipeline.stream", "decode_drain_exception");
        }
    };

    // 尝试前置初始化解码器（若此时已有 extradata）
    create_or_get_decoder(0, default_video_time_base_num, default_video_time_base_den);

    while (active_.load(std::memory_order_relaxed) && !packet_queue_.is_cancelled()) {
        QueuedPacket pkt;
        if (!packet_queue_.pop(pkt, std::chrono::milliseconds(50))) {
            if (decoder) {
                drain_frames(decoder.get());
            }
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

                // ⚠️ 若底层缓冲已满导致背压，先排空已解码帧，再重新发送当前包，严防静默吞包
                if (ret == decode_again) {
                    drain_frames(dec);
                    ret = dec->send_packet(pkt.data.data(), actual_size, pts, dts);
                }

                if (ret == decode_ok) {
                    drain_frames(dec);
                } else if (ret == decode_again) {
                    // 排空后仍背压：本包被丢弃。持续性背压说明解码器已停止消费，必须可观测
                    ZL_LOGW(stream_id_, "pipeline.stream", "decode_backpressure_drop size=%zu", actual_size);
                } else if (ret != AVERROR_EOF) {
                    // 解码单包遇到错误（如弱网残损包），执行冲刷清空 DPB，
                    // 并等待下一个关键帧，避免参考错误持续污染后续画面
                    dec->flush();
                    need_keyframe = true;
                    ZL_LOGW(stream_id_, "pipeline.stream", "decode_packet_failed rc=%d", ret);
                }
            } else {
                // 解码器不可用 (codec 无效 / 创建退避窗口内 / 创建失败)：本包被丢弃
                decoder_unavailable_drops_.fetch_add(1, std::memory_order_relaxed);
            }
        } catch (...) {
            if (!need_keyframe) {
                ZL_LOGW(stream_id_, "pipeline.stream", "decode_packet_exception");
            }
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
        session->request_stop();
        session->join();
    }
    if (worker.joinable()) {
        worker.join();
    }
}

bool Stream::has_ai_consumer() const noexcept {
    for (const auto &entry : consumers) {
        if (entry.second == Zhulong_CONSUMER_AI) {
            return true;
        }
    }
    return false;
}

std::shared_ptr<DecodeSession> Stream::sync_decode_pipeline(const Zhulong_decode_policy_t &policy) {
    std::lock_guard<std::mutex> lock(mutex);
    const bool should_decode = has_ai_consumer();

    if (should_decode && !decode_session_) {
        // 创建全新独立会话并启动工作线程，队列初始状态未取消
        decode_session_ = std::make_shared<DecodeSession>(
            id, video_meta_, policy,
            [this](Zhulong_status_t fatal_err) {
                this->on_decode_fatal_error(fatal_err);
            });
        return nullptr;
    } else if (!should_decode && decode_session_) {
        // 移出会话指针，以便调用方在锁外调用 stop 与 join
        auto retiring = std::move(decode_session_);
        return retiring;
    }
    return nullptr;
}

void Stream::on_decode_fatal_error(Zhulong_status_t fatal_err) {
    // ⚠️ request_stop() 也要拿 mutex，且本函数在**解码线程**上执行（DecodeSession 的熔断回调）。
    //    两个临界区必须分开：嵌套就是同线程二次加锁 std::mutex —— 直接死锁，
    //    随后 join() 永远等不到解码线程退出，整机停机挂死。
    {
        std::lock_guard<std::mutex> lock(mutex);
        status.state = Zhulong_STREAM_FAILED;
        status.error = fatal_err;
    }
    ZL_LOGE(id, "pipeline.stream", "fatal_decode_error fail_closed status=%d", fatal_err);
    request_stop();
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

uint64_t Stream::callback_exception_count() const noexcept {
    return callback_exceptions_.load(std::memory_order_relaxed);
}

void Stream::on_ready(const Media &media) {
    std::shared_ptr<DecodeSession> session;
    {
        std::lock_guard<std::mutex> lock(mutex);
        video_meta_ = media.video;
        audio_meta_ = media.audio;
        // FAILED 是终态：迟到的就绪回调整不得把它洗回 RUNNING，否则熔断会被静默撤销
        if (status.state != Zhulong_STREAM_FAILED) {
            status.state = Zhulong_STREAM_RUNNING;
        }
        session = decode_session_;
    }
    // 参数集在此完成 Annex B 归一化；放在锁外，不与 dispatch 热路径争锁
    preview_normalizer_.update_metadata(
        media.video.view.codec,
        media.video.extradata.empty() ? nullptr : media.video.extradata.data(),
        media.video.extradata.size(),
        media.video.view.time_base_num,
        media.video.view.time_base_den);
    if (media.audio.has_audio) {
        audio_normalizer_.update_metadata(
            media.audio.codec,
            media.audio.sample_rate,
            media.audio.channels,
            media.audio.time_base_num,
            media.audio.time_base_den);
    }
    if (session) {
        session->update_metadata(media.video);
    }
    ZL_LOGI(id, "pipeline.stream", "stream_ready video: codec=%d width=%d height=%d extradata=%zu tb=%d/%d audio: has=%d codec=%d rate=%d ch=%d",
        media.video.view.codec, media.video.view.width, media.video.view.height, media.video.extradata.size(),
        media.video.view.time_base_num, media.video.view.time_base_den,
        media.audio.has_audio ? 1 : 0, media.audio.codec, media.audio.sample_rate, media.audio.channels);
}

std::shared_ptr<const Stream::SubscriberList> Stream::make_subscriber_snapshot(
    const std::shared_ptr<Subscription> &append, Zhulong_subscription_id exclude) const {
    auto list = std::make_shared<SubscriberList>();
    list->reserve(subscriptions.size() + (append ? 1u : 0u));
    for (const auto &entry : subscriptions) {
        if (exclude != 0 && entry.first == exclude) continue;
        list->push_back(entry.second);
    }
    if (append) {
        list->push_back(append);
    }
    return list;
}

void Stream::dispatch(const Zhulong_packet_view &packet) {
    if (packet.track_type != ZHULONG_TRACK_VIDEO) {
        ZL_LOGE(id, "pipeline.stream", "dispatch_track_type_mismatch expected_video track=%u", packet.track_type);
        return;
    }

    // 单次加锁同时取得解码会话与订阅者快照 (仅拷贝 shared_ptr，热路径无堆分配)
    std::shared_ptr<DecodeSession> session;
    std::shared_ptr<const SubscriberList> subscribers;
    {
        std::lock_guard<std::mutex> lock(mutex);
        session = decode_session_;
        subscribers = subscriber_snapshot_;
    }

    // 无人接收时不做整形：normalize() 会逐包扫 NAL 并推进时间轴，全部是纯开销。
    // 原点因此从「首个消费者看到的第一包」建立，比在无人期空转更贴近订阅语义。
    if (!session && subscribers->empty()) {
        return;
    }

    if (!preview_normalizer_.origin_ready()) {
        video_origin_arrival_us_ = std::chrono::duration_cast<std::chrono::microseconds>(
            Clock::now().time_since_epoch()).count();
    }

    // 预览契约整形：单调微秒时间戳 + 帧分类 + 参数集注入。
    // 返回值中的 data 可能借用整形器内部缓冲，仅在下一次 dispatch 前有效，
    // 与 engine.h 的「回调期间借用」契约一致；解码队列会在 push_packet 内深拷贝。
    const Zhulong_packet_view shaped = preview_normalizer_.normalize(packet);

    // 1. 优先推送至 AI 解码队列 (先于 Preview 回调，避免慢回调延迟 AI 处理)
    if (session && !cancel.stopped.load()) {
        session->push_packet(shaped);
    }

    // 2. 在锁外逐个调用订阅回调（按轨过滤仅接收视频轨的订阅者）
    //    逐订阅者隔离异常：抛异常是调用方违约 (engine.h)，不得中断同流其他订阅者的数据供给
    for (const auto &subscription : *subscribers) {
        if (cancel.stopped.load()) break;
        if ((subscription->track_mask & ZHULONG_TRACK_MASK_VIDEO) == 0) continue;
        try {
            subscription->invoke(shaped);
        } catch (...) {
            // invoke 已在抛出前恢复 in_callback 并置 enabled=false，此处仅计数：
            // 违约订阅者静默失效，流状态保持 RUNNING，其他订阅者不受影响
            callback_exceptions_.fetch_add(1, std::memory_order_relaxed);
        }
    }
}

void Stream::dispatch_audio(const Zhulong_packet_view &packet) {
    if (packet.track_type != ZHULONG_TRACK_AUDIO) {
        ZL_LOGE(id, "pipeline.stream", "dispatch_audio_track_type_mismatch expected_audio track=%u", packet.track_type);
        return;
    }

    const int64_t now_us = std::chrono::duration_cast<std::chrono::microseconds>(
        Clock::now().time_since_epoch()).count();

    const Zhulong_packet_view shaped = audio_normalizer_.normalize(
        packet,
        preview_normalizer_.origin_ready(),
        video_origin_arrival_us_,
        preview_normalizer_.timestamp_shift_us(),
        preview_normalizer_.discontinuity_generation(),
        now_us);

    if (shaped.size == 0) {
        // 在视频原点建立前被丢弃，不向下分发
        return;
    }

    // 锚点一次性日志：音频“有音轨但没声音”时，base 与丢包数就是第一现场。
    // 每路流只打一行，不参与热路径。
    if (!audio_anchor_logged_ && audio_normalizer_.anchor_established()) {
        audio_anchor_logged_ = true;
        ZL_LOGI(id, "pipeline.stream", "audio_anchor_established base_us=%lld dropped_before_origin=%llu",
                static_cast<long long>(audio_normalizer_.base_offset_us()),
                static_cast<unsigned long long>(audio_normalizer_.dropped_before_origin()));
    }

    std::shared_ptr<const SubscriberList> subscribers;
    {
        std::lock_guard<std::mutex> lock(mutex);
        subscribers = subscriber_snapshot_;
    }

    for (const auto &subscription : *subscribers) {
        if (cancel.stopped.load()) break;
        if ((subscription->track_mask & ZHULONG_TRACK_MASK_AUDIO) == 0) continue;
        try {
            subscription->invoke(shaped);
        } catch (...) {
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
            [this](const Zhulong_packet_view &packet) {
                dispatch_audio(packet);
            },
            [this](const Media &media) {
                on_ready(media);
            },
            id);
    } catch (const Failure &failure) {
        error = failure.status;
        if (error != Zhulong_ERR_CANCELLED) {
            ZL_LOGE(id, "pipeline.stream", "capture_failed rc=%d", static_cast<int>(error));
        }
    } catch (const std::bad_alloc &) {
        error = Zhulong_ERR_OUT_OF_MEMORY;
        ZL_LOGE(id, "pipeline.stream", "capture_failed rc=%d", static_cast<int>(error));
    } catch (...) {
        error = Zhulong_ERR_INTERNAL;
        ZL_LOGE(id, "pipeline.stream", "capture_failed rc=%d", static_cast<int>(error));
    }

    std::lock_guard<std::mutex> lock(mutex);
    // C ABI 只有 STARTING/RUNNING/FAILED 三态，没有 STOPPED：正常停机在这里统一表达为
    // FAILED + Zhulong_ERR_CANCELLED，调用方靠 error 区分「被停机」与「真故障」（见 engine.h）。
    // ⚠️ 但熔断（on_decode_fatal_error，来自解码线程）先记录的真实故障必须保留：它随后会
    //    request_stop()，让本线程以 ERR_CANCELLED 收尾；若无条件覆盖，FAILED + CANCELLED
    //    会被读成「受控停机」，硬件故障就此静默。
    const bool fatal_error_recorded = status.state == Zhulong_STREAM_FAILED &&
                                      status.error != Zhulong_OK &&
                                      status.error != Zhulong_ERR_CANCELLED;
    if (!fatal_error_recorded) {
        status = {Zhulong_STREAM_FAILED, error == Zhulong_OK ? Zhulong_ERR_CANCELLED : error};
    }
}

namespace {

/** @brief 宿主 UTC 毫秒时钟（单调性由 Stream 的 last_capture 提升保证）。 */
int64_t system_now_ms() noexcept {
    return std::chrono::duration_cast<std::chrono::milliseconds>(
               std::chrono::system_clock::now().time_since_epoch())
        .count();
}

} // namespace

int64_t Stream::advance_capture_locked() {
    last_capture_ms_ = std::max(system_now_ms(), last_capture_ms_);
    return last_capture_ms_;
}

int64_t Stream::advance_since_locked() {
    // +1 让“同一毫秒内改窗”也能把旧帧与新帧分到两侧；同时推进 last_capture，
    // 否则改窗后的第一帧仍可能拿到 < since 的时间戳。
    last_capture_ms_ = std::max(system_now_ms(), last_capture_ms_ + 1);
    view_since_ms_ = last_capture_ms_;
    return view_since_ms_;
}

Stream::ViewSnapshot Stream::view_snapshot(int frame_w, int frame_h) {
    std::lock_guard<std::mutex> lock(mutex);
    ViewSnapshot snapshot{};

    if (view_configured_) {
        const bool size_changed = !view_geometry_ready_ || frame_w != view_frame_w_ || frame_h != view_frame_h_;
        if (size_changed) {
            view_window_ = compute_view_window(view_rect_, frame_w, frame_h);
            view_frame_w_ = frame_w;
            view_frame_h_ = frame_h;
            view_geometry_ready_ = true;
            advance_since_locked();
        }
        snapshot.window = view_window_;
    }

    snapshot.capture_time_ms = advance_capture_locked();
    return snapshot;
}

Stream::ViewApplyResult Stream::set_view(bool clear, const ViewRect &rect) {
    std::lock_guard<std::mutex> lock(mutex);
    ViewApplyResult result{};

    if (clear || view_rect_is_full_frame(rect)) {
        // 幂等：从未配置过的流上重复清除不得推进边界，否则每次 acquire 都会制造
        // 一个假的“窗口切换点”，把在途帧误判成上一代。
        const bool was_configured = view_configured_;
        view_configured_ = false;
        view_rect_ = identity_view_rect();
        view_window_ = ViewWindow{};
        result.ready = true;
        result.rect = identity_view_rect();
        result.effective_since_ms = was_configured ? advance_since_locked() : view_since_ms_;
        return result;
    }

    const bool same = view_configured_ &&
                      view_rect_.x1 == rect.x1 && view_rect_.y1 == rect.y1 &&
                      view_rect_.x2 == rect.x2 && view_rect_.y2 == rect.y2;
    if (same) {
        result.ready = view_geometry_ready_;
        result.rect = view_geometry_ready_
                          ? window_to_view_rect(view_window_, view_frame_w_, view_frame_h_)
                          : identity_view_rect();
        result.effective_since_ms = view_geometry_ready_ ? view_since_ms_ : 0;
        return result;
    }

    view_configured_ = true;
    view_rect_ = rect;
    if (view_geometry_ready_ && view_frame_w_ >= 2 && view_frame_h_ >= 2) {
        view_window_ = compute_view_window(view_rect_, view_frame_w_, view_frame_h_);
        result.ready = true;
        result.rect = window_to_view_rect(view_window_, view_frame_w_, view_frame_h_);
        result.effective_since_ms = advance_since_locked();
        return result;
    }

    // 尚无解码帧尺寸：配置登记成功，但视图暂不生效（NOT_READY）。
    view_window_ = ViewWindow{};
    result.ready = false;
    result.rect = identity_view_rect();
    result.effective_since_ms = 0;
    return result;
}

void Stream::get_view(ViewRect *out_rect, int64_t *out_since_ms) const {
    std::lock_guard<std::mutex> lock(mutex);
    const bool effective = view_configured_ && view_geometry_ready_;
    if (out_rect != nullptr) {
        *out_rect = effective ? window_to_view_rect(view_window_, view_frame_w_, view_frame_h_)
                              : identity_view_rect();
    }
    if (out_since_ms != nullptr) {
        if (effective) {
            *out_since_ms = view_since_ms_;
        } else {
            // 未配置 → 沿用最后一次清除的边界；已配置但未就绪 → 0（与 NOT_READY 契约一致）。
            *out_since_ms = view_configured_ ? 0 : view_since_ms_;
        }
    }
}


} // namespace zhulong
