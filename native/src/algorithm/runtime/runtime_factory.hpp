#pragma once

#include "inference_backend.hpp"
#include "vision_backend.hpp"

namespace zhulong {

// 获取当前平台编译配置下的默认推理加速后端
std::shared_ptr<IInferenceBackend> get_default_inference_backend();

// 获取当前平台编译配置下的默认视觉前处理加速后端
std::shared_ptr<IVisionBackend> get_default_vision_backend();

} // namespace zhulong
