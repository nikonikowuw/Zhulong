/**
 * @file runtime_factory.hpp
 * @brief 编译期后端工厂：返回当前构建配置下的默认推理/视觉后端单例
 *
 * ⚠️ 后端在**编译期**由 CMake cache 变量选定（ZHULONG_INFERENCE_BACKEND /
 * ZHULONG_VISION_BACKEND），不引入运行期 dlopen 后端分流。
 * ⚠️ native/sdk/include/zhulong/algo.hpp 通过 #include 直接依赖本私有头，
 * 因此具体后端实现（如 RknnInferenceBackend）**必须**编译进 libZhulongAlgoRuntime.so，
 * 不能作为独立 .so 由插件单独加载。
 */

#pragma once

#include "inference_backend.hpp"
#include "vision_backend.hpp"

namespace zhulong {

/**
 * @brief 获取当前平台编译配置下的默认推理后端单例
 */
std::shared_ptr<IInferenceBackend> get_default_inference_backend();

/**
 * @brief 获取当前平台编译配置下的默认视觉前处理后端单例
 */
std::shared_ptr<IVisionBackend> get_default_vision_backend();

} // namespace zhulong

