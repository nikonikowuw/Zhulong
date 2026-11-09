/**
 * @file rknn_inference_backend.cpp
 * @brief RKNN 推理后端实现（模型加载、上下文派生、Tensor 桥接）
 *
 * ⚠️ 本文件是允许 include <rknn_api.h> 的第三处（另两处为 rknn_types.hpp / rknn_error_map.hpp）。
 *
 * 输入 Tensor 是 packed 布局；DMA 绑定只接受与当前输入属性一致的 packed 布局。
 * 输出经 want_float=1 直接写入预分配 Tensor，不据此声称 NPU 原生 float32 执行。
 */

#include "rknn_inference_backend.hpp"

#include "rknn_error_map.hpp"
#include "rknn_types.hpp"

#include <common/path_utils.hpp>

#include <rknn_api.h>

#include <cstdio>
#include <cstring>
#include <limits>
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

// 元数据来自 SDK 也要校验：成功返回不等于属性可用于分配/绑定。
bool valid_tensor_attr(const rknn_tensor_attr& attr) noexcept {
    if (attr.n_dims == 0 || attr.n_dims > RKNN_MAX_DIMS || attr.n_elems == 0 || attr.size == 0) {
        return false;
    }
    uint32_t elements = 1;
    for (uint32_t d = 0; d < attr.n_dims; ++d) {
        if (attr.dims[d] == 0 || elements > std::numeric_limits<uint32_t>::max() / attr.dims[d]) {
            return false;
        }
        elements *= attr.dims[d];
    }
    return elements == attr.n_elems;
}

rknn_tensor_format input_format(const Tensor& input, rknn_tensor_format model_format) noexcept {
    const auto& shape = input.shape();
    if ((shape.size() == 4 && shape[3] <= 4) || (shape.size() == 3 && shape[2] <= 4)) {
        return RKNN_TENSOR_NHWC;
    }
    if ((shape.size() == 4 && shape[1] <= 4) || (shape.size() == 3 && shape[0] <= 4)) {
        return RKNN_TENSOR_NCHW;
    }
    return model_format;
}

// Tensor 没有物理 stride/容量字段，不能把 packed 内存当带 padding 的原生张量透传。
bool packed_dma_compatible(const Tensor& input, const rknn_tensor_attr& attr) noexcept {
    const auto& shape = input.shape();
    const bool dims_match = (shape.size() == attr.n_dims) ||
        (attr.n_dims == 4 && attr.dims[0] == 1 && shape.size() == 3);
    if (!dims_match || (attr.fmt != RKNN_TENSOR_NHWC && attr.fmt != RKNN_TENSOR_NCHW) ||
        attr.size_with_stride != attr.size) {
        return false;
    }
    if (shape.size() == attr.n_dims) {
        for (size_t d = 0; d < shape.size(); ++d) {
            if (shape[d] != attr.dims[d]) {
                return false;
            }
        }
    } else {
        // shape.size() == 3, attr.n_dims == 4, attr.dims[0] == 1
        for (size_t d = 0; d < 3; ++d) {
            if (shape[d] != attr.dims[d + 1]) {
                return false;
            }
        }
    }
    if (attr.n_dims != 4) {
        return attr.w_stride == 0 && attr.h_stride == 0;
    }
    const uint32_t width = attr.dims[attr.fmt == RKNN_TENSOR_NHWC ? 2 : 3];
    const uint32_t height = attr.dims[attr.fmt == RKNN_TENSOR_NHWC ? 1 : 2];
    return (attr.w_stride == 0 || attr.w_stride == width) &&
           (attr.h_stride == 0 || attr.h_stride == height);
}

std::vector<int64_t> normalize_input_shape(const std::vector<int64_t>& shape, const rknn_tensor_attr& attr) {
    if (attr.n_dims == 4 && attr.dims[0] == 1 && shape.size() == 3) {
        return {1, shape[0], shape[1], shape[2]};
    }
    return shape;
}

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
            if (ret != RKNN_SUCC || !valid_tensor_attr(attr)) {
                return nullptr;
            }
        }

        asset->output_attrs.resize(asset->io_num.n_output);
        for (uint32_t i = 0; i < asset->io_num.n_output; ++i) {
            rknn_tensor_attr& attr = asset->output_attrs[i];
            std::memset(&attr, 0, sizeof(attr));
            attr.index = i;
            ret = rknn_query(root, RKNN_QUERY_OUTPUT_ATTR, &attr, sizeof(attr));
            if (ret != RKNN_SUCC || !valid_tensor_attr(attr)) {
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
        session->current_input_attrs = asset->input_attrs;
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
 * @brief 执行 RKNN 硬件推理（packed 输入 → 同步运行 → 预分配 float32 输出）
 *
 * Host 与 DMA 输入均保留 Runtime 转换；不以 dtype 相等推断可跳过模型预处理。
 *
 * @param session 该 Worker 的独占执行会话
 * @param inputs 输入张量列表（必须 packed，数量与模型输入数一致）
 * @param[out] outputs 输出张量列表（失败时被清空）
 * @return zhulong_status_t ZHULONG_PLUGIN_OK 成功；其余为映射后的错误码
 */

zhulong_status_t RknnInferenceBackend::forward(SessionContext* session,
                                               const std::vector<Tensor>& inputs,
                                               std::vector<Tensor>& outputs) {
    outputs.clear();
    if (!session) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }
    auto* state = dynamic_cast<RknnSessionContext*>(session);
    if (!state || !state->valid || !state->model) {
        return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    }

    try {
        const RknnModelAsset& asset = *state->model;

        // 校验输入数量与模型要求一致
        if (inputs.size() != asset.io_num.n_input) {
            return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
        }

        // 所有 SDK 窄化和明显非法输入必须在修改 session/硬件状态之前拒绝。
        uint32_t dma_count = 0;
        for (const auto& input : inputs) {
            if (input.byte_size() == 0 || input.byte_size() > std::numeric_limits<uint32_t>::max() ||
                input.shape().empty() || input.shape().size() > RKNN_MAX_DIMS) {
                return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
            }
            for (const int64_t dim : input.shape()) {
                if (dim <= 0 || dim > std::numeric_limits<uint32_t>::max()) {
                    return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
                }
            }
            if (input.dma_buf_fd() >= 0) {
                ++dma_count;
            } else if (input.raw_data() == nullptr) {
                return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
            }
        }
        if (dma_count > 0 && dma_count != asset.io_num.n_input) {
            return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
        }

        // 处理动态 shape 切换与多尺度模型支持
        if (asset.is_dynamic) {
            bool shape_changed = !state->shapes_set || (state->current_input_shapes.size() != inputs.size());
            if (!shape_changed) {
                for (size_t i = 0; i < inputs.size(); ++i) {
                    if (normalize_input_shape(inputs[i].shape(), asset.input_attrs[i]) != state->current_input_shapes[i]) {
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

                    const auto norm_shape = normalize_input_shape(in.shape(), asset.input_attrs[i]);
                    set_attr.n_dims = static_cast<uint32_t>(norm_shape.size());
                    for (size_t d = 0; d < norm_shape.size(); ++d) {
                        set_attr.dims[d] = static_cast<uint32_t>(norm_shape[d]);
                    }

                    set_attr.fmt = input_format(in, asset.input_attrs[i].fmt);
                    set_attr.type = to_rknn_tensor_type(in.dtype());
                }

                // 先备好可抛异常的候选元数据；SDK 一旦切换，旧 shape 缓存立即失效。
                std::vector<std::vector<int64_t>> next_shapes;
                next_shapes.reserve(inputs.size());
                for (uint32_t i = 0; i < asset.io_num.n_input; ++i) {
                    next_shapes.push_back(normalize_input_shape(inputs[i].shape(), asset.input_attrs[i]));
                }
                std::vector<rknn_tensor_attr> next_inputs(asset.io_num.n_input);
                std::vector<rknn_tensor_attr> next_outputs(asset.io_num.n_output);
                state->shapes_set = false;
                const int set_ret = rknn_set_input_shapes(state->ctx, asset.io_num.n_input, set_attrs.data());
                if (set_ret != RKNN_SUCC) {
                    return map_rknn_error(set_ret);
                }
                state->cached_input_mems.clear();

                const auto query_attrs = [&](rknn_query_cmd command,
                                             std::vector<rknn_tensor_attr>& attrs) -> zhulong_status_t {
                    for (uint32_t j = 0; j < attrs.size(); ++j) {
                        attrs[j].index = j;
                        const int ret = rknn_query(state->ctx, command, &attrs[j], sizeof(attrs[j]));
                        if (ret != RKNN_SUCC) {
                            return map_rknn_error(ret);
                        }
                        if (!valid_tensor_attr(attrs[j])) {
                            return ZHULONG_PLUGIN_ERR_INTERNAL;
                        }
                    }
                    return ZHULONG_PLUGIN_OK;
                };
                const zhulong_status_t input_status = query_attrs(RKNN_QUERY_CURRENT_INPUT_ATTR, next_inputs);
                if (input_status != ZHULONG_PLUGIN_OK) {
                    return input_status;
                }
                const zhulong_status_t output_status = query_attrs(RKNN_QUERY_CURRENT_OUTPUT_ATTR, next_outputs);
                if (output_status != ZHULONG_PLUGIN_OK) {
                    return output_status;
                }
                state->current_input_attrs = std::move(next_inputs);
                state->current_output_attrs = std::move(next_outputs);
                state->current_input_shapes = std::move(next_shapes);
                state->shapes_set = true;
            }
        }

        const auto& input_attrs = state->current_input_attrs;
        const auto& output_attrs = state->current_output_attrs;
        for (uint32_t i = 0; i < asset.io_num.n_input; ++i) {
            if (inputs[i].element_count() != input_attrs[i].n_elems) {
                return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
            }
            if (dma_count > 0) {
                if (!packed_dma_compatible(inputs[i], input_attrs[i])) {
                    return ZHULONG_PLUGIN_ERR_NOT_SUPPORTED;
                }
                if (inputs[i].byte_size() < input_attrs[i].size_with_stride) {
                    return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
                }
            }
        }
        // 输出容量按 want_float 的实际字节数检查，分配前拒绝超出 uint32 API 范围。
        for (const auto& attr : output_attrs) {
            if (!valid_tensor_attr(attr) || attr.n_elems > std::numeric_limits<uint32_t>::max() / sizeof(float)) {
                return ZHULONG_PLUGIN_ERR_INTERNAL;
            }
        }

        if (dma_count == asset.io_num.n_input) {
            // 全量 DMA-BUF 注册输入；保留 Runtime 转换，不承诺端到端零拷贝。
            if (state->cached_input_mems.size() != asset.io_num.n_input) {
                state->cached_input_mems.resize(asset.io_num.n_input);
            }

            for (uint32_t i = 0; i < asset.io_num.n_input; ++i) {
                const rknn_tensor_attr& attr = input_attrs[i];
                auto& cached = state->cached_input_mems[i];
                const int in_fd = inputs[i].dma_buf_fd();
                void* in_va = const_cast<void*>(inputs[i].raw_data());
                const size_t in_size = inputs[i].byte_size();

                const bool same_memory = cached.matches(in_fd, in_va, in_size);
                if (same_memory && cached.dtype == inputs[i].dtype()) {
                    continue;
                }

                // dtype 变化只重绑，不重复注册同一块内存；新注册仍由 RAII 独占。
                RknnTensorMemPtr next_mem(nullptr, RknnTensorMemDeleter{state->ctx});
                rknn_tensor_mem* binding_mem = cached.mem.get();
                if (!same_memory) {
                    next_mem.reset(rknn_create_mem_from_fd(state->ctx, in_fd, in_va,
                                                         static_cast<uint32_t>(in_size), 0));
                    if (!next_mem) {
                        return ZHULONG_PLUGIN_ERR_INTERNAL;
                    }
                    binding_mem = next_mem.get();
                }

                rknn_tensor_attr io_attr = attr;
                io_attr.index = i;
                io_attr.type = to_rknn_tensor_type(inputs[i].dtype());
                // dtype 相等不证明 layout/量化/转换侧归一化可跳过，与 Host 路径同口径。
                io_attr.pass_through = 0;

                const int set_ret = rknn_set_io_mem(state->ctx, binding_mem, &io_attr);
                if (set_ret != RKNN_SUCC) {
                    // 失败只回收新注册，不提交内存凭据或 dtype（杜绝下帧幽灵命中）。
                    return map_rknn_error(set_ret);
                }

                // 仅在 set_io_mem 成功后提交；重绑 dtype 时保留同一注册句柄。
                if (same_memory) {
                    cached.dtype = inputs[i].dtype();
                } else {
                    cached.commit(std::move(next_mem), in_fd, in_va, in_size, inputs[i].dtype());
                }
            }
        } else {
            // 全量常规 Host 内存路径：若此前会话曾绑定过 DMA 内存，先清理以防上下文冲突
            // （unique_ptr 析构自动 rknn_destroy_mem，无需手工传 context）。
            if (!state->cached_input_mems.empty()) {
                state->cached_input_mems.clear();
            }

            std::vector<rknn_input> rknn_inputs(asset.io_num.n_input);
            for (uint32_t i = 0; i < asset.io_num.n_input; ++i) {
                const rknn_tensor_attr& attr = input_attrs[i];
                rknn_input& in = rknn_inputs[i];
                in = {};
                in.index = i;
                in.buf = const_cast<void*>(inputs[i].raw_data());
                in.size = static_cast<uint32_t>(inputs[i].byte_size());
                in.pass_through = 0;
                in.type = to_rknn_tensor_type(inputs[i].dtype());
                in.fmt = asset.is_dynamic ? input_format(inputs[i], attr.fmt) : attr.fmt;
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

        // is_prealloc = 1 直接写预分配 Tensor，避免后端再做一次输出拷贝。
        outputs.reserve(asset.io_num.n_output);
        for (uint32_t i = 0; i < asset.io_num.n_output; ++i) {
            const rknn_tensor_attr& attr = output_attrs[i];
            std::vector<int64_t> shape;
            shape.reserve(attr.n_dims);
            for (uint32_t d = 0; d < attr.n_dims; ++d) {
                shape.push_back(static_cast<int64_t>(attr.dims[d]));
            }
            outputs.emplace_back(std::move(shape), DataType::FLOAT32);
        }

        std::vector<rknn_output> rknn_outputs(asset.io_num.n_output);
        for (uint32_t i = 0; i < asset.io_num.n_output; ++i) {
            rknn_output& out = rknn_outputs[i];
            out = {};
            out.want_float = 1;  // 统一转 float32，避免手工反量化（不猜 zp/scale）
            out.is_prealloc = 1; // 调用方提供输出缓冲；不代表 NPU 原生 float32 执行。
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
