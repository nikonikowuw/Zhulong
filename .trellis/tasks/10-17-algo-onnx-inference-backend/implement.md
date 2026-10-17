# 接入 ONNX Runtime 推理后端框架 (Execution Plan)

## 0. 实施原则与防御准则
- **头文件绝对隔离**：`onnxruntime_cxx_api.h` 仅限 `native/src/backends/onnx/` 内部包含，严禁污染 `sdk/include/`；
- **异常防爆门**：所有对外虚函数实现必须在内部拦截 `Ort::Exception`、`std::exception` 及未知异常，返回 `zhulong_status_t`，严禁 C++ 异常穿越 C ABI；
- **显式所有权 (Rule of 5)**：`OnnxModelAsset` 与 `OnnxSessionContext` 禁用拷贝，由 `shared_ptr` 管理不可变资产生命周期；
- **产物卫生**：CMake 配置 `SKIP_BUILD_RPATH TRUE` 与 `-Wl,-rpath-link`，绝不向宿主 `ZhulongEngine` 引入任何 ONNX 动态库依赖。

---

## 1. 任务清单 (Checklist)

### 阶段 1：数据结构、错误码映射与测试模型准备
- [x] **1.1** 编写 `native/src/backends/onnx/onnx_types.hpp`：
  - 定义 `TensorMeta`，声明 `OnnxModelAsset`（继承自 `ModelAsset`）与 `OnnxSessionContext`（继承自 `SessionContext`）；
  - 显式声明禁用拷贝构造与赋值（Rule of 5）。
- [x] **1.2** 编写 `native/src/backends/onnx/onnx_error_map.hpp`：
  - 实现 `map_onnx_error(OrtErrorCode)`，将 `OrtErrorCode` 严格映射至统一状态码 `zhulong_status_t`。
- [x] **1.3** 准备或生成轻量测试用 ONNX 夹具（Fixture）：
  - 生成一个极小确定性的浮点模型（如 `add_const.onnx` 或线性检测桩，输入 `[1, 3, 32, 32]`，输出预测），放置在 `native/tests/fixtures/onnx/`。

### 阶段 2：`OnnxInferenceBackend` 核心实现
- [x] **2.1** 编写 `native/src/backends/onnx/onnx_inference_backend.hpp`：
  - 继承 `IInferenceBackend`，声明 `load_model`、`create_session`、`forward`。
- [x] **2.2** 编写 `native/src/backends/onnx/onnx_inference_backend.cpp`：
  - **`load_model`**：参数校验、创建 `Ort::Env` 与 `Ort::SessionOptions`（限制 `intra_op_num_threads=1`、`inter_op_num_threads=1`）、抽取解析输入输出节点名称和维度类型、异常拦截与返回 `nullptr`；
  - **`create_session`**：Worker 独立会话派生，持有 `asset` 强引用，初始化独立的 `Ort::RunOptions`；
  - **`forward`**：参数门禁（空指针、输入维度/数量校验）、利用 `Ort::Value::CreateTensor` 对输入 `Tensor` 的连续内存进行零拷贝包装、调用 `Session::Run`、将结果解析回写至 `std::vector<Tensor>`，全流程捕获异常映射返回。

### 阶段 3：构建系统集成与工厂分发
- [x] **3.1** 修改 `native/CMakeLists.txt`：
  - 增加 `ZHULONG_INFERENCE_BACKEND` 的 `onnx` 校验分支；
  - 声明 `ZHULONG_ONNX_ROOT` 路径校验逻辑（检查 `include/onnxruntime_cxx_api.h` 与 `lib/libonnxruntime.so`）；
  - 将 `onnx_inference_backend.cpp` 条件编入 `ZhulongAlgoRuntime`；
  - 配置 `ZHULONG_INFERENCE_ONNX=1` 编译宏，添加 `-Wl,-rpath-link`，清空 `BUILD_RPATH`。
- [x] **3.2** 修改 `native/src/algorithm/runtime/runtime_factory.cpp`：
  - 在 `#ifdef ZHULONG_INFERENCE_ONNX` 下实例化 `OnnxInferenceBackend` 单例。
- [x] **3.3** 更新 `native/scripts/build.py`：
  - 增加对 `--inference-backend onnx` 与 `ZHULONG_ONNX_ROOT` 的环境检测与参数透传。

### 阶段 4：单元测试与验证
- [x] **4.1** 编写 `native/tests/onnx_backend_test.cpp`：
  - 测试正常加载、模型元数据检查；
  - 测试输入零拷贝直传与前向推理输出数值匹配；
  - 测试多 Worker 并发会话隔离（多线程并发调用 `forward`）；
  - 测试异常分支（无效路径、空指针 session、维度不匹配输入）。
- [x] **4.2** CMake 注册测试目标：
  - 仅在 `ZHULONG_INFERENCE_BACKEND=onnx` 且 `BUILD_TESTING=ON` 时编译并注册 `ZhulongOnnxTests`。

### 阶段 5：质量检验与回归验证
- [x] **5.1** 执行 ONNX 后端独立单元测试，验证全通过。
- [x] **5.2** 运行 ASan/UBSan 内存与线程探测，验证零内存泄漏、零未定义行为。
- [x] **5.3** 验证既有默认 `cpu` 与 `rknn` 构建配置不受影响（零回归）。

---

## 2. 验证命令集

```bash
# 1. 准备/指定 ONNX Runtime 根目录（开发机本地或 vendor 目录）
export ZHULONG_ONNX_ROOT=/path/to/onnxruntime

# 2. 构建与单元测试 (ONNX 模式)
python3 native/scripts/build.py configure --inference-backend onnx
cmake --build build -j$(nproc)
ctest --test-dir build -R ZhulongOnnx -V

# 3. ASan 内存检测
python3 native/scripts/build.py configure --inference-backend onnx --sanitizer address
cmake --build build -j$(nproc)
ctest --test-dir build -R ZhulongOnnx -V

# 4. 回归测试：确认默认 CPU 路径构建与测试正常
python3 native/scripts/build.py configure --inference-backend cpu
cmake --build build -j$(nproc)
ctest --test-dir build -V
```
