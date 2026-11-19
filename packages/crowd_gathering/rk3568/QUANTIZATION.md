# 量化与精度 — crowd_gathering / rk3568

本档位只交付一个权重。转换证据（源 ONNX 哈希、opset、`rknn.config` 参数、复现命令）见
`CONVERSION.md`，本文件只谈精度策略与实测。

## 每个模型的精度模式

| 模型 | 交付文件 | 精度模式 | 校准集 | 工具链 | 备注 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `scrfd_person_2.5g` | `models/scrfd_person_2.5g.rknn`<br>1,310,432 B<br>`e9edaf70…a85c6f` | **INT8**（w8a8，`do_quantization=True`） | `build/calib/crowd_640x384`，23 张 | rknn-toolkit2 2.3.2（`bd980be9`） | 输入归一化在模型内；15 个输出张量默认也量化成 INT8，运行时按 scale/zero-point 反量化 |

量化是**每个模型**的属性，不是档位属性：若将来再加一个人体模型，可以单独选 FP16 而不动这个。
FP16 对照模型只用于评估，留在 `build/models/scrfd_person_2.5g_fp.rknn`，不进交付包。

## 校准集

`build/calib/crowd_640x384/`（由 `scripts/convert_scrfd_person.py dataset` 生成，不进版本库）：

- 20 张来自 rknn_model_zoo `datasets/COCO/subset`（与 zoo 的 `dataset.txt` 同源），覆盖室内外、单人/多人、远近景；
- 3 张来自本仓库真实监控截帧：`packages/crowd_gathering/onnx/sample.jpg`、
  `packages/ppe_helmet/onnx/sample.jpg`、`packages/face_recognition/onnx/sample.jpg`。

每张都先按**运行期同样的语义** letterbox 成 640×384（等比缩放 + 居中 + 114 灰边，RGB），
再交给校准器。这一点很关键：如果拿原始 16:9 图直接校准，NPU 看到的分布和线上不一致
（补边像素占比不同），近景人体的分数会更容易饱和。

23 张对 2.5 GMACs 的小模型够用；numpy 侧没有做随机增广 —— 增广会改变 letterbox 统计，
反而偏离线上分布。

## 板端数值验证（2026-11-19，192.168.19.71）

同一张 `sample.jpg`（640×443，6 个检出：1 个聚集告警 + 5 个人体），走完整插件链路：

| 后端 | 检出数 | 5 个人体分数 | 单帧 cost_ms |
| :--- | ---: | :--- | ---: |
| ONNX fp32（x86_64 宿主，ONNX Runtime） | 6 | 0.720 / 0.709 / 0.696 / 0.646 / 0.576 | 56.7 |
| **RKNN INT8（板端 NPU）** | 6 | 0.724 / 0.713 / 0.696 / 0.641 / 0.579 | **26.2** |
| RKNN FP16（板端 NPU） | 6 | 0.721 / 0.712 / 0.697 / 0.649 / 0.577 | 65.7 |

- 分数最大偏差 **0.005**，框坐标偏差 ≤0.005（归一化，640 宽下约 3 px）；检出集合完全一致。
- INT8 比 FP16 快 **2.5×**，所以交付选 INT8。FP16 在这颗 NPU 上反而最慢（fp16 路径没有
  专门的加速，且权重需要按 fp16 打包）—— 别被「低精度一定快」的直觉带走。
- 运行时版本：`librknnrt.so` 2.3.2（`429f97ae6b@2025-04-09`）、驱动 RKNPU 0.9.8、RGA 1.10.1、
  内核 5.10.226。**工具链与板端 runtime 同为 2.3.2**，这是能直接复用模型的前提；版本错位时
  先对齐再谈精度。

判定影响：`score_threshold` 默认 0.5，0.005 的偏移不改变告警/非告警结论。

## 已知偏差与噪声

1. **输出张量也被量化**：工具链提示
   `The default output dtype of '<id>' is changed from 'float32' to 'int8'`。这是 RKNN 的默认行为
   （输出也走 INT8），运行时靠 `want_float=1` 用模型内 scale/zp 反量化。上面的实测说明精度够用；
   但**不要**把 rknn 分数与 onnx 分数当作同一物理量做绝对比较（两档位共用阈值 0.5 是可以的）。
2. **`E RKNN: ... Unkown op target: 0` ×5**（每个 stride 组一条）：640×640 的对照转换同样打印 5 条，
   与本档位把高度改成 384 无关，属工具链通用日志噪声。升级工具链时复查这条是否消失。
3. **没有逐层精度报告**：2.3.2 的 `accuracy_analysis()` 需要单独跑一遍（会额外花几分钟），当前
   精度证据是端到端对照而非逐层余弦相似度。若后续出现召回下降，第一步跑
   `rknn.accuracy_analysis(inputs=[...], output_dir=...)` 定位是哪个 stride 组掉点。
4. **校准集不含长尾场景**（夜间、逆光、雨雾、密集人群）。COCO subset 的夜间样本很少；园区夜班
   场景若召回不足，优先扩充校准集重转，而不是先调阈值 —— 阈值调低会把影子/反光也召回来。

## 顺带：`manifest.json` 的 compute_cost 为什么是 30

宿主按 `Σ compute_cost × sample_fps ≤ 1000` 做算力预算（`internal/task/store.go` 的
`Points = compute_cost * sample_fps`，`internal/task/service.go` 的 `MaxComputePoints = 1000`）。
30 的依据是板上端到端 `cost_ms`：

- 本档位 26.2 ms/帧（NPU，含 letterbox、解码、聚类、时序）；
- onnx 档位 20 的参照值是 56.7 ms/帧（CPU），NPU 时间与 CPU 时间不是同一资源 —— NPU 在 RK3568 上
  是**独占串行**的，多算法并发时排队，所以按「单帧 NPU 占用 + 与 face（85）共存仍有余量」取值；
- 5 fps 采样下本包 150 点，与 face_rk3568 的 85×5=425 点共存时合计 575/1000，留了余量给第三人。

若后续在同一台设备上并发更多 NPU 算法、实测出现周期性掉帧，再按实测排队时间上调 —— 这个值是
调度权重，不是延迟指标。

