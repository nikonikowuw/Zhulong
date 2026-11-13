#include "quality_assessor.hpp"

#include <algorithm>
#include <cmath>
#include <cstddef>
#include <vector>

namespace zhulong::face {

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

    // RKNN 模型自带均值/方差归一化（mean=127.5, std=127.5）：HWC uint8 裁剪块直接作为输入
    const std::vector<zhulong::Tensor> outputs = session.forward({patch_tensor});
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
