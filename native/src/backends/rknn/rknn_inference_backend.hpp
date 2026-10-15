/**
 * @file rknn_inference_backend.hpp
 * @brief RKNN 推理后端：IInferenceBackend 的 Rockchip RKNPU2 实现
 *
 * ⚠️ 本头文件不得 include <rknn_api.h>；RKNN 专有类型只在 rknn_types.hpp /
 * rknn_error_map.hpp / rknn_inference_backend.cpp 内出现。
 *
 * ⚠️ 构建期约束（R14）：链接目标必须是 librknnrt.so；
 * `librknn_api.so` 是陈旧残留（1.5.2，仅 25 个 rknn_ 符号），严禁链接。
 * 该库必须编进 libZhulongAlgoRuntime.so，不能作为独立 .so 由插件加载。
 *
 * ⚠️ 性能口径（R13 相关）：RK3568 为单 NPU 核，不得声称吞吐随 worker 数线性扩展；
 * 本实现不启用 RKNN_FLAG_ASYNC_MASK。
 */

#pragma once

#include <algorithm/runtime/inference_backend.hpp>

namespace zhulong {

/**
 * @brief Rockchip RKNPU2 推理后端实现
 */
class RknnInferenceBackend : public IInferenceBackend {
public:
    std::shared_ptr<ModelAsset> load_model(const std::string& model_path_or_config) override;
    std::unique_ptr<SessionContext> create_session(std::shared_ptr<ModelAsset> model,
                                                   uint32_t worker_id) override;
    zhulong_status_t forward(SessionContext* session,
                             const std::vector<Tensor>& inputs,
                             std::vector<Tensor>& outputs) override;
};

} // namespace zhulong

