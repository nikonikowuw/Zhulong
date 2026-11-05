# PRD: 任务级前置裁剪区域 (Crop Region)

> 一句话：裁剪是**分析视图**的属性，不是算法的参数。宿主让 AI 流只发布一个窗口，
> 算法包对此完全无感，结果由宿主按**引擎回报的窗口几何与生效起点**逆映射回全幅坐标。

> **评审修订（R3.1）**：边界时钟改为“视图快照点的 `capture_time_ms` + 引擎
> `effective_since_ms`”，不再依赖 Go 侧本地改窗时间；补首帧/重放时序、奇数尺寸
> 夹取、宿主 C ABI 措辞、迁移号与相关文件清单。算法包仍然零改动、零重建，
> 算法 SDK/IPC 不变；**宿主 C ABI 是向后兼容新增**。
>
> **评审修订（R3.2）**：窗口几何改以**解码帧尺寸**为基准（不是 RTSP 元数据），
> 修正迁移号（`000032`）、C ABI 最小边长阈值、错误码/哨兵、`ApplyTo` 错误出口、
> apply 串行化、通知 ctx 与前端保存门禁。

## Goal

在任务（= 一路 camera）上配置**唯一一处物理裁剪窗口**。宿主的 AI 分析流在发布前把该窗口物化为一张
紧打包的完整图像，算法包因此获得更高的有效分辨率（远景小目标、局部车牌/人脸）；模型输出的窗口局部
坐标由宿主在结果入口逆映射回全幅归一化坐标，抓拍、告警、ROI/MASK、预览与前端渲染全部无感。

**算法包零改动、零重建，算法 SDK/IPC 不变；宿主 `Zhulong` C ABI 新增视图接口。**

---

## Background & 现状事实

| 编号 | 事实 | 证据 |
| :--- | :--- | :--- |
| **F1** | ROI/MASK 是推理**之后**的逻辑过滤，只能减少误报，不能提升清晰度 | `internal/region/filter.go`（按底边中心点判定） |
| **F2** | 任务与相机一对一，`camera_id` 唯一；`roi`/`mask` 按算法存、`crop` 按任务存，粒度不同是语义正确的 | `000013_rebuild_channel_tasks.up.sql:24`、`internal/task/model.go:54` |
| **F3** | **AI 流是每相机一条独立物理流**（`aiStreams`，ConsumerAI 拉流 + 每流一个 FrameBroker），预览/录像走主码流；同一路物理 `Stream` 可能同时被 preview/recording 消费者持有 | `internal/camera/stream_hub.go:76-82`、`native/src/pipeline/engine.cpp`（每流 broker） |
| **F4** | 现有算法包以"帧就是相机画面"为前提：**8 个源文件**用 `raw_view()` 取源几何（face/plate/crowd 的 detector、aligner、pipeline） | `packages/face_recognition/*/src/detection/detector.cpp:317-335`、`.../alignment/aligner.cpp:227`、`packages/plate_recognition/onnx/src/detection/plate_detector.cpp:67`、`packages/crowd_gathering/onnx/src/pipeline/crowd_gathering.cpp` |
| **F5** | 模型输入是静态尺寸；算法端已有 letterbox | `native/sdk/include/zhulong/runtime/vision_backend.hpp` |
| **F6** | 全系统坐标契约是严格归一化 `[x1,y1,x2,y2] ∈ [0,1]`，下游按全幅解释 | `native/sdk/include/zhulong/plugin.h`、`internal/record/snapshot.go` |
| **F7** | 宿主的"平台无关"= 单产物 + 运行期探测，不是"没有硬件代码"：MPP 解码/JPEG 都以 dlopen 装载 | `native/src/nodes/decode/mpp_loader.cpp:59`、`native/CMakeLists.txt:144-148` |
| **F8** | AI 流的帧分发报文可前缀兼容扩展（`decode_frame_dispatch` + `LEGACY_FRAME_DISPATCH_SIZE`），但本方案**不需要**改布局；只把 `capture_time_ms` 的取值点前移到视图快照 | `native/sdk/src/internal/motion_gate.hpp:36-55`、`native/src/algorithm/ipc/ipc_protocol.hpp:126-140` |

**关键结论（F4）**：任何"让算法进程自己裁剪"的方案都会让 `Image::width()` 与 `raw_view().width`
变成两套基准，而现网 8 个文件正是混着用的。裁剪必须发生在算法进程**之前**，这样
`width/height/planes/strides` 一起变小，所有既有代码天然正确。

---

## Requirements

### R1. 数据契约

- `analysis_tasks.crop_json TEXT NOT NULL DEFAULT '{}'`（新增列，一对迁移）。
- 内容 `{"crop": [x1, y1, x2, y2]}` 或 `{}`；**存储/响应/引擎入口**里 `null` / 缺省 / `[]` / `{}`
  一律等价于「不裁剪」；`[0,0,1,1]`（整帧）统一归一化为「不裁剪」。
- **创建/按相机 upsert 请求**的 `crop` 形状是 `null` 或**恰好 4 个数字**的数组；长度 3/5、`[]`、`{}`
  都不是合法请求形状（400）。需要“显式清空”请走 `PUT /tasks/:id` 的三态语义。
- 只允许 **1 处**矩形；坐标严格归一化 `0 <= x1 < x2 <= 1`，最小边长 `MinCropSide = 0.02`（不复用
  ROI/MASK 的 `0.01`：窗口会被算法端放大到模型输入，太小只剩无意义插值）。
- 校验失败 → `apperr.Invalid("INVALID_CROP_CONFIG", …)`，不落库；三语文案加进
  `internal/task/locale.go`。
- DB 只负责存储 JSON，不提供“单矩形”的关系约束；“单矩形”由 API/前端/引擎入口共同保证。
- `roi`/`mask` 保持现状（`task_algorithms.region_config_json`），形状不变。

### R2. AI 流视图（宿主，唯一做像素的地方）

- 裁剪在 **AI 流的发布路径**上物化一次：`FramePublisher` 取窗口快照 → 运动检测（窗口内）→
  `FrameBroker` 把窗口写成**紧打包**的一帧 → 广播。客户端看到的是一张完整的小图。
- 窗口几何是 `(归一化矩形, 帧尺寸)` 的**纯函数**：`floor` 起点 + `ceil` 终点，**一律向外取偶**，
  并在取偶后**夹回帧边界**，保证奇数宽高也不会越界。像素格式只影响打包的 stride/offset，
  不参与窗口边界计算。这里的“帧尺寸”指**解码帧**的 `width/height`，不是 RTSP 元数据尺寸
  （`codecpar`）：两者在 1088 裁切、分辨率热切换、MPP 对齐等场景会不一致，而按元数据算 `E`
  会造成映射偏差，甚至每帧越界被 `fail_publish`。
  **不含任何硬件对齐**——硬件对齐是拷贝实现的内部细节，绝不改变几何
  （RGA 不接受时退回 CPU 行拷贝，窗口一模一样）。
- 窗口由引擎计算并回报 `E`，同时回报该视图的**生效起点** `effective_since_ms`：
  `capture_time_ms < effective_since_ms` 的帧属于上一视图，`>=` 的属于当前视图。
  Go 只保存引擎回报值，不自己算像素。`E` 的分子/分母永远等于实际切图所用的像素窗口与帧尺寸。
- `capture_time_ms` 由宿主在**取视图快照的同一原子点**生成（不是 broker 发布时），
  保证“像素用哪个窗口”与“结果用哪个 E”由同一个时间戳串起来。旧算法包已经会把这个字段
  原样回传，因此不需要重建。
- 流不存在时返回 `NOT_FOUND`，**不得顺手拉起拉流**（`ResolveAIStreamID` 会 acquire，禁止在保存路径使用）。
- 还没有解码帧（尺寸未知）时设置**非空**窗口返回 `NOT_READY`：配置已保存但暂不生效，首个解码帧
  后由 `get_view` 收敛；**清空窗口永远成功**，不受尺寸门禁影响。
- 生效范围只有 AI 流：预览、录像、抓拍历史仍拿全幅帧。
- **只改 crop 不重启算法进程**：任务更新时比较“除 crop 外的任务语义”，未变化则跳过算法子树重启通知，
  只刷新视图。

### R3. 坐标逆映射（宿主）

- 被裁剪的流，所有结果都是窗口局部归一化坐标；宿主在**唯一结果入口**（`StreamHub.consumeAIResults`）
  用引擎回报的 `E` 线性逆映射：`g = E.origin + c * E.span`，再 clamp 到 `[0,1]`。
- 选窗规则：`res.Timestamp`（由 `capture_time_ms` 解析）早于 `effective_since_ms` 用上一代
  `E`，否则用当前 `E`；**不使用 Go 侧的 `time.Now()` 改窗时间**。
- 覆盖全部坐标字段（detections / persons / faces / vehicles / plates / landmarks）；解析器会把
  `person_structure` 归一化为 `scene_structure` 并把 faces 提升到顶层，逆映射必须同时覆盖顶层
  `Faces`、`Persons[].BodyBox`、`Vehicles[].VehicleBox`、`Plates[].Box`，以及兼容路径里的嵌套
  `Persons[].Faces[]`。
- 下游（ROI/MASK、抓拍、告警、轨迹、前端）继续吃全幅坐标，零改动。

### R4. 透明性与门禁

- 运动门禁天然变成"窗口内有运动"（检测跑在窗口上），现存"窗口外运动放行整帧"的限制消失。
- 抓拍历史 ring 保留全幅帧（`FramePublisher::push_frame`），AC6 由构造保证。
- 算法包**不重建**：它们只是收到一张更小的帧；`sdk_version`、`IPC_VERSION`、`zhulong_frame_view`
  全部不变。插件契约只需补：帧视图可能是裁剪后的分析视图，像素阈值以 `image.width()/height()` 为基准，
  不得假设相机分辨率/宽高比，且必须原样回传 `capture_time_ms`。
- 视图属于物理 `Stream`，不是 AI 订阅。AI 订阅全部撤销但 preview/recording 仍持有同一个 `Stream`
  时，宿主在最后一次 AI 订阅释放时显式清空视图；下一次 AI acquire 必须重新应用当前任务的 crop。

### R5. 前端交互

- 区域弹窗新增**任务级 CROP 区块**（不随选中算法切换），ROI/MASK 仍跟随算法；同画布叠加、色相区分。
- 自由比例框选；单选互斥（画新框即替换）；一键清除恢复全图。
- 色相取 `cyan`（`CATEGORY_HUES` 现成，不占 teal/amber/destructive 状态色）。
- 最小边长用独立的 `MIN_CROP_SIDE = 0.02`，不复用 ROI/MASK 的 `0.01`（否则"画得出来、后端 400"）。
- 切换选中算法时 crop 不丢失或重置（属于任务，不属于算法），需测试钉住；切换相机时按新任务重新载入。
- **crop 的编辑门禁是“画面已出首帧”**（`surfaceState === 'live'`），与是否选中算法无关；
  画面未就绪时 crop 的编辑缓冲不得提交，必须提交已保存的基线值。
- **保存仍要求至少一个算法**（现有规则：无算法 = 删除任务），所以“无算法时也可查看/编辑 crop”
  不等于“可保存 crop”；删任务路径不得附带 crop。
- `saveChannelTask` 是整任务 upsert，crop 字段缺省会被解释为“清空”，所以调用方必须显式带上
  `crop: Rect | null`。

---

## 关键技术决策

| 决策点 | 选定方案 | 理由 |
| :--- | :--- | :--- |
| **裁剪执行点** | **宿主 AI 流（媒体层）** | 算法进程只看得到窗口，8 个 `raw_view()` 文件天然正确；包内执行会引入双基准且静默错框 |
| **下发通道** | **不需要控制消息**；几何随帧分发隐含 | 客户端从 dispatch 的 `width/height/strides/offsets` 就能看到窗口；没有控制消息就没有缓存过期/能力协商/重启 |
| **边界时钟** | **视图快照点的 `capture_time_ms` + 引擎 `effective_since_ms`** | 不依赖 Go 本地时间；旧包已回传 `capture_time_ms`；不改 IPC 布局 |
| **像素成本** | 每流每帧一次窗口写（RGA 或行拷贝） | 与 N 个算法无关；客户端帧变小，总带宽反而下降 |
| **逆映射位置** | **Go，结果入口单点** | 一处实现、表驱动测试；不必为每种结果类型维护 C++ 字段清单 |
| **旧包策略** | **天然支持，不重建、不告警、不门禁** | 旧包拿到的是小图，坐标语义一致，`capture_time_ms` 原样回传即可 |
| **几何唯一性** | 引擎按**解码帧尺寸**算窗口并回报 `E` + `effective_since_ms`，Go 只存 | 尺寸来源与像素来源同一；三段（像素窗口 / 归一化 E / 逆映射）由同一个返回值串起来 |
| **硬件对齐** | 不进几何；RGA 不行就 CPU | 避免"物化重试/外扩 64px"整类问题，AC4/AC11 由构造保证 |
| **热更新** | 下一帧生效；视图快照与 capture_time 原子 | 窗口是流状态，帧边界原子切换；无需重启算法进程 |
| **宿主 ABI** | 新增 `Zhulong_stream_set_view/get_view` + `Zhulong_view_state`，`ZHULONG_ABI_VERSION` +1 | 算法 SDK/IPC 不变，但宿主 C ABI 是新增符号，必须显式版本化 |
| **分类色** | `cyan` | `CATEGORY_HUES` 无 amber 槽位，分类色不得占用状态色 |

---

## Acceptance Criteria

- [ ] **AC1 契约兼容**：未配置 crop 的任务，引擎视图未激活；帧分发的字段、布局与内容路径与今天一致
      （`capture_time_ms` 仍取宿主时钟；因改为在视图快照点取值，毫秒值允许与旧实现的发布点有差异，
      不把该值当成 ABI 字节契约）。
- [ ] **AC2 唯一性与校验**：DB 只存储 JSON；写入路径/API/前端只允许 1 个矩形；越界/退化/NaN/`<0.02`
      被拒绝并返回稳定错误码 `INVALID_CROP_CONFIG`；宿主 C ABI 只做结构性校验（阈值权威在 Go）；
      **存储/响应/引擎入口**的 `null`/`[]`/`{}` ≡ 不裁剪；
      `PUT /tasks/:id` 的“字段缺省”不得清空，显式 `null`/`[]`/`{}` 才清空；创建/按相机 upsert
      请求只接受 `null` 或恰好 4 个数字的数组；整帧 `[0,0,1,1]` 归一化为无裁剪。
- [ ] **AC3 模型输入**：配置 crop 后，模型输入张量内容 == 窗口内容（合成图案定位断言），尺寸仍为模型要求。
- [ ] **AC4 坐标逆映射**：4 类结果（detection/person/vehicle/scene）的 box、body_box、vehicle_box、
      plate box、landmarks 全部映射；边界帧按 `capture_time_ms < effective_since_ms` 精确选上一代 E；
      窗口像素误差 ≤ 1 px；越界 clamp 到 `[0,1]`。
- [ ] **AC5 与 ROI/MASK 协同**：对**窗口内**目标，`crop + roi + mask` 的过滤结果 == `roi + mask`（全局坐标）；
      窗口外目标按设计不参与，测试目标全部落在窗口内。
- [ ] **AC6 抓拍对齐**：抓拍 JPEG 尺寸 == 相机分辨率，框在全幅图上位置正确。
- [ ] **AC7 前端编辑与回显**：绘制/替换/清除/重开回显正确；切换选中算法时 crop 不丢；
      画面未就绪时 crop 编辑缓冲不提交。
- [ ] **AC8 质量门禁**：`make check` 全绿。
- [ ] **AC9 旧包零改动**：未修改的现有算法包（face/plate/crowd）在裁剪流上直接产出正确的全幅坐标
      （e2e；依赖旧包原样回传 dispatch 的 `capture_time_ms`）。
- [ ] **AC10 窗口一致性**：`set_view`/`get_view` 回报的 `E`/`effective_since_ms` == 引擎实际像素窗口/切换点
      == Go 逆映射使用的 `E`/时间边界（`E` 以 float32 跨 ABI，像素断言用 ≤1px 容差）。
- [ ] **AC11 降级不改几何（第二刀验收）**：RGA 不可用/拒绝时 CPU 行拷贝的输出与 RGA
      **几何一致、内容一致**（同格式纯拷贝；以板端 spike 为准）。
      **第一刀只交付 CPU 路径**，本 AC 在 RGA 后端落地后生效；CPU 路径自身的几何/内容
      由 AC4/AC10 覆盖。
- [ ] **AC12 热切换与清除**：运行中改窗/清除下一帧生效；清除时在途旧窗口帧按 `since` 归旧窗；
      算法包重启后仍是当前窗口；AI 订阅全撤但 preview 仍持有同一 Stream 时，下一次 AI acquire
      必须重新应用当前 crop（无缓存残留）。

---

## Out of Scope

- **多窗口 / 级联裁剪 / per-algorithm 覆盖**：一路 camera 只有一个窗口（取景是相机属性）。
- **自动目标跟踪裁剪（Auto-tracking/PTZ-crop）与物理云台**。
- **窗口内的二次缩放策略**：仍由算法包自己的 letterbox 决定，宿主只做窗口裁剪。
- **窗口内多区域运动门禁**：门禁只回答"窗口内是否有运动"，不做分区。
- **音频轨**：AI 流目前只有视频轨。
- **同一结果批次内连续多次窗口变化的强一致历史**：宿主只保留上一代窗口；正常任务保存/分辨率
  重协商频率下足够，极端连续变更见 design §6.3 的已知限制。
