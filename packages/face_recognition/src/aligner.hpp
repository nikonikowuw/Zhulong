/**
 * @file aligner.hpp
 * @brief ArcFace 5 点几何关键点相似变换对齐模块 (Umeyama 算法)
 */

#pragma once

#include <zhulong/algo.hpp>
#include <array>
#include <vector>

namespace zhulong::face {

struct Point2f {
    float x{0.0f};
    float y{0.0f};
};

/**
 * @brief ArcFace 标准 112x112 参考人脸模板点
 */
inline constexpr std::array<Point2f, 5> ARCFACE_REF_POINTS = {{
    {38.2946f, 51.6963f}, // 左眼
    {73.5318f, 51.5014f}, // 右眼
    {56.0252f, 71.7366f}, // 鼻尖
    {41.5493f, 92.3655f}, // 左嘴角
    {70.7299f, 92.2041f}  // 右嘴角
}};

class Aligner {
public:
    Aligner() = default;

    /**
     * @brief 基于 Umeyama 算法求解 2x3 相似变换仿射矩阵
     * @param src_points 来源 5 个关键点 (检测坐标)
     * @param out_mat 输出 2x3 仿射变换矩阵（按行优先存储 6 个 float）
     * @return true 计算成功，false 点退化或奇异
     */
    static bool estimate_similarity_transform(
        const std::array<Point2f, 5>& src_points,
        std::array<float, 6>& out_mat
    );

    /**
     * @brief 基于 2x3 仿射矩阵对原图执行双线性插值采样与重对齐
     * @param image 原图只读视图
     * @param affine_mat 2x3 相似变换仿射矩阵 (src -> dst)
     * @param dst_w 输出宽度 (默认 112)
     * @param dst_h 输出高度 (默认 112)
     * @return 连续排布的 RGB24 像素 buffer (dst_w * dst_h * 3 字节)
     */
    static std::vector<uint8_t> crop_and_align(
        const zhulong::Image& image,
        const std::array<float, 6>& affine_mat,
        int dst_w = 112,
        int dst_h = 112
    );
};

} // namespace zhulong::face
