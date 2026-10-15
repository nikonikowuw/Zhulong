/**
 * @file runtime_factory.cpp
 * @brief 编译期后端工厂实现（条件编译选择 CPU / 厂商加速后端）
 *
 * 具体后端在编译期由 ZHULONG_INFERENCE_RKNN / ZHULONG_VISION_RGA 宏选定；
 * 两个开关正交，可自由组合（如 RKNN 推理 + CPU 前处理）。
 * 两个 getter 均返回函数内静态单例，保证进程内只构造一次且线程安全。
 */

#include "runtime_factory.hpp"
#include "cpu_inference_backend.hpp"
#include "cpu_vision_backend.hpp"

#ifdef ZHULONG_INFERENCE_RKNN
#include <backends/rknn/rknn_inference_backend.hpp>
#endif
#ifdef ZHULONG_VISION_RGA
#include <backends/rga/rga_vision_backend.hpp>
#endif

namespace zhulong {

/**
 * @brief 返回当前编译配置选定的推理后端单例
 */
std::shared_ptr<IInferenceBackend> get_default_inference_backend() {
#ifdef ZHULONG_INFERENCE_RKNN
    static auto backend = std::make_shared<RknnInferenceBackend>();
#else
    static auto backend = std::make_shared<CpuInferenceBackend>();
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

