/**
 * @file mock_face_plugin.cpp
 * @brief 宿主 FacePluginLoader 动态加载与 TTL 空闲卸载黑盒测试使用的最小人脸插件替身
 *
 * 设计要点：
 * 1. 只导出宿主 dlsym 依赖的两个符号，不链接任何推理后端，可在 CPU 与 ONNX 后端下通用；
 * 2. extract 生成确定性且严格 L2 单位归一化的 512 维向量，便于断言宿主端未做二次加工；
 * 3. 每次调用向 ZHULONG_MOCK_PLUGIN_MARKER 指定的文件追加事件行，
 *    使测试能够断言 Reaper 确实调用了 zhulong_face_extract_release。
 */

#include <cmath>
#include <cstdint>
#include <cstdio>
#include <cstdlib>

namespace {

constexpr int32_t MOCK_PLUGIN_OK = 0;
constexpr int32_t MOCK_PLUGIN_ERR_INVALID_ARGUMENT = -1;
constexpr int32_t MOCK_PLUGIN_ERR_INTERNAL = -99;
constexpr uint32_t MOCK_FEATURE_DIM = 512;

/** @brief 向标记文件追加一行事件（未配置环境变量时静默跳过） */
void record_event(const char *event) {
    const char *marker = std::getenv("ZHULONG_MOCK_PLUGIN_MARKER");
    if (marker == nullptr || marker[0] == '\0') {
        return;
    }
    if (FILE *file = std::fopen(marker, "a")) {
        std::fprintf(file, "%s\n", event);
        std::fclose(file);
    }
}

} // namespace

extern "C" {

/**
 * @brief 与真实插件同签名的单次特征提取替身
 *
 * @return MOCK_PLUGIN_OK 成功；MOCK_PLUGIN_ERR_INVALID_ARGUMENT 参数非法
 */
int32_t zhulong_face_extract_feature(
    const uint8_t *pixel_data,
    int32_t width,
    int32_t height,
    int32_t stride,
    int32_t format,
    float *out_features,
    uint32_t *feature_dim
) {
    (void)stride;
    (void)format;
    if (pixel_data == nullptr || out_features == nullptr || feature_dim == nullptr) {
        return MOCK_PLUGIN_ERR_INVALID_ARGUMENT;
    }
    if (width <= 0 || height <= 0 || *feature_dim < MOCK_FEATURE_DIM) {
        return MOCK_PLUGIN_ERR_INVALID_ARGUMENT;
    }

    record_event("extract");

    float norm_sq = 0.0f;
    for (uint32_t i = 0; i < MOCK_FEATURE_DIM; ++i) {
        float value = static_cast<float>((i * 37 + 11) % 101) + 1.0f;
        out_features[i] = value;
        norm_sq += value * value;
    }
    float norm = std::sqrt(norm_sq);
    if (norm < 1e-6f) {
        return MOCK_PLUGIN_ERR_INTERNAL;
    }
    for (uint32_t i = 0; i < MOCK_FEATURE_DIM; ++i) {
        out_features[i] /= norm;
    }

    *feature_dim = MOCK_FEATURE_DIM;
    return MOCK_PLUGIN_OK;
}

/** @brief 显式释放替身上下文（真实插件在此释放模型会话与张量资源） */
void zhulong_face_extract_release() {
    record_event("release");
}

} // extern "C"
