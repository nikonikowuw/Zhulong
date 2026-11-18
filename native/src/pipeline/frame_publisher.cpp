/**
 * @file frame_publisher.cpp
 * @brief AI 消费流帧发布线程实现
 *
 * 核心机制：
 * 1. 50ms 轮询响应：拉取硬件帧时采用 50ms 超时，确保在物理流停机时 stop 信号能在 50ms 内响应并优雅汇合；
 * 2. 发布侧基准对齐节流：若当前时间早于 next_due，直接 drop 丢弃未到期帧；
 *    若发生解码严重停顿或长间隔，重置基准点（next_due = now + interval），严防恢复后连发补帧冲击下游；
 * 3. 及时释放：帧写入共享内存槽位后立即 reset() 释放只读借用引用，使解码器底层缓冲区得以迅速回收。
 */

#include "pipeline/frame_publisher.hpp"
#include "common/log.hpp"

#include <algorithm>

namespace zhulong {

FramePublisher::FramePublisher(std::shared_ptr<Stream> stream, ipc::FrameBroker* broker,
                               CropCopyFn crop)
    : stream_(std::move(stream)), broker_(broker), crop_(std::move(crop)) {}

FramePublisher::~FramePublisher() {
    // 析构函数隐式 noexcept：thread::join 理论上可抛 system_error，在此收口
    try {
        stop();
    } catch (...) {
        ZL_LOGE(stream_ ? stream_->id : 0, "pipeline.frame_publisher", "frame_publisher_stop_exception");
    }
}

void FramePublisher::start() {
    if (!worker_.joinable()) {
        stop_.store(false, std::memory_order_relaxed);
        worker_ = std::thread(&FramePublisher::publish_loop, this);
        if (stream_) {
            ZL_LOGI(stream_->id, "pipeline.frame_publisher", "frame_publisher_started");
        }
    }
}

void FramePublisher::stop() {
    stop_.store(true, std::memory_order_relaxed);
    if (worker_.joinable()) {
        worker_.join();
        if (stream_) {
            ZL_LOGI(stream_->id, "pipeline.frame_publisher", "frame_publisher_stopped");
        }
    }
    // ⚠️ 必须释放快照帧引用：即使 worker 已汇合，若这里不清空，
    // HardwareFrame 引用仍持有解码器 buffer，停流时会拖到宽限期超时。
    // ⚠️ 丢弃对象的析构必须在互斥锁外执行，防止 release_fn 触发外部闭包时形成潜在死锁（.trellis/spec/native/pipeline-guidelines.md §3）。
    // ⚠️ 本函数会被析构函数调用（隐式 noexcept），因此逐帧摘出、锁外释放，**不**构造临时
    //    std::deque —— libstdc++ 的 deque 默认构造即分配，OOM 时会直接 std::terminate。
    for (;;) {
        HardwareFrame::ConstPtr victim;
        {
            std::lock_guard<std::mutex> lock(latest_mutex_);
            if (history_frames_.empty()) {
                // 副本环只持有堆上字节（不再牵制解码器缓冲），直接清空即可；
                // clear() 不分配，满足 noexcept 约束。
                snapshot_frames_.clear();
                snapshot_bytes_ = 0;
                published_pts_records_.clear();
                break;
            }
            victim = std::move(history_frames_.front());
            history_frames_.pop_front();
        }
        // victim 在此处（锁外）析构
    }
    motion_detector_.reset();
}

HardwareFrame::ConstPtr FramePublisher::latest_frame() const {
    return find_frame(0);
}

HardwareFrame::ConstPtr FramePublisher::find_frame(int64_t pts) const {
    return resolve_frame(pts, 0).frame;
}

int64_t FramePublisher::lookup_capture_time_ms_locked(int64_t pts) const {
    if (pts <= 0) {
        return 0;
    }
    // 借用环自身不携带 capture_time_ms（HardwareFrame 是解码器产出的，字段固定），
    // 但副本环与借用环是**同一次 push_frame 同步插入**的，副本自带采集时钟，
    // 因此同 pts 的副本是权威来源。
    for (auto it = snapshot_frames_.rbegin(); it != snapshot_frames_.rend(); ++it) {
        if (it->has_pts && it->pts == pts) {
            return it->capture_time_ms;
        }
    }
    // 兜底：发布时登记的轻量记录（仅在帧带 pts_us 时登记，可能查不到 → 0，按身份未知处理）。
    for (auto it = published_pts_records_.rbegin(); it != published_pts_records_.rend(); ++it) {
        if (it->has_pts && it->raw_pts == pts) {
            return it->capture_time_ms;
        }
    }
    return 0;
}

FramePublisher::SnapshotMatch FramePublisher::resolve_frame(int64_t pts,
                                                            int64_t capture_time_ms) const {
    SnapshotMatch match;
    std::lock_guard<std::mutex> lock(latest_mutex_);

    const bool want_time = capture_time_ms > 0;
    const bool want_pts = pts > 0;

    // 1) 身份锚点优先：capture_time_ms 由发布线程单调生成，不受插件不回传 pts 或
    //    RTSP 时基重置影响，是唯一能稳定跨进程标识「哪一帧」的键。
    if (want_time) {
        for (auto it = snapshot_frames_.rbegin(); it != snapshot_frames_.rend(); ++it) {
            if (it->capture_time_ms != capture_time_ms) {
                continue;
            }
            match.source = SnapshotSource{it->pts, it->has_pts, it->capture_time_ms, true};
            match.frame = frame_from_snapshot(*it);
            // 同一帧若还留在借用环里，改用硬件帧：保留零拷贝与硬件 JPEG 直通。
            if (it->has_pts) {
                for (auto hit = history_frames_.rbegin(); hit != history_frames_.rend(); ++hit) {
                    if ((*hit)->has_pts && (*hit)->pts == it->pts) {
                        match.frame = *hit;
                        break;
                    }
                }
            }
            return match;
        }
    }

    // 2) PTS 精确匹配：旧结果不带 capture_time_ms 时的回退键。调用方同时给了
    //    capture_time_ms 时，这一分支不算「精确」——我们没能证实是同一帧。
    const bool exact_by_pts = !want_time;
    if (want_pts) {
        for (auto it = history_frames_.rbegin(); it != history_frames_.rend(); ++it) {
            if ((*it)->has_pts && (*it)->pts == pts) {
                match.frame = *it;
                match.source = SnapshotSource{(*it)->pts, true,
                                              lookup_capture_time_ms_locked((*it)->pts),
                                              exact_by_pts};
                return match;
            }
        }
        for (auto it = snapshot_frames_.rbegin(); it != snapshot_frames_.rend(); ++it) {
            if (it->has_pts && it->pts == pts) {
                match.frame = frame_from_snapshot(*it);
                match.source = SnapshotSource{it->pts, true, it->capture_time_ms, exact_by_pts};
                return match;
            }
        }
    }

    // 3) 未命中：回退到按给定键**最近**的副本帧，并计数 + 限频告警。
    //    ⚠️ 绝不能回退最新帧——那是时间上最远的选择，会把旧帧的检测框画到更新的
    //    画面上，正是「抓拍帧与检测结果不一致、坐标偏移」的直接来源。
    if ((want_time || want_pts) && !snapshot_frames_.empty()) {
        const SnapshotFrame* nearest = nullptr;
        int64_t nearest_delta = 0;
        size_t considered = 0;
        const int64_t wanted = want_time ? capture_time_ms : pts;
        for (const auto& cached : snapshot_frames_) {
            const bool usable = want_time ? cached.capture_time_ms > 0 : cached.has_pts;
            if (!usable) {
                continue;
            }
            ++considered;
            const int64_t cached_key = want_time ? cached.capture_time_ms : cached.pts;
            const int64_t delta =
                cached_key > wanted ? cached_key - wanted : wanted - cached_key;
            if (nearest == nullptr || delta < nearest_delta) {
                nearest = &cached;
                nearest_delta = delta;
            }
        }
        if (nearest != nullptr) {
            snapshot_miss_count_.fetch_add(1, std::memory_order_relaxed);
            const auto now = std::chrono::steady_clock::now();
            if (now - last_snapshot_miss_warn_ > std::chrono::seconds(1)) {
                last_snapshot_miss_warn_ = now;
                ZL_LOGW(stream_ ? stream_->id : 0, "pipeline.frame_publisher",
                        "snapshot_pts_evicted requested_pts=%lld requested_capture_time_ms=%lld "
                        "nearest_pts=%lld nearest_capture_time_ms=%lld delta=%lld "
                        "held=%zu considered=%zu range_pts=[%lld,%lld]",
                        static_cast<long long>(pts), static_cast<long long>(capture_time_ms),
                        static_cast<long long>(nearest->pts),
                        static_cast<long long>(nearest->capture_time_ms),
                        static_cast<long long>(nearest_delta), snapshot_frames_.size(), considered,
                        static_cast<long long>(snapshot_frames_.front().pts),
                        static_cast<long long>(snapshot_frames_.back().pts));
            }
            match.frame = frame_from_snapshot(*nearest);
            match.source =
                SnapshotSource{nearest->pts, nearest->has_pts, nearest->capture_time_ms, false};
            return match;
        }
    }

    // 4) 无键查询（抓最新）或副本不可用时的兜底：保持旧语义，优先借用环尾部。
    //    此时调用方没有指定帧，取到的就是它所要求的那一帧，故 exact 为真。
    const bool exact_latest = !want_time && !want_pts;
    if (!history_frames_.empty()) {
        const HardwareFrame& latest = *history_frames_.back();
        match.source = SnapshotSource{latest.pts, latest.has_pts,
                                      lookup_capture_time_ms_locked(latest.pts), exact_latest};
        match.frame = history_frames_.back();
        return match;
    }
    if (!snapshot_frames_.empty()) {
        const SnapshotFrame& cached = snapshot_frames_.back();
        match.frame = frame_from_snapshot(cached);
        match.source = SnapshotSource{cached.pts, cached.has_pts, cached.capture_time_ms,
                                      exact_latest};
        return match;
    }
    return match;
}

size_t FramePublisher::snapshot_cache_frames() const {
    std::lock_guard<std::mutex> lock(latest_mutex_);
    return snapshot_frames_.size();
}

size_t FramePublisher::snapshot_cache_bytes() const {
    std::lock_guard<std::mutex> lock(latest_mutex_);
    return snapshot_bytes_;
}

bool FramePublisher::cache_snapshot_frame(const HardwareFrame& frame,
                                           int64_t capture_time_ms) {
    if (!crop_) {
        return false;
    }
    if (frame.format != FRAME_FORMAT_NV12 && frame.format != FRAME_FORMAT_YUV420P) {
        return false;
    }
    // 与 encode_snapshot 的取整规则一致：全幅按 2 像素向内取整（色度下采样要求）。
    const uint32_t width = frame.width & ~static_cast<uint32_t>(1);
    const uint32_t height = frame.height & ~static_cast<uint32_t>(1);
    if (width < 2 || height < 2) {
        return false;
    }
    ViewWindow window{};
    window.active = true;
    window.x = 0;
    window.y = 0;
    window.w = static_cast<int>(width);
    window.h = static_cast<int>(height);
    const PackedLayout layout = packed_layout_for(window, frame.format);
    if (!layout.valid || layout.bytes == 0 || layout.bytes > snapshot_bytes_capacity) {
        return false;
    }

    // ⚠️ 拷贝在锁外执行：3 MB/帧的 memcpy 不得挡住 find_frame 的查询路径。
    auto bytes = std::make_shared<std::vector<uint8_t>>(layout.bytes);
    if (!crop_(frame, window, bytes->data(), bytes->size(), static_cast<int>(layout.stride), -1)) {
        return false;
    }

    SnapshotFrame cached{};
    cached.bytes = std::move(bytes);
    cached.layout = layout;
    cached.width = width;
    cached.height = height;
    cached.format = frame.format;
    cached.pts = frame.pts;
    cached.has_pts = frame.has_pts;
    cached.capture_time_ms = capture_time_ms;
    cached.pts_us = frame.pts_us;
    cached.has_pts_us = frame.has_pts_us;
    cached.time_base_num = frame.time_base_num;
    cached.time_base_den = frame.time_base_den;
    cached.stream_id = frame.stream_id;
    cached.cached_at = std::chrono::steady_clock::now();

    std::lock_guard<std::mutex> lock(latest_mutex_);
    snapshot_bytes_ += cached.bytes->size();
    snapshot_frames_.push_back(std::move(cached));
    evict_snapshot_frames_locked(std::chrono::steady_clock::now());
    return true;
}

void FramePublisher::evict_snapshot_frames_locked(std::chrono::steady_clock::time_point now) {
    while (!snapshot_frames_.empty()) {
        const bool too_many = snapshot_frames_.size() > snapshot_frames_capacity;
        const bool too_big = snapshot_bytes_ > snapshot_bytes_capacity;
        const bool too_old = now - snapshot_frames_.front().cached_at > snapshot_window &&
                             snapshot_frames_.size() > 1;
        if (!too_many && !too_big && !too_old) {
            break;
        }
        snapshot_bytes_ -= snapshot_frames_.front().bytes->size();
        snapshot_frames_.pop_front();
    }
}

HardwareFrame::ConstPtr FramePublisher::frame_from_snapshot(const SnapshotFrame& cached) const {
    if (!cached.bytes || cached.bytes->empty()) {
        return nullptr;
    }
    // `shared_ptr` 不可拷贝进 ConstPtr 的 deleter，故用 release_fn 延长字节生命周期：
    // 临时帧析构时 release_fn 释放对 vector 的最后一个引用。
    auto holder = cached.bytes;
    auto frame = std::make_shared<HardwareFrame>();
    uint8_t* base = holder->data();
    frame->width = cached.width;
    frame->height = cached.height;
    frame->stride = cached.layout.stride;
    frame->vstride = cached.height;
    frame->format = cached.format;
    frame->dma_fd = -1;
    frame->dev_ptr = nullptr;
    frame->host_ptr = base + cached.layout.offsets[0];
    for (int plane = 0; plane < frame_plane_count; ++plane) {
        if (cached.layout.strides[plane] == 0) {
            continue;
        }
        frame->host_planes[plane] = base + cached.layout.offsets[plane];
        frame->plane_strides[plane] = static_cast<uint32_t>(cached.layout.strides[plane]);
    }
    frame->pts = cached.pts;
    frame->has_pts = cached.has_pts;
    frame->pts_us = cached.pts_us;
    frame->has_pts_us = cached.has_pts_us;
    frame->time_base_num = cached.time_base_num;
    frame->time_base_den = cached.time_base_den;
    frame->stream_id = cached.stream_id;
    frame->release_fn = [holder](HardwareFrame* /*self*/) mutable { holder.reset(); };
    return frame;
}

void FramePublisher::push_frame(HardwareFrame::ConstPtr frame, int64_t capture_time_ms) {
    if (!frame) {
        return;
    }
    // 副本物化必须在锁外完成（见 cache_snapshot_frame 注释）。
    const bool cached = cache_snapshot_frame(*frame, capture_time_ms);
    HardwareFrame::ConstPtr evicted_frame;
    {
        std::lock_guard<std::mutex> lock(latest_mutex_);
        if (history_frames_.size() >= history_capacity) {
            evicted_frame = std::move(history_frames_.front());
            history_frames_.pop_front();
        }
        history_frames_.push_back(std::move(frame));
        if (cached) {
            evict_snapshot_frames_locked(std::chrono::steady_clock::now());
        }
    }
    evicted_frame.reset(); // 锁外析构底层 HardwareFrame
}

void FramePublisher::record_published_pts(int64_t capture_time_ms, int64_t raw_pts,
                                          bool has_pts, int64_t pts_us) {
    std::lock_guard<std::mutex> lock(latest_mutex_);
    if (published_pts_records_.size() >= published_pts_capacity) {
        published_pts_records_.pop_front();
    }
    published_pts_records_.push_back(PublishedPtsRecord{
        capture_time_ms,
        raw_pts,
        has_pts,
        pts_us,
    });
}

bool FramePublisher::resolve_pts_us(bool has_capture_time_ms, int64_t capture_time_ms,
                                    bool has_raw_pts, int64_t raw_pts,
                                    int64_t &out_pts_us) const noexcept {
    std::lock_guard<std::mutex> lock(latest_mutex_);
    if (published_pts_records_.empty()) {
        return false;
    }

    if (has_capture_time_ms && capture_time_ms > 0) {
        for (auto it = published_pts_records_.rbegin(); it != published_pts_records_.rend(); ++it) {
            if (it->capture_time_ms == capture_time_ms) {
                out_pts_us = it->pts_us;
                return true;
            }
        }
        return false;
    }

    if (has_raw_pts) {
        for (auto it = published_pts_records_.rbegin(); it != published_pts_records_.rend(); ++it) {
            if (it->has_pts && it->raw_pts == raw_pts) {
                out_pts_us = it->pts_us;
                return true;
            }
        }
    }
    return false;
}

void FramePublisher::set_max_sample_fps(uint32_t fps) noexcept {
    const uint32_t previous = max_sample_fps_.exchange(fps, std::memory_order_relaxed);
    if (previous != fps && stream_) {
        ZL_LOGI(stream_->id, "pipeline.frame_publisher", "publish_rate_changed fps=%u", fps);
    }
}

uint32_t FramePublisher::max_sample_fps() const noexcept {
    return max_sample_fps_.load(std::memory_order_relaxed);
}

void FramePublisher::publish_loop() noexcept {
    if (!stream_) return;
    auto next_due = std::chrono::steady_clock::now();
    auto last_drop_warn = std::chrono::steady_clock::time_point::min();
    // 异常日志限频：持续 OOM 时不能每帧刷屏
    auto last_exception_warn = std::chrono::steady_clock::time_point::min();

    while (!stop_.load(std::memory_order_relaxed)) {
        // ⚠️ 线程入口必须自行收口异常：本函数是 noexcept，而 publish_frame 会为
        //    客户端快照/发送缓冲分配内存，任何逃出的异常都会直接 std::terminate 掉
        //    整个宿主进程。单帧失败只丢这一帧，绝不停掉整路 AI 发布。
        try {
            publish_once(next_due, last_drop_warn);
        } catch (...) {
            const auto now = std::chrono::steady_clock::now();
            if (now - last_exception_warn > std::chrono::seconds(1)) {
                ZL_LOGE(stream_->id, "pipeline.frame_publisher", "publish_loop_exception");
                last_exception_warn = now;
            }
        }
    }
}

void FramePublisher::publish_once(std::chrono::steady_clock::time_point &next_due,
                                  std::chrono::steady_clock::time_point &last_drop_warn) {
    HardwareFrame::ConstPtr frame;
    // 等待最长 50ms 超时，确保 stop 信号能在 50ms 内被响应
    if (!stream_->acquire_frame(frame, std::chrono::milliseconds(50))) {
        return;
    }

    if (!frame) {
        return;
    }

    const uint32_t fps = max_sample_fps_.load(std::memory_order_relaxed);
    const auto now = std::chrono::steady_clock::now();

    if (fps > 0) {
        const auto interval = std::chrono::duration_cast<std::chrono::steady_clock::duration>(
            std::chrono::duration<double>(1.0 / static_cast<double>(fps))
        );

        if (now < next_due) {
            // 发布侧节流：未到期帧立即在发布侧丢弃，规避无谓的 shm 拷贝与 socket 广播通知
            frame.reset();
            return;
        }

        if (now > next_due + interval * 2) {
            // 解码停顿或长间隔后重置基准点，防止连发补帧
            next_due = now + interval;
        } else {
            next_due += interval;
        }
    }

    bool dispatched = false;
    int64_t dispatched_capture_time_ms = 0;
    if (broker_ && broker_->is_running()) {
        // 视图快照必须是本帧的第一件事：窗口几何、capture_time 与后续像素/结果
        // 由同一次加锁串起来（边界时钟的不变量）。
        const auto snapshot = stream_->view_snapshot(static_cast<int>(frame->width),
                                                    static_cast<int>(frame->height));
        if (snapshot.window.active != last_window_.active ||
            snapshot.window.x != last_window_.x || snapshot.window.y != last_window_.y ||
            snapshot.window.w != last_window_.w || snapshot.window.h != last_window_.h) {
            // 换窗后网格内容全变：重置底图，避免把窗口切换误判成一次 anti-flash。
            motion_detector_.reset();
            last_window_ = snapshot.window;
        }

        // 运动元数据只是「省算力」的提示，不是正确性前提：检测失败一律保持
        // FrameMotionInfo 的默认值（has_motion = 1）走全帧推理（fail-open），
        // 绝不让一次像素读取失败变成漏检。
        MotionResult motion_res{};
        ipc::FrameMotionInfo motion_info{};
        if (motion_detector_.detect(*frame, snapshot.window, motion_res)) {
            motion_info.has_motion = motion_res.has_motion ? 1U : 0U;
            motion_info.motion_box_count = motion_res.box_count;
            std::copy_n(motion_res.boxes.begin(), motion_res.box_count, motion_info.motion_boxes);
        }
        if (!broker_->publish_frame(*frame, snapshot.window, motion_info, crop_,
                                    snapshot.capture_time_ms)) {
            if (now - last_drop_warn > std::chrono::seconds(1)) {
                ZL_LOGW(stream_->id, "pipeline.frame_publisher", "frame_publish_failed");
                last_drop_warn = now;
            }
        } else {
            dispatched = true;
            dispatched_capture_time_ms = snapshot.capture_time_ms;
        }
    }

    // 抓拍快照历史**只收确实提交给 broker 的帧**，与 latest_frame()/find_frame() 的
    // 「最近一次发布过的帧」契约一致：未下发的帧留在旧记录上，不会被误当成在途结果。
    // ⚠️ 必须在 frame.reset() 之前压入；淘汰帧析构在互斥锁外执行。
    if (dispatched) {
        if (frame->has_pts_us) {
            record_published_pts(dispatched_capture_time_ms, frame->pts,
                                 frame->has_pts, frame->pts_us);
        }
        push_frame(frame, dispatched_capture_time_ms);
    }

    // ⚠️ 防慢算法拖垮解码器准则：
    // 帧复制进共享内存（或无客户端丢弃）后立即 reset 释放物理帧借用引用，绝不将 HardwareFrame 持有至推理阶段
    frame.reset();
}

} // namespace zhulong
