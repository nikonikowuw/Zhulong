# PRD: 人脸识别插件集成 SCRFD 人体检测双模型

## Goal

将 InsightFace `scrfd_person_2.5g` 作为真实人体检测模型集成进 `packages/face_recognition` 算法插件内部，与现有的 `det_500m` 人脸检测模型形成 C++ 内存级双模型协同流水线。
彻底终结通过人脸几何比例“脑补”人体框的缺陷，实现真正的“人体检测 + 人脸 5 点对齐 + 512 维特征提取与底库检索”人员结构化输出（`PersonItem`）。

---

## Background & 已确认的仓库事实

| 编号 | 事实 | 证据 |
| :--- | :--- | :--- |
| F1 | 当前 `FaceRecognitionPlugin` 使用 `det_500m` 检出人脸后，通过解剖学比例几何外推估算人体框 `body_box`，当人员背身、侧脸、低头或远距离时人脸漏检会导致整个人体完全漏检 | `packages/face_recognition/src/detector.cpp:245-257` |
| F2 | 本地存在已验证且量化就绪的模型权重：`/home/niko/dev/rknn_model_zoo/examples/scrfd_person_2.5g/model/`，包含 `scrfd_person_2.5g.onnx` (3.6MB) 与 `scrfd_person_2.5g_rk3568_i8.rknn` (1.3MB) | 本地文件系统已确认 |
| F3 | `scrfd_person_2.5g` 属于 InsightFace SCRFD 家族，输入为 $640 \times 640$ RGB，归一化为 $(x - 127.5)/128$，其 5 个关键点为**可见身体框（visible body）角点**，输出 15 个头（5 尺度 $\times$ {score, bbox, visible_kps}），非人脸关键点 | `scrfd_person_2.5g/cpp/scrfd_person_2.5g.h:50` |
| F4 | 插件对外导出为 `ZHULONG_REGISTER_PERSON_STRUCTURE_PLUGIN`，入口签名 `std::vector<zhulong::PersonItem> detect_persons(...)` 原生支持 1 个人体挂载 $0 \sim N$ 张人脸 | `native/sdk/include/zhulong/algo.hpp:147-160, 795` |
| F5 | Go 宿主层 `internal/record/service.go:processPersonStructure` 天然支持 `len(p.Faces) == 0` 的情况（此时仅生成人员抓拍 `Capture`，不生成人脸识别 `Recognition`） | `internal/record/service.go:55-155` |
| F6 | 底库录入与单张图片特征提取（`zhulong_face_extract_feature`）仅需人脸检测与特征模型，不需要人体检测介入 | `packages/face_recognition/src/face_recognition.cpp:330-450` |

---

## Requirements

### R1. 模型资产集成与寻址支持
- 将 `scrfd_person_2.5g.onnx` 集成至 `packages/face_recognition/targets/cpu/models/`。
- 将 `scrfd_person_2.5g_rk3568_i8.rknn` 重命名并集成至 `packages/face_recognition/targets/rk3568/models/scrfd_person_2.5g.rknn`。
- 升级 `resolve_model_path` 与 `load_model_preferred`，支持按当前平台动态加载 `models/scrfd_person_2.5g`。
- 更新 CPU 与 RK3568 的 `manifest.json` 与 `build_package.py` 资产打包列表。

### R2. C++ 插件双模型前向与 15 头 SCRFD 人体解码
- 在 `FaceRecognitionPlugin` 中新增 `person_model_` 模型实例与每 Worker 会话映射。
- 在 `FaceDetector` 内部新增 SCRFD Person 解码器：
  - 适配 5 个 FPN Stride（`8, 16, 32, 64, 128`），每个尺度 1 个 Anchor；
  - 正确解析 15 个输出张量（5 score + 5 bbox + 5 kps）；
  - 逆映射 Letterbox 缩放与补边，还原原图真实像素坐标与归一化浮点坐标；
  - 执行 NMS 过滤抑制重叠人体框。

### R3. 内存级人体与人脸空间几何挂载 (Spatial Association)
- 人体框采用 `scrfd_person_2.5g` 预测出的 `box`（全身预测框），保证安防长宽比稳定，人脸严格对应人体上部先验。
- 在单次 `detect_persons` 调用内：
  1. 调用 `person_model_` 检出所有人体框候选 `BodyBox`；
  2. 调用 `det_model_` 检出所有人脸框及 5 点关键点；
  3. 执行空间包含度匹配：
     - 若人脸中心落入某人体框的上半身区域（$y \le y_{top} + 0.40 \cdot h$）且人脸区域落在人体内的重叠比例高于阈值，则将该人脸绑定至该人体；
     - 允许多张人脸挂载（如果出现拥挤重叠）或 1 对 1 贪心最优匹配；
     - **背身/无脸人体**：保留 `PersonItem`，`faces` 为空，置信度取人体得分，`track_id` 保留；
     - **孤立人脸（大头特写未检出人体）**：兜底生成 `body_box = face_box` 的 `PersonItem`，保证人脸比对不丢失。
  4. 对每个挂载的人脸项执行 Umeyama 仿射对齐、FaceLiVTv2-L 512 维特征抽取以及底库余弦检索。

### R4. 性能、内存与零拷贝保障
- 复用现有 `image` 的输入张量缓存机制，人体检测与人脸检测共用同一份 RGB24 / DMA-BUF 预处理输出，避免双份前处理开销。
- 确保 CPU fallback 桩路径在模型缺失或单测环境下能够平滑退化，不抛异常不崩溃。
- 单张人脸提取接口 `zhulong_face_extract_feature` 保持轻量，不加载人体检测模型。

---

## Acceptance Criteria

- [x] **AC1**: `packages/face_recognition` 可通过 `build_package.py` 分别针对 `cpu` 和 `rk3568` 打包成功，包内包含人脸与人体模型资产。
- [x] **AC2**: 插件测试覆盖双模型协同链路：真实图片中能同时检出真实人体框与人脸 5 点关键点，并在 `PersonItem` 中正确关联。
- [x] **AC3**: 背身/遮挡测试：在只有人体无清晰人脸的图像中，系统能够输出 `body_score > 0` 且 `faces` 为空的 `PersonItem`，解决背身漏报问题。
- [x] **AC4**: 现有单张人脸提取接口 `zhulong_face_extract_feature` 单元测试通过，特征提取与 L2 归一化行为不受影响。
- [x] **AC5**: `make native-test` 与 `make check` 全绿，无内存泄漏与数据竞争。
