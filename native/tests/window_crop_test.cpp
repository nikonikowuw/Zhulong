/**
 * @file window_crop_test.cpp
 * @brief 宿主侧 CPU 窗口物化回归：紧打包布局、vstride、独立 plane_strides 与越界拒绝
 */

#include "../src/nodes/preprocess/window_crop.hpp"

#include <cassert>
#include <cstdint>
#include <cstring>
#include <iostream>
#include <vector>

using namespace zhulong;

namespace {

constexpr int kWidth = 320;
constexpr int kHeight = 240;
constexpr int kViewX = 64;
constexpr int kViewY = 32;
constexpr int kViewW = 128;
constexpr int kViewH = 64;

int align_up(int value, int alignment) { return ((value + alignment - 1) / alignment) * alignment; }

uint8_t pattern(int row, int col) { return static_cast<uint8_t>((row * 31 + col * 17 + 7) & 0xFF); }

/** @brief 单块连续 NV12 源帧（含 vstride > height）。 */
struct Nv12Source {
    std::vector<uint8_t> buffer;
    HardwareFrame frame;

    Nv12Source(int width, int height, int stride, int vstride) {
        buffer.assign(static_cast<size_t>(stride) * vstride +
                          static_cast<size_t>(stride) * ((height + 1) / 2),
                      0);
        frame.width = static_cast<uint32_t>(width);
        frame.height = static_cast<uint32_t>(height);
        frame.stride = static_cast<uint32_t>(stride);
        frame.vstride = static_cast<uint32_t>(vstride);
        frame.format = FRAME_FORMAT_NV12;
        frame.host_ptr = buffer.data();

        for (int r = 0; r < vstride; ++r) {
            for (int c = 0; c < stride; ++c) {
                buffer[static_cast<size_t>(r) * stride + c] = pattern(r, c);
            }
        }
        uint8_t* uv = buffer.data() + static_cast<size_t>(stride) * vstride;
        for (int r = 0; r < (height + 1) / 2; ++r) {
            for (int c = 0; c < stride; ++c) {
                uv[static_cast<size_t>(r) * stride + c] = pattern(r + 100, c);
            }
        }
    }
};

void test_nv12_window_with_vstride() {
    const int stride = align_up(kWidth, 64);
    const int vstride = kHeight + 16;
    Nv12Source src(kWidth, kHeight, stride, vstride);

    const ViewWindow view{true, kViewX, kViewY, kViewW, kViewH};
    const PackedLayout layout = packed_layout_for(view, FRAME_FORMAT_NV12);
    assert(layout.valid);

    std::vector<uint8_t> dst(layout.bytes, 0xEE);
    WindowCrop crop;
    assert(crop.copy(src.frame, view, dst.data(), dst.size(), static_cast<int>(layout.stride), -1));
    assert(crop.cpu_count() == 1);

    for (int r = 0; r < kViewH; ++r) {
        for (int c = 0; c < kViewW; ++c) {
            const uint8_t expected = src.buffer[static_cast<size_t>(kViewY + r) * stride + kViewX + c];
            const uint8_t actual = dst[layout.offsets[0] + static_cast<size_t>(r) * layout.stride + c];
            assert(actual == expected);
        }
    }

    const uint8_t* src_uv_base =
        src.buffer.data() + static_cast<size_t>(stride) * vstride;
    for (int r = 0; r < kViewH / 2; ++r) {
        for (int c = 0; c < kViewW; ++c) {
            const uint8_t expected =
                src_uv_base[static_cast<size_t>(kViewY / 2 + r) * stride + kViewX + c];
            const uint8_t actual =
                dst[layout.offsets[1] + static_cast<size_t>(r) * layout.stride + c];
            assert(actual == expected);
        }
    }
}

/** @brief YUV420P 三平面独立缓冲 + 独立行跨度。 */
void test_yuv420p_independent_plane_strides() {
    const int y_stride = align_up(kWidth + 7, 32);
    const int c_stride = align_up(kWidth / 2 + 5, 32);
    std::vector<uint8_t> y(static_cast<size_t>(y_stride) * kHeight);
    std::vector<uint8_t> u(static_cast<size_t>(c_stride) * (kHeight / 2));
    std::vector<uint8_t> v(static_cast<size_t>(c_stride) * (kHeight / 2));
    for (size_t i = 0; i < y.size(); ++i) y[i] = static_cast<uint8_t>(i & 0xFF);
    for (size_t i = 0; i < u.size(); ++i) u[i] = static_cast<uint8_t>((i + 11) & 0xFF);
    for (size_t i = 0; i < v.size(); ++i) v[i] = static_cast<uint8_t>((i + 23) & 0xFF);

    HardwareFrame frame;
    frame.width = kWidth;
    frame.height = kHeight;
    frame.stride = static_cast<uint32_t>(y_stride);
    frame.format = FRAME_FORMAT_YUV420P;
    frame.host_planes[0] = y.data();
    frame.host_planes[1] = u.data();
    frame.host_planes[2] = v.data();
    frame.plane_strides[0] = static_cast<uint32_t>(y_stride);
    frame.plane_strides[1] = static_cast<uint32_t>(c_stride);
    frame.plane_strides[2] = static_cast<uint32_t>(c_stride);

    const ViewWindow view{true, kViewX, kViewY, kViewW, kViewH};
    const PackedLayout layout = packed_layout_for(view, FRAME_FORMAT_YUV420P);
    assert(layout.valid);

    std::vector<uint8_t> dst(layout.bytes, 0xEE);
    WindowCrop crop;
    assert(crop.copy(frame, view, dst.data(), dst.size(), static_cast<int>(layout.stride), -1));

    for (int r = 0; r < kViewH; ++r) {
        for (int c = 0; c < kViewW; ++c) {
            assert(dst[static_cast<size_t>(r) * layout.stride + c] ==
                   y[static_cast<size_t>(kViewY + r) * y_stride + kViewX + c]);
        }
    }
    const int chroma_rows = kViewH / 2;
    const int chroma_cols = kViewW / 2;
    for (int r = 0; r < chroma_rows; ++r) {
        for (int c = 0; c < chroma_cols; ++c) {
            const uint8_t got_u =
                dst[layout.offsets[1] + static_cast<size_t>(r) * layout.strides[1] + c];
            const uint8_t got_v =
                dst[layout.offsets[2] + static_cast<size_t>(r) * layout.strides[2] + c];
            assert(got_u == u[static_cast<size_t>(kViewY / 2 + r) * c_stride + kViewX / 2 + c]);
            assert(got_v == v[static_cast<size_t>(kViewY / 2 + r) * c_stride + kViewX / 2 + c]);
        }
    }
}

void test_rejects_invalid_inputs() {
    Nv12Source src(kWidth, kHeight, align_up(kWidth, 64), kHeight);
    WindowCrop crop;
    const PackedLayout layout =
        packed_layout_for(ViewWindow{true, 0, 0, 64, 64}, FRAME_FORMAT_NV12);
    std::vector<uint8_t> dst(layout.bytes, 0);

    // 越界窗口
    assert(!crop.copy(src.frame, ViewWindow{true, kWidth - 32, 0, 64, 64}, dst.data(), dst.size(),
                      static_cast<int>(layout.stride), -1));
    // 负起点
    assert(!crop.copy(src.frame, ViewWindow{true, -2, 0, 64, 64}, dst.data(), dst.size(),
                      static_cast<int>(layout.stride), -1));
    // 奇数几何
    assert(!crop.copy(src.frame, ViewWindow{true, 0, 0, 63, 64}, dst.data(), dst.size(),
                      static_cast<int>(layout.stride), -1));
    // 目标缓冲不足
    assert(!crop.copy(src.frame, ViewWindow{true, 0, 0, 64, 64}, dst.data(), dst.size() - 1,
                      static_cast<int>(layout.stride), -1));
    // 目标跨度与紧打包布局不匹配（偏小或偏大）
    assert(!crop.copy(src.frame, ViewWindow{true, 0, 0, 64, 64}, dst.data(), dst.size(),
                      static_cast<int>(layout.stride) + 16, -1));
    assert(!crop.copy(src.frame, ViewWindow{true, 0, 0, 64, 64}, dst.data(), dst.size(),
                      static_cast<int>(layout.stride) - 1, -1));
    // 空目标
    assert(!crop.copy(src.frame, ViewWindow{true, 0, 0, 64, 64}, nullptr, dst.size(),
                      static_cast<int>(layout.stride), -1));

    HardwareFrame unknown;
    unknown.width = kWidth;
    unknown.height = kHeight;
    unknown.stride = kWidth;
    unknown.format = FRAME_FORMAT_UNKNOWN;
    unknown.host_ptr = src.buffer.data();
    assert(!crop.copy(unknown, ViewWindow{true, 0, 0, 64, 64}, dst.data(), dst.size(),
                      static_cast<int>(layout.stride), -1));

    // 源既无 host_planes/host_ptr 也无 dmabuf
    HardwareFrame empty;
    empty.width = kWidth;
    empty.height = kHeight;
    empty.stride = kWidth;
    empty.format = FRAME_FORMAT_NV12;
    assert(!crop.copy(empty, ViewWindow{true, 0, 0, 64, 64}, dst.data(), dst.size(),
                      static_cast<int>(layout.stride), -1));

    // vstride 非零但小于 height：必须拒绝，防止 y_plane_size 低估映射范围
    HardwareFrame bad_vstride;
    bad_vstride.width = kWidth;
    bad_vstride.height = kHeight;
    bad_vstride.stride = src.frame.stride;
    bad_vstride.vstride = kHeight - 2;
    bad_vstride.format = FRAME_FORMAT_NV12;
    bad_vstride.host_ptr = src.buffer.data();
    assert(!crop.copy(bad_vstride, ViewWindow{true, 0, 0, 64, 64}, dst.data(), dst.size(),
                      static_cast<int>(layout.stride), -1));

    // 连续缓冲帧（host_ptr）显式提供了非法 plane_strides（小于最小行字节数）：必须拒绝
    HardwareFrame bad_plane_stride;
    bad_plane_stride.width = kWidth;
    bad_plane_stride.height = kHeight;
    bad_plane_stride.stride = src.frame.stride;
    bad_plane_stride.vstride = src.frame.vstride;
    bad_plane_stride.format = FRAME_FORMAT_NV12;
    bad_plane_stride.host_ptr = src.buffer.data();
    bad_plane_stride.plane_strides[0] = kWidth - 2;
    assert(!crop.copy(bad_plane_stride, ViewWindow{true, 0, 0, 64, 64}, dst.data(), dst.size(),
                      static_cast<int>(layout.stride), -1));
}

} // namespace

int main() {
    test_nv12_window_with_vstride();
    test_yuv420p_independent_plane_strides();
    test_rejects_invalid_inputs();
    std::cout << "[PASS] window_crop_test" << std::endl;
    return 0;
}
