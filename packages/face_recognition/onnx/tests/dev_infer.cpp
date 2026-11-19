/**
 * @file dev_infer.cpp
 * @brief 开发期自测：包根 `sample.jpg` → 本 target 产品代码 → 画框 → `result.jpg` + 终端明细表
 *
 * 这个可执行文件**只服务开发期**，不是交付物：
 *   - `add_executable(... EXCLUDE_FROM_ALL)`，不进安装规则，物理上进不了算法包归档；
 *   - 依赖 stb（header-only），产品代码零依赖。
 *
 * 与宿主黑盒验包（`zhulong_algo_runner --verify-plugin`）的分工：
 *   - 验包证明「ABI 生命周期通、能跑出结果」，但它不含图像库，只能吃 PPM 或合成帧；
 *   - 本工具给开发者一张**画好的图**，用眼睛判断框和号码对不对，跨 target 不比数值
 *     （量化不同，结果本就不同）。
 *
 * 用法（在包目录内）：
 *     make test
 *     ./build/<id>_devtest --sample sample.jpg --out build/out/result.jpg
 *
 * 输出：`result.jpg`（vehicle/plate/face 框与标签）+ stdout 明细表。
 */

#include <zhulong/algo.hpp>
#include <zhulong/plugin.h>
#include <zhulong/runtime/gallery_registry.hpp>

#include "attribute/person_attribute.hpp"
#include "common/face_enroll_enhance.hpp"
#include "pipeline/face_recognition.hpp"

#define STB_IMAGE_IMPLEMENTATION
#include "stb_image.h"
#define STB_IMAGE_WRITE_IMPLEMENTATION
#include "stb_image_write.h"

#include <algorithm>
#include <chrono>
#include <cmath>
#include <cstdio>
#include <cstring>
#include <string>
#include <vector>

extern "C" {
zhulong_status_t zhulong_face_extract_feature(
    const uint8_t* pixel_data,
    int32_t width,
    int32_t height,
    int32_t stride,
    int32_t format,
    float* out_features,
    uint32_t* feature_dim
);
void zhulong_face_extract_release();
}

namespace {

/** @brief 终端明细表的一行 */
struct DetailRow {
    std::string kind;
    std::string label;
    float score{0.0f};
    float box[4]{0.0f, 0.0f, 0.0f, 0.0f};
    std::string text;
};

/**
 * @brief 在 RGB24 缓冲上画一个矩形框（归一化坐标 → 像素，越界裁剪）
 *
 * 不引字体：label 打不出来就打到终端表里。图的作用是「框对不对」，
 * 字号/字体/渲染顺序一旦进断言就会天天假红。
 */
void draw_box(uint8_t* canvas, int width, int height, const float box[4], uint8_t r, uint8_t g, uint8_t b,
              int thickness = 2) {
    int x1 = static_cast<int>(box[0] * static_cast<float>(width));
    int y1 = static_cast<int>(box[1] * static_cast<float>(height));
    int x2 = static_cast<int>(box[2] * static_cast<float>(width));
    int y2 = static_cast<int>(box[3] * static_cast<float>(height));
    if (x1 > x2) std::swap(x1, x2);
    if (y1 > y2) std::swap(y1, y2);
    if (x1 < 0) x1 = 0;
    if (y1 < 0) y1 = 0;
    if (x2 >= width) x2 = width - 1;
    if (y2 >= height) y2 = height - 1;

    const auto put = [&](int x, int y) {
        if (x < 0 || y < 0 || x >= width || y >= height) return;
        uint8_t* pixel = canvas + (static_cast<size_t>(y) * width + x) * 3;
        pixel[0] = r;
        pixel[1] = g;
        pixel[2] = b;
    };
    for (int t = 0; t < thickness; ++t) {
        for (int x = x1; x <= x2; ++x) {
            put(x, y1 + t);
            put(x, y2 - t);
        }
        for (int y = y1; y <= y2; ++y) {
            put(x1 + t, y);
            put(x2 - t, y);
        }
    }
}

void print_table(const std::string& sample, const char* result_type_name, uint32_t count,
                 double cost_ms, const std::vector<DetailRow>& rows) {
    std::printf("\n=== dev infer ===\n");
    std::printf("sample      : %s\n", sample.c_str());
    std::printf("result_type : %s\n", result_type_name);
    std::printf("count       : %u\n", count);
    std::printf("cost_ms     : %.2f\n", cost_ms);
    if (rows.empty()) {
        std::printf("(no rows)\n");
        return;
    }
    std::printf("%-8s %-16s %6s  %-34s %s\n", "kind", "label", "score", "box[x1,y1,x2,y2]", "text");
    for (const auto& row : rows) {
        std::printf("%-8s %-16s %6.3f  [%.3f,%.3f,%.3f,%.3f] %s\n", row.kind.c_str(), row.label.c_str(), row.score,
                    row.box[0], row.box[1], row.box[2], row.box[3], row.text.c_str());
    }
}

#define DEV_CHECK(cond, msg)                                                                           \
    do {                                                                                               \
        if (!(cond)) {                                                                                 \
            std::fprintf(stderr, "[✗] regression check failed: %s (%s:%d)\n", (msg), __FILE__, __LINE__); \
            return false;                                                                              \
        }                                                                                              \
    } while (false)

struct RgbBuffer {
    int width{0};
    int height{0};
    std::vector<uint8_t> rgb;
};

RgbBuffer crop_face_padded(const uint8_t* src, int src_w, int src_h, const float box[4], int canvas_size) {
    const float bw = std::max(1.0f, (box[2] - box[0]) * static_cast<float>(src_w));
    const float bh = std::max(1.0f, (box[3] - box[1]) * static_cast<float>(src_h));
    const float cx = 0.5f * (box[0] + box[2]) * static_cast<float>(src_w);
    const float cy = 0.5f * (box[1] + box[3]) * static_cast<float>(src_h);
    const float side = std::max(bw, bh) * 1.8f;
    const int x1 = std::max(0, static_cast<int>(std::floor(cx - side * 0.5f)));
    const int y1 = std::max(0, static_cast<int>(std::floor(cy - side * 0.5f)));
    const int x2 = std::min(src_w, static_cast<int>(std::ceil(cx + side * 0.5f)));
    const int y2 = std::min(src_h, static_cast<int>(std::ceil(cy + side * 0.5f)));
    const int cw = std::max(1, x2 - x1);
    const int ch = std::max(1, y2 - y1);

    RgbBuffer out;
    out.width = canvas_size;
    out.height = canvas_size;
    out.rgb.assign(static_cast<size_t>(canvas_size) * static_cast<size_t>(canvas_size) * 3, 128);

    const int target = std::min(canvas_size - 40, std::max(cw, ch));
    const int dst_w = std::max(1, static_cast<int>(std::round(static_cast<float>(target) * cw / std::max(cw, ch))));
    const int dst_h = std::max(1, static_cast<int>(std::round(static_cast<float>(target) * ch / std::max(cw, ch))));
    const int off_x = (canvas_size - dst_w) / 2;
    const int off_y = (canvas_size - dst_h) / 2;

    for (int y = 0; y < dst_h; ++y) {
        const float sy = (static_cast<float>(y) + 0.5f) * static_cast<float>(ch) / static_cast<float>(dst_h) - 0.5f;
        const int sy0 = std::max(0, std::min(ch - 1, static_cast<int>(std::floor(sy))));
        const int sy1 = std::min(ch - 1, sy0 + 1);
        const float wy = std::max(0.0f, std::min(1.0f, sy - static_cast<float>(sy0)));
        for (int x = 0; x < dst_w; ++x) {
            const float sx = (static_cast<float>(x) + 0.5f) * static_cast<float>(cw) / static_cast<float>(dst_w) - 0.5f;
            const int sx0 = std::max(0, std::min(cw - 1, static_cast<int>(std::floor(sx))));
            const int sx1 = std::min(cw - 1, sx0 + 1);
            const float wx = std::max(0.0f, std::min(1.0f, sx - static_cast<float>(sx0)));
            for (int c = 0; c < 3; ++c) {
                const float p00 = src[((y1 + sy0) * src_w + (x1 + sx0)) * 3 + c];
                const float p01 = src[((y1 + sy0) * src_w + (x1 + sx1)) * 3 + c];
                const float p10 = src[((y1 + sy1) * src_w + (x1 + sx0)) * 3 + c];
                const float p11 = src[((y1 + sy1) * src_w + (x1 + sx1)) * 3 + c];
                const float val = (1.0f - wy) * ((1.0f - wx) * p00 + wx * p01) +
                                  wy * ((1.0f - wx) * p10 + wx * p11);
                out.rgb[((off_y + y) * canvas_size + (off_x + x)) * 3 + c] =
                    static_cast<uint8_t>(std::round(val));
            }
        }
    }
    return out;
}

RgbBuffer blur_downsample_buffer(const RgbBuffer& src, int small_dim) {
    std::vector<uint8_t> small(static_cast<size_t>(small_dim) * static_cast<size_t>(small_dim) * 3, 0);
    for (int y = 0; y < small_dim; ++y) {
        const int sy = (y * src.height + src.height / 2) / small_dim;
        for (int x = 0; x < small_dim; ++x) {
            const int sx = (x * src.width + src.width / 2) / small_dim;
            for (int c = 0; c < 3; ++c) {
                small[(y * small_dim + x) * 3 + c] = src.rgb[(sy * src.width + sx) * 3 + c];
            }
        }
    }
    RgbBuffer out;
    out.width = src.width;
    out.height = src.height;
    out.rgb.resize(src.rgb.size());
    for (int y = 0; y < src.height; ++y) {
        const float fy = (static_cast<float>(y) + 0.5f) * static_cast<float>(small_dim) / static_cast<float>(src.height) - 0.5f;
        const int y0 = std::max(0, std::min(small_dim - 1, static_cast<int>(std::floor(fy))));
        const int y1 = std::min(small_dim - 1, y0 + 1);
        const float wy = std::max(0.0f, std::min(1.0f, fy - static_cast<float>(y0)));
        for (int x = 0; x < src.width; ++x) {
            const float fx = (static_cast<float>(x) + 0.5f) * static_cast<float>(small_dim) / static_cast<float>(src.width) - 0.5f;
            const int x0 = std::max(0, std::min(small_dim - 1, static_cast<int>(std::floor(fx))));
            const int x1 = std::min(small_dim - 1, x0 + 1);
            const float wx = std::max(0.0f, std::min(1.0f, fx - static_cast<float>(x0)));
            for (int c = 0; c < 3; ++c) {
                const float p00 = small[(y0 * small_dim + x0) * 3 + c];
                const float p01 = small[(y0 * small_dim + x1) * 3 + c];
                const float p10 = small[(y1 * small_dim + x0) * 3 + c];
                const float p11 = small[(y1 * small_dim + x1) * 3 + c];
                const float val = (1.0f - wy) * ((1.0f - wx) * p00 + wx * p01) +
                                  wy * ((1.0f - wx) * p10 + wx * p11);
                out.rgb[(y * src.width + x) * 3 + c] = static_cast<uint8_t>(std::round(val));
            }
        }
    }
    return out;
}

bool check_unit_norm(const std::vector<float>& feat) {
    if (feat.size() != 512) return false;
    float norm_sq = 0.0f;
    for (float v : feat) {
        if (!std::isfinite(v)) return false;
        norm_sq += v * v;
    }
    return std::fabs(std::sqrt(norm_sq) - 1.0f) < 0.01f;
}

/** @brief 把属性列表拼成 "LongSleeve:0.87 Female:0.93" 形式，便于终端肉眼核对 */
std::string attribute_summary(const std::vector<zhulong::PersonAttribute>& attributes) {
    std::string text;
    for (const auto& attribute : attributes) {
        if (!text.empty()) {
            text.push_back(' ');
        }
        char score[16] = {};
        std::snprintf(score, sizeof(score), ":%.2f", attribute.score);
        text += attribute.name;
        text += score;
    }
    return text;
}

/** @brief 人脸终端注记的公共拼装：身份（命中者姓名 / 关联人体下标）+ 人脸属性
 *
 *  只打属性会让「比对结果是否还接得上」这件事实测不出来；只打身份则看不出属性阶段
 *  是否真的跑过。两者都拼上，`--verify` 与肉眼核对才能同时覆盖。
 *  `person_index` 传 -1 表示该类型没有人体关联（内嵌的 `FaceItem`）。
 */
std::string face_annotation_text(bool matched, const std::string& person_name, int32_t person_index,
                                 const std::vector<zhulong::PersonAttribute>& attributes) {
    std::string text;
    if (matched && !person_name.empty()) {
        text = person_name;
    } else if (person_index >= 0) {
        text = "person#" + std::to_string(person_index);
    } else {
        text = "face";
    }
    const std::string attribute_text = attribute_summary(attributes);
    if (!attribute_text.empty()) {
        text += " | ";
        text += attribute_text;
    }
    return text;
}

/** @brief 场级人脸项（带 person_index）的注记 */
std::string face_annotation(const zhulong::FaceStructureItem& face) {
    return face_annotation_text(face.matched, face.person_name, face.person_index, face.attributes);
}

/** @brief 嵌在人体项里的人脸（`FaceItem` 没有 person_index）的注记 */
std::string face_annotation(const zhulong::FaceItem& face) {
    return face_annotation_text(face.matched, face.person_name, -1, face.attributes);
}

/** @brief 属性自检：名字必须在标签表内、分数在 (0,1]、按分数降序，且不超过 26 条 */
bool valid_attributes(const std::vector<zhulong::PersonAttribute>& attributes) {
    if (attributes.size() > static_cast<size_t>(zhulong::face::kPersonAttributeCount)) {
        return false;
    }
    float previous = 1.0f;
    for (const auto& attribute : attributes) {
        if (!std::isfinite(attribute.score) || !(attribute.score > 0.0f) || attribute.score > 1.0f ||
            attribute.score > previous) {
            return false;
        }
        previous = attribute.score;
        bool known = false;
        for (const std::string_view label : zhulong::face::kPersonAttributeLabels) {
            known = known || (label == attribute.name);
        }
        if (!known) {
            return false;
        }
    }
    return true;
}

bool run_regression_checks(zhulong::face::FaceRecognitionPlugin& plugin,
                           zhulong::Session& session,
                           const uint8_t* sample_rgb,
                           int width,
                           int height) {
    zhulong::runtime::GalleryRegistry::instance().clear();

    // 1. 合成噪声与全黑帧必须 fail-closed（零检出）
    {
        std::vector<uint8_t> noise(100 * 100 * 3, 0);
        for (size_t i = 0; i < noise.size(); ++i) {
            noise[i] = static_cast<uint8_t>((i * 7 + 13) % 256);
        }
        std::vector<uint8_t> black(100 * 100 * 3, 0);
        for (const auto* buf : {noise.data(), black.data()}) {
            zhulong_frame_view fv{};
            fv.frame_id = 1;
            fv.width = 100;
            fv.height = 100;
            fv.format = ZHULONG_PIXEL_FORMAT_RGB24;
            fv.planes[0] = buf;
            fv.strides[0] = 300;
            fv.dma_buf_fd = -1;
            zhulong::TaskContext ctx;
            ctx.task_id = 900;
            const auto out = plugin.detect_scene_structure(session, zhulong::Image(fv), ctx);
            DEV_CHECK(out.persons.empty() && out.faces.empty(), "synthetic noise/black frame must yield 0 detections");
        }
    }

    // 2. SceneStructure v2 主路径：真实图必须检出人体与人脸，且特征与 eDifFIQA(T) 质量合法
    zhulong_frame_view sample_fv{};
    sample_fv.frame_id = 10;
    sample_fv.pts = 400000;
    sample_fv.timestamp_us = 1000000;
    sample_fv.width = width;
    sample_fv.height = height;
    sample_fv.format = ZHULONG_PIXEL_FORMAT_RGB24;
    sample_fv.planes[0] = sample_rgb;
    sample_fv.strides[0] = width * 3;
    sample_fv.dma_buf_fd = -1;

    zhulong::TaskContext base_ctx;
    base_ctx.task_id = 901;
    const auto scene = plugin.detect_scene_structure(session, zhulong::Image(sample_fv), base_ctx);
    DEV_CHECK(!scene.persons.empty(), "expected >= 1 person on sample");
    DEV_CHECK(scene.faces.size() >= 2, "expected >= 2 faces on sample");
    for (const auto& p : scene.persons) {
        DEV_CHECK(p.faces.empty(), "SceneStructure person item faces must be empty");
        DEV_CHECK(p.body_box[0] >= 0.0f && p.body_box[2] <= 1.0f && p.body_box[0] < p.body_box[2], "valid body x");
        DEV_CHECK(p.body_box[1] >= 0.0f && p.body_box[3] <= 1.0f && p.body_box[1] < p.body_box[3], "valid body y");
        DEV_CHECK(valid_attributes(p.attributes), "person attributes must use known labels with ordered scores");
    }
    for (const auto& f : scene.faces) {
        DEV_CHECK(f.quality >= 0.28f && f.quality <= 1.0f, "face quality must pass default eDifFIQA(T) gate");
        DEV_CHECK(check_unit_norm(f.features), "face feature must be 512-d L2 normalized");
    }

    // 3. 任务级 min_face_quality=0.99 门禁必须拦截常规人脸
    {
        zhulong::TaskContext strict_ctx;
        strict_ctx.task_id = 902;
        strict_ctx.params.json = "{\"min_face_quality\":0.99}";
        const auto strict_scene = plugin.detect_scene_structure(session, zhulong::Image(sample_fv), strict_ctx);
        DEV_CHECK(strict_scene.faces.empty(), "min_face_quality=0.99 must filter out normal faces");
    }

    // 4. 构造单人裁剪图，验证 eDifFIQA(T) 退化区分、跨帧融合与底库登记门禁
    const auto& face0 = scene.faces[0];
    const auto& face1 = scene.faces[1];
    const RgbBuffer photo_a = crop_face_padded(sample_rgb, width, height, face0.box, 320);
    const RgbBuffer photo_b = crop_face_padded(sample_rgb, width, height, face1.box, 320);
    const RgbBuffer photo_degraded = blur_downsample_buffer(photo_a, 16);

    // 4a. 底库登记提取入口 (--extract-feature) + eDifFIQA(T) 质检
    std::vector<float> enroll_feat(512, 0.0f);
    uint32_t enroll_dim = 512;
    zhulong_status_t ext_rc = zhulong_face_extract_feature(
        photo_a.rgb.data(), photo_a.width, photo_a.height, photo_a.width * 3,
        ZHULONG_PIXEL_FORMAT_RGB24, enroll_feat.data(), &enroll_dim);
    DEV_CHECK(ext_rc == ZHULONG_PLUGIN_OK && enroll_dim == 512, "single-face enrollment photo should succeed");
    DEV_CHECK(check_unit_norm(enroll_feat), "extracted enrollment feature must have unit L2 norm");

    uint32_t bad_dim = 512;
    std::vector<float> bad_feat(512, 0.0f);
    const zhulong_status_t bad_rc = zhulong_face_extract_feature(
        photo_degraded.rgb.data(), photo_degraded.width, photo_degraded.height, photo_degraded.width * 3,
        ZHULONG_PIXEL_FORMAT_RGB24, bad_feat.data(), &bad_dim);
    DEV_CHECK(bad_rc == ZHULONG_PLUGIN_ERR_NOT_FOUND, "severely degraded photo must be rejected by enrollment gate");

    const zhulong_status_t multi_rc = zhulong_face_extract_feature(
        sample_rgb, width, height, width * 3,
        ZHULONG_PIXEL_FORMAT_RGB24, bad_feat.data(), &bad_dim);
    DEV_CHECK(multi_rc == ZHULONG_PLUGIN_ERR_AMBIGUOUS, "multi-face photo must return AMBIGUOUS");

    auto cosine_sim = [](const std::vector<float>& a, const std::vector<float>& b) {
        double dot = 0.0;
        for (size_t i = 0; i < a.size() && i < b.size(); ++i) {
            dot += static_cast<double>(a[i]) * static_cast<double>(b[i]);
        }
        return static_cast<float>(dot);
    };

    // 4d. 无 EXIF 侧躺（90° CW）单人录入照自动转正并提取高一致特征
    {
        std::vector<uint8_t> rot_rgb;
        int rot_w = 0, rot_h = 0;
        DEV_CHECK(zhulong::face::rotate_rgb24(photo_a.rgb.data(), photo_a.width, photo_a.height,
                                              photo_a.width * 3, 90, rot_rgb, rot_w, rot_h),
                  "rotate_rgb24 90 deg must succeed");
        std::vector<float> rot_feat(512, 0.0f);
        uint32_t rot_dim = 512;
        const zhulong_status_t rot_rc = zhulong_face_extract_feature(
            rot_rgb.data(), rot_w, rot_h, rot_w * 3,
            ZHULONG_PIXEL_FORMAT_RGB24, rot_feat.data(), &rot_dim);
        DEV_CHECK(rot_rc == ZHULONG_PLUGIN_OK && rot_dim == 512,
                  "90-deg rotated single-face photo should succeed via orientation fallback");
        DEV_CHECK(check_unit_norm(rot_feat), "rotated photo feature must have unit L2 norm");
        const float rot_sim = cosine_sim(enroll_feat, rot_feat);
        std::printf("  [check] 90-deg fallback cosine_sim=%.4f\n", rot_sim);
        DEV_CHECK(rot_sim >= 0.80f,
                  "rotated photo feature must strongly match upright enrollment feature");
    }

    // 4e. 暗光欠曝单人录入照自适应提亮并保持特征一致性
    {
        std::vector<uint8_t> dark_rgb;
        DEV_CHECK(zhulong::face::apply_gamma_rgb24(photo_a.rgb.data(), photo_a.width, photo_a.height,
                                                   photo_a.width * 3, 2.4f, dark_rgb),
                  "apply_gamma_rgb24 darkening must succeed");
        std::vector<float> dark_feat(512, 0.0f);
        uint32_t dark_dim = 512;
        const zhulong_status_t dark_rc = zhulong_face_extract_feature(
            dark_rgb.data(), photo_a.width, photo_a.height, photo_a.width * 3,
            ZHULONG_PIXEL_FORMAT_RGB24, dark_feat.data(), &dark_dim);
        DEV_CHECK(dark_rc == ZHULONG_PLUGIN_OK && dark_dim == 512,
                  "darkened single-face photo should succeed via adaptive gamma enhancement");
        DEV_CHECK(check_unit_norm(dark_feat), "darkened photo feature must have unit L2 norm");
        const float dark_sim = cosine_sim(enroll_feat, dark_feat);
        std::printf("  [check] dark gamma cosine_sim=%.4f\n", dark_sim);
        DEV_CHECK(dark_sim >= 0.70f,
                  "darkened photo feature must remain consistent with normal enrollment feature");
    }

    // 4f. 极小人脸（< 32px）在录入端必须被最小像素门禁拒绝
    {
        constexpr int tiny_canvas = 320;
        constexpr int tiny_patch = 28;
        std::vector<uint8_t> tiny_rgb(tiny_canvas * tiny_canvas * 3, 128);
        const int off = (tiny_canvas - tiny_patch) / 2;
        for (int y = 0; y < tiny_patch; ++y) {
            const int sy = std::min(photo_a.height - 1, y * photo_a.height / tiny_patch);
            for (int x = 0; x < tiny_patch; ++x) {
                const int sx = std::min(photo_a.width - 1, x * photo_a.width / tiny_patch);
                for (int c = 0; c < 3; ++c) {
                    tiny_rgb[((off + y) * tiny_canvas + (off + x)) * 3 + c] =
                        photo_a.rgb[(sy * photo_a.width + sx) * 3 + c];
                }
            }
        }
        std::vector<float> tiny_feat(512, 0.0f);
        uint32_t tiny_dim = 512;
        const zhulong_status_t tiny_rc = zhulong_face_extract_feature(
            tiny_rgb.data(), tiny_canvas, tiny_canvas, tiny_canvas * 3,
            ZHULONG_PIXEL_FORMAT_RGB24, tiny_feat.data(), &tiny_dim);
        DEV_CHECK(tiny_rc == ZHULONG_PLUGIN_ERR_NOT_FOUND,
                  "tiny (<32px) face must be rejected by enrollment size gate");
    }

    zhulong_face_extract_release();

    // 4b. 跨帧特征融合与陌生人隔离
    zhulong::runtime::GalleryRegistry::instance().upsert({
        "face_sample_0",
        "PERSON_SAMPLE_0",
        "SamplePerson0",
        "sample0.jpg",
        enroll_feat,
    });

    auto run_photo = [&](const RgbBuffer& img, int64_t ts_us, uint64_t fid, uint64_t tid, const char* params) {
        zhulong_frame_view fv{};
        fv.frame_id = fid;
        fv.pts = fid * 40000;
        fv.timestamp_us = ts_us;
        fv.width = img.width;
        fv.height = img.height;
        fv.format = ZHULONG_PIXEL_FORMAT_RGB24;
        fv.planes[0] = img.rgb.data();
        fv.strides[0] = img.width * 3;
        fv.dma_buf_fd = -1;
        zhulong::TaskContext ctx;
        ctx.task_id = tid;
        ctx.params.json = params;
        return plugin.detect_scene_structure(session, zhulong::Image(fv), ctx);
    };

    const auto r1 = run_photo(photo_a, 1000000, 1, 903, "{\"threshold\":0.50,\"top_k\":3}");
    DEV_CHECK(r1.faces.size() >= 1 && r1.faces[0].matched && !r1.faces[0].is_fused,
              "first observation must match gallery with is_fused=false");
    const auto r2 = run_photo(photo_a, 1200000, 2, 903, "{\"threshold\":0.50,\"top_k\":3}");
    DEV_CHECK(r2.faces.size() >= 1 && r2.faces[0].matched && r2.faces[0].is_fused,
              "second consecutive same-person frame must activate is_fused=true");
    DEV_CHECK(r2.faces[0].person_id == "PERSON_SAMPLE_0", "fused face must keep enrolled person_id");
    const auto r3 = run_photo(photo_b, 1400000, 3, 903, "{\"threshold\":0.50,\"top_k\":3}");
    DEV_CHECK(r3.faces.size() >= 1 && !r3.faces[0].matched && !r3.faces[0].is_fused,
              "different person must stay isolated from fusion pool");

    // 4c. 退化图像质量分远低于清晰图像或直接被拒
    const auto r_deg = run_photo(photo_degraded, 1600000, 4, 904, "{\"min_face_quality\":0.0}");
    if (!r_deg.faces.empty()) {
        DEV_CHECK(r_deg.faces[0].quality < r1.faces[0].quality - 0.15f,
                  "degraded face must receive significantly lower eDifFIQA(T) score");
    }

    zhulong::runtime::GalleryRegistry::instance().clear();
    std::printf("[✓] face_recognition regression checks passed\n");
    return true;
}

} // namespace

int main(int argc, char** argv) {
    std::string sample_path = "sample.jpg";
    std::string output_path = "build/out/result.jpg";
    std::string config_json = "{}";
    // 任务级配置原文：与宿主经 MSG_TASK_CONFIG 下发的内容等价，用来在本地验证
    // 「每个 stream 一份配置」的解析路径（不需要跑 broker 与包进程）。
    std::string task_config_json = "{}";
    bool run_check = false;
    for (int i = 1; i < argc; ++i) {
        const std::string arg = argv[i];
        if (arg == "--sample" && i + 1 < argc) {
            sample_path = argv[++i];
        } else if (arg == "--out" && i + 1 < argc) {
            output_path = argv[++i];
        } else if (arg == "--config" && i + 1 < argc) {
            config_json = argv[++i];
        } else if (arg == "--task-config" && i + 1 < argc) {
            task_config_json = argv[++i];
        } else if (arg == "--check") {
            run_check = true;
        } else {
            std::fprintf(stderr, "unknown argument: %s\n", arg.c_str());
            return 2;
        }
    }

    int width = 0;
    int height = 0;
    int channels = 0;
    uint8_t* pixels = stbi_load(sample_path.c_str(), &width, &height, &channels, 3);
    if (pixels == nullptr) {
        std::fprintf(stderr, "failed to decode sample %s: %s\n", sample_path.c_str(), stbi_failure_reason());
        return 1;
    }
    const size_t pixel_bytes = static_cast<size_t>(width) * static_cast<size_t>(height) * 3;
    std::vector<uint8_t> canvas(pixels, pixels + pixel_bytes);
    stbi_image_free(pixels);

    // 直接调用本 target 的产品类（不走 C ABI 表）：dev 工具就该像算法作者那样用它。
    zhulong::face::FaceRecognitionPlugin plugin;
    zhulong_status_t rc = plugin.init(config_json);
    if (rc != ZHULONG_PLUGIN_OK) {
        std::fprintf(stderr, "init failed: %d (models/ 是否与包根同级？)\n", static_cast<int>(rc));
        return 1;
    }
    zhulong::Session session = plugin.create_session(0);
    if (!session.valid()) {
        std::fprintf(stderr, "create_session failed\n");
        return 1;
    }

    if (run_check && !run_regression_checks(plugin, session, canvas.data(), width, height)) {
        return 1;
    }

    zhulong_frame_view frame;
    std::memset(&frame, 0, sizeof(frame));
    frame.frame_id = 1;
    frame.pts = 0;
    frame.width = width;
    frame.height = height;
    frame.format = ZHULONG_PIXEL_FORMAT_RGB24;
    frame.planes[0] = canvas.data();
    frame.strides[0] = width * 3;
    frame.dma_buf_fd = -1;

    // 与产品路径一致：算法方法需要 TaskContext。params.json 在生产里由骨架的
    // drain_control_messages 挂上（MSG_TASK_CONFIG），这里用 --task-config 直接注入。
    zhulong::TaskContext task_context;
    task_context.task_id = 1;
    task_context.params.json = std::string_view(task_config_json);

    const auto started = std::chrono::steady_clock::now();
    // 走产品主路径（SCENE_STRUCTURE）：人脸属性只挂在这个出口上，用 `detect_persons`
    // 自测会让「人脸属性没接上」这类回归在出图里完全看不出来。
    const zhulong::SceneStructure scene =
        plugin.detect_scene_structure(session, zhulong::Image(frame), task_context);
    const double cost_ms =
        std::chrono::duration<double, std::milli>(std::chrono::steady_clock::now() - started).count();

    std::vector<DetailRow> rows;
    for (const auto& person : scene.persons) {
        DetailRow row_person;
        row_person.kind = "person";
        row_person.label = "body";
        row_person.score = person.body_score;
        std::memcpy(row_person.box, person.body_box, sizeof(row_person.box));
        row_person.text = attribute_summary(person.attributes);
        draw_box(canvas.data(), width, height, row_person.box, 0, 255, 0);
        rows.push_back(row_person);

        // 真人体项内部的 faces 恒为空（场级入口把每张脸平铺进 scene.faces），这里仍遍历，
        // 是为了在「实现退化回旧布局」时不留死角。
        for (const auto& face : person.faces) {
            DetailRow row_face;
            row_face.kind = "face";
            row_face.label = face.matched ? "matched" : "unmatched";
            row_face.score = face.quality;
            std::memcpy(row_face.box, face.box, sizeof(row_face.box));
            row_face.text = face_annotation(face);
            draw_box(canvas.data(), width, height, row_face.box, 255, 128, 0);
            rows.push_back(row_face);
        }
    }
    for (const auto& face : scene.faces) {
        DetailRow row_face;
        row_face.kind = "face";
        row_face.label = face.matched ? "matched" : "unmatched";
        row_face.score = face.quality;
        std::memcpy(row_face.box, face.box, sizeof(row_face.box));
        // 身份与属性都要留：只打属性会让「比对结果是否还接得上」这件事实测不出来。
        row_face.text = face_annotation(face);
        draw_box(canvas.data(), width, height, row_face.box, 255, 128, 0);
        rows.push_back(row_face);
    }

    const char* result_type_name = "scene_structure";
    const uint32_t count = static_cast<uint32_t>(scene.persons.size() + scene.faces.size());
    print_table(sample_path, result_type_name, count, cost_ms, rows);

    // 先落地再宣告成功：反过来写会在写盘失败时同时输出「failed to write」和
    // 「result written」，看日志的人只能靠猜。
    if (stbi_write_jpg(output_path.c_str(), width, height, 3, canvas.data(), 92) == 0) {
        std::fprintf(stderr, "failed to write %s\n", output_path.c_str());
        return 1;
    }
    std::printf("result written: %s\n", output_path.c_str());
    return 0;
}
