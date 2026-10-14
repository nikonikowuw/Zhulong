#include "rknn_inference_backend.hpp"

#include "rknn_error_map.hpp"
#include "rknn_types.hpp"

#include <rknn_api.h>

#include <cstring>
#include <memory>
#include <utility>
#include <vector>

namespace zhulong {
namespace {

// 取文件基名作为 model_id（不引入 <filesystem>，保持交叉工具链最小依赖）。
std::string file_basename(const std::string& path) {
    const auto slash = path.find_last_of("/\\");
    return slash == std::string::npos ? path : path.substr(slash + 1);
}

// rknn_outputs_release 的 RAII 守卫：保证所有路径（含异常）恰好释放一次。
struct OutputsReleaseGuard {
    rknn_context ctx;
    std::vector<rknn_output>& outputs;
    bool armed{false};

    ~OutputsReleaseGuard() {
        if (armed) {
            rknn_outputs_release(ctx, static_cast<uint32_t>(outputs.size()), outputs.data());
        }
    }
};

// rknn_destroy 的 RAII 守卫：保证 load_model 的失败返回与异常路径都恰好销毁一次根上下文。
// 在所有权正式移交给 RknnModelAsset 之前，根上下文由本守卫持有（asset 的 root_valid
// 只有到最后才置位，因此不会与 asset 析构形成 double-destroy）。
struct ContextGuard {
    rknn_context ctx{0};
    bool armed{false};

    ContextGuard(rknn_context context, bool active) : ctx(context), armed(active) {}

    ~ContextGuard() {
        if (armed && ctx) {
            rknn_destroy(ctx);
            armed = false;
        }
    }
};

} // namespace

std::shared_ptr<ModelAsset> RknnInferenceBackend::load_model(const std::string& model_path_or_config) {
    try {
        if (model_path_or_config.empty()) {
            return nullptr;
        }

        auto asset = std::make_shared<RknnModelAsset>();
        asset->model_path = model_path_or_config;
        asset->config_json = model_path_or_config;

        rknn_context root = 0;
        // size == 0 表示 model 参数是文件路径。
        int ret = rknn_init(&root, const_cast<char*>(model_path_or_config.c_str()), 0, 0, nullptr);
        if (ret != RKNN_SUCC) {
            return nullptr;
        }

        // ⚠️ 根上下文在成功交付给 asset 之前由守卫持有：任何提前返回（含 resize 抛出的
        // std::bad_alloc 等异常）都会销毁 root，避免「init 成功但后续失败」时的上下文泄漏。
        ContextGuard root_guard(root, true);

        ret = rknn_query(root, RKNN_QUERY_IN_OUT_NUM, &asset->io_num, sizeof(asset->io_num));
        if (ret != RKNN_SUCC || asset->io_num.n_input == 0 || asset->io_num.n_output == 0) {
            return nullptr;
        }

        // R9：tensor 属性必须从 Runtime 查询，不从文件猜测；拿不到就拒绝该模型。
        asset->input_attrs.resize(asset->io_num.n_input);
        for (uint32_t i = 0; i < asset->io_num.n_input; ++i) {
            rknn_tensor_attr& attr = asset->input_attrs[i];
            std::memset(&attr, 0, sizeof(attr));
            attr.index = i; // ⚠️ 查询前必须设置目标序号
            ret = rknn_query(root, RKNN_QUERY_INPUT_ATTR, &attr, sizeof(attr));
            if (ret != RKNN_SUCC || attr.n_dims == 0 || attr.n_dims > RKNN_MAX_DIMS ||
                attr.n_elems == 0 || attr.size == 0) {
                return nullptr;
            }
        }

        asset->output_attrs.resize(asset->io_num.n_output);
        for (uint32_t i = 0; i < asset->io_num.n_output; ++i) {
            rknn_tensor_attr& attr = asset->output_attrs[i];
            std::memset(&attr, 0, sizeof(attr));
            attr.index = i;
            ret = rknn_query(root, RKNN_QUERY_OUTPUT_ATTR, &attr, sizeof(attr));
            if (ret != RKNN_SUCC || attr.n_dims == 0 || attr.n_dims > RKNN_MAX_DIMS ||
                attr.n_elems == 0 || attr.size == 0) {
                return nullptr;
            }
        }

        // SDK 版本查询是安全的（真机实测：查过 SDK_VERSION 后 dup 仍 ret=0）。
        rknn_query(root, RKNN_QUERY_SDK_VERSION, &asset->sdk_version, sizeof(asset->sdk_version));

        // ⚠️ 关键缺陷规避（RK3568 真机实测，2026-10-14，librknnrt.so 2.3.2 + RKNPU 驱动 0.9.8）：
        // RKNN_QUERY_MEM_SIZE 会**污染上下文** —— 查过 MEM_SIZE 的上下文，其首次
        // rknn_dup_context 必定 SIGSEGV（崩在 librknnrt.so 内部）；未查过的上下文连续 3 次
        // dup 全部 ret=0。对照实验：none→OK、SDK_VERSION→OK、MEM_SIZE→SIGSEGV(139)。
        // 而 MEM_SIZE 之后 rknn_inputs_set/rknn_run/rknn_outputs_get 均正常，即只有 dup 受影响。
        // 因此本查询改用**独立的一次性上下文**，主 root 上下文绝不调用 RKNN_QUERY_MEM_SIZE。
        // 详见 implement.md §5.7。代价：load_model 期间峰值多一份权重（约 9MB，仅加载期）。
        {
            rknn_context probe = 0;
            if (rknn_init(&probe, const_cast<char*>(model_path_or_config.c_str()), 0, 0, nullptr) == RKNN_SUCC) {
                rknn_query(probe, RKNN_QUERY_MEM_SIZE, &asset->mem_size, sizeof(asset->mem_size));
                rknn_destroy(probe);
            }
        }

        asset->model_id = file_basename(model_path_or_config);
        asset->root_ctx = root;
        asset->root_valid = true;
        root_guard.armed = false; // 所有权正式移交 asset：守卫不再销毁 root。
        return asset;
    } catch (...) {
        return nullptr;
    }
}

std::unique_ptr<SessionContext> RknnInferenceBackend::create_session(std::shared_ptr<ModelAsset> model,
                                                                     uint32_t worker_id) {
    auto asset = std::dynamic_pointer_cast<RknnModelAsset>(model);
    if (!asset || !asset->root_valid) {
        return nullptr;
    }

    try {
        // ⚠️ 先分配 session 对象再 dup：若 make_unique 抛 std::bad_alloc，此时尚未 dup，
        // 不会泄漏任何上下文；dup 成功后立刻置 valid，中间无抛异常操作。
        auto session = std::make_unique<RknnSessionContext>();
        {
            // 不假定 rknn_dup_context 可并发调用：串行化派生。
            std::lock_guard<std::mutex> lock(asset->dup_mutex);
            int ret = rknn_dup_context(&asset->root_ctx, &session->ctx);
            if (ret != RKNN_SUCC) {
                return nullptr; // session->ctx 仍为 0 且 valid=false，析构不会误销毁。
            }
        }

        session->valid = true;
        session->worker_id = worker_id;
        session->model = asset;
        return session;
    } catch (...) {
        return nullptr;
    }
}

zhulong_status_t RknnInferenceBackend::forward(SessionContext* session,
                                               const std::vector<Tensor>& inputs,
                                               std::vector<Tensor>& outputs) {
    if (!session) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }
    auto* state = dynamic_cast<RknnSessionContext*>(session);
    if (!state || !state->valid || !state->model) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }

    outputs.clear();
    try {
        RknnModelAsset& asset = *state->model;

        if (inputs.size() != asset.io_num.n_input) {
            return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
        }

        std::vector<rknn_input> rknn_inputs(asset.io_num.n_input);
        for (uint32_t i = 0; i < asset.io_num.n_input; ++i) {
            const rknn_tensor_attr& attr = asset.input_attrs[i];
            // Tensor 的 std::vector<uint8_t> 天然 packed；rknn_input 无 stride 字段。
            if (inputs[i].byte_size() < attr.size) {
                return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
            }
            rknn_input& in = rknn_inputs[i];
            in = {};
            in.index = i;
            in.buf = const_cast<void*>(inputs[i].raw_data());
            in.size = attr.size;
            in.pass_through = 0;
            in.type = attr.type;
            in.fmt = attr.fmt;
        }

        int ret = rknn_inputs_set(state->ctx, asset.io_num.n_input, rknn_inputs.data());
        if (ret != RKNN_SUCC) {
            return map_rknn_error(ret);
        }

        // 同步阻塞：extend=nullptr → non_block=0，返回时设备已不再使用输入。
        ret = rknn_run(state->ctx, nullptr);
        if (ret != RKNN_SUCC) {
            return map_rknn_error(ret);
        }

        std::vector<rknn_output> rknn_outputs(asset.io_num.n_output);
        for (uint32_t i = 0; i < asset.io_num.n_output; ++i) {
            rknn_output& out = rknn_outputs[i];
            out = {};
            out.want_float = 1; // 统一转 float32，避免手工反量化（不猜 zp/scale）
            out.is_prealloc = 0; // buf 由 runtime 分配，release 时释放
            out.index = i;
        }

        ret = rknn_outputs_get(state->ctx, asset.io_num.n_output, rknn_outputs.data(), nullptr);
        if (ret != RKNN_SUCC) {
            return map_rknn_error(ret);
        }

        OutputsReleaseGuard guard{state->ctx, rknn_outputs, true};

        for (uint32_t i = 0; i < asset.io_num.n_output; ++i) {
            const rknn_tensor_attr& attr = asset.output_attrs[i];
            if (attr.n_dims == 0 || attr.n_dims > RKNN_MAX_DIMS) {
                outputs.clear();
                return ZHULONG_PLUGIN_ERR_INTERNAL;
            }
            std::vector<int64_t> shape;
            shape.reserve(attr.n_dims);
            for (uint32_t d = 0; d < attr.n_dims; ++d) {
                if (attr.dims[d] == 0) {
                    outputs.clear();
                    return ZHULONG_PLUGIN_ERR_INTERNAL;
                }
                shape.push_back(static_cast<int64_t>(attr.dims[d]));
            }

            // 输出经 want_float=1 已统一为 FLOAT32。
            Tensor tensor(std::move(shape), DataType::FLOAT32);
            if (rknn_outputs[i].buf == nullptr || rknn_outputs[i].size < tensor.byte_size()) {
                outputs.clear();
                return ZHULONG_PLUGIN_ERR_INTERNAL;
            }
            std::memcpy(tensor.raw_data(), rknn_outputs[i].buf,
                        std::min<size_t>(tensor.byte_size(), rknn_outputs[i].size));
            outputs.push_back(std::move(tensor));
        }

        return ZHULONG_PLUGIN_OK;
    } catch (const std::bad_alloc&) {
        outputs.clear();
        return ZHULONG_PLUGIN_ERR_OUT_OF_MEMORY;
    } catch (...) {
        outputs.clear();
        return ZHULONG_PLUGIN_ERR_INTERNAL;
    }
}

} // namespace zhulong
