/**
 * @file inference_backend.hpp
 * @brief 推理后端抽象接口（IInferenceBackend）与资源模型基类
 *
 * 核心设计原则：
 * 1. 三层资源解耦：ModelAsset（模型资源组，整包共享不可变权重）与
 *    SessionContext（Worker 执行上下文，线程绑定独占会话）分离，支持权重共享。
 * 2. 编译期后端绑定：具体实现（CPU / RKNN / ...）由 CMake 变量选定并编入
 *    libZhulongAlgoRuntime.so，不引入运行期 dlopen 后端分流。
 * 3. 后端可替换前提：上层（SDK 门面、Runner）只依赖本接口，不感知厂商 SDK 类型。
 */

#pragma once

#include <string>
#include <vector>
#include <memory>
#include <cstdint>
#include <zhulong/plugin.h>
#include "tensor.hpp"

namespace zhulong {

/**
 * @brief 不可变模型资产基类（模型资源组，进程内多线程共享）
 */
struct ModelAsset {
    virtual ~ModelAsset() = default;
    std::string model_id;     /**< 模型标识（用于诊断与缓存键） */
    std::string config_json;  /**< 模型加载配置（由 model_create 传入的原文本） */
};

/**
 * @brief 单个 Worker 独占执行会话基类（Worker 执行上下文）
 */
struct SessionContext {
    virtual ~SessionContext() = default;
    uint32_t worker_id{0};    /**< 绑定该会话的 Worker ID */
};

/**
 * @brief 硬件推理后端抽象接口
 */
class IInferenceBackend {
public:
    virtual ~IInferenceBackend() = default;

    /**
     * @brief 载入不可变模型资产与权重（整包共享）
     *
     * @param model_path_or_config 模型文件路径或加载配置
     * @return std::shared_ptr<ModelAsset> 模型资产；失败返回 nullptr（不抛异常）
     */
    virtual std::shared_ptr<ModelAsset> load_model(const std::string& model_path_or_config) = 0;

    /**
     * @brief 派生 Worker 独立执行会话（各后端自行实现显存权重共享，如 rknn_dup_context）
     *
     * @param model 共享的模型资产（会话持有其引用，保证生命周期早于 model 结束）
     * @param worker_id Worker 唯一标识
     * @return std::unique_ptr<SessionContext> 独占会话；失败返回 nullptr
     */
    virtual std::unique_ptr<SessionContext> create_session(std::shared_ptr<ModelAsset> model, uint32_t worker_id) = 0;

    /**
     * @brief 执行硬件推理
     *
     * ⚠️ 会话为单 Worker 独占，不得跨线程并发调用同一 session。
     * ⚠️ 失败返回非 OK 状态码，但 outputs 的语义（空 vector）与「合法空输出」不可区分。
     *
     * @param session 该 Worker 的独占执行会话
     * @param inputs 输入张量列表
     * @param[out] outputs 输出张量列表（失败时可能为空）
     * @return zhulong_status_t ZHULONG_PLUGIN_OK 成功；其余为错误码
     */
    virtual zhulong_status_t forward(SessionContext* session,
                                     const std::vector<Tensor>& inputs,
                                     std::vector<Tensor>& outputs) = 0;
};

} // namespace zhulong

