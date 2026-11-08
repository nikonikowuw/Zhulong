/**
 * @file aligner_matcher_test.cpp
 * @brief 包内自测：人脸几何对齐（Umeyama）与 1:N 比对（Matcher）
 *
 * 归属约定（见 `.trellis/spec/native/algo-plugin-contract.md` §5.5）：
 *   - **包内自测**：不需要模型与会话的纯逻辑 → 留在包内 `tests/`，由包自己的
 *     `make unit-test` 驱动。第三方拿到「SDK 前缀 + 包目录」就能跑，
 *     `make sdk-clean-check` 的干净容器也覆盖这一段。
 *   - **金标回归**：需要真实权重与金标样本的准确率断言 → `native/tests/`
 *     （那里才有 fixtures 与金标期望值），不在这里重复。
 *
 * 为什么这两块必须钉住：
 *   - `estimate_similarity_transform` 曾把缩放系数的交叉项符号写反（`- s01 * (-sin)`
 *     而不是 `- s01 * sin`），带滚转的人脸被缩放到比模板小若干个百分点、5 点残差
 *     约 1.9px，而整条流水线照常出结果 —— 只有「已知变换的逆算」能钉住它。
 *   - `Matcher::match` 只保留 (下标, 相似度) 再部分排序，候选人字段按排序后的下标
 *     回查底库，因此必须有断言保证 Top-K 与底库条目一一对应。
 *
 * ⚠️ 刻意不用 `assert()`：RelWithDebInfo 会定义 NDEBUG 把断言整段编译掉
 *    （见 native/CMakeLists.txt 中 -UNDEBUG 的说明）。这里用带上下文的显式检查，
 *    失败时既打印用例名又返回非零退出码。
 */

#include "alignment/aligner.hpp"
#include "common/face_association.hpp"
#include "common/face_feature_fusion.hpp"
#include "common/face_image_quality.hpp"
#include "common/face_quality.hpp"
#include "common/scene_filter.hpp"
#include "detection/detector.hpp"
#include "detection/scrfd_decode.hpp"
#include "matching/matcher.hpp"

// HWC→NCHW 归一化只存在于「模型不自带归一化」的档位（ONNX）；RKNN 档位由模型自身
// 完成归一化，因此没有这个头。按文件是否存在决定测不测，而不是按 SDK 后端宏 ——
// 宏取决于你链接的 SDK 前缀，跟包自身的档位无关。
#if __has_include("common/face_preprocess.hpp")
#include "common/face_preprocess.hpp"
#define ZHULONG_FACE_HAS_PREPROCESS_HELPER 1
#endif

#include <algorithm>
#include <array>
#include <cmath>
#include <cstddef>
#include <cstdint>
#include <cstdio>
#include <limits>
#include <string>
#include <vector>

namespace {

int g_failures = 0;

void check(bool condition, const std::string& what) {
    if (!condition) {
        std::fprintf(stderr, "FAIL: %s\n", what.c_str());
        ++g_failures;
    }
}

bool nearly_equal(float a, float b, float tolerance) {
    return std::fabs(a - b) <= tolerance;
}

/** @brief 按 `dst = scale·R(angle)·src + t` 变换一组点 */
std::array<zhulong::face::Point2f, 5> apply_transform(
    const std::array<zhulong::face::Point2f, 5>& points,
    float angle_deg, float scale, float tx, float ty) {
    const float radians = angle_deg * 3.14159265358979f / 180.0f;
    const float cos_t = std::cos(radians);
    const float sin_t = std::sin(radians);
    std::array<zhulong::face::Point2f, 5> out{};
    for (size_t i = 0; i < points.size(); ++i) {
        out[i].x = scale * (cos_t * points[i].x - sin_t * points[i].y) + tx;
        out[i].y = scale * (sin_t * points[i].x + cos_t * points[i].y) + ty;
    }
    return out;
}

// ============================================================================
// 1. 相似变换：已知变换的逆算
// ============================================================================

void test_similarity_transform_recovers_known_geometry() {
    struct TransformCase {
        const char* name;
        float angle_deg;
        float scale;
        float tx;
        float ty;
    };
    const TransformCase cases[] = {
        {"identity", 0.0f, 1.0f, 0.0f, 0.0f},
        {"roll+5", 5.0f, 1.0f, 0.0f, 0.0f},
        {"roll+20-scale1.7", 20.0f, 1.7f, -40.0f, 12.0f},
        {"roll-12-scale0.6", -12.0f, 0.6f, 33.0f, -7.0f},
        {"roll+90-scale2.5", 90.0f, 2.5f, 100.0f, -60.0f},
        {"roll+180-scale1.2", 180.0f, 1.2f, -8.0f, 4.0f},
    };

    for (const auto& test_case : cases) {
        // 参考模板经已知相似变换 ⇒ 反算出的矩阵必须把它变回来
        const auto source = apply_transform(zhulong::face::kArcfaceRefPoints, test_case.angle_deg,
                                            test_case.scale, test_case.tx, test_case.ty);

        std::array<float, 6> matrix{};
        const bool solved = zhulong::face::Aligner::estimate_similarity_transform(source, matrix);
        check(solved, std::string("estimate_similarity_transform 应成功: ") + test_case.name);
        if (!solved) {
            continue;
        }

        // (a) 5 点残差：正确的矩阵应当把 source 精确映射回参考模板
        float max_residual = 0.0f;
        for (size_t i = 0; i < source.size(); ++i) {
            const float mapped_x = matrix[0] * source[i].x + matrix[1] * source[i].y + matrix[2];
            const float mapped_y = matrix[3] * source[i].x + matrix[4] * source[i].y + matrix[5];
            max_residual = std::max(max_residual,
                                    std::hypot(mapped_x - zhulong::face::kArcfaceRefPoints[i].x,
                                               mapped_y - zhulong::face::kArcfaceRefPoints[i].y));
        }
        check(max_residual < 0.01f,
              std::string("5 点重投影残差应 < 0.01px，实际 ") + std::to_string(max_residual) + ": " +
                  test_case.name);

        // (b) 缩放系数：矩阵的 scale = 已知变换的倒数
        const float recovered_scale = std::hypot(matrix[0], matrix[3]);
        const float expected_scale = 1.0f / test_case.scale;
        check(nearly_equal(recovered_scale, expected_scale, 1e-3f),
              std::string("缩放系数应为 ") + std::to_string(expected_scale) + "，实际 " +
                  std::to_string(recovered_scale) + ": " + test_case.name);

        // (c) 旋转角：矩阵的旋转应等于已知角度的相反数
        const float recovered_angle_deg = std::atan2(matrix[3], matrix[0]) * 180.0f / 3.14159265358979f;
        float expected_angle_deg = -test_case.angle_deg;
        while (expected_angle_deg <= -180.0f) expected_angle_deg += 360.0f;
        while (expected_angle_deg > 180.0f) expected_angle_deg -= 360.0f;
        check(nearly_equal(recovered_angle_deg, expected_angle_deg, 1e-2f),
              std::string("旋转角应为 ") + std::to_string(expected_angle_deg) + "°，实际 " +
                  std::to_string(recovered_angle_deg) + ": " + test_case.name);
    }
}

void test_similarity_transform_rejects_degenerate_input() {
    const std::array<zhulong::face::Point2f, 5> all_same{{
        {50.0f, 50.0f}, {50.0f, 50.0f}, {50.0f, 50.0f}, {50.0f, 50.0f}, {50.0f, 50.0f},
    }};
    std::array<float, 6> matrix{};
    check(!zhulong::face::Aligner::estimate_similarity_transform(all_same, matrix),
          "5 点完全重合必须判为退化");

    std::array<zhulong::face::Point2f, 5> with_nan = zhulong::face::kArcfaceRefPoints;
    with_nan[2].x = std::nanf("");
    check(!zhulong::face::Aligner::estimate_similarity_transform(with_nan, matrix),
          "含 NaN 的关键点必须失败（NaN 会一路污染特征向量）");

    std::array<zhulong::face::Point2f, 5> with_inf = zhulong::face::kArcfaceRefPoints;
    with_inf[0].y = INFINITY;
    check(!zhulong::face::Aligner::estimate_similarity_transform(with_inf, matrix),
          "含 Inf 的关键点必须失败");
}

// ============================================================================
// 2. 裁剪对齐：通道顺序、格式白名单、越界与非法输入
// ============================================================================

/** @brief 构造 packed 帧视图（RGB/BGR 共用） */
zhulong_frame_view make_packed_view(uint8_t* pixels, int width, int height, int stride,
                                    zhulong_pixel_format_t format) {
    zhulong_frame_view view{};
    view.width = width;
    view.height = height;
    view.format = format;
    view.planes[0] = pixels;
    view.strides[0] = stride;
    view.dma_buf_fd = -1;
    return view;
}

void test_crop_and_align_identity_preserves_pixels() {
    constexpr int kSize = 4;
    std::vector<uint8_t> pixels(static_cast<size_t>(kSize) * kSize * 3);
    for (int y = 0; y < kSize; ++y) {
        for (int x = 0; x < kSize; ++x) {
            const size_t offset = (static_cast<size_t>(y) * kSize + x) * 3;
            pixels[offset + 0] = static_cast<uint8_t>(10 + x * 20); // R
            pixels[offset + 1] = static_cast<uint8_t>(50 + y * 20); // G
            pixels[offset + 2] = static_cast<uint8_t>(200 - x);     // B
        }
    }

    const std::array<float, 6> identity{1.0f, 0.0f, 0.0f, 0.0f, 1.0f, 0.0f};

    // RGB24：恒等矩阵 ⇒ 逐像素原样搬运
    zhulong_frame_view rgb_view = make_packed_view(pixels.data(), kSize, kSize, kSize * 3,
                                                   ZHULONG_PIXEL_FORMAT_RGB24);
    const zhulong::Image rgb_image(rgb_view);
    std::vector<uint8_t> rgb_out(static_cast<size_t>(kSize) * kSize * 3, 0);
    check(zhulong::face::Aligner::crop_and_align_direct(rgb_image, identity, rgb_out.data(), kSize, kSize),
          "恒等矩阵的 RGB24 裁剪应成功");
    check(rgb_out == pixels, "恒等矩阵的 RGB24 裁剪应逐像素一致");

    // BGR24：同样的内存，输出必须是交换过的 R/B
    zhulong_frame_view bgr_view = make_packed_view(pixels.data(), kSize, kSize, kSize * 3,
                                                   ZHULONG_PIXEL_FORMAT_BGR24);
    const zhulong::Image bgr_image(bgr_view);
    std::vector<uint8_t> bgr_out(static_cast<size_t>(kSize) * kSize * 3, 0);
    check(zhulong::face::Aligner::crop_and_align_direct(bgr_image, identity, bgr_out.data(), kSize, kSize),
          "恒等矩阵的 BGR24 裁剪应成功");
    bool channels_swapped = true;
    for (size_t i = 0; i < pixels.size(); i += 3) {
        if (bgr_out[i + 0] != pixels[i + 2] || bgr_out[i + 1] != pixels[i + 1] || bgr_out[i + 2] != pixels[i + 0]) {
            channels_swapped = false;
            break;
        }
    }
    check(channels_swapped, "BGR24 输入必须交换成 RGB24 输出");
}

void test_crop_and_align_handles_yuv_gray() {
    constexpr int kSize = 4;
    constexpr int kUvSize = 2;
    const std::array<float, 6> identity{1.0f, 0.0f, 0.0f, 0.0f, 1.0f, 0.0f};

    // Y=128/U=V=128 ⇒ 中灰：任何插值实现都应给出近似相等的三通道
    std::vector<uint8_t> y_plane(static_cast<size_t>(kSize) * kSize, 128);
    std::vector<uint8_t> u_plane(static_cast<size_t>(kUvSize) * kUvSize, 128);
    std::vector<uint8_t> v_plane(static_cast<size_t>(kUvSize) * kUvSize, 128);

    zhulong_frame_view yuv_view{};
    yuv_view.width = kSize;
    yuv_view.height = kSize;
    yuv_view.format = ZHULONG_PIXEL_FORMAT_YUV420P;
    yuv_view.planes[0] = y_plane.data();
    yuv_view.planes[1] = u_plane.data();
    yuv_view.planes[2] = v_plane.data();
    yuv_view.strides[0] = kSize;
    yuv_view.strides[1] = kUvSize;
    yuv_view.strides[2] = kUvSize;
    yuv_view.dma_buf_fd = -1;

    const zhulong::Image yuv_image(yuv_view);
    std::vector<uint8_t> yuv_out(static_cast<size_t>(kSize) * kSize * 3, 0);
    check(zhulong::face::Aligner::crop_and_align_direct(yuv_image, identity, yuv_out.data(), kSize, kSize),
          "YUV420P 裁剪应成功");
    bool gray = true;
    for (size_t i = 0; i + 2 < yuv_out.size(); i += 3) {
        if (std::abs(static_cast<int>(yuv_out[i]) - 128) > 2 ||
            std::abs(static_cast<int>(yuv_out[i + 1]) - 128) > 2 ||
            std::abs(static_cast<int>(yuv_out[i + 2]) - 128) > 2) {
            gray = false;
            break;
        }
    }
    check(gray, "YUV420P 中灰输入应还原为近似中灰 RGB");

    // NV12 交错 UV 平面
    std::vector<uint8_t> uv_plane(static_cast<size_t>(kUvSize) * kUvSize * 2, 128);
    zhulong_frame_view nv12_view = yuv_view;
    nv12_view.format = ZHULONG_PIXEL_FORMAT_NV12;
    nv12_view.planes[1] = uv_plane.data();
    nv12_view.planes[2] = nullptr;
    nv12_view.strides[1] = kUvSize * 2;
    nv12_view.strides[2] = 0;
    const zhulong::Image nv12_image(nv12_view);
    std::vector<uint8_t> nv12_out(static_cast<size_t>(kSize) * kSize * 3, 0);
    check(zhulong::face::Aligner::crop_and_align_direct(nv12_image, identity, nv12_out.data(), kSize, kSize),
          "NV12 裁剪应成功");

    // NV12 奇数宽（w=5 时 uv_w=3，UV 自然行宽 6 字节）且 strides[1]=0 必须正确以 2*uv_w 回退
    constexpr int kOddW = 5;
    constexpr int kOddH = 5;
    constexpr int kOddUvW = (kOddW + 1) / 2;
    constexpr int kOddUvH = (kOddH + 1) / 2;
    std::vector<uint8_t> odd_y(kOddW * kOddH, 128);
    std::vector<uint8_t> odd_uv(kOddUvW * kOddUvH * 2, 128);
    zhulong_frame_view odd_nv12_view{};
    odd_nv12_view.width = kOddW;
    odd_nv12_view.height = kOddH;
    odd_nv12_view.format = ZHULONG_PIXEL_FORMAT_NV12;
    odd_nv12_view.planes[0] = odd_y.data();
    odd_nv12_view.planes[1] = odd_uv.data();
    odd_nv12_view.strides[0] = 0;
    odd_nv12_view.strides[1] = 0;
    const zhulong::Image odd_nv12_image(odd_nv12_view);
    std::vector<uint8_t> odd_nv12_out(static_cast<size_t>(kOddW) * kOddH * 3, 0);
    check(zhulong::face::Aligner::crop_and_align_direct(odd_nv12_image, identity, odd_nv12_out.data(), kOddW, kOddH),
          "奇数宽且 zero stride 的 NV12 裁剪应成功");
}

void test_crop_and_align_rejects_bad_input() {
    constexpr int kSize = 4;
    std::vector<uint8_t> pixels(static_cast<size_t>(kSize) * kSize * 3, 128);
    std::vector<uint8_t> out(static_cast<size_t>(kSize) * kSize * 3, 0);
    const std::array<float, 6> identity{1.0f, 0.0f, 0.0f, 0.0f, 1.0f, 0.0f};
    const std::array<float, 6> singular{1.0f, 2.0f, 0.0f, 2.0f, 4.0f, 0.0f};
    const std::array<float, 6> nan_matrix{std::nanf(""), 0.0f, 0.0f, 0.0f, 1.0f, 0.0f};

    zhulong_frame_view view = make_packed_view(pixels.data(), kSize, kSize, kSize * 3, ZHULONG_PIXEL_FORMAT_RGB24);
    const zhulong::Image image(view);

    check(!zhulong::face::Aligner::crop_and_align_direct(image, identity, nullptr, kSize, kSize),
          "空目标缓冲必须失败");
    check(!zhulong::face::Aligner::crop_and_align_direct(image, identity, out.data(), 0, kSize),
          "零宽目标必须失败");
    check(!zhulong::face::Aligner::crop_and_align_direct(image, singular, out.data(), kSize, kSize),
          "奇异矩阵必须失败");
    check(!zhulong::face::Aligner::crop_and_align_direct(image, nan_matrix, out.data(), kSize, kSize),
          "含 NaN 的矩阵必须失败");

    zhulong_frame_view negative_stride = view;
    negative_stride.strides[0] = -kSize * 3;
    const zhulong::Image negative_stride_image(negative_stride);
    check(!zhulong::face::Aligner::crop_and_align_direct(negative_stride_image, identity, out.data(), kSize, kSize),
          "负 stride 必须失败（静默正向读取会采到别人的内存）");

    zhulong_frame_view rgba = view;
    rgba.format = ZHULONG_PIXEL_FORMAT_RGBA32;
    const zhulong::Image rgba_image(rgba);
    check(!zhulong::face::Aligner::crop_and_align_direct(rgba_image, identity, out.data(), kSize, kSize),
          "RGBA32 必须显式失败，不能退化成按 3 通道读");

    zhulong_frame_view unknown = view;
    unknown.format = ZHULONG_PIXEL_FORMAT_UNKNOWN;
    const zhulong::Image unknown_image(unknown);
    check(!zhulong::face::Aligner::crop_and_align_direct(unknown_image, identity, out.data(), kSize, kSize),
          "未知像素格式必须失败");

    zhulong_frame_view no_plane = view;
    no_plane.planes[0] = nullptr;
    const zhulong::Image no_plane_image(no_plane);
    check(!zhulong::face::Aligner::crop_and_align_direct(no_plane_image, identity, out.data(), kSize, kSize),
          "空像素平面必须失败");

    // YUV420P 缺少 U/V 平面必须显式失败
    zhulong_frame_view yuv_missing_plane{};
    yuv_missing_plane.width = kSize;
    yuv_missing_plane.height = kSize;
    yuv_missing_plane.format = ZHULONG_PIXEL_FORMAT_YUV420P;
    yuv_missing_plane.planes[0] = pixels.data();
    yuv_missing_plane.planes[1] = nullptr;
    yuv_missing_plane.planes[2] = nullptr;
    yuv_missing_plane.strides[0] = kSize;
    const zhulong::Image yuv_missing_image(yuv_missing_plane);
    check(!zhulong::face::Aligner::crop_and_align_direct(yuv_missing_image, identity, out.data(), kSize, kSize),
          "YUV420P 缺少 U/V 平面必须显式失败");

    // NV12 缺少 UV 平面必须显式失败
    zhulong_frame_view nv12_missing_plane = yuv_missing_plane;
    nv12_missing_plane.format = ZHULONG_PIXEL_FORMAT_NV12;
    const zhulong::Image nv12_missing_image(nv12_missing_plane);
    check(!zhulong::face::Aligner::crop_and_align_direct(nv12_missing_image, identity, out.data(), kSize, kSize),
          "NV12 缺少 UV 平面必须显式失败");

    // 正 stride 小于有效宽度的非法情况必须失败
    zhulong_frame_view bad_stride = view;
    bad_stride.strides[0] = kSize * 3 - 1;
    const zhulong::Image bad_stride_image(bad_stride);
    check(!zhulong::face::Aligner::crop_and_align_direct(bad_stride_image, identity, out.data(), kSize, kSize),
          "Packed 格式 stride 小于 width*3 必须失败");

    // 显式传入目标容量过小时必须失败（防缓冲区溢出）
    check(!zhulong::face::Aligner::crop_and_align_direct(image, identity, out.data(), out.size() - 1, kSize, kSize),
          "目标缓冲容量不足时必须显式失败");
}

// ============================================================================
// 3. 1:N 比对：Top-K 与下标回查、阈值边界、退化输入
// ============================================================================

zhulong::FaceTemplate make_template(const std::string& face_id, const std::string& person_id,
                                    std::vector<float> features) {
    zhulong::FaceTemplate item;
    item.face_id = face_id;
    item.person_id = person_id;
    item.person_name = person_id + "-name";
    item.features = std::move(features);
    return item;
}

void test_matcher_returns_expected_top_k() {
    using namespace zhulong::face;

    std::vector<zhulong::FaceTemplate> gallery;
    gallery.push_back(make_template("f1", "p1", {1.0f, 0.0f, 0.0f}));
    gallery.push_back(make_template("f2", "p2", {0.6f, 0.8f, 0.0f}));
    gallery.push_back(make_template("f3", "p3", {0.0f, 1.0f, 0.0f}));
    gallery.push_back(make_template("f4", "p4", {-1.0f, 0.0f, 0.0f}));
    gallery.push_back(make_template("f5", "p5", {0.0f, 0.0f, 1.0f}));

    const float probe[] = {1.0f, 0.0f, 0.0f};
    const zhulong::face::Matcher matcher;
    const zhulong::face::MatchResult result =
        matcher.match(probe, 3, gallery,
                      MatchLimits{zhulong::face::kDefaultMatchThreshold, zhulong::face::kDefaultMatchTopK});

    check(result.matched, "相似度 1.0 必须命中");
    check(nearly_equal(result.similarity, 1.0f, 1e-6f), "Top-1 相似度应为 1.0");
    check(result.person_id == "p1", "Top-1 person_id 应来自相似度最高的底库条目");
    check(result.matched_face_id == "f1", "Top-1 matched_face_id 应为 f1");
    check(result.candidates.size() == zhulong::face::kDefaultMatchTopK, "候选人数量应等于 top_k");

    // 降序 + 下标回查：默认 top_k=3 ⇒ f1(1.0) > f2(0.6) > f3(0.0 或 f5 的 0.0)
    check(!result.candidates.empty() && result.candidates[0].face_id == "f1", "候选人第 1 名应为 f1");
    check(result.candidates.size() > 1 && result.candidates[1].face_id == "f2", "候选人第 2 名应为 f2");
    bool descending = true;
    for (size_t i = 1; i < result.candidates.size(); ++i) {
        if (result.candidates[i].similarity > result.candidates[i - 1].similarity) {
            descending = false;
        }
    }
    check(descending, "候选人必须按相似度降序");
    for (const auto& candidate : result.candidates) {
        check(candidate.person_name == candidate.person_id + "-name",
              "候选人姓名必须回查到底库同一条目（排序下标不得错位）");
    }
}

void test_matcher_threshold_boundary_and_degenerate_input() {
    using namespace zhulong::face;

    std::vector<zhulong::FaceTemplate> gallery;
    gallery.push_back(make_template("f1", "p1", {0.6f, 0.8f, 0.0f}));

    // 阈值与 top_k 逐次传入（Matcher 无状态）：同一路流的不同任务可以有不同阈值，
    // 存进共享成员会在多路并发时互相覆盖。
    const float probe[] = {1.0f, 0.0f, 0.0f};
    const zhulong::face::Matcher matcher;
    check(!matcher.match(probe, 3, gallery, MatchLimits{0.61f, 1}).matched, "相似度低于阈值不得命中");
    check(matcher.match(probe, 3, gallery, MatchLimits{0.6f, 1}).matched, "相似度等于阈值应命中（>= 语义）");

    // 空底库
    const zhulong::face::MatchResult empty_gallery =
        matcher.match(probe, 3, std::vector<zhulong::FaceTemplate>{}, MatchLimits{0.6f, 1});
    check(!empty_gallery.matched && empty_gallery.candidates.empty(), "空底库必须返回未命中且无候选人");

    // 探针非法
    check(!matcher.match(nullptr, 3, gallery, MatchLimits{0.6f, 1}).matched, "空探针必须返回未命中");
    check(!matcher.match(probe, 0, gallery, MatchLimits{0.6f, 1}).matched, "零维探针必须返回未命中");

    // 维度不符的底库条目被跳过
    std::vector<zhulong::FaceTemplate> mixed;
    mixed.push_back(make_template("bad-dim", "p0", {1.0f, 0.0f}));
    mixed.push_back(make_template("f1", "p1", {0.6f, 0.8f, 0.0f}));
    const zhulong::face::MatchResult mixed_result = matcher.match(probe, 3, mixed, MatchLimits{0.6f, 1});
    check(mixed_result.matched_face_id == "f1", "维度不符的底库条目必须被跳过");
    check(mixed_result.candidates.size() == 1, "维度不符的条目不计入候选人");

    // 反平行向量：余弦为 -1，截断到 0 ⇒ 未命中
    std::vector<zhulong::FaceTemplate> opposite;
    opposite.push_back(make_template("f-neg", "p-neg", {-1.0f, 0.0f, 0.0f}));
    const zhulong::face::MatchResult negative = matcher.match(probe, 3, opposite, MatchLimits{0.6f, 1});
    check(nearly_equal(negative.similarity, 0.0f, 1e-6f), "负相关相似度必须截断为 0");
    check(!negative.matched, "截断为 0 后不得命中");

    // top_k 大于底库规模 ⇒ 返回全部
    check(matcher.match(probe, 3, gallery, MatchLimits{0.4f, 100}).candidates.size() == gallery.size(),
          "top_k 超过底库规模时应返回全部条目");

    // top_k == 0 时：候选人列表必须为空，但 Top-1 决策仍能正确取出全局最高分（而不是未排序的第一项）
    std::vector<zhulong::FaceTemplate> multi_gallery;
    multi_gallery.push_back(make_template("f_low", "p_low", {0.1f, 0.994987f, 0.0f}));
    multi_gallery.push_back(make_template("f_high", "p_high", {0.95f, 0.31225f, 0.0f}));
    const zhulong::face::MatchResult zero_top_k = matcher.match(probe, 3, multi_gallery,
                                                                MatchLimits{0.5f, 0});
    check(zero_top_k.candidates.empty(), "top_k=0 时候选人列表必须为空");
    check(zero_top_k.matched, "top_k=0 时若最高分达标仍应命中 Top-1");
    check(zero_top_k.matched_face_id == "f_high", "top_k=0 时 Top-1 必须取到真实的最高分条目");
}

// ============================================================================
// 4b. 比对参数解析：任务级 JSON 覆盖进程级默认（两个来源必须共用一套规则）
// ============================================================================

void test_resolve_match_params() {
    using zhulong::face::MatchParams;
    using zhulong::face::resolve_match_params;

    const MatchParams defaults{0.40f, 3};

    // 未下发（空视图）= 原样使用默认值
    const MatchParams untouched = resolve_match_params(std::string_view{}, defaults);
    check(nearly_equal(untouched.threshold, 0.40f, 1e-6f), "未下发时阈值必须保持默认");
    check(untouched.top_k == 3, "未下发时 top_k 必须保持默认");

    // 只覆盖其中一项：另一项必须保持默认，而不是被清零
    const MatchParams only_threshold = resolve_match_params(R"({"threshold":0.55})", defaults);
    check(nearly_equal(only_threshold.threshold, 0.55f, 1e-6f), "任务级阈值应覆盖默认");
    check(only_threshold.top_k == 3, "未声明的 top_k 必须保持默认");

    const MatchParams only_top_k = resolve_match_params(R"({"top_k":7})", defaults);
    check(only_top_k.top_k == 7, "任务级 top_k 应覆盖默认");
    check(nearly_equal(only_top_k.threshold, 0.40f, 1e-6f), "未声明的阈值必须保持默认");

    // 上下文里的空格 / 制表符不能影响解析
    const MatchParams spaced = resolve_match_params("{\n  \"threshold\" : 0.65 , \"top_k\" : 2\n}", defaults);
    check(nearly_equal(spaced.threshold, 0.65f, 1e-6f), "空白不应影响阈值解析");
    check(spaced.top_k == 2, "空白不应影响 top_k 解析");

    // 完整成员名匹配：my_threshold / top_kx 不得冒充
    const MatchParams lookalike = resolve_match_params(R"({"my_threshold":0.9,"top_kx":9})", defaults);
    check(nearly_equal(lookalike.threshold, 0.40f, 1e-6f), "相似键名不得被当成 threshold");
    check(lookalike.top_k == 3, "相似键名不得被当成 top_k");

    // 越界值回落默认（包比宿主 schema 新/旧时必须退化而不是拒绝工作）
    const MatchParams bad_threshold_high = resolve_match_params(R"({"threshold":1.5})", defaults);
    check(nearly_equal(bad_threshold_high.threshold, 0.40f, 1e-6f), "阈值 >1 必须回落默认");
    const MatchParams bad_threshold_low = resolve_match_params(R"({"threshold":-0.2})", defaults);
    check(nearly_equal(bad_threshold_low.threshold, 0.40f, 1e-6f), "负阈值必须回落默认");
    const MatchParams bad_top_k_zero = resolve_match_params(R"({"top_k":0})", defaults);
    check(bad_top_k_zero.top_k == 3, "top_k=0 必须回落默认");
    const MatchParams bad_top_k_huge = resolve_match_params(R"({"top_k":21})", defaults);
    check(bad_top_k_huge.top_k == 3, "top_k 超上限必须回落默认");

    // 非数值（字符串/布尔/null）不得抛异常，直接回落默认
    const MatchParams non_numeric = resolve_match_params(R"({"threshold":"high","top_k":true})", defaults);
    check(nearly_equal(non_numeric.threshold, 0.40f, 1e-6f), "字符串阈值必须回落默认");
    check(non_numeric.top_k == 3, "布尔 top_k 必须回落默认");

    // 边界值本身合法：阈值 0.0 / 1.0，top_k 1 / 20
    const MatchParams edge_low = resolve_match_params(R"({"threshold":0.0,"top_k":1})", defaults);
    check(nearly_equal(edge_low.threshold, 0.0f, 1e-6f), "阈值 0.0 是合法配置（不是「未设置」）");
    check(edge_low.top_k == 1, "top_k=1 是合法配置");
    const MatchParams edge_high = resolve_match_params(R"({"threshold":1.0,"top_k":20})", defaults);
    check(nearly_equal(edge_high.threshold, 1.0f, 1e-6f), "阈值 1.0 是合法配置");
    check(edge_high.top_k == 20, "top_k=20 是合法配置");

    // 进程级默认不是 0.40/3 时，任务级缺省必须跟着进程级走
    const MatchParams custom_defaults{0.72f, 8};
    const MatchParams inherits = resolve_match_params(R"({"top_k":2})", custom_defaults);
    check(nearly_equal(inherits.threshold, 0.72f, 1e-6f), "任务级缺省阈值必须继承进程级默认");
    check(inherits.top_k == 2, "声明的 top_k 覆盖进程级默认");

    // 扩展参数与别名解析
    const MatchParams full_config = resolve_match_params(
        R"({
            "match_threshold": 0.82,
            "det_threshold": 0.62,
            "person_det_threshold": 0.55,
            "min_face_size": 48,
            "face_quality_threshold": 0.65,
            "filter_unmatched": true
        })",
        defaults);
    check(nearly_equal(full_config.threshold, 0.82f, 1e-6f), "match_threshold 别名解析成功");
    check(nearly_equal(full_config.face_det_threshold, 0.62f, 1e-6f), "det_threshold 别名解析成功");
    check(nearly_equal(full_config.person_det_threshold, 0.55f, 1e-6f), "person_det_threshold 解析成功");
    check(full_config.min_face_size == 48, "min_face_size 解析成功");
    check(nearly_equal(full_config.min_face_quality, 0.65f, 1e-6f), "face_quality_threshold 别名解析成功");
    check(full_config.filter_unmatched == true, "filter_unmatched 布尔解析成功");

    // 越界值回落测试
    const MatchParams out_of_bounds = resolve_match_params(
        R"({
            "face_det_threshold": 0.05,
            "min_face_size": 1000,
            "min_face_quality": 1.5,
            "filter_unmatched": false
        })",
        defaults);
    check(nearly_equal(out_of_bounds.face_det_threshold, defaults.face_det_threshold, 1e-6f),
          "越界 face_det_threshold 必须回落");
    check(out_of_bounds.min_face_size == defaults.min_face_size, "越界 min_face_size 必须回落");
    check(nearly_equal(out_of_bounds.min_face_quality, defaults.min_face_quality, 1e-6f),
          "越界 min_face_quality 必须回落");
    check(out_of_bounds.filter_unmatched == false, "filter_unmatched 为 false 应生效");

    // 带有 true 前缀的非法标识符不得误判为 true
    const MatchParams invalid_bool = resolve_match_params(R"({"filter_unmatched": true_story})", defaults);
    check(!invalid_bool.filter_unmatched, "带有 true 前缀的非法字符串不得误判为 true");

    // 新增 CCTV 图像门禁与分差字段默认关闭、占位默认值一致
    check(!untouched.enable_face_image_gate, "图像质量门禁默认必须关闭");
    check(nearly_equal(untouched.min_face_sharpness, 30.0f, 1e-6f), "min_face_sharpness 默认应为 30.0");
    check(nearly_equal(untouched.max_face_underexposure, 0.50f, 1e-6f), "max_face_underexposure 默认应为 0.50");
    check(nearly_equal(untouched.max_face_overexposure, 0.30f, 1e-6f), "max_face_overexposure 默认应为 0.30");
    check(nearly_equal(untouched.min_match_margin, 0.0f, 1e-6f), "min_match_margin 默认必须为 0.0（关闭）");

    // 进程级默认 + 任务级局部覆盖
    const MatchParams proc_defaults = resolve_match_params(
        R"({
            "enable_face_image_gate": true,
            "min_face_sharpness": 45.5,
            "max_face_underexposure": 0.40,
            "max_face_overexposure": 0.20,
            "min_match_margin": 0.08
        })",
        defaults);
    check(proc_defaults.enable_face_image_gate, "进程级 enable_face_image_gate 解析成功");
    check(nearly_equal(proc_defaults.min_face_sharpness, 45.5f, 1e-5f), "进程级 min_face_sharpness 解析成功");
    check(nearly_equal(proc_defaults.max_face_underexposure, 0.40f, 1e-6f), "进程级 max_face_underexposure 解析成功");
    check(nearly_equal(proc_defaults.max_face_overexposure, 0.20f, 1e-6f), "进程级 max_face_overexposure 解析成功");
    check(nearly_equal(proc_defaults.min_match_margin, 0.08f, 1e-6f), "进程级 min_match_margin 解析成功");

    const MatchParams task_override = resolve_match_params(
        R"({"enable_face_image_gate": false, "min_match_margin": 0.15})",
        proc_defaults);
    check(!task_override.enable_face_image_gate, "任务级可单独关闭进程级图像门禁");
    check(nearly_equal(task_override.min_face_sharpness, 45.5f, 1e-5f), "未声明的清晰度阈值继承进程级默认");
    check(nearly_equal(task_override.min_match_margin, 0.15f, 1e-6f), "任务级 min_match_margin 覆盖进程级默认");

    // 边界合法值（含 0.0 与 schema 上限 100000.0）
    const MatchParams gate_edges_low = resolve_match_params(
        R"({
            "min_face_sharpness": 0.0,
            "max_face_underexposure": 0.0,
            "max_face_overexposure": 0.0,
            "min_match_margin": 0.0
        })",
        proc_defaults);
    check(nearly_equal(gate_edges_low.min_face_sharpness, 0.0f, 1e-6f), "min_face_sharpness=0.0 合法");
    check(nearly_equal(gate_edges_low.max_face_underexposure, 0.0f, 1e-6f),
          "max_face_underexposure=0.0 合法（表示最严，不是关闭哨兵）");
    check(nearly_equal(gate_edges_low.max_face_overexposure, 0.0f, 1e-6f), "max_face_overexposure=0.0 合法");
    check(nearly_equal(gate_edges_low.min_match_margin, 0.0f, 1e-6f), "min_match_margin=0.0 合法");

    const MatchParams gate_edges_high = resolve_match_params(
        R"({
            "min_face_sharpness": 100000.0,
            "max_face_underexposure": 1.0,
            "max_face_overexposure": 1.0,
            "min_match_margin": 1.0
        })",
        defaults);
    check(nearly_equal(gate_edges_high.min_face_sharpness, 100000.0f, 1e-3f), "min_face_sharpness=100000.0 合法");
    check(nearly_equal(gate_edges_high.max_face_underexposure, 1.0f, 1e-6f), "max_face_underexposure=1.0 合法");
    check(nearly_equal(gate_edges_high.max_face_overexposure, 1.0f, 1e-6f), "max_face_overexposure=1.0 合法");
    check(nearly_equal(gate_edges_high.min_match_margin, 1.0f, 1e-6f), "min_match_margin=1.0 合法");

    // 超上限（含足以导致 float 窄化溢出 UB 的超大浮点数 1e100）与负数必须安全回落
    const MatchParams gate_invalid = resolve_match_params(
        R"({
            "min_face_sharpness": 1e100,
            "max_face_underexposure": 1.05,
            "max_face_overexposure": -0.01,
            "min_match_margin": 1.2
        })",
        proc_defaults);
    check(std::isfinite(gate_invalid.min_face_sharpness) &&
              nearly_equal(gate_invalid.min_face_sharpness, 45.5f, 1e-5f),
          "超上限 1e100 的 min_face_sharpness 必须安全回落，不得窄化溢出为 Inf");
    check(nearly_equal(gate_invalid.max_face_underexposure, 0.40f, 1e-6f), "越界 max_face_underexposure 必须回落");
    check(nearly_equal(gate_invalid.max_face_overexposure, 0.20f, 1e-6f), "负 max_face_overexposure 必须回落");
    check(nearly_equal(gate_invalid.min_match_margin, 0.08f, 1e-6f), "越界 min_match_margin 必须回落");

    // 跨帧特征融合默认开启、进程级/任务级覆盖与边界回落
    check(untouched.enable_face_feature_fusion, "跨帧特征融合默认必须开启 (true)");
    check(nearly_equal(untouched.fusion_similarity_threshold, 0.62f, 1e-6f),
          "fusion_similarity_threshold 默认应为 0.62");
    check(nearly_equal(untouched.fusion_ttl_seconds, 3.0f, 1e-6f),
          "fusion_ttl_seconds 默认应为 3.0");

    const MatchParams fusion_proc = resolve_match_params(
        R"({
            "enable_face_feature_fusion": false,
            "fusion_similarity_threshold": 0.70,
            "fusion_ttl_seconds": 5.5
        })",
        defaults);
    check(!fusion_proc.enable_face_feature_fusion, "进程级关闭 enable_face_feature_fusion 解析成功");
    check(nearly_equal(fusion_proc.fusion_similarity_threshold, 0.70f, 1e-6f),
          "进程级 fusion_similarity_threshold 解析成功");
    check(nearly_equal(fusion_proc.fusion_ttl_seconds, 5.5f, 1e-5f),
          "进程级 fusion_ttl_seconds 解析成功");

    const MatchParams fusion_task = resolve_match_params(
        R"({"enable_face_feature_fusion": true, "fusion_similarity_threshold": 0.45, "fusion_ttl_seconds": 0.5})",
        fusion_proc);
    check(fusion_task.enable_face_feature_fusion, "任务级可重新开启跨帧特征融合");
    check(nearly_equal(fusion_task.fusion_similarity_threshold, 0.45f, 1e-6f),
          "fusion_similarity_threshold 下界 0.45 合法");
    check(nearly_equal(fusion_task.fusion_ttl_seconds, 0.5f, 1e-6f),
          "fusion_ttl_seconds 下界 0.5 合法");

    const MatchParams fusion_high = resolve_match_params(
        R"({"fusion_similarity_threshold": 0.95, "fusion_ttl_seconds": 30.0})",
        defaults);
    check(nearly_equal(fusion_high.fusion_similarity_threshold, 0.95f, 1e-6f),
          "fusion_similarity_threshold 上界 0.95 合法");
    check(nearly_equal(fusion_high.fusion_ttl_seconds, 30.0f, 1e-5f),
          "fusion_ttl_seconds 上界 30.0 合法");

    const MatchParams fusion_oob = resolve_match_params(
        R"({
            "enable_face_feature_fusion": false_alarm,
            "fusion_similarity_threshold": 0.44,
            "fusion_ttl_seconds": 30.1
        })",
        fusion_proc);
    check(!fusion_oob.enable_face_feature_fusion, "非法布尔字面量不得改写 enable_face_feature_fusion");
    check(nearly_equal(fusion_oob.fusion_similarity_threshold, 0.70f, 1e-6f),
          "低于 0.45 的 fusion_similarity_threshold 必须回落");
    check(nearly_equal(fusion_oob.fusion_ttl_seconds, 5.5f, 1e-5f),
          "高于 30.0 的 fusion_ttl_seconds 必须回落");
}

void test_face_quality_assessment() {
    using namespace zhulong::face;

    // 1. 标准正面人脸 (1920x1080 画面，中心人脸，置信度 0.95)
    FaceDetection frontal;
    frontal.face_box = {0.45f, 0.20f, 0.55f, 0.38f}; // 宽 192px, 高 194px, ar ~ 1.01
    frontal.face_score = 0.95f;
    frontal.landmarks = {
        Point2f{0.48f, 0.25f}, // 左眼
        Point2f{0.52f, 0.25f}, // 右眼
        Point2f{0.50f, 0.29f}, // 鼻尖 (中心)
        Point2f{0.485f, 0.34f},// 左嘴
        Point2f{0.515f, 0.34f} // 右嘴
    };

    const auto frontal_res = assess_face_quality(frontal, 1920, 1080, 32, 0.40f);
    check(frontal_res.pass, "标准正面人脸必须通过质量评估");
    check(frontal_res.quality_score >= 0.80f, "正面人脸质量分应 >= 0.80");
    check(frontal_res.yaw_score >= 0.90f, "正面人脸偏航对称分应 >= 0.90");
    check(frontal_res.pitch_score >= 0.90f, "正面人脸俯仰结构分应 >= 0.90");

    // 2. 严重侧脸 (鼻尖显著偏向右眼，偏航角严重不对称)
    FaceDetection side_face = frontal;
    side_face.landmarks[2] = Point2f{0.518f, 0.29f}; // 鼻尖极度右偏
    const auto side_res = assess_face_quality(side_face, 1920, 1080, 32, 0.40f);
    check(side_res.yaw_score < 0.40f, "严重侧脸偏航对称分应明显衰减");
    check(side_res.quality_score < frontal_res.quality_score, "侧脸综合质量分应低于正脸");

    // 3. 尺寸过小 (宽 20px, 低于 min_face_size=32)
    FaceDetection tiny_face = frontal;
    tiny_face.face_box = {0.495f, 0.295f, 0.505f, 0.305f}; // 宽 19.2px, 高 10.8px
    const auto tiny_res = assess_face_quality(tiny_face, 1920, 1080, 32, 0.40f);
    check(!tiny_res.pass, "低于最小像素尺寸的人脸必须被拒绝");
    check(tiny_res.quality_score == 0.0f, "尺寸不达标时质量分应为 0");

    // 4. 关键点倒立/手性错乱 (左眼仍在左、右眼仍在右，但嘴巴比眼睛高 → 非刚体旋转可达拓扑)
    FaceDetection inverted = frontal;
    inverted.landmarks[0] = Point2f{0.48f, 0.35f}; // 眼睛在下方
    inverted.landmarks[1] = Point2f{0.52f, 0.35f};
    inverted.landmarks[3] = Point2f{0.485f, 0.25f}; // 嘴巴在上方
    inverted.landmarks[4] = Point2f{0.515f, 0.25f};
    const auto inv_res = assess_face_quality(inverted, 1920, 1080, 32, 0.40f);
    check(inv_res.pitch_score == 0.0f, "倒立或垂直结构错乱时俯仰分必须为 0");
    check(!inv_res.pass && inv_res.quality_score == 0.0f, "眼嘴拓扑倒置必须硬拒绝");
    check(!assess_face_quality(inverted, 1920, 1080, 32, 0.0f).pass,
          "眼嘴拓扑倒置即使在 min_face_quality=0 时也不得放行");

    // 5. 宽高比畸变 (极端扁脸，宽 400px，高 20px)
    FaceDetection distorted = frontal;
    distorted.face_box = {0.3f, 0.2f, 0.7f, 0.22f};
    const auto dist_res = assess_face_quality(distorted, 1000, 1000, 32, 0.40f);
    check(dist_res.aspect_score < 0.30f, "畸变扁脸长宽比得分应极低");

    // 6. 异常输入防守 (0宽、负高、0分辨率)
    check(!assess_face_quality(frontal, 0, 1080, 32, 0.40f).pass, "0分辨率必须安全返回未通过");
    FaceDetection degenerate = frontal;
    degenerate.face_box = {0.5f, 0.5f, 0.5f, 0.5f};
    check(!assess_face_quality(degenerate, 1920, 1080, 32, 0.40f).pass, "退化0面积框必须安全返回未通过");
}

// ============================================================================
// 5. 人脸 → 人体挂载判据（纯几何，零尺寸框不得 NaN）
// ============================================================================

void test_face_association() {
    // 人体框：x∈[0.25,0.75]、y∈[0.25,1.0]（宽 0.5、高 0.75，全部为二进制精确值）
    const std::array<float, 4> body{0.25f, 0.25f, 0.75f, 1.0f};

    // 上半身中央、完全落在人体框内 → 挂载
    check(zhulong::face::face_belongs_to_person({0.375f, 0.375f, 0.5f, 0.5f}, body),
          "上半身中央的人脸应挂载到人体");

    // 人脸中心落在人体下半身 → 不挂载（避免把路人甲的脸挂到别人身上）
    check(!zhulong::face::face_belongs_to_person({0.375f, 0.8125f, 0.5f, 0.9375f}, body),
          "下半身的人脸不得挂载");

    // 人体框左侧很远处 → 不挂载
    check(!zhulong::face::face_belongs_to_person({0.0f, 0.375f, 0.0625f, 0.5f}, body),
          "框外左侧的人脸不得挂载");

    // IoF 恰好 0.5 的边界：face x∈[0.1875,0.3125]，与 body 交集 0.0625/0.125 = 0.5
    check(zhulong::face::face_belongs_to_person({0.1875f, 0.375f, 0.3125f, 0.5f}, body),
          "IoF 等于 0.50 应挂载（>= 语义）");

    // IoF 0.375 < 0.50 → 不挂载
    const std::array<float, 4> shifted_body{0.265625f, 0.25f, 0.75f, 1.0f};
    check(!zhulong::face::face_belongs_to_person({0.1875f, 0.375f, 0.3125f, 0.5f}, shifted_body),
          "IoF 低于 0.50 不得挂载");

    // 零尺寸框：不得除出 NaN/inf，且必须判否
    check(!zhulong::face::face_belongs_to_person({0.5f, 0.5f, 0.5f, 0.5f}, body),
          "零尺寸人脸框不得挂载（且不得产生 NaN）");
    check(!zhulong::face::face_belongs_to_person({0.375f, 0.375f, 0.5f, 0.5f},
                                                 {0.5f, 0.5f, 0.5f, 0.5f}),
          "零尺寸人体框不得挂载（且不得产生 NaN）");

    // NaN 输入 → 判否
    const float nan = std::nanf("");
    check(!zhulong::face::face_belongs_to_person({nan, 0.375f, 0.5f, 0.5f}, body),
          "含 NaN 的人脸框不得挂载");
}

// ============================================================================
// 4. HWC→NCHW 归一化（仅 ONNX 档位需要；RKNN 模型自带归一化）
// ============================================================================

#if defined(ZHULONG_FACE_HAS_PREPROCESS_HELPER)
void test_hwc_to_nchw_normalized() {
    const uint8_t pixels[] = {128, 64, 0};
    std::vector<float> out(3, 0.0f);
    const float mean = 127.5f;
    const float inv_std = 1.0f / 127.5f;
    check(zhulong::face::hwc_to_nchw_normalized(pixels, 1, 1, mean, inv_std, out.data()),
          "1×1 图像归一化应成功");
    check(nearly_equal(out[0], (128.0f - mean) * inv_std, 1e-6f), "R 平面归一化值不符");
    check(nearly_equal(out[1], (64.0f - mean) * inv_std, 1e-6f), "G 平面归一化值不符");
    check(nearly_equal(out[2], (0.0f - mean) * inv_std, 1e-6f), "B 平面归一化值不符");

    check(!zhulong::face::hwc_to_nchw_normalized(nullptr, 1, 1, mean, inv_std, out.data()),
          "空源指针必须失败");
    check(!zhulong::face::hwc_to_nchw_normalized(pixels, 0, 1, mean, inv_std, out.data()),
          "零高必须失败");
    check(!zhulong::face::hwc_to_nchw_normalized(pixels, 1, 1, mean, inv_std, nullptr),
          "空目标指针必须失败");
}
#endif

void test_detector_dimensions() {
    using namespace zhulong::face;

    static_assert(kCctvStreamModelWidth % 128 == 0, "CCTV 宽度必须能被人脸/人体最大步长 128 整除");
    static_assert(kCctvStreamModelHeight % 128 == 0, "CCTV 高度必须能被人体最大步长 128 整除");
    static_assert(kCctvStreamModelHeight % 32 == 0, "CCTV 高度必须能被人脸最大步长 32 整除");
    static_assert(kDefaultModelWidth == 640 && kDefaultModelHeight == 640, "默认分辨率必须保持 640x640");
    static_assert(kCctvStreamModelWidth == 640 && kCctvStreamModelHeight == 384, "CCTV 优化分辨率为 640x384");

    FaceDetector default_face_detector;
    check(default_face_detector.model_width() == 640, "FaceDetector 默认宽度必须为 640");
    check(default_face_detector.model_height() == 640, "FaceDetector 默认高度必须为 640");

    FaceDetector cctv_face_detector(kCctvStreamModelWidth, kCctvStreamModelHeight);
    check(cctv_face_detector.model_width() == 640, "FaceDetector CCTV 宽度应为 640");
    check(cctv_face_detector.model_height() == 384, "FaceDetector CCTV 高度应为 384");

    // set_input_size 是**带校验**的：不合格的分辨率必须返回 false 且不改动实例状态。
    // 这条是「静默零检出」的唯一防线 —— 布局不符时解码只会返回空结果，没有错误码可报。
#if defined(ZHULONG_INFERENCE_RKNN)
    // rk3568 档位：人脸模型为动态双 shape (640x640 与 640x384)
    check(cctv_face_detector.set_input_size(kDefaultModelWidth, kDefaultModelHeight),
          "人脸检测器必须支持 640x640");
    check(cctv_face_detector.set_input_size(kCctvStreamModelWidth, kCctvStreamModelHeight),
          "人脸检测器必须支持 640x384 实时推理");
    check(!cctv_face_detector.set_input_size(320, 192),
          "动态双 shape 档位必须拒绝未量化包含的分辨率");
    check(!cctv_face_detector.set_input_size(640, 200),
          "不能被 32 整除的尺寸必须被拒绝");
    check(cctv_face_detector.model_width() == kCctvStreamModelWidth &&
          cctv_face_detector.model_height() == kCctvStreamModelHeight,
          "被拒绝的 set_input_size 不得改动实例状态");

    PersonDetector default_person_detector;
    check(default_person_detector.model_width() == 640, "PersonDetector 默认宽度必须为 640");
    check(default_person_detector.model_height() == 640, "PersonDetector 默认高度必须为 640");

    PersonDetector cctv_person_detector(kCctvStreamModelWidth, kCctvStreamModelHeight);
    check(cctv_person_detector.model_width() == 640, "PersonDetector CCTV 宽度应为 640");
    check(cctv_person_detector.model_height() == 384, "PersonDetector CCTV 高度应为 384");

    // 人体模型仍为静态 640x640
    check(!cctv_person_detector.set_input_size(640, 256),
          "静态人体模型档位必须拒绝非 640x640 分辨率");
    check(cctv_person_detector.set_input_size(kDefaultModelWidth, kDefaultModelHeight),
          "静态人体模型必须接受 640x640");
#else
    check(cctv_face_detector.set_input_size(320, 192), "320x192 能被人脸粒度 32 整除，应接受");
    check(cctv_face_detector.model_width() == 320, "FaceDetector set_input_size 宽度应生效");
    check(cctv_face_detector.model_height() == 192, "FaceDetector set_input_size 高度应生效");
    check(!cctv_face_detector.set_input_size(320, 200), "200 不能被人脸粒度 32 整除，应拒绝");
    check(cctv_face_detector.model_width() == 320 && cctv_face_detector.model_height() == 192,
          "被拒绝的 set_input_size 不得改动实例状态");

    PersonDetector default_person_detector;
    check(default_person_detector.model_width() == 640, "PersonDetector 默认宽度必须为 640");
    check(default_person_detector.model_height() == 640, "PersonDetector 默认高度必须为 640");

    PersonDetector cctv_person_detector(kCctvStreamModelWidth, kCctvStreamModelHeight);
    check(cctv_person_detector.model_width() == 640, "PersonDetector CCTV 宽度应为 640");
    check(cctv_person_detector.model_height() == 384, "PersonDetector CCTV 高度应为 384");

    check(cctv_person_detector.set_input_size(640, 256), "640x256 能被人体粒度 128 整除，应接受");
    check(cctv_person_detector.model_width() == 640, "PersonDetector set_input_size 宽度应生效");
    check(cctv_person_detector.model_height() == 256, "PersonDetector set_input_size 高度应生效");
    check(!cctv_person_detector.set_input_size(640, 192), "192 不能被人体粒度 128 整除，应拒绝");
    check(cctv_person_detector.model_width() == 640 && cctv_person_detector.model_height() == 256,
          "被拒绝的 set_input_size 不得改动实例状态");
#endif
}


// ============================================================================
// 6. SCRFD 输出解码（网格 → 原帧像素框、letterbox 逆变换、定长 Top-K、NMS）
//
// 这一段是全流水线里「写错也不报错」的极端：anchor 索引偏一位、letterbox 反算漏掉
// pad_top、或者把「满员即 break」当优化，症状都只有**零检出**或「远处小脸都在、近处
// 大脸丢了」——没有崩溃、没有错误码。所以它被抽成 header-only 纯函数（scrfd_decode.hpp），
// 用合成张量把每条数学钉死。
// ============================================================================

/** @brief 造一个 packed float32 张量并填入给定数据（形状只用于推导 element_count） */
zhulong::Tensor make_float_tensor(std::initializer_list<int64_t> dims, const std::vector<float>& values) {
    zhulong::Tensor tensor(std::vector<int64_t>(dims), zhulong::DataType::FLOAT32);
    check(tensor.element_count() == values.size(), "测试夹具：张量元素数与填值数量不符");
    std::copy(values.begin(), values.end(), tensor.data<float>());
    return tensor;
}

/** @brief 把若干组输出张量打包成 `Session::forward` 的返回形态（尺寸由数据长度决定） */
std::vector<zhulong::Tensor> pack_outputs(const std::vector<std::vector<float>>& groups) {
    std::vector<zhulong::Tensor> outputs;
    outputs.reserve(groups.size());
    for (const auto& values : groups) {
        outputs.push_back(make_float_tensor({static_cast<int64_t>(values.size()), 1}, values));
    }
    return outputs;
}

void test_scrfd_decode_maps_letterbox_back_to_source() {
    using namespace zhulong::face;

    // 模型 32×32、单层 stride 8 ⇒ 4×4 网格 × 2 anchor = 32 个位置
    // letterbox：原图 64×48 → scale 0.5 → 32×24 → 垂直居中补边 pad_top=4、pad_left=0
    constexpr int kModel = 32;
    constexpr int kAnchors = 2;
    constexpr size_t kAnchorsTotal = (kModel / 8) * (kModel / 8) * kAnchors;
    const std::array<int, 1> strides{8};

    std::vector<float> scores(kAnchorsTotal, 0.0f);
    std::vector<float> boxes(kAnchorsTotal * 4, 0.0f);
    std::vector<float> kps(kAnchorsTotal * 10, 0.0f);

    // 网格 (h=1, w=1) 的第 2 个 anchor ⇒ index = (1*4 + 1) * 2 + 1 = 11，中心 = (8, 8) 模型像素
    const size_t index = (1 * 4 + 1) * 2 + 1;
    scores[index] = 0.9f;
    // 偏移 0.5 个 stride = 4 像素
    boxes[index * 4 + 0] = 0.5f;
    boxes[index * 4 + 1] = 0.5f;
    boxes[index * 4 + 2] = 0.5f;
    boxes[index * 4 + 3] = 0.5f;
    // 关键点全部落在中心（偏移 0）

    const auto candidates = decode_scrfd_candidates(pack_outputs({scores, boxes, kps}), strides, kAnchors, kModel, kModel, 0.5f, 0.4f,
                                                    ScrfdDecodeLimits{128, 128},
                                                    ScrfdLetterbox{0.5f, 0, 4, 64, 48});

    check(candidates.size() == 1, "应解出唯一候选");
    if (candidates.size() == 1) {
        // x1 = (8 - 4 - 0) / 0.5 = 8 ; y1 = (8 - 4 - 4) / 0.5 = 0
        // x2 = (8 + 4 - 0) / 0.5 = 24 ; y2 = (8 + 4 - 4) / 0.5 = 16
        check(nearly_equal(candidates[0].box[0], 8.0f, 1e-3f), "x1 未按 letterbox 逆变换映射回原帧");
        check(nearly_equal(candidates[0].box[1], 0.0f, 1e-3f), "y1 未扣除 pad_top");
        check(nearly_equal(candidates[0].box[2], 24.0f, 1e-3f), "x2 未按 letterbox 逆变换映射回原帧");
        check(nearly_equal(candidates[0].box[3], 16.0f, 1e-3f), "y2 未按 letterbox 逆变换映射回原帧");
        // 关键点偏移 0：x = (8 - 0) / 0.5 = 16 ; y = (8 - 4) / 0.5 = 8
        check(nearly_equal(candidates[0].kps[0].x, 16.0f, 1e-3f), "关键点 x 未按 letterbox 逆变换");
        check(nearly_equal(candidates[0].kps[0].y, 8.0f, 1e-3f), "关键点 y 未扣除 pad_top");
        check(nearly_equal(candidates[0].score, 0.9f, 1e-6f), "分数未透传");
    }
}

void test_scrfd_decode_keeps_best_score_across_strides() {
    using namespace zhulong::face;

    // 模型 32×32、stride 8 / 16 各一层、每格 1 anchor
    // 回归点：候选上限 1 时，**细层先扫**不得把粗层（远景小脸 vs 近景大脸）的高分框挤掉。
    constexpr int kModel = 32;
    constexpr int kAnchors = 1;
    const std::array<int, 2> strides{8, 16};
    const size_t fine = (kModel / 8) * (kModel / 8) * kAnchors;   // 16
    const size_t coarse = (kModel / 16) * (kModel / 16) * kAnchors; // 4

    std::vector<float> score_fine(fine, 0.0f), score_coarse(coarse, 0.0f);
    std::vector<float> box_fine(fine * 4, 0.0f), box_coarse(coarse * 4, 0.0f);
    std::vector<float> kps_fine(fine * 10, 0.0f), kps_coarse(coarse * 10, 0.0f);

    score_fine[0] = 0.6f;   // 细层：低分
    score_coarse[0] = 0.9f; // 粗层：高分（必须活下来）
    for (int i = 0; i < 4; ++i) {
        box_fine[i] = 0.5f;
        box_coarse[i] = 0.5f;
    }

    const auto candidates = decode_scrfd_candidates(pack_outputs({score_fine, score_coarse, box_fine, box_coarse, kps_fine, kps_coarse}), strides, kAnchors, kModel, kModel, 0.5f, 0.4f,
                                                    ScrfdDecodeLimits{1, 1},
                                                    ScrfdLetterbox{1.0f, 0, 0, 32, 32});

    check(candidates.size() == 1, "候选上限为 1 时应只保留一条");
    if (candidates.size() == 1) {
        check(nearly_equal(candidates[0].score, 0.9f, 1e-6f),
              "候选上限截断必须按分数取 Top-K，不得因扫描顺序丢掉粗层高分框");
    }
}

void test_scrfd_decode_nms_and_filters() {
    using namespace zhulong::face;

    constexpr int kModel = 32;
    constexpr int kAnchors = 2;
    constexpr size_t kAnchorsTotal = (kModel / 8) * (kModel / 8) * kAnchors;
    const std::array<int, 1> strides{8};

    std::vector<float> scores(kAnchorsTotal, 0.0f);
    std::vector<float> boxes(kAnchorsTotal * 4, 0.0f);
    std::vector<float> kps(kAnchorsTotal * 10, 0.0f);

    // 同一格子两个 anchor 给出完全重合的框：IoU = 1 ⇒ 只留高分那个
    scores[0] = 0.90f;
    scores[1] = 0.80f;
    // 另加一个低分框（应被阈值挡掉）与一个退化框（x2 == x1，应被丢弃）
    scores[2] = 0.30f;
    scores[3] = 0.95f;
    for (size_t a = 0; a < 2; ++a) {
        boxes[a * 4 + 0] = 0.5f;
        boxes[a * 4 + 1] = 0.5f;
        boxes[a * 4 + 2] = 0.5f;
        boxes[a * 4 + 3] = 0.5f;
    }
    boxes[3 * 4 + 0] = 1.0f;  // 退化：x1 = 中心 - 8 = 0
    boxes[3 * 4 + 1] = 1.0f;
    boxes[3 * 4 + 2] = -1.0f; // x2 = 中心 - 8 = 0 ⇒ x2 == x1
    boxes[3 * 4 + 3] = -1.0f;

    const auto candidates = decode_scrfd_candidates(pack_outputs({scores, boxes, kps}), strides, kAnchors, kModel, kModel, 0.5f, 0.4f,
                                                    ScrfdDecodeLimits{128, 128},
                                                    ScrfdLetterbox{1.0f, 0, 0, 32, 32});

    check(candidates.size() == 1, "NMS 后应只剩 1 条（重合框被抑制、低分与退化框被丢弃）");
    if (candidates.size() == 1) {
        check(nearly_equal(candidates[0].score, 0.90f, 1e-6f), "NMS 后保留的必须是最高分框");
    }

    // NaN 分数不得参与比较，也不得把整帧结果清空
    std::vector<float> nan_scores = scores;
    nan_scores[4] = std::nanf("");
    const auto with_nan = decode_scrfd_candidates(pack_outputs({nan_scores, boxes, kps}), strides, kAnchors, kModel, kModel, 0.5f, 0.4f,
                                                  ScrfdDecodeLimits{128, 128},
                                                  ScrfdLetterbox{1.0f, 0, 0, 32, 32});
    check(with_nan.size() == 1, "NaN 分数候选必须被跳过，且不影响其余候选");

    // 全部低于阈值 ⇒ 空结果
    const auto none = decode_scrfd_candidates(pack_outputs({std::vector<float>(kAnchorsTotal, 0.1f), boxes, kps}), strides, kAnchors, kModel, kModel, 0.5f, 0.4f,
                                              ScrfdDecodeLimits{128, 128},
                                              ScrfdLetterbox{1.0f, 0, 0, 32, 32});
    check(none.empty(), "全部低于阈值时必须返回空结果");
}

void test_scrfd_decode_rejects_bad_layout() {
    using namespace zhulong::face;

    constexpr int kModel = 32;
    constexpr int kAnchors = 2;
    constexpr size_t kAnchorsTotal = (kModel / 8) * (kModel / 8) * kAnchors;
    const std::array<int, 1> strides{8};

    std::vector<float> scores(kAnchorsTotal, 0.9f);
    std::vector<float> boxes(kAnchorsTotal * 4, 0.5f);
    std::vector<float> kps(kAnchorsTotal * 10, 0.0f);

    // 1) 张量个数不符（少一组）
    std::vector<zhulong::Tensor> two_tensors;
    two_tensors.push_back(make_float_tensor({static_cast<int64_t>(kAnchorsTotal), 1}, scores));
    two_tensors.push_back(make_float_tensor({static_cast<int64_t>(kAnchorsTotal * 4), 1}, boxes));
    check(decode_scrfd_candidates(two_tensors, strides, kAnchors, kModel, kModel, 0.5f, 0.4f,
                                  ScrfdDecodeLimits{128, 128},
                                  ScrfdLetterbox{1.0f, 0, 0, 32, 32}).empty(),
          "张量个数不符时必须整体放弃解码（否则会越界索引）");

    // 2) element_count 不符（模拟换了 anchor 数或空间尺度却没改解码参数）
    std::vector<float> short_scores(kAnchorsTotal - 1, 0.9f);
    check(decode_scrfd_candidates(pack_outputs({short_scores, boxes, kps}), strides, kAnchors, kModel, kModel, 0.5f, 0.4f,
                                  ScrfdDecodeLimits{128, 128},
                                  ScrfdLetterbox{1.0f, 0, 0, 32, 32}).empty(),
          "元素数不符时必须整体放弃解码");

    // 3) dtype 不符（量化档位会产出 INT8/INT32）
    std::vector<zhulong::Tensor> wrong_dtype;
    wrong_dtype.push_back(zhulong::Tensor({static_cast<int64_t>(kAnchorsTotal), 1}, zhulong::DataType::INT32));
    wrong_dtype.push_back(make_float_tensor({static_cast<int64_t>(kAnchorsTotal * 4), 1}, boxes));
    wrong_dtype.push_back(make_float_tensor({static_cast<int64_t>(kAnchorsTotal * 10), 1}, kps));
    check(decode_scrfd_candidates(wrong_dtype, strides, kAnchors, kModel, kModel, 0.5f, 0.4f,
                                  ScrfdDecodeLimits{128, 128},
                                  ScrfdLetterbox{1.0f, 0, 0, 32, 32}).empty(),
          "非 FLOAT32 输出必须被拒绝（按 float 读 Int8 会得到垃圾框）");

    // 4) 模型尺寸不能被 stride 整除 / letterbox 参数非法
    check(decode_scrfd_candidates(pack_outputs({scores, boxes, kps}), strides, kAnchors, 30, 30, 0.5f, 0.4f,
                                  ScrfdDecodeLimits{128, 128},
                                  ScrfdLetterbox{1.0f, 0, 0, 32, 32}).empty(),
          "模型尺寸不能被 stride 整除时必须拒绝解码");
    check(decode_scrfd_candidates(pack_outputs({scores, boxes, kps}), strides, kAnchors, kModel, kModel, 0.5f, 0.4f,
                                  ScrfdDecodeLimits{128, 128},
                                  ScrfdLetterbox{0.0f, 0, 0, 32, 32}).empty(),
          "det_scale <= 0 时必须拒绝解码（否则除零出 inf）");
    check(decode_scrfd_candidates(pack_outputs({scores, boxes, kps}), strides, kAnchors, kModel, kModel, 0.5f, 0.4f,
                                  ScrfdDecodeLimits{0, 0},
                                  ScrfdLetterbox{1.0f, 0, 0, 32, 32}).empty(),
          "候选上限为 0 时必须返回空结果");
}


// ============================================================================
// 7. `filter_unmatched` 的场景裁剪（容器重排 + person_index 重映射）
//
// 裁剪本身不难，难的是「裁掉一个人体后，挂在他身上的脸还指着旧下标」—— 宿主会据此
// 把脸画到别人的框里（或越界）。这里不需要模型，只造场景结构体即可钉死。
// ============================================================================

zhulong::PersonItem make_person(float x1, float y1, float x2, float y2, float score) {
    zhulong::PersonItem person;
    person.track_id = 0;
    person.body_box[0] = x1;
    person.body_box[1] = y1;
    person.body_box[2] = x2;
    person.body_box[3] = y2;
    person.body_score = score;
    return person;
}

zhulong::FaceStructureItem make_face(int32_t person_index, bool matched) {
    zhulong::FaceStructureItem face;
    face.box[0] = 0.1f;
    face.box[1] = 0.1f;
    face.box[2] = 0.2f;
    face.box[3] = 0.2f;
    face.quality = 0.8f;
    face.matched = matched;
    face.person_index = person_index;
    return face;
}

void test_drop_unmatched_persons() {
    using namespace zhulong::face;

    // 人体 0：背身（无脸）→ 必须消失
    // 人体 1：挂一张已匹配人脸 → 保留，新下标 0
    // 人体 2：挂一张已匹配 + 一张未匹配人脸 → 保留，新下标 1
    zhulong::SceneStructure scene;
    scene.persons.push_back(make_person(0.0f, 0.2f, 0.2f, 1.0f, 0.9f));
    scene.persons.push_back(make_person(0.3f, 0.2f, 0.5f, 1.0f, 0.8f));
    scene.persons.push_back(make_person(0.6f, 0.2f, 0.8f, 1.0f, 0.7f));
    scene.faces.push_back(make_face(1, true));
    scene.faces.push_back(make_face(2, true));
    scene.faces.push_back(make_face(2, false));
    scene.faces.push_back(make_face(-1, true)); // 孤立人脸：无人体可挂，保持 -1

    drop_unmatched_persons(scene);

    check(scene.persons.size() == 2, "无人脸/只挂未匹配人脸的人体必须被裁掉");
    check(scene.faces.size() == 4, "裁剪人体不得丢人脸（未匹配脸已由上层过滤）");
    if (scene.persons.size() == 2 && scene.faces.size() == 4) {
        check(nearly_equal(scene.persons[0].body_box[0], 0.3f, 1e-6f), "保留的第一个人体应是原下标 1");
        check(nearly_equal(scene.persons[1].body_box[0], 0.6f, 1e-6f), "保留的第二个人体应是原下标 2");
        check(scene.faces[0].person_index == 0, "原下标 1 的人脸必须重映射为 0");
        check(scene.faces[1].person_index == 1, "原下标 2 的人脸必须重映射为 1");
        check(scene.faces[2].person_index == 1, "同一人体的第二张脸必须指向同一新下标");
        check(scene.faces[3].person_index == -1, "孤立人脸的 person_index 必须保持 -1");
    }

    // 全部人体都无人脸 ⇒ 场景里既没有人体也没有索引可指
    zhulong::SceneStructure dropped;
    dropped.persons.push_back(make_person(0.0f, 0.2f, 0.2f, 1.0f, 0.9f));
    dropped.faces.push_back(make_face(-1, true));
    drop_unmatched_persons(dropped);
    check(dropped.persons.empty(), "没有已匹配人脸时所有人体都必须消失");
    check(dropped.faces.size() == 1 && dropped.faces[0].person_index == -1,
          "孤立人脸不得因为裁剪被误连到不存在的人体上");

    // 语义边界：越界/负下标的 face.person_index 不得越界访问
    zhulong::SceneStructure rogue;
    rogue.persons.push_back(make_person(0.0f, 0.2f, 0.2f, 1.0f, 0.9f));
    rogue.faces.push_back(make_face(7, true));
    drop_unmatched_persons(rogue);
    check(rogue.persons.empty() && rogue.faces[0].person_index == -1,
          "越界的 person_index 必须按「无关联」处理，绝不得越界读 persons");

    // 幂等
    zhulong::SceneStructure idempotent;
    idempotent.persons.push_back(make_person(0.3f, 0.2f, 0.5f, 1.0f, 0.8f));
    idempotent.faces.push_back(make_face(0, true));
    drop_unmatched_persons(idempotent);
    const size_t persons_after_first = idempotent.persons.size();
    drop_unmatched_persons(idempotent);
    check(idempotent.persons.size() == persons_after_first && idempotent.faces[0].person_index == 0,
          "重复裁剪必须幂等");
}

// ============================================================================
// 8. 像素局部坐标系：分辨率/长宽比/滚转/镜像不变性、侧脸不反弹与拓扑/NaN 硬拒
// ============================================================================

zhulong::face::FaceDetection make_pixel_face_detection(
    float x1, float y1, float x2, float y2,
    const std::array<zhulong::face::Point2f, 5>& pts_px,
    float score,
    int img_w,
    int img_h,
    bool with_pixel_cache = false
) {
    zhulong::face::FaceDetection det;
    const float fw = static_cast<float>(img_w);
    const float fh = static_cast<float>(img_h);
    det.face_box = {x1 / fw, y1 / fh, x2 / fw, y2 / fh};
    det.face_score = score;
    for (size_t i = 0; i < pts_px.size(); ++i) {
        det.landmarks[i] = {pts_px[i].x / fw, pts_px[i].y / fh};
    }
    det.landmarks_pixels = pts_px;
    det.has_landmark_pixels = with_pixel_cache;
    det.is_real_detection = true;
    return det;
}

std::array<zhulong::face::Point2f, 5> rotate_points_around(
    const std::array<zhulong::face::Point2f, 5>& pts,
    float cx, float cy, float angle_deg
) {
    const float rad = angle_deg * 3.14159265358979323846f / 180.0f;
    const float c = std::cos(rad);
    const float s = std::sin(rad);
    std::array<zhulong::face::Point2f, 5> out{};
    for (size_t i = 0; i < pts.size(); ++i) {
        const float dx = pts[i].x - cx;
        const float dy = pts[i].y - cy;
        out[i].x = cx + c * dx - s * dy;
        out[i].y = cy + s * dx + c * dy;
    }
    return out;
}

void test_face_quality_invariance_and_topology() {
    using namespace zhulong::face;

    // 基准不对称偏航人脸（像素尺寸 120×140，略偏右，非满分以便检验连续评分不变性）
    const std::array<Point2f, 5> base_pts{{
        {240.0f - 22.0f, 180.0f - 20.0f}, // le
        {240.0f + 22.0f, 180.0f - 20.0f}, // re (eye_span = 44)
        {240.0f + 8.0f, 180.0f + 4.0f},   // nose (偏右，r_pitch = 24 / 48 = 0.50)
        {240.0f - 16.0f, 180.0f + 28.0f}, // lm
        {240.0f + 16.0f, 180.0f + 28.0f}, // rm
    }};
    const auto ref_det = make_pixel_face_detection(180.0f, 110.0f, 300.0f, 250.0f, base_pts, 0.90f, 640, 360, false);
    const auto ref_q = assess_face_quality(ref_det, 640, 360, 32, 0.40f);
    check(ref_q.pass && ref_q.yaw_score > 0.2f && ref_q.yaw_score < 0.95f,
          "基准测试脸应处于连续软评分中段并通过");

    // 1) 不同全图分辨率与画面长宽比下，同一像素几何的评分完全一致
    const std::pair<int, int> resolutions[] = {
        {640, 360}, {1280, 720}, {1920, 1080}, {1080, 1920}, {3840, 2160}, {2048, 1536}
    };
    for (const auto& [w, h] : resolutions) {
        for (bool cache_px : {false, true}) {
            const auto det = make_pixel_face_detection(180.0f, 110.0f, 300.0f, 250.0f, base_pts, 0.90f, w, h, cache_px);
            const auto q = assess_face_quality(det, w, h, 32, 0.40f);
            check(q.pass == ref_q.pass &&
                      nearly_equal(q.yaw_score, ref_q.yaw_score, 1e-4f) &&
                      nearly_equal(q.pitch_score, ref_q.pitch_score, 1e-4f) &&
                      nearly_equal(q.quality_score, ref_q.quality_score, 1e-4f),
                  "同一像素几何在不同全图分辨率/长宽比下评分必须一致");
        }
    }

    // 2) 既有误拒回归：32×36 远距小正脸在 4K 全幅（face_h=10.2px, 旧归一化 10.2/2160=0.0047<=0.005）
    //    与 640×360 AI 视图裁剪下都必须正常通过且分数一致
    const std::array<Point2f, 5> small_pts{{
        {110.0f, 111.0f}, {122.0f, 111.0f}, {116.0f, 116.0f}, {111.5f, 121.2f}, {120.5f, 121.2f}
    }};
    const auto small_4k = assess_face_quality(
        make_pixel_face_detection(100.0f, 100.0f, 132.0f, 136.0f, small_pts, 0.92f, 3840, 2160),
        3840, 2160, 32, 0.40f);
    const auto small_crop = assess_face_quality(
        make_pixel_face_detection(100.0f, 100.0f, 132.0f, 136.0f, small_pts, 0.92f, 640, 360),
        640, 360, 32, 0.40f);
    check(small_4k.pass && small_4k.pitch_score > 0.95f,
          "4K 全幅下的 32px 合法小脸不得因全图高度归一化台阶被误判俯仰 0 分");
    check(nearly_equal(small_4k.quality_score, small_crop.quality_score, 1e-4f),
          "32px 小脸在 4K 全幅与 640x360 AI 视图下的质量分必须一致");

    // 3) 滚转不变性（含任意角、90°/180° 矩形框长短边互换，以及退化阈值边界附近样本）
    const float angles[] = {0.0f, 17.5f, -33.0f, 62.0f, 90.0f, -90.0f, 180.0f};
    const auto sq_ref = assess_face_quality(
        make_pixel_face_detection(180.0f, 120.0f, 300.0f, 240.0f, base_pts, 0.90f, 1920, 1080),
        1920, 1080, 32, 0.40f);
    for (float deg : angles) {
        const auto rot_pts = rotate_points_around(base_pts, 240.0f, 180.0f, deg);
        const auto rot_q = assess_face_quality(
            make_pixel_face_detection(180.0f, 120.0f, 300.0f, 240.0f, rot_pts, 0.90f, 1920, 1080),
            1920, 1080, 32, 0.40f);
        check(rot_q.pass == sq_ref.pass &&
                  nearly_equal(rot_q.yaw_score, sq_ref.yaw_score, 1e-4f) &&
                  nearly_equal(rot_q.pitch_score, sq_ref.pitch_score, 1e-4f),
              "滚转变化不得改变 yaw_score / pitch_score 与通过结论");
    }

    // 矩形框 120×160 在 ±90° 滚转时变为 160×120（min_dim 恒为 120），紧邻 eye_span=0.12*min_dim(14.4px) 的两侧样本结论必须滚转不变
    for (float eye_span_px : {13.6f, 15.2f}) {
        const bool expect_pass = (eye_span_px >= 14.4f);
        const std::array<Point2f, 5> edge_pts{{
            {300.0f - 0.5f * eye_span_px, 300.0f - 15.0f},
            {300.0f + 0.5f * eye_span_px, 300.0f - 15.0f},
            {300.0f, 300.0f},
            {300.0f - 8.0f, 300.0f + 15.0f},
            {300.0f + 8.0f, 300.0f + 15.0f},
        }};
        const auto q0 = assess_face_quality(
            make_pixel_face_detection(240.0f, 220.0f, 360.0f, 380.0f, edge_pts, 0.95f, 1280, 720),
            1280, 720, 32, 0.0f);
        const auto q90 = assess_face_quality(
            make_pixel_face_detection(220.0f, 240.0f, 380.0f, 360.0f,
                                      rotate_points_around(edge_pts, 300.0f, 300.0f, 90.0f), 0.95f, 1280, 720),
            1280, 720, 32, 0.0f);
        const auto q180 = assess_face_quality(
            make_pixel_face_detection(240.0f, 220.0f, 360.0f, 380.0f,
                                      rotate_points_around(edge_pts, 300.0f, 300.0f, 180.0f), 0.95f, 1280, 720),
            1280, 720, 32, 0.0f);
        check(q0.pass == expect_pass && q90.pass == expect_pass && q180.pass == expect_pass,
              "矩形框 0°/90°/180° 滚转下 eye_span 退化边界结论必须一致");
    }

    // 4) 左右镜像（水平翻转 + 交换左右眼/左右嘴角索引）后 yaw/pitch 一致
    const auto rolled_pts = rotate_points_around(base_pts, 240.0f, 180.0f, 22.0f);
    std::array<Point2f, 5> mirrored_pts{};
    constexpr float kMirrorW = 640.0f;
    mirrored_pts[0] = {kMirrorW - rolled_pts[1].x, rolled_pts[1].y}; // 新左眼 = 镜像原右眼
    mirrored_pts[1] = {kMirrorW - rolled_pts[0].x, rolled_pts[0].y}; // 新右眼 = 镜像原左眼
    mirrored_pts[2] = {kMirrorW - rolled_pts[2].x, rolled_pts[2].y}; // 鼻尖
    mirrored_pts[3] = {kMirrorW - rolled_pts[4].x, rolled_pts[4].y}; // 新左嘴角 = 镜像原右嘴角
    mirrored_pts[4] = {kMirrorW - rolled_pts[3].x, rolled_pts[3].y}; // 新右嘴角 = 镜像原左嘴角
    const auto orig_rolled_q = assess_face_quality(
        make_pixel_face_detection(180.0f, 120.0f, 300.0f, 240.0f, rolled_pts, 0.90f, 640, 360),
        640, 360, 32, 0.40f);
    const auto mirror_q = assess_face_quality(
        make_pixel_face_detection(kMirrorW - 300.0f, 120.0f, kMirrorW - 180.0f, 240.0f, mirrored_pts, 0.90f, 640, 360),
        640, 360, 32, 0.40f);
    check(nearly_equal(orig_rolled_q.yaw_score, mirror_q.yaw_score, 1e-4f) &&
              nearly_equal(orig_rolled_q.pitch_score, mirror_q.pitch_score, 1e-4f) &&
              orig_rolled_q.pass == mirror_q.pass,
          "左右镜像（交换 eye/mouth 索引）后 yaw/pitch 评分必须一致");

    // 5) ~55° 普通侧脸（eye_span ≈ 0.28*min_dim，三维鼻尖投影略越过右眼）不被硬拒，且越界点不反弹高分
    const std::array<Point2f, 5> profile_55_pts{{
        {200.0f - 14.0f, 180.0f - 18.0f}, // le
        {200.0f + 14.0f, 180.0f - 18.0f}, // re (eye_span = 28px = 0.28 * 100)
        {200.0f + 21.0f, 180.0f + 2.0f},  // nose (越过右眼 7px，|nose_u| = 21px = 0.21 * 100)
        {200.0f - 6.0f, 180.0f + 22.0f},  // lm
        {200.0f + 16.0f, 180.0f + 22.0f}, // rm
    }};
    const auto profile_55_q0 = assess_face_quality(
        make_pixel_face_detection(150.0f, 125.0f, 250.0f, 240.0f, profile_55_pts, 0.92f, 1280, 720),
        1280, 720, 32, 0.0f);
    const auto profile_55_qdef = assess_face_quality(
        make_pixel_face_detection(150.0f, 125.0f, 250.0f, 240.0f, profile_55_pts, 0.92f, 1280, 720),
        1280, 720, 32, 0.40f);
    check(profile_55_q0.pass && profile_55_qdef.pass && profile_55_q0.quality_score > 0.50f,
          "~55° 普通侧脸不得被拓扑硬拒，在默认质量门限与关闭门限下均应通过");
    check(profile_55_q0.yaw_score == 0.0f, "鼻尖越过单眼的侧脸偏航分量应软衰减至 0，不得因 std::abs 反弹");

    // 鼻尖继续外移（仍在框边缘附近 0.60*min_dim）不得出现 std::abs 反弹；飞出框外远端（1.5*min_dim）必须硬拒
    auto moderate_overshoot = profile_55_pts;
    moderate_overshoot[2].x = 200.0f + 60.0f;
    const auto mod_q = assess_face_quality(
        make_pixel_face_detection(150.0f, 125.0f, 250.0f, 240.0f, moderate_overshoot, 0.92f, 1280, 720),
        1280, 720, 32, 0.0f);
    check(mod_q.yaw_score == 0.0f, "外移侧向鼻尖不得因 std::abs 反向回升出正偏航分");

    auto far_outlier = profile_55_pts;
    far_outlier[2].x = 200.0f + 150.0f;
    const auto far_q = assess_face_quality(
        make_pixel_face_detection(150.0f, 125.0f, 250.0f, 240.0f, far_outlier, 0.99f, 1280, 720),
        1280, 720, 32, 0.0f);
    check(!far_q.pass && far_q.quality_score == 0.0f,
          "鼻尖飞出框外远端即使在 face_score=0.99 且 min_face_quality=0 时也必须硬拒绝");

    // 6) 俯视 CCTV 大俯仰正脸（r_pitch = 0.95 < 1.10）在 min_face_quality=0 时不被拓扑硬拒
    auto steep_overhead = base_pts;
    steep_overhead[2].y = 160.0f + 0.95f * 48.0f; // eye_mid.y=160, face_h=48
    const auto overhead_q = assess_face_quality(
        make_pixel_face_detection(180.0f, 110.0f, 300.0f, 250.0f, steep_overhead, 0.90f, 1280, 720),
        1280, 720, 32, 0.0f);
    check(overhead_q.pass && overhead_q.quality_score > 0.0f,
          "俯视大俯仰正脸 (r_pitch=0.95) 在质量门限关闭时不应被当成拓扑异常硬拒");

    // 7) 各类非法拓扑与非有限输入在 min_face_quality=0 + 高分下全部硬拒
    auto same_eyes = base_pts;
    same_eyes[1] = same_eyes[0];
    check(!assess_face_quality(
              make_pixel_face_detection(180.0f, 110.0f, 300.0f, 250.0f, same_eyes, 0.99f, 1280, 720),
              1280, 720, 32, 0.0f)
               .pass,
          "重合眼点必须硬拒");

    auto crossed_mouth = base_pts;
    std::swap(crossed_mouth[3], crossed_mouth[4]);
    check(!assess_face_quality(
              make_pixel_face_detection(180.0f, 110.0f, 300.0f, 250.0f, crossed_mouth, 0.99f, 1280, 720),
              1280, 720, 32, 0.0f)
               .pass,
          "嘴角手性交叉必须硬拒");

    auto nose_above_eyes = base_pts;
    nose_above_eyes[2].y = 155.0f; // 高于双眼 y=160
    check(!assess_face_quality(
              make_pixel_face_detection(180.0f, 110.0f, 300.0f, 250.0f, nose_above_eyes, 0.99f, 1280, 720),
              1280, 720, 32, 0.0f)
               .pass,
          "鼻尖高于双眼连线必须硬拒");

    auto nose_below_chin = base_pts;
    nose_below_chin[2].y = 160.0f + 1.15f * 48.0f; // r_pitch = 1.15 >= 1.10
    check(!assess_face_quality(
              make_pixel_face_detection(180.0f, 110.0f, 300.0f, 250.0f, nose_below_chin, 0.99f, 1280, 720),
              1280, 720, 32, 0.0f)
               .pass,
          "鼻尖远低于嘴线 (r_pitch >= 1.10) 必须硬拒");

    const float nan = std::nanf("");
    const float inf = std::numeric_limits<float>::infinity();
    FaceDetection nan_score = ref_det;
    nan_score.face_score = nan;
    const auto nan_score_q = assess_face_quality(nan_score, 640, 360, 32, 0.0f);
    check(!nan_score_q.pass && nan_score_q.quality_score == 0.0f && std::isfinite(nan_score_q.quality_score),
          "NaN face_score 不得穿透 min_face_quality=0 门禁");

    FaceDetection nan_box = ref_det;
    nan_box.face_box[2] = inf;
    check(!assess_face_quality(nan_box, 640, 360, 32, 0.0f).pass, "Inf face_box 必须硬拒");

    FaceDetection nan_kps = ref_det;
    nan_kps.landmarks[2].x = nan;
    check(!assess_face_quality(nan_kps, 640, 360, 32, 0.0f).pass, "NaN 关键点必须硬拒");

    check(!assess_face_quality(ref_det, 640, 360, 32, nan).pass, "NaN min_face_quality 必须硬拒");
}

// ============================================================================
// 9. 对齐 RGB 清晰度（上采样行列式补偿 + 近景大脸保护）、曝光统计与流式图像门禁
// ============================================================================

/** @brief 在源图像素空间绘制指定尺度 (face_size_px) 的合成五官图案，并返回对应 5 关键点 */
std::array<zhulong::face::Point2f, 5> render_synthetic_face_canvas(
    std::vector<uint8_t>& rgb_canvas,
    int canvas_w,
    int canvas_h,
    float face_size_px
) {
    rgb_canvas.assign(static_cast<size_t>(canvas_w) * static_cast<size_t>(canvas_h) * 3, 118);
    const float scale = face_size_px / 112.0f;
    const float origin_x = 0.5f * (static_cast<float>(canvas_w) - face_size_px);
    const float origin_y = 0.5f * (static_cast<float>(canvas_h) - face_size_px);

    auto fill_rect = [&](float u0, float v0, float u1, float v1, uint8_t r, uint8_t g, uint8_t b) {
        const int x0 = std::clamp(static_cast<int>(std::lround(origin_x + u0 * scale)), 0, canvas_w);
        const int y0 = std::clamp(static_cast<int>(std::lround(origin_y + v0 * scale)), 0, canvas_h);
        const int x1 = std::clamp(std::max(x0 + 1, static_cast<int>(std::lround(origin_x + u1 * scale))), 0, canvas_w);
        const int y1 = std::clamp(std::max(y0 + 1, static_cast<int>(std::lround(origin_y + v1 * scale))), 0, canvas_h);
        for (int y = y0; y < y1; ++y) {
            for (int x = x0; x < x1; ++x) {
                const size_t off = (static_cast<size_t>(y) * static_cast<size_t>(canvas_w) + static_cast<size_t>(x)) * 3;
                rgb_canvas[off + 0] = r;
                rgb_canvas[off + 1] = g;
                rgb_canvas[off + 2] = b;
            }
        }
    };

    // 脸部底色 + 眉毛、眼白/瞳孔、鼻梁高光与鼻翼阴影、唇线（保证一维五官边缘在不同源尺度下成比例缩放）
    fill_rect(14.0f, 12.0f, 98.0f, 104.0f, 196, 168, 152);
    fill_rect(26.0f, 38.0f, 48.0f, 43.0f, 32, 28, 26);   // 左眉
    fill_rect(64.0f, 38.0f, 86.0f, 43.0f, 32, 28, 26);   // 右眉
    fill_rect(30.0f, 48.0f, 46.0f, 56.0f, 235, 235, 235);// 左眼白
    fill_rect(66.0f, 48.0f, 82.0f, 56.0f, 235, 235, 235);// 右眼白
    fill_rect(35.0f, 49.0f, 42.0f, 55.0f, 20, 20, 22);   // 左瞳孔
    fill_rect(70.0f, 49.0f, 77.0f, 55.0f, 20, 20, 22);   // 右瞳孔
    fill_rect(53.0f, 52.0f, 59.0f, 74.0f, 228, 205, 190);// 鼻梁高光
    fill_rect(48.0f, 72.0f, 64.0f, 77.0f, 75, 58, 52);   // 鼻底阴影
    fill_rect(38.0f, 89.0f, 74.0f, 95.0f, 110, 45, 52);  // 嘴唇

    std::array<zhulong::face::Point2f, 5> src_pts{};
    for (size_t i = 0; i < src_pts.size(); ++i) {
        src_pts[i].x = origin_x + zhulong::face::kArcfaceRefPoints[i].x * scale;
        src_pts[i].y = origin_y + zhulong::face::kArcfaceRefPoints[i].y * scale;
    }
    return src_pts;
}

/** @brief 在源图像素空间施加固定半径均值模糊核（模拟传感器固定像素 PSF 失焦/运动模糊） */
std::vector<uint8_t> blur_rgb_canvas(const std::vector<uint8_t>& src, int w, int h, int radius, int passes) {
    std::vector<uint8_t> cur = src;
    std::vector<uint8_t> tmp(src.size(), 0);
    for (int pass = 0; pass < passes; ++pass) {
        for (int y = 0; y < h; ++y) {
            for (int x = 0; x < w; ++x) {
                int sum_r = 0, sum_g = 0, sum_b = 0, count = 0;
                for (int dx = -radius; dx <= radius; ++dx) {
                    const int sx = std::clamp(x + dx, 0, w - 1);
                    const size_t off = (static_cast<size_t>(y) * static_cast<size_t>(w) + static_cast<size_t>(sx)) * 3;
                    sum_r += cur[off + 0];
                    sum_g += cur[off + 1];
                    sum_b += cur[off + 2];
                    ++count;
                }
                const size_t dst = (static_cast<size_t>(y) * static_cast<size_t>(w) + static_cast<size_t>(x)) * 3;
                tmp[dst + 0] = static_cast<uint8_t>(sum_r / count);
                tmp[dst + 1] = static_cast<uint8_t>(sum_g / count);
                tmp[dst + 2] = static_cast<uint8_t>(sum_b / count);
            }
        }
        for (int y = 0; y < h; ++y) {
            for (int x = 0; x < w; ++x) {
                int sum_r = 0, sum_g = 0, sum_b = 0, count = 0;
                for (int dy = -radius; dy <= radius; ++dy) {
                    const int sy = std::clamp(y + dy, 0, h - 1);
                    const size_t off = (static_cast<size_t>(sy) * static_cast<size_t>(w) + static_cast<size_t>(x)) * 3;
                    sum_r += tmp[off + 0];
                    sum_g += tmp[off + 1];
                    sum_b += tmp[off + 2];
                    ++count;
                }
                const size_t dst = (static_cast<size_t>(y) * static_cast<size_t>(w) + static_cast<size_t>(x)) * 3;
                cur[dst + 0] = static_cast<uint8_t>(sum_r / count);
                cur[dst + 1] = static_cast<uint8_t>(sum_g / count);
                cur[dst + 2] = static_cast<uint8_t>(sum_b / count);
            }
        }
    }
    return cur;
}

void test_face_image_quality_and_gate() {
    using namespace zhulong::face;

    // 1) 源图像素空间 PSF 模糊下，32 / 64 / 200 三种尺度经真实对齐后的清晰度排序与近景不误拒
    constexpr int kCanvas = 320;
    constexpr int kPatch = 112;
    MatchParams gate_on;
    gate_on.enable_face_image_gate = true;
    gate_on.min_face_sharpness = 30.0f;
    gate_on.max_face_underexposure = 0.50f;
    gate_on.max_face_overexposure = 0.30f;

    for (float face_px : {32.0f, 64.0f, 200.0f}) {
        std::vector<uint8_t> sharp_canvas;
        const auto pts = render_synthetic_face_canvas(sharp_canvas, kCanvas, kCanvas, face_px);
        const std::vector<uint8_t> blurry_canvas = blur_rgb_canvas(sharp_canvas, kCanvas, kCanvas, 3, 2);

        std::array<float, 6> affine{};
        check(Aligner::estimate_similarity_transform(pts, affine), "合成多尺度人脸相似变换求解应成功");
        const float det = affine_determinant(affine);

        std::vector<uint8_t> sharp_patch(kPatch * kPatch * 3, 0);
        std::vector<uint8_t> blurry_patch(kPatch * kPatch * 3, 0);
        zhulong_frame_view sharp_view = make_packed_view(
            sharp_canvas.data(), kCanvas, kCanvas, kCanvas * 3, ZHULONG_PIXEL_FORMAT_RGB24);
        zhulong_frame_view blurry_view = make_packed_view(
            const_cast<uint8_t*>(blurry_canvas.data()), kCanvas, kCanvas, kCanvas * 3, ZHULONG_PIXEL_FORMAT_RGB24);
        const zhulong::Image sharp_img(sharp_view);
        const zhulong::Image blurry_img(blurry_view);
        check(Aligner::crop_and_align_direct(sharp_img, affine, sharp_patch.data(), sharp_patch.size(), kPatch, kPatch),
              "清晰多尺度人脸对齐应成功");
        check(Aligner::crop_and_align_direct(blurry_img, affine, blurry_patch.data(), blurry_patch.size(), kPatch, kPatch),
              "模糊多尺度人脸对齐应成功");

        FaceImageQualityMetrics sharp_m, blurry_m;
        const bool sharp_pass = passes_face_image_gate(sharp_patch.data(), kPatch, kPatch, det, gate_on, &sharp_m);
        (void)passes_face_image_gate(blurry_patch.data(), kPatch, kPatch, det, gate_on, &blurry_m);

        check(sharp_m.valid && blurry_m.valid, "多尺度对齐块指标计算应有效");
        check(sharp_m.sharpness > blurry_m.sharpness * 2.0f,
              std::string("源尺度 ") + std::to_string(static_cast<int>(face_px)) +
                  "px 下清晰脸清晰度必须显著高于 PSF 模糊脸");
        check(sharp_pass,
              std::string("源尺度 ") + std::to_string(static_cast<int>(face_px)) +
                  "px 的清晰人脸必须通过图像质量门禁");

        if (face_px > 112.0f) {
            // 200px 近景大脸 |det| ≈ 0.314 < 1.0：max(1, |det|) 钳制为 1.0，不得折减清晰度
            check(std::abs(det) < 1.0f && nearly_equal(sharp_m.sharpness, sharp_m.raw_laplacian_variance, 1e-4f),
                  "近景 >=112px 大脸 (|det|<1) 不得被下采样行列式折减清晰度");
        } else {
            // 32px / 64px 远距小脸 |det| > 1.0：乘以 |det| 补偿上采样稀释
            check(std::abs(det) > 1.0f &&
                      nearly_equal(sharp_m.sharpness, sharp_m.raw_laplacian_variance * std::abs(det), 1e-2f),
                  "远距 <112px 小脸必须按 |det(affine)| 补偿双线性上采样衰减");
        }
    }

    // 2) 曝光边界归属（严格不等：Y==32 非欠曝，Y==224 非过曝；仅统计跳过 1px 边框的内部像素）
    constexpr int kSmall = 6; // 内部像素 4×4 = 16
    auto make_solid_patch = [](uint8_t v) {
        return std::vector<uint8_t>(static_cast<size_t>(kSmall * kSmall * 3), v);
    };

    const auto gray_32 = assess_face_image_quality(make_solid_patch(32).data(), kSmall, kSmall, 1.0f);
    check(gray_32.valid && gray_32.underexposed_ratio == 0.0f && gray_32.overexposed_ratio == 0.0f &&
              gray_32.raw_laplacian_variance == 0.0f && gray_32.sharpness == 0.0f,
          "Y==32 边界像素必须归为正常（非欠曝），且均匀色块方差精确非负为 0");

    const auto gray_31 = assess_face_image_quality(make_solid_patch(31).data(), kSmall, kSmall, 1.0f);
    check(nearly_equal(gray_31.underexposed_ratio, 1.0f, 1e-6f), "Y==31 像素必须计入欠曝");

    const auto gray_224 = assess_face_image_quality(make_solid_patch(224).data(), kSmall, kSmall, 1.0f);
    check(gray_224.valid && gray_224.overexposed_ratio == 0.0f && gray_224.underexposed_ratio == 0.0f,
          "Y==224 边界像素必须归为正常（非过曝）");

    const auto gray_225 = assess_face_image_quality(make_solid_patch(225).data(), kSmall, kSmall, 1.0f);
    check(nearly_equal(gray_225.overexposed_ratio, 1.0f, 1e-6f), "Y==225 像素必须计入过曝");

    // 1px 外边框为纯黑/纯白、内部 4×4 中间各置 4 个暗点与 2 个亮点 → 比例精确为 4/16=0.25 与 2/16=0.125
    std::vector<uint8_t> mixed_patch(kSmall * kSmall * 3, 128);
    for (int y = 0; y < kSmall; ++y) {
        for (int x = 0; x < kSmall; ++x) {
            if (x == 0 || y == 0 || x == kSmall - 1 || y == kSmall - 1) {
                const size_t off = static_cast<size_t>(y * kSmall + x) * 3;
                const uint8_t v = ((x + y) % 2 == 0) ? 0 : 255;
                mixed_patch[off + 0] = mixed_patch[off + 1] = mixed_patch[off + 2] = v;
            }
        }
    }
    for (int i = 1; i <= 4; ++i) {
        const size_t off = static_cast<size_t>(1 * kSmall + i) * 3;
        mixed_patch[off + 0] = mixed_patch[off + 1] = mixed_patch[off + 2] = 10; // 4 个内部欠曝像素
    }
    for (int i = 1; i <= 2; ++i) {
        const size_t off = static_cast<size_t>(2 * kSmall + i) * 3;
        mixed_patch[off + 0] = mixed_patch[off + 1] = mixed_patch[off + 2] = 240; // 2 个内部过曝像素
    }
    const auto mixed_m = assess_face_image_quality(mixed_patch.data(), kSmall, kSmall, 1.0f);
    check(nearly_equal(mixed_m.underexposed_ratio, 0.25f, 1e-6f) &&
              nearly_equal(mixed_m.overexposed_ratio, 0.125f, 1e-6f),
          "曝光统计必须只计内部像素，1px 边框不得污染比例");

    // 3) 门禁开关语义与异常入参防卫
    const auto black_patch = make_solid_patch(0);
    MatchParams gate_off;
    gate_off.enable_face_image_gate = false;
    check(passes_face_image_gate(black_patch.data(), kSmall, kSmall, 1.0f, gate_off),
          "enable_face_image_gate=false 时必须无条件放行，不改变原决策");
    check(passes_face_image_gate(nullptr, 0, 0, 0.0f, gate_off),
          "开关关闭时即便未传脸块也直接短路放行");

    check(!passes_face_image_gate(black_patch.data(), kSmall, kSmall, 1.0f, gate_on),
          "开启图像门禁后纯黑无纹理脸块必须被拦截");

    // max 类阈值设为 0.0 时是最严门禁（不是关闭哨兵）：哪怕仅有 12.5% 过曝也必须拒绝
    MatchParams strict_over = gate_on;
    strict_over.min_face_sharpness = 0.0f;
    strict_over.max_face_underexposure = 1.0f;
    strict_over.max_face_overexposure = 0.0f;
    check(!passes_face_image_gate(mixed_patch.data(), kSmall, kSmall, 1.0f, strict_over),
          "max_face_overexposure=0.0 必须作为最严上限拦截任何过曝内部像素");

    check(!assess_face_image_quality(nullptr, kSmall, kSmall, 1.0f).valid, "空缓冲必须返回 invalid");
    check(!assess_face_image_quality(mixed_patch.data(), 2, kSmall, 1.0f).valid, "宽 <3 必须返回 invalid");
    check(!assess_face_image_quality(mixed_patch.data(), kSmall, 2, 1.0f).valid, "高 <3 必须返回 invalid");
    check(!assess_face_image_quality(mixed_patch.data(), kSmall, kSmall, 0.0f).valid, "零行列式必须返回 invalid");
    check(!assess_face_image_quality(mixed_patch.data(), kSmall, kSmall, std::nanf("")).valid, "NaN 行列式必须返回 invalid");
    check(!assess_face_image_quality(mixed_patch.data(), kSmall, kSmall, std::numeric_limits<float>::infinity()).valid,
          "Inf 行列式必须返回 invalid");

    // 门限参数非法（NaN/负值）时**放行**：配置越界已由 resolve_match_params 兜住，
    // 这里再 fail-closed 只会让「一张脸都不出」，而线上看不出根因。统计量仍留 valid=false 供诊断。
    MatchParams nan_gate = gate_on;
    nan_gate.min_face_sharpness = std::nanf("");
    FaceImageQualityMetrics nan_metrics;
    check(passes_face_image_gate(mixed_patch.data(), kSmall, kSmall, 1.0f, nan_gate, &nan_metrics),
          "含 NaN 阈值的图像门禁必须放行（不得因配置笔误整路静默无人脸）");
    check(!nan_metrics.valid, "参数非法时输出的统计量必须标记 invalid，便于调用侧诊断");

    MatchParams negative_gate = gate_on;
    negative_gate.max_face_overexposure = -0.1f;
    check(passes_face_image_gate(mixed_patch.data(), kSmall, kSmall, 1.0f, negative_gate),
          "负值门限同样必须放行");

    // 门限参数非法不改变「对齐块本身非法必须拒绝」的语义
    check(!passes_face_image_gate(nullptr, kSmall, kSmall, 1.0f, nan_gate),
          "参数非法但缓冲为空时仍必须拒绝（那是数据问题，不是配置问题）");
}

// ============================================================================
// 10. 身份级聚合、两层确定性 tie-break、min_match_margin、非有限防卫与准入联动
// ============================================================================

void test_matcher_person_aggregation_and_margin() {
    using namespace zhulong::face;
    const Matcher matcher;
    const float probe[] = {1.0f, 0.0f, 0.0f};

    // 1) 同人多模板去重聚合：每人只占一个 Top-K 名额，并携带该人员最高分照片 face_id
    std::vector<zhulong::FaceTemplate> multi_tpl_gallery;
    multi_tpl_gallery.push_back(make_template("f1_mid", "p1", {0.875f, 0.4841229f, 0.0f}));
    multi_tpl_gallery.push_back(make_template("f1_best", "p1", {0.9375f, 0.3479853f, 0.0f}));
    multi_tpl_gallery.push_back(make_template("f1_low", "p1", {0.75f, 0.6614378f, 0.0f}));
    multi_tpl_gallery.push_back(make_template("f2_low", "p2", {0.50f, 0.8660254f, 0.0f}));
    multi_tpl_gallery.push_back(make_template("f2_best", "p2", {0.625f, 0.7806247f, 0.0f}));
    multi_tpl_gallery.push_back(make_template("f3_only", "p3", {0.25f, 0.9682458f, 0.0f}));

    const MatchResult agg = matcher.match(probe, 3, multi_tpl_gallery, MatchLimits{0.40f, 3, 0.0f});
    check(agg.matched && agg.person_id == "p1" && agg.matched_face_id == "f1_best" &&
              nearly_equal(agg.similarity, 0.9375f, 1e-6f),
          "Top-1 必须命中 p1 及其最高分模板 f1_best");
    check(agg.candidates.size() == 3, "同人 3 张模板不得挤占 Top-3 的其他人员名额");
    if (agg.candidates.size() == 3) {
        check(agg.candidates[0].person_id == "p1" && agg.candidates[0].face_id == "f1_best" &&
                  agg.candidates[0].person_name == "p1-name",
              "第 1 候选人代表照与元数据应为 p1 / f1_best");
        check(agg.candidates[1].person_id == "p2" && agg.candidates[1].face_id == "f2_best" &&
                  nearly_equal(agg.candidates[1].similarity, 0.625f, 1e-6f),
              "第 2 候选人应为 p2 / f2_best");
        check(agg.candidates[2].person_id == "p3" && agg.candidates[2].face_id == "f3_only",
              "第 3 候选人应为 p3 / f3_only");
    }

    // 2) 两层确定性 tie-break（对齐宿主 SearchByImage）+ 空 person_id 畸形模板跳过
    //    - 同人等分取较小 face_id（与槽位顺序无关）
    //    - 跨人等分取较小 person_id（即使较小 person_id 的代表 face_id 字典序更大）
    std::vector<zhulong::FaceTemplate> tie_gallery;
    tie_gallery.push_back(make_template("f_orphan", "", {0.99f, 0.141067f, 0.0f})); // 空 person_id 必须跳过
    tie_gallery.push_back(make_template("f_00", "p_beta", {0.75f, 0.6614378f, 0.0f}));
    tie_gallery.push_back(make_template("f_99_late", "p_alpha", {0.75f, 0.6614378f, 0.0f}));
    tie_gallery.push_back(make_template("f_88_early", "p_alpha", {0.75f, 0.6614378f, 0.0f}));
    tie_gallery.push_back(make_template("f_neg_z", "p_zeta", {-0.8f, 0.6f, 0.0f}));  // 截断为 0.0
    tie_gallery.push_back(make_template("f_neg_g", "p_gamma", {-0.5f, 0.866f, 0.0f})); // 截断为 0.0

    const MatchResult tie_res = matcher.match(probe, 3, tie_gallery, MatchLimits{0.40f, 4, 0.0f});
    check(tie_res.matched && tie_res.person_id == "p_alpha" && tie_res.matched_face_id == "f_88_early",
          "空 person_id 必须被跳过，跨人等分选较小 person_id (p_alpha)，同人等分选较小 face_id (f_88_early)");
    check(tie_res.candidates.size() == 4, "有效唯一身份数应为 4（空 person_id 不入列）");
    if (tie_res.candidates.size() == 4) {
        check(tie_res.candidates[0].person_id == "p_alpha" && tie_res.candidates[0].face_id == "f_88_early" &&
                  tie_res.candidates[1].person_id == "p_beta" && tie_res.candidates[1].face_id == "f_00" &&
                  tie_res.candidates[2].person_id == "p_gamma" &&
                  tie_res.candidates[3].person_id == "p_zeta",
              "等分（含负内积同截断至 0.0）必须按 person_id 升序稳定排列");
    }

    // 3) min_match_margin 边界（二进制精确值 0.75 - 0.50 = 0.25）与 top_k=0/1 解耦
    std::vector<zhulong::FaceTemplate> margin_gallery;
    // 同人 p1 有两张高分模板 (0.75 与 0.74)，不得把自己当成竞争身份！第二身份是 p2 (0.50)
    margin_gallery.push_back(make_template("f1_a", "p1", {0.75f, 0.6614378f, 0.0f}));
    margin_gallery.push_back(make_template("f1_b", "p1", {0.74f, 0.6726069f, 0.0f}));
    margin_gallery.push_back(make_template("f2_a", "p2", {0.50f, 0.8660254f, 0.0f}));

    check(matcher.match(probe, 3, margin_gallery, MatchLimits{0.40f, 3, 0.25f}).matched,
          "分差等于 min_match_margin (0.75 - 0.50 == 0.25) 必须命中 (>= 语义，且同人第二张 0.74 不参与竞争)");

    const MatchResult margin_reject = matcher.match(probe, 3, margin_gallery, MatchLimits{0.40f, 3, 0.26f});
    check(!margin_reject.matched && margin_reject.person_id.empty() &&
              margin_reject.person_name.empty() && margin_reject.matched_face_id.empty(),
          "不同身份分差不足 (0.25 < 0.26) 必须拒识且清空确认身份字段");
    check(nearly_equal(margin_reject.similarity, 0.75f, 1e-6f) && margin_reject.candidates.size() == 2,
          "分差拒识时仍须保留最高相似度与唯一身份候选证据");

    // 单一有效身份底库（即使有多张照片）不受 min_match_margin 阻断
    std::vector<zhulong::FaceTemplate> single_person_gallery{
        make_template("f1_a", "p1", {0.75f, 0.6614378f, 0.0f}),
        make_template("f1_b", "p1", {0.70f, 0.7141428f, 0.0f}),
    };
    check(matcher.match(probe, 3, single_person_gallery, MatchLimits{0.40f, 3, 0.50f}).matched,
          "底库仅含单一身份时开启 min_match_margin 仍应按阈值正常命中");

    // 同分不同身份在开启分差时必须拒识
    check(!matcher.match(probe, 3, tie_gallery, MatchLimits{0.40f, 3, 0.01f}).matched,
          "不同身份最高分相等 (margin=0) 且开启 min_match_margin 时必须拒识");

    // top_k = 1 与 top_k = 0 不得截断第二身份分差计算
    const MatchResult top1_reject = matcher.match(probe, 3, margin_gallery, MatchLimits{0.40f, 1, 0.30f});
    check(!top1_reject.matched && top1_reject.candidates.size() == 1 &&
              top1_reject.candidates[0].person_id == "p1",
          "top_k=1 时仍必须正确获取第二身份分差并拒识，同时只返回 1 个候选人");

    const MatchResult top0_reject = matcher.match(probe, 3, margin_gallery, MatchLimits{0.40f, 0, 0.30f});
    const MatchResult top0_pass = matcher.match(probe, 3, margin_gallery, MatchLimits{0.40f, 0, 0.20f});
    check(!top0_reject.matched && top0_reject.candidates.empty(),
          "top_k=0 时仍必须执行第二身份分差拒识且候选人列表为空");
    check(top0_pass.matched && top0_pass.candidates.empty() && top0_pass.person_id == "p1",
          "top_k=0 时分差达标仍应正常命中");

    // 4) 非有限探针、模板与门限防卫（含 0.0f * Inf = NaN 交叉项）
    const float nan = std::nanf("");
    const float inf = std::numeric_limits<float>::infinity();
    const float nan_probe[] = {1.0f, nan, 0.0f};
    const float inf_probe[] = {inf, 0.0f, 0.0f};
    check(!matcher.match(nan_probe, 3, margin_gallery,
                         MatchLimits{0.40f, 3, 0.0f}).matched, "NaN 探针必须返回空未匹配");
    check(!matcher.match(inf_probe, 3, margin_gallery,
                         MatchLimits{0.40f, 3, 0.0f}).matched, "Inf 探针必须返回空未匹配");
    check(!matcher.match(probe, 3, margin_gallery,
                         MatchLimits{nan, 3, 0.0f}).matched, "NaN threshold 必须返回空未匹配");
    check(!matcher.match(probe, 3, margin_gallery,
                         MatchLimits{0.40f, 3, nan}).matched, "NaN min_match_margin 必须返回空未匹配");

    std::vector<zhulong::FaceTemplate> dirty_gallery = margin_gallery;
    dirty_gallery.push_back(make_template("f_nan", "p_nan", {nan, 0.0f, 0.0f}));
    dirty_gallery.push_back(make_template("f_zero_times_inf", "p_inf0", {0.9f, inf, 0.0f})); // probe[1]=0 → 0*inf=NaN
    dirty_gallery.push_back(make_template("f_pos_inf", "p_inf1", {inf, 0.0f, 0.0f}));        // probe[0]=1 → +inf
    const MatchResult clean_after_dirty = matcher.match(probe, 3, dirty_gallery,
                                                        MatchLimits{0.40f, 5, 0.25f});
    check(clean_after_dirty.matched && clean_after_dirty.person_id == "p1" &&
              clean_after_dirty.candidates.size() == 2,
          "含 NaN / ±Inf 的畸形模板必须被跳过，不得破坏排序或污染候选");

    // 5) 通过共用产品函数 should_emit_face_result 钉住 filter_unmatched × min_match_margin 组合语义
    MatchParams keep_unmatched;
    keep_unmatched.filter_unmatched = false;
    keep_unmatched.min_match_margin = 0.26f;

    MatchParams drop_unmatched = keep_unmatched;
    drop_unmatched.filter_unmatched = true;

    check(should_emit_face_result(margin_reject.matched, keep_unmatched),
          "filter_unmatched=false 时，因分差拒识的目标仍应输出抓拍与候选证据");
    check(!should_emit_face_result(margin_reject.matched, drop_unmatched),
          "filter_unmatched=true 且因 min_match_margin 拒识时，疑似目标必须经 should_emit_face_result 整体丢弃");
    check(should_emit_face_result(agg.matched, drop_unmatched),
          "filter_unmatched=true 时，通过分差校验的已确认身份必须正常输出");
}

// ============================================================================
// 11. SCRFD pre/post-NMS 预算分离、重复 anchor 拥挤召回与等分扫描序确定性
// ============================================================================

void test_scrfd_decode_crowded_budget_and_determinism() {
    using namespace zhulong::face;

    // 16×16 网格 × stride 8 × 2 anchors = 512 个候选位置（模型 128×128）
    constexpr int kModel = 128;
    constexpr int kAnchors = 2;
    constexpr size_t kGrid = kModel / 8; // 16
    constexpr size_t kTotal = kGrid * kGrid * kAnchors; // 512
    const std::array<int, 1> strides{8};

    // 1) 重复 anchor 拥挤召回：目标 A 在左上角产生 160 个高度重叠的高分重复框 (0.90~0.99)，
    //    目标 B (0.85) 与目标 C (0.82) 位于互不重叠的其它区域。
    std::vector<float> scores(kTotal, 0.0f);
    std::vector<float> boxes(kTotal * 4, 0.0f);
    std::vector<float> kps(kTotal * 10, 0.0f);

    // 把前 160 个 anchor 全部回调到同一像素框 [8, 8, 24, 24]（模拟单张近景清晰脸触发大量重叠高分 anchor）
    for (size_t idx = 0; idx < 160; ++idx) {
        const size_t cell = idx / kAnchors;
        const float cx = static_cast<float>((cell % kGrid) * 8);
        const float cy = static_cast<float>((cell / kGrid) * 8);
        scores[idx] = 0.90f + 0.0005f * static_cast<float>(160 - idx);
        boxes[idx * 4 + 0] = (cx - 8.0f) / 8.0f;
        boxes[idx * 4 + 1] = (cy - 8.0f) / 8.0f;
        boxes[idx * 4 + 2] = (24.0f - cx) / 8.0f;
        boxes[idx * 4 + 3] = (24.0f - cy) / 8.0f;
    }

    // 目标 B：格子 (h=12, w=4, a=0) → 中心 (32, 96)，框 [28, 92, 36, 100]
    const size_t idx_b = (12 * kGrid + 4) * kAnchors + 0;
    scores[idx_b] = 0.85f;
    boxes[idx_b * 4 + 0] = boxes[idx_b * 4 + 1] = boxes[idx_b * 4 + 2] = boxes[idx_b * 4 + 3] = 0.5f;

    // 目标 C：格子 (h=12, w=12, a=0) → 中心 (96, 96)，框 [92, 92, 100, 100]
    const size_t idx_c = (12 * kGrid + 12) * kAnchors + 0;
    scores[idx_c] = 0.82f;
    boxes[idx_c * 4 + 0] = boxes[idx_c * 4 + 1] = boxes[idx_c * 4 + 2] = boxes[idx_c * 4 + 3] = 0.5f;

    const auto packed = pack_outputs({scores, boxes, kps});

    // 旧单一 128 预算：B/C 在 NMS 前就被 A 的 160 个重复 anchor 挤出堆外，只剩 1 人
    const auto legacy_128 = decode_scrfd_candidates(packed, strides, kAnchors, kModel, kModel, 0.5f, 0.4f,
                                                    ScrfdDecodeLimits{128, 128},
                                                    ScrfdLetterbox{1.0f, 0, 0, kModel, kModel});
    check(legacy_128.size() == 1, "对照基线：pre-NMS 预算仅 128 时重复 anchor 确实会挤掉其余人脸");

    // 分离预算（ONNX 512/128 与 RK3568 256/128）：A 的重复框在 NMS 中合并，B 与 C 完整召回
    const auto onnx_budget = decode_scrfd_candidates(packed, strides, kAnchors, kModel, kModel, 0.5f, 0.4f,
                                                     ScrfdDecodeLimits{512, 128},
                                                     ScrfdLetterbox{1.0f, 0, 0, kModel, kModel});
    const auto rk_budget = decode_scrfd_candidates(packed, strides, kAnchors, kModel, kModel, 0.5f, 0.4f,
                                                   ScrfdDecodeLimits{256, 128},
                                                   ScrfdLetterbox{1.0f, 0, 0, kModel, kModel});
    check(onnx_budget.size() == 3 && rk_budget.size() == 3,
          "提升 pre-NMS 预算后（512 与 256），重复 anchor 不再挤掉场景内其他非重叠人脸");

    // 2) post-NMS 目标硬上限（控制下游特征提取次数）与零预算保护
    const auto capped_post = decode_scrfd_candidates(packed, strides, kAnchors, kModel, kModel, 0.5f, 0.4f,
                                                     ScrfdDecodeLimits{512, 2},
                                                     ScrfdLetterbox{1.0f, 0, 0, kModel, kModel});
    check(capped_post.size() == 2 && nearly_equal(capped_post[1].score, 0.85f, 1e-6f),
          "post-NMS 上限必须按排序精确截断为前 max_detections 个独立目标");
    check(decode_scrfd_candidates(packed, strides, kAnchors, kModel, kModel, 0.5f, 0.4f,
                                  ScrfdDecodeLimits{0, 128},
                                  ScrfdLetterbox{1.0f, 0, 0, kModel, kModel})
              .empty(),
          "max_pre_nms_candidates=0 必须返回空");
    check(decode_scrfd_candidates(packed, strides, kAnchors, kModel, kModel, 0.5f, 0.4f,
                                  ScrfdDecodeLimits{512, 0},
                                  ScrfdLetterbox{1.0f, 0, 0, kModel, kModel})
              .empty(),
          "max_detections=0 必须返回空");

    // 3) 超 pre-NMS 预算的等分候选确定性：小顶堆淘汰与最终排序同时遵守 seq 升序（保留更早扫到的格子）
    std::vector<float> eq_scores(kTotal, 0.0f);
    std::vector<float> eq_boxes(kTotal * 4, 0.0f);
    std::vector<float> eq_kps(kTotal * 10, 0.0f);
    size_t placed = 0;
    for (size_t h = 0; h < kGrid; h += 2) {
        for (size_t w = 0; w < kGrid; w += 2) {
            // 每隔 2 格放一个 4×4 小框，彼此零重叠，且分数完全相等为 0.75f（共 64 个）
            const size_t idx = (h * kGrid + w) * kAnchors;
            eq_scores[idx] = 0.75f;
            eq_boxes[idx * 4 + 0] = eq_boxes[idx * 4 + 1] = eq_boxes[idx * 4 + 2] = eq_boxes[idx * 4 + 3] = 0.25f;
            ++placed;
        }
    }
    check(placed == 64, "测试夹具应放置 64 个非重叠等分目标");

    const auto eq_packed = pack_outputs({eq_scores, eq_boxes, eq_kps});
    const auto run1 = decode_scrfd_candidates(eq_packed, strides, kAnchors, kModel, kModel, 0.5f, 0.4f,
                                              ScrfdDecodeLimits{10, 6},
                                              ScrfdLetterbox{1.0f, 0, 0, kModel, kModel});
    const auto run2 = decode_scrfd_candidates(eq_packed, strides, kAnchors, kModel, kModel, 0.5f, 0.4f,
                                              ScrfdDecodeLimits{10, 6},
                                              ScrfdLetterbox{1.0f, 0, 0, kModel, kModel});

    check(run1.size() == 6 && run2.size() == 6, "等分超预算解码应精确输出 max_detections=6 条");
    bool exact_early_seq = (run1.size() == 6 && run2.size() == 6);
    for (size_t i = 0; i < run1.size() && exact_early_seq; ++i) {
        if (run1[i].seq != static_cast<uint32_t>(i) ||
            run1[i].seq != run2[i].seq ||
            run1[i].box != run2[i].box ||
            run1[i].score != run2[i].score) {
            exact_early_seq = false;
        }
    }
    check(exact_early_seq, "超 pre-NMS 预算的等分候选必须确定性保留最早扫描序号 (seq=0..5) 且多次运行一致");
}

// ============================================================================
// 12. 跨帧特征状态池融合、同帧互斥、锚点防漂移、TTL/容量淘汰与多路隔离
// ============================================================================

void test_face_feature_fusion() {
    using namespace zhulong::face;
    using Clock = std::chrono::steady_clock;

    auto make_basis_512 = [](size_t idx) {
        std::array<float, kFusionFeatureDim> v{};
        v[idx % kFusionFeatureDim] = 1.0f;
        return v;
    };

    auto l2_norm_512 = [](const std::array<float, kFusionFeatureDim>& v) {
        double sum = 0.0;
        for (float x : v) {
            sum += static_cast<double>(x) * static_cast<double>(x);
        }
        return static_cast<float>(std::sqrt(sum));
    };

    const Clock::time_point t0 = Clock::now();
    MatchParams params; // 默认 enable=true, sim_threshold=0.62, ttl=3.0s, threshold=0.40
    Matcher matcher;

    // 1) 单帧均低于阈值 (0.384 < 0.40) 的两次互补视角同人观测，融合后跨过 0.40 命中底库
    {
        std::vector<float> gallery_vec(kFusionFeatureDim, 0.0f);
        gallery_vec[0] = 0.60f;
        gallery_vec[1] = 0.80f; // 单位模长: 0.36 + 0.64 = 1.0
        std::vector<zhulong::FaceTemplate> gallery{
            {"face_alice", "person_alice", "Alice", "alice.jpg", gallery_vec}
        };

        std::array<float, kFusionFeatureDim> obs1{};
        obs1[0] = 0.64f;
        obs1[2] = std::sqrt(1.0f - 0.64f * 0.64f); // 与 Alice 余弦 = 0.64 * 0.60 = 0.384 < 0.40

        std::array<float, kFusionFeatureDim> obs2{};
        obs2[1] = 0.48f;
        obs2[2] = std::sqrt(1.0f - 0.48f * 0.48f); // 与 Alice 余弦 = 0.48 * 0.80 = 0.384 < 0.40
        // obs1·obs2 = obs1[2]*obs2[2] ≈ 0.7684 * 0.8773 = 0.6741 >= 0.62

        check(!matcher.match(obs1.data(), kFusionFeatureDim, gallery, MatchLimits{0.40f, 3}).matched,
              "第 1 帧单帧特征 (0.384) 低于 0.40，单独比对不应命中");
        check(!matcher.match(obs2.data(), kFusionFeatureDim, gallery, MatchLimits{0.40f, 3}).matched,
              "第 2 帧单帧特征 (0.384) 低于 0.40，单独比对不应命中");

        StreamFeatureFusionPool pool;
        const auto f1 = pool.fuse_frame({FusionFaceObservation{&obs1, 0.70f}}, params, t0);
        check(f1.size() == 1 && !f1[0].is_fused, "首帧入池应新建状态且 is_fused=false");
        check(nearly_equal(l2_norm_512(f1[0].features), 1.0f, 1e-5f), "首帧输出必须为单位向量");

        const auto f2 = pool.fuse_frame(
            {FusionFaceObservation{&obs2, 0.70f}}, params, t0 + std::chrono::milliseconds(200));
        check(f2.size() == 1 && f2[0].is_fused && pool.state_count() == 1,
              "第 2 帧同人特征 (>=0.62) 应并入原状态并置 is_fused=true");
        check(nearly_equal(l2_norm_512(f2[0].features), 1.0f, 1e-5f),
              "融合后特征必须严格重做 L2 单位归一化");

        const MatchResult fused_match = matcher.match(f2[0].features.data(), kFusionFeatureDim, gallery,
                                                      MatchLimits{0.40f, 3});
        check(fused_match.matched && fused_match.person_id == "person_alice" && fused_match.similarity > 0.41f,
              "跨帧质量加权融合后相似度 (~0.420) 应成功跨过 0.40 命中 Alice");
    }

    // 2) 同帧双脸一对一互斥配对 + 更高相似度候选优先夺得已有状态
    {
        StreamFeatureFusionPool pool;
        const auto anchor = make_basis_512(0);
        pool.fuse_frame({FusionFaceObservation{&anchor, 0.80f}}, params, t0);

        // 刻意把相似度较低的旁人 (0.70) 排在索引 0，真主 (0.95) 排在索引 1
        std::array<float, kFusionFeatureDim> lower_cand{};
        lower_cand[0] = 0.70f;
        lower_cand[1] = std::sqrt(1.0f - 0.70f * 0.70f);

        std::array<float, kFusionFeatureDim> true_owner{};
        true_owner[0] = 0.95f;
        true_owner[2] = std::sqrt(1.0f - 0.95f * 0.95f);

        const auto out = pool.fuse_frame(
            {
                FusionFaceObservation{&lower_cand, 0.75f},
                FusionFaceObservation{&true_owner, 0.85f},
            },
            params,
            t0 + std::chrono::milliseconds(100));
        check(out.size() == 2, "双脸帧应输出 2 条结果");
        check(!out[0].is_fused && out[1].is_fused && pool.state_count() == 2,
              "高相似真主 (0.95) 必须优先夺得已有状态 (is_fused=true)，同帧次相似人脸必须另建新状态 (is_fused=false)");
    }

    // 3) 融合特征 + 最高质量锚点双重门禁防漂移（阻断渐变过渡串入路人）
    {
        MatchParams strict_params = params;
        strict_params.fusion_similarity_threshold = 0.70f;

        StreamFeatureFusionPool pool;
        // 帧 1：高质量正脸锚点 (0°: [1, 0, ...], quality=0.95)
        std::array<float, kFusionFeatureDim> f0{};
        f0[0] = 1.0f;
        pool.fuse_frame({FusionFaceObservation{&f0, 0.95f}}, strict_params, t0);

        // 帧 2~4：侧转到 40° (cos40°≈0.766 >= 0.70，允许融合，把 fused 均值拉向 ~32°；但 quality=0.80 < 0.95 不替换锚点)
        std::array<float, kFusionFeatureDim> f40{};
        f40[0] = std::cos(40.0f * 3.14159265f / 180.0f);
        f40[1] = std::sin(40.0f * 3.14159265f / 180.0f);
        for (int step = 1; step <= 3; ++step) {
            const auto r = pool.fuse_frame(
                {FusionFaceObservation{&f40, 0.80f}},
                strict_params,
                t0 + std::chrono::milliseconds(100 * step));
            check(r[0].is_fused, "与锚点余弦 0.766 >= 0.70 的同人侧脸应正常融合");
        }

        // 帧 5：60° 异人目标 —— 与拉偏后的 fused 均值夹角约 28° (cos≈0.88 > 0.70)，但与 0° 锚点余弦仅 cos60°=0.50 < 0.70
        std::array<float, kFusionFeatureDim> stranger60{};
        stranger60[0] = 0.50f;
        stranger60[1] = std::sqrt(1.0f - 0.50f * 0.50f);
        const auto drift_check = pool.fuse_frame(
            {FusionFaceObservation{&stranger60, 0.85f}},
            strict_params,
            t0 + std::chrono::milliseconds(500));
        check(!drift_check[0].is_fused && pool.state_count() == 2,
              "偏离最高质量锚点的目标必须被双重门禁拦截，绝不允许顺着融合均值漂移串人");
    }

    // 4) 单路 32 容量上限淘汰、TTL 超时淘汰、时钟回退防护与开关动态关闭清池
    {
        StreamFeatureFusionPool pool;
        for (size_t i = 0; i < kMaxActiveStatesPerStream; ++i) {
            const auto v = make_basis_512(i);
            const auto r = pool.fuse_frame(
                {FusionFaceObservation{&v, 0.60f}},
                params,
                t0 + std::chrono::milliseconds(static_cast<int>(i) * 10));
            check(!r[0].is_fused, "正交基向量应各自新建状态");
        }
        check(pool.state_count() == kMaxActiveStatesPerStream, "状态池应精确达到 32 条上限");

        // 第 33 个目标到达：淘汰最早的 idx=0，保留 idx=1
        const auto v32 = make_basis_512(kMaxActiveStatesPerStream);
        pool.fuse_frame({FusionFaceObservation{&v32, 0.60f}}, params, t0 + std::chrono::milliseconds(400));
        check(pool.state_count() == kMaxActiveStatesPerStream, "超出上限后状态池大小仍不得超过 32");

        const auto v1 = make_basis_512(1);
        const auto hit_v1 = pool.fuse_frame(
            {FusionFaceObservation{&v1, 0.70f}}, params, t0 + std::chrono::milliseconds(410));
        check(hit_v1[0].is_fused, "未被容量淘汰的较新状态 (idx=1) 仍应正常命中融合");

        const auto v0 = make_basis_512(0);
        const auto miss_v0 = pool.fuse_frame(
            {FusionFaceObservation{&v0, 0.70f}}, params, t0 + std::chrono::milliseconds(420));
        check(!miss_v0[0].is_fused, "最久未命中的状态 (idx=0) 应已被容量上限淘汰");

        // 时钟异常轻微回退不得崩溃或误删活跃状态
        const auto back_clock = pool.fuse_frame(
            {FusionFaceObservation{&v0, 0.75f}}, params, t0 + std::chrono::milliseconds(415));
        check(back_clock[0].is_fused, "时钟轻微回退时应钳位保护已有状态并继续融合");

        // 超过 TTL (3.0s) 后全部过期重来
        const auto after_ttl = pool.fuse_frame(
            {FusionFaceObservation{&v0, 0.80f}}, params, t0 + std::chrono::milliseconds(3600));
        check(!after_ttl[0].is_fused && pool.state_count() == 1,
              "超过 fusion_ttl_seconds (3.0s) 未观测后旧状态必须全部过期淘汰");

        // 动态关闭 enable_face_feature_fusion：立即清池且直通单帧
        MatchParams disabled = params;
        disabled.enable_face_feature_fusion = false;
        const auto passthrough = pool.fuse_frame(
            {FusionFaceObservation{&v0, 0.80f}}, disabled, t0 + std::chrono::milliseconds(3700));
        check(!passthrough[0].is_fused && pool.state_count() == 0,
              "关闭 enable_face_feature_fusion 时必须清空池状态并输出 is_fused=false");
    }

    // 5) 多路 task_id 隔离与畸形向量 (NaN / 全零) 防卫
    {
        TaskFeatureFusionManager mgr;
        const auto v = make_basis_512(7);

        const auto s1 = mgr.fuse_frame(101, {FusionFaceObservation{&v, 0.80f}}, params, t0);
        const auto s2 = mgr.fuse_frame(202, {FusionFaceObservation{&v, 0.80f}}, params, t0);
        check(!s1[0].is_fused && !s2[0].is_fused && mgr.stream_count() == 2,
              "不同 task_id 的同人特征必须严格按流隔离，互不跨流融合");

        const auto s1_next = mgr.fuse_frame(
            101, {FusionFaceObservation{&v, 0.85f}}, params, t0 + std::chrono::milliseconds(100));
        check(s1_next[0].is_fused && mgr.state_count_for(101) == 1 && mgr.state_count_for(202) == 1,
              "同一 task_id 的后续帧应正常融合且不影响其他流");

        // NaN 与全零特征不得入池污染
        std::array<float, kFusionFeatureDim> nan_vec = v;
        nan_vec[3] = std::nanf("");
        std::array<float, kFusionFeatureDim> zero_vec{};
        const auto bad = mgr.fuse_frame(
            101,
            {
                FusionFaceObservation{&nan_vec, 0.90f},
                FusionFaceObservation{&zero_vec, 0.90f},
                FusionFaceObservation{nullptr, 0.90f},
            },
            params,
            t0 + std::chrono::milliseconds(150));
        check(bad.size() == 3 && !bad[0].is_fused && !bad[1].is_fused && !bad[2].is_fused &&
                  mgr.state_count_for(101) == 1,
              "NaN、全零或空指针观测必须安全拒绝入池，不得破坏已有正常状态");

        // 空观测下动态关闭开关同样清掉对应 task_id 的池
        MatchParams disabled = params;
        disabled.enable_face_feature_fusion = false;
        mgr.fuse_frame(101, {}, disabled, t0 + std::chrono::milliseconds(200));
        check(mgr.state_count_for(101) == 0 && mgr.state_count_for(202) == 1,
              "关闭某路融合开关只清空该 task_id 状态池，不波及其他视频流");
    }

    // 6) 无效观测必须显式标记 valid=false，不能悄悄输出零向量
    //    （零向量送进比对只会得到「相似度 0 的伪候选人证据」，看起来像一次正常的未识别）
    {
        StreamFeatureFusionPool pool;
        const auto good = make_basis_512(3);
        std::array<float, kFusionFeatureDim> nan_vec = good;
        nan_vec[0] = std::nanf("");
        std::array<float, kFusionFeatureDim> zero_vec{};

        const auto out = pool.fuse_frame(
            {
                FusionFaceObservation{&good, 0.80f},
                FusionFaceObservation{&nan_vec, 0.80f},
                FusionFaceObservation{&zero_vec, 0.80f},
                FusionFaceObservation{nullptr, 0.80f},
            },
            params, t0);
        check(out.size() == 4 && out[0].valid, "首条有效观测必须标记 valid=true");
        check(!out[1].valid && !out[2].valid && !out[3].valid,
              "NaN / 全零 / 空指针观测必须标记 valid=false，调用侧据此整条丢弃");
        check(pool.state_count() == 1, "无效观测不得占用状态池名额");

        // 关闭融合的直通路径共用同一套可用性判定
        MatchParams disabled = params;
        disabled.enable_face_feature_fusion = false;
        const auto through = pool.fuse_frame(
            {FusionFaceObservation{&good, 0.80f}, FusionFaceObservation{nullptr, 0.80f}, FusionFaceObservation{&zero_vec, 0.80f}},
            disabled, t0 + std::chrono::milliseconds(10));
        check(through[0].valid && !through[1].valid && !through[2].valid,
              "关闭融合的直通路径同样必须标记 valid（直通不等于放弃校验）");
    }

    // 7) 池满时的腾位规则：优先淘汰本帧未命中的状态；全部在用则**不拆**任何状态
    //    （拆掉刚更新的状态会让该脸的 is_fused 每帧 1/0 抖动，融合永远攒不起来）
    {
        StreamFeatureFusionPool pool;
        std::vector<std::array<float, kFusionFeatureDim>> people;
        for (size_t i = 0; i < kMaxActiveStatesPerStream; ++i) {
            people.push_back(make_basis_512(i));
        }
        auto make_frame = [&people](bool include_stale) {
            std::vector<FusionFaceObservation> frame;
            frame.reserve(people.size());
            for (size_t i = 0; i < people.size(); ++i) {
                if (!include_stale && i == 5) {
                    continue; // 故意漏掉一个人，让它的状态这一帧变成「未被命中」
                }
                frame.push_back(FusionFaceObservation{&people[i], 0.80f});
            }
            return frame;
        };
        auto count_fused = [](const std::vector<FusedFaceOutput>& outputs) {
            return std::count_if(outputs.begin(), outputs.end(),
                                 [](const FusedFaceOutput& f) { return f.is_fused; });
        };

        auto t = t0;
        pool.fuse_frame(make_frame(true), params, t); // 建满 32 个状态
        t += std::chrono::milliseconds(40);
        const auto steady = pool.fuse_frame(make_frame(true), params, t);
        check(count_fused(steady) == static_cast<long>(kMaxActiveStatesPerStream),
              "32 人连续两帧必须全部完成融合");

        // 第二帧漏掉 people[5] → 它的状态本帧未被命中，新手来时应先摘它
        t += std::chrono::milliseconds(40);
        const auto stale = pool.fuse_frame(make_frame(false), params, t);
        check(pool.state_count() == kMaxActiveStatesPerStream, "池容量不变");
        check(count_fused(stale) == static_cast<long>(kMaxActiveStatesPerStream) - 1,
              "漏掉的那个人本帧新建状态尚未融合");

        const auto newcomer = make_basis_512(kMaxActiveStatesPerStream + 7);
        auto with_newcomer = make_frame(true);
        with_newcomer.push_back(FusionFaceObservation{&newcomer, 0.80f});
        t += std::chrono::milliseconds(40);
        const auto admitted = pool.fuse_frame(with_newcomer, params, t);
        check(admitted.size() == kMaxActiveStatesPerStream + 1 && admitted.back().valid,
              "第 33 张脸本帧照常输出（valid=true）");
        check(!admitted.back().is_fused, "新手首帧不可能已融合");
        check(pool.state_count() == kMaxActiveStatesPerStream, "容量上限仍然成立");
        check(count_fused(admitted) == static_cast<long>(kMaxActiveStatesPerStream),
              "本帧命中的 32 人全部保持 is_fused，不得有人因腾位被拆状态");

        // 再来一帧：此时全部 32 个状态都在用 → 新手继续不占位，且**没有任何状态被拆**
        t += std::chrono::milliseconds(40);
        const auto saturated = pool.fuse_frame(with_newcomer, params, t);
        check(!saturated.back().is_fused && pool.state_count() == kMaxActiveStatesPerStream,
              "池满且全部在用时应放弃为新面孔建状态，而不是拆掉刚更新的状态");
        check(count_fused(saturated) == static_cast<long>(kMaxActiveStatesPerStream),
              "饱和帧不得破坏任何既有状态的融合连续性");
    }
}

} // namespace

int main() {
    test_similarity_transform_recovers_known_geometry();
    test_similarity_transform_rejects_degenerate_input();
    test_crop_and_align_identity_preserves_pixels();
    test_crop_and_align_handles_yuv_gray();
    test_crop_and_align_rejects_bad_input();
    test_matcher_returns_expected_top_k();
    test_matcher_threshold_boundary_and_degenerate_input();
    test_matcher_person_aggregation_and_margin();
    test_face_quality_assessment();
    test_face_quality_invariance_and_topology();
    test_face_image_quality_and_gate();
    test_resolve_match_params();
    test_face_association();
    test_detector_dimensions();
    test_scrfd_decode_maps_letterbox_back_to_source();
    test_scrfd_decode_keeps_best_score_across_strides();
    test_scrfd_decode_nms_and_filters();
    test_scrfd_decode_rejects_bad_layout();
    test_scrfd_decode_crowded_budget_and_determinism();
    test_drop_unmatched_persons();
    test_face_feature_fusion();
#if defined(ZHULONG_FACE_HAS_PREPROCESS_HELPER)
    test_hwc_to_nchw_normalized();
#endif

    if (g_failures != 0) {
        std::fprintf(stderr, "%d 项检查失败\n", g_failures);
        return 1;
    }
    std::printf("人脸几何对齐与比对测试全部通过\n");
    return 0;
}
