/**
 * @file main.cpp
 * @brief 人员非法聚集检测算法可执行文件入口
 */

#include <zhulong/algo_main.hpp>

#include "pipeline/crowd_gathering.hpp"

ZHULONG_ALGO_MAIN(zhulong::crowd::CrowdGatheringPlugin,
                  zhulong::sdk::DetectionResultAdapter,
                  "CrowdGathering", "1.0.0")
