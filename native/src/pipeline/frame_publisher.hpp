/**
 * @file frame_publisher.hpp
 * @brief AI 消费流帧发布线程 (从 DecodeSession 帧队列拉取并经 FrameBroker 发布)
 *
 * 核心设计与职责：
 * 1. 物理发布侧节流：根据当前所有连接的算法 Runner 的 max(sample_fps) 在 Native 侧直接丢弃未到期帧，
 *    避免无谓的共享内存写入与 IPC 广播，零像素数据进入 Go GC 堆；
 * 2. 帧生命周期闭环：从 Stream 拉取的只读硬件帧在写入共享内存并压入抓拍历史环（上限 4 帧）后，
 *    发布工作线程立即 drop 本地借用引用，严防将底层硬件帧持有至推理阶段，防止解码器缓冲区耗尽。
 */

#pragma once

#include "pipeline/stream.hpp"
#include "pipeline/motion_detector.hpp"
#include "algorithm/ipc/frame_broker.hpp"

#include <atomic>
#include <chrono>
#include <deque>
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
     * @brief 按 PTS 查询历史帧；若未匹配或 pts<=0 则退回最新帧
     *
     * @param pts 目标帧时间戳 (pts <= 0 时等价于 latest_frame())
     * @return 匹配的帧；若历史缓存中已滑出则回退到 latest_frame()
     */
    HardwareFrame::ConstPtr find_frame(int64_t pts) const;

    /**
     * @brief 压入一帧到历史缓存（测试及内部调用）
     *
     * 淘汰旧帧时确保在互斥锁外执行析构，遵守 pipeline-guidelines 规则。
     */
    void push_frame(HardwareFrame::ConstPtr frame);

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

    // 抓拍用的历史帧环形缓存：容量固定为 4 帧（在 720p/1080p 解码池 10/16 帧限制下，
    // 兼顾实测 1~4 帧回传延迟与解码器缓冲安全冗余）。
    // ⚠️ mutable + mutex：查询接口是 const，但需要加锁读取。
    static constexpr size_t history_capacity = 4;
    mutable std::mutex latest_mutex_;
    std::deque<HardwareFrame::ConstPtr> history_frames_;

    MotionDetector motion_detector_;
};

} // namespace zhulong
