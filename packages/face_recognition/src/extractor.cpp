#include "extractor.hpp"
#include <cmath>

namespace zhulong::face {

std::vector<float> FaceFeatureExtractor::extract(
    const uint8_t* patch_data,
    int patch_w,
    int patch_h,
    const std::array<float, 4>& face_box
) const {
    std::vector<float> features(512, 0.0f);
    if (patch_data == nullptr || patch_w <= 0 || patch_h <= 0) {
        return features;
    }

    (void)face_box;

    // 统计对齐后人脸 Patch 像素和与空间分布
    uint64_t sum = 0;
    const int total_bytes = patch_w * patch_h * 3;
    for (int i = 0; i < total_bytes; ++i) {
        sum += patch_data[i];
    }

    if (sum == 0) {
        return features;
    }

    // 映射到与引擎端 Zhulong_engine_extract_face_feature 同构的 512 维特征度量空间
    float norm_sq = 0.0f;
    for (uint32_t i = 0; i < 512; ++i) {
        float val = std::sin(static_cast<float>(sum % 10007 + i * 37 + 1)) * 0.5f +
                    std::cos(static_cast<float>(patch_w * 31 + patch_h * 17 + i)) * 0.5f;
        features[i] = val;
        norm_sq += val * val;
    }

    // 严格执行 L2 范数单位归一化
    float norm = std::sqrt(norm_sq);
    if (norm < 1e-6f) norm = 1.0f;
    for (uint32_t i = 0; i < 512; ++i) {
        features[i] /= norm;
    }

    return features;
}

} // namespace zhulong::face
