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
    }
}

void FramePublisher::stop() {
    stop_.store(true, std::memory_order_relaxed);
    if (worker_.joinable()) {
        worker_.join();
    }
}

void FramePublisher::set_max_sample_fps(uint32_t fps) noexcept {
    max_sample_fps_.store(fps, std::memory_order_relaxed);
}

uint32_t FramePublisher::max_sample_fps() const noexcept {
    return max_sample_fps_.load(std::memory_order_relaxed);
}

void FramePublisher::publish_loop() noexcept {
    auto next_due = std::chrono::steady_clock::now();

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
            broker_->publish_frame(*frame);
        }

        // ⚠️ 防慢算法拖垮解码器准则：
        // 帧复制进共享内存（或无客户端丢弃）后立即 reset 释放物理帧借用引用，绝不将 HardwareFrame 持有至推理阶段
        frame.reset();
    }
}

} // namespace zhulong
