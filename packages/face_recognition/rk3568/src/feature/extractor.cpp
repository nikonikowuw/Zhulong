#include "extractor.hpp"

#include <cmath>
#include <vector>

namespace zhulong::face {

namespace {

/** @brief 单位化时的零范数保护下限 */
constexpr float kMinimumFeatureNorm = 1e-6f;

} // namespace

std::vector<float> FaceFeatureExtractor::extract(
    zhulong::Session& session,
    const zhulong::Tensor& patch_tensor
) const {
    constexpr size_t kPatchElementCount =
        static_cast<size_t>(kFeaturePatchWidth) * static_cast<size_t>(kFeaturePatchHeight) * 3;

    if (!session.valid() || patch_tensor.dtype() != zhulong::DataType::UINT8 ||
        patch_tensor.element_count() != kPatchElementCount) {
        return {};
    }

    // RKNN 模型自带均值/方差归一化：HWC uint8 裁剪块直接作为输入
    const std::vector<zhulong::Tensor> outputs = session.forward({patch_tensor});
    if (outputs.size() != 1 || outputs[0].dtype() != zhulong::DataType::FLOAT32 ||
        outputs[0].element_count() != kFeatureDimension) {
        return {};
    }

    const float* src = outputs[0].data<float>();
    if (src == nullptr) {
        return {};
    }

    std::vector<float> features(src, src + kFeatureDimension);

    float norm_sq = 0.0f;
    for (const float value : features) {
        norm_sq += value * value;
    }
    const float norm = std::sqrt(norm_sq);
    if (!(norm > kMinimumFeatureNorm)) {
        // 零向量 / NaN 输出不能冒充「有效的单位特征」：登记进底库后会永远匹配不上，
        // 而且错因看起来像「底库照片质量差」。宁可返回空让上层报错。
        return {};
    }
    for (float& value : features) {
        value /= norm;
    }
    return features;
}

} // namespace zhulong::face
