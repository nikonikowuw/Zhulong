/**
 * @file onnx_inference_backend.cpp
 * @brief ONNX Runtime 推理后端实现（模型加载、Worker 会话隔离与零拷贝输入张量桥接）
 *
 * ⚠️ 本文件是允许 include <onnxruntime_cxx_api.h> 的内部实现文件。
 */

#include "onnx_inference_backend.hpp"

#include "onnx_error_map.hpp"
#include "onnx_types.hpp"
#include <algorithm/runtime/cpu_inference_backend.hpp>

#include <onnxruntime_cxx_api.h>

#include <cstring>
#include <memory>
#include <utility>
#include <vector>

namespace zhulong {
namespace {

/**
 * @brief 取文件基名作为 model_id
 */
std::string file_basename(const std::string& path) {
    if (path.empty()) {
        return "";
    }
    if (path.back() == '/' || path.back() == '\\') {
        return "";
    }
    const auto slash = path.find_last_of("/\\");
    return slash == std::string::npos ? path : path.substr(slash + 1);
}

/**
 * @brief 获取进程级共享的 Ort::Env 实例
 *
 * ORT 内部维护全局 LoggingManager，进程内复用单一 Ort::Env 可避免多次创建/销毁环境引发状态冲突。
 */
std::shared_ptr<Ort::Env> get_shared_ort_env() {
    static auto env = std::make_shared<Ort::Env>(ORT_LOGGING_LEVEL_WARNING, "ZhulongOnnx");
    return env;
}

std::shared_ptr<CpuInferenceBackend> get_cpu_fallback_backend() {
    static auto backend = std::make_shared<CpuInferenceBackend>();
    return backend;
}

bool is_mock_model_spec(const std::string& path) {
    if (path.empty()) return false;
    if (path.front() == '{' && path.find("mock.model") != std::string::npos) return true;
    if (path.size() >= 8 && path.compare(path.size() - 8, 8, ".weights") == 0) return true;
    return false;
}

} // namespace

OnnxModelAsset::OnnxModelAsset()
    : memory_info(Ort::MemoryInfo::CreateCpu(OrtArenaAllocator, OrtMemTypeDefault)) {}

OnnxModelAsset::~OnnxModelAsset() = default;

OnnxSessionContext::OnnxSessionContext() : run_options() {}

OnnxSessionContext::~OnnxSessionContext() = default;

std::shared_ptr<ModelAsset> OnnxInferenceBackend::load_model(const std::string& model_path_or_config) {
    if (is_mock_model_spec(model_path_or_config)) {
        return get_cpu_fallback_backend()->load_model(model_path_or_config);
    }

    try {
        const std::string basename = file_basename(model_path_or_config);
        if (model_path_or_config.empty() || basename.empty()) {
            return nullptr;
        }

        auto asset = std::make_shared<OnnxModelAsset>();
        asset->model_path = model_path_or_config;
        asset->config_json = model_path_or_config;
        asset->model_id = basename;
        asset->env = get_shared_ort_env();

        Ort::SessionOptions opts;
        // 边缘并发防争抢：限制单算子内与算子间线程数为 1，由外层多 Worker 控制并发度
        opts.SetIntraOpNumThreads(1);
        opts.SetInterOpNumThreads(1);
        opts.SetGraphOptimizationLevel(GraphOptimizationLevel::ORT_ENABLE_ALL);

        asset->session = std::make_unique<Ort::Session>(*asset->env, model_path_or_config.c_str(), opts);

        const size_t input_count = asset->session->GetInputCount();
        const size_t output_count = asset->session->GetOutputCount();
        if (input_count == 0 || output_count == 0) {
            return nullptr;
        }

        Ort::AllocatorWithDefaultOptions allocator;

        auto parse_node_meta = [&](size_t count, auto get_name_fn, auto get_type_fn,
                                   std::vector<TensorMeta>& target) -> bool {
            target.reserve(count);
            for (size_t i = 0; i < count; ++i) {
                auto name_alloc = get_name_fn(i);
                if (!name_alloc || std::strlen(name_alloc.get()) == 0) {
                    return false;
                }

                Ort::TypeInfo type_info = get_type_fn(i);
                if (type_info.GetONNXType() != ONNX_TYPE_TENSOR) {
                    return false;
                }

                auto tensor_info = type_info.GetTensorTypeAndShapeInfo();
                const ONNXTensorElementDataType onnx_type = tensor_info.GetElementType();
                DataType zhulong_type = DataType::FLOAT32;
                if (!map_onnx_tensor_type(onnx_type, zhulong_type)) {
                    return false;
                }

                std::vector<int64_t> dims = tensor_info.GetShape();
                if (dims.empty()) {
                    return false;
                }

                TensorMeta meta;
                meta.name = name_alloc.get();
                meta.dims = std::move(dims);
                meta.onnx_type = onnx_type;
                meta.zhulong_type = zhulong_type;
                target.push_back(std::move(meta));
            }
            return true;
        };

        if (!parse_node_meta(
                input_count,
                [&](size_t i) { return asset->session->GetInputNameAllocated(i, allocator); },
                [&](size_t i) { return asset->session->GetInputTypeInfo(i); },
                asset->inputs_meta)) {
            return nullptr;
        }

        if (!parse_node_meta(
                output_count,
                [&](size_t i) { return asset->session->GetOutputNameAllocated(i, allocator); },
                [&](size_t i) { return asset->session->GetOutputTypeInfo(i); },
                asset->outputs_meta)) {
            return nullptr;
        }

        // 在 inputs_meta / outputs_meta 填充完毕后再缓存 const char* 指针，防止扩容导致悬垂指针
        auto populate_name_ptrs = [](const std::vector<TensorMeta>& meta_list,
                                     std::vector<const char*>& ptr_list) {
            ptr_list.reserve(meta_list.size());
            for (const auto& meta : meta_list) {
                ptr_list.push_back(meta.name.c_str());
            }
        };
        populate_name_ptrs(asset->inputs_meta, asset->input_names_ptr);
        populate_name_ptrs(asset->outputs_meta, asset->output_names_ptr);

        return asset;
    } catch (...) {
        return nullptr;
    }
}

std::unique_ptr<SessionContext> OnnxInferenceBackend::create_session(std::shared_ptr<ModelAsset> model,
                                                                     uint32_t worker_id) {
    auto asset = std::dynamic_pointer_cast<OnnxModelAsset>(model);
    if (!asset || !asset->session || asset->inputs_meta.empty() || asset->outputs_meta.empty()) {
        auto cpu_asset = std::dynamic_pointer_cast<CpuModelAsset>(model);
        if (cpu_asset) {
            return get_cpu_fallback_backend()->create_session(model, worker_id);
        }
        return nullptr;
    }

    try {
        auto session = std::make_unique<OnnxSessionContext>();
        session->worker_id = worker_id;
        session->asset = std::move(asset);
        return session;
    } catch (...) {
        return nullptr;
    }
}

zhulong_status_t OnnxInferenceBackend::forward(SessionContext* session,
                                               const std::vector<Tensor>& inputs,
                                               std::vector<Tensor>& outputs) {
    if (!session) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }
    auto* state = dynamic_cast<OnnxSessionContext*>(session);
    if (!state || !state->asset || !state->asset->session) {
        auto* cpu_state = dynamic_cast<CpuSessionContext*>(session);
        if (cpu_state) {
            return get_cpu_fallback_backend()->forward(session, inputs, outputs);
        }
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }

    outputs.clear();
    try {
        OnnxModelAsset& asset = *state->asset;

        if (inputs.size() != asset.inputs_meta.size()) {
            return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
        }

        std::vector<Ort::Value> ort_inputs;
        ort_inputs.reserve(inputs.size());

        for (size_t i = 0; i < inputs.size(); ++i) {
            const Tensor& in_tensor = inputs[i];
            const TensorMeta& meta = asset.inputs_meta[i];

            if (in_tensor.dtype() != meta.zhulong_type) {
                return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
            }

            const auto& in_shape = in_tensor.shape();
            if (in_shape.empty() || in_shape.size() != meta.dims.size()) {
                return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
            }

            for (size_t d = 0; d < in_shape.size(); ++d) {
                if (in_shape[d] <= 0) {
                    return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
                }
                if (meta.dims[d] > 0 && in_shape[d] != meta.dims[d]) {
                    return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
                }
            }

            const size_t elem_count = in_tensor.element_count();
            const size_t expected_bytes = elem_count * data_type_size(in_tensor.dtype());
            if (elem_count == 0 || in_tensor.byte_size() != expected_bytes || in_tensor.raw_data() == nullptr) {
                return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
            }

            // 零拷贝包装输入 Tensor 的连续内存
            ort_inputs.push_back(Ort::Value::CreateTensor(
                asset.memory_info,
                const_cast<void*>(in_tensor.raw_data()),
                in_tensor.byte_size(),
                in_shape.data(),
                in_shape.size(),
                meta.onnx_type
            ));
        }

        auto ort_outputs = asset.session->Run(
            state->run_options,
            asset.input_names_ptr.data(),
            ort_inputs.data(),
            ort_inputs.size(),
            asset.output_names_ptr.data(),
            asset.output_names_ptr.size()
        );

        if (ort_outputs.size() != asset.outputs_meta.size()) {
            return ZHULONG_PLUGIN_ERR_INTERNAL;
        }

        outputs.reserve(ort_outputs.size());
        for (size_t i = 0; i < ort_outputs.size(); ++i) {
            if (!ort_outputs[i].IsTensor() || !ort_outputs[i].HasValue()) {
                outputs.clear();
                return ZHULONG_PLUGIN_ERR_INTERNAL;
            }

            auto out_info = ort_outputs[i].GetTensorTypeAndShapeInfo();
            std::vector<int64_t> out_shape = out_info.GetShape();
            if (out_shape.empty()) {
                outputs.clear();
                return ZHULONG_PLUGIN_ERR_INTERNAL;
            }
            for (int64_t dim : out_shape) {
                if (dim <= 0) {
                    outputs.clear();
                    return ZHULONG_PLUGIN_ERR_INTERNAL;
                }
            }

            DataType out_dtype = DataType::FLOAT32;
            if (!map_onnx_tensor_type(out_info.GetElementType(), out_dtype)) {
                outputs.clear();
                return ZHULONG_PLUGIN_ERR_NOT_SUPPORTED;
            }

            Tensor out_tensor(std::move(out_shape), out_dtype);
            if (out_info.GetElementCount() != out_tensor.element_count()) {
                outputs.clear();
                return ZHULONG_PLUGIN_ERR_INTERNAL;
            }

            const void* src_ptr = ort_outputs[i].GetTensorRawData();
            if (out_tensor.byte_size() > 0) {
                if (!src_ptr) {
                    outputs.clear();
                    return ZHULONG_PLUGIN_ERR_INTERNAL;
                }
                std::memcpy(out_tensor.raw_data(), src_ptr, out_tensor.byte_size());
            }

            outputs.push_back(std::move(out_tensor));
        }

        return ZHULONG_PLUGIN_OK;
    } catch (const Ort::Exception& e) {
        outputs.clear();
        return map_onnx_error(e.GetOrtErrorCode());
    } catch (const std::bad_alloc&) {
        outputs.clear();
        return ZHULONG_PLUGIN_ERR_OUT_OF_MEMORY;
    } catch (...) {
        outputs.clear();
        return ZHULONG_PLUGIN_ERR_INTERNAL;
    }
}

} // namespace zhulong
