/**
 * @file face_enroll_enhance.hpp
 * @brief 底库录入与静态特征提取的纯数据图像增强与翻转 TTA 工具（Header-only，无模型依赖）
 *
 * 职责边界：
 *   1. `flip_rgb24_horizontal` + `fuse_l2_normalized_features`：对齐后 112×112 脸块的水平镜像
 *      TTA 特征融合，消除单侧光照/轻微偏航带来的不对称噪声；
 *   2. `rotate_rgb24`：无 EXIF 旋转信息的侧躺/倒置录入照兜底转正（90°/180°/270°）；
 *   3. `auto_enhance_dark_patch_rgb24` / `apply_gamma_rgb24`：仅针对欠曝/暗光图的受控 Gamma 提亮
 *      （正常曝光图不改像素，避免人为拉大与视频流之间的域偏移）。
 */

#pragma once

#include "common/face_image_quality.hpp"

#include <algorithm>
#include <array>
#include <cmath>
#include <cstddef>
#include <cstdint>
#include <limits>
#include <vector>

namespace zhulong::face {

/** @brief 暗光增强触发的平均亮度上限（BT.601 Y < 75 视为偏暗） */
inline constexpr float kEnrollDarkLumaThreshold = 75.0f;
/** @brief 暗光自适应 Gamma 校正的目标平均亮度 */
inline constexpr float kEnrollTargetLuma = 115.0f;
/** @brief 检测兜底时使用的固定暗光提亮 Gamma（< 1.0 提亮暗部） */
inline constexpr float kEnrollDetectGamma = 0.60f;
/** @brief 自适应 Gamma 下限（避免极暗图过度放大传感器噪声） */
inline constexpr float kEnrollMinAdaptiveGamma = 0.55f;
/** @brief 自适应 Gamma 上限 */
inline constexpr float kEnrollMaxAdaptiveGamma = 0.92f;
/** @brief 禁止触发提亮的过曝像素比例上限（防止本已局部高光的逆光人脸被拉爆） */
inline constexpr float kEnrollMaxOverexposureForEnhance = 0.02f;
/** @brief 特征合并时的零范数保护下限 */
inline constexpr double kEnrollFusionNormEps = 1e-6;

/**
 * @brief 对紧凑 RGB24 图像做水平镜像翻转（`src` 与 `dst` 不得重叠）
 */
inline bool flip_rgb24_horizontal(const uint8_t* src, uint8_t* dst, int width, int height) noexcept {
    if (src == nullptr || dst == nullptr || src == dst || width <= 0 || height <= 0) {
        return false;
    }
    const size_t w = static_cast<size_t>(width);
    const size_t h = static_cast<size_t>(height);
    if (w > std::numeric_limits<size_t>::max() / 3 ||
        h > std::numeric_limits<size_t>::max() / (w * 3)) {
        return false;
    }

    const size_t row_bytes = w * 3;
    for (size_t y = 0; y < h; ++y) {
        const uint8_t* src_row = src + y * row_bytes;
        uint8_t* dst_row = dst + y * row_bytes;
        for (size_t x = 0; x < w; ++x) {
            const size_t src_off = (w - 1 - x) * 3;
            const size_t dst_off = x * 3;
            dst_row[dst_off + 0] = src_row[src_off + 0];
            dst_row[dst_off + 1] = src_row[src_off + 1];
            dst_row[dst_off + 2] = src_row[src_off + 2];
        }
    }
    return true;
}

/**
 * @brief 按顺时针角度（90 / 180 / 270）旋转 RGB24 图像并写入紧凑缓冲
 */
inline bool rotate_rgb24(const uint8_t* src,
                         int width,
                         int height,
                         int stride,
                         int cw_degrees,
                         std::vector<uint8_t>& out_buf,
                         int& out_width,
                         int& out_height) {
    if (src == nullptr || width <= 0 || height <= 0) {
        return false;
    }
    const int64_t min_stride = static_cast<int64_t>(width) * 3;
    if (min_stride > std::numeric_limits<int32_t>::max()) {
        return false;
    }
    const int effective_stride = (stride == 0) ? static_cast<int>(min_stride) : stride;
    if (effective_stride < min_stride) {
        return false;
    }
    if (cw_degrees != 90 && cw_degrees != 180 && cw_degrees != 270) {
        return false;
    }

    const int dst_w = (cw_degrees == 180) ? width : height;
    const int dst_h = (cw_degrees == 180) ? height : width;
    const size_t dst_bytes = static_cast<size_t>(dst_w) * static_cast<size_t>(dst_h) * 3;
    out_buf.resize(dst_bytes);

    for (int sy = 0; sy < height; ++sy) {
        const uint8_t* src_row = src + static_cast<size_t>(sy) * static_cast<size_t>(effective_stride);
        for (int sx = 0; sx < width; ++sx) {
            int dx = 0;
            int dy = 0;
            if (cw_degrees == 90) {
                dx = height - 1 - sy;
                dy = sx;
            } else if (cw_degrees == 180) {
                dx = width - 1 - sx;
                dy = height - 1 - sy;
            } else {
                dx = sy;
                dy = width - 1 - sx;
            }
            const size_t src_off = static_cast<size_t>(sx) * 3;
            const size_t dst_off =
                (static_cast<size_t>(dy) * static_cast<size_t>(dst_w) + static_cast<size_t>(dx)) * 3;
            out_buf[dst_off + 0] = src_row[src_off + 0];
            out_buf[dst_off + 1] = src_row[src_off + 1];
            out_buf[dst_off + 2] = src_row[src_off + 2];
        }
    }

    out_width = dst_w;
    out_height = dst_h;
    return true;
}

/**
 * @brief 估计 RGB24 图像的 BT.601 平均亮度（大图自动按步长抽样，保持微秒级耗时）
 */
inline bool compute_rgb24_mean_luma(const uint8_t* rgb,
                                    int width,
                                    int height,
                                    int stride,
                                    float& out_mean_luma) noexcept {
    if (rgb == nullptr || width <= 0 || height <= 0) {
        return false;
    }
    const int64_t min_stride = static_cast<int64_t>(width) * 3;
    if (min_stride > std::numeric_limits<int32_t>::max()) {
        return false;
    }
    const int effective_stride = (stride == 0) ? static_cast<int>(min_stride) : stride;
    if (effective_stride < min_stride) {
        return false;
    }

    const int step = std::max(1, std::min(width, height) / 160);
    double sum = 0.0;
    size_t count = 0;
    for (int y = 0; y < height; y += step) {
        const uint8_t* row = rgb + static_cast<size_t>(y) * static_cast<size_t>(effective_stride);
        for (int x = 0; x < width; x += step) {
            sum += static_cast<double>(rgb_pixel_luma(row, static_cast<size_t>(x) * 3));
            ++count;
        }
    }
    if (count == 0) {
        return false;
    }
    out_mean_luma = static_cast<float>(sum / static_cast<double>(count));
    return std::isfinite(out_mean_luma);
}

/**
 * @brief 使用 256 项 LUT 对 RGB24 图像做快速 Gamma 校正并输出紧凑 RGB24 缓冲
 */
inline bool apply_gamma_rgb24(const uint8_t* src,
                              int width,
                              int height,
                              int stride,
                              float gamma,
                              std::vector<uint8_t>& out_buf) {
    if (src == nullptr || width <= 0 || height <= 0 || !std::isfinite(gamma) || gamma <= 0.0f) {
        return false;
    }
    const int64_t min_stride = static_cast<int64_t>(width) * 3;
    if (min_stride > std::numeric_limits<int32_t>::max()) {
        return false;
    }
    const int effective_stride = (stride == 0) ? static_cast<int>(min_stride) : stride;
    if (effective_stride < min_stride) {
        return false;
    }

    std::array<uint8_t, 256> lut{};
    for (int v = 0; v < 256; ++v) {
        const float norm = static_cast<float>(v) / 255.0f;
        const float mapped = std::pow(norm, gamma) * 255.0f;
        lut[static_cast<size_t>(v)] = static_cast<uint8_t>(std::clamp(std::lround(mapped), 0L, 255L));
    }

    const size_t dst_bytes = static_cast<size_t>(width) * static_cast<size_t>(height) * 3;
    out_buf.resize(dst_bytes);
    const size_t row_bytes = static_cast<size_t>(width) * 3;
    for (int y = 0; y < height; ++y) {
        const uint8_t* src_row = src + static_cast<size_t>(y) * static_cast<size_t>(effective_stride);
        uint8_t* dst_row = out_buf.data() + static_cast<size_t>(y) * row_bytes;
        for (size_t i = 0; i < row_bytes; ++i) {
            dst_row[i] = lut[src_row[i]];
        }
    }
    return true;
}

/**
 * @brief 仅当对齐人脸块确实偏暗且无明显高光过曝时，做受控自适应 Gamma 提亮
 *
 * 正常光照（mean_luma >= 75）直接返回 false，保证绝大多数照片保持原始自然像素域。
 */
inline bool auto_enhance_dark_patch_rgb24(const uint8_t* patch_rgb,
                                          int width,
                                          int height,
                                          std::vector<uint8_t>& out_buf) {
    const FaceImageQualityMetrics metrics = assess_face_image_quality(patch_rgb, width, height, 1.0f);
    if (!metrics.valid ||
        metrics.mean_luma >= kEnrollDarkLumaThreshold ||
        metrics.overexposed_ratio > kEnrollMaxOverexposureForEnhance) {
        return false;
    }

    const float clamped_luma = std::max(metrics.mean_luma, 8.0f);
    const float raw_gamma = std::log(kEnrollTargetLuma / 255.0f) / std::log(clamped_luma / 255.0f);
    if (!std::isfinite(raw_gamma)) {
        return false;
    }
    const float gamma = std::clamp(raw_gamma, kEnrollMinAdaptiveGamma, kEnrollMaxAdaptiveGamma);
    return apply_gamma_rgb24(patch_rgb, width, height, width * 3, gamma, out_buf);
}

/**
 * @brief 合并两路同维度单位特征（如原图 + 水平镜像 TTA），返回严格 L2 归一化特征
 *
 * 若 `feat_b` 不可用（维度不符/非有限/零向量），或正交相消导致求和模长归零，安全回退 `feat_a`。
 */
inline bool fuse_l2_normalized_features(const std::vector<float>& feat_a,
                                        const std::vector<float>& feat_b,
                                        std::vector<float>& out_fused) {
    if (feat_a.empty()) {
        return false;
    }

    auto l2_normalize_vec = [](const std::vector<float>& src, std::vector<float>& dst) -> bool {
        double sum_sq = 0.0;
        for (float v : src) {
            if (!std::isfinite(v)) {
                return false;
            }
            sum_sq += static_cast<double>(v) * static_cast<double>(v);
        }
        if (!std::isfinite(sum_sq) || sum_sq <= kEnrollFusionNormEps * kEnrollFusionNormEps) {
            return false;
        }
        const double inv_norm = 1.0 / std::sqrt(sum_sq);
        dst.resize(src.size());
        for (size_t i = 0; i < src.size(); ++i) {
            dst[i] = static_cast<float>(static_cast<double>(src[i]) * inv_norm);
        }
        return true;
    };

    std::vector<float> unit_a;
    if (!l2_normalize_vec(feat_a, unit_a)) {
        return false;
    }

    std::vector<float> unit_b;
    if (feat_b.size() != feat_a.size() || !l2_normalize_vec(feat_b, unit_b)) {
        out_fused = std::move(unit_a);
        return true;
    }

    std::vector<double> summed(unit_a.size(), 0.0);
    double sum_sq = 0.0;
    for (size_t i = 0; i < unit_a.size(); ++i) {
        const double v = static_cast<double>(unit_a[i]) + static_cast<double>(unit_b[i]);
        summed[i] = v;
        sum_sq += v * v;
    }

    if (!std::isfinite(sum_sq) || sum_sq <= kEnrollFusionNormEps * kEnrollFusionNormEps) {
        out_fused = std::move(unit_a);
        return true;
    }

    const double inv_norm = 1.0 / std::sqrt(sum_sq);
    out_fused.resize(summed.size());
    for (size_t i = 0; i < summed.size(); ++i) {
        out_fused[i] = static_cast<float>(summed[i] * inv_norm);
    }
    return true;
}

} // namespace zhulong::face
