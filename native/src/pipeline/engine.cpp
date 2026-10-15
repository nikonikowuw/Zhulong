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

#include "pipeline/engine.hpp"

#include <algorithm>
#include <cstdlib>
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
    } catch (...) {
        reaper_stop.store(true);
        throw;
    }
}

void Engine::stop() {
    std::lock_guard<std::mutex> transition(lifecycle);
    std::map<Zhulong_stream_id, std::unique_ptr<FramePublisher>> publishers_to_stop;
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
}

void Engine::wake_reaper() {
    {
        std::lock_guard<std::mutex> timer(timer_mutex);
        ++wake_epoch;
    }
    timer_wakeup.notify_all();
}

void Engine::reap() noexcept {
    // 兜底唤醒上限：即使出现未预期的状态变化，最迟 1 秒内重新扫描
    constexpr auto kMaxSleep = std::chrono::seconds(1);

    while (!reaper_stop.load()) {
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
            if (reaper_stop.load()) break;
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
                stream_retired.notify_all();
            }
            continue;
        }

        const auto now = Clock::now();
        const auto deadline = earliest == Clock::time_point::max() ? now + kMaxSleep
                                                                   : std::min(earliest, now + kMaxSleep);
        std::unique_lock<std::mutex> timer(timer_mutex);
        timer_wakeup.wait_until(timer, deadline, [this, seen_epoch] {
            return reaper_stop.load() || wake_epoch != seen_epoch;
        });
    }
}

std::shared_ptr<Stream> Engine::find_stream(Zhulong_stream_id id) {
    if (!running) throw Failure{Zhulong_ERR_NOT_RUNNING};
    const auto found = streams.find(id);
    if (found == streams.end()) throw Failure{Zhulong_ERR_NOT_FOUND};
    return found->second;
}

Zhulong_stream_id Engine::acquire(const std::string &url, const Options &options, uint64_t consumer, int32_t kind) {
    std::unique_lock<std::mutex> operation(control);
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

        if (!existing->consumers.emplace(consumer, kind).second) {
            throw Failure{Zhulong_ERR_DUPLICATE};
        }

        existing->expiry = Clock::time_point::max();
        // 动态同步解码节点状态 (若新加入 AI 消费者则拉起解码器)
        try {
            existing->sync_decode_pipeline();
        } catch (...) {
            existing->consumers.erase(consumer);
            throw;
        }

        // 若当前流包含 AI 消费且尚未创建 FrameBroker，则按需实例化并启动发布线程
        if (!ensure_frame_broker(existing, kind, [&] {
                existing->consumers.erase(consumer);
                existing->sync_decode_pipeline();
            })) {
            throw Failure{Zhulong_ERR_IO};
        }

        return existing->id;
    }

    if (!next_stream) throw Failure{Zhulong_ERR_INTERNAL};
    auto stream = std::make_shared<Stream>(next_stream++, url, options);
    stream->consumers.emplace(consumer, kind);
    streams.emplace(stream->id, stream);

    try {
        stream->start();
    } catch (...) {
        streams.erase(stream->id);
        throw;
    }

    if (!ensure_frame_broker(stream, kind, [&] {
            stream->request_stop();
            streams.erase(stream->id);
        })) {
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
        if (rollback) rollback();
        return false;
    }

    broker->set_on_result([this](uint64_t stream_id, uint32_t /*client_id*/, const uint8_t* payload, size_t size) {
        this->dispatch_ai_result(stream_id, reinterpret_cast<const char*>(payload), size);
    });

    // ⚠️ publisher 持有 broker 的裸指针，因此必须在 broker 入池之后再启动，
    // 且两者在 release()/stop() 中按 publisher → broker 的顺序停止。
    auto publisher = std::make_unique<FramePublisher>(stream, broker.get());
    publisher->start();
    brokers.emplace(stream->id, std::move(broker));
    publishers.emplace(stream->id, std::move(publisher));
    return true;
}

void Engine::release(Zhulong_stream_id id, uint64_t consumer) {
    std::shared_ptr<DecodeSession> session_to_stop;
    std::unique_ptr<FramePublisher> publisher_to_stop;
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

        stream->consumers.erase(consumer);
        // 动态同步解码节点状态 (获取需停止的会话指针，在锁外 join)
        session_to_stop = stream->sync_decode_pipeline();

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
            stream->expiry = Clock::now() + stream_grace_period;
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
                                          Zhulong_packet_callback callback, uintptr_t token) {
    std::lock_guard<std::mutex> operation(control);
    auto stream = find_stream(id);

    const auto found = stream->consumers.find(consumer);
    if (found == stream->consumers.end()) throw Failure{Zhulong_ERR_NOT_FOUND};

    if (found->second != Zhulong_CONSUMER_PREVIEW) throw Failure{Zhulong_ERR_INVALID_ARGUMENT};

    std::lock_guard<std::mutex> lock(stream->mutex);
    for (const auto &entry : stream->subscriptions) {
        if (entry.second->consumer == consumer) {
            throw Failure{Zhulong_ERR_DUPLICATE};
        }
    }

    if (!next_subscription) throw Failure{Zhulong_ERR_INTERNAL};
    const auto subscription = next_subscription++;
    stream->subscriptions.emplace(subscription, std::make_shared<Subscription>(consumer, callback, token));
    stream->rebuild_subscribers();
    return subscription;
}

void Engine::unsubscribe(Zhulong_stream_id id, Zhulong_subscription_id subscription) {
    std::unique_lock<std::mutex> operation(control);
    auto stream = find_stream(id);
    std::shared_ptr<Subscription> removed;

    {
        std::lock_guard<std::mutex> lock(stream->mutex);
        const auto found = stream->subscriptions.find(subscription);
        if (found == stream->subscriptions.end()) throw Failure{Zhulong_ERR_NOT_FOUND};
        removed = found->second;
        stream->subscriptions.erase(found);
        stream->rebuild_subscribers();
    }

    operation.unlock();
    removed->disable_and_drain();
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
        sub->invoke(stream_id, payload, size);
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

void Engine::set_publish_rate(Zhulong_stream_id id, uint32_t max_sample_fps) {
    std::lock_guard<std::mutex> lock(control);
    if (!running) throw Failure{Zhulong_ERR_NOT_RUNNING};
    auto it = publishers.find(id);
    if (it == publishers.end() || !it->second) {
        throw Failure{Zhulong_ERR_NOT_FOUND};
    }
    it->second->set_max_sample_fps(max_sample_fps);
}

} // namespace zhulong
