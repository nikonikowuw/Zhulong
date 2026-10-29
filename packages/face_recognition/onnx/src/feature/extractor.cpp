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

    std::vector<zhulong::Tensor> outputs;
    // ONNX FaceLiVTv2 / ArcFace: [1, 3, 112, 112] FLOAT32 NCHW, RGB, (x - 127.5f) / 127.5f
    zhulong::Tensor onnx_patch({1, 3, 112, 112}, zhulong::DataType::FLOAT32);
    const uint8_t* src_hwc = patch_tensor.data<uint8_t>();
    float* dst_nchw = onnx_patch.data<float>();
    if (src_hwc && dst_nchw) {
        constexpr int plane_size = 112 * 112;
        float* dst_r = dst_nchw;
        float* dst_g = dst_nchw + plane_size;
        float* dst_b = dst_nchw + plane_size * 2;
        constexpr float mean = 127.5f;
        constexpr float inv_std = 1.0f / 127.5f;
        for (int i = 0; i < plane_size; ++i) {
            dst_r[i] = (static_cast<float>(src_hwc[i * 3 + 0]) - mean) * inv_std;
            dst_g[i] = (static_cast<float>(src_hwc[i * 3 + 1]) - mean) * inv_std;
            dst_b[i] = (static_cast<float>(src_hwc[i * 3 + 2]) - mean) * inv_std;
        }
        outputs = session.forward({onnx_patch});
    }

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

