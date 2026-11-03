# PRD: 任务级前置裁剪区域 (Crop Region)

> 一句话：裁剪是**分析视图**的属性，不是算法的参数。宿主让 AI 流只发布一个窗口，
> 算法包对此完全无感，结果由宿主逆映射回全幅坐标。

## Goal

在任务（= 一路 camera）上配置**唯一一处物理裁剪窗口**。宿主的 AI 分析流在发布前把该窗口物化为一张
紧打包的完整图像，算法包因此获得更高的有效分辨率（远景小目标、局部车牌/人脸）；模型输出的窗口局部
坐标由宿主在结果入口逆映射回全幅归一化坐标，抓拍、告警、ROI/MASK、预览与前端渲染全部无感。

**算法包零改动、零重建，SDK/ABI 不变。**

---

## Background & 现状事实

| 编号 | 事实 | 证据 |
| :--- | :--- | :--- |
| **F1** | ROI/MASK 是推理**之后**的逻辑过滤，只能减少误报，不能提升清晰度 | `internal/region/filter.go`（按底边中心点判定） |
| **F2** | 任务与相机一对一，`camera_id` 唯一；`roi`/`mask` 按算法存、`crop` 按任务存，粒度不同是语义正确的 | `000013_rebuild_channel_tasks.up.sql:24`、`internal/task/model.go:54` |
| **F3** | **AI 流是每相机一条独立物理流**（`aiStreams`，ConsumerAI 拉流 + 每流一个 FrameBroker），预览/录像走主码流 | `internal/camera/stream_hub.go:76-82`、`native/src/pipeline/engine.cpp`（每流 broker） |
| **F4** | 现有算法包以"帧就是相机画面"为前提：**8 个源文件**用 `raw_view()` 取源几何（face/plate/crowd 的 detector、aligner、pipeline） | `packages/face_recognition/*/src/detection/detector.cpp:317-335`、`.../alignment/aligner.cpp:227`、`packages/plate_recognition/onnx/src/detection/plate_detector.cpp:67`、`packages/crowd_gathering/onnx/src/pipeline/crowd_gathering.cpp` |
| **F5** | 模型输入是静态尺寸；算法端已有 letterbox | `native/sdk/include/zhulong/runtime/vision_backend.hpp` |
| **F6** | 全系统坐标契约是严格归一化 `[x1,y1,x2,y2] ∈ [0,1]`，下游按全幅解释 | `native/sdk/include/zhulong/plugin.h`、`internal/record/snapshot.go` |
| **F7** | 宿主的"平台无关"= 单产物 + 运行期探测，不是"没有硬件代码"：MPP 解码/JPEG 都以 dlopen 装载 | `native/src/nodes/decode/mpp_loader.cpp:59`、`native/CMakeLists.txt:144-148` |
| **F8** | AI 流的帧分发报文可前缀兼容扩展（`decode_frame_dispatch` + `LEGACY_FRAME_DISPATCH_SIZE`），但本方案**不需要**改它 | `native/sdk/src/internal/motion_gate.hpp:36-55` |

**关键结论（F4）**：任何"让算法进程自己裁剪"的方案都会让 `Image::width()` 与 `raw_view().width`
变成两套基准，而现网 8 个文件正是混着用的。裁剪必须发生在算法进程**之前**，这样
`width/height/planes/strides` 一起变小，所有既有代码天然正确。

---

## Requirements

### R1. 数据契约

- `analysis_tasks.crop_json TEXT NOT NULL DEFAULT '{}'`（新增列，一对迁移）。
- 内容 `{"crop": [x1, y1, x2, y2]}` 或 `{}`；`null` / 缺省 / `[]` / `{}` 一律等价于「不裁剪」。
- 只允许 **1 处**矩形；坐标严格归一化 `0 <= x1 < x2 <= 1`，最小边长 `MinCropSide = 0.02`（不复用
  ROI/MASK 的 `0.01`：窗口会被算法端放大到模型输入，太小只剩无意义插值）。
- `crop == 整帧` 归一化为「不裁剪」，不产生运行时分支。
- `roi`/`mask` 保持现状（`task_algorithms.region_config_json`），形状不变。

### R2. AI 流视图（宿主，唯一做像素的地方）
- 裁剪在 **AI 流的发布路径**上物化一次：`FramePublisher` 取窗口快照 → 运动检测（窗口内）→
  `FrameBroker` 把窗口写成**紧打包**的一帧 → 广播。客户端看到的是一张完整的小图。
- 窗口几何是 `(归一化矩形, 帧尺寸, 像素格式)` 的**纯函数**：
  `floor` 起点 + `ceil` 终点，**一律向外取偶**（4:2:0 必需，其他格式也无害）。
  **不含任何硬件对齐**——硬件对齐是拷贝实现的内部细节，绝不改变几何
  （RGA 不接受时退回 CPU 行拷贝，窗口一模一样）。
- 窗口由引擎计算并回报（`set_view` 的 out 参数 / `get_view`），Go 只保存回报值，不自己算像素。
- 流不存在时返回 `NOT_FOUND`，**不得顺手拉起拉流**（`ResolveAIStreamID` 会 acquire，禁止在保存路径使用）。
- 分辨率未知时返回 `NOT_READY`：配置已保存但暂不生效，宿主在首个结果批次前用 `get_view` 收敛。
- 生效范围只有 AI 流：预览、录像、抓拍历史仍拿全幅帧。
- **只改 crop 不重启算法进程**：任务更新时跳过算法子树未变化的包重启通知，只刷新视图。

### R3. 坐标逆映射（宿主）

- 被裁剪的流，所有结果都是窗口局部归一化坐标；宿主在**唯一结果入口**（`StreamHub.consumeAIResults`）
  用 `E` 线性逆映射：`g = E.origin + c * E.span`，再 clamp 到 `[0,1]`。
- 覆盖全部坐标字段（detections / persons / faces / vehicles / plates / landmarks）。
- 换窗边界用结果自带的 `capture_time_ms` 与本地改窗时间戳判定，保留上一窗口即可精确处理在途帧。
- 下游（ROI/MASK、抓拍、告警、轨迹、前端）继续吃全幅坐标，零改动。

### R4. 透明性与门禁

- 运动门禁天然变成"窗口内有运动"（检测跑在窗口上），现存"窗口外运动放行整帧"的限制消失。
- 抓拍历史 ring 保留全幅帧（`FramePublisher::push_frame`），AC6 由构造保证。
- 算法包**不重建**：它们只是收到一张更小的帧；`sdk_version`、`IPC_VERSION`、`zhulong_frame_view`
  全部不变。插件契约只需补一句"帧视图可能是裁剪后的分析视图，像素阈值以 `image.width()/height()` 为基准"。

### R5. 前端交互

- 区域弹窗新增**任务级 CROP 区块**（不随选中算法切换），ROI/MASK 仍跟随算法；同画布叠加、色相区分。
- 自由比例框选；单选互斥（画新框即替换）；一键清除恢复全图。
- 色相取 `cyan`（`CATEGORY_HUES` 现成，不占 teal/amber/destructive 状态色）。
- 最小边长用独立的 `MIN_CROP_SIDE = 0.02`，不复用 ROI/MASK 的 `0.01`（否则"画得出来、后端 400"）。
- 切换选中算法时 crop 不丢失或重置（属于任务，不属于算法），需测试钉住。

---

## 关键技术决策

| 决策点 | 选定方案 | 理由 |
| :--- | :--- | :--- |
| **裁剪执行点** | **宿主 AI 流（媒体层）** | 算法进程只看得到窗口，8 个 `raw_view()` 文件天然正确；包内执行会引入双基准且静默错框 |
| **下发通道** | **不需要**（几何随帧分发隐含） | 客户端从 dispatch 的 `width/height/strides/offsets` 就能看到窗口；没有控制消息就没有缓存过期/能力协商/重启 |
| **像素成本** | 每流每帧一次窗口写（RGA 或行拷贝） | 与 N 个算法无关；客户端帧变小，总带宽反而下降 |
| **逆映射位置** | **Go，结果入口单点** | 一处实现、表驱动测试；不必为每种结果类型维护 C++ 字段清单 |
| **旧包策略** | **天然支持，不重建、不告警、不门禁** | 旧包拿到的是小图，坐标语义一致；`supports_crop`/`MSG_STREAM_REGION` 全部不存在 |
| **几何唯一性** | 引擎算窗口并回报 `E`，Go 只存 | 三段（像素窗口 / 归一化 E / 逆映射）由同一个返回值串起来 |
| **硬件对齐** | 不进几何；RGA 不行就 CPU | 避免"物化重试/外扩 64px"整类问题，AC4/AC11 由构造保证 |
| **热更新** | 下一帧生效 | 窗口是流状态，帧边界原子切换；无需重启算法进程 |
| **分类色** | `cyan` | `CATEGORY_HUES` 无 amber 槽位，分类色不得占用状态色 |

---

## Acceptance Criteria

- [ ] **AC1 契约兼容**：未配置 crop 的任务，引擎视图未激活，帧分发与今天逐字节一致（复用现有 broker 测试）。
- [ ] **AC2 唯一性与校验**：DB/API/前端都只允许 1 个矩形；越界/退化/NaN/`<0.02` 被拒绝并返回稳定错误码
      `INVALID_CROP_CONFIG`；`null`/`[]`/`{}` ≡ 不裁剪；更新时"字段缺省"不得清空。
- [ ] **AC3 模型输入**：配置 crop 后，模型输入张量内容 == 窗口内容（合成图案定位断言），尺寸仍为模型要求。
- [ ] **AC4 坐标逆映射**：4 类结果（detection/person/vehicle/scene）的 box、body_box、vehicle_box、plate box、
      landmarks 全部映射；窗口像素误差 ≤ 1 px；越界 clamp 到 `[0,1]`。
- [ ] **AC5 与 ROI/MASK 协同**：`crop + roi + mask` 的过滤结果 == `roi + mask`（全局坐标）。
- [ ] **AC6 抓拍对齐**：抓拍 JPEG 尺寸 == 相机分辨率，框在全幅图上位置正确。
- [ ] **AC7 前端编辑与回显**：绘制/替换/清除/重开回显正确；切换选中算法时 crop 不丢。
- [ ] **AC8 质量门禁**：`make check` 全绿。
- [ ] **AC9 旧包零改动**：未修改的现有算法包（face/plate/crowd）在裁剪流上直接产出正确的全幅坐标（e2e）。
- [ ] **AC10 窗口一致性**：`set_view`/`get_view` 回报的 `E` == 引擎实际像素窗口 == Go 逆映射使用的 `E`。
- [ ] **AC11 降级不改几何**：RGA 不可用/拒绝时 CPU 行拷贝的输出与 RGA **逐字节相同**（同格式纯拷贝）。
- [ ] **AC12 热切换与清除**：运行中改窗口下一帧生效；清除后下一帧恢复全幅；算法包重启后仍是当前窗口（无缓存残留）。

---

## Out of Scope

- **多窗口 / 级联裁剪 / per-algorithm 覆盖**：一路 camera 只有一个窗口（取景是相机属性）。
- **自动目标跟踪裁剪（Auto-tracking/PTZ-crop）与物理云台**。
- **窗口内的二次缩放策略**：仍由算法包自己的 letterbox 决定，宿主只做窗口裁剪。
- **窗口内多区域运动门禁**：门禁只回答"窗口内是否有运动"，不做分区。
- **音频轨**：AI 流目前只有视频轨。
