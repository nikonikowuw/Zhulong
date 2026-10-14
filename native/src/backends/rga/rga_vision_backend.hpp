#pragma once

#include <algorithm/runtime/cpu_vision_backend.hpp>
#include <algorithm/runtime/vision_backend.hpp>

#include <atomic>
#include <memory>

namespace zhulong {

// 源图像素坐标下的裁剪框（两阶段算法的 ROI）。
// 语义：覆盖 [x, x + width) × [y, y + height)。
struct CropRect {
    int x{0};
    int y{0};
    int width{0};
    int height{0};
};

// RGA 硬件视觉前处理后端。
//
// 设计要点（对应 prd R1–R9 与 design §5/§7）：
//
// 1. **组合而非继承** CPU 实现：任一 RGA 前置条件不满足时**透明回退**到 CPU，
//    调用方只看到成功结果。回退是常态路径，不是异常路径。
// 2. **对齐值运行期探测**，绝不硬编码 4 或 16：硬编码 4 会在 RGA3（RK3576/RK3588
//    的 16）静默出错；硬编码 16 会浪费 RGA2 带宽。
// 3. **只做「改参数」级适配（O(1)）**：源侧 stride 直接传实际 linesize；`srect`
//    坐标按探测到的 offset 对齐取整。**任何需要重排内存（O(N)）的适配一律不做**
//    —— CPU 都遍历全图了，RGA 的收益已归零，不如直接回退 CPU（design §5.3 判据）。
// 4. **不引入 importbuffer_fd**（DMA-BUF 属后续任务，R9）：只用
//    wrapbuffer_virtualaddr，它不接管内存所有权。
// 5. **多平面格式（NV12/YUV420P）用单 buffer 连续布局**：RGA 的 im2d API 没有
//    多平面入口，plane 偏移由库按 `wstride × hstride` 推导（真机实测，见
//    `algo-plugin-contract.md` §9）。因此调用方必须提供**连续**的平面布局；
//    不连续一律回退 CPU（重排是 O(N)，不做）。
// 6. **每次调用前用 `imcheck_t` 预校验**：RGA 驱动对越界 DMA 不总是返回错误码
//    —— 本任务实测过一次越界 `imresize` 使 RK3568 直接复位（`uptime -s` 可对照）。
//    预校验的开销远小于一次整机重启，因此作为强制前置步骤保留。
class RgaVisionBackend : public IVisionBackend {
public:
    RgaVisionBackend();
    ~RgaVisionBackend() override = default;

    zhulong_status_t letterbox(const zhulong_frame_view& src,
                               Tensor& dst,
                               LetterboxParams& inout_params) override;

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

    // 运行期能力探测结果（供测试与运维观测，不跨越 C ABI）。
    [[nodiscard]] bool rga_usable() const noexcept { return rga_usable_; }
    [[nodiscard]] uint32_t stride_align() const noexcept { return stride_align_; }
    [[nodiscard]] uint32_t offset_align() const noexcept { return offset_align_; }
    [[nodiscard]] bool stride_aligned(int stride_bytes) const noexcept {
        return stride_align_ > 0 && (static_cast<uint32_t>(stride_bytes) % stride_align_ == 0u);
    }
    [[nodiscard]] const char* api_version() const noexcept { return api_version_; }
    [[nodiscard]] const char* driver_version() const noexcept { return driver_version_; }

    // ⚠️ 后端是 `runtime_factory` 里的进程内单例，被多个 worker 线程共享，
    //    因此计数器必须是原子的（否则是数据竞争）。
    [[nodiscard]] uint64_t fallback_count() const noexcept { return fallback_count_.load(); }
    // AC9：「适配后成功」的计数 —— RGA 路径使用**非紧密**的源行跨度（即把调用方的
    // 实际 `linesize` 传给 RGA，而非取 `width × bpp`）。与 `fallback_count()` 一起
    // 区分「适配后成功」与「回退成功」两条路径。
    [[nodiscard]] uint64_t stride_adapted_count() const noexcept {
        return stride_adapted_count_.load();
    }
    // AC10：「裁剪坐标取整后成功」的计数 —— `srect.x/y` 被向上取整到
    // `offset_align()` 的倍数后 RGA 成功（形式 1，内存不动）。
    [[nodiscard]] uint64_t rect_adapted_count() const noexcept {
        return rect_adapted_count_.load();
    }

private:
    // 探测 RGA 可用性：imcheckHeader() + /dev/rga 可访问性 + 版本与对齐值查询。
    void probe();

    // 返回 true 表示 RGA 路径成功并已写入 dst；false 表示应回退 CPU。
    bool try_rga_letterbox(const zhulong_frame_view& src, Tensor& dst, LetterboxParams& inout_params);
    bool try_rga_resize(const uint8_t* src, int src_w, int src_h, int src_stride,
                        zhulong_pixel_format_t src_format, uint8_t* dst, int dst_stride,
                        int dst_w, int dst_h, zhulong_pixel_format_t dst_format);
    bool try_rga_crop_scale(const zhulong_frame_view& src, const CropRect& roi,
                            uint8_t* dst, int dst_stride, int dst_w, int dst_h,
                            zhulong_pixel_format_t dst_format);

    CpuVisionBackend fallback_;   // 组合：回退目标
    bool rga_usable_{false};
    uint32_t stride_align_{0};    // 运行期探测；0 表示未取到 → 不使用 RGA
    uint32_t offset_align_{0};    // 运行期探测（rect 偏移对齐）；0 表示未取到
    char api_version_[64]{};
    char driver_version_[64]{};
    std::atomic<uint64_t> fallback_count_{0};
    std::atomic<uint64_t> stride_adapted_count_{0};
    std::atomic<uint64_t> rect_adapted_count_{0};
};

} // namespace zhulong
