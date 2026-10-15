/**
 * @file extractor.hpp
 * @brief 512 维高判别力人脸特征抽取器 (L2 范数归一化)
 */

#pragma once

#include <zhulong/algo.hpp>
#include <array>
#include <vector>
#include <cstdint>

namespace zhulong::face {

class FaceFeatureExtractor {
public:
    FaceFeatureExtractor() = default;

    /**
     * @brief 使用会话执行真实 NPU 特征提取 (FaceLiVTv2-L)
     * @param session 当前 Worker 的特征提取会话
     * @param patch_tensor 112x112 RGB888 UINT8 输入张量
     * @return 512 维 L2 归一化特征向量
     */
    std::vector<float> extract(
        zhulong::Session& session,
        const zhulong::Tensor& patch_tensor
    ) const;

    /**
     * @brief 从对齐后的 112x112 人脸图像提取 512 维特征向量 (CPU 兼容/降级路径)
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
        const std::array<float, 4>& face_box = {0.0f, 0.0f, 0.0f, 0.0f}
    ) const;
};

} // namespace zhulong::face

