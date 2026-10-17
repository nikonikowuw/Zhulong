/**
 * @file face_feature_extract_test.cpp
 * @brief Zhulong_engine_extract_face_feature C ABI 边界防御与真实推理端到端测试
 *
 * 测试在两种推理后端配置下都必须通过，但断言语义不同：
 *   - ZHULONG_TEST_ONNX_INFERENCE（ZHULONG_INFERENCE_BACKEND=onnx）：
 *     真实人脸样本必须检出并产出 512 维 L2 单位特征；无脸图必须返回 NOT_FOUND；
 *   - CPU 桩后端：不具备真实人脸能力，必须闭环报错（UNSUPPORTED），
 *     绝不允许用桩坐标伪造出「看起来有效」的特征向量。
 */

#include "Zhulong/engine.h"

#include <cmath>
#include <cstdint>
#include <dlfcn.h>
#include <fstream>
#include <iostream>
#include <string>
#include <vector>

#if defined(ZHULONG_TEST_ONNX_INFERENCE)
constexpr bool kRealInference = true;
#else
constexpr bool kRealInference = false;
#endif

namespace {

constexpr uint32_t kFeatureDim = 512;

struct PpmImage {
    int width{0};
    int height{0};
    std::vector<uint8_t> rgb;
};

std::string resolve_test_file(const std::string &rel) {
    const std::vector<std::string> prefixes = {
        "",
        "native/",
        "../native/",
        "../../native/",
        "../../../native/",
        "../",
        "../../",
        "../../../",
    };
    for (const auto &prefix : prefixes) {
        std::string candidate = prefix + rel;
        if (std::ifstream(candidate).good()) {
            return candidate;
        }
    }
    return rel;
}

bool load_ppm(const std::string &path, PpmImage &out) {
    std::ifstream file(resolve_test_file(path), std::ios::binary);
    if (!file.is_open()) {
        return false;
    }
    std::string magic;
    int max_val = 0;
    if (!(file >> magic) || magic != "P6") {
        return false;
    }
    if (!(file >> out.width >> out.height >> max_val) || out.width <= 0 || out.height <= 0 || max_val <= 0) {
        return false;
    }
    file.get(); // 跳过分隔符
    out.rgb.resize(static_cast<size_t>(out.width) * out.height * 3);
    file.read(reinterpret_cast<char *>(out.rgb.data()), static_cast<std::streamsize>(out.rgb.size()));
    return file.gcount() == static_cast<std::streamsize>(out.rgb.size());
}

float l2_norm(const float *values) {
    float norm_sq = 0.0f;
    for (uint32_t i = 0; i < kFeatureDim; ++i) {
        norm_sq += values[i] * values[i];
    }
    return std::sqrt(norm_sq);
}

} // namespace

int main(int argc, char** argv) {
    // 插件单次提取 ABI 的路径（用于「宿主 ABI 与插件内部特征空间一致」交叉校验）
    const char* plugin_so = (argc > 1) ? argv[1] : "./libFaceRecognitionPlugin.so";

    Zhulong_engine_h engine = nullptr;
    if (Zhulong_engine_create(&engine) != Zhulong_OK || engine == nullptr) return 1;

    float features[kFeatureDim];
    uint32_t dim = kFeatureDim;
    std::vector<uint8_t> pixels(100 * 100 * 3, 0);

    // 1. 参数防御校验
    if (Zhulong_engine_extract_face_feature(engine, nullptr, 100, 100, 300, 2, features, &dim) != Zhulong_ERR_INVALID_ARGUMENT) return 2;
    if (Zhulong_engine_extract_face_feature(engine, pixels.data(), 0, 100, 300, 2, features, &dim) != Zhulong_ERR_INVALID_ARGUMENT) return 3;
    if (Zhulong_engine_extract_face_feature(engine, pixels.data(), 100, -1, 300, 2, features, &dim) != Zhulong_ERR_INVALID_ARGUMENT) return 4;
    if (Zhulong_engine_extract_face_feature(engine, pixels.data(), 100, 100, 300, 2, nullptr, &dim) != Zhulong_ERR_INVALID_ARGUMENT) return 5;
    if (Zhulong_engine_extract_face_feature(engine, pixels.data(), 100, 100, 300, 2, features, nullptr) != Zhulong_ERR_INVALID_ARGUMENT) return 6;

    uint32_t small_dim = 256;
    if (Zhulong_engine_extract_face_feature(engine, pixels.data(), 100, 100, 300, 2, features, &small_dim) != Zhulong_ERR_INVALID_ARGUMENT) return 7;

    // 非法像素格式
    if (Zhulong_engine_extract_face_feature(engine, pixels.data(), 100, 100, 300, 99, features, &dim) != Zhulong_ERR_UNSUPPORTED) return 8;

    // 非法 stride（小于 width * 3）
    if (Zhulong_engine_extract_face_feature(engine, pixels.data(), 100, 100, 299, 2, features, &dim) != Zhulong_ERR_INVALID_ARGUMENT) return 9;

    // 2. 全黑无脸图片：两种后端都必须返回 NOT_FOUND
    if (Zhulong_engine_extract_face_feature(engine, pixels.data(), 100, 100, 300, 2, features, &dim) != Zhulong_ERR_NOT_FOUND) return 10;

    // 3. 纯噪点无脸图片：真实后端返回 NOT_FOUND；桩后端必须闭环报错而不是伪造特征
    for (size_t i = 0; i < pixels.size(); ++i) {
        pixels[i] = static_cast<uint8_t>((i * 7 + 13) % 256);
    }
    dim = kFeatureDim;
    {
        int rc = Zhulong_engine_extract_face_feature(engine, pixels.data(), 100, 100, 0, 2, features, &dim);
        int expected = kRealInference ? Zhulong_ERR_NOT_FOUND : Zhulong_ERR_UNSUPPORTED;
        if (rc != expected) {
            std::cerr << "noise image: expected rc=" << expected << ", got " << rc << std::endl;
            return 11;
        }
    }

    // 4. 真实人脸测试样本 (face_detect_sample.ppm)
    PpmImage sample;
    if (!load_ppm("tests/fixtures/onnx/face_detect_sample.ppm", sample) &&
        !load_ppm("../native/tests/fixtures/onnx/face_detect_sample.ppm", sample)) {
        std::cerr << "Failed to find face_detect_sample.ppm test fixture" << std::endl;
        return 12;
    }

    dim = kFeatureDim;
    float rgb_features[kFeatureDim];
    int rc = Zhulong_engine_extract_face_feature(
        engine, sample.rgb.data(), sample.width, sample.height, sample.width * 3, 2, rgb_features, &dim);

    if (!kRealInference) {
        // CPU 桩后端不具备真实人脸能力：必须显式 UNSUPPORTED，严禁返回伪特征
        if (rc != Zhulong_ERR_UNSUPPORTED) {
            std::cerr << "stub backend: expected fail-closed UNSUPPORTED, got rc=" << rc << std::endl;
            return 13;
        }
    } else {
        if (rc != Zhulong_OK) {
            std::cerr << "Real face extraction failed with rc=" << rc << std::endl;
            return 14;
        }
        if (dim != kFeatureDim) return 15;
        if (std::abs(l2_norm(rgb_features) - 1.0f) > 1e-3f) return 16;

        // 5. BGR24 输入必须得到与 RGB24 高度一致的特征（余弦相似度 > 0.99）
        std::vector<uint8_t> bgr_sample = sample.rgb;
        for (size_t i = 0; i + 2 < bgr_sample.size(); i += 3) {
            std::swap(bgr_sample[i + 0], bgr_sample[i + 2]);
        }
        dim = kFeatureDim;
        float bgr_features[kFeatureDim];
        rc = Zhulong_engine_extract_face_feature(
            engine, bgr_sample.data(), sample.width, sample.height, sample.width * 3, 1, bgr_features, &dim);
        if (rc != Zhulong_OK) return 17;
        if (dim != kFeatureDim) return 18;

        float dot = 0.0f;
        for (uint32_t i = 0; i < kFeatureDim; ++i) {
            dot += rgb_features[i] * bgr_features[i];
        }
        if (dot < 0.99f) {
            std::cerr << "RGB and BGR cosine similarity too low: " << dot << std::endl;
            return 19;
        }

        // 6. 交叉校验：宿主 ABI 与插件内部单次提取必须落在同一特征空间（同一图片余弦相似度 = 1.0），
        //    杜绝宿主侧另起炉灶重算特征（如回退到数学模拟桩）。
        void* handle = dlopen(plugin_so, RTLD_NOW | RTLD_LOCAL);
        if (handle == nullptr) {
            std::cerr << "failed to dlopen plugin for cross-check: " << dlerror() << std::endl;
            return 20;
        }
        using PluginExtractFn = Zhulong_status_t (*)(
            const uint8_t*, int32_t, int32_t, int32_t, int32_t, float*, uint32_t*);
        auto plugin_extract = reinterpret_cast<PluginExtractFn>(
            dlsym(handle, "zhulong_face_extract_feature"));
        if (plugin_extract == nullptr) {
            std::cerr << "plugin is missing zhulong_face_extract_feature" << std::endl;
            dlclose(handle);
            return 21;
        }

        uint32_t plugin_dim = kFeatureDim;
        float plugin_features[kFeatureDim];
        if (plugin_extract(sample.rgb.data(), sample.width, sample.height, sample.width * 3, 2,
                           plugin_features, &plugin_dim) != Zhulong_OK) {
            std::cerr << "plugin-internal extraction failed" << std::endl;
            dlclose(handle);
            return 22;
        }
        if (plugin_dim != kFeatureDim) {
            std::cerr << "plugin-internal feature dim = " << plugin_dim << std::endl;
            dlclose(handle);
            return 23;
        }

        float cross_dot = 0.0f;
        for (uint32_t i = 0; i < kFeatureDim; ++i) {
            cross_dot += rgb_features[i] * plugin_features[i];
        }
        if (cross_dot < 0.9999f) {
            std::cerr << "host/plugin feature space mismatch, cosine = " << cross_dot << std::endl;
            dlclose(handle);
            return 24;
        }
        dlclose(handle);
    }

    // 7. 引擎销毁必须汇合 Reaper 线程并回收插件资源（由 ASan/TSan 构建进一步审计）
    Zhulong_engine_destroy(engine);

    std::cout << "All face feature extract tests passed successfully!" << std::endl;
    return 0;
}
