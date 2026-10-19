# Design: 人脸识别插件集成 SCRFD 人体检测双模型

## 1. 架构与边界

在 `packages/face_recognition` 算法插件内部构建“双检测器协同 + 单特征抽取”的 C++ 级流水线：

```txt
                              输入视频帧 (zhulong::Image)
                                        │
           ┌────────────────────────────┴────────────────────────────┐
           ▼                                                         ▼
【PersonDetector (scrfd_person_2.5g)】              【FaceDetector (det_500m)】
  - 15 头输出 (Strides: 8,16,32,64,128)               - 9 头输出 (Strides: 8,16,32)
  - 1 Anchor/grid                                     - 2 Anchors/grid
  - 输出: PersonBodyDetection                         - 输出: FaceDetection
    (body_box, body_score)                              (face_box, 5-landmarks, face_score)
           │                                                         │
           └────────────────────────────┬────────────────────────────┘
                                        ▼
                  【SpatialAssociation (空间几何包含匹配)】
                    - 人脸中心落在人体上半身 (y <= y_top + 0.40 * h)
                    - 人脸在人体内重叠比 IoF >= 0.70
                    - 关联成功: Face 挂载进对应 Person 的 faces 列表
                    - 背身/无脸人体: faces 为空，输出独立 PersonItem
                    - 孤立人脸: 兜底输出 body_box = face_box 的 PersonItem
                                        │
                                        ▼
                  【Aligner + Extractor + Matcher】
                    (仅对挂载的人脸执行)
                    - Umeyama 仿射变换 -> 112x112 Direct Align
                    - FaceLiVTv2-L 抽取 512 维向量 (L2 归一化)
                    - GalleryRegistry 1:N 余弦比对与 Top-K 筛选
                                        │
                                        ▼
                 返回 std::vector<zhulong::PersonItem>
```

---

## 2. 模块详细设计

### 2.1 PersonDetector (SCRFD Person 2.5G 解码器)
- **输入契约**：$640 \times 640$ RGB24 图像（共享 Letterbox 预处理结果），归一化 $(x - 127.5)/128$。
- **输出张量**：15 个 Tensor（5 尺度 $\times$ 3 类型）：
  - 尺度：$S \in \{8, 16, 32, 64, 128\}$
  - $0 \sim 4$：`score`，维度 $[1, 1, 640/S, 640/S]$
  - $5 \sim 9$：`bbox`，维度 $[1, 4, 640/S, 640/S]$（按行排布 $dx_1, dy_1, dx_2, dy_2$）
  - $10 \sim 14$：`kps`，维度 $[1, 10, 640/S, 640/S]$（可见身体区域角点）
- **解码算法**：
  $$\begin{aligned}
  cx &= w \cdot S, \quad cy = h \cdot S \\
  px_1 &= (cx - bbox[0] \cdot S - pad_{left}) / scale \\
  py_1 &= (cy - bbox[1] \cdot S - pad_{top}) / scale \\
  px_2 &= (cx + bbox[2] \cdot S - pad_{left}) / scale \\
  py_2 &= (cy + bbox[3] \cdot S - pad_{top}) / scale
  \end{aligned}$$
  通过 `clamp` 约束在 $[0, width] \times [0, height]$，并归一化到 $[0.0, 1.0]$。
- **NMS 抑制**：IoU 阈值 $0.40$，分数阈值 $0.50$。

### 2.2 空间几何关联算法 (Spatial Association)
对检出的 $M$ 个人体框 $B_i$ 与 $N$ 个人脸框 $F_j$：
1. **几何约束**：
   - 人脸中心点 $C_j = (fx_{mid}, fy_{mid})$ 必须满足：
     $$bx_{1,i} \le fx_{mid} \le bx_{2,i} \quad \text{且} \quad by_{1,i} \le fy_{mid} \le by_{1,i} + 0.40 \cdot (by_{2,i} - by_{1,i})$$
   - 人脸包含度 $\text{IoF}(F_j, B_i) = \frac{\text{Area}(F_j \cap B_i)}{\text{Area}(F_j)} \ge 0.70$。
2. **贪心/最优匹配**：
   - 当多个人体满足条件时，选择中心点纵向距离顶端最近（头部重合度最高）的人体。
   - 匹配成功的 Face 挂载至 $B_i$ 的 `faces` 数组，并标记该 Face 已匹配。
3. **退化与兜底分支**：
   - 若 $B_i$ 未匹配到任何 Face：生成 `PersonItem`，`faces = {}`，`body_score = B_i.score`（支持背身/遮挡）。
   - 若 $F_j$ 未能落入任何人柱（近景特写或极端姿态）：生成 `PersonItem`，`body_box = face_box`，`faces = {F_j}`（防止漏掉身份信息）。

### 2.3 生命周期与会话管理
- `FaceRecognitionPlugin` 持有：
  - `zhulong::Model person_model_`
  - `zhulong::Model det_model_`
  - `zhulong::Model rec_model_`
- `create_session(worker_id)`：
  - 并发保护下为每个 Worker 创建并保存各自独立的 `person_session` 与 `rec_session`。
  - 返回 `det_session` 作为主 Worker 会话凭证。
- `FaceExtractorContext`（用于底库照片录入）：
  - 保持单例，内部**不加载** `person_model_`，仅加载 `det_model_` 与 `rec_model_`，启动速度与内存占用保持原状。

---

## 3. 资产与构建配置

- **CPU 模型**：`packages/face_recognition/targets/cpu/models/scrfd_person_2.5g.onnx`
- **RK3568 模型**：`packages/face_recognition/targets/rk3568/models/scrfd_person_2.5g.rknn`
- **打包配置**：
  - `targets/cpu/manifest.json`：更新计算消耗估值（`compute_cost: 35 -> 40`，`memory_mb: 128 -> 160`）
  - `targets/rk3568/manifest.json`：更新计算消耗估值（`compute_cost: 35 -> 45`，`memory_mb: 64 -> 80`）
  - `build_package.py`：自动同步复制 `models/` 下所有模型。

---

## 4. 回归与验证方案

1. **单测套件扩展**：
   - `ZhulongFaceRecognitionPluginTests`：验证无模型桩状态下的平滑运行。
   - `ZhulongFaceRecognitionOnnxTests`：使用 ONNX 运行时执行真实图像全链路测试（包含人体和人脸的真实图片）。
2. **场景用例覆盖**：
   - 典型场景：既有人体又有人脸 -> 输出关联完整的 `PersonItem`；
   - 背身场景：仅有人体无正脸 -> 输出 `faces` 为空的 `PersonItem`；
   - 特写场景：仅有人脸无身体 -> 输出 `body_box = face_box` 的 `PersonItem`。
