/**
 * @file frame_publisher.hpp
 * @brief AI 消费流帧发布线程 (从 DecodeSession 帧队列拉取并经 FrameBroker 发布)
 *
 * 核心设计与职责：
 * 1. 物理发布侧节流：根据当前所有连接的算法 Runner 的 max(sample_fps) 在 Native 侧直接丢弃未到期帧，
 *    避免无谓的共享内存写入与 IPC 广播，零像素数据进入 Go GC 堆；
 * 2. 帧生命周期闭环：从 Stream 拉取的只读硬件帧在写入共享内存后立即 drop 释放引用，
 *    严防将底层硬件帧持有至推理阶段，防止解码器缓冲区耗尽。
 */

#pragma once

#include "pipeline/stream.hpp"
#include "algorithm/ipc/frame_broker.hpp"

#include <atomic>
#include <chrono>
#include <memory>
#include <mutex>
#include <thread>

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
     */
    FramePublisher(std::shared_ptr<Stream> stream, ipc::FrameBroker* broker);

    /** @brief 析构函数，调用 stop() 汇合后台工作线程 */
    ~FramePublisher();

    FramePublisher(const FramePublisher&) = delete;
    FramePublisher& operator=(const FramePublisher&) = delete;

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
     * ⚠️ 该帧是「当前最新一帧」，不是「产生某个推理结果的那一帧」。抓拍在推理结果
     * 回传后才发起，因此与结果之间存在固有延迟（实测约 1~4 帧）。调用方必须把
     * 该偏差视为已知语义。
     *
     * ⚠️ 只保留 1 帧的理由：MPP 的 buffer group 在 1080p 下仅 16 个 buffer，
     * 多保留几帧就会把解码器自己的缓冲耗尽。因此这里采用「新帧覆盖旧帧」语义，
     * 绝不排队。
     *
     * @return 最新帧的只读智能指针；尚未发布过任何帧时返回空指针。
     *         返回值的生命周期由 shared_ptr 决定，调用方持有期间底层 buffer 不会被复用。
     */
    HardwareFrame::ConstPtr latest_frame() const;

private:
    void publish_loop() noexcept;

    std::shared_ptr<Stream> stream_;
    ipc::FrameBroker* broker_{nullptr};
    std::atomic<bool> stop_{false};
    std::atomic<uint32_t> max_sample_fps_{0};
    std::thread worker_;

    // 抓拍用的最新帧引用：新帧覆盖旧帧（不排队），因此最多只多占 1 个解码 buffer。
    // ⚠️ mutable + mutex：latest_frame() 是 const 查询接口，但需要加锁读取。
    mutable std::mutex latest_mutex_;
    HardwareFrame::ConstPtr latest_frame_;
};

} // namespace zhulong
