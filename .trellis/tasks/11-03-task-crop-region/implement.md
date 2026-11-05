# Implementation Plan: AI 流视图裁剪 (Crop Region)

> 顺序：spike → Go 契约 → 引擎视图/边界时钟 → 物化 → 逆映射 → 前端 → 门禁。
> 与 R3 的差别：边界改用“视图快照点的 `capture_time_ms` + 引擎 `effective_since_ms`”；
> 首帧/重放、奇数尺寸、零拷贝分叉、`CropCopyFn` 上下文、迁移号与前端保存门禁全部落到步骤里。
> **算法包、算法 SDK、IPC 布局都不动**；宿主 `Zhulong` C ABI 新增视图接口并升版本。
> R3.2 追加：窗口几何以**解码帧尺寸**为准（`view_snapshot` 内计算）、迁移号 `000032`、
> C ABI 只做结构校验、`NOT_READY` 新码、`ApplyCrop` 错误出口、apply 串行化、通知 ctx、裸 fd 同步 helper。

---

## 实现进度（2026-11，CPU-only 第一刀）

- [x] 阶段 1：迁移 `000032`、`region.ParseCrop/ValidateCrop/Reproject`、三态 API、
  `crop_json` 列、`INVALID_CROP_CONFIG` 本地化、`TaskResponse.crop`、swagger。
- [x] 阶段 2：C ABI 6、`Zhulong_stream_set_view/get_view`、`NOT_READY(-17)`、
  `Stream::view_snapshot`（解码帧尺寸 + 边界时钟）、释放前 `SetView(NULL)`。
- [x] 阶段 3：`FrameBroker` 窗口化物化（容量前置门禁 + fail_publish）、
  `WindowCrop` CPU 分平面行拷贝、紧打包布局、运动检测改为窗口内采样。
- [x] 阶段 4：`region.Reproject`（box/person 重映射/landmark/NaN 丢弃）、
  `TaskSavedListener` + `RefreshTaskCrop`（`applyMu` 串行）、`CropFor` 读库 fail-safe。
- [x] 阶段 5：crop schema、crop 工具（绘制/替换/清除）、窗口外压暗、
  共享撤销栈、保存门禁（画面 live + 至少一个算法）。
- [x] 门禁（宿主侧）：`native-test` 33/33、`go-check`、tasks 前端 173 用例、
  `format:check` / `lint` / `tsc` / `pnpm build`、`api-docs`、`webui-dist-check`。
- [ ] 阶段 0 / 6.5：板端 spike（`codecpar` vs 解码帧尺寸、旧包 e2e、RGA 一致性、性能基线）。
- [ ] RGA 第二刀：`rga_host_loader.{hpp,cpp}` + 板端验证（AC11 在此时生效）。

---

## 阶段 0：板端 spike（先做，决定性能档位与边界时钟可行性）

> **落地状态（2026-11）**：宿主窗口物化已按 **CPU-only** 实现并合并
> （`native/src/nodes/preprocess/window_crop.{hpp,cpp}` + `common/view_window.hpp`），
> 1080p 半宽半高约 0.8 MB/帧，CPU 行拷贝在目标帧率下足够；**RGA 同格式拷贝降级为可选第二刀**。
> 下列 0.1/0.3 的 RGA 数据仍需板端采集后才能把 RGA 重新放回默认路径；
> 0.2/0.4/0.5 是 CPU 路径也必须验证的现场前提。

- [ ] **0.1 RGA 窗口拷贝**
  - 在 `native/tests/` 加临时探针（或扩 `rga_vision_test.cpp`）：`imcheck_t` + `improcess`
    做**同格式窗口拷贝**（NV12 → NV12），源为 dmabuf，目标分别测 **shm/VA** 与 **dma slot fd**；
    `srect{odd,odd,w,h}` 与 `srect{even,even,w,h}` 各一组。
  - 验证读回内容正确、无撕裂；分别确认 RGA 写 shm、写 dma slot 的缓存一致性
    （客户端读 dma fd 已有 `DMA_BUF_IOCTL_SYNC`，shm 没有）。
  - 同时测两路 `FramePublisher` 并发 `improcess` 的稳定性；`imcheck` 通过但 `improcess` 失败时
    CPU 回退是否安全（是否已部分写）。
- [ ] **0.2 紧打包窗口通过客户端校验**
  - 用真实 face 包（`packages/face_recognition/rk3568`）跑一帧人工构造的窗口帧：
    `dst_stride=align_up(w,64)`、NV12 `offsets[1]=dst_stride*h`，确认
    `resolve_source` 的 `plane0_bpp` / `stride_align` / `deduce_continuous_hstride` 三条全部通过。
- [ ] **0.3 成本基线**
  - 1080p NV12 半宽半高窗口，`sample_fps=5/20` 下记录 RGA / CPU 两条路径的每帧耗时与宿主 CPU；
    同时记录按面积裁 50% 的口径，写回 design §5.4。
- [ ] **0.4 边界时钟**
  - 用**未修改的旧包**跑一次 `set_view` 与一次分辨率重协商：确认 dispatch 的 `capture_time_ms`
    与引擎 `effective_since_ms` 能严格区分旧/新窗口；覆盖同一毫秒内改窗（`last_capture+1` 推进）。
  - 确认旧包会把 `capture_time_ms` 原样回传（`native/sdk/src/algo_main.cpp:1265`），不需要重建。
- [ ] **0.5 尺寸来源一致性**
  - 同一路 RTSP 下打印并比对 `codecpar` 的 `width/height` 与解码帧 `HardwareFrame::width/height`
    （FFmpeg 软解 / MPP 硬解各一次），覆盖 1088 裁切类码流与分辨率热切换。
  - 结论写回 design §4.1；**任何不一致都必须由“按帧尺寸算几何”吸收**，不允许回到元数据。
- [ ] **产物**：结论写回 design §5.4/§9.3；RGA 任一条不通过则锁定 CPU-only 物化（架构不变）；
  边界时钟或尺寸来源不通过则先修，不进入阶段 1。

---

## 阶段 1：存储与契约（Go）

- [ ] **1.1 迁移**
  - `internal/database/migrations/000032_add_crop_json_to_analysis_tasks.{up,down}.sql`
    - `up`: `ALTER TABLE analysis_tasks ADD COLUMN crop_json TEXT NOT NULL DEFAULT '{}';`
    - `down`: `DROP COLUMN`
  - **仓库当前实际最大是 `000031`**（`000030_create_alert_destinations`、
    `000031_create_system_settings` 已落）；`000026` 仍为空号。**本任务取 `000032`**，
    落地前再核对一次实际 max。
  - ⚠️ `golang-migrate` 的 `Up()` 只跑 version > current；后续任务只能取当时 max+1，不能回头补号。
  - 仿 `store_test.go` 的迁移对称性测试加一条；再加一条“编号唯一 + 单调递增 + up/down 成对”的检查。
    **不要断言连续**：现存 `000026` 空洞会让它永远红。
- [ ] **1.2 `internal/region/crop.go`**
  - `const MinCropSide = 0.02`；`func Identity() Rect`。
  - `ParseCrop(raw string) (*Rect, error)`：
    - `""`/空白/`"{}"`/顶层 `"null"`/顶层 `"[]"`/`{"crop":null}`/`{"crop":[]}` → `(nil, nil)`；
    - `{"crop":[x1,y1,x2,y2]}` 长度必须 4；顶层 `[x1,y1,x2,y2]` 是同义别名；`[0,0,1,1]` 归一化为 `nil`；
    - 未知键忽略；非法 JSON/长度/坐标 → error。
  - `ValidateCrop(r Rect) error`：显式拒绝 NaN/Inf；`0<=x1<x2<=1`；边长 ≥ `MinCropSide`。
    **不得**调用 `Rect.Validate()`（那里写死 `MinSideLength=0.01`）。
    阈值权威只在此处：宿主 C ABI 只做结构性校验，不在 float32 上比较 `0.02`。
  - `Rect.UnmarshalJSON` 要求恰好 4 个数字（不能依赖 Go 数组的截断/补零）；`ParseCrop` 对存储
    JSON 里的数组也显式检查长度 == 4。⚠️ 这会改变 ROI/MASK 存储解析的失败模式（`[]`/长度 3/5
    从“校验失败”变“解析失败”，错误码仍 `INVALID_REGION_CONFIG`）；顺手确认 store/region 测试
    不依赖旧的截断容忍。
  - `ViewState{Rect, Since}`：`Since` 来自引擎；零值表示尚未发生窗口切换。
  - `Reproject` 先落签名/identity 快路径，阶段 4 完成字段。
- [ ] **1.3 `internal/task`**
  - `Task` 增 `CropJSON string`（`gorm:"column:crop_json;not null;default:'{}'"`）。
  - `writeTaskRow` 的 `Updates(map)` **补 `"crop_json": task.CropJSON`**（`store.go:221-235`，不加就不写）。
  - `CreateTaskRequest` 增 `Crop *region.Rect`；`ToTask()` 归一化：整帧 → `nil`、`nil` → `"{}"`、
    其他 → `{"crop":[...]}`。`Rect.UnmarshalJSON` 要求恰好 4 个数字；`[]`/`{}`/长度 3/5 在创建
    请求里返回 400；存储/响应/引擎入口仍把 `[]`/`{}` 当作无裁剪。
  - `UpdateTaskRequest` 增 `Crop json.RawMessage`：省略=不改、`null`/`[]`/`{}`=清除、
    `[x1,y1,x2,y2]`=设置、其他=`INVALID_CROP_CONFIG`。
  - 新增 `func (req *UpdateTaskRequest) ApplyCrop(t *Task) error`，由 `Service.UpdateTask` 在
    `ApplyTo` **之前**调用（`ApplyTo` 无返回值、且在 `req.Algorithms != nil` 时提前 return）：
    先解析校验、再改内存，失败直接 400，不落库。
  - `TaskResponse` 增 `Crop *region.Rect`；`ToResponse()` 从 `CropJSON` 解析，失败按 `nil`；
    读取路径只在 service 记一次 warning（不要每行刷屏）。
  - `prepareTask` 调 `region.ParseCrop`，失败 → `apperr.Invalid("INVALID_CROP_CONFIG", …)`。
  - `internal/task/locale.go` 增加 `INVALID_CROP_CONFIG` 三语文案（en/zh-Hans/zh-Hant）。
- [ ] **1.4 `TaskSavedListener` 与“只改 crop”**
  - `task.Service` 增 `TaskSavedListener`（`OnTaskSaved(ctx, taskID, cameraID)`）与
    `SetTaskSavedListener`；Create/Update/Delete 保存成功后回调。
    回调前用 `context.WithoutCancel(ctx)` + 短超时包装：写库已提交，客户端断连不该让新 crop 静默失效。
  - `UpdateTask`：在 `req.ApplyTo(task)` **之前**深拷贝旧任务（含 `Algorithms`）；
    保存后比较“除 crop 外的任务语义”：
    `CameraID/Enabled/Name` + 算法集合（按 `AlgorithmID` 规范化排序）的
    `AlgorithmID/Enabled/SampleFPS/ConfigJSON/RegionConfigJSON/ScheduleJSON`；
    忽略 `CropJSON`/`UpdatedAt`/子项 `ID/CreatedAt/UpdatedAt`。
    仅 crop 变 → 跳过 `notifyTasksChanged`，只发 `OnTaskSaved`；其他字段变 → 两者都发。
  - `SaveTask`：复用已读出的 `existing` 做同样比较，不额外再读。
  - `DeleteTask`：保存成功后发 `OnTaskSaved`；即使 AI 订阅释放没有销毁 Stream，
    `RefreshTaskCrop` 也能用 `(nil,nil)` 清空。
- [ ] **1.5 `internal/app/adapters.go`**
  - 用 `task.Store.GetTaskByCamera` 实现 `camera.TaskCropProvider`：
    - 任务不存在 / `!t.Enabled` / 无 enabled 算法 → `(nil, nil)`；
    - 无 crop → `(nil, nil)`；
    - `region.ParseCrop` 失败 → `(nil, error)`（Hub 记 warning、保留当前视图）；
    - 成功 → `(*Rect, nil)`。
  - `internal/app/app.go` 里 `SetTaskCropProvider` + `SetTaskSavedListener` 接到
    `streamHub.RefreshTaskCrop`；注意 Fx 构造顺序（provider 在 StreamHub 构造后、算法启动前）。
- [ ] **1.6 测试**（表驱动）
  - `ParseCrop`：`null`/`[]`/`{}`/顶层 `[]`/顶层 4 元数组/整帧/单矩形/越界/退化/`<0.02`/长度 3/5/NaN/未知键。
  - Store 往返：创建 → 读取 → 更新 → 读取（含“只改 name 不清 crop”）。
  - `ApplyTo`：三态（缺省/`null`+`[]`+`{}`/数组）+ `algorithms` 同时存在时不吞 crop。
  - 迁移 up/down 对称 + 老任务默认 `{}` 读路径 + 编号唯一。
  - `make api-docs` 后 `internal/apidocs/{docs.go,swagger.json,swagger.yaml}` 三份都出现 `crop` 字段。
  - swag 解析注意：
    - Makefile 的 `--dir` 必须包含 `internal/region`，否则 `TaskResponse.Crop *region.Rect` 报
      `cannot find type definition: region.Rect`；
    - `UpdateTaskRequest.Crop` 是 `json.RawMessage`，必须加 `swaggertype:"array,number"`，
      否则报 `cannot find type definition: json.RawMessage`。
  - “只改 crop 不重启”表驱动：crop only / crop+name / 换算法但字段相同 / 算法重排 / 新增算法。

---

## 阶段 2：引擎流视图与边界时钟（native + cgo + hub）

- [ ] **2.1 `native/src/common/view_window.hpp`**（header-only，不放 .cpp、不进 AlgoIpc 归档）
  - `struct ViewWindow { bool active; int x, y, w, h; };`
  - `ViewWindow compute_view_window(const region rect, int frame_w, int frame_h);`
    —— floor/ceil + 外向取偶 + **`min(W & ~1, …)` 夹回** + 右/下边缘退化时把起点推 2px；
    取整后若恰好等于整帧（`x1=y1=0 && x2=W&~1 && y2=H&~1`）归为 `inactive`；
    纯函数、无后端依赖。`frame_w/frame_h` 必须是**解码帧尺寸**，不是 `video_meta_`。
    测试覆盖 `1919×1079`、贴边、`x2=1.0`、整帧、极小窗口。
  - `using CropCopyFn = std::function<bool(const HardwareFrame&, const ViewWindow&,
    uint8_t* dst, size_t dst_size, int dst_stride, int dst_dma_fd)>;`
    —— `dst_dma_fd` 必须显式传（`-1`=shm，`>=0`=dma），CPU 写 dma slot 时据此做写同步
    （用 3.1 的 `ScopedFdSync`，不要拿裸 fd 去构 `ScopedCpuSync`）。
  - ⚠️ `ViewWindow` 不能放进 `ZhulongAlgoIpcObjects`：那批对象也归档进算法包 SDK。
- [ ] **2.2 `Stream` 持有视图与快照时钟**
  - 在 `Stream::mutex` 下保存：归一化配置（`pending_rect_`）、`ViewState`、几何缓存
    `(rect, frame_w, frame_h) → E + window`、`last_capture_ms_`。
  - `ViewSnapshot view_snapshot(int frame_w, int frame_h)`：同一把锁内返回 `{window, capture_time_ms}`；
    `capture_time_ms = max(now_ms, last_capture_ms_)`，只增不减。
  - **几何在这里解析**：`(pending_rect_, frame_w, frame_h)` 与缓存不同 → 重算 `E`，视作一次几何变更，
    `effective_since_ms = max(now_ms, last_capture_ms_+1)` 并推进 `last_capture_ms_`。
  - `set_view()`：有缓存时立即按缓存尺寸重算并推进 since（同值幂等）；无尺寸时只记 `pending_rect_`，
    返回 `NOT_READY`，`ViewState` 保持 `{0,0,1,1}, 0`。清空路径无条件成功并推进 since。
  - `on_ready()` **不算几何**：只更新元数据/日志；尺寸变化的后果由下一次 `view_snapshot` 落地。
  - `get_view()` 返回当前 `ViewState`；未切过帧时返回 `{0,0,1,1}, 0`。
  - 线程安全：几何、`ViewState`、`capture_time` 必须同一临界区，不能拆成两把锁。
- [ ] **2.3 C ABI（`native/include/Zhulong/engine.h` + `native/src/abi/engine.cpp`）**
  - `Zhulong_view_rect` + `Zhulong_view_state{rect, effective_since_ms}`；
    `Zhulong_stream_set_view` / `Zhulong_stream_get_view`（design §4.3）。
  - 非 acquire；`NOT_FOUND` / `NOT_READY` / `INVALID_ARGUMENT` 三种失败语义；清空永远 OK。
  - 新增 `Zhulong_ERR_NOT_READY = -17`（空号）；Go 侧补 `internal/engine/errors.go` 的
    `StatusNotReady` 与 `statusMessages` 文案（`mapNativeStatus` 通用，无需改）；
    **不新增 `ErrStreamNotFound`**，复用 `ErrNotFound`（同码两哨兵会让 `errors.Is` 互相为真）。
  - **非法坐标只做结构校验**（非有限 / `0<=x1<x2<=1` / 退化）；**不在 C 侧比较 `MinCropSide`**：
    0.02 过 float32 是 `0.0199999996`，边界值会被误拒。
  - `boundary()` 包住，异常不穿越 ABI；`out_effective` 可空（set）。
  - `ZHULONG_ABI_VERSION` 升到 6；确认 `internal/engine` 的 `expectedABIVersion` 自动跟随头文件。
- [ ] **2.4 Go 接线**
  - `internal/region` 增 `ViewState`/`Identity`。
  - `internal/engine/stream.go`：`SetView/GetView`（cgo + 参数校验 + `runtime.KeepAlive` +
    `ErrStreamNotReady` 哨兵；`NOT_FOUND` 复用 `ErrNotFound`）。
    两者走 `borrowHandle{requireGeneration: &gen}`（照 `setPublishRate` 的既有模式），
    保证引擎 stop/start 换代后旧句柄不会作用到新的 stream id。
  - `camera.MediaStream` 增 `SetView(ctx, crop *region.Rect) (region.ViewState, error)` /
    `GetView(ctx) (region.ViewState, error)`；所有 fake 补齐。
  - `camera.TaskCropProvider` / `StreamHub.SetTaskCropProvider` / `RefreshTaskCrop`（幂等）。
  - `aiStreamRef` 增 `applyMu sync.Mutex`、`viewMu sync.Mutex`、`view region.ViewState`、
    `viewPrev region.Rect`、`viewPending bool`；初始化 identity。
  - **acquire 顺序**：`awaitStreamReady → provider.CropFor → SetView（成功 state 用于初始化 ref）→
    注册 aiStreamRef → SubscribeAIResults → 启动 consumeAIResults → 返回 broker path`。
  - existing-ref 快路径在返回前 `ensureView`（读 provider + SetView）。`ensureView` 与
    `RefreshTaskCrop` 共用 `ref.applyMu`，在锁内完成“读库 + SetView”（仍在 Hub 锁外），
    防止旧值后落导致视图回退。
  - `SetView` 失败：`NOT_READY` → pending；`NOT_FOUND` → 流失效，acquire 失败/重试；其他 → 保留当前
    （新流为 identity）并 warning；不得伪造 E，`get_view` 为准。
  - **`SetView` 成功返回的 state 立刻写入 ref**（新 ref 初始化；existing `updateView` 保留 `viewPrev`），
    不能等首次 `GetView`；否则 `GetView` 失败会把已生效窗口当全幅。
  - `consumeAIResults` 改为接收 `*aiStreamRef`；每条结果 `GetView` → `updateView` → `viewFor`。
  - 释放/关闭：锁内只摘 ref 并把 `stream` 交给局部变量，锁外先 `SetView(nil)`（短超时/后台 ctx）
    再 `Close`；`NOT_FOUND` 按正常情况；provider error 保留当前视图；不要按 cameraID 重新查 ref。
- [ ] **2.5 变更通知（热更新）**
  - `internal/app` 把 `OnTaskSaved` 接到 `streamHub.RefreshTaskCrop`。
  - 只 crop 变 → 跳过 `notifyTasksChanged`，只发 `OnTaskSaved`；其他字段变 → 两者都发。
- [ ] **2.6 测试**
  - `NOT_READY` → 首个结果批次 `GetView` 收敛；清空永远成功。
  - 流重建后视图重放；共享 preview 的 Stream 在 AI 全撤后显式清空、下次 acquire 重放。
  - **非 acquire**：对不存在的流调用 `SetView` 不得触发拉流（fake engine 断言无 acquire）。
  - **apply 串行**：`RefreshTaskCrop` 与 existing-ref `ensureView` 并发时，最终视图 == 最后一次读到的配置。
  - **尺寸来源**：fake 元数据 1088 / fake 帧 1080 下，`view_snapshot` 按 1080 算 `E`，不丢帧、不偏映射。
  - **通知 ctx**：保存后请求 ctx 取消，crop 仍生效（`context.WithoutCancel` + 短超时）。
  - **NOT_READY 正常路径**：`awaitStreamReady` 后首帧未到时 `SetView` 返回 `NOT_READY`；
    首个 `view_snapshot` 后 `GetView` 直接给出激活的 `E`。
  - **首帧**：`SetView` 在 `SubscribeAIResults` 前生效；首批结果不得按全幅 buff 映射。
  - **边界**：`capture_time < since` / `== since` / `> since` 三条；同一毫秒内改窗。
  - **只改 crop**：不触发算法重启、只触发 `RefreshTaskCrop`；crop+name 两者都触发。
  - `internal/app` / `internal/camera` / `internal/engine` 走 wrapper：
    `python3 native/scripts/build.py go test -race ./internal/camera/... ./internal/app/... ./internal/engine/...`

---

## 阶段 3：物化（native）

> 动 `window_crop` 之前回读一遍 `.trellis/spec/native/algo-plugin-contract.md` 的 CPU Cache
> 同步守则与 `hardware-backends.md` 的两阶段裁剪章节（research 摘要不含这些细节）。

- [ ] **3.1 窗口拷贝（宿主侧）**
  - `native/src/nodes/preprocess/window_crop.{hpp,cpp}`（`ZhulongEngine`）：CPU 分平面行拷贝 +
    后端选择 + `window_rga_count`/`window_cpu_count`。
  - `native/src/backends/rga/rga_host_loader.{hpp,cpp}`（`ZhulongEngine`，**唯一**宿主侧含
    vendored RGA 头的 TU）：dlopen `librga.so.2`（仿 `mpp_loader.cpp`）+ `imcheck_t` 预检 + `improcess`。
  - `CropCopyFn` 用 `std::function`（带 `dst_size`/`dst_dma_fd`），不要裸函数指针；`noexcept`/失败返回 false。
  - `Engine` 构造 `WindowCrop` → `FramePublisher` 持有 → 传给 broker；`WindowCrop` 生命周期覆盖 publisher。
  - CPU 路径：
    - NV12/YUV420P 的 UV 起点用 `HardwareFrame::y_plane_size()`（含 `vstride`），不是 `stride*height`；
    - 源行跨度用 `plane_strides[i]`，为 0 回退 `stride`；
    - dmabuf 源 `mmap` + 同步：在 `native/src/platform/dmabuf/dmabuf_sync.hpp` 加 header-only 的
      裸 fd RAII（`ScopedFdSync`），`motion_detector` 与 `window_crop` 共用；
    - `dst_dma_fd >= 0` 且 CPU 写：`ScopedFdSync(dst_dma_fd, DMA_SYNC_WRITE)`，shm（-1）不做；
      ⚠️ 现成的 `ScopedCpuSync` 只接受 `DmaBuffer&`，不能拿裸 fd 构造；
    - 目标 shm 且 RGA 写：按 spike 结论 flush 或 CPU。
  - RGA 路径：`srect{view.x,view.y,w,h}` → `drect{0,0,w,h}`；预检/库/设备失败 → CPU，几何不变；
    多 publisher 并发按 spike 串行化；`improcess` 运行期失败/部分写 → 该帧 fail_publish。
- [ ] **3.2 `FramePublisher`**
  - 顺序：`节流 → view_snapshot(frame.width, frame.height) {window,capture_time} → motion(window) →
    publish(frame, view, motion, copy, capture_time) → push_frame(全幅)`。
  - 末次视图切换时调一次 `motion_detector_.reset()`（可选但建议）：否则换窗首帧会走 anti-flash
    （`active_ratio > 70%` → `report_static`），多压一帧。
  - `MotionDetector::detect(frame, view, out)`：只读 Y 平面窗口；`x/y` 偏移、`vstride`、`plane_strides`；
    先校验窗口在 `frame.width/height` 内；窗口 <16、越界或格式不支持 fail-open。
- [ ] **3.3 `FrameBroker::publish_frame`**
  - 签名加 `const ViewWindow&`、`const CropCopyFn&`、`int64_t capture_time_ms`。
  - **`zero_copy = frame.dma_fd >= 0 && !view.active`**；窗口激活时绝不准发全幅 fd。
  - `view.active == false` → 原路径字段/布局/内容不变；`capture_time_ms` 用传入值。
  - `view.active == true` → 先校验窗口在 `frame.width/height` 内（不满足 `fail_publish`，绝不越界），
    再一律写 slot（dma slot 优先，否则 shm）；容量门禁用 `window_payload_bytes(view, format)`，
    **仍要单独校验 ≤ `options_.slot_size`**（默认 4 MiB，不是“按全幅分配”）；
    调用 `copy(frame, view, dst, dst_size, dst_stride, /*dst_dma_fd=*/use_dma_slot ? fd : -1)`；
    填窗口 `width/height/strides/offsets`；dma 目标置 `FRAME_DISPATCH_FLAG_DMA_FD`；
    `CropCopyFn` false → `fail_publish`，绝不发半截帧。
  - 未知格式/无 copy 函数 → fail_publish；不按 BGR24 猜。
  - 更新 `ipc_protocol.hpp` 中 `capture_time_ms` 的注释为“宿主视图快照时钟”，**不改布局**。
- [ ] **3.4 测试（`native/tests/`）**
  - `view_window_test.cpp`（纯函数，含奇数 1919×1079、右/下边缘、整帧、极小窗口；
    整帧取整后归 `inactive`；`frame_w/h` 用帧尺寸而非元数据）。
  - `frame_broker_test.cpp`：四象限 + 角标图案断言 slot 内容 == 期望窗口；NV12/YUV420P/BGR24；
    边界（贴边、`x2=1.0`）；`crop==整帧` 与无视图路径一致；`dma_fd>=0 + view.active` 强制 slot；
    copy 返回 false / 窗口越出帧尺寸 → 无 dispatch、槽位 FREE、配额回滚；未知格式拒绝。
  - `window_crop_test.cpp`：CPU 路径逐字节；`vstride > height`、独立 `plane_strides`；
    RGA 路径在无设备时跳过。
  - `native/CMakeLists.txt` 里 `add_executable` + `add_test`；把 `window_crop.cpp` / `rga_host_loader.cpp`
    加进 `ZhulongEngine` 源列表（**不要**加进 `ZhulongAlgoIpcObjects`）；
    `ZhulongEngine` 加 `vendor/rga/include` PRIVATE include，**不链接** `librga`。
  - `make native-build`（改完 C++ 必须跑；Air 不监听 native）。
- [ ] **3.5 门禁**：`git diff --name-only | grep '^packages/.*/src/'` 必须为空——这次它是真门禁，
  因为架构保证算法包不需要改；若需要改，说明设计漏了一层。

---

## 阶段 4：结果逆映射（Go）

- [ ] **4.1 `region.Reproject`**
  - identity（`{0,0,1,1}`）快路径直接返回原值，不乘 1。
  - 字段清单按 design §6.2 的实际解析形状：detection、scene/person 的 `Persons[].BodyBox` + 顶层
    `Faces[].Box/.Landmarks`、vehicle 的 `VehicleBox` + `Plates[].Box`、default/unknown 的嵌套
    `Persons[].Faces[]`。
  - 逐轴线性 + clamp `[0,1]`；NaN/Inf 按 design §6.2 丢弃 item/landmark，不 clamp 成 0。
- [ ] **4.2 Hub 单点**
  - `consumeAIResults`：`ParseInferenceResult` 之后、`region.Filter` 之前调用；
    每条结果先 `GetView` 更新 `ref.view`，再 `viewFor(res.Timestamp)`，再 `Reproject`。
  - `viewFor`：`Since` 零值 → 当前；`ts < Since` → `viewPrev`；否则当前。
  - `GetView` 失败（closed/not found）保留当前视图；引擎 since 倒退时保留当前并 warning。
- [ ] **4.3 测试**
  - 4 类结果逐字段（含 landmarks、scene faces、嵌套 faces）+ clamp + NaN/Inf + identity 快路径。
  - `viewPrev` 边界：改窗前后各一条结果 + `capture_time == since` + 同一毫秒改窗。
  - **未配置 crop 时逐字段不变**（防止把 identity 写成“乘以 1”丢精度）。
  - 共享 preview 的 Stream：AI 全撤后清空、再 acquire 重放；旧 subscription 的结果不使用新 ref 的 E。

---

## 阶段 5：前端（任务级裁剪）

- [ ] **5.1 数据层**
  - `web/src/features/tasks/data/region-config.ts`：`rectSchema` 不改成全局 0.02；
    新增 `MIN_CROP_SIDE = 0.02` 与 `cropRectSchema`（同样的 tuple + 更严的 refine + 整帧归一化）。
  - `web/src/features/tasks/components/region-geometry.ts`（注意不在 `data/`）：
    `isLargeEnough`/`resizeRect` 的 `MIN_SIDE_LENGTH` 参数化（`minSide` 入参，默认 0.01），
    crop 编辑器传 `MIN_CROP_SIDE`。
  - `schema.ts`：`taskSchema.crop = cropRectSchema.nullable().default(null)`（Zod 默认 strip，
    漏了会静默丢字段再清空配置）。
  - `tasks-api.ts`：`SaveChannelTaskRequest.crop: Rect | null`；`CreateTaskRequest` 也加 `crop`
    并在 `createTask` 的显式 body 里带上；`UpdateTaskDto` 增加可选 `crop`，但只有显式设置时才发送
    （`updateTask` 的合并语义不能被普通字段编辑清空）。
  - `use-sync-channel-algorithms.ts` 的 payload 必须带 crop；`saveChannelTask` 是整任务 upsert，
    缺省会清空。
- [ ] **5.2 编辑器**
  - crop 状态并入 `useRegionEditor`（`crop` / `setCrop` / `clearCrop` / `resetCrop` /
    `serializeCrop`），**仍不进** `RegionDraft`；不单开 `useCropEditor`，避免两套状态。
    基线从 `task.crop` 载入；切换算法只 `reset(region)` 不动 crop，切换相机才 `resetCrop`。
  - `RegionCanvas`：新增 `'crop'` tool、`crop`/`onCropChange`/`cropMinSide` props、
    crop 图元（青色、四块压暗、CROP 标签）与手柄；拖拽状态机新增
    `crop-creating`/`crop-moving`/`crop-resizing`；
    命中按 tool 隔离：crop tool 只命中 crop，roi/mask tool 只命中对应分类，select 模式 ROI/MASK 优先，
    crop 只在空白/手柄命中。窗口外区域压暗。
  - `RegionTool` 加 `'crop'` 会波及 `region-editor-toolbar.tsx` 的 `requestTool`/`counts` 与
    `use-region-drag.ts` 的 `creating` 分支，必须一起改；Crop 只有 1 个矩形，工具栏提供清除。
  - 撤销栈天然共享：`snapshot()` 压入 `{draft, crop}`，`undo()` 一起恢复，避免两个 Ctrl+Z。
  - `ChannelTaskRegionViewport` / `channel-task-dialog`：
    - crop 区块**不随选中算法切换**；没有选中算法时也可查看/编辑（只要 `cameraId` 有值），
      但**保存仍要求至少一个算法**（无算法 = 删除任务），两者不要混为一谈；
    - ROI/MASK 仍跟随选中算法；
    - crop 取 `cyan`；`showCropEditor` 与 `showEditor`（算法）分开。
- [ ] **5.3 保存门禁**
  - crop 的编辑门禁是**画面已出首帧**（`surfaceState === 'live'`），不是 `canEditRegion`
    （后者还要求已勾选算法）；画面未就绪时 crop 编辑缓冲不得提交，提交已保存基线。
  - 保存仍要求至少一个算法；删任务分支（`payloadDrafts.length === 0`）不得附带 `crop`。
  - `handleSave`：`syncMutation.mutate({ ..., crop })` 显式带 `crop: Rect | null`；
    `toPayloadDrafts` 保持只产出算法草稿，不要把 crop 塞进算法项。
  - 切相机 reset crop 编辑器；切算法不 reset；`currentExistingTask.crop` 重新载入。
- [ ] **5.4 展示**
  - `task-sub-table`：任务级“已裁剪”徽章，与算法级“区域过滤”徽章分开。
  - i18n `locales/{en,zh-Hans,zh-Hant}.json` 三份键对齐（标题/提示/清除/“该区域外的目标不会被识别”/
    画面未就绪提示）。
- [ ] **5.5 测试（真实无头浏览器，不是 JSDOM）**
  - 绘制 → 替换 → 清除 → 重开回显。
  - 切换选中算法时 crop 不丢失；roi/mask 仍随算法切换。
  - 画面未就绪时 crop 编辑不提交；`saveChannelTask` payload 含 `crop`（含 `null`）；无算法时不提交 crop。
  - `tasks-api.test.ts` 补 `crop` 字段；`region-geometry.test.ts` 补 `minSide` 参数化。

---

## 阶段 6：门禁、文档与发布

- [ ] **6.1 Go**
  - `python3 native/scripts/build.py go test -race ./internal/camera/... ./internal/app/... ./internal/engine/...`
  - `go test -race ./internal/task/... ./internal/region/...`
- [ ] **6.2 Native**：`make native-test`（含新登记的测试）；`make native-build`。
- [ ] **6.3 前端**：`cd web && pnpm test`。
- [ ] **6.4 总门禁**：`make check`。
- [ ] **6.5 板端**：`make board-check`（RGA 窗口拷贝 + 实际封装）与 spike 复测。
- [ ] **6.6 文档/规范**
  - `.trellis/spec/native/algo-plugin-contract.md`：帧视图可能是裁剪后的分析视图；
    像素阈值以 `image.width()/height()` 为基准；不得假设相机分辨率/宽高比；
    必须原样回传 `capture_time_ms`。
  - `.trellis/spec/native/pipeline-guidelines.md`：视图语义、view snapshot/capture_time、
    物化点、history 全幅不变量、共享 Stream 生命周期、**几何以解码帧尺寸为准**、
    **`has_motion` 是窗口内语义**（“窗口外运动放行整帧”的旧限制已消失）。
  - `.trellis/spec/native/hardware-backends.md`：宿主侧 RGA 同格式拷贝、紧打包 stride、
    shm/dma slot 缓存一致性、与算法侧 `crop_scale` 的区别（宿主窗口不做硬件对齐外扩）。
  - `.trellis/spec/native/cgo-contract.md`：`set_view/get_view`、`Zhulong_view_state`、
    ABI 6、失败语义与指针生命周期。
  - `.trellis/spec/backend/camera-guidelines.md`：Hub 持有 `ViewState`、`viewPrev` 边界、
    非 acquire 禁令、共享 preview 时的显式清空。
  - `.trellis/spec/backend/task-guidelines.md`：`crop_json`、三态更新、`writeTaskRow` 列、
    `TaskSavedListener`、只改 crop 的比较规则。
  - `.trellis/spec/backend/http-api-guidelines.md`：`crop` 的 null/[]/{} 语义与 `INVALID_CROP_CONFIG`。
  - `.trellis/spec/backend/error-handling.md`：`INVALID_CROP_CONFIG` 的稳定机器码、400 映射与三语文案。
  - `.trellis/spec/frontend/theme-and-i18n.md`：crop cyan 与三语键。
- [ ] **6.7 发布**
  - 升级宿主二进制 + 前端；**算法包不重建**（`sdk_version` / `IPC_VERSION` 未变）。
  - 发布说明注明宿主 C ABI 6、`crop_json` 列、以及“宿主/前端同版本”要求。
  - 回滚只需下掉宿主（`crop_json` 列对老宿主透明；down 迁移删列）。

---

## 验收对照

| AC | 证据 |
| :--- | :--- |
| AC1 契约兼容 | `view.active=false` 时 broker 字段/布局/内容路径与今天一致；`capture_time` 取值点差异已记录 |
| AC2 唯一性与校验 | `ParseCrop`/`ValidateCrop` 表驱动 + 三态更新测试 + 前端 `cropRectSchema` + locale；C ABI 只做结构校验（阈值权威在 Go） |
| AC3 模型输入 | 合成图案 + 真实包的窗口内容断言（尺寸仍为模型要求） |
| AC4 逆映射 | 4 类结果逐字段 + 顶层/嵌套 faces + clamp + NaN + `capture_time`/`since` 边界 |
| AC5 ROI/MASK | Go 表驱动（目标全在窗口内）：`crop+roi+mask` == `roi+mask`（全局坐标） |
| AC6 抓拍 | 全幅 JPEG 尺寸/框位置断言（history ring 全幅） |
| AC7 前端 | 真实浏览器用例：绘制/替换/清除/回显/切算法/未就绪不提交 |
| AC8 门禁 | `make check` 全绿 |
| AC9 旧包零改动 | 未修改的 face/plate/crowd 包在裁剪流上的 e2e 坐标断言 |
| AC10 窗口一致性 | `set_view`/`get_view` 的 E/since == 实际像素窗口/切换点（引擎单点断言 + 日志；E 为 float32，像素断言 ≤1px 容差） |
| AC11 降级不改几何 | CPU vs RGA 几何/内容一致（板端 spike 结论写回） |
| AC12 热切换与清除 | 运行中改窗/清除后下一帧生效；共享 preview 的 Stream 清空/重放；包重启后仍是当前窗口 |
