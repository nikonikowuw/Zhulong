/**
 * @file face_recognition.cpp
 * @brief 本 target 的算法实现编译单元 + 宿主引擎用的人脸特征提取 C ABI 入口
 *
 * 类定义在 `face_recognition.hpp`（内联）；本 TU 负责两件「必须恰好一份」的事：
 *   1. 让「实现的编译单元」与包入口（`main.cpp`）分开，迁移期宿主仍能用它编出 `.so`；
 *   2. 导出 `zhulong_face_extract_feature` / `zhulong_face_extract_release`
 *      （宿主引擎的 `Zhulong_engine_extract_face_feature` 走这两个符号）。
 */

#include "face_recognition.hpp"

extern "C" {

ZHULONG_PLUGIN_EXPORT zhulong_status_t zhulong_face_extract_feature(
    const uint8_t* pixel_data,
    int32_t width,
    int32_t height,
    int32_t stride,
    int32_t format,
    float* out_features,
    uint32_t* feature_dim
) {
    try {
        return zhulong::face::FaceExtractorContext::instance().extract(
            pixel_data, width, height, stride, format, out_features, feature_dim
        );
    } catch (...) {
        return ZHULONG_PLUGIN_ERR_INTERNAL;
    }
}

ZHULONG_PLUGIN_EXPORT void zhulong_face_extract_release() {
    try {
        zhulong::face::FaceExtractorContext::instance().release();
    } catch (...) {}
}

} // extern "C"
