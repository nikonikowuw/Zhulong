# 接入 ONNX Runtime 推理后端框架 (PRD)

## 1. Goal & Context

### 1.1 背景
当前 Zhulong 原生引擎（`libZhulongAlgoRuntime.so`）在推理后端上支持了 `CpuInferenceBackend`（确定性 Mock 桩）与 `RknnInferenceBackend`（Rockchip NPU 加速）。
为了打破对单一厂商硬件（Rockchip）的强绑定，满足 x86 工控机、边缘通用计算、算法包基准校验（Golden Baseline）以及本地 CI/CD 真实模型自动化测试需求，需要基于现有的 `IInferenceBackend` 抽象扩展接入标准的 **ONNX Runtime (ORT)** 推理后端。

### 1.2 目标与范围收敛（方案 A：通用 CPU 推理底座）
首期聚焦构建稳定可靠的 **通用 CPU 推理底座（x86_64 与 aarch64）**：
1. 模型的安全加载与不可变资产（`OnnxModelAsset`）管理，支持多 Worker 共享同一模型权重；
2. Worker 独占执行会话（`OnnxSessionContext`）派生与并发线程安全；
3. `zhulong::Tensor` 与 `Ort::Value` 之间类型安全、低开销的张量桥接；
4. 构建系统集成（CMake 支持 `ZHULONG_INFERENCE_BACKEND=onnx`，依赖隔离与产物 RPATH 卫生）；
5. 边缘并发场景下的 CPU 线程池调优（防线程争抢）；
6. 预留清晰的 Execution Provider 扩展点，为后续二期平滑接入 GPU (CUDA/TensorRT) 或 Minimal Build 打下坚实基础。

---

## 2. Requirements & Constraints

### 2.1 运行时接口实现 (`IInferenceBackend`)
- **不可变模型资产 (`OnnxModelAsset`)**：
  - 封装不可变的 `Ort::Env` 与加载的模型权重；
  - 启动阶段自动解析并缓存模型的输入输出张量元数据（Node Names、Dimensions、Data Types）；
  - 遵循 Rule of 5 禁用浅拷贝，RAII 保证析构安全。
- **Worker 独占执行会话 (`OnnxSessionContext`)**：
  - 支持多 Worker 并发从同一 `OnnxModelAsset` 派生独立会话；
  - 线程隔离：每个 Worker 持有独立的执行会话与 RunOptions。
- **前向推理 (`forward`)**：
  - 输入：校验 `zhulong::Tensor` 维度（如 NCHW/NHWC）与数据类型，适配或零拷贝视图绑定至 `Ort::Value`；
  - 执行：调用 `Ort::Session::Run`；
  - 输出：将输出 `Ort::Value` 安全回写至输出 `std::vector<Tensor>`；
  - 异常安全：全流程包裹 `try-catch` 拦截 `Ort::Exception` 与标准异常，严格映射为 `zhulong_status_t`，禁止异常穿越 C ABI。

### 2.2 线程与资源控制 (边缘优化)
- 限制 `intra_op_num_threads = 1`（或由配置/CPU 核心数约束），`inter_op_num_threads = 1`，避免多路视频流并发分析时引发剧烈的 CPU 上下文切换。
- 开启 ORT 基础/全量图优化级别（`ORT_ENABLE_ALL`）。

### 2.3 构建系统与依赖隔离
- `native/CMakeLists.txt` 扩展 `ZHULONG_INFERENCE_BACKEND=onnx` 分支；
- 新增 `ZHULONG_ONNX_ROOT` 变量（指向包含 `include/onnxruntime_cxx_api.h` 及 `lib/libonnxruntime.so` 的前缀）；
- 严格遵循现有链接隔离规约：
  - 仅链接进 `libZhulongAlgoRuntime.so`，宿主引擎 `ZhulongEngine` 绝不链接；
  - 配置 `-Wl,-rpath-link`，并在产物中清空 `BUILD_RPATH`；
  - ORT 头文件仅允许在 `native/src/backends/onnx/` 内包含，严禁外溢至公开 SDK 头文件。

---

## 3. Acceptance Criteria

- [x] **AC1 (依赖隔离与接口实现)**：`native/src/backends/onnx/` 完成 `OnnxInferenceBackend` 实现，ORT 专有头文件和类型严格封装在后端目录内。
- [x] **AC2 (模型加载与多会话派生)**：支持加载标准 `.onnx` 模型文件，支持单一模型并发派生多个 Worker SessionContext，生命周期由 `shared_ptr` 安全管控，析构无泄漏、无 double-destroy。
- [x] **AC3 (张量推理正确性)**：使用标准测试模型进行推理测试，输入合成张量，输出的 Tensor 维度与数值精度符合预期。
- [x] **AC4 (异常与错误安全)**：当输入维度不匹配、类型不匹配、模型文件损坏或为 nullptr 时，能安全拦截并返回规范错误码（如 `ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT`、`ZHULONG_PLUGIN_ERR_NOT_FOUND`），不发生 crash 或未捕获异常。
- [x] **AC5 (构建产物与链接隔离)**：在 `-DZHULONG_INFERENCE_BACKEND=onnx -DZHULONG_ONNX_ROOT=...` 配置下，CMake 健全性检查通过，`libZhulongAlgoRuntime.so` 成功构建，`BUILD_RPATH` 为空，现有 CPU/RKNN 路径保持零回归。

---

## 4. Technical Constraints

- **C++ 标准**：C++17。
- **目标架构**：x86_64 及 aarch64 (Linux)。
- **ABI 防御**：遵循 `zhulong_plugin_api_v1` 契约，状态码统一。
