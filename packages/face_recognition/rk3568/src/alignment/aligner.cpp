#include "aligner.hpp"

#include <zhulong/runtime/yuv_sampling.hpp>

#include <algorithm>
#include <cmath>
#include <cstddef>

namespace zhulong::face {

namespace {

/** @brief 退化判据：源点方差/旋转范数低于它视为无法求解（近共线的 5 点） */
constexpr float kDegenerateThreshold = 1e-6f;
/** @brief 仿射矩阵行列式下限（可逆性判据） */
constexpr float kSingularDeterminant = 1e-7f;
/** @brief 只接受这些 packed/YUV 像素格式；其余一律 fail closed */
constexpr zhulong_pixel_format_t kSupportedFormats[] = {
    ZHULONG_PIXEL_FORMAT_RGB24,
    ZHULONG_PIXEL_FORMAT_BGR24,
    ZHULONG_PIXEL_FORMAT_YUV420P,
    ZHULONG_PIXEL_FORMAT_NV12,
};

bool is_supported_format(zhulong_pixel_format_t format) {
    for (const auto supported : kSupportedFormats) {
        if (format == supported) {
            return true;
        }
    }
    return false;
}

/** @brief 三通道交错 packed 像素的双线性采样（一像素写出 c0/c1/c2） */
inline void sample_packed_channels(
    const uint8_t* src, int w, int h, int stride,
    float fx, float fy, float& out_c0, float& out_c1, float& out_c2
) {
    fx = std::clamp(fx, 0.0f, static_cast<float>(w - 1));
    fy = std::clamp(fy, 0.0f, static_cast<float>(h - 1));

    const int x0 = static_cast<int>(fx);
    const int y0 = static_cast<int>(fy);
    const int x1 = std::min(x0 + 1, w - 1);
    const int y1 = std::min(y0 + 1, h - 1);

    const float dx = fx - static_cast<float>(x0);
    const float dy = fy - static_cast<float>(y0);

    const uint8_t* p00 = src + static_cast<size_t>(y0) * stride + static_cast<size_t>(x0) * 3;
    const uint8_t* p01 = src + static_cast<size_t>(y0) * stride + static_cast<size_t>(x1) * 3;
    const uint8_t* p10 = src + static_cast<size_t>(y1) * stride + static_cast<size_t>(x0) * 3;
    const uint8_t* p11 = src + static_cast<size_t>(y1) * stride + static_cast<size_t>(x1) * 3;

    out_c0 = (1.0f - dx) * (1.0f - dy) * p00[0] + dx * (1.0f - dy) * p01[0] +
             (1.0f - dx) * dy * p10[0] + dx * dy * p11[0];
    out_c1 = (1.0f - dx) * (1.0f - dy) * p00[1] + dx * (1.0f - dy) * p01[1] +
             (1.0f - dx) * dy * p10[1] + dx * dy * p11[1];
    out_c2 = (1.0f - dx) * (1.0f - dy) * p00[2] + dx * (1.0f - dy) * p01[2] +
             (1.0f - dx) * dy * p10[2] + dx * dy * p11[2];
}

/** @brief RGB24/BGR24/YUV420P/NV12 通用单点采样（格式已在入口校验） */
inline void sample_pixel_rgb(
    const zhulong_frame_view& raw,
    float fx, float fy, uint8_t& out_r, uint8_t& out_g, uint8_t& out_b
) {
    const int w = raw.width;
    const int h = raw.height;
    const int uv_w = (w + 1) / 2;
    const int uv_h = (h + 1) / 2;
    const float uv_x = (fx + 0.5f) * static_cast<float>(uv_w) / static_cast<float>(w) - 0.5f;
    const float uv_y = (fy + 0.5f) * static_cast<float>(uv_h) / static_cast<float>(h) - 0.5f;

    if (raw.format == ZHULONG_PIXEL_FORMAT_YUV420P && raw.planes[0] && raw.planes[1] && raw.planes[2]) {
        const int u_stride = raw.strides[1] > 0 ? raw.strides[1] : uv_w;
        const int v_stride = raw.strides[2] > 0 ? raw.strides[2] : uv_w;

        const float y_val = zhulong::sample_plane_bilinear(raw.planes[0], w, h, raw.strides[0] > 0 ? raw.strides[0] : w, fx, fy);
        const float u_val = zhulong::sample_plane_bilinear(raw.planes[1], uv_w, uv_h, u_stride, uv_x, uv_y);
        const float v_val = zhulong::sample_plane_bilinear(raw.planes[2], uv_w, uv_h, v_stride, uv_x, uv_y);
        zhulong::yuv_to_rgb_u8(y_val, u_val, v_val, out_r, out_g, out_b);
        return;
    }

    if (raw.format == ZHULONG_PIXEL_FORMAT_NV12 && raw.planes[0] && raw.planes[1]) {
        const int uv_stride = raw.strides[1] > 0 ? raw.strides[1] : w;

        const float y_val = zhulong::sample_plane_bilinear(raw.planes[0], w, h, raw.strides[0] > 0 ? raw.strides[0] : w, fx, fy);
        float u_val = 128.0f;
        float v_val = 128.0f;
        zhulong::sample_nv12_uv_bilinear(raw.planes[1], uv_w, uv_h, uv_stride, uv_x, uv_y, u_val, v_val);
        zhulong::yuv_to_rgb_u8(y_val, u_val, v_val, out_r, out_g, out_b);
        return;
    }

    const int stride = raw.strides[0] > 0 ? raw.strides[0] : w * 3;
    float c0 = 0.0f;
    float c1 = 0.0f;
    float c2 = 0.0f;
    sample_packed_channels(raw.planes[0], w, h, stride, fx, fy, c0, c1, c2);
    if (raw.format == ZHULONG_PIXEL_FORMAT_BGR24) {
        out_r = static_cast<uint8_t>(std::clamp(c2, 0.0f, 255.0f));
        out_g = static_cast<uint8_t>(std::clamp(c1, 0.0f, 255.0f));
        out_b = static_cast<uint8_t>(std::clamp(c0, 0.0f, 255.0f));
    } else {
        out_r = static_cast<uint8_t>(std::clamp(c0, 0.0f, 255.0f));
        out_g = static_cast<uint8_t>(std::clamp(c1, 0.0f, 255.0f));
        out_b = static_cast<uint8_t>(std::clamp(c2, 0.0f, 255.0f));
    }
}

} // namespace

bool Aligner::estimate_similarity_transform(
    const std::array<Point2f, 5>& src_points,
    std::array<float, 6>& out_mat
) {
    constexpr size_t kPointCount = 5;

    // 1. 计算质心
    Point2f src_mean{0.0f, 0.0f};
    Point2f dst_mean{0.0f, 0.0f};
    for (size_t i = 0; i < kPointCount; ++i) {
        src_mean.x += src_points[i].x;
        src_mean.y += src_points[i].y;
        dst_mean.x += kArcfaceRefPoints[i].x;
        dst_mean.y += kArcfaceRefPoints[i].y;
    }
    constexpr float kCountAsFloat = static_cast<float>(kPointCount);
    src_mean.x /= kCountAsFloat;
    src_mean.y /= kCountAsFloat;
    dst_mean.x /= kCountAsFloat;
    dst_mean.y /= kCountAsFloat;

    // 2. 去中心化并计算源点方差与协方差 Σ[j][k] = Σ dst_j * src_k
    float src_var = 0.0f;
    float s00 = 0.0f;
    float s01 = 0.0f;
    float s10 = 0.0f;
    float s11 = 0.0f;

    for (size_t i = 0; i < kPointCount; ++i) {
        const float sx = src_points[i].x - src_mean.x;
        const float sy = src_points[i].y - src_mean.y;
        const float dx = kArcfaceRefPoints[i].x - dst_mean.x;
        const float dy = kArcfaceRefPoints[i].y - dst_mean.y;

        src_var += sx * sx + sy * sy;
        s00 += dx * sx;
        s01 += dx * sy;
        s10 += dy * sx;
        s11 += dy * sy;
    }

    src_var /= kCountAsFloat;
    s00 /= kCountAsFloat;
    s01 /= kCountAsFloat;
    s10 /= kCountAsFloat;
    s11 /= kCountAsFloat;

    if (src_var < kDegenerateThreshold) {
        return false;
    }

    // 3. 2D 极分解求最优旋转（R = [[cos, -sin], [sin, cos]]）
    //
    // 目标：最大化 Σ qᵀRp = cos·(s00 + s11) + sin·(s10 - s01)
    float cos_t = s00 + s11;
    float sin_t = s10 - s01;
    const float norm_t = std::sqrt(cos_t * cos_t + sin_t * sin_t);
    if (norm_t < kDegenerateThreshold) {
        return false;
    }
    cos_t /= norm_t;
    sin_t /= norm_t;

    // 4. 缩放系数 = trace(Rᵀ Σ) / src_var
    //    = (cos·(s00 + s11) + sin·(s10 - s01)) / src_var
    // ⚠️ 交叉项 s01 的系数是 -sin（历史缺陷曾写成 +sin，即 `- s01 * (-sin)`），
    //    那会让带滚转的人脸被缩放到比参考模板小几个百分点，进而稀释特征判别力。
    const float scale = (cos_t * (s00 + s11) + sin_t * (s10 - s01)) / src_var;
    if (!std::isfinite(scale) || scale <= 0.0f) {
        return false;
    }

    // 5. 平移向量 t = dst_mean - scale · R · src_mean
    const float a = scale * cos_t;
    const float b = -scale * sin_t;
    const float c = scale * sin_t;
    const float d = scale * cos_t;
    const float tx = dst_mean.x - (a * src_mean.x + b * src_mean.y);
    const float ty = dst_mean.y - (c * src_mean.x + d * src_mean.y);

    // 6. 结果必须全部有限：NaN 会一路污染特征向量，最后表现为「相似度全是 0」
    if (!std::isfinite(a) || !std::isfinite(b) || !std::isfinite(tx) ||
        !std::isfinite(c) || !std::isfinite(d) || !std::isfinite(ty)) {
        return false;
    }

    out_mat = {a, b, tx, c, d, ty};
    return true;
}

bool Aligner::crop_and_align_direct(
    const zhulong::Image& image,
    const std::array<float, 6>& affine_mat,
    uint8_t* dst_buf,
    int dst_w,
    int dst_h
) {
    if (dst_buf == nullptr || dst_w <= 0 || dst_h <= 0) {
        return false;
    }

    const auto& raw = image.raw_view();
    if (raw.planes[0] == nullptr || raw.width <= 0 || raw.height <= 0) {
        return false;
    }
    // 负 stride（上下翻转或 DMA-BUF 特殊布局）不在本采样器的建模范围内：
    // 静默按正向读取会采到别人的内存，必须显式失败。
    if (raw.strides[0] < 0 || raw.strides[1] < 0 || raw.strides[2] < 0) {
        return false;
    }
    // 未支持的格式不能退化成「当三通道 packed 读」—— 那会产出颜色全错的裁剪块，
    // 特征提取看起来成功但结果无意义。
    if (!is_supported_format(raw.format)) {
        return false;
    }

    // 求解 2x3 相似变换的逆矩阵 M⁻¹ = [im0 im1 itx; im3 im4 ity]
    const float m0 = affine_mat[0];
    const float m1 = affine_mat[1];
    const float m2 = affine_mat[2];
    const float m3 = affine_mat[3];
    const float m4 = affine_mat[4];
    const float m5 = affine_mat[5];

    const float det = m0 * m4 - m1 * m3;
    if (!std::isfinite(det) || std::abs(det) < kSingularDeterminant) {
        return false;
    }

    const float inv_det = 1.0f / det;
    const float im0 = m4 * inv_det;
    const float im1 = -m1 * inv_det;
    const float im3 = -m3 * inv_det;
    const float im4 = m0 * inv_det;

    for (int dy = 0; dy < dst_h; ++dy) {
        uint8_t* dst_row = dst_buf + static_cast<size_t>(dy) * static_cast<size_t>(dst_w) * 3;
        for (int dx = 0; dx < dst_w; ++dx) {
            const float ox = static_cast<float>(dx) - m2;
            const float oy = static_cast<float>(dy) - m5;

            const float sx = im0 * ox + im1 * oy;
            const float sy = im3 * ox + im4 * oy;

            uint8_t r = 0;
            uint8_t g = 0;
            uint8_t b = 0;
            sample_pixel_rgb(raw, sx, sy, r, g, b);
            const size_t dst_offset = static_cast<size_t>(dx) * 3;
            dst_row[dst_offset + 0] = r;
            dst_row[dst_offset + 1] = g;
            dst_row[dst_offset + 2] = b;
        }
    }

    return true;
}

} // namespace zhulong::face
