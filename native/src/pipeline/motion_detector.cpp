/**
 * @file motion_detector.cpp
 * @brief 轻量级视频流前置运动检测器实现
 */

#include "pipeline/motion_detector.hpp"

#include "platform/dmabuf/dmabuf_sync.hpp"

#include <algorithm>
#include <cstring>
#include <optional>
#include <utility>

#if defined(__linux__)
#include <sys/mman.h>
#endif

namespace zhulong {

namespace {

/** @brief 两个灰度样本的绝对差（避免 <cstdlib> 的 abs 重载歧义） */
constexpr int abs_diff(uint8_t a, uint8_t b) noexcept {
    const int d = static_cast<int>(a) - static_cast<int>(b);
    return d < 0 ? -d : d;
}

/**
 * @brief 按裸 fd 只读映射 DMA-BUF 并完成一次 CPU 访问窗口的 RAII 看护
 *
 * 只借用 fd，绝不 close / 释放 —— fd 的所有权属于 HardwareFrame 的 release_fn。
 */
class ScopedDmaReader {
public:
    ScopedDmaReader() = default;
    ScopedDmaReader(const ScopedDmaReader&) = delete;
    ScopedDmaReader& operator=(const ScopedDmaReader&) = delete;
    ScopedDmaReader(ScopedDmaReader&&) = delete;
    ScopedDmaReader& operator=(ScopedDmaReader&&) = delete;

    ~ScopedDmaReader() {
#if defined(__linux__)
        sync_.reset();
        if (addr_ != nullptr) {
            ::munmap(addr_, size_);
        }
#endif
    }

    /**
     * @brief 建立只读映射；成功返回可读指针，失败返回 nullptr（调用方降级放行）
     *
     * ⚠️ 本函数是发布线程热路径上的 noexcept：同步守卫用 `std::optional` 原地构造，
     *    不像 `make_unique` 那样在 OOM 时抛 `bad_alloc` 穿出 noexcept 触发 std::terminate。
     */
    const uint8_t* open(int dma_fd, size_t map_size) noexcept {
#if defined(__linux__)
        if (dma_fd < 0 || map_size == 0) {
            return nullptr;
        }
        void* addr = ::mmap(nullptr, map_size, PROT_READ, MAP_SHARED, dma_fd, 0);
        if (addr == MAP_FAILED || addr == nullptr) {
            return nullptr;
        }
        sync_.emplace(dma_fd, platform::kDmaSyncRead);
        if (!sync_->active()) {
            sync_.reset();
            ::munmap(addr, map_size);
            return nullptr;
        }
        addr_ = addr;
        size_ = map_size;
        return static_cast<const uint8_t*>(addr_);
#else
        (void)dma_fd;
        (void)map_size;
        return nullptr;
#endif
    }

private:
#if defined(__linux__)
    void* addr_{nullptr};
    size_t size_{0};
    std::optional<platform::ScopedFdSync> sync_;
#endif
};

} // namespace

MotionDetector::MotionDetector() {
    prev_grid_.resize(grid_size, 0);
    curr_grid_.resize(grid_size, 0);
}

void MotionDetector::reset() noexcept {
    initialized_ = false;
    hold_frames_remaining_ = 0;
    still_frames_count_ = 0;
    std::fill(prev_grid_.begin(), prev_grid_.end(), 0);
    std::fill(curr_grid_.begin(), curr_grid_.end(), 0);
}

bool MotionDetector::sample_y_plane(const HardwareFrame& frame, const ViewWindow& view,
                                   uint8_t* out_grid) noexcept {
    if (frame.format != FRAME_FORMAT_NV12 && frame.format != FRAME_FORMAT_YUV420P) {
        return false;
    }
    const uint32_t stride = frame.plane_strides[0] > 0 ? frame.plane_strides[0] : frame.stride;
    if (frame.width < min_view_dimension || frame.height < min_view_dimension || stride < frame.width) {
        return false;
    }

    // 窗口只做只读采样：越界/过小一律 fail-open，绝不因一次读取失败关掉整条流。
    uint32_t origin_x = 0;
    uint32_t origin_y = 0;
    uint32_t sample_w = frame.width;
    uint32_t sample_h = frame.height;
    if (view.active) {
        if (view.x < 0 || view.y < 0 ||
            view.w < static_cast<int>(min_view_dimension) ||
            view.h < static_cast<int>(min_view_dimension) ||
            static_cast<uint32_t>(view.x) + static_cast<uint32_t>(view.w) > frame.width ||
            static_cast<uint32_t>(view.y) + static_cast<uint32_t>(view.h) > frame.height) {
            return false;
        }
        origin_x = static_cast<uint32_t>(view.x);
        origin_y = static_cast<uint32_t>(view.y);
        sample_w = static_cast<uint32_t>(view.w);
        sample_h = static_cast<uint32_t>(view.h);
    }

    ScopedDmaReader dma_reader;
    const uint8_t* y_src = nullptr;

    if (frame.host_planes[0] != nullptr) {
        y_src = frame.host_planes[0];
    } else if (frame.host_ptr != nullptr) {
        y_src = frame.host_ptr;
    } else if (frame.dma_fd >= 0) {
        size_t map_size = frame.y_plane_size();
        if (map_size == 0) {
            map_size = static_cast<size_t>(stride) * frame.height;
        }
        y_src = dma_reader.open(frame.dma_fd, map_size);
    }

    if (y_src == nullptr) {
        return false;
    }

    for (int y = 0; y < grid_height; ++y) {
        const uint32_t sy = origin_y + (static_cast<uint32_t>(y) * sample_h) / grid_height;
        const uint8_t* row = y_src + static_cast<size_t>(sy) * stride;
        uint8_t* dst = out_grid + static_cast<size_t>(y) * grid_width;

        for (int x = 0; x < grid_width; ++x) {
            const uint32_t sx = origin_x + (static_cast<uint32_t>(x) * sample_w) / grid_width;
            dst[x] = row[sx];
        }
    }

    return true;
}

void MotionDetector::report_static(MotionResult& out_result) noexcept {
    // 底图滚动：任何「判定为静止」的帧都成为下一帧的参考
    std::memcpy(prev_grid_.data(), curr_grid_.data(), grid_size);

    // hold-off：刚从运动滑到静止时继续放行，避免「站着不动 = 目标消失」抖动。
    // 保持期内没有有效外接框（box_count = 0），调用方按全帧处理。
    if (hold_frames_remaining_ > 0) {
        --hold_frames_remaining_;
        out_result.has_motion = true;
        out_result.box_count = 0;
        still_frames_count_ = 0;
        return;
    }

    // 真正脱离 hold-off 的静止帧：累加静止帧数
    ++still_frames_count_;
    if (still_frames_count_ >= motion_heartbeat_frames) {
        // 心跳保底巡检触发：单帧强制放行（box_count = 0），以刷新静态目标状态。
        // ⚠️ 严禁设置 hold_frames_remaining_：心跳只是单帧抽检，不得连带触发 10 帧放行！
        still_frames_count_ = 0;
        out_result.has_motion = true;
        out_result.box_count = 0;
        return;
    }

    out_result.has_motion = false;
    out_result.box_count = 0;
}

bool MotionDetector::detect(const HardwareFrame& frame, MotionResult& out_result) noexcept {
    return detect(frame, ViewWindow{}, out_result);
}

bool MotionDetector::detect(const HardwareFrame& frame, const ViewWindow& view,
                            MotionResult& out_result) noexcept {
    out_result = MotionResult{};

    if (!sample_y_plane(frame, view, curr_grid_.data())) {
        return false;
    }

    // 首帧只建底图，不做差分判定（无参考帧可比）。
    // ⚠️ 但绝不能返回 has_motion = false：流启动与每次换窗口 / reset() 之后的第一帧，
    //    往往正是「画面里已经站着人」的时刻；判静会让推理一直停到心跳
    //    （motion_heartbeat_frames）才恢复，改一次裁剪区域就盲十秒。
    //    无外接框（box_count = 0）表示「不知道在哪」，下游按全帧推理。
    if (!initialized_) {
        std::memcpy(prev_grid_.data(), curr_grid_.data(), grid_size);
        initialized_ = true;
        still_frames_count_ = 0;
        out_result.has_motion = true;
        out_result.box_count = 0;
        return true;
    }

    // 1. 宏块活跃统计
    std::array<std::array<bool, block_x_count>, block_y_count> active_blocks{};
    int total_active_pixels = 0;

    for (int by = 0; by < block_y_count; ++by) {
        for (int bx = 0; bx < block_x_count; ++bx) {
            int block_active = 0;
            const int start_y = by * block_height;
            const int start_x = bx * block_width;

            for (int dy = 0; dy < block_height; ++dy) {
                const size_t row_offset = static_cast<size_t>(start_y + dy) * grid_width + start_x;
                const uint8_t* p_curr = &curr_grid_[row_offset];
                const uint8_t* p_prev = &prev_grid_[row_offset];

                for (int dx = 0; dx < block_width; ++dx) {
                    if (abs_diff(p_curr[dx], p_prev[dx]) > diff_threshold) {
                        block_active++;
                    }
                }
            }

            total_active_pixels += block_active;
            if (block_active >= block_active_thresh) {
                active_blocks[by][bx] = true;
            }
        }
    }

    // 2. 全屏光照突变防抖（Anti-flash / Scene change）
    const float active_ratio =
        static_cast<float>(total_active_pixels) / static_cast<float>(grid_size);
    if (active_ratio > max_active_ratio) {
        report_static(out_result);
        return true;
    }

    // 3. 最小微噪门槛
    if (total_active_pixels < min_active_pixels) {
        report_static(out_result);
        return true;
    }

    // 4. 8-连通域 BFS 宏块聚类（队列与结果都是栈上定长数组，不进堆）
    std::array<std::array<bool, block_x_count>, block_y_count> visited{};
    std::array<Component, block_count> components{};
    size_t component_count = 0;

    // BFS 队列：每个宏块最多入队一次，容量恒为 block_count
    std::array<int, block_count> queue{};

    for (int by = 0; by < block_y_count; ++by) {
        for (int bx = 0; bx < block_x_count; ++bx) {
            if (!active_blocks[by][bx] || visited[by][bx]) {
                continue;
            }

            Component comp{bx, by, bx, by, 0};
            size_t head = 0;
            size_t tail = 0;
            queue[tail++] = by * block_x_count + bx;
            visited[by][bx] = true;

            while (head < tail) {
                const int index = queue[head++];
                const int cx = index % block_x_count;
                const int cy = index / block_x_count;
                comp.block_count++;

                comp.min_bx = std::min(comp.min_bx, cx);
                comp.max_bx = std::max(comp.max_bx, cx);
                comp.min_by = std::min(comp.min_by, cy);
                comp.max_by = std::max(comp.max_by, cy);

                for (int dy = -1; dy <= 1; ++dy) {
                    for (int dx = -1; dx <= 1; ++dx) {
                        if (dx == 0 && dy == 0) {
                            continue;
                        }
                        const int nx = cx + dx;
                        const int ny = cy + dy;
                        if (nx < 0 || nx >= block_x_count || ny < 0 || ny >= block_y_count) {
                            continue;
                        }
                        if (active_blocks[ny][nx] && !visited[ny][nx]) {
                            visited[ny][nx] = true;
                            queue[tail++] = ny * block_x_count + nx;
                        }
                    }
                }
            }

            components[component_count++] = comp;
        }
    }

    out_result.has_motion = true;
    hold_frames_remaining_ = motion_hold_frames;
    still_frames_count_ = 0;

    if (component_count > 0) {
        // 取面积最大的前 N 个连通域
        std::sort(components.begin(), components.begin() + component_count,
                  [](const Component& a, const Component& b) {
                      return a.block_count > b.block_count;
                  });

        const size_t take_count = std::min(component_count, ipc::MAX_IPC_MOTION_BOXES);
        out_result.box_count = static_cast<uint32_t>(take_count);

        for (size_t i = 0; i < take_count; ++i) {
            const auto& comp = components[i];
            out_result.boxes[i].x1 =
                static_cast<float>(comp.min_bx * block_width) / static_cast<float>(grid_width);
            out_result.boxes[i].y1 =
                static_cast<float>(comp.min_by * block_height) / static_cast<float>(grid_height);
            out_result.boxes[i].x2 =
                static_cast<float>((comp.max_bx + 1) * block_width) / static_cast<float>(grid_width);
            out_result.boxes[i].y2 =
                static_cast<float>((comp.max_by + 1) * block_height) / static_cast<float>(grid_height);
        }
    } else {
        // 宏块未形成聚类但全屏活跃像素达到门槛（离散噪声型运动），退化为全局最小外接框
        int min_x = grid_width;
        int max_x = -1;
        int min_y = grid_height;
        int max_y = -1;

        for (int y = 0; y < grid_height; ++y) {
            const size_t row_offset = static_cast<size_t>(y) * grid_width;
            const uint8_t* p_curr = &curr_grid_[row_offset];
            const uint8_t* p_prev = &prev_grid_[row_offset];
            for (int x = 0; x < grid_width; ++x) {
                if (abs_diff(p_curr[x], p_prev[x]) > diff_threshold) {
                    min_x = std::min(min_x, x);
                    max_x = std::max(max_x, x);
                    min_y = std::min(min_y, y);
                    max_y = std::max(max_y, y);
                }
            }
        }

        if (max_x >= min_x && max_y >= min_y) {
            out_result.box_count = 1;
            out_result.boxes[0].x1 =
                static_cast<float>(min_x) / static_cast<float>(grid_width);
            out_result.boxes[0].y1 =
                static_cast<float>(min_y) / static_cast<float>(grid_height);
            out_result.boxes[0].x2 =
                static_cast<float>(max_x + 1) / static_cast<float>(grid_width);
            out_result.boxes[0].y2 =
                static_cast<float>(max_y + 1) / static_cast<float>(grid_height);
        }
    }

    std::memcpy(prev_grid_.data(), curr_grid_.data(), grid_size);
    return true;
}

} // namespace zhulong
