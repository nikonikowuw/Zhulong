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
 * 4. Broker 锁外启动：
 *    - broker 启动会阻塞拿目录锁并申请槽位内存，acquire 在 control 锁外完成启动、锁内提交；
 *      brokers_pending 保证同一流同时只有一个启动者（共用 shm 名与 socket 路径）。
 */

#include "common/log.hpp"
#include "pipeline/engine.hpp"
#include "nodes/decode/mpp_decoder.hpp"

#include <algorithm>
#include <cstdlib>
#include <cstring>
#include <limits>
#include <string>
#include <string_view>
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
    // 唤醒等待同流 broker 落定的 acquire：它们会看到 running == false 并退出
    broker_settled.notify_all();

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
    // 硬件探测首次会 dlopen MPP 库：放在 control 锁外，避免拖住全部控制操作
    const int32_t hardware_available = MppDecodeNode::is_supported() ? 1 : 0;
    std::lock_guard<std::mutex> operation(control);
    out_stats.active_sessions = static_cast<int32_t>(active_decode_sessions_.load());
    out_stats.max_sessions = decode_policy_.max_sessions;
    out_stats.rejected_requests = static_cast<int32_t>(rejected_requests_.load());
    out_stats.hardware_available = hardware_available;
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
    constexpr auto max_reaper_sleep = std::chrono::seconds(1);

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
    const auto deadline = earliest == Clock::time_point::max() ? now + max_reaper_sleep
                                                               : std::min(earliest, now + max_reaper_sleep);
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

namespace {

/** @brief 当前解码策略是否强制要求 MPP 硬解（不允许软解回退） */
bool policy_requires_mpp(const Zhulong_decode_policy_t &policy) noexcept {
    return policy.backend == Zhulong_DECODE_BACKEND_MPP ||
           (policy.backend == Zhulong_DECODE_BACKEND_AUTO && policy.allow_software_fallback == 0);
}

/** @brief 流是否已进入终态 FAILED（拉流线程已退出或正在退出，不得再复用） */
bool stream_failed(const Stream &stream) {
    std::lock_guard<std::mutex> lock(stream.mutex);
    return stream.status.state == Zhulong_STREAM_FAILED;
}

} // namespace

RetiredResources::~RetiredResources() {
    try {
        finish();
    } catch (...) {
        ZL_LOGE(0, "pipeline.engine", "retired_resources_finish_exception");
    }
}

void RetiredResources::finish() {
    // ⚠️ 顺序不可调换：publisher 持有 broker 裸指针，必须先汇合发布线程再停 broker
    if (publisher) {
        publisher->stop();
        publisher.reset();
    }
    if (broker) {
        broker->stop();
        broker.reset();
    }
    Stream::stop_decode_session(std::move(session));
    session.reset();
    if (stream_to_stop) {
        stream_to_stop->request_stop();
        stream_to_stop->join();
        stream_to_stop.reset();
    }
    // 最后才放开保活引用：此时本次退役的解码线程均已汇合
    keepalive.reset();
}

Zhulong_stream_id Engine::acquire(const std::string &url, const Options &options, uint64_t consumer, int32_t kind) {
    // ⚠️ 退役资源包必须声明在 control 锁守卫之前：其析构兜底 finish()（含 join），
    //    异常路径上也要保证先解锁、后汇合。
    RetiredResources discard;   // 本次多余/失败的 broker 与 publisher
    RetiredResources rollback;  // 回滚消费者注册而退役的资源
    std::shared_ptr<DecodeSession> session_to_stop;

    // 1. 静态前置门控：若为 AI 消费且强制要求硬解，前置探测硬件是否就绪。
    //    首次探测会 dlopen MPP 库，放在 control 锁外，不拖住其它流的控制操作。
    Zhulong_decode_policy_t admission_policy{};
    {
        std::lock_guard<std::mutex> lock(control);
        admission_policy = decode_policy_;
    }
    if (kind == Zhulong_CONSUMER_AI && policy_requires_mpp(admission_policy) && !MppDecodeNode::is_supported()) {
        rejected_requests_.fetch_add(1, std::memory_order_relaxed);
        ZL_LOGE(0, "pipeline.engine", "acquire_hardware_unavailable fail_closed url=%s", url.c_str());
        throw Failure{Zhulong_ERR_HARDWARE_UNAVAILABLE};
    }

    std::unique_lock<std::mutex> operation(control);
    std::shared_ptr<Stream> stream;
    bool created = false;

    for (;;) {
        if (!running) throw Failure{Zhulong_ERR_NOT_RUNNING};

        std::shared_ptr<Stream> existing;
        for (const auto &entry : streams) {
            if (entry.second->url != url) continue;
            // FAILED 是终态：拉流线程已退出（或熔断后正在退出）。复用它只会把新消费者挂到一条
            // 死流上；若另一消费者仍持有它，重新 acquire 的一方会反复拿到同一条死流。
            // 跳过它另建新流，旧流随其消费者全部释放后经宽限期回收。
            if (stream_failed(*entry.second)) continue;
            existing = entry.second;
            break;
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

        const auto previous_expiry = existing->expiry;
        existing->expiry = Clock::time_point::max();
        // 动态同步解码节点状态 (若新加入 AI 消费者则拉起解码器)
        try {
            session_to_stop = existing->sync_decode_pipeline(decode_policy_);
        } catch (...) {
            existing->consumers.erase(consumer);
            // 恢复宽限期：否则无消费者的流 expiry 停在 max，永远不会被 Reaper 回收
            existing->expiry = previous_expiry;
            if (will_start_decode) {
                active_decode_sessions_.fetch_sub(1, std::memory_order_relaxed);
            }
            throw;
        }
        stream = existing;
        break;
    }

    if (!stream) {
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
        auto fresh = std::make_shared<Stream>(next_stream++, url, options);
        fresh->consumers.emplace(consumer, kind);
        streams.emplace(fresh->id, fresh);

        try {
            // ⚠️ 必须传入全局解码策略：默认值会把「仅 MPP」静默降级为 AUTO，绕过 fail-closed
            fresh->start(decode_policy_);
        } catch (...) {
            ZL_LOGE(fresh->id, "pipeline.engine", "stream_start_failed");
            streams.erase(fresh->id);
            if (is_new_decode) active_decode_sessions_.fetch_sub(1, std::memory_order_relaxed);
            operation.unlock();
            fresh->request_stop();
            fresh->join();
            throw;
        }
        stream = std::move(fresh);
        created = true;
    }

    const auto id = stream->id;

    // 2. AI 消费者按需建立 FrameBroker。broker 启动要阻塞拿目录锁、申请槽位内存，
    //    只能在 control 锁外进行；同一流同时只允许一个启动者（brokers_pending），
    //    后到者等待前者落定后复查。
    bool start_broker = false;
    std::string base_dir;
    if (kind == Zhulong_CONSUMER_AI) {
        for (;;) {
            if (!running) {
                operation.unlock();
                Stream::stop_decode_session(std::move(session_to_stop));
                throw Failure{Zhulong_ERR_NOT_RUNNING};
            }
            const auto found = stream->consumers.find(consumer);
            if (found == stream->consumers.end()) {
                // 等待期间本消费者已被（违约的并发 release）注销，无需回滚
                operation.unlock();
                Stream::stop_decode_session(std::move(session_to_stop));
                throw Failure{Zhulong_ERR_NOT_FOUND};
            }
            if (brokers.count(id)) break;                 // 已有 broker（先行者已提交）
            if (!brokers_pending.count(id)) {
                start_broker = true;
                break;
            }
            broker_settled.wait(operation);
        }
    }

    if (!start_broker) {
        operation.unlock();
        Stream::stop_decode_session(std::move(session_to_stop));
        return id;
    }

    try {
        brokers_pending.insert(id);
    } catch (...) {
        detach_consumer_locked(stream, consumer, created, rollback);
        operation.unlock();
        Stream::stop_decode_session(std::move(session_to_stop));
        rollback.finish();
        throw;
    }
    base_dir = broker_base_dir;
    operation.unlock();
    Stream::stop_decode_session(std::move(session_to_stop));

    std::unique_ptr<ipc::FrameBroker> broker = start_frame_broker(id, base_dir);

    operation.lock();
    brokers_pending.erase(id);
    broker_settled.notify_all();

    bool committed = false;
    Zhulong_status_t failure = Zhulong_ERR_IO;
    if (!running) {
        // 停机已在窗口内发生：stop() 负责流与池，本次只需丢弃自己启动的 broker
        discard.broker = std::move(broker);
        failure = Zhulong_ERR_NOT_RUNNING;
    } else if (!stream->consumers.count(consumer)) {
        discard.broker = std::move(broker);
        failure = Zhulong_ERR_NOT_FOUND;
    } else if (!broker) {
        detach_consumer_locked(stream, consumer, created, rollback);
    } else if (brokers.count(id)) {
        // 理论上 brokers_pending 已排除并发启动；保守起见，已有 broker 时丢弃自己的
        discard.broker = std::move(broker);
        committed = true;
    } else if (commit_frame_broker_locked(stream, broker, discard)) {
        committed = true;
    } else {
        detach_consumer_locked(stream, consumer, created, rollback);
    }
    operation.unlock();

    discard.finish();
    rollback.finish();
    if (!committed) throw Failure{failure};
    return id;
}

std::unique_ptr<ipc::FrameBroker> Engine::start_frame_broker(Zhulong_stream_id id, const std::string &base_dir) {
    try {
        auto broker = std::make_unique<ipc::FrameBroker>();
        // 结果回调在 start 之前登记：set_on_result 本身不加锁，start 后再写会与 broker 收包线程竞争
        broker->set_on_result([this](uint64_t stream_id, uint32_t /*client_id*/, const uint8_t* payload, size_t size) {
            this->dispatch_ai_result(stream_id, reinterpret_cast<const char*>(payload), size);
        });
        std::string err;
        if (!broker->start(id, base_dir, {}, &err)) {
            ZL_LOGE(id, "pipeline.engine", "frame_broker_start_failed");
            return nullptr;
        }
        return broker;
    } catch (...) {
        ZL_LOGE(id, "pipeline.engine", "frame_broker_start_exception");
        return nullptr;
    }
}

bool Engine::commit_frame_broker_locked(const std::shared_ptr<Stream> &stream,
                                        std::unique_ptr<ipc::FrameBroker> &broker,
                                        RetiredResources &discard) {
    const auto id = stream->id;
    // ⚠️ publisher 持有 broker 的**裸指针**：任何失败路径都把二者交给 discard，
    //    由其在锁外按 publisher → broker 的顺序停止（发布线程汇合不得发生在 control 锁内）。
    std::shared_ptr<FramePublisher> publisher;
    try {
        auto crop = window_crop;
        publisher = std::make_shared<FramePublisher>(
            stream, broker.get(),
            [crop](const HardwareFrame &frame, const ViewWindow &view, uint8_t *dst,
                   size_t dst_size, int dst_stride, int dst_dma_fd) {
                return crop->copy(frame, view, dst, dst_size, dst_stride, dst_dma_fd);
            });
        publisher->start();
    } catch (...) {
        ZL_LOGE(id, "pipeline.engine", "frame_publisher_start_failed");
        discard.publisher = std::move(publisher);
        discard.broker = std::move(broker);
        return false;
    }

    try {
        // map 节点分配失败时实参尚未被移动，broker 仍由局部持有
        brokers.emplace(id, std::move(broker));
    } catch (...) {
        ZL_LOGE(id, "pipeline.engine", "broker_map_insert_failed");
        discard.publisher = std::move(publisher);
        discard.broker = std::move(broker);
        return false;
    }

    try {
        publishers.emplace(id, publisher);
    } catch (...) {
        ZL_LOGE(id, "pipeline.engine", "broker_map_insert_failed");
        discard.publisher = std::move(publisher);
        auto brk_it = brokers.find(id);
        if (brk_it != brokers.end()) {
            discard.broker = std::move(brk_it->second);
            brokers.erase(brk_it);
        }
        return false;
    }
    return true;
}

void Engine::detach_consumer_locked(const std::shared_ptr<Stream> &stream, uint64_t consumer,
                                    bool retire_immediately, RetiredResources &out) {
    const auto id = stream->id;
    const bool was_decoding = stream->has_ai_consumer();
    stream->consumers.erase(consumer);
    if (was_decoding && !stream->has_ai_consumer() && active_decode_sessions_ > 0) {
        active_decode_sessions_.fetch_sub(1, std::memory_order_relaxed);
    }

    // 保活到 finish() 末尾：退役会话的熔断回调捕获的是 Stream 裸 this
    out.keepalive = stream;
    out.session = stream->sync_decode_pipeline(decode_policy_);

    // 当该流的全部 AI 消费者均已注销时，摘下其 FramePublisher 与 FrameBroker
    if (!stream->has_ai_consumer()) {
        auto pub_it = publishers.find(id);
        if (pub_it != publishers.end()) {
            out.publisher = std::move(pub_it->second);
            publishers.erase(pub_it);
        }
        auto brk_it = brokers.find(id);
        if (brk_it != brokers.end()) {
            out.broker = std::move(brk_it->second);
            brokers.erase(brk_it);
        }
    }

    if (stream->consumers.empty()) {
        if (retire_immediately) {
            streams.erase(id);
            out.stream_to_stop = stream;
        } else {
            stream->expiry = Clock::now() + get_stream_grace_period();
            ZL_LOGI(id, "pipeline.engine", "stream_idle");
            wake_reaper();
        }
    }
}

void Engine::release(Zhulong_stream_id id, uint64_t consumer) {
    // ⚠️ 声明在锁守卫之前：析构兜底 finish() 时 control 已释放
    RetiredResources retired;
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

        detach_consumer_locked(stream, consumer, false, retired);
    }
    // 锁外执行发布线程与 broker 的停止及汇合，随后停止解码会话
    retired.finish();
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

namespace {

constexpr bool is_json_ws(char ch) noexcept {
    return ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n';
}

bool parse_json_int64(const char *data, size_t size, size_t &pos, int64_t &out_val) noexcept {
    if (pos >= size) return false;
    bool negative = false;
    size_t cur = pos;
    if (data[cur] == '-') {
        negative = true;
        ++cur;
    }
    if (cur >= size || data[cur] < '0' || data[cur] > '9') {
        return false;
    }
    const uint64_t limit = negative
        ? (static_cast<uint64_t>(std::numeric_limits<int64_t>::max()) + 1ULL)
        : static_cast<uint64_t>(std::numeric_limits<int64_t>::max());
    uint64_t acc = 0;
    while (cur < size && data[cur] >= '0' && data[cur] <= '9') {
        const uint64_t digit = static_cast<uint64_t>(data[cur] - '0');
        if (acc > (limit - digit) / 10ULL) {
            return false;
        }
        acc = acc * 10ULL + digit;
        ++cur;
    }
    if (cur < size) {
        const char next = data[cur];
        if (!is_json_ws(next) && next != ',' && next != '}') {
            return false;
        }
    }
    if (negative) {
        if (acc == static_cast<uint64_t>(std::numeric_limits<int64_t>::max()) + 1ULL) {
            out_val = std::numeric_limits<int64_t>::min();
        } else {
            out_val = -static_cast<int64_t>(acc);
        }
    } else {
        out_val = static_cast<int64_t>(acc);
    }
    pos = cur;
    return true;
}

/**
 * @brief 在宿主侧为算法回传的顶层 JSON 补齐归一化预览时间戳 `pts_us`
 *
 * 算法包（`.zpk`）独立编译分发，IPC `FrameDispatchMsg` 固定 184 字节；
 * Runner 在 `MSG_RESULT` 中原样回传 `capture_time_ms` 与 `pts`，宿主据此从
 * `FramePublisher` 的轻量标量环反查 `PreviewNormalizer` 产出的 `pts_us` 并注入顶层 JSON，
 * 零改动兼容新旧算法包。
 */
bool enrich_ai_result_pts_us(const FramePublisher &publisher,
                             const char *payload, size_t size,
                             std::string &out) {
    size_t start = 0;
    while (start < size && is_json_ws(payload[start])) {
        ++start;
    }
    if (start >= size || payload[start] != '{') {
        return false;
    }
    size_t end = size;
    while (end > start && is_json_ws(payload[end - 1])) {
        --end;
    }
    if (end <= start + 1 || payload[end - 1] != '}') {
        return false;
    }

    int depth = 0;
    bool expecting_key = false;
    bool has_capture_time_ms = false;
    int64_t capture_time_ms = 0;
    bool has_raw_pts = false;
    int64_t raw_pts = 0;

    size_t pos = start;
    while (pos < end) {
        while (pos < end && is_json_ws(payload[pos])) {
            ++pos;
        }
        if (pos >= end) break;

        const char ch = payload[pos];
        if (ch == '"') {
            const size_t key_start = pos + 1;
            ++pos;
            bool has_escape = false;
            while (pos < end) {
                if (payload[pos] == '\\') {
                    has_escape = true;
                    pos += 2;
                    if (pos > end) return false;
                    continue;
                }
                if (payload[pos] == '"') {
                    break;
                }
                ++pos;
            }
            if (pos >= end || payload[pos] != '"') {
                return false;
            }
            const size_t key_end = pos;
            ++pos; // 跳过右引号

            if (depth == 1 && expecting_key) {
                while (pos < end && is_json_ws(payload[pos])) {
                    ++pos;
                }
                if (pos >= end || payload[pos] != ':') {
                    return false;
                }
                ++pos; // 跳过 ':'
                expecting_key = false;

                if (!has_escape) {
                    const std::string_view key(payload + key_start, key_end - key_start);
                    if (key == "pts_us") {
                        return false;
                    }
                    if (key == "capture_time_ms" || key == "pts") {
                        while (pos < end && is_json_ws(payload[pos])) {
                            ++pos;
                        }
                        int64_t parsed = 0;
                        if (parse_json_int64(payload, end, pos, parsed)) {
                            if (key == "capture_time_ms") {
                                has_capture_time_ms = true;
                                capture_time_ms = parsed;
                            } else {
                                has_raw_pts = true;
                                raw_pts = parsed;
                            }
                        }
                    }
                }
            }
            continue;
        }

        if (ch == '{') {
            ++depth;
            if (depth == 1) {
                expecting_key = true;
            }
            ++pos;
            continue;
        }
        if (ch == '[') {
            ++depth;
            ++pos;
            continue;
        }
        if (ch == '}' || ch == ']') {
            --depth;
            if (depth < 0) {
                return false;
            }
            if (depth == 0) {
                if (ch != '}' || pos != end - 1) {
                    return false;
                }
                ++pos;
                break;
            }
            ++pos;
            continue;
        }
        if (ch == ',') {
            if (depth == 1) {
                expecting_key = true;
            }
            ++pos;
            continue;
        }
        ++pos;
    }

    if (depth != 0 || (!has_capture_time_ms && !has_raw_pts)) {
        return false;
    }

    int64_t pts_us = 0;
    if (!publisher.resolve_pts_us(has_capture_time_ms, capture_time_ms,
                                  has_raw_pts, raw_pts, pts_us)) {
        return false;
    }

    const std::string suffix = ",\"pts_us\":" + std::to_string(pts_us);
    out.clear();
    out.reserve(size + suffix.size());
    out.append(payload, end - 1);
    out.append(suffix);
    out.append(payload + (end - 1), size - (end - 1));
    return true;
}

} // namespace

void Engine::dispatch_ai_result(Zhulong_stream_id stream_id, const char* payload, size_t size) {
    if (!payload || size == 0) return;

    std::shared_ptr<FramePublisher> publisher;
    {
        std::lock_guard<std::mutex> lock(control);
        const auto pub_it = publishers.find(stream_id);
        if (pub_it != publishers.end()) {
            publisher = pub_it->second;
        }
    }

    std::string enriched;
    const char *dispatch_data = payload;
    size_t dispatch_size = size;
    if (publisher) {
        try {
            if (enrich_ai_result_pts_us(*publisher, payload, size, enriched)) {
                dispatch_data = enriched.data();
                dispatch_size = enriched.size();
            }
        } catch (...) {
            // OOM 等异常降级保留原始 JSON，绝不中断 AI 结果派发
        }
    }

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
            sub->invoke(stream_id, dispatch_data, dispatch_size);
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
        Registration(Engine &eng, Cancellation *cancel) noexcept
            : engine(eng), cancellation(cancel) {}
        Registration(const Registration &) = delete;
        Registration &operator=(const Registration &) = delete;
        Registration(Registration &&) = delete;
        Registration &operator=(Registration &&) = delete;
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
