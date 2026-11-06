#include "extractor.hpp"

#include "common/face_preprocess.hpp"

#include <cmath>
#include <cstring>
#include <utility>
#include <vector>

namespace zhulong::face {

namespace {

/** @brief FaceLiVTv2 / ArcFace 输入归一化：(x - 127.5) / 127.5 */
constexpr float kEmbeddingMean = 127.5f;
constexpr float kEmbeddingInvStd = 1.0f / 127.5f;
/** @brief 单位化时的零范数保护下限 */
constexpr float kMinimumFeatureNorm = 1e-6f;

/**
 * @brief 把输入张量搬进 forward 的入参列表
 *
 * `Session::forward` 收 `const std::vector<Tensor>&`：直接写 `forward({patch})` 会先深拷
 * 一份输入（1×3×112×112 float32 = 150 KB，每脸每帧一份）。因此先 move 进局部 vector。
 */
std::vector<zhulong::Tensor> single_input(zhulong::Tensor&& tensor) {
    std::vector<zhulong::Tensor> inputs;
    inputs.reserve(1);
    inputs.emplace_back(std::move(tensor));
    return inputs;
}

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

    // HWC(uint8 RGB) → NCHW(float32) 归一化，与检测前处理共用同一实现。
    zhulong::Tensor onnx_patch({1, 3, kFeaturePatchHeight, kFeaturePatchWidth}, zhulong::DataType::FLOAT32);
    if (!hwc_to_nchw_normalized(patch_tensor.data<uint8_t>(), kFeaturePatchHeight, kFeaturePatchWidth,
                                kEmbeddingMean, kEmbeddingInvStd, onnx_patch.data<float>())) {
        return {};
    }

    const std::vector<zhulong::Tensor> outputs = session.forward(single_input(std::move(onnx_patch)));
    if (outputs.size() != 1 || outputs[0].dtype() != zhulong::DataType::FLOAT32 ||
        outputs[0].element_count() != kFeatureDimension) {
        return {};
    }

    const float* src = outputs[0].data<float>();
    if (src == nullptr) {
        return {};
    }

    std::vector<float> features(kFeatureDimension);
    std::memcpy(features.data(), src, kFeatureDimension * sizeof(float));

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
