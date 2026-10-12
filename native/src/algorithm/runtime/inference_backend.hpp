#pragma once

#include <string>
#include <vector>
#include <memory>
#include <cstdint>
#include <zhulong/plugin.h>
#include "tensor.hpp"

namespace zhulong {

struct ModelAsset {
    virtual ~ModelAsset() = default;
    std::string model_id;
    std::string config_json;
};

struct SessionContext {
    virtual ~SessionContext() = default;
    uint32_t worker_id{0};
};

class IInferenceBackend {
public:
    virtual ~IInferenceBackend() = default;

    // 载入不可变模型资产与权重 (整包共享)
    virtual std::shared_ptr<ModelAsset> load_model(const std::string& model_path_or_config) = 0;

    // 派生 Worker 独立执行会话 (各后端自行实现显存权重共享，如 rknn_dup_context)
    virtual std::unique_ptr<SessionContext> create_session(std::shared_ptr<ModelAsset> model, uint32_t worker_id) = 0;

    // 执行硬件推理
    virtual zhulong_status_t forward(SessionContext* session,
                                     const std::vector<Tensor>& inputs,
                                     std::vector<Tensor>& outputs) = 0;
};

} // namespace zhulong
