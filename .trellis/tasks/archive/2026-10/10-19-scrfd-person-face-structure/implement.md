# Implementation Plan: 人脸识别插件集成 SCRFD 人体检测双模型

## 1. Ordered Checklist

### Phase 1: 资产集成与配置就绪
- [x] **1.1 复制模型权重**
  - 从 `/home/niko/dev/rknn_model_zoo/examples/scrfd_person_2.5g/model/scrfd_person_2.5g.onnx` 复制至 `packages/face_recognition/targets/cpu/models/`
  - 从 `/home/niko/dev/rknn_model_zoo/examples/scrfd_person_2.5g/model/scrfd_person_2.5g_rk3568_i8.rknn` 复制并重命名为 `packages/face_recognition/targets/rk3568/models/scrfd_person_2.5g.rknn`
- [x] **1.2 更新 Manifest 与打包脚本**
  - 更新 `packages/face_recognition/targets/cpu/manifest.json` 与 `targets/rk3568/manifest.json` 中的计算资源预估（算力点数与内存大小）
  - 确认 `packages/face_recognition/scripts/build_package.py` 能够正确将 `scrfd_person_2.5g` 打入发布包

### Phase 2: C++ 人体检测解码器实现
- [x] **2.1 定义 PersonBodyDetection 与 PersonDetector 接口**
  - 在 `packages/face_recognition/src/detector.hpp` 中声明 `PersonBodyDetection` 结构体
  - 声明 `PersonDetector` 类（包含 `detect(session, image)` 与 CPU 降级桩方法）
- [x] **2.2 实现 SCRFD Person 15 头解码与 NMS**
  - 在 `packages/face_recognition/src/detector.cpp` 中实现 `PersonDetector::decode_scrfd_person`：
    - 遍历 5 个 FPN Stride（`8, 16, 32, 64, 128`），每个尺度 1 个 Anchor
    - 解析 15 个输出头，扣除 Letterbox 居中 Padding 并按 `det_scale` 还原像素坐标
    - 提取 `box`（全身预测框）并计算 IoU NMS
- [x] **2.3 适配 ONNX 与 RKNN 异构张量排布**
  - 支持 ONNX float32 NCHW 预处理与 RKNN uint8 HWC 预处理输入

### Phase 3: 插件全流程集成与空间几何关联
- [x] **3.1 插件生命周期集成双模型**
  - 在 `FaceRecognitionPlugin` 中新增 `person_model_` 与 `person_sessions_` 映射
  - 在 `init()` 中加载 `models/scrfd_person_2.5g`
  - 在 `create_session(worker_id)` 中初始化 Worker 独占的 `person_session`
- [x] **3.2 空间几何关联 (Spatial Association)**
  - 在 `FaceRecognitionPlugin::detect_persons` 中实现空间包含度匹配：
    - 收集 `PersonBodyDetection` 与 `FaceDetection`
    - 按 $y \le y_{top} + 0.40 \cdot h$ 且 $\text{IoF} \ge 0.70$ 关联 Face 到 Person
    - 处理无脸人体（生成 `faces` 为空的 `PersonItem`）与孤立人脸（生成 `body_box = face_box` 的 `PersonItem`）
  - 对挂载的人脸执行 Umeyama 对齐、特征抽取与底库匹配

### Phase 4: 自动化测试与验证
- [x] **4.1 扩展 C++ 测试套件**
  - 更新 `native/tests/face_recognition_plugin_test.cpp`，覆盖插件无模型桩状态下的基础生命周期
  - 更新 `native/tests/face_recognition_onnx_test.cpp`，加载真实 `scrfd_person_2.5g.onnx` 验证人体与人脸协同检测
- [x] **4.2 执行本地与全系统质量检查**
  - 运行 `python3 native/scripts/build.py ctest`
  - 运行 `python3 packages/face_recognition/scripts/build_package.py --target cpu`
  - 运行 `make check`

---

## 2. Validation Commands

```bash
# 1. 验证 C++ 算法与引擎构建
make native-build

# 2. 运行人脸识别插件专用测试套件
build/native/host/ZhulongFaceRecognitionPluginTests build/native/host/libFaceRecognitionPlugin.so
build/native/host/ZhulongFaceRecognitionOnnxTests build/native/host/libFaceRecognitionPlugin.so

# 3. 运行本地打包与黑盒验包脚本
python3 packages/face_recognition/scripts/build_package.py --target cpu

# 4. 收工前全系统健康检查
make check
```

---

## 3. Rollback Points

- 若双模型内存消耗超预期或解码有缺陷：
  可在 `FaceRecognitionPlugin` 中保留配置开关，通过 `config_json` 降级为单 SCRFD 模式。
- `git checkout -- packages/face_recognition/src/ native/tests/` 随时还原。
