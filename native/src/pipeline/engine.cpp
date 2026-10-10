/**
 * @file engine.cpp
 * @brief Zhulong Native 引擎核心控制器实现
 */

#include "pipeline/engine.hpp"

#include <utility>

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

    timer_wakeup.notify_all();
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

void Engine::reap() noexcept {
    while (!reaper_stop.load()) {
        {
            std::unique_lock<std::mutex> timer(timer_mutex);
            timer_wakeup.wait_for(timer, std::chrono::milliseconds(50), [this] {
                return reaper_stop.load();
            });
        }
        if (reaper_stop.load()) break;

        for (;;) {
            std::shared_ptr<Stream> expired;
            {
                std::lock_guard<std::mutex> lock(control);
                if (reaper_stop.load()) break;
                for (const auto &entry : streams) {
                    if (entry.second->consumers.empty() && Clock::now() >= entry.second->expiry) {
                        expired = entry.second;
                        break;
                    }
                }
            }
            if (!expired) break;

            expired->request_stop();
            expired->join();

            {
                std::lock_guard<std::mutex> lock(control);
                streams.erase(expired->id);
            }
            stream_retired.notify_all();
        }
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
        existing->sync_decode_pipeline();
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
    // 动态同步解码节点状态 (若已无 AI 消费者则休眠解码器)
    stream->sync_decode_pipeline();

    if (stream->consumers.empty()) {
        stream->expiry = Clock::now() + stream_grace_period;
    }
}

Zhulong_stream_status Engine::status(Zhulong_stream_id id) {
    std::lock_guard<std::mutex> operation(control);
    auto stream = find_stream(id);
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
