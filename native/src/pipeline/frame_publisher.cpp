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

FramePublisher::FramePublisher(std::shared_ptr<Stream> stream, ipc::FrameBroker* broker)
    : stream_(std::move(stream)), broker_(broker) {}

FramePublisher::~FramePublisher() {
    stop();
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
    std::deque<HardwareFrame::ConstPtr> frames_to_drop;
    {
        std::lock_guard<std::mutex> lock(latest_mutex_);
        frames_to_drop = std::move(history_frames_);
        history_frames_.clear();
    }
    frames_to_drop.clear();
    motion_detector_.reset();
}

HardwareFrame::ConstPtr FramePublisher::latest_frame() const {
    return find_frame(0);
}

HardwareFrame::ConstPtr FramePublisher::find_frame(int64_t pts) const {
    std::lock_guard<std::mutex> lock(latest_mutex_);
    if (history_frames_.empty()) {
        return nullptr;
    }
    if (pts > 0) {
        // 从最新的帧往旧查找，优先匹配目标 PTS
        for (auto it = history_frames_.rbegin(); it != history_frames_.rend(); ++it) {
            if ((*it)->has_pts && (*it)->pts == pts) {
                return *it;
            }
        }
    }
    // 未指定有效 PTS 或目标帧已被淘汰，回退为最新帧
    return history_frames_.back();
}

void FramePublisher::push_frame(HardwareFrame::ConstPtr frame) {
    if (!frame) {
        return;
    }
    HardwareFrame::ConstPtr evicted_frame;
    {
        std::lock_guard<std::mutex> lock(latest_mutex_);
        if (history_frames_.size() >= kHistoryCapacity) {
            evicted_frame = std::move(history_frames_.front());
            history_frames_.pop_front();
        }
        history_frames_.push_back(std::move(frame));
    }
    evicted_frame.reset(); // 锁外析构底层 HardwareFrame
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

    while (!stop_.load(std::memory_order_relaxed)) {
        HardwareFrame::ConstPtr frame;
        // 等待最长 50ms 超时，确保 stop 信号能在 50ms 内被响应
        if (!stream_->acquire_frame(frame, std::chrono::milliseconds(50))) {
            continue;
        }

        if (!frame) {
            continue;
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
                continue;
            }

            if (now > next_due + interval * 2) {
                // 解码停顿或长间隔后重置基准点，防止连发补帧
                next_due = now + interval;
            } else {
                next_due += interval;
            }
        }

        if (broker_ && broker_->is_running()) {
            // 运动元数据只是「省算力」的提示，不是正确性前提：检测失败一律保持
            // FrameMotionInfo 的默认值（has_motion = 1）走全帧推理（fail-open），
            // 绝不让一次像素读取失败变成漏检。
            MotionResult motion_res{};
            ipc::FrameMotionInfo motion_info{};
            if (motion_detector_.detect(*frame, motion_res)) {
                motion_info.has_motion = motion_res.has_motion ? 1U : 0U;
                motion_info.motion_box_count = motion_res.box_count;
                std::copy_n(motion_res.boxes, motion_res.box_count, motion_info.motion_boxes);
            }
            if (!broker_->publish_frame(*frame, motion_info)) {
                if (now - last_drop_warn > std::chrono::seconds(1)) {
                    ZL_LOGW(stream_->id, "pipeline.frame_publisher", "frame_publish_failed");
                    last_drop_warn = now;
                }
            }
        }

        // 抓拍快照历史引用：环形保留最近 kHistoryCapacity 帧，支持按 PTS 精确匹配。
        // ⚠️ 必须在 frame.reset() 之前压入；淘汰帧析构在互斥锁外执行。
        push_frame(frame);

        // ⚠️ 防慢算法拖垮解码器准则：
        // 帧复制进共享内存（或无客户端丢弃）后立即 reset 释放物理帧借用引用，绝不将 HardwareFrame 持有至推理阶段
        frame.reset();
    }
}

} // namespace zhulong
