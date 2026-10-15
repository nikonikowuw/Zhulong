#include "Zhulong/engine.h"
#include <cassert>
#include <cmath>
#include <vector>

int main() {
    Zhulong_engine_h engine = nullptr;
    if (Zhulong_engine_create(&engine) != Zhulong_OK || engine == nullptr) return 1;

    float features[512];
    uint32_t dim = 512;
    std::vector<uint8_t> pixels(100 * 100 * 3, 0);

    // 1. 参数防御校验
    if (Zhulong_engine_extract_face_feature(engine, nullptr, 100, 100, 300, 2, features, &dim) != Zhulong_ERR_INVALID_ARGUMENT) return 2;
    if (Zhulong_engine_extract_face_feature(engine, pixels.data(), 0, 100, 300, 2, features, &dim) != Zhulong_ERR_INVALID_ARGUMENT) return 3;
    if (Zhulong_engine_extract_face_feature(engine, pixels.data(), 100, -1, 300, 2, features, &dim) != Zhulong_ERR_INVALID_ARGUMENT) return 4;
    if (Zhulong_engine_extract_face_feature(engine, pixels.data(), 100, 100, 300, 2, nullptr, &dim) != Zhulong_ERR_INVALID_ARGUMENT) return 5;
    if (Zhulong_engine_extract_face_feature(engine, pixels.data(), 100, 100, 300, 2, features, nullptr) != Zhulong_ERR_INVALID_ARGUMENT) return 6;

    uint32_t small_dim = 256;
    if (Zhulong_engine_extract_face_feature(engine, pixels.data(), 100, 100, 300, 2, features, &small_dim) != Zhulong_ERR_INVALID_ARGUMENT) return 7;

    // 非法格式
    if (Zhulong_engine_extract_face_feature(engine, pixels.data(), 100, 100, 300, 99, features, &dim) != Zhulong_ERR_UNSUPPORTED) return 8;

    // 非法 stride（小于 width * 3）
    if (Zhulong_engine_extract_face_feature(engine, pixels.data(), 100, 100, 299, 2, features, &dim) != Zhulong_ERR_INVALID_ARGUMENT) return 9;

    // 2. 全黑无脸图片应返回 NOT_FOUND
    if (Zhulong_engine_extract_face_feature(engine, pixels.data(), 100, 100, 300, 2, features, &dim) != Zhulong_ERR_NOT_FOUND) return 10;

    // 3. 填充有效像素数据
    for (size_t i = 0; i < pixels.size(); ++i) {
        pixels[i] = static_cast<uint8_t>((i * 7 + 13) % 256);
    }

    dim = 512;
    if (Zhulong_engine_extract_face_feature(engine, pixels.data(), 100, 100, 0, 2, features, &dim) != Zhulong_OK) return 11;
    if (dim != 512) return 12;

    // 校验特征单位 L2 归一化
    float norm_sq = 0.0f;
    for (uint32_t i = 0; i < 512; ++i) {
        norm_sq += features[i] * features[i];
    }
    float norm = std::sqrt(norm_sq);
    if (std::abs(norm - 1.0f) > 1e-4f) return 13;

    // 4. BGR24 格式支持
    dim = 512;
    if (Zhulong_engine_extract_face_feature(engine, pixels.data(), 100, 100, 300, 1, features, &dim) != Zhulong_OK) return 14;
    if (dim != 512) return 15;

    Zhulong_engine_destroy(engine);
    return 0;
}
