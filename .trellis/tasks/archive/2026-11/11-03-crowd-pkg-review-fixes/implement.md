# 实施记录：crowd_gathering 评审问题修复

对应 PRD 的 R1–R16。每条给出改动位置与验证方式。

## 阻断项

### R1 单测被 NDEBUG 编译掉 → 拆出真正的自测目标

- 新增 `packages/crowd_gathering/onnx/tests/unit_test.cpp`：显式 `check()` + 失败计数 + 非零退出，
  覆盖配置解析、输入尺寸校验、聚类几何（含 aspect 不变性）、时序防抖（含宽限期伸展）。
- `tests/dev_infer.cpp`：删掉 `run_internal_unit_tests()` 与 `<cassert>`，只保留出图 CLI。
- `CMakeLists.txt`：新增 `crowd_gathering_unittest`（只编 `spatial_clustering.cpp` + `temporal_tracker.cpp`，
  不加载模型）。
- `Makefile`：新增 `unit-test` 目标，`test: unit-test $(DEVTEST)`。
- `native/CMakeLists.txt`：新增 `ZhulongCrowdGatheringTests` 直接编译包内同一份 `tests/unit_test.cpp`
  （spec §5.5 的方式，不复制副本），`make check` / `make native-test` 由此覆盖。

验证：`make unit-test` 通过；两次变异测试（去掉 aspect 折算、把宽限期写死 1.5s）都能让该目标
**失败并返回非零**，恢复后重新通过。

### R2 模型路径解析缺可执行文件目录

- `src/pipeline/crowd_gathering.cpp`：新增 `executable_directory()`（`/proc/self/exe` → `dladdr` 兜底，
  处理 ` (deleted)` 后缀），`resolve_model_path` 顺序改为
  **exe 目录 → env → 开发期源码树 → CWD**。
- 新增 `model_unavailable_status()`：文件在但加载失败 → `NOT_SUPPORTED`（当前构建无真实后端），
  文件缺失 → `NOT_FOUND`。

验证：把 exe + `models/` 拷到 `/tmp/crowd_inst`，从 `cd /tmp` 执行
`--verify --sample <ppm>` → `status:"passed"`、`sample_decoded:true`；
`--broker /tmp/zhulong_broker_999.sock` → 退出码 `5`（连不上 socket），即 model_create 已成功。
反向确认：只有 exe 没有 `models/` 时退出码 `2` + `stage:"model_create" rc=-4`。

## 正确性

### R3 归一化空间不等比

- `SpatialClusterer::cluster` 增加 `aspect_ratio` 形参，接地点 X 先乘 `width/height` 再算距离；
  aspect 非有限或 ≤0 时退化为 1.0（可预测，不产生 NaN 粘连）。
- `crowd_gathering.cpp` 从 `image.raw_view()` 传 `width/height`。
- `spatial_clustering.cpp`：`count_threshold <= 1` 提前拒绝；簇输出按「人数降序 → 质心 Y → X」定序
  （原来依赖 `unordered_map` 迭代顺序，结果不可复现）。

### R4 宽限期与滞留时长耦合

- 新增 `grace_period_seconds(duration) = max(1.5s, duration × 0.25)`，淘汰判据改用它。
- 单测用「3s 空帧 + 60s 阈值」钉住：固定 1.5s 宽限期的实现会被判失败。

### R5 配置区间两份真值

- `crowd_config.hpp` 的 clamp 区间改为具名常量并与 `config.schema.json` 逐项一致
  （0.1–1.0 / 2–20 / 0.5–3.0 / 0–60）。宿主按 schema 校验，算法侧只做防御性回落。

## 性能

### R6 每帧张量分配

- 新增 `DetectorWorkspace`（letterboxed UINT8 张量 + NCHW 输入张量 + 诊断标记），按 `worker_id`
  由插件持有（`unique_ptr` 保证地址稳定），`create_session` 时预分配。
- `Session::forward` 改为传 `workspace.model_inputs()`（const 引用），去掉原先
  `forward({onnx_input})` 隐式整块拷贝（约 5MB/帧）。

### R7 每帧重解析任务配置

- 插件新增 `config_for(task_id, json)`：按 task_id 缓存 `(原文, CrowdConfig)`，原文不变直接复用；
  解析在锁外做。

### R8 640×640 补边浪费

- 新增进程级配置 `detect_input_width` / `detect_input_height`（区间 128–1280，128 的倍数），
  `PersonDetector::set_input_size` 带校验 fail closed；schema 同步。
- **默认档位改为 640×384（16:9 CCTV）**：letterbox 进 640×640 时内容区只有 640×360，
  方形输入的补边占约 44% 画布，内容分辨率相同而算力白多掏。单测钉住默认值，
  README/schema 的默认与说明同步更新。

  验证（dev 样本 640×443，非 16:9）：默认运行结果与显式 `{"detect_input_height":384}` **逐框一致**，
  与显式 640×640 不同；本样本上 55ms vs 96ms，真实 16:9 流两者内容分辨率相同。

### R9 全局互斥

- `TemporalTracker` 改为「每 task 一个 `TaskEntry{mutex, tracker}`」，`unique_ptr` 值保证地址稳定；
  全局锁只在取/建条目时短暂持有。

## 规范与一致性

- **R10 静默零检出**：`scrfd_outputs_match` 回填失败原因，首次不符时经 `log_warning` 输出一条
  `zhulongLog` 记录（每 Worker 只报一次）；工作区与模型尺寸失配同样留痕。
  新增 `src/common/runner_log.hpp`：单行 JSON、定长缓冲、单次写入（裸 stderr 文本会被宿主丢弃，
  见 spec §5.0.2）。
- **R11 SDK NMS 复用**：`person_detector.cpp` 删除手写 IoU + 抑制循环，改用 `zhulong::ops::nms`
  （IoU 在逐轴线性缩放下不变，故像素空间候选归一化后再 NMS 等价）；包内只剩时序跟踪需要的
  `box_iou` 一份。
- **R12 JSON 助手**：合并为单一深度感知扫描 `find_json_value_start`（只认顶层对象成员，跳过字符串内容），
  数值用 `strtod` 免分配并校验数字尾部；`<exception>` 等头文件按自包含补齐。
- **R13 档位断言**：`static_assert(std::string_view(zhulong::TARGET_MODEL_EXT) == ".onnx")`。
- **R14 门禁接入**：`make sdk-clean-check` 与 `make pkg-drift-check` 列表加入该包；
  `native/CMakeLists.txt` 注册 `ZhulongCrowdGathering` ctest。
- **R15 验包报告校验**：`build_package.py` 解析报告末行，`status != passed` 直接失败；
  `sample_decoded=false` 打印告警并明确标注「仅 ABI 冒烟」，不再打印「通过」。
- **R16 死代码/可读性**：删除 `TrackedCluster::track_id`、`next_track_id_`、`TemporalTracker::reset_task`、
  未使用的 `<iostream>`；`TaskStreamTracker::update` 拆成 match/append/evict/collect 四段；
  阈值魔法数改为具名常量；`DisjointSet` 索引改 `size_t`、`find` 改迭代路径压缩。

## 文档

- 新增 `packages/crowd_gathering/README.md`：输出语义、算法链路、配置层级、四条已知限制
  （含「人数/滞留时长受 ABI 限制无法上报」）。
- `.trellis/spec/native/algo-plugin-contract.md` 新增 §5.0.1（资源路径锚点）与 §5.0.2（包内诊断通道）。

## 验证汇总

| 检查 | 结果 |
| :--- | :--- |
| `make unit-test`（包内） | 通过；三次变异测试均被捕获（aspect 折算 / 固定宽限期 / 默认档位回退方形） |
| `make test`（真实权重出图） | 通过；检测结果与改造前逐框一致（5 条：1 alarm + 4 person） |
| `make all` + `-Wall -Wextra -Wshadow -Wnon-virtual-dtor -Wold-style-cast` | 自有 TU 零告警（仅 stb 三方头告警） |
| `native/tests/check_plugin_symbols.py` | 退出码 0（无 `zhulong_*` 导出） |
| `make native-build` / `make native-test` | 30/30 通过（含新增 `ZhulongCrowdGathering`） |
| `make pkg-drift-check` | 通过（单 target 跳过比较） |
| `build_package.py --skip-build` | 打包成功；ffmpeg 缺失路径正确降级为「仅 ABI 冒烟」告警 |
| `cd /tmp` 下 `--verify` / `--broker` | passed / 退出码 5（model_create 成功） |
| `make sdk-clean-check` | **未运行**（需 docker + 网络） |
| `make check` | **未运行**：工作区存在与本任务无关的 web/、internal/camera 在途改动 |
