#include "quality_assessor.hpp"

#include "common/face_preprocess.hpp"

#include <algorithm>
#include <cmath>
#include <cstddef>
#include <utility>
#include <vector>

namespace zhulong::face {

namespace {

/** @brief eDifFIQA(T) 输入归一化：(x / 255 - 0.5) / 0.5 = (x - 127.5) / 127.5 */
constexpr float kQualityMean = 127.5f;
constexpr float kQualityInvStd = 1.0f / 127.5f;

std::vector<zhulong::Tensor> single_input(zhulong::Tensor&& tensor) {
    std::vector<zhulong::Tensor> inputs;
    inputs.reserve(1);
    inputs.emplace_back(std::move(tensor));
    return inputs;
}

} // namespace

bool FaceQualityAssessor::assess(
    zhulong::Session& session,
    const zhulong::Tensor& patch_tensor,
    float& out_score
) const {
    constexpr size_t kPatchElementCount =
        static_cast<size_t>(kQualityPatchWidth) * static_cast<size_t>(kQualityPatchHeight) * 3;

    if (!session.valid() || patch_tensor.dtype() != zhulong::DataType::UINT8 ||
        patch_tensor.element_count() != kPatchElementCount) {
        return false;
    }

    zhulong::Tensor onnx_patch({1, 3, kQualityPatchHeight, kQualityPatchWidth}, zhulong::DataType::FLOAT32);
    if (!hwc_to_nchw_normalized(patch_tensor.data<uint8_t>(), kQualityPatchHeight, kQualityPatchWidth,
                                kQualityMean, kQualityInvStd, onnx_patch.data<float>())) {
        return false;
    }

    const std::vector<zhulong::Tensor> outputs = session.forward(single_input(std::move(onnx_patch)));
    if (outputs.size() != 1 || outputs[0].dtype() != zhulong::DataType::FLOAT32 ||
        outputs[0].element_count() != 1) {
        return false;
    }

    const float* src = outputs[0].data<float>();
    if (src == nullptr || !std::isfinite(src[0])) {
        return false;
    }

    out_score = std::clamp(src[0], 0.0f, 1.0f);
    return true;
}

} // namespace zhulong::face
