/**
 * @file view_window_test.cpp
 * @brief 窗口几何与紧打包布局的纯函数回归（无设备、无后端依赖）
 */

#include "../src/common/view_window.hpp"

#include <cassert>
#include <cmath>
#include <cstdint>
#include <iostream>

using namespace zhulong;

namespace {

constexpr uint32_t kFrameFormatUnknown = FRAME_FORMAT_UNKNOWN;

void assert_near(float actual, float expected, const char *what) {
    if (std::fabs(actual - expected) > 1e-6f) {
        std::cerr << "[FAIL] " << what << ": got " << actual << " want " << expected << std::endl;
        std::abort();
    }
}

void test_full_frame_is_inactive() {
    const ViewWindow win = compute_view_window(ViewRect{0.0f, 0.0f, 1.0f, 1.0f}, 1920, 1080);
    assert(!win.active);

    // 取整后恰好覆盖整帧：同样按不裁剪处理（省一次拷贝，保持字节等价）。
    const ViewWindow rounded = compute_view_window(ViewRect{0.0f, 0.0f, 1.0f, 0.999f}, 1920, 1080);
    assert(!rounded.active);

    const ViewWindow small = compute_view_window(ViewRect{0.0f, 0.0f, 1.0f, 1.0f}, 1, 1);
    assert(!small.active);
}

void test_basic_rect() {
    // 用 1/4 网格：float32 下可精确表示，便于断言 E 与像素窗口逐值对应。
    const ViewWindow win = compute_view_window(ViewRect{0.25f, 0.25f, 0.5f, 0.5f}, 1920, 1080);
    assert(win.active);
    assert(win.x == 480 && win.y == 270 && win.w == 480 && win.h == 270);

    const ViewRect back = window_to_view_rect(win, 1920, 1080);
    assert_near(back.x1, 0.25f, "x1");
    assert_near(back.y1, 0.25f, "y1");
    assert_near(back.x2, 0.5f, "x2");
    assert_near(back.y2, 0.5f, "y2");

    // 外向取整：0.6f 略大于 0.6，ceil 后窗口必须覆盖配置区域（不得向内收缩）。
    const ViewWindow outward = compute_view_window(ViewRect{0.0f, 0.0f, 1.0f, 0.6f}, 1920, 1080);
    assert(outward.active);
    assert(outward.y == 0);
    assert(static_cast<double>(outward.h) >= 0.6 * 1080.0);
}

void test_odd_dimensions_stay_in_bounds() {
    // 1919×1079 + x2/y2=1.0：取偶后必须夹回 W&~1 / H&~1，否则会越界一列/一行。
    const ViewWindow win = compute_view_window(ViewRect{0.1f, 0.1f, 1.0f, 1.0f}, 1919, 1079);
    assert(win.active);
    assert(win.x >= 0 && win.y >= 0);
    assert(win.x + win.w <= 1919 && win.y + win.h <= 1079);
    assert((win.x % 2) == 0 && (win.y % 2) == 0);
    assert((win.w % 2) == 0 && (win.h % 2) == 0);
    assert(win.x + win.w == 1918 && win.y + win.h == 1078);

    const ViewRect back = window_to_view_rect(win, 1919, 1079);
    assert(back.x2 <= 1.0f && back.y2 <= 1.0f);
}

void test_right_bottom_edge() {
    const ViewWindow win = compute_view_window(ViewRect{0.5f, 0.5f, 1.0f, 1.0f}, 1919, 1079);
    assert(win.active);
    assert(win.x + win.w == 1918);
    assert(win.y + win.h == 1078);
    assert(win.w >= 2 && win.h >= 2);
}

void test_minimum_window_clamp() {
    // 极小矩形在取偶后可能只剩 0..1，必须把起点推回，保证至少 2px。
    const ViewWindow win = compute_view_window(ViewRect{0.0f, 0.0f, 0.0001f, 0.0001f}, 1920, 1080);
    if (win.active) {
        assert(win.w >= 2 && win.h >= 2);
        assert(win.x >= 0 && win.y >= 0);
    }
}

void test_packed_layout_nv12() {
    const ViewWindow win{true, 0, 0, 640, 360};
    const PackedLayout layout = packed_layout_for(win, FRAME_FORMAT_NV12);
    assert(layout.valid);
    assert(layout.stride == 640); // 640 已是 64 的倍数
    assert(layout.offsets[0] == 0);
    assert(layout.offsets[1] == 640u * 360u);
    assert(layout.strides[0] == 640 && layout.strides[1] == 640);
    assert(layout.bytes == 640u * 360u + 640u * 180u);
}

void test_packed_layout_yuv420p_alignment() {
    const ViewWindow win{true, 0, 0, 638, 358};
    const PackedLayout layout = packed_layout_for(win, FRAME_FORMAT_YUV420P);
    assert(layout.valid);
    assert(layout.stride == 640); // align_up(638, 64)
    assert(layout.strides[1] == 320 && layout.strides[2] == 320);
    assert(layout.offsets[1] == 640u * 358u);
    assert(layout.offsets[2] == layout.offsets[1] + 320u * 179u);
    assert(layout.bytes == 640u * 358u + 2u * (320u * 179u));
}

void test_packed_layout_rejects_unknown() {
    const ViewWindow win{true, 0, 0, 640, 360};
    assert(!packed_layout_for(win, kFrameFormatUnknown).valid);
    assert(!packed_layout_for(ViewWindow{}, FRAME_FORMAT_NV12).valid);
}

void test_packed_layout_rejects_overflow() {
    const ViewWindow nv12{true, 0, 0, 65536, 65536};
    assert(!packed_layout_for(nv12, FRAME_FORMAT_NV12).valid);

    const ViewWindow yuv420p{true, 0, 0, 65536, 57344};
    assert(!packed_layout_for(yuv420p, FRAME_FORMAT_YUV420P).valid);
}

void test_full_frame_helper() {
    assert(view_rect_is_full_frame(ViewRect{0.0f, 0.0f, 1.0f, 1.0f}));
    assert(view_rect_is_full_frame(ViewRect{-0.5f, -0.5f, 1.5f, 1.5f}));
    assert(!view_rect_is_full_frame(ViewRect{0.0f, 0.0f, 0.99f, 1.0f}));
}

} // namespace

int main() {
    test_full_frame_is_inactive();
    test_basic_rect();
    test_odd_dimensions_stay_in_bounds();
    test_right_bottom_edge();
    test_minimum_window_clamp();
    test_packed_layout_nv12();
    test_packed_layout_yuv420p_alignment();
    test_packed_layout_rejects_unknown();
    test_packed_layout_rejects_overflow();
    test_full_frame_helper();
    std::cout << "[PASS] view_window_test" << std::endl;
    return 0;
}
