/**
 * @file onnx_inference_backend.hpp
 * @brief ONNX Runtime 推理后端：IInferenceBackend 的跨平台通用推理实现
 *
 * ⚠️ 本头文件不得 include <onnxruntime_cxx_api.h>；ONNX Runtime 专有类型只在
 * onnx_types.hpp / onnx_error_map.hpp / onnx_inference_backend.cpp 内出现。
 */

#pragma once

#include <algorithm/runtime/inference_backend.hpp>

namespace zhulong {

/**
 * @brief ONNX Runtime 推理后端实现
 */
class OnnxInferenceBackend : public IInferenceBackend {
public:
    std::shared_ptr<ModelAsset> load_model(const std::string& model_path_or_config) override;

    std::unique_ptr<SessionContext> create_session(std::shared_ptr<ModelAsset> model,
                                                   uint32_t worker_id) override;

    zhulong_status_t forward(SessionContext* session,
                             const std::vector<Tensor>& inputs,
                             std::vector<Tensor>& outputs) override;
};

} // namespace zhulong
