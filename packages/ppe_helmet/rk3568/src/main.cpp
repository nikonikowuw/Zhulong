/**
 * @file main.cpp
 * @brief 安全帽穿戴检测算法可执行文件入口
 *
 * 版本号不在这里手写：CMake 配置期从 `manifest.json` 提取 `version` 编成宏
 * （见 CMakeLists.txt），避免清单升版后二进制仍回显旧版本。
 */

#include <zhulong/algo_main.hpp>

#include "pipeline/ppe_helmet.hpp"

#ifndef ZHULONG_PPE_PLUGIN_VERSION
#define ZHULONG_PPE_PLUGIN_VERSION "0.0.0"
#endif

ZHULONG_ALGO_MAIN(zhulong::ppe::PpeHelmetPlugin,
                  zhulong::sdk::DetectionResultAdapter,
                  "PpeHelmet", ZHULONG_PPE_PLUGIN_VERSION)
