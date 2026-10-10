/**
 * @file engine.hpp
 * @brief Zhulong Native 引擎核心控制器 (物理流池管理、Reaper 与 Probe)
 */

#pragma once

#include "pipeline/stream.hpp"

#include <atomic>
#include <condition_variable>
#include <map>
#include <memory>
#include <mutex>
#include <thread>

namespace zhulong {

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
