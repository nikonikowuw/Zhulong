# 算法插件 SDK、公共运行库与通用 Runner：实施计划

## 实施阶段

### 阶段一：纯 C ABI 头文件与基础类型定义
- [ ] 编写 `native/sdk/include/zhulong/plugin.h`：定义定宽整数、错误码、不透明句柄、视图结构体与 `zhulong_plugin_api_v1` 函数表；
- [ ] 编写 C 语言兼容性测试 `native/tests/plugin_c_abi_test.c`：验证纯 C 编译器（gcc -std=c99）能够无警告解析头文件；
- [ ] 验证命令：`cmake --build build/native --target ZhulongPluginCAbiTests && ctest --test-dir build/native -R ZhulongPluginCAbiTests`。

### 阶段二：公共算法运行库底层与 HAL 抽象
- [ ] 搭建 `native/src/algorithm/runtime/` 目录；
- [ ] 定义硬件抽象层中立接口：`inference_backend.hpp` (`IInferenceBackend`) 与 `vision_backend.hpp` (`IVisionBackend`)；
- [ ] 实现 `cpu_inference_backend.cpp` 与 `cpu_vision_backend.cpp`（包含通用 Letterbox、双线性缩放与 NMS 后处理）；
- [ ] 编译生成 `libzhulong_algo_runtime.so`；
- [ ] 编写运行库单元测试 `native/tests/algo_runtime_test.cpp` 验证 CPU 桩与图像前处理。

### 阶段三：类 PyTorch/OpenCV 现代 C++17 SDK
- [ ] 编写 `native/sdk/include/zhulong/algo.hpp`：实现 `zhulong::Tensor`, `zhulong::Model`, `zhulong::Session`, `zhulong::ops`；
- [ ] 实现导出宏 `ZHULONG_REGISTER_PLUGIN`，集成全异常 `try-catch` 拦截防爆门；
- [ ] 编写两个脱机示例插件：
  - `native/tests/plugins/mock_detector/mock_detector.cpp`（模拟目标检测，输出检测框）
  - `native/tests/plugins/mock_classifier/mock_classifier.cpp`（模拟分类，输出置信度分数）
- [ ] 验证示例插件仅通过引入 `zhulong/algo.hpp` 即可独立编译为 `.so`，不链接任何宿主私有符号。

### 阶段四：通用 Native Runner 与独立黑盒验包 CLI
- [ ] 在 `native/src/algorithm/runner/` 实现 `zhulong_algo_runner` 主程序；
- [ ] 实现动态库装载器：`dlopen`、符号提取、ABI 版本兼容校验；
- [ ] 实现多线程 Worker 调度器与任务状态管理；
- [ ] 实现独立黑盒验包 CLI 模式 (`--verify-plugin <so> --sample <file>`) 与输入文件适配器，输出结构化验包报告；
- [ ] 编写 Runner 与验包 CLI 自动化集成测试 `native/tests/algo_runner_test.cpp`。

### 阶段五：工程集成、构建与 Sanitizer 门禁
- [ ] 将所有新增 target 接入 `native/CMakeLists.txt`，确保被 `make native-test` 统一调度；
- [ ] 执行常规测试：`make native-test`；
- [ ] 执行 ASan / UBSan 内存与未定义行为检查；
- [ ] 确保无编译告警、无内存泄漏。
