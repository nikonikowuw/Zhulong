# 执行计划：真实人脸人体结构化算法包 (10-15-real-person-face-structure)

## 门禁与前置检查

- [x] 确认工作区纯净，CTest 18/18 基线通过。
- [x] 验证 `ZhulongAlgoRuntime` 与 `GalleryRegistry` 状态。

---

## 阶段一：真实人脸人体检测器实现 (Step 1)

- [x] **1.1 编写检测器组件 (`detector.hpp` / `detector.cpp`)**：
  - 支持从实际图像帧分析人脸区域、关键点与关联人体框；
  - 严格归一化坐标并正确处理全黑/负样本图像；
  - 编写独立单元测试验证检测输出。

---

## 阶段二：人脸姿态对齐与双线性采样 (Step 2)

- [x] **2.1 完善对齐采样器 (`aligner.hpp` / `aligner.cpp`)**：
  - 基于 Umeyama 矩阵对图像进行双线性插值采样；
  - 输出 $112 \times 112$ 标准正脸张量。

---

## 阶段三：特征提取与底库检索闭环 (Step 3)

- [x] **3.1 编写特征抽取器 (`extractor.hpp` / `extractor.cpp`)**：
  - 抽取 512 维特征向量，执行 L2 归一化；
  - 与 `matcher.hpp` 协同完成 1:N 检索。

---

## 阶段四：插件入口整合与 C ABI 交付 (Step 4)

- [x] **4.1 整合插件入口 (`face_recognition.cpp`)**：
  - 串联检测 ➔ 对齐 ➔ 特征抽取 ➔ 底库比对完整流水线；
  - 输出规范的 `zhulong::PersonItem` 与 `zhulong::FaceItem`；
  - 保证 Arena 连续内存分配与 `result_release` 无泄漏。

---

## 阶段五：自动化打包、黑盒验包与全栈门禁 (Step 5)

- [x] **5.1 运行 CTest 单元与集成测试套件**：
  - 验证 `ZhulongFaceRecognitionPluginTests`。
- [x] **5.2 执行 Runner 黑盒准入验包**：
  - `zhulong_algo_runner --verify-plugin ...`。
- [x] **5.3 运行打包脚本与 Go 验包测试**：
  - `python3 packages/face_recognition/scripts/build_package.py`；
  - `python3 native/scripts/build.py go test -race ./internal/algorithm -run TestFaceRecognitionPackageVerification`。
- [x] **5.4 全量门禁校验**：
  - `make check`。
