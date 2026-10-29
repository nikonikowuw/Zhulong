/**
 * @file extractor.hpp
 * @brief 512 维高判别力人脸特征抽取器 (L2 范数归一化)
 */

#pragma once

#include <zhulong/algo.hpp>
#include <vector>

namespace zhulong::face {

class FaceFeatureExtractor {
public:
    FaceFeatureExtractor() = default;

    /**
     * @brief Use the inference session to extract a normalized 512D feature vector.
     * @return Empty when the model session or output is invalid.
     */
    std::vector<float> extract(
        zhulong::Session& session,
        const zhulong::Tensor& patch_tensor
    ) const;

private:
};

} // namespace zhulong::face

