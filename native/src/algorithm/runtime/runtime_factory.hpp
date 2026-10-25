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
 * @brief 当前平台编译配置下的默认推理后端单例
 */
std::shared_ptr<IInferenceBackend> get_default_inference_backend();

/**
 * @brief 当前平台编译配置下的默认视觉前处理后端单例
 */
std::shared_ptr<IVisionBackend> get_default_vision_backend();

/**
 * @brief 当前编译配置下的推理后端标识（onnx / rknn / test_stub）
 *
 * ⚠️ 刻意做成自由函数而不是 `IInferenceBackend` 的虚函数：该接口的 vtable 位于
 *    plugin.so 与 libZhulongAlgoRuntime.so 的边界上，增删虚函数会让已编译的旧插件
 *    在新运行库上调用错槽位（实测直接段错误）。诊断类信息一律绕开虚表。
 *
 * `test_stub` 只出现在测试夹具构建（`ZHULONG_INFERENCE_BACKEND=test_stub`）中，
 * 其实现位于 `native/tests/fixtures/`，永远不会进入产品运行时或 SDK 分发物。
 * 产品构建只有 `onnx` / `rknn`；未选择后端时编译期即失败（见 runtime_factory.cpp）。
 */
inline const char* default_inference_backend_name() noexcept {
#if defined(ZHULONG_INFERENCE_RKNN)
    return "rknn";
#elif defined(ZHULONG_INFERENCE_ONNX)
    return "onnx";
#elif defined(ZHULONG_INFERENCE_TEST_STUB)
    return "test_stub";
#else
    return "unselected";
#endif
}

} // namespace zhulong

