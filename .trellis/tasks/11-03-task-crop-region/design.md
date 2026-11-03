# Design: 任务级裁剪 = AI 流视图（宿主媒体层）

> 契约在任务（相机），像素在宿主 AI 流，几何由引擎单一拥有，逆映射在宿主结果入口。
> 算法包、SDK、IPC 一律不动。

---

## 0. 修订记录

| 版本 | 执行点 | 下发 | 逆映射 | 结论 |
| :--- | :--- | :--- | :--- | :--- |
| 初版 | 算法包骨架 | 并入 `MSG_TASK_CONFIG` | 骨架序列化 | 违反「骨架不解析算法配置」，旧包会丢弃整条配置 |
| R1 | 算法包骨架 | `MSG_STREAM_REGION`（带 `algorithm_id`） | 骨架序列化 | 粒度错成 per-algorithm，同一路流会出现两个坐标系 |
| R2 | 算法包运行库 | `MSG_STREAM_REGION`（流级）+ Broker 缓存重放 | 骨架序列化 | 粒度对了，但**执行点错了**（见 0.1） |
| **R3（本版）** | **宿主 AI 流（媒体层）** | **无**（几何随帧分发隐含） | **宿主 Go 结果入口** | 算法包零改动、零重建；无 IPC/能力门禁/物化回退 |

### 0.1 为什么推翻 R2（不是工作量问题，是正确性问题）

R2 把窗口算在包骨架、像素采样在包运行库，前提是「插件后处理一律以 `image.width()/height()` 为基准」。
这个前提**在本仓不成立**：

- `packages/face_recognition/onnx/src/detection/detector.cpp:317-335`：`orig_w/orig_h` 取 `raw_view()`（全幅），
  而 `ops::letterbox` 回填的 `scale/pad` 会按 R2 的语义变成窗口基准 → de-letterbox 与归一化基准打架。
- `packages/face_recognition/onnx/src/alignment/aligner.cpp:227-228` + `pipeline/face_recognition.cpp:233-240`：
  landmark 像素坐标按 `image.width()` 换算，`crop_and_align_direct` 却按 `raw.width/strides/planes[0]` 采全幅
  → **截脸截错位置，特征照样输出**。
- 同类用法共 **8 个文件**（face onnx/rk3568 的 detector+aligner、plate 的 pipeline+detector、crowd 的
  pipeline+person_detector），全部命中。

也就是说 R2 的「插件零改动」只在文档里成立，落地后是静默错框；而 implement 里「`packages/*/src/**` diff 为空」
的门禁恰好保证不了任何事。R2 的其余补丁（`supports_crop` 能力门禁、Broker 独立缓存、清除时"不发消息"、
RGA 精确 srect + 物化重试 + `offset_align` 话题）都是为「裁剪发生在订阅者进程」付的利息。

**R3 把执行点前移到算法进程之前**：客户端收到的就是一张窗口大小的完整图像，
`width/height/planes/strides` 一起变小，8 个文件天然正确，上面所有补丁全部消失。

---

## 1. 架构拓扑

```txt
相机主码流 ──► Stream(main) ───────────────────────────► 预览 / 录像 / WebRTC（全幅，不动）

相机 AI 流（每 camera 一条物理流，aiStreams；ConsumerAI 拉流 + 每流一个 FrameBroker）
  DecodeSession ──► FramePublisher
                      ├─ 全幅帧 ───────────────► history ring ──► Zhulong_stream_snapshot（AC6）
                      ├─ ViewSnapshot ─────────► MotionDetector（窗口内门禁）
                      └─ publish_frame(frame, view, motion, copy)
                                                    │
                                       FrameBroker（流级）
                                         窗口拷贝: RGA / CPU 行拷贝
                                         紧打包写入 slot（dma slot 或 shm slot）
                                                    │ MSG_FRAME_DISPATCH(width=窗口)
                                                    ▼
                                            算法包（一行不改，看到一张小图）
                                                    │ MSG_RESULT（窗口局部归一化坐标）
                                                    ▼
                        StreamHub.consumeAIResults：Reproject(E) → region.Filter
                                                    ├─► Capture / Alarm / Spatial
                                                    └─► WebSocket 事件（全幅坐标）
```

---

## 2. 六条不变量

1. **窗口只有一个所有者**：引擎的 `ViewWindow`。Go 只保存引擎回报的 `E`，不自己算像素。
2. **几何是纯函数**：`(归一化矩形, 帧尺寸) → 像素窗口`，只做 `floor`/`ceil` 与**统一的外向取偶**；
   硬件对齐不进几何（RGA 不满足就 CPU，窗口不变）。
3. **像素只动一次**：窗口在 broker 写 slot 的那一次拷贝里物化，不新开缓冲、不缩放、不二次遍历。
4. **客户端永远看到一张完整的小图**：`width/height` = 窗口，`planes/strides/offsets` 紧打包。
5. **结果坐标系由流决定**：被裁过的流，所有结果都是窗口局部；宿主逆映射一次。
6. **全幅只存在于 history/preview**：抓拍、录像、预览都不经过窗口。

---

## 3. 数据契约

### 3.1 存储

```sql
ALTER TABLE analysis_tasks ADD COLUMN crop_json TEXT NOT NULL DEFAULT '{}';
```

- 归属任务（= 相机）；`{"crop":[x1,y1,x2,y2]}` 或 `{}`。
- `null` / 缺省 / `[]` / `{}` → 无裁剪；解析后统一归一化为 `nil`。
- `MinCropSide = 0.02`（独立常量，不复用 `region.MinSideLength = 0.01`）。
- 校验失败 → `apperr.Invalid("INVALID_CROP_CONFIG", …)`，不落库。
- **迁移编号**：当前最大是 `000025`；`11-03-alert-push-multi-channel` 也声明了 `000026`，
  落地时取当时的实际最大号 +1，不要硬编码。

### 3.2 `internal/region`

```go
const MinCropSide = 0.02

// ParseCrop: "" / "{}" / `{"crop":null}` / `{"crop":[]}` → (nil, nil)
//             `{"crop":[x1,y1,x2,y2]}` → (*Rect, nil)，校验失败返回错误
func ParseCrop(raw string) (*Rect, error)

// ValidateCrop 用 MinCropSide；不要复用 Rect.Validate()（那里写死 0.01）。
func ValidateCrop(r Rect) error

// Reproject 把窗口局部归一化结果映射回全幅；identity 视图直接返回原值。
func Reproject(res aitypes.InferenceResult, view Rect) aitypes.InferenceResult
```

> Go 不需要 `EffectivePixels`：像素窗口由引擎算，Go 只消费回报值。

### 3.3 HTTP

- 响应：`TaskResponse.Crop *region.Rect \`json:"crop"\``（`null` 表示无裁剪）。
- 创建 / 按相机 upsert（`CreateTaskRequest`）是**全量替换**：`crop` 缺省 = 不裁剪。
- `PUT /tasks/:id`（`UpdateTaskRequest`）是**合并**语义，必须三态：

```go
// 省略 = 不改；null = 清除；数组 = 设置。用 RawMessage 保留三态。
Crop json.RawMessage `json:"crop,omitempty"`
```

- `ApplyTo` 在 `req.Algorithms != nil` 时提前 `return`，crop 处理必须放在该分支之前。
- `writeTaskRow` 是显式列 map（`internal/task/store.go:230-235`），**必须补 `crop_json` 列**，
  否则"保存成功、重开还是旧的"。
- `region_config_json` 的语义与位置不变。

---

## 4. 流视图（引擎）

### 4.1 窗口计算（纯函数）

```txt
x1 = clamp(floor(nx1 * W), 0, W-1)      x2 = clamp(ceil(nx2 * W), x1+1, W)
y1 = clamp(floor(ny1 * H), 0, H-1)      y2 = clamp(ceil(ny2 * H), y1+1, H)

// 统一外向取偶（所有格式，不是硬件探测值）：
//   起点向下取偶、终点向上取偶，尺寸因此必为偶数，4:2:0 的色度对齐自动满足。
//   代价每边 ≤1 px，换来 RK3568 offset_align=2 下 RGA 恒定可用。
x1 &= ~1 ; y1 &= ~1 ; x2 = (x2 + 1) & ~1 ; y2 = (y2 + 1) & ~1

E = [x1/W, y1/H, x2/W, y2/H]            // 逆映射唯一依据
```

- **不做硬件对齐外扩**：RGA 的 `offset_align_` / `stride_align_` 只影响*怎么拷*，不影响*拷哪块*。
- `E` 与像素窗口严格对应，误差只剩 `1/W`、`1/H`。
- 窗口按 `(归一化配置, 帧尺寸)` 缓存，`video_meta_` 更新时重算。

### 4.2 归属与生命周期

- 视图属于 `Stream`（AI 流），不是 broker、不是 publisher、更不是包。
- `Stream` 保存归一化配置；`view_snapshot()` 返回当前 `ViewWindow{active,x,y,w,h}`。
- 流重建（AI 消费者全撤 → 再接入）会新建 `Stream`，**视图随流消失**；宿主在 acquire 后重放。
  没有跨流缓存 ⇒ 没有"清不掉"的旧窗口。

### 4.3 C ABI（`native/include/Zhulong/engine.h`）

```c
typedef struct {
    float x1, y1, x2, y2;   /* 严格归一化 [0,1]；{0,0,1,1} 表示全幅/未设置 */
} Zhulong_view_rect;

/* 设置 AI 流视图。view == NULL 或 {0,0,1,1} 表示清除。 */
Zhulong_status_t Zhulong_stream_set_view(Zhulong_engine_h engine, Zhulong_stream_id stream,
                                         const Zhulong_view_rect *view,
                                         Zhulong_view_rect *out_effective);

/* 查询当前生效的 E（未设置或尚未生效时返回 {0,0,1,1}）。 */
Zhulong_status_t Zhulong_stream_get_view(Zhulong_engine_h engine, Zhulong_stream_id stream,
                                         Zhulong_view_rect *out_effective);
```

语义（写进头文件注释）：

- **非 acquire**：流不存在 → `ZHULONG_ERR_NOT_FOUND`，绝不顺手拉起拉流。
  （`StreamHub.ResolveAIStreamID` 内部会 `ResolveAIStreamBrokerPath` 去 acquire，**禁止**在保存路径上用它。）
- 分辨率未知 → `ZHULONG_ERR_NOT_READY`：配置**已保存**，但视图暂不生效（保持全幅）；
  视频元数据到达后引擎自动计算，`get_view` 与后续帧立即使用。
- 非法坐标（非有限、越界、退化、`< MinCropSide`）→ `ZHULONG_ERR_INVALID_ARGUMENT`。
- 幂等；同值重复设置不改变 `E`。
- `out_effective` 可为 `NULL`。

### 4.4 Go 侧接线

- `camera` 域新增只读接口（由 `internal/app` 适配到 `task.Store.GetTaskByCamera`）：

```go
type TaskCropProvider interface {
    CropFor(ctx context.Context, cameraID string) (*region.Rect, bool)
}
func (h *StreamHub) SetTaskCropProvider(p TaskCropProvider)
func (h *StreamHub) RefreshTaskCrop(ctx context.Context, cameraID string) // 幂等重放
```

- `camera.MediaStream` 接口增 `SetView` / `GetView`（`internal/camera/stream_media.go`）。
- `aiStreamRef` 增 `streamID uint64`、`view region.Rect`、`viewPrev region.Rect`、`viewChangedAt time.Time`。
- 应用点：
  1. `acquireAIStreamBrokerPath` 成功后（含失败恢复重放）调用 `SetView`；
  2. `NOT_READY` 时置 `viewPending`，在**首个结果批次**里用 `GetView` 收敛（结果必然晚于视频元数据）；
  3. 释放 / 关闭时清空本地视图（引擎那边的流也随之销毁）。
- 每批结果调用一次 `GetView`（单次 CGO，量级与已有的 `sub.Next` 相同），捕获分辨率重协商后的新 `E`；
  返回值变化时按 `viewPrev/viewChangedAt` 滚动。

### 4.5 变更通知与热更新

- `task.Service` 已有一个 `TaskChangeListener`（重启算法包）；**新增**一个按相机语义的通知：

```go
type TaskSavedListener interface { OnTaskSaved(ctx context.Context, taskID, cameraID string) }
```

- 在 Create / Update / Delete 保存成功后通知；`internal/app` 把它接到 `streamHub.RefreshTaskCrop`。
- **只改 crop 时不重启算法进程**：`UpdateTask` 已加载旧任务，比较算法子树（enabled / sampleFps /
  configJson / regionConfigJson / scheduleJson）是否变化；只 crop 变 → 跳过 `notifyTasksChanged`，
  只发 `OnTaskSaved`。其他任何字段变 → 两者都发（保持现有行为）。
- `SaveTask`（按相机 upsert）同样先取 `GetTaskByCamera` 做同一比较（多一次读，保存路径可接受）。
- 删除任务：AI 流随订阅释放而销毁，视图随之消失；Hub 在释放时清本地状态。
- 流尚未建立时 `SetView` 返回 `NOT_FOUND`，这是**正常情况**（acquire 时会重放），不记 warning。
- 因此：**改 crop = 下一帧生效，算法进程不重启、不断流**。

---

## 5. 物化（引擎）

### 5.1 发布顺序

`FramePublisher::publish_loop`（`native/src/pipeline/frame_publisher.cpp`）：

```txt
acquire frame
  → sample_fps 节流（丢帧不产生任何成本）
  → view = stream->view_snapshot()
  → motion_detector.detect(frame, view, motion)     // 门禁只回答"窗口内有没有运动"
  → broker->publish_frame(frame, view, motion, copy) // 窗口在这里物化
  → push_frame(frame)                               // history 永远全幅（AC6）
```

- 节流在物化之前，成本按 `sample_fps` 计，不按相机帧率。
- `has_motion` 语义天然收紧到窗口；`motion_boxes` 下游无人消费，320×180 网格在非 16:9 窗口上的拉伸无影响。

### 5.2 Broker 写 slot

`FrameBroker::publish_frame(const HardwareFrame&, const ViewWindow&, const FrameMotionInfo&, CropCopyFn)`：

- `view.active == false` → **今天的行为逐字节不变**：`dma_fd >= 0` 直通，否则分平面 memcpy。
- `view.active == true` → 一律写 slot（`dma_slots_` 可用则写 dma slot，否则写 shm slot），
  紧打包布局，`MSG_FRAME_DISPATCH` 报窗口尺寸。

紧打包布局（与 `frame_payload_bytes` / `resolve_source` 的校验一一对应）：

| 格式 | `dst_stride` | `offsets` | `strides` |
| :--- | :--- | :--- | :--- |
| BGR24/RGB24 | `align_up(w*3, 192)` | `[0]=0` | `[0]=dst_stride` |
| NV12 | `align_up(w, 64)` | `[0]=0`、`[1]=dst_stride*h` | `[0]=[1]=dst_stride` |
| YUV420P | `align_up(w, 64)` | `[0]=0`、`[1]=dst_stride*h`、`[2]=dst_stride*h+(dst_stride/2)*(h/2)` | `[0]=dst_stride`、`[1]=[2]=dst_stride/2` |

- 64 字节对齐覆盖 `stride_align_` 探测值（RK3568 debugfs 实测 4）与未来更大值；
  packed 用 192（`3 × 64`）同时满足 `%3==0` 与 `%64==0`。
- 紧打包后客户端侧 `resolve_source` 的三个校验全部天然成立：
  `y_stride % plane0_bpp == 0`、`y_stride % stride_align == 0`、
  `deduce_continuous_hstride` 推出的 `hstride == h`（`native/src/backends/rga/rga_vision_backend.cpp:243-305`）。
- `slot_size` 恒 ≥ 全幅，窗口一定装得下。

### 5.3 窗口拷贝（宿主侧，按目录规则分层）

```cpp
// native/src/common/view_window.hpp —— header-only；宿主与 AlgoIpc 共享类型，不新增 .o
typedef struct { bool active; int x, y, w, h; } ViewWindow;
typedef bool (*CropCopyFn)(const HardwareFrame&, const ViewWindow&, uint8_t* dst, int dst_stride);
```

| 文件 | 归属目标 | 职责 |
| :--- | :--- | :--- |
| `src/common/view_window.hpp` | 仅头文件 | `ViewWindow` + `CropCopyFn` 契约 + 纯函数窗口计算 |
| `src/nodes/preprocess/window_crop.{hpp,cpp}` | `ZhulongEngine` | CPU 分平面行拷贝 + 后端选择 |
| `src/backends/rga/rga_host_loader.{hpp,cpp}` | `ZhulongEngine` | **唯一**包含 vendored RGA 头的 TU；dlopen `librga.so.2` + `imcheck_t`/`improcess` |

- **不能放进 `ZhulongAlgoIpcObjects`**：那批对象也归档进算法包 SDK（`libzhulong_algo_sdk.a`），
  宿主侧的窗口拷贝不得进入包归档。因此 `FrameBroker::publish_frame` 只接收一个 `CropCopyFn`：
  `Engine` 启动时构造 `WindowCrop` → `FramePublisher` 持有 → 调 `broker->publish_frame(frame, view, motion, copy)`。
- `rga`：`srect{view.x,view.y,w,h}` → `drect{0,0,w,h}`，预检失败/库缺失/`/dev/rga` 不可访问 → 该帧走 `cpu`，
  **几何不变**。与 `mpp_loader.cpp` 同一 dlopen 模式，宿主仍是单产物。
- `cpu`：分平面逐行 memcpy。dmabuf 源需要 `mmap` + `DMA_BUF_IOCTL_SYNC(START/END)`（复用
  `motion_detector` 已建立的模式，抽公共 helper）。
- 计数器：`window_rga_count` / `window_cpu_count`，接入现有日志，板端能一眼看出"一直在走 CPU"。
- **不引入缩放**：缩放仍由算法包自己的 letterbox 完成，避免二次重采样与额外容差。

### 5.4 成本

| 场景 | 今天 | 加窗口后 |
| :--- | :--- | :--- |
| RK3568 MPP dmabuf + RGA | fd 直通，0 拷贝 | RGA 同格式拷到 slot（1 次硬件拷贝，窗口字节） |
| RK3568 MPP dmabuf + 无 RGA | 0 拷贝 | mmap+sync+行拷贝到 shm slot（1 次 CPU 拷贝） |
| x86 软解 host frame | 分平面 memcpy 到 slot | 同一份 memcpy 变成窗口行拷贝（0 额外成本） |
| 无客户端 / 被节流丢弃 | 不写 slot | 不写 slot |

客户端帧变小 ⇒ 每个包的 letterbox 读取量与总线流量下降；总成本与包数量无关。

---

## 6. 逆映射（宿主 Go）

### 6.1 收口点

`internal/camera/stream_hub.go` 的 `consumeAIResults`：`ParseInferenceResult` 之后、`region.Filter` 之前。

```go
view := h.viewFor(cameraID, res.Timestamp)   // 处理在途帧的旧窗口
res = region.Reproject(res, view)
```

### 6.2 字段清单（`internal/aitypes`）

| 结果类型 | 字段 |
| :--- | :--- |
| detection | `Detections[].Box` |
| person_structure | `Persons[].BodyBox`、`Persons[].Faces[].Box`、`.Landmarks` |
| vehicle_structure | `Vehicles[].VehicleBox`、`Vehicles[].Plates[].Box` |
| scene_structure | `Faces[].Box`、`.Landmarks`（+ 与 person 同构的字段，若载荷包含） |
| classification / beacon | 无坐标，原样透传 |

- 逐轴线性：`g = E.origin + c * E.span`，再 clamp 到 `[0,1]`；NaN 输入直接丢弃该字段（保持有限）。
- 不用在 C++ 里逐字段映射——这正是 R3 删掉的一类维护负担。

### 6.3 换窗边界

- 每次 `view` 变化：`viewPrev = 旧 view`、`viewChangedAt = now`。
- 结果用 `res.Timestamp`（由 `capture_time_ms` 解析，`internal/aitypes/model.go:219`，同一宿主时钟）比较：
  早于 `viewChangedAt` 的用 `viewPrev`，否则用当前 `E`。
- 只保留一代旧窗口即可覆盖在途帧（每客户端 ≤2 帧）。

### 6.4 旧包为什么天然正确

窗口是宿主在分发前施加的，与包版本无关：**被裁过的流上，任何包产出的都是窗口局部坐标**。
不存在"这个包不知道 crop"的分支，因此没有 `supports_crop`、没有坐标系协商、没有错位风险。

---

## 7. 透明性与已知限制

### 7.1 透明

1. ROI/MASK 仍吃全幅坐标（`filter.go` 不变）；
2. 抓拍/录像/预览走全幅 history 与主码流；
3. 前端叠加层无差异；
4. 同一路流上所有算法共享同一窗口（同一 `E`），告警/抓拍/融合不会互相错位。

### 7.2 限制

| 限制 | 说明 |
| :--- | :--- |
| 一路相机一个窗口 | 目标语义；per-algorithm 覆盖不在本期 |
| 窗口外目标不可见 | 语义如此；边界目标会被截断成小框，ROI/MASK 按底边中心判定，截断框的判定可能与直觉不同 |
| 非 16:9 改变算法输入视场 | 像素阈值以 `image.width()/height()` 为基准；假设固定宽高比的算法会看到不同形状，写进插件契约 |
| 换窗 1–2 帧在途 | 用 `capture_time_ms` 选窗口，边界帧可能用任一窗口，两者都合法 |
| RGA 不可用时的 CPU 拷贝 | 1080p 裁 50% ≈ 1.5 MB/帧；5–20 fps 下可接受，计数器可观测 |
| 运动门禁只回答窗口内 | 语义正确；不做窗口内分区 |

---

## 8. 兼容 / 迁移 / 回滚

| 场景 | 行为 |
| :--- | :--- |
| 升级前的老任务 | `crop_json='{}'` → 视图未激活，行为 100% 不变 |
| 新宿主 + 老算法包 | **直接生效**（包看到小图，坐标由宿主映射），无需重建 |
| 老宿主 + 新前端 | 老宿主忽略 `crop` 字段/列 → 全幅；前端不显示配置入口 |
| 回滚 | `down` 迁移删列；引擎 `set_view(NULL)`；无残留状态（视图随流销毁） |
| 算法包回滚 | 与本功能无关（`sdk_version` / `IPC_VERSION` / `zhulong_frame_view` 均未变） |

---

## 9. 验证

### 9.1 开工前的 spike（板端半天）

1. `improcess` 同格式窗口拷贝 → shm/VA 目标是否被驱动接受、RGA 写入后的缓存一致性（读回校验）。
2. 紧打包窗口（`dst_stride=align_up(w,64)`、`offsets[1]=dst_stride*h`）通过真实 face 包
   `resolve_source` 全部校验。
3. 非 16:9 窗口 + 目标 `sample_fps` 下的每帧成本与 CPU 占用。

任一条不通过：退回 CPU-only 物化（架构不变，只影响性能）。

### 9.2 测试矩阵

- **窗口纯函数**：1080p/720p、奇数宽高、贴四边、`x2=1.0`、`crop==整帧`、极小窗口。
- **物化定位**：合成图案（四象限 + 非对称角标）断言 slot 内容 == 期望窗口；RGB24/BGR24/NV12/YUV420P。
- **无视图回归**：`view.active=false` 时输出与原路径逐字节一致（含 dmabuf 直通的 dispatch 字段）。
- **降级**：CPU 与 RGA 输出逐字节相同（同格式纯拷贝）。
- **分发契约**：窗口帧的 `width/height/strides/offsets` 满足 `resolve_source` 的三个校验。
- **Go 逆映射**：4 类结果逐字段、clamp、identity 快路径、`viewPrev` 边界。
- **Hub**：`NOT_READY` → 结果批次收敛；流重建后重放；释放后清空。
- **抓拍**：全幅 JPEG 尺寸/框位置。
- **前端**：真实无头浏览器，绘制/替换/清除/切算法不丢。

### 9.3 风险

| 风险 | 缓解 |
| :--- | :--- |
| RGA 写 shm 的缓存一致性 | spike 1；不行则 CPU 行拷贝 |
| 宿主新增 dlopen 依赖 | 与 MPP 同模式；探测失败即 CPU，功能不降级 |
| 动了 publish 路径 | 改动限定在"写 slot 的那一次拷贝"与"取窗口快照"；history/preview 顺序不变 |
| 分辨率重协商 | 引擎重算窗口；Go 每批 `GetView` 收敛；e2e 覆盖 |
| 迁移号冲突 | 落地时按当时最大号 +1，不硬编码 000026 |

---

## 10. 被否决的方案（避免回潮）

| 方案 | 否决理由 |
| :--- | :--- |
| 包内执行（R1/R2） | 8 个文件混用两套坐标基准，"零改动"不成立，失败静默 |
| `MSG_STREAM_REGION` + Broker 缓存 | 清除需要"发一条 has_crop=0"才能覆盖缓存；没有消息就没有这个类 |
| `supports_crop` 能力门禁 | manifest 未落库，要加第二对迁移；且门禁不能保证坐标正确 |
| 骨架 + 运行库各算一次窗口 | 三层一致性靠人守；R3 用"引擎算、Go 存"消灭 |
| RGA 精确 srect + 物化重试 | 失败参数是源偏移，同一个 srect 救不了自己；且几何一变 AC4 就崩 |
| 宿主逐客户端裁剪 | Broker slot 是全客户端共享的，per-client 会破坏零拷贝模型 |
