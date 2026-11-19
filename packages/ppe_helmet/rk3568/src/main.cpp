/**
 * @file main.cpp
 * @brief 安全帽穿戴检测算法可执行文件入口
 */

#include <zhulong/algo_main.hpp>

#include "pipeline/ppe_helmet.hpp"

ZHULONG_ALGO_MAIN(zhulong::ppe::PpeHelmetPlugin,
                  zhulong::sdk::DetectionResultAdapter,
                  "PpeHelmet", "1.0.0")
