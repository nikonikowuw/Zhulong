/**
 * @file rknn_inference_backend.cpp
 * @brief RKNN 推理后端实现（模型加载、上下文派生、Tensor 桥接）
 *
 * ⚠️ 本文件是允许 include <rknn_api.h> 的第三处（另两处为 rknn_types.hpp / rknn_error_map.hpp）。
 *
 * ⚠️ 不得声称零拷贝（R13）：rknn_input 无 stride 字段 → 传入 buffer 必须 packed；
 * 输出经 want_float=1 转 float32 后**拷贝**进 Tensor。零拷贝的正确做法是 DMA-BUF（后续任务）。
 */

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

/**
 * @brief 取文件基名作为 model_id（不引入 <filesystem>，保持交叉工具链最小依赖）
 */
std::string file_basename(const std::string& path) {
    if (path.empty()) {
        return "";
    }
    const auto end = path.find_last_not_of("/\\");
    if (end == std::string::npos) {
        return "";
    }
    const auto slash = path.find_last_of("/\\", end);
    return slash == std::string::npos ? path.substr(0, end + 1) : path.substr(slash + 1, end - slash);
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

/**
 * @brief 加载 RKNN 模型并构建根上下文资产
 *
 * ⚠️ 主 root 上下文绝不调用 RKNN_QUERY_MEM_SIZE（R10：会污染上下文使首次
 * rknn_dup_context SIGSEGV，2.3.2 实测缺陷，官方性质未定）；该数据用独立的一次性
 * 上下文查询后立即销毁。
 *
 * @param model_path_or_config 模型文件路径
 * @return std::shared_ptr<ModelAsset> 模型资产；失败返回 nullptr（不抛异常）
 */
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

/**
 * @brief 派生 Worker 独占执行会话（rknn_dup_context，经 dup_mutex 串行化）
 *
 * @param model 共享的根上下文资产
 * @param worker_id Worker 唯一标识
 * @return std::unique_ptr<SessionContext> 独占会话；失败返回 nullptr
 */
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

/**
 * @brief 执行 RKNN 硬件推理（输入 packed 设置 → 同步运行 → 输出拷贝回 Tensor）
 *
 * ⚠️ 本实现接受拷贝，不得声称零拷贝：rknn_input 无 stride 字段，传入 buffer 必须 packed；
 * 输出经 want_float=1 转 float32 后 memcpy 进 Tensor。
 *
 * @param session 该 Worker 的独占执行会话
 * @param inputs 输入张量列表（必须 packed，数量与模型输入数一致）
 * @param[out] outputs 输出张量列表（失败时被清空）
 * @return zhulong_status_t ZHULONG_PLUGIN_OK 成功；其余为映射后的错误码
 */
static rknn_tensor_type to_rknn_tensor_type(DataType dt) {
    switch (dt) {
        case DataType::FLOAT32: return RKNN_TENSOR_FLOAT32;
        case DataType::UINT8:   return RKNN_TENSOR_UINT8;
        case DataType::INT8:    return RKNN_TENSOR_INT8;
        case DataType::INT32:   return RKNN_TENSOR_INT32;
        case DataType::INT64:   return RKNN_TENSOR_INT64;
        default:                return RKNN_TENSOR_UINT8;
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

        // 校验输入数量与模型要求一致
        if (inputs.size() != asset.io_num.n_input) {
            return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
        }

        // 统一校验输入元素数与模型期望一致（支持不同精度输入如 UINT8 -> FP16 由 Runtime 自动转换）
        for (uint32_t i = 0; i < asset.io_num.n_input; ++i) {
            if (inputs[i].element_count() != asset.input_attrs[i].n_elems) {
                return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
            }
        }

        // 检查 DMA-BUF 输入分布：全量检查是否为混合输入
        uint32_t dma_count = 0;
        for (const auto& in_tensor : inputs) {
            if (in_tensor.dma_buf_fd() >= 0) {
                ++dma_count;
            }
        }

        // ⚠️ 硬件契约：同 Context 严禁混用 rknn_set_io_mem 与 rknn_inputs_set
        if (dma_count > 0 && dma_count != asset.io_num.n_input) {
            return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
        }

        if (dma_count == asset.io_num.n_input) {
            // 全量 DMA-BUF 零拷贝输入路径
            if (state->cached_input_mems.size() != asset.io_num.n_input) {
                state->cached_input_mems.resize(asset.io_num.n_input);
            }

            for (uint32_t i = 0; i < asset.io_num.n_input; ++i) {
                const rknn_tensor_attr& attr = asset.input_attrs[i];
                auto& cached = state->cached_input_mems[i];
                const int in_fd = inputs[i].dma_buf_fd();
                void* in_va = const_cast<void*>(inputs[i].raw_data());
                const size_t in_size = inputs[i].byte_size();

                if (cached.mem == nullptr || cached.fd != in_fd ||
                    cached.va != in_va || cached.size != in_size) {

                    // 准备阶段：释放旧映射
                    cached.reset(state->ctx);

                    // 校验输入尺寸是否满足模型要求
                    if (in_size < attr.size) {
                        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
                    }

                    rknn_tensor_mem* new_mem = rknn_create_mem_from_fd(
                        state->ctx,
                        in_fd,
                        in_va,
                        static_cast<uint32_t>(in_size),
                        0
                    );
                    if (!new_mem) {
                        return ZHULONG_PLUGIN_ERR_INTERNAL;
                    }

                    rknn_tensor_attr io_attr = attr;
                    io_attr.index = i;
                    io_attr.type = to_rknn_tensor_type(inputs[i].dtype());
                    // 如果数据类型已与模型期望类型一致，pass_through 设为 1 避免二次转换
                    io_attr.pass_through = (io_attr.type == attr.type) ? 1 : 0;

                    int set_ret = rknn_set_io_mem(state->ctx, new_mem, &io_attr);
                    if (set_ret != RKNN_SUCC) {
                        // 事务回滚：立即销毁新句柄，不修改 cached 元数据
                        rknn_destroy_mem(state->ctx, new_mem);
                        return map_rknn_error(set_ret);
                    }

                    // 事务提交：仅在 set_io_mem 成功后提交凭据
                    cached.mem = new_mem;
                    cached.fd = in_fd;
                    cached.va = in_va;
                    cached.size = in_size;
                }
            }
        } else {
            // 全量常规 Host 内存路径：若此前会话曾绑定过 DMA 内存，先清理以防上下文冲突
            if (!state->cached_input_mems.empty()) {
                for (auto& item : state->cached_input_mems) {
                    item.reset(state->ctx);
                }
                state->cached_input_mems.clear();
            }

            std::vector<rknn_input> rknn_inputs(asset.io_num.n_input);
            for (uint32_t i = 0; i < asset.io_num.n_input; ++i) {
                const rknn_tensor_attr& attr = asset.input_attrs[i];
                rknn_input& in = rknn_inputs[i];
                in = {};
                in.index = i;
                in.buf = const_cast<void*>(inputs[i].raw_data());
                in.size = static_cast<uint32_t>(inputs[i].byte_size());
                in.pass_through = 0;
                in.type = to_rknn_tensor_type(inputs[i].dtype());
                in.fmt = attr.fmt;
            }

            int set_ret = rknn_inputs_set(state->ctx, asset.io_num.n_input, rknn_inputs.data());
            if (set_ret != RKNN_SUCC) {
                return map_rknn_error(set_ret);
            }
        }

        // 同步阻塞：extend=nullptr → non_block=0，返回时设备已不再使用输入。
        int ret = rknn_run(state->ctx, nullptr);
        if (ret != RKNN_SUCC) {
            return map_rknn_error(ret);
        }

        // 提前构造输出 Tensor 并挂载到 outputs，实现 is_prealloc = 1 零拷贝直写
        outputs.reserve(asset.io_num.n_output);
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
            outputs.emplace_back(std::move(shape), DataType::FLOAT32);
        }

        std::vector<rknn_output> rknn_outputs(asset.io_num.n_output);
        for (uint32_t i = 0; i < asset.io_num.n_output; ++i) {
            rknn_output& out = rknn_outputs[i];
            out = {};
            out.want_float = 1;  // 统一转 float32，避免手工反量化（不猜 zp/scale）
            out.is_prealloc = 1; // 零拷贝直写预分配 Tensor，消除中间 malloc 与 memcpy
            out.index = i;
            out.buf = outputs[i].raw_data();
            out.size = static_cast<uint32_t>(outputs[i].byte_size());
        }

        ret = rknn_outputs_get(state->ctx, asset.io_num.n_output, rknn_outputs.data(), nullptr);
        if (ret != RKNN_SUCC) {
            outputs.clear();
            return map_rknn_error(ret);
        }

        OutputsReleaseGuard guard{state->ctx, rknn_outputs, true};

        // 校验输出尺寸一致性，防范 Stride 截断错位
        for (uint32_t i = 0; i < asset.io_num.n_output; ++i) {
            if (rknn_outputs[i].size != outputs[i].byte_size()) {
                outputs.clear();
                return ZHULONG_PLUGIN_ERR_INTERNAL;
            }
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
