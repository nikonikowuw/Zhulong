/**
 * @file stub_inference_backend.hpp
 * @brief 测试夹具专用的确定性桩推理后端（**不属于产品面**）
 *
 * 归属：`native/tests/fixtures/`，只在 `ZHULONG_INFERENCE_BACKEND=test_stub` 的构建里
 * 被编译进算法运行时；产品构建（`onnx` / `rknn`）不得出现本文件，SDK 分发物也不含它。
 *
 * 核心设计原则：
 * 1. 零硬件依赖：不链接任何厂商 SDK，使板端/宿主的 broker 与 runner 回归可在无模型时跑通。
 * 2. 确定性输出：以输入首元素为种子生成确定性检测框，便于断言与回归。
 * 3. 只认显式夹具标记：`test_stub:` 前缀，真实模型路径（`.onnx` / `.rknn`）一律失败，
 *    绝不返回「加载成功但输出是合成数据」的假资产。
 */

#pragma once

#include <algorithm/runtime/inference_backend.hpp>

namespace zhulong {

/**
 * @brief 桩模型资产（共享只读权重）
 */
struct StubModelAsset : public ModelAsset {
    std::vector<uint8_t> weights;  /**< 模拟权重数据 */
    int input_w{640};              /**< 期望输入宽度 */
    int input_h{640};              /**< 期望输入高度 */
    int input_c{3};                /**< 期望输入通道数 */
};

/**
 * @brief 桩 Worker 独占会话上下文
 */
struct StubSessionContext : public SessionContext {
    // 独占的 Worker 输入输出临时空间
    std::vector<float> workspace;  /**< 该 Worker 独占的临时计算空间 */
};

/**
 * @brief 桩推理后端实现（IInferenceBackend 的测试夹具实现）
 */
class StubInferenceBackend : public IInferenceBackend {
public:
    std::shared_ptr<ModelAsset> load_model(const std::string& model_path_or_config) override;

    std::unique_ptr<SessionContext> create_session(std::shared_ptr<ModelAsset> model, uint32_t worker_id) override;

    zhulong_status_t forward(SessionContext* session,
                             const std::vector<Tensor>& inputs,
                             std::vector<Tensor>& outputs) override;
};

} // namespace zhulong
