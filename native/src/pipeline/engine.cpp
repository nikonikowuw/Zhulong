/**
 * @file engine.cpp
 * @brief Zhulong Native 引擎核心管道（Pipeline）与流管理器实现
 *
 * 核心机制说明：
 * 1. 订阅者生命周期与同步排空（Drain）：
 *    - invoke 在执行用户回调前后维护 active 计数与 in_callback 标志；
 *    - disable_and_drain 标记 enabled=false 并等待 active 归零，确保返回后用户回调彻底终止。
 * 2. 物理流多路复用与 8 秒宽限期（Grace Period）：
 *    - 多个相同 URL 和参数的请求共享同一个物理流；
 *    - 当最后一个消费者释放后，流进入 8 秒宽限期，若宽限期内有新消费者接入则无缝恢复；
 *    - 若宽限期耗尽，后台 Reaper 线程负责平滑关闭并回收 Worker 线程。
 * 3. 锁粒度控制：
 *    - 严禁在持有 control 全局流池锁时进行 join() 或排空等待，杜绝死锁隐患。
 */

#include "engine.hpp"

#include <utility>

namespace zhulong {

// 线程局部变量初始化：默认不在回调上下文中
thread_local bool in_callback = false;

// ============================================================================
//                               Subscription 实现
// ============================================================================

void Subscription::invoke(const Zhulong_packet_view &packet) {
    // 快速加锁检查状态并标记 active
    {
        std::lock_guard<std::mutex> lock(mutex);
        if (!enabled) return;
        active = true;
    }

    // 设置线程局部标记，防止用户在回调中非法调用控制接口导致死锁
    in_callback = true;
    try {
        // 在锁外调用用户注册的回调函数，避免用户代码耗时阻塞分发通道
        callback(token, &packet);
    } catch (...) {
        // 异常防御：即使异常的用户回调抛出了 C++ 异常，也必须恢复状态并唤醒等待排空的线程
        in_callback = false;
        std::lock_guard<std::mutex> lock(mutex);
        active = false;
        enabled = false;
        drained.notify_all();
        throw;
    }

    // 正常退出：清理回调标记并通知排空条件变量
    in_callback = false;
    std::lock_guard<std::mutex> lock(mutex);
    active = false;
    drained.notify_all();
}

void Subscription::disable_and_drain() {
    std::unique_lock<std::mutex> lock(mutex);
    // 禁用后续派发
    enabled = false;
    // 阻塞等待当前正在执行的回调完全返回
    drained.wait(lock, [this] { return !active; });
}

// ============================================================================
//                                 Stream 实现
// ============================================================================

Stream::Stream(Zhulong_stream_id stream_id, std::string normalized_url, Options stream_options)
    : id(stream_id), url(std::move(normalized_url)), options(stream_options) {}

Stream::~Stream() {
    request_stop();
    join();
}

void Stream::start() {
    // 启动拉流 Worker 工作线程
    worker = std::thread(&Stream::capture, this);
}

void Stream::request_stop() {
    // 设置协作式取消标志，中断底层网络 IO 与循环
    cancel.stopped.store(true);
}

void Stream::join() {
    if (worker.joinable()) {
        worker.join();
    }
}

void Stream::dispatch(const Zhulong_packet_view &packet) {
    // 快照机制：在锁保护下复制当前的订阅者指针列表，随后立即释放互斥锁
    // 避免持有 stream->mutex 的情况下执行耗时的回调
    std::vector<std::shared_ptr<Subscription>> snapshot;
    {
        std::lock_guard<std::mutex> lock(mutex);
        for (const auto &entry : subscriptions) {
            snapshot.push_back(entry.second);
        }
    }

    // 在锁外逐个派发数据包
    for (const auto &subscription : snapshot) {
        if (cancel.stopped.load()) break;
        subscription->invoke(packet);
    }
}

void Stream::capture() noexcept {
    Zhulong_status_t error = Zhulong_OK;
    try {
        // 调用底层 RTSP 读取模块，传入包派发回调与连接就绪回调
        read_rtsp(url, options, cancel,
            [this](const Zhulong_packet_view &packet) {
                dispatch(packet);
            },
            [this] {
                // 连接成功且读取到流信息后，将状态置为 RUNNING
                std::lock_guard<std::mutex> lock(mutex);
                status.state = Zhulong_STREAM_RUNNING;
            });
    } catch (const Failure &failure) {
        error = failure.status;
    } catch (const std::bad_alloc &) {
        error = Zhulong_ERR_OUT_OF_MEMORY;
    } catch (...) {
        error = Zhulong_ERR_INTERNAL;
    }

    // 线程退出前更新流的终态信息
    std::lock_guard<std::mutex> lock(mutex);
    status = {Zhulong_STREAM_FAILED, error == Zhulong_OK ? Zhulong_ERR_CANCELLED : error};
}

// ============================================================================
//                                 Engine 实现
// ============================================================================

Engine::~Engine() {
    stop();
}

void Engine::start() {
    // 保证生命周期调用串行化
    std::lock_guard<std::mutex> transition(lifecycle);
    std::lock_guard<std::mutex> lock(control);
    if (running) return;

    reaper_stop.store(false);
    try {
        // 启动后台过期流清理线程
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
        // 请求停止所有物理流
        for (const auto &entry : streams) {
            entry.second->request_stop();
        }
    }

    // 唤醒可能阻塞在定时器或退休条件变量上的线程
    timer_wakeup.notify_all();
    stream_retired.notify_all();

    // 请求停止所有活跃的流探测任务
    {
        std::lock_guard<std::mutex> lock(probe_mutex);
        for (const auto &entry : probes) {
            entry.second->stopped.store(true);
        }
    }

    // 在接管流资源之前先回收 Reaper 线程，避免与 Reaper 竞争回收
    if (reaper.joinable()) {
        reaper.join();
    }

    // 交换出所有物理流并在释放 control 锁后执行 join（防止阻塞 control 锁）
    std::map<Zhulong_stream_id, std::shared_ptr<Stream>> retired;
    {
        std::lock_guard<std::mutex> lock(control);
        retired.swap(streams);
    }
    for (const auto &entry : retired) {
        entry.second->join();
    }

    // 等待所有探测（Probe）任务完全排空
    std::unique_lock<std::mutex> lock(probe_mutex);
    probes_drained.wait(lock, [this] { return probes.empty(); });
}

void Engine::reap() noexcept {
    while (!reaper_stop.load()) {
        {
            // 每 50ms 轮询一次，或收到立即唤醒通知
            std::unique_lock<std::mutex> timer(timer_mutex);
            timer_wakeup.wait_for(timer, std::chrono::milliseconds(50), [this] {
                return reaper_stop.load();
            });
        }
        if (reaper_stop.load()) break;

        // 持续查找并回收已过期的物理流
        for (;;) {
            std::shared_ptr<Stream> expired;
            {
                std::lock_guard<std::mutex> lock(control);
                if (reaper_stop.load()) break;
                for (const auto &entry : streams) {
                    // 若消费者列表为空且当前时间已达到或超过宽限期到期点
                    if (entry.second->consumers.empty() && Clock::now() >= entry.second->expiry) {
                        expired = entry.second;
                        break;
                    }
                }
            }
            if (!expired) break;

            // ⚠️ 极其关键：在持有 control 锁之外执行停止与 join，防止阻塞流池操作
            expired->request_stop();
            expired->join();

            {
                std::lock_guard<std::mutex> lock(control);
                streams.erase(expired->id);
            }
            // 唤醒可能正在等待该旧流完全释放的 acquire 操作
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

        // 检查是否存在相同 URL 的物理流
        std::shared_ptr<Stream> existing;
        for (const auto &entry : streams) {
            if (entry.second->url == url) {
                existing = entry.second;
                break;
            }
        }

        if (!existing) break;

        // 若流已无消费者且宽限期已过，等待 Reaper 彻底关闭该流后再重新创建，避免新旧连接冲突
        if (existing->consumers.empty() && Clock::now() >= existing->expiry) {
            const auto id = existing->id;
            stream_retired.wait(operation, [this, id] {
                return !running || !streams.count(id);
            });
            continue;
        }

        // 校验请求的 options 是否与已存在的物理流一致
        if (!(existing->options == options)) throw Failure{Zhulong_ERR_CONFIG_CONFLICT};

        // 消费者 ID 必须在当前流中唯一
        if (!existing->consumers.emplace(consumer, kind).second) {
            throw Failure{Zhulong_ERR_DUPLICATE};
        }

        // 重新激活流：取消宽限期倒计时，恢复物理流连接复用
        existing->expiry = Clock::time_point::max();
        return existing->id;
    }

    // 创建全新物理流
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

    // 释放消费者前，必须先取消该消费者注册的所有数据包订阅
    {
        std::lock_guard<std::mutex> lock(stream->mutex);
        for (const auto &entry : stream->subscriptions) {
            if (entry.second->consumer == consumer) {
                throw Failure{Zhulong_ERR_BUSY};
            }
        }
    }

    stream->consumers.erase(consumer);

    // 当流的所有消费者均已释放时，启动 8 秒宽限期倒计时
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

    // 确认消费者已在流中注册
    const auto found = stream->consumers.find(consumer);
    if (found == stream->consumers.end()) throw Failure{Zhulong_ERR_NOT_FOUND};

    // 仅允许预览类消费者订阅原始数据包
    if (found->second != Zhulong_CONSUMER_PREVIEW) throw Failure{Zhulong_ERR_INVALID_ARGUMENT};

    std::lock_guard<std::mutex> lock(stream->mutex);
    // 同一个流中同一消费者不可重复订阅
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

    // 释放流池锁，防止在执行排空等待期间阻塞其他流操作
    operation.unlock();

    // 执行同步排空：等待正在执行的用户回调彻底退出
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

    // RAII 清理守卫：探测结束时自动从活跃列表中移除并唤醒排空等待
    struct Registration {
        Engine &engine;
        Cancellation *cancellation;
        ~Registration() {
            std::lock_guard<std::mutex> lock(engine.probe_mutex);
            engine.probes.erase(cancellation);
            engine.probes_drained.notify_all();
        }
    } registration{*this, cancellation.get()};

    // 同步执行 RTSP 探测
    return read_rtsp(url, options, *cancellation);
}

} // namespace zhulong
