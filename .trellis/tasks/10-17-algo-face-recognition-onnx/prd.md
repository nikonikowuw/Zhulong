# PRD: 接入 ONNX 版本人脸识别算法包

## Goal

基于已实现的 ONNX Runtime 推理后端（`OnnxInferenceBackend`，编译宏 `ZHULONG_INFERENCE_ONNX`），使人脸结构化算法插件（`packages/face_recognition`）具备完整加载和运行 ONNX 格式的人脸检测模型（SCRFD `det_500m.onnx`）与人脸特征提取模型（`facelivtv2-l.onnx`）的能力，完成 detect → align → embed → 1:N 检索完整链路，为 CPU 与通用 Linux 环境交付高性能、高保真的人脸识别算法能力。

## 架构定位与上下文

- **当前基础**：
  - `native/src/backends/onnx/` 已实现 `IInferenceBackend` 门面（`OnnxInferenceBackend`），具备模型加载、Worker 独占 `SessionContext` 隔离及零拷贝张量前向推理。
  - `packages/face_recognition/src/` 已有完整算法插件骨架（`FaceDetector`, `Aligner`, `FaceFeatureExtractor`, `Matcher`），目前仅深度打通了 RKNN NPU 路径和 CPU 几何 Mock 桩。
- **本次目标**：打通真实的 ONNX 模型数据流与预处理/后处理，实现 CPU 目标平台下的真实推理与底库匹配。

## Requirements

### R1. 真实 ONNX 模型加载与解析
- `FaceRecognitionPlugin::init` 能够定位并正确加载 ONNX 模型：
  - 检测模型：`models/det_500m.onnx`
  - 识别模型：`models/facelivtv2-l.onnx`
- 支持模型寻址（相对 CWD、动态库所在目录 `models/` 目录）；加载失败时安全报错或回退，不抛出任何未捕获异常。

### R2. 预处理与张量适配（NCHW + Float32）
- **检测前处理**：
  - 输入尺寸：640×640，通道顺序 RGB；
  - 张量形状：`[1, 3, 640, 640]`，数据类型 `DataType::FLOAT32`；
  - 归一化公式：`val = (pixel - 127.5f) / 128.0f`（标准 SCRFD 契约）；
  - 保持与现有 RKNN NHWC uint8 分流共存。
- **特征提取前处理**：
  - Umeyama 仿射对齐输出裁剪为人脸 Patch：112×112，通道顺序 RGB；
  - 张量形状：`[1, 3, 112, 112]`，数据类型 `DataType::FLOAT32`；
  - 归一化公式：`val = (pixel - 127.5f) / 127.5f`（FaceLiVTv2 / ArcFace 契约）。

### R3. SCRFD 9 头输出解码保真
- 支持解析 3 个 feature stride（8, 16, 32）对应的 9 个输出节点：
  - score: `[12800, 1]`, `[3200, 1]`, `[800, 1]`
  - bbox: `[12800, 4]`, `[3200, 4]`, `[800, 4]`
  - kps: `[12800, 10]`, `[3200, 10]`, `[800, 10]`
- ⚠️ **严禁对 score 进行二次 Sigmoid**（ONNX 模型图内已包含 Sigmoid 算子）；
- 检出的人脸框与 5 点关键点严格归一化到 `[0.0, 1.0]` 相对坐标。

### R4. 512 维特征提取与 L2 范数归一化
- 特征模型输出固定为 `[1, 512]` FLOAT32 张量；
- 原地执行单位 L2 范数归一化：`norm = sqrt(sum(v_i^2))`, `v_i = v_i / norm`；
- 将归一化后的 probe 特征送入 `GalleryRegistry` 快照进行 1:N 余弦相似度比对，输出命中人脸标识 `matched_face_id`、人员 ID `person_id`、相似度 `similarity` 与候选集。

### R5. 算法包规范与构建集成
- 算法包 manifest：在 `packages/face_recognition/targets/cpu/manifest.json` 中声明 CPU/ONNX 资源规格；
- CMake 构建配置：支持在 `--inference-backend onnx` 配置下编译产出 `libFaceRecognitionPlugin.so`；
- 保证构建产物动态库依赖隔离，RPATH 为空，不外溢 ORT 专有头文件。

### R6. 单元测试与内存安全保障
- 单元测试验证：覆盖模型加载、张量预处理、前向推理、特征比对全流程；
- 内存与异常安全：在 ASan/UBSan 下运行无内存泄漏、越界访问；所有插件导出 API 严格保证异常不逃逸出 C ABI。

## Acceptance Criteria

- [ ] `packages/face_recognition` 在 `ZHULONG_INFERENCE_ONNX` 编译宏下能够正确编译并通过链接。
- [ ] 人脸检测输入预处理正确生成 `[1, 3, 640, 640]` FLOAT32 NCHW 张量，归一化严格遵循 `(x-127.5)/128.0`。
- [ ] SCRFD 9 头输出解码逻辑适配 ONNX 输出张量布局，不重复进行 Sigmoid，检出框坐标归一化到 `[0.0, 1.0]`。
- [ ] 人脸对齐生成的 112×112 Patch 正确转换为 `[1, 3, 112, 112]` FLOAT32 NCHW 张量，归一化严格遵循 `(x-127.5)/127.5`。
- [ ] 特征提取模型输出 512 维向量，并通过单位 L2 范数归一化与底库完成 1:N 检索。
- [ ] 具备配套 Native 单元测试覆盖 ONNX 识别全链路，通过 AddressSanitizer/UndefinedSanitizer 审计无内存泄漏与越界。
