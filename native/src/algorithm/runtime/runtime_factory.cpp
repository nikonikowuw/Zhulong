#include "runtime_factory.hpp"
#include "cpu_inference_backend.hpp"
#include "cpu_vision_backend.hpp"

namespace zhulong {

std::shared_ptr<IInferenceBackend> get_default_inference_backend() {
    static auto backend = std::make_shared<CpuInferenceBackend>();
    return backend;
}

std::shared_ptr<IVisionBackend> get_default_vision_backend() {
    static auto backend = std::make_shared<CpuVisionBackend>();
    return backend;
}

} // namespace zhulong
