# CCTV 人脸包优化设计

## Boundary

行为缺口分别位于包内几何评分、流式 `build_face_item`、Matcher 和 SCRFD 解码。
只在这些行为的归属位置修复，不在 SDK、Go 或前端增加拦截。

新代码必须是**头文件**：包内 `make unit-test` 只链 `aligner.cpp + matcher.cpp`，
`native/CMakeLists.txt` 的 `ZhulongFaceGeometryTests` 编的是同一份清单。新增 `.cpp`
会让两个入口都链接失败；确实需要时，两处都要登记。同时，流式门禁与过滤决策须抽出
纯头文件函数供 `build_face_item` 与 `unit-test` 共用，避免单测在测试文件里重写 `if` 自证。

## Geometric Quality

- 入口先完成完整有限性校验：`image_width > 0`、`image_height > 0`、`face_box[0..3]` 全部有限且
  `norm_w > 0`、`norm_h > 0`、`face_score` 有限且 `> 0`、`min_face_quality` 有限；通过 `pixel_landmarks`
  取像素五点并逐点校验有限。避免 `std::clamp(NaN, 0, 1)` 保留 `NaN` 并在 `min_face_quality <= 0`
  短路分支下把 `NaN` 漏进 `FaceItem.quality`。
- **必须先在像素空间旋转**：归一化坐标的 x/y 尺度不同（`x/W`、`y/H`），在该空间做旋转
  等于把画面按长宽比拉斜。像素空间是等距的：以双眼连线单位向量 `u = (re − le) / ‖re − le‖`
  为局部横轴，其顺时针正交向量 `v = (−u.y, u.x)`（图像坐标系下指向嘴部方向）为局部纵轴。
- **有向偏航对称比（防越界反弹）**：在 `u` 轴上取有向差 `d_le = (nose − le)·u`、`d_re = (re − nose)·u`
  （嘴部同理 `d_lm = (nose − lm)·u`、`d_rm = (rm − nose)·u`）。只有两端均为正（鼻尖投影落在两眼/两嘴角之间）
  才计算 `min / max`；一旦 `<= 0`（如 `≳48°` 侧脸三维鼻尖投影越过单眼，或异常点飞出外侧），该项对称比取 `0.0f`
  走软评分衰减，消除旧实现 `std::abs` 让框外远点反向回升趋近 `1.0` 的漏洞。
- 退化与拓扑硬拒按**滚转稳定的检测框尺度** `min_dim = min(pixel_width, pixel_height)` 归一
  （不用绝对像素、随图高变化的归一化长度，也不用随 90° 滚转互换的单轴 `box_w`/`box_h`）：
  - 硬拒：`eye_span < 0.12 × min_dim`；`face_h = (mouth_mid − eye_mid)·v < 0.15 × min_dim`；
    左右嘴角相对眼线反向或重合 `(rm − lm)·u <= 0`；鼻尖垂直位置拓扑越界 `r_pitch <= 0.0`（鼻在眼上）
    或 `r_pitch >= 1.10`（远低于嘴线，保留俯视 CCTV 大俯仰余量）〔拟定〕。
  - 边界物理含义：「眼线塌缩或五官手性/上下序自相矛盾」（`eye_span` 边界约对应 `|yaw| ≳ 75°`），
    **不是**普通侧脸（≈50°–60° 时 `eye_span ≈ 0.25–0.35 × min_dim`，即使鼻尖越过单眼使 `yaw_score = 0`，
    其余分量仍走连续软评分）。
  - 数值上必须钉一张 ~55° 侧脸仍通过、且 `min_face_quality` 降到 0 时也不被硬拒。
- 保留原尺寸、宽高比、综合权重。`aspect_score` 用检测框像素宽高比，**本来就不是滚转不变**，
  这是设计选择，不是缺陷；验收要求姿态分量与拓扑退化结论滚转不变。
- 对未旋转正常正脸输入，局部坐标比值与原像素等价（现有 `test_face_quality_assessment` 正脸/小脸/倒立/畸变期望保持兼容，
  倒立用例同时从 `pass=true` 收紧为 `pass=false`）。真正消除的误拒是旧 `face_h <= 0.005f` 那条全图高度归一化台阶——
  1080p 全幅是 5.4px、4K 是 10.8px，而宿主 AI 视图裁剪到 640×360 时只剩 1.8px（见 algo-plugin-contract §6.7）。

## Aligned RGB Quality

- 新增 `common/face_image_quality.hpp`（纯标准库依赖，符合 algo-plugin-contract §4.1 的 `src/common/` 规则），
  仅读紧凑 RGB 脸块（`crop_and_align_direct` 恒输出 RGB，YUV 源已在 `sample_pixel_rgb` 内完成换算），
  无额外堆分配、无第三方依赖。
- 防御式入口：`rgb == nullptr`、`width < 3`、`height < 3`（跳过 1px 边框后内部像素数为 0）、
  `affine_det` 非有限或 `|affine_det| <= 0` 时直接返回无效/未通过。
- 亮度：`Y = 0.299R + 0.587G + 0.114B`（BT.601，浮点；`RGB=(32,32,32)` 与 `(224,224,224)` 在 IEEE-754 含 FMA 下均精确等于 `32.0f`/`224.0f`）。
- 清晰度：4 邻域 `∇² = 4c − (上+下+左+右)`，只统计内部像素（跳过 1px 边框，`N = (w−2)(h−2)`），
  用 `double` 累加 `sum` 与 `sum_sq` 计算总体方差（`÷N`）并钳制非负，避免纯色块 `float` 灾难性相消产生微负值。
- **仅对上采样做仿射行列式补偿**：`sharpness = var_patch × max(1.0f, |det(affine)|)`，其中 `det = m0·m4 − m1·m3`
  为 `src → dst(112×112)` 变换行列式（`s = 目标像素/源像素`，`|det| = s²`）。
  - 量纲与物理机制：光滑二维场的 `∇²` 幅值缩放 `s⁻²`、方差缩放 `s⁻⁴`，但人脸主要由一维五官边缘构成，
    固定传感器 PSF + 双线性上采样下边缘过渡带面积占比 `∝ s`，使 `< 112px` 小脸的 `var_patch` 经验上按 `s⁻² ~ s⁻³`
    衰减；乘以 `|det| = s²` 可消除 32px 远距小脸被 112×112 上采样系统性压低（避免退化成第二个 `min_face_size`）。
  - 下采样钳制（`|det| < 1` 取 `1.0`）：当源脸 `>= 112px`（如 200px～400px 近景大脸）时，`crop_and_align_direct`
    无预滤波采样已达 112×112 满分辨率，脸块对比度受像素量化与自然纹理上限约束，不再随 `s⁻²` 增长；若直接乘 `< 1`
    的 `|det|` 会将清晰近景脸打折 3×～13× 导致误拒。
  - 合成跨尺度测试须在**源图像素空间**施加固定半径模糊核（模拟相机失焦/运动 PSF），验证 32/64/200 三种尺度下清晰与模糊排序一致。
- 曝光：暗端比例 = `Y < 32` 的像素占比，亮端比例 = `Y > 224` 的占比〔拟定〕。
  边界为**严格不等**，`Y == 32` 计为非暗、`Y == 224` 计为非亮；分母是内部像素数 `N`。
- 参数（schema 与解析默认值两档位同步）：
  - `enable_face_image_gate`：bool，默认 `false`。**关闭语义只能有这一处开关**。
  - `min_face_sharpness`：`[0.0, 100000.0]`，默认 `30.0`〔占位，必须现场标定〕。显式给宽松上限，杜绝 JSON `double`
    窄化转型 `static_cast<float>` 溢出为 `+Inf`（C++ UB），同时让前端动态表单获得合法有界区间。
  - `max_face_underexposure` / `max_face_overexposure`：`[0.0, 1.0]`，默认 `0.5` / `0.3`〔占位〕。
  - 不用 `0`/`1` 分别充当"关闭"哨兵：对 max 类阈值 `0` 是最严，用户按直觉填 0 会拒掉全部。
- 提供头文件门禁判定函数（如 `passes_face_image_gate(rgb, w, h, affine_det, match_params)`），关闭开关时直接放行，
  由 `build_face_item` 与 `unit-test` 共用；指标独立于原几何综合分，保持 `FaceItem.quality` 契约，不改变登记流程。

## Calibration Contract

- 指标函数是纯头文件、无模型依赖，能被 `make unit-test` 与临时 harness 直接调用。
- 生产路径**不新增**每脸指标输出：`FaceItem` 契约不动，宿主的原生日志只保留 `message_bytes`
  （algo-plugin-contract §5.0.2），正文在运维侧看不到。因此现场标定只能在采集样本上离线做。
- 所以：默认关闭、阈值标为占位、文档写明"开箱即用不成立"，不承诺未经测量的识别率提升。

## Identity Matching

- 入口校验：`probe_features == nullptr`、`feature_dim == 0`、`gallery.empty()`、`!isfinite(threshold)`、
  `!isfinite(min_match_margin)`、或探针任一维 `!isfinite` 时，直接返回未匹配空结果（防止 `NaN` 门限绕过 `> 0` 分支）。
- 模板侧**不做逐元素扫描**：每模板算完 `dot` 后判 `std::isfinite(dot)`（IEEE-754 下模板含 `NaN` 或 `±Inf`——
  含 `0.0f * Inf = NaN`——均会使 `dot` 为 `NaN` 或 `±Inf`，一次判据全部拦截）。
  这一步同时保护排序：`std::clamp(NaN, 0, 1)` 返回 `NaN` 会破坏严格弱序导致 UB。
- 维度不符或 `person_id.empty()` 的畸形模板直接跳过（防止空 ID 聚合成假身份或产出 `matched=true` 但 `person_id=""`）。
- **零节点分配的人员级聚合**：不在每张脸上构造 `std::unordered_map`（万级底库会触发数千次链表节点 `new/delete`）。
  仅用单次 `reserve(gallery.size())` 的紧凑 `(index, score)` 数组建大顶堆（`std::make_heap` / `std::pop_heap`）
  或原地排序，依次提取不重复 `person_id` 直至拿到 `max(top_k, 2)` 个身份（`top_k <= 20` 时用栈/小数组线性去重，
  超界回退紧凑去重），最终只对入选的 `<= top_k` 项复制字符串。
- 同分 tie-break（对齐宿主 `person.SearchByImage` 的两层规则）：
  - **同人多模板选代表照**：相似度相等时取较小 `face_id`；
  - **跨身份候选排序**：相似度相等时取较小 `person_id`（若 `person_id` 相同则取较小 `face_id`，再以底库下标兜底构成严格全序）。
    万级底库中大量负内积会同被截断为 `0.0f`，跨人全序决胜是结果确定性的必要条件。
- 候选过滤语义：C++ 侧**保持不过滤匹配阈值**（抓拍候选是"证据"，未命中时也告诉宿主最像谁）；
  宿主以图搜人（`SearchByImage`）是按阈值过滤的"检索"。两者有意不同，写进包说明。
- `min_match_margin`：`[0.0, 1.0]`，默认 `0.0`（关闭），逐次调用入参（默认形参 `0.0f` 保持旧 5 参调用兼容），不存全局状态。
- 命中判据：`best_sim >= threshold` 且（`min_match_margin <= 0.0f` 或底库仅含单一有效身份 或
  `(best_sim - second_person_sim) >= min_match_margin`）。第二身份在聚合中独立取到，不受 `top_k=0/1` 截断影响。
- 拒识仍返回候选及最高相似度，不伪造确认身份（`person_id`/`person_name`/`matched_face_id` 留空，`matched=false`）。
- 在 `common/scene_filter.hpp`（或 `matching/matcher.hpp`）提供纯头文件准入函数（如
  `should_emit_face_result(const MatchResult&, const MatchParams&)`），封装 `!filter_unmatched || match_result.matched`，
  供 `build_face_item` 与 `unit-test` 共用。

## Bounded Detection Budget

- `ScrfdCandidate` 增加单调递增扫描序号 `seq`；**小顶堆比较器与最终排序同时纳入 `seq`**：
  - 小顶堆淘汰比较器：`(a.score != b.score) ? (a.score > b.score) : (a.seq < b.seq)`，保证满员时**低分或同分但较晚扫到**
    的候选位于 `top()` 优先被淘汰（留存集合严格确定，可先与 `top()` 比较再入堆避免多余调整）；
  - 最终排序：`(score 降序, seq 升序)` 已构成严格全序，直接用零额外缓冲的 `std::sort`。
- 分离 pre-NMS 候选上限与 post-NMS 目标上限，任一上限为 `0` 返回空；NMS 循环在 `kept.size() >= max_detections`
  时立即 `break`（贪心 NMS 已按序确定，无需继续计算尾部 IoU）。
- 预算**按档位给**（`detector.cpp` 已在漂移白名单里，允许不同）：
  - 拟定 pre-NMS：onnx `512` / rk3568 `256`；post-NMS 目标上限两档位都保持 `128`。
  - 理由：NMS 最坏 O(n²)，n=512 时约 13 万次 IoU/帧〔估算，未实测〕；真正昂贵的是 post-NMS 之后的每脸 512 维特征抽取，
    `128` 就是它的硬上界。
  - x86 上的 ONNX 耗时**不能**用来定 rk3568 的预算；本任务不执行交叉产物，板端预算取保守值并标注"待板端实测"。
- 人脸/人体检测器分别传参，两档位原有不同前处理、模型路径和形状保持不变。
- 内存：候选结构体含序号约 64B，512 条约 32KB，无增长型分配；`suppressed` 按候选数预留，`kept` 按 `min(candidates.size(), max_detections)` 预留。

## Expected Files

- 两档位 `src/common/face_quality.hpp`：像素局部有向坐标系、滚转稳定框尺度退化判据、有向偏航比与有限性/拓扑硬拒。
- 两档位 `src/common/face_image_quality.hpp`（新增，头文件）：亮度、清晰度（`max(1, |det|)`）、曝光统计与门禁函数。
- 两档位 `src/common/match_config.hpp`、`config.schema.json`：新参数、有界区间与解析默认值。
- 两档位 `src/common/scene_filter.hpp`：补充流式单脸比对结果准入纯函数（`filter_unmatched` 门禁共用）。
- 两档位 `src/matching/matcher.hpp` / `.cpp`：零节点分配身份聚合、分差、有限性与两层 tie-break。
- 两档位 `src/pipeline/face_recognition.cpp`：流式接入 `passes_face_image_gate`、`min_match_margin` 与 `should_emit_face_result`。
- 两档位 `src/detection/scrfd_decode.hpp`、`src/detection/detector.cpp`：pre/post-NMS 预算分离、堆+排序序号定序与提前终止。
- 两档位 `tests/aligner_matcher_test.cpp`：无模型回归（覆盖全部新增头文件与 Matcher 契约）。
- `packages/face_recognition/README.md`（新增，**包级单份**）：参数、候选语义、标定限制与组合开关行为。
  先例是 `packages/crowd_gathering/README.md`；漂移检查不比较 `.md`，两档位各写一份必然腐烂。
- 不调整 CMake。`tests/dev_infer.cpp` 与 `FaceItem` 输出契约不动。

## Compatibility

- 无 ABI、Go、数据库、模型或原默认阈值变化；新配置可选，缺失时图像门禁与分差关闭。
- 唯一人员 Top-K 是有意语义优化，每项仍携带代表照片 ID；记录于 schema 与包说明。
- 几何异常拒绝是修正，不允许通过关闭质量阈值绕过；但它只吃掉 `|yaw| ≳ 75°` 与五官手性/上下序自相矛盾，
  普通侧脸与俯视大俯仰正脸仍由连续软评分处理。
- ⚠️ **`filter_unmatched` × `min_match_margin`**：近分拒识 + 过滤未匹配 = 疑似本人
  **不入库、不抓拍、前端看不到**（`should_emit_face_result` 返回 false，`build_face_item` 丢弃）。
  这是两项同时开启时的必然结果，必须写进包说明；两者都不改变单独开启时的行为。

## 已决判据（评审结论）

| 位置 | 决定 |
| :--- | :--- |
| 退化与拓扑判据 | 按 `min(box_w, box_h)` 归一（滚转稳定）：`eye_span < 0.12×min_dim` / `face_h < 0.15×min_dim` / 嘴角交叉 / `r_pitch ∉ (0, 1.10)`；偏航用有向投影防反弹 |
| 图像门禁开关 | 单一 `enable_face_image_gate`（默认 `false`）；`min_face_sharpness ∈ [0, 100000]` 防 `float` 窄化溢出 |
| 清晰度尺度 | `var_patch × max(1, \|det(affine)\|)`（`double` 累加方差），只补偿 `< 112px` 上采样，不折减近景大脸 |
| 候选过滤与准入 | C++ 抓拍候选不过滤阈值；图像门禁与 `filter_unmatched` 准入抽为共用头文件函数供单测覆盖 |
| pre/post-NMS 预算 | pre-NMS：onnx `512` / rk3568 `256`；post-NMS：`128`（提前 `break`）；序号同时入小顶堆与 `std::sort` |
| tie-break 与聚合 | 同人代表照取较小 `face_id`，跨人候选取较小 `person_id`（对齐 `SearchByImage`）；堆/紧凑数组聚合，不用 `unordered_map` |
