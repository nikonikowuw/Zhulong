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
    std::vector<zhulong::FaceTemplate> gallery;
    gallery.push_back(make_template("f1", "p1", {1.0f, 0.0f, 0.0f}));
    gallery.push_back(make_template("f2", "p2", {0.6f, 0.8f, 0.0f}));
    gallery.push_back(make_template("f3", "p3", {0.0f, 1.0f, 0.0f}));
    gallery.push_back(make_template("f4", "p4", {-1.0f, 0.0f, 0.0f}));
    gallery.push_back(make_template("f5", "p5", {0.0f, 0.0f, 1.0f}));

    const float probe[] = {1.0f, 0.0f, 0.0f};
    const zhulong::face::Matcher matcher;
    const zhulong::face::MatchResult result = matcher.match(probe, 3, gallery);

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
    std::vector<zhulong::FaceTemplate> gallery;
    gallery.push_back(make_template("f1", "p1", {0.6f, 0.8f, 0.0f}));

    const float probe[] = {1.0f, 0.0f, 0.0f};
    const zhulong::face::Matcher below(0.61f, 1);
    const zhulong::face::Matcher at_threshold(0.6f, 1);
    check(!below.match(probe, 3, gallery).matched, "相似度低于阈值不得命中");
    check(at_threshold.match(probe, 3, gallery).matched, "相似度等于阈值应命中（>= 语义）");

    // 空底库
    const zhulong::face::MatchResult empty_gallery = at_threshold.match(probe, 3, std::vector<zhulong::FaceTemplate>{});
    check(!empty_gallery.matched && empty_gallery.candidates.empty(), "空底库必须返回未命中且无候选人");

    // 探针非法
    check(!at_threshold.match(nullptr, 3, gallery).matched, "空探针必须返回未命中");
    check(!at_threshold.match(probe, 0, gallery).matched, "零维探针必须返回未命中");

    // 维度不符的底库条目被跳过
    std::vector<zhulong::FaceTemplate> mixed;
    mixed.push_back(make_template("bad-dim", "p0", {1.0f, 0.0f}));
    mixed.push_back(make_template("f1", "p1", {0.6f, 0.8f, 0.0f}));
    const zhulong::face::MatchResult mixed_result = at_threshold.match(probe, 3, mixed);
    check(mixed_result.matched_face_id == "f1", "维度不符的底库条目必须被跳过");
    check(mixed_result.candidates.size() == 1, "维度不符的条目不计入候选人");

    // 反平行向量：余弦为 -1，截断到 0 ⇒ 未命中
    std::vector<zhulong::FaceTemplate> opposite;
    opposite.push_back(make_template("f-neg", "p-neg", {-1.0f, 0.0f, 0.0f}));
    const zhulong::face::MatchResult negative = at_threshold.match(probe, 3, opposite);
    check(nearly_equal(negative.similarity, 0.0f, 1e-6f), "负相关相似度必须截断为 0");
    check(!negative.matched, "截断为 0 后不得命中");

    // top_k 大于底库规模 ⇒ 返回全部
    const zhulong::face::Matcher big_top_k(0.4f, 100);
    check(big_top_k.match(probe, 3, gallery).candidates.size() == gallery.size(),
          "top_k 超过底库规模时应返回全部条目");
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

} // namespace

int main() {
    test_similarity_transform_recovers_known_geometry();
    test_similarity_transform_rejects_degenerate_input();
    test_crop_and_align_identity_preserves_pixels();
    test_crop_and_align_handles_yuv_gray();
    test_crop_and_align_rejects_bad_input();
    test_matcher_returns_expected_top_k();
    test_matcher_threshold_boundary_and_degenerate_input();
    test_face_association();
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
