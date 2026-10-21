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

void zhulong_face_plugin_engine_created();
void zhulong_face_plugin_engine_destroyed();

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
        zhulong_face_plugin_engine_created();
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
        zhulong_face_plugin_engine_destroyed();
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

/**
 * @brief 注册视频压缩数据包订阅回调（用于 Preview 预览）
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
    if (out_subscription) *out_subscription = 0;
    return boundary([&] {
        require(engine && stream && preview_consumer_id && callback && out_subscription);
        *out_subscription = engine->implementation.subscribe(stream, preview_consumer_id, callback, token);
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

/**
 * @brief 人脸识别插件动态加载、Fail-Closed 报错与 TTL 空闲自动卸载管理器
 *
 * 核心设计：
 * 1. 懒加载（Lazy dlopen）：首次提取请求才挂载 libFaceRecognitionPlugin.so 并启动后台 Reaper 线程；
 * 2. 闭环报错（Fail-Closed）：插件缺失或符号缺失时显式返回 Zhulong_ERR_UNSUPPORTED，
 *    绝不允许回退到数学模拟特征，防止向人员底库写入无效向量；
 * 3. TTL 空闲回收：空闲超过 ZHULONG_FACE_PLUGIN_IDLE_TIMEOUT_SEC（默认 60 秒）后，
 *    Reaper 调用插件的 zhulong_face_extract_release 并 dlclose，把模型常驻内存交还操作系统；
 * 4. 生命周期汇合：最后一个引擎句柄销毁时同步停止 Reaper 并卸载，杜绝异步悬挂。
 *
 * 并发契约：extract() 全程持有 mu_，保证 Reaper 与 shutdown() 不会在推理执行途中
 * dlclose 掉正在运行的代码段（Use-After-Unload）。
 */
/**
 * @brief 动态库文件扩展名（含前导点）
 *
 * dlopen 需要真实文件名，而各平台共享库后缀不同：Linux 用 .so，macOS 用 .dylib。
 * 候选路径表必须由它拼装，硬编码 .so 会让加载器在 macOS 上永远 miss。
 */
#if defined(__APPLE__)
constexpr const char* kPluginExt = ".dylib";
#else
constexpr const char* kPluginExt = ".so";
#endif

class FacePluginLoader {
public:
    using ExtractFn = zhulong_status_t (*)(const uint8_t*, int32_t, int32_t, int32_t, int32_t, float*, uint32_t*);
    using ReleaseFn = void (*)();

    /** @brief 进程内唯一实例（有意泄漏，规避静态析构顺序问题） */
    static FacePluginLoader& instance() {
        static FacePluginLoader* inst = new FacePluginLoader();
        return *inst;
    }

    void on_engine_created() {
        engine_count_.fetch_add(1, std::memory_order_relaxed);
    }

    void on_engine_destroyed() {
        // 仅当最后一个引擎句柄被销毁时才真正卸载插件与模型资源
        if (engine_count_.fetch_sub(1, std::memory_order_acq_rel) <= 1) {
            engine_count_.store(0, std::memory_order_relaxed);
            shutdown();
        }
    }

    zhulong_status_t extract(
        const uint8_t* pixel_data,
        int32_t width,
        int32_t height,
        int32_t stride,
        int32_t format,
        float* out_features,
        uint32_t* feature_dim
    ) {
        std::lock_guard<std::mutex> lock(mu_);
        zhulong_status_t st = ensure_loaded_locked();
        if (st != Zhulong_OK) {
            return st;
        }

        last_active_ = std::chrono::steady_clock::now();
        // 唤醒 Reaper 以最新活跃时间重算等待窗口（避免其按旧时间戳提前卸载）
        cv_.notify_all();
        return extract_fn_(pixel_data, width, height, stride, format, out_features, feature_dim);
    }

    void shutdown() {
        {
            std::lock_guard<std::mutex> lock(mu_);
            stop_reaper_ = true;
            cv_.notify_all();
            unload_locked();
        }
        if (reaper_thread_.joinable()) {
            reaper_thread_.join();
        }
    }

private:
    FacePluginLoader() {
        int timeout_sec = 60;
        const char* env_timeout = std::getenv("ZHULONG_FACE_PLUGIN_IDLE_TIMEOUT_SEC");
        if (env_timeout && env_timeout[0] != '\0') {
            try {
                int parsed = std::stoi(env_timeout);
                if (parsed > 0) timeout_sec = parsed;
            } catch (...) {}
        }
        idle_timeout_ = std::chrono::seconds(timeout_sec);
    }

    FacePluginLoader(const FacePluginLoader&) = delete;
    FacePluginLoader& operator=(const FacePluginLoader&) = delete;

    /**
     * @brief 按优先级生成插件寻址候选列表
     *
     * ZHULONG_FACE_PLUGIN_PATH 为显式运维覆盖：一旦设置即为唯一候选，
     * 不再静默回退到其它路径的同名插件，避免加载到非预期的实现。
     */
    std::vector<std::string> candidate_paths() const {
        const char* env_path = std::getenv("ZHULONG_FACE_PLUGIN_PATH");
        if (env_path && env_path[0] != '\0') {
            return {std::string(env_path)};
        }

        // 插件文件名随平台变化：Linux 是 .so，macOS 是 .dylib。硬编码 .so 会让
        // 加载器在 macOS 上永远找不到插件，extract 一路返回 UNSUPPORTED，
        // 把「模型/插件不可用」伪装成正常路径。
        const std::vector<std::string> file_names = {
            "libFaceRecognitionPlugin" + std::string(kPluginExt),
            "FaceRecognitionPlugin" + std::string(kPluginExt),
            std::string("plugin") + kPluginExt,
        };

        std::vector<std::string> candidates;
        const char* env_dir = std::getenv("ZHULONG_PLUGIN_DIR");
        if (env_dir && env_dir[0] != '\0') {
            for (const auto& name : file_names) {
                candidates.push_back(std::string(env_dir) + "/" + name);
            }
        }

        const std::vector<std::string> rel_dirs = {
            "build/native/host",
            "packages/face_recognition/targets/cpu",
            "",
            ".",
            "../build/native/host",
            "../../build/native/host",
            "../../../build/native/host",
        };
        for (const auto& dir : rel_dirs) {
            for (const auto& name : file_names) {
                candidates.push_back(dir.empty() ? name : dir + "/" + name);
            }
        }

        Dl_info info;
        if (dladdr(reinterpret_cast<void*>(&Zhulong_engine_extract_face_feature), &info) && info.dli_fname) {
            std::string so_path = info.dli_fname;
            auto pos = so_path.find_last_of('/');
            if (pos != std::string::npos) {
                std::string dir = so_path.substr(0, pos);
                for (const auto& name : file_names) {
                    candidates.push_back(dir + "/" + name);
                    candidates.push_back(dir + "/../build/native/host/" + name);
                }
            }
        }
        return candidates;
    }

    zhulong_status_t ensure_loaded_locked() {
        if (handle_ && extract_fn_) {
            return Zhulong_OK;
        }

        for (const auto& path : candidate_paths()) {
            if (!std::ifstream(path).good()) {
                continue;
            }
            void* h = dlopen(path.c_str(), RTLD_NOW | RTLD_LOCAL);
            if (!h) {
                continue;
            }
            auto extract_fn = reinterpret_cast<ExtractFn>(dlsym(h, "zhulong_face_extract_feature"));
            if (!extract_fn) {
                dlclose(h);
                continue;
            }

            handle_ = h;
            extract_fn_ = extract_fn;
            release_fn_ = reinterpret_cast<ReleaseFn>(dlsym(h, "zhulong_face_extract_release"));
            last_active_ = std::chrono::steady_clock::now();
            if (!reaper_thread_.joinable()) {
                stop_reaper_ = false;
                reaper_thread_ = std::thread(&FacePluginLoader::reaper_loop, this);
            }
            cv_.notify_all();
            return Zhulong_OK;
        }

        return Zhulong_ERR_UNSUPPORTED;
    }

    void unload_locked() {
        if (!handle_) return;
        if (release_fn_) {
            try {
                release_fn_();
            } catch (...) {}
        }
        dlclose(handle_);
        handle_ = nullptr;
        extract_fn_ = nullptr;
        release_fn_ = nullptr;
    }

    void reaper_loop() {
        std::unique_lock<std::mutex> lock(mu_);
        while (!stop_reaper_) {
            if (handle_ == nullptr) {
                cv_.wait(lock, [this] { return stop_reaper_ || handle_ != nullptr; });
                continue;
            }

            auto now = std::chrono::steady_clock::now();
            auto elapsed = std::chrono::duration_cast<std::chrono::seconds>(now - last_active_);
            if (elapsed >= idle_timeout_) {
                unload_locked();
            } else {
                auto wait_time = idle_timeout_ - elapsed;
                cv_.wait_for(lock, wait_time, [this] { return stop_reaper_ || handle_ == nullptr; });
            }
        }
    }

    std::mutex mu_;
    std::condition_variable cv_;
    void* handle_{nullptr};
    ExtractFn extract_fn_{nullptr};
    ReleaseFn release_fn_{nullptr};
    std::chrono::steady_clock::time_point last_active_{};
    std::chrono::seconds idle_timeout_{60};
    bool stop_reaper_{false};
    std::thread reaper_thread_;
    std::atomic<int> engine_count_{0};
};

void zhulong_face_plugin_engine_created() {
    FacePluginLoader::instance().on_engine_created();
}

void zhulong_face_plugin_engine_destroyed() {
    FacePluginLoader::instance().on_engine_destroyed();
}

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
        if (request != nullptr) {
            if (request->quality != 0) {
                quality = request->quality < 1 ? 1 : (request->quality > 100 ? 100 : request->quality);
            }
            box_count = request->box_count;
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

        // 3. 取最新帧（内部短锁），返回后锁已释放
        zhulong::HardwareFrame::ConstPtr frame = engine->implementation.latest_frame(stream);
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

/**
 * @brief 从图像像素数据同步提取 512 维人脸特征向量
 */
extern "C" Zhulong_status_t Zhulong_engine_extract_face_feature(
    Zhulong_engine_h engine,
    const uint8_t *pixel_data,
    int32_t width,
    int32_t height,
    int32_t stride,
    int32_t format,
    float *out_features,
    uint32_t *feature_dim
) {
    (void)engine;
    return boundary([&] {
        require(pixel_data != nullptr);
        require(width > 0 && height > 0);
        require(out_features != nullptr);
        require(feature_dim != nullptr && *feature_dim >= 512);

        // 仅支持 RGB24 (2) 与 BGR24 (1)
        if (format != 1 && format != 2) {
            throw zhulong::Failure{Zhulong_ERR_UNSUPPORTED};
        }

        const int64_t min_stride = static_cast<int64_t>(width) * 3;
        int effective_stride = (stride == 0) ? static_cast<int>(min_stride) : stride;
        if (effective_stride < min_stride) {
            throw zhulong::Failure{Zhulong_ERR_INVALID_ARGUMENT};
        }

        zhulong_status_t rc = FacePluginLoader::instance().extract(
            pixel_data, width, height, effective_stride, format, out_features, feature_dim
        );
        if (rc != Zhulong_OK) {
            throw zhulong::Failure{rc};
        }
    });
}
