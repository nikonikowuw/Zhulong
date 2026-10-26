/**
 * @file face_plugin_entry.cpp
 * @brief 迁移期桥接：把包内的算法类重新导出成 `.so` 插件的 C ABI 入口
 *
 * ⚠️ 这**不是**包的一部分，是宿主侧临时胶水（阶段 4 起宿主只 exec 包根可执行文件，
 *    本文件连同 `FaceRecognitionPlugin` target 一起删除）。
 *    单独立一个 TU 的原因：包内入口是 `main`（algo_main），而迁移期的 `.so` 需要的是
 *    `zhulong_plugin_get_api`。
 */

#include <zhulong/algo.hpp>

#include "face_recognition.hpp"

ZHULONG_REGISTER_PERSON_STRUCTURE_PLUGIN(zhulong::face::FaceRecognitionPlugin,
                                         "FaceRecognition", "1.0.0")
