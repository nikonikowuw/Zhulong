/**
 * @file face_preprocess.hpp
 * @brief 检测与特征提取共用的张量前处理（HWC uint8 → NCHW float32）
 *
 * 人脸检测（SCRFD 640×640）、人体检测（SCRFD Person 640×640）与特征提取
 * （FaceLiVTv2 112×112）三个模型的前处理只有尺寸与归一化常数不同，实现必须唯一：
 * 各写一份的做法在换量化档位时很容易只改对其中一处。
 */

#pragma once

#include <cstddef>
#include <cstdint>

namespace zhulong::face {

/**
 * @brief HWC 交错 uint8 RGB → NCHW 平面 float32，按 (x - mean) * inv_std 归一化
 *
 * @param src_hwc 源像素首地址（HWC、3 通道、行间无 padding）
 * @param height 源高度
 * @param width 源宽度
 * @param mean 归一化均值
 * @param inv_std 归一化标准差倒数
 * @param dst_nchw 目标首地址（容量需至少为 3 * width * height 个 float）
 * @return true 写入成功；false 表示入参非法（空指针或非正尺寸）
 *
 * @note `noexcept`：本函数是逐像素循环，任何失败都是入参问题，不靠异常表达。
 */
inline bool hwc_to_nchw_normalized(const uint8_t* src_hwc, int height, int width,
                                   float mean, float inv_std, float* dst_nchw) noexcept {
    if (src_hwc == nullptr || dst_nchw == nullptr || height <= 0 || width <= 0) {
        return false;
    }

    const size_t plane_size = static_cast<size_t>(width) * static_cast<size_t>(height);
    float* dst_r = dst_nchw;
    float* dst_g = dst_nchw + plane_size;
    float* dst_b = dst_nchw + plane_size * 2;

    for (size_t i = 0; i < plane_size; ++i) {
        dst_r[i] = (static_cast<float>(src_hwc[i * 3 + 0]) - mean) * inv_std;
        dst_g[i] = (static_cast<float>(src_hwc[i * 3 + 1]) - mean) * inv_std;
        dst_b[i] = (static_cast<float>(src_hwc[i * 3 + 2]) - mean) * inv_std;
    }
    return true;
}

} // namespace zhulong::face
