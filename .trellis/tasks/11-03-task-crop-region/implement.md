# Implementation Plan: AI 流视图裁剪 (Crop Region)

> 顺序：spike → Go 契约 → 引擎视图 → 物化 → 逆映射 → 前端 → 门禁。
> 与 R2 的最大差别：**算法包、SDK、IPC 都不动**；新增代码集中在宿主媒体层与 Go 结果入口。

---

## 阶段 0：板端 spike（先做，决定性能档位）

- [ ] **0.1 RGA 窗口拷贝**
  - 在 `native/tests/` 加临时探针（或直接扩 `rga_vision_test.cpp`）：`imcheck_t` + `improcess`
    做**同格式窗口拷贝**（NV12 → NV12），源为 dmabuf、目标为 shm/VA，`srect{odd,odd,w,h}` 与
    `srect{even,even,w,h}` 各一组。
  - 验证读回内容正确、无撕裂；确认 RGA 写 shm 的缓存一致性（必要时显式 flush 或改 CPU）。
- [ ] **0.2 紧打包窗口通过客户端校验**
  - 用真实 face 包（`packages/face_recognition/rk3568`）跑一帧人工构造的窗口帧：
    `dst_stride=align_up(w,64)`、NV12 `offsets[1]=dst_stride*h`，确认
    `resolve_source` 的 `plane0_bpp` / `stride_align` / `deduce_continuous_hstride` 三条全部通过。
- [ ] **0.3 成本基线**
  - 1080p NV12 裁 50%，`sample_fps=5/20` 下记录 RGA / CPU 两条路径的每帧耗时与宿主 CPU。
- [ ] **产物**：结论写回 design §5.4 与 §9.3；不通过则锁定 CPU-only 物化（架构不变）。

---

## 阶段 1：存储与契约（Go）

- [ ] **1.1 迁移**
  - `internal/database/migrations/0000NN_add_crop_json_to_analysis_tasks.{up,down}.sql`
    - `up`: `ALTER TABLE analysis_tasks ADD COLUMN crop_json TEXT NOT NULL DEFAULT '{}';`
    - `down`: `DROP COLUMN`
  - **NN 取落地时实际最大号 +1**（`11-03-alert-push-multi-channel` 也声明了 000026，不要抢号）。
  - 仿 `store_test.go` 的 `TestSupportsMotionGateMigrationUpAndDownSymmetry` 加一条对称性测试。
- [ ] **1.2 `internal/region/crop.go`**
  - `const MinCropSide = 0.02`
  - `ParseCrop(raw string) (*Rect, error)`：`""`/`"{}"`/`{"crop":null}`/`{"crop":[]}` → `(nil, nil)`；
    数组必须长度 4；未知键忽略（向前兼容）；校验失败返回错误。
  - `ValidateCrop(r Rect) error`：有限、`0<=x1<x2<=1`、边长 ≥ `MinCropSide`。
    **不得**调用 `Rect.Validate()`（那里写死 `MinSideLength=0.01`）。
  - `Reproject(res aitypes.InferenceResult, view Rect) aitypes.InferenceResult`（阶段 4 完成，可先落签名）。
- [ ] **1.3 `internal/task`**
  - `Task` 增 `CropJSON string`（`gorm:"column:crop_json;not null;default:'{}'"`）。
  - `writeTaskRow` 的 `Updates(map)` **补 `"crop_json": task.CropJSON`**（`store.go:230-235`，不加就不写）。
  - `CreateTaskRequest` 增 `Crop *region.Rect`；`ToTask()` 归一化：整帧 → `nil`、`nil` → `"{}"`。
  - `UpdateTaskRequest` 增 `Crop json.RawMessage`：省略=不改、`null`=清除、数组=设置；
    在 `ApplyTo` 的 `req.Algorithms != nil` 提前 `return` **之前**处理。
  - `TaskResponse` 增 `Crop *region.Rect`；`ToResponse()` 从 `CropJSON` 解析（解析失败按 `nil` 并记一次 warning）。
  - `prepareTask` 调 `region.ParseCrop`，失败 → `apperr.Invalid("INVALID_CROP_CONFIG", …)`。
- [ ] **1.4 测试**（表驱动）
  - `ParseCrop`：`null`/`[]`/`{}`/单矩形/越界/退化/`<0.02`/长度 3/5/NaN。
  - Store 往返：创建 → 读取 → 更新 → 读取（含"只改 name 不清 crop"）。
  - `ApplyTo`：三态（缺省/`null`/数组）+ `algorithms` 同时存在时不吞 crop。
  - 迁移 up/down 对称 + 老任务默认 `{}` 读路径。
  - `make api-docs` 后 `internal/apidocs/docs.go` 的 `crop` 字段存在。

---

## 阶段 2：引擎流视图（native + cgo + hub）

- [ ] **2.1 `native/src/common/view_window.hpp`**（header-only，不放 .cpp、不进 AlgoIpc 归档）
  - `struct ViewWindow { bool active; int x, y, w, h; };`
  - `ViewWindow compute_view_window(const region rect, int frame_w, int frame_h);`
    —— floor/ceil + **统一外向取偶**（design §4.1），纯函数、无后端依赖。
  - 单测：1080p/720p、奇偶、贴边、`x2=1.0`、整帧、极小窗口。
  - ⚠️ `ViewWindow` 不能放进 `ZhulongAlgoIpcObjects`：那批对象也归档进算法包 SDK。
- [ ] **2.2 `Stream` 持有视图**
  - 保存归一化配置；`view_snapshot()` 返回当前 `ViewWindow`；`video_meta_` 变化时重算。
  - 线程安全：配置用一个受锁的小结构，或原子替换 `shared_ptr<const ViewWindow>`。
- [ ] **2.3 C ABI（`native/src/abi/engine.cpp`）**
  - `Zhulong_stream_set_view` / `Zhulong_stream_get_view`（design §4.3）。
  - 非 acquire；`NOT_FOUND` / `NOT_READY` / `INVALID_ARGUMENT` 三种失败语义。
  - `boundary()` 包住，异常不穿越 ABI。
- [ ] **2.4 Go 接线**
  - `internal/engine/stream.go`：`SetView/GetView`（cgo + 参数校验 + `runtime.KeepAlive`）。
  - `camera.MediaStream` 增方法；测试 fake 补齐。
  - `camera` 新增 `TaskCropProvider`；`StreamHub.SetTaskCropProvider` + `RefreshTaskCrop(ctx, cameraID)`（幂等）。
  - `internal/app/adapters.go`：用 `task.Store.GetTaskByCamera` 实现 provider（解析 `CropJSON`，失败按无裁剪 + 一次 warning）。
  - `aiStreamRef` 增 `streamID / view / viewPrev / viewChangedAt / viewPending`。
  - `acquireAIStreamBrokerPath` 成功后 `SetView`（恢复路径同样）；`NOT_READY` → `viewPending`；
    `NOT_FOUND`（流未建立）按正常情况处理；释放/关闭时清空。
- [ ] **2.5 变更通知（热更新）**
  - `task.Service` 新增 `TaskSavedListener`（Create/Update/Delete 保存成功后回调，带 `taskID/cameraID`）。
  - `UpdateTask` / `SaveTask`：比较算法子树（enabled / sampleFps / configJson / regionConfigJson /
    scheduleJson）是否变化；**只 crop 变 → 跳过 `notifyTasksChanged`**，只发 `OnTaskSaved`。
  - `internal/app` 把 `OnTaskSaved` 接到 `streamHub.RefreshTaskCrop`。
- [ ] **2.6 测试**
  - `NOT_READY` → 首个结果批次 `GetView` 收敛。
  - 流重建后视图重放；释放后本地视图清空。
  - **非 acquire**：对不存在的流调用 `SetView` 不得触发拉流（用 fake engine 断言无 acquire）。
  - **只改 crop**：算法子树不变 → 不触发算法重启、只触发 `RefreshTaskCrop`；
    同时改 fps/config → 两者都触发（表驱动新旧任务对）。
  - `internal/app` / `internal/camera` 走 wrapper：`python3 native/scripts/build.py go test -race ./internal/camera/... ./internal/app/...`

---

## 阶段 3：物化（native）

- [ ] **3.1 窗口拷贝（宿主侧）**
  - `native/src/nodes/preprocess/window_crop.{hpp,cpp}`（`ZhulongEngine`）：CPU 分平面行拷贝 + 后端选择。
  - `native/src/backends/rga/rga_host_loader.{hpp,cpp}`（`ZhulongEngine`，**唯一**含 vendored RGA 头的 TU）：
    dlopen `librga.so.2`（仿 `mpp_loader.cpp`）+ `imcheck_t` 预检 + `improcess`。
  - `CropCopyFn` 接口定义在 `common/view_window.hpp`；`Engine` 构造 → `FramePublisher` 持有 → 传给 broker。
  - dmabuf 源的 CPU 路径：`mmap + DMA_BUF_IOCTL_SYNC`（抽公共 helper，与 motion detector 共用）。
  - 失败/缺失/预检不通过 → 该帧 CPU，**几何不变**；计数器 `window_rga_count` / `window_cpu_count`。
- [ ] **3.2 `FramePublisher`**
  - 顺序改成 `节流 → view_snapshot → motion(view) → publish(frame, view, motion) → push_frame(全幅)`。
  - `MotionDetector::detect(frame, view, out)`：只读 Y 平面窗口；网格拉伸不影响 `has_motion`。
- [ ] **3.3 `FrameBroker::publish_frame(frame, view, motion, copy)`**
  - 多接一个 `CropCopyFn` 参数（由 publisher 注入，broker 不依赖宿主实现）。
  - `view.active == false` → 原路径逐字节不变。
  - `view.active == true` → 一律写 slot（dma slot 优先），紧打包布局（design §5.2 表）。
  - `MSG_FRAME_DISPATCH` 填窗口尺寸/紧打包 stride/offset。
  - 容量门禁用窗口 payload 计算；`slot_size` 不变（窗口 ≤ 全幅）。
- [ ] **3.4 测试（`native/tests/`）**
  - `view_window_test.cpp`（纯函数）。
  - `frame_broker_test.cpp`：四象限 + 角标图案断言 slot 内容 == 期望窗口；NV12/YUV420P/BGR24；
    边界（贴边、`x2=1.0`）；`crop==整帧` 与无视图逐字节一致；无视图回归。
  - `window_crop_test.cpp`：CPU 路径逐字节；RGA 路径在无设备时跳过。
  - `native/CMakeLists.txt` 里 `add_executable` + `add_test`（现在是一个个显式登记的，
    新文件不登记 `make native-test` 不会跑）；同时把 `window_crop.cpp` / `rga_host_loader.cpp`
    加进 `ZhulongEngine` 源列表（**不要**加进 `ZhulongAlgoIpcObjects`）。
  - `make native-build`（改完 C++ 必须跑；Air 不监听 native）。
- [ ] **3.5 门禁**：`git diff --name-only | grep '^packages/.*/src/'` 必须为空——这次它是真门禁，
  因为架构保证算法包不需要改；若需要改，说明设计漏了一层。

---

## 阶段 4：结果逆映射（Go）

- [ ] **4.1 `region.Reproject`**
  - identity（`{0,0,1,1}`）快路径直接返回原值。
  - 字段清单见 design §6.2；逐轴线性 + clamp `[0,1]`；NaN 丢弃该字段。
- [ ] **4.2 Hub 单点**
  - `consumeAIResults`：`ParseInferenceResult` 之后、`region.Filter` 之前调用。
  - `h.viewFor(cameraID, res.Timestamp)`：`Timestamp.Before(viewChangedAt)` → `viewPrev`，否则当前 `E`。
  - 每批 `GetView` 刷新（分辨率重协商）；变化时滚动 `viewPrev/viewChangedAt`。
- [ ] **4.3 测试**
  - 4 类结果逐字段（含 landmarks、scene faces）+ clamp + identity 快路径。
  - `viewPrev` 边界：改窗前后各一条结果。
  - **未配置 crop 时逐字段不变**（防止把 identity 写成"乘以 1"丢精度）。

---

## 阶段 5：前端（任务级裁剪）

- [ ] **5.1 数据层**
  - `web/src/features/tasks/data/region-config.ts`：`rectSchema` 不改成全局 0.02；
    新增 `MIN_CROP_SIDE = 0.02` 与 `cropRectSchema`（同样的 tuple + 更严的 refine）。
  - `region-geometry.ts`：把写死的 `MIN_SIDE_LENGTH` 参数化（`minSide` 入参，默认 0.01），
    crop 编辑器传 `MIN_CROP_SIDE`。
  - `schema.ts`：`taskSchema.crop = cropRectSchema.nullable().default(null)`（Zod 默认 strip，
    漏了会静默丢字段再清空配置）。
  - `tasks-api.ts`：`SaveChannelTaskRequest.crop: Rect | null`；`taskAlgorithmsPayload` 不变（crop 是同级字段）。
- [ ] **5.2 编辑器**
  - `useCropEditor`（单矩形、绘制即替换、清除、`isDirty`）；**不进** `RegionDraft`。
  - `RegionCanvas`：新增 `'crop'` tool、`crop`/`onCropChange` props、crop 图元与手柄，
    命中优先级 crop → roi/mask；渲染上把窗口外区域压暗。
  - 撤销栈放在弹窗层：`snapshot()` 同时记录 `{regionDraft, cropDraft}`，避免两个 Ctrl+Z。
  - `ChannelTaskRegionViewport` / `channel-task-dialog`：crop 区块不随选中算法切换；
    ROI/MASK 仍跟随算法；crop 取 `cyan`。
- [ ] **5.3 展示**
  - `task-sub-table`：任务级"已裁剪"徽章，与算法级"区域过滤"徽章分开。
  - i18n `locales/{en,zh-Hans,zh-Hant}.json` 三份键对齐（标题/提示/清除/"该区域外的目标不会被识别"）。
- [ ] **5.4 测试（真实无头浏览器，不是 JSDOM）**
  - 绘制 → 替换 → 清除 → 重开回显。
  - 切换选中算法时 crop 不丢失；roi/mask 仍随算法切换。
  - `tasks-api.test.ts` 补 `crop` 字段（含 `null`）。

---

## 阶段 6：门禁、文档与发布

- [ ] **6.1 Go**
  - `python3 native/scripts/build.py go test -race ./internal/camera/... ./internal/app/... ./internal/engine/...`
  - `go test -race ./internal/task/... ./internal/region/...`
- [ ] **6.2 Native**：`make native-test`（含新登记的测试）。
- [ ] **6.3 前端**：`cd web && pnpm test`。
- [ ] **6.4 总门禁**：`make check`。
- [ ] **6.5 板端**：`make board-check`（RGA 窗口拷贝 + 实际封装）与 spike 复测。
- [ ] **6.6 文档/规范**
  - `.trellis/spec/native/algo-plugin-contract.md`：帧视图可能是裁剪后的分析视图；
    像素阈值以 `image.width()/height()` 为基准；不得假设相机分辨率/宽高比。
  - `.trellis/spec/native/pipeline-guidelines.md`：视图语义、物化点、history 全幅不变量。
  - `.trellis/spec/native/hardware-backends.md`：宿主侧 RGA 同格式拷贝、紧打包 stride、缓存一致性。
  - `.trellis/spec/backend/camera-guidelines.md`：Hub 持有 `E`、`viewPrev` 边界、非 acquire 禁令。
  - `.trellis/spec/backend/task-guidelines.md`：`crop_json`、三态更新、`writeTaskRow` 列。
- [ ] **6.7 发布**
  - 只升级宿主二进制；**算法包不重建**（`sdk_version` / `IPC_VERSION` 未变）。
  - 回滚只需下掉宿主（`crop_json` 列对老宿主透明）。

---

## 验收对照

| AC | 证据 |
| :--- | :--- |
| AC1 契约兼容 | `view.active=false` 与今天逐字节一致的 broker 测试 |
| AC2 唯一性与校验 | `ParseCrop`/`ValidateCrop` 表驱动 + 三态更新测试 + 前端 `cropRectSchema` |
| AC3 模型输入 | 合成图案 + 真实包的窗口内容断言（尺寸仍为模型要求） |
| AC4 逆映射 | 4 类结果逐字段 + clamp + 误差预算（window px ≤1） |
| AC5 ROI/MASK | Go 表驱动：`crop+roi+mask` == `roi+mask` |
| AC6 抓拍 | 全幅 JPEG 尺寸/框位置断言（history ring 全幅） |
| AC7 前端 | 真实浏览器用例：绘制/替换/清除/回显/切算法 |
| AC8 门禁 | `make check` 全绿 |
| AC9 旧包零改动 | 未修改的 face/plate/crowd 包在裁剪流上的 e2e 坐标断言 |
| AC10 窗口一致性 | `set_view`/`get_view` 回报 `E` == 实际像素窗口（引擎单点断言 + 日志） |
| AC11 降级不改几何 | CPU vs RGA 输出逐字节相同 |
| AC12 热切换与清除 | 运行中改窗/清除后下一帧生效；包重启后仍是当前窗口 |
