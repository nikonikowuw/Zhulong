# 原生层日志桥接 — 技术设计

> 父任务：`.trellis/tasks/10-20-industrial-logging` · 交付契约 C-2（**含一处修订，见 §2**）

## 1. 库拓扑与日志出口

```txt
                    ┌──────────────────────────────────────────┐
                    │  ZhulongLog (STATIC)  ← 新增             │
                    │    src/common/log.cpp                    │
                    │    - 全局 sink 槽位（启动期写、运行期读）  │
                    │    - ZL_LOGD/I/W/E 宏                    │
                    │    - 无 sink 时 → stderr 单行 JSON        │
                    └──────────────┬───────────────────────────┘
                                   │ PUBLIC 链接
                    ┌──────────────▼───────────────────────────┐
                    │  ZhulongAlgoIpc (STATIC)                 │
                    │    unix_socket.cpp 的裸 std::cerr → ZL_LOGE│
                    └──────┬────────────────────────┬──────────┘
                           │                        │
        ┌──────────────────▼──────────┐   ┌─────────▼────────────────────┐
        │ ZhulongEngine (STATIC)      │   │ zhulong_algo_runner (EXE)    │
        │ abi/engine.cpp, pipeline/   │   │ runner.cpp                  │
        │ nodes/ + ZhulongAlgoIpc     │   │ ZhulongAlgoIpc + runtime.so │
        └──────────┬──────────────────┘   └─────────┬────────────────────┘
                   │ CGO 静态链接                    │ stderr 管道
                   ▼                                 ▼
        ┌──────────────────────────┐      ┌──────────────────────────┐
        │ Go 进程内                 │      │ Go 侧 os/exec 管道        │
        │ C ABI callback            │      │ runner protocol/native log│
        │ → nativelog.Queue → zap   │      │ → bounded JSON parser     │
        └──────────────────────────┘      └──────────────────────────┘

ZhulongAlgoRuntime (SHARED) 中的 backend/platform 实现不链接 ZhulongLog。
```

**`ZhulongAlgoRuntime`（.so）不链接 `ZhulongLog`**：`dmabuf_allocator.cpp` 同时编入 `ZhulongAlgoIpc`（静态）与 `ZhulongAlgoRuntime`（共享）。backend 与 runtime allocator 保持无日志核心依赖；插件模型/worker/process 错误由 runner API 边界输出 `module=algorithm.runner`、稳定 operation token 与 numeric `rc`，不提供 backend-specific 诊断。platform allocator 的直接日志只在 `ZhulongAlgoIpc` 编译变体启用，runtime 变体保持静默。

---

## 2. 契约 C-2（**修订**）：传递长度参数

### 2.1 修订理由

父任务 `design.md` §3.2 原定义的回调签名不带长度：

The parent design omitted lengths from the callback type (`const char *module, const char *text`).

Go 侧接收时必须 `C.GoString`（内部先 `strlen` 再分配再拷贝）。
`strlen` 在 C++ 工作线程上对 C 内存做一次 O(n) 扫描，虽然日志行很短（< 2 KiB），
但这是**在实时链路线程上做无谓工作**。传递长度可消除它，且让契约更显式。

### 2.2 修订后的签名

```c
/**
 * @brief 日志回调函数原型
 *
 *   - 回调由 native 任意工作线程同步调用（不是 Go runtime 创建的线程）。
 *   - module 必须是非空 UTF-8（1..128 字节），text 必须是 UTF-8（0..2048 字节）；超限拒绝并计数。
 *   - 长度分别由 module_len / text_len 给出，缓冲区仅在回调期间有效。
 *   - 严禁在回调内调用引擎任何控制接口（acquire/release/probe/stop/destroy）。
 *   - Go callback 只做长度校验、同步拷贝与非阻塞入队；完整延迟/分配由 benchmark 验收。
 *
 * @param token      注册时传入的不透明整数（Go 侧为 cgo.Handle）
 * @param level      Zhulong_LOG_* 之一
 * @param stream_id  关联的 streamId；0 表示无单一流上下文
 * @param module     模块标签（如 "pipeline.stream"），非 NUL 结尾依赖
 * @param module_len module 的字节长度
 * @param text       日志正文（UTF-8），非 NUL 结尾依赖
 * @param text_len   text 的字节长度
 */
typedef void (*Zhulong_log_callback)(uintptr_t token, int32_t level, uint64_t stream_id,
    const char *module, size_t module_len,
    const char *text, size_t text_len);
```

C++ 侧格式化后直接得到 text_len，module 长度只在有界 module 上计算。不得用未经测量的纳秒/每秒分配估算替代板端 benchmark。

### 2.3 Go 侧的分配代价（需测量）

Go must synchronously copy the callback's C-owned module/text buffers before returning; `C.GoStringN` is the conversion approach. These copies allocate, so benchmark the full callback (cgo, conversion and enqueue) on the target SoC; a `TryEnqueue`-only benchmark is not representative. Do not retain C pointers or promise zero allocation.

---

## 3. C++ 日志设施

### 3.1 Internal logging API

`log.hpp` declares `LogLevel` (`Debug=0`, `Info=1`, `Warn=2`, `Error=3`), a length-based callback carrying `stream_id`, sink registration, and `log_write(level, stream_id, module, format, ...)`. The `ZL_LOGD/I/W/E` macros require an explicit stream ID; use `0` when no single stream applies.

Registration is immutable while any producer can call `log_write`. Native producer calls only format bounded data and enqueue; no writer I/O or blocking wait is allowed on the caller.

### 3.2 Implementation contract

`log_write` filters by level, formats a bounded `Record`, then uses exactly one of two paths:

- Engine process: invoke the registered C callback synchronously with `stream_id`, lengths and callback-lifetime buffers. Go copies the bytes and performs only bounded queue admission.
- Standalone runner: push to a fixed-capacity MPSC ring using non-waiting producer admission. Contention/full increments a drop counter; there is no retry loop or producer-side I/O.

> **Corrected 2026-10-21.** "Non-waiting producer admission" was originally implemented as `try_lock` on the ring mutex. That is wrong when the ring lock is shared with the writer thread: the writer re-acquires the mutex on every `wait_for` wakeup, so a `try_lock` producer loses the race and silently drops a healthy record — measured at ~2.7% of runner process starts, including the first `Error` record. Producers now block on the ring mutex; the critical section is bounded by a fixed-size `Record` copy because serialization and stderr writes happen after the lock is released, so producer latency is still bounded and cannot deadlock. Drop counting now covers only genuinely full capacity and post-stop admission. See `.trellis/spec/backend/logging-guidelines.md` §3 and `native/tests/native_log_test.cpp`.

Both bounded queues have a reviewed capacity of 256 records: `nativelog.Queue` in the Go host and the MPSC ring in the standalone runner. At maximum module/text lengths (128/2048 bytes), the Go queue's record payload plus 72-byte `Record` slots is approximately 0.55 MiB before allocator/channel overhead; the native ring's raw fixed-record payload is approximately 0.53 MiB before metadata. The runner's two bounded stdout/stderr readers retain at most 128 KiB of line content in total. This is approximately 1.2 MiB of fixed queue/line storage across the two processes, excluding writer serialization buffers, allocator/runtime overhead and concurrent producer stack scratch. Verify peak RSS and drop behavior on the RK3568 EVB1 target; this is a sizing estimate, not a measured RSS guarantee.

A dedicated runner writer owns JSON serialization and stderr writes. It emits complete `zhulongLog` records within the pipe atomic-write limit, preserves JSON closure when truncating, replaces invalid UTF-8, and reports recoverable queue-drop/write-error counts. Writer blocking is isolated from producer threads. Startup and drain/stop are explicit runner lifecycle operations; startup failure aborts runner startup.

The native minimum level defaults to `Info`; the host's runtime logger policy remains authoritative for Go-side output filtering.

Detailed queue/writer implementation and tests belong in `implement.md`; do not move serialization or sink I/O back to producer threads. The runner subprocess and its stdout/stderr readers are separate from the C callback lifecycle. Stop the subprocess and join both readers before the final `logger.Sync()`; otherwise a reader could emit after the logger has been synchronized.

### 3.3 Build integration

Add one `ZhulongLog` static library and link it publicly through `ZhulongAlgoIpc` together with `Threads::Threads`. Do not link it into `ZhulongAlgoRuntime`; the runner starts/stops its writer explicitly around worker lifetime. Keep exact CMake edits in `implement.md`.

### 3.4 ABI declaration consistency

`engine.cpp` includes both internal `log.hpp` and public `engine.h`; a compile-time type check must ensure `Zhulong_log_callback` and `zhulong::LogSink` remain equivalent. Keep the assertion in the implementation task, not as a code listing here.

### 3.5 ABI registration behavior

`Zhulong_set_log_callback` is a thin C ABI operation isolated by the existing exception boundary. Register only before the first engine producer starts; unregister only after `engine_destroy` returns and all probe/engine producers are quiescent. The setter must reject invalid lifecycle transitions and return a stable status; callback/token mutation while active is forbidden.

### 3.6 Remove direct native stderr logging

Replace the two `std::cerr` exception paths in `unix_socket.cpp` with `ZL_LOGE(0, "ipc.unix_socket", ...)`. Do not include `e.what()`, client identifiers or socket paths; emit a stable failure event only. Keep third-party FFmpeg logging quiet as specified in the PRD.

---

## 4. CGO bridge contract

| Element | Contract |
| --- | --- |
| Registration | Register the callback before native producers start; on failure, abort startup. |
| Token ownership | Go creates/deletes `cgo.Handle`; C receives only its integer value, never a Go pointer. |
| Callback data | `stream_id`, level, and bounded module/text byte slices are valid only during the callback. |
| Bounds | Module is nonempty and at most 128 bytes; text is at most 2048 bytes and may be empty. Reject/drop oversized or empty-module records and increment the invalid-input counter. |
| Validation | Module pointer must be non-null; text pointer may be null only for zero length. Reject other invalid pointer/length pairs before conversion and count them. |
| Copying and ownership | Native owns module/text buffers and lends them only until callback return. Go copies synchronously with `C.GoStringN`, which allocates Go-owned strings managed by Go GC; never retain or `C.free` borrowed native pointers. Enqueue only the Go-owned `Record`. |
| Levels | Explicit mapping between C `0..3` and zap `-1..2`; unknown values map to a documented fallback. |
| Panic boundary | `cgo.Handle.Value()` is valid only while registered. Native quiescence, callback unregister, then handle deletion are mandatory. |

The `Engine` stores one handle for the registered sink. Callback registration precedes `Engine.Start`; unregister occurs only after `Engine.Close` returned and all producers are quiescent. The queue remains running through `engine_destroy` so teardown callbacks remain valid.

The exact declarations and conversion code belong in `implement.md`; this section records ownership and behavioral contracts only.
Level-map tests must cover all four C values and one invalid value; do not infer correctness from numeric overlap.

---

## 5. runner 日志结构化解析

### 5.1 Current failure modes

The current reader uses `bufio.Scanner`'s default line limit, writes each entire line as an unstructured `text` field, and does not handle `Scanner.Err()`. An oversized line can silently stop all later runner logging. Replace this path with the bounded classification contract below.

### 5.2 Classification and bounded reading

`streamLogs` uses a bounded reader with a 64 KiB retained-line limit. If a line exceeds the limit, discard its remainder through the next newline, count it, then continue with subsequent lines. A terminal reader error is recorded with a stable reason and ends that reader; do not claim recovery from an unrecoverable I/O error.

Dispatch order is:

1. Runner protocol event: record validated status/stage/step, `rc`, and `error_present`; never log raw error text.
2. Native JSON log (`zhulongLog=true`): record validated module, streamId, level and message byte count. Omit arbitrary message text; the later `logsafe` package is not a general secret detector and does not authorize it.
3. Unknown line: record source and byte count only; never copy raw line content into application logs.

Token-like fields use a strict ASCII allowlist. Oversize input, invalid values and rate-limited records increment visible counters. Parsing/read code belongs in `implement.md`.

`observeFailureStage` 封装既有的 `"stage":"connect"` 探测逻辑，保持 `failureStage` 语义不变。

### 5.3 限流

runner 崩溃循环时会以最大速度刷 stderr。**必须限流**，否则：

1. 主日志被冲垮，故障前几秒的上下文被挤出滚动窗口；
2. 若 sink 是文件，短时间内写入量可达 GB 级。

方案：复用 `nativelog.Queue` 的丢弃计数机制，或引入 `zapcore.NewSamplerWithOptions`
仅作用于 runner 日志分支。

**本设计选定**：在 `RunnerProcess` 内按 stdout/stderr 来源做有界限流，每秒最多输出 `N` 条 runner 事件；超出部分增加原子丢弃计数，在下一次可写日志时报告。runner never logs arbitrary raw content; `logsafe` does not authorize restoring messages, paths or exception text. Any future diagnostic text requires an explicit source allowlist and separate security review.

理由：`zapcore` 的 sampler 作用于整个 logger，会连带限流 supervisor 自身的日志；
而限流应该只针对「runner 输出」这一条通道。

⚠️ 补齐上报必须存在（与 `nativelog` 同一原则）：**不能让「日志被限流」这件事本身不可见。**

### 5.4 敏感字段边界

B does not output `broker_socks`, file/socket paths, URLs or unparsed runner lines. C provides typed `logsafe` helpers, but they do not make arbitrary message text safe; B adds no placeholder sanitizer and assumes no API that has not been delivered.

---

## 6. Lifecycle wiring

Startup order: start the `nativelog` consumer, register the callback, then start native producers. Registration failure aborts startup and rolls back the consumer.

Shutdown order is the table below. If producer quiescence or callback unregister cannot be confirmed, retain the handle and queue and return the failure; do not risk a use-after-free.

| 顺序 | 动作 | 反了的后果 |
| --- | --- | --- |
| 1 | `nativeLog.Start()` | — |
| 2 | `native.SetNativeLogSink(...)` | Must precede engine producers; registration failure aborts startup |
| 3 | `native.Start()` | Producers may now log through a live callback and queue |
| … | … | … |
| 4 | close HTTP/business entry points; stop the runner subprocess and join its stdout/stderr readers; `native.Stop(ctx)` quiesces native producers | No new callbacks or runner log writes may be emitted after final logger sync |
| 5 | `native.Close()` / `engine_destroy` | Callback and queue remain live for teardown logs |
| 6 | `native.CloseNativeLogSink()` then delete cgo.Handle | Only after all native producers are quiescent |
| 7 | `nativeLog.Stop(ctx)` drains queue | Must not run while callback is registered |
| 8 | `logger.Sync()` | Last logging lifecycle action |

---

## 7. 日志点铺开计划

按优先级分批，每批独立提交、独立验证。**不得一次铺满 59 个文件。**

| 批次 | 范围 | 文件数 | 重点日志点 |
| --- | --- | --- | --- |
| B1 | `src/pipeline/` | 10 | 流建立/失败/释放、订阅 attach/release、背压丢帧、发布率变更 |
| B2 | `algorithm/runner/runner.cpp` 插件 API 边界 | 模型、worker、task、process 失败的稳定 operation token 与 numeric `rc`；不声称 backend-specific 诊断 |
| B3 | `src/nodes/` | 10 | RTSP 建连/超时、解码器初始化、MPP 回退到软件解码 |
| B4 | `platform/dmabuf_allocator.cpp`（仅 `ZhulongAlgoIpc` 编译变体） | 1 源文件 / 2 个 target | 分配失败；runtime `.so` 变体保持无日志核心依赖 |

B2/B4 依据实际 CMake 拓扑修订：RKNN/ONNX/RGA source 和 `dmabuf_allocator.cpp` 均编入 `ZhulongAlgoRuntime`；该共享库按已批准方案不链接 `ZhulongLog`。B2 在 `runner.cpp` 插件 API 边界记录 `algorithm.runner` + operation token + numeric `rc`，仅提供通用失败摘要。B4 的 allocator 直接日志只在 `ZhulongAlgoIpc` target 编译定义下启用；runtime 编译变体不含日志调用。两条限制都避免共享库 duplicate sink/writer 状态，不输出供应商错误文本、路径或模型输入。

- 必须带 `module` 标签（`pipeline.stream`、`algorithm.runner`、`nodes.decode`、`platform.dmabuf`）
- **不得包含**：RTSP URL、任何形式的凭据、帧数据、二进制 blob、base64
- Failure records use stable reason/code and allowlisted numeric context; do not log exception `what()`, vendor error prose, user input or paths.
- Runner plugin operation failure records use only fixed operation tokens, stream ID when available, and numeric `rc`; they do not identify the selected backend or expose vendor error text.
- 热路径（每帧执行）上的日志必须 `Debug` 级，且优先考虑用计数器替代逐帧日志

---

## 8. 测试策略

| 层 | 测试 | 位置 |
| --- | --- | --- |
| C ABI（纯 C） | 注册 / 注销 / NULL 不崩溃 | `native/tests/engine_abi_test.c` |
| C++ unit | `log_write` producer never writes stderr; runner writer emits valid JSON, escaping and truncation policy preserve full line | `native/tests/native_log_test.cpp` |
| C++ unit | installed sink receives level, streamId, lengths; min-level filtering works | same |
| C++ unit | full/contended runner queue drops without blocking; drop/write failure counters are visible | same |
| Go unit | `zapLevel` / `cLogLevel` four levels + invalid values, bounded lengths, invalid token lifetime contract | `internal/engine/log_bridge_test.go` |
| Go unit | runner protocol/native log/unparsed classification, oversized-line drain and later-line recovery, rate limit | `internal/algorunner/process_test.go` |
| Go integration | shutdown ordering: stop/close native, unregister/delete handle, drain queue, logger sync | `internal/app/runtime_test.go` |

Static gates assert there is no direct `std::cerr`/`std::cout` logging outside the runner protocol and FFmpeg logging remains disabled. Exact commands belong in `implement.md`.

---

## 9. 对父任务 design.md 的修订（规划期已完成）

1. ✅ §3.2 契约 C-2 的回调签名增加 `module_len` / `text_len`（理由见本文件 §2.1）。
2. ✅ §3.2 补充显式映射函数与「两侧数值不同」的警告。
3. ✅ §3.3 停机顺序约束（`nativelog.Stop` 先于 `closeNative`）已在父任务 design 记录；
   本文件 §6 进一步细化为 6 步顺序表。
