/**
 * @file engine.cpp
 * @brief Zhulong Native 引擎核心控制器实现
 */

#include "pipeline/engine.hpp"

#include <algorithm>
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
    {
        std::lock_guard<std::mutex> lock(control);
        running = false;
        reaper_stop.store(true);
        for (const auto &entry : streams) {
            entry.second->request_stop();
        }
    }

    wake_reaper();
    stream_retired.notify_all();

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
    return stream->id;
}

void Engine::release(Zhulong_stream_id id, uint64_t consumer) {
    std::shared_ptr<DecodeSession> session_to_stop;
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

        if (stream->consumers.empty()) {
            stream->expiry = Clock::now() + stream_grace_period;
            wake_reaper();
        }
    }
    // 锁外执行解码线程的停止与汇合，消除跨流死锁与阻塞
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

} // namespace zhulong
