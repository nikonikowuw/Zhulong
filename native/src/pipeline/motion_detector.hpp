/**
 * @file motion_detector.hpp
 * @brief 轻量级流级运动区域检测器（基于 NV12/YUV420P Y 平面下采样差分）
 *
 * 核心机制：
 * 1. 320×180 固定网格下采样：16:9 严格对齐，单帧网格 57,600 字节（~56KB），
 *    1080p 下单点覆盖 6×6 像素，4K 下覆盖 12×12 像素。
 * 2. 检测本身零堆分配、零系统调用：两块网格缓冲区与聚类工作区全部是成员/栈上数组，
 *    detect() 全程只做内存读写。
 * 3. 大面积突变 fail-open（Anti-flash / Scene change）：单帧变化面积占比超阈值时无法区分
 *    环境光突变（红外夜视切换、自动曝光跳档）与近处大目标 / 小窗口内运动，因此本帧全帧放行
 *    （has_motion = true, box_count = 0）并以本帧重置参考底图，光照跳变只多推理一帧。
 * 4. 持续运动保持（hold-off）：从「有运动」滑到「静止」后继续保持 motion_hold_frames
 *    帧放行，覆盖「人/车站住不动就不该立刻判定目标消失」的场景。
 * 5. 宏块聚类：16×9 个 20×20 宏块做 8-连通域聚合，输出最多 4 个归一化外接矩形。
 *
 * ⚠️ 唯一的外部代价在 DMA-BUF 路径：RK 平台 MPP 硬解帧只填 `dma_fd`（见
 *    `nodes/decode/mpp_decoder.cpp`，`host_planes` 为空），因此每帧需要
 *    `mmap` + `DMA_BUF_IOCTL_SYNC(START/END)` + `munmap` 三次系统调用才能读到 Y 平面。
 *    这是「不额外持有解码缓冲」的取舍，不要在文档里把它写成纯缓存命中。
 */

#pragma once

#include "algorithm/ipc/ipc_protocol.hpp"
#include "common/view_window.hpp"

#include <Zhulong/frame.hpp>
#include <array>
#include <cstddef>
#include <cstdint>
#include <vector>

namespace zhulong {

/**
 * @brief 单帧运动检测输出结果
 *
 * 外接框直接复用 IPC 线格式的 `ipc::IpcMotionBox`：同一份归一化语义不该有两个结构体，
 * 否则每次跨层搬运都要逐字段重抄一遍。
 */
struct MotionResult {
    /**
     * ⚠️ 默认放行：`MotionResult` 的默认构造只可能出现在「没真正检测过」的场景，
     *    那时绝不能静默跳过推理。detect() 成功执行后会无条件覆写本字段。
     */
    bool has_motion{true};
    uint32_t box_count{0};                                          /**< 有效外接框数量（0 ~ MAX_IPC_MOTION_BOXES） */
    std::array<ipc::IpcMotionBox, ipc::MAX_IPC_MOTION_BOXES> boxes{}; /**< 归一化外接矩形列表 */
};

/**
 * @brief 轻量级视频流前置运动检测器
 *
 * 单实例绑定单条物理流（`FramePublisher` 每个 AI 消费通道一个），因此「每流每帧只算一次」
 * 由持有关系本身保证，与挂载了多少个算法包无关。
 */
class MotionDetector {
public:
    static constexpr int grid_width = 320;
    static constexpr int grid_height = 180;
    static constexpr size_t grid_size = static_cast<size_t>(grid_width) * grid_height;

    static constexpr int block_x_count = 16;
    static constexpr int block_y_count = 9;
    static constexpr size_t block_count = static_cast<size_t>(block_x_count) * block_y_count;
    static constexpr int block_width = grid_width / block_x_count;   // 20
    static constexpr int block_height = grid_height / block_y_count; // 20

    static constexpr int diff_threshold = 25;         /**< 灰度差分阈值 (0-255) */
    static constexpr int block_active_thresh = 10;    /**< 宏块内最小活跃像素数 (2.5%) */
    static constexpr int min_active_pixels = 30;      /**< 全屏最小运动像素数门槛 (~0.05%) */
    static constexpr float max_active_ratio = 0.70f;  /**< 光照突变全屏占比门槛 (PRD R1: 70%) */
    static constexpr uint32_t min_view_dimension = 16; /**< 画面/窗口最小有效宽高门槛 */

    /**
     * @brief 运动停止后继续放行推理的帧数（@5fps 约 2 秒）
     *
     * 逐帧差分是瞬时判据：被摄主体站住一帧就会判成静止。若不 hold，
     * 「人站着不动 → 目标消失」会持续抖动。
     */
    static constexpr uint32_t motion_hold_frames = 10;

    /**
     * @brief 画面持续静止时强制放行心跳巡检的帧数间隔（@5fps 约 10 秒）
     *
     * 解决长时间静止导致静态目标检出丢失或微动漏检的问题。
     */
    static constexpr uint32_t motion_heartbeat_frames = 50;

    MotionDetector();
    ~MotionDetector() = default;

    MotionDetector(const MotionDetector&) = delete;
    MotionDetector& operator=(const MotionDetector&) = delete;
    MotionDetector(MotionDetector&&) = delete;
    MotionDetector& operator=(MotionDetector&&) = delete;

    /**
     * @brief 重置检测器内部参考背景（在流切换、断流重连或跳帧后调用）
     */
    void reset() noexcept;

    /**
     * @brief 对输入硬件帧执行运动检测（全幅，兼容既有调用点）
     *
     * ⚠️ fail-open：输入非法 / 格式不支持 / 无参考底图（首帧）都返回 has_motion = true
     *    （首帧 path 返回 true 但 box_count = 0）。漏检代价高于算力。
     *
     * @param frame 输入解码帧（NV12 或 YUV420P）
     * @param[out] out_result 检测结果
     * @return true 成功执行检测；false 输入非法或格式不支持（调用方必须降级放行）
     */
    bool detect(const HardwareFrame& frame, MotionResult& out_result) noexcept;

    /**
     * @brief 在 AI 分析视图窗口内执行运动检测
     *
     * `view.active == false` 时等价于全幅检测。窗口越界/尺寸过小时返回 false（fail-open）。
     * 网格仍是固定的 320×180，只是采样源变成窗口内部——窗口越小，单点覆盖的源像素越少。
     */
    bool detect(const HardwareFrame& frame, const ViewWindow& view, MotionResult& out_result) noexcept;

private:
    bool sample_y_plane(const HardwareFrame& frame, const ViewWindow& view, uint8_t* out_grid) noexcept;

    /** @brief 宏块连通域（聚类中间结果，栈上数组承载，不进堆） */
    struct Component {
        int min_bx{0};
        int min_by{0};
        int max_bx{0};
        int max_by{0};
        int block_count{0};
    };

    /**
     * @brief 判定为无有效运动：滚动底图并按 hold-off 决定是否仍然放行
     */
    void report_static(MotionResult& out_result) noexcept;

    bool initialized_{false};
    uint32_t hold_frames_remaining_{0};
    uint32_t still_frames_count_{0};
    std::vector<uint8_t> prev_grid_;
    std::vector<uint8_t> curr_grid_;
};

} // namespace zhulong
