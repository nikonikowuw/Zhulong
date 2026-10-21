# 工业级运行日志体系 — 技术设计（父任务）

> 本文只做**跨子任务的架构决策与契约冻结**。子任务内部的实现细节见各子任务的 `design.md`。

## 1. 现状拓扑与问题定位

```txt
                       ┌──────────────────────────────────────┐
                       │  cmd/Zhulong/main.go                 │
                       │  loadConfig → app.New(config).Run()  │
                       └───────────────┬──────────────────────┘
                                       │
                       ┌───────────────▼──────────────────────┐
                       │ internal/app/logger.go               │
                       │ NewProductionConfig() / Dev          │
                       │ zap.ReplaceGlobals(logger)           │
                       └───────────────┬──────────────────────┘
                                       │ Fx DI (*zap.Logger)
   ┌───────────────────────────────────┼───────────────────────────────────┐
   │                                   │                                   │
   ▼                                   ▼                                   ▼
┌──────────────────┐   ┌───────────────────────────┐   ┌──────────────────────────┐
│ httpmiddleware   │   │ 各业务模块 (camera / task │   │ internal/engine (CGO)    │
│  Recovery        │   │ / person / record / ...)  │   │  ✗ 无日志                │
│  RequestID ──────┼──▶│  logger.Named(...)        │   └──────────┬───────────────┘
│  AccessLog       │   │  ⚠ 部分 NewNop() 兜底     │              │ C ABI
│  (含 SSE/WS)     │   └───────────────────────────┘              ▼
└────────┬─────────┘                                              ┌──────────────────┐
         │                                                        │ native/src (C++) │
         ▼                                                        │ ✗ 零日志设施     │
┌───────────────────────────────┐                                 │ ⚠ 裸 std::cerr  │
│ internal/httputil/errors.go   │                                 └──────────────────┘
│ zap.L().Error(...)            │
│ ⚠ 无 requestId                │        ┌──────────────────────────┐
└───────────────────────────────┘        │ internal/algorunner      │
                                         │ runner stdout/stderr ────┼─▶ zap.String("text")
                                         │ ⚠ 未解析 JSON / 无限流   │
                                         └──────────────────────────┘
```

**关键断点**（编号对应 `prd.md` 的 Requirements）：

| # | 断点 | 归属子任务 |
| --- | --- | --- |
| ① | `AccessLog` 有 `requestId`，`WriteError` 没有 → 证据链断开 | A |
| ② | service 层 `Error` + `WriteError` → 双重记录 | A |
| ③ | `NewProductionConfig` 隐式开启采样 | A |
| ④ | SSE/WS 走 `AccessLog` → latency 污染 | A |
| ⑤ | CGO 边界后 C++ 无日志通道 | B |
| ⑥ | 裸 `std::cerr` 破坏 JSON 行 | B |
| ⑦ | runner 结构化输出被当纯文本 | B |
| ⑧ | 无统一脱敏 / 无日志注入防护 | C |
| ⑨ | 前端生产环境静默丢弃错误 | C |

## 2. 目标架构

```txt
┌────────────────────────────────────────────────────────────────────────────┐
│ 配置层  config.toml                                                        │
│   [logging] level / format / output / [logging.sampling] / [logging.rotation]│
└───────────────────────────────┬────────────────────────────────────────────┘
                                │ loadConfigFile → app.Config.Logging
                                ▼
┌────────────────────────────────────────────────────────────────────────────┐
│ internal/app/logger.go                                                     │
│   zapcore.NewCore(encoder, sink, AtomicLevel)                              │
│   + optional Sampler  + separate nativelog.Queue                          │
│   → (*zap.Logger, zap.AtomicLevel, error)                                  │
└───────────────────────────────┬────────────────────────────────────────────┘
                                │ Fx DI
        ┌───────────────────────┼────────────────────────┬───────────────────────┐
        ▼                       ▼                        ▼                       ▼
┌──────────────────┐  ┌────────────────────┐  ┌──────────────────┐  ┌────────────────────┐
│ internal/logctx │  │ 业务模块           │  │ internal/engine  │  │ SIGHUP handler     │
│  WithRequestID   │  │  logctx.Logger(    │  │  SetNativeLogSink│  │  AtomicLevel       │
│  RequestID       │◀─┤    ctx, s.logger)  │  │  ┌───────────────┴──┐  .SetLevel  │
│  Fields / Logger │  │  → 保留 Named 前缀 │  │  │ cgo.Handle token │  └────────────────────┘
└──────────────────┘  └────────────────────┘  │  └───────┬──────────┘
        ▲                                     └──────────┼──────────┘
        │ logctx.WithRequestID(ctx, id)                  │ Zhulong_log_callback
┌───────┴──────────────────┐                             ▼
│ httpmiddleware.RequestID │                  ┌────────────────────────────┐
└──────────────────────────┘                  │ native/src/common/log.hpp  │
                                              │  ZL_LOGD/I/W/E             │
┌──────────────────────────┐                  │  → bounded queue        │
│ httputil.WriteError      │                  │  → writer goroutine        │
│  logctx.Logger(ctx, ...) │                  └────────────────────────────┘
│  → 带 requestId          │
└──────────────────────────┘
```

## 3. 冻结的跨子任务契约

以下三条契约在子任务之间共享，**先冻结再实施**，任何一方不得单方面变更。

### 3.1 契约 C-1：`internal/logctx` API（子任务 A 交付，B/C 消费）

| API | Contract |
| --- | --- |
| `WithRequestID`, `RequestID` | Carry/read request ID through standard context; absent ID is empty. |
| `WithTraceID` | Reserve a trace ID field; this task does not consume it. |
| `WithStreamID`, `StreamID` | Associate/read a long-lived stream ID; absent ID is zero. |
| `Fields` | Return request/trace/stream fields; return nil when none exist. |
| `Logger` | Add context fields while preserving the base logger's `Named` prefix; nil base uses `zap.L()`. |

**Constraints**: depend only on `context` and zap; no Gin, `httputil` or `httpmiddleware` dependency. The no-fields path must remain allocation-free and be benchmarked.

### 3.1.1 版本核实结论（zap v1.28.0，已对照模块缓存源码）

契约 C-1 / C-3 的实现依赖以下 zap 事实，**已核实**（详见子任务 A 的 `design.md` §0）：

| 事实 | 结论 | 对契约的影响 |
| --- | --- | --- |
| `AtomicLevel` 所在包 | **`go.uber.org/zap`**，不在 `zapcore` | SIGHUP 调级写 `zap.AtomicLevel` |
| `zapcore.Field.Clone()` | **v1.28.0 中不存在** | 直接导致契约 C-3 修订（见 §3.3） |
| `zapcore.Level` 数值 | `Debug=-1, Info=0, Warn=1, Error=2` | 契约 C-2 必须显式映射，不能依赖数值巧合 |
| `NewProductionConfig()` 采样 | 默认 `100:100` | 现状「日志被静默丢弃」的根因 |
| `Encoder.EncodeEntry` | 并发安全；返回 `*buffer.Buffer`，`Free()` 后切片失效 | 异步方案若要保留必须拷贝 |

### 3.2 契约 C-2：C ABI 日志回调（子任务 B 交付）

| Element | Contract |
| --- | --- |
| C log levels | `DEBUG=0`, `INFO=1`, `WARN=2`, `ERROR=3`; Go maps explicitly to zap levels. |
| Callback payload | Integer token, level, `stream_id`, module/text byte pointers and explicit lengths. |
| Buffer lifetime | Native worker invokes synchronously; module/text are UTF-8 and valid only until callback return. Copy immediately if retained. |
| Stream association | `stream_id=0` means no single stream; call sites with stream context pass it explicitly. |
| Callback restrictions | Validate/copy bounded buffers and attempt nonblocking queue admission; no sink I/O and no engine control APIs from callback. |
| Registration lifetime | Register before the first engine create. Keep callback/token valid through producer shutdown and `engine_destroy`; unregister only after all producers quiesce. Never mutate callback/token while producers are active. |
| Oversize input | Reject/drop and count oversized C callback fields; the standalone runner writer may truncate only if it preserves valid JSON, otherwise drop and count. |

The exact C declaration is owned by subtask B's ABI design. This parent contract freezes semantics and ownership, not a second copy of the header.

> **修订记录（规划期）**：原定签名不带长度（`const char *module, const char *text`）。
> 规划期发现 Go 侧必须用 `C.GoString`，其内部先 `strlen` 再分配 —— 相当于在
> **实时链路线程**上做一次无谓的 O(n) 扫描。传递长度可消除它，且让契约更显式。
> Go 侧改用 `C.GoStringN(ptr, len)`，跳过 `strlen`。详见子任务 B 的 `design.md` §2。

**约束**：

- ⚠️ C levels `0..3` differ from zap levels `-1..2`; Go must map explicitly and test all four values plus an invalid value. The independent C enum avoids coupling the ABI to zap internals.
- 回调携带 `stream_id`；`0` 表示事件不属于单个 stream。C++ 日志调用点有 stream 上下文时显式传入，不依赖可能跨线程复用错误的隐式 thread-local 状态。
- 注册必须在第一个 `Zhulong_engine_create` 前；保持 callback/token 有效到常驻引擎、probe producer 停止且 `engine_destroy` 返回。sink/token 只允许在无 producer 时修改，且不得与回调并发读写。
- 回调长度受硬上限约束；超过上限的消息截断为仍然有效的 UTF-8/JSON 或丢弃并计数。
- `module` / `text` 仅在回调期间有效，调用方立即复制；C++ 不得保留 Go 指针。
- Go `TryEnqueue` 只承诺有界、非阻塞；`C.GoStringN` 的分配属于回调成本，必须测量，不能把整条 CGO 路径称为零分配。

### 3.3 契约 C-3：原生日志队列（子任务 A 交付，B 消费）

The native bridge uses a dedicated bounded `nativelog.Queue`, not a generic zap `AsyncCore`: the C ABI carries only fixed fields and zap v1.28.0 has no `Field.Clone()` for safely retaining arbitrary fields.

| Contract | Requirement |
| --- | --- |
| Record shape | Level, stream ID, module, text and callback observation time. |
| Producer | Bounded queue admission; no synchronous sink I/O. Contention/full may drop and must be counted. |
| Consumer | Explicit start, structured output and periodic aggregate drop reporting. |
| Shutdown | After native producers stop, destroy returns, callback is unregistered and handle deleted, drain/stop the queue. Stop and join runner readers before the final logger sync. |
| Loss | Drops are observable at runtime and on graceful shutdown; no absolute zero-loss or power-failure persistence promise. |

**Why asynchronous**: the callback runs on capture/decode workers. A synchronous file write can inject storage latency into the real-time stream. The queue isolates that I/O while preserving bounded memory and visible loss.

**丢弃策略**（按 level 分级，写进实现）：

| level | 队列满时行为 |
| --- | --- |
| Debug / Info | 直接丢弃，计入 `Dropped()` |
| Warn / Error | 丢弃并**立即**计入，由消费者 goroutine 在下次补齐时以 `logger.Warn("native log dropped", zap.Uint64("count", n))` 上报 |

> ⚠️ 丢弃计数必须补齐上报。否则排障时会陷入「日志很干净 → 系统没问题」的假象，
> 而实际是日志被丢了 —— 这比没有日志更危险。

**停机顺序约束**：

1. 停止 HTTP 与业务侧新 producer；停止 runner 子进程并 join stdout/stderr readers，避免其在 logger Sync 后继续写入。
2. 调用 `native.Stop()` 停止常驻引擎、probe 和 worker；callback、`cgo.Handle`、队列消费者仍保持有效。
3. 调用 `native.Close()` 完成 `engine_destroy`；销毁期间若产生日志，仍由有效 callback 入队。
4. 确认 native producer 已停止后注销 C callback，再删除 `cgo.Handle`。
5. `nativelog.Queue.Stop(ctx)` 排空并报告未排空数量；所有 Go/native/runner 日志 producer 均停止后，执行 `logger.Sync()`。

若停止/销毁无法证明 producer 已 quiesce，禁止释放 handle 或队列；错误路径宁可保留资源到进程退出，也不能让回调访问已释放对象。

## 4. 子任务边界与依赖

```txt
  ┌──────────────────────────┐
  │ A. logging-core-hardening│  P0 · 无前置依赖 · 必须最先完成
  │  - 配置化 logger          │
  │  - logctx (C-1)           │
  │  - 去重 / AccessLog / 命名│
  │  - nativelog.Queue (C-3)  │
  └────────────┬─────────────┘
               │ 交付 C-1 + C-3
               ▼
  ┌──────────────────────────┐        ┌────────────────────────────────┐
  │ B. native-log-bridge     │        │ C. logging-safety-and-frontend │
  │  P0 · 依赖 A 的 C-1/C-3   │        │  P1 · 依赖 A/B，依序收口       │
  │  - C ABI 回调 (C-2)       │        │  - 统一脱敏器                   │
  │  - C++ ZL_LOG* 宏         │        │  - 日志注入防护                 │
  │  - std::cerr 清理         │        │  - 前端统一 logger + 上报       │
  │  - runner JSON 解析       │        │                                │
  └──────────────────────────┘        └────────────────────────────────┘
```

**依赖不是树形位置，而是写在子任务产物里的硬顺序**：

- B 必须等 A 的 `logctx`（C-1）与 `nativelog.Queue`（C-3）落地后再开工。
- C 在 B 之后执行：C 会收敛 runner 的日志调用点，避免 B/C 并发修改 `internal/algorunner/process.go`。B 在 C 之前不得输出 broker socket 路径或未脱敏错误文本；需要诊断时只记录稳定 code/stage/rc。
- 因此本任务固定执行顺序为 A → B → C。若未来要并行 C 的前端部分，应先把 logsafe 与前端上报拆成独立任务。

## 5. 关键设计决策与取舍

| 决策 | 选项 | 选定 | 理由 |
| --- | --- | --- | --- |
| 生产环境是否开采样 | 开（zap 默认） / 关 | **关** | 关闭采样仍可能在队列溢出、输出失败或掉电时丢日志；以有界资源和可见丢失计数为目标 |
| 轮转归属 | journald / 内置轮转 | **按部署 profile 选择** | systemd 环境可由 journald 托管；其他环境必须启用文件轮转。文件预算覆盖活动文件、归档和临时文件；目标设备、空间预算与保留期确认前不选默认部署方案 |
| 时间戳时区 | 本地 / UTC | **UTC** | 设备可能跨时区或被 NTP 校时；持续时间以 `_ms` 字段显式标单位 |
| 去重层级 | service 记 / HTTP 边界记 | **HTTP 错误边界记一次 Error** | access event 不记 Error；后台循环错误仍由所属组件记 |
| 原生日志通道 | 独立进程 / 共享内存 / C ABI 回调 | **C ABI 回调 + 有界非阻塞队列** | 同进程内延迟低；队列允许有计数的丢弃，不宣称整条 CGO 路径零分配 |
| 原生日志是否走 zapcore | 通用 `AsyncCore` / 固定 Record 队列 | **固定 Record 队列** | 任意 zap field 无法通用安全深拷贝；固定字段可验证、可限长 |
| `traceId` | 引入 OTel SDK / 预留字段 | **只预留字段** | 避免为一个字段引入整套依赖树；实际 request/stream 关联用独立字段 |
| 前端错误上报 | 直连 / 经后端落盘 | **经后端落盘** | 边缘设备通常无外网；必须配置全局上报预算和后端速率上限 |

## 6. 回滚形态

| 变更 | 回滚方式 | 影响面 |
| --- | --- | --- |
| `[logging]` 配置扩展 | 删除新增键，回到 `development` 单键 | 无 —— 新键全部有零值默认 |
| `logctx` 引入 | 中间件与 `WriteError` 去掉 `logctx` 调用 | 无 —— 纯附加字段 |
| `nativelog.Queue` 引入 | 不注册回调 / 不调用 `TryEnqueue` | 无 —— 本任务内尚无消费方，天然隔离 |
| 删 service 层重复日志 | 恢复被删的 `logger.Error` 调用 | ⚠️ 需保留 `apperr.Err` 非空保证，否则日志会丢 |
| C ABI 新增接口 | 不注册回调即退化为「无 native 日志」 | 无 —— 纯增量，既有符号不变 |
| C++ `std::cerr` → `ZL_LOG*` | 回退宏定义即可 | 无 |
| 前端统一 logger | 回退为 `console.*` | 无 |

**唯一有实质风险的回滚点是「删 service 层重复日志」**：回滚时必须同步恢复 `apperr.Err` 的传递，
否则会从「重复记录」退化为「完全不记录」。这一点必须在子任务 A 的 `implement.md` 中标注为回滚检查项。

## 7. 集成验收方法

> ⚠️ **范围外硬约束（勘察发现）**：`native/src/nodes/capture/rtsp_input.cpp:271` 的
> `initialize_ffmpeg()` 刻意设置 `av_log_set_level(AV_LOG_QUIET)` + 空回调，
> 目的是**防止 FFmpeg 把含凭据的完整 RTSP URL 打入日志**。
> 因此：本任务**严禁**重新启用 FFmpeg 日志，也**严禁**安装 `av_log_set_callback` 转发。
> 恢复 FFmpeg 诊断需要配套的 URL 脱敏回调，属于独立决策（见子任务 B 的 `prd.md` 排除项）。
> AC-10 的静态门禁必须把这一条也纳入。

- **AC-1（HTTP 关联）** 由 A 的 `logctx` 验证：access event 与唯一 error event 带同一 `requestId`。
- **AC-7（流关联）** 由 A/B 联合验证：Go 与 C++ stream 事件带相同 `streamId`；HTTP `requestId` 不跨生命周期继承。
- **AC-3（结构化纯净）** 需要 A 的 JSON encoder + B 的 runner/native 输出路径共同验证；bootstrap 诊断通道单独验收。
- **AC-12（无凭据泄漏）** 需要 C 的源头脱敏覆盖 A/B 日志点，并对有效/畸形 URL、错误字符串和前端 payload 做运行时测试。
- **AC-13（有界丢失可见）** 压满 native/runner 队列并注入 sink 写入错误；producer 不做同步 I/O，丢弃/错误计数可观察。
- **AC-14（未认证上报有界）** 验证 per-IP 与进程级请求/字节预算，最终阈值由部署 profile 签字确认。
