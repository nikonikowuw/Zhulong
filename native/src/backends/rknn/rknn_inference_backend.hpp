#pragma once

// RKNN 推理后端：IInferenceBackend 的 Rockchip RKNPU2 实现。
//
// ⚠️ 本头文件不得 include <rknn_api.h>；RKNN 专有类型只在 rknn_types.hpp /
// rknn_error_map.hpp / rknn_inference_backend.cpp 内出现。

#include <algorithm/runtime/inference_backend.hpp>

namespace zhulong {

class RknnInferenceBackend : public IInferenceBackend {
public:
    std::shared_ptr<ModelAsset> load_model(const std::string& model_path_or_config) override;
    std::unique_ptr<SessionContext> create_session(std::shared_ptr<ModelAsset> model,
                                                   uint32_t worker_id) override;
    zhulong_status_t forward(SessionContext* session,
                             const std::vector<Tensor>& inputs,
                             std::vector<Tensor>& outputs) override;
};

} // namespace zhulong
