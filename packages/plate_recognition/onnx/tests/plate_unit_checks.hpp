/**
 * @file plate_unit_checks.hpp
 * @brief 纯逻辑自测：采样坐标 / 号牌结构 / 帧视图守卫 / 默认值一致性
 *
 * 三个刻意的设计决定：
 *
 * 1. **不用 `assert`**。devtest 走 `RelWithDebInfo`（CMakeLists 的 `-DNDEBUG`），
 *    assert 会被整块编译掉 —— `make test` 于是打印「checks passed」而一条都没跑。
 *    仓库早就踩过（`native/CMakeLists.txt` 的 ASSERT_TEST_TARGETS 就是那次事故的补丁），
 *    这里用永远求值的 `PLATE_CHECK`。
 *
 * 2. **是 header 而不是 TU**。保持 header-only 便于在包内自测与 `devtest` 中复用。
 *
 * 3. **不加载模型**。这里只测纯函数，因此不需要 Session、不需要权重、不需要推理后端，
 *    任何档位都能跑；模型金标与多 worker 回归断言由 `tests/dev_infer.cpp --check` 承担。
 */

#pragma once

#include "common/plate_defaults.hpp"
#include "common/plate_semantics.hpp"
#include "format/plate_format.hpp"
#include "recognition/plate_recognizer.hpp"

#include <algorithm>
#include <cmath>
#include <cstdio>
#include <string>

namespace zhulong::plate::test {

/** @brief 累计失败数（跨多次调用共享，便于一个可执行文件里跑好几组） */
inline int& failure_count() noexcept {
    static int count = 0;
    return count;
}

/** @brief 断言失败时打印 文件:行 与表达式原文，便于直接跳到出错点 */
inline void check(bool ok, const char* expr, const char* file, int line) noexcept {
    if (ok) {
        return;
    }
    ++failure_count();
    std::fprintf(stderr, "[FAIL] %s:%d: %s\n", file, line, expr);
}

/**
 * @brief 无条件求值的断言（**不是** assert）
 *
 * assert 在 NDEBUG 下会连表达式一起消失；这个宏永远求值，并把失败位置打出来。
 * 用 `do { } while (0)` 包住，避免 `if (x) PLATE_CHECK(y); else ...` 这类悬挂 else。
 */
#define PLATE_CHECK(cond) \
    do { \
        ::zhulong::plate::test::check(static_cast<bool>(cond), #cond, __FILE__, __LINE__); \
    } while (0)

/** @brief 浮点近似比较（采样坐标是 float，不能用 == 钉） */
inline bool near(float lhs, float rhs, float epsilon = 1e-4f) noexcept {
    return std::fabs(lhs - rhs) <= epsilon;
}

/**
 * @brief 带相对容差的比较
 *
 * 「步长 × 目标尺寸」这类断言里两个 float 都是几百量级（例如 200.28 - 199.76），
 * 相减会抵消掉大部分有效位：绝对容差 1e-4 会随机抢红，相对容差才稳。
 */
inline bool near_scaled(float lhs, float rhs, float relative = 1e-3f) noexcept {
    const float scale = std::max(1.0f, std::fabs(rhs));
    return std::fabs(lhs - rhs) <= relative * scale;
}

/** @brief 采样坐标映射：单层恒等、新能源等价单层、双层上下半各占一段
 *
 * ⚠️ 这里钉的是**步长 × 目标尺寸 == 原图尺寸**这种恒等式，而不是「落在某段里」：
 *    后者对 5/12 还是 4/12 都成立，改错常数也能过（试过）。恒等式才能把
 *    双层切块的分段高度、左右切分点、单层缩放比全部钉住。
 */
inline void check_sampling_geometry() {
    constexpr int kTargetW = kRecognitionWidth;
    constexpr int kTargetH = kRecognitionHeight;
    constexpr float kFloatTargetW = static_cast<float>(kTargetW);
    constexpr float kFloatTargetH = static_cast<float>(kTargetH);

    // 1) 单层：1:1 时必须是恒等映射（回归「(x+0.5)*scale-0.5」的像素中心语义）
    {
        const float crop_x = 100.0f;
        const float crop_y = 200.0f;
        const float crop_w = kFloatTargetW;
        const float crop_h = kFloatTargetH;
        for (int y = 0; y < kTargetH; y += 7) {
            for (int x = 0; x < kTargetW; x += 13) {
                const auto point = plate_sample_point(x, y, kTargetW, kTargetH, crop_x, crop_y, crop_w, crop_h,
                                                     PlateKind::SingleLine);
                PLATE_CHECK(near(point.fx, crop_x + static_cast<float>(x)));
                PLATE_CHECK(near(point.fy, crop_y + static_cast<float>(y)));
            }
        }
    }

    // 2) 单层：缩放比恒等式 —— 168 列步长之和 == crop_w，48 行步长之和 == crop_h
    {
        constexpr float crop_x = 100.0f;
        constexpr float crop_y = 200.0f;
        constexpr float crop_w = 170.0f;
        constexpr float crop_h = 60.0f;
        const auto origin = plate_sample_point(0, 0, kTargetW, kTargetH, crop_x, crop_y, crop_w, crop_h,
                                              PlateKind::SingleLine);
        const auto next_x = plate_sample_point(1, 0, kTargetW, kTargetH, crop_x, crop_y, crop_w, crop_h,
                                               PlateKind::SingleLine);
        const auto next_y = plate_sample_point(0, 1, kTargetW, kTargetH, crop_x, crop_y, crop_w, crop_h,
                                               PlateKind::SingleLine);
        PLATE_CHECK(near_scaled((next_x.fx - origin.fx) * kFloatTargetW, crop_w));
        PLATE_CHECK(near_scaled((next_y.fy - origin.fy) * kFloatTargetH, crop_h));
        PLATE_CHECK(origin.fx > crop_x - 0.5f && origin.fx < crop_x + 0.5f);
        PLATE_CHECK(origin.fy > crop_y - 0.5f && origin.fy < crop_y + 0.5f);
    }

    // 3) 新能源按单层采样：两条路径必须逐点相同，否则 8 位牌会整体错位
    {
        const auto energy = plate_sample_point(10, 10, kTargetW, kTargetH, 0.0f, 0.0f, 100.0f, 40.0f,
                                               PlateKind::NewEnergy);
        const auto single = plate_sample_point(10, 10, kTargetW, kTargetH, 0.0f, 0.0f, 100.0f, 40.0f,
                                               PlateKind::SingleLine);
        PLATE_CHECK(near(energy.fx, single.fx));
        PLATE_CHECK(near(energy.fy, single.fy));
    }

    // 4) 双层：上半段高 5/12、下半段占 [1/3, 1) —— 用「步长 × 行数」钉住分段高度
    {
        constexpr float crop_x = 100.0f;
        constexpr float crop_y = 200.0f;
        constexpr float crop_w = 170.0f;
        constexpr float crop_h = 60.0f;
        constexpr int half_width = kTargetW / 2;

        const auto upper_top = plate_sample_point(0, 0, kTargetW, kTargetH, crop_x, crop_y, crop_w, crop_h,
                                                 PlateKind::DoubleLine);
        const auto upper_next_row = plate_sample_point(0, 1, kTargetW, kTargetH, crop_x, crop_y, crop_w, crop_h,
                                                      PlateKind::DoubleLine);
        const auto upper_next_col = plate_sample_point(1, 0, kTargetW, kTargetH, crop_x, crop_y, crop_w, crop_h,
                                                      PlateKind::DoubleLine);
        const auto lower_top = plate_sample_point(half_width, 0, kTargetW, kTargetH, crop_x, crop_y, crop_w, crop_h,
                                                 PlateKind::DoubleLine);
        const auto lower_next_row = plate_sample_point(half_width, 1, kTargetW, kTargetH, crop_x, crop_y, crop_w,
                                                       crop_h, PlateKind::DoubleLine);
        const auto lower_next_col = plate_sample_point(half_width + 1, 0, kTargetW, kTargetH, crop_x, crop_y, crop_w,
                                                       crop_h, PlateKind::DoubleLine);

        // 上半：48 行步长之和 == 5/12 * crop_h；左右铺满整块宽度（84 列）
        PLATE_CHECK(near_scaled((upper_next_row.fy - upper_top.fy) * kFloatTargetH, crop_h * 5.0f / 12.0f));
        PLATE_CHECK(near_scaled((upper_next_col.fx - upper_top.fx) * static_cast<float>(half_width), crop_w));
        PLATE_CHECK(upper_top.fy > crop_y - 0.5f && upper_top.fy < crop_y + 0.5f);

        // 下半：48 行步长之和 == crop_h - crop_h/3；起点 == crop_y + crop_h/3
        PLATE_CHECK(near_scaled((lower_next_row.fy - lower_top.fy) * kFloatTargetH, crop_h - crop_h / 3.0f));
        PLATE_CHECK(
            near_scaled((lower_next_col.fx - lower_top.fx) * static_cast<float>(kTargetW - half_width), crop_w));
        PLATE_CHECK(lower_top.fy > crop_y + crop_h / 3.0f - 0.5f);

        // 两段不越界（参考实现刻意让 [1/3, 5/12) 重叠，所以只钉上下界）
        for (int y = 0; y < kTargetH; y += 5) {
            PLATE_CHECK(plate_sample_point(kTargetW - 1, y, kTargetW, kTargetH, crop_x, crop_y, crop_w, crop_h,
                                           PlateKind::DoubleLine).fy < crop_y + crop_h);
            PLATE_CHECK(plate_sample_point(0, y, kTargetW, kTargetH, crop_x, crop_y, crop_w, crop_h,
                                           PlateKind::DoubleLine).fy >= crop_y - 0.5f);
        }
    }
}

/** @brief 号牌结构校验：合法牌必须过，结构性废牌必须被点名拒绝 */
inline void check_plate_format() {
    // 合法：普通 7 位 / 教练 / 警用 / 双层新能 / 大型新能 / 挂车 / 使领馆
    PLATE_CHECK(validate_plate_number("豫B2V9L7").valid);
    PLATE_CHECK(validate_plate_number("辽DU4356").valid);
    PLATE_CHECK(validate_plate_number("京A12345").valid);
    PLATE_CHECK(validate_plate_number("粤AD12345").valid);
    PLATE_CHECK(validate_plate_number("粤AF12345").valid);
    PLATE_CHECK(validate_plate_number("京A12345D").valid);
    PLATE_CHECK(validate_plate_number("京A12345F").valid);
    PLATE_CHECK(validate_plate_number("京A1234警").valid);
    PLATE_CHECK(validate_plate_number("粤B1234学").valid);
    PLATE_CHECK(validate_plate_number("冀B1234挂").valid);
    PLATE_CHECK(validate_plate_number("使123456").valid);
    PLATE_CHECK(validate_plate_number("领123456").valid);

    // 字符表的额外尾字（险/品）必须被接受：模型能解出来、校验永远否决 = 静默丢真牌
    PLATE_CHECK(validate_plate_number("京A1234险").valid);
    PLATE_CHECK(validate_plate_number("冀B1234品").valid);

    // 非法：长度 / 省份 / 发牌机关（I、O）/ 序号位 / 新能源布局 / UTF-8
    PLATE_CHECK(validate_plate_number("").problem == PlateFormatProblem::BadLength);
    PLATE_CHECK(validate_plate_number("京A12").problem == PlateFormatProblem::BadLength);
    PLATE_CHECK(validate_plate_number("京A12345678").problem == PlateFormatProblem::BadLength);
    PLATE_CHECK(validate_plate_number("美A12345").problem == PlateFormatProblem::BadProvince);
    PLATE_CHECK(validate_plate_number("京I12345").problem == PlateFormatProblem::BadAuthority);
    PLATE_CHECK(validate_plate_number("京O12345").problem == PlateFormatProblem::BadAuthority);
    PLATE_CHECK(validate_plate_number("京A1学345").problem == PlateFormatProblem::BadCharacter);
    PLATE_CHECK(validate_plate_number("京A123456").problem == PlateFormatProblem::BadNewEnergyLayout);
    PLATE_CHECK(validate_plate_number("\xE4\xBA").problem == PlateFormatProblem::InvalidUtf8);
    // 过长编码（overlong）：C0 80 是「用两字节表示 U+0000」，必须判非法
    PLATE_CHECK(validate_plate_number("\xC0\x80").problem == PlateFormatProblem::InvalidUtf8);
}

/** @brief 帧视图守卫：非法帧必须在进入推理前就被拒绝，而不是靠「跑一遍看结果」 */
inline void check_frame_view_guard() {
    uint8_t dummy[3 * 100 * 100] = {0};

    zhulong_frame_view good{};
    good.planes[0] = dummy;
    good.width = 100;
    good.height = 100;
    good.format = ZHULONG_PIXEL_FORMAT_RGB24;
    good.strides[0] = 300;
    PLATE_CHECK(frame_view_supported(good));

    // 负 stride：底朝上的缓冲区，静默当 top-down 读会上下翻转且无错误码
    zhulong_frame_view negative_stride = good;
    negative_stride.strides[0] = -300;
    PLATE_CHECK(!frame_view_supported(negative_stride));

    // 缺 UV 平面：NV12/YUV420P 少平面时按 packed 解释等于整幅串色
    zhulong_frame_view nv12_without_uv = good;
    nv12_without_uv.format = ZHULONG_PIXEL_FORMAT_NV12;
    nv12_without_uv.strides[0] = 100;
    PLATE_CHECK(!frame_view_supported(nv12_without_uv));
    nv12_without_uv.planes[1] = dummy;
    PLATE_CHECK(frame_view_supported(nv12_without_uv));

    zhulong_frame_view i420_without_v = good;
    i420_without_v.format = ZHULONG_PIXEL_FORMAT_YUV420P;
    i420_without_v.planes[1] = dummy;
    PLATE_CHECK(!frame_view_supported(i420_without_v));

    // RGBA32：检测侧 letterbox 同样拒绝，两处必须一致（否则「检出为空、识别恒空」难归因）
    zhulong_frame_view rgba = good;
    rgba.format = ZHULONG_PIXEL_FORMAT_RGBA32;
    rgba.strides[0] = 400;
    PLATE_CHECK(!frame_view_supported(rgba));

    // 空平面 / 非正尺寸
    zhulong_frame_view no_plane = good;
    no_plane.planes[0] = nullptr;
    PLATE_CHECK(!frame_view_supported(no_plane));
    zhulong_frame_view zero_size = good;
    zero_size.height = 0;
    PLATE_CHECK(!frame_view_supported(zero_size));

    // stride 容不下一行像素 = 按它读就越界；stride == 0 是紧凑排列，必须放行
    zhulong_frame_view tight_stride = good;
    tight_stride.strides[0] = 100 * 3 - 1;
    PLATE_CHECK(!frame_view_supported(tight_stride));
    zhulong_frame_view compact_stride = good;
    compact_stride.strides[0] = 0;
    PLATE_CHECK(frame_view_supported(compact_stride));

    // NV12：UV 平面一行是 2*ceil(width/2) 字节；奇数宽下比 width 大 1
    zhulong_frame_view odd_nv12 = good;
    odd_nv12.width = 101;
    odd_nv12.height = 100;
    odd_nv12.format = ZHULONG_PIXEL_FORMAT_NV12;
    odd_nv12.planes[1] = dummy;
    odd_nv12.strides[0] = 101;
    odd_nv12.strides[1] = 101;  // 需要 102
    PLATE_CHECK(!frame_view_supported(odd_nv12));
    odd_nv12.strides[1] = 102;
    PLATE_CHECK(frame_view_supported(odd_nv12));
}

/** @brief 可接受区间的边界语义：(0, 1]，0 与负数、>1 全部越界 */
inline constexpr bool threshold_in_range_self_check(double value) noexcept {
    return value > kThresholdMinExclusive && value <= kThresholdMaxInclusive;
}

/** @brief 默认值 / 类别语义：改一处忘一处会导致「schema 说 A、算法跑 B」 */
inline void check_defaults_and_semantics() {
    PLATE_CHECK(threshold_in_range_self_check(static_cast<double>(kDefaultConfidenceThreshold)));
    PLATE_CHECK(threshold_in_range_self_check(static_cast<double>(kDefaultNmsThreshold)));
    PLATE_CHECK(!threshold_in_range_self_check(0.0));
    PLATE_CHECK(!threshold_in_range_self_check(1.0001));
    PLATE_CHECK(threshold_in_range_self_check(1.0));

    // 检测头的类别索引 → 语义：只有 1/2 有专门含义，其余（含预留位）按单层采样
    PLATE_CHECK(plate_kind_from_index(0) == PlateKind::SingleLine);
    PLATE_CHECK(plate_kind_from_index(1) == PlateKind::DoubleLine);
    PLATE_CHECK(plate_kind_from_index(2) == PlateKind::NewEnergy);
    PLATE_CHECK(plate_kind_from_index(3) == PlateKind::SingleLine);
    PLATE_CHECK(plate_kind_from_index(-1) == PlateKind::SingleLine);
}

/**
 * @brief 跑完全部纯逻辑检查
 * @return 失败条数（0 表示全过）；调用方负责把它变成退出码
 */
inline int run_pure_unit_checks() {
    const int before = failure_count();
    check_sampling_geometry();
    check_plate_format();
    check_frame_view_guard();
    check_defaults_and_semantics();
    return failure_count() - before;
}

} // namespace zhulong::plate::test
