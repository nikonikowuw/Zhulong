/**
 * @file extractor.hpp
 * @brief 512 维高判别力人脸特征抽取器 (L2 范数归一化)
 */

#pragma once

#include <zhulong/algo.hpp>

#include <cstddef>
#include <vector>

namespace zhulong::face {

/** @brief 特征提取输入裁剪块尺寸（ArcFace 标准 112×112） */
inline constexpr int kFeaturePatchWidth = 112;
inline constexpr int kFeaturePatchHeight = 112;
/** @brief 特征维度（FaceLiVTv2-L / ArcFace 均为 512） */
inline constexpr size_t kFeatureDimension = 512;

class FaceFeatureExtractor {
public:
    FaceFeatureExtractor() = default;

    /**
     * @brief Use the inference session to extract a normalized feature vector.
     * @return Empty when the session, the patch layout or the model output is invalid.
     */
    std::vector<float> extract(
        zhulong::Session& session,
        const zhulong::Tensor& patch_tensor
    ) const;
};

} // namespace zhulong::face
