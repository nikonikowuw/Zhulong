/**
 * @file window_crop.hpp
 * @brief 宿主侧 AI 分析视图物化（CPU 分平面行拷贝）
 *
 * 归属 `ZhulongEngine`（宿主），**绝不**进入 `ZhulongAlgoIpcObjects`：那批对象会归档进
 * 算法包 SDK，而窗口物化是宿主职责。FrameBroker 只接收 `CropCopyFn` 回调。
 *
 * 本期只实现 CPU 后端：1080p 半宽半高窗口约 0.78 MB/帧，在 5–20 fps 下远低于
 * RK3568 的 memcpy 带宽。RGA 后端是独立优化项，接入后也必须写同一个 ViewWindow
 * 与同一套紧打包布局（几何不得因后端而变）。
 */

#pragma once

#include "common/view_window.hpp"

#include <atomic>
#include <cstddef>
#include <cstdint>

namespace zhulong {

/**
 * @brief 窗口物化器。实例由 Engine 持有并转交给 FramePublisher，生命周期覆盖 publisher。
 *
 * 线程模型：`copy()` 可被多个 FramePublisher 并发调用；除计数外无可变状态，
 * 每次调用只读 `HardwareFrame` 并写目标缓冲区。
 */
class WindowCrop {
public:
    WindowCrop() = default;
    WindowCrop(const WindowCrop&) = delete;
    WindowCrop& operator=(const WindowCrop&) = delete;

    /**
     * @brief 把 `view` 指定的窗口以下列紧打包布局写入 `dst`。
     *
     * 支持 NV12 / YUV420P；未知格式返回 false（调用方必须 fail_publish，不得按 BGR24 猜）。
     * `dst_stride` 必须与 `packed_layout_for(view, frame.format).stride` 严格一致；
     * `dst_dma_fd >= 0` 表示目标为 DMA 槽位，写入期间做 `DMA_SYNC_WRITE`；`-1` 表示 shm。
     */
    bool copy(const HardwareFrame& frame, const ViewWindow& view, uint8_t* dst,
              size_t dst_size, int dst_stride, int dst_dma_fd) noexcept;

    /** @brief 走 CPU 行拷贝的成功帧数。 */
    [[nodiscard]] uint64_t cpu_count() const noexcept {
        return cpu_count_.load(std::memory_order_relaxed);
    }

    /** @brief 走 RGA 的成功帧数（本期恒为 0，保留给独立优化项）。 */
    [[nodiscard]] uint64_t rga_count() const noexcept {
        return rga_count_.load(std::memory_order_relaxed);
    }

private:
    std::atomic<uint64_t> cpu_count_{0};
    std::atomic<uint64_t> rga_count_{0};
};

} // namespace zhulong
