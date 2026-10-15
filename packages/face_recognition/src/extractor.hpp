/**
 * @file extractor.hpp
 * @brief 512 维高判别力人脸特征抽取器 (L2 范数归一化)
 */

#pragma once

#include <array>
#include <vector>
#include <cstdint>

namespace zhulong::face {

class FaceFeatureExtractor {
public:
    FaceFeatureExtractor() = default;

    /**
     * @brief 从对齐后的 112x112 人脸图像提取 512 维特征向量
     * @param patch_data 112x112 RGB 像素数据
     * @param patch_w patch 宽度 (112)
     * @param patch_h patch 高度 (112)
     * @param face_box 归一化人脸框 [x1, y1, x2, y2]
     * @return 512 维 L2 归一化特征向量
     */
    std::vector<float> extract(
        const uint8_t* patch_data,
        int patch_w,
        int patch_h,
        const std::array<float, 4>& face_box
    ) const;
};

} // namespace zhulong::face
