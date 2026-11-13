# Design: 任务级裁剪 = AI 流视图（宿主媒体层）

> 契约在任务（相机），像素在宿主 AI 流，几何由引擎单一拥有，逆映射在宿主结果入口。
> 算法包、算法 SDK、IPC 布局一律不动；宿主 `Zhulong` C ABI 新增视图接口并升版本号。

---

## 0. 修订记录

| 版本 | 执行点 | 下发 | 逆映射 | 结论 |
| :--- | :--- | :--- | :--- | :--- |
| 初版 | 算法包骨架 | 并入 `MSG_TASK_CONFIG` | 骨架序列化 | 违反「骨架不解析算法配置」，旧包会丢弃整条配置 |
| R1 | 算法包骨架 | `MSG_STREAM_REGION`（带 `algorithm_id`） | 骨架序列化 | 粒度错成 per-algorithm，同一路流会出现两个坐标系 |
| R2 | 算法包运行库 | `MSG_STREAM_REGION`（流级）+ Broker 缓存重放 | 骨架序列化 | 粒度对了，但**执行点错了**（见 0.1） |
| **R3（本版）** | **宿主 AI 流（媒体层）** | **无控制消息**（几何随帧分发隐含） | **宿主 Go 结果入口** | 算法包零改动、零重建；无 IPC/能力门禁/物化回退 |
| **R3.1（评审修订）** | 同上 | 同上；**`capture_time_ms` 提前到视图快照点** | 同上；按引擎 `E` + `effective_since_ms` | 修边界时钟、首帧/重放、奇数尺寸、零拷贝分叉、`CropCopyFn` 上下文、迁移号与文档路径 |
| **R3.2（评审修订）** | 同上 | 同上 | 同上 | 窗口几何改以**解码帧尺寸**为基准（`view_snapshot` 内计算）；修迁移号冲突、C ABI 阈值/哨兵/错误码、`ApplyTo` 错误出口、apply 串行化、通知 ctx、裸 fd 同步 helper、`slot_size` 口径、前端保存门禁 |

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
                      ├─ ViewSnapshot {window, capture_time_ms}
                      │     └───────────────► MotionDetector（窗口内门禁）
                      └─ publish_frame(frame, view, motion, copy, capture_time_ms)
                                                    │
                                       FrameBroker（流级）
                                         窗口拷贝: RGA / CPU 行拷贝
                                         紧打包写入 slot（dma slot 或 shm slot）
                                         写成功后填 MSG_FRAME_DISPATCH(width=窗口,
                                                       capture_time_ms=视图快照时钟)
                                                    ▼
                                            算法包（一行不改，看到一张小图；
                                                    原样回传 capture_time_ms）
                                                    │ MSG_RESULT（窗口局部归一化坐标）
                                                    ▼
                        StreamHub.consumeAIResults：
                          GetView(E, effective_since_ms) → Reproject(按 capture_time 选窗)
                          → region.Filter
                                                    ├─► Capture / Alarm / Spatial
                                                    └─► WebSocket 事件（全幅坐标）
```

**边界时钟**：`FramePublisher` 在取视图快照的同一把锁内生成 `capture_time_ms`；引擎在
`set_view` 改变配置、或帧尺寸变化导致 `E` 重算时生成 `effective_since_ms`。二者严格有序
（毫秒不够时用 `max(now, last_capture+1)` 推进），Host 只按这两个值判断旧/新窗口，
不使用 Go 本地改窗时间。

---

## 2. 七条不变量

1. **窗口只有一个所有者**：引擎的 `ViewWindow`。Go 只保存引擎回报的 `E` 与 `effective_since_ms`，不自己算像素。
2. **几何是纯函数**：`(归一化矩形, 帧尺寸) → 像素窗口`，只做 `floor`/`ceil`、**统一的外向取偶**，
   并把取偶后的端点**夹回帧边界**；奇数宽高不得产生 `x2 > W` 或 `y2 > H`。
   这里的“帧尺寸”永远是**解码帧**的 `width/height`（不是 RTSP 元数据），且几何在取快照时按本帧尺寸求解。
3. **像素只动一次**：窗口在 broker 写 slot 的那一次拷贝里物化，不新开缓冲、不缩放、不二次遍历。
4. **客户端永远看到一张完整的小图**：`width/height` = 窗口，`planes/strides/offsets` 紧打包。
5. **结果坐标系由流决定**：被裁过的流，所有结果都是窗口局部；宿主按引擎回报的 `E` 逆映射一次。
6. **全幅只存在于 history/preview**：抓拍、录像、预览都不经过窗口。
7. **视图快照与 capture_time 原子**：窗口、`capture_time_ms`、`effective_since_ms` 由同一把
   `Stream::mutex` 串起来；任何“旧窗口像素 + 新时间戳”或“新窗口像素 + 旧时间戳”的组合都不允许出现。

附加生命周期不变量：**视图属于 `Stream`，不属于 AI 订阅**。同一物理 `Stream` 可能同时被
preview/recording 持有；AI 订阅全撤不等于 `Stream` 销毁，因此“视图随流消失”不能作为清除依据，
必须显式清空/重放。

---

## 3. 数据契约

### 3.1 存储

```sql
ALTER TABLE analysis_tasks ADD COLUMN crop_json TEXT NOT NULL DEFAULT '{}';
```

- 归属任务（= 相机）；`{"crop":[x1,y1,x2,y2]}` 或 `{}`。
- `null` / 缺省 / `[]` / `{}` → 无裁剪；解析后统一归一化为 `nil`；`[0,0,1,1]` 也归一化为 `nil`。
- `MinCropSide = 0.02`（独立常量，不复用 `region.MinSideLength = 0.01`）。
- 校验失败 → `apperr.Invalid("INVALID_CROP_CONFIG", …)`，不落库；三语文案必须加进
  `internal/task/locale.go`，否则 `httputil.WriteError` 会回落到英文 `apperr.Message`。
- DB 只存储 JSON，不提供单矩形/范围的关系约束；“单矩形”由 API/前端/引擎入口共同保证。
- **迁移编号**：仓库当前实际最大是 `000031`（`000030_create_alert_destinations`、
  `000031_create_system_settings` 已落）；`000026` 仍为空号。**本任务取 `000032`**，
  落地前再核对一次实际 max（不要相信文档里的数字）。
  ⚠️ `golang-migrate` 的 `Up()` 只应用 version > `schema_migrations.version` 的迁移；
  后续任务只能取当时 max+1，不能回头补号。
- **迁移编号检查**：只断言“唯一 + 单调递增 + up/down 成对存在”，**不要断言连续**——现存的
  `000026` 空洞就是反例，连续性断言会让测试树永远红。

### 3.2 `internal/region`

```go
const MinCropSide = 0.02

// Identity 返回全幅视图。
func Identity() Rect

// ViewState 是引擎回报的生效窗口：Since 之前发布的帧属于上一代窗口。
type ViewState struct {
    Rect  Rect       // 生效 E，严格归一化
    Since time.Time  // 生效起点；零值表示尚未发生过窗口切换
}

// ParseCrop:
//   "" / 空白 / "{}" / "null" / 顶层 "[]" /
//   {"crop":null} / {"crop":[]} → (nil, nil)
//   {"crop":[x1,y1,x2,y2]}     → (*Rect, nil)，整帧归一化为 nil
//   顶层 [x1,y1,x2,y2]          → 与 {"crop":[...]} 等价的别名（手工 SQL/旧格式兜底）
//   其他顶层数组长度 / 校验失败   → error
//   未知键忽略（向前兼容）
func ParseCrop(raw string) (*Rect, error)

// ValidateCrop 用 MinCropSide；不要复用 Rect.Validate()（那里写死 0.01）。
// 必须显式拒绝 NaN/Inf；0 <= x1 < x2 <= 1；边长 >= MinCropSide。
func ValidateCrop(r Rect) error

// Rect.UnmarshalJSON 必须要求恰好 4 个数字；不能依赖 Go 数组的“多余元素忽略/缺失补零”。
// Rect 与 ROI/MASK 共用；已有 4 元数组不受影响，畸形数据会更早报错。
// ParseCrop 对存储 JSON 里的数组也显式检查长度 == 4。
//   ⚠️ 这会改变 ROI/MASK 存储解析的失败模式：`[]` / 长度 3/5 从“校验失败”变成“解析失败”，
//   错误码仍是 INVALID_REGION_CONFIG；实现时顺手确认 store/region 测试不依赖旧的截断容忍。
func (r *Rect) UnmarshalJSON(b []byte) error

// Reproject 把窗口局部归一化结果映射回全幅；identity 视图直接返回原值。
func Reproject(res aitypes.InferenceResult, view Rect) aitypes.InferenceResult
```

> Go 不需要 `EffectivePixels`：像素窗口由引擎算，Go 只消费 `E` 与 `Since`。
> **不要再定义一个与 `ViewState.Since` 语义不同的本地改窗时间。**
>
> `MinCropSide` 的权威只在 Go：HTTP、存储、引擎入口共用 `ValidateCrop`。宿主 C ABI 只做
> **结构性**校验（有限、`0 <= x1 < x2 <= 1`、非退化），不在 float32 上比较 `0.02`
> （`0.02f ≈ 0.0199999996`，边界值会被误拒）。

### 3.3 HTTP

- 响应：`TaskResponse.Crop *region.Rect \`json:"crop"\``（`null` 表示无裁剪）。
- 创建 / 按相机 upsert（`CreateTaskRequest`）是**全量替换**：`Crop *region.Rect`；
  缺省/nil 或 `[0,0,1,1]` = 不裁剪。`Rect.UnmarshalJSON` 要求恰好 4 个数字；
  `[]`/`{}`/长度 3/5 都不是合法请求形状（400）。
  存储/响应/引擎入口仍把 `[]`/`{}` 当作无裁剪。需要显式清空请走 `PUT /tasks/:id`。
- `PUT /tasks/:id`（`UpdateTaskRequest`）是**合并**语义，必须三态：

```go
// 省略       = 不改
// null / [] / {} = 清除
// [x1,y1,x2,y2] = 设置
// 其他       = INVALID_CROP_CONFIG
Crop json.RawMessage `json:"crop,omitempty"`
```

- `ApplyTo` 在 `req.Algorithms != nil` 时提前 `return`，而且**没有返回值**，扛不了 crop 的 400。
  三态解析与校验必须放进独立的 `func (req *UpdateTaskRequest) ApplyCrop(t *Task) error`，由
  `Service.UpdateTask` 在 `ApplyTo` **之前**调用：先解析校验、再改内存，失败直接返回
  `INVALID_CROP_CONFIG`，不落库。
- `writeTaskRow` 是显式列 map（`internal/task/store.go:221-235`），**必须补 `crop_json` 列**，
  否则"保存成功、重开还是旧的"。
- `TaskResponse.ToResponse` 从 `CropJSON` 解析；解析失败按 `nil` 返回，并在 service 读取路径
  记一次 warning（不要每行刷屏）。`TaskCropProvider` 解析失败返回 error，由 Hub 记录并保留当前视图。
- `region_config_json` 的语义与位置不变。
- `INVALID_CROP_CONFIG` 加入 `internal/task/locale.go`（en / zh-Hans / zh-Hant）。
- `make api-docs` 会重写 `internal/apidocs/{docs.go,swagger.json,swagger.yaml}` **三份**，三份都要提交；
  生成后确认 `crop` 出现在 `TaskResponse` 与两个请求体（`CreateTaskRequest` / `UpdateTaskRequest`）里。

---

## 4. 流视图（引擎）

### 4.1 窗口计算（纯函数）

下文的 `W`/`H` 就是本帧的 `width`/`height`（解码帧，不是 RTSP 元数据）。

```txt
若 W < 2 或 H < 2：未激活。
若归一化矩形 == {0,0,1,1}：未激活（整帧归一化为无裁剪）。

x1 = clamp(floor(nx1 * W), 0, W-1)
x2 = clamp(ceil (nx2 * W), x1+1, W)
y1 = clamp(floor(ny1 * H), 0, H-1)
y2 = clamp(ceil (ny2 * H), y1+1, H)

// 统一外向取偶（所有格式，不是硬件探测值）
x1 &= ~1 ; y1 &= ~1
x2 = min(W & ~1, (x2 + 1) & ~1)
y2 = min(H & ~1, (y2 + 1) & ~1)

// 奇数宽高在右/下边缘会退化；把起点左/上推 2px，保持 2px 最小窗口
if (x2 < 2 || y2 < 2) 未激活
if (x1 > x2 - 2) x1 = x2 - 2
if (y1 > y2 - 2) y1 = y2 - 2

E = [x1/W, y1/H, x2/W, y2/H]            // 逆映射唯一依据
```

- **关键修正（R3.1）**：取偶后必须 `min(W & ~1, …)`，否则 `W` 为奇数且 `nx2=1.0` 时
  `x2` 会变成 `W+1`，RGA/CPU 直接越界。design 的测试矩阵必须覆盖 `1919×1079`。
- **不做硬件对齐外扩**：RGA 的 `offset_align_` / `stride_align_` 只影响*怎么拷*，不影响*拷哪块*。
- `E` 与像素窗口严格对应，误差只剩 `1/W`、`1/H`。
- **尺寸来源只有一个：解码帧的 `width/height`**（`HardwareFrame`），不是 RTSP 元数据
  （`codecpar` / `video_meta_`）。二者在 1088 裁切、分辨率热切换、MPP 对齐等场景会不一致：
  用元数据算 `E` 再切帧，轻则 `E` 的分母错（映射偏移至多 ~0.7%），重则 `y+h > frame.height`
  触发每帧 `fail_publish`，整路 AI 静默停摆。因此窗口在**取快照时按本帧尺寸**计算，按
  `(归一化配置, frame_w, frame_h)` 缓存；尺寸变化视作一次几何变更（推进 `effective_since_ms`）。
- 取整后若窗口恰好等于整帧（`x1==0 && y1==0 && x2==W&~1 && y2==H&~1`），按 `inactive` 处理：
  省一次拷贝，也让“无裁剪”路径保持字节等价。
- 像素格式不参与窗口边界计算；它只决定 5.2 的 `strides/offsets`。

### 4.2 归属、生命周期与快照时钟

- 视图属于 `Stream`（AI 流），不是 broker、不是 publisher、更不是包。
- `Stream` 在 `mutex` 下保存：归一化配置（`pending_rect_`）、当前 `ViewState`、几何缓存
  `(rect, frame_w, frame_h) → E + window`、`last_capture_ms_`。
- `view_snapshot(frame_w, frame_h)` 在**同一把锁**内返回：

```cpp
struct ViewSnapshot {
    bool active;
    int x, y, w, h;
    int64_t capture_time_ms;  // 单调推进的宿主 UTC 毫秒
};
```

  `capture_time_ms = max(now_ms, last_capture_ms_)`；`last_capture_ms_` 只增不减。
  **几何就在这个函数里解析**：若 `(pending_rect_, frame_w, frame_h)` 与缓存不同，重算 `E` 并把它
  当作一次几何变更——`effective_since_ms = max(now_ms, last_capture_ms_ + 1)`，同时把
  `last_capture_ms_` 推到该值。这样“像素用哪个窗口”和“结果用哪个 `E`”仍由同一次加锁串起来。

- `set_view()` 只改归一化配置：
  - 已有几何缓存（至少切过一帧）且配置同值 → 幂等，不推进 `effective_since_ms`；
  - 已有几何缓存且配置变了 → 立即按缓存尺寸重算 `E` 并推进一次 `since`
    （`max(now_ms, last_capture_ms_ + 1)`，同时推进 `last_capture_ms_`），保证：
    - 改窗前已取快照的旧帧：`capture_time_ms < effective_since_ms`；
    - 改窗后取快照的新帧：`capture_time_ms = max(now, last_capture_ms_) >= effective_since_ms`；
  - 还没有任何解码帧（无尺寸）→ 只记录 `pending_rect_`，`ViewState` 保持 `{0,0,1,1}, 0`，
    返回 `NOT_READY`；配置在首个 `view_snapshot` 里生效并同时推进 `since`。
  只推进 `since` 而不推进 `last_capture_ms_` 会让改窗后的第一帧仍落在旧时间戳，必须避免。
- `on_ready()` **不再计算几何**：它只更新元数据与日志。尺寸变化的几何后果由下一次
  `view_snapshot` 按真实帧尺寸落地（并推进 `since`）。
- `get_view()` 返回当前 `ViewState`；还没有切过任何帧时返回 `{0,0,1,1}, 0`，与 `NOT_READY` 的返回值一致。
- **流重建的准确说法**：
  - 所有消费者都释放 → `Stream` 销毁，视图自然消失；
  - **只有 AI 消费者全撤、preview/recording 仍持有同一 `Stream` 时，`Stream` 不销毁，视图也不会自动消失**。
    因此最后一次 AI 订阅释放时必须显式 `SetView(NULL)`；下一次 AI acquire 必须按 DB 当前 crop 重新应用。
  - 没有跨流缓存；显式清空是唯一正确的清除依据。

### 4.3 C ABI（`native/include/Zhulong/engine.h`）

```c
typedef struct {
    float x1, y1, x2, y2;   /* 严格归一化 [0,1]；{0,0,1,1} 表示全幅/未设置 */
} Zhulong_view_rect;

typedef struct {
    Zhulong_view_rect rect;        /* 生效 E */
    int64_t effective_since_ms;    /* 宿主 UTC 毫秒；>= 该值的帧使用 rect */
} Zhulong_view_state;

/* 设置 AI 流视图。view == NULL 或 {0,0,1,1} 表示清除。
   out_effective 可为 NULL。 */
Zhulong_status_t Zhulong_stream_set_view(Zhulong_engine_h engine, Zhulong_stream_id stream,
                                         const Zhulong_view_rect *view,
                                         Zhulong_view_state *out_effective);

/* 查询当前生效 E 与生效起点（未设置时 rect={0,0,1,1}, since=0）。 */
Zhulong_status_t Zhulong_stream_get_view(Zhulong_engine_h engine, Zhulong_stream_id stream,
                                         Zhulong_view_state *out_effective);
```

语义（写进头文件注释）：

- **非 acquire**：流不存在 → `ZHULONG_ERR_NOT_FOUND`，绝不顺手拉起拉流。
  （`StreamHub.ResolveAIStreamID` 内部会 `ResolveAIStreamBrokerPath` 去 acquire，**禁止**在保存路径上用它。）
- **清空永远成功**：`view == NULL` 或 `{0,0,1,1}` 无条件清除配置并推进 `effective_since_ms`；
  即使还没有解码帧也返回 `OK`。
- **非空窗口 + 还拿不到解码帧尺寸** → `ZHULONG_ERR_NOT_READY`：配置已保存，但视图暂不生效
  （保持全幅）；首个 `view_snapshot` 会按真实帧尺寸计算并推进 `effective_since_ms`，后续
  `get_view`/帧立即使用。`out_effective` 此时返回 `{0,0,1,1}, 0`。
- 非法坐标（非有限、`0 <= x1 < x2 <= 1`、退化）→ `ZHULONG_ERR_INVALID_ARGUMENT`。
  ⚠️ **C 侧不比较 `MinCropSide`**：0.02 过 float32 是 `0.0199999996`，边界值会被误拒；
  最小边长的权威在 Go 的 `ValidateCrop`。
- 幂等；同值重复设置不改变 `E`，也不推进 `effective_since_ms`。
- 新增 `Zhulong_ERR_NOT_READY = -17`（当前空号）。Go 侧必须同步：`internal/engine/errors.go` 的
  `StatusNotReady` 常量与 `statusMessages` 文案（`mapNativeStatus` 是通用的，不用改）。
  **不要新增 `ErrStreamNotFound`**：`Zhulong_ERR_NOT_FOUND(-4)` 已有 `ErrNotFound` 独占，
  `errors.go` 明确要求“每个哨兵必须独占一个状态码”，同码两哨兵会让 `errors.Is` 互相为真。
- `ZHULONG_ABI_VERSION` 升到 **6**（新增视图接口）；算法包 SDK 的 `sdk_version`、`IPC_VERSION`、
  `zhulong_frame_view` 不变。

### 4.4 Go 侧接线

- `camera` 域新增只读接口（由 `internal/app` 适配到 `task.Store.GetTaskByCamera`）：

```go
type TaskCropProvider interface {
    // 返回 nil, nil 表示“当前无 crop/任务不存在/任务停用”；error 表示读库或解析失败。
    CropFor(ctx context.Context, cameraID string) (*region.Rect, error)
}
func (h *StreamHub) SetTaskCropProvider(p TaskCropProvider)
func (h *StreamHub) RefreshTaskCrop(ctx context.Context, cameraID string) // 幂等重放
```

- `camera.MediaStream` 接口增：

```go
SetView(ctx context.Context, crop *region.Rect) (region.ViewState, error)
GetView(ctx context.Context) (region.ViewState, error)
```

- `internal/engine/stream.go` 实现两个方法：`crop == nil` 传 `NULL`；`Zhulong_view_state` 映射为
  `region.ViewState`（`Since = time.UnixMilli(EffectiveSinceMs)`，0 为零值）；`runtime.KeepAlive` 覆盖调用。
  新增 `ErrStreamNotReady` 哨兵；`NOT_FOUND` 复用既有 `ErrNotFound`（**不要**定义同码的
  `ErrStreamNotFound`，见 §4.3）。Hub 一律用 `errors.Is`，不判字符串。
  CGO 调用走 `borrowHandle{requireGeneration: &gen}`（照 `setPublishRate` 的既有模式），
  保证引擎 stop/start 换代后旧句柄不会作用到新的 stream id。
- `aiStreamRef` 增：

```go
type aiStreamRef struct {
    stream MediaStream
    aiSub  *engine.AIResultSubscription
    cancel context.CancelFunc

    // applyMu 串行化「读 provider + SetView」这一个复合动作。
    // 不加它，RefreshTaskCrop（读到 v2）与 existing-ref 快路径 ensureView（读到 v1）
    // 可能倒序落库，视图回退到旧配置。串行后「最后写入」必然对应「最后读取」。
    applyMu sync.Mutex

    viewMu     sync.Mutex
    view       region.ViewState // Rect + Since
    viewPrev   region.Rect
    viewPending bool
}
```

  初始化 `view = {Rect: region.Identity()}`、`viewPrev = region.Identity()`。

- **acquire 顺序（硬约束，修首帧错映射）**：

```text
awaitStreamReady
→ provider.CropFor
→ mediaStream.SetView(crop)（成功 state 用于初始化 ref；NOT_READY → pending）
→ 创建 aiStreamRef 并插入 h.aiStreams（先注册，后启动消费者）
→ SubscribeAIResults
→ 启动 consumeAIResults(ctx, cameraID, ref, sub)
→ 返回 broker path
```

  `acquireAIStreamBrokerPath` 的 **existing-ref 快路径**也必须在返回 path 前 `ensureView`（读 provider +
  `SetView`），不能只依赖“新建流时已经设过”。`ensureView` 与 `RefreshTaskCrop` 共用 `ref.applyMu`，
  在锁内完成“读库 + `SetView`”两步，**仍在 Hub 锁外**。
- `NOT_READY` 在这里是**正常路径**（`awaitStreamReady` 只保证元数据就绪，首个解码帧未必已出）：
  置 `viewPending` 即可，首个 `view_snapshot` 会按真实帧尺寸生效。
- `SetView` 失败处理：`NOT_READY` → 置 `viewPending`；`NOT_FOUND` → 视为流已失效，acquire 失败/走重试；
  其他错误 → 保留当前（新流为 identity）并记 warning，**绝不伪造 `E`**。`get_view` 是唯一真相；
  即使 `SetView` 失败，结果入口仍按实际 `get_view` 逆映射，最坏退化为全幅而不是错框。
- **`SetView` 成功返回的 state 必须立刻写入 ref**（新 ref 用它初始化 `view`；existing ref 走 `updateView`
  保留 `viewPrev`），不能等第一次 `GetView`。否则 `SetView` 已生效但 `GetView` 失败时，ref 仍是 identity，
  会把窗口局部坐标当全幅，造成错框。

- `viewFor(timestamp)`：
  - `view.Since.IsZero()` → 当前 `Rect`（通常是 identity）；
  - `timestamp.Before(view.Since)` → `viewPrev`；
  - 否则 → `view.Rect`。
- `updateView(state)`：
  - `state.Since != view.Since`（或 `Rect` 变化）时：`viewPrev = view.Rect`，`view = state`，清 `viewPending`；
  - `state.Since == 0` 且当前 `view.Since != 0` 时不要倒退（引擎异常/旧库），保留当前并记录 warning。
- **每批结果调用一次 `GetView`**（`sub.Next` 当前返回单条结果，因此就是每条结果一次；量级可接受）：
  - 成功：`updateView`；失败（`ErrClosed`/`NOT_FOUND`）：保留当前视图，不更新。
- `RefreshTaskCrop(ctx, cameraID)`：
  1. 锁内取 `ref` 与 provider；锁外**在 `ref.applyMu` 内**读 DB + 调 `SetView`（仍不得持 Hub 锁）；
  2. 无 ref 直接返回（acquire 时会重放）；
  3. provider 返回 `(nil, nil)` → `SetView(nil)` 清空；
  4. provider error → 记 warning，**保留当前视图**（不要误清）；
  5. `NOT_READY` → `viewPending = true`；`NOT_FOUND` → 正常（流未建立），不记 warning；
  6. 成功 → `updateView`。
- 释放 / 关闭：
  - `ReleaseAIStreamSubscriber`：**锁内只摘 ref 并把 `stream` 交给局部变量**，锁外先
    `SetView(nil)`（短超时/后台 ctx）再 `Close`；这样 preview 仍持有 Stream 时不会留下旧窗口，
    也不会把 CGO 带进 Hub 锁。
  - `handleAIStreamFailure` / `Close` 同样清本地状态；引擎流销毁后视图自然消失。
- **不要在 Hub 锁内做 CGO**；`RefreshTaskCrop` 与 `consumeAIResults` 都遵守“锁内取 ref、锁外调用”的既有模式。
- **旧订阅隔离**：`consumeAIResults` 接收自己的 `*aiStreamRef`，不要按 `cameraID` 再查 `h.aiStreams`；
  重连/重建后的旧结果不得套用新 ref 的 `E`。

### 4.5 变更通知与热更新

- `task.Service` 已有一个 `TaskChangeListener`（重启算法包）；**新增**一个按相机语义的通知：

```go
type TaskSavedListener interface { OnTaskSaved(ctx context.Context, taskID, cameraID string) }
```

- 在 Create / Update / Delete 保存成功后通知；`internal/app` 把它接到 `streamHub.RefreshTaskCrop`。
- **通知不得用请求 ctx**：写库已经提交，客户端断开导致 ctx 取消时 `CropFor` 读库失败只会让新 crop
  静默不生效到下一次 acquire。用 `context.WithoutCancel(ctx)` + 短超时包装后再调 `RefreshTaskCrop`。
- **只改 crop 时不重启算法进程**：
  - `UpdateTask` 在 `req.ApplyTo(task)` **之前**深拷贝旧任务（含 `Algorithms`），否则 `ApplyTo` 原地修改后
    已经没有旧值可比较。
  - 比较“除 crop 外的任务语义”：`CameraID/Enabled/Name` + 算法集合（按 `AlgorithmID` 规范化排序）的
    `AlgorithmID/Enabled/SampleFPS/ConfigJSON/RegionConfigJSON/ScheduleJSON`；忽略 `CropJSON`、
    `UpdatedAt`、子项 `ID/CreatedAt/UpdatedAt`。
  - 仅 crop 变 → 跳过 `notifyTasksChanged`，只发 `OnTaskSaved`；其他任何字段变 → 两者都发（保持现有行为）。
  - `SaveTask`（按相机 upsert）同样用已读出的 `existing` 做比较（不需要额外再读一次）。
- 删除任务：保存成功后发 `OnTaskSaved`；即使 AI 订阅释放因 preview 共享而没有销毁 Stream，
  `RefreshTaskCrop` 也会用 `(nil,nil)` 显式清空视图。
- 流尚未建立时 `SetView` 返回 `NOT_FOUND`，这是**正常情况**（acquire 时会重放），不记 warning。
- 因此：**改 crop = 下一帧生效，算法进程不重启、不断流**。

---

## 5. 物化（引擎）

### 5.1 发布顺序

`FramePublisher::publish_loop`（`native/src/pipeline/frame_publisher.cpp`）：

```txt
acquire frame
  → sample_fps 节流（丢帧不产生任何成本）
  → snapshot = stream->view_snapshot(frame.width, frame.height)  // {window, capture_time_ms} 原子
  → motion_detector.detect(frame, snapshot.window, motion)
  → broker->publish_frame(frame, snapshot.window, motion, copy, snapshot.capture_time_ms)
  → push_frame(frame)                               // history 永远全幅（AC6）
```

- 节流在物化之前，成本按 `sample_fps` 计，不按相机帧率。
- `capture_time_ms` 由 `view_snapshot` 生成，不是 broker 发布时；这保证“旧窗口像素 + 新时间戳”
  不会出现（见 §4.2）。
- `has_motion` 语义天然收紧到窗口；`motion_boxes` 下游无人消费，320×180 网格在非 16:9 窗口上的拉伸无影响。
- `MotionDetector::detect(frame, view, out)` 只读 Y 平面的窗口区域；先校验窗口在 `frame.width/height` 内，
  窗口尺寸 `<16`、越界或格式不支持时 fail-open（`has_motion=1`），绝不因为一次读取失败关掉整条流。

### 5.2 Broker 写 slot

`FrameBroker::publish_frame(const HardwareFrame&, const ViewWindow&, const FrameMotionInfo&,
const CropCopyFn&, int64_t capture_time_ms)`：

- **零拷贝分叉修正（R3.1）**：

```cpp
const bool zero_copy = frame.dma_fd >= 0 && !view.active;
```

  绝不能只写 `frame.dma_fd >= 0`，否则窗口尺寸会配全幅 fd 发给客户端。
- `view.active == false` → **今天的行为**：`zero_copy` 直通，否则分平面 memcpy；
  `capture_time_ms` 取快照值（这是与旧实现的唯一差异，字段布局不变）。
- `view.active == true` → 先校验 `0 <= x`、`x+w <= frame.width`、`0 <= y`、`y+h <= frame.height`；
  不满足直接 `fail_publish`，**绝不越界读**。几何已按帧尺寸计算，这里只是防御
  （覆盖元数据/帧尺寸的瞬时不一致）。
  然后一律写 slot：
  - `dma_slots_` 可用则直接写 dma slot（`dma_fd_to_send = fd`，`flags |= FRAME_DISPATCH_FLAG_DMA_FD`）；
  - 否则写 shm slot（`dma_fd_to_send = -1`）；
  - 调用 `copy(..., dst_dma_fd = use_dma_slot ? fd : -1)`；目标缓存同步由 `WindowCrop` 据此决定。
  - 紧打包布局，`MSG_FRAME_DISPATCH` 报窗口尺寸。
- 容量门禁用 `window_payload_bytes(view, frame.format)`，**不是**全幅 `frame_payload_bytes(frame)`。
  ⚠️ `slot_size` **不是**“按全幅分配”：`Engine::ensure_frame_broker` 用默认 `BrokerOptions`
  （8 × 4 MiB，见 `native/src/pipeline/engine.cpp`），4K 全幅本来就发不出去。窗口能让原本超限的帧
  重新可发，但**仍要单独校验** `window_payload_bytes <= slot_size`，不能假设“窗口一定装得下”。
- `CropCopyFn` 返回 false → `fail_publish`：归还在途配额、槽位置 `FREE`、`total_dropped_++`，
  **绝不发送半截帧**。未知格式/无 copy 函数一律 fail_publish，不按 BGR24 猜。

紧打包布局（与 `frame_payload_bytes` / `resolve_source` 的校验一一对应）：

| 格式 | `dst_stride` | `offsets` | `strides` |
| :--- | :--- | :--- | :--- |
| BGR24/RGB24 | `align_up(w*3, 192)` | `[0]=0` | `[0]=dst_stride` |
| NV12 | `align_up(w, 64)` | `[0]=0`、`[1]=dst_stride*h` | `[0]=[1]=dst_stride` |
| YUV420P | `align_up(w, 64)` | `[0]=0`、`[1]=dst_stride*h`、`[2]=dst_stride*h+(dst_stride/2)*(h/2)` | `[0]=dst_stride`、`[1]=[2]=dst_stride/2` |

- 64 字节对齐覆盖当前已知的 `stride_align` 探测值（RK3568 实测 4、RGA3 常见 16/64）；
  若未来平台探测到 >64，必须用 `align_up(w, max(64, stride_align))`，不能假设 64 永远够。
- packed 用 192（`3 × 64`）同时满足 `%3==0` 与 `%64==0`。
- 紧打包后客户端侧 `resolve_source` 的三个校验全部天然成立：
  `y_stride % plane0_bpp == 0`、`y_stride % stride_align == 0`、
  `deduce_continuous_hstride` 推出的 `hstride == h`（`native/src/backends/rga/rga_vision_backend.cpp:243-305`）。
- `msg.capture_time_ms = capture_time_ms`（<=0 时回退 now，仅作为防御）；`msg.width/height` 用窗口值。

### 5.3 窗口拷贝（宿主侧，按目录规则分层）

```cpp
// native/src/common/view_window.hpp —— header-only；宿主与 AlgoIpc 共享类型，不新增 .o
typedef struct { bool active; int x, y, w, h; } ViewWindow;
using CropCopyFn = std::function<bool(const HardwareFrame& frame, const ViewWindow& view,
                                      uint8_t* dst, size_t dst_size, int dst_stride,
                                      int dst_dma_fd)>;
```

| 文件 | 归属目标 | 职责 |
| :--- | :--- | :--- |
| `src/common/view_window.hpp` | 仅头文件 | `ViewWindow` + `CropCopyFn` 契约 + 纯函数窗口计算 |
| `src/nodes/preprocess/window_crop.{hpp,cpp}` | `ZhulongEngine` | CPU 分平面行拷贝 + 后端选择 + 计数器 |
| `src/backends/rga/rga_host_loader.{hpp,cpp}` | `ZhulongEngine` | **可选第二刀**（第一刀未落地）：唯一宿主侧包含 vendored RGA 头的 TU；dlopen `librga.so.2` + `imcheck_t`/`improcess` |

> **第一刀落地口径（2026-11，与实现一致）**：`WindowCrop` 只实现 CPU 分平面行拷贝
> （`window_rga_count` 恒为 0）；`rga_host_loader`、RGA 缓存一致性 spike 与并发验证全部归入
> **可选第二刀**。1080p 半宽半高约 0.8 MB/帧、20fps ≈ 15.6 MB/s，CPU 行拷贝在当前目标上足够；
> 先把正确性（窗口/边界时钟/逆映射）跑通，再按板端 profiling 决定是否需要 RGA。
> `view_window.hpp` / `CropCopyFn` / `ScopedFdSync` / 紧打包布局这些**接口**已经按双后端设计，
> 第二刀只需补一个后端实现，不改契约。

- **不能用裸函数指针**：`WindowCrop` 的 RGA loader、后端状态、`window_rga_count/window_cpu_count`
  需要上下文。用 `std::function`（或加 `void* ctx`），并明确 `noexcept`/失败返回 false。
- `dst_dma_fd` **必须**传：`-1` 表示 shm，`>=0` 表示 dma slot；`WindowCrop` 只有拿到 fd 才能对
  CPU 写入做写同步。只看裸指针无法区分 shm/dma slot；`FrameBroker` 选槽时决定该值，不能靠猜。
  ⚠️ 现成的 `ScopedCpuSync` 构造是 `ScopedCpuSync(DmaBuffer&, flags)`（拥有型缓冲），**不接受裸 fd**；
  `CropCopyFn` 手里只有 fd。因此在 `native/src/platform/dmabuf/dmabuf_sync.hpp` 加一个 header-only 的
  裸 fd RAII（如 `ScopedFdSync`：`START(flags)` / `END(flags)`，失败不抛），`motion_detector` 与
  `window_crop` 共用它，不再各自手写 ioctl。
- **不能放进 `ZhulongAlgoIpcObjects`**：那批对象也归档进算法包 SDK（`libzhulong_algo_sdk.a`），
  宿主侧的窗口拷贝不得进入包归档。`FrameBroker` 只接收 `CropCopyFn`：
  `Engine` 构造 `WindowCrop` → `FramePublisher` 持有 → 传给 broker。
- **CMake**：`ZhulongEngine` 需要 `target_include_directories(... PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/vendor/rga/include)`；
  **不得**链接 `librga.so`，只 `dlopen`/`dlsym`，否则宿主产物会多出 NEEDED。
- `rga`：`srect{view.x,view.y,w,h}` → `drect{0,0,w,h}`，预检失败/库缺失/`/dev/rga` 不可访问 → 该帧走 `cpu`，
  **几何不变**。与 `mpp_loader.cpp` 同一 dlopen 模式，宿主仍是单产物。
  - `librga.so.2` 与 vendored header 的 ABI/版本必须板端 spike；`imcheckHeader` 失败即 CPU。
  - 多个 `FramePublisher` 并发 `improcess` 的线程安全/硬件串行要验证；必要时串行化窗口拷贝。
  - RGA 写 shm 与写 dma slot 的缓存一致性分别验证；客户端读 dma fd 已有 `DMA_BUF_IOCTL_SYNC`，
    shm 没有，必须显式处理或退回 CPU。
- `cpu`：分平面逐行 memcpy。
  - **NV12/YUV420P 的 UV 起点用 `HardwareFrame::y_plane_size()`（含 `vstride`）**，不是 `stride * height`；
  - 源行跨度用 `plane_strides[i]`，为 0 才回退 `stride`；
  - dmabuf 源需要 `mmap` + `DMA_BUF_IOCTL_SYNC(START/END)`（用上面的 `ScopedFdSync`）；
  - 目标为 dma slot 且 CPU 写入时，必须 `ScopedFdSync(dst_dma_fd, platform::kDmaSyncWrite)`；
  - 目标为 shm 且 RGA 写入时，按 spike 结论决定 flush 或 CPU。
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
1080p NV12 半宽半高窗口约 **0.78 MB/帧**；若按面积裁 50% 则约 **1.5 MB/帧**。本方案成本表按半宽半高口径。

---

## 6. 逆映射（宿主 Go）

### 6.1 收口点

`internal/camera/stream_hub.go` 的 `consumeAIResults`：`ParseInferenceResult` 之后、`region.Filter` 之前。

```go
state, err := ref.stream.GetView(ctx) // 每条结果刷新一次；失败保留当前
if err == nil {
    ref.updateView(state)
}
view := ref.viewFor(res.Timestamp)    // 按 capture_time 选上一代/当前
res = region.Reproject(res, view)
// 再 region.Filter
```

### 6.2 字段清单（`internal/aitypes`，按解析后的实际形状）

| 结果类型 | 字段 |
| :--- | :--- |
| detection | `Detections[].Box` |
| person_structure | 解析器归一化为 `scene_structure`：`Persons[].BodyBox`、顶层 `Faces[].Box/.Landmarks` |
| scene_structure | `Persons[].BodyBox`、顶层 `Faces[].Box/.Landmarks` |
| vehicle_structure | `Vehicles[].VehicleBox`、`Vehicles[].Plates[].Box` |
| default/unknown task_type | 以上全部，另加兼容路径 `Persons[].Faces[].Box/.Landmarks` |
| classification / beacon | 无坐标，原样透传 |

- 逐轴线性：`g = E.origin + c * E.span`，再 clamp 到 `[0,1]`。
- **非有限值策略**：box 任一分量 NaN/Inf → 丢弃该 detection/person/vehicle/plate/face；
  landmark 任一分量非有限 → 丢弃该 landmark；绝不把 NaN clamp 成 0（会静默造出左上角目标）。
- identity 视图（`{0,0,1,1}`）**逐字段原样返回**，不要乘 1，避免浮点噪声。

### 6.3 换窗边界（R3.1 的核心修正）

- 每次 `set_view`/`get_view` 拿到 `ViewState{Rect, Since}`：
  - `Since` 与当前不同 → `viewPrev = 当前 Rect`，`view = 新 state`；
  - `Since.IsZero()` 且当前非零 → 保留当前，记 warning（引擎不应倒退）。
- `viewFor(ts)`：`ts.Before(Since)` → `viewPrev`，否则 `view.Rect`。
- `Since` 来自引擎的视图快照时钟；Host **不再**用 `time.Now()` 作为改窗时间。
- 只保留一代旧窗口即可覆盖正常在途帧（每客户端 ≤2 帧）。**已知限制**：如果两个窗口变更之间
  完全没有 `GetView`/结果批次，Host 只能看到最后一代；任务保存/分辨率重协商的正常频率下不会发生。
  若未来需要强保证，再让 `get_view` 返回最近 N 代或引入 result epoch；本期不扩大。
- **旧包兼容**：算法骨架早已把 dispatch 的 `capture_time_ms` 原样回传（`native/sdk/src/algo_main.cpp:1265`）；
  本方案只改该字段的生成点，不改布局，因此旧包不需要重建。

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
| 连续多次窗口变化 | Host 只保留上一代；两个变更之间没有结果批次时，中间一代可能不可见（§6.3） |
| RGA 不可用时的 CPU 拷贝 | 1080p 半宽半高约 0.78 MB/帧；5–20 fps 下可接受，计数器可观测 |
| 运动门禁只回答窗口内 | 语义正确；不做窗口内分区 |
| 同版本发布 | 新前端 + 旧宿主没有能力探测；旧宿主会忽略 `crop`，新前端可能显示编辑入口但保存无效。发布说明要求宿主+前端同版本 |
| 配置晚一帧生效 | 几何按解码帧尺寸算，`set_view` 之后到首帧之间仍是全幅；`get_view` 如实回报 `NOT_READY`，不会假装已生效 |

---

## 8. 兼容 / 迁移 / 回滚

| 场景 | 行为 |
| :--- | :--- |
| 升级前的老任务 | `crop_json='{}'` → 视图未激活；字段布局/内容路径不变（`capture_time` 取值点前移，毫秒值允许差异） |
| 新宿主 + 老算法包 | **直接生效**（包看到小图，坐标由宿主映射，`capture_time` 原样回传），无需重建 |
| 老宿主 + 新前端 | 老宿主忽略 `crop` 字段/列 → 全幅；新前端没有能力探测，**不承诺**自动隐藏入口（要求同版本发布） |
| 回滚 | `down` 迁移删列；引擎 `set_view(NULL)`；AI 订阅释放时显式清空，无残留状态 |
| 共享 Stream | AI 全撤但 preview 仍在 → `Stream` 不销毁；最后一次 AI 订阅释放时显式清空，下一次 acquire 重放 |
| 算法包回滚 | 与本功能无关（`sdk_version` / `IPC_VERSION` / `zhulong_frame_view` 均未变） |

---

## 9. 验证

### 9.1 开工前的 spike（板端半天起）

1. `improcess` 同格式窗口拷贝 → shm/VA 目标是否被驱动接受；RGA 写 **shm** 与写 **dma slot** 的
   缓存一致性分别验证（读回校验）。
2. 紧打包窗口（`dst_stride=align_up(w,64)`、`offsets[1]=dst_stride*h`）通过真实 face 包
   `resolve_source` 全部校验。
3. 非 16:9 窗口 + 目标 `sample_fps` 下的每帧成本与 CPU 占用。
4. **边界时钟**：用旧包（不改）跑一次 `set_view`/分辨率重协商，确认 dispatch 的 `capture_time_ms`
   与 `effective_since_ms` 能严格区分旧/新窗口；覆盖同一毫秒内改窗。
5. **并发**：两路 `FramePublisher` 同时走 RGA 的稳定性；`imcheck` 通过但 `improcess` 失败时
   CPU 回退是否安全。
6. **尺寸来源一致性**：同一路 RTSP 下比对 `codecpar` 的 `width/height` 与解码帧
   `HardwareFrame::width/height`（FFmpeg 软解 / MPP 硬解各一次），覆盖 1088 裁切与分辨率热切换。
   任何不一致都必须由“按帧尺寸算几何”吸收，不能靠元数据。

任一条不通过：退回 CPU-only 物化（架构不变，只影响性能）；边界时钟或尺寸来源不通过则必须先修，
不进入阶段 1。

### 9.2 测试矩阵

- **窗口纯函数**：1080p/720p、奇数宽高（1919×1079）、贴四边、`x2=1.0`、`crop==整帧`、极小窗口。
- **物化定位**：合成图案（四象限 + 非对称角标）断言 slot 内容 == 期望窗口；RGB24/BGR24/NV12/YUV420P；
  含 `vstride > height`、独立 `plane_strides`。
- **无视图回归**：`view.active=false` 时输出与原路径字段/布局/内容一致（`capture_time` 允许取值点差异）。
- **零拷贝分叉**：`frame.dma_fd >= 0 + view.active` 必须走 slot，绝不能发全幅 fd。
- **降级**：CPU 与 RGA 输出几何/内容一致；RGA 不可用时 CPU。
- **几何来源**：元数据 1920×1088 / 解码帧 1920×1080 时配置贴底窗口 → 不丢帧，且 `E` 以 1080 为分母。
- **ABI 阈值**：恰好 `MinCropSide` 的矩形经 float32 往返后仍被接受（C 侧不做阈值判定）。
- **固定 slot_size**：窗口 payload > `BrokerOptions::slot_size` → `fail_publish`，不越界写。
- **分发契约**：窗口帧的 `width/height/strides/offsets` 满足 `resolve_source` 的三个校验。
- **Go 逆映射**：4 类结果逐字段、顶层 Faces、嵌套 Faces 兼容、clamp、NaN/Inf、identity 快路径、
  `viewPrev` 边界（`capture_time < since` / `== since` / `> since`）。
- **Hub 首帧/重放**：SetView 在 Subscribe 前生效；existing-ref 快路径重放；`NOT_READY` → 结果批次收敛；
  共享 preview 的 Stream 在 AI 全撤后清空、下次 acquire 重放；`provider error` 不移除当前视图。
- **抓拍**：全幅 JPEG 尺寸/框位置。
- **前端**：真实无头浏览器，绘制/替换/清除/切算法不丢/画面未就绪不提交 crop。
- **迁移**：up/down 对称；老任务默认 `{}`；编号唯一且单调（**不断言连续**，现存 `000026` 空洞）。

### 9.3 风险

| 风险 | 缓解 |
| :--- | :--- |
| RGA 写 shm/dma slot 的缓存一致性 | spike 1；不行则 CPU 行拷贝 |
| 宿主新增 dlopen 依赖 | 与 MPP 同模式；探测失败即 CPU，功能不降级 |
| 动了 publish 路径 | 改动限定在“写 slot 的那一次拷贝”与“取视图快照”；history/preview 顺序不变 |
| 分辨率重协商 | 引擎重算窗口并推进 `effective_since_ms`；Go 每条结果 `GetView` 收敛；e2e 覆盖 |
| 共享 Stream 的视图残留 | 最后一次 AI 订阅释放时显式 `SetView(NULL)`；下次 acquire 重放 |
| 连续两次窗口变化 | 只保留一代；文档化限制；正常频率下不触发 |
| 迁移号漂移/乱序 | 落地时重新核对实际 max；alert 任务重编号到当时 max+1；迁移检查只断言唯一/单调，不硬编码编号 |
| 元数据尺寸 ≠ 解码帧尺寸 | 几何按帧尺寸算（spike 6）；`E` 的分母永远等于实际切图尺寸，不会因元数据偏差错映射或永久丢帧 |
| C ABI 上比较最小边长 | C 侧只做结构性校验；`MinCropSide` 权威在 Go（float32 边界会误拒） |
| 前端旧宿主兼容 | 发布说明要求同版本；不做静默能力探测 |

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
| Go 本地 `time.Now()` 作为改窗边界 | 改窗调用、engine 生效、结果回传三点不重合；旧窗口像素会被新 E 映射 |
| 结果 JSON 追加 `view_epoch` | 旧包不会回传，AC9 直接失效；本方案用旧包已有的 `capture_time_ms` + 引擎 `since` |
