/**
 * @file main.cpp
 * @brief 包根可执行文件入口：只做「算法类 + 结果适配器 + 包元数据」的绑定
 *
 * 单独一个 TU 的原因：迁移期宿主还在用 `PlateRecognitionPlugin.so` 跑回归，
 * 那个目标不能带 `main`。入口与算法实现分开后，两边共用同一份算法对象。
 */

#include <zhulong/algo_main.hpp>

#include "plate_recognition.hpp"

ZHULONG_ALGO_MAIN(zhulong::plate::PlateRecognitionPlugin,
                  zhulong::detail::VehicleStructureResultAdapter,
                  "PlateRecognition", "1.0.0")
