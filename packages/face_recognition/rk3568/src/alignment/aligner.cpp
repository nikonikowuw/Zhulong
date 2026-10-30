#include "aligner.hpp"

#include <zhulong/runtime/yuv_sampling.hpp>

#include <algorithm>
#include <cmath>
#include <cstddef>

namespace zhulong::face {

namespace {

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

    if (raw.planes[0]) {
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
        return;
    }

    out_r = out_g = out_b = 0;
}

} // namespace

bool Aligner::estimate_similarity_transform(
    const std::array<Point2f, 5>& src_points,
    std::array<float, 6>& out_mat
) {
    // 1. 计算质心
    Point2f src_mean{0.0f, 0.0f};
    Point2f dst_mean{0.0f, 0.0f};
    for (size_t i = 0; i < 5; ++i) {
        src_mean.x += src_points[i].x;
        src_mean.y += src_points[i].y;
        dst_mean.x += ARCFACE_REF_POINTS[i].x;
        dst_mean.y += ARCFACE_REF_POINTS[i].y;
    }
    src_mean.x /= 5.0f;
    src_mean.y /= 5.0f;
    dst_mean.x /= 5.0f;
    dst_mean.y /= 5.0f;

    // 2. 去中心化并计算源点方差与协方差矩阵
    float src_var = 0.0f;
    float s00 = 0.0f, s01 = 0.0f;
    float s10 = 0.0f, s11 = 0.0f;

    for (size_t i = 0; i < 5; ++i) {
        float sx = src_points[i].x - src_mean.x;
        float sy = src_points[i].y - src_mean.y;
        float dx = ARCFACE_REF_POINTS[i].x - dst_mean.x;
        float dy = ARCFACE_REF_POINTS[i].y - dst_mean.y;

        src_var += sx * sx + sy * sy;
        s00 += dx * sx;
        s01 += dx * sy;
        s10 += dy * sx;
        s11 += dy * sy;
    }

    src_var /= 5.0f;
    s00 /= 5.0f;
    s01 /= 5.0f;
    s10 /= 5.0f;
    s11 /= 5.0f;

    if (src_var < 1e-6f) {
        return false;
    }

    // 3. 2D 协方差矩阵极分解求解最优旋转与缩放
    // 矩阵形式为 A = [s00 s01; s10 s11]
    // 旋转角 theta = atan2(s10 - s01, s00 + s11)
    float cos_t = s00 + s11;
    float sin_t = s10 - s01;
    float norm_t = std::sqrt(cos_t * cos_t + sin_t * sin_t);
    if (norm_t < 1e-6f) {
        return false;
    }
    cos_t /= norm_t;
    sin_t /= norm_t;

    // 缩放系数 scale = (s00*cos + s10*sin - s01*sin + s11*cos) / src_var
    float scale = (s00 * cos_t + s10 * sin_t - s01 * (-sin_t) + s11 * cos_t) / src_var;
    if (scale <= 0.0f) {
        scale = 1.0f;
    }

    // 平移向量 t = dst_mean - scale * R * src_mean
    float a = scale * cos_t;
    float b = -scale * sin_t;
    float c = scale * sin_t;
    float d = scale * cos_t;

    float tx = dst_mean.x - (a * src_mean.x + b * src_mean.y);
    float ty = dst_mean.y - (c * src_mean.x + d * src_mean.y);

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
    if (!dst_buf || dst_w <= 0 || dst_h <= 0) {
        return false;
    }

    const auto& raw = image.raw_view();
    if (raw.planes[0] == nullptr || raw.width <= 0 || raw.height <= 0) {
        return false;
    }

    // 求解 2x3 相似变换的逆矩阵
    // M = [m0 m1 m2; m3 m4 m5]
    // det = m0*m4 - m1*m3
    const float m0 = affine_mat[0];
    const float m1 = affine_mat[1];
    const float m2 = affine_mat[2];
    const float m3 = affine_mat[3];
    const float m4 = affine_mat[4];
    const float m5 = affine_mat[5];

    const float det = m0 * m4 - m1 * m3;
    if (std::abs(det) < 1e-7f) {
        return false;
    }

    const float inv_det = 1.0f / det;
    const float im0 = m4 * inv_det;
    const float im1 = -m1 * inv_det;
    const float im3 = -m3 * inv_det;
    const float im4 = m0 * inv_det;

    for (int dy = 0; dy < dst_h; ++dy) {
        uint8_t* dst_row = dst_buf + static_cast<size_t>(dy) * dst_w * 3;
        for (int dx = 0; dx < dst_w; ++dx) {
            float ox = static_cast<float>(dx) - m2;
            float oy = static_cast<float>(dy) - m5;

            float sx = im0 * ox + im1 * oy;
            float sy = im3 * ox + im4 * oy;

            uint8_t r = 0;
            uint8_t g = 0;
            uint8_t b = 0;
            sample_pixel_rgb(raw, sx, sy, r, g, b);
            dst_row[dx * 3 + 0] = r;
            dst_row[dx * 3 + 1] = g;
            dst_row[dx * 3 + 2] = b;
        }
    }

    return true;
}

std::vector<uint8_t> Aligner::crop_and_align(
    const zhulong::Image& image,
    const std::array<float, 6>& affine_mat,
    int dst_w,
    int dst_h
) {
    if (dst_w <= 0 || dst_h <= 0) {
        return {};
    }

    std::vector<uint8_t> dst_patch(static_cast<size_t>(dst_w) * dst_h * 3, 0);
    if (!crop_and_align_direct(image, affine_mat, dst_patch.data(), dst_w, dst_h)) {
        return dst_patch;
    }
    return dst_patch;
}

} // namespace zhulong::face
