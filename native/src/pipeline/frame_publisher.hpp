/**
 * @file frame_publisher.hpp
 * @brief AI 消费流帧发布线程 (从 DecodeSession 帧队列拉取并经 FrameBroker 发布)
 *
 * 核心设计与职责：
 * 1. 物理发布侧节流：根据当前所有连接的算法 Runner 的 max(sample_fps) 在 Native 侧直接丢弃未到期帧，
 *    避免无谓的共享内存写入与 IPC 广播，零像素数据进入 Go GC 堆；
 * 2. 帧生命周期闭环：从 Stream 拉取的只读硬件帧在写入共享内存并压入抓拍历史环（借用环 4 帧 +
 *    像素副本环，见 `SnapshotFrame`）后，发布工作线程立即 drop 本地借用引用，严防将底层硬件帧
 *    持有至推理阶段，防止解码器缓冲区耗尽。
 */

#pragma once

#include "common/view_window.hpp"
#include "pipeline/stream.hpp"
#include "pipeline/motion_detector.hpp"
#include "algorithm/ipc/frame_broker.hpp"

#include <array>
#include <atomic>
#include <chrono>
#include <deque>
#include <memory>
#include <mutex>
#include <thread>
#include <vector>

namespace zhulong {

/**
 * @brief 专属物理流帧发布组件
 *
 * 生命周期与物理流上的 AI 消费通道绑定：
 * 1. 独立工作线程从 stream->acquire_frame 拉取已解码硬件帧；
 * 2. 根据关联任务的 max(sample_fps) 在发布侧严格节流；
 * 3. 及时将帧推入 FrameBroker 共享内存槽位；
 * 4. 严守防慢算法拖垮解码器准则：发布后立即释放 HardwareFrame 引用。
 */
class FramePublisher {
public:
    /**
     * @brief 构造函数
     * @param stream 关联的物理流共享指针
     * @param broker 对应的 FrameBroker 实例指针
     * @param crop   窗口物化回调（由 Engine 提供，生命周期覆盖 publisher；可空 = 不支持裁剪）
     */
    FramePublisher(std::shared_ptr<Stream> stream, ipc::FrameBroker* broker,
                   CropCopyFn crop = {});

    /** @brief 析构函数，调用 stop() 汇合后台工作线程 */
    ~FramePublisher();

    FramePublisher(const FramePublisher&) = delete;
    FramePublisher& operator=(const FramePublisher&) = delete;
    FramePublisher(FramePublisher&&) = delete;
    FramePublisher& operator=(FramePublisher&&) = delete;

    /** @brief 启动帧发布后台线程 */
    void start();

    /** @brief 停止发布线程并等待工作线程汇合退出 */
    void stop();

    /** @brief 动态设置最大采样率 (0 表示不节流，按解码原帧率发布) */
    void set_max_sample_fps(uint32_t fps) noexcept;

    /** @brief 获取当前最大采样率 */
    [[nodiscard]] uint32_t max_sample_fps() const noexcept;

    /**
     * @brief 取出最近一次发布过的最新一帧（抓拍用），不阻塞
     *
     * @return 最新帧的只读智能指针；尚未发布过任何帧时返回空指针。
     *         返回值的生命周期由 shared_ptr 决定，调用方持有期间底层 buffer 不会被复用。
     */
    HardwareFrame::ConstPtr latest_frame() const;

    /**
     * @brief 一次抓拍实际解析到的帧身份
     *
     * `exact == false` 表示请求的那一帧已经滑出历史窗口，`pts` / `capture_time_ms`
     * 描述的是**实际取到的替代帧**。调用方（宿主）据此判断证据图与检测结果能否
     * 绑定——这是把「框画在哪一帧上」从猜测变成可断言事实的唯一途径。
     */
    struct SnapshotSource {
        int64_t pts{0};
        bool has_pts{false};
        int64_t capture_time_ms{0};
        bool exact{false};
    };

    /** @brief 抓拍帧解析结果：命中帧 + 它的身份（frame 为空表示一帧都没有） */
    struct SnapshotMatch {
        HardwareFrame::ConstPtr frame;
        SnapshotSource source{};
    };

    /**
     * @brief 按帧身份解析历史帧（抓拍专用）
     *
     * 由高到低：
     * 1. `capture_time_ms > 0` 时按它精确匹配副本环——这是发布侧生成的单调身份，
     *    不受 `pts` 缺失（插件不回传）或时基重置影响；
     * 2. `pts > 0` 时按它精确匹配（借用环优先，命中即保留硬件 JPEG 直通）；
     * 3. 都未命中时回退到**按给定键最近**的副本帧，而不是最新帧——最新帧是时间上
     *    最远的选择，正是「旧帧的检测框画到新画面上」的直接来源；同时计数 + 限频告警。
     *
     * @param pts 目标帧 PTS（<= 0 表示不按 PTS 匹配）
     * @param capture_time_ms 目标帧采集时钟（<= 0 表示不按它匹配；两者都 <= 0 等价于
     *        latest_frame()）
     * @return 命中帧与其身份；`source.exact` 只有在「确实取到了请求的那一帧」时为真
     */
    SnapshotMatch resolve_frame(int64_t pts, int64_t capture_time_ms) const;

    /**
     * @brief 按 PTS 查询历史帧（`resolve_frame(pts, 0).frame` 的简写）
     *
     * 两级历史，优先级从高到低：
     * 1. `history_frames_`：最近 4 个已发布帧的**借用引用**（零拷贝，解码池预算内）；
     * 2. `snapshot_frames_`：已发布帧的**紧打包像素副本**（解码池无关，由
     *    `snapshot_window` / `snapshot_bytes_capacity` 界定）。慢算法的回传延迟常超过
     *    4 帧，只有副本环能保证「结果对应的那一帧」还在。
     *
     * @param pts 目标帧时间戳 (pts <= 0 时等价于 latest_frame())
     * @return 匹配的帧；都未命中时回退到**PTS 最近**的副本帧（而不是最新帧——最新帧是
     *         时间上最远的选择，正是「框画在别的帧上」的直接来源），并计数 + 限频告警
     */
    HardwareFrame::ConstPtr find_frame(int64_t pts) const;

    /**
     * @brief 压入一帧到历史缓存（测试及内部投递路径共用）
     *
     * 进入 4 帧借用环；当借用环满载淘汰队头帧时，才按需在锁外物化像素副本进入副本环。
     * `broker_frame_id > 0` 且关联 broker 时，若该帧已由所有接收客户端干净 ACK 且未收到
     * `MSG_RESULT`，淘汰时直接零拷贝丢弃。
     * 淘汰旧帧时确保在互斥锁外析构，遵守 pipeline-guidelines 规则。
     *
     * @param frame 已发布帧
     * @param capture_time_ms 本帧发布时打下的单调采集毫秒时间戳（与发给算法的
     *        `capture_time_ms` 同源），作为抓拍的帧身份锚点
     * @param broker_frame_id broker 分配的帧序号（0 表示不走 broker 生命周期判定，保守物化）
     */
    void push_frame(HardwareFrame::ConstPtr frame, int64_t capture_time_ms = 0,
                    uint64_t broker_frame_id = 0);

    /**
     * @brief 标记某一已发布帧收到了算法回传的 `MSG_RESULT`
     *
     * 必须在同一 socket 的 `MSG_FRAME_ACK` 之前由结果派发路径调用（`Engine::dispatch_ai_result`）：
     * 借用环淘汰该帧时，只要 `result_seen` 为真，即便所有客户端已完成 ACK，也必须物化副本以服务
     * 异步排队的 Go `SnapshotStore`。
     */
    void mark_result_frame(bool has_capture_time_ms, int64_t capture_time_ms,
                           bool has_raw_pts, int64_t raw_pts) noexcept;

    /** @brief 抓拍历史未命中精确 PTS、只能回退最近帧的次数（可观测性） */
    [[nodiscard]] uint64_t snapshot_miss_count() const noexcept {
        return snapshot_miss_count_.load(std::memory_order_relaxed);
    }

    /** @brief 当前抓拍像素副本环持有的帧数与字节数（可观测性/测试） */
    [[nodiscard]] size_t snapshot_cache_frames() const;
    [[nodiscard]] size_t snapshot_cache_bytes() const;

    /** @brief 累计物化进抓拍像素副本环的帧数（可观测性/测试：验证静止/空帧零拷贝跳过） */
    [[nodiscard]] uint64_t snapshot_materialized_count() const noexcept {
        return snapshot_materialized_count_.load(std::memory_order_relaxed);
    }

    /**
     * @brief 记录已发布帧的采集时钟 / 原始 PTS 与预览归一化时间戳映射
     *
     * 仅保存轻量标量（零 HardwareFrame 引用），用于在宿主侧将算法回传的
     * MSG_RESULT 精确对齐回同源物理流的 WebCodecs 预览时间戳（pts_us）。
     */
    void record_published_pts(int64_t capture_time_ms, int64_t raw_pts,
                              bool has_pts, int64_t pts_us);

    /**
     * @brief 根据回传的 capture_time_ms（优先）或原始 PTS 反查对应的归一化 pts_us
     *
     * @param has_capture_time_ms 回传 JSON 是否携带 capture_time_ms
     * @param capture_time_ms     帧发布时打下的单调采集毫秒时间戳（> 0 时作为唯一帧标识匹配）
     * @param has_raw_pts         回传 JSON 是否携带原始 pts
     * @param raw_pts             解码帧原始 PTS（仅在 capture_time_ms <= 0 时作为回退匹配键）
     * @param[out] out_pts_us     命中时写入对应的微秒级归一化时间戳（首帧合法值为 0）
     * @return 命中返回 true，未命中返回 false
     */
    bool resolve_pts_us(bool has_capture_time_ms, int64_t capture_time_ms,
                        bool has_raw_pts, int64_t raw_pts,
                        int64_t &out_pts_us) const noexcept;

private:
    /** @brief 线程入口：逐帧收口异常，绝不让其穿出 noexcept 线程入口 */
    void publish_loop() noexcept;

    /**
     * @brief 单次发布迭代（允许抛出，由 publish_loop 统一收口）
     *
     * broker 发布路径会分配（客户端快照、发送缓冲），单帧失败只丢这一帧。
     *
     * @param[in,out] next_due 发布侧节流基准点
     * @param[in,out] last_drop_warn 发布失败日志限频时间点
     */
    void publish_once(std::chrono::steady_clock::time_point &next_due,
                     std::chrono::steady_clock::time_point &last_drop_warn);

    std::shared_ptr<Stream> stream_;
    ipc::FrameBroker* broker_{nullptr};
    CropCopyFn crop_;
    /** @brief 上一次发布使用的窗口；变化时重置运动检测底图（只由发布线程写）。 */
    ViewWindow last_window_{};
    std::atomic<bool> stop_{false};
    std::atomic<uint32_t> max_sample_fps_{0};
    std::thread worker_;

    // 抓拍用的借用环：容量固定为 4 帧（在 720p/1080p 解码池 10/16 帧限制下，
    // 兼顾实测 1~4 帧回传延迟与解码器缓冲安全冗余）。
    // ⚠️ mutable + mutex：查询接口是 const，但需要加锁读取。
    static constexpr size_t history_capacity = 4;
    static constexpr size_t published_pts_capacity = 64;
    static constexpr size_t pending_result_marks_capacity = 16;

    // 抓拍用的像素副本环：与解码池解耦，仅在借用环淘汰「仍在途 / 超时 / 已产出结果」的帧时按需物化，
    // 避免静止帧、无目标帧和未投递帧白白拷贝 3 MB 非缓存 DMA-BUF 并把真正需要抓拍的帧挤出窗口。
    // 1080p NV12 单帧 3.1 MB，硬上限 12 帧 / 24 MiB / 3000 ms（覆盖多人脸多帧串行推理峰值）。
    static constexpr size_t snapshot_frames_capacity = 12;
    static constexpr size_t snapshot_bytes_capacity = 24u * 1024u * 1024u;
    static constexpr std::chrono::milliseconds snapshot_window{3000};

    /** @brief 4 帧借用环条目：绑定硬件帧、采集身份、broker 帧序号与结果标记 */
    struct HistoryEntry {
        HardwareFrame::ConstPtr frame;
        int64_t capture_time_ms{0};
        uint64_t broker_frame_id{0};
        bool result_seen{false};
    };

    /**
     * @brief 兜底暂存「在 push_frame 落环前就先到达的 MSG_RESULT」标记
     *
     * 极速算法或发布线程在 publish_frame 之后、push_frame 之前被调度抢占时，
     * mark_result_frame 可能早于 push_frame 到达。固定数组零堆分配（保持 noexcept），
     * push_frame 入环时认领并转为 HistoryEntry::result_seen。
     */
    struct PendingResultMark {
        bool active{false};
        bool has_capture_time_ms{false};
        int64_t capture_time_ms{0};
        bool has_raw_pts{false};
        int64_t raw_pts{0};
    };

    /** @brief 抓拍副本像素缓冲复用池（避免每次物化重复分配与清零 3 MB vector） */
    struct SnapshotBufferPool {
        mutable std::mutex mutex;
        std::vector<std::unique_ptr<std::vector<uint8_t>>> buffers;
        size_t pooled_bytes{0};
    };

    /**
     * @brief 一帧已发布画面的紧打包像素副本（NV12 / YUV420P）
     *
     * 布局由 `packed_layout_for()` 给出，与 `encode_snapshot()` 的 CPU 视图完全一致，
     * 因此副本可以直接包成临时 `HardwareFrame` 供抓拍编码，无需再碰解码器缓冲。
     */
    struct SnapshotFrame {
        std::shared_ptr<std::vector<uint8_t>> bytes;
        PackedLayout layout{};
        uint32_t width{0};
        uint32_t height{0};
        uint32_t format{0};
        int64_t pts{0};
        bool has_pts{false};
        int64_t capture_time_ms{0};
        int64_t pts_us{0};
        bool has_pts_us{false};
        int32_t time_base_num{1};
        int32_t time_base_den{90000};
        uint64_t stream_id{0};
        std::chrono::steady_clock::time_point cached_at{};
    };

    /**
     * @brief 从复用池借出（或首次分配）指定字节数的像素缓冲，析构时自动归还池中
     */
    std::shared_ptr<std::vector<uint8_t>> acquire_snapshot_buffer(size_t bytes);

    /**
     * @brief 在锁外物化一帧像素副本；格式不支持或回调缺失时返回 false
     */
    bool materialize_snapshot_frame(const HardwareFrame& frame, int64_t capture_time_ms,
                                    SnapshotFrame& out);

    /**
     * @brief 按 pts 反查采集时钟（调用方持锁）
     *
     * 优先查借用环与副本环，最后回退到发布时登记的 PublishedPtsRecord；查不到返回 0。
     */
    int64_t lookup_capture_time_ms_locked(int64_t pts) const;

    /** @brief 把副本包装成只读 HardwareFrame（调用方持锁，副本字节随 release_fn 存活） */
    HardwareFrame::ConstPtr frame_from_snapshot(const SnapshotFrame& cached) const;

    /**
     * @brief 按时间窗/帧数/字节预算淘汰最旧副本（调用方持锁），将释放的像素缓冲移交调用方锁外析构
     */
    void evict_snapshot_frames_locked(
        std::chrono::steady_clock::time_point now,
        std::vector<std::shared_ptr<std::vector<uint8_t>>>& recycled_out);

    struct PublishedPtsRecord {
        int64_t capture_time_ms{0};
        int64_t raw_pts{0};
        bool has_pts{false};
        int64_t pts_us{0};
    };

    mutable std::mutex latest_mutex_;
    std::deque<HistoryEntry> history_frames_;
    std::deque<SnapshotFrame> snapshot_frames_;
    size_t snapshot_bytes_{0};
    std::shared_ptr<SnapshotBufferPool> snapshot_pool_{std::make_shared<SnapshotBufferPool>()};
    mutable std::atomic<uint64_t> snapshot_miss_count_{0};
    std::atomic<uint64_t> snapshot_materialized_count_{0};
    mutable std::chrono::steady_clock::time_point last_snapshot_miss_warn_{};
    std::deque<PublishedPtsRecord> published_pts_records_;
    std::array<PendingResultMark, pending_result_marks_capacity> pending_result_marks_{};
    size_t pending_result_cursor_{0};

    MotionDetector motion_detector_;
};

} // namespace zhulong
