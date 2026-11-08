/**
 * @file face_image_quality.hpp
 * @brief 对齐后紧凑 RGB 人脸块的无模型清晰度与曝光质量评估及流式前置门禁
 *
 * 设计约束：
 *   1. 纯头文件、仅依赖标准库与 `common/match_config.hpp`（遵循 algo-plugin-contract §4.1）；
 *   2. 零堆分配：直接扫描 `Aligner::crop_and_align_direct` 产出的紧凑 RGB24 脸块；
 *   3. 仅对小脸上采样做仿射行列式补偿：`sharpness = var_patch * max(1.0f, |det(affine)|)`。
 *      `< 112px` 远距小脸经双线性上采样后边缘梯度按尺度稀释，乘 `|det| = s²` 消除尺寸偏置；
 *      `>= 112px` 近景大脸已达 112×112 满采样分辨率，钳制下限 `1.0f` 防止 `< 1` 的 `|det|` 误拒清晰近景脸；
 *   4. 独立门禁开关 `enable_face_image_gate`（默认关闭），不改变 `FaceItem.quality` 几何分契约。
 */

#pragma once

#include "common/match_config.hpp"

#include <algorithm>
#include <array>
#include <cmath>
#include <cstddef>
#include <cstdint>
#include <limits>

namespace zhulong::face {

/** @brief 欠曝像素判定上界（严格小于：Y == 32.0f 计为正常像素） */
inline constexpr float kUnderexposedLumaThreshold = 32.0f;
/** @brief 过曝像素判定下界（严格大于：Y == 224.0f 计为正常像素） */
inline constexpr float kOverexposedLumaThreshold = 224.0f;

/** @brief 对齐脸块图像质量统计结果 */
struct FaceImageQualityMetrics {
    bool valid{false};                  /**< 输入缓冲、尺寸与仿射行列式是否合法 */
    float raw_laplacian_variance{0.0f}; /**< 未补偿的内部像素 4-邻域拉普拉斯方差 */
    float sharpness{0.0f};              /**< 上采样尺度补偿后清晰度: var_patch * max(1.0f, |det(affine)|) */
    float underexposed_ratio{0.0f};     /**< 内部像素 Y < 32.0f 占比 [0.0, 1.0] */
    float overexposed_ratio{0.0f};      /**< 内部像素 Y > 224.0f 占比 [0.0, 1.0] */
    float mean_luma{0.0f};              /**< 内部像素平均亮度 Y */
};

/** @brief 计算 2x3 行优先仿射矩阵 (src -> dst) 的行列式 m0*m4 - m1*m3 */
[[nodiscard]] inline float affine_determinant(const std::array<float, 6>& affine_mat) noexcept {
    return affine_mat[0] * affine_mat[4] - affine_mat[1] * affine_mat[3];
}

/** @brief BT.601 浮点亮度（RGB=(32,32,32) 与 (224,224,224) 在 IEEE-754 含 FMA 下均精确等于 32.0f/224.0f） */
[[nodiscard]] inline float rgb_pixel_luma(const uint8_t* rgb, size_t byte_offset) noexcept {
    return 0.299f * static_cast<float>(rgb[byte_offset + 0]) +
           0.587f * static_cast<float>(rgb[byte_offset + 1]) +
           0.114f * static_cast<float>(rgb[byte_offset + 2]);
}

/**
 * @brief 对齐块几何是否可用：与门限配置无关的廉价判定
 *
 * 单独抽出来是因为它有两处消费者：`assess_face_image_quality` 的前置校验，以及门禁在
 * **门限参数非法时**仍要区分「数据坏」（一律拒绝）与「配置坏」（放行）。
 */
[[nodiscard]] inline bool face_image_block_usable(const uint8_t* rgb,
                                                 int width,
                                                 int height,
                                                 float affine_det) noexcept {
    return rgb != nullptr && width >= 3 && height >= 3 &&
           std::isfinite(affine_det) && std::abs(affine_det) > 0.0f;
}

/**
 * @brief 统计紧凑 RGB24 对齐脸块的清晰度与曝光比例
 *
 * 只统计跳过 1px 边框后的内部像素 `N = (width - 2) * (height - 2)`；
 * 拉普拉斯方差采用 `double` 累加总体方差并钳制非负，避免纯色块单精度相消出微负值。
 */
[[nodiscard]] inline FaceImageQualityMetrics assess_face_image_quality(
    const uint8_t* rgb,
    int width,
    int height,
    float affine_det
) noexcept {
    FaceImageQualityMetrics metrics;
    if (!face_image_block_usable(rgb, width, height, affine_det)) {
        return metrics;
    }

    const float abs_det = std::abs(affine_det);

    const size_t w = static_cast<size_t>(width);
    const size_t h = static_cast<size_t>(height);
    if (w > std::numeric_limits<size_t>::max() / 3 ||
        h > std::numeric_limits<size_t>::max() / (w * 3)) {
        return metrics;
    }

    const size_t row_bytes = w * 3;
    const size_t interior_count = (w - 2) * (h - 2);
    if (interior_count == 0) {
        return metrics;
    }

    size_t dark_count = 0;
    size_t bright_count = 0;
    double luma_sum = 0.0;
    double lap_sum = 0.0;
    double lap_sum_sq = 0.0;

    for (size_t y = 1; y + 1 < h; ++y) {
        const size_t row_offset = y * row_bytes;
        const size_t top_row_offset = (y - 1) * row_bytes;
        const size_t bottom_row_offset = (y + 1) * row_bytes;

        float left_luma = rgb_pixel_luma(rgb, row_offset + 0);
        float center_luma = rgb_pixel_luma(rgb, row_offset + 3);

        for (size_t x = 1; x + 1 < w; ++x) {
            const size_t next_x_bytes = (x + 1) * 3;
            const size_t cur_x_bytes = x * 3;
            const float right_luma = rgb_pixel_luma(rgb, row_offset + next_x_bytes);
            const float top_luma = rgb_pixel_luma(rgb, top_row_offset + cur_x_bytes);
            const float bottom_luma = rgb_pixel_luma(rgb, bottom_row_offset + cur_x_bytes);

            if (center_luma < kUnderexposedLumaThreshold) {
                ++dark_count;
            } else if (center_luma > kOverexposedLumaThreshold) {
                ++bright_count;
            }

            const double c = static_cast<double>(center_luma);
            luma_sum += c;

            const double lap = 4.0 * c - (static_cast<double>(top_luma) +
                                          static_cast<double>(bottom_luma) +
                                          static_cast<double>(left_luma) +
                                          static_cast<double>(right_luma));
            lap_sum += lap;
            lap_sum_sq += lap * lap;

            left_luma = center_luma;
            center_luma = right_luma;
        }
    }

    const double inv_n = 1.0 / static_cast<double>(interior_count);
    const double mean_lap = lap_sum * inv_n;
    const double raw_var = std::max(0.0, lap_sum_sq * inv_n - mean_lap * mean_lap);
    const double scale_comp = static_cast<double>(std::max(1.0f, abs_det));
    const double compensated = raw_var * scale_comp;
    constexpr double kMaxFloatAsDouble = static_cast<double>(std::numeric_limits<float>::max());

    metrics.valid = true;
    metrics.raw_laplacian_variance = static_cast<float>(std::min(raw_var, kMaxFloatAsDouble));
    metrics.sharpness = static_cast<float>(std::min(compensated, kMaxFloatAsDouble));
    metrics.underexposed_ratio = static_cast<float>(static_cast<double>(dark_count) * inv_n);
    metrics.overexposed_ratio = static_cast<float>(static_cast<double>(bright_count) * inv_n);
    metrics.mean_luma = static_cast<float>(luma_sum * inv_n);
    return metrics;
}

/**
 * @brief 流式特征提取前的对齐脸块图像质量门禁（由 `extract_qualified_face` 与 `unit-test` 共用）
 *
 * 放行/拒绝语义分两类，**不要混用**：
 *   1. 开关关闭或门限参数非法（NaN/负值）→ **放行**。参数越界应由 `resolve_match_params`
 *      的区间钳制兜住；这里再 fail-closed 的后果是「一张脸都不出」，而线上只看得到
 *      「识别突然全失效」，根本看不出是哪个门限写错了。
 *   2. 对齐块本身非法（空指针、尺寸 < 3、仿射退化）→ **拒绝并写 `valid=false`**：
 *      那是数据问题，不是配置问题，放行只会把垃圾特征送进底库比对。
 */
[[nodiscard]] inline bool passes_face_image_gate(
    const uint8_t* rgb,
    int width,
    int height,
    float affine_det,
    const MatchParams& match_params,
    FaceImageQualityMetrics* out_metrics = nullptr
) noexcept {
    if (out_metrics != nullptr) {
        *out_metrics = FaceImageQualityMetrics{}; // 先置「无效」，任何早退路径都不会留下陈旧统计
    }
    if (!match_params.enable_face_image_gate) {
        return true;
    }
    // 数据本身非法（空缓冲 / 尺寸过小 / 仿射退化）与门限配置无关，一律拒绝
    if (!face_image_block_usable(rgb, width, height, affine_det)) {
        return false;
    }
    if (!std::isfinite(match_params.min_face_sharpness) ||
        !std::isfinite(match_params.max_face_underexposure) ||
        !std::isfinite(match_params.max_face_overexposure) ||
        match_params.min_face_sharpness < 0.0f ||
        match_params.max_face_underexposure < 0.0f ||
        match_params.max_face_overexposure < 0.0f) {
        return true; // 门限非法：放行并保持 metrics.valid=false，让调用侧能诊断配置问题
    }

    const FaceImageQualityMetrics metrics = assess_face_image_quality(rgb, width, height, affine_det);
    if (out_metrics != nullptr) {
        *out_metrics = metrics;
    }
    if (!metrics.valid) {
        return false;
    }

    return metrics.sharpness >= match_params.min_face_sharpness &&
           metrics.underexposed_ratio <= match_params.max_face_underexposure &&
           metrics.overexposed_ratio <= match_params.max_face_overexposure;
}

} // namespace zhulong::face
