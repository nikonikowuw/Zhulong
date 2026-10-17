# 接入 ONNX Runtime 推理后端框架 (Technical Design)

## 1. 架构总览与分层设计

ONNX 推理后端作为 `IInferenceBackend` 的具体实现，位于 `native/src/backends/onnx/`，编译期直接编入 `libZhulongAlgoRuntime.so`。

```txt
┌────────────────────────────────────────────────────────────────────────┐
│ 算法插件 (.so) / 算法 Runner (zhulong_algo_runner)                     │
│  └─ SDK 门面 (zhulong::Model / ops::letterbox / ops::detect)           │
├────────────────────────────────────────────────────────────────────────┤
│ 运行时接口抽象层 (IInferenceBackend)                                   │
│  ├─ ModelAsset (不可变权重资产)                                        │
│  ├─ SessionContext (Worker 独占执行会话)                               │
│  └─ forward (zhulong::Tensor ⇄ zhulong::Tensor)                        │
├────────────────────────────────────────────────────────────────────────┤
│ ONNX 后端实现 (native/src/backends/onnx/)                              │
│  ├─ OnnxModelAsset (持有 Ort::Env + Ort::Session + 张量元信息)         │
│  ├─ OnnxSessionContext (持有 ModelAsset 强引用 + Ort::RunOptions)      │
│  ├─ onnx_types.hpp / onnx_error_map.hpp                                │
│  └─ OnnxInferenceBackend::forward (零拷贝包装输入 ➔ Run ➔ 导出输出)    │
├────────────────────────────────────────────────────────────────────────┤
│ 第三方依赖隔离层: libonnxruntime.so (仅供 ZhulongAlgoRuntime 动态链接) │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 2. 核心类与数据结构设计

### 2.1 依赖头文件绝对物理隔离
- **物理隔离原则**：`#include <onnxruntime_cxx_api.h>` **严禁**出现在任何 `sdk/include/` 的头文件中。
- 仅在 `native/src/backends/onnx/` 目录下的 `.hpp` 与 `.cpp` 中引用。

### 2.2 `OnnxModelAsset`（不可变模型资产）
```cpp
namespace zhulong {

struct TensorMeta {
    std::string name;
    std::vector<int64_t> dims;
    ONNXTensorElementDataType onnx_type;
    DataType zhulong_type;
};

struct OnnxModelAsset : public ModelAsset {
    // 禁止拷贝，保证底层 ORT 句柄不被浅拷贝导致双重释放 (Rule of 5)
    OnnxModelAsset() = default;
    ~OnnxModelAsset() override = default;
    OnnxModelAsset(const OnnxModelAsset&) = delete;
    OnnxModelAsset& operator=(const OnnxModelAsset&) = delete;

    std::shared_ptr<Ort::Env> env;
    std::unique_ptr<Ort::Session> session;
    
    // 缓存解析后的输入/输出元信息，避免每次 forward 时重复分配和反射
    std::vector<TensorMeta> inputs_meta;
    std::vector<TensorMeta> outputs_meta;
    std::vector<const char*> input_names_ptr;
    std::vector<const char*> output_names_ptr;
    
    // ORT 内存分配信息（用于零拷贝包装外部 Tensor）
    Ort::MemoryInfo memory_info = Ort::MemoryInfo::CreateCpu(OrtArenaAllocator, OrtMemTypeDefault);
};

} // namespace zhulong
```

### 2.3 `OnnxSessionContext`（Worker 独占执行会话）
```cpp
namespace zhulong {

struct OnnxSessionContext : public SessionContext {
    OnnxSessionContext() = default;
    ~OnnxSessionContext() override = default;
    OnnxSessionContext(const OnnxSessionContext&) = delete;
    OnnxSessionContext& operator=(const OnnxSessionContext&) = delete;

    // 持有 ModelAsset 的 shared_ptr 保证模型权重晚于所有 Worker 会话销毁
    std::shared_ptr<OnnxModelAsset> asset;
    Ort::RunOptions run_options;
};

} // namespace zhulong
```

---

## 3. 核心调用流与关键机制

### 3.1 模型加载流程 (`load_model`)
1. **参数校验**：检查模型文件存在性与扩展名。
2. **环境与选项初始化**：
   - 创建 `Ort::Env`（设置日志级别为 `ORT_LOGGING_LEVEL_WARNING`）；
   - 配置 `Ort::SessionOptions`：
     ```cpp
     Ort::SessionOptions opts;
     opts.SetIntraOpNumThreads(1); // ⚠️ 边缘防争抢：限制算子内多线程
     opts.SetInterOpNumThreads(1); // 限制算子间多线程
     opts.SetGraphOptimizationLevel(GraphOptimizationLevel::ORT_ENABLE_ALL);
     ```
3. **Session 构造与元信息抽取**：
   - 实例化 `Ort::Session`；
   - 遍历 `session->GetInputCount()` 与 `session->GetOutputCount()`；
   - 解析每个 Input/Output 的名字、Shape、TensorElementType，并建立与 `zhulong::DataType` 的类型映射表；
   - 预分配并填充 `input_names_ptr` 与 `output_names_ptr` 指针缓存。
4. **异常防护**：全流程外层包裹 `try { ... } catch (const Ort::Exception& e) { ... }`，失败时记录日志并安全返回 `nullptr`（不抛出异常）。

### 3.2 Worker 会话派生流程 (`create_session`)
1. 校验 `model != nullptr`；
2. 构造 `std::unique_ptr<OnnxSessionContext>`；
3. 绑定 `worker_id` 和 `model` 强引用；
4. 初始化独立的 `Ort::RunOptions`；
5. 返回派生的上下文句柄。

### 3.3 前向推理流程 (`forward`)
1. **参数与状态前置门禁**：
   - 校验 `session != nullptr`；
   - 校验 `inputs.size() == asset->inputs_meta.size()`；
2. **输入张量零拷贝包装**：
   - 遍历 `inputs`，校验每个输入 Tensor 的 `dtype()` 和 `element_count()` 是否匹配模型要求；
   - 使用 `Ort::Value::CreateTensor` 包装现有 `inputs[i].raw_data()`：
     ```cpp
     auto val = Ort::Value::CreateTensor(
         asset->memory_info,
         const_cast<void*>(inputs[i].raw_data()),
         inputs[i].byte_size(),
         shape.data(),
         shape.size(),
         onnx_type
     );
     ```
   - ⚠️ 零拷贝关键点：直接借用 `Tensor` 的连续内存，不进行临时 `memcpy`！调用期间 `inputs` 生命周期受外部保证。
3. **执行推理**：
   - 调用 `session->Run(run_options, input_names_ptr.data(), ort_inputs.data(), ort_inputs.size(), output_names_ptr.data(), output_names_ptr.size())`；
4. **输出张量回写**：
   - 遍历返回的 `ort_outputs`；
   - 获取各输出张量的实际 Shape 与元素总数；
   - 构造目标 `zhulong::Tensor(dims, dtype)`；
   - 执行一次 `memcpy` 将 ORT 内部内存数据拷贝进目标 `Tensor`（`Tensor` 接管该生命周期，ORT 临时输出随作用域销毁安全释放）；
   - 追加到 `outputs`。
5. **异常防爆门**：
   ```cpp
   catch (const Ort::Exception& e) {
       return map_onnx_error(e.GetOrtErrorCode());
   } catch (const std::bad_alloc&) {
       return ZHULONG_PLUGIN_ERR_OUT_OF_MEMORY;
   } catch (...) {
       return ZHULONG_PLUGIN_ERR_INTERNAL;
   }
   ```

---

## 4. 数据类型与错误码映射表

### 4.1 数据类型映射
| `zhulong::DataType` | `ONNXTensorElementDataType` | 支持说明 |
| :--- | :--- | :--- |
| `FLOAT32` | `ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT` | 基础浮点，CV 模型常用 |
| `UINT8` | `ONNX_TENSOR_ELEMENT_DATA_TYPE_UINT8` | 输入图像原生像素 |
| `INT8` | `ONNX_TENSOR_ELEMENT_DATA_TYPE_INT8` | 量化模型输入输出 |
| `INT32` | `ONNX_TENSOR_ELEMENT_DATA_TYPE_INT32` | 类别、坐标与索引 |
| `INT64` | `ONNX_TENSOR_ELEMENT_DATA_TYPE_INT64` | 形状与大整数 |

### 4.2 错误码映射 (`map_onnx_error`)
| `OrtErrorCode` | `zhulong_status_t` | 说明 |
| :--- | :--- | :--- |
| `ORT_OK` (0) | `ZHULONG_PLUGIN_OK` | 成功 |
| `ORT_INVALID_ARGUMENT` | `ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT` | 参数或模型输入非法 |
| `ORT_NO_SUCHFILE` | `ZHULONG_PLUGIN_ERR_IO` | 模型文件不存在 |
| `ORT_NO_MODEL` | `ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT` | 模型解析失败 |
| `ORT_NOT_IMPLEMENTED` | `ZHULONG_PLUGIN_ERR_NOT_SUPPORTED` | 算子不支持 |
| `ORT_ENGINE_ERROR` | `ZHULONG_PLUGIN_ERR_INTERNAL` | 运行时引擎内部错误 |
| `ORT_RUNTIME_EXCEPTION` | `ZHULONG_PLUGIN_ERR_INTERNAL` | 运行时异常 |
| `ORT_FAIL` 及其他 | `ZHULONG_PLUGIN_ERR_INTERNAL` | 默认兜底 |

---

## 5. 构建系统集成方案 (CMake & build.py)

1. **`native/CMakeLists.txt`**：
   - 增加 `ZHULONG_INFERENCE_BACKEND` 的 `onnx` 校验：
     ```cmake
     if(NOT ZHULONG_INFERENCE_BACKEND MATCHES "^(cpu|rknn|onnx)$")
         message(FATAL_ERROR "ZHULONG_INFERENCE_BACKEND 取值非法: ${ZHULONG_INFERENCE_BACKEND}")
     endif()
     ```
   - 声明缓存变量：`ZHULONG_ONNX_ROOT`；
   - 路径强校验：
     - `${ZHULONG_ONNX_ROOT}/include/onnxruntime_cxx_api.h` 必须存在；
     - `${ZHULONG_ONNX_ROOT}/lib/libonnxruntime.so` 必须存在；
   - 链接规约：
     - `ZhulongAlgoRuntime` 链接 `${ZHULONG_ONNX_ROOT}/lib/libonnxruntime.so`；
     - 添加编译宏：`ZHULONG_INFERENCE_ONNX=1`；
     - 添加链接选项：`-Wl,-rpath-link,${ZHULONG_ONNX_ROOT}/lib`；
     - 清除 `BUILD_RPATH` 避免污染开发机路径。
2. **`native/src/algorithm/runtime/runtime_factory.cpp`**：
   ```cpp
   #ifdef ZHULONG_INFERENCE_ONNX
   #include <backends/onnx/onnx_inference_backend.hpp>
   #endif
   
   std::shared_ptr<IInferenceBackend> get_default_inference_backend() {
   #if defined(ZHULONG_INFERENCE_RKNN)
       static auto backend = std::make_shared<RknnInferenceBackend>();
   #elif defined(ZHULONG_INFERENCE_ONNX)
       static auto backend = std::make_shared<OnnxInferenceBackend>();
   #else
       static auto backend = std::make_shared<CpuInferenceBackend>();
   #endif
       return backend;
   }
   ```
3. **`native/scripts/build.py`**：
   - 增加对 `--inference-backend onnx` 的支持；
   - 如果开启 `onnx`，自动定位或接收 `--onnx-root` 参数并透传给 CMake。

---

## 6. 测试与验证策略

1. **单元测试 (`tests/onnx_backend_test.cpp`)**：
   - **模型加载与元数据解析测试**：加载一个微型单算子 ONNX 模型（如 `Identity` 或 `Add`），验证输入输出名称与维度正确被识别；
   - **多 Worker 会话隔离测试**：派生两个独立 session，在不同线程并发调用 `forward`，验证输出确定性且互不干扰；
   - **异常分支测试**：传递空指针 session、错误尺寸输入 Tensor、不存在的文件路径，验证严格返回对应的 `ZHULONG_PLUGIN_ERR_*`；
   - **内存泄漏审计**：借助 ASan 跑完单元测试，验证无内存泄漏、无 double-free。
