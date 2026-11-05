/**
 * @file stream.hpp
 * @brief 物理流上下文与节点处理流水线 (Capture ➔ Decode ➔ Dispatch)
 *
 * 核心架构与设计原则：
 * 1. 独立可重建解码会话（DecodeSession）：
 *    - 解决生命周期失配问题：首个 AI 消费者接入时按需创建会话，最后一个 AI 消费者释放时移出锁外安全销毁；
 *    - 再次接入时重新分配未取消的干净会话，严禁复用已取消的有界队列；
 * 2. 拥塞重同步与关键帧门控（Congestion & Keyframe Resync）：
 *    - 视频 P/B 帧具有前序参考依赖，包队列满载或解码出错时激活 waiting_keyframe_；
 *    - 丢弃积压旧包并拒绝后续非关键帧，直到收到完整 IDR/关键帧，清空 DPB 重新解码，杜绝花屏与延迟累积；
 * 3. 优先级分发与异常隔离（Prioritized Dispatch & Exception Isolation）：
 *    - 先将数据包送入 AI 解码队列，再通过不可变快照在锁外并发调用 Preview 订阅回调；
 *    - 逐订阅者隔离回调异常，违约抛错的订阅者静默失效，不影响同流其他订阅者与主媒体捕获；
 * 4. 时间戳保真与延迟回收：
 *    - 严格保留 has_pts / has_dts 及原流时基；
 *    - 解码帧封装为 HardwareFrame::ConstPtr 只读借用，绑定 release_fn 闭包，由 shared_ptr 保证多消费者安全访问与延迟回收。
 */

#pragma once

#include "Zhulong/engine.h"
#include "Zhulong/frame.hpp"
#include "nodes/capture/rtsp_input.hpp"
#include "nodes/decode/decoder.hpp"
#include "pipeline/bounded_queue.hpp"
#include "common/view_window.hpp"
#include "pipeline/preview_normalizer.hpp"
#include "pipeline/audio_normalizer.hpp"
#include "pipeline/subscription.hpp"

#include <atomic>
#include <map>
#include <memory>
#include <mutex>
#include <string>
#include <thread>
#include <vector>

namespace zhulong {

/** @brief 解码会话累计丢包与重同步计数快照 (仅供测试与运维观测，不跨越 C ABI) */
struct DecodeCounters {
    uint64_t decoder_unavailable_drops{0}; /**< 解码器不可用（退避窗口内 / codec 无效 / 创建失败）导致的丢包数 */
    uint64_t keyframe_gate_drops{0};       /**< 关键帧门控拒绝的包数（生产者侧 + 解码线程侧） */
    uint64_t congestion_resyncs{0};        /**< 队列溢出触发的关键帧重同步次数 */
};

/**
 * @brief 解码队列数据包缓存结构体
 *
 * 内部持久化压缩数据，并保真记录源流时间戳与时基。
 */
struct QueuedPacket {
    std::vector<uint8_t> data;             /**< 压缩数据载荷（预留尾部填充空间） */
    int64_t pts{0};                        /**< 显示时间戳 (仅当 has_pts 为 true 时有效) */
    int64_t dts{0};                        /**< 解码时间戳 (仅当 has_dts 为 true 时有效) */
    bool has_pts{false};                   /**< 是否包含有效的显示时间戳 */
    bool has_dts{false};                   /**< 是否包含有效的解码时间戳 */
    int32_t time_base_num{1};              /**< 时间基分子 */
    int32_t time_base_den{90000};          /**< 时间基分母 */
    int32_t codec{0};                      /**< 视频编码格式 */
    bool key_frame{false};                 /**< 是否为关键帧 / IDR 帧 */
};

/**
 * @brief 独立可重建的解码会话 (生命周期随 AI 消费者接入/释放而创建与销毁)
 *
 * 封装单路物理流的独立软硬件解码工作线程与有界队列。
 */
class DecodeSession {
public:
    /**
     * @brief 构造解码会话并启动解码工作线程
     * @param stream_id 关联的物理流 ID
     * @param video_meta 初始视频元数据
     * @param policy 全局解码策略
     * @param on_fatal_error 致命硬件/解码错误回调 (熔断通知)
     */
    DecodeSession(uint64_t stream_id, Video video_meta,
                  Zhulong_decode_policy_t policy = {Zhulong_DECODE_BACKEND_AUTO, 1, 0},
                  std::function<void(Zhulong_status_t)> on_fatal_error = nullptr);

    /** @brief 析构函数，请求停止并汇合解码工作线程 */
    ~DecodeSession();

    /** @brief 协作式停止并唤醒队列 */
    void request_stop();

    /**
     * @brief 等待解码 Worker 线程退出
     *
     * ⚠️ 锁外汇合禁令：严禁在持有 Stream::mutex 或 Engine::control 锁的状态下调用本函数。
     */
    void join();

    /**
     * @brief 推送压缩数据包至解码队列
     *
     * 包含拥塞检测与关键帧重同步逻辑：
     *   - 若处于 waiting_keyframe_ 状态，直接丢弃非关键帧；
     *   - 若 packet_queue_ 满载导致 push 失败，置位 waiting_keyframe_ 并清空积压队列。
     *
     * @param packet 数据包视图借用指针
     * @return true 成功送入队列；false 被拒绝、丢弃或队列已取消
     */
    bool push_packet(const Zhulong_packet_view &packet);

    /**
     * @brief 从输出队列拉取已解码的硬件帧 (供 AI 消费者只读借用)
     * @param[out] out_frame 输出硬件帧只读智能指针 (HardwareFrame::ConstPtr)
     * @param timeout 最大等待超时
     * @return true 成功获取一帧；false 超时或队列已取消
     */
    bool pop_frame(HardwareFrame::ConstPtr &out_frame,
                   std::chrono::milliseconds timeout = std::chrono::milliseconds(50));

    /** @brief 动态更新流元数据 (如连接就绪后补充的 SPS/PPS extradata) */
    void update_metadata(const Video &video_meta);

    /** @brief 获取当前帧队列中的元素个数 */
    size_t frame_queue_size() const;

    /** @brief 获取当前包队列中的元素个数 */
    size_t packet_queue_size() const;

    /** @brief 获取累计丢包与重同步计数快照 (无锁，relaxed 读取) */
    DecodeCounters counters() const;

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

    // 累计观测计数 (仅统计用途，relaxed 原子，无同步依赖)
    std::atomic<uint64_t> decoder_unavailable_drops_{0};
    std::atomic<uint64_t> keyframe_gate_drops_{0};
    std::atomic<uint64_t> congestion_resyncs_{0};

    Zhulong_decode_policy_t policy_{Zhulong_DECODE_BACKEND_AUTO, 1, 0};
    std::function<void(Zhulong_status_t)> on_fatal_error_{nullptr};

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
    Audio audio_meta_{};                        /**< 缓存的 RTSP 音频流元数据 (v5) */
    std::shared_ptr<DecodeSession> decode_session_{nullptr}; /**< 动态解码会话 (按需激活) */

    /**
     * @brief 预览契约整形器（单调微秒时间戳 / 参数集注入 / 帧分类）
     *
     * ⚠️ 仅由拉流 Worker 线程访问（on_ready 与 dispatch 均在该线程），内部无锁。
     * 本层平台无关：RK3568 与 RK3588 共用同一实现，平台差异仅体现在 Go 侧的资源配额。
     */
    PreviewNormalizer preview_normalizer_{};
    AudioNormalizer audio_normalizer_{};        /**< 音频时间戳归一化器 (v5) */
    int64_t video_origin_arrival_us_{0};        /**< 视频首帧到达时的单调微秒时间戳 */
    bool audio_anchor_logged_{false};           /**< 音频锚点一次性日志门闸（只由拉流线程置位） */

    // 被逐订阅者隔离的回调异常累计次数 (仅统计用途，relaxed 原子)
    std::atomic<uint64_t> callback_exceptions_{0};

    /**
     * @brief 构造函数
     * @param stream_id 物理流唯一 ID
     * @param normalized_url 规范化后的 RTSP URL
     * @param stream_options 流传输与超时配置
     */
    Stream(Zhulong_stream_id stream_id, std::string normalized_url, Options stream_options);

    /** @brief 析构函数，停止并汇合所有工作线程 */
    ~Stream();

    /** @brief 启动流拉取后台 Worker 线程 */
    void start();

    /** @brief 请求停止流拉取与解码 Worker */
    void request_stop();

    /** @brief 等待所有 Worker 线程完全退出 (必须在锁外调用) */
    void join();

    /** @brief Worker 线程入口函数，负责调用底层 FFmpeg 循环拉流 */
    void capture() noexcept;

    /** @brief 流就绪时回调，接收复合媒体元数据 */
    void on_ready(const Media &media);

    /** @brief 将 subscriptions 重新发布为不可变快照 (调用方必须持有 mutex) */
    void rebuild_subscribers();

    /**
     * @brief 将解析出的数据包整形后派发至该流的所有活跃订阅者及解码队列
     *
     * 派发前统一经过 PreviewNormalizer：补齐单调微秒时间戳、帧分类，
     * 并在关键帧自缺参数集时注入 VPS/SPS/PPS。
     * 订阅回调与解码队列拿到的都是整形后的视图（pts/dts/time_base 仍为源流原值）。
     */
    void dispatch(const Zhulong_packet_view &packet);

    /**
     * @brief 将音频数据包整形后派发至订阅了音频轨的活跃订阅者 (v5)
     *
     * 结构上杜绝音频进入 PreviewNormalizer 与 DecodeSession。
     */
    void dispatch_audio(const Zhulong_packet_view &packet);

    /** @brief 检查当前是否有 AI 消费者 */
    bool has_ai_consumer() const;

    /**
     * @brief 根据当前消费者列表动态调整解码会话启停
     * @param policy 解码策略
     * @return 若需要休眠，返回需要停止的旧会话对象 (调用方须在锁外调用 stop_decode_session)
     */
    std::shared_ptr<DecodeSession> sync_decode_pipeline(
        const Zhulong_decode_policy_t &policy = {Zhulong_DECODE_BACKEND_AUTO, 1, 0});

    /** @brief 致命不可逆解码错误回调 (Fail-Closed 熔断当前物理流) */
    void on_decode_fatal_error(Zhulong_status_t fatal_err);

    /** @brief 在锁外安全停止并汇合指定的解码会话 */
    static void stop_decode_session(std::shared_ptr<DecodeSession> session);

    /**
     * @brief 原生 C++ 消费/拉取解码帧 (只读借用)
     * @param[out] out_frame 输出硬件帧只读共享指针
     * @param timeout 最大等待超时时长
     * @return true 成功拉取；false 超时或无活跃解码会话
     */
    bool acquire_frame(HardwareFrame::ConstPtr &out_frame,
                       std::chrono::milliseconds timeout = std::chrono::milliseconds(50));

    /** @brief 查询当前解码帧队列积压数 */
    size_t frame_queue_size() const;

    /** @brief 检查当前是否存在活跃的解码会话 */
    bool has_active_decoder() const;

    /** @brief 获取被隔离的订阅者回调异常累计次数 (无锁，relaxed 读取) */
    uint64_t callback_exception_count() const;

    // =========================================================================
    // AI 分析视图（任务级裁剪窗口）
    // =========================================================================

    /** @brief 一次发布用的视图快照：窗口 + 统一时间戳。 */
    struct ViewSnapshot {
        ViewWindow window{};        /**< active=false 表示本帧不裁剪 */
        int64_t capture_time_ms{0}; /**< 单调推进的宿主 UTC 毫秒 */
    };

    /** @brief set_view 的结果；`ready == false` 表示尚无解码帧尺寸（NOT_READY）。 */
    struct ViewApplyResult {
        bool ready{false};
        ViewRect rect{};
        int64_t effective_since_ms{0};
    };

    /**
     * @brief 为本次发布取视图快照。
     *
     * 几何在**本函数内**按 `frame_w/frame_h` 求解：与缓存尺寸不同即视为一次几何变更，
     * 推进 `effective_since_ms`。`capture_time_ms` 与窗口在同一临界区内生成，
     * 因此“像素用哪个窗口”与“结果用哪个 E”由同一个时间戳串起来。
     *
     * ⚠️ frame_w/frame_h 必须是**解码帧**尺寸，不是 RTSP 元数据尺寸。
     */
    ViewSnapshot view_snapshot(int frame_w, int frame_h);

    /**
     * @brief 设置或清除视图。`clear == true` 或整帧矩形等价于清除。
     *
     * 已有几何缓存时立即按缓存尺寸重算并推进 since；尚无解码帧尺寸时只登记配置并
     * 返回 `ready=false`，由首个 view_snapshot 落地。
     */
    ViewApplyResult set_view(bool clear, const ViewRect &rect);

    /** @brief 查询当前生效 E 与生效起点（未设置/未就绪时 rect=identity, since=0）。 */
    void get_view(ViewRect *out_rect, int64_t *out_since_ms) const;

private:
    /** @brief 取一次单调递增的发布时钟（只增不减）。 */
    int64_t advance_capture_locked();
    /** @brief 推进视图生效起点；同时推进 last_capture，保证改窗后的第一帧落在新时间戳。 */
    int64_t advance_since_locked();

    bool view_configured_{false};
    ViewRect view_rect_{};
    ViewWindow view_window_{};
    int view_frame_w_{0};
    int view_frame_h_{0};
    bool view_geometry_ready_{false};
    int64_t view_since_ms_{0};
    int64_t last_capture_ms_{0};
};

} // namespace zhulong
