/**
 * @file rtsp_integration_test.cpp
 * @brief Zhulong RTSP 集成测试集（配合 Python RTSP 真实循环回路服务）
 *
 * 测试覆盖范围：
 * 1. 独立流探测（Probe）：
 *    - H.264 / H.265 元数据、SPS/PPS 提取、超时响应（stall-open / stall-info）、凭据脱敏防泄露；
 *    - 探测结果生命周期独立性：验证探测结果在引擎销毁后依然有效可用。
 * 2. 物理流池化与多路复用（Pool Multiplexing）：
 *    - 相同 URL 物理连接复用；参数冲突拦截；消费者引用计数；
 *    - 8 秒宽限期机制：最后一个消费者释放后进入宽限期，在宽限期内重新连接复用连接；宽限期超时后平滑关闭。
 * 3. 数据包派发与时间戳保真：
 *    - 时间戳严格遵循 RTP 滴答（90kHz 步长 3600），绝不篡改重写为系统墙上时钟；
 *    - 关键帧标识正确性。
 * 4. 同步排空（Drain）与非阻塞流池：
 *    - 慢消费者阻塞时，unsubscribe 或 stop 准确等待排空，且等待期间不占用流池 control 全局锁；
 *    - 快照派发隔离：慢消费者不阻碍其他消费者的注销排空。
 * 5. 异常防护与协作式取消快速响应。
 */

#include "Zhulong/engine.h"

#include <atomic>
#include <chrono>
#include <condition_variable>
#include <future>
#include <iostream>
#include <mutex>
#include <stdexcept>
#include <string>
#include <thread>
#include <vector>

using Clock = std::chrono::steady_clock;
using namespace std::chrono_literals;

#define CHECK(condition) do { \
    if (!(condition)) throw std::runtime_error("check failed at line " + std::to_string(__LINE__) + ": " #condition); \
} while (false)

/** @brief RAII 管理的引擎生命周期测试辅助对象 */
struct EngineOwner {
    Zhulong_engine_h engine = nullptr;
    EngineOwner() {
        CHECK(Zhulong_engine_create(&engine) == Zhulong_OK);
        CHECK(Zhulong_engine_start(engine) == Zhulong_OK);
    }
    ~EngineOwner() {
        Zhulong_engine_destroy(engine);
    }
};

/** @brief RAII 管理的探测结果测试辅助对象 */
struct ResultOwner {
    Zhulong_probe_result_h result = nullptr;
    ~ResultOwner() {
        Zhulong_probe_result_destroy(result);
    }
};

/** @brief 等待断言成立的轮询辅助函数 */
template <typename Predicate>
void wait_until(Predicate predicate, std::chrono::milliseconds timeout = 4000ms) {
    const auto deadline = Clock::now() + timeout;
    while (!predicate()) {
        CHECK(Clock::now() < deadline);
        std::this_thread::sleep_for(10ms);
    }
}

/** @brief 回调状态记录与排空验证结构 */
struct CallbackState {
    Zhulong_engine_h engine = nullptr;
    int32_t expected_codec = Zhulong_CODEC_H264;
    std::atomic<int> count{0};
    std::atomic<bool> invalid{false};
    std::atomic<int> key_frames{0};
    std::mutex mutex;
    std::condition_variable changed;
    bool block = false;
    bool entered = false;
    bool leave = false;
    int64_t last_pts = 0;
    bool has_previous = false;
    int64_t pts_step = 0;
    std::vector<uint8_t> copied;
};

/** @brief 测试用数据包接收回调 */
void callback(uintptr_t token, const Zhulong_packet_view *packet) {
    auto &state = *reinterpret_cast<CallbackState *>(token);

    // 基础包视图有效性断言与自死锁检查（在回调中调用引擎控制接口必须返回 ERR_CALLBACK_CONTEXT）
    if (!packet || !packet->data || !packet->size || packet->codec != state.expected_codec ||
        packet->time_base_num != 1 || packet->time_base_den != 90000 ||
        (!packet->has_pts && packet->pts != 0) || (!packet->has_dts && packet->dts != 0) ||
        Zhulong_engine_stop(state.engine) != Zhulong_ERR_CALLBACK_CONTEXT) {
        state.invalid.store(true);
    }

    // 记录 PTS 步进
    if (state.has_previous && packet->has_pts) {
        state.pts_step = packet->pts - state.last_pts;
    }
    state.last_pts = packet->pts;
    state.has_previous = packet->has_pts;
    state.copied.assign(packet->data, packet->data + packet->size);
    if (packet->key_frame) state.key_frames.fetch_add(1);
    state.count.fetch_add(1);

    // 慢消费者模拟逻辑（用于测试排空 wait）
    std::unique_lock<std::mutex> lock(state.mutex);
    if (state.block) {
        state.entered = true;
        state.changed.notify_all();
        if (!state.changed.wait_for(lock, 2s, [&] { return state.leave; })) {
            state.invalid.store(true);
        }
    }
}

/** @brief 测试探测接口各用例（编码格式、扩展参数集、超时与凭据保护） */
void probe_cases(Zhulong_engine_h engine, const std::string &base) {
    for (const auto &path : {"/h264", "/h265", "/large-extra", "/stall-close"}) {
        ResultOwner result;
        const auto start = Clock::now();
        CHECK(Zhulong_engine_probe(engine, (base + path).c_str(), nullptr, &result.result) == Zhulong_OK);
        CHECK(Clock::now() - start < 4s);
        Zhulong_video_view view{};
        CHECK(Zhulong_probe_result_view(result.result, &view) == Zhulong_OK);
        CHECK(view.codec == (std::string(path) == "/h265" ? Zhulong_CODEC_H265 : Zhulong_CODEC_H264));
        CHECK(view.width == 64 && view.height == 48);
        CHECK(view.time_base_num == 1 && view.time_base_den == 90000);
        CHECK(view.fps_num >= 0 && view.fps_den > 0);
        CHECK(view.extradata && view.extradata_size > 0);
        if (std::string(path) == "/large-extra") CHECK(view.extradata_size > 512);
    }

    // 测试 UDP 探测
    Zhulong_stream_options udp{Zhulong_TRANSPORT_UDP, 4000, 1000};
    ResultOwner udp_result;
    CHECK(Zhulong_engine_probe(engine, (base + "/udp").c_str(), &udp, &udp_result.result) == Zhulong_OK);

    // 测试超时检测（stall-open / stall-info）
    for (const auto &path : {"/stall-open", "/stall-info"}) {
        Zhulong_stream_options options{Zhulong_TRANSPORT_TCP, 250, 5000};
        ResultOwner result;
        const auto start = Clock::now();
        CHECK(Zhulong_engine_probe(engine, (base + path).c_str(), &options, &result.result) == Zhulong_ERR_TIMEOUT);
        CHECK(!result.result);
        CHECK(Clock::now() - start < 1500ms);
    }

    // 凭据保护测试：含认证信息的 URL 在探测失败时不应泄漏凭据
    auto credentials = base;
    credentials.insert(7, "fixture-user:fixture-password@");
    Zhulong_stream_options options{Zhulong_TRANSPORT_TCP, 200, 500};
    ResultOwner result;
    const auto status = Zhulong_engine_probe(engine, (credentials + "/unsupported").c_str(), &options, &result.result);
    CHECK(status == Zhulong_ERR_UNSUPPORTED);
    CHECK(!result.result);
}

/** @brief 测试探测结果的独立生命周期（即使引擎已销毁，探测结果内存依然有效） */
void result_lifetime_case(const std::string &base) {
    ResultOwner result;
    std::vector<uint8_t> copied;
    {
        EngineOwner owner;
        CHECK(Zhulong_engine_probe(owner.engine, (base + "/result-lifetime").c_str(), nullptr,
                                   &result.result) == Zhulong_OK);
        Zhulong_video_view video{};
        CHECK(Zhulong_probe_result_view(result.result, &video) == Zhulong_OK);
        CHECK(video.extradata && video.extradata_size > 0);
        copied.assign(video.extradata, video.extradata + video.extradata_size);
    }
    // 引擎已在作用域末尾销毁，继续访问探测结果应当安全
    Zhulong_video_view video{};
    CHECK(Zhulong_probe_result_view(result.result, &video) == Zhulong_OK);
    CHECK(video.width == 64 && video.height == 48 && video.extradata_size > 0);
    CHECK(copied == std::vector<uint8_t>(video.extradata, video.extradata + video.extradata_size));
}

/** @brief 测试物理流池化复用、订阅、时间戳步长与 8 秒宽限期 */
void pool_cases(Zhulong_engine_h engine, const std::string &base) {
    Zhulong_stream_id first = 0, second = 0;
    const auto url = base + "/pool";

    // 1. 同一 URL 复用同一物理流 ID
    CHECK(Zhulong_stream_acquire(engine, url.c_str(), nullptr, 1, Zhulong_CONSUMER_PREVIEW, &first) == Zhulong_OK);
    auto normalized = "  RTSP" + url.substr(4) + "\n";
    CHECK(Zhulong_stream_acquire(engine, normalized.c_str(), nullptr, 2, Zhulong_CONSUMER_RECORDING, &second) == Zhulong_OK);
    CHECK(first == second);

    // 2. 重复注册同一个 consumer_id 拒绝
    CHECK(Zhulong_stream_acquire(engine, url.c_str(), nullptr, 1, Zhulong_CONSUMER_PREVIEW, &second) == Zhulong_ERR_DUPLICATE);
    CHECK(second == 0);

    // 3. 配置冲突拒绝（TCP vs UDP）
    Zhulong_stream_options udp{Zhulong_TRANSPORT_UDP, 0, 0};
    CHECK(Zhulong_stream_acquire(engine, url.c_str(), &udp, 3, Zhulong_CONSUMER_PREVIEW, &second) == Zhulong_ERR_CONFIG_CONFLICT);

    CallbackState state;
    state.engine = engine;
    Zhulong_subscription_id subscription = 0;

    // 4. 仅允许 PREVIEW 消费者订阅
    CHECK(Zhulong_stream_subscribe(engine, first, 2, callback, reinterpret_cast<uintptr_t>(&state), &subscription) == Zhulong_ERR_INVALID_ARGUMENT);
    CHECK(Zhulong_stream_subscribe(engine, first, 42, callback, 0, &subscription) == Zhulong_ERR_NOT_FOUND);

    wait_until([&] {
        Zhulong_stream_status status{};
        CHECK(Zhulong_stream_get_status(engine, first, &status) == Zhulong_OK);
        CHECK(status.state != Zhulong_STREAM_FAILED);
        return status.state == Zhulong_STREAM_RUNNING;
    });

    std::this_thread::sleep_for(100ms);
    CHECK(state.count.load() == 0); // 未订阅前不派发包

    // 5. 成功订阅并接收数据包
    CHECK(Zhulong_stream_subscribe(engine, first, 1, callback, reinterpret_cast<uintptr_t>(&state), &subscription) == Zhulong_OK);
    Zhulong_subscription_id duplicate = 1;
    CHECK(Zhulong_stream_subscribe(engine, first, 1, callback, 0, &duplicate) == Zhulong_ERR_DUPLICATE);
    CHECK(duplicate == 0);

    // 6. 存在活跃订阅时拒绝直接释放消费者（返回 ERR_BUSY）
    CHECK(Zhulong_stream_release(engine, first, 1) == Zhulong_ERR_BUSY);

    wait_until([&] { return state.count.load() >= 4; });

    // 7. 取消订阅排空验证
    CHECK(Zhulong_stream_unsubscribe(engine, first, subscription) == Zhulong_OK);
    const auto count = state.count.load();
    std::this_thread::sleep_for(100ms);
    CHECK(state.count.load() == count && !state.invalid.load());
    CHECK(state.pts_step == 3600); // 25fps RTP ticks (90000 / 25 = 3600)
    CHECK(state.key_frames.load() > 0);
    CHECK(!state.copied.empty());
    CHECK(Zhulong_stream_unsubscribe(engine, first, subscription) == Zhulong_ERR_NOT_FOUND);

    // 8. 并发 acquire / release 压力测试
    std::atomic<bool> failed{false};
    std::vector<std::thread> callers;
    for (uint64_t index = 10; index < 18; ++index) {
        callers.emplace_back([&, index] {
            for (int round = 0; round < 30; ++round) {
                Zhulong_stream_id id = 0;
                if (Zhulong_stream_acquire(engine, url.c_str(), nullptr, index, Zhulong_CONSUMER_AI, &id) != Zhulong_OK ||
                    id != first || Zhulong_stream_release(engine, id, index) != Zhulong_OK) {
                    failed.store(true);
                }
            }
        });
    }
    for (auto &thread : callers) thread.join();
    CHECK(!failed.load());

    // 9. 宽限期测试：释放全部消费者后进入 8 秒宽限期
    CHECK(Zhulong_stream_release(engine, first, 1) == Zhulong_OK);
    CHECK(Zhulong_stream_release(engine, first, 1) == Zhulong_ERR_NOT_FOUND);
    CHECK(Zhulong_stream_release(engine, first, 2) == Zhulong_OK);

    // 宽限期内重新连接：复用原有流句柄
    std::this_thread::sleep_for(150ms);
    CHECK(Zhulong_stream_acquire(engine, url.c_str(), nullptr, 3, Zhulong_CONSUMER_PREVIEW, &second) == Zhulong_OK);
    CHECK(second == first);
    CHECK(Zhulong_stream_release(engine, first, 3) == Zhulong_OK);

    // 等待超过 8 秒宽限期：底层流被 Reaper 回收
    std::this_thread::sleep_for(8200ms);
    Zhulong_stream_status status{};
    CHECK(Zhulong_stream_get_status(engine, first, &status) == Zhulong_ERR_NOT_FOUND);

    // 宽限期过后再次接入：重新分配全新物理流 ID
    CHECK(Zhulong_stream_acquire(engine, url.c_str(), nullptr, 4, Zhulong_CONSUMER_PREVIEW, &second) == Zhulong_OK);
    CHECK(first != second);
    wait_until([&] {
        CHECK(Zhulong_stream_get_status(engine, second, &status) == Zhulong_OK);
        return status.state == Zhulong_STREAM_RUNNING;
    });
}

/** @brief 测试 H.265 / HEVC 数据包接收与关键帧识别 */
void hevc_packet_case(Zhulong_engine_h engine, const std::string &base) {
    Zhulong_stream_id stream = 0;
    CHECK(Zhulong_stream_acquire(engine, (base + "/h265-packets").c_str(), nullptr, 1,
                                Zhulong_CONSUMER_PREVIEW, &stream) == Zhulong_OK);
    CallbackState state;
    state.engine = engine;
    state.expected_codec = Zhulong_CODEC_H265;
    Zhulong_subscription_id subscription = 0;
    CHECK(Zhulong_stream_subscribe(engine, stream, 1, callback, reinterpret_cast<uintptr_t>(&state), &subscription) == Zhulong_OK);
    wait_until([&] { return state.count.load() >= 4; });
    CHECK(Zhulong_stream_unsubscribe(engine, stream, subscription) == Zhulong_OK);
    CHECK(!state.invalid.load() && state.key_frames.load() > 0);
    CHECK(Zhulong_stream_release(engine, stream, 1) == Zhulong_OK);
}

/** @brief 测试慢消费者排空（unsubscribe / stop）且排空期间不阻塞其他并发查询 */
void drain_case(Zhulong_engine_h engine, const std::string &base, bool stop) {
    Zhulong_stream_id stream = 0;
    const auto url = base + (stop ? "/stop-drain" : "/unsubscribe-drain");
    CHECK(Zhulong_stream_acquire(engine, url.c_str(), nullptr, 1, Zhulong_CONSUMER_PREVIEW, &stream) == Zhulong_OK);

    CallbackState state;
    state.engine = engine;
    state.block = true;
    Zhulong_subscription_id subscription = 0;
    CHECK(Zhulong_stream_subscribe(engine, stream, 1, callback, reinterpret_cast<uintptr_t>(&state), &subscription) == Zhulong_OK);

    {
        std::unique_lock<std::mutex> lock(state.mutex);
        CHECK(state.changed.wait_for(lock, 4s, [&] { return state.entered; }));
    }

    // 启动异步排空调用
    auto drain = std::async(std::launch::async, [&] {
        return stop ? Zhulong_engine_stop(engine) : Zhulong_stream_unsubscribe(engine, stream, subscription);
    });

    // 确认排空处于阻塞等待状态
    CHECK(drain.wait_for(100ms) == std::future_status::timeout);

    // 关键断言：排空期间不得持有全局流池锁，其他线程的查询操作必须能够立即完成
    auto status_call = std::async(std::launch::async, [&] {
        Zhulong_stream_status status{};
        return Zhulong_stream_get_status(engine, stream, &status);
    });
    CHECK(status_call.wait_for(100ms) == std::future_status::ready);
    CHECK(status_call.get() == (stop ? Zhulong_ERR_NOT_RUNNING : Zhulong_OK));

    // 释放慢消费者阻塞，允许排空完成
    {
        std::lock_guard<std::mutex> lock(state.mutex);
        state.leave = true;
        state.changed.notify_all();
    }

    CHECK(drain.wait_for(1500ms) == std::future_status::ready);
    CHECK(drain.get() == Zhulong_OK);
    const auto count = state.count.load();
    std::this_thread::sleep_for(100ms);
    CHECK(state.count.load() == count && !state.invalid.load());
    if (!stop) CHECK(Zhulong_stream_release(engine, stream, 1) == Zhulong_OK);
}

/** @brief 测试快照派发机制下的独立排空隔离 */
void snapshot_drain_case(Zhulong_engine_h engine, const std::string &base) {
    Zhulong_stream_id stream = 0, same = 0;
    const auto url = base + "/snapshot-drain";
    CHECK(Zhulong_stream_acquire(engine, url.c_str(), nullptr, 1, Zhulong_CONSUMER_PREVIEW, &stream) == Zhulong_OK);
    CHECK(Zhulong_stream_acquire(engine, url.c_str(), nullptr, 2, Zhulong_CONSUMER_PREVIEW, &same) == Zhulong_OK);
    CHECK(stream == same);

    CallbackState first, second;
    first.engine = second.engine = engine;
    Zhulong_subscription_id first_id = 0, second_id = 0;
    CHECK(Zhulong_stream_subscribe(engine, stream, 1, callback, reinterpret_cast<uintptr_t>(&first), &first_id) == Zhulong_OK);
    CHECK(Zhulong_stream_subscribe(engine, stream, 2, callback, reinterpret_cast<uintptr_t>(&second), &second_id) == Zhulong_OK);

    wait_until([&] { return second.count.load() > 0; });
    {
        std::unique_lock<std::mutex> lock(first.mutex);
        first.block = true;
        CHECK(first.changed.wait_for(lock, 4s, [&] { return first.entered; }));
    }

    // 第一个消费者正在阻塞；此时注销第二个消费者，应当快速完成，不受第一个阻塞影响
    auto drain = std::async(std::launch::async, [&] {
        return Zhulong_stream_unsubscribe(engine, stream, second_id);
    });
    CHECK(drain.wait_for(200ms) == std::future_status::ready);
    CHECK(drain.get() == Zhulong_OK);

    const auto count = second.count.load();
    {
        std::lock_guard<std::mutex> lock(first.mutex);
        first.leave = true;
        first.changed.notify_all();
    }
    CHECK(Zhulong_stream_unsubscribe(engine, stream, first_id) == Zhulong_OK);
    std::this_thread::sleep_for(100ms);
    CHECK(second.count.load() == count && !first.invalid.load() && !second.invalid.load());
    CHECK(Zhulong_stream_release(engine, stream, 1) == Zhulong_OK);
    CHECK(Zhulong_stream_release(engine, stream, 2) == Zhulong_OK);
}

/** @brief 测试用户回调抛出异常时的异常封闭与流状态转移 */
void throwing_callback_case(Zhulong_engine_h engine, const std::string &base) {
    Zhulong_stream_id stream = 0;
    CHECK(Zhulong_stream_acquire(engine, (base + "/throwing-callback").c_str(), nullptr, 1,
                                Zhulong_CONSUMER_PREVIEW, &stream) == Zhulong_OK);
    Zhulong_subscription_id subscription = 0;
    CHECK(Zhulong_stream_subscribe(engine, stream, 1, [](uintptr_t, const Zhulong_packet_view *) {
        throw std::runtime_error("consumer exception must stay inside the worker");
    }, 0, &subscription) == Zhulong_OK);

    // 流应安全转移至 FAILED 状态，且错误码为 ERR_INTERNAL，绝不导致进程崩溃
    wait_until([&] {
        Zhulong_stream_status status{};
        CHECK(Zhulong_stream_get_status(engine, stream, &status) == Zhulong_OK);
        if (status.state != Zhulong_STREAM_FAILED) return false;
        CHECK(status.error == Zhulong_ERR_INTERNAL);
        return true;
    });

    CHECK(Zhulong_stream_unsubscribe(engine, stream, subscription) == Zhulong_OK);
    CHECK(Zhulong_stream_release(engine, stream, 1) == Zhulong_OK);
}

/** @brief 测试协作式取消与超时快速响应 */
void cancellation_cases(Zhulong_engine_h engine, const std::string &base) {
    for (const auto &path : {"/stall-open", "/stall-info"}) {
        CHECK(Zhulong_engine_start(engine) == Zhulong_OK);
        auto probe = std::async(std::launch::async, [&] {
            ResultOwner result;
            Zhulong_stream_options options{Zhulong_TRANSPORT_TCP, 10000, 10000};
            return Zhulong_engine_probe(engine, (base + path).c_str(), &options, &result.result);
        });
        std::this_thread::sleep_for(150ms);
        const auto start = Clock::now();
        CHECK(Zhulong_engine_stop(engine) == Zhulong_OK);
        CHECK(Clock::now() - start < 1500ms); // 验证在 1.5s 内快速打断并返回
        CHECK(probe.get() == Zhulong_ERR_CANCELLED);
    }

    CHECK(Zhulong_engine_start(engine) == Zhulong_OK);
    Zhulong_stream_id stream = 0;
    Zhulong_stream_options options{Zhulong_TRANSPORT_TCP, 4000, 300};
    CHECK(Zhulong_stream_acquire(engine, (base + "/stall-read").c_str(), &options, 1, Zhulong_CONSUMER_AI, &stream) == Zhulong_OK);

    // 帧读取空闲超时触发验证
    wait_until([&] {
        Zhulong_stream_status status{};
        CHECK(Zhulong_stream_get_status(engine, stream, &status) == Zhulong_OK);
        if (status.state != Zhulong_STREAM_FAILED) return false;
        CHECK(status.error == Zhulong_ERR_TIMEOUT);
        return true;
    }, 5000ms);

    CHECK(Zhulong_engine_stop(engine) == Zhulong_OK);
    CHECK(Zhulong_engine_start(engine) == Zhulong_OK);

    options.idle_timeout_ms = 10000;
    CHECK(Zhulong_stream_acquire(engine, (base + "/stall-read-stop").c_str(), &options, 1,
                                Zhulong_CONSUMER_AI, &stream) == Zhulong_OK);
    std::this_thread::sleep_for(2500ms);
    Zhulong_stream_status status{};
    CHECK(Zhulong_stream_get_status(engine, stream, &status) == Zhulong_OK);
    CHECK(status.state == Zhulong_STREAM_RUNNING);

    const auto stop_start = Clock::now();
    CHECK(Zhulong_engine_stop(engine) == Zhulong_OK);
    CHECK(Clock::now() - stop_start < 1500ms);

    // 多次连续取消压力测试
    for (int repeat = 0; repeat < 12; ++repeat) {
        CHECK(Zhulong_engine_start(engine) == Zhulong_OK);
        CHECK(Zhulong_stream_acquire(engine, (base + "/stall-open").c_str(), nullptr, 1, Zhulong_CONSUMER_AI, &stream) == Zhulong_OK);
        std::this_thread::sleep_for(5ms);
        const auto start = Clock::now();
        CHECK(Zhulong_engine_stop(engine) == Zhulong_OK);
        CHECK(Clock::now() - start < 1500ms);
    }
}

int main(int argc, char **argv) {
    try {
        CHECK(argc == 2);
        EngineOwner owner;
        const std::string base = argv[1];

        probe_cases(owner.engine, base);
        result_lifetime_case(base);
        pool_cases(owner.engine, base);
        hevc_packet_case(owner.engine, base);
        snapshot_drain_case(owner.engine, base);
        throwing_callback_case(owner.engine, base);
        drain_case(owner.engine, base, false);
        drain_case(owner.engine, base, true);
        cancellation_cases(owner.engine, base);

        std::cout << "probe, pool, packet clock, callback drain, cancellation and stress passed\n";
    } catch (const std::exception &error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
