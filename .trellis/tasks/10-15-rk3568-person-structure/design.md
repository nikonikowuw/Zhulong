# 架构与技术设计：RK3568 真实人体/人脸结构化算法接入 (10-15-rk3568-person-structure)

## 1. 架构总览与链路拓扑

本设计将 `packages/face_recognition` 算法包从原先的启发式/伪代码实现（像素扫描与静态 sin/cos 算法）全面重构为**基于深度学习真实双模型的端到端感知流水线**，并在 Rockchip RK3568 边缘硬件上通过 RKNPU2 与 RGA 硬件加速运行。

```txt
┌─────────────────────────────────────────────────────────────────────────────┐
│ 视频帧 (zhulong_frame_view: BGR24 / NV12 原生分辨率，如 1080p / 4K)          │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
                                       ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│ 阶段一：前处理与尺度适配 (Letterbox / Preprocess)                            │
│  - 原帧等比例缩放并靠左上放置 (保持宽高比)，生成 640×640 RGB UINT8 画布       │
│  - 缩放比：det_scale = min(640.0 / orig_w, 640.0 / orig_h)                  │
│  - NPU 内部 Toolkit2 mean/std 自动归一化：(x - 127.5) / 128.0               │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
                                       ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│ 阶段二：人脸与关键点检测 (SCRFD det_500m on RKNN)                           │
│  - 模型：det_500m_rk3568_i8.rknn (INT8, 927KB, 1.0 TOPS 耗时 ~10ms)          │
│  - 9 个输出分支：stride 8 (80×80), 16 (40×40), 32 (20×20) 各输出 score, bbox, kps│
│  - 解码：根据网格中心 cx, cy 还原检测框及 5 点关键点坐标                     │
│  - 过滤：Score 阈值过滤 (score >= 0.50，注意：输出已含 Sigmoid) + IoU NMS(0.40)│
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
                                       ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│ 阶段三：坐标反算映射回原帧分辨率 (Back-Projection to Original Frame)         │
│  - 消除 Letterbox 缩放影响，将人脸框与 5 点关键点精确反算回原始高清帧像素坐标：│
│    orig_box = det_box / det_scale;  orig_kps = det_kps / det_scale;         │
│  - 同时推导原帧下的人体框 body_box 并记录 track_id                           │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
                                       ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│ 阶段四：原帧外扩裁剪与局部缓存 (Margin Crop on Original High-Res Frame)       │
│  - ⚠️ 关键性能与精度优化：避免在 640×640 低清图上抠脸导致二次模糊失真！     │
│  - 在原帧上对人脸框向外扩展 25%~30% Margin (包含额头与下巴完整轮廓) 并 clamp:│
│    crop_x1 = max(0, orig_x1 - 0.25 * w), crop_y1 = max(0, orig_y1 - 0.25 * h)│
│    crop_x2 = min(orig_w, orig_x2 + 0.25 * w), crop_y2 = min(orig_h, ...)   │
│  - 将此 ROI 提取为连续小图像块（~150x150，完全驻留 A55 L1/L2 Cache）        │
│  - 关键点平移至局部坐标：local_kps = orig_kps - (crop_x1, crop_y1)          │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
                                       ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│ 阶段五：局部高质量姿态对齐 (Umeyama Alignment on Local High-Res ROI)         │
│  - 在局部 Patch 上利用 Umeyama 算法计算至 ArcFace 112×112 模板的相似仿射矩阵；│
│  - 双线性插值重采样生成极其清晰的 112×112 RGB 人脸图像 Patch                 │
│  - Cache 极其友好：消除了跨数千字节 stride 全画幅寻址引发的 DDR/L2 颠簸！    │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
                                       ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│ 阶段六：特征提取与 1:N 底库比对 (FaceLiVTv2 on RKNN & Matcher)              │
│  - 模型：facelivtv2-l.rknn (18MB, 512 维, 1.0 TOPS 耗时 ~25ms)               │
│  - 输入高清 112×112 RGB，NPU 内部自动完成 (x - 127.5) / 127.5 归一化        │
│  - 输出 512 维特征向量，执行严格 L2 范数单位化                               │
│  - 消费 GalleryRegistry 快照，执行余弦相似度比对，判定 matched、person_id、 │
│    person_name 与 Top-K 候选人 (candidates)                                 │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
                                       ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│ 阶段七：单块连续 Arena 内存打包与 C ABI 输出 (Result Packing)                │
│  - 纯 C ABI 物理隔离 (zhulong_plugin_api_v1, ResultType == 3)               │
│  - 所有结果统一归一化为 [0.0, 1.0] 浮点坐标                                  │
│  - 单块连续 Arena 打包 (zhulong_person_item[] + zhulong_face_item[])        │
│  - internal_token 指向 Arena 首地址，result_release 通过单次 std::free 释放 │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 2. 核心模块与职责划分

| 模块 | 源码文件 | 核心职责 |
| --- | --- | --- |
| **插件门面** | `packages/face_recognition/src/face_recognition.cpp` | 插件生命周期管理（`init`, `create_session`, `detect_persons`），持有检测与识别双 Model 实例，协调多会话派生 |
| **检测引擎** | `packages/face_recognition/src/detector.hpp`, `detector.cpp` | SCRFD 检测模型前处理（Letterbox）、前向推理、9 个输出分支锚点解码、NMS 与人体框推导 |
| **对齐引擎** | `packages/face_recognition/src/aligner.hpp`, `aligner.cpp` | Umeyama 仿射矩阵求解、ArcFace 112×112 模板对齐重采样 |
| **特征引擎** | `packages/face_recognition/src/extractor.hpp`, `extractor.cpp` | FaceLiVTv2 特征模型前向推理、512 维向量提取与 L2 归一化 |
| **匹配引擎** | `packages/face_recognition/src/matcher.hpp`, `matcher.cpp` | `GalleryRegistry` 底库快照检索、余弦相似度计算与 Top-K 候选人排序 |
| **打包脚本** | `packages/face_recognition/scripts/build_package.py` | 组装 `.so`、`manifest.json`、`sample.jpg` 与 `models/` 权重为 `.tar.gz` |

---

## 3. 详细数据流与模型契约

### 3.1 人脸检测模型 (SCRFD-500M)
- **文件**：`models/det_500m.rknn`（来源：`/home/niko/dev/rknn_model_zoo/examples/insightface/det_500m/model/det_500m_rk3568_i8.rknn`）
- **输入**：`[1, 640, 640, 3]` UINT8 RGB
- **输出**：9 个张量（按 stride 8, 16, 32 划分）：
  - Stride 8 (80×80×2=12800 anchors)：`score [12800, 1]`, `bbox [12800, 4]`, `kps [12800, 10]`
  - Stride 16 (40×40×2=3200 anchors)：`score [3200, 1]`, `bbox [3200, 4]`, `kps [3200, 10]`
  - Stride 32 (20×20×2=800 anchors)：`score [800, 1]`, `bbox [800, 4]`, `kps [800, 10]`
- **前处理**：保持比例居左上摆放（Letterbox），右侧/下侧补 0，缩放系数 `det_scale = min(640.0 / w, 640.0 / h)`。
- **解码数学公式**：
  $$cx = w \times stride, \quad cy = h \times stride$$
  $$x_1 = (cx - bbox_0 \times stride) / det\_scale / orig\_w$$
  $$y_1 = (cy - bbox_1 \times stride) / det\_scale / orig\_h$$
  $$x_2 = (cx + bbox_2 \times stride) / det\_scale / orig\_w$$
  $$y_2 = (cy + bbox_3 \times stride) / det\_scale / orig\_h$$
  $$kps_{k,x} = (cx + kps_{2k} \times stride) / det\_scale / orig\_w$$
  $$kps_{k,y} = (cy + kps_{2k+1} \times stride) / det\_scale / orig\_h$$
- **⚠️ 重点**：`score` 输出在图内已执行 Sigmoid，值域直接为 $[0, 1]$，严禁二次计算 Sigmoid；默认过滤阈值 0.50，NMS IoU 阈值 0.40。

### 3.2 原帧外扩裁剪与人脸姿态对齐 (Margin Crop & Umeyama Alignment)
- **原帧映射与外扩**：
  从 640×640 检测层获得的框与 5 点坐标除以 `det_scale` 恢复为原帧像素坐标。在原帧上将人脸框向四周外扩 25%：
  $$crop\_x_1 = \max(0, orig\_x_1 - 0.25 \times w), \quad crop\_y_1 = \max(0, orig\_y_1 - 0.25 \times h)$$
  $$crop\_x_2 = \min(orig\_w, orig\_x_2 + 0.25 \times w), \quad crop\_y_2 = \min(orig\_h, orig\_y_2 + 0.25 \times h)$$
  将该局部 ROI 连续复制到局部工作区，关键点相应偏移到局部坐标系：$kps_{local} = kps_{orig} - (crop\_x_1, crop\_y_1)$。
- **模板**：ArcFace 5 点 112×112 标准正脸参考坐标：
  `左眼: [38.2946, 51.6963], 右眼: [73.5318, 51.5014], 鼻尖: [56.0252, 71.7366], 左嘴角: [41.5493, 92.3655], 右嘴角: [70.7299, 92.2041]`
- **双线性插值**：在局部高分辨率 ROI 上计算仿射逆矩阵，重采样生成高保真 `[112, 112, 3]` RGB888 连续字节缓存，杜绝模糊与大分辨率全帧 Cache 颠簸。

### 3.3 特征提取模型 (FaceLiVTv2-L)
- **文件**：`models/facelivtv2-l.rknn`（来源：`/home/niko/dev/rknn_model_zoo/examples/facelivt/facelivtv2_l/model/facelivtv2-l.rknn`）
- **输入**：`[1, 112, 112, 3]` UINT8 RGB
- **输出**：`[1, 512]` FLOAT32 特征向量
- **后处理**：
  $$norm = \sqrt{\sum_{i=0}^{511} v_i^2}, \quad \hat{v}_i = \frac{v_i}{\max(norm, 10^{-6})}$$
- **1:N 比对**：
  $$similarity = \sum_{i=0}^{511} \hat{v}_i \cdot \hat{g}_i$$
  若 $similarity \ge threshold$（默认 0.40），判定 `matched = true`，填充最高相似度底库人员姓名与编号，并记录 Top-K 候选人。

---

## 4. 多会话与资源管理设计

针对 `algo.hpp` 约定单个 `PluginClass` 派生会话机制，本设计采用双 Model + Worker 会话容器方案：
1. `FaceRecognitionPlugin` 内部拥有：
   - `zhulong::Model det_model_`: 持有检测模型静态只读权重；
   - `zhulong::Model rec_model_`: 持有识别模型静态只读权重；
   - `std::unordered_map<uint32_t, zhulong::Session> rec_sessions_`: 为每个 `worker_id` 维护专属特征提取会话。
2. 在 `create_session(worker_id)` 执行时：
   - 派生检测会话：`auto det_session = det_model_.create_session(worker_id);`
   - 派生特征会话：`rec_sessions_[worker_id] = rec_model_.create_session(worker_id);`
   - 返回 `det_session` 作为主会话句柄。
3. `Session` 在 `algo.hpp` 中新增 `worker_id()` 访问器（`return ctx_ ? ctx_->worker_id : 0;`），使 `detect_persons` 能以 $O(1)$ 索引到对应的 `rec_session`，实现多线程 Worker 零锁安全并发。

---

## 5. 开发机与 CI 回归兼容性（Host CPU Fallback）

- 开发机与 CI 跑在 x86_64 CPU 上，链接 `CpuInferenceBackend` 与 `CpuVisionBackend`；
- 当 `det_model_.load()` 或 `rec_model_.load()` 处于 CPU 后端时：
  - `CpuInferenceBackend` 返回确定性 Mock 权重与模拟张量；
  - 检测器和特征提取器检测到后端非 RKNN 时，输出合法稳定的确定性回归结果；
  - 保证本地 `make check`、`ZhulongFaceRecognitionPluginTests` 以及 Go 宿主验包测试 `TestFaceRecognitionPackageVerification` 100% 稳定通过，无环境硬编码阻碍。

---

## 6. 内存所有权与 C ABI 边界

- 遵照平台 C ABI（`ResultType == 3`）：
  - 顶层输出：`zhulong_person_item` 数组；
  - 每个 PersonItem 挂载关联的 `zhulong_face_item` 数组；
  - 每个 FaceItem 挂载 `zhulong_face_candidate` 数组；
  - 所有数据结构在单个连续的 **Arena 内存块**（`malloc`）中扁平序列化；
  - `result->internal_token` 记录 Arena 首指针，通过 `zhulong_plugin_api_v1.result_release` 单次 `std::free` 完成全生命周期释放，零内存泄露。

## 7. 极致零拷贝设计 (Maximum Zero-Copy & Low-Latency Architecture)

为在 RK3568（1.0 TOPS，DDR 带宽与 A55 Cache 资源紧张）上达到最佳吞吐与最低端到端延迟，本设计在全管线落实 **7 重零拷贝/零堆重分配** 机制：

| 链路环节 | 常规低效实现（多重拷贝） | 本方案极致零拷贝实现 (Zero-Copy) | 性能收益 |
| --- | --- | --- | --- |
| **1. 视频帧接收** | 将 FrameBroker 共享内存拷贝到私有 buffer | `zhulong_frame_view` 原地只读借用，零像素拷贝 | 节省全帧（6~24MB）DDR 搬移 |
| **2. 检测前处理** | 缩放 ➔ 裁剪 ➔ 贴入 Letterbox 临时图 ➔ 拷入 Tensor | RGA/CPU 直接在检测输入 Tensor 目标 ROI 上计算并填充边框，**直写目标 Tensor** | 消除中间 640×640 临时图分配 |
| **3. 后处理解码** | 动态 `std::vector` 动态扩容 `push_back` | Worker/栈上预分配固定容量静态工作区（`DET_500M_MAX_FACES`），零堆分配 | 消除推理热路径动态内存管理与锁开销 |
| **4. 姿态对齐与采样** | 原帧 ➔ 裁剪外扩小图 ➔ 双线性重采样 ➔ 拷入特征 Tensor | 结合坐标逆映射，**直接从原帧双线性重采样直写特征输入 Tensor (112×112)** | **消灭裁剪临时图与对齐临时图，直写 NPU 缓冲** |
| **5. 特征归一化** | 拷贝至新 vector 做 L2 归一化 | 模型输出 512 维 Float 张量**原地 (In-Place) 计算与归一化** | 零临时向量分配 |
| **6. 1:N 底库比对** | 每次比对拷贝底库数据结构 | `GalleryRegistry::snapshot()` **只读无锁借用快照指针** | 零底库内存拷贝，多线程零锁竞争 |
| **7. 结果输出打包** | 多级 STL 嵌套容器深拷贝转换 | 一次性 `malloc` 计算精确尺寸的连续 Arena 块，直接序列化 C 结构体 | 杜绝树状碎片分配，释放仅需单次 `std::free` |
