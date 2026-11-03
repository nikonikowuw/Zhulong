# crowd_gathering — 人员非法聚集检测算法包

基于 SCRFD 2.5G 人体检测 + 透视自适应空间聚类 + 簇级驻留积分时序防抖的聚集研判算法。
当前档位：`onnx`（x86_64）。

## 目录

```
packages/crowd_gathering/
├── README.md                 本文件
└── onnx/
    ├── manifest.json         包元信息（id / version / platform.target）
    ├── config.schema.json    宿主校验与前端渲染用的配置契约
    ├── models/scrfd_person_2.5g.onnx
    ├── sample.jpg            开发期样本帧
    ├── src/                  算法源码（Stage 切片）
    ├── tests/                包内自测（unit_test）+ 开发期出图（dev_infer）
    └── scripts/build_package.py  组装 .tar.gz 并跑黑盒验包
```

## 开发命令

```bash
cd packages/crowd_gathering/onnx
make unit-test   # 纯逻辑自测：配置解析 / 聚类几何 / 时序防抖（不加载模型）
make test        # 真实权重出图 → build/out/result-crowd_gathering-onnx.jpg
make verify      # 包根可执行文件的一次性验包（骨架只解 netpbm，jpg 会报 sample_decoded=false）
```

`make test` 前需要 `make sdk-install`（SDK 前缀）与 `ONNXRUNTIME_ROOT` 或 `BUNDLE_RUNTIME=1`。

## 输出语义

| 最终 label | 宿主行为 | 说明 |
| :--- | :--- | :--- |
| `crowd_gathering_alarm` | 告警入库 + 全幅图/裁剪图抓拍 | 标签必须含 `alarm`（`internal/record` 的 `isAlarmLabel` 按子串识别）；`score` 是簇内人体检测置信度均值，**不是**告警概率 |
| `person` | 普通目标抓拍（受冷却去重） | 仅在 `report_person_boxes=true` 时上报，用于前端画框与调试 |

## 算法链路

1. **人体检测**：SCRFD 2.5G（5 层 FPN，stride 8/16/32/64/128，每格 1 anchor），letterbox 到模型输入尺寸后归一化（mean 127.5 / std 128），解码后归一化坐标 + NMS（`zhulong::ops::nms`）。
   默认输入 **640×384（16:9 CCTV 档位）**：letterbox 进 640×640 时内容区只有 640×360，方形输入的补边占了约 44% 的画布 —— 内容分辨率一样，算力却白多掏，所以默认按监控流取 640×384。
2. **空间聚类**：以框底边中点为接地点，按「平均身高」为尺度做相对距离聚类（并查集）。**帧宽高比参与距离折算**：归一化 x 与 y 是两个尺度，不折算会让横向判据被放宽 `W/H` 倍。
3. **时序防抖**：簇级 IoU/质心匹配的 track，累计滞留时长超过 `gather_duration_seconds` 才告警；宽限期 `max(1.5s, duration × 0.25)` 随阈值伸展，避免长时长配置下被单次漏检清零。

## 配置

两类键都在 `config.schema.json` 里；`detect_input_*` 只认进程级通道（`--config`），因为它决定模型输入尺寸与每 Worker 的前处理工作区，任务级下发不会、也不该改动它。

| 键 | 默认 | 区间 | 层级 |
| :--- | ---: | :--- | :--- |
| `score_threshold` | 0.5 | 0.1 – 1.0 | 进程级 / 任务级 |
| `gather_count_threshold` | 3 | 2 – 20 | 进程级 / 任务级 |
| `gather_distance_ratio` | 1.2 | 0.5 – 3.0 | 进程级 / 任务级 |
| `gather_duration_seconds` | 5.0 | 0 – 60 | 进程级 / 任务级 |
| `report_person_boxes` | false | 布尔 | 进程级 / 任务级 |
| `detect_input_width` / `detect_input_height` | 640×384 | 128 – 1280，128 的倍数 | **仅进程级** |

代码里的区间常量在 `src/common/crowd_config.hpp`，与 schema 逐项对应；两边改动必须同步。

## 已知限制（都是当前 ABI/契约的边界，不是漏实现）

1. **簇内人数与滞留时长不上报**：检测结果载荷只有 `box / confidence / label`（`zhulong_bbox` 无 attributes 字段），`person_count` 与 `dwell_seconds` 算完即丢。要展示「5 人聚集 12 秒」需要 ABI v2 的平级对象模型，不要在 `label` 里塞数字（会让告警类型碎片化）。
2. **同一路相机的并发簇共享一个告警冷却键**（宿主 `alarm:<camera>:<label>`）：同帧多个簇、或告警持续期间反复触发，同一冷却窗口内只落一条告警记录。
3. **黑盒验包只证明 ABI 生命周期**：`--verify` 是单帧推理，默认配置下 `gather_duration_seconds=5`、`report_person_boxes=false` → 结果必然为 0 条。要验证真实检出请用 `make test`（金标图 + 出图）。
4. **仅 onnx 档位**：`static_assert(TARGET_MODEL_EXT == ".onnx")` 会在用 rknn 前缀构建时直接编译失败。新增板端档位时按 `check_package_drift.py` 的登记流程加 target。
