#include "aligner.hpp"
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

} // namespace zhulong::face
