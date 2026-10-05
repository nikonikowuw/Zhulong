/**
 * @file engine.hpp
 * @brief Zhulong Native 引擎内部核心管道（Pipeline）与流管理器定义
 *
 * 核心架构与线程/锁模型（⚠️ 极其关键）：
 * 1. 锁层次划分（严格防死锁）：
 *    - Engine::lifecycle：仅用于串行化 start() 与 stop() 状态转换。拉流 Worker 线程与 Reaper 线程绝不访问此锁。
 *    - Engine::control：保护流池（streams 容器）、引用计数与全局流 ID 递增。
 *      ⚠️ 规则：任何长时间操作（如 join 线程、等待回调排空 drain）前必须释放 control 锁，绝不可在持有锁期间阻塞！
 *    - Stream::mutex：保护单个物理流内部的订阅者列表（subscriptions）与状态变更（status）。
 *    - Subscription::mutex：保护单个订阅通道的 enabled 与 active 状态。
 * 2. 回调安全与同步排空（Drain）：
 *    - Subscription::invoke 派发在独立于 control 锁的快照中进行；
 *    - Subscription::disable_and_drain 保证在返回后无任何回调在执行，便于 Go 侧安全释放 cgo.Handle。
 * 3. 宽限期回收机制（Reaper Thread）：
 *    - 当物理流的所有消费者释放时，流不会立即关闭，而是进入 8 秒宽限期；
 *    - 后台 Reaper 线程负责周期性回收超时的物理流并执行 join，防止资源泄漏或过早断开。
 */

#pragma once

#include "nodes/capture/rtsp_input.hpp"

#include <condition_variable>
#include <map>
#include <memory>
#include <mutex>
#include <thread>

namespace zhulong {

/**
 * @brief 线程局部变量：标记当前线程是否正处于用户包回调调用链中
 * 用于 C ABI 边界防自死锁与非法重入检测。
 */
extern thread_local bool in_callback;

/**
 * @brief 数据包订阅通道上下文
 *
 * 维护用户注册的回调函数与状态，支持在单生产者模型下的安全调用与同步排空。
 */
struct Subscription {
    uint64_t consumer;                          /**< 关联的预览消费者 ID */
    Zhulong_packet_callback callback;           /**< 用户提供的包回调函数指针 */
    uintptr_t token;                            /**< 用户上下文令牌（不可保留为裸 Go 指针） */
    std::mutex mutex;                           /**< 保护 enabled 与 active 状态 */
    std::condition_variable drained;            /**< 当 active 变为 false 时发出的排空信号 */
    bool enabled = true;                        /**< 订阅是否有效，置 false 即拒绝后续派发 */
    bool active = false;                        /**< 当前是否有回调正在执行中（单流单生产者） */

    Subscription(uint64_t consumer_id, Zhulong_packet_callback function, uintptr_t value)
        : consumer(consumer_id), callback(function), token(value) {}

    /**
     * @brief 派发数据包给回调函数（在锁外调用，具备异常安全防护）
     * @param packet 数据包视图借用指针
     */
    void invoke(const Zhulong_packet_view &packet);

    /**
     * @brief 禁用该订阅并阻塞等待所有正在执行的回调彻底排空
     */
    void disable_and_drain();
};

/**
 * @brief 物理 RTSP 输入流上下文
 *
 * 每个物理流对应一条真实的 RTSP 连接，可在多个消费者之间复用。
 */
struct Stream {
    const Zhulong_stream_id id;                 /**< 物理流全局唯一 ID */
    const std::string url;                      /**< 规范化后的 RTSP URL */
    const Options options;                      /**< 传输与超时配置 */

    // consumers 容器与 expiry 时间点仅在持有 Engine::control 互斥锁时访问
    std::map<uint64_t, int32_t> consumers;      /**< 当前复用该流的消费者映射 (consumer_id -> kind) */
    Clock::time_point expiry = Clock::time_point::max(); /**< 宽限期到期时间点，默认不过期 */

    Cancellation cancel;                        /**< 控制拉流 Worker 协作式取消状态 */
    std::thread worker;                         /**< 负责 FFmpeg 阻塞读取与解包的 Worker 线程 */
    std::mutex mutex;                           /**< 保护 subscriptions 与 status 的互斥锁 */
    std::map<Zhulong_subscription_id, std::shared_ptr<Subscription>> subscriptions; /**< 订阅者列表 */
    Zhulong_stream_status status{Zhulong_STREAM_STARTING, Zhulong_OK};              /**< 流当前运行状态 */

    Stream(Zhulong_stream_id stream_id, std::string normalized_url, Options stream_options);
    ~Stream();

    /** @brief 启动流拉取后台 Worker 线程 */
    void start();

    /** @brief 请求停止流拉取（置 cancel 标志） */
    void request_stop();

    /** @brief 等待 Worker 线程完全退出 */
    void join();

    /** @brief Worker 线程入口函数，负责调用底层 FFmpeg 循环拉流 */
    void capture() noexcept;

    /** @brief 将解析出的数据包派发至该流的所有活跃订阅者 */
    void dispatch(const Zhulong_packet_view &packet);
};

/**
 * @brief Native 引擎核心控制器
 *
 * 统一管理物理流池（Stream Pool）、后台回收线程（Reaper）、流生命周期与独立探测任务（Probe）。
 */
struct Engine {
    // 生命周期互斥锁：仅串行化 start() 与 stop() 接口调用，Worker 和 Reaper 绝不持有此锁
    std::mutex lifecycle;

    // 流池控制锁：保护 streams 容器及消费者引用计数的快速修改
    // ⚠️ 规则：在执行 join() 或 drain() 等阻塞等待前，必须释放此锁
    std::mutex control;

    // 当流从 streams 中彻底移除并 join 完毕后触发，用于唤醒等待旧连接关闭的 acquire 调用
    std::condition_variable stream_retired;

    bool running = false;                       /**< 引擎运行状态 */
    uint64_t next_stream = 1;                   /**< 物理流 ID 生成器 */
    uint64_t next_subscription = 1;             /**< 订阅 ID 生成器 */
    std::map<Zhulong_stream_id, std::shared_ptr<Stream>> streams; /**< 物理流池 (ID -> Stream) */

    std::thread reaper;                         /**< 定期回收宽限期到期流的清理线程 */
    std::atomic<bool> reaper_stop{true};        /**< 清理线程停止控制信号 */
    std::mutex timer_mutex;                     /**< Reaper 定时唤醒互斥锁 */
    std::condition_variable timer_wakeup;       /**< Reaper 定时唤醒条件变量 */

    std::mutex probe_mutex;                     /**< 探测任务列表互斥锁 */
    std::condition_variable probes_drained;     /**< 探测任务全部排空信号 */
    std::map<Cancellation *, std::shared_ptr<Cancellation>> probes; /**< 活跃中的探测取消对象 */

    ~Engine();

    /** @brief 启动引擎流池与后台清理线程 */
    void start();

    /** @brief 停止引擎，取消所有拉流与探测，等待全部线程 join */
    void stop();

    /** @brief 后台清理线程函数：周期性扫描并释放已过期的物理流 */
    void reap() noexcept;

    /** @brief 根据流 ID 查找物理流对象（必须在 running 为 true 时调用） */
    std::shared_ptr<Stream> find_stream(Zhulong_stream_id id);

    /** @brief 获取或复用物理流（支持按 URL 与 Options 匹配） */
    Zhulong_stream_id acquire(const std::string &url, const Options &options, uint64_t consumer, int32_t kind);

    /** @brief 释放消费者对物理流的引用，若无消费者则启动宽限期计时 */
    void release(Zhulong_stream_id id, uint64_t consumer);

    /** @brief 查询指定流的当前运行状态 */
    Zhulong_stream_status status(Zhulong_stream_id id);

    /** @brief 注册数据包回调订阅 */
    Zhulong_subscription_id subscribe(Zhulong_stream_id id, uint64_t consumer, Zhulong_packet_callback callback, uintptr_t token);

    /** @brief 取消数据包订阅（执行同步排空） */
    void unsubscribe(Zhulong_stream_id id, Zhulong_subscription_id subscription);

    /** @brief 独立同步探测指定 RTSP 流的视频元数据 */
    Video probe(const std::string &url, const Options &options);
};

} // namespace zhulong
