/**
 * @file engine.cpp
 * @brief Zhulong Native 引擎 C ABI 边界与异常隔离层实现
 *
 * 核心设计：
 * 1. 异常防火墙（Exception Firewall）：
 *    - C++ 异常严禁穿越 extern "C" ABI 边界，否则在跨语言运行时（如 Go CGO）中会导致崩溃或未定义行为。
 *    - 通过 boundary() 模板包裹每个接口，捕获所有异常并映射为预定义的 Zhulong_status_t 错误码。
 * 2. 回调重入防御（Re-entrancy Guard）：
 *    - 检查 thread_local 标记 in_callback。在数据包分发回调执行期间，严防调用控制 API 导致自死锁。
 * 3. 内存所有权与 RAII 桥接：
 *    - 句柄在创建/探测时通过 new / make_unique 分配，销毁时 delete。
 */

#include "Zhulong/engine.h"
#include "pipeline/engine.hpp"

#include <memory>
#include <new>
#include <utility>

// 不透明结构体具体定义：持有底层 C++ 引擎与探测视频结果实例
struct Zhulong_engine_t { zhulong::Engine implementation; };
struct Zhulong_probe_result_t { zhulong::Video video; };

namespace {

/**
 * @brief C ABI 异常隔离防火墙模板
 *
 * 保证所有 C ABI 导出函数均为 noexcept：
 * 1. 检测 thread_local 回调状态，杜绝回调中的非法控制重入；
 * 2. 捕获内部业务失败异常 Failure 并映射其 status 状态码；
 * 3. 捕获 std::bad_alloc 映射为 Zhulong_ERR_OUT_OF_MEMORY；
 * 4. 捕获其余未知异常映射为 Zhulong_ERR_INTERNAL。
 */
template <typename Function>
Zhulong_status_t boundary(Function function) noexcept {
    // 严禁在底层包派发回调上下文内调用引擎控制函数
    if (zhulong::in_callback) return Zhulong_ERR_CALLBACK_CONTEXT;
    try {
        function();
        return Zhulong_OK;
    } catch (const zhulong::Failure &failure) {
        return failure.status;
    } catch (const std::bad_alloc &) {
        return Zhulong_ERR_OUT_OF_MEMORY;
    } catch (...) {
        return Zhulong_ERR_INTERNAL;
    }
}

/**
 * @brief 参数有效性断言辅助函数，校验失败时抛出 Failure 异常
 */
void require(bool valid) {
    if (!valid) throw zhulong::Failure{Zhulong_ERR_INVALID_ARGUMENT};
}

} // namespace

// ============================================================================
//                               引擎生命周期 API
// ============================================================================

extern "C" Zhulong_status_t Zhulong_engine_create(Zhulong_engine_h *out_engine) {
    if (out_engine) *out_engine = nullptr;
    return boundary([&] {
        require(out_engine != nullptr);
        // 全局只执行一次 FFmpeg 网络与日志环境初始化
        zhulong::initialize_ffmpeg();
        *out_engine = new Zhulong_engine_t;
    });
}

extern "C" Zhulong_status_t Zhulong_engine_start(Zhulong_engine_h engine) {
    return boundary([&] {
        require(engine != nullptr);
        engine->implementation.start();
    });
}

extern "C" Zhulong_status_t Zhulong_engine_stop(Zhulong_engine_h engine) {
    return boundary([&] {
        require(engine != nullptr);
        engine->implementation.stop();
    });
}

extern "C" void Zhulong_engine_destroy(Zhulong_engine_h engine) {
    // 严禁在回调上下文中自销毁（防止对当前正在执行的线程进行 join 导致死锁）
    if (zhulong::in_callback) return;
    try {
        delete engine;
    } catch (...) {}
}

// ============================================================================
//                               物理流管理 API
// ============================================================================

extern "C" Zhulong_status_t Zhulong_stream_acquire(Zhulong_engine_h engine, const char *url,
    const Zhulong_stream_options *options, uint64_t consumer_id, int32_t consumer_kind,
    Zhulong_stream_id *out_stream) {
    if (out_stream) *out_stream = 0;
    return boundary([&] {
        require(engine && out_stream && consumer_id &&
                consumer_kind >= Zhulong_CONSUMER_PREVIEW &&
                consumer_kind <= Zhulong_CONSUMER_AI);
        // 对 RTSP URL 进行严格格式校验与规范化，解析选项后在引擎流池中注册或复用
        *out_stream = engine->implementation.acquire(
            zhulong::normalize_rtsp_url(url),
            zhulong::parse_options(options),
            consumer_id,
            consumer_kind
        );
    });
}

extern "C" Zhulong_status_t Zhulong_stream_release(Zhulong_engine_h engine, Zhulong_stream_id stream,
    uint64_t consumer_id) {
    return boundary([&] {
        require(engine && stream && consumer_id);
        engine->implementation.release(stream, consumer_id);
    });
}

extern "C" Zhulong_status_t Zhulong_stream_get_status(Zhulong_engine_h engine, Zhulong_stream_id stream,
    Zhulong_stream_status *out_status) {
    if (out_status) *out_status = {Zhulong_STREAM_FAILED, Zhulong_ERR_INVALID_ARGUMENT};
    return boundary([&] {
        require(engine && stream && out_status);
        *out_status = engine->implementation.status(stream);
    });
}

extern "C" Zhulong_status_t Zhulong_stream_subscribe(Zhulong_engine_h engine, Zhulong_stream_id stream,
    uint64_t preview_consumer_id, Zhulong_packet_callback callback, uintptr_t token,
    Zhulong_subscription_id *out_subscription) {
    if (out_subscription) *out_subscription = 0;
    return boundary([&] {
        require(engine && stream && preview_consumer_id && callback && out_subscription);
        *out_subscription = engine->implementation.subscribe(stream, preview_consumer_id, callback, token);
    });
}

extern "C" Zhulong_status_t Zhulong_stream_unsubscribe(Zhulong_engine_h engine, Zhulong_stream_id stream,
    Zhulong_subscription_id subscription) {
    return boundary([&] {
        require(engine && stream && subscription);
        // 同步排空：此调用返回后确保无任何正在执行或未来待执行的回调
        engine->implementation.unsubscribe(stream, subscription);
    });
}

// ============================================================================
//                               流探测（Probe） API
// ============================================================================

extern "C" Zhulong_status_t Zhulong_engine_probe(Zhulong_engine_h engine, const char *url,
    const Zhulong_stream_options *options, Zhulong_probe_result_h *out_result) {
    if (out_result) *out_result = nullptr;
    return boundary([&] {
        require(engine && out_result);
        auto result = std::make_unique<Zhulong_probe_result_t>();
        result->video = engine->implementation.probe(
            zhulong::normalize_rtsp_url(url),
            zhulong::parse_options(options)
        );
        // 移交所有权给 C ABI 句柄，后续需调用 Zhulong_probe_result_destroy 释放
        *out_result = result.release();
    });
}

extern "C" Zhulong_status_t Zhulong_probe_result_view(Zhulong_probe_result_h result, Zhulong_video_view *out_view) {
    if (out_view) *out_view = {};
    return boundary([&] {
        require(result && out_view);
        // 借出底层元数据视图（指针有效期与 result 绑定）
        *out_view = result->video.borrowed_view();
    });
}

extern "C" void Zhulong_probe_result_destroy(Zhulong_probe_result_h result) {
    if (zhulong::in_callback) return;
    try {
        delete result;
    } catch (...) {}
}
