/**
 * @file cpu_inference_backend.cpp
 * @brief CPU 推理后端桩实现（确定性 Mock 输出）
 */

#include "cpu_inference_backend.hpp"
#include <cmath>
#include <cstring>

namespace zhulong {

std::shared_ptr<ModelAsset> CpuInferenceBackend::load_model(const std::string& model_path_or_config) {
    auto asset = std::make_shared<CpuModelAsset>();
    asset->model_id = "cpu_mock_model";
    asset->config_json = model_path_or_config;
    // 模拟 1MB 权重数据
    asset->weights.resize(1024 * 1024, 0x5A);
    return asset;
}

std::unique_ptr<SessionContext> CpuInferenceBackend::create_session(std::shared_ptr<ModelAsset> model, uint32_t worker_id) {
    if (!model) return nullptr;
    auto session = std::make_unique<CpuSessionContext>();
    session->worker_id = worker_id;
    session->workspace.resize(1024, 0.0f);
    return session;
}

zhulong_status_t CpuInferenceBackend::forward(SessionContext* session,
                                              const std::vector<Tensor>& inputs,
                                              std::vector<Tensor>& outputs) {
    if (!session || inputs.empty()) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }

    // 简单模拟前向计算输出: 输出一个 1x6 的张量 (假设为 [batch, 6]: x, y, w, h, conf, class_id)
    outputs.clear();
    Tensor out_tensor({1, 6}, DataType::FLOAT32);
    float* data = out_tensor.data<float>();

    // 基于输入张量第 0 个元素的均值计算确定性检测框
    float seed = 0.5f;
    if (inputs[0].element_count() > 0) {
        if (inputs[0].dtype() == DataType::UINT8) {
            seed = static_cast<float>(inputs[0].data<uint8_t>()[0]) / 255.0f;
        } else if (inputs[0].dtype() == DataType::FLOAT32) {
            seed = std::abs(inputs[0].data<float>()[0]);
        }
    }

    data[0] = 100.0f + seed * 50.0f; // x
    data[1] = 120.0f + seed * 30.0f; // y
    data[2] = 200.0f;                // w
    data[3] = 300.0f;                // h
    data[4] = 0.88f;                 // confidence
    data[5] = 0.0f;                  // class_id (e.g. person)

    outputs.push_back(std::move(out_tensor));
    return ZHULONG_PLUGIN_OK;
}

} // namespace zhulong

