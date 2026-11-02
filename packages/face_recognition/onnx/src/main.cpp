/**
 * @file main.cpp
 * @brief 包根可执行文件入口：算法类 + 结果适配器 + 包元数据的唯一绑定点
 *
 * 单独一个 TU：迁移期宿主还在用 `.so` 插件跑回归（由 native/tests/migration 重新导出
 * C ABI 入口），那个目标不能带 `main`。两边共用同一份算法对象。
 */

#include <zhulong/algo_main.hpp>

#include "pipeline/face_recognition.hpp"

ZHULONG_ALGO_MAIN(zhulong::face::FaceRecognitionPlugin,
                  zhulong::sdk::SceneStructureResultAdapter,
                  "FaceRecognition", "1.0.0")
