# ppe_helmet — 安全帽穿戴检测算法包

本包基于 **DAMO-YOLO-S**（`iic/cv_tinynas_object-detection_damoyolo_safety-helmet`），检测画面中的头部目标 `head_helmet`（`safety hat`）与 `head_nohelmet`（`no safety hat`），直接输出未佩戴安全帽人员的**头部框**。
一个包同时提供 `onnx`（x86_64）与 `rk3568`（aarch64 + RKNN/RGA）两档，不含任何宿主代码改动。

## 1. 输出语义

| 标签 | class_id | 何时输出 | 宿主行为 |
| --- | --- | --- | --- |
| `no_helmet` | 1 | 检测到 `head_nohelmet`，且连续 `confirm_frames` 帧确认 | 命中 `alarmKeywords`，产生告警抓拍 |
| `helmet` | 0 | 仅 `report_compliant=true` 时，检测到 `head_helmet` | 命中 `nonAlarmExactLabels`，只抓拍、不告警 |

- 框是**头部框**（DAMO-YOLO-S 仅检测头部，不依赖人体框）。
- 同一头部（IoU ≥ 0.50）同时命中 `head_helmet` 与 `head_nohelmet` 时，取分数高者；平票（分数差 ≤ 1e-4）按 `no_helmet`（安全侧）。
- 每个 `task_id` 独立做 IoU 跟踪；违规连续 `confirm_frames` 帧才上报，合规帧清零。
- `helmet` **不走**上述时序确认：本帧有头部证据就输出一次，宿主侧若要按人去重需自行处理。

宿主侧无需改动：`no_helmet` 已在 `internal/record/service.go` 的
`alarmKeywords` 中，`helmet` 已在 `nonAlarmExactLabels` 中。

## 2. 模型与输入

| 项 | 值 |
| --- | --- |
| 检测器 | DAMO-YOLO-S（`TinyNAS_res` + `GiraffeNeckV2` + `ZeroHead`），2 类 |
| 类别 | `head_helmet`(0, `safety hat`)、`head_nohelmet`(1, `no safety hat`) |
| 输入 | RGB，512×288，保持 `[0, 255]` 量纲（不除 255），letterbox 补边 114 |
| 输出头 | `cls_8/reg_8`、`cls_16/reg_16`、`cls_32/reg_32` |
| 解码 | `cls_*` 为模型内已 sigmoid 概率；`reg_*` 为 17 bin（`reg_max=16`）DFL softmax 期望，锚点无 `+0.5` 偏移 |
| NMS | 按 `class_id` 分组，IoU 0.50 |
| 权重 | `models/ppe_helmet.onnx` / `models/ppe_helmet.rknn`（INT8） |

上游模型在 Roboflow `PPE Detection v3`（23000+ 图，10 epochs）上报告 mAP50 0.735，
其中 `head_helmet` 0.866、`vest` 0.935；本包不使用 vest/gloves/mask 等类别。
RKNN 档量化校准集包含 `ppe.jpg` 与 19 张 COCO 图，`score_*` 的 INT8 上限约 0.78；
若换成纯 COCO 校准，板端置信度会饱和在 0.5 附近——量产换模型时必须保留现场场景图。

`sample.jpg` 为 960×640，左侧边缘有一名未戴安全帽的工人，是本包的端到端冒烟样本。

## 3. 配置

两档共用同一份 `config.schema.json`：

| 键 | 默认 | 范围 | 说明 |
| --- | --- | --- | --- |
| `score_threshold` | 0.40 | 0.05–0.95 | `head_helmet` / `head_nohelmet` 置信度阈值 |
| `nms_threshold` | 0.50 | 0.10–0.90 | 同类 NMS 的 IoU 阈值 |
| `min_head_height_ratio` | 0.02 | 0.005–0.50 | 小于画面高度该比例的头部不参与研判 |
| `report_compliant` | `false` | bool | 是否额外输出 `helmet` 头部框（调试/画框用） |
| `confirm_frames` | 2 | 1–10 | 违规连续确认帧数；单帧侧脸/逆光容易误报 |

- 进程级 `--config '<json>'` 设置默认值；任务级 `params.json` 按 `task_id` 覆盖。
- 默认 0.25 是为了召回 `sample.jpg` 边缘工人（person 0.269 / head_nohelmet 0.301）；
  现场若误报偏多，优先上调到 0.30–0.35，再观察召回。
- 单帧调试（`--verify` / `devtest`）建议显式 `{"confirm_frames": 1}`，否则首帧只建 track、不出结果。

## 4. 构建与测试

### 4.1 x86_64 / ONNX

```bash
make sdk-install
make -C packages/ppe_helmet/onnx test    SDK_PREFIX=$PWD/build/sdk/prefix
make -C packages/ppe_helmet/onnx verify  SDK_PREFIX=$PWD/build/sdk/prefix
make -C packages/ppe_helmet package TARGET=onnx
```

- `test` 先跑单元测试，再用 `sample.jpg` 跑 `devtest`，期望 1 个 `no_helmet`。
- `verify` 是 ABI 生命周期冒烟；骨架只解 PPM/PGM，传 `sample.jpg` 会得到
  `sample_decoded=false`。真实检出由 `devtest` 或板端 `--verify --sample sample.ppm` 证明。
- `package` 会构建入口、过符号门禁、组装扁平目录、跑黑盒验包，并要求 `devtest`
  在 `sample.jpg` 上至少检出 1 个违规，否则拒绝打包。

### 4.2 RK3568 / RKNN

```bash
make sdk-cross-install
make -C packages/ppe_helmet package TARGET=rk3568
```

默认使用：

- SDK：`build/sdk/rk3568`
- 工具链：`build/native/cross-rk3568-rknn-rga/toolchain.cmake`
- 厂商库：`native/vendor/rknpu2`、`native/vendor/rga`

缺少时显式传：

```bash
make -C packages/ppe_helmet package TARGET=rk3568 \
  SDK_PREFIX=$PWD/build/sdk/rk3568 \
  TOOLCHAIN=$PWD/build/native/cross-rk3568-rknn-rga/toolchain.cmake \
  RKNN_ROOT=$PWD/native/vendor/rknpu2 \
  RGA_ROOT=$PWD/native/vendor/rga
```

交叉构建只编译、不在宿主执行；产物 `NEEDED` 为 `librknnrt.so` 与 `librga.so.2`，
与 RK3568 板端 `/usr/lib/aarch64-linux-gnu/` 下的运行库一致。

## 5. 板端部署与验证

```bash
scp build/packages/ppe_helmet-aarch64-rk3568.tar.gz root@192.168.19.71:/opt/zhulong/
ssh root@192.168.19.71
mkdir -p /opt/zhulong/ppe_helmet_test
tar xzf /opt/zhulong/ppe_helmet-aarch64-rk3568.tar.gz -C /opt/zhulong/ppe_helmet_test
cd /opt/zhulong/ppe_helmet_test
chmod +x ppe_helmet

./ppe_helmet --verify --sample sample.jpg          # ABI 冒烟，sample_decoded=false
ZHULONG_PPE_MODEL_DIR=$PWD ./ppe_helmet_devtest \
  --sample sample.jpg --out /tmp/ppe.jpg            # 真实检出（stb 解 JPEG）
```

本仓库在 RK3568 上的实测（`sample.ppm` 为 OpenCV 正确解码的 RGB 图）：

| 档 | 目标 | 数量 | score | cost_ms |
| --- | --- | --- | --- | --- |
| RKNN INT8 | `no_helmet` | 1 | 0.254984 | 32 |
| ONNX | `no_helmet` | 1 | 0.268933 | 67–170 |

> `--verify` 只解 PPM/PGM。用 ffmpeg 默认参数把 `sample.jpg` 转 PPM 会产生 1–2 级色彩偏移，
> 本样本的边缘人员分数会掉到 0.25 以下，表现为 `count=0`；这不是模型/包的问题。
> 需要可复现的 PPM 时用 OpenCV 手动写：

```bash
python3 - <<'PY'
import cv2
im = cv2.imread('sample.jpg')
with open('sample.ppm', 'wb') as f:
    f.write(f'P6\n{im.shape[1]} {im.shape[0]}\n255\n'.encode())
    f.write(im[:, :, ::-1].tobytes())   # OpenCV BGR -> PPM RGB
PY
```

## 6. 交付物

```text
build/packages/ppe_helmet-x86_64-onnx.tar.gz      13 MB
build/packages/ppe_helmet-aarch64-rk3568.tar.gz    5.2 MB
```

归档根目录（扁平，无平台子目录）：

```text
config.schema.json
manifest.json
models/ppe_helmet.onnx | models/ppe_helmet.rknn
ppe_helmet           # 0755，静态链接 SDK 运行时
sample.jpg
```

通过宿主 `POST /api/v1/algorithms/install`（算法包安装接口/页面）上传即可；
`manifest.platform.target` 必须与宿主 `[algorithm].target` 一致。

本包按包内入口独立构建/测试，不修改根 Makefile：

- `make -C packages/ppe_helmet/onnx test SDK_PREFIX=$PWD/build/sdk/prefix`
- `make -C packages/ppe_helmet package TARGET=onnx|rk3568`
- 如需手工核对两档源码一致性：`python3 native/scripts/check_package_drift.py packages/ppe_helmet`。

## 7. 已知限制

- **只研判安全帽**。vest、gloves、glasses、face_mask 等类别模型能预测，但本包不消费；
  需要“全套 PPE”时按同样的关联模式扩展，而不是改输出标签。
- **依赖人体检测**。人员框漏检、遮挡、背身、只露半身时不会输出违规；没有头框时按
  `unknown` 处理，不会“无头即违规”。
- **小目标**：`min_person_height_ratio` 默认 0.08，远景小人直接跳过；调低会增加误报。
- **精度**：上游 mAP50 0.735，属于通用 PPE 模型；工地逆光、反光背心、安全帽颜色与
  训练集差异大时，建议用现场图重新校准/微调，并保持 `ppe.jpg` 这类场景图在量化校准集里。
- **时序**：只有 `confirm_frames` 的帧级确认 + IoU 跟踪，没有跨镜头 re-ID；
  人员快速交叉时可能短暂断轨。
- **量化差异**：RKNN INT8 与 ONNX 的分数/框会有小幅差异（本样本 0.255 vs 0.269），
  验收以板端为准。
- `--verify` 不支持 JPEG；`sample.jpg` 仅用于 `devtest` 和宿主展示。

## 8. 来源与许可

- 上游模型工程：[hafizqaim/Workspace-Safety-Detection-using-YOLOv8](https://github.com/hafizqaim/Workspace-Safety-Detection-using-YOLOv8)，
  数据集 Roboflow `PPE Detection v3`（`ppe detection.v3i.yolov8`）。
- 模型导出、RKNN 转换与量化脚本来自 Rockchip
  [rknn_model_zoo `yolov8_ppe`](https://github.com/airockchip/rknn_model_zoo/tree/main/examples/yolov8_ppe)（Apache-2.0）。
- `sample.jpg`：Wikimedia Commons，
  [10-01-19 Mauricio Macri- Recorrido por obra de av. Patricios](https://commons.wikimedia.org/wiki/File:10-01-19_Mauricio_Macri-_Recorrido_por_obra_de_av._Patricios_(4291254782).jpg)，
  摄影 Sandra Hernández / Gobierno de la Ciudad de Buenos Aires，CC BY 2.0，仅作演示输入。
