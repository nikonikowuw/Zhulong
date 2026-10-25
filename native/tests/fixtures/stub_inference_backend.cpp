/**
 * @file stub_inference_backend.cpp
 * @brief 测试夹具专用桩推理后端（**不属于产品面，不执行真实推理**）
 *
 * ⚠️ 硬边界：本后端只接受带 `test_stub:` 前缀的显式测试夹具标记，任何真实模型路径
 * （.onnx / .rknn / .om …）一律 `load_model` 失败，让插件 init 硬失败并由 Runner 以
 * `model_create` 失败上报。绝不返回「加载成功但输出是合成数据」的假资产 —— 那会把
 * 「进程健康、模型加载成功、结果永久为空/伪造」变成最难归因的现场故障。
 * 真实推理必须使用 `ZHULONG_INFERENCE_BACKEND=onnx|rknn`；本文件只在 `test_stub` 构建里被编译。
 */

#include "stub_inference_backend.hpp"
#include <cmath>
#include <cstring>

namespace zhulong {

namespace {

/**
 * @brief 测试夹具标记前缀：`test_stub:<任意标识>`
 *
 * 只有显式写下这个前缀的调用方（native 侧的 mock 插件与后端单测）才会拿到桩资产；
 * 真实算法包传入的是文件路径，永远不可能命中该前缀。
 */
constexpr const char* kStubMarkerPrefix = "test_stub:";

bool is_stub_marker(const std::string& value) {
    const size_t prefix_len = std::strlen(kStubMarkerPrefix);
    return value.size() >= prefix_len &&
           value.compare(0, prefix_len, kStubMarkerPrefix) == 0;
}

} // namespace

std::shared_ptr<ModelAsset> StubInferenceBackend::load_model(const std::string& model_path_or_config) {
    if (!is_stub_marker(model_path_or_config)) {
        // 真实模型路径：桩后端不具备推理能力，直接失败（不做任何降级或伪造）。
        return nullptr;
    }
    auto asset = std::make_shared<StubModelAsset>();
    asset->model_id = "test_stub_model";
    asset->config_json = model_path_or_config;
    // 模拟 1MB 权重数据
    asset->weights.resize(1024 * 1024, 0x5A);
    return asset;
}

std::unique_ptr<SessionContext> StubInferenceBackend::create_session(std::shared_ptr<ModelAsset> model, uint32_t worker_id) {
    if (!model) return nullptr;
    auto session = std::make_unique<StubSessionContext>();
    session->worker_id = worker_id;
    session->workspace.resize(1024, 0.0f);
    return session;
}

zhulong_status_t StubInferenceBackend::forward(SessionContext* session,
                                             	const std::vector<Tensor>& inputs,
                                              std::vector<Tensor>& outputs) {
    if (!session || inputs.empty()) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }

    // 确定性夹具输出（形状 [1, 6]）：只有持有 `test_stub:` 夹具资产的测试能走到这里，
    // 真实算法包在该后端下根本无法通过 load_model / init。
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
