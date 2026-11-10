/**
 * @file engine.hpp
 * @brief Zhulong Native 引擎核心控制器 (物理流池管理、Reaper 与 Probe)
 *
 * 核心机制与架构契约：
 * 1. 锁分层与锁外汇合禁令（R15）：
 *    - lifecycle 锁仅串行化 start() / stop() 状态流转；
 *    - control 锁保护物理流池容器、FrameBroker 与消费者映射的快速修改；
 *    - ⚠️ 严禁在持有 control 互斥锁时调用 thread::join() 或同步排空（drain），
 *      以防单路流的阻塞等待引起 status、acquire 等全局控制操作死锁；
 * 2. 8 秒宽限期机制（Grace Period）：
 *    - 当物理流的所有消费者释放后，流进入 8 秒宽限期；
 *    - 宽限期内新消费者接入可直接零等待复用已有 RTSP 连接，超时后由后台 Reaper 线程回收；
 * 3. 5 步优雅停机顺序（5-Step Graceful Shutdown）：
 *    - 第一步：置位 running=false，向所有流发出 request_stop() 停止拉流；
 *    - 第二步：唤醒并等待 Reaper 线程安全汇合；
 *    - 第三步：停止发布线程与 FrameBroker；
 *    - 第四步：同步排空所有 AI 结果订阅（AISubscription）；
 *    - 第五步：汇合所有物理流工作线程并排空在途 Probe 探测任务。
 */

#pragma once

#include "common/view_window.hpp"
#include "nodes/preprocess/window_crop.hpp"
#include "pipeline/stream.hpp"
#include "pipeline/frame_publisher.hpp"
#include "pipeline/ai_subscription.hpp"
#include "algorithm/ipc/frame_broker.hpp"

#include <atomic>
#include <functional>
#include <condition_variable>
#include <map>
#include <memory>
#include <mutex>
#include <set>
#include <thread>

namespace zhulong {

/**
 * @brief 在 control 锁内摘下、锁外停止的退役资源包
 *
 * 锁内只做「从池里移出」，`finish()` 在锁外按固定顺序停机：
 * publisher（汇合发布线程）→ broker → 解码会话 → 需立即退役的流 → 释放 Stream 保活引用。
 *
 * ⚠️ 成员声明顺序即析构安全网：publisher 声明在 broker 之后，因此即使 finish() 未被调用，
 *    隐式析构也是先 publisher（其析构会 stop() 汇合）再 broker，绝不让发布线程触碰已释放的 broker。
 * ⚠️ 析构函数会兜底调用 finish()（含 join），因此本对象必须声明在 control 锁守卫**之前**，
 *    保证异常路径上也是先解锁、后汇合。
 */
struct RetiredResources {
    std::unique_ptr<ipc::FrameBroker> broker;      /**< 待停止的 FrameBroker */
    std::shared_ptr<FramePublisher> publisher;     /**< 待停止的发布线程（持有 broker 裸指针） */
    std::shared_ptr<DecodeSession> session;        /**< 待汇合的解码会话 */
    std::shared_ptr<Stream> stream_to_stop;        /**< 已从池中移除、需立即停止并汇合的流 */
    /** 会话汇合前 Stream 不得析构：DecodeSession 的熔断回调捕获的是 Stream 裸 this */
    std::shared_ptr<Stream> keepalive;

    RetiredResources() = default;
    ~RetiredResources();
    RetiredResources(const RetiredResources &) = delete;
    RetiredResources &operator=(const RetiredResources &) = delete;
    RetiredResources(RetiredResources &&) = delete;
    RetiredResources &operator=(RetiredResources &&) = delete;

    /** @brief 锁外按序停止并汇合全部资源（幂等，必须在 control 锁外调用） */
    void finish();
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

    // per-stream FrameBroker 与 FramePublisher 池 (受 control 互斥锁保护)
    std::map<Zhulong_stream_id, std::unique_ptr<ipc::FrameBroker>> brokers; /**< AI 帧分发 Broker 实例池 */
    std::map<Zhulong_stream_id, std::shared_ptr<FramePublisher>> publishers; /**< AI 帧发布线程池 */
    /**
     * 正在锁外启动 broker 的流 ID（受 control 保护）。同一流的 broker 共用 shm 名与 socket 路径，
     * 并发启动第二个会与第一个争抢同名资源，因此后到者在 broker_settled 上等待前者落定。
     */
    std::set<Zhulong_stream_id> brokers_pending;
    std::condition_variable broker_settled;     /**< brokers_pending 变化或引擎停机时广播 */
    std::string broker_base_dir{"/tmp"};        /**< Broker socket 存放目录 (读取环境变量覆写) */
    std::shared_ptr<WindowCrop> window_crop{std::make_shared<WindowCrop>()}; /**< 宿主侧窗口物化器（与 publisher 共享） */

    std::mutex ai_sub_mutex;                    /**< 保护 AI 结果订阅池 */
    std::map<Zhulong_subscription_id, std::shared_ptr<AISubscription>> ai_subscriptions; /**< AI 结果订阅池 */
    std::atomic<uint64_t> ai_callback_exceptions_{0}; /**< 被隔离的 AI 结果回调异常累计次数 */

    std::thread reaper;                         /**< 定期回收宽限期到期流的清理线程 */
    std::atomic<bool> reaper_stop{true};        /**< 清理线程停止控制信号 */
    std::mutex timer_mutex;                     /**< Reaper 定时唤醒互斥锁 (同时保护 wake_epoch) */
    std::condition_variable timer_wakeup;       /**< Reaper 定时唤醒条件变量 */
    uint64_t wake_epoch = 0;                    /**< 唤醒纪元：任何可能改变最近到期时间的操作都会递增，防止丢失唤醒 */

    std::mutex probe_mutex;                     /**< 探测任务列表互斥锁 */
    std::condition_variable probes_drained;     /**< 探测任务全部排空信号 */
    std::map<Cancellation *, std::shared_ptr<Cancellation>> probes; /**< 活跃中的探测取消对象 */

    Zhulong_decode_policy_t decode_policy_{Zhulong_DECODE_BACKEND_AUTO, 1, 0}; /**< 全局解码策略 */
    std::atomic<uint32_t> active_decode_sessions_{0}; /**< 处于解码状态的物理解码流计数 */
    std::atomic<uint32_t> rejected_requests_{0};      /**< 累计被拒绝的拉流请求计数 */

    Engine() = default;
    ~Engine();

    Engine(const Engine &) = delete;
    Engine &operator=(const Engine &) = delete;
    Engine(Engine &&) = delete;
    Engine &operator=(Engine &&) = delete;

    /** @brief 配置全局解码策略 */
    void configure_decode(const Zhulong_decode_policy_t &policy);

    /** @brief 查询解码器运行态统计信息 */
    void get_decode_stats(Zhulong_decode_stats_t &out_stats);

    /** @brief 启动引擎流池与后台清理线程 */
    void start();

    /** @brief 停止引擎，取消所有拉流与探测，等待全部线程 join */
    void stop();

    /** @brief 后台清理线程函数：休眠至最近的流到期时间，回收已过期的物理流 */
    void reap() noexcept;

    /** @brief 单次回收扫描（允许抛出，由 reap 统一收口） */
    void reap_once();

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

    /** @brief 注册数据包回调订阅 (v5 支持按轨订阅) */
    Zhulong_subscription_id subscribe(Zhulong_stream_id id, uint64_t consumer,
                                      Zhulong_packet_callback callback, uintptr_t token,
                                      uint32_t track_mask = ZHULONG_TRACK_MASK_VIDEO);

    /** @brief 查询指定流的音频流元数据 (v5) */
    void audio_info(Zhulong_stream_id id, Zhulong_audio_view *out_view);

    /** @brief 取消数据包订阅（执行同步排空） */
    void unsubscribe(Zhulong_stream_id id, Zhulong_subscription_id subscription);

    /** @brief 注册 AI 推理结果事件订阅 */
    Zhulong_subscription_id subscribe_ai_results(Zhulong_stream_id id, Zhulong_ai_result_callback callback, uintptr_t token);

    /** @brief 取消 AI 推理结果事件订阅（执行同步排空） */
    void unsubscribe_ai_results(Zhulong_stream_id id, Zhulong_subscription_id subscription);

    /** @brief 分发 AI 推理结果至已注册的回调 */
    void dispatch_ai_result(Zhulong_stream_id stream_id, const char* payload, size_t size);

    /** @brief 获取被隔离的 AI 结果回调异常累计次数 (无锁，relaxed 读取) */
    uint64_t ai_callback_exception_count() const noexcept {
        return ai_callback_exceptions_.load(std::memory_order_relaxed);
    }

    /** @brief 独立同步探测指定 RTSP 流的视频元数据 */
    Video probe(const std::string &url, const Options &options);

    /**
     * @brief 锁外创建并启动该流的 FrameBroker（含结果回调登记）
     *
     * broker 启动会阻塞获取与 Go 侧清扫互斥的目录锁、申请 32MB 槽位内存，
     * 因此**必须在 control 锁外**调用，否则一次慢启动会卡住 status/acquire 等全部控制操作。
     *
     * @return 启动成功的 broker；失败返回空指针（已记录日志）
     */
    std::unique_ptr<ipc::FrameBroker> start_frame_broker(Zhulong_stream_id id, const std::string &base_dir);

    /**
     * @brief 持 control 锁把已启动的 broker 连同新建的 FramePublisher 提交进池
     *
     * @param[in,out] broker  成功时被移入池；失败时移入 discard
     * @param[out]    discard 失败时需在锁外停止的 publisher/broker
     * @return 提交成功返回 true
     *
     * ⚠️ 必须在持有 control 锁时调用；失败时 brokers/publishers 保持调用前状态。
     */
    bool commit_frame_broker_locked(const std::shared_ptr<Stream> &stream,
                                    std::unique_ptr<ipc::FrameBroker> &broker,
                                    RetiredResources &discard);

    /**
     * @brief 持 control 锁注销一个消费者，并把因此退役的资源摘入 out
     *
     * 统一 release() 与 acquire() 回滚两条路径：解码计数、解码会话、无 AI 消费者时的
     * publisher/broker、以及流空闲后的宽限期（或立即退役）。
     *
     * @param retire_immediately 流变为无消费者时立即从池中移除并停止（用于新建流的回滚），
     *                           否则进入宽限期等待 Reaper
     * ⚠️ 必须在持有 control 锁时调用；out 必须在锁外 finish()。
     */
    void detach_consumer_locked(const std::shared_ptr<Stream> &stream, uint64_t consumer,
                                bool retire_immediately, RetiredResources &out);

    /** @brief 获取指定流关联的 FrameBroker socket 绝对路径 */
    std::string get_broker_path(Zhulong_stream_id id);

    /**
     * @brief 采集指定流 FrameBroker 的客户端投递统计快照
     *
     * 只读快照，内部短暂持有 control 锁与 broker 自身的互斥锁；两层锁顺序与
     * broadcast_ai_message 一致（control → broker），不会与发布/回调路径形成环。
     *
     * @param id       物理流 ID
     * @param[out] out 调用方提供的数组，可为空（`capacity == 0` 时仅查询数量）
     * @param capacity 数组容量
     * @return 当前客户端的**总数**（可能大于 capacity，表示结果被截断）
     * @throws Failure Zhulong_ERR_NOT_RUNNING / Zhulong_ERR_NOT_FOUND
     */
    uint32_t broker_client_stats(Zhulong_stream_id id, Zhulong_broker_client_stats *out, uint32_t capacity);

    /** @brief 动态设置指定流的帧发布采样帧率 */
    void set_publish_rate(Zhulong_stream_id id, uint32_t max_sample_fps);

    /**
     * @brief 设置或清除 AI 流的分析视图。
     *
     * 只操作已存在的流（非 acquire）；`ready == false` 时抛出 Zhulong_ERR_NOT_READY，
     * 调用方据此把配置标记为 pending（首个解码帧后由引擎落地）。
     */
    void set_view(Zhulong_stream_id id, bool clear, const ViewRect &rect,
                  ViewRect *out_rect, int64_t *out_since_ms);

    /** @brief 查询当前生效 E 与生效起点（只读，不 acquire）。 */
    void get_view(Zhulong_stream_id id, ViewRect *out_rect, int64_t *out_since_ms);

    /**
     * @brief 向指定流（或所有活跃流）关联的 FrameBroker 广播下行控制消息
     * @param stream_id 目标流 ID（为 0 表示向所有包含 Broker 的活跃流广播）
     * @param msg_type 消息类型（如 MSG_GALLERY_*）
     * @param payload 负载数据
     * @param payload_size 负载大小
     * @return 状态码
     */
    Zhulong_status_t broadcast_ai_message(Zhulong_stream_id stream_id, uint16_t msg_type, const void* payload, size_t payload_size);

    /**
     * @brief 取指定流当前最新帧的只读引用（抓拍用）
     *
     * 仅在该流存在 FramePublisher（即存在 AI 消费者）时可用；否则抛出 Zhulong_ERR_NOT_FOUND。
     * 返回的 shared_ptr 持有期间底层解码 buffer 不会被复用，因此调用方必须尽快完成
     * 像素拷贝（典型耗时 < 5ms）并释放。
     *
     * ⚠️ 本方法内部会短暂持有 control 锁取 publisher 指针，但**在锁外**读取最新帧，
     * 因此与解码/发布线程之间无锁竞争。严禁在回调上下文调用。
     *
     * @param id 物理流 ID
     * @return 最新帧只读指针；流存在但尚未发布过帧时返回空指针
     * @throws Failure Zhulong_ERR_NOT_RUNNING / Zhulong_ERR_NOT_FOUND
     */
    HardwareFrame::ConstPtr latest_frame(Zhulong_stream_id id);

    /**
     * @brief 获取指定流指定 PTS 对应的历史帧引用（抓拍专用）
     *
     * @param id 物理流 ID
     * @param pts 目标帧时间戳 (pts <= 0 时等价于 latest_frame())
     * @return 匹配帧只读指针；流存在但尚未发布过帧时返回空指针
     * @throws Failure Zhulong_ERR_NOT_RUNNING / Zhulong_ERR_NOT_FOUND
     */
    HardwareFrame::ConstPtr find_frame(Zhulong_stream_id id, int64_t pts);
};

} // namespace zhulong
