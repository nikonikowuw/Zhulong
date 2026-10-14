/**
 * @file frame_publisher.hpp
 * @brief AI 消费流帧发布线程 (从 DecodeSession 帧队列拉取并经 FrameBroker 发布)
 */

#pragma once

#include "pipeline/stream.hpp"
#include "algorithm/ipc/frame_broker.hpp"

#include <atomic>
#include <chrono>
#include <memory>
#include <thread>

namespace zhulong {

/**
 * @brief 专属流帧发布组件
 *
 * 生命周期与流上的 AI 消费通道绑定：
 * 1. 独立工作线程从 stream->acquire_frame 拉取已解码物理帧；
 * 2. 根据关联任务的 max(sample_fps) 在发布侧严格节流；
 * 3. 及时将帧推入 FrameBroker 共享内存槽位；
 * 4. 严守防慢算法拖垮解码器准则：发布后立即 drop 掉 HardwareFrame 引用。
 */
class FramePublisher {
public:
    FramePublisher(std::shared_ptr<Stream> stream, ipc::FrameBroker* broker);
    ~FramePublisher();

    FramePublisher(const FramePublisher&) = delete;
    FramePublisher& operator=(const FramePublisher&) = delete;

    /** @brief 启动帧发布后台线程 */
    void start();

    /** @brief 停止发布线程并等待工作线程 join 退出 */
    void stop();

    /** @brief 动态设置最大采样率 (0 表示不节流，按解码原帧率发布) */
    void set_max_sample_fps(uint32_t fps) noexcept;

    /** @brief 获取当前最大采样率 */
    [[nodiscard]] uint32_t max_sample_fps() const noexcept;

private:
    void publish_loop() noexcept;

    std::shared_ptr<Stream> stream_;
    ipc::FrameBroker* broker_{nullptr};
    std::atomic<bool> stop_{false};
    std::atomic<uint32_t> max_sample_fps_{0};
    std::thread worker_;
};

} // namespace zhulong
