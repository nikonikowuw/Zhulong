/**
 * @file runtime_factory.cpp
 * @brief 编译期后端工厂实现（条件编译选择厂商加速后端 / 测试桩）
 *
 * 具体后端在编译期由 ZHULONG_INFERENCE_RKNN / ZHULONG_INFERENCE_ONNX /
 * ZHULONG_INFERENCE_TEST_STUB 宏选定；视觉后端由 ZHULONG_VISION_RGA 选定，
 * 两者正交，可自由组合（如 RKNN 推理 + CPU 前处理）。
 * 两个 getter 均返回函数内静态单例，保证进程内只构造一次且线程安全。
 *
 * ⚠️ 没有默认后端：未选择推理后端时**编译期失败**（`#error`），
 *    而不是静默落到某个桩实现上——「编译成功但不可推理」是本仓库明令禁止的产物形态。
 */

#include "runtime_factory.hpp"
#include "cpu_vision_backend.hpp"

#ifdef ZHULONG_INFERENCE_RKNN
#include <backends/rknn/rknn_inference_backend.hpp>
#elif defined(ZHULONG_INFERENCE_ONNX)
#include <backends/onnx/onnx_inference_backend.hpp>
#elif defined(ZHULONG_INFERENCE_TEST_STUB)
#include <stub_inference_backend.hpp>
#else
#error "no inference backend selected: pass --inference-backend onnx|rknn (test_stub only for fixture builds)"
#endif

#ifdef ZHULONG_VISION_RGA
#include <backends/rga/rga_vision_backend.hpp>
#endif

namespace zhulong {

/**
 * @brief 返回当前编译配置选定的推理后端单例
 */
std::shared_ptr<IInferenceBackend> get_default_inference_backend() {
#if defined(ZHULONG_INFERENCE_RKNN)
    static auto backend = std::make_shared<RknnInferenceBackend>();
#elif defined(ZHULONG_INFERENCE_ONNX)
    static auto backend = std::make_shared<OnnxInferenceBackend>();
#elif defined(ZHULONG_INFERENCE_TEST_STUB)
    static auto backend = std::make_shared<StubInferenceBackend>();
#endif
    return backend;
}

/**
 * @brief 返回当前编译配置选定的视觉前处理后端单例
 */
std::shared_ptr<IVisionBackend> get_default_vision_backend() {
#ifdef ZHULONG_VISION_RGA
    static auto backend = std::make_shared<RgaVisionBackend>();
#else
    static auto backend = std::make_shared<CpuVisionBackend>();
#endif
    return backend;
}

} // namespace zhulong
