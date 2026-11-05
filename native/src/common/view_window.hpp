/**
 * @file view_window.hpp
 * @brief AI 分析视图窗口：宿主与 AlgoIpc 共享的纯几何契约（header-only）
 *
 * 三条不变量：
 *  1. 几何是 `(归一化矩形, 解码帧尺寸)` 的纯函数，不含任何硬件对齐外扩；
 *  2. 窗口与归一化 E 由同一个函数派生，逆映射的唯一依据就是窗口本身；
 *  3. 紧打包布局同时服务两处：FrameBroker 填 dispatch、WindowCrop 选目标 stride。
 *
 * ⚠️ 本头文件只做类型与纯函数，不放实现（不新增 .o），也不得进入 AlgoIpc 归档之外的
 *    平台相关依赖。宿主与算法包共享类型并不等于共享代码路径。
 */

#pragma once

#include <Zhulong/frame.hpp>

#include <algorithm>
#include <cmath>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <limits>

namespace zhulong {

/**
 * @brief 归一化裁剪矩形，严格 `[0,1]` 且 `x1 < x2`、`y1 < y2`。
 *
 * 用 float 是因为它直接对应 C ABI 的 `Zhulong_view_rect`；几何计算内部提升到 double。
 */
struct ViewRect {
    float x1{0.0f};
    float y1{0.0f};
    float x2{1.0f};
    float y2{1.0f};
};

/** @brief 全幅视图（= 未设置视图形状）。 */
inline ViewRect identity_view_rect() noexcept { return ViewRect{}; }

/** @brief 该矩形是否覆盖全幅（`view == NULL` 的等价形态）。 */
inline bool view_rect_is_full_frame(const ViewRect &rect) noexcept {
    return rect.x1 <= 0.0f && rect.y1 <= 0.0f && rect.x2 >= 1.0f && rect.y2 >= 1.0f;
}

/**
 * @brief 像素窗口。
 *
 * `active == false` 表示“不裁剪”，调用方必须走既有全幅路径（零拷贝直通/原 stride 拷贝）。
 */
struct ViewWindow {
    bool active{false};
    int x{0};
    int y{0};
    int w{0};
    int h{0};
};

/**
 * @brief 计算像素窗口：floor 起点 + ceil 终点 + 统一外向取偶 + 夹回帧边界。
 *
 * ⚠️ `frame_w/frame_h` 必须是**解码帧**尺寸，不是 RTSP 元数据尺寸。二者在 1088 裁切、
 *    MPP 对齐、分辨率热切换时会不一致；用元数据算窗口会让 E 的分母与像素来源脱钩。
 *
 * 取偶后若窗口恰好等于整帧，返回 `active=false`：省一次拷贝，并让“无裁剪”路径与
 * 既有字节布局保持等价。
 */
inline ViewWindow compute_view_window(const ViewRect &rect, int frame_w, int frame_h) noexcept {
    ViewWindow win{};
    if (frame_w < 2 || frame_h < 2) {
        return win;
    }
    if (view_rect_is_full_frame(rect)) {
        return win;
    }

    const double width = static_cast<double>(frame_w);
    const double height = static_cast<double>(frame_h);

    int x1 = static_cast<int>(std::floor(static_cast<double>(rect.x1) * width));
    int x2 = static_cast<int>(std::ceil(static_cast<double>(rect.x2) * width));
    int y1 = static_cast<int>(std::floor(static_cast<double>(rect.y1) * height));
    int y2 = static_cast<int>(std::ceil(static_cast<double>(rect.y2) * height));

    x1 = std::clamp(x1, 0, frame_w - 1);
    x2 = std::clamp(x2, x1 + 1, frame_w);
    y1 = std::clamp(y1, 0, frame_h - 1);
    y2 = std::clamp(y2, y1 + 1, frame_h);

    // 统一外向取偶（所有格式，不是硬件探测值）：RGA/CPU 两条路径共用同一几何。
    x1 &= ~1;
    y1 &= ~1;
    x2 = std::min(frame_w & ~1, (x2 + 1) & ~1);
    y2 = std::min(frame_h & ~1, (y2 + 1) & ~1);

    // 奇数宽高在右/下边缘会退化；把起点左/上推 2px，保持 2px 最小窗口。
    if (x2 < 2 || y2 < 2) {
        return win;
    }
    if (x1 > x2 - 2) {
        x1 = x2 - 2;
    }
    if (y1 > y2 - 2) {
        y1 = y2 - 2;
    }
    if (x1 < 0 || y1 < 0) {
        return win;
    }

    // 取整后退化为整帧 → 仍按“不裁剪”处理。
    if (x1 == 0 && y1 == 0 && x2 == (frame_w & ~1) && y2 == (frame_h & ~1)) {
        return win;
    }

    win.active = true;
    win.x = x1;
    win.y = y1;
    win.w = x2 - x1;
    win.h = y2 - y1;
    return win;
}

/** @brief 由像素窗口反推生效 E（逆映射的唯一依据）。 */
inline ViewRect window_to_view_rect(const ViewWindow &win, int frame_w, int frame_h) noexcept {
    if (!win.active || frame_w <= 0 || frame_h <= 0) {
        return identity_view_rect();
    }
    const double width = static_cast<double>(frame_w);
    const double height = static_cast<double>(frame_h);
    return ViewRect{
        static_cast<float>(static_cast<double>(win.x) / width),
        static_cast<float>(static_cast<double>(win.y) / height),
        static_cast<float>(static_cast<double>(win.x + win.w) / width),
        static_cast<float>(static_cast<double>(win.y + win.h) / height),
    };
}

/** @brief 向上取整到 alignment 的倍数（align_up(w, 64) 覆盖已知的 4/16/64 探测值）。 */
inline uint32_t view_align_up(uint32_t value, uint32_t alignment) noexcept {
    if (alignment == 0) {
        return value;
    }
    return ((value + alignment - 1) / alignment) * alignment;
}

/**
 * @brief 窗口的紧打包布局。
 *
 * 与 `resolve_source` 的三条校验一一对应：
 *  - `y_stride % plane0_bpp == 0`（NV12 bpp=1、YUV420P bpp=1）；
 *  - `y_stride % stride_align == 0`（64 覆盖已知探测值 4/16/64）；
 *  - `deduce_continuous_hstride` 推出的 `hstride == h`。
 */
struct PackedLayout {
    bool valid{false};
    uint32_t stride{0};
    uint32_t offsets[4]{0, 0, 0, 0};
    int32_t strides[4]{0, 0, 0, 0};
    size_t bytes{0};
};

/**
 * @brief 按窗口尺寸计算紧打包布局；未知格式返回 `valid=false`（调用方必须 fail_publish）。
 *
 * NV12  : stride = align_up(w, 64)，UV 紧跟在 Y 之后；
 * YUV420P: stride = align_up(w, 64)，U/V 行跨度 = stride/2。
 */
inline PackedLayout packed_layout_for(const ViewWindow &win, uint32_t format) noexcept {
    PackedLayout layout{};
    if (!win.active || win.w <= 0 || win.h <= 0) {
        return layout;
    }
    const uint32_t w = static_cast<uint32_t>(win.w);
    const uint32_t h = static_cast<uint32_t>(win.h);

    if (format == FRAME_FORMAT_NV12 || format == FRAME_FORMAT_YUV420P) {
        const uint32_t stride = view_align_up(w, 64);
        if (stride == 0 || stride > static_cast<uint32_t>(std::numeric_limits<int32_t>::max())) {
            return layout;
        }
        const uint32_t y_size = stride * h;
        layout.stride = stride;
        layout.offsets[0] = 0;
        layout.strides[0] = static_cast<int32_t>(stride);

        if (format == FRAME_FORMAT_NV12) {
            layout.offsets[1] = y_size;
            layout.strides[1] = static_cast<int32_t>(stride);
            layout.bytes = static_cast<size_t>(y_size) + static_cast<size_t>(stride) * ((h + 1) / 2);
        } else {
            const uint32_t chroma_stride = stride / 2;
            const uint32_t chroma_rows = (h + 1) / 2;
            const uint32_t u_size = chroma_stride * chroma_rows;
            layout.offsets[1] = y_size;
            layout.offsets[2] = y_size + u_size;
            layout.strides[1] = static_cast<int32_t>(chroma_stride);
            layout.strides[2] = static_cast<int32_t>(chroma_stride);
            layout.bytes = static_cast<size_t>(y_size) + static_cast<size_t>(u_size) * 2;
        }
        layout.valid = true;
    }
    return layout;
}

/**
 * @brief 窗口物化回调。
 *
 * `dst_dma_fd >= 0` 表示目标是 DMA 槽位（CPU 写入必须做写同步）；`-1` 表示 shm 槽位。
 * 返回 false 表示“该帧不能安全物化”，调用方必须 fail_publish——绝不发送半截帧。
 */
using CropCopyFn = std::function<bool(const HardwareFrame &frame, const ViewWindow &view,
                                      uint8_t *dst, size_t dst_size, int dst_stride,
                                      int dst_dma_fd)>;

} // namespace zhulong
