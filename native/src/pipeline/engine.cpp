/**
 * @file engine.cpp
 * @brief Zhulong Native 引擎核心控制器实现
 *
 * 核心实现说明：
 * 1. 锁外汇合与状态分离（R15）：
 *    - 在 release() 与 stop() 路径中，先在 control 锁内将退役的 session、publisher、broker 与 stream
 *      移动至外部临时容器，退出临界区后再执行 stop() 与 join()，杜绝锁内汇合导致全局流管理死锁；
 * 2. 唤醒纪元（wake_epoch）：
 *    - 维护 wake_epoch 单调递增计数器。每当有消费者释放并启动宽限期时递增并广播 timer_wakeup，
 *      配合 1 秒兜底超时，确保 Reaper 线程绝不会因条件变量时序丢失唤醒；
 * 3. 严格五步停机编排：
 *    - 生产者停止 ➔ 唤醒 Reaper ➔ 停止发布与 Broker ➔ 同步排空 AI 订阅 ➔ 汇合拉流线程与探测排空。
 */

#include "common/log.hpp"
#include "pipeline/engine.hpp"
#include "nodes/decode/mpp_decoder.hpp"

#include <algorithm>
#include <cstdlib>
#include <cstring>
#include <utility>
#include <vector>

namespace zhulong {

Engine::~Engine() {
    stop();
}

void Engine::start() {
    std::lock_guard<std::mutex> transition(lifecycle);
    std::lock_guard<std::mutex> lock(control);
    if (running) return;

    const char* env_sock = std::getenv("ZHULONG_FRAME_BROKER_SOCK");
    if (env_sock && *env_sock != '\0') {
        broker_base_dir = env_sock;
    } else {
        broker_base_dir = "/tmp";
    }

    reaper_stop.store(false);
    try {
        reaper = std::thread(&Engine::reap, this);
        running = true;
        ZL_LOGI(0, "pipeline.engine", "engine_started");
    } catch (...) {
        reaper_stop.store(true);
        throw;
    }
}

void Engine::stop() {
    std::lock_guard<std::mutex> transition(lifecycle);
    std::map<Zhulong_stream_id, std::shared_ptr<FramePublisher>> publishers_to_stop;
    std::map<Zhulong_stream_id, std::unique_ptr<ipc::FrameBroker>> brokers_to_stop;
    {
        std::lock_guard<std::mutex> lock(control);
        running = false;
        reaper_stop.store(true);
        for (const auto &entry : streams) {
            entry.second->request_stop();
        }
        publishers_to_stop.swap(publishers);
        brokers_to_stop.swap(brokers);
    }

    wake_reaper();
    stream_retired.notify_all();

    // 锁外停止 publisher 和 broker（在 join stream 之前，严格符合五步停机顺序）
    for (auto &entry : publishers_to_stop) {
        entry.second->stop();
    }
    publishers_to_stop.clear();

    for (auto &entry : brokers_to_stop) {
        entry.second->stop();
    }
    brokers_to_stop.clear();

    std::vector<std::shared_ptr<AISubscription>> ai_subs_to_drain;
    {
        std::lock_guard<std::mutex> lock(ai_sub_mutex);
        for (auto &entry : ai_subscriptions) {
            ai_subs_to_drain.push_back(entry.second);
        }
        ai_subscriptions.clear();
    }
    for (auto &sub : ai_subs_to_drain) {
        sub->disable_and_drain();
    }

    {
        std::lock_guard<std::mutex> lock(probe_mutex);
        for (const auto &entry : probes) {
            entry.second->stopped.store(true);
        }
    }

    if (reaper.joinable()) {
        reaper.join();
    }

    std::map<Zhulong_stream_id, std::shared_ptr<Stream>> retired;
    {
        std::lock_guard<std::mutex> lock(control);
        retired.swap(streams);
    }
    for (const auto &entry : retired) {
        entry.second->join();
    }

    std::unique_lock<std::mutex> lock(probe_mutex);
    probes_drained.wait(lock, [this] { return probes.empty(); });
    active_decode_sessions_.store(0);
    ZL_LOGI(0, "pipeline.engine", "engine_stopped");
}

void Engine::configure_decode(const Zhulong_decode_policy_t &policy) {
    std::lock_guard<std::mutex> operation(control);
    decode_policy_ = policy;
    ZL_LOGI(0, "pipeline.engine", "decode_policy_configured backend=%d allow_sw_fallback=%d max_sessions=%d",
            policy.backend, policy.allow_software_fallback, policy.max_sessions);
}

void Engine::get_decode_stats(Zhulong_decode_stats_t &out_stats) {
    std::lock_guard<std::mutex> operation(control);
    out_stats.active_sessions = static_cast<int32_t>(active_decode_sessions_.load());
    out_stats.max_sessions = decode_policy_.max_sessions;
    out_stats.rejected_requests = static_cast<int32_t>(rejected_requests_.load());
    out_stats.hardware_available = MppDecodeNode::is_supported() ? 1 : 0;
}

void Engine::wake_reaper() {
    {
        std::lock_guard<std::mutex> timer(timer_mutex);
        ++wake_epoch;
    }
    timer_wakeup.notify_all();
}

void Engine::reap() noexcept {
    while (!reaper_stop.load()) {
        // ⚠️ 线程入口必须自行收口异常：本函数是 noexcept，而回收扫描会分配
        //    （过期流快照 vector），任何逃出的异常都会直接 std::terminate 掉整个宿主。
        try {
            reap_once();
        } catch (...) {
            ZL_LOGE(0, "pipeline.engine", "reaper_exception");
            // 异常路径不经过条件变量等待，自己补一次有界休眠，避免持续 OOM 时空转烧 CPU。
            std::this_thread::sleep_for(std::chrono::seconds(1));
        }
    }
}

void Engine::reap_once() {
    // 兜底唤醒上限：即使出现未预期的状态变化，最迟 1 秒内重新扫描
    constexpr auto kMaxSleep = std::chrono::seconds(1);

    // 先记录唤醒纪元再扫描：扫描之后发生的任何变更都会让下面的等待立即返回
    uint64_t seen_epoch = 0;
    {
        std::lock_guard<std::mutex> timer(timer_mutex);
        seen_epoch = wake_epoch;
    }

    std::vector<std::shared_ptr<Stream>> expired;
    auto earliest = Clock::time_point::max();
    {
        std::lock_guard<std::mutex> lock(control);
        if (reaper_stop.load()) return;
        const auto now = Clock::now();
        for (const auto &entry : streams) {
            const auto &stream = entry.second;
            if (!stream->consumers.empty()) continue;
            if (now >= stream->expiry) {
                expired.push_back(stream);
            } else {
                earliest = std::min(earliest, stream->expiry);
            }
        }
    }

    if (!expired.empty()) {
        // 先统一发出停止信号，再逐个汇合：某一路 FFmpeg 取消较慢时不会延后其它流的停止
        for (const auto &stream : expired) stream->request_stop();
        for (const auto &stream : expired) {
            stream->join();
            {
                std::lock_guard<std::mutex> lock(control);
                streams.erase(stream->id);
            }
            ZL_LOGI(stream->id, "pipeline.engine", "stream_retired");
            stream_retired.notify_all();
        }
        return;
    }

    const auto now = Clock::now();
    const auto deadline = earliest == Clock::time_point::max() ? now + kMaxSleep
                                                               : std::min(earliest, now + kMaxSleep);
    std::unique_lock<std::mutex> timer(timer_mutex);
    timer_wakeup.wait_until(timer, deadline, [this, seen_epoch] {
        return reaper_stop.load() || wake_epoch != seen_epoch;
    });
}

std::shared_ptr<Stream> Engine::find_stream(Zhulong_stream_id id) {
    if (!running) throw Failure{Zhulong_ERR_NOT_RUNNING};
    const auto found = streams.find(id);
    if (found == streams.end()) throw Failure{Zhulong_ERR_NOT_FOUND};
    return found->second;
}

Zhulong_stream_id Engine::acquire(const std::string &url, const Options &options, uint64_t consumer, int32_t kind) {
    std::unique_lock<std::mutex> operation(control);

    // 1. 静态前置门控：若为 AI 消费且强制要求硬解，前置探测硬件是否就绪
    const bool requires_mpp = (decode_policy_.backend == Zhulong_DECODE_BACKEND_MPP) ||
                              (decode_policy_.backend == Zhulong_DECODE_BACKEND_AUTO && decode_policy_.allow_software_fallback == 0);
    if (kind == Zhulong_CONSUMER_AI && requires_mpp) {
        if (!MppDecodeNode::is_supported()) {
            rejected_requests_.fetch_add(1, std::memory_order_relaxed);
            ZL_LOGE(0, "pipeline.engine", "acquire_hardware_unavailable fail_closed url=%s", url.c_str());
            throw Failure{Zhulong_ERR_HARDWARE_UNAVAILABLE};
        }
    }

    for (;;) {
        if (!running) throw Failure{Zhulong_ERR_NOT_RUNNING};

        std::shared_ptr<Stream> existing;
        for (const auto &entry : streams) {
            if (entry.second->url == url) {
                existing = entry.second;
                break;
            }
        }

        if (!existing) break;

        if (existing->consumers.empty() && Clock::now() >= existing->expiry) {
            const auto id = existing->id;
            stream_retired.wait(operation, [this, id] {
                return !running || !streams.count(id);
            });
            continue;
        }

        if (!(existing->options == options)) throw Failure{Zhulong_ERR_CONFIG_CONFLICT};

        const bool was_decoding = existing->has_ai_consumer();
        const bool will_start_decode = (kind == Zhulong_CONSUMER_AI && !was_decoding);

        if (will_start_decode) {
            if (decode_policy_.max_sessions > 0 && active_decode_sessions_.load() >= static_cast<uint32_t>(decode_policy_.max_sessions)) {
                rejected_requests_.fetch_add(1, std::memory_order_relaxed);
                ZL_LOGW(existing->id, "pipeline.engine", "decode_capacity_exceeded upgrade active=%u max=%d",
                        active_decode_sessions_.load(), decode_policy_.max_sessions);
                throw Failure{Zhulong_ERR_CAPACITY_EXCEEDED};
            }
            active_decode_sessions_.fetch_add(1, std::memory_order_relaxed);
        }

        if (!existing->consumers.emplace(consumer, kind).second) {
            if (will_start_decode) {
                active_decode_sessions_.fetch_sub(1, std::memory_order_relaxed);
            }
            throw Failure{Zhulong_ERR_DUPLICATE};
        }

        existing->expiry = Clock::time_point::max();
        // 动态同步解码节点状态 (若新加入 AI 消费者则拉起解码器)
        std::shared_ptr<DecodeSession> session_to_stop;
        try {
            session_to_stop = existing->sync_decode_pipeline(decode_policy_);
        } catch (...) {
            existing->consumers.erase(consumer);
            if (will_start_decode) {
                active_decode_sessions_.fetch_sub(1, std::memory_order_relaxed);
            }
            throw;
        }

        // 若当前流包含 AI 消费且尚未创建 FrameBroker，则按需实例化并启动发布线程
        if (!ensure_frame_broker(existing, kind, [&] {
                existing->consumers.erase(consumer);
                if (will_start_decode) {
                    active_decode_sessions_.fetch_sub(1, std::memory_order_relaxed);
                }
                session_to_stop = existing->sync_decode_pipeline(decode_policy_);
            })) {
            operation.unlock();
            Stream::stop_decode_session(std::move(session_to_stop));
            throw Failure{Zhulong_ERR_IO};
        }

        operation.unlock();
        Stream::stop_decode_session(std::move(session_to_stop));
        return existing->id;
    }

    const bool is_new_decode = (kind == Zhulong_CONSUMER_AI);
    if (is_new_decode) {
        if (decode_policy_.max_sessions > 0 && active_decode_sessions_.load() >= static_cast<uint32_t>(decode_policy_.max_sessions)) {
            rejected_requests_.fetch_add(1, std::memory_order_relaxed);
            ZL_LOGW(0, "pipeline.engine", "decode_capacity_exceeded new_stream active=%u max=%d",
                    active_decode_sessions_.load(), decode_policy_.max_sessions);
            throw Failure{Zhulong_ERR_CAPACITY_EXCEEDED};
        }
        active_decode_sessions_.fetch_add(1, std::memory_order_relaxed);
    }

    if (!next_stream) {
        if (is_new_decode) active_decode_sessions_.fetch_sub(1, std::memory_order_relaxed);
        throw Failure{Zhulong_ERR_INTERNAL};
    }
    auto stream = std::make_shared<Stream>(next_stream++, url, options);
    stream->consumers.emplace(consumer, kind);
    streams.emplace(stream->id, stream);

    std::shared_ptr<Stream> stream_to_stop;
    try {
        stream->start();
    } catch (...) {
        ZL_LOGE(stream->id, "pipeline.engine", "stream_start_failed");
        streams.erase(stream->id);
        if (is_new_decode) active_decode_sessions_.fetch_sub(1, std::memory_order_relaxed);
        stream_to_stop = stream;
        operation.unlock();
        if (stream_to_stop) {
            stream_to_stop->request_stop();
            stream_to_stop->join();
        }
        throw;
    }

    if (!ensure_frame_broker(stream, kind, [&] {
            streams.erase(stream->id);
            if (is_new_decode) active_decode_sessions_.fetch_sub(1, std::memory_order_relaxed);
            stream_to_stop = stream;
        })) {
        operation.unlock();
        if (stream_to_stop) {
            stream_to_stop->request_stop();
            stream_to_stop->join();
        }
        throw Failure{Zhulong_ERR_IO};
    }

    return stream->id;
}

bool Engine::ensure_frame_broker(const std::shared_ptr<Stream> &stream, int32_t kind,
                                 const std::function<void()> &rollback) {
    if (kind != Zhulong_CONSUMER_AI) return true;
    if (brokers.count(stream->id)) return true;

    auto broker = std::make_unique<ipc::FrameBroker>();
    std::string err;
    if (!broker->start(stream->id, broker_base_dir, {}, &err)) {
        ZL_LOGE(stream->id, "pipeline.engine", "frame_broker_start_failed");
        if (rollback) rollback();
        return false;
    }

    broker->set_on_result([this](uint64_t stream_id, uint32_t /*client_id*/, const uint8_t* payload, size_t size) {
        this->dispatch_ai_result(stream_id, reinterpret_cast<const char*>(payload), size);
    });

    // ⚠️ publisher 持有 broker 的**裸指针**，任何退出路径都必须先停止 publisher（汇合发布线程）
    // 再让 broker 析构；正常路径由 release()/stop() 按 publisher → broker 顺序停止。
    // 下面「先启动再入池」能成立，靠的就是 catch 分支里那个停止顺序。
    auto crop = window_crop;
    auto publisher = std::make_shared<FramePublisher>(
        stream, broker.get(),
        [crop](const HardwareFrame &frame, const ViewWindow &view, uint8_t *dst,
               size_t dst_size, int dst_stride, int dst_dma_fd) {
            return crop->copy(frame, view, dst, dst_size, dst_stride, dst_dma_fd);
        });
    try {
        publisher->start();
    } catch (...) {
        ZL_LOGE(stream->id, "pipeline.engine", "frame_publisher_start_failed");
        broker->stop();
        if (rollback) rollback();
        return false;
    }

    try {
        brokers.emplace(stream->id, std::move(broker));
        publishers.emplace(stream->id, std::move(publisher));
    } catch (...) {
        ZL_LOGE(stream->id, "pipeline.engine", "broker_map_insert_failed");
        // ⚠️ 顺序不可调换：发布线程可能正在 broker_->is_running()/publish_frame()。
        // 先汇合发布线程，再让 broker 从 map 里析构；否则就是 use-after-free。
        if (publisher) {
            publisher->stop();
        }
        auto pub_it = publishers.find(stream->id);
        if (pub_it != publishers.end()) {
            pub_it->second->stop();
            publishers.erase(pub_it);
        }
        auto brk_it = brokers.find(stream->id);
        if (brk_it != brokers.end()) {
            brk_it->second->stop();
            brokers.erase(brk_it);
        }
        // 未入池的 broker 仍由局部 unique_ptr 拥有，其析构晚于局部 publisher，会自己 stop()
        if (rollback) rollback();
        return false;
    }
    return true;
}

void Engine::release(Zhulong_stream_id id, uint64_t consumer) {
    std::shared_ptr<DecodeSession> session_to_stop;
    std::shared_ptr<FramePublisher> publisher_to_stop;
    std::unique_ptr<ipc::FrameBroker> broker_to_stop;
    {
        std::lock_guard<std::mutex> operation(control);
        auto stream = find_stream(id);

        if (!stream->consumers.count(consumer)) throw Failure{Zhulong_ERR_NOT_FOUND};

        {
            std::lock_guard<std::mutex> lock(stream->mutex);
            for (const auto &entry : stream->subscriptions) {
                if (entry.second->consumer == consumer) {
                    throw Failure{Zhulong_ERR_BUSY};
                }
            }
        }

        const bool was_decoding = stream->has_ai_consumer();
        stream->consumers.erase(consumer);
        const bool still_decoding = stream->has_ai_consumer();
        if (was_decoding && !still_decoding) {
            if (active_decode_sessions_ > 0) {
                active_decode_sessions_.fetch_sub(1, std::memory_order_relaxed);
            }
        }
        // 动态同步解码节点状态 (获取需停止的会话指针，在锁外 join)
        session_to_stop = stream->sync_decode_pipeline(decode_policy_);

        // 当该流的全部 AI 消费者均已注销时，销毁其 FramePublisher 与 FrameBroker
        if (!stream->has_ai_consumer()) {
            auto pub_it = publishers.find(id);
            if (pub_it != publishers.end()) {
                publisher_to_stop = std::move(pub_it->second);
                publishers.erase(pub_it);
            }
            auto brk_it = brokers.find(id);
            if (brk_it != brokers.end()) {
                broker_to_stop = std::move(brk_it->second);
                brokers.erase(brk_it);
            }
        }

        if (stream->consumers.empty()) {
            stream->expiry = Clock::now() + get_stream_grace_period();
            ZL_LOGI(id, "pipeline.engine", "stream_idle");
            wake_reaper();
        }
    }
    // 锁外执行发布线程与 broker 的停止及汇合，随后停止解码会话
    if (publisher_to_stop) {
        publisher_to_stop->stop();
    }
    if (broker_to_stop) {
        broker_to_stop->stop();
    }
    Stream::stop_decode_session(std::move(session_to_stop));
    ZL_LOGI(id, "pipeline.engine", "stream_consumer_released");
}

Zhulong_stream_status Engine::status(Zhulong_stream_id id) {
    std::shared_ptr<Stream> stream;
    {
        std::lock_guard<std::mutex> operation(control);
        stream = find_stream(id);
    }
    // 状态轮询只需流自身的锁，不再与 acquire/release 争用 control
    std::lock_guard<std::mutex> lock(stream->mutex);
    return stream->status;
}

Zhulong_subscription_id Engine::subscribe(Zhulong_stream_id id, uint64_t consumer,
                                          Zhulong_packet_callback callback, uintptr_t token,
                                          uint32_t track_mask) {
    if ((track_mask & (ZHULONG_TRACK_MASK_VIDEO | ZHULONG_TRACK_MASK_AUDIO)) == 0) {
        throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
    }
    std::lock_guard<std::mutex> operation(control);
    auto stream = find_stream(id);

    const auto found = stream->consumers.find(consumer);
    if (found == stream->consumers.end()) throw Failure{Zhulong_ERR_NOT_FOUND};

    if (found->second != Zhulong_CONSUMER_PREVIEW) throw Failure{Zhulong_ERR_INVALID_ARGUMENT};

    std::lock_guard<std::mutex> lock(stream->mutex);
    for (const auto &entry : stream->subscriptions) {
        // 去重粒度是「消费者 + 轨道掩码」：同一预览消费者可以分别订阅视频与音频，
        // 但同一轨道集不能注册两条（那才是真正的重复，只会造成双份回调）。
        if (entry.second->consumer == consumer && entry.second->track_mask == track_mask) {
            throw Failure{Zhulong_ERR_DUPLICATE};
        }
    }

    if (!next_subscription) throw Failure{Zhulong_ERR_INTERNAL};
    const auto subscription = next_subscription++;
    auto created = std::make_shared<Subscription>(consumer, callback, token, track_mask);

    // ⚠️ 先构建「含新订阅」的快照，再一次性提交：快照分配失败时 map 与快照都没动，
    //    不会留下「已登记但不可派发」的孤儿订阅（那会让该消费者的 release 永远撞 BUSY）。
    auto snapshot = stream->make_subscriber_snapshot(created, 0);
    stream->subscriptions.emplace(subscription, std::move(created));
    stream->subscriber_snapshot_ = std::move(snapshot); // shared_ptr 移动赋值，不抛
    ZL_LOGI(id, "pipeline.engine", "packet_subscription_attached track_mask=%u", track_mask);
    return subscription;
}

void Engine::audio_info(Zhulong_stream_id id, Zhulong_audio_view *out_view) {
    if (!out_view) throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
    std::shared_ptr<Stream> stream;
    {
        std::lock_guard<std::mutex> operation(control);
        stream = find_stream(id);
    }
    std::lock_guard<std::mutex> lock(stream->mutex);
    if (stream->status.state != Zhulong_STREAM_RUNNING) {
        throw Failure{Zhulong_ERR_NOT_RUNNING};
    }
    out_view->has_audio = stream->audio_meta_.has_audio ? 1 : 0;
    out_view->codec = stream->audio_meta_.codec;
    out_view->sample_rate = stream->audio_meta_.sample_rate;
    out_view->channels = stream->audio_meta_.channels;

    const size_t req_size = out_view->has_audio ? stream->audio_meta_.extradata.size() : 0;
    out_view->extradata_size = req_size;
    if (req_size > 0) {
        if (!out_view->extradata || out_view->extradata_capacity < req_size) {
            throw Failure{Zhulong_ERR_BUFFER_TOO_SMALL};
        }
        std::memcpy(out_view->extradata, stream->audio_meta_.extradata.data(), req_size);
    }
}

void Engine::unsubscribe(Zhulong_stream_id id, Zhulong_subscription_id subscription) {
    std::unique_lock<std::mutex> operation(control);
    auto stream = find_stream(id);
    std::shared_ptr<Subscription> removed;

    {
        std::lock_guard<std::mutex> lock(stream->mutex);
        const auto found = stream->subscriptions.find(subscription);
        if (found == stream->subscriptions.end()) throw Failure{Zhulong_ERR_NOT_FOUND};
        // ⚠️ 先构建「不含该订阅」的快照，再提交删除：快照构建失败时直接抛出，
        //    订阅仍是完整登记状态（可重试），不会出现「已从 map 删除、却仍在旧快照里派发」
        //    且 disable_and_drain() 因异常未被调用的半注销状态。
        auto snapshot = stream->make_subscriber_snapshot(nullptr, subscription);
        removed = found->second;
        stream->subscriptions.erase(found);
        stream->subscriber_snapshot_ = std::move(snapshot);
    }

    operation.unlock();
    removed->disable_and_drain();
    ZL_LOGI(id, "pipeline.engine", "packet_subscription_released");
}

Zhulong_subscription_id Engine::subscribe_ai_results(Zhulong_stream_id id,
                                                    Zhulong_ai_result_callback callback,
                                                    uintptr_t token) {
    if (!callback) throw Failure{Zhulong_ERR_INVALID_ARGUMENT};
    std::lock_guard<std::mutex> operation(control);
    if (!running) throw Failure{Zhulong_ERR_NOT_RUNNING};
    auto stream = find_stream(id);

    std::lock_guard<std::mutex> lock(ai_sub_mutex);
    if (!next_subscription) throw Failure{Zhulong_ERR_INTERNAL};
    const auto subscription = next_subscription++;
    ai_subscriptions.emplace(subscription, std::make_shared<AISubscription>(id, callback, token));
    ZL_LOGI(id, "pipeline.engine", "ai_result_subscription_attached");
    return subscription;
}

void Engine::unsubscribe_ai_results(Zhulong_stream_id id, Zhulong_subscription_id subscription) {
    std::shared_ptr<AISubscription> removed;
    {
        std::lock_guard<std::mutex> lock(ai_sub_mutex);
        const auto found = ai_subscriptions.find(subscription);
        if (found == ai_subscriptions.end() || found->second->stream_id != id) {
            throw Failure{Zhulong_ERR_NOT_FOUND};
        }
        removed = found->second;
        ai_subscriptions.erase(found);
    }
    if (removed) {
        removed->disable_and_drain();
        ZL_LOGI(id, "pipeline.engine", "ai_result_subscription_released");
    }
}

void Engine::dispatch_ai_result(Zhulong_stream_id stream_id, const char* payload, size_t size) {
    if (!payload || size == 0) return;
    std::vector<std::shared_ptr<AISubscription>> targets;
    {
        std::lock_guard<std::mutex> lock(ai_sub_mutex);
        for (const auto& entry : ai_subscriptions) {
            if (entry.second->stream_id == stream_id) {
                targets.push_back(entry.second);
            }
        }
    }
    for (const auto& sub : targets) {
        try {
            sub->invoke(stream_id, payload, size);
        } catch (...) {
            ai_callback_exceptions_.fetch_add(1, std::memory_order_relaxed);
            ZL_LOGW(stream_id, "pipeline.engine", "ai_callback_exception");
        }
    }
}

Video Engine::probe(const std::string &url, const Options &options) {
    auto cancellation = std::make_shared<Cancellation>();
    {
        std::lock_guard<std::mutex> operation(control);
        if (!running) throw Failure{Zhulong_ERR_NOT_RUNNING};
        std::lock_guard<std::mutex> lock(probe_mutex);
        probes.emplace(cancellation.get(), cancellation);
    }

    struct Registration {
        Engine &engine;
        Cancellation *cancellation;
        ~Registration() {
            std::lock_guard<std::mutex> lock(engine.probe_mutex);
            engine.probes.erase(cancellation);
            engine.probes_drained.notify_all();
        }
    } registration{*this, cancellation.get()};

    return read_rtsp(url, options, *cancellation);
}

std::string Engine::get_broker_path(Zhulong_stream_id id) {
    std::lock_guard<std::mutex> lock(control);
    if (!running) throw Failure{Zhulong_ERR_NOT_RUNNING};
    auto it = brokers.find(id);
    if (it == brokers.end() || !it->second || !it->second->is_running()) {
        throw Failure{Zhulong_ERR_NOT_FOUND};
    }
    return it->second->socket_path();
}

uint32_t Engine::broker_client_stats(Zhulong_stream_id id, Zhulong_broker_client_stats *out,
                                   uint32_t capacity) {
    std::lock_guard<std::mutex> lock(control);
    if (!running) throw Failure{Zhulong_ERR_NOT_RUNNING};
    auto it = brokers.find(id);
    if (it == brokers.end() || !it->second || !it->second->is_running()) {
        throw Failure{Zhulong_ERR_NOT_FOUND};
    }

    const ipc::FrameBroker &broker = *it->second;
    const std::vector<ipc::ClientInfo> snapshot = broker.clients_snapshot();
    const uint32_t max_in_flight = broker.max_in_flight_per_client();

    // 防御：out == nullptr 时即使 capacity > 0 也不得写（契约只保证 capacity == 0 时可空）
    if (out == nullptr) {
        capacity = 0;
    }

    for (size_t i = 0; i < snapshot.size() && i < capacity; ++i) {
        Zhulong_broker_client_stats &slot = out[i];
        std::memset(&slot, 0, sizeof(slot));
        slot.client_id = snapshot[i].client_id;
        slot.in_flight = snapshot[i].in_flight_count;
        slot.max_in_flight = max_in_flight;
        slot.delivered_frames = snapshot[i].delivered_frames;
        slot.dropped_frames = snapshot[i].dropped_frames;
        // 定长拷贝 + 强制结尾：name 由另一端握手时的 strncpy 写入，这里不信任那个上限。
        std::strncpy(slot.name, snapshot[i].name.c_str(), sizeof(slot.name) - 1);
        slot.name[sizeof(slot.name) - 1] = '\0';
    }

    // 返回**总数**而不是写入数：调用方靠 out_count > capacity 判断是否被截断。
    return static_cast<uint32_t>(snapshot.size());
}

void Engine::set_publish_rate(Zhulong_stream_id id, uint32_t max_sample_fps) {
    std::lock_guard<std::mutex> lock(control);
    if (!running) throw Failure{Zhulong_ERR_NOT_RUNNING};
    auto it = publishers.find(id);
    if (it == publishers.end() || !it->second) {
        throw Failure{Zhulong_ERR_NOT_FOUND};
    }
    it->second->set_max_sample_fps(max_sample_fps);
}

void Engine::set_view(Zhulong_stream_id id, bool clear, const ViewRect &rect,
                      ViewRect *out_rect, int64_t *out_since_ms) {
    std::lock_guard<std::mutex> lock(control);
    auto stream = find_stream(id);
    // 锁顺序与 release() 一致：control -> Stream::mutex。
    auto result = stream->set_view(clear, rect);
    if (!result.ready) {
        throw Failure{Zhulong_ERR_NOT_READY};
    }
    if (out_rect != nullptr) {
        *out_rect = result.rect;
    }
    if (out_since_ms != nullptr) {
        *out_since_ms = result.effective_since_ms;
    }
}

void Engine::get_view(Zhulong_stream_id id, ViewRect *out_rect, int64_t *out_since_ms) {
    std::lock_guard<std::mutex> lock(control);
    auto stream = find_stream(id);
    stream->get_view(out_rect, out_since_ms);
}

HardwareFrame::ConstPtr Engine::latest_frame(Zhulong_stream_id id) {
    return find_frame(id, 0);
}

HardwareFrame::ConstPtr Engine::find_frame(Zhulong_stream_id id, int64_t pts) {
    std::shared_ptr<FramePublisher> publisher;
    {
        std::lock_guard<std::mutex> lock(control);
        if (!running) throw Failure{Zhulong_ERR_NOT_RUNNING};
        auto it = publishers.find(id);
        if (it == publishers.end() || !it->second) {
            // 无 AI 消费者就没有 FramePublisher，也就没有已解码的帧可供抓拍。
            // 不自行拉起拉流：那是 acquire() 的职责，默默拉流会引来无法解释的带宽消耗。
            throw Failure{Zhulong_ERR_NOT_FOUND};
        }
        publisher = it->second;
    }
    // 锁外读取：publisher 由 shared_ptr 保活，其内部短锁与 control 无关
    return publisher->find_frame(pts);
}

Zhulong_status_t Engine::broadcast_ai_message(Zhulong_stream_id stream_id, uint16_t msg_type, const void* payload, size_t payload_size) {
    std::lock_guard<std::mutex> lock(control);
    if (!running) return Zhulong_ERR_NOT_RUNNING;

    if (stream_id != 0) {
        auto it = brokers.find(stream_id);
        if (it == brokers.end() || !it->second || !it->second->is_running()) {
            return Zhulong_ERR_NOT_FOUND;
        }
        it->second->broadcast_message(msg_type, payload, payload_size);
        return Zhulong_OK;
    }

    // stream_id == 0: 广播给所有活跃流的 Broker
    for (auto& [id, broker] : brokers) {
        if (broker && broker->is_running()) {
            broker->broadcast_message(msg_type, payload, payload_size);
        }
    }
    return Zhulong_OK;
}

} // namespace zhulong
