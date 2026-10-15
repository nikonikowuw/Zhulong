# 真实人脸人体结构化算法包 (10-15-real-person-face-structure)

## Goal

在 `packages/face_recognition/` 中实现真实的「人、脸结构化算法」流水线，替换原先的静态跟踪弹基线（Tracer Bullet）：实现真实的人脸/人体目标检测、5 点关键点定位、Umeyama 人脸姿态仿射对齐、512 维高判别力特征提取与 1:N 底库比对，输出符合平台纯 C ABI 标准的完整 `person_structure` 数据结构。

## Requirements

- **R1 真实目标检测与 5 点关键点回归 (Detection & Landmarks)**：
  - 替换插件内硬编码的静态坐标与关键点；
  - 基于真实图像像素前向推理，检出画面中多目标的人脸框 `face_box` 与 5 点关键点（双眼、鼻尖、双嘴角）；
  - 支持人体框 `body_box` 关联（将人脸与其所在的人体空间区域关联绑定）；
  - 目标坐标与关键点严格按图像宽高归一化至 $[0.0, 1.0]$；
  - 严守负样本准入契约：全黑或纯噪声图像不得产生假阳性误检目标。
- **R2 真实人脸姿态对齐 (Umeyama Alignment)**：
  - 消费检测阶段给出的真实 5 点关键点；
  - 运用 Umeyama 算法计算至 ArcFace 112×112 参考人脸模板的相似性仿射变换矩阵，矫正侧脸与倾斜姿态；
  - 输出 112×112 归一化正脸人脸张量。
- **R3 真实 512 维特征抽取与动态底库检索 (Feature & Matching)**：
  - 从对齐后的人脸张量中提取 512 维特征向量，严格执行 L2 范数归一化；
  - 无缝消费 `zhulong::runtime::GalleryRegistry` 下行热同步底库；
  - 基于余弦距离完成 1:N 检索，输出命中状态 `matched`、人员标识 `person_id`、相似度 `similarity` 与 Top-K 候选人列表。
- **R4 完备的 `person_structure` 结构体语义**：
  - 遵守平台 C ABI `zhulong_person_item` 与 `zhulong_face_item` 内存排布；
  - 支持人体与人脸级联结构化；若未检出独立人体，自动降级为纯人脸退化语义（`body_box = face_box`, `body_score = face_score`, `track_id = 0`），保证与 Go 宿主及前端 Canvas 渲染层完全兼容。
- **R5 平台自适应与构建门禁**：
  - CPU 目标架构：不引入难以在开发环境离线编译的复杂外部大框架，保证离线编译与 CI 100% 可行；
  - RK3568 / RK3588 目标架构：提供标准 `manifest.json` 与 NPU 加速配置；
  - 打包生成标准 `.tar.gz` 算法包，单包 ≤ 500MB，物理扁平归档。
- **R6 自动化测试与黑盒验包**：
  - 编写 CTest 原生单元测试，覆盖多目标检测、关键点、对齐与匹配全流程；
  - `zhulong_algo_runner --verify-plugin` 黑盒准入验包测试通过；
  - Go 宿主端 `internal/algorithm` 包校验测试与 `make check` 全栈门禁 100% 绿灯。

## Acceptance Criteria

- [x] 算法插件基于输入图像真实像素计算人脸框与 5 点关键点，坐标非固定常量。
- [x] 包含 5 点关键点仿射对齐到标准 112x112 正脸的过程。
- [x] 提取 512 维特征向量并通过余弦距离与底库正确匹配。
- [x] 严格遵守 `person_structure` C ABI，结果在单块 Arena 内存中序列化且 `result_release` 无泄漏。
- [x] 原生 CTest 测试套件全部通过（18/18 且无回归）。
- [x] `zhulong_algo_runner --verify-plugin` 验包成功。
- [x] `packages/face_recognition/scripts/build_package.py` 成功打出薄包。
- [x] `make check` 全栈门禁（前端 + Native + Go + Bridge）100% 绿灯。
