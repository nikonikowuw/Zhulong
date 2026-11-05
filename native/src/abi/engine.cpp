/**
 * @file engine.cpp
 * @brief Zhulong Native 引擎 C ABI 边界与异常防爆门实现
 *
 * 核心设计原则：
 * 1. 异常防爆门（Exception Firewall）：
 *    - C++ 异常绝对禁止穿越 extern "C" ABI 边界进入上层（如 Go CGO），否则会导致进程崩溃或未定义行为。
 *    - 通过 boundary() 模板拦截包裹每一个 ABI 导出接口，捕获所有异常并映射为预定义的 Zhulong_status_t 错误码。
 * 2. 回调重入防御（Re-entrancy Guard）：
 *    - 检查 thread_local 标记 in_callback。在数据包/结果分发回调执行期间，严防调用控制 API 导致自死锁。
 * 3. 内存所有权与生命周期：
 *    - 句柄在创建/探测时通过 new / make_unique 分配，由对应 destroy 函数显式释放，遵循“谁分配谁释放”。
 */

#include "Zhulong/engine.h"
#include "common/log.hpp"
#include "pipeline/engine.hpp"

#include <array>
#include <atomic>
#include <chrono>
#include <cmath>
#include <condition_variable>
#include <cstdlib>
#include <cstddef>
#include <cstring>
#include <dlfcn.h>
#include <fstream>
#include <iostream>
#include <memory>
#include <mutex>
#include <new>
#include <string>
#include <thread>
#include <type_traits>
#include <utility>
#include <vector>

namespace zhulong {

/**
 * @brief 抓拍实现（定义在 abi/snapshot.cpp）
 *
 * 前置声明而非 include 头文件：snapshot 的内部结构体不需要在本文件可见，
 * 只要有一个明确的函数边界就够了。
 *
 * @param frame 已取到的只读帧引用
 * @param quality 已收敛到 [1,100] 的 JPEG 质量
 * @param boxes 归一化 [x1,y1,x2,y2] 框列表（可为空）
 * @param[out] out 成功时接收分配好的结果句柄
 * @return Zhulong_status_t
 */
Zhulong_status_t encode_snapshot(const HardwareFrame &frame, int32_t quality,
                                 const std::vector<std::array<float, 4>> &boxes,
                                 Zhulong_snapshot_h *out);

} // namespace zhulong

// 不透明结构体具体定义：持有底层 C++ 引擎与探测视频结果实例
struct Zhulong_engine_t { zhulong::Engine implementation; };
struct Zhulong_probe_result_t { zhulong::Video video; };

namespace {

std::mutex g_log_registration_mutex;
size_t g_engine_handles = 0;
static_assert(std::is_same<Zhulong_log_callback, zhulong::LogSink>::value,
    "public Zhulong log callback and internal LogSink ABI must stay identical");

// FrameBroker 客户端统计的跨语言布局：Go 侧直接用 cgo 映射本结构体，
// 尺寸或 8 字节自然对齐一旦漂移，就是读错字段而不是编译错误。
static_assert(sizeof(Zhulong_broker_client_stats) == 64, "Zhulong_broker_client_stats size drift");
static_assert(offsetof(Zhulong_broker_client_stats, delivered_frames) % 8 == 0,
    "delivered_frames must stay 8-byte aligned");
static_assert(offsetof(Zhulong_broker_client_stats, dropped_frames) % 8 == 0,
    "dropped_frames must stay 8-byte aligned");

/**
 * @brief C ABI 异常防爆门模板
 *
 * 保证所有 C ABI 导出函数均为 noexcept：
 * 1. 检测 thread_local 回调状态，杜绝回调中的非法控制重入（返回 Zhulong_ERR_CALLBACK_CONTEXT）；
 * 2. 捕获内部业务失败异常 Failure 并映射其 status 状态码；
 * 3. 捕获 std::bad_alloc 映射为 Zhulong_ERR_OUT_OF_MEMORY；
 * 4. 捕获其余未知异常映射为 Zhulong_ERR_INTERNAL。
 *
 * @tparam Function 待执行的闭包逻辑类型
 * @param function 待包装的业务逻辑调用
 * @return Zhulong_status_t 统一的 C ABI 状态码
 */
template <typename Function>
Zhulong_status_t boundary(Function function) noexcept {
    // 严禁在底层回调上下文内调用引擎控制函数（防重入自死锁）
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
 * @param valid 校验表达式真假值
 * @throws Failure 若 valid 为 false 抛出 Zhulong_ERR_INVALID_ARGUMENT
 */
void require(bool valid) {
    if (!valid) throw zhulong::Failure{Zhulong_ERR_INVALID_ARGUMENT};
}

} // namespace

// ============================================================================
//                               引擎生命周期 API
// ============================================================================

/**
 * @brief 创建并分配 Native 引擎实例
 *
 * ⚠️ 失败安全保证：
 *   - 进入时若 out_engine 非空，先将指针置空；
 *   - 全局仅在此处触发一次 FFmpeg 网络与日志环境初始化（单例保证）。
 *
 * @param[out] out_engine 输出参数，接收创建成功后的引擎句柄指针
 * @return Zhulong_status_t 执行状态码
 */
extern "C" Zhulong_status_t Zhulong_engine_get_abi_version(uint32_t *out_version) {
    return boundary([&] {
        require(out_version != nullptr);
        *out_version = ZHULONG_ABI_VERSION;
    });
}

extern "C" Zhulong_status_t Zhulong_engine_create(Zhulong_engine_h *out_engine) {
    if (out_engine) *out_engine = nullptr;
    return boundary([&] {
        require(out_engine != nullptr);
        std::lock_guard<std::mutex> registration_lock(g_log_registration_mutex);
        // 全局只执行一次 FFmpeg 网络与日志环境初始化
        zhulong::initialize_ffmpeg();
        auto *created = new Zhulong_engine_t;
        ++g_engine_handles;
        *out_engine = created;
    });
}

extern "C" Zhulong_status_t Zhulong_set_log_callback(Zhulong_log_callback callback, uintptr_t token) {
    return boundary([&] {
        require((callback == nullptr) == (token == 0));
        std::lock_guard<std::mutex> registration_lock(g_log_registration_mutex);
        if (g_engine_handles != 0) throw zhulong::Failure{Zhulong_ERR_BUSY};
        if (!zhulong::set_log_sink(callback, token)) throw zhulong::Failure{Zhulong_ERR_INVALID_ARGUMENT};
    });
}

/**
 * @brief 启动引擎后台工作线程
 * @param engine 引擎句柄
 * @return Zhulong_status_t 执行状态码
 */
extern "C" Zhulong_status_t Zhulong_engine_start(Zhulong_engine_h engine) {
    return boundary([&] {
        require(engine != nullptr);
        engine->implementation.start();
    });
}

/**
 * @brief 停止引擎并排空后台任务
 * @param engine 引擎句柄
 * @return Zhulong_status_t 执行状态码
 */
extern "C" Zhulong_status_t Zhulong_engine_stop(Zhulong_engine_h engine) {
    return boundary([&] {
        require(engine != nullptr);
        engine->implementation.stop();
    });
}

/**
 * @brief 销毁引擎实例并释放所有内部资源
 *
 * ⚠️ 安全约束：
 *   - 严禁在回调上下文中自销毁（防止对当前正在执行的线程进行汇合导致死锁）；
 *   - 支持传入空句柄安全调用（无操作）；
 *   - 销毁内部异常全部由防爆门隔离，不向外抛出。
 *
 * @param engine 待销毁的引擎句柄
 */
extern "C" void Zhulong_engine_destroy(Zhulong_engine_h engine) {
    // 严禁在回调上下文中自销毁（防止对当前正在执行的线程进行汇合导致死锁）
    if (zhulong::in_callback) return;
    // 空句柄为无操作：绝不能递减引擎引用计数，否则会误卸载仍在使用中的插件与模型
    if (engine == nullptr) return;
    try {
        {
            std::lock_guard<std::mutex> registration_lock(g_log_registration_mutex);
            delete engine;
            if (g_engine_handles > 0) --g_engine_handles;
        }
    } catch (...) {}
}

// ============================================================================
//                               物理流管理 API
// ============================================================================

/**
 * @brief 在引擎流池中注册并获取物理流句柄
 *
 * ⚠️ 失败安全保证：
 *   - 进入时若 out_stream 非空，先置 0；
 *   - consumer_id 必须全局唯一，若同一物理流重复注册相同 consumer_id 将返回失败。
 *
 * @param engine 引擎句柄
 * @param url RTSP 地址字符串
 * @param options 流选项配置指针
 * @param consumer_id 消费者唯一标识
 * @param consumer_kind 消费者类型（Preview 或 AI）
 * @param[out] out_stream 成功时接收分配的物理流 ID
 * @return Zhulong_status_t 执行状态码
 */
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

/**
 * @brief 释放指定消费者的流引用
 *
 * 当物理流的所有消费者均已释放后，流进入 8 秒宽限期（Grace Period），超时未复用则自动优雅停机。
 *
 * @param engine 引擎句柄
 * @param stream 物理流 ID
 * @param consumer_id 待释放的消费者唯一标识
 * @return Zhulong_status_t 执行状态码
 */
extern "C" Zhulong_status_t Zhulong_stream_release(Zhulong_engine_h engine, Zhulong_stream_id stream,
    uint64_t consumer_id) {
    return boundary([&] {
        require(engine && stream && consumer_id);
        engine->implementation.release(stream, consumer_id);
    });
}

/**
 * @brief 获取物理流当前运行状态
 *
 * ⚠️ 失败安全保证：
 *   - 发生异常或参数错误时，out_status 默认置为 STREAM_FAILED 与对应错误码。
 *
 * @param engine 引擎句柄
 * @param stream 物理流 ID
 * @param[out] out_status 接收状态信息结构体
 * @return Zhulong_status_t 执行状态码
 */
extern "C" Zhulong_status_t Zhulong_stream_get_status(Zhulong_engine_h engine, Zhulong_stream_id stream,
    Zhulong_stream_status *out_status) {
    if (out_status) *out_status = {Zhulong_STREAM_FAILED, Zhulong_ERR_INVALID_ARGUMENT};
    return boundary([&] {
        require(engine && stream && out_status);
        *out_status = engine->implementation.status(stream);
    });
}

/**
 * @brief 获取物理流 FrameBroker 对应的 Unix Domain Socket 路径
 *
 * ⚠️ 缓冲区大小契约：
 *   - 若传入缓冲区长度不足，*inout_len 将回填所需的包含尾零的实际字节数，并返回 Zhulong_ERR_BUFFER_TOO_SMALL。
 *
 * @param engine 引擎句柄
 * @param stream 物理流 ID
 * @param[out] out_path 接收 socket 路径的字符数组指针
 * @param[in,out] inout_len 输入传入缓冲长度，输出实际拷贝/所需字节数
 * @return Zhulong_status_t 执行状态码
 */
extern "C" Zhulong_status_t Zhulong_stream_get_broker_path(Zhulong_engine_h engine,
    Zhulong_stream_id stream, char *out_path, size_t *inout_len) {
    return boundary([&] {
        require(engine && stream && out_path && inout_len);
        require(*inout_len > 0);

        std::string path = engine->implementation.get_broker_path(stream);
        size_t needed = path.size() + 1;
        if (*inout_len < needed) {
            *inout_len = needed;
            throw zhulong::Failure{Zhulong_ERR_BUFFER_TOO_SMALL};
        }
        std::memcpy(out_path, path.c_str(), needed);
        *inout_len = needed;
    });
}

/**
 * @brief 查询指定流 FrameBroker 的客户端投递统计快照
 *
 * @param engine      引擎句柄
 * @param stream      物理流 ID
 * @param out_clients 调用方提供的数组（capacity 为 0 时可为 NULL）
 * @param capacity    out_clients 可容纳的条数
 * @param[out] out_count 当前客户端**总数**（可能大于 capacity，表示被截断）
 * @return Zhulong_status_t 执行状态码
 */
extern "C" Zhulong_status_t Zhulong_stream_get_broker_clients(Zhulong_engine_h engine,
    Zhulong_stream_id stream, Zhulong_broker_client_stats *out_clients,
    uint32_t capacity, uint32_t *out_count) {
    // 失败时先清空输出：调用方只看 out_count 也不会读到未初始化内存。
    if (out_count) *out_count = 0;
    return boundary([&] {
        require(engine && stream && out_count);
        // capacity 与指针必须成对：允许 (NULL, 0) 表示只查数量，其余组合一律拒绝。
        require(out_clients != nullptr || capacity == 0);
        *out_count = engine->implementation.broker_client_stats(stream, out_clients, capacity);
    });
}

/**
 * @brief 动态调节物理流向共享内存发布帧的最大采样帧率
 * @param engine 引擎句柄
 * @param stream 物理流 ID
 * @param max_sample_fps 最大发布采样率（0 表示不限帧率）
 * @return Zhulong_status_t 执行状态码
 */
extern "C" Zhulong_status_t Zhulong_stream_set_publish_rate(Zhulong_engine_h engine,
    Zhulong_stream_id stream, uint32_t max_sample_fps) {
    return boundary([&] {
        require(engine && stream);
        engine->implementation.set_publish_rate(stream, max_sample_fps);
    });
}

namespace {

/** @brief C ABI 视图矩形 -> 内部 ViewRect。 */
zhulong::ViewRect to_view_rect(const Zhulong_view_rect &rect) {
    return zhulong::ViewRect{rect.x1, rect.y1, rect.x2, rect.y2};
}

/** @brief 内部 ViewRect -> C ABI 视图矩形。 */
Zhulong_view_rect from_view_rect(const zhulong::ViewRect &rect) {
    return Zhulong_view_rect{rect.x1, rect.y1, rect.x2, rect.y2};
}

/**
 * @brief 结构校验：有限、严格归一化、非退化。
 *
 * ⚠️ 刻意不校验最小边长：Go 侧 MinCropSide=0.02 过 float32 会舍入成 0.0199999996，
 *    在这里比较阈值会把边界值误拒。阈值权威只在宿主 Go 层。
 */
bool valid_view_rect(const Zhulong_view_rect &rect) {
    if (!std::isfinite(rect.x1) || !std::isfinite(rect.y1) ||
        !std::isfinite(rect.x2) || !std::isfinite(rect.y2)) {
        return false;
    }
    if (rect.x1 < 0.0f || rect.y1 < 0.0f || rect.x2 > 1.0f || rect.y2 > 1.0f) {
        return false;
    }
    return rect.x1 < rect.x2 && rect.y1 < rect.y2;
}

} // namespace

/**
 * @brief 设置 AI 流分析视图（v6）
 *
 * `view == NULL` 或整帧等价于清除；清除永远成功。非空窗口在还没有解码帧尺寸时
 * 返回 Zhulong_ERR_NOT_READY（配置已登记，首个解码帧后由引擎落地）。
 */
extern "C" Zhulong_status_t Zhulong_stream_set_view(Zhulong_engine_h engine,
    Zhulong_stream_id stream, const Zhulong_view_rect *view,
    Zhulong_view_state *out_effective) {
    if (out_effective) *out_effective = {};
    return boundary([&] {
        require(engine && stream);

        bool clear = (view == nullptr);
        zhulong::ViewRect rect = zhulong::identity_view_rect();
        if (!clear) {
            require(valid_view_rect(*view));
            rect = to_view_rect(*view);
            clear = zhulong::view_rect_is_full_frame(rect);
        }

        zhulong::ViewRect effective = zhulong::identity_view_rect();
        int64_t since = 0;
        engine->implementation.set_view(stream, clear, rect, &effective, &since);
        if (out_effective) {
            out_effective->rect = from_view_rect(effective);
            out_effective->effective_since_ms = since;
        }
    });
}

/** @brief 查询 AI 流当前生效 E 与生效起点（v6，只读，不 acquire）。 */
extern "C" Zhulong_status_t Zhulong_stream_get_view(Zhulong_engine_h engine,
    Zhulong_stream_id stream, Zhulong_view_state *out_effective) {
    if (out_effective) *out_effective = {};
    return boundary([&] {
        require(engine && stream && out_effective);
        zhulong::ViewRect rect = zhulong::identity_view_rect();
        int64_t since = 0;
        engine->implementation.get_view(stream, &rect, &since);
        out_effective->rect = from_view_rect(rect);
        out_effective->effective_since_ms = since;
    });
}

/**
 * @brief 按轨订阅流的数据包通道（v5）
 */
extern "C" Zhulong_status_t Zhulong_stream_subscribe_track(Zhulong_engine_h engine, Zhulong_stream_id stream,
    uint64_t preview_consumer_id, uint32_t track_mask,
    Zhulong_packet_callback callback, uintptr_t token,
    Zhulong_subscription_id *out_subscription) {
    if (out_subscription) *out_subscription = 0;
    return boundary([&] {
        require(engine && stream && preview_consumer_id && callback && out_subscription);
        *out_subscription = engine->implementation.subscribe(stream, preview_consumer_id, callback, token, track_mask);
    });
}

/**
 * @brief 注册视频压缩数据包订阅回调（保持仅视频订阅语义）
 *
 * ⚠️ 失败安全保证：
 *   - 进入时若 out_subscription 非空，先置 0。
 *
 * @param engine 引擎句柄
 * @param stream 物理流 ID
 * @param preview_consumer_id 预览消费者唯一标识
 * @param callback 压缩包数据同步回调函数指针
 * @param token 透传给回调的用户上下文令牌（Go 侧为 cgo.Handle）
 * @param[out] out_subscription 成功时接收分配的订阅 ID
 * @return Zhulong_status_t 执行状态码
 */
extern "C" Zhulong_status_t Zhulong_stream_subscribe(Zhulong_engine_h engine, Zhulong_stream_id stream,
    uint64_t preview_consumer_id, Zhulong_packet_callback callback, uintptr_t token,
    Zhulong_subscription_id *out_subscription) {
    return Zhulong_stream_subscribe_track(engine, stream, preview_consumer_id, ZHULONG_TRACK_MASK_VIDEO,
                                         callback, token, out_subscription);
}

/**
 * @brief 查询指定物理流的音频元数据（v5）
 */
extern "C" Zhulong_status_t Zhulong_stream_audio_info(Zhulong_engine_h engine, Zhulong_stream_id stream,
                                                      Zhulong_audio_view *out_view) {
    return boundary([&] {
        require(engine && stream && out_view);
        engine->implementation.audio_info(stream, out_view);
    });
}

/**
 * @brief 注销数据包订阅
 *
 * ⚠️ 同步排空（Drain）契约：
 *   - 此函数为同步阻塞调用，返回后严格保证：所有正在执行或在途的回调均已彻底完成，且未来绝不会再触发该回调；
 *   - 上层调用者（如 Go CGO）在函数返回后方可安全执行令牌销毁（如 cgo.Handle.Delete()）。
 *
 * @param engine 引擎句柄
 * @param stream 物理流 ID
 * @param subscription 待注销的订阅 ID
 * @return Zhulong_status_t 执行状态码
 */
extern "C" Zhulong_status_t Zhulong_stream_unsubscribe(Zhulong_engine_h engine, Zhulong_stream_id stream,
    Zhulong_subscription_id subscription) {
    return boundary([&] {
        require(engine && stream && subscription);
        // 同步排空：此调用返回后确保无任何正在执行或未来待执行的回调
        engine->implementation.unsubscribe(stream, subscription);
    });
}

/**
 * @brief 注册 AI 结构化推理结果订阅回调
 * @param engine 引擎句柄
 * @param stream 物理流 ID
 * @param callback 推理结果 JSON 数据同步回调
 * @param token 用户上下文令牌
 * @param[out] out_subscription 成功时接收分配的订阅 ID
 * @return Zhulong_status_t 执行状态码
 */
extern "C" Zhulong_status_t Zhulong_stream_subscribe_ai_results(Zhulong_engine_h engine,
    Zhulong_stream_id stream, Zhulong_ai_result_callback callback, uintptr_t token,
    Zhulong_subscription_id *out_subscription) {
    if (out_subscription) *out_subscription = 0;
    return boundary([&] {
        require(engine && stream && callback && out_subscription);
        *out_subscription = engine->implementation.subscribe_ai_results(stream, callback, token);
    });
}

/**
 * @brief 注销 AI 结构化推理结果订阅（同步排空）
 * @param engine 引擎句柄
 * @param stream 物理流 ID
 * @param subscription 待注销的订阅 ID
 * @return Zhulong_status_t 执行状态码
 */
extern "C" Zhulong_status_t Zhulong_stream_unsubscribe_ai_results(Zhulong_engine_h engine,
    Zhulong_stream_id stream, Zhulong_subscription_id subscription) {
    return boundary([&] {
        require(engine && stream && subscription);
        engine->implementation.unsubscribe_ai_results(stream, subscription);
    });
}

/**
 * @brief 向指定流或所有活跃流的 AI FrameBroker 广播下行控制报文
 * @param engine 引擎句柄
 * @param stream 目标流 ID（为 0 表示广播给所有活跃流）
 * @param msg_type 消息类型枚举
 * @param payload 报文负载指针
 * @param payload_size 负载大小（不得超过 64KB）
 * @return Zhulong_status_t 执行状态码
 */
extern "C" Zhulong_status_t Zhulong_engine_broadcast_ai_message(Zhulong_engine_h engine,
    Zhulong_stream_id stream, uint16_t msg_type, const void *payload, size_t payload_size) {
    return boundary([&] {
        require(engine);
        if (payload_size > zhulong::ipc::MAX_IPC_PAYLOAD_SIZE) {
            throw zhulong::Failure{Zhulong_ERR_INVALID_ARGUMENT};
        }
        if (payload_size > 0 && !payload) {
            throw zhulong::Failure{Zhulong_ERR_INVALID_ARGUMENT};
        }
        Zhulong_status_t st = engine->implementation.broadcast_ai_message(stream, msg_type, payload, payload_size);
        if (st != Zhulong_OK) {
            throw zhulong::Failure{st};
        }
    });
}

// ============================================================================
//                               流探测（Probe） API
// ============================================================================

/**
 * @brief 探测 RTSP 视频流元数据
 *
 * 在独立的私有线程与上下文内打开 RTSP 连接，读取流信息后立即断开，不占用常规物理流槽位。
 *
 * @param engine 引擎句柄
 * @param url RTSP 地址字符串
 * @param options 流选项配置指针
 * @param[out] out_result 成功时接收分配的探测结果不透明句柄
 * @return Zhulong_status_t 执行状态码
 */
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

/**
 * @brief 从探测结果句柄中提取只读视频元数据视图
 *
 * ⚠️ 借用指针有效期：
 *   - out_view 内部包含的 extradata 指针为借用指针，指向探测结果句柄内部内存；
 *   - 其生命周期与 Zhulong_probe_result_h 严格绑定，不得在句柄销毁后继续访问。
 *
 * @param result 探测结果句柄
 * @param[out] out_view 接收视频参数视图结构体
 * @return Zhulong_status_t 执行状态码
 */
extern "C" Zhulong_status_t Zhulong_probe_result_view(Zhulong_probe_result_h result, Zhulong_video_view *out_view) {
    if (out_view) *out_view = {};
    return boundary([&] {
        require(result && out_view);
        // 生成借用指针视图（指针有效期与 result 绑定）
        *out_view = result->video.borrowed_view();
    });
}

/**
 * @brief 销毁探测结果句柄并释放内存
 * @param result 待销毁的探测结果句柄
 */
extern "C" void Zhulong_probe_result_destroy(Zhulong_probe_result_h result) {
    if (zhulong::in_callback) return;
    try {
        delete result;
    } catch (...) {}
}

// 宿主不再动态装载任何人脸插件：底库登记的特征提取改由「包可执行文件 +
// --extract-feature 帧协议」完成（internal/faceextract）。
// 旧实现直接装载 libFaceRecognitionPlugin.so、解析两个符号并起 TTL reaper；
// 包形态下 PIE 可执行文件无法被动态装载，那条通道已整体下线。

/**
 * @brief 对指定流当前最新一帧执行抓拍（全幅 + 可选裁剪）
 *
 * 分层职责：
 *   1. 在本文件解析并校验 ABI 参数（归一化坐标、质量、box 数量）;
 *   2. 向 Engine 取最新帧引用（短锁），然后**立即释放 control 锁**;
 *   3. 锁外调用 encode_snapshot() 做像素拷贝与 JPEG 编码。
 *
 * ⚠️ 为什么拆成两段：JPEG 编码在 RK3568 上约 35~60ms/帧。若在持有
 * Engine::control 的期间编码，所有流的 acquire/release/status 都会一起阻塞。
 */
extern "C" Zhulong_status_t Zhulong_stream_snapshot(Zhulong_engine_h engine, Zhulong_stream_id stream,
    const Zhulong_snapshot_request *request, Zhulong_snapshot_h *out_result) {
    if (out_result) *out_result = nullptr;
    return boundary([&] {
        require(engine != nullptr);
        require(stream != 0);
        require(out_result != nullptr);

        // 1. 参数解析与收敛：质量 0 走默认 85，越界夹到 [1,100]
        int32_t quality = 85;
        int32_t box_count = 0;
        int64_t target_pts = 0;
        if (request != nullptr) {
            if (request->quality != 0) {
                quality = request->quality < 1 ? 1 : (request->quality > 100 ? 100 : request->quality);
            }
            box_count = request->box_count;
            target_pts = request->target_pts;
            require(box_count >= 0 && box_count <= ZHULONG_SNAPSHOT_MAX_BOXES);
            if (box_count > 0) {
                require(request->boxes != nullptr);
            }
        }

        // 2. 把 C 数组拷进局部 vector —— 只读一次，避免与调用方内存生命周期纠缠。
        //    ⚠️ 严禁把 request->boxes 的指针带进后续异步路径：C 侧不保证它一直有效。
        std::vector<std::array<float, 4>> boxes;
        boxes.reserve(static_cast<size_t>(box_count));
        for (int32_t i = 0; i < box_count; ++i) {
            const float *src = request->boxes + static_cast<size_t>(i) * 4;
            for (int c = 0; c < 4; ++c) {
                // 非有限值（NaN/Inf）在浮点比较里恒为 false，必须单独拦截，
                // 否则会一路传到 (int)(NaN * width) 的未定义行为。
                require(std::isfinite(src[c]));
            }
            boxes.push_back({src[0], src[1], src[2], src[3]});
        }

        // 3. 取帧（优先按 target_pts 匹配历史帧，未匹配或未指定时退回最新帧，内部短锁），返回后锁已释放
        zhulong::HardwareFrame::ConstPtr frame = engine->implementation.find_frame(stream, target_pts);
        if (!frame) {
            // 流存在但尚未发布过帧（刚接入/重建解码器）。这是暂时状态，不是配置错误。
            throw zhulong::Failure{Zhulong_ERR_NOT_FOUND};
        }

        // 4. 锁外编码
        const Zhulong_status_t rc = zhulong::encode_snapshot(*frame, quality, boxes, out_result);
        if (rc != Zhulong_OK) {
            throw zhulong::Failure{rc};
        }
    });
}

Zhulong_status_t Zhulong_engine_configure_decode(Zhulong_engine_h engine, const Zhulong_decode_policy_t *policy) {
    return boundary([&] {
        require(engine != nullptr);
        require(policy != nullptr);
        require(policy->backend >= 0 && policy->backend <= 2);
        require(policy->max_sessions >= 0);
        engine->implementation.configure_decode(*policy);
    });
}

Zhulong_status_t Zhulong_engine_get_decode_stats(Zhulong_engine_h engine, Zhulong_decode_stats_t *out_stats) {
    return boundary([&] {
        require(engine != nullptr);
        require(out_stats != nullptr);
        engine->implementation.get_decode_stats(*out_stats);
    });
}

