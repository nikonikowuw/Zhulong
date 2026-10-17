# 实施计划：接入 ONNX 版本人脸识别算法包

## 执行清单

- [x] **Task 1: 检测端 NCHW Float32 预处理实现**
  - 在 `packages/face_recognition/src/detector.cpp` 中新增根据模型期望格式选择 HWC uint8 或 NCHW float32 张量的逻辑。
  - 实现标准 SCRFD 归一化：`(x - 127.5f) / 128.0f`。
  - 保证原 RKNN NPU 路径分支与 CPU 桩分支行为不受影响。

- [x] **Task 2: 仿射对齐与特征端 NCHW Float32 预处理实现**
  - 在 `packages/face_recognition/src/aligner.cpp` 中支持输出 `[1, 3, 112, 112]` FLOAT32 张量。
  - 实现 ArcFace/FaceLiVTv2 归一化：`(x - 127.5f) / 127.5f`。
  - 在 `packages/face_recognition/src/extractor.cpp` 中适配 NCHW float32 输入张量并完成 512 维输出的 L2 归一化。

- [x] **Task 3: SCRFD 解码器校验与模型对齐**
  - 审查 `decode_scrfd`，确保正确解析 ONNX SCRFD 9 头输出（3 stride × {score, bbox, kps}）。
  - 确认跳过二次 Sigmoid，并保证输出人脸框与 5 点关键点严格归一化到 `[0.0, 1.0]`。

- [x] **Task 4: 构建系统与算法包目标集成**
  - 检查并更新 `packages/face_recognition/CMakeLists.txt`（如存在）或主 CMake 配置，确保在 ONNX 模式下能正确编译 `libFaceRecognitionPlugin.so`。
  - 验证 `packages/face_recognition/targets/cpu/manifest.json` 配置。

- [x] **Task 5: 原生单元测试与精度校验**
  - 编写/扩充 `native/tests/face_recognition_plugin_test.cpp` 或专用测试用例，覆盖 ONNX 模式下人脸检测、特征提取与 1:N 底库比对。
  - 验证多 Worker 并发会话隔离与张量生命周期安全。

- [x] **Task 6: 内存安全审计与回归验证**
  - 运行 AddressSanitizer/UndefinedSanitizer 审计：
    `python3 native/scripts/build.py test --inference-backend onnx --sanitizer address`
  - 确保无内存泄漏、越界及符号未定义问题。

## 验证命令与检查点

```bash
# 1. 配置并构建 ONNX 推理后端及原生测试
python3 native/scripts/build.py configure --inference-backend onnx
cmake --build build/native/host --parallel 8 --target ZhulongOnnxTests

# 2. 运行 ONNX 后端与人脸插件测试
ctest --test-dir build/native/host -R "ZhulongOnnx|FaceRecognition" -V

# 3. 运行 AddressSanitizer 内存安全审计
python3 native/scripts/build.py configure --inference-backend onnx --sanitizer address
cmake --build build/native/host-address --parallel 8 --target ZhulongOnnxTests
ctest --test-dir build/native/host-address -R "ZhulongOnnx|FaceRecognition" -V

# 4. 全量回归测试
python3 native/scripts/build.py test
```
