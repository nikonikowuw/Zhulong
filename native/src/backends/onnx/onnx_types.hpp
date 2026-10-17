/**
 * @file onnx_types.hpp
 * @brief ONNX Runtime 私有资源模型：不可变模型资产与 Worker 独占执行会话上下文
 *
 * ⚠️ 本文件是允许 include <onnxruntime_cxx_api.h> 的内部头文件之一（另一个是 onnx_error_map.hpp）。
 * 它只被 native/src/backends/onnx/ 内的实现引用，严禁泄露到 runtime/ 或 sdk/include/
 * （directory-structure.md 铁律 4：厂商/第三方推理 SDK 物理隔离）。
 *
 * 核心设计原则：
 * 1. OnnxModelAsset 每模型一个、进程内共享，持有 Ort::Env、Ort::Session 及解析后的输入/输出张量元信息。
 * 2. OnnxSessionContext 每 Worker 一个、独占，持有所属 OnnxModelAsset 的强引用与独立的 Ort::RunOptions。
 * 3. 严格遵循 Rule of 5，显式禁用拷贝与移动，保证底层 ORT 句柄生命周期安全。
 */

#pragma once

#include <onnxruntime_cxx_api.h>

#include <algorithm/runtime/inference_backend.hpp>

#include <cstdint>
#include <memory>
#include <string>
#include <vector>

namespace zhulong {

/**
 * @brief 单个输入/输出张量节点的元信息缓存
 */
struct TensorMeta {
    std::string name;                                                       /**< 节点名称 */
    std::vector<int64_t> dims;                                              /**< 静态/动态维度列表（动态维度在模型中可能为 -1） */
    ONNXTensorElementDataType onnx_type{ONNX_TENSOR_ELEMENT_DATA_TYPE_UNDEFINED}; /**< ORT 元素类型 */
    DataType zhulong_type{DataType::FLOAT32};                               /**< 映射后的 Zhulong Tensor 数据类型 */
};

/**
 * @brief ONNX Runtime 不可变模型资产（整包多线程共享）
 *
 * ⚠️ 成员声明顺序不可随意调换：env 必须先于 session 声明，
 * 确保析构时先销毁 session 再释放 env 引用。
 */
struct OnnxModelAsset : public ModelAsset {
    OnnxModelAsset();
    ~OnnxModelAsset() override;
    OnnxModelAsset(const OnnxModelAsset&) = delete;
    OnnxModelAsset& operator=(const OnnxModelAsset&) = delete;
    OnnxModelAsset(OnnxModelAsset&&) = delete;
    OnnxModelAsset& operator=(OnnxModelAsset&&) = delete;

    std::shared_ptr<Ort::Env> env;            /**< 进程级共享 ORT 运行环境 */
    std::unique_ptr<Ort::Session> session;    /**< 已加载并完成图优化的 ORT 会话（Run 并发安全） */
    std::string model_path;                   /**< 模型文件路径 */

    std::vector<TensorMeta> inputs_meta;      /**< 模型输入张量元信息列表 */
    std::vector<TensorMeta> outputs_meta;     /**< 模型输出张量元信息列表 */
    std::vector<const char*> input_names_ptr; /**< 指向 inputs_meta[i].name.c_str() 的连续指针缓存 */
    std::vector<const char*> output_names_ptr;/**< 指向 outputs_meta[i].name.c_str() 的连续指针缓存 */

    Ort::MemoryInfo memory_info{nullptr};     /**< CPU Arena 内存信息（供零拷贝包装输入 Tensor 复用） */
};

/**
 * @brief ONNX Runtime Worker 独占执行会话上下文
 */
struct OnnxSessionContext : public SessionContext {
    OnnxSessionContext();
    ~OnnxSessionContext() override;
    OnnxSessionContext(const OnnxSessionContext&) = delete;
    OnnxSessionContext& operator=(const OnnxSessionContext&) = delete;
    OnnxSessionContext(OnnxSessionContext&&) = delete;
    OnnxSessionContext& operator=(OnnxSessionContext&&) = delete;

    std::shared_ptr<OnnxModelAsset> asset;    /**< 持有模型资产强引用，确保权重晚于所有会话销毁 */
    Ort::RunOptions run_options{nullptr};     /**< 该 Worker 独占的运行期控制选项 */
};

} // namespace zhulong
