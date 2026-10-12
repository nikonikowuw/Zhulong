# 算法插件 SDK、公共运行库与通用 Runner (子任务一)

## 1. 目标与定位

交付算法插件架构父任务（`10-10-algorithm-plugin-architecture`）阶段一底座：
- 冻结插件版本化纯 C ABI 与不透明句柄契约 (`native/sdk/include/zhulong/plugin.h`)；
- 封装类似 PyTorch / OpenCV 体验的现代 C++17 极简高层 SDK (`native/sdk/include/zhulong/algo.hpp`)；
- 实现通用 Native Runner 进程 (`zhulong_algo_runner`)，支持插件动态装载、多 Worker 调度与任务状态隔离；
- 实现公共算法运行库底层架构 (`libzhulong_algo_runtime.so`)，定义硬件抽象层 (`IInferenceBackend` 与 `IVisionBackend`) 并提供 CPU 优化测试桩；
- 实现单文件、单次推理的独立黑盒验包 CLI 工具与 2 个独立脱机示例插件；
- 全套能力在 Host 开发机脱机闭环，通过 `make native-test` 100% 自动化验证。

## 2. 需求范围 (Requirements)

### R1. 纯 C ABI 与跨语言/跨编译器防爆门
- 公开头文件 `native/sdk/include/zhulong/plugin.h` 严格使用纯 C 类型与定宽整数（`uint32_t`, `int64_t` 等），禁止包含 C++ class、STL 或厂商私有头文件；
- 导出版本化 API 查询表（`zhulong_plugin_api_v1`），定义模型组、Worker 执行实例、任务状态、同步 `process` 与结果释放（`result_release`）函数指针；
- 所有 C 导出入口强制包裹 `try { ... } catch (...)` 异常防爆门，严禁 C++ 异常穿越动态库边界。

### R2. 现代 C++17 高层 SDK (类 PyTorch/OpenCV 体验)
- 在 `native/sdk/include/zhulong/algo.hpp` 提供现代 C++17 Header-only 薄封装；
- 暴露 `zhulong::Model`（模型载入与生命周期）、`zhulong::Session`（Worker 会话与权重共享绑定）、`zhulong::Tensor`（多维张量与数据视图）、`zhulong::ops::letterbox` 与 `zhulong::ops::nms`；
- 内部自动通过 RAII 管理资源，并通过宏/内联胶水将用户 C++ 类导出为合法的纯 C ABI 函数表，算法作者无需手动编写 C 指针管理样板代码。

### R3. 公共算法运行库与硬件抽象层 (HAL)
- 产出独立动态库 `libzhulong_algo_runtime.so`；
- 抽象统一推理后端接口 `IInferenceBackend`（模型加载、基于平台特性的会话派生与权重共享、同步执行）；
- 抽象统一视觉前处理接口 `IVisionBackend`（图像色彩转换、缩放与 Letterbox 补边）；
- 实现默认的 `CpuInferenceBackend` 与 `CpuVisionBackend`（集成 ARM NEON / SIMD 优化），确保在无专有 NPU 硬件的开发主机上 100% 可用且可作为后续透明兜底。

### R4. 通用原生 Runner 进程 (`zhulong_algo_runner`)
- 独立原生可执行程序，作为一包一进程的宿主底座；
- 负责 `dlopen` 加载算法 `.so`、校验 ABI 版本协商、创建模型组与 Worker 线程池；
- 实现 Task 与 Worker 的亲和绑定，确保单摄像机任务的时序状态隔离。

### R5. 独立单次黑盒验包 CLI (R12)
- Runner 内置黑盒验包命令行模式（如 `--verify-plugin <so_path> --sample <file_path>`）；
- 内部适配器读取指定文件样本转换为合法输入，调用插件标准 `process` 路径执行一次；
- 校验输出协议合法性、字段完整性与释放契约；合法空结果判定通过，不评估识别语义与精度；
- 输出结构化验包报告，区分环境/ABI/加载/执行/协议各阶段状态。

### R6. 独立脱机示例插件
- 构建至少 2 个独立示例插件（如 `mock_detector_plugin.so` 与 `mock_classifier_plugin.so`）；
- 示例插件仅依赖导出的 SDK 头文件与约定运行时，不依赖主引擎内部私有类（`ZhulongEngine` 等）。

## 3. 验收标准 (Acceptance Criteria)

- [x] **AC1 (ABI 纯净与异常拦截)**：纯 C ABI 头文件通过 C99 与 C++17 编译器双向严格编译检查，插件模拟抛出 C++ 异常时在 C ABI 边界被拦截并返回标准错误码，不发生进程崩溃。
- [x] **AC2 (高层 SDK 极简开发体验)**：示例插件使用 `zhulong/algo.hpp` 编写，代码不出现原始 C ABI 句柄与显式 `free`，全链路通过 RAII 自动管理资源生命周期。
- [x] **AC3 (HAL 抽象与 CPU 兜底验证)**：公共运行库通过抽象接口驱动 CPU 优化桩完成完整推理与图像 Letterbox 前处理，内存无泄漏。
- [x] **AC4 (通用 Runner 调度与状态隔离)**：Runner 成功装载插件并由多 Worker 并发处理不同任务请求，验证同模型资源共享与任务状态彼此隔离。
- [x] **AC5 (独立黑盒验包 CLI)**：验包工具在不启动 Go 主程序与媒体流的前提下，对合法样本成功完成单次推理验包并输出结构化报告；测试用例覆盖非法 ABI、文件损坏、执行异常等边界场景。
- [x] **AC6 (工程构建与回归测试)**：新增模块接入 `make native-test`，全部单元测试与 CTest 100% 通过；Sanitizer (ASan/UBSan) 检查零内存泄漏与未定义行为。

## 4. 边界与范围外
- 本子任务不涉及跨进程共享内存（IPC FrameBroker）与 Go 进程生命周期编排（留给子任务二与子任务三）；
- 本子任务不链接真实 RKNN / RGA 硬件动态库（留给子任务四），采用中立 HAL 接口与 CPU 桩实现进行 Host 闭环。
