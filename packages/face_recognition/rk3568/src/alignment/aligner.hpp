/**
 * @file aligner.hpp
 * @brief ArcFace 5 点几何关键点相似变换对齐模块 (Umeyama 算法)
 */

#pragma once

#include "common/face_types.hpp"

#include <zhulong/algo.hpp>

#include <array>
#include <cstdint>

namespace zhulong::face {

/**
 * @brief ArcFace 标准 112x112 参考人脸模板点
 */
inline constexpr std::array<Point2f, 5> kArcfaceRefPoints = {{
    {38.2946f, 51.6963f}, // 左眼
    {73.5318f, 51.5014f}, // 右眼
    {56.0252f, 71.7366f}, // 鼻尖
    {41.5493f, 92.3655f}, // 左嘴角
    {70.7299f, 92.2041f}  // 右嘴角
}};

/**
 * @brief 人脸几何对齐（无状态，纯函数集合）
 *
 * 全部是静态方法、没有实例状态与不变量，保留类名只作为命名作用域（与
 * `zhulong::ops::` 同样的用法）；实例化它没有任何意义，故禁用构造函数。
 */
class Aligner {
public:
    Aligner() = delete;

    /**
     * @brief 基于 Umeyama 算法求解 2x3 相似变换仿射矩阵 (src -> dst)
     * @param src_points 来源 5 个关键点像素坐标
     * @param out_mat 输出 2x3 仿射变换矩阵（按行优先存储 6 个 float）
     * @return true 计算成功；false 表示点退化、奇异或结果非有限值
     */
    static bool estimate_similarity_transform(
        const std::array<Point2f, 5>& src_points,
        std::array<float, 6>& out_mat
    );

    /**
     * @brief 基于 2x3 仿射矩阵对原图执行双线性插值采样与重对齐，直接写入目标缓冲区（零堆分配）
     * @param image 原图只读视图
     * @param affine_mat 2x3 相似变换仿射矩阵 (src -> dst)
     * @param dst_buf 目标缓冲区指针
     * @param dst_capacity 目标缓冲区容量字节数（>0 时开启防溢出边界校验；0 表示调用方已自行保证容量足够）
     * @param dst_w 输出宽度 (默认 112)
     * @param dst_h 输出高度 (默认 112)
     * @return true 采样成功；false 表示原图/矩阵/像素格式非法或缓冲区容量不足（fail closed，绝不返回半成品）
     */
    static bool crop_and_align_direct(
        const zhulong::Image& image,
        const std::array<float, 6>& affine_mat,
        uint8_t* dst_buf,
        size_t dst_capacity,
        int dst_w = 112,
        int dst_h = 112
    );

    /**
     * @brief 兼容无显式容量形参的重载（内部转发，保持现有接口调用兼容）
     */
    static bool crop_and_align_direct(
        const zhulong::Image& image,
        const std::array<float, 6>& affine_mat,
        uint8_t* dst_buf,
        int dst_w = 112,
        int dst_h = 112
    ) {
        return crop_and_align_direct(image, affine_mat, dst_buf, 0, dst_w, dst_h);
    }
};

} // namespace zhulong::face
