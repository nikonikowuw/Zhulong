# Go 结构化日志规范 (Uber Zap)

> `go.uber.org/zap` 零分配结构化日志、强类型字段、等级划分与敏感凭据脱敏。

---

## 1. 核心原则

1. **强类型零分配 (Zero Allocation)**：
   - 热路径与核心业务统一使用 `*zap.Logger` 配合强类型字段（如 `zap.String()`, `zap.Int()`, `zap.Error()`），避免反射与堆内存分配开销。
   - 允许在非高频配置初始化阶段按需使用 `*zap.SugaredLogger`。
2. **绝对禁止敏感信息泄漏**：
   - 严禁打印密码、RTSP 流认证凭据、JWT 签名密钥或 API Token。
   - 严禁将原始视频帧、大块图像 Buffer 序列化至日志。
3. **分模块打标命名**：各模块使用子 Logger 区分业务域：`logger.Named("camera")` 或 `logger.With(zap.String("module", "camera"))`。
4. **停机主动刷新 (Sync)**：在 Fx `OnStop` 钩子中执行 `_ = logger.Sync()`，确保进程退出前缓冲区日志落盘。

---

## 2. 日志级别准则

| 级别 | 调用 | 适用场景 | 生产默认 |
| --- | --- | --- | --- |
| **DEBUG** | `logger.Debug` | 详细帧序列号、底层驱动临时状态、探测数据 | 关闭 (按需开启) |
| **INFO** | `logger.Info` | 关键生命周期（服务启动/停止、相机上线、录像落盘、配置更新） | **开启** |
| **WARN** | `logger.Warn` | 暂不影响主流程但需注意（短暂丢包重试、推理队列丢旧帧） | **开启** |
| **ERROR** | `logger.Error` | 功能性严重故障（硬件不可用、DB 失败、Pipeline 崩溃） | **开启** |

---

## 3. Logger 初始化与 Fx 集成模式

本仓库使用 zap v1.28.0。该版本 `zap.NewProductionConfig()` 的采样默认值是 `100:100`（见 zap `config.go` 的 `SamplingConfig` 初始化），会在突发日志中静默丢弃记录。因此，应用 logger 必须显式组装 core；不得使用 `NewProductionConfig()`，生产默认采样关闭，只有配置显式启用且 `initial` / `thereafter` 均为正数时才安装 sampler。

应用配置只接受 `debug|info|warn|error` 和 `json|console`。默认输出 `stderr`；JSON level 小写、console level 大写彩色；时间戳为 UTC ISO8601，duration 编码为毫秒。Zap 自身编码/写入诊断经 `zap.ErrorOutput` 写到 stderr，不能被误认为业务结构化事件。

文件输出必须在生命周期启动阶段、服务启动前执行 `LoggerSink.Prepare()`；Fx 构造函数只校验配置和构建 logger，不做目录创建或文件打开。Sink 关闭前先调用 `logger.Sync()`，随后关闭 sink。文件输出必须启用内置有界轮转并设置显式正数 `max_total_size_mb`。总预算覆盖活动文件、归档及压缩临时文件的最坏输出预留；最多一个 gzip 压缩 worker，预留不适配时跳过压缩。轮转/写入失败不得退化为无限追加，sink 健康错误和拒写计数可通过 `LoggerSink.Health()` 观察，并经独立 stderr 诊断。

具体的文件预算、保留期和 journald 主机配额仍由部署 profile sign-off；不得从示例配置推断生产配额。`Sync()` 对当前文件执行 `fsync`，轮转和压缩时同步目录项，但不保证设备控制器、电源故障或文件系统能把数据持久化到介质，也不承诺突然掉电零丢失。

Fx provider 必须把 `*zap.Logger` 声明为必需依赖；缺失时由 Fx 返回装配错误，不可 panic 或由生产装配静默补成 Nop。测试若要静音，显式注入 `zap.NewNop()`。

`internal/logctx` 只依赖 `context` 和 zap，负责传递 `requestId` / `traceId` / `streamId`。业务层可用 `logctx.Logger(ctx, base)` 添加上下文字段，同时保留已有 `Named(...)` 前缀；不得让该包依赖 Gin 或 HTTP 错误处理中间件。长生命周期流事件应使用业务 stream ID；不能把请求级 `requestId` 自动传播到连接后续事件。

`internal/nativelog.Queue` 是有界、非阻塞的 native event 通道。队列满、锁竞争和无效输入均丢弃并计数；不得让实时/native producer 等待日志 I/O。消费者独立输出结构化记录并周期性报告累计丢弃数；停机先等 native producers quiesce、注销 callback、删除 handle，再 Stop/drain queue，最后 `logger.Sync()`。队列计数仍可能在突发掉电时未持久化。

`Zhulong_set_log_callback` 的 C ABI token 仅承载 `runtime/cgo.Handle` 整数。callback 参数中的 module/text 是借用缓冲区，只在同步调用期间有效；Go trampoline 必须先校验空指针、长度与 UTF-8，再用 `C.GoStringN` 复制到 Go 内存并非阻塞地提交 `nativelog.Queue`，不得保留 C 指针。注册仅能在 native producers 尚未启动时进行；销毁顺序是停止并 join runner/native producers、销毁 engine、注销 callback、删除 handle、排空队列，最后同步应用 logger。

C++ `ZhulongLog` producer 只做有界格式化和队列/回调提交，不写 sink I/O。独立 runner 使用 256 条有界队列和 writer 输出完整 JSON-lines；runner 协议错误行与 native writer 共用 stderr 写锁，避免记录交错。应用侧 runner parser 只输出 allowlisted module/stream ID/长度与稳定 operation token/数值 `rc`，不得转发原始行、message 正文、异常或供应商错误文本。新增 native call sites 只允许稳定事件 token 和经过审查的数值上下文；不得记录 URL、凭据、路径、帧内容或任意外部文本。FFmpeg 日志必须继续由 RTSP capture 入口静默，不得转发 FFmpeg callback。

日志库拓扑是显式安全边界：`ZhulongAlgoRuntime` 不链接 `ZhulongLog`；runner 插件 API 只提供通用 operation/`rc` 失败摘要。共享 DMA-BUF allocator 仅在 `ZhulongAlgoIpc` 编译变体启用 native logging，避免把 sink 状态复制进 runtime `.so`。


---

## 4. 业务安全与操作审计日志 (Audit Logs)

与面向开发者/运维排障的 Zap 运行日志不同，**操作与安全审计日志**面向管理员审计溯源，必须遵循以下规则：

1. **持久化与独立建模**：存储于 SQLite `audit_logs` 表，支持结构化查询（按动作、状态、时间范围过滤与分页）和管理员界面直观呈现。
2. **异步非阻塞管道**：业务关键路径调用 `audit.Service.Record(...)` 必须是非阻塞的（基于缓冲 Channel），缓冲区满时应降级记录警告并丢弃，绝不可阻塞主业务请求或导致写锁饥饿。
3. **容量保护与防爆盘 (FIFO Rolling Retention)**：默认设置严格上限（如 `5,000` 条），在批量落盘后触发滚动清理最旧记录，防止嵌入式存储空间被撑爆。容量裁剪必须豁免 `audit.clear` 自审计记录，否则「清理历史」会在长时间运行后被裁掉。
4. **凭据安全与脱敏**：
   - 严禁将明文密码、会话 Token 写入审计 `detail` 或 `error_msg`；
   - 包含流媒体凭证的 URL（如 RTSP）必须通过 `SanitizeURL` / `url.URL.Redacted()` 脱敏隐藏密码后再行记录。
5. **停机排空 (Graceful Drain)**：在系统关机生命周期中，必须在 HTTP 端口停止监听并排空流量后、SQLite 关闭连接前执行 `audit.Stop(ctx)`，将剩余缓冲区日志全部批量落盘。
6. **动作命名空间与常量**：`action` 必须是 `<domain>.<resource>.<verb>` 或 `<domain>.<verb>` 形式（如 `person.face.add`、`camera.create`、`auth.login`、`time.ntp_sync`、`storage.update_config`），并在模块的 `model.go` 中声明为导出常量，禁止在调用点写裸字符串。审计日志按 `action` 精确匹配查询，裸动词（`"CREATE"`）既无法按业务域归组，也无法与其它域的动作一并筛选。严禁在动作前缀中混入 HTTP URL 路径分类（如错误的 `system.time.*` 或 `system.storage.*`），命名空间首段必须严格对应业务领域。
7. **成功与失败成对记录**：一次写操作要么成功要么失败，两者都必须在审计中留痕——只记成功会让被拒绝的操作（越权尝试、不可变字段改写、并发冲突）在审计里完全消失。失败条目必须填 `Status: audit.StatusFailed` 与 `ErrorMsg`。
8. **声明式记录（Handler 层）**：HTTP 写操作在 Handler 中通过 `internal/audit/http`（包名 `audithttp`）的 Tag API 声明意图，由审计中间件统一收口：
   - `audithttp.Tag(c, action, target, detail)` / `TagMiddleware(...)`：在绑定/副作用**之前**声明，保证绑定失败与业务失败也留痕；`TagMiddleware` 必须注册在 `httputil.HandleJSON` 之前。
   - `TagAction` / `TagTarget` / `TagDetail` / `TagActor`：绑定成功或结果产生后细化。共用同一路由但 action 依赖请求体时（如 camera update/toggle）必须用 `TagAction` 改写，否则两个操作会被记录为同一个 action。
   - `TagError(code, safeMessage)`：HTTP 2xx 但领域失败时显式标记（如 NTP `SyncStatus.State == failed`）。
   - 未调用任何 Tag 的请求不产生审计记录，只读接口无需处理。
9. **失败信息脱敏**：`error_msg` 只能使用 `httputil.WriteError` 保存的已脱敏元数据（`httputil.SafeErrorFromContext` 读取的 `Code`/对外 `Message`）或调用方显式提供的安全摘要；严禁写入原始 `c.Errors`、数据库错误、panic 原文或堆栈。
10. **panic 记录**：`c.Next()` 之后的收口代码在 handler panic 时**不会执行**，`defer` 读取还会拿到默认 200。panic 必须经 `httpmiddleware.Recovery` 的 `PanicObserver` 回调记录为 failed，且保持 `Recovery` 位于最外层。
11. **归因字段**：单账号系统以**请求 IP 为主要归因字段**，取自 TCP 连接来源；未认证请求使用 `anonymous`，缺失 Actor 使用 `unknown`，只有 `RecordSystem` 才使用 `system` 与空 IP。严禁将空用户名默认回退为 `admin`。
    * ⚠️ 不要用 `c.GetString("username")` 取当前账号：本代码库没有任何 `c.Set("username", ...)`，该表达式恒为空，后面接 `if username == "" { username = "admin" }` 就是对每条记录伪造身份。正确做法是 `audit.FromContext(c.Request.Context())` 读取 `auth.RequireAuth` 写入的 Actor（不引入 `auth` 依赖），未认证时记为 `audit.UsernameAnonymous`。
12. **强保证路径**：普通审计走可丢弃的异步队列；只有清空日志（`audit.clear`）使用同步事务 `ClearWithAudit`，因为「已成功清空」这一事实不能依赖可丢弃队列。
13. **禁止心跳型审计 (No Heartbeat Auditing)**：这是本规范最容易犯、后果最严重的一条。审计表按容量 FIFO 裁剪，**任何按固定周期无条件写入的「我还活着 / 我又成功了一次」记录都会在有限天数的运行后把真实操作记录全部挤掉**。5000 条上限下，一条 900s 周期的记录就是 96 条/天、约 52 天滚穿整表 —— 届时 `camera.create`、`auth.login`、`storage.emergency_stop` 这些真正需要溯源的证据被静默销毁，而表看起来「一切正常」。
    - **判据**：这条记录是否可能成为事故追溯时需要的证据？「周期巡检成功了」的答案永远是**否**；「墙钟被跳变了 5 秒」「进入高危复核」「从断网恢复」的答案是是。
    - **周期性遥测一律走运行日志**（Zap，Debug/Info 级），不进 `audit_logs`。「当期状态」类信息（如 `SyncStatus.lastSyncTime` / `offsetMs`）应通过状态查询 API 暴露并由前端消费，**不得双写成审计行**。
    - **长时失败只记跃迁**：后台重试退避（如 NTP 探针 30s → 60s → … → interval）必须在**进入失败态时记一次、恢复时记一次**，而不是每次重试一条。用带锁的 `lastXxxFailed` 边沿检测器实现（见 `internal/systemtime/service.go` 的 `recordProbeFailure` / `recordProbeSuccess`）。
    - **同一 action 不得混用两类归因**：`username='system'` + 空 IP 是后台事件的标志，有真实 IP 是人工操作的标志。若一个 action 同时被两者使用（历史缺陷：`time.ntp_sync` 既被后台探针又被「立即对时」按钮写入），筛选与告警都会失效 —— 必须拆成不同 action（`time.ntp_sync` 留给人工，后台用 `time.clock_step` / `time.ntp_sync_failed` / `time.ntp_sync_recovered`）。
    - **后台事件挂在返回 error 上时必须带上结果语义**：若状态机/驱动器返回 `(outcome, err)`，绝不能在 `errors.Is(err, Sentinel)` 时丢弃 outcome 直接落到成功分支 —— 那正是「15 分钟时钟偏移复核」被记成 `success` 的根因。

---

## 5. HTTP 请求错误与 AccessLog 归属

请求级意外 5xx 的唯一 `Error` 事件由 `httputil.WriteError` 记录；即使没有 cause，也包含稳定的 code/status。cause 存在时仅写服务端日志，不得写入响应。预期 4xx 即使有 cause 也不升级为 `Error`。确定错误确实返回到 `WriteError` 后，删除 service 层同一请求的重复 `logger.Error`；后台循环、生命周期和异步 worker 的错误日志仍由所属组件记录。

普通 API 的 access event 固定为 `Info`，包含 `requestId`、method、route template、status、bytes、client IP 和 `latency_ms`。不得记录原始 URL/path、query 或 body。`/events` SSE 与 WebSocket 记录 `stream.started` / `stream.closed`；成功会话使用 `session_ms`，失败握手使用 `established=false` 与 `latency_ms`。连接时长不得误写成普通请求的 latency。`Recovery` 不再单独重复记录 panic；它把安全错误、内部 panic 值和堆栈交给唯一的 `WriteError` event。

Logger 子名采用 `<domain>.<layer>` 点分隔形式，例如 `camera.stream_hub`、`person.handler`、`algorithm.supervisor`。

## 6. 轮转与持久化边界

内置轮转只支持一个进程写同一路径。写前检查 active size，超限按 `fsync` → close → rename → reopen 顺序轮转；归档名使用 UTC 时间戳与单调 sequence，保留顺序按 sequence，`max_age` 按时间戳计算。压缩最多一个 worker，启动前先按源大小加保守扩张余量预留临时输出；临时输出受限写入，预留不足就跳过压缩。预算冲突时取消压缩并拒绝当前写入，不得突破总预算。

Sink `Sync()` 仅请求操作系统同步当前活动文件；它不代表设备缓存或存储硬件已经持久化。目录项在轮转/压缩变更后同步，但平台需验证实际掉电行为，不能承诺零丢失。

## Scenario: Background Workers and the Audit Trail

### 1. Scope / Trigger
- Trigger: a background goroutine (periodic probe, watchdog, supervisor) wants to record its work in `audit_logs` via `audit.Service.RecordSystem`.
- The audit trail answers "who did what", not "is the system still healthy". Health is a metric; poll results are run logs.

### 2. Signatures
- `audit.Service.RecordSystem(action, target, detail string, opErr error)` — background events, empty IP, `username='system'`.
- `systemtime.SystemEventRecorder` — the deliberately narrow interface a service receives instead of `*audit.Service`, so a service can never record user operations by accident.
- `systemtime.(SyncOutcome, error)` from `ProcessNTPResult` — outcome rides alongside the error so a sentinel error can never be mistaken for success.

### 3. Contracts
- A background worker records **transitions and real state changes only**. Stable steady state produces zero audit rows per cycle, indefinitely.
- Steady-state poll telemetry goes to the run log at `Debug`/`Info`; the current value is exposed through the status API. Neither is duplicated into `audit_logs`.
- Transient failures are edge-debounced: one row entering the failed state, one row on recovery. `N` backoff retries produce `0` additional rows.
- When a state machine returns `(outcome, err)` and `err` carries an expected sentinel (e.g. `ErrPanicReview`), the caller must branch on `outcome`, never on `err == nil` alone. Dropping the outcome on a sentinel path is how a high-risk condition gets persisted as `success`.
- An `action` name belongs to exactly one attribution class. Interpreters that correlate by action must be able to assume `username='system'` + empty IP means the same kind of origin every time.

### 4. Acceptance
- `TestProbeSteadyStateSlewRecordsNoAudit` — N healthy cycles, 0 audit rows, N debug records.
- `TestProbeStepRecordsClockStep`, `TestProbePanicReviewRecordsFailure` — a real wall-clock step and a panic review are audited; the latter as `failed`.
- `TestProbeFailureTransitionRecordedOnce` — 3 failing cycles produce exactly 1 failure row; the recovery cycle adds exactly 1 more.
- Migration `000017` purges historical `time.ntp_sync` rows attributed to `system` and leaves human-attributed rows intact.

## Scenario: Configurable Runtime Logging and Correlation

### 1. Scope / Trigger
- Trigger: a host changes logger policy, correlates an HTTP failure, or queues preformatted native log records.
- The Go host owns policy, request/stream context and queue lifecycle. This contract does not define the C ABI callback; a future native adapter must copy C buffers into Go-owned strings before enqueue.

### 2. Signatures
- `app.NewLogger(config Config) (*zap.Logger, zap.AtomicLevel, LoggerSink, error)`; `LoggerSink` exposes `Prepare() error`, `Health() (error, uint64)`, and `Close() error`.
- `logctx.WithRequestID(ctx context.Context, id string) context.Context`, `WithTraceID`, `WithStreamID`; corresponding `RequestID`, `TraceID`, `StreamID` getters; `Fields(ctx context.Context) []zap.Field`; `Logger(ctx context.Context, base *zap.Logger) *zap.Logger`.
- `nativelog.NewQueue(capacity int, logger *zap.Logger) (*Queue, error)`; `Start() error`, `TryEnqueue(record Record) bool`, `RecordDrop()`, `Dropped() uint64`, `Stop(ctx context.Context) (pending int, err error)`.
- `nativelog.Record` contains `Level`, `StreamID uint64`, Go-owned `Module` / `Message` strings, and `SeenAt time.Time`.

### 3. Contracts
- `logging.level` is `debug|info|warn|error`; `format` is `json|console`; `output` is `stderr`, `stdout`, or a file path. File output requires enabled bounded rotation and positive `max_total_size_mb`. Sampling is opt-in and requires positive `initial` and `thereafter`.
- SIGHUP rereads only `logging.level` from the absolute `ConfigPath` selected at startup. It never changes sink or encoder; read/validation failure warns and retains the active level.
- `logctx` stores only non-empty IDs. `Fields(nil)` and an empty context return nil; `Logger` preserves the base logger name and uses `zap.L()` only for a nil base. HTTP request IDs belong to the request lifetime; later stream events use `streamId` instead.
- Queue admission is bounded and nonblocking, including mutex contention. Invalid records, contention, full capacity, and post-stop admission return false and increment `Dropped()`. The consumer never holds the admission lock during sink I/O; producers must be quiescent before `Stop`.
- The C++ `ZhulongLog` runner ring must **not** use `try_lock` for producer admission. Its ring lock is shared with the writer, which re-acquires it on every `wait_for` wakeup, so a `try_lock` producer loses that race under the scheduler and silently drops a healthy record — measured at ~2.7% of runner process starts, including the first `Error`. Block on the ring mutex instead: the critical section is bounded by a fixed-size `Record` copy because serialization and stderr writes happen after the lock is released, so producer latency stays bounded and cannot deadlock. Drop counting is reserved for genuinely full capacity and post-stop admission. `native/tests/native_log_test.cpp` guards this with 400 start-write-stop cycles.
- `WriteError` owns the unique Error event for unexpected HTTP 5xx, with requestId, stable code and status even without a cause. Expected 4xx remain non-Error; AccessLog remains Info.

### 3.1 Sensitive data: typed helpers, not a universal scrubber

The primary control is the call site: sensitive values are never passed to a log
as free text. `internal/logsafe` provides the typed helpers, and `SanitizingCore`
is defence in depth only.

| Helper | Contract |
| --- | --- |
| `Safe(s)` | Normalises UTF-8, replaces control characters, truncates at a valid boundary to 2048 bytes. **Not** a secret detector: credentials embedded in prose, base64 or concatenated strings pass through. |
| `URL(raw)` | Returns lower-cased scheme + fixed marker only; never userinfo/host/path/query/fragment. Empty, relative, oversized or malformed → fixed marker. |
| `Path(s)` / `Paths` | Never return input. State that "a path was involved" without exposing device IDs or account names. |
| `Secret(s)` | Always the fixed placeholder. |
| `IP(raw)` | Masked network summary (IPv4 /24, IPv6 /64). Must be **idempotent**: `SanitizingCore` re-applies it to the same `clientIp` field, so a non-idempotent version replaces the useful summary with the unparseable marker. |
| `ErrorSummary(err)` | Never calls `err.Error()`; reports presence only. |

`internal/logsafe` imports the standard library only and must never import
`internal/camera` or any business package.

**Known sensitive keys** handled by `SanitizingCore` (defence in depth, matched on
normalised key): `password`, `passwd`, `pwd`, `token`, `accessToken`,
`refreshToken`, `secret`, `clientSecret`, `jwtSecret`, `credential(s)`,
`authorization`, `cookie`, `setCookie`, `apiKey`, `rtspUrl`, `url`, `dsn`, `path`,
`filePath`, `socketPath`. `clientIp` uses the typed IP helper.

**Never logged**, at any level: raw exception messages, stack traces of
application errors, user prose, URL queries, filesystem/device paths, cookie or
credential values, and application-defined `ObjectMarshaler`/`ArrayMarshaler`
output (omitted with a fixed marker, never invoked).

`SanitizingCore` is always enabled and has no production disable switch. It
sanitises `Write` message and fields, `With` fields, `ErrorType` values and known
keys; `Check` attaches only itself (`ce.AddCore`) so `Write` cannot bypass it.

**Measured overhead** (x86_64 host, Go toolchain and zap as pinned in `go.mod`;
no hard latency threshold is promised — safety and correctness take precedence):

| Benchmark | Plain | Sanitizing |
| --- | --- | --- |
| discard core, 3 fields | 169 ns/op, 2 allocs | 505 ns/op, 3 allocs |
| JSON encoder, 3 fields | 614 ns/op, 2 allocs | 950 ns/op, 3 allocs |
| message only | 69 ns/op, 0 allocs | 96 ns/op, 0 allocs |
| `With` fields | — | 464 ns/op, 6 allocs |
| oversized 2048-byte field | 106 ns/op, 1 alloc | 1579 ns/op, 3 allocs |

The oversized case is the worst relative regression because it must rewrite and
re-check the whole value; it is also the case a real log line rarely hits.

⚠️ **Sampling order.** When `logging.sampling.enabled`, the sampler wraps the
sanitising core — `logger → sampler → SanitizingCore → encoder`. The reverse
order silently disables sampling: a sampler decides inside its own `Check`, which
is never consulted when the wrapper is outermost and attaches only itself.
Measured with 1000 identical records at `first=1/thereafter=100`: correct order
writes 10, inverted order writes 1000 while appearing configured. Sanitisation is
unaffected, because it happens in `Write`/`With`, which the sampler calls
through to; a sampler-dropped entry is never written anywhere, so nothing
unsanitised escapes.

### 3.2 Client log ingestion (`POST /api/v1/client-logs`)

The endpoint is **public/unauthenticated** on purpose: browser errors peak
exactly when a session has expired. Everything below is mandatory because of
that.

- Schema: `msg` is a stable event ID matching `^[a-z0-9_.-]{1,64}$` — **not
  prose**. `route` is an allowlisted category (`cameras.list`), never a URL:
  query, fragment and path segments are rejected. Fields are scalars from a
  closed allowlist, max 16 per entry, string values max 256 bytes. Optional
  top-level `dropped` is an integer in `[0, 2^32-1]`.
- Limits: 50 entries/request, 64 KiB body (413), invalid input → 422. Invalid
  input is **rejected**, never truncated-and-logged, and body content is never
  echoed in the response.
- Rate limits: per-IP 60 req/minute, burst 10, LRU-bounded key space of 1024.
  Process-wide provisional defaults: 120 req/minute, 2 MiB/minute, 8 in-flight,
  configurable under `[clientlog]`; zero value falls back and a missing section
  does **not** abort startup. ⚠️ These defaults are bounded-by-construction but
  **not deployment-signed** — they are not a capacity or bandwidth conclusion
  for any target board.
- When the per-IP key space is full, evict the least-recently-used key. Never
  reject an unknown key: that would let one source exhaust the map and lock out
  every legitimate client, turning a DoS defence into a DoS.
- Emit only: stable event IDs, allowlisted typed fields, `source=web`, server
  requestId and a **masked** client IP. Client-supplied IP/URL/arbitrary values
  are never logged. No audit record is created: an anonymous report has no
  responsible actor.
- Client-origin events must not carry a server stacktrace. Raise the logger's
  stacktrace threshold above Error for this handler; a browser report has no
  meaningful server stack, and the global policy would leak absolute build paths
  on every client error.

### 3.3 Frontend logger

`web/src/lib/logger.ts` is the only production `console.*` owner.

- Accepts four levels and a **closed** event-ID union plus allowlisted field
  keys; unknown keys are dropped at runtime, not just by TypeScript.
- Production buffers `warn`/`error` only: 100-entry ring (overflow drops oldest
  and counts), flush every 5s or at 20 entries, max 50 entries and 48 KiB
  encoded per upload, dedup `(level,event)` to 3/second.
- Uploads use `fetch(credentials: "omit", keepalive: true)`. Failure counts the
  batch as dropped and removes it; it never calls `log()`, never requeues and
  never retries immediately — otherwise a crash-looping page becomes an
  unbounded request loop against a public endpoint.
- On `visibilitychange → hidden`, at most two `sendBeacon` batches, sharing the
  single-flight gate with `fetch`. Beacon cannot set a credentials mode, so that
  path cannot promise omission; the endpoint ignores session cookies.
- Never uploaded: `Error.message`, stack traces, URL query, request/response
  bodies. Global handlers report only an event class.
- Browser crash, process kill and final-batch rejection can still lose events;
  Beacon acceptance is not server acknowledgement.

### 4. Validation & Error Matrix
| Condition | Required behavior |
| --- | --- |
| Unsupported level/format, invalid sampling thresholds, or unbounded file output | Logger construction returns an error; startup fails without fallback to a different sink or `zap.NewNop()`. |
| SIGHUP config missing or level invalid | Emit Warn; retain the last valid `AtomicLevel`; process stays alive. |
| Unexpected HTTP 5xx, with or without cause | Exactly one Error event from `WriteError`; include requestId, code and status. |
| Expected HTTP 4xx with a cause | No Error event solely because a cause exists. |
| Invalid/full/contended/post-stop queue admission | Return false immediately and increment the drop counter. |
| Queue Stop before Start | Return `ErrNotStarted`; do not panic or close the producer channel. |
| Queue drain exceeds context deadline | Return pending count and an error wrapping `ctx.Err()`. |

### 5. Good / Base / Bad Cases
- Good: update the on-disk TOML level to `debug`, send SIGHUP, then observe a debug event while PID remains unchanged.
- Base: keep defaults at `info` / `stderr` with sampling and rotation disabled; no file-budget assumptions are implied.
- Bad: select file output without a total-size budget, accept `fatal` as a runtime level, propagate requestId into a later stream lifetime, or block a native producer on sink I/O.

### 6. Tests Required
- Logger tests assert level/format allowlists, explicit sampling counts, UTC timestamps, bounded rotation and refusal after sink failure.
- SIGHUP test atomically replaces the TOML file, sends a real process signal, observes the level and emitted debug record, checks PID stability, then proves invalid/missing config retains the level.
- `logctx` tests assert ID isolation, logger-name preservation and nil fields for empty context; benchmark the no-field path.
- HTTP tests assert matching requestId on access/error events, one 5xx Error, no cause-driven 4xx Error, and SSE/WebSocket `session_ms` versus handshake `latency_ms`.
- Queue `-race` tests cover full/contended admission, exact drop counts, concurrent Stop, drain, timeout pending count and consumer failures.

### 7. Wrong vs Correct
#### Wrong
```go
cfg := zap.NewProductionConfig() // silently enables zap's default 100:100 sampler
logger, _ := cfg.Build()
```
#### Correct
```go
logger, level, sink, err := app.NewLogger(config) // explicit policy; validate err
if err != nil {
    return err
}
if err := sink.Prepare(); err != nil { // call from the host lifecycle before serving
    return err
}
logger.Info("logging initialized", zap.String("level", level.Level().String()))
```

