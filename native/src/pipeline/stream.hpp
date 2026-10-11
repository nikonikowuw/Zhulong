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
    bool has_pts{false};
    bool has_dts{false};
    int32_t time_base_num{1};
    int32_t time_base_den{90000};
    int32_t codec{0};
    bool key_frame{false};
};

/**
 * @brief 独立可重建的解码会话 (生命周期随 AI 消费者接入/释放而创建与销毁)
 */
class DecodeSession {
public:
    DecodeSession(uint64_t stream_id, Video video_meta);
    ~DecodeSession();

    /** @brief 协作式停止并唤醒队列 */
    void request_stop();

    /** @brief 等待解码 Worker 线程退出 (严禁在持锁状态下调用) */
    void join();

    /** @brief 推送压缩数据包至解码队列 (含关键帧重同步逻辑) */
    bool push_packet(const Zhulong_packet_view &packet);

    /** @brief 从输出队列拉取已解码的硬件帧 (带超时，供 AI 消费者借用) */
    bool pop_frame(HardwareFrame::ConstPtr &out_frame,
                   std::chrono::milliseconds timeout = std::chrono::milliseconds(50));

    /** @brief 动态更新流元数据 (如连接就绪后补充的 SPS/PPS extradata) */
    void update_metadata(const Video &video_meta);

    /** @brief 获取当前帧队列中的元素个数 */
    size_t frame_queue_size() const;

    /** @brief 获取当前包队列中的元素个数 */
    size_t packet_queue_size() const;

    /** @brief 检查当前会话是否处于活跃状态 */
    bool is_active() const;

private:
    void decode_loop() noexcept;

    /** @brief 取得至少 size 字节容量的复用缓冲 (避免每包堆分配) */
    std::vector<uint8_t> acquire_buffer(size_t size);

    /** @brief 归还包缓冲供后续复用 */
    void recycle_buffer(std::vector<uint8_t> &&buffer);

    const uint64_t stream_id_{0};
    mutable std::mutex meta_mutex_;
    Video video_meta_;
    std::atomic<int32_t> time_base_num_{0};     /**< 元数据时间基缓存，供生产者热路径免锁读取 */
    std::atomic<int32_t> time_base_den_{0};
    std::atomic<uint64_t> meta_generation_{0};  /**< 编码/extradata 变化时自增，驱动解码器重建 */

    std::atomic<bool> active_{true};
    /** 初始即等待关键帧：会话可能在 GOP 中途创建，禁止把孤立 P 帧喂入解码器 */
    std::atomic<bool> waiting_keyframe_{true};

    std::mutex pool_mutex_;
    std::vector<std::vector<uint8_t>> buffer_pool_;

    BoundedQueue<QueuedPacket> packet_queue_{60, OverflowStrategy::DROP_NEWEST};
    BoundedQueue<HardwareFrame::ConstPtr> frame_queue_{3, OverflowStrategy::DROP_OLDEST};
    std::thread worker_;
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
    mutable std::mutex mutex;                   /**< 保护 subscriptions, status 与 decode_session_ 的互斥锁 */
    std::map<Zhulong_subscription_id, std::shared_ptr<Subscription>> subscriptions; /**< 订阅者列表 */
    using SubscriberList = std::vector<std::shared_ptr<Subscription>>;
    std::shared_ptr<const SubscriberList> subscriber_snapshot_{std::make_shared<const SubscriberList>()}; /**< 不可变快照，随 subscriptions 变更重建 (持有 mutex 时写) */
    Zhulong_stream_status status{Zhulong_STREAM_STARTING, Zhulong_OK};              /**< 流当前运行状态 */

    Video video_meta_{};                        /**< 缓存的 RTSP 视频流元数据 */
    std::shared_ptr<DecodeSession> decode_session_{nullptr}; /**< 动态解码会话 (按需激活) */

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

    /** @brief 流就绪时回调，接收视频元数据 */
    void on_ready(const Video &video);

    /** @brief 将 subscriptions 重新发布为不可变快照 (调用方必须持有 mutex) */
    void rebuild_subscribers();

    /** @brief 将解析出的数据包派发至该流的所有活跃订阅者及解码队列 */
    void dispatch(const Zhulong_packet_view &packet);

    /** @brief 检查当前是否有 AI 消费者 */
    bool has_ai_consumer() const;

    /**
     * @brief 根据当前消费者列表动态调整解码会话启停
     * @return 若需要休眠，返回需要停止的旧会话对象 (调用方须在锁外调用 stop_decode_session)
     */
    std::shared_ptr<DecodeSession> sync_decode_pipeline();

    /** @brief 在锁外安全停止并汇合指定的解码会话 */
    static void stop_decode_session(std::shared_ptr<DecodeSession> session);

    /** @brief 原生 C++ 消费/拉取解码帧 (只读借用) */
    bool acquire_frame(HardwareFrame::ConstPtr &out_frame,
                       std::chrono::milliseconds timeout = std::chrono::milliseconds(50));

    /** @brief 查询当前解码帧队列积压数 */
    size_t frame_queue_size() const;

    /** @brief 检查当前是否存在活跃的解码会话 */
    bool has_active_decoder() const;
};

} // namespace zhulong
