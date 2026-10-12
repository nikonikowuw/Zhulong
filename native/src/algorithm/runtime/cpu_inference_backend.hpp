#pragma once

#include "inference_backend.hpp"

namespace zhulong {

struct CpuModelAsset : public ModelAsset {
    std::vector<uint8_t> weights;
    int input_w{640};
    int input_h{640};
    int input_c{3};
};

struct CpuSessionContext : public SessionContext {
    // 独占的 Worker 输入输出临时空间
    std::vector<float> workspace;
};

class CpuInferenceBackend : public IInferenceBackend {
public:
    std::shared_ptr<ModelAsset> load_model(const std::string& model_path_or_config) override;

    std::unique_ptr<SessionContext> create_session(std::shared_ptr<ModelAsset> model, uint32_t worker_id) override;

    zhulong_status_t forward(SessionContext* session,
                             const std::vector<Tensor>& inputs,
                             std::vector<Tensor>& outputs) override;
};

} // namespace zhulong
