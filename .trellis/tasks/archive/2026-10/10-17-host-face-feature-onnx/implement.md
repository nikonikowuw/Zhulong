# 执行计划：宿主引擎人脸特征提取接入 ONNX 真实推理与 TTL 自动休眠

## 执行步骤清单

### 步骤 1：插件侧导出单次特征提取与释放 C ABI (`packages/face_recognition`)
- [x] 在 `packages/face_recognition/src/face_recognition.cpp` 实现：
  - `zhulong_face_extract_feature`：初始化检测与抽取模型、SCRFD 检测、置信度排序选最优人脸、仿射对齐至 112×112、提取 512 维归一化特征；未检出人脸返回 `ZHULONG_PLUGIN_ERR_NOT_FOUND`；
  - `zhulong_face_extract_release`：显式释放单例上下文中的会话与模型资源；
- [x] 更新 `native/cmake/plugin-exports.map`，确保 `zhulong_face_extract_feature` 与 `zhulong_face_extract_release` 被导出；
- [x] 验证：编译插件，使用 `nm -D --defined-only` 确认符号导出。

### 步骤 2：宿主侧动态加载、Fail-Closed 报错与 TTL 空闲卸载 (`native/src/abi/engine.cpp`)
- [x] 在 `native/src/abi/engine.cpp` 实现 `FacePluginLoader`：
  - 多候选路径 `dlopen` 与 `dlsym` 挂载；
  - 加载失败时显式返回 `Zhulong_ERR_UNSUPPORTED`（Fail Closed）；
  - 实现后台 Reaper 线程与条件变量，支持超时（默认 60s，支持 `ZHULONG_FACE_PLUGIN_IDLE_TIMEOUT_SEC`）自动释放模型并 `dlclose`；
  - 在 `Zhulong_engine_destroy` 中实现无泄漏退出清理；
- [x] 改造 `Zhulong_engine_extract_face_feature` 接入 `FacePluginLoader`；
- [x] 验证：编译 Native 代码并跑通基础调用。

### 步骤 3：完善 Native 测试与 Go 测试样本
- [x] 更新 `native/tests/face_feature_extract_test.cpp`：
  - 验证使用真实人脸样本（`face_detect_sample.ppm`）提取出 512 维真实有效特征且 L2 模长为 1.0；
  - 验证全黑/噪点图片返回 `Zhulong_ERR_NOT_FOUND`；
  - 验证动态库不可用时返回 `Zhulong_ERR_UNSUPPORTED`；
  - 验证 TTL 超时后自动卸载（设置短期环境变量如 1 秒测试卸载后可再次唤醒）；
    → 实际落在独立的黑盒用例 `native/tests/face_plugin_ttl_test.cpp`（配合最小插件替身
      `native/tests/mock_face_plugin.cpp`），不依赖 ONNX Runtime，CPU/ONNX 后端均可运行；
- [x] 更新 `internal/engine/engine_face_test.go`：
  - 将原本 64x64 纯色块替换为有效的人脸图片样本，确保通过真实人脸检测与特征提取；
- [x] 验证：
  - `python3 native/scripts/build.py test --inference-backend onnx`
  - `python3 native/scripts/build.py go test -race ./internal/engine`

### 步骤 4：回归与完整验证
- [x] 运行 `make check`，确认全绿。

## 验证命令

```bash
# 1. 编译并运行 Native 单元测试 (含 ONNX 测试)
python3 native/scripts/build.py test --inference-backend onnx

# 2. 运行 Go Engine 边界与测试
python3 native/scripts/build.py go test -race ./internal/engine

# 3. 运行全链路代码门禁
make check
```

## 回滚计划
若动态加载或符号导出产生未预期问题，可通过 Git 检出恢复 `packages/face_recognition/src/face_recognition.cpp`、`native/cmake/plugin-exports.map` 和 `native/src/abi/engine.cpp`。

## 后续待办（本任务未做，已与用户确认）

- **`internal/person` 错误映射过粗**：`internal/person/service.go` 的 `AddFaceWithReader` 把
  `ExtractFaceFeature` 的**任何**错误都映射为 `400 NO_FACE_DETECTED`。于是插件缺失
  （`engine.ErrUnsupported`）或推理内部故障在 UI 上会显示成「图片里没有人脸」，
  而 `design.md` 的错误矩阵要求 `500 FEATURE_EXTRACT_FAILED`。
  修它需要跨包错误契约：`internal/person` 禁止 `import "C"`、也不应依赖 `internal/engine`，
  因此要么在 `internal/person` 定义中立 sentinel 并由适配层转换，要么引入独立的错误分类包。
  属于「改公共接口 / 挪行为归属」的设计判断，未在本任务内改动。
