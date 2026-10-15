/**
 * @file cpu_inference_backend.hpp
 * @brief CPU 推理后端桩实现（无硬件依赖的确定性 Mock，用于 host 回归）
 *
 * 核心设计原则：
 * 1. 零硬件依赖：不链接任何厂商 SDK，使 host 构建与测试可在开发机完整跑通。
 * 2. 确定性输出：以输入首元素为种子生成确定性检测框，便于断言与回归。
 * 3. 资源模型对齐：同样遵循 ModelAsset（共享权重）/ SessionContext（独占工作区）分离。
 */

#pragma once

#include "inference_backend.hpp"

namespace zhulong {

/**
 * @brief CPU 模拟模型资产（共享只读权重）
 */
struct CpuModelAsset : public ModelAsset {
    std::vector<uint8_t> weights;  /**< 模拟权重数据 */
    int input_w{640};              /**< 期望输入宽度 */
    int input_h{640};              /**< 期望输入高度 */
    int input_c{3};                /**< 期望输入通道数 */
};

/**
 * @brief CPU Worker 独占会话上下文
 */
struct CpuSessionContext : public SessionContext {
    // 独占的 Worker 输入输出临时空间
    std::vector<float> workspace;  /**< 该 Worker 独占的临时计算空间 */
};

/**
 * @brief CPU 推理后端实现（IInferenceBackend 的 host 桩实现）
 */
class CpuInferenceBackend : public IInferenceBackend {
public:
    std::shared_ptr<ModelAsset> load_model(const std::string& model_path_or_config) override;

    std::unique_ptr<SessionContext> create_session(std::shared_ptr<ModelAsset> model, uint32_t worker_id) override;

    zhulong_status_t forward(SessionContext* session,
                             const std::vector<Tensor>& inputs,
                             std::vector<Tensor>& outputs) override;
};

} // namespace zhulong

