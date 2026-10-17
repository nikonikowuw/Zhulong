/**
 * @file onnx_backend_test.cpp
 * @brief ONNX Runtime 推理后端单元测试（元数据解析、前向精度、多 Worker 并发隔离与异常防御）
 */

#include <backends/onnx/onnx_error_map.hpp>
#include <backends/onnx/onnx_inference_backend.hpp>
#include <backends/onnx/onnx_types.hpp>
#include <algorithm/runtime/inference_backend.hpp>
#include <algorithm/runtime/runtime_factory.hpp>
#include <algorithm/runtime/tensor.hpp>

#include <cmath>
#include <cstdint>
#include <cstdio>
#include <fstream>
#include <iostream>
#include <memory>
#include <string>
#include <thread>
#include <type_traits>
#include <vector>

namespace {

int g_failures = 0;

void require(bool condition, const std::string& label) {
    std::cout << (condition ? "PASS " : "FAIL ") << label << "\n";
    if (!condition) {
        ++g_failures;
    }
}

std::string resolve_model_path(int argc, char** argv) {
    if (argc > 1 && argv[1] != nullptr && argv[1][0] != '\0') {
        return argv[1];
    }
#ifdef ZHULONG_ONNX_TEST_MODEL_PATH
    return ZHULONG_ONNX_TEST_MODEL_PATH;
#else
    return "native/tests/fixtures/onnx/add_const.onnx";
#endif
}

// Case 1: 错误码与数据类型映射表全覆盖测试
void case_error_and_type_map() {
    struct ErrorExpectation {
        OrtErrorCode code;
        zhulong_status_t expected;
    };
    const ErrorExpectation expectations[] = {
        {ORT_OK, ZHULONG_PLUGIN_OK},
        {ORT_FAIL, ZHULONG_PLUGIN_ERR_INTERNAL},
        {ORT_INVALID_ARGUMENT, ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT},
        {ORT_NO_SUCHFILE, ZHULONG_PLUGIN_ERR_NOT_FOUND},
        {ORT_NO_MODEL, ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT},
        {ORT_ENGINE_ERROR, ZHULONG_PLUGIN_ERR_INTERNAL},
        {ORT_RUNTIME_EXCEPTION, ZHULONG_PLUGIN_ERR_INTERNAL},
        {ORT_INVALID_PROTOBUF, ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT},
        {ORT_MODEL_LOADED, ZHULONG_PLUGIN_ERR_ALREADY_EXISTS},
        {ORT_NOT_IMPLEMENTED, ZHULONG_PLUGIN_ERR_NOT_SUPPORTED},
        {ORT_INVALID_GRAPH, ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT},
        {ORT_EP_FAIL, ZHULONG_PLUGIN_ERR_NOT_SUPPORTED},
    };

    for (const auto& item : expectations) {
        const zhulong_status_t actual = zhulong::map_onnx_error(item.code);
        require(actual == item.expected,
                "case1: map_onnx_error(" + std::to_string(static_cast<int>(item.code)) + ")");
    }
    require(zhulong::map_onnx_error(static_cast<OrtErrorCode>(9999)) == ZHULONG_PLUGIN_ERR_INTERNAL,
            "case1: unknown OrtErrorCode -> INTERNAL");

    zhulong::DataType dt = zhulong::DataType::FLOAT32;
    require(zhulong::map_onnx_tensor_type(ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT, dt) &&
                dt == zhulong::DataType::FLOAT32,
            "case1: FLOAT -> FLOAT32");
    require(zhulong::map_onnx_tensor_type(ONNX_TENSOR_ELEMENT_DATA_TYPE_UINT8, dt) &&
                dt == zhulong::DataType::UINT8,
            "case1: UINT8 -> UINT8");
    require(zhulong::map_onnx_tensor_type(ONNX_TENSOR_ELEMENT_DATA_TYPE_INT8, dt) &&
                dt == zhulong::DataType::INT8,
            "case1: INT8 -> INT8");
    require(zhulong::map_onnx_tensor_type(ONNX_TENSOR_ELEMENT_DATA_TYPE_INT32, dt) &&
                dt == zhulong::DataType::INT32,
            "case1: INT32 -> INT32");
    require(zhulong::map_onnx_tensor_type(ONNX_TENSOR_ELEMENT_DATA_TYPE_INT64, dt) &&
                dt == zhulong::DataType::INT64,
            "case1: INT64 -> INT64");
    require(!zhulong::map_onnx_tensor_type(ONNX_TENSOR_ELEMENT_DATA_TYPE_UNDEFINED, dt),
            "case1: UNDEFINED rejected");
    require(!zhulong::map_onnx_tensor_type(ONNX_TENSOR_ELEMENT_DATA_TYPE_STRING, dt),
            "case1: STRING rejected");
}

// Case 2: 模型加载、张量元信息解析与前向推理数值精度验证
void case_load_and_forward(const std::string& model_path) {
    auto backend = zhulong::get_default_inference_backend();
    require(backend != nullptr, "case2: get_default_inference_backend non-null");
    require(std::dynamic_pointer_cast<zhulong::OnnxInferenceBackend>(backend) != nullptr,
            "case2: default backend is OnnxInferenceBackend");

    auto asset = backend->load_model(model_path);
    require(asset != nullptr, "case2: load_model succeeds");
    if (!asset) {
        return;
    }

    auto* onnx_asset = dynamic_cast<zhulong::OnnxModelAsset*>(asset.get());
    require(onnx_asset != nullptr, "case2: asset is OnnxModelAsset");
    if (!onnx_asset) {
        return;
    }

    require(onnx_asset->model_id == "add_const.onnx", "case2: model_id parsed as basename");
    require(onnx_asset->inputs_meta.size() == 1, "case2: inputs_meta count == 1");
    require(onnx_asset->outputs_meta.size() == 1, "case2: outputs_meta count == 1");
    if (onnx_asset->inputs_meta.size() != 1 || onnx_asset->outputs_meta.size() != 1) {
        return;
    }

    const std::vector<int64_t> expected_dims = {1, 3, 32, 32};
    require(onnx_asset->inputs_meta[0].name == "input", "case2: input node name == 'input'");
    require(onnx_asset->inputs_meta[0].dims == expected_dims, "case2: input dims == [1,3,32,32]");
    require(onnx_asset->inputs_meta[0].zhulong_type == zhulong::DataType::FLOAT32,
            "case2: input dtype == FLOAT32");

    require(onnx_asset->outputs_meta[0].name == "output", "case2: output node name == 'output'");
    require(onnx_asset->outputs_meta[0].dims == expected_dims, "case2: output dims == [1,3,32,32]");
    require(onnx_asset->outputs_meta[0].zhulong_type == zhulong::DataType::FLOAT32,
            "case2: output dtype == FLOAT32");

    auto session = backend->create_session(asset, 7);
    require(session != nullptr, "case2: create_session succeeds");
    if (!session) {
        return;
    }
    require(session->worker_id == 7, "case2: session worker_id == 7");

    zhulong::Tensor input(expected_dims, zhulong::DataType::FLOAT32);
    float* in_ptr = input.data<float>();
    const size_t elem_count = input.element_count();
    for (size_t i = 0; i < elem_count; ++i) {
        in_ptr[i] = static_cast<float>(i) * 0.125f - 50.0f;
    }
    const void* raw_before = input.raw_data();

    std::vector<zhulong::Tensor> outputs;
    const zhulong_status_t rc = backend->forward(session.get(), {input}, outputs);
    require(rc == ZHULONG_PLUGIN_OK, "case2: forward returns ZHULONG_PLUGIN_OK");
    require(input.raw_data() == raw_before, "case2: input tensor buffer preserved");
    require(outputs.size() == 1, "case2: outputs.size() == 1");
    if (outputs.size() != 1) {
        return;
    }

    require(outputs[0].shape() == expected_dims, "case2: output shape == [1,3,32,32]");
    require(outputs[0].dtype() == zhulong::DataType::FLOAT32, "case2: output dtype == FLOAT32");
    require(outputs[0].element_count() == elem_count, "case2: output element_count == 3072");

    const float* out_ptr = outputs[0].data<float>();
    bool all_match = true;
    for (size_t i = 0; i < elem_count; ++i) {
        const float expected = in_ptr[i] + 2.0f;
        if (std::fabs(out_ptr[i] - expected) > 1e-5f) {
            all_match = false;
            break;
        }
    }
    require(all_match, "case2: output values match input + 2.0f");
}

// Case 3: 多 Worker 会话并发隔离与共享模型生命周期测试
void case_multi_worker_concurrency(const std::string& model_path) {
    zhulong::OnnxInferenceBackend backend;
    auto asset = backend.load_model(model_path);
    require(asset != nullptr, "case3: load_model succeeds");
    if (!asset) {
        return;
    }

    constexpr uint32_t kWorkers = 4;
    constexpr uint32_t kIterations = 25;
    std::vector<std::unique_ptr<zhulong::SessionContext>> sessions;
    sessions.reserve(kWorkers);
    for (uint32_t w = 0; w < kWorkers; ++w) {
        auto s = backend.create_session(asset, w);
        require(s != nullptr, "case3: create_session worker " + std::to_string(w));
        if (!s) {
            return;
        }
        sessions.push_back(std::move(s));
    }

    // 提前释放外部 asset 引用：验证 SessionContext 内部持有 shared_ptr<OnnxModelAsset> 保活模型权重
    asset.reset();

    std::vector<bool> worker_ok(kWorkers, true);
    std::vector<std::thread> threads;
    threads.reserve(kWorkers);

    for (uint32_t w = 0; w < kWorkers; ++w) {
        threads.emplace_back([&, w]() {
            const std::vector<int64_t> dims = {1, 3, 32, 32};
            for (uint32_t iter = 0; iter < kIterations; ++iter) {
                zhulong::Tensor input(dims, zhulong::DataType::FLOAT32);
                const float base_val = static_cast<float>(w * 100 + iter) * 0.5f;
                float* in_data = input.data<float>();
                const size_t n = input.element_count();
                for (size_t i = 0; i < n; ++i) {
                    in_data[i] = base_val + static_cast<float>(i % 17);
                }

                std::vector<zhulong::Tensor> outputs;
                const zhulong_status_t rc = backend.forward(sessions[w].get(), {input}, outputs);
                if (rc != ZHULONG_PLUGIN_OK || outputs.size() != 1 || outputs[0].element_count() != n) {
                    worker_ok[w] = false;
                    break;
                }

                const float* out_data = outputs[0].data<float>();
                for (size_t i = 0; i < n; ++i) {
                    const float expected = base_val + static_cast<float>(i % 17) + 2.0f;
                    if (std::fabs(out_data[i] - expected) > 1e-5f) {
                        worker_ok[w] = false;
                        break;
                    }
                }
                if (!worker_ok[w]) {
                    break;
                }
            }
        });
    }

    for (auto& t : threads) {
        t.join();
    }

    bool all_workers_ok = true;
    for (uint32_t w = 0; w < kWorkers; ++w) {
        if (!worker_ok[w]) {
            all_workers_ok = false;
        }
    }
    require(all_workers_ok, "case3: 4 concurrent workers x 25 iterations isolated & accurate");
}

// Case 4: 异常分支与非法参数边界测试
void case_faults(const std::string& model_path) {
    // 编译期静态验证 Rule of 5
    static_assert(!std::is_copy_constructible<zhulong::OnnxModelAsset>::value,
                  "OnnxModelAsset must not be copy-constructible");
    static_assert(!std::is_copy_assignable<zhulong::OnnxModelAsset>::value,
                  "OnnxModelAsset must not be copy-assignable");
    static_assert(!std::is_move_constructible<zhulong::OnnxModelAsset>::value,
                  "OnnxModelAsset must not be move-constructible");
    static_assert(!std::is_move_assignable<zhulong::OnnxModelAsset>::value,
                  "OnnxModelAsset must not be move-assignable");

    static_assert(!std::is_copy_constructible<zhulong::OnnxSessionContext>::value,
                  "OnnxSessionContext must not be copy-constructible");
    static_assert(!std::is_copy_assignable<zhulong::OnnxSessionContext>::value,
                  "OnnxSessionContext must not be copy-assignable");
    static_assert(!std::is_move_constructible<zhulong::OnnxSessionContext>::value,
                  "OnnxSessionContext must not be move-constructible");
    static_assert(!std::is_move_assignable<zhulong::OnnxSessionContext>::value,
                  "OnnxSessionContext must not be move-assignable");

    zhulong::OnnxInferenceBackend backend;

    require(backend.load_model("") == nullptr, "case4: load_model(\"\") -> nullptr");
    require(backend.load_model("/nonexistent/path/to/model.onnx") == nullptr,
            "case4: load_model(nonexistent) -> nullptr");
    require(backend.load_model("/nonexistent/path/to/dir/") == nullptr,
            "case4: load_model(trailing slash) -> nullptr");

    const std::string garbage_path = "/tmp/zhulong_onnx_garbage.onnx";
    {
        std::ofstream out(garbage_path, std::ios::binary | std::ios::trunc);
        const char bad_bytes[] = "not a valid protobuf onnx model file";
        out.write(bad_bytes, sizeof(bad_bytes));
    }
    require(backend.load_model(garbage_path) == nullptr, "case4: load_model(corrupted file) -> nullptr");
    std::remove(garbage_path.c_str());

    require(backend.create_session(nullptr, 0) == nullptr, "case4: create_session(nullptr) -> nullptr");
    auto foreign_asset = std::make_shared<zhulong::ModelAsset>();
    require(backend.create_session(foreign_asset, 0) == nullptr,
            "case4: create_session(foreign ModelAsset) -> nullptr");

    std::vector<zhulong::Tensor> outputs;
    require(backend.forward(nullptr, {}, outputs) == ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT,
            "case4: forward(nullptr session) -> INVALID_ARGUMENT");

    zhulong::SessionContext foreign_session;
    require(backend.forward(&foreign_session, {}, outputs) == ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT,
            "case4: forward(foreign SessionContext) -> INVALID_ARGUMENT");

    auto asset = backend.load_model(model_path);
    require(asset != nullptr, "case4: load valid model for input validation tests");
    if (!asset) {
        return;
    }
    auto session = backend.create_session(asset, 0);
    require(session != nullptr, "case4: create valid session for input validation tests");
    if (!session) {
        return;
    }

    // 输入数量不匹配
    require(backend.forward(session.get(), {}, outputs) == ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT,
            "case4: forward(empty inputs) -> INVALID_ARGUMENT");

    zhulong::Tensor valid_input({1, 3, 32, 32}, zhulong::DataType::FLOAT32);
    require(backend.forward(session.get(), {valid_input, valid_input}, outputs) ==
                ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT,
            "case4: forward(too many inputs) -> INVALID_ARGUMENT");

    // 输入数据类型不匹配
    zhulong::Tensor wrong_dtype({1, 3, 32, 32}, zhulong::DataType::UINT8);
    require(backend.forward(session.get(), {wrong_dtype}, outputs) ==
                ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT,
            "case4: forward(wrong dtype) -> INVALID_ARGUMENT");

    // 输入维度阶数不匹配
    zhulong::Tensor wrong_rank({3, 32, 32}, zhulong::DataType::FLOAT32);
    require(backend.forward(session.get(), {wrong_rank}, outputs) ==
                ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT,
            "case4: forward(wrong rank) -> INVALID_ARGUMENT");

    // 输入形状尺寸不匹配
    zhulong::Tensor wrong_shape({1, 3, 16, 16}, zhulong::DataType::FLOAT32);
    require(backend.forward(session.get(), {wrong_shape}, outputs) ==
                ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT,
            "case4: forward(wrong shape) -> INVALID_ARGUMENT");

    // 外部空指针 Tensor
    zhulong::Tensor null_ext = zhulong::Tensor::from_dma_buf(10, {1, 3, 32, 32}, zhulong::DataType::FLOAT32, nullptr);
    require(backend.forward(session.get(), {null_ext}, outputs) ==
                ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT,
            "case4: forward(null raw_data tensor) -> INVALID_ARGUMENT");
}

// Case 5: 重复加载与销毁无泄漏测试
void case_repeated_start_stop(const std::string& model_path) {
    zhulong::OnnxInferenceBackend backend;
    bool all_ok = true;
    for (int i = 0; i < 5; ++i) {
        auto asset = backend.load_model(model_path);
        if (!asset) {
            all_ok = false;
            break;
        }
        auto session = backend.create_session(asset, static_cast<uint32_t>(i));
        if (!session) {
            all_ok = false;
            break;
        }
        zhulong::Tensor input({1, 3, 32, 32}, zhulong::DataType::FLOAT32);
        std::vector<zhulong::Tensor> outputs;
        if (backend.forward(session.get(), {input}, outputs) != ZHULONG_PLUGIN_OK || outputs.size() != 1) {
            all_ok = false;
            break;
        }
        session.reset();
        asset.reset();
    }
    require(all_ok, "case5: repeated load/session/forward/destroy x 5");
}

} // namespace

int main(int argc, char** argv) {
    const std::string model_path = resolve_model_path(argc, argv);
    std::cout << "=== Zhulong ONNX backend tests (model=" << model_path << ") ===" << std::endl;

    case_error_and_type_map();
    case_load_and_forward(model_path);
    case_multi_worker_concurrency(model_path);
    case_faults(model_path);
    case_repeated_start_stop(model_path);

    std::cout << "=== failures=" << g_failures << " ===" << std::endl;
    return g_failures == 0 ? 0 : 1;
}
