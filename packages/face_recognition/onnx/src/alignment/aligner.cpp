#include "aligner.hpp"
#include <algorithm>
#include <cmath>

namespace zhulong::face {

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

    const uint8_t* src_data = raw.planes[0];
    const int src_w = raw.width;
    const int src_h = raw.height;
    const int src_stride = raw.strides[0] > 0 ? raw.strides[0] : src_w * 3;
    const bool is_bgr = (raw.format == ZHULONG_PIXEL_FORMAT_BGR24);
    const int c_r = is_bgr ? 2 : 0;
    const int c_g = 1;
    const int c_b = is_bgr ? 0 : 2;

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

    const int channel_map[3] = {c_r, c_g, c_b};

    for (int dy = 0; dy < dst_h; ++dy) {
        uint8_t* dst_row = dst_buf + static_cast<size_t>(dy) * dst_w * 3;
        for (int dx = 0; dx < dst_w; ++dx) {
            float ox = static_cast<float>(dx) - m2;
            float oy = static_cast<float>(dy) - m5;

            float sx = im0 * ox + im1 * oy;
            float sy = im3 * ox + im4 * oy;

            if (sx >= 0.0f && sx < static_cast<float>(src_w - 1) &&
                sy >= 0.0f && sy < static_cast<float>(src_h - 1)) {
                int x0 = static_cast<int>(sx);
                int y0 = static_cast<int>(sy);
                int x1 = x0 + 1;
                int y1 = y0 + 1;

                float wx = sx - static_cast<float>(x0);
                float wy = sy - static_cast<float>(y0);

                const uint8_t* p00 = src_data + static_cast<size_t>(y0) * src_stride + x0 * 3;
                const uint8_t* p10 = src_data + static_cast<size_t>(y0) * src_stride + x1 * 3;
                const uint8_t* p01 = src_data + static_cast<size_t>(y1) * src_stride + x0 * 3;
                const uint8_t* p11 = src_data + static_cast<size_t>(y1) * src_stride + x1 * 3;

                for (int c = 0; c < 3; ++c) {
                    int src_c = channel_map[c];
                    float v = (1.0f - wx) * (1.0f - wy) * p00[src_c] +
                              wx * (1.0f - wy) * p10[src_c] +
                              (1.0f - wx) * wy * p01[src_c] +
                              wx * wy * p11[src_c];
                    dst_row[dx * 3 + c] = static_cast<uint8_t>(std::clamp(v, 0.0f, 255.0f));
                }
            } else {
                int cx = std::clamp(static_cast<int>(sx), 0, src_w - 1);
                int cy = std::clamp(static_cast<int>(sy), 0, src_h - 1);
                const uint8_t* p = src_data + static_cast<size_t>(cy) * src_stride + cx * 3;
                dst_row[dx * 3 + 0] = p[c_r];
                dst_row[dx * 3 + 1] = p[c_g];
                dst_row[dx * 3 + 2] = p[c_b];
            }
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
