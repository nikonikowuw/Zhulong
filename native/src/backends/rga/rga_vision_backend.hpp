/**
 * @file rga_vision_backend.hpp
 * @brief RGA 硬件视觉前处理后端（组合 CPU 实现，任一前置条件不满足时透明回退）
 *
 * 核心设计原则（对应 prd R1–R9 与 design §5/§7）：
 * 1. **组合而非继承** CPU 实现：RGA 前置条件不满足时透明回退 CPU。
 *    CPU 也不支持的输入仍返回错误；DMA CPU 同步失败必须直接失败，不继续访问或提交。
 * 2. **对齐值运行期探测**，绝不硬编码真值：硬编码 4 会在 RGA3（RK3576/RK3588
 *    的 16）上白跑一次注定被 imcheck_t 拒掉的尝试；硬编码 16 会浪费 RGA2 带宽。
 *    debugfs 不可读时回退到平台基准值（4，见 hardware-backends.md §3.1）——那是
 *    「探测失败的安全假设」而不是平台真值，因此**失败事实必须可观测**（last_error()）
 *    而不是静默降速。
 * 3. **只做「改参数」级适配（O(1)）**：源侧 stride 直接传实际 linesize；`srect`
 *    坐标按探测到的 offset 对齐取整。**任何需要重排内存（O(N)）的适配一律不做**
 *    —— CPU 都遍历全图了，RGA 的收益已归零，不如直接回退 CPU（design §5.3 判据）。
 * 4. 有 DMA-BUF fd 时用 wrapbuffer_fd，否则用 wrapbuffer_virtualaddr；两者都不转移
 *    缓冲区所有权。本后端不缓存 importbuffer handle，也不据此声称整条链路零拷贝。
 * 5. **多平面格式（NV12/YUV420P）用单 buffer 连续布局**：RGA 的 im2d API 没有
 *    多平面入口，plane 偏移由库按 `wstride × hstride` 推导（真机实测，见
 *    `algo-plugin-contract.md` §9）。因此调用方必须提供**连续**的平面布局；
 *    不连续一律回退 CPU（重排是 O(N)，不做）。
 * 6. **每次调用前用 `imcheck_t` 预校验**：RGA 驱动对越界 DMA 不总是返回错误码
 *    —— 本任务实测过一次越界 `imresize` 使 RK3568 直接复位（`uptime -s` 可对照）。
 *    预校验的开销远小于一次整机重启，因此作为强制前置步骤保留。
 *
 * ⚠️ 厂商头文件隔离：rga/im2d.h 等只允许在 native/src/backends/rga/ 内引用，
 * 严禁泄露至公开 SDK、引擎核心或调度层。
 * ⚠️ 设备权限：/dev/rga 为 root:video，运行用户须加入 video 组。
 */

#pragma once

#include <algorithm/runtime/cpu_vision_backend.hpp>
#include <zhulong/runtime/vision_backend.hpp>

#include <atomic>
#include <memory>

namespace zhulong {

/**
 * @brief 源图像素坐标下的裁剪框（两阶段算法的 ROI）
 *
 * 语义：覆盖 [x, x + width) × [y, y + height)。
 */
struct CropRect {
    int x{0};       /**< 左边界（源图像素坐标） */
    int y{0};       /**< 上边界（源图像素坐标） */
    int width{0};   /**< 裁剪宽度 */
    int height{0};  /**< 裁剪高度 */
};

/**
 * @brief RGA 硬件视觉前处理后端（详见本文件头部设计要点）
 */
class RgaVisionBackend : public IVisionBackend {
public:
    RgaVisionBackend();
    ~RgaVisionBackend() override = default;
    RgaVisionBackend(const RgaVisionBackend&) = delete;
    RgaVisionBackend& operator=(const RgaVisionBackend&) = delete;
    RgaVisionBackend(RgaVisionBackend&&) = delete;
    RgaVisionBackend& operator=(RgaVisionBackend&&) = delete;

    /**
     * @brief 图像 Letterbox 预处理；RGA 路径不满足前置条件时透明回退 CPU
     */
    zhulong_status_t letterbox(const zhulong_frame_view& src,
                               Tensor& dst,
                               LetterboxParams& inout_params) override;

    /**
     * @brief 双线性图像缩放；RGA 路径不满足前置条件时透明回退 CPU
     */
    zhulong_status_t resize(const uint8_t* src, int src_w, int src_h, int src_stride,
                            zhulong_pixel_format_t src_format, uint8_t* dst, int dst_stride,
                            int dst_w, int dst_h, zhulong_pixel_format_t dst_format) override;

    // 两阶段算法的「裁剪 + 缩放 + CSC」（design §5.4）：**一次 `improcess`** 完成，
    // 全链路零 O(N) 内存重排（`srect` 是参数，不是内存）。
    //
    // ⚠️ `IVisionBackend` 当前**没有**裁剪入口（prd R5 禁止改接口），因此本方法
    //    暂不可由插件经 `zhulong::ops::*` 调用；它把 §5.4 的机制落地并真机验证，
    //    供后续在接口层暴露时直接复用。返回值语义：
    //      `OK`               → RGA 路径完成
    //      `NOT_SUPPORTED`    → RGA 不可用 / 格式不支持 / ROI 无法在 O(1) 内适配
    //      `INVALID_ARGUMENT` → 参数非法
    zhulong_status_t crop_scale(const zhulong_frame_view& src, const CropRect& roi,
                                uint8_t* dst, int dst_stride, int dst_w, int dst_h,
                                zhulong_pixel_format_t dst_format);

    /**
     * @brief RGA 是否可用（运行期探测结果，供测试与运维观测，不跨越 C ABI）
     */
    [[nodiscard]] bool rga_usable() const noexcept { return rga_usable_; }

    /**
     * @brief 探测到的行跨度字节对齐值（RK3568 实测 4；未取到为 0 → 不使用 RGA）
     */
    [[nodiscard]] uint32_t stride_align() const noexcept { return stride_align_; }

    /**
     * @brief 探测到的 ROI 偏移对齐值（RK3568 实测 2；未取到为 0）
     */
    [[nodiscard]] uint32_t offset_align() const noexcept { return offset_align_; }

    /**
     * @brief 判定给定行跨度字节数是否满足硬件对齐要求
     */
    [[nodiscard]] bool stride_aligned(int stride_bytes) const noexcept {
        return stride_align_ > 0 && (static_cast<uint32_t>(stride_bytes) % stride_align_ == 0u);
    }

    /**
     * @brief RGA 库 API 版本字符串
     */
    [[nodiscard]] const char* api_version() const noexcept { return api_version_; }

    /**
     * @brief RGA 驱动版本字符串
     */
    [[nodiscard]] const char* driver_version() const noexcept { return driver_version_; }

    // ⚠️ 后端是 `runtime_factory` 里的进程内单例，被多个 worker 线程共享，
    //    因此计数器必须是原子的（否则是数据竞争）。
    /**
     * @brief 回退到 CPU 的累计次数
     */
    [[nodiscard]] uint64_t fallback_count() const noexcept { return fallback_count_.load(); }

    // AC9：「适配后成功」的计数 —— RGA 路径使用**非紧密**的源行跨度（即把调用方的
    // 实际 `linesize` 传给 RGA，而非取 `width × bpp`）。与 `fallback_count()` 一起
    // 区分「适配后成功」与「回退成功」两条路径。
    /**
     * @brief 使用非紧密源行跨度适配后成功的累计次数
     */
    [[nodiscard]] uint64_t stride_adapted_count() const noexcept {
        return stride_adapted_count_.load();
    }

    /**
     * @brief 最近一次 RGA 尝试失败的原因（RGA 成功则为 ZHULONG_PLUGIN_OK）
     *
     * ⚠️ 运行库刻意不链接 `ZhulongLog`（见 native/CMakeLists.txt），因此后端没有日志通道。
     *    本字段就是「RGA 为何降级」的对外可观测面：NOT_SUPPORTED 表示前置条件不满足
     *    （格式/对齐/不可用），其余码是 imcheck_t / improcess 映射后的结果。
     *    `fallback_count()` 只回答「降级了多少次」，不回答「为何」。
     */
    [[nodiscard]] zhulong_status_t last_error() const noexcept {
        return last_error_.load(std::memory_order_relaxed);
    }

    // AC10：「裁剪坐标取整后成功」的计数 —— `srect.x/y` 被向上取整到
    // `offset_align()` 的倍数后 RGA 成功（形式 1，内存不动）。
    /**
     * @brief 裁剪坐标取整后成功的累计次数（形式 1，内存不动）
     */
    [[nodiscard]] uint64_t rect_adapted_count() const noexcept {
        return rect_adapted_count_.load();
    }

private:
    // 探测 RGA 可用性：imcheckHeader() + /dev/rga 可访问性 + 版本与对齐值查询。
    void probe();

    // 记录最近一次 RGA 尝试的失败原因（供 last_error() 观测）。
    void record_rga_error(zhulong_status_t status) noexcept {
        last_error_.store(status, std::memory_order_relaxed);
    }

    // 三个 try_* 的返回语义：OK = RGA 已完成；INVALID_ARGUMENT = 入参非法；
    // OUT_OF_MEMORY = 目标缓冲分配失败；NOT_SUPPORTED = 前置条件不满足；
    // INVALID_STATE = DMA CPU 同步失败，必须停止；其余 = imcheck_t / improcess 映射，允许 CPU 回退。
    zhulong_status_t try_rga_letterbox(const zhulong_frame_view& src, Tensor& dst, LetterboxParams& inout_params);
    zhulong_status_t try_rga_resize(const uint8_t* src, int src_w, int src_h, int src_stride,
                                    zhulong_pixel_format_t src_format, uint8_t* dst, int dst_stride,
                                    int dst_w, int dst_h, zhulong_pixel_format_t dst_format);
    zhulong_status_t try_rga_crop_scale(const zhulong_frame_view& src, const CropRect& roi,
                                        uint8_t* dst, int dst_stride, int dst_w, int dst_h,
                                        zhulong_pixel_format_t dst_format);

    CpuVisionBackend fallback_;   /**< 组合：回退目标 */
    bool rga_usable_{false};      /**< 探测得到的 RGA 可用性 */
    uint32_t stride_align_{0};    /**< 运行期探测；0 表示未取到 → 不使用 RGA */
    uint32_t offset_align_{0};    /**< 运行期探测（rect 偏移对齐）；0 表示未取到 */
    char api_version_[64]{};      /**< RGA 库 API 版本字符串缓冲 */
    char driver_version_[64]{};   /**< RGA 驱动版本字符串缓冲 */
    std::atomic<uint64_t> fallback_count_{0};        /**< CPU 回退计数（原子） */
    std::atomic<uint64_t> stride_adapted_count_{0};  /**< 非紧密 stride 适配成功计数（原子） */
    std::atomic<uint64_t> rect_adapted_count_{0};    /**< 裁剪坐标取整成功计数（原子） */
    std::atomic<zhulong_status_t> last_error_{ZHULONG_PLUGIN_OK}; /**< 最近一次 RGA 失败原因（原子） */
};

} // namespace zhulong

