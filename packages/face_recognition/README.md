# face_recognition — 人脸识别与人像关联算法包

基于 SCRFD 人体/人脸级联检测 + 几何拓扑硬过滤 + Umeyama 5 点仿射对齐 + **eDifFIQA(T) 轻量级神经网络人脸质量评估** + 可选图像清晰度/曝光门禁 + FaceLiVTv2-L 512 维特征提取与身份级 1:N 比对的边缘监控人脸识别算法包。
当前档位：`onnx`（x86_64 宿主推理）与 `rk3568`（RK3568 RKNN NPU 推理）。

## 目录

```
packages/face_recognition/
├── README.md                 本文件（包级单份说明）
├── onnx/                     x86_64 ONNX Runtime 档位
│   ├── manifest.json         包元信息（id / version / platform.target=onnx）
│   ├── config.schema.json    宿主校验与前端渲染用的配置契约
│   ├── models/               det_500m / scrfd_person_2.5g / ediffiqa_t / facelivtv2-l (.onnx)
│   ├── sample.jpg            开发期冒烟样本帧
│   ├── src/                  算法源码（common / detection / alignment / quality / feature / matching / pipeline）
│   ├── tests/                包内无模型自测（aligner_matcher_test）+ 开发期出图（dev_infer）
│   └── scripts/build_package.py
└── rk3568/                   RK3568 RKNN 档位（与 onnx 共享通用算法源码，受漂移门禁约束）
    ├── manifest.json         包元信息（platform.target=rk3568）
    ├── config.schema.json    与 onnx 档位逐字节同步的配置契约
    ├── models/               对应 .rknn 量化权重（含 ediffiqa_t.rknn INT8）
    └── ...
```

两档位通用源码一致性由仓库根 `make pkg-drift-check`（`native/scripts/check_package_drift.py`）守卫；`config.schema.json` 及白名单内的 `src/detection/detector.cpp`、`src/quality/quality_assessor.cpp`、`src/feature/extractor.cpp`、`src/pipeline/face_recognition.cpp` 修改时必须双端人工核对。

## 开发命令

```bash
# 包内无模型数学/纯逻辑自测（几何质检、图像门禁、对齐、身份聚合比对、SCRFD 解码、场景裁剪）
make -C packages/face_recognition/onnx unit-test

# 真实 ONNX 权重单图推理冒烟 → build/out/result-face_recognition-onnx.jpg
make -C packages/face_recognition/onnx test

# 多档位源码漂移门禁与双端 schema 一致性检查
make pkg-drift-check
diff -u packages/face_recognition/onnx/config.schema.json packages/face_recognition/rk3568/config.schema.json

# 算法包独立打包（在 packages/face_recognition 目录或其 Makefile 中）
make package TARGET=onnx     # 或 python3 scripts/build_package.py --target onnx
make package TARGET=rk3568   # 或 python3 scripts/build_package.py --target rk3568
```

`unit-test` 与 `test` 需要已安装 SDK 前缀（`make sdk-install`）及 `ONNXRUNTIME_ROOT`（或 `BUNDLE_RUNTIME=1`）。构建 `rk3568` 需要板端 SDK 前缀（`make sdk-cross-install`），在宿主机上**只编译不执行**，真实板端耗时与验包需在硬件上运行 `make board-check`。

## 算法链路

1. **人体与人脸检测（分离式 SCRFD 解码预算）**
   - 流式默认人脸检测输入为 **640×384（16:9 CCTV 档位）**；人体检测在 `onnx` 为 640×384，在 `rk3568` 因静态量化形状保持 640×640（人体模型缺失时自动退化为纯人脸语义）；宿主 `--extract-feature` 底库登记使用 640×640。
   - `scrfd_decode.hpp` 分离 **pre-NMS 候选保留预算** 与 **post-NMS 单帧目标上限**：`onnx` 档位为 `512 / 128`，`rk3568` 档位为 `256 / 128`（`rk3568` 预算在 Cortex-A55 上的后处理耗时**待板端实测**）。这样既避免单张近景人脸触发大量高分重复 anchor 在 NMS 前挤掉同帧其他人脸，又把每帧最昂贵的 512 维特征提取次数硬封顶在 128 次。
   - 小顶堆淘汰比较器与最终排序同时引入网格扫描序号 `seq`，保证超预算等分候选在多次运行间逐字节确定性一致。
   - 解码分**两趟**：第一趟全网格只读 score、把 `(score, seq, 位置)` 压进定长堆；第二趟才对 ≤pre-NMS 预算的幸存者做 letterbox 逆变换与 5 点还原。几何非法的候选会占一个预算名额并在第二趟被丢弃（预算同时是内存上界，因此不回补）。宿主实测：12800 个 anchor 全过阈值的病态密集帧，后处理从 545 µs 降到 101 µs，稀疏帧不变（36 µs）；`rk3568` 仍需板端实测。
2. **像素局部坐标系几何与拓扑硬过滤（`src/common/face_quality.hpp`）**
   - 在等距原图像素空间建立眼线有向正交基 `u = (re - le) / ||re - le||`、`v = (-u.y, u.x)`，执行零成本（`< 1 μs`）物理尺寸与拓扑前置硬拒绝：非有限输入、零/负面积框、低于 `min_face_size`、眼距塌缩（`< 0.12 × min_dim`）、眼嘴高度塌缩或倒置（`< 0.15 × min_dim`）、左右嘴角手性交叉、鼻尖越出眼嘴合理区间（`r_pitch <= 0` 或 `>= 1.10`）以及飞出框外远端的离群关键点直接拦下，避免无效废框触发后续对齐与模型推理。
   - 综合质量打分已升级交由第 3 步 `eDifFIQA(T)` 神经网络模型承担，规则几何分不再用于硬拦截常规侧脸或轻微遮挡目标。
3. **5 点相似变换对齐、eDifFIQA(T) 质量评估与可选图像门禁（`src/alignment/` + `src/quality/` + `src/common/face_image_quality.hpp`）**
   - Umeyama 最小二乘求解相似变换矩阵，双线性采样输出 112×112 RGB24 对齐脸块。
   - **流式图像质量门禁（`enable_face_image_gate`，默认关闭）**：跳过对齐块 1px 外边框后，在 `110×110` 内部像素上计算 BT.601 亮度 4 邻域拉普拉斯方差（`< 112px` 小脸按 `|det(affine)|` 补偿），同时统计暗部欠曝比例（`Y < 32`）与亮部过曝比例（`Y > 224`）。门限参数非法时放行，对齐块本身非法时拒绝。
   - **eDifFIQA(T) 深度学习人脸质量评估（`src/quality/quality_assessor.*`）**：与 FaceLiVTv2-L 共用同一 `112×112` RGB24 对齐脸块（ONNX 侧归一化为 `(x - 127.5) / 127.5`，RKNN 侧均值/方差已内置于 `.rknn`），输出 `[0.0, 1.0]` 质量分并受 `min_face_quality` 门控（默认 `0.28`）。
     - **流式识别**：低于 `min_face_quality` 在特征提取前直接拦截，节省 512 维特征抽取算力；通过后得分写入 `FaceItem.quality` 并作为跨帧特征融合权重。
     - **底库登记（`--extract-feature`）**：同步接入 `eDifFIQA(T)` 质量门禁，低于 `min_face_quality` 时以帧级 `NO_FACE_DETECTED` 拒绝低质登记照（热进程不退出）。
     - 注：`rk3568` 档位当前打包 `ediffiqa_t.rknn`（INT8 量化，1.9 MB；相比 ONNX/FP16 分值存在约 `0.01~0.02` 系统下偏，板端耗时待实测）。
4. **特征抽取、按流跨帧特征融合与身份级 1:N 比对（`src/common/face_feature_fusion.hpp` + `src/matching/matcher.*`）**
   - FaceLiVTv2-L 输出 512 维单位特征向量；流式路径在 `enable_face_feature_fusion=true`（默认开启）时按 `task_id` 隔离维护最多 32 条活跃状态池，对当帧合格人脸与活跃状态做一对一贪心互斥配对。
   - **池满时的腾位规则**：同帧人数超过 32 时优先淘汰**本帧未被命中**的状态；若 32 个状态本帧全部在用，则不为新面孔建状态（该脸照常输出单帧特征、`is_fused=false`）。绝不拆掉刚更新过的状态——否则那张脸的 `is_fused` 会每帧 1/0 抖动、融合永远攒不起来。
   - **无效观测不落池**：空指针/含非有限分量/零模长的特征一律标记 `FusedFaceOutput::valid=false` 并由调用侧整条丢弃；直通（关闭融合）路径共用同一判定。零向量若被送进比对，只会得到「相似度 0 的伪候选人证据」。
   - **双重同人门禁 + 质量加权防漂移**：观测特征与状态的当前融合特征及历史最高质量锚点特征的余弦相似度均须 `>= fusion_similarity_threshold`（默认 `0.62`）才允许融合；以质量分加权累加（累计权重软上限 `6.0`）并严格重做 L2 单位归一化，融合帧数 `>= 2` 时置 `is_fused = true`。
   - **按唯一 `person_id` 聚合候选**：同一登记人员名下多张底库模板只占用 1 个 `top_k` 名额，取其最高分模板为代表并保留 `face_id`；两层确定性 tie-break 与宿主 `person.SearchByImage` 对齐——同人等分取字典序较小 `face_id`，跨人等分取较小 `person_id`（再按 `face_id`、槽位下标）。
   - **不同身份分差门禁（`min_match_margin`）**：与输出 `top_k` 解耦，若底库存在第二名不同身份，则要求 `best_score - second_person_score >= min_match_margin` 才置 `matched = true`（底库仅含单一身份时不受分差阻断）。
5. **人体关联与 `filter_unmatched` 场景裁剪（`src/common/face_association.hpp` + `scene_filter.hpp`）**
   - 通过 IoF `>= 0.50` 且人脸中心落在人体框上半部把人脸挂载到人体目标；开启 `filter_unmatched` 时过滤未确认身份的人脸与未挂载已确认人脸的人体，并同步重映射 `person_index`。

## 配置参数与三层生效优先级

参数支持三层生效优先级：
**任务级 JSON（Web 界面/API，最高） > 进程级 `--config` > 算法包 `.env` 环境变量 > 编译期默认值（最低）**。
越界或类型非法字段自动安全回落到上一层默认（详见 `src/common/match_config.hpp`），同时保留历史别名兼容（`match_threshold` → `threshold`、`det_threshold` → `face_det_threshold`、`face_quality_threshold` → `min_face_quality`）。

### 1. Web 界面常用参数（`config.schema.json` 动态表单直接渲染）

| 键名 | 界面标题 | 默认值 | 区间 | 说明 |
| :--- | :--- | ---: | :--- | :--- |
| `threshold` | 人脸比对阈值 | 0.40 | 0.0 – 1.0 | 识别严格程度，越高越不易误识，越低越不易漏检 |
| `face_det_threshold` | 人脸检测阈值 | 0.50 | 0.1 – 1.0 | 人脸检出灵敏度，逆光或远距可调低，杂乱背景可调高 |
| `person_det_threshold` | 人体检测阈值 | 0.50 | 0.1 – 1.0 | 人体检出灵敏度，用于人脸与人体的关联绑定 |
| `min_face_size` | 最小人脸尺寸 | 32 | 16 – 512 | 过滤远景过小人脸，低于此像素尺寸不进行识别 (单位: 像素) |
| `filter_unmatched` | 忽略陌生人 | false | 布尔 | 开启后仅记录已登记人员，未比中人员不产生抓拍和记录 |
| `top_k` | 候选人员数量 | 3 | 1 – 20 | 识别命中时展示的最高相似度候选人数上限 |
| `enable_face_feature_fusion` | 多帧特征融合 | true | 布尔 | 自动融合连续多帧特征，显著提升走近与轻微侧脸时的识别效果 |

### 2. 高阶/专家调优参数（收归算法包 `.env` 或任务 JSON 源码模式）

日常现场部署保持默认即可。如需深度微调，可在算法包根目录下配置 `.env` 文件（参考 `.env.example`）或在任务对话框中切换为“JSON 源码”模式配置：

| 键名 | 对应环境变量 | 默认值 | 区间 | 说明 |
| :--- | :--- | ---: | :--- | :--- |
| `min_face_quality` | `ZHULONG_MIN_FACE_QUALITY` | 0.28 | 0.0 – 1.0 | `eDifFIQA(T)` 模型质量分门槛（`0.0` 仅关闭质量分阈值，几何/拓扑硬拒仍生效） |
| `fusion_similarity_threshold` | `ZHULONG_FUSION_SIMILARITY_THRESHOLD` | 0.62 | 0.45 – 0.95 | 跨帧判定同人并允许融合的最小余弦相似度（同时校验当前融合向量与最高质量锚点） |
| `fusion_ttl_seconds` | `ZHULONG_FUSION_TTL_SECONDS` | 3.0 | 0.5 – 30.0 | 单路特征状态池未再次观测到同人时的过期淘汰秒数 |
| `min_match_margin` | `ZHULONG_MIN_MATCH_MARGIN` | 0.00 | 0.0 – 1.0 | 第一与第二不同身份的最小相似度分差（`0.0` 表示关闭；开启前须按底库规模与现场样本标定） |
| `enable_face_image_gate` | `ZHULONG_ENABLE_FACE_IMAGE_GATE` | false | 布尔 | 流式图像清晰度与曝光门禁总开关（默认关闭） |
| `min_face_sharpness` | `ZHULONG_MIN_FACE_SHARPNESS` | 30.0 | 0.0 – 100000.0 | 仅在 `enable_face_image_gate=true` 时生效；上采样补偿后拉普拉斯方差下限 |
| `max_face_underexposure` | `ZHULONG_MAX_FACE_UNDEREXPOSURE` | 0.50 | 0.0 – 1.0 | 仅在 `enable_face_image_gate=true` 时生效；内部 `Y < 32` 像素占比上限 |
| `max_face_overexposure` | `ZHULONG_MAX_FACE_OVEREXPOSURE` | 0.30 | 0.0 – 1.0 | 仅在 `enable_face_image_gate=true` 时生效；内部 `Y > 224` 像素占比上限 |

## 重要契约与运维须知

1. **新增门禁默认关闭，占位阈值必须离线标定后再启用**：
   `enable_face_image_gate` 与 `min_match_margin` 默认均为关闭状态，不改变既有任务行为。`min_face_sharpness=30.0`、`max_face_underexposure=0.50`、`max_face_overexposure=0.30` 仅为占位默认值，受镜头焦距、ISP 降噪强度、补光条件影响很大；且当前 `FaceItem` ABI 不携带图像清晰度/曝光字段、宿主也不落盘插件日志正文（仅统计 `message_bytes`）。上线开启前请提取现场相机抓拍帧做离线分布标定。关闭图像门禁请将 `enable_face_image_gate` 设为 `false`，**切勿**把 `max_face_underexposure` / `max_face_overexposure` 填成 `0.0`（`0.0` 代表不允许任何暗/亮饱和像素的最严上限）。
2. **流式抓拍候选"不过滤" vs 宿主以图搜人"按阈值过滤"**：
   C++ 流式推理输出的 `FaceItem.candidates` **故意不按 `threshold` 截断过滤**——当目标未命中（`matched=false`）时，抓拍记录仍携带底库中最接近的 Top-K 唯一身份及相似度，便于安保事后核查"最像谁"；而宿主 Go 侧 `person.SearchByImage` 接口面向主动检索，候选列表按阈值过滤。两者语义差异是有意设计。
3. **`filter_unmatched` 与 `min_match_margin` 同时开启的丢弃行为**：
   当 `min_match_margin > 0` 时，即使第一名得分 `>= threshold`，只要与第二名不同身份的分差 `< min_match_margin`，该目标就会被判为未确认身份（`matched=false`）。若同时开启 `filter_unmatched=true`，此类"高分但分差不足的疑似目标"会被 `should_emit_face_result` 及场景裁剪**整体丢弃，不产生任何抓拍图片、入库记录或 `candidates` 证据**。若业务现场希望保留疑似人员抓拍供人工复核，请保持 `filter_unmatched=false`。
4. **已知边界**：
   - `sample.jpg` 单帧冒烟（`make test`）仅验证模型加载与推理链路可用，不代表真实 CCTV 场景识别率。
   - 宿主解码器与共享内存帧的 YUV 色彩空间历史契约问题跨 SDK/引擎边界，不在本算法包内部单侧修改范围内。
