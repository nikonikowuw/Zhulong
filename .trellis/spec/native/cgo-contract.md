# CGO 跨语言交互与内存安全契约

> Go 与 C++ 原生引擎之间的 CGO 边界、不透明句柄、指针规则与内存生命周期。

---

## 1. 核心设计原则

1. **热路径批处理 (Batching)**：跨栈切换有固定开销，**严禁逐像素、逐框调用 CGO**，必须以整帧、批处理或流控制粒度调用。
2. **纯 C ABI 隔离**：C++ 必须通过 `extern "C"` 导出 C 兼容符号，禁止向 Go 暴露 C++ 类或模板。
3. **内存归属谁分配谁释放**：
   - Go 侧内存由 GC 管理；C 侧内存（`malloc`/`new`）由 C 显式释放。
   - `C.CString` **必须紧跟 `defer C.free(unsafe.Pointer(cStr))`**。

---

## 2. 严格的 Go-C 指针规则 (cgocheck 强制)

1. **禁止传含 Go 指针的内存**：传给 C 的连续内存中，**严禁包含任何指向 Go 堆对象的指针**。
2. **禁止 C 侧跨调用保留 Go 指针**：C 函数返回后**严禁长期缓存 Go 内存指针**；如需异步必须在 C 侧深拷贝，或由 Go 使用 `runtime.Pinner`。
3. **防止提前 GC**：将 Go 切片传给 C 同步调用时，调用后紧跟 `runtime.KeepAlive(slice)`。

---

## 3. 不透明句柄模式与当前桥接契约

### C 侧公开头文件 (`native/include/Zhulong/engine.h`)

```c
#ifndef ZHULONG_ENGINE_H
#define ZHULONG_ENGINE_H
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct Zhulong_engine_t *Zhulong_engine_h;
typedef int32_t Zhulong_status_t;

enum {
    Zhulong_OK = 0,
    Zhulong_ERR_INVALID_ARGUMENT = -1,
    Zhulong_ERR_OUT_OF_MEMORY = -2,
    Zhulong_ERR_INTERNAL = -99
};

Zhulong_status_t Zhulong_engine_create(Zhulong_engine_h *out_engine);
Zhulong_status_t Zhulong_engine_start(Zhulong_engine_h engine);
Zhulong_status_t Zhulong_engine_stop(Zhulong_engine_h engine);
void Zhulong_engine_destroy(Zhulong_engine_h engine);

#ifdef __cplusplus
}
#endif
#endif
```

所有 ABI 函数捕获 C++ 异常；`start` / `stop` 返回状态码，`destroy` 无返回值且空句柄可安全销毁。公开头文件保持纯 C，不暴露 C++ 类型。以上仅示范保留的生命周期子集；新增 probe、stream、subscription 的完整签名以 `native/include/Zhulong/engine.h` 为准，执行合同见 [ingestion-contract.md](./ingestion-contract.md)。

### Go 侧桥接 (`internal/engine/`)

```go
/*
#cgo CFLAGS: -I${SRCDIR}/../../native/include
// 完整静态库与系统依赖由 native/scripts/build.py 的 CGO_LDFLAGS 提供。
#include <stdint.h>
#include <Zhulong/engine.h>

void zhulongPacketCallbackBridge(uintptr_t token, const Zhulong_packet_view *packet);
*/
import "C"

type Engine struct { ... }
func New() *Engine
func (e *Engine) Start() error
func (e *Engine) Stop() error
func (e *Engine) Ready() bool
func (e *Engine) Close() error

func (e *Engine) Probe(ctx context.Context, uri string, options StreamOptions) (VideoInfo, error)
func (e *Engine) Acquire(ctx context.Context, uri string, consumer Consumer, options StreamOptions) (*Stream, error)
func (s *Stream) Status(ctx context.Context) (StreamStatus, error)
func (s *Stream) Subscribe(ctx context.Context, options SubscriptionOptions) (*Subscription, error)
func (s *Stream) Close() error
func (s *Subscription) Next(ctx context.Context) (Packet, error)
func (s *Subscription) Done() <-chan struct{}
func (s *Subscription) Err() error
func (s *Subscription) Close() error
```

> 桥接层按职责分文件：`engine.go`（Engine 生命周期与订阅注册）、`handle_lease.go`（句柄租约与前置条件）、
> `probe.go`（私有探测引擎）、`broker.go`（FrameBroker 查询与发布节流）、`stream.go` / `subscription.go` /
> `ai_subscription.go` / `queue_core.go`。新增跨 CGO 调用放进职责相符的文件，不要再堆回 `engine.go`。

> 人脸特征提取**不在**本包：它从「engine 里 dlopen 人脸插件」改成了「与跟包可执行文件说一个小协议」，实现在 `internal/faceextract`，宿主不认识算法二进制。历史文档列过 `Engine.ExtractFaceFeature`，该符号已不存在。

所有导出方法要求传入**非 nil** 的 `context.Context`（与标准库惯例一致）；本包不提供 nil 语义，内部辅助函数也不接受 nil（如 `normalizeTimeoutMs` / `clampToDeadline` 已拆开，不再用一个布尔量表示「是否收敛」）。

`Start` 在持锁下创建 C-owned handle 并启动；启动失败时销毁 handle。`Stop` 不销毁句柄；幂等 `Close` 停止并销毁。Go 只保存不透明句柄值，不向 C 侧暴露可逃逸的 Go 指针。C++ 静态库由 `make native-build` 构建到 `build/native/host/libZhulongEngine.a`。Go 命令必须通过 `native/scripts/build.py go ...` 或 Make：脚本传递 Engine 内容摘要路径、FFmpeg 三个静态归档及目标系统库，避免 Go 外部归档缓存过期。交叉产物使用独立目录，不覆盖 host。

句柄生命周期防护与并发控制：
- **句柄租约统一走 `borrowHandle`**：所有跨 CGO 访问底层 `handle` 的调用都经 `Engine.borrowHandle(handleRequirement)` 取得 `handleLease` 与 `release` 函数，在 `handleMu` 读锁保护下执行（`release` 前所有 CGO 调用必然排在 `Close()` 取写锁之前、`Zhulong_engine_destroy` 之前），消除野指针与 Use-After-Free 隐患。**不要**再手写「RLock → mu → 守卫 → 取 handle」四行模板。
  - `handleRequirement` 的三个条件（`requireRunning` / `requireGeneration` / `deniedError`）必须显式传，helper 不做缺省推断。
  - ⚠️ **停机排空路径（`releaseStream` / `unsubscribeWith`）故意不要求运行态**：`stopLocked` 先把 `running` 置 false，再逐个 `s.Close()`；给它们加上运行态检查会让关流全部退化成 `ErrNotRunning`，token 便堆到下次排空。
- **锁获取顺序严格单向**：`lifecycleMu → handleMu → mu → {probesMu, streamsMu}`，注释写在 `Engine` 类型上。`mu` 只保护状态字段，**绝不在其下做 CGO 调用**；`handle` 同时受 `handleMu`（指针不悬空）与 `mu`（状态自洽）保护。
- **私有探测引擎的 stop/destroy 互斥**：`Probe` 使用隔离的私有引擎实例。`stopLocked` 把 `activeProbes` 快照出锁后逐个 `stop`，而探测 goroutine 自己也会 `stop` 并在返回时 `destroy` —— 两者必须经 `probeContext.mu` 串行，否则「快照晚用一步」就是对已释放句柄调用 `Zhulong_engine_stop`。`destroy` 必须吸收 `stop` 语义（先 stop 再 destroy 并标记 `destroyed`），保证 destroy 之后不会再有 stop。
- **停机注销延迟排空（两分支）**：注销返回后 `cgo.Handle` 的释放时机由 `planTokenRelease` 决定 —— 仅 `Zhulong_OK` / `Zhulong_ERR_NOT_FOUND`（已同步排空或本就无此订阅）可立即 `Delete`；**其余全部状态**（停机中的 `ERR_NOT_RUNNING` 与任何未预期状态，如 `ERR_CALLBACK_CONTEXT`、`ERR_BUSY`）都走 `default` 分支暂存到 `pendingTokens`，等底层工作线程全部 `join()` 汇合完成后统一释放。默认分支必须保守：晚一点释放永远优于提前释放（残余回调会命中已删除句柄）或永不释放（`cgo.Handle` 泄漏）。由于两条路径行为一致，不要再为 `ERR_NOT_RUNNING` 写单独的等同分支。
- **`Stream` 前置检查统一走 `lease`**：`Status` / `BrokerPath` / `SetPublishRate` / `Snapshot` 用 `Stream.lease(ctx)` 一次性完成「ctx 校验 + 关闭态校验 + 在锁内取 `generation`/`id`」，避免四处复制同一段样板，也避免解锁后再读不可变字段这种「靠 happens-before 推理」的写法。
- **ABI 版本取自头文件宏**：`expectedABIVersion` 用 `uint32(C.ZHULONG_ABI_VERSION)` 而非手写常量，避免 Go 常量与 `engine.h` 漂移；该检查校验的是「头文件」与「静态库归档」是否同源，不匹配时 `Start` 直接拒绝。
- **阻塞式 CGO 调用的超时上限**：`maxAllowedTimeout`（2 分钟）同时是优雅停机的最坏阻塞上界 —— `Close` 必须等持读锁的在途调用（如 `Acquire` 的 RTSP open）返回才能取写锁销毁句柄。放宽该上限前先确认停机预算。
- **Go 桥接私有状态码必须独占**：`NativeError.Is` 只比较 `Code`，因此每个哨兵必须一个码（例如 `StatusSnapshotUnavailable` 不能复用 `StatusUnsupported`，否则两类失败会互相 `errors.Is` 为真）。Go 私有码从 `-100` 起，绝不跨越 ABI 边界。
- **回调 panic 必须可观测**：`//export` 回调内 `recover` 之后必须转成订阅终态（`nativeCallbackPanic` → `ErrInternal`）或计入丢弃（日志通道 `RecordDrop`），严禁裸 `_ = recover()` 静默吞掉 —— 否则「偶发丢帧」将既无日志也无错误码。
  - ⚠️ **recover 分支必须先判定接收者非 nil**：`cgo.Handle(token).Value()` 在句柄已删除时**自己会 panic**，此时 `recover()` 拿到的正是这次 panic，而 `sub` 还没被赋值。若直接 `sub.reportCallbackPanic(...)`，nil 接收者上的二次 panic 发生在 deferred 函数里会冲出 ABI 边界变成进程崩溃 —— 比原本要拦的问题更糟。

---

## 4. 调用方缓冲区元数据与按轨订阅（ABI v5）

`Zhulong_stream_audio_info` 返回的是**流级常量**（codec / 采样率 / 声道 / extradata），刻意不进包视图：
每包都带一份采样率只会让热路径白白变大。

- **缓冲区由 C 侧分配**：`extradata` 必须指向 `C.malloc` 得到的内存（容量写进 `extradata_capacity`），
  返回后用 `C.GoBytes` 复制成 Go 副本，再 `C.free`。
  ⚠️ 不要把 `&goBuf[0]` 塞进 `Zhulong_audio_view` 再传 `&view`：cgo 的指针检查禁止
  「Go 指针指向的 Go 内存里还含 Go 指针」，即使目标是无指针的 `[]byte` 也会在运行时
  panic（`argument of cgo function has Go pointer to unpinned Go pointer`，已实测踩到）。
  真要用 Go 缓冲就先 `runtime.Pinner.Pin`，否则一律 C 分配。C 侧只在调用期间写入，**不得**保存该指针。
- **容量不足是正常路径，不是错误**：返回 `Zhulong_ERR_BUFFER_TOO_SMALL`，并把实际所需长度写进
  `extradata_size`；调用方据此扩容重试。不要写“先查一次大小再取一次”的双调用协议 —— 流可能在两次调用间重建。
- **运行态与宽限期**：`state != RUNNING`（启动中/已失败）返回 `Zhulong_ERR_NOT_RUNNING`；
  流被**回收**后返回 `Zhulong_ERR_NOT_FOUND`。已 `release` 但仍在 8 秒宽限期的流**仍可查询**（物理连接还在），
  这是有意为之：`audio_info` 只写调用方缓冲区，不借出内部指针，宽限期内查询不会造成悬垂。
  父 PRD 里「流关闭后返回 NOT_FOUND」的措辞应按此口径修正。
- **不返回内部借用指针**：绝不把 `audio_meta_.extradata` 的地址交出去，否则流销毁后就是悬垂指针；
  也不允许用“反正是立即深拷贝”当借口省掉缓冲区协议。
- **按轨订阅**：`Zhulong_stream_subscribe_track(..., track_mask, ...)`，视频/音频各自一条订阅、各自独立队列与
  独立取消；`Zhulong_stream_subscribe` 等价于 `TRACK_MASK_VIDEO`。同一预览消费者可同时持有视频与音频两条订阅，
  但同一 `track_mask` 重复订阅仍返回 `ERR_DUPLICATE`（去重粒度 = 消费者 + 轨道掩码）。
- **音频回调的包视图契约不变**：`data` 仍只在同步回调期间有效，`pts_us/dts_us` 已是视频输出时间轴上的微秒值；
  再往上一层（Go 的音频通道）必须复制成自持缓冲才能入队。
  - 音频 `pts_us` **可能为负**（首枚音频包比视频原点更早到达），接收方必须接受而不做 clamp；
  - 源流缺 DTS 时 `has_dts=0` 且置 `ZHULONG_PKT_FLAG_DTS_SYNTHETIC`，`dts_us` 已由 `pts_us` 合成；
  - 视频 discontinuity generation 变化或重锚后的首包会置 `ZHULONG_PKT_FLAG_DISCONTINUITY`，
    接收方必须按「时间轴离开上一段」处理（丢弃陈旧音频并重锚），不能只当作告警位。

---

## 5. 线程绑定与异步回调守则

1. **线程绑定 API (`runtime.LockOSThread`)**：若底层涉及对线程局部存储（TLS）敏感的 NPU/GPU 上下文，调用该 CGO 接口的 goroutine 必须在调用前执行 `runtime.LockOSThread()`，退出时 `defer runtime.UnlockOSThread()`。
2. **异步回调使用 `cgo.Handle`**：若 C 侧需要异步上报事件，在 Go 侧通过 `cgo.NewHandle(obj)` 创建安全令牌并传给 C，回调时通过 `//export` 函数传回并由 Go 解析，完成后显式 `handle.Delete()`，严禁在 C 侧缓存 Go 原生指针。

---

## 6. 当前接入实现的验证边界

- `make native-test` 运行 C++17 生命周期、纯 C ABI、真实本地 RTSP/RTP 和构建合同测试；`make go-check` 包含 Go vet/race 与真实 loopback RTSP 桥接门禁（`run_go_bridge_tests.py`）。
- Go Engine wrapper 已完整实现 Probe 探测（独立私有 Engine 隔离取消）、Stream 物理流复用/宽限期管理、有界深拷贝包订阅（Subscription）、同步排空后释放 `cgo.Handle`。
- 包只在同步回调期间借用，入队前深拷贝至 Go 内存；probe-result view 仅在 result destroy 前有效。注销须 drain 在途回调后才允许 Go 侧 `cgo.Handle.Delete()`。
- 两条订阅通道（视频包 / AI 结果）共用 `queueCore[T]`：「入队 → 非阻塞唤醒 → 幂等终态」只实现一次，准入策略各异（包订阅超限即 `ErrBackpressure` 终止；AI 结果采用丢最旧且计数）。`Next` 仅支持单消费者 —— `notify` 容量为 1，扇出必须由业务层完成。
- `TestProbeContextStopDestroyAreSerialized` 用空句柄把「私有引擎 stop/destroy 必须串行」钉成可被 `-race` 抓到的回归测试；`TestPlanTokenRelease` 钉死 token 释放时机（含未知状态走保守分支）；`TestSentinelCodesAreUnique` 钉死「一个哨兵一个状态码」。
- stop 取消输入和 probe、join worker/reaper，之后才释放资源。回调内控制 API 返回 CALLBACK_CONTEXT，destroy 由外部所有者串行调用。
- Linux host 与 Native sanitizer 检查不证明真实摄像机、解码、板端或旧系统兼容；具体命令/断言/残余限制见 [接入合同](./ingestion-contract.md)。

---

## 7. 单次人脸特征提取（离线录入通道）

底库登记需要「一张照片 → 一个 512 维特征」，这是宿主唯一需要算法能力却不走视频流的场景。

- **不再经过 CGO，也不再装载任何算法产物**：实现是 `internal/faceextract`，它跟**包可执行文件**说一个小协议（`<entry> --extract-feature`，stdin 帧流 → stdout 逐行 JSON）。CGO 边界只剩引擎自身的流媒体/抓拍 API。
- **解码在 Go 侧**：包骨架不链接任何图像编解码器，所以 JPEG/PNG/WebP 解码 + EXIF 方向归一化留在 `internal/faceextract/decode.go`，跨边界只有裸 RGB24 像素。
- **业务解耦**：`internal/person` 仍只认 `FaceFeatureExtractor` 接口（`ExtractFaceFeature(ctx, imageBytes)`），由 Uber Fx 注入 `internal/app` 的适配器；领域包不 import 进程/管道/解码细节。
- **提供者包可配**：`[person].extractor_algorithm`（默认 `face_recognition`）。包入口在**每次请求**时按当前安装版本解析，升级/回滚/重装立即生效。

### 7.1 协议与进程形态

| 项 | 约定 |
| :--- | :--- |
| 命令 | `<pkg>/<manifest.id> --extract-feature`（不接 `--broker`） |
| stdin | 28 字节帧头（`ZLFE` magic、`request_id`、宽高、stride、format、`payload_len`）+ 裸像元，可重复至 EOF |
| stdout | 每帧一行 JSON：成功 `{"request_id":…,"status":"passed","feature_dim":512,"features":[…]}`；失败带 `code` |
| 日志 | 一律 stderr（stdout 只能是协议行） |
| 退出码 | `0` = stdin 正常 EOF；`5` = 能力缺失 / 帧头帧体截断 |
| 帧级错误 | 无脸、输入非法**不退出进程**——一张坏图不该毁掉热进程 |

- ⚠️ **stdout 的行协议必顶住厂商库的噪声**：实测 librga 在 RGA 后端初始化时往 stdout 打
  `rga_api version 1.10.1_[10]`，宿主读到非 JSON 行即判 `errProtocolDesync`、杀死热进程，
  表现为「登记永远 `FEATURE_EXTRACT_FAILED`」（而 `--verify` 看起来正常）。因此骨架在
  `ZHULONG_ALGO_MAIN` 生成的 `run()` 最开头调用 `install_protocol_channel()`：dup 一份 fd
  专供协议，再把 fd1 改用 stderr（噪声归日志）。新增厂商后端时不得假设它守规矩——
  这是每一条新增硬件库都会再碰一次的坑。

- 能力用**弱符号**提供：骨架 `extern "C" __attribute__((weak)) zhulong_face_extract_feature(...)`，face 包定义、静态链入即解析；plate 包不定义 → 如实回 `UNSUPPORTED`。
- ⚠️ 弱引用**不会**让 ld 抽取静态归档成员：定义必须来自包内直接编译的源文件，搬进 `.a` 会让能力静默消失（表现为「底库登记功能没了且没有错误日志」）。

### 7.2 Fail-Closed 闭环报错（严禁静默退化）

| 场景 | 包进程输出 | Go 侧错误 | HTTP |
| :--- | :--- | :--- | :--- |
| 成功 | `status=passed`，`feature_dim=512` | `nil` | 200 |
| 无脸 / 输出无法解码 | `code=NO_FACE_DETECTED` | `ErrNoFaceDetected` | 400 `NO_FACE_DETECTED` |
| 帧头非法 / 尺寸越界 / payload 长度不符 | `code=INVALID_INPUT` | `ErrInvalidArgument` | 400 |
| 包未安装 / 入口缺失 / 符号缺失 | `code=UNSUPPORTED`，exit 5 | `ErrUnsupported` / `ErrEntry` | 500 |
| 模型不可用 | `code=MODEL_UNAVAILABLE`，exit 5 | `ErrUnsupported` | 500 |
| 崩溃 / 超时 / 非 JSON / `request_id` 错配 | — | `ErrInternal` | 500 `FEATURE_EXTRACT_FAILED` |
| `feature_dim` 为 0 或 > 512 | `status=passed` 但维度非法 | `ErrInternal` | 500 |

⚠️ **严禁任何失败路径写零向量**：零向量与真实登记在库里不可区分，会静默永不匹配。

### 7.3 进程生命周期与并发

- **并发 1、一问一答**：与旧的插件互斥锁语义等价；请求排队由 30s 超时兜底，不引入无界并发 spawn。
- **只对「流失步」换进程**：写失败 / 读失败 / 超时 / 非 JSON / `request_id` 错配 / 维度非法 → 杀进程组 + `Wait()` → 重建 → 重发该帧，第二次失败 fail-closed。**帧级裁决（`NO_FACE_DETECTED` / `INVALID_INPUT`）不回显错误但换了进程**：包进程是健康的，换掉它只会让下一帧再付一次模型冷加载（见 §6.1「一张坏图不该毁掉热进程」）。`request_id` 必须回显并校验——错配等于把别人的特征写进底库。
- **调用方取消不重试**：ctx 到期/取消时子进程已被 `AfterFunc` 杀掉，重试只会再起一个立刻再杀的进程；此时对外仍是 `ErrInternal`（宿主侧失败），但不进重试循环。
- **单行上限 256 KiB**：stdout 一行 = 一条 JSON 回复，超过上限按失步处理；不然失控的包能用一行输出吃光宿主内存。
- **`Close` 之后不再 spawn**：reaper 已停，此后再 spawn 就没人回收——`Extract` 直接返回 `ErrInternal`。
- **超时杀进程组**：`kill(-pid, SIGKILL)`，只杀单进程会留下抱着模型的孙进程。已经 `Wait()` 回收的进程**不得再发信号**：pid 可能已被复用，`kill(-pid)` 会误伤别的进程组。
- **超时文案要诚实**：报告实际等了多久 + 配置预算；预算可能来自调用方 ctx 而不是 `RequestTimeout`，只报后者会把排查引向错的旋钮。
- **空闲回收**：空闲 `ZHONG_FACE_PLUGIN_IDLE_TIMEOUT_SEC`（默认 60s，沿用历史变量）后回收，内存曲线与旧实现一致；计时与请求共用一把锁，绝不事后补 Reset（会把正在推理的进程杀掉）。
- **缓存 key 含包身份**：`<entry 路径>` + `(dev, ino, size, mtime)`。包被原地替换后必须换进程，否则新登记的底照由旧模型产出，与流式识别不同源（表现是识别率莫名下降，且没有任何错误）。
- **spawn 复用** `algorunner.StartPinned`：`Pdeathsig` 挂在 fork 的那个 OS 线程上，必须 pin→spawn→`Wait`→unlock 在同一 goroutine 内完成。

### 7.4 验证边界

- Go 侧单测（`internal/faceextract`）用**桩 provider 可执行文件**驱动，覆盖：happy path、复用只起一个进程、五类失败码、`feature_dim` 0/513、`request_id` 错配、崩溃后重试一次、二次失败即终、超时杀组、stdout 非 JSON、包原地替换、空闲回收、坏帧不进进程；外加**帧级裁决不换进程**、**失步才换进程**、**取消不重试**、**带 padding 的 stride**、**超长单行**、**Close 幂等且不再 spawn**。
- `packRGB24` 的方向映射与旧逐像素 switch 实现逐字节等价（5 类 image × 8 个方向），防止仿射系数写错导致「行列对调但顺序错」的静默错图；`BenchmarkPackRGB24` 给定 1920x1080 量化基线（当前约 39 ms/帧，登记是人速操作，未做类型特化）。
- 真包集成验收只做一次（`packages/face_recognition` 的 `make test` + 打包期的黑盒验包），Go 测试不硬依赖 `build/packages/` 下的产物。
