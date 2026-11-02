/**
 * @file onnx_inference_backend.hpp
 * @brief ONNX Runtime 推理后端：IInferenceBackend 的跨平台通用推理实现
 *
 * ⚠️ 本头文件不得 include <onnxruntime_cxx_api.h>；ONNX Runtime 专有类型只在
 * onnx_types.hpp / onnx_error_map.hpp / onnx_inference_backend.cpp 内出现。
 *
 * ⚠️ `last_load_status()` 是**非虚**成员：IInferenceBackend 的 vtable 位于
 * plugin.so 与 libZhulongAlgoRuntime.so 的边界上（内部 ABI），绝不能为了多带一个
 * 诊断字段而改虚表。
 */

#pragma once

#include <zhulong/runtime/inference_backend.hpp>

#include <atomic>

namespace zhulong {

/**
 * @brief ONNX Runtime 推理后端实现
 */
class OnnxInferenceBackend : public IInferenceBackend {
public:
    OnnxInferenceBackend() = default;
    ~OnnxInferenceBackend() override = default;
    OnnxInferenceBackend(const OnnxInferenceBackend&) = delete;
    OnnxInferenceBackend& operator=(const OnnxInferenceBackend&) = delete;
    OnnxInferenceBackend(OnnxInferenceBackend&&) = delete;
    OnnxInferenceBackend& operator=(OnnxInferenceBackend&&) = delete;

    std::shared_ptr<ModelAsset> load_model(const std::string& model_path_or_config) override;

    std::unique_ptr<SessionContext> create_session(std::shared_ptr<ModelAsset> model,
                                                   uint32_t worker_id) override;

    zhulong_status_t forward(SessionContext* session,
                             const std::vector<Tensor>& inputs,
                             std::vector<Tensor>& outputs) override;

    /**
     * @brief 最近一次 load_model 的结果（成功为 ZHULONG_PLUGIN_OK）
     *
     * 为什么需要它：`load_model` 的返回类型是 `shared_ptr`，没有状态码通道，且运行库
     * **刻意不链接 `ZhulongLog`**（见 native/CMakeLists.txt），后端没有日志可打。
     * 若不额外暴露，运维只能看到「模型加载失败」而无法区分「文件不存在 / protobuf
     * 损坏 / 图非法 / 内存不足 / EP 不支持」，等于把 spec §6.3 的错误码映射做成了死代码。
     *
     * 语义：单例后端可能被多个模型加载共享，因此只保证「最近一次调用」的结果，
     * 不保证与某次具体失败一一对应；它是诊断面，不是同步原语。
     */
    [[nodiscard]] zhulong_status_t last_load_status() const noexcept {
        return last_load_status_.load(std::memory_order_relaxed);
    }

private:
    // 失败时记录映射后的状态并返回 nullptr，保证「每个失败出口都留下原因」。
    std::shared_ptr<ModelAsset> fail_load(zhulong_status_t status) noexcept {
        last_load_status_.store(status, std::memory_order_relaxed);
        return nullptr;
    }

    std::atomic<zhulong_status_t> last_load_status_{ZHULONG_PLUGIN_OK};
};

} // namespace zhulong
