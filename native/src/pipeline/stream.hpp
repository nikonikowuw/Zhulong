/**
 * @file stream.hpp
 * @brief 物理流上下文与节点处理流水线 (Capture ➔ Decode ➔ Dispatch)
 */

#pragma once

#include "Zhulong/engine.h"
#include "Zhulong/frame.hpp"
#include "nodes/capture/rtsp_input.hpp"
#include "nodes/decode/decoder.hpp"
#include "pipeline/bounded_queue.hpp"
#include "pipeline/subscription.hpp"

#include <atomic>
#include <map>
#include <memory>
#include <mutex>
#include <string>
#include <thread>
#include <vector>

namespace zhulong {

/** @brief 解码队列数据包缓存 */
struct QueuedPacket {
    std::vector<uint8_t> data;
    int64_t pts{0};
    int64_t dts{0};
    int32_t codec{0};
    int32_t key_frame{0};
};

/**
 * @brief 物理 RTSP 输入流上下文及节点流水线拓扑
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
    std::mutex mutex;                           /**< 保护 subscriptions, status 与 decode 状态的互斥锁 */
    std::map<Zhulong_subscription_id, std::shared_ptr<Subscription>> subscriptions; /**< 订阅者列表 */
    Zhulong_stream_status status{Zhulong_STREAM_STARTING, Zhulong_OK};              /**< 流当前运行状态 */

    // 解码节点与有界队列 (按需激活)
    BoundedQueue<QueuedPacket> packet_queue{60, OverflowStrategy::DROP_OLDEST};
    BoundedQueue<HardwareFrame::ConstPtr> frame_queue{3, OverflowStrategy::DROP_OLDEST};
    std::thread decode_worker;
    std::atomic<bool> decode_active{false};

    Stream(Zhulong_stream_id stream_id, std::string normalized_url, Options stream_options);
    ~Stream();

    /** @brief 启动流拉取后台 Worker 线程 */
    void start();

    /** @brief 请求停止流拉取与解码 Worker */
    void request_stop();

    /** @brief 等待所有 Worker 线程完全退出 */
    void join();

    /** @brief Worker 线程入口函数，负责调用底层 FFmpeg 循环拉流 */
    void capture() noexcept;

    /** @brief 解码 Worker 线程入口函数，负责消费 packet_queue 并产出 HardwareFrame */
    void decode_loop() noexcept;

    /** @brief 将解析出的数据包派发至该流的所有活跃订阅者及解码队列 */
    void dispatch(const Zhulong_packet_view &packet);

    /** @brief 检查当前是否有 AI 消费者 */
    bool has_ai_consumer() const;

    /** @brief 根据当前消费者列表动态调整解码器启停 */
    void sync_decode_pipeline();
};

} // namespace zhulong
