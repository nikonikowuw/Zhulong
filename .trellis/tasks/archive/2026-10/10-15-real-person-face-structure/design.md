# 技术设计：真实人脸人体结构化算法包 (10-15-real-person-face-structure)

## 1. 架构目标与模块切片

在 `packages/face_recognition/` 下实现工业级三阶段结构化流水线：

```txt
packages/face_recognition/src/
├── face_recognition.cpp       # 插件 C ABI 导出入口 (适配器适配与生命周期管理)
├── detector.hpp / .cpp        # 人脸检测与 5 点关键点定位 + 人体空间关联
├── aligner.hpp / .cpp         # Umeyama 相似性仿射变换与 112×112 人脸重采样
├── extractor.hpp / .cpp       # 512 维特征抽取器 (L2 范数单位归一化)
└── matcher.hpp / .cpp         # 1:N 底库 Cosine 相似度检索与 Top-K 排序
```

---

## 2. 核心流水线设计

### 阶段一：目标检测与关键点 (Detection & Landmarks)
1. **输入借用与空帧检测**：
   - 消费 `zhulong::Image`（只读视图）；
   - 全黑负样本（像素和全为 0）直接返回空结果集，严守黑盒准入测试负样本契约。
2. **人脸检测与关键点定位**：
   - 从图像实际空间像素分布中提取人脸位置框 `[x1, y1, x2, y2]` 与检测置信度 `face_score`；
   - 提取 5 点关键点：左眼、右眼、鼻尖、左嘴角、右嘴角，全部归一化至 $[0.0, 1.0]$；
   - 关联人体框 `body_box`：结合人体人体解剖学几何模型（头部约占人体整体高度 1/6 ~ 1/7，宽度约 1/2.5），在未接入专用行人检测器前，自适应外扩并约束在图像边界内生成对应人体框；当接入行人检测器时通过 IoU 空间匹配。

### 阶段二：人脸姿态对齐 (Umeyama Alignment)
1. **参考坐标系**：
   - 采用 ArcFace 标准 $112 \times 112$ 参考关键点坐标：
     - 左眼：$(38.2946, 51.6963)$
     - 右眼：$(73.5318, 51.5014)$
     - 鼻尖：$(56.0252, 71.7366)$
     - 左嘴角：$(41.5493, 92.3655)$
     - 右嘴角：$(70.7299, 92.2041)$
2. **仿射变换矩阵**：
   - 借助 `aligner.hpp` 现有的 Umeyama 算法，求解最佳相似性变换矩阵 $M \in \mathbb{R}^{2 \times 3}$；
   - 对原始图像人脸执行双线性插值采样，输出 $112 \times 112 \times 3$ 正脸 patch。

### 阶段三：特征提取与底库比对 (Feature & Matching)
1. **特征提取**：
   - 从对齐后的正脸 patch 抽取 512 维稠密特征向量；
   - 特征向量与引擎侧 `Zhulong_engine_extract_face_feature` 保持同构特征空间，确保注册库特征与在线探针特征互通；
   - 严格进行 L2 范数单位归一化：$\vec{f} = \vec{f} / \|\vec{f}\|_2$。
2. **1:N 底库检索**：
   - 消费 `zhulong::runtime::GalleryRegistry::instance().snapshot()`；
   - 计算探针特征与所有底库人脸的余弦相似度；
   - 排序并筛选出置信度高于阈值（默认 0.60）的命中结果，输出 Top-K 候选人。

---

## 3. ABI 契约与内存安全

- **任务类型**：`task_type = "person_structure"`（类型 3）；
- **内存所有权**：
  - 插件在 `process` 中通过 `Single-Block Arena` 分配整棵结果树；
  - 宿主消费完毕后调用 `result_release`，插件单次释放，零嵌套泄漏；
- **异常防护**：
  - 插件内部所有 C++ 逻辑包裹在防爆门中，异常不穿越动态库 ABI 边界。
