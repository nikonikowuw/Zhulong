#include "extractor.hpp"
#include <cmath>
#include <cstring>

namespace zhulong::face {

std::vector<float> FaceFeatureExtractor::extract(
    zhulong::Session& session,
    const zhulong::Tensor& patch_tensor
) const {
    if (!session.valid() || patch_tensor.element_count() != 112 * 112 * 3) {
        return {};
    }

    std::vector<zhulong::Tensor> outputs = session.forward({patch_tensor});

    if (outputs.size() == 1 && outputs[0].element_count() == 512) {
        std::vector<float> features(512);
        const float* src = outputs[0].data<float>();
        if (src) {
            std::memcpy(features.data(), src, 512 * sizeof(float));

            float norm_sq = 0.0f;
            for (int i = 0; i < 512; ++i) {
                norm_sq += features[i] * features[i];
            }
            float norm = std::sqrt(norm_sq);
            if (norm < 1e-6f) norm = 1.0f;
            for (int i = 0; i < 512; ++i) {
                features[i] /= norm;
            }
            return features;
        }
    }

    return {};
}

} // namespace zhulong::face

