# face_recognition — 人脸识别与人像关联算法包

基于 SCRFD 人体/人脸级联检测 + 像素局部坐标系几何质检 + Umeyama 5 点仿射对齐 + 可选图像清晰度/曝光门禁 + FaceLiVTv2-L 512 维特征提取与身份级 1:N 比对的边缘监控人脸识别算法包。
当前档位：`onnx`（x86_64 宿主推理）与 `rk3568`（RK3568 RKNN NPU 推理）。

## 目录

```
packages/face_recognition/
├── README.md                 本文件（包级单份说明）
├── onnx/                     x86_64 ONNX Runtime 档位
│   ├── manifest.json         包元信息（id / version / platform.target=onnx）
│   ├── config.schema.json    宿主校验与前端渲染用的配置契约
│   ├── models/               det_500m / scrfd_person_2.5g / facelivtv2-l (.onnx)
│   ├── sample.jpg            开发期冒烟样本帧
│   ├── src/                  算法源码（common / detection / alignment / extraction / matching / pipeline）
│   ├── tests/                包内无模型自测（aligner_matcher_test）+ 开发期出图（dev_infer）
│   └── scripts/build_package.py
└── rk3568/                   RK3568 RKNN 档位（与 onnx 共享通用算法源码，受漂移门禁约束）
    ├── manifest.json         包元信息（platform.target=rk3568）
    ├── config.schema.json    与 onnx 档位逐字节同步的配置契约
    ├── models/               对应 .rknn 量化权重
    └── ...
```

两档位通用源码一致性由仓库根 `make pkg-drift-check`（`native/scripts/check_package_drift.py`）守卫；`config.schema.json` 及白名单内的 `src/detection/detector.cpp`、`src/pipeline/face_recognition.cpp` 修改时必须双端人工核对。

## 开发命令

```bash
# 包内无模型数学/纯逻辑自测（几何质检、图像门禁、对齐、身份聚合比对、SCRFD 解码、场景裁剪）
make -C packages/face_recognition/onnx unit-test

# 真实 ONNX 权重单图推理冒烟 → build/out/result-face_recognition-onnx.jpg
make -C packages/face_recognition/onnx test

# 多档位源码漂移门禁与双端 schema 一致性检查
make pkg-drift-check
diff -u packages/face_recognition/onnx/config.schema.json packages/face_recognition/rk3568/config.schema.json
```

`unit-test` 与 `test` 需要已安装 SDK 前缀（`make sdk-install`）及 `ONNXRUNTIME_ROOT`（或 `BUNDLE_RUNTIME=1`）。交叉档位 `rk3568` 在宿主机上**只编译不执行**，真实板端耗时与验包需在硬件上运行 `make board-check`。

## 算法链路

1. **人体与人脸检测（分离式 SCRFD 解码预算）**
   - 流式默认人脸检测输入为 **640×384（16:9 CCTV 档位）**；人体检测在 `onnx` 为 640×384，在 `rk3568` 因静态量化形状保持 640×640（人体模型缺失时自动退化为纯人脸语义）；宿主 `--extract-feature` 底库登记使用 640×640。
   - `scrfd_decode.hpp` 分离 **pre-NMS 候选保留预算** 与 **post-NMS 单帧目标上限**：`onnx` 档位为 `512 / 128`，`rk3568` 档位为 `256 / 128`（`rk3568` 预算在 Cortex-A55 上的后处理耗时**待板端实测**）。这样既避免单张近景人脸触发大量高分重复 anchor 在 NMS 前挤掉同帧其他人脸，又把每帧最昂贵的 512 维特征提取次数硬封顶在 128 次。
   - 小顶堆淘汰比较器与最终排序同时引入网格扫描序号 `seq`，保证超预算等分候选在多次运行间逐字节确定性一致。
   - 解码分**两趟**：第一趟全网格只读 score、把 `(score, seq, 位置)` 压进定长堆；第二趟才对 ≤pre-NMS 预算的幸存者做 letterbox 逆变换与 5 点还原。几何非法的候选会占一个预算名额并在第二趟被丢弃（预算同时是内存上界，因此不回补）。宿主实测：12800 个 anchor 全过阈值的病态密集帧，后处理从 545 µs 降到 101 µs，稀疏帧不变（36 µs）；`rk3568` 仍需板端实测。
2. **像素局部坐标系几何与拓扑质检（`src/common/face_quality.hpp`）**
   - 在等距原图像素空间建立眼线有向正交基 `u = (re - le) / ||re - le||`、`v = (-u.y, u.x)`，消除全图分辨率、画面长宽比与滚转对偏航/俯仰评分的非预期影响。
   - 按滚转稳定的 `min(box_w, box_h)` 归一退化判据：非有限输入、零/负面积框、低于 `min_face_size`、眼距塌缩（`< 0.12 × min_dim`）、眼嘴高度塌缩或倒置（`< 0.15 × min_dim`）、左右嘴角手性交叉、鼻尖越出眼嘴合理区间（`r_pitch <= 0` 或 `>= 1.10`）以及飞出框外远端的离群关键点一律硬拒绝；普通大侧脸（鼻尖投影越过单侧眼角/嘴角）偏航分量置 0 走连续软评分衰减，不因绝对值反向反弹。
3. **5 点相似变换对齐与可选流式图像质量门禁（`src/alignment/` + `src/common/face_image_quality.hpp`）**
   - Umeyama 最小二乘求解相似变换矩阵，双线性采样输出 112×112 RGB24 对齐脸块。
   - **仅在流式识别路径**（`FaceRecognitionPlugin::extract_qualified_face`）提供由 `enable_face_image_gate` 控制的图像质量门禁（不影响底库照片登记）：跳过对齐块 1px 外边框后，在 `110×110` 内部像素上计算 BT.601 亮度 4 邻域拉普拉斯方差，并按 `sharpness = var_patch × max(1.0, |det(affine)|)` 仅对 `< 112px` 远距小脸补偿双线性上采样导致的方差稀释（`>= 112px` 近景清晰大脸不折减）；同时按严格不等式统计暗部欠曝比例（`Y < 32`）与亮部过曝比例（`Y > 224`）。
   - 门禁的放行/拒绝分两类：**门限参数非法（NaN/负值）时放行**（配置越界已由 `resolve_match_params` 的区间钳制兜住，再 fail-closed 只会让整路静默无人脸），**对齐块本身非法（空缓冲/尺寸过小/仿射退化）时拒绝**。
4. **特征抽取、按流跨帧特征融合与身份级 1:N 比对（`src/common/face_feature_fusion.hpp` + `src/matching/matcher.*`）**
   - FaceLiVTv2-L 输出 512 维单位特征向量；流式路径在 `enable_face_feature_fusion=true`（默认开启）时按 `task_id` 隔离维护最多 32 条活跃状态池，对当帧合格人脸与活跃状态做一对一贪心互斥配对。
   - **池满时的腾位规则**：同帧人数超过 32 时优先淘汰**本帧未被命中**的状态；若 32 个状态本帧全部在用，则不为新面孔建状态（该脸照常输出单帧特征、`is_fused=false`）。绝不拆掉刚更新过的状态——否则那张脸的 `is_fused` 会每帧 1/0 抖动、融合永远攒不起来。
   - **无效观测不落池**：空指针/含非有限分量/零模长的特征一律标记 `FusedFaceOutput::valid=false` 并由调用侧整条丢弃；直通（关闭融合）路径共用同一判定。零向量若被送进比对，只会得到「相似度 0 的伪候选人证据」。
   - **双重同人门禁 + 质量加权防漂移**：观测特征与状态的当前融合特征及历史最高质量锚点特征的余弦相似度均须 `>= fusion_similarity_threshold`（默认 `0.62`）才允许融合；以质量分加权累加（累计权重软上限 `6.0`）并严格重做 L2 单位归一化，融合帧数 `>= 2` 时置 `is_fused = true`。
   - **按唯一 `person_id` 聚合候选**：同一登记人员名下多张底库模板只占用 1 个 `top_k` 名额，取其最高分模板为代表并保留 `face_id`；两层确定性 tie-break 与宿主 `person.SearchByImage` 对齐——同人等分取字典序较小 `face_id`，跨人等分取较小 `person_id`（再按 `face_id`、槽位下标）。
   - **不同身份分差门禁（`min_match_margin`）**：与输出 `top_k` 解耦，若底库存在第二名不同身份，则要求 `best_score - second_person_score >= min_match_margin` 才置 `matched = true`（底库仅含单一身份时不受分差阻断）。
5. **人体关联与 `filter_unmatched` 场景裁剪（`src/common/face_association.hpp` + `scene_filter.hpp`）**
   - 通过 IoF `>= 0.50` 且人脸中心落在人体框上半部把人脸挂载到人体目标；开启 `filter_unmatched` 时过滤未确认身份的人脸与未挂载已确认人脸的人体，并同步重映射 `person_index`。

## 配置参数

所有键均支持进程级（`--config`）默认与任务级（`MSG_TASK_CONFIG`）按需覆盖；越界或类型非法字段自动回退到上一层默认（详见 `src/common/match_config.hpp`）。同时保留历史任务 JSON 别名兼容：`match_threshold` → `threshold`、`det_threshold` → `face_det_threshold`、`face_quality_threshold` → `min_face_quality`。

| 键 | 默认 | 区间 | 说明 |
| :--- | ---: | :--- | :--- |
| `threshold` | 0.40 | 0.0 – 1.0 | 1:N 余弦相似度命中阈值（`>=` 命中） |
| `top_k` | 3 | 1 – 20 | 按唯一 `person_id` 聚合返回的候选人员上限 |
| `face_det_threshold` | 0.50 | 0.1 – 1.0 | SCRFD 人脸检出置信度阈值 |
| `person_det_threshold` | 0.50 | 0.1 – 1.0 | SCRFD 人体检出置信度阈值 |
| `min_face_size` | 32 | 16 – 512 | 人脸框短边最小像素尺寸硬门槛 |
| `min_face_quality` | 0.40 | 0.0 – 1.0 | 几何与姿态综合质量分门槛（`0.0` 仅关闭软分阈值，拓扑/非有限硬拒仍生效） |
| `filter_unmatched` | false | 布尔 | 仅输出 `matched=true` 的已确认人员，过滤陌生人与未确认目标 |
| `enable_face_image_gate` | false | 布尔 | 流式图像清晰度与曝光门禁总开关（**默认关闭**） |
| `min_face_sharpness` | 30.0 | 0.0 – 100000.0 | 仅在 `enable_face_image_gate=true` 时生效；上采样补偿后拉普拉斯方差下限（**占位默认值，须现场标定**） |
| `max_face_underexposure` | 0.50 | 0.0 – 1.0 | 仅在 `enable_face_image_gate=true` 时生效；内部 `Y < 32` 像素占比上限（`0.0` 为最严，非关闭哨兵） |
| `max_face_overexposure` | 0.30 | 0.0 – 1.0 | 仅在 `enable_face_image_gate=true` 时生效；内部 `Y > 224` 像素占比上限（`0.0` 为最严，非关闭哨兵） |
| `min_match_margin` | 0.00 | 0.0 – 1.0 | 第一与第二不同身份的最小相似度分差（`0.0` 表示关闭；**开启前须按底库规模与现场样本标定**） |
| `enable_face_feature_fusion` | true | 布尔 | 流式跨帧质量加权特征融合开关（**默认开启**，关闭时立即清空该路历史状态） |
| `fusion_similarity_threshold` | 0.62 | 0.45 – 0.95 | 跨帧判定同人并允许融合的最小余弦相似度（同时校验当前融合向量与最高质量锚点） |
| `fusion_ttl_seconds` | 3.0 | 0.5 – 30.0 | 单路特征状态池未再次观测到同人时的过期淘汰秒数 |

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
