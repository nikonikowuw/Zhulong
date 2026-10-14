/**
 * @file engine.hpp
 * @brief Zhulong Native 引擎核心控制器 (物理流池管理、Reaper 与 Probe)
 */

#pragma once

#include "pipeline/stream.hpp"
#include "pipeline/frame_publisher.hpp"
#include "algorithm/ipc/frame_broker.hpp"

#include <atomic>
#include <functional>
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

    // per-stream FrameBroker 与 FramePublisher 池 (受 control 互斥锁保护)
    std::map<Zhulong_stream_id, std::unique_ptr<ipc::FrameBroker>> brokers; /**< AI 帧分发 Broker 实例池 */
    std::map<Zhulong_stream_id, std::unique_ptr<FramePublisher>> publishers; /**< AI 帧发布线程池 */
    std::string broker_base_dir{"/tmp"};        /**< Broker socket 存放目录 (读取环境变量覆写) */

    std::thread reaper;                         /**< 定期回收宽限期到期流的清理线程 */
    std::atomic<bool> reaper_stop{true};        /**< 清理线程停止控制信号 */
    std::mutex timer_mutex;                     /**< Reaper 定时唤醒互斥锁 (同时保护 wake_epoch) */
    std::condition_variable timer_wakeup;       /**< Reaper 定时唤醒条件变量 */
    uint64_t wake_epoch = 0;                    /**< 唤醒纪元：任何可能改变最近到期时间的操作都会递增，防止丢失唤醒 */

    std::mutex probe_mutex;                     /**< 探测任务列表互斥锁 */
    std::condition_variable probes_drained;     /**< 探测任务全部排空信号 */
    std::map<Cancellation *, std::shared_ptr<Cancellation>> probes; /**< 活跃中的探测取消对象 */

    ~Engine();

    /** @brief 启动引擎流池与后台清理线程 */
    void start();

    /** @brief 停止引擎，取消所有拉流与探测，等待全部线程 join */
    void stop();

    /** @brief 后台清理线程函数：休眠至最近的流到期时间，回收已过期的物理流 */
    void reap() noexcept;

    /** @brief 唤醒清理线程重新计算最近到期时间 (可在持有 control 时调用，不会反向加锁) */
    void wake_reaper();

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

    /**
     * @brief 为该流按需创建 FrameBroker 与 FramePublisher
     *
     * 仅在 kind == Zhulong_CONSUMER_AI 且该流尚无 broker 时生效。
     * @param rollback broker 启动失败时由调用方执行的补偿动作（回滚消费者注册等）
     * @return 创建成功返回 true；失败时已执行 rollback 并返回 false
     *
     * ⚠️ 必须在持有 control 锁时调用；失败时本函数不改变 brokers/publishers。
     */
    bool ensure_frame_broker(const std::shared_ptr<Stream> &stream, int32_t kind, const std::function<void()> &rollback);

    /** @brief 获取指定流关联的 FrameBroker socket 绝对路径 */
    std::string get_broker_path(Zhulong_stream_id id);

    /** @brief 动态设置指定流的帧发布采样帧率 */
    void set_publish_rate(Zhulong_stream_id id, uint32_t max_sample_fps);
};

} // namespace zhulong
