#include "runtime_factory.hpp"
#include "cpu_inference_backend.hpp"
#include "cpu_vision_backend.hpp"

#ifdef ZHULONG_INFERENCE_RKNN
#include <backends/rknn/rknn_inference_backend.hpp>
#endif

namespace zhulong {

std::shared_ptr<IInferenceBackend> get_default_inference_backend() {
#ifdef ZHULONG_INFERENCE_RKNN
    static auto backend = std::make_shared<RknnInferenceBackend>();
#else
    static auto backend = std::make_shared<CpuInferenceBackend>();
#endif
    return backend;
}

std::shared_ptr<IVisionBackend> get_default_vision_backend() {
    static auto backend = std::make_shared<CpuVisionBackend>();
    return backend;
}

} // namespace zhulong
