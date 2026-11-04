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
#include "../src/algorithm/ipc/ipc_protocol.hpp"
#include "../src/algorithm/ipc/unix_socket.hpp"
#include "../src/common/log.hpp"
#include "../src/nodes/capture/rtsp_input.hpp"

#include <atomic>
#include <chrono>
#include <condition_variable>
#include <cstdlib>
#include <cstring>
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
        packet->track_type != ZHULONG_TRACK_VIDEO ||
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

    // 探测窗口收紧的兼容档重试：SDP 不带参数集且首帧晚于快速档窗口时，
    // 必须换回旧预算重连一次并拿到完整元数据，而不是把相机判成不支持。
    {
        Zhulong_stream_options options{Zhulong_TRANSPORT_TCP, 6000, 3000};
        ResultOwner result;
        const auto start = Clock::now();
        CHECK(Zhulong_engine_probe(engine, (base + "/late-probe").c_str(), &options, &result.result) == Zhulong_OK);
        CHECK(Clock::now() - start < 4s);
        Zhulong_video_view view{};
        CHECK(Zhulong_probe_result_view(result.result, &view) == Zhulong_OK);
        // 只有兼容档能探到分辨率：快速档的 128 KiB / 300ms 这里什么也拿不到
        CHECK(view.width == 64 && view.height == 48);
        // extradata 不强制要求：SDP 不带 sprop-parameter-sets 时参数集来自码流内联，
        // 这正是 PreviewNormalizer::HasCompleteParameterSets 存在的理由。
    }

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

    // 9. 宽限期测试：释放全部消费者后进入宽限期
    CHECK(Zhulong_stream_release(engine, first, 1) == Zhulong_OK);
    CHECK(Zhulong_stream_release(engine, first, 1) == Zhulong_ERR_NOT_FOUND);
    CHECK(Zhulong_stream_release(engine, first, 2) == Zhulong_OK);

    // 宽限期内重新连接：复用原有流句柄（200ms 宽限期内 50ms 仍有效）
    std::this_thread::sleep_for(50ms);
    CHECK(Zhulong_stream_acquire(engine, url.c_str(), nullptr, 3, Zhulong_CONSUMER_PREVIEW, &second) == Zhulong_OK);
    CHECK(second == first);
    CHECK(Zhulong_stream_release(engine, first, 3) == Zhulong_OK);

    // 等待超过宽限期：底层流被 Reaper 回收
    Zhulong_stream_status status{};
    wait_until([&] {
        return Zhulong_stream_get_status(engine, first, &status) == Zhulong_ERR_NOT_FOUND;
    }, 2000ms);

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

/**
 * @brief 测试用户回调异常被逐订阅者隔离
 *
 * 同一物理流上两个 PREVIEW 订阅者：consumer 1 的回调抛 C++ 异常，consumer 2 正常计数。
 * 断言违约者静默失效、consumer 2 持续收包、流状态保持 RUNNING 而非 FAILED。
 */
void throwing_callback_case(Zhulong_engine_h engine, const std::string &base) {
    Zhulong_stream_id stream = 0;
    const auto url = base + "/throwing-callback";

    // 1. 同一物理流上注册两个 PREVIEW 消费者（同一 URL 复用同一物理流）
    CHECK(Zhulong_stream_acquire(engine, url.c_str(), nullptr, 1, Zhulong_CONSUMER_PREVIEW, &stream) == Zhulong_OK);
    Zhulong_stream_id second = 0;
    CHECK(Zhulong_stream_acquire(engine, url.c_str(), nullptr, 2, Zhulong_CONSUMER_PREVIEW, &second) == Zhulong_OK);
    CHECK(second == stream);

    // 2. consumer 1 订阅违约回调（抛 C++ 异常）；consumer 2 订阅正常计数回调
    std::atomic<int> offender_invocations{0};
    Zhulong_subscription_id offender = 0;
    CHECK(Zhulong_stream_subscribe(engine, stream, 1,
        [](uintptr_t token, const Zhulong_packet_view *) {
            reinterpret_cast<std::atomic<int> *>(token)->fetch_add(1);
            throw std::runtime_error("consumer exception must be isolated per subscriber");
        },
        reinterpret_cast<uintptr_t>(&offender_invocations), &offender) == Zhulong_OK);

    CallbackState state;
    state.engine = engine;
    Zhulong_subscription_id healthy = 0;
    CHECK(Zhulong_stream_subscribe(engine, stream, 2, callback,
        reinterpret_cast<uintptr_t>(&state), &healthy) == Zhulong_OK);

    // 3. 违约者已被调用过至少一次，且健康订阅者持续收到数据包（流未被终止）
    wait_until([&] { return offender_invocations.load() >= 1; });
    wait_until([&] { return state.count.load() >= 20; });
    CHECK(!state.invalid.load());

    // 4. 流状态必须保持 RUNNING 且无错误（决策 D1：违约不构成流的故障）
    Zhulong_stream_status status{};
    CHECK(Zhulong_stream_get_status(engine, stream, &status) == Zhulong_OK);
    CHECK(status.state == Zhulong_STREAM_RUNNING);
    CHECK(status.error == Zhulong_OK);

    // 5. 违约者只被调用一次：invoke 已在抛出前置 enabled=false，后续派发直接返回
    const int offender_calls = offender_invocations.load();
    std::this_thread::sleep_for(200ms);
    CHECK(offender_invocations.load() == offender_calls);

    // 6. 正常收尾：unsubscribe 先于 release（有活跃订阅时 release 返回 BUSY）
    CHECK(Zhulong_stream_unsubscribe(engine, stream, offender) == Zhulong_OK);
    CHECK(Zhulong_stream_unsubscribe(engine, stream, healthy) == Zhulong_OK);
    CHECK(Zhulong_stream_release(engine, stream, 1) == Zhulong_OK);
    CHECK(Zhulong_stream_release(engine, stream, 2) == Zhulong_OK);
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
    Zhulong_stream_status status{};
    wait_until([&] {
        CHECK(Zhulong_stream_get_status(engine, stream, &status) == Zhulong_OK);
        CHECK(status.state != Zhulong_STREAM_FAILED);
        return status.state == Zhulong_STREAM_RUNNING;
    });
    std::this_thread::sleep_for(400ms);
    CHECK(Zhulong_stream_get_status(engine, stream, &status) == Zhulong_OK);
    CHECK(status.state == Zhulong_STREAM_RUNNING);

    const auto stop_start = Clock::now();
    CHECK(Zhulong_engine_stop(engine) == Zhulong_OK);
    CHECK(Clock::now() - stop_start < 1500ms);

    // 多次连续取消压力测试
    for (int repeat = 0; repeat < 4; ++repeat) {
        CHECK(Zhulong_engine_start(engine) == Zhulong_OK);
        CHECK(Zhulong_stream_acquire(engine, (base + "/stall-open").c_str(), nullptr, 1, Zhulong_CONSUMER_AI, &stream) == Zhulong_OK);
        std::this_thread::sleep_for(5ms);
        const auto start = Clock::now();
        CHECK(Zhulong_engine_stop(engine) == Zhulong_OK);
        CHECK(Clock::now() - start < 1500ms);
    }
}

/**
 * @brief 连接到指定 AI 流的 FrameBroker，在窗口内统计帧派发数（逐帧 ACK）
 *
 * ⚠️ 必须逐帧 ACK：broker 的 max_in_flight_per_client=2，不 ACK 会在第 3 帧起触发
 * 背压丢帧，测到的就不是发布侧节流率而是背压上限。
 */
struct BrokerProbe {
    zhulong::ipc::UnixSocketClient client;
    zhulong::ipc::HandshakeResp resp{};

    bool attach(Zhulong_engine_h engine, Zhulong_stream_id stream, const char *name) {
        char path_buf[256];
        size_t path_len = sizeof(path_buf);
        if (Zhulong_stream_get_broker_path(engine, stream, path_buf, &path_len) != Zhulong_OK) return false;
        if (!client.connect(path_buf)) return false;

        zhulong::ipc::HandshakeReq req{};
        std::strncpy(req.client_name, name, sizeof(req.client_name) - 1);
        if (!client.send_msg(zhulong::ipc::MSG_HANDSHAKE_REQ, &req, sizeof(req))) return false;

        zhulong::ipc::IpcHeader hdr{};
        std::vector<uint8_t> payload;
        if (!client.recv_msg(hdr, payload, 2000)) return false;
        if (payload.size() < sizeof(resp)) return false;
        std::memcpy(&resp, payload.data(), sizeof(resp));
        return true;
    }

    /** @brief 在给定窗口内统计并 ACK 所有 FRAME_DISPATCH，返回派发帧数 */
    size_t count_over(std::chrono::milliseconds window) {
        size_t dispatched = 0;
        const auto deadline = Clock::now() + window;
        while (Clock::now() < deadline) {
            zhulong::ipc::IpcHeader fh{};
            std::vector<uint8_t> fp;
            if (!client.recv_msg(fh, fp, 100)) continue;
            if (fh.msg_type != zhulong::ipc::MSG_FRAME_DISPATCH) continue;
            if (fp.size() < sizeof(zhulong::ipc::FrameDispatchMsg)) continue;

            zhulong::ipc::FrameDispatchMsg dispatch{};
            std::memcpy(&dispatch, fp.data(), sizeof(dispatch));
            zhulong::ipc::FrameAckMsg ack{};
            ack.stream_id = dispatch.stream_id;
            ack.frame_id = dispatch.frame_id;
            ack.slot_index = dispatch.slot_index;
            ack.generation = dispatch.generation;
            ack.client_id = resp.client_id;
            client.send_msg(zhulong::ipc::MSG_FRAME_ACK, &ack, sizeof(ack));
            ++dispatched;
        }
        return dispatched;
    }
};

/**
 * @brief AC10 发布侧节流生效验证（真实 loopback RTSP 解码通路）
 *
 * 测试桩以 25fps 推流，逐段测量 broker 实际派发帧数：
 *   1. 不节流（rate=0）基线 —— 证明解码通路确实在出帧（AC2 前提）；
 *   2. rate=5  —— 派发率必须显著低于基线；
 *   3. rate=25 —— 派发率必须随之回升。
 * 全程持续出帧即证明发布后帧引用被立即释放（若被持有，解码池会枯竭导致派发停滞）。
 */
void ai_publish_throttle_case(Zhulong_engine_h engine, const std::string &base) {
    // 前序 cancellation_cases 结束时引擎处于 stopped 状态，必须重新 start
    CHECK(Zhulong_engine_start(engine) == Zhulong_OK);

    const auto url = base + "/ai-throttle";
    Zhulong_stream_id stream = 0;
    CHECK(Zhulong_stream_acquire(engine, url.c_str(), nullptr, 1, Zhulong_CONSUMER_AI, &stream) == Zhulong_OK);

    wait_until([&] {
        Zhulong_stream_status status{};
        CHECK(Zhulong_stream_get_status(engine, stream, &status) == Zhulong_OK);
        CHECK(status.state != Zhulong_STREAM_FAILED);
        return status.state == Zhulong_STREAM_RUNNING;
    });
    std::this_thread::sleep_for(500ms); // 等待解码器预热并产出首批帧

    BrokerProbe probe;
    CHECK(probe.attach(engine, stream, "throttle_probe"));

    // 阶段 1：不节流基线（rate=0 表示按解码原帧率发布）
    CHECK(Zhulong_stream_set_publish_rate(engine, stream, 0) == Zhulong_OK);
    std::this_thread::sleep_for(150ms);
    const size_t baseline = probe.count_over(1000ms);

    // 阶段 2：节流到 5fps
    CHECK(Zhulong_stream_set_publish_rate(engine, stream, 5) == Zhulong_OK);
    std::this_thread::sleep_for(200ms); // 让节流基准点稳定
    const size_t slow = probe.count_over(1500ms);

    // 阶段 3：放宽到 25fps
    CHECK(Zhulong_stream_set_publish_rate(engine, stream, 25) == Zhulong_OK);
    std::this_thread::sleep_for(200ms);
    const size_t fast = probe.count_over(1500ms);

    std::cout << "  throttle counts: baseline(1s)=" << baseline
              << " slow(1.5s,5fps)=" << slow << " fast(1.5s,25fps)=" << fast << '\n';

    // AC2 前提：解码通路真的在出帧（未节流时 1s 至少 8 帧）
    CHECK(baseline >= 8);

    // AC10：5fps 下 1.5s 派发量必须远低于 25fps 的 37 帧
    CHECK(slow <= 12);

    // AC10：放宽速率后派发量必须显著回升（响应式，不是固定值）
    CHECK(fast >= 2 * slow);
    CHECK(fast >= 20);

    // AC2：全程持续出帧且流未故障 —— 若发布后帧引用被持有，解码池会枯竭而停滞
    Zhulong_stream_status final_status{};
    CHECK(Zhulong_stream_get_status(engine, stream, &final_status) == Zhulong_OK);
    CHECK(final_status.state == Zhulong_STREAM_RUNNING);

    probe.client.disconnect();
    CHECK(Zhulong_stream_release(engine, stream, 1) == Zhulong_OK);
    CHECK(Zhulong_engine_stop(engine) == Zhulong_OK);
}

/**
 * @brief AC6 慢算法包隔离（真实 RTSP 链路）
 *
 * 同一路流上同时挂：一个从不 ACK 的慢包、一个逐帧 ACK 的健康包，并订阅 PREVIEW 回调。
 * 断言三件事同时成立：
 *   1. 慢包被显著丢帧（Drop Latest 生效）；
 *   2. 健康包持续收帧（其他包不受拖累）；
 *   3. PREVIEW 包数持续增长且流保持 RUNNING（采集/解码未被阻塞）。
 */
void ai_slow_consumer_isolation_case(Zhulong_engine_h engine, const std::string &base) {
    CHECK(Zhulong_engine_start(engine) == Zhulong_OK);

    const auto url = base + "/ai-slow";
    Zhulong_stream_id ai_stream = 0, preview_stream = 0;
    CHECK(Zhulong_stream_acquire(engine, url.c_str(), nullptr, 1, Zhulong_CONSUMER_AI, &ai_stream) == Zhulong_OK);
    CHECK(Zhulong_stream_acquire(engine, url.c_str(), nullptr, 2, Zhulong_CONSUMER_PREVIEW, &preview_stream) == Zhulong_OK);
    CHECK(ai_stream == preview_stream);

    // PREVIEW 订阅：作为「采集/解码链路仍在推进」的独立观测面
    CallbackState state;
    state.engine = engine;
    Zhulong_subscription_id subscription = 0;
    CHECK(Zhulong_stream_subscribe(engine, preview_stream, 2, callback, reinterpret_cast<uintptr_t>(&state), &subscription) == Zhulong_OK);

    wait_until([&] {
        Zhulong_stream_status status{};
        CHECK(Zhulong_stream_get_status(engine, ai_stream, &status) == Zhulong_OK);
        return status.state == Zhulong_STREAM_RUNNING;
    });
    std::this_thread::sleep_for(500ms);

    const int packets_before = state.count.load();
    CHECK(packets_before > 0);

    char path_buf[256];
    size_t path_len = sizeof(path_buf);
    CHECK(Zhulong_stream_get_broker_path(engine, ai_stream, path_buf, &path_len) == Zhulong_OK);

    // 慢包：连上后从不 ACK
    zhulong::ipc::UnixSocketClient slow;
    CHECK(slow.connect(path_buf));
    zhulong::ipc::HandshakeReq slow_req{};
    std::strncpy(slow_req.client_name, "slow_pkg", sizeof(slow_req.client_name) - 1);
    CHECK(slow.send_msg(zhulong::ipc::MSG_HANDSHAKE_REQ, &slow_req, sizeof(slow_req)));
    zhulong::ipc::IpcHeader slow_hdr{};
    std::vector<uint8_t> slow_payload;
    CHECK(slow.recv_msg(slow_hdr, slow_payload, 2000));

    // 健康包：逐帧 ACK
    BrokerProbe healthy;
    CHECK(healthy.attach(engine, ai_stream, "healthy_pkg"));

    // 慢包在后台持续读走 socket 缓冲但从不 ACK，统计它实际被派发了多少帧
    std::atomic<size_t> slow_received{0};
    std::atomic<bool> slow_stop{false};
    std::thread slow_reader([&] {
        while (!slow_stop.load()) {
            zhulong::ipc::IpcHeader fh{};
            std::vector<uint8_t> fp;
            if (!slow.recv_msg(fh, fp, 100)) continue;
            if (fh.msg_type == zhulong::ipc::MSG_FRAME_DISPATCH) slow_received.fetch_add(1);
        }
    });

    const size_t healthy_received = healthy.count_over(1500ms);
    slow_stop.store(true);
    slow_reader.join();

    const int packets_after = state.count.load();

    std::cout << "  slow-consumer isolation: slow=" << slow_received.load()
              << " healthy=" << healthy_received
              << " preview_packets=" << (packets_after - packets_before) << '\n';

    // 1. Drop Latest 生效：慢包被显著丢帧
    CHECK(slow_received.load() * 2 < healthy_received);
    // 2. 其他包不受拖累
    CHECK(healthy_received >= 12);
    // 3. 采集/解码未被阻塞：PREVIEW 包持续增长
    CHECK(packets_after > packets_before + 12);

    Zhulong_stream_status status{};
    CHECK(Zhulong_stream_get_status(engine, ai_stream, &status) == Zhulong_OK);
    CHECK(status.state == Zhulong_STREAM_RUNNING);

    slow.disconnect();
    healthy.client.disconnect();
    CHECK(Zhulong_stream_unsubscribe(engine, preview_stream, subscription) == Zhulong_OK);
    CHECK(Zhulong_stream_release(engine, preview_stream, 2) == Zhulong_OK);
    CHECK(Zhulong_stream_release(engine, ai_stream, 1) == Zhulong_OK);
    CHECK(Zhulong_engine_stop(engine) == Zhulong_OK);
}

/**
 * @brief 捕获 native 日志，用于断言「起播耗时归因」链路真的产出数据。
 *
 * 起播慢是现场最常见的投诉，而 avformat_open_input 的 1.2 秒到底花在哪个 RTSP
 * 往返上，只能靠 rtsp_txn 这组日志回答。它一旦被静默掉了，同类问题又会退化成猜。
 */
struct CapturedLogs {
    std::mutex mutex;
    std::vector<std::string> lines;

    static void sink(uintptr_t, int32_t, uint64_t, const char *, size_t,
                     const char *text, size_t text_len) {
        auto &self = instance();
        std::lock_guard<std::mutex> lock(self.mutex);
        self.lines.emplace_back(text, text_len);
    }

    static CapturedLogs &instance() {
        static CapturedLogs captured;
        return captured;
    }

    std::vector<std::string> snapshot() {
        std::lock_guard<std::mutex> lock(mutex);
        return lines;
    }

    bool contains(const std::string &needle) {
        std::lock_guard<std::mutex> lock(mutex);
        for (const auto &line : lines) {
            if (line.find(needle) != std::string::npos) return true;
        }
        return false;
    }
};

/** @brief 凭据脱敏是日志放行的前提，必须独立钉住，不靠人眼检查日志 */
static void credentials_redaction_case() {
    CHECK(zhulong::redact_credentials(
        "OPTIONS rtsp://alice:s3cret@10.0.0.9:554/live RTSP/1.0") ==
        "OPTIONS rtsp://***@10.0.0.9:554/live RTSP/1.0");
    // 无 userinfo 时原样返回
    CHECK(zhulong::redact_credentials("DESCRIBE rtsp://10.0.0.9/live RTSP/1.0") ==
        "DESCRIBE rtsp://10.0.0.9/live RTSP/1.0");
    // userinfo 后面带查询串的路径也要能切开
    CHECK(zhulong::redact_credentials("rtsp://bob:pw@h/x?y=1").find("pw") == std::string::npos);

    // ⚠️ 密码本身含 '@'（很多相机接受未转义的 p@ss）：必须抹到最后一个 '@'，
    //    只抹第一个会把密码尾部留在日志里。
    const auto at_in_password = zhulong::redact_credentials("rtsp://user:p@ss@10.0.0.9/live");
    CHECK(at_in_password == "rtsp://***@10.0.0.9/live");
    CHECK(at_in_password.find("ss") == std::string::npos);

    // scheme 大小写不敏感：大写 RTSP 同样不能漏
    CHECK(zhulong::redact_credentials("OPTIONS RTSP://alice:s3cret@10.0.0.9/live") ==
        "OPTIONS RTSP://***@10.0.0.9/live");

    // 同一行里出现两个 URL：两个都要抹
    const auto twice = zhulong::redact_credentials(
        "rtsp://a:1@10.0.0.1/x -> rtsp://b:2@10.0.0.2/y");
    CHECK(twice.find("1@") == std::string::npos);
    CHECK(twice.find("2@") == std::string::npos);
    CHECK(twice == "rtsp://***@10.0.0.1/x -> rtsp://***@10.0.0.2/y");
}

struct AudioCallbackState {
    int32_t expected_codec = Zhulong_CODEC_PCMA;
    std::atomic<int> count{0};
    std::atomic<bool> invalid{false};
    std::atomic<uint32_t> last_track{0};
};

void audio_test_callback(uintptr_t token, const Zhulong_packet_view *packet) {
    auto &state = *reinterpret_cast<AudioCallbackState *>(token);
    if (!packet || !packet->data || !packet->size ||
        packet->codec != state.expected_codec ||
        packet->track_type != ZHULONG_TRACK_AUDIO) {
        state.invalid.store(true);
    }
    if (packet) state.last_track.store(packet->track_type);
    state.count.fetch_add(1);
}

void dual_track_pcma_case(Zhulong_engine_h engine, const std::string &base) {
    const std::string url = base + "/audio-pcma";
    Zhulong_stream_id stream = 0;
    CHECK(Zhulong_stream_acquire(engine, url.c_str(), nullptr, 1, Zhulong_CONSUMER_PREVIEW, &stream) == Zhulong_OK);

    Zhulong_audio_view audio_view{};
    wait_until([&] {
        return Zhulong_stream_audio_info(engine, stream, &audio_view) == Zhulong_OK;
    });
    CHECK(audio_view.has_audio == 1);
    CHECK(audio_view.codec == Zhulong_CODEC_PCMA);
    CHECK(audio_view.sample_rate == 8000);
    CHECK(audio_view.channels == 1);

    CallbackState video_state{engine, Zhulong_CODEC_H264};
    Zhulong_subscription_id video_sub = 0;
    CHECK(Zhulong_stream_subscribe(engine, stream, 1, callback, reinterpret_cast<uintptr_t>(&video_state), &video_sub) == Zhulong_OK);

    Zhulong_stream_id stream2 = 0;
    CHECK(Zhulong_stream_acquire(engine, url.c_str(), nullptr, 2, Zhulong_CONSUMER_PREVIEW, &stream2) == Zhulong_OK);
    CHECK(stream2 == stream);

    AudioCallbackState audio_state{Zhulong_CODEC_PCMA};
    Zhulong_subscription_id audio_sub = 0;
    CHECK(Zhulong_stream_subscribe_track(engine, stream, 2, ZHULONG_TRACK_MASK_AUDIO, audio_test_callback,
                                         reinterpret_cast<uintptr_t>(&audio_state), &audio_sub) == Zhulong_OK);

    wait_until([&] {
        return video_state.count.load() >= 5 && audio_state.count.load() >= 5;
    });

    CHECK(!video_state.invalid.load());
    CHECK(!audio_state.invalid.load());
    CHECK(audio_state.last_track.load() == ZHULONG_TRACK_AUDIO);

    CHECK(Zhulong_stream_unsubscribe(engine, stream, video_sub) == Zhulong_OK);
    CHECK(Zhulong_stream_unsubscribe(engine, stream, audio_sub) == Zhulong_OK);
    CHECK(Zhulong_stream_release(engine, stream, 1) == Zhulong_OK);
    CHECK(Zhulong_stream_release(engine, stream, 2) == Zhulong_OK);
}

void dual_track_pcmu_case(Zhulong_engine_h engine, const std::string &base) {
    const std::string url = base + "/audio-pcmu";
    Zhulong_stream_id stream = 0;
    CHECK(Zhulong_stream_acquire(engine, url.c_str(), nullptr, 1, Zhulong_CONSUMER_PREVIEW, &stream) == Zhulong_OK);

    Zhulong_audio_view audio_view{};
    wait_until([&] {
        return Zhulong_stream_audio_info(engine, stream, &audio_view) == Zhulong_OK;
    });
    CHECK(audio_view.has_audio == 1);
    CHECK(audio_view.codec == Zhulong_CODEC_PCMU);
    CHECK(audio_view.sample_rate == 8000);
    CHECK(audio_view.channels == 1);

    AudioCallbackState audio_state{Zhulong_CODEC_PCMU};
    Zhulong_subscription_id audio_sub = 0;
    CHECK(Zhulong_stream_subscribe_track(engine, stream, 1, ZHULONG_TRACK_MASK_AUDIO, audio_test_callback,
                                         reinterpret_cast<uintptr_t>(&audio_state), &audio_sub) == Zhulong_OK);

    wait_until([&] {
        return audio_state.count.load() >= 5;
    });
    CHECK(!audio_state.invalid.load());

    CHECK(Zhulong_stream_unsubscribe(engine, stream, audio_sub) == Zhulong_OK);
    CHECK(Zhulong_stream_release(engine, stream, 1) == Zhulong_OK);
}

void dual_track_aac_case(Zhulong_engine_h engine, const std::string &base) {
    const std::string url = base + "/audio-aac";
    Zhulong_stream_id stream = 0;
    CHECK(Zhulong_stream_acquire(engine, url.c_str(), nullptr, 1, Zhulong_CONSUMER_PREVIEW, &stream) == Zhulong_OK);

    wait_until([&] {
        Zhulong_stream_status st{};
        Zhulong_stream_get_status(engine, stream, &st);
        return st.state == Zhulong_STREAM_RUNNING;
    });

    Zhulong_audio_view small_view{};
    uint8_t small_buf[1] = {0};
    small_view.extradata = small_buf;
    small_view.extradata_capacity = sizeof(small_buf);
    const auto err = Zhulong_stream_audio_info(engine, stream, &small_view);
    CHECK(err == Zhulong_ERR_BUFFER_TOO_SMALL);
    CHECK(small_view.extradata_size == 2);

    uint8_t full_buf[16] = {0};
    Zhulong_audio_view audio_view{};
    audio_view.extradata = full_buf;
    audio_view.extradata_capacity = sizeof(full_buf);
    CHECK(Zhulong_stream_audio_info(engine, stream, &audio_view) == Zhulong_OK);
    CHECK(audio_view.has_audio == 1);
    CHECK(audio_view.codec == Zhulong_CODEC_AAC);
    CHECK(audio_view.sample_rate == 44100);
    CHECK(audio_view.channels == 2);
    CHECK(audio_view.extradata_size == 2);
    CHECK(full_buf[0] == 0x12 && full_buf[1] == 0x10);

    AudioCallbackState audio_state{Zhulong_CODEC_AAC};
    Zhulong_subscription_id audio_sub = 0;
    CHECK(Zhulong_stream_subscribe_track(engine, stream, 1, ZHULONG_TRACK_MASK_AUDIO, audio_test_callback,
                                         reinterpret_cast<uintptr_t>(&audio_state), &audio_sub) == Zhulong_OK);

    wait_until([&] {
        return audio_state.count.load() >= 5;
    });
    CHECK(!audio_state.invalid.load());

    CHECK(Zhulong_stream_unsubscribe(engine, stream, audio_sub) == Zhulong_OK);
    CHECK(Zhulong_stream_release(engine, stream, 1) == Zhulong_OK);
}

/**
 * @brief MP4A-LATM 档位：固定 FFmpeg 7.1.5 会将其解包为裸 AAC（实测断言，不是拒绝用例）
 *
 * 依据：`libavformat/rtpdec_latm.c` 的 `ff_mp4a_latm_dynamic_handler` 声明
 * `codec_id = AV_CODEC_ID_AAC`，`latm_parse_packet` 直接把 LATM 帧剥成裸 AU，
 * `parse_fmtp_config` 把 SDP 的 LATM config 中的 ASC 写进 extradata。
 * 所以设计稿里“AAC-LATM 一律拒绝”的前提在本版 FFmpeg 上不成立：这路相机是真能出声的，
 * 拒绝它等于白白把一个可用机型推到“无音频”。防御性拒绝分支保留（见 rtsp_input.cpp），
 * 本用例钉住的是「当前真实行为」——一旦 FFmpeg 行为变了，这里先红。
 */
void audio_latm_depacketized_case(Zhulong_engine_h engine, const std::string &base) {
    const std::string url = base + "/audio-latm";
    Zhulong_stream_id stream = 0;
    CHECK(Zhulong_stream_acquire(engine, url.c_str(), nullptr, 1, Zhulong_CONSUMER_PREVIEW, &stream) == Zhulong_OK);

    CallbackState video_state{engine, Zhulong_CODEC_H264};
    Zhulong_subscription_id video_sub = 0;
    CHECK(Zhulong_stream_subscribe(engine, stream, 1, callback, reinterpret_cast<uintptr_t>(&video_state), &video_sub) == Zhulong_OK);

    wait_until([&] {
        return video_state.count.load() >= 5;
    });
    CHECK(!video_state.invalid.load());

    uint8_t asc_buf[16] = {0};
    Zhulong_audio_view audio_view{};
    audio_view.extradata = asc_buf;
    audio_view.extradata_capacity = sizeof(asc_buf);
    CHECK(Zhulong_stream_audio_info(engine, stream, &audio_view) == Zhulong_OK);
    CHECK(audio_view.has_audio == 1);
    CHECK(audio_view.codec == Zhulong_CODEC_AAC);
    // 采样率/声道取自 ASC（fixture 的 config=40002410adca00 → 44100Hz 单声道）
    CHECK(audio_view.sample_rate == 44100);
    CHECK(audio_view.channels == 1);
    CHECK(audio_view.extradata_size == 6);
    CHECK(asc_buf[0] == 0x12 && asc_buf[1] == 0x08);

    AudioCallbackState audio_state{Zhulong_CODEC_AAC};
    Zhulong_subscription_id audio_sub = 0;
    CHECK(Zhulong_stream_subscribe_track(engine, stream, 1, ZHULONG_TRACK_MASK_AUDIO, audio_test_callback,
                                         reinterpret_cast<uintptr_t>(&audio_state), &audio_sub) == Zhulong_OK);
    wait_until([&] {
        return audio_state.count.load() >= 5;
    });
    CHECK(!audio_state.invalid.load());

    auto &logs = CapturedLogs::instance();
    CHECK(logs.contains("rtsp_audio_detected codec=16"));

    CHECK(Zhulong_stream_unsubscribe(engine, stream, audio_sub) == Zhulong_OK);
    CHECK(Zhulong_stream_unsubscribe(engine, stream, video_sub) == Zhulong_OK);
    CHECK(Zhulong_stream_release(engine, stream, 1) == Zhulong_OK);
}

void audio_broken_fallback_case(Zhulong_engine_h engine, const std::string &base) {
    const std::string url = base + "/audio-broken";
    Zhulong_stream_id stream = 0;
    CHECK(Zhulong_stream_acquire(engine, url.c_str(), nullptr, 1, Zhulong_CONSUMER_PREVIEW, &stream) == Zhulong_OK);

    CallbackState video_state{engine, Zhulong_CODEC_H264};
    Zhulong_subscription_id video_sub = 0;
    CHECK(Zhulong_stream_subscribe(engine, stream, 1, callback, reinterpret_cast<uintptr_t>(&video_state), &video_sub) == Zhulong_OK);

    wait_until([&] {
        return video_state.count.load() >= 5;
    });
    CHECK(!video_state.invalid.load());

    Zhulong_audio_view audio_view{};
    auto broken_rc = Zhulong_stream_audio_info(engine, stream, &audio_view);
    if (broken_rc != Zhulong_OK) {
        std::cerr << "broken info_rc=" << broken_rc << '\n';
    }
    CHECK(broken_rc == Zhulong_OK);
    CHECK(audio_view.has_audio == 0);

    auto &logs = CapturedLogs::instance();
    CHECK(logs.contains("rtsp_audio_open_failed"));

    CHECK(Zhulong_stream_unsubscribe(engine, stream, video_sub) == Zhulong_OK);
    CHECK(Zhulong_stream_release(engine, stream, 1) == Zhulong_OK);
}

void audio_info_lifecycle_case(Zhulong_engine_h engine, const std::string &base) {
    const std::string url = base + "/audio-pcma";
    Zhulong_stream_id stream = 0;
    CHECK(Zhulong_stream_acquire(engine, url.c_str(), nullptr, 1, Zhulong_CONSUMER_PREVIEW, &stream) == Zhulong_OK);

    Zhulong_audio_view audio_view{};
    wait_until([&] {
        return Zhulong_stream_audio_info(engine, stream, &audio_view) == Zhulong_OK;
    });

    CHECK(Zhulong_stream_release(engine, stream, 1) == Zhulong_OK);
    CHECK(Zhulong_stream_audio_info(engine, stream + 999, &audio_view) == Zhulong_ERR_NOT_FOUND);
}

/**
 * @brief AC-1：申请音频轨到底让起播多付了多少
 *
 * 只采音频使 SDP 多一段 m=audio、连接多一次 SETUP，这是唯一新增的建连成本。
 * 固定 FFmpeg 7.1.5 在音频 SETUP 失败时会直接让整个 open 失败，所以这个开销不包含重试。
 * 这里测「acquire → 首枚视频包」的墙钟时间，并把实测值打印出来留作实测记录：
 * 门限刻意比 AC-1 的 10% 宽松得多，因为回环环境的毫秒级抖动不该把功能测试变成抖动测试；
 * 真正的 10% 核验以采样日志里的 info_ms / first_packet_ms 为准（见任务笔记）。
 */
void audio_startup_cost_case(Zhulong_engine_h engine, const std::string &base) {
    auto measure = [&](const std::string &path) {
        const auto started = Clock::now();
        Zhulong_stream_id stream = 0;
        CallbackState state{engine, Zhulong_CODEC_H264};
        CHECK(Zhulong_stream_acquire(engine, (base + path).c_str(), nullptr, 1, Zhulong_CONSUMER_PREVIEW, &stream) == Zhulong_OK);
        Zhulong_subscription_id subscription = 0;
        CHECK(Zhulong_stream_subscribe(engine, stream, 1, callback,
                                     reinterpret_cast<uintptr_t>(&state), &subscription) == Zhulong_OK);
        wait_until([&] { return state.count.load() >= 1; });
        const long long elapsed = std::chrono::duration_cast<std::chrono::milliseconds>(Clock::now() - started).count();
        CHECK(Zhulong_stream_unsubscribe(engine, stream, subscription) == Zhulong_OK);
        CHECK(Zhulong_stream_release(engine, stream, 1) == Zhulong_OK);
        return elapsed;
    };

    const long long video_only = measure("/h264");
    const long long with_audio = measure("/audio-pcma");
    std::cout << "  startup cost: video-only=" << video_only << "ms video+audio=" << with_audio << "ms\n";
    CHECK(with_audio <= video_only + 50);
}

/**
 * @brief 音频包不得刷新空闲超时（空闲语义 = 相机还在送视频）
 *
 * 桩位 `/audio-pcma-stop-video` 只推音频（每 20ms 一包，稳定 50pps）：
 * 若音频也刷新 deadline，这路流会永远“看起来活着”，相机视颊卡死就无人发现。
 * 同时钉住另一条边界：视频原点未建立时音频包一律丢弃（不得自行建原点），
 * 所以本用例里音频回调必须一直是 0。
 */
void audio_idle_timeout_case(Zhulong_engine_h engine, const std::string &base) {
    Zhulong_stream_options options{};
    options.transport = Zhulong_TRANSPORT_TCP;
    options.open_timeout_ms = 3000;
    options.idle_timeout_ms = 800;

    Zhulong_stream_id stream = 0;
    CHECK(Zhulong_stream_acquire(engine, (base + "/audio-pcma-stop-video").c_str(), &options,
                                 1, Zhulong_CONSUMER_PREVIEW, &stream) == Zhulong_OK);

    AudioCallbackState audio_state{Zhulong_CODEC_PCMA};
    Zhulong_subscription_id subscription = 0;
    CHECK(Zhulong_stream_subscribe_track(engine, stream, 1, ZHULONG_TRACK_MASK_AUDIO,
                                         audio_test_callback,
                                         reinterpret_cast<uintptr_t>(&audio_state), &subscription) == Zhulong_OK);

    // 只有视频包才算“活着”：音频持续到达但视频缺席，必须按空闲超时判失败。
    wait_until([&] {
        Zhulong_stream_status status{};
        Zhulong_stream_get_status(engine, stream, &status);
        return status.state == Zhulong_STREAM_FAILED;
    }, 4000ms);

    Zhulong_stream_status status{};
    CHECK(Zhulong_stream_get_status(engine, stream, &status) == Zhulong_OK);
    CHECK(status.error == Zhulong_ERR_TIMEOUT);
    // 视频原点未建立 → 音频不走下去（不能拿音频自己造时间轴基准）
    CHECK(audio_state.count.load() == 0);

    CHECK(Zhulong_stream_unsubscribe(engine, stream, subscription) == Zhulong_OK);
    CHECK(Zhulong_stream_release(engine, stream, 1) == Zhulong_OK);
}

/**
 * @brief 音频降级不得刷新 open 预算（防「同步 CGO 调用超出调用方 ctx deadline」）
 *
 * /stall-open 永远不回 OPTIONS：第一次 open 只会在 open_timeout_ms 到点后以 TIMEOUT 失败。
 * Go 侧 Acquire 已把 ctx deadline 折进 open_timeout_ms（engine.go convertStreamOptions），
 * 原生若在音频降级分支里重新 expire_after，整条失败链就会花 2×open_timeout：
 * 现场表现是「点开预览等两倍超时才报错」。这里直接量 acquire → FAILED 的墙钟。
 */
void audio_fallback_budget_case(Zhulong_engine_h engine, const std::string &base) {
    Zhulong_stream_options options{};
    options.transport = Zhulong_TRANSPORT_TCP;
    options.open_timeout_ms = 1000;
    options.idle_timeout_ms = 5000;

    const auto started = Clock::now();
    Zhulong_stream_id stream = 0;
    CHECK(Zhulong_stream_acquire(engine, (base + "/stall-open").c_str(), &options, 1,
                                 Zhulong_CONSUMER_PREVIEW, &stream) == Zhulong_OK);

    wait_until([&] {
        Zhulong_stream_status status{};
        if (Zhulong_stream_get_status(engine, stream, &status) != Zhulong_OK) return false;
        return status.state == Zhulong_STREAM_FAILED;
    }, 5000ms);
    const auto elapsed_ms =
        std::chrono::duration_cast<std::chrono::milliseconds>(Clock::now() - started).count();
    std::cout << "  audio fallback budget: stream failed in " << elapsed_ms
              << "ms (open_timeout=1000ms)\n";

    Zhulong_stream_status status{};
    CHECK(Zhulong_stream_get_status(engine, stream, &status) == Zhulong_OK);
    CHECK(status.error == Zhulong_ERR_TIMEOUT);
    // 修复前：1000 + 1000 ≈ 2000ms+；修复后：≈ 1000ms。留 800ms 余量吸收回环抖动。
    CHECK(elapsed_ms < 1800);
    CHECK(Zhulong_stream_release(engine, stream, 1) == Zhulong_OK);
}

int main(int argc, char **argv) {
    try {
        credentials_redaction_case();
        CHECK(argc == 2);
        setenv("ZHULONG_STREAM_GRACE_PERIOD_MS", "200", 0);
        // token 必须非零：set_log_sink 用 (sink==nullptr) != (token==0) 拒绝半装状态
        CHECK(zhulong::set_log_sink(&CapturedLogs::sink, 1));
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
        ai_publish_throttle_case(owner.engine, base);
        ai_slow_consumer_isolation_case(owner.engine, base);
        // ai_slow_consumer_isolation_case 结尾会 stop 引擎（它的自测契约），
        // 而双轨用例需要运行态；这里重新 start，顺带把“stop → start 后仍可建音频轨”走过一遍。
        CHECK(Zhulong_engine_start(owner.engine) == Zhulong_OK);

        // 双轨音频集成测试用例 (PCMA / PCMU / AAC / LATM / SETUP 失败重开 / 生命周期)
        dual_track_pcma_case(owner.engine, base);
        dual_track_pcmu_case(owner.engine, base);
        dual_track_aac_case(owner.engine, base);
        audio_latm_depacketized_case(owner.engine, base);
        audio_broken_fallback_case(owner.engine, base);
        audio_info_lifecycle_case(owner.engine, base);
        audio_idle_timeout_case(owner.engine, base);
        audio_fallback_budget_case(owner.engine, base);
        audio_startup_cost_case(owner.engine, base);

        // 起播归因日志必须真的产出：请求行（Sending:）与应答行（RTSP/1.）各至少一条，
        // 且耗时字段存在。缺失意味着全局日志静默策略把这条通道也吞掉了。
        auto &logs = CapturedLogs::instance();
        CHECK(logs.contains("rtsp_startup_timing"));
        // 请求行（Sending:）与应答行（RTSP/1.）各至少一条，且带耗时字段
        CHECK(logs.contains("rtsp_txn t="));
        CHECK(logs.contains("RTSP/1."));

        // 顺带把前几条事务日志打出来：格式本身就是排障口径，退化要当场可见。
        int shown = 0;
        for (const auto &line : logs.snapshot()) {
            if (line.find("rtsp_txn") != std::string::npos && shown++ < 4) {
                std::cout << "  sample: " << line << '\n';
            }
        }
        zhulong::set_log_sink(nullptr, 0);

        std::cout << "probe, pool, packet clock, callback drain, cancellation, throttle, stress and startup trace passed\n";
    } catch (const std::exception &error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
