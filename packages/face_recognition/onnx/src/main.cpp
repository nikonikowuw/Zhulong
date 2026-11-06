/**
 * @file main.cpp
 * @brief 包根可执行文件入口：算法类 + 结果适配器 + 包元数据的唯一绑定点
 *
 * 单独一个 TU：迁移期宿主还在用 `.so` 插件跑回归（由 native/tests/migration 重新导出
 * C ABI 入口），那个目标不能带 `main`。两边共用同一份算法对象。
 *
 * 版本号**不在这里手写**：CMake 配置期从 `manifest.json` 抠出 `version` 编成宏
 * （见 CMakeLists.txt），否则清单升版后二进制还回显旧版本，且没有任何门禁会发现。
 */

#include <zhulong/algo_main.hpp>

#include "pipeline/face_recognition.hpp"

#ifndef ZHULONG_FACE_PLUGIN_VERSION
#define ZHULONG_FACE_PLUGIN_VERSION "0.0.0"
#endif

ZHULONG_ALGO_MAIN(zhulong::face::FaceRecognitionPlugin,
                  zhulong::sdk::SceneStructureResultAdapter,
                  "FaceRecognition", ZHULONG_FACE_PLUGIN_VERSION)
