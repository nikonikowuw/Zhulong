# 实现方案与执行计划：RK3568 真实人体/人脸结构化算法接入 (10-15-rk3568-person-structure)

## 1. 执行清单 (Ordered Checklist)

### 阶段一：模型资产引入与包结构配置
- [x] **1.1 复制真实 RKNN 模型至算法包目录**：
  - 将 `/home/niko/dev/rknn_model_zoo/examples/insightface/det_500m/model/det_500m_rk3568_i8.rknn` 复制至 `packages/face_recognition/targets/rk3568/models/det_500m.rknn`；
  - 将 `/home/niko/dev/rknn_model_zoo/examples/facelivt/facelivtv2_l/model/facelivtv2-l.rknn` 复制至 `packages/face_recognition/targets/rk3568/models/facelivtv2-l.rknn`；
  - 检查 `packages/face_recognition/targets/rk3568/manifest.json` 配置。
  - *验证*：文件存在且 SHA-256 吻合，大小约 927KB 与 18MB。

### 阶段二：C++ SDK 与算法流水线核心重构
- [x] **2.1 增强 `native/sdk/include/zhulong/algo.hpp` 会话能力**：
  - 在 `zhulong::Session` 类中新增 `uint32_t worker_id() const noexcept { return ctx_ ? ctx_->worker_id : 0; }`。
  - *验证*：`make native-build` 编译通过。
- [x] **2.2 重构人脸检测与原帧映射模块 (`detector.hpp` / `detector.cpp`)**：
  - 接入真实 SCRFD `det_500m` 模型推理；
  - 实现输入图像 Letterbox 居左上缩放（画布 640×640 RGB UINT8）；
  - 实现 Stride 8, 16, 32 共 9 个输出头锚点解码算法（根据 cx, cy 与 stride 还原检测框与 5 点关键点）；
  - 实现基于置信度过滤（threshold >= 0.50）与 IoU NMS（threshold <= 0.40）；
  - 将检出人脸框与 5 点坐标反算映射回原帧像素坐标；
  - 实现人体框（`body_box`）自适应解剖学估算与坐标归一化；
  - 保留 CPU 后端 Mock Fallback，确保 host 开发机测试稳定运行。
  - *验证*：编写或更新单元测试，真实前向能检出非固定坐标的人脸框与 5 点关键点。
- [x] **2.3 升级对齐引擎实现原帧高保真单阶段直写 (`aligner.hpp` / `aligner.cpp`)**：
  - 基于原帧像素坐标计算 Umeyama 仿射逆矩阵；
  - 直接从原帧高保真像素双线性重采样**单阶段直写**目标特征输入 Tensor (112×112)，彻底消除中间裁剪图与过渡内存；
  - 严格限制采样边界（Clamp），消除跨界野指针。
  - *验证*：输入倾斜人脸，输出对齐后的 112×112 图像清晰无畸变，且零堆临时 buffer 分配。
- [x] **2.4 重构特征提取模块 (`extractor.hpp` / `extractor.cpp`)**：
  - 接入真实 `facelivtv2-l` 模型推理（输入 112×112 RGB UINT8）；
  - 从前向输出张量读取 512 维特征，执行**原地 (In-Place)** 单位 L2 范数归一化；
  - 保留 CPU 后端 Mock Fallback，确保 host 测试兼容。
  - *验证*：特征输出非全零且范数为 1.0 ± 1e-4，无额外特征 vector 堆拷贝。
- [x] **2.5 组装插件门面 (`face_recognition.cpp`)**：
  - 初始化加载 `det_model` 与 `rec_model`；
  - `create_session(worker_id)` 同步派生检测会话与特征会话；
  - `detect_persons` 串联：检测 ➔ 坐标反算回原帧 ➔ 原帧外扩裁剪 ➔ Umeyama 局部对齐 ➔ 特征抽取 ➔ Matcher 1:N 底库比对 ➔ PersonItem 打包；
  - 严守 C ABI ResultType == 3 与连续单块 Arena 内存布局契约。
  - *验证*：原生 CTest 测试 `ZhulongFaceRecognitionPluginTests` 通过。

### 阶段三：算法包打包与自动化准入验证
- [x] **3.1 增强 `packages/face_recognition/scripts/build_package.py`**：
  - 支持拷贝 `models/` 目录中的真实权重；
  - 支持组装 `face_recognition-rk3568.tar.gz`；
  - 自动运行 `zhulong_algo_runner --verify-plugin` 执行黑盒准入验收。
  - *验证*：脚本运行成功生成 `.tar.gz`，大小约 19MB，验包退出码为 0。

### 阶段四：跨层集成测试与全栈质量门禁
- [x] **4.1 本地原生与 Go 宿主门禁验证**：
  - 运行 `python3 native/scripts/build.py test`，所有 C++ 原生测试绿灯；
  - 运行 Go 算法验包测试 `go test -v ./internal/algorithm -run TestFaceRecognition`；
  - 运行 `make check`，全栈门禁 100% 绿灯。
- [x] **4.2 RK3568 交叉编译验证**：
  - 运行 `python3 native/scripts/build.py --profile native/profiles/rk3568-rknn-rga.json build`，确认交叉产物编译无错误。

---

## 2. 验证命令集 (Validation Commands)

```bash
# 1. 本地 Native 编译与测试
python3 native/scripts/build.py build
python3 native/scripts/build.py test

# 2. 组装 CPU 与 RK3568 算法包并黑盒验包
python3 packages/face_recognition/scripts/build_package.py --target cpu
python3 packages/face_recognition/scripts/build_package.py --target rk3568 --no-verify

# 3. Go 算法宿主端测试
go test -v -race ./internal/algorithm -run TestFaceRecognition

# 4. RK3568 交叉编译全量产物
python3 native/scripts/build.py --profile native/profiles/rk3568-rknn-rga.json build

# 5. 全栈收工质量门禁 (必须全绿)
make check
```

---

## 3. 回滚方案 (Rollback Points)

- 若修改破坏了本地 host 测试或 C ABI 契约，可回滚修改：
  ```bash
  git checkout packages/face_recognition/ native/sdk/include/zhulong/algo.hpp
  ```
- 若模型文件占用过大或格式不合预期，直接删除 `packages/face_recognition/targets/rk3568/models/` 重新放置。
