# crowd_gathering — 人员非法聚集检测算法包

基于 SCRFD 2.5G 人体检测 + 透视自适应空间聚类 + 簇级驻留积分时序防抖的聚集研判算法。
两个档位：

| 档位 | 平台 | 权重 | 检测输入 |
| :--- | :--- | :--- | :--- |
| `onnx/` | x86_64 / `onnx` | `models/scrfd_person_2.5g.onnx` | 运行期可配，默认 640×384 |
| `rk3568/` | aarch64 / `rk3568` | `models/scrfd_person_2.5g.rknn`（INT8） | **静态 640×384**（转换时固定） |

## 目录

```
packages/crowd_gathering/
├── README.md                 本文件
├── scripts/build_package.py  组装 .tar.gz 并跑黑盒验包（--target onnx|rk3568）
├── onnx/
│   ├── manifest.json         包元信息（id / version / platform.target）
│   ├── config.schema.json    宿主校验与前端渲染用的配置契约
│   ├── models/scrfd_person_2.5g.onnx
│   ├── sample.jpg            开发期样本帧
│   ├── src/ tests/
│   └── Makefile
└── rk3568/
    ├── manifest.json         同上（arch=aarch64, target=rk3568）
    ├── config.schema.json    不含 detect_input_*（静态形状，不可调）
    ├── models/scrfd_person_2.5g.rknn
    ├── CONVERSION.md         ONNX → RKNN 的完整证据链（sha256 / opset / 归一化位置）
    ├── QUANTIZATION.md       量化模式、校准集、板上数值验证
    ├── scripts/convert_scrfd_person.py   转换 + 校准集生成
    ├── src/ tests/           与 onnx 档位同源，仅前处理/尺寸校验/扩展名不同
    └── Makefile
```

## 开发命令

ONNX 档位（x86_64 宿主）：

```bash
cd packages/crowd_gathering/onnx
make unit-test   # 纯逻辑自测：配置解析 / 聚类几何 / 时序防抖（不加载模型）
make test        # 真实权重出图 → build/out/result-crowd_gathering-onnx.jpg
make verify      # 包根可执行文件的一次性验包（骨架只解 netpbm，jpg 会报 sample_decoded=false）
```

`make test` 前需要 `make sdk-install`（SDK 前缀）与 `ONNXRUNTIME_ROOT` 或 `BUNDLE_RUNTIME=1`。

RK3568 档位（交叉构建 + 板端验证）：

```bash
# 交叉构建（产物在宿主不可执行；pkg 会跳过黑盒验包）
cd packages/crowd_gathering && make pkg TARGET=rk3568
# → build/packages/crowd_gathering-aarch64-rk3568.tar.gz

# 板上跑单测 / 出图 / 验包（示例：scp 到 /tmp 后执行）
./crowd_gathering_unittest
ZHULONG_CROWD_MODEL_DIR=$PWD ./crowd_gathering_devtest --sample sample.jpg --out /tmp/out.jpg
./crowd_gathering --verify --sample sample.jpg     # 包内 exe 与 models/ 同级
```

`rk3568` 的 Makefile 会自动解析本仓库的交叉工具链与厂商前缀，也可手动指定：

```bash
make -C packages/crowd_gathering/rk3568 \
    TOOLCHAIN=$PWD/build/native/cross-rk3568-rknn-rga/toolchain.cmake \
    SDK_PREFIX=$PWD/build/sdk/rk3568 \
    RKNN_ROOT=$PWD/native/vendor/rknpu2 RGA_ROOT=$PWD/native/vendor/rga
```

板端部署时 `config.toml` 的 `[algorithm].target` 必须是 `rk3568`（与
`rk3568/manifest.json` 的 `platform.target` 一致），否则宿主会按 onnx 档位找包、
按扩展名加载权重而拿到空目录。

## 输出语义

| 最终 label | 宿主行为 | 说明 |
| :--- | :--- | :--- |
| `crowd_gathering_alarm` | 告警入库 + 全幅图/裁剪图抓拍 | 标签必须含 `alarm`（`internal/record` 的 `isAlarmLabel` 按子串识别）；`score` 是簇内人体检测置信度均值，**不是**告警概率 |
| `person` | 普通目标抓拍（受冷却去重） | 仅在 `report_person_boxes=true` 时上报，用于前端画框与调试 |

## 算法链路

1. **人体检测**：SCRFD 2.5G（5 层 FPN，stride 8/16/32/64/128，每格 1 anchor），解码后归一化坐标 + NMS（`zhulong::ops::nms`）。
   - `onnx/`：letterbox 到模型输入尺寸后自行归一化（mean 127.5 / std 128，NCHW float32）。
   - `rk3568/`：归一化写进 `.rknn`、NPU 内执行；应用侧只做 letterbox（114 灰边）并喂 HWC uint8。
   默认输入 **640×384（16:9 CCTV 档位）**：letterbox 进 640×640 时内容区只有 640×360，方形输入的补边占了约 44% 的画布 —— 内容分辨率一样，算力却白多掏，所以按监控流取 640×384。rk3568 档位在转换时就把这个尺寸钉死。
2. **空间聚类**：以框底边中点为接地点，按「平均身高」为尺度做相对距离聚类（并查集）。**帧宽高比参与距离折算**：归一化 x 与 y 是两个尺度，不折算会让横向判据被放宽 `W/H` 倍。
3. **时序防抖**：簇级 IoU/质心匹配的 track，累计滞留时长超过 `gather_duration_seconds` 才告警；宽限期 `max(1.5s, duration × 0.25)` 随阈值伸展，避免长时长配置下被单次漏检清零。

## 配置

两类键都在 `config.schema.json` 里。`detect_input_*` **只有 onnx 档位**有：它决定模型输入尺寸与每 Worker 的前处理工作区，只认进程级通道（`--config`），任务级下发不会、也不该改动它。rk3568 档位的输入尺寸由权重静态形状决定，schema 里不暴露该键。

| 键 | 默认 | 区间 | 层级 |
| :--- | ---: | :--- | :--- |
| `score_threshold` | 0.5 | 0.1 – 1.0 | 进程级 / 任务级 |
| `gather_count_threshold` | 3 | 2 – 20 | 进程级 / 任务级 |
| `gather_distance_ratio` | 1.2 | 0.5 – 3.0 | 进程级 / 任务级 |
| `gather_duration_seconds` | 5.0 | 0 – 60 | 进程级 / 任务级 |
| `report_person_boxes` | false | 布尔 | 进程级 / 任务级 |
| `detect_input_width` / `detect_input_height` | 640×384 | 128 – 1280，128 的倍数 | **仅进程级**（仅 onnx 档位） |

代码里的区间常量在 `src/common/crowd_config.hpp`（两档位同源），与 schema 逐项对应；两边改动必须同步。

## 两档位的源码关系

`src/` 与 `tests/` 是**同一份实现的自包含副本**，由 `native/scripts/check_package_drift.py`
（`make pkg-drift-check`）守着。允许不同的只有三处，均已登记理由：

- `src/detection/person_detector.cpp`：前处理（ONNX 需 NCHW float32 + mean/std，RKNN 模型自带归一化）；
- `src/detection/person_detector.hpp`：工作区布局与尺寸校验（静态形状只接受 640×384）；
- `src/pipeline/crowd_gathering.cpp`：权重扩展名与开发期源码树前缀。

其余文件（聚类、时序、配置解析、单测）必须逐字一致 —— 修一处就要同步另一处。

## 已知限制（都是当前 ABI/契约的边界，不是漏实现）

1. **簇内人数与滞留时长不上报**：检测结果载荷只有 `box / confidence / label`（`zhulong_bbox` 无 attributes 字段），`person_count` 与 `dwell_seconds` 算完即丢。要展示「5 人聚集 12 秒」需要 ABI v2 的平级对象模型，不要在 `label` 里塞数字（会让告警类型碎片化）。
2. **同一路相机的并发簇共享一个告警冷却键**（宿主 `alarm:<camera>:<label>`）：同帧多个簇、或告警持续期间反复触发，同一冷却窗口内只落一条告警记录。
3. **黑盒验包只证明 ABI 生命周期**：`--verify` 是单帧推理，默认配置下 `gather_duration_seconds=5`、`report_person_boxes=false` → 结果必然为 0 条。要验证真实检出请用 `make test`（金标图 + 出图）。
4. **rk3568 档位的检测分辨率不可运行期调整**：`.rknn` 是静态形状，改分辨率必须重新转换权重（见 `rk3568/CONVERSION.md`）。`set_input_size` 对其它尺寸返回 false，插件启动失败而不是静默零检出。
5. **INT8 输出的分数不是 ONNX 的原样浮点**：RKNN 工具链默认把 15 个输出张量也量化成 INT8，运行时按 scale/zero-point 反量化。实测与本机 ONNX fp32 的分数差 ≤0.005（见 `rk3568/QUANTIZATION.md`），但跨版本的分数绝对一致性不保证。
