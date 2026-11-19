# scrfd_person_2.5g：ONNX → RKNN（rk3568，640×384）

本档位只打包一个权重 `models/scrfd_person_2.5g.rknn`。源模型是 rknn_model_zoo 的
`examples/scrfd_person_2.5g/model/scrfd_person_2.5g.onnx`，与本仓库
`packages/crowd_gathering/onnx/models/scrfd_person_2.5g.onnx` 为同一份文件（md5
`2e19c0119309b45204f66e9701093f5c`）。

## 为什么在转换时把输入钉死成 640×384

zoo 的 ONNX 输入是**动态** `[1,3,?,?]`，ONNX 档位靠 `--config` 在运行期改分辨率。
RKNN 走不了这条路：

- `.rknn` 是静态图，转换时就必须给出具体 shape；
- Zhulong 的 RKNN 后端按 `element_count` 校验输入
  （`native/src/backends/rknn/rknn_inference_backend.cpp`），模型与运行期尺寸不一致时
  `forward` 失败 → 表现为**静默零检出**。

所以转换时固定 `[1,3,384,640]`，运行期 `PersonDetector::set_input_size` 只放行
`kStaticModelWidth=640` / `kStaticModelHeight=384`（见 `src/detection/person_detector.hpp`），
其它尺寸让插件启动失败（`ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT`）。CCTV 标准 16:9 输入
进 640×384 时 letterbox 无需补边，代价更低。

**改分辨率 = 重新转换权重**，不是改配置。

## 归一化位置

`(x - 127.5) / 128`、RGB 顺序，由 `rknn.config(mean_values=[[127.5]*3], std_values=[[128.0]*3])`
写进模型、在 NPU 内执行。**应用侧不做归一化**，只做 letterbox（等比缩放 + 114 灰边、居中）并喂
HWC uint8 —— 见 `src/detection/person_detector.cpp` 的 `preprocess_detector_input()`。
`114` 与 `native/src/algorithm/runtime/letterbox_target.hpp` 的 `letterbox_pad_value` 一致。

## 证据

| 项 | 值 |
| :--- | :--- |
| 源模型 | `../onnx/models/scrfd_person_2.5g.onnx`（3,710,223 B） |
| 源模型 SHA-256 | `76522ba15eecb0712780509e912884aba066e9834be0c85761918cdcf76de5b5` |
| IR / opset | 6 / 11（producer `pytorch 1.7`） |
| 源输入 | `input.1`，`[1,3,?,?]`，FLOAT |
| 源输出 | 15 × FLOAT（5 个 stride × score/bbox/kps） |
| 工具链 | rknn-toolkit2 **2.3.2**（commit `bd980be9`），Python 3.10，onnx 1.16.2 |
| `target_platform` | `rk3568` |
| `load_onnx` | `inputs=['input.1']`，`input_size_list=[[1,3,384,640]]` |
| 归一化 | mean `[127.5,127.5,127.5]` / std `[128.0,128.0,128.0]` |
| 量化 | INT8（`do_quantization=True`），校准集见 `QUANTIZATION.md` |
| 产物 | `models/scrfd_person_2.5g.rknn`（1,310,432 B） |
| 产物 SHA-256 | `e9edaf701198ab0b06f5331a3e64ab3cc3fae14441f5e1eb1dc7b0d0b7a85c6f` |
| 运行时 | 板端 `librknnrt.so` 2.3.2（`429f97ae6b@2025-04-09`），与工具链同版本 |

## 复现

在仓库根目录，用装了 rknn-toolkit2 2.3.2 的 Python 环境（本仓库的临时环境是
`build/tools/rknn-venv`，不进版本库）：

```bash
# 1) 校准集：按运行期前处理（letterbox 640×384，114 灰边，RGB）生成
python3 packages/crowd_gathering/rk3568/scripts/convert_scrfd_person.py dataset \
    --images /home/niko/dev/rknn_model_zoo/datasets/COCO/subset \
             packages/crowd_gathering/onnx/sample.jpg \
             packages/ppe_helmet/onnx/sample.jpg \
             packages/face_recognition/onnx/sample.jpg \
    --out-dir build/calib/crowd_640x384

# 2) 转换
python3 packages/crowd_gathering/rk3568/scripts/convert_scrfd_person.py build \
    --quant int8 --dataset build/calib/crowd_640x384/dataset.txt
```

脚本会打印源模型与产物的 SHA-256，可直接与本文件核对。脚本默认从
`packages/crowd_gathering/onnx/models/` 取 ONNX、写到本档位的 `models/`；`--out` 可改路径
（例如把 FP16 对照模型放到 `build/models/`，避免混进交付包）。
