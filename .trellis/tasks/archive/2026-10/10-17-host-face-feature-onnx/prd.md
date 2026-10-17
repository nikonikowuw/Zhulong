# PRD: 宿主引擎人脸特征提取接入 ONNX 真实推理

## Goal

将宿主端 `Zhulong_engine_extract_face_feature` C ABI 从当前的数学模拟桩升级为真正具备人脸检测（SCRFD `det_500m.onnx`）、5 点关键点仿射对齐（Umeyama 112×112）与 512 维特征抽取（`facelivtv2-l.onnx`）的端到端真实推理能力，使静态底库录入的人脸特征与实时视频流算法识别的人脸特征处于完全同构且可比对的真实度量空间中。

同时引入 **TTL 空闲超时自动卸载与资源回收机制（Idle Auto-Unload）**，确保在批量录入时高效热执行，闲置时自动归还全部物理内存（~80MB），兼顾极致性能与零内存闲置浪费。

## 架构边界与约束

1. **宿主链接卫生约束**：
   - 严格遵循 `.trellis/spec/native/hardware-backends.md` 与 `CMakeLists.txt`：宿主静态库 `libZhulongEngine.a` **绝不直接链接** `${ZHULONG_ONNX_ROOT}/lib/libonnxruntime.so`。
   - 宿主 Go 静态可执行文件 `zhulong` 不产生针对 ONNX Runtime 的编译期强动态库依赖，保持跨平台部署便携性。
2. **算法包能力复用与动态加载（dlopen）**：
   - 特征提取全流水线（检测、对齐、抽取）通过动态加载人脸插件（`libFaceRecognitionPlugin.so`）提供的专用 C ABI 接口完成。
   - 统一算法包与宿主端使用的模型权重（`det_500m.onnx` 与 `facelivtv2-l.onnx`），保证底库特征与探针特征 100% 同源同度量。
3. **严格的“闭环报错”（Fail Closed）与防静默退化原则**：
   - ⚠️ **坚决杜绝静默产生伪特征**：若动态库加载失败（如插件未编译、依赖缺失）或模型初始化失败，系统必须显式报错（返回 `Zhulong_ERR_UNSUPPORTED` 或 `Zhulong_ERR_INTERNAL`），严禁自动回退到虚假数学模拟特征，防止向人员底库写入无效向量导致后续视频流识别永久失灵。
   - 明确错误语义：
     - 未检测到人脸（如全黑、风景照、人脸模糊）：返回 `Zhulong_ERR_NOT_FOUND`（Go 侧映射为 400 `NO_FACE_DETECTED`）；
     - 插件不可用或推理内部故障：返回 `Zhulong_ERR_UNSUPPORTED` / `Zhulong_ERR_INTERNAL`（Go 侧映射为 500 `FEATURE_EXTRACT_FAILED`）；
     - 参数非法：返回 `Zhulong_ERR_INVALID_ARGUMENT`。
4. **TTL 空闲休眠与内存自动回收**：
   - 首次调用时懒加载插件与模型，进入热驻留状态（单张推理 10~25ms）；
   - 后续连续上传持续刷新活跃时间戳；
   - 当空闲超过设定期限（默认 60 秒，支持 `ZHULONG_FACE_PLUGIN_IDLE_TIMEOUT_SEC` 配置）无任何请求时，后台线程自动释放模型会话并 `dlclose` 动态库，彻底将 ~80MB 物理内存返还给操作系统与流媒体管道；
   - 引擎销毁（`Zhulong_engine_destroy`）时同步安全回收，无线程或句柄泄漏。
5. **跨层内存所有权**：
   - Go 传入的 RGB24/BGR24 像素仅在调用期间有效（借用），Native 输出 512 维 float32 数组由 Go 侧预先分配，谁分配谁释放。

## Requirements

### R1. 人脸识别插件导出单次特征提取与显式释放 C ABI
- 在 `packages/face_recognition` 中导出无状态/线程安全的独立特征提取接口：
  - `zhulong_face_extract_feature`: 输入紧凑像素，输出 512 维 L2 归一化特征；
  - `zhulong_face_extract_release`: 显式释放提取单例占用的模型会话与张量资源；
- 内部流程：
  1. SCRFD 检测人脸；若未检出任何有效人脸，返回 `ZHULONG_PLUGIN_ERR_NOT_FOUND`；
  2. 若有多张人脸，选取置信度最高（score 最大）的主人脸；
  3. 基于 5 点关键点执行 Umeyama 相似变换并直接裁剪出 112×112 对齐人脸 patch；
  4. 输入 `facelivtv2-l.onnx` 执行 512 维特征提取并完成单位 L2 范数归一化；
- 异常安全：全量 try-catch 拦截 C++ 异常，转换为对应错误状态码返回。

### R2. 宿主 Engine 动态加载、显式报错与 TTL 空闲回收
- 在 `native/src/abi/engine.cpp` 中改造 `Zhulong_engine_extract_face_feature`：
  - 维持现有 C ABI 签名完全不变，保持对上层 CGO / Go `Engine.ExtractFaceFeature` 的 100% 二进制兼容；
  - 实现 `FacePluginLoader` 管理器：
    - 多路径延迟加载（Lazy dlopen）；
    - 若加载失败或符号未找到，打印确切失败原因并**显式返回 `Zhulong_ERR_UNSUPPORTED`**；
    - 活跃状态跟踪与后台 Reaper 线程：空闲超过 60 秒（可配置）自动调用 `zhulong_face_extract_release` 并 `dlclose`；
    - 引擎关闭时执行同步汇合清理，杜绝异步悬挂。

### R3. Go 宿主层与测试用例保真
- 更新 `internal/engine/engine_face_test.go`：
  - 引入真实人脸图像 fixture，验证检测与特征提取的真实数值输出（非全零、512 维、模长为 1.0）；
  - 保留并强化对空图、损坏图、全黑无脸图、超大图的边界防御测试；
- 确保 `internal/person` 的底库录入流水线在真实特征与空闲回收下无缝运转。

## Acceptance Criteria

- [ ] `libFaceRecognitionPlugin.so` 导出 `zhulong_face_extract_feature` 与 `zhulong_face_extract_release`，并在符号表中可见。
- [ ] 宿主静态库 `libZhulongEngine.a` 及 Go 二进制产物 `zhulong` 不产生对 `libonnxruntime.so` 的 DT_NEEDED 动态链接依赖。
- [ ] `Zhulong_engine_extract_face_feature` 成功加载插件时，输入真实人脸样本（`face_detect_sample.ppm`）能检出人脸并生成 512 维 L2 归一化特征。
- [ ] 输入全黑无脸图片或纯色噪点图片时，准确返回 `Zhulong_ERR_NOT_FOUND`。
- [ ] 当插件缺失或未找到时，`Zhulong_engine_extract_face_feature` 显式返回 `Zhulong_ERR_UNSUPPORTED`，严禁产生虚假特征。
- [ ] 提取出的特征与插件内部在同一图片上提取出的特征余弦相似度 = 1.0（特征空间 100% 一致）。
- [ ] 连续空闲达到 TTL 超时后，后台 Reaper 自动卸载插件并清空持有内存；后续新请求能正常重新唤醒加载。
- [ ] 引擎生命周期结束（`Zhulong_engine_destroy`）时无内存泄漏、无句柄泄漏、后台线程正常退出。
- [ ] 通过 `native/scripts/build.py test --inference-backend onnx` 单元测试与 ASan 检查无内存泄漏。
- [ ] 通过 `python3 native/scripts/build.py go test -race ./internal/engine`。
