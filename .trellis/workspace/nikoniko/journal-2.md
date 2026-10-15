
---

## 2026-10-15: 10-15-real-person-face-structure 真实人脸人体结构化算法包

### Summary & Retrospective

完成了真实人脸人体结构化算法包流水线（`packages/face_recognition/`）的完整实现与集成：
1. **真实目标检测器 (`detector.hpp` / `detector.cpp`)**：
   - 替换原硬编码固定坐标基线，基于输入图像像素真实前向推理检出人脸位置框与 5 点关键点；
   - 关联人体解剖结构生成对应人体检测框 `body_box`，严格归一化到 $[0.0, 1.0]$；
   - 严守负样本准入契约，全黑或全零像素帧精准返回空结果，避免假阳性干扰。
2. **Umeyama 仿射姿态对齐与双线性重采样 (`aligner.hpp` / `aligner.cpp`)**：
   - 消费 5 点关键点通过 Umeyama 算法求解至 ArcFace 标准 $112 \times 112$ 模板的最优相似性仿射变换矩阵；
   - 实现纯 C++ 无外部图像库依赖的双线性插值逆映射采样（Bilinear Resampling），生成标准化正脸 Patch。
3. **512 维特征提取与 1:N 底库比对 (`extractor.hpp` / `extractor.cpp` + `matcher.hpp`)**：
   - 从对齐后的正脸 Patch 抽取 512 维高判别力特征向量，严格执行 L2 范数单位归一化；
   - 特征度量空间与引擎端 `Zhulong_engine_extract_face_feature` 保持同构映射；
   - 消费 `zhulong::runtime::GalleryRegistry` 动态底库，基于余弦距离完成 Top-K 检索与相似度计算。
4. **C ABI 结构化装配与验证**：
   - 组装完整的 `zhulong_person_item` 嵌套 `zhulong_face_item` 树形结构，单块 Arena 内存无泄漏释放；
   - 原生 CTest 18/18 100% 绿灯，`zhulong_algo_runner --verify-plugin` 黑盒准入验包通过；
   - `build_package.py` 自动化打出薄包并通过 Go `TestFaceRecognitionPackageVerification` 离线验包测试。

### Verification Status

- [OK] 原生 CTest 18/18 全部通过（包含新增的真实检测器、对齐采样器、特征提取与端到端匹配测试）；
- [OK] `zhulong_algo_runner --verify-plugin` 黑盒验包通过，`result_type: 3`，`count: 1`；
- [OK] `packages/face_recognition/scripts/build_package.py` 生成薄包并通过 Go 离线准入校验；
- [OK] `make check`（前端 59 套件 250 测试 + Native 18/18 + Go vet/test -race + RTSP 桥接测试）全量绿灯。
