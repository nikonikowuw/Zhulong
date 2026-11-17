/**
 * @file clip_recorder.hpp
 * @brief 事件录像片段：GOP 环形缓冲 + 单活跃片段状态机 + 每路独立写线程
 *
 * 数据面（拉流线程，Stream::dispatch 内同步调用）：
 *   - 只录视频轨；整形后的包**深拷贝**进环形缓冲（shaped data 借用整形器缓冲，只在本次 dispatch 有效）；
 *   - 环形缓冲按 GOP 组织，首包永远是关键帧：保留覆盖 pre_ms 所需的最少 GOP，同时总字节 ≤ byte_cap，
 *     超限从最老 GOP 整组淘汰；
 *   - 有活跃片段时把实时包 **try_push** 进写队列：拉流线程绝不等待写线程（R4.1）；
 *     写队列满 ⇒ 片段立即以 truncated 收尾，后续包不再进入该片段；
 *   - 时间轴不连续（DISCONTINUITY / gop_id 在非关键帧上跳变 / codec 变化）⇒ 清空缓冲，
 *     活跃片段以 truncated 收尾，等待下一个关键帧重新建缓冲。
 *
 * 控制面（ABI 线程）：trigger() 新建或延长片段；单片段总长 ≤ max_ms，超长时**立即**结束旧片段
 *   再新建（新片段从缓冲中的关键帧起，覆盖旧片段尾部，不丢画面）。
 *
 * 写线程：顺序消费 begin / sample / end 三类条目，写 fMP4，结束后把完成事件推入
 *   engine 级有界完成队列（溢出丢最旧）。
 *
 * 锁顺序：mutex_ → queue_mutex_；写线程只拿 queue_mutex_ 与完成队列自己的锁。
 */

#pragma once

#include "Zhulong/engine.h"

#include <atomic>
#include <chrono>
#include <condition_variable>
#include <cstddef>
#include <cstdint>
#include <deque>
#include <functional>
#include <memory>
#include <mutex>
#include <optional>
#include <string>
#include <thread>
#include <vector>

namespace zhulong {

constexpr int64_t default_clip_buffer_byte_cap = 32ll * 1024 * 1024;
constexpr int64_t default_clip_queue_byte_cap = 48ll * 1024 * 1024;
constexpr size_t default_clip_queue_packet_cap = 4096;
constexpr int64_t default_clip_max_ms = 120000;
constexpr int64_t default_bframe_pts_reorder_offset_us = 500000;
/**
 * 缓冲在 pre_ms 之外额外保留的时长：事件 pts 来自推理结果，回到 trigger 时已落后最新包
 * 一个推理延迟（通常 < 1s）。只按 pre_ms 相对最新包保留，片段前段会被系统性地吃掉这段延迟。
 */
constexpr int64_t clip_buffer_event_lag_slack_ms = 2000;
constexpr size_t default_clip_event_capacity = 256;
constexpr std::chrono::milliseconds default_clip_shutdown_budget{3000};

/** @brief 片段完成事件（与 Zhulong_clip_event 一一对应，不含 reserved） */
struct ClipEvent {
    uint64_t seq{0};
    int32_t status{0};          /**< ZHULONG_CLIP_COMPLETED / TRUNCATED / FAILED */
    int32_t codec{0};
    int64_t duration_ms{0};
    int64_t size_bytes{0};
    int64_t first_pts_us{0};
    int64_t last_pts_us{0};
};

/**
 * @brief engine 级完成事件队列（有界，溢出丢最旧）兼片段序号分配器
 *
 * 丢最旧而不是拒最新：Go 侧轮询停摆时，最新的完成状态更有价值；被挤掉的旧片段
 * 由 Go 的启动修正兜底（行状态停在 recording → 下次启动按文件存在性修正）。
 */
class ClipEventQueue {
public:
    explicit ClipEventQueue(size_t capacity = default_clip_event_capacity);

    /** @brief engine 内单调递增的片段序号（从 1 开始） */
    uint64_t next_sequence() noexcept;

    /** @brief 推入完成事件；满载时丢弃最旧一条（不抛） */
    void push(const ClipEvent &event) noexcept;

    /** @brief 最多取出 capacity 条并出队，返回实际条数 */
    size_t poll(ClipEvent *out, size_t capacity) noexcept;

    /** @brief 因溢出被丢弃的事件累计数（仅观测） */
    uint64_t dropped() const noexcept { return dropped_.load(std::memory_order_relaxed); }

private:
    const size_t capacity_;
    std::mutex mutex_;
    std::deque<ClipEvent> events_;
    std::atomic<uint64_t> next_seq_{1};
    std::atomic<uint64_t> dropped_{0};
};

/** @brief recorder 配置（pre_ms > 0；上限 <= 0 时使用默认值） */
struct RecorderSettings {
    int64_t pre_ms{0};
    int64_t byte_cap{default_clip_buffer_byte_cap};
    int64_t queue_byte_cap{default_clip_queue_byte_cap};
};

/** @brief 一个已深拷贝的视频样本（环形缓冲与写队列共享同一份字节） */
struct ClipSample {
    int64_t pts_us{0};
    int64_t dts_us{0};
    bool key_frame{false};
    std::vector<uint8_t> bytes;
};
using ClipSamplePtr = std::shared_ptr<const ClipSample>;

/** @brief 新建片段时交给写出器的参数 */
struct ClipWriteRequest {
    std::string path;
    int32_t codec{0};
    int32_t width_hint{0};
    int32_t height_hint{0};
    std::vector<uint8_t> parameter_sets_hint;  /**< Annex B，首个关键帧缺参数集时兜底 */
    int64_t pts_reorder_offset_us{0};          /**< B 帧重排正偏移 */
};

/** @brief 写出器抽象（生产实现为 fMP4；单测注入可阻塞的桩以模拟慢盘） */
class ClipFileWriter {
public:
    virtual ~ClipFileWriter() = default;
    /** @brief 写入一个样本；false 表示写出器已失败 */
    virtual bool write(const ClipSample &sample) noexcept = 0;
    /** @brief 收尾并落成最终文件；false 表示失败（半成品已清理） */
    virtual bool finish(int64_t *out_size_bytes) noexcept = 0;
    /** @brief 放弃并清理半成品 */
    virtual void abort() noexcept = 0;
};
using ClipWriterFactory = std::function<std::unique_ptr<ClipFileWriter>(const ClipWriteRequest &)>;

/** @brief 生产用 fMP4 写出器工厂 */
ClipWriterFactory make_fmp4_writer_factory();

/** @brief trigger 参数（path 已由 ABI 层深拷贝） */
struct ClipTriggerParams {
    int64_t event_pts_us{0};    /**< <= 0：取缓冲中最新包的 pts */
    int64_t post_ms{0};
    int64_t max_ms{0};          /**< <= 0：默认 120000 */
    std::string path;
    int32_t width_hint{0};
    int32_t height_hint{0};
    std::vector<uint8_t> parameter_sets_hint;
};

/** @brief trigger 结果 */
struct ClipTriggerOutcome {
    uint64_t seq{0};
    bool created{false};
    int32_t codec{0};
    int64_t offset_ms{0};
    int64_t start_pts_us{0};
};

class ClipRecorder {
public:
    ClipRecorder(Zhulong_stream_id stream_id, const RecorderSettings &settings,
                 std::shared_ptr<ClipEventQueue> events,
                 ClipWriterFactory writer_factory = make_fmp4_writer_factory());
    /** @brief 兜底 shutdown()；正常路径应由所有者在锁外显式调用 shutdown() */
    ~ClipRecorder();

    ClipRecorder(const ClipRecorder &) = delete;
    ClipRecorder &operator=(const ClipRecorder &) = delete;
    ClipRecorder(ClipRecorder &&) = delete;
    ClipRecorder &operator=(ClipRecorder &&) = delete;

    /** @brief 更新 pre_ms 与上限（不影响已入队的数据） */
    void configure(const RecorderSettings &settings);

    /**
     * @brief 拉流线程送入一个整形后的视频包（深拷贝；绝不阻塞等待写线程）
     *
     * noexcept：分配失败只截断活跃片段并清空缓冲，绝不让异常冲进 read_rtsp 把流打成 FAILED。
     */
    void on_packet(const Zhulong_packet_view &packet) noexcept;

    /**
     * @brief 新建或延长片段
     * @throws Failure Zhulong_ERR_NOT_READY（缓冲为空 / 已关闭）
     */
    ClipTriggerOutcome trigger(const ClipTriggerParams &params);

    /**
     * @brief 停止接收新包，活跃片段以 completed 收尾，有上限地等待写线程排空后汇合（幂等）
     *
     * 超过 budget 仍未排空时置 abort：写线程跳过剩余样本、把当前片段以 truncated 收尾后退出。
     * ⚠️ 必须在 Stream::mutex / Engine::control 之外调用（内部 join）。
     */
    void shutdown(std::chrono::milliseconds budget = default_clip_shutdown_budget) noexcept;

    /** @brief 观测快照（仅测试与诊断） */
    struct Snapshot {
        size_t gop_count{0};
        size_t sample_count{0};
        int64_t buffered_bytes{0};
        int64_t oldest_pts_us{0};
        int64_t newest_pts_us{0};
        bool active{false};
        uint64_t truncations{0};
    };
    Snapshot snapshot() const;

private:
    struct Gop {
        int64_t start_pts_us{0};
        int64_t bytes{0};
        std::vector<ClipSamplePtr> samples;
    };

    struct ActiveClip {
        uint64_t seq{0};
        int32_t codec{0};
        int64_t start_pts_us{0};
        int64_t end_pts_us{0};
    };

    enum class WriterItemKind { begin, sample, end };
    struct WriterItem {
        WriterItemKind kind{WriterItemKind::sample};
        uint64_t seq{0};
        int32_t status{0};
        std::shared_ptr<const ClipWriteRequest> request;
        ClipSamplePtr sample;
    };

    struct WritingClip {
        uint64_t seq{0};
        int32_t codec{0};
        std::unique_ptr<ClipFileWriter> writer;
        bool failed{false};
        bool aborted{false};
        int64_t samples{0};
        int64_t first_pts_us{0};
        int64_t last_pts_us{0};
        int64_t max_pts_us{0};
        int64_t first_dts_us{0};
        int64_t last_dts_us{0};
    };

    void on_packet_body(const Zhulong_packet_view &packet);
    void evict_locked();
    void clear_buffer_locked() noexcept;
    void feed_active_locked(const ClipSamplePtr &sample);
    void end_active_locked(int32_t status);

    /** @brief 控制条目与新片段回填：无视容量上限（不可丢） */
    void enqueue_forced(WriterItem &&item);
    /** @brief 实时样本：超过字节/包数上限即拒绝 */
    bool try_enqueue_sample(const ClipSamplePtr &sample);

    void writer_loop() noexcept;
    void process_item(WriterItem &item) noexcept;
    void finalize_writing(int32_t status) noexcept;

    const Zhulong_stream_id stream_id_;
    const std::shared_ptr<ClipEventQueue> events_;
    const ClipWriterFactory writer_factory_;

    // ---- 受 mutex_ 保护（拉流线程 / ABI 线程） ----
    mutable std::mutex mutex_;
    RecorderSettings settings_{};
    std::deque<Gop> gops_;
    int64_t buffered_bytes_{0};
    int64_t newest_pts_us_{0};
    int32_t codec_{0};
    bool waiting_keyframe_{true};
    bool has_last_gop_{false};
    uint32_t last_gop_id_{0};
    bool has_bframes_{false};
    std::vector<uint8_t> last_parameter_sets_;
    bool shut_{false};
    std::optional<ActiveClip> active_;
    std::optional<ActiveClip> recent_backfill_;
    uint64_t truncations_{0};

    // ---- 写队列（queue_mutex_） ----
    std::mutex queue_mutex_;
    std::condition_variable queue_ready_;
    std::condition_variable writer_exited_cv_;
    std::deque<WriterItem> queue_;
    int64_t queued_bytes_{0};
    size_t queued_samples_{0};
    int64_t queue_byte_cap_{default_clip_queue_byte_cap};
    bool stop_requested_{false};
    bool writer_exited_{false};
    std::atomic<bool> abort_{false};

    // ---- 只由写线程访问 ----
    std::optional<WritingClip> writing_;

    std::mutex shutdown_mutex_;
    bool shutdown_done_{false};
    std::thread writer_;
};

} // namespace zhulong
