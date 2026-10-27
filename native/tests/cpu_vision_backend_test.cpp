// CPU 视觉前处理后端回归测试（不依赖任何硬件后端）。
//
// 存在理由：`CpuVisionBackend::resize` 的直接覆盖原先只存在于 rga_vision_test.cpp，
// 而那个 target 仅在 ZHULONG_VISION_BACKEND=rga 时构建 —— 也就是 host 的
// `make native-test` 从来没有直接调用过 resize。本文件补齐这条缺口，并把
// 「NV12 奇数宽紧凑 stride / 负 stride / 失败不改写 out 参数」这三类回归固化下来。
//
// ⚠️ 直接实例化 CpuVisionBackend（而不是走 get_default_vision_backend），
//    因为这些用例断言的是 CPU 后端的既定语义，不应随编译配置切换后端而变。

#include <algorithm/runtime/cpu_vision_backend.hpp>
#include <zhulong/runtime/tensor.hpp>
#include <zhulong/runtime/vision_backend.hpp>

#include <cmath>
#include <cstdint>
#include <cstdlib>
#include <iostream>
#include <limits>
#include <vector>

namespace {

void require(bool condition, const char* what) {
    if (!condition) {
        std::cerr << "[FAIL] " << what << '\n';
        std::exit(1);
    }
}

/** @brief 构造 packed 24 位纯色帧 */
std::vector<uint8_t> make_solid(int width, int height, uint8_t r, uint8_t g, uint8_t b) {
    std::vector<uint8_t> buffer(static_cast<size_t>(width) * height * 3);
    for (size_t i = 0; i < buffer.size(); i += 3) {
        buffer[i + 0] = r;
        buffer[i + 1] = g;
        buffer[i + 2] = b;
    }
    return buffer;
}

/** @brief 构造 NV12 帧（紧凑布局），返回各平面 */
struct Nv12Frame {
    std::vector<uint8_t> y;
    std::vector<uint8_t> uv;
};

Nv12Frame make_nv12(int width, int height, uint8_t luma, uint8_t chroma) {
    const int uv_w = (width + 1) / 2;
    const int uv_h = (height + 1) / 2;
    Nv12Frame frame;
    frame.y.assign(static_cast<size_t>(width) * height, luma);
    frame.uv.assign(static_cast<size_t>(uv_w) * 2 * uv_h, chroma);
    return frame;
}

zhulong_frame_view make_nv12_view(const Nv12Frame& frame, int width, int height, int y_stride,
                                  int uv_stride) {
    zhulong_frame_view view{};
    view.width = width;
    view.height = height;
    view.format = ZHULONG_PIXEL_FORMAT_NV12;
    view.planes[0] = frame.y.data();
    view.planes[1] = frame.uv.data();
    view.strides[0] = y_stride;
    view.strides[1] = uv_stride;
    return view;
}

// ── 1. resize：常量色场在双线性缩放后必须恒等 ─────────────────────────────
void test_resize_constant_field() {
    zhulong::CpuVisionBackend backend;
    const std::vector<uint8_t> src = make_solid(2, 2, 10, 20, 30);
    std::vector<uint8_t> dst(static_cast<size_t>(4) * 4 * 3, 0);

    const zhulong_status_t rc =
        backend.resize(src.data(), 2, 2, 0, ZHULONG_PIXEL_FORMAT_RGB24, dst.data(), 0, 4, 4,
                       ZHULONG_PIXEL_FORMAT_RGB24);
    require(rc == ZHULONG_PLUGIN_OK, "resize: constant field ok");
    for (size_t i = 0; i < dst.size(); i += 3) {
        require(dst[i + 0] == 10, "resize: constant field R");
        require(dst[i + 1] == 20, "resize: constant field G");
        require(dst[i + 2] == 30, "resize: constant field B");
    }
}

// ── 2. resize：RGB24 -> BGR24 必须交换 R/B 通道 ────────────────────────────
void test_resize_channel_swap() {
    zhulong::CpuVisionBackend backend;
    const std::vector<uint8_t> src = make_solid(2, 2, 10, 20, 30);
    std::vector<uint8_t> dst(static_cast<size_t>(2) * 2 * 3, 0);

    const zhulong_status_t rc =
        backend.resize(src.data(), 2, 2, 0, ZHULONG_PIXEL_FORMAT_RGB24, dst.data(), 0, 2, 2,
                       ZHULONG_PIXEL_FORMAT_BGR24);
    require(rc == ZHULONG_PLUGIN_OK, "resize: rgb->bgr ok");
    for (size_t i = 0; i < dst.size(); i += 3) {
        require(dst[i + 0] == 30, "resize: bgr swap B");
        require(dst[i + 1] == 20, "resize: bgr swap G");
        require(dst[i + 2] == 10, "resize: bgr swap R");
    }
}

// ── 3. resize：stride 契约（0 = packed；不足必须拒绝） ─────────────────────
void test_resize_stride_contract() {
    zhulong::CpuVisionBackend backend;
    const std::vector<uint8_t> src = make_solid(2, 2, 100, 100, 100);
    std::vector<uint8_t> dst(static_cast<size_t>(2) * 2 * 3, 0);

    // stride==0 等价于 packed
    zhulong_status_t rc =
        backend.resize(src.data(), 2, 2, 0, ZHULONG_PIXEL_FORMAT_RGB24, dst.data(), 0, 2, 2,
                       ZHULONG_PIXEL_FORMAT_RGB24);
    require(rc == ZHULONG_PLUGIN_OK, "resize: stride 0 == packed");

    // 源 stride 不足
    rc = backend.resize(src.data(), 2, 2, 5, ZHULONG_PIXEL_FORMAT_RGB24, dst.data(), 0, 2, 2,
                        ZHULONG_PIXEL_FORMAT_RGB24);
    require(rc == ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT, "resize: short src stride rejected");

    // 源 stride 为负
    rc = backend.resize(src.data(), 2, 2, -1, ZHULONG_PIXEL_FORMAT_RGB24, dst.data(), 0, 2, 2,
                        ZHULONG_PIXEL_FORMAT_RGB24);
    require(rc == ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT, "resize: negative src stride rejected");

    // 目标 stride 不足
    rc = backend.resize(src.data(), 2, 2, 0, ZHULONG_PIXEL_FORMAT_RGB24, dst.data(), 5, 2, 2,
                        ZHULONG_PIXEL_FORMAT_RGB24);
    require(rc == ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT, "resize: short dst stride rejected");
}

// ── 4. resize：未实现格式必须在读源像素前拒绝 ──────────────────────────────
void test_resize_rejects_unsupported_before_reading() {
    zhulong::CpuVisionBackend backend;
    // 只给 width*height*1 字节：若实现先按 24 位读，ASan 构型下会立刻越界报错。
    std::vector<uint8_t> tiny(4, 0xAB);
    std::vector<uint8_t> dst(static_cast<size_t>(2) * 2 * 3, 0);

    const zhulong_status_t rc =
        backend.resize(tiny.data(), 2, 2, 0, ZHULONG_PIXEL_FORMAT_RGBA32, dst.data(), 0, 2, 2,
                       ZHULONG_PIXEL_FORMAT_RGB24);
    require(rc == ZHULONG_PLUGIN_ERR_NOT_SUPPORTED, "resize: RGBA32 -> NOT_SUPPORTED");

    const zhulong_status_t dst_rc =
        backend.resize(tiny.data(), 2, 2, 0, ZHULONG_PIXEL_FORMAT_RGB24, dst.data(), 0, 2, 2,
                       ZHULONG_PIXEL_FORMAT_UNKNOWN);
    require(dst_rc == ZHULONG_PLUGIN_ERR_NOT_SUPPORTED, "resize: UNKNOWN dst -> NOT_SUPPORTED");
}

// ── 5. letterbox：补边值必须是 114 ────────────────────────────────────────
void test_letterbox_pad_value() {
    zhulong::CpuVisionBackend backend;
    const std::vector<uint8_t> src = make_solid(4, 2, 100, 100, 100);
    std::vector<uint8_t> pixels(static_cast<size_t>(4) * 2 * 3);

    zhulong_frame_view view{};
    view.width = 4;
    view.height = 2;
    view.format = ZHULONG_PIXEL_FORMAT_RGB24;
    view.planes[0] = src.data();
    view.strides[0] = 4 * 3;

    zhulong::LetterboxParams params;
    params.target_width = 4;
    params.target_height = 4;
    params.target_format = ZHULONG_PIXEL_FORMAT_RGB24;

    zhulong::Tensor out;
    const zhulong_status_t rc = backend.letterbox(view, out, params);
    require(rc == ZHULONG_PLUGIN_OK, "letterbox: 4x2 -> 4x4 ok");
    require(out.shape() == std::vector<int64_t>({4, 4, 3}), "letterbox: output shape");
    // r = min(4/4, 4/2) = 1 -> unpad 4x2，上下各补 1 行
    require(params.pad_top == 1, "letterbox: pad_top == 1");
    require(params.pad_left == 0, "letterbox: pad_left == 0");
    require(std::fabs(params.scale - 1.0f) < 1e-6f, "letterbox: scale == 1");

    const uint8_t* out_pixels = out.data<uint8_t>();
    auto row_start = [&](int row) { return out_pixels + static_cast<size_t>(row) * 4 * 3; };

    for (int c = 0; c < 4 * 3; ++c) {
        require(row_start(0)[c] == 114, "letterbox: top pad row == 114");
        require(row_start(3)[c] == 114, "letterbox: bottom pad row == 114");
    }
    for (int row = 1; row <= 2; ++row) {
        for (int c = 0; c < 4 * 3; ++c) {
            require(row_start(row)[c] == 100, "letterbox: content row preserved");
        }
    }
}

// ── 6. letterbox：NV12 奇数宽 + 紧凑 stride 必须被接受 ────────────────────
// 回归：色度行宽是 2*ceil(w/2)，不是 w。按 w 回落会把奇数宽紧凑帧误判为 stride 不足。
void test_letterbox_nv12_odd_width_compact() {
    zhulong::CpuVisionBackend backend;
    const int width = 3;
    const int height = 3;
    const Nv12Frame frame = make_nv12(width, height, 150, 128);

    zhulong::Tensor compact_out;
    zhulong_frame_view compact_view = make_nv12_view(frame, width, height, 0, 0);
    zhulong::LetterboxParams compact_params;
    compact_params.target_width = width;
    compact_params.target_height = height;
    const zhulong_status_t compact_rc = backend.letterbox(compact_view, compact_out, compact_params);
    require(compact_rc == ZHULONG_PLUGIN_OK, "letterbox: odd-width NV12 compact accepted");

    // 显式给出真实紧凑 stride（UV 行宽 = 2*ceil(3/2) = 4）必须得到逐字节相同的结果
    zhulong::Tensor explicit_out;
    zhulong_frame_view explicit_view = make_nv12_view(frame, width, height, width, 4);
    zhulong::LetterboxParams explicit_params;
    explicit_params.target_width = width;
    explicit_params.target_height = height;
    const zhulong_status_t explicit_rc = backend.letterbox(explicit_view, explicit_out, explicit_params);
    require(explicit_rc == ZHULONG_PLUGIN_OK, "letterbox: odd-width NV12 explicit stride accepted");
    require(explicit_out.byte_size() == compact_out.byte_size(), "letterbox: same byte size");
    require(std::equal(compact_out.data<uint8_t>(), compact_out.data<uint8_t>() + compact_out.byte_size(),
                       explicit_out.data<uint8_t>()),
            "letterbox: compact stride == explicit chroma stride");
}

// ── 7. letterbox：负 stride 必须被拒绝（不得当成紧凑布局） ─────────────────
// 回归：负 stride 表示底朝上缓冲区；静默回落成 top-down 会产出上下翻转的图且无错误码。
void test_letterbox_rejects_negative_strides() {
    zhulong::CpuVisionBackend backend;
    const Nv12Frame frame = make_nv12(4, 4, 100, 128);

    const int bad_y_stride[] = {-2};
    for (const int y_stride : bad_y_stride) {
        zhulong::Tensor out;
        zhulong_frame_view view = make_nv12_view(frame, 4, 4, y_stride, 0);
        zhulong::LetterboxParams params;
        params.target_width = 4;
        params.target_height = 4;
        const zhulong_status_t rc = backend.letterbox(view, out, params);
        require(rc == ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT, "letterbox: negative luma stride rejected");
    }

    {
        zhulong::Tensor out;
        zhulong_frame_view view = make_nv12_view(frame, 4, 4, 4, -2);
        zhulong::LetterboxParams params;
        params.target_width = 4;
        params.target_height = 4;
        const zhulong_status_t rc = backend.letterbox(view, out, params);
        require(rc == ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT, "letterbox: negative chroma stride rejected");
    }

    {
        // YUV420P 的 V 平面负 stride 同样必须拒绝
        std::vector<uint8_t> y_plane(16, 100);
        std::vector<uint8_t> u_plane(4, 128);
        std::vector<uint8_t> v_plane(4, 128);
        zhulong_frame_view view{};
        view.width = 4;
        view.height = 4;
        view.format = ZHULONG_PIXEL_FORMAT_YUV420P;
        view.planes[0] = y_plane.data();
        view.planes[1] = u_plane.data();
        view.planes[2] = v_plane.data();
        view.strides[0] = 4;
        view.strides[1] = 2;
        view.strides[2] = -2;

        zhulong::Tensor out;
        zhulong::LetterboxParams params;
        params.target_width = 4;
        params.target_height = 4;
        const zhulong_status_t rc = backend.letterbox(view, out, params);
        require(rc == ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT,
                "letterbox: negative YUV420P V stride rejected");
    }
}

// ── 8. letterbox：target 尺寸为 0/负 必须拒绝，不得静默回落 640 ────────────
void test_letterbox_rejects_non_positive_target() {
    zhulong::CpuVisionBackend backend;
    const std::vector<uint8_t> src = make_solid(4, 4, 10, 20, 30);

    for (const int bad : {0, -8}) {
        zhulong_frame_view view{};
        view.width = 4;
        view.height = 4;
        view.format = ZHULONG_PIXEL_FORMAT_RGB24;
        view.planes[0] = src.data();
        view.strides[0] = 4 * 3;

        zhulong::LetterboxParams params;
        params.target_width = bad;
        params.target_height = 4;
        zhulong::Tensor out;
        const zhulong_status_t rc = backend.letterbox(view, out, params);
        require(rc == ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT, "letterbox: target_width <= 0 rejected");
    }
}

// ── 9. letterbox：任何失败路径都不得改写 dst / 几何回填 ────────────────────
void test_letterbox_failure_does_not_clobber_outputs() {
    zhulong::CpuVisionBackend backend;

    // 先成功跑一次，得到可复用的 dst 与已回填的 params
    const std::vector<uint8_t> src = make_solid(4, 4, 77, 88, 99);
    zhulong_frame_view good{};
    good.width = 4;
    good.height = 4;
    good.format = ZHULONG_PIXEL_FORMAT_RGB24;
    good.planes[0] = src.data();
    good.strides[0] = 4 * 3;

    zhulong::LetterboxParams params;
    params.target_width = 4;
    params.target_height = 4;
    zhulong::Tensor out;
    require(backend.letterbox(good, out, params) == ZHULONG_PLUGIN_OK, "letterbox: baseline ok");

    const float baseline_scale = params.scale;
    const int baseline_pad_top = params.pad_top;
    std::vector<uint8_t> baseline(out.byte_size());
    std::copy(out.data<uint8_t>(), out.data<uint8_t>() + out.byte_size(), baseline.begin());

    // 非法输入：负 stride 的 NV12
    const Nv12Frame frame = make_nv12(4, 4, 100, 128);
    zhulong_frame_view bad = make_nv12_view(frame, 4, 4, -1, 0);
    zhulong::LetterboxParams bad_params;
    bad_params.target_width = 4;
    bad_params.target_height = 4;
    // 故意填入会被「成功路径」覆盖的哨兵值
    bad_params.scale = 123.0f;
    bad_params.pad_left = 7;
    bad_params.pad_top = 9;

    const zhulong_status_t rc = backend.letterbox(bad, out, bad_params);
    require(rc == ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT, "letterbox: bad input rejected");
    require(bad_params.scale == 123.0f, "letterbox: failure must not backfill scale");
    require(bad_params.pad_left == 7, "letterbox: failure must not backfill pad_left");
    require(bad_params.pad_top == 9, "letterbox: failure must not backfill pad_top");
    require(std::equal(baseline.begin(), baseline.end(), out.data<uint8_t>()),
            "letterbox: failure must not rewrite dst");

    // 成功路径的几何回填仍然正确（确保上面的断言不是靠「从不回填」通过的）
    require(baseline_scale == params.scale, "letterbox: baseline scale stable");
    require(baseline_pad_top == params.pad_top, "letterbox: baseline pad_top stable");
}

} // namespace

int main() {
    test_resize_constant_field();
    test_resize_channel_swap();
    test_resize_stride_contract();
    test_resize_rejects_unsupported_before_reading();
    test_letterbox_pad_value();
    test_letterbox_nv12_odd_width_compact();
    test_letterbox_rejects_negative_strides();
    test_letterbox_rejects_non_positive_target();
    test_letterbox_failure_does_not_clobber_outputs();
    std::cout << "CPU vision backend regression tests passed!\n";
    return 0;
}
