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

#include <common/path_utils.hpp>

#include <rknn_api.h>

#include <cstring>
#include <memory>
#include <utility>
#include <vector>

namespace zhulong {
namespace {

// rknn_outputs_release 的 RAII 守卫：保证所有路径（含异常）恰好释放一次。
struct OutputsReleaseGuard {
    rknn_context ctx{0};
    std::vector<rknn_output>& outputs;
    bool armed{false};

    OutputsReleaseGuard(rknn_context context, std::vector<rknn_output>& outs, bool active) noexcept
        : ctx(context), outputs(outs), armed(active) {}

    ~OutputsReleaseGuard() {
        if (armed && ctx != 0) {
            rknn_outputs_release(ctx, static_cast<uint32_t>(outputs.size()), outputs.data());
        }
    }

    OutputsReleaseGuard(const OutputsReleaseGuard&) = delete;
    OutputsReleaseGuard& operator=(const OutputsReleaseGuard&) = delete;
    OutputsReleaseGuard(OutputsReleaseGuard&&) = delete;
    OutputsReleaseGuard& operator=(OutputsReleaseGuard&&) = delete;
};

// rknn_destroy 的 RAII 守卫：保证 load_model 的失败返回与异常路径都恰好销毁一次根上下文。
// 在所有权正式移交给 RknnModelAsset 之前，根上下文由本守卫持有（asset 的 root_valid
// 只有到最后才置位，因此不会与 asset 析构形成 double-destroy）。
struct ContextGuard {
    rknn_context ctx{0};
    bool armed{false};

    ContextGuard(rknn_context context, bool active) noexcept : ctx(context), armed(active) {}

    ~ContextGuard() {
        if (armed && ctx != 0) {
            rknn_destroy(ctx);
            armed = false;
        }
    }

    ContextGuard(const ContextGuard&) = delete;
    ContextGuard& operator=(const ContextGuard&) = delete;
    ContextGuard(ContextGuard&&) = delete;
    ContextGuard& operator=(ContextGuard&&) = delete;
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

        // SDK 版本查询只用于记录证据：失败不影响可用性（不查 MEM_SIZE，见下）。
        ret = rknn_query(root, RKNN_QUERY_SDK_VERSION, &asset->sdk_version, sizeof(asset->sdk_version));
        if (ret != RKNN_SUCC) {
            std::memset(&asset->sdk_version, 0, sizeof(asset->sdk_version));
        }
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
                ContextGuard probe_guard(probe, true);
                rknn_query(probe, RKNN_QUERY_MEM_SIZE, &asset->mem_size, sizeof(asset->mem_size));

                // 查询动态 shape 属性（在 probe 上下文中安全探测，不污染 root 上下文）
                asset->dynamic_ranges.resize(asset->io_num.n_input);
                for (uint32_t i = 0; i < asset->io_num.n_input; ++i) {
                    std::memset(&asset->dynamic_ranges[i], 0, sizeof(rknn_input_range));
                    asset->dynamic_ranges[i].index = i;
                    int qret = rknn_query(probe, RKNN_QUERY_INPUT_DYNAMIC_RANGE,
                                          &asset->dynamic_ranges[i], sizeof(rknn_input_range));
                    if (qret == RKNN_SUCC && asset->dynamic_ranges[i].shape_number > 0) {
                        asset->is_dynamic = true;
                    }
                }
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
        session->current_output_attrs = asset->output_attrs;
        return session;
    } catch (...) {
        return nullptr;
    }
}

/**
 * @brief zhulong::DataType → RKNN 张量类型
 *
 * ⚠️ 不写 `default`：DataType 新增枚举值时，`-Wswitch` 必须在编译期报错，
 *    而不是静默把新类型当 UINT8 喂给 NPU。末尾的返回只是满足编译器的完整性要求。
 */
static rknn_tensor_type to_rknn_tensor_type(DataType dt) noexcept {
    switch (dt) {
        case DataType::FLOAT32: return RKNN_TENSOR_FLOAT32;
        case DataType::UINT8:   return RKNN_TENSOR_UINT8;
        case DataType::INT8:    return RKNN_TENSOR_INT8;
        case DataType::INT32:   return RKNN_TENSOR_INT32;
        case DataType::INT64:   return RKNN_TENSOR_INT64;
    }
    return RKNN_TENSOR_UINT8;
}

/**
 * @brief 执行 RKNN 硬件推理（输入 packed 设置 → 同步运行 → 输出拷贝回 Tensor）
 *
 * ⚠️ 本实现接受拷贝，不得声称零拷贝：rknn_input 无 stride 字段，传入 buffer 必须 packed；
 * 输出经 want_float=1 转 float32 后 memcpy 进 Tensor。真正的零拷贝是 DMA-BUF 输入路径。
 *
 * @param session 该 Worker 的独占执行会话
 * @param inputs 输入张量列表（必须 packed，数量与模型输入数一致）
 * @param[out] outputs 输出张量列表（失败时被清空）
 * @return zhulong_status_t ZHULONG_PLUGIN_OK 成功；其余为映射后的错误码
 */

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

        // 处理动态 shape 切换与多尺度模型支持
        if (asset.is_dynamic) {
            bool shape_changed = !state->shapes_set || (state->current_input_shapes.size() != inputs.size());
            if (!shape_changed) {
                for (size_t i = 0; i < inputs.size(); ++i) {
                    if (inputs[i].shape() != state->current_input_shapes[i]) {
                        shape_changed = true;
                        break;
                    }
                }
            }

            if (shape_changed) {
                std::vector<rknn_tensor_attr> set_attrs(asset.io_num.n_input);
                for (uint32_t i = 0; i < asset.io_num.n_input; ++i) {
                    const auto& in = inputs[i];
                    auto& set_attr = set_attrs[i];
                    std::memset(&set_attr, 0, sizeof(set_attr));
                    set_attr.index = i;
                    // ⚠️ 必须保留 original tensor name（如 "input.1"）
                    std::snprintf(set_attr.name, sizeof(set_attr.name), "%s", asset.input_attrs[i].name);

                    const auto& shape = in.shape();
                    if (shape.size() > RKNN_MAX_DIMS || shape.empty()) {
                        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
                    }
                    set_attr.n_dims = static_cast<uint32_t>(shape.size());
                    for (size_t d = 0; d < shape.size(); ++d) {
                        if (shape[d] <= 0) {
                            return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
                        }
                        set_attr.dims[d] = static_cast<uint32_t>(shape[d]);
                    }

                    // 推导张量布局 fmt：四维输入若最后一维通道数 <= 4 判定为 NHWC
                    if (shape.size() == 4 && shape[3] <= 4) {
                        set_attr.fmt = RKNN_TENSOR_NHWC;
                    } else if (shape.size() == 4 && shape[1] <= 4) {
                        set_attr.fmt = RKNN_TENSOR_NCHW;
                    } else {
                        set_attr.fmt = asset.input_attrs[i].fmt;
                    }
                    set_attr.type = to_rknn_tensor_type(in.dtype());
                }

                int set_ret = rknn_set_input_shapes(state->ctx, asset.io_num.n_input, set_attrs.data());
                if (set_ret != RKNN_SUCC) {
                    return map_rknn_error(set_ret);
                }

                // shape 变动时清空 DMA 绑定缓存，防止旧尺寸物理映射错位
                if (!state->cached_input_mems.empty()) {
                    state->cached_input_mems.clear();
                }

                // 刷新当前生效的输出张量属性
                state->current_output_attrs.resize(asset.io_num.n_output);
                for (uint32_t j = 0; j < asset.io_num.n_output; ++j) {
                    std::memset(&state->current_output_attrs[j], 0, sizeof(rknn_tensor_attr));
                    state->current_output_attrs[j].index = j;
                    int qret = rknn_query(state->ctx, RKNN_QUERY_CURRENT_OUTPUT_ATTR,
                                          &state->current_output_attrs[j], sizeof(rknn_tensor_attr));
                    if (qret != RKNN_SUCC || state->current_output_attrs[j].n_dims == 0 ||
                        state->current_output_attrs[j].size == 0) {
                        return map_rknn_error(qret);
                    }
                }

                state->current_input_shapes.resize(inputs.size());
                for (size_t i = 0; i < inputs.size(); ++i) {
                    state->current_input_shapes[i] = inputs[i].shape();
                }
                state->shapes_set = true;
            }
        }

        // 统一校验输入有效性（动态模型按 element_count > 0 校验，静态模型按 load 期属性严格对齐）
        for (uint32_t i = 0; i < asset.io_num.n_input; ++i) {
            const auto& in = inputs[i];
            if (in.byte_size() == 0 || in.element_count() == 0) {
                return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
            }
            if (!asset.is_dynamic && in.element_count() != asset.input_attrs[i].n_elems) {
                return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
            }
            if (in.dma_buf_fd() < 0 && in.raw_data() == nullptr) {
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

                if (cached.matches(in_fd, in_va, in_size)) {
                    continue; // 凭据命中，本帧无需重新注册
                }

                // 先校验再动缓存：失败的输入不应该把上一个有效绑定拆掉。
                if (in_size < attr.size) {
                    return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
                }

                // 事务开始：新句柄由 RAII 独占，任何提前返回都自动 rknn_destroy_mem。
                RknnTensorMemPtr next_mem(
                    rknn_create_mem_from_fd(state->ctx, in_fd, in_va,
                                            static_cast<uint32_t>(in_size), 0),
                    RknnTensorMemDeleter{state->ctx});
                if (!next_mem) {
                    return ZHULONG_PLUGIN_ERR_INTERNAL;
                }

                rknn_tensor_attr io_attr = attr;
                io_attr.index = i;
                io_attr.type = to_rknn_tensor_type(inputs[i].dtype());
                // 如果数据类型已与模型期望类型一致，pass_through 设为 1 避免二次转换
                io_attr.pass_through = (io_attr.type == attr.type) ? 1 : 0;

                const int set_ret = rknn_set_io_mem(state->ctx, next_mem.get(), &io_attr);
                if (set_ret != RKNN_SUCC) {
                    // 事务回滚：next_mem 析构即销毁新句柄，不提交凭据（杜绝下帧幽灵缓存命中）
                    return map_rknn_error(set_ret);
                }

                // 事务提交：旧绑定在此刻才被替换，只在 set_io_mem 成功后提交凭据。
                cached.commit(std::move(next_mem), in_fd, in_va, in_size);
            }
        } else {
            // 全量常规 Host 内存路径：若此前会话曾绑定过 DMA 内存，先清理以防上下文冲突
            // （unique_ptr 析构自动 rknn_destroy_mem，无需手工传 context）。
            if (!state->cached_input_mems.empty()) {
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
                if (asset.is_dynamic && inputs[i].shape().size() == 4) {
                    in.fmt = (inputs[i].shape()[3] <= 4) ? RKNN_TENSOR_NHWC : RKNN_TENSOR_NCHW;
                } else {
                    in.fmt = attr.fmt;
                }
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
            const rknn_tensor_attr& attr = (asset.is_dynamic && state->shapes_set)
                                               ? state->current_output_attrs[i]
                                               : asset.output_attrs[i];
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
