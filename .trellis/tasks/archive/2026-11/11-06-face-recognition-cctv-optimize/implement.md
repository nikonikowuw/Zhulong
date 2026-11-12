# 执行与验证计划

## Approval Gate

- [x] 用户批准任务创建和第一阶段包内范围。
- [x] 已检查实现、测试、schema、SDK 前缀及漂移要求，并完成规划评审修订。
- [x] 用户确认最新最终规划摘要；此前不运行 task start、不改产品代码。

## Ordered Work

1. 实施前重新检查工作区 `git status`（当前产品目录均干净，仅存本地环境文件与本任务目录）及对应规范：
   注意 `algo-plugin-contract.md` 超出 32KB 自动注入上限，务必按行号直读 §4.1、§5.0.2、§5.5、§6.7。
2. 先钉退化/拓扑判据与眼线局部有向坐标系：增补尺寸/长宽比/滚转（含 90°/180° 边界样本）/镜像不变性、
   侧脸框外投影不反弹、拓扑错乱与 NaN/Inf 测试，最小修改 `face_quality.hpp`（按 `min(box_w, box_h)` 归一）。
3. 添加 `common/face_image_quality.hpp`：RGB 清晰度（`var × max(1, |det|)`，`double` 累加方差）、曝光统计、
   门禁函数 `passes_face_image_gate` 及合成像素测试（在源图像素空间施加固定模糊核，验证 32/64/200 三尺度排序与近景不误拒）。
4. 增补参数解析和 schema 边界测试（含 `min_face_sharpness ∈ [0, 100000]` 防 `float` 窄化溢出），
   接入单一 `enable_face_image_gate` 开关；仅在流式特征提取前调用门禁函数。
5. 在 `common/scene_filter.hpp` 抽出 `should_emit_face_result`，在 `Matcher` 实现零 `unordered_map` 节点分配的身份级聚合、
   两层 tie-break（同人 `face_id` 升序、跨人 `person_id` 升序）、`>=` 分差判据、空 `person_id` 跳过与有限性校验；
   增补多模板、近分不同身份（用二进制精确浮点边界如 `0.75 - 0.50 = 0.25`）、单身份、同分、有限性、`top_k=0/1`、
   以及通过共用函数验证的 `filter_unmatched × min_match_margin` 组合测试。
6. 增补重复 anchor 挤占、超 pre-NMS 预算等分定序与最终目标上限测试，分离 SCRFD pre/post-NMS 预算；
   扫描序号同时纳入小顶堆淘汰比较器与 `std::sort`，并在 post-NMS 满员时提前 `break`。
7. 同步 rk3568 通用代码、两边 `config.schema.json` 与白名单接入点（`face_recognition.cpp`、`detector.cpp`），
   不改变该档位前处理、形状及路径，不执行交叉产物。
8. 写 `packages/face_recognition/README.md`（包级单份），过 `trellis-check` 检查最终 diff 和规范，只记录已验证契约。

约束（易错项，写入 checklist）：

- 新代码全部 header-only：包内 `unit-test` 与 `ZhulongFaceGeometryTests` 都只链
  `aligner.cpp + matcher.cpp`，新增 `.cpp` 会让两个入口链接失败。
- 流式门禁与 `filter_unmatched` 准入必须经共用头文件函数进入 `build_face_item`，严禁在测试文件手写 `if` 自证。
- 不新增 `FaceItem` 字段、不新增生产日志正文：宿主只保留 `message_bytes`（spec §5.0.2）。
- `pipeline/face_recognition.cpp` 与 `detector.cpp` 在漂移白名单里，且 `config.schema.json` 不在
  `check_package_drift.py` 的 `src/tests` 比较范围内，这三处必须**两边人工对齐并 diff 验证**。

## Validation

从包内最小测试开始，完整检查留到实施收尾；测试相关改动允许主动运行回归。

```bash
make -C packages/face_recognition/onnx unit-test \
  SDK_PREFIX=/home/niko/dev/Go/Zhulong/build/sdk/prefix \
  ONNXRUNTIME_ROOT=/home/niko/dev/Go/Zhulong/build/deps/onnxruntime
diff -u packages/face_recognition/onnx/config.schema.json \
  packages/face_recognition/rk3568/config.schema.json
python3 native/scripts/check_package_drift.py packages/face_recognition
make pkg-drift-check
make native-build
make -C packages/face_recognition/onnx test \
  SDK_PREFIX=/home/niko/dev/Go/Zhulong/build/sdk/prefix \
  ONNXRUNTIME_ROOT=/home/niko/dev/Go/Zhulong/build/deps/onnxruntime
make check
```

基线状态：`unit-test` 与 `pkg-drift-check` 已通过，两档位 `config.schema.json` 当前一致，因此任何失败都应归因于本轮改动。

- 包内数学回归用显式判据和非零退出码，不只用 `assert`。
- sample smoke 只证明产品推理能运行，不验证 CCTV 身份识别率。
- 覆盖进程默认和任务覆盖，保留现有 JSON、别名及错误输入回归。
- 如全栈失败，区分本轮问题、已有修改和环境问题，不顺手修无关业务。
- SDK 缺失时走现有 Makefile 入口；不下载新依赖，不跑裸 engine 相关 Go 命令。
- 不执行 RKNN 交叉产物、板端测试和 `sdk-clean-check`。
- 后处理耗时只能在宿主 ONNX 档位记录，引用时必须标注平台；板端预算标为"待实测"，
  不得用 x86 数字声称已验证板端成本。

## Review Risks

- 分别核查两档位 `face_recognition.cpp`、`detector.cpp` 与 `config.schema.json`；漂移门禁不覆盖这三处差异。
- 图像门禁关闭不得改变原几何分及拒识门槛；开启时不得因 `|det| < 1` 误拒近景清晰大脸。
- 几何退化尺度不得耦合未旋转单轴 `box_w`/`box_h`；侧脸越界投影不得因绝对值反弹高分。
- 第二身份与候选返回数解耦，不把同人照片当竞争身份；不在每脸比对路径分配 `unordered_map` 节点。
- 质量测试不能只复制实现公式自证，应使用独立几何变换和源图空间 PSF 合成像素输入。
- 小顶堆淘汰比较器与最终排序必须同时纳入扫描序号，保证超预算等分输入确定性。
- 有限性过滤不得变成逐元素扫全底库（每帧 N×512）；比较器与门限分支不得被 `NaN` 穿透。
- tie-break 不得依赖快照顺序巧合（同人按 `face_id` 升序，跨人按 `person_id` 升序）。
- `filter_unmatched × min_match_margin` 的"近分被整体丢弃"通过共用产品函数测试并在文档写明。
- 保留所有无关未跟踪文件，不重置、不提交。

## Final Report

列出实际行为变化、新参数默认值与有界范围（标注占位待标定）、验证结果、未执行的现场评测与板端耗时，
不宣称未经测量的识别率提升。
