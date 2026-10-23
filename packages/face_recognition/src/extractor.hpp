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

    void set_is_onnx(bool v) noexcept { is_onnx_ = v; }
    bool is_onnx() const noexcept { return is_onnx_; }

    /**
     * @brief Use the inference session to extract a normalized 512D feature vector.
     * @return Empty when the model session or output is invalid.
     */
    std::vector<float> extract(
        zhulong::Session& session,
        const zhulong::Tensor& patch_tensor
    ) const;

private:
    bool is_onnx_{false};
};

} // namespace zhulong::face

