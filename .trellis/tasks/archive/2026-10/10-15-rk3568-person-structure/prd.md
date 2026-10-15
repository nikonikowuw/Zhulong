# 真实人脸/人体结构化算法接入 (10-15-rk3568-person-structure)

## Goal

将 `packages/face_recognition` 算法包从当前的启发式/伪代码实现（像素亮度中心检测 + sin/cos 特征计算）重构为**基于真实深度学习神经网络模型的完整算法流水线**：
1. 基于 RKNN / RKNPU2 在 RK3568 上执行真实人脸检测（SCRFD `det_500m`）与 5 点关键点定位；
2. 基于真实 5 点关键点与 Umeyama 仿射对齐生成标准 112×112 正脸 Patch；
3. 基于 FaceLiVTv2 模型（`facelivtv2-l`）提取真实 512 维特征向量，并与底库（`GalleryRegistry`）进行 1:N 余弦比对；
4. 按照平台 C ABI 规范（ResultType == 3，`zhulong_person_item`）完成结构化打包与单块 Arena 内存释放；
5. 打包为符合 `platform.target: "rk3568"` 的 `.tar.gz` 算法包，通过 `zhulong_algo_runner --verify-plugin` 黑盒准入，并成功接入宿主执行实时视频流分析。

## Target Platform & Hardware Constraints

- **芯片平台**：Rockchip RK3568（Quad A55 @ 2.0GHz，单核 NPU 1.0 TOPS @ INT8）。
- **NPU 运行库**：RKNPU2 Runtime 2.3.2 (`librknnrt.so`)，内核驱动 v0.9.8。
- **图像加速**：RGA2E (`librga.so` 2.1.0) / CPU Vision Backend。
- **内存与拓扑契约**：
  - 纯 C ABI 物理隔离（`zhulong_plugin_api_v1`），异常绝不穿越动态库边界；
  - 严格坐标归一化：所有目标检测框、人脸框与关键点均归一化至 `[0.0, 1.0]`；
  - 单块连续 Arena 内存序列化，`result_release` 无泄漏；
  - 本地 Host (x86_64) CPU 桩环境与 CI 回归继续保持兼容（`make check` / CTest 全部通过）。

## Confirmed Models & Provenance

在宿主机 `/home/niko/dev/rknn_model_zoo/` 中已就绪并标定的真实模型：
1. **人脸检测模型**：`det_500m_rk3568_i8.rknn` (SCRFD-500M, 输入 640×640, 9 个输出分支：score/bbox/kps)
2. **特征提取模型**：`facelivtv2-l.rknn` (FaceLiVTv2-L, 输入 112×112 RGB, 输出 512 维向量)
3. **CPU 桩模型**：`det_500m.onnx` 与 `facelivtv2-l.onnx`（用于模型结构验证与开发环境）

## Requirements

- **R1 真实人脸与关键点检测 (SCRFD on RKNN)**：
  - 替换 `packages/face_recognition/src/detector.cpp` 中的像素扫描启发式逻辑；
  - 加载 `det_500m` 模型，输入经 Letterbox 缩放至 640×640 并进行前处理（RGB, `(x - 127.5) / 128.0`）；
  - 解析 3 个尺度（stride 8, 16, 32）的 9 个输出张量（注意：score 输出已含 Sigmoid，无需重复 Sigmoid）；
  - 执行网格锚点还原、置信度阈值过滤与 NMS（IoU 过滤），输出人脸框与 5 点关键点；
  - 根据人脸位置合理估算/关联对应的人体框（`body_box`）与 `body_score`。
- **R2 真实人脸姿态对齐与原帧外扩裁剪 (Margin Crop & Umeyama Alignment)**：
  - 将 640×640 检测层输出的人脸框与 5 点坐标精确映射回原始高清视频帧（消除 Letterbox 比例影响）；
  - 在原帧高保真图像上对人脸框进行四周 25%~30% 外扩与安全边界裁剪（防低清二次放大模糊与全帧跨行 Cache 颠簸）；
  - 运用 Umeyama 算法在局部高清 ROI 上计算 ArcFace 112×112 参考模板的相似性仿射变换矩阵，双线性插值生成标准正脸 Patch。
- **R3 真实 512 维特征提取与底库匹配 (FaceLiVTv2 on RKNN)**：
  - 替换 `packages/face_recognition/src/extractor.cpp` 中的 sin/cos 伪算法；
  - 加载 `facelivtv2-l` 模型，输入 112×112 RGB（`(x - 127.5) / 127.5`），推理输出 512 维特征张量；
  - 执行 L2 范数单位归一化；
  - 消费 `GalleryRegistry` 快照，计算余弦相似度并完成 1:N 检索，填充 `matched`、`person_id`、`person_name`、`similarity` 与 Top-K 候选人。
- **R4 多模型/多 Worker 会话隔离**：
  - 插件需同时管理检测模型与识别模型两个 Model 实例；
  - 在 `create_session(worker_id)` 时为该 Worker 分配独立的检测与特征 Session（通过 `rknn_dup_context` 共享物理权重内存）。
- **R5 算法包构建与系统接入验证**：
  - 更新 `packages/face_recognition/targets/rk3568/models/`，将模型文件纳入打包范围；
  - 自动化构建脚本 `build_package.py --target rk3568` 输出完整 `.tar.gz` 算法包；
  - 算法包通过 `zhulong_algo_runner --verify-plugin` 准入测试并在 Go 宿主侧成功加载调度。
- **R6 极致零拷贝与零堆冗余分配 (Zero-Copy & Allocation-Free Dataflow)**：
  - 原帧只读借用：直接利用 `zhulong_frame_view` 共享内存只读指针，严禁全帧 `memcpy`；
  - 缩放直写：Letterbox 图像缩放直接写入检测模型输入 Tensor，无中间过渡缓冲；
  - 仿射单阶段直写：基于 Umeyama 逆矩阵，直接从原帧双线性重采样写入识别模型输入 Tensor（112×112），彻底消灭「裁剪临时图 ➔ 对齐临时图 ➔ 模型输入」的中间多重分配与数据搬移；
  - 解码栈上化：检测锚点解码与 NMS 使用 Worker 预分配固定缓冲区，避免推理热路径动态 `vector` 频繁扩容；
  - 结果单块打包：单次 `malloc` 分配连续 Arena 内存直接序列化结果树。

## Acceptance Criteria

- [x] `packages/face_recognition` 移除所有启发式假代码，全面接入 NPU/RKNN 真实前向推理。
- [x] 检测阶段能正确检出输入图像中的人脸框与 5 点关键点，坐标非静态常量。
- [x] 特征阶段输出真实的 512 维浮点向量，能够与底库模板进行真实的余弦相似度匹配（同一人 > 0.65，不同人 < 0.40）。
- [x] 算法包在 RK3568 下通过 `build_package.py --target rk3568` 打包，`zhulong_algo_runner --verify-plugin` 成功通过。
- [x] 开发机原生单元测试与 `make check` 全流程不回归、100% 绿灯。
