# Go 日志核心加固 — 技术设计

> 父任务：`.trellis/tasks/10-20-industrial-logging` · 契约 C-1 / C-3 的交付方

## 0. 版本核实（zap v1.28.0，已对照模块缓存源码）

本设计中的所有 zap API 均已对照 `/home/niko/go/pkg/mod/go.uber.org/zap@v1.28.0` 核实。
**其中两处与常见写法不同，必须注意**：

| 事实 | 核实结果 | 影响 |
| --- | --- | --- |
| `AtomicLevel` 的位置 | 在 **`go.uber.org/zap`**（顶层包），**不在** `zapcore` | 写 `zap.AtomicLevel` / `zap.NewAtomicLevel()`，不是 `zapcore.*` |
| `zapcore.Field.Clone()` | **v1.28.0 中不存在** | 任何「先 Clone 再入队」的异步日志方案都不可用 |
| `zapcore.Level` 数值 | `DebugLevel = -1`、`InfoLevel = 0`、`WarnLevel = 1`、`ErrorLevel = 2` | 与 C 侧枚举的映射必须显式写，不依赖数值巧合 |
| `zap.NewProductionConfig()` 采样 | **默认 `100:100`**（`config.go:148` 注释明示） | 这就是「突发故障日志被静默丢弃」的来源 |
| `Encoder.EncodeEntry` 并发安全 | 是。`encoder.go:453`：「Clone and EncodeEntry methods will be called concurrently and shouldn't modify the receiver」 | 同一 encoder 可被多 goroutine 并发 `EncodeEntry` |
| `Encoder.EncodeEntry` 返回值 | `(*buffer.Buffer, error)`，需自行 `Free()` | `buf.Bytes()` 的切片在 `Free()` 后失效，**保留必须拷贝** |
| `zap.AtomicLevel` 的接收者 | 值类型；`SetLevel`/`Level`/`Enabled` 是值接收者，`UnmarshalText` 是**指针**接收者 | 传值时用 `lvl.SetLevel(...)`；`lvl.UnmarshalText(...)` 需可寻址 |

The reference `zapcore/ioCore.Write` pattern must release its pooled buffer after the synchronous sink write. If bytes must outlive `EncodeEntry`, copy them before freeing the buffer.

---

## 1. 配置模型

### 1.1 Configuration fields

| Field | Contract |
| --- | --- |
| `logging.development` | Controls development-only defaults; synchronize with the existing app flag. |
| `logging.level` | `debug|info|warn|error`; strict validation, default `info`. |
| `logging.format` | `json|console`; omitted value follows development mode. |
| `logging.output` | `stderr`, `stdout` or a file path; file output requires bounded rotation. |
| `logging.sampling` | Explicit opt-in with positive `initial` and `thereafter`; default disabled. |
| `logging.rotation` | Per-file size, backup count, age, compression and required `max_total_size_mb`; deployment values remain profile-specific. |
| `Config.ConfigPath` | Internal normalized absolute path actually selected by Viper; not user-configurable. |

`fileConfig.Logging` embeds the app logging config. Strict `UnmarshalExact` means the example config must match the schema; SIGHUP rereads the actual selected file.

`cmd/Zhulong/config.go` 的 `fileConfig.Logging` 改为内嵌 `app.LoggingConfig`，并在 `loadConfigFile` 中把 `values.Logging.Development` 同步到 `Config.Development`。同时保存 `viper.ConfigFileUsed()` 的规范化绝对路径到 `Config.ConfigPath`，供 SIGHUP 只重读实际配置文件。

`viper.UnmarshalExact` 严格模式意味着 `config.toml.example` 必须与结构体同步更新，否则未知键会中止启动。

### 1.2 Defaults and profile configuration

Default output is stderr with JSON encoding, `info` level, sampling disabled and rotation disabled for stream outputs. A file path requires rotation to be enabled and `max_total_size_mb` to be positive. The hard total covers the active file, archives and rotation/compression temporary files. The concrete size/retention and journald quota must be set by the deployment profile; do not treat the following sample values as approved production budgets.

---

## 2. Logger construction contract

`newLogger` returns the process logger, its shared `zap.AtomicLevel`, and an error. It validates only `debug|info|warn|error` and `json|console`; invalid values fail startup. Time is encoded in UTC and durations in milliseconds. Development mode may default to console format; production defaults to JSON.

Build the core directly rather than using `zap.NewProductionConfig`, whose default sampler is `100:100`. Sampling is opt-in and requires positive thresholds; disabled means no intentional sampling, not losslessness under queue or sink failure. Add caller metadata and error-level stack traces. zap internal diagnostics use a separate one-line JSON stderr writer. Replace the global logger only after successful construction.

Logger creation and sink errors return through Fx startup; no panic or silent fallback. Bootstrap errors before logger construction remain on stderr and are outside post-initialization log-stream assertions.

---

## 3. Sink 与轮转

### 3.1 Sink contract

The default sink is locked stderr; stdout is an explicit alternative. File output is rejected unless bounded rotation is enabled. Sink construction errors fail startup rather than falling back silently.

### 3.2 Storage route

| Route | Selection | Responsibility |
| --- | --- | --- |
| journald-managed stderr | systemd deployment profile | Host profile sets the aggregate quota and retention; values remain unconfirmed. |
| built-in rotation | profile without systemd | Application enforces bounded size/backups/age and exposes sink health. |

Do not assume a host quota from the example profile. Choose the route and numeric budget only after deployment storage and retention requirements are confirmed.

### 3.3 内置轮转的正确性契约（仅 fallback 路线）

若采用内置轮转，必须满足以下契约，并逐条写单元测试：

| # | 契约 | 理由 |
| --- | --- | --- |
| 1 | 配置显式正数 `max_total_size_mb`；统计活动文件、所有归档及轮转/压缩临时文件的逻辑字节数，不允许超预算 | 仅限制活动文件和备份数量，无法约束压缩期间的额外副本 |
| 2 | **单写入者假设**：同一路径只允许一个进程写入 | 多进程轮转需要文件锁，超出本任务范围；若违反，归档会互相覆盖 |
| 3 | 写前检查大小，超限则 `close` → `rename` → 重新 `open` | 顺序不能反，否则新写入会落进待归档文件 |
| 4 | 归档名同时含**单调递增序号**和 UTC 创建时间戳；序号只用于排序，`max_age_days` 只依据时间戳清理 | 序号本身不携带年龄信息，mtime 可能被外部工具改写 |
| 5 | `max_backups` 约束归档数量，删除最旧归档以满足数量和总量预算 | 防止 eMMC 被写爆 |
| 6 | `compress` 使用单个有界 worker，最多一个压缩任务在途；启动压缩前按压缩器的最坏输出上界原子预留目标临时文件空间，源归档继续计入总量预算；预留失败时跳过压缩，不得创建无界临时副本或任务队列 | 压缩不得阻塞写路径，也不得突破总容量上限 |
| 7 | 总量预算耗尽或轮转失败时停止写入该文件 sink，置位 sink 健康错误并经独立 stderr/宿主通道报告；不得退化为无限追加 | 避免日志故障把 eMMC 撑满，同时让故障可见 |
| 8 | `Sync()` 对当前 sink 执行其支持的 flush/fsync；文档说明具体持久化窗口。不得声称 Sync 可防止突发掉电时丢失此前未同步数据 | fsync 每条日志会显著影响性能，设备级保证需按平台验证 |

sink 故障时禁止无限追加。对可恢复的短暂写入失败，应进入故障状态、通过独立宿主通道报告并计数；恢复策略按所选部署 profile 明确。`Sync()` 先排空已知异步队列再同步 sink，但不承诺断电零丢失。

---

## 4. Runtime level reload (SIGHUP)

The Fx lifecycle handler rereads only `logging.level` from the actual selected `ConfigPath`; it does not rebuild the sink, encoder or core. The config publisher must use atomic file replacement. Every read value passes the same strict `debug|info|warn|error` allowlist as startup. Read or validation failure emits a warning and keeps the current level; it is not a panic or startup error. On shutdown, unregister the signal and join the handler goroutine before returning.

Update the existing shared `zap.AtomicLevel` with `SetLevel`; do not use `UnmarshalText` to bypass validation.

---

## 5. Contract C-1: `internal/logctx`

`logctx` may depend only on `context` and zap; it must not import Gin, `httputil` or `httpmiddleware`, which would recreate the existing dependency cycle. It stores request ID, trace ID and stream ID under package-private typed keys and exposes accessors plus a logger helper.

`Fields(ctx)` returns only populated fields and returns nil when none exist. `Logger(ctx, base)` preserves the `Named` prefix, adds those fields when present, and uses `zap.L()` only when `base` is nil. The no-fields benchmark target is 0 allocs/op; verify it rather than inferring it from the API.

---

## 6. HTTP 层接线

### 6.1 Request ID middleware

Accept `X-Request-ID` only when it passes the existing validation; otherwise generate one. Store it in both Gin context and the standard `context.Context`, return it in the response header, and keep compatibility accessors as thin wrappers over `logctx`. Standard context is the propagation path across `httputil`, services and repositories.

### 6.2 Access logging and long-lived streams

Access events are always `Info`; unexpected 5xx severity belongs to `WriteError`. Normal requests produce one completion event with request ID, method, route template, status, `latency_ms`, bytes and the typed client-IP field. Never log the raw URL path, query or request body.

Classify streaming routes before the handler runs, using the registered route template or a WebSocket upgrade request. Emit `stream.request.started` and `stream.request.completed`; include `established=false` and `latency_ms` for failed handshakes. Successful WebSocket 101 and SSE responses include `session_ms` instead of ordinary request latency. Verify these cases with real Gin routing tests.

### 6.3 `httputil.WriteError`

For an unexpected 5xx, log exactly one Error with request ID, stable error code and status, even when the cause is nil. Include the cause only when present. A 4xx remains represented by the Info access event; a non-nil cause alone must not promote it to Error.

Keep `zap.L()` as the base logger to preserve the package-level API; `zap.ReplaceGlobals` makes it the same process logger. Add request-context fields through `logctx.Logger`.

---

## 7. 双重日志消除

### 7.1 规则

> 请求路径内的意外 5xx 由 `WriteError` 记一条 Error（无 cause 时也包含稳定 code/status）；access event 固定记为 Info。
> service 只有在错误不经过此边界（例如后台循环）时才记录 Error。

### 7.2 ⚠️ 前置保证（本任务风险最高的动作）

`WriteError` 对意外 5xx 记录且只记录一条 Error，不依赖 `appError.Err != nil` 才决定是否留证据。
删除 service 重复 Error 前确认错误路径确实到达 `WriteError`；后台错误不在此规则内。

执行顺序：

1. 先给 `WriteError` 加回归测试：覆盖有 cause 的 5xx、无 cause 的 5xx、带 cause 的 4xx；断言只有预期的 5xx 各有一条 Error。
2. 再逐个核对 service `logger.Error` 是否属于同一个请求失败且确实返回到 `WriteError`。
3. 逐处删除重复记录，每删一处跑该模块测试；未经过 `WriteError` 的错误保留。
4. 保留后台循环的 `logger.Error`，在代码注释或核对记录中说明归类理由。

已识别的高密度区（实施时优先核对）：

| 文件 | 行 | 处置 |
| --- | --- | --- |
| `internal/record/service.go` | 357 / 371 / 385 / 399 / 413 / 427 / 441 / 451 | 请求路径内 → 删 |
| `internal/network/watchdog.go` | 197 | 后台循环 → **保留** |
| `internal/algorunner/process.go` | 292 / 372 | 后台监控 → **保留** |

### 7.3 规则落地

规则写入 `.trellis/spec/backend/logging-guidelines.md`，并附本节的判定表。
若后续引入 `golangci-lint`，可加自定义 linter；本任务先用 spec + review 约束。

---

## 8. 命名统一与装配 fail fast

### 8.1 `Named` 命名表

规范：`<domain>.<layer>`，全小写点分隔。

| 现状 | 改为 |
| --- | --- |
| `person_handler` | `person.handler` |
| `person_service` | `person.service` |
| `algorithm_handler` | `algorithm.handler` |
| `algorithm_service` | `algorithm.service` |
| `algorithm_supervisor` | `algorithm.supervisor` |
| `record.lifecycle` / `record.handler` | 已合规 |
| `camera.handler` / `camera.service` / `camera.scheduler` / `stream_hub` | `stream_hub` → `camera.stream_hub` |
| `mediafile` | `mediafile.service` |
| `audit.handler` / `audit.service` | 已合规 |
| `gallery_broadcaster` | `algorithm.gallery_broadcaster` |

### 8.2 Fail fast

Required Fx providers return an assembly error when a logger dependency is missing; they do not panic or silently substitute `Nop`. Tests that intentionally suppress output inject `zap.NewNop()` explicitly. Update tests that previously relied on nil-to-Nop behavior, without banning explicit test Nop loggers.

### 8.3 `main.go` 去重

- `-reset-network` 分支改为复用 `newLogger`（传入同一份 config），删除重复的 `zap.NewProduction()/NewDevelopment()` 逻辑。
- 配置加载失败不再用 stdlib `log.Fatal`：改为 `fmt.Fprintf(os.Stderr, ...)` + `os.Exit(1)`，
  保持与 zap 输出格式不混淆（此时 logger 尚未建立，无法结构化）。
- 顺带：`fmt.Println` 的成功提示改走 stderr 或保留（这是 CLI 输出，非日志，可接受）。

---

## 9. 契约 C-3（**已修订**）：`internal/nativelog`

### 9.1 修订说明

父任务 `design.md` 原定 C-3 为通用 `AsyncCore`（包装 `zapcore.Core`）。
**规划期核实后修订为 `nativelog.Queue`**，理由：

1. **`zapcore.Field.Clone()` 在 v1.28.0 中不存在**（已核实）。通用 `AsyncCore` 若要保留
   `[]zapcore.Field` 就必须自行深拷贝，而 `ObjectMarshaler` / `ReflectType` 字段的深拷贝无法通用实现。
   唯一安全的通用做法是「在调用线程上同步 `EncodeEntry`」—— 但那样在 C++ 采集线程上就跑了一遍编码。
2. **原生日志回调只携带固定字段** —— C ABI carries level, `stream_id`, bounded module/text buffers and explicit lengths, not arbitrary zap fields. A generic `AsyncCore` would add unsafe deep-copy and encoding complexity.
3. `nativelog.Queue` 只搬运**预格式化字符串**，在 native 线程上零编码、零反射、零 zap 依赖。

### 9.2 Queue contract

| Operation/data | Contract |
| --- | --- |
| `Record` | Level, stream ID, module, message text and observation time; strings are owned by Go before enqueue. |
| Construction | Bounded capacity and explicit overflow policy; confirm capacity per deployment profile. |
| Producer admission | Bounded nonblocking admission: no retries, spins or waiting on locks. Contention/full returns a counted drop immediately; if a lock is used, acquire it with a nonblocking attempt and count/drop on contention. The consumer must never hold the admission lock during sink I/O. Do not promise zero allocation or absolute losslessness. |
| Consumer | Started/stopped explicitly; emits structured records and periodically reports aggregate drops. |
| Invalid input | `RecordDrop` increments the same observable counter without creating a log entry. |
| Stop | Only after all native producers are quiescent, callback unregistered and handle deleted; drain within context, report timeout/pending count, and never close a channel that may still receive. |

The exact Go types and methods belong in `implement.md`.

### 9.3 丢弃策略

| level | 队列满时 | 理由 |
| --- | --- | --- |
| Debug / Info | 丢弃 + 计数 | 低优先级事件；仍需保留可见丢失计数 |
| Warn / Error | 队列满时也可能丢弃 + 计数 | 不阻塞实时 producer；不能承诺关键级别绝不丢 |

> 队列丢弃计数在进程崩溃前仍可能未写入持久介质。提供运行期计数/健康状态和优雅停机汇总；不承诺突发断电后计数也一定留存。

**消费者行为**：消费者输出 `streamId`（非零时）、`module`、`SeenAt` 与消息文本；每秒报告累计丢弃计数。计数本身也受 sink 故障影响，因此还应暴露在进程健康状态中。

**注意**：`Stop` 必须在引擎所有 producer 停止、回调注销且 handle 删除后调用；随后排空队列，最后才 `logger.Sync()`。

### 9.5 本任务的交付边界

子任务 A **只交付** `internal/nativelog` 的完整实现 + 单元测试（含并发与丢弃计数测试）。
**不接入** C ABI（由子任务 B 调用 `TryEnqueue`）。接入点由 B 通过一个小适配器完成：

Subtask B supplies the C ABI adapter and passes `stream_id`, module/text and event time into this queue; no additional example code is needed here.

---

## 10. 对父任务 design.md 的修订（规划期已完成）

以下三项修订已在**规划期**落实到父任务 `design.md`，本子任务实施时无需重复：

1. ✅ §3.3 契约 C-3 由 `AsyncCore` 改为 `nativelog.Queue`（含 §9.1 的修订理由）。
2. ✅ §3.2 契约 C-2 补充：**C 侧枚举与 zap level 的映射在 Go 桥接层显式完成**
   （已核实 zap 数值为 `-1/0/1/2`，与 C 侧 `0/1/2/3` 不同，**必须显式映射**）。
3. ✅ §7 集成验收补充：native producer 销毁、callback 注销、handle 删除、队列 drain/Stop、logger Sync 的严格顺序。

**本子任务实施时仍需做的事**：如果阶段 1.1 的环境核实发现 zap 版本已变（`go.mod` 中不再是 v1.28.0），
必须重新核实 §0 的全部结论，并同步修订父任务 `design.md` 的 §3.1.1 版本核实表。
