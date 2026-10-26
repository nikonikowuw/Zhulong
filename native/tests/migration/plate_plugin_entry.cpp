/**
 * @file plate_plugin_entry.cpp
 * @brief 迁移期桥接：把包内的算法类重新导出成 `.so` 插件的 C ABI 入口
 *
 * ⚠️ 这**不是**包的一部分，是宿主侧临时胶水（阶段 4 起宿主只 exec 包根可执行文件，
 *    本文件连同 `PlateRecognitionPlugin` target 一起删除）。
 *    单独立一个 TU 的原因：包内入口是 `main`（algo_main），而迁移期的 `.so` 需要的是
 *    `zhulong_plugin_get_api`，两者不能共存在同一个目标里。
 */

#include <zhulong/algo.hpp>

#include "plate_recognition.hpp"

ZHULONG_REGISTER_VEHICLE_STRUCTURE_PLUGIN(zhulong::plate::PlateRecognitionPlugin,
                                          "PlateRecognition", "1.0.0")
