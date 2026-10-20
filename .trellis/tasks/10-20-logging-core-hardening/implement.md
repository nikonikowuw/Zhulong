# 执行计划：Go 日志核心加固

> 父任务：`.trellis/tasks/10-20-industrial-logging` · 前置依赖：无（本子任务是 B 的前置）

## 0. 前置阅读

1. `.trellis/spec/backend/logging-guidelines.md` —— 既有 zap 规范与审计日志边界
2. `.trellis/spec/backend/middleware-guidelines.md` —— 中间件顺序约束（`Recovery` 必须最外层）
3. `.trellis/spec/backend/error-handling.md` —— `apperr` / `httputil` 契约
4. `.trellis/spec/backend/dependency-injection.md` —— Fx 装配规范
5. `design.md`（本任务）§0 —— zap v1.28.0 的 API 核实结论，**动手前必读**

## 1. 开工前的环境核实（先做，不写代码）

```bash
# 确认 zap 版本与模块缓存路径
grep 'go.uber.org/zap' go.mod
go env GOMODCACHE

# 复核 design.md §0 的关键结论（防止版本升级后失效）
Z=$(go env GOMODCACHE)/go.uber.org/zap@v1.28.0
grep -n 'Sampling is enabled at 100:100' -B2 $Z/config.go
grep -n 'func (f Field) Clone' $Z/zapcore/field.go   # 期望：无输出（v1.28.0 不存在）
grep -n 'DebugLevel Level = iota - 1' $Z/zapcore/level.go
grep -n 'type AtomicLevel struct' $Z/level.go        # 注意是 zap 包，不是 zapcore
```

若结论与 `design.md` §0 不符 → **先更新 design.md，再继续**。

```bash
# 确认仓库内没有解析日志的脚本（UTC 化的影响面）
grep -rn "\.log" scripts/ Makefile .air.toml 2>/dev/null
```

## 2. 实施顺序

### 阶段一：配置层（不改行为，先立骨架）

- [x] **1.1 扩展 `app.Config` 与 `LoggingConfig`**
  - `internal/app/config.go`：新增 `LoggingConfig` / `SamplingConfig` / `RotationConfig`，`Config` 增加 `Logging` 与启动时实际读取的 `ConfigPath`。
  - `DefaultConfig()` 补默认值：`Level: "info"`、`Output: "stderr"`、`Sampling.Enabled: false`、`Rotation.Enabled: false`；文件输出必须显式给出正数 `Rotation.MaxTotalSizeMB`。
  - 验证：`python3 native/scripts/build.py go test ./internal/app/ -run TestConfig`

- [x] **1.2 配置解析接线**
  - `cmd/Zhulong/config.go`：`fileConfig.Logging` 改为内嵌 `app.LoggingConfig`；
    `loadConfigFile` 同步 `Development`，并补齐 `settings.SetDefault(...)`。
  - `config.toml` 与 `config.toml.example` 同步扩展（viper `UnmarshalExact` 严格模式，缺一不可）。
  - 验证：`python3 native/scripts/build.py go test ./cmd/... -run TestLoadConfig`
  - **回归点**：仅含 `[logging] development=false` 的旧配置必须仍可启动（AC-A2）。

- [x] **1.3 配置白名单校验**
  - `newLogger` 返回 `(*zap.Logger, zap.AtomicLevel, error)`。
  - `level` 仅允许 `debug|info|warn|error`；`dpanic|panic|fatal` 与拼错值均启动失败。文件输出还需校验轮转启用、总容量为正数且不小于活动文件上限。
  - `format` 仅允许 `json|console`；采样启用时 `initial` / `thereafter` 必须为正数。
  - 验证：`go test` 覆盖所有非法值和合法边界。
  - 验证：`python3 native/scripts/build.py go test ./internal/app/ -run TestNewLogger`

### 阶段二：logger 构造与 sink

- [x] **2.1 重写 `newLogger`**
  - 按 `design.md` §2 实现：`zap.NewAtomicLevel()` + 显式 encoder config + 显式 core 组装。
  - **不使用 `zap.NewProductionConfig()`**（其采样默认 100:100）。
  - `zap.ErrorOutput(zapcore.Lock(os.Stderr))` 隔离 zap 内部错误。
  - 时间戳 UTC ISO8601；duration 毫秒。
  - 验证：`python3 native/scripts/build.py go test ./internal/app/ -run TestNewLogger`

- [x] **2.2 采样开关**
  - `sampling.enabled=true` 时才包 `zapcore.NewSamplerWithOptions`。
  - 补测试：开启采样后突发 1000 条同级别日志，落盘 < 1000；
    关闭采样后突发 1000 条，落盘 = 1000（AC-A5）。
  - 验证：`python3 native/scripts/build.py go test ./internal/app/ -run TestSampling`

- [x] **2.3 sink 抽象**
  - `openLogSink`：`stderr` / `stdout` / 文件路径三分支。
  - 文件路径且 `rotation.enabled=false` → 启动配置错误；禁止无界 `os.OpenFile` 追加。
  - 测试 sink 写入/轮转故障不会退化为无限追加，并能从独立诊断路径观察故障；容量测试须将压缩中的源文件和按压缩器最坏输出上界预留的临时目标一并计入总预算，预算不足时验证跳过压缩。
  - 验证：`python3 native/scripts/build.py go test ./internal/app/ -run TestOpenLogSink`

- [ ] **2.4 部署后端与轮转 profile**（有界轮转机制已实现；部署 profile / journald 宿主预算仍待平台 sign-off，未虚构配额。）
  - 先确认支持的部署 profile 与磁盘预算；systemd profile 可用 journald，但必须记录 host 总预算和 Zhulong 可用份额。
  - 文件后端必须实现有限总量的轮转；轮转失败不允许继续无限追加。
  - `Sync()` 的持久化能力与掉电丢失窗口按目标文件系统/宿主声明，不承诺绝对零丢失。
  - 验证：`python3 native/scripts/build.py go test ./internal/app/ -run TestRotatingSink`

### 阶段三：关联性（契约 C-1）

- [x] **3.1 新建 `internal/logctx`**
  - 按 `design.md` §5 实现 requestId、traceId、streamId 字段。
  - import 白名单只允许 `context` 与 `go.uber.org/zap`。
  - 验证：`python3 native/scripts/build.py go test -race ./internal/logctx/`

- [x] **3.2 基准测试（AC-A12）**
  - `BenchmarkLoggerNoFields`：期望 `0 allocs/op`。
  - `BenchmarkLoggerWithRequestID`：记录基线，作为后续回归参照。
  - 验证：`python3 native/scripts/build.py go test ./internal/logctx/ -bench . -benchmem`

- [x] **3.3 中间件接线**
  - `internal/httpmiddleware/http.go`：`RequestID` 改用 `logctx.WithRequestID`；
    删除私有 `requestIDContextKey`；`RequestIDFromContext` 改为 `logctx.RequestID` 的薄封装。
  - 验证：`python3 native/scripts/build.py go test ./internal/httpmiddleware/ -race`

- [x] **3.4 `httputil.WriteError` 带上 requestId 并单点记录意外 5xx**
  - 用 `logctx.Logger(c.Request.Context(), zap.L())` 记录 requestId。
  - 对 5xx 无论 cause 是否为空都记一条 Error（稳定 code/status）；预期 4xx 不因存在 cause 自动记 Error。
  - 先测有 cause 5xx、无 cause 5xx、带 cause 4xx，再删 service 重复日志。
  - 验证：`python3 native/scripts/build.py go test ./internal/httputil/ -run TestWriteError`

### 阶段四：噪声治理（⚠️ 风险最高）

- [x] **4.1 逐点核对 `apperr.Err` 非空**
  - `grep -rn "logger.Error" internal/*/service.go`
  - 对每一处，向上追溯对应的 `apperr.Internal(...)` / `apperr.NotFound(...)` 调用，确认 `Err` 字段非空。
  - **产出核对清单**（写进本文件下方「核对记录」或 PR 描述）。
  - ⚠️ 任一 `Err` 为空 → 该处 **不删** service 日志，并在代码注释中标注原因。

- [x] **4.2 删除请求路径内的重复日志**
  - 对每处核实该失败确实返回到 `httputil.WriteError`；只删除由同一边界重复记录的请求错误。
  - 后台循环、独立 goroutine 与不经过 HTTP 错误边界的失败必须保留本地 Error。
  - 每删一处跑该模块测试。
  - 验证：`python3 native/scripts/build.py go test -race ./internal/record/...`

- [x] **4.3 AccessLog 长连接分流（AC-A8）**
  - 在 `c.Next()` 前按 `c.FullPath()` 与 Upgrade 请求头识别 `/api/v1/cameras/events` 和 WebSocket 路由；SSE 的响应 Content-Type 不能作为 start 事件的前置判据。
  - access 事件固定为 Info；意外 5xx Error 由 `WriteError` 单点记录。
  - start/completed 均记录；普通请求耗时字段为 `latency_ms`；只有 WebSocket 101 或 SSE 响应已建立时记录 `session_ms`，失败握手记录 `latency_ms`。
  - 验证真实 Gin 路由下的 SSE 建立、断开与 WebSocket 握手失败。

- [x] **4.4 命名统一（AC-A10）**
  - 按 `design.md` §8.1 的对照表逐项修改。
  - 验证：`grep -rn 'Named("' internal | grep -v _test` → 全部为 `<domain>.<layer>` 形式

### 阶段五：生产装配验证与去重

- [x] **5.1 移除生产 logger 静默降级（AC-A9）**
  - Fx 必需 logger/provider 缺失时返回启动错误；组件构造函数不得 panic。
  - 生产构造路径不使用隐式 nil → Nop；测试需要静音时显式传 `zap.NewNop()`。
  - 验证：Fx wiring 测试覆盖缺失 logger，`go test -race` 覆盖受影响组件。

- [x] **5.2 `main.go` 去重**
  - `-reset-network` 分支复用 `newLogger`，删除重复的 `zap.NewProduction()/NewDevelopment()`。
  - 配置加载失败改 `fmt.Fprintf(os.Stderr, ...) + os.Exit(1)`，不用 stdlib `log.Fatal`。
  - 验证：`python3 native/scripts/build.py go test ./cmd/...`

### 阶段六：运行期调级（AC-A6）

- [x] **6.1 SIGHUP handler**
  - 按 `design.md` §4 实现 `registerLogLevelSignal`，使用 `Config.ConfigPath` 在每次信号时重读 `logging.level`。
  - 手动白名单只接受 `debug|info|warn|error`；不能直接把 `AtomicLevel.UnmarshalText` 当作白名单。
  - OnStop 停止 signal 通知并等待 handler goroutine 退出。
  - 验证：单测覆盖真实文件更新、有效级别、非法级别保持旧值、读取失败保持旧值。

- [ ] **6.2 手工端到端验证**（定向测试已在当前进程修改磁盘配置、发送真实 `SIGHUP`、确认 PID 不变，并断言新启用的 `debug` 事件实际输出；完整 Zhulong live service 未启动。其启动 `OnBootCheck` 会应用持久化时区，system-time NTP worker 随后可能校准系统时钟，因此完整 live 验收应在隔离主机或时间 namespace 中进行。）
  ```bash
  make build && ./build/Zhulong &
  PID=$(pidof Zhulong)
  # 修改实际使用的 config.toml: logging.level = "debug"，再发信号
  kill -HUP "$PID"
  sleep 1
  [ "$(pidof Zhulong)" = "$PID" ] && echo "PID unchanged OK"
  ```
  必须观察新产生的 debug 记录；只验证 PID 不变不算通过。

### 阶段七：契约 C-3 交付（`internal/nativelog`）

- [x] **7.1 实现 `nativelog.Queue`**
  - API 包含 `TryEnqueue`、`RecordDrop()`（记录入队前的无效输入）、`Dropped()`、消费者 goroutine 和 Stop 超时语义。
  - 不要求 lock-free，但 `TryEnqueue` 不得等待互斥锁；可使用 `TryLock` 或等价非阻塞 admission，竞争/满载均立即拒绝并计数。消费者不得持 admission 锁执行 sink I/O。Stop 后入队安全拒绝、不 panic；不得关闭仍可能被 producer 写入的 channel。
  - 验证：`python3 native/scripts/build.py go test -race ./internal/nativelog/`

- [x] **7.2 补测试（AC-A11）**
  - 多 producer 并发、队列饱和、锁竞争时立即丢弃并准确计数、`RecordDrop()` 计数准确性和 `-race`。
  - 与 Stop 并发时 API 不 panic；producer quiesced 后 Stop 排空，ctx 超时返回并报告 pending 数。
  - 消费者输出 native dropped 计数；sink 出错时计数/健康状态可观察。

- [x] **7.3 停机顺序契约与单测**
  - A 只实现队列 Stop 的前置条件，不在本任务接入 native lifecycle。
  - 文档/队列测试要求：native Stop + Close 完成、回调注销并删除 handle 后，才停止队列；最后 Sync logger。
  - 子任务 B 负责 `runtime.go` 生命周期接线与 `TestRuntimeShutdownOrder`。

### 阶段八：收尾

- [x] **8.1 更新 `logging-guidelines.md`**
  - 记录：`NewProductionConfig` 采样默认 100:100 的事实与本项目选择
  - 记录：双重日志判定规则（`design.md` §7.1）
  - 记录：UTC 时间戳约定
  - 记录：`logctx` 用法与依赖白名单
  - 记录：`nativelog` 的存在与「日志不能成为实时链路延迟来源」的约束

- [x] **8.2 全量门禁**（在授权后以离线 pnpm wrapper 执行字面 `make check` 并全绿：浏览器 86 个文件 / 502 tests、Native CTest 21/21、Go race/vet/bridge 与 API docs 均通过。`frontend-install` 的 frozen-lockfile 离线安装通过，未下载依赖。全仓 `format:check` 仍有 35 个未改文件不符合 Prettier；本轮改动文件已单独检查通过。详见 §4.6。）
  ```bash
  make check
  ```
  含前端 lint/type-check/test + native-test + api-docs + go-check。

## 3. 核对记录（实施时填写）

### 3.1 `apperr.Err` 非空核对清单（阶段 4.1 产出）

| 文件:行 | 上游错误 | `Err` 非空？ | 处置 |
| --- | --- | --- | --- |
| `internal/record/service.go:357` | `apperr.Internal(..., err)`，`ListRecognitions` store error | 是（store error） | 移除重复服务层 Error；handler 返回到 `WriteError` |
| `internal/record/service.go:370` | `apperr.Internal(..., err)`，`GetRecognition` store error | 是（store error） | 同上 |
| `internal/record/service.go:383` | `apperr.Internal(..., err)`，`ListAlarms` store error | 是（store error） | 同上 |
| `internal/record/service.go:396` | `apperr.Internal(..., err)`，`GetAlarm` store error | 是（store error） | 同上 |
| `internal/record/service.go:409` | `apperr.Internal(..., err)`，`ListCaptures` store error | 是（store error） | 同上 |
| `internal/record/service.go:422` | `apperr.Internal(..., err)`，`GetCapture` store error | 是（store error） | 同上 |
| `internal/record/service.go:435` | `apperr.Internal(..., err)`，`ListAlarmTypes` store error | 是（store error） | 同上 |
| `internal/record/service.go:444` | `apperr.Internal(..., err)`，`ListTargetTypes` store error | 是（store error） | 同上 |
| `internal/camera/stream_hub.go:199` | decrypt error wrapped by `fmt.Errorf`; `StreamWS` maps it to `apperr.KindInternal` with the error as cause | 是 | 移除同步 service Error；`WriteError` 记录唯一 5xx Error |

预期 not-found（如 `GetRecognition` 结果为 nil）不属于内部故障路径，无 cause 且没有被移除的 Error 日志。保留的 Error 均为后台/生命周期/异步边界：`record` batch writers、camera credential decrypt（该服务继续返回成功响应）、camera scheduler health check、network watchdog/apply rollback、algorithm supervisor/process、storage gate、system-time healing、database migration、audit batch writer、native log consumer 与 HTTP server lifecycle；这些不经由本次请求的 `WriteError`。


### 3.2 决策记录

| 决策点 | 结论 | 日期 |
| --- | --- | --- |
| 是否实现内置轮转（阶段 2.4） | 是。自研有界 file sink，预算包含 active、archives 与 gzip 临时预留；平台部署 profile / journald 配额仍待 sign-off | 2026-10-20 |
| WebSocket 升级后 Content-Type 是否可取（阶段 4.3） | 不依赖 Content-Type；经 `Hijack` 与 101 状态判断成功升级 | 2026-10-20 |

## 4. 验证命令速查

```bash
# 相关包与 HTTP 服务 race tests
python3 native/scripts/build.py go test -race ./internal/app ./internal/record ./internal/camera ./internal/httpmiddleware ./internal/httputil ./internal/logctx ./internal/nativelog ./cmd/Zhulong

# 无字段 logctx benchmark
go test ./internal/logctx -run '^$' -bench 'BenchmarkLoggerNoFields|BenchmarkLoggerWithRequestID' -benchmem

# 全量后端 race / vet gate
make go-check

# Native CTest
make native-test

# 完整门禁（应补齐 API docs 生成）：
GOPROXY=off make check PNPM='pnpm --offline'
```

### 4.1 实施阶段验证记录（此前运行）

- 相关包 race 测试通过；`BenchmarkLoggerNoFields` 为 0 B/op、0 allocs/op，带 requestId 为 192 B/op、2 allocs/op。
- `make native-test`：21/21 CTest 通过。
- `make go-check`：gofmt 与 `go vet` 通过；全量 race 测试除 `internal/storage` 三个 scope 外失败外均通过：两项 user-audit test 期望 1 条却观察到 3 条，`TestService_StartStopAndStatus` 观察到 `CanWrite=false`。bridge tests 因 Go test 阶段失败而未运行。三项已用非 race 的定向命令复现；未修改 storage/audit。
- `GOPROXY=off make check PNPM='pnpm --offline'`：frontend lint 通过；Chromium 测试 28 个文件/155 tests 通过，另 2 个 suite 因动态 import / iframe localhost CORS 连接错误失败，目标提前退出。
- `GOPROXY=off make api-docs`：当时失败于 `go run ...@v1.16.6` 的模块弃用信息查询；后续确认精确版本的 `.info` / `.mod` / `.zip` 和源码均已缓存，并用本地 file proxy 成功生成，详见 §4.4。未联网下载依赖。
### 4.2 本轮 Check Agent 复核记录（2026-10-20）

- `python3 native/scripts/build.py go test -race ./internal/app ./internal/record ./internal/camera ./internal/httpmiddleware ./internal/httputil ./internal/logctx ./internal/nativelog ./cmd/Zhulong`：通过，覆盖采样开关、sink、配置、SIGHUP 模拟、请求/流日志与队列测试。
- `go test ./internal/logctx -run '^$' -bench 'BenchmarkLoggerNoFields|BenchmarkLoggerWithRequestID' -benchmem`：通过；无字段路径 `0 B/op, 0 allocs/op`，requestId 路径 `192 B/op, 2 allocs/op`。
- `GOPROXY=off make check PNPM='pnpm --offline'`：失败并在 frontend 阶段提前停止。lint 通过；Chromium 有 27 个 test files / 155 tests 通过，2 个 suite 因动态导入失败、iframe localhost CORS 连接错误未通过，另有 1 个 unhandled browser error。后端目标未由此命令执行。
- `GOPROXY=off make go-check`：gofmt、`go vet` 通过；全量 Go race tests 中除 `internal/storage` 外均通过。失败仍为 `TestHandler_UserAuditUsesAuthenticatedActor` 与 `TestHandler_UserAuditWithoutActorIsAnonymous`（各观察到 3 条审计记录，预期 1 条），以及 `TestService_StartStopAndStatus`（初始 `CanWrite=false`）。未改动 storage/audit。
- `GOPROXY=off make native-test`：21/21 CTest 通过。
- `GOPROXY=off make api-docs`：该命令在禁用 proxy 时因模块弃用信息查询失败；后续用本机 cache file proxy 成功，见 §4.4。未联网下载依赖。
- `python3 native/scripts/build.py go test -race ./internal/app -run '^TestLogLevelReloaderReadsUpdatedConfigOnSIGHUP$'`：通过；测试在当前进程实际发 SIGHUP、验证 PID 不变，并断言调级后的 debug 事件可见。
- 本轮没有发现需要修改的任务范围代码缺陷。未做完整服务 live SIGHUP/PID/debug-output 手工验证，也没有部署 profile/journald 预算 sign-off；AC-A6 的 full-service 手工验证、AC-A14 与部署 profile 项保持未通过/未签署。完整服务启动可能应用持久化时区并由 NTP worker 校准时钟，需隔离运行环境。

### 4.3 Repeatable Phase 2.2 delta check

- Reviewed the process-level SIGHUP test, the executable logging contract appendix, and this implementation record. The test atomically replaces the TOML config, sends a real SIGHUP to the current process, observes the emitted debug event, checks PID stability, and uses an exact Warn-level filter for invalid/missing config warnings. No spec inconsistency or task-scope defect found.
- `python3 native/scripts/build.py go test -race ./internal/app -run '^TestLogLevelReloaderReadsUpdatedConfigOnSIGHUP$' -count=1`: passed (uncached).
- `git diff --check`: passed.
- This delta check does not complete AC-A6 full-service live verification, deployment profile/journald sign-off, or AC-A14. The later diagnostics in §4.4 were not a full `make check` or full live-host verification.

### 4.4 Authorized investigation of previously failing quality gates

- Storage failures are caused by the test temp filesystem, not logging changes. `/tmp` is a tmpfs with 1,917,874,176 bytes available, below `DefaultStorageConfig().EmergencyStopMinMB` (2,048 MiB). The tests use `t.TempDir()` under `/tmp`; the storage gate correctly disables writes. In the audit handler tests, startup records emergency-stop, the request lowers the free-space threshold to 100 MiB and records emergency-resume, and the update itself records one user audit entry, hence three total entries. The exact three race tests fail with the default temp path and pass when `TMPDIR=/var/tmp`.
- `TMPDIR=/var/tmp GOPROXY=off make go-check`: passed formatting, vet, all `cmd/...` and `internal/...` race tests, and Go bridge integration tests. No storage/audit source change was made.
- `pnpm --dir web --offline exec vitest run src/hooks/use-table-url-state.test.ts src/features/auth/data/schema.test.ts --browser.headless`: passed 2 files / 25 tests.
- Browser failures remain in the real-browser infrastructure path. The two previously failing test files pass directly (25/25). A diagnostic package-script invocation did not apply the requested file filter and ran the full suite; it reproduced dynamic imports from `localhost` and iframe CORS/server connection errors, with a different set of suites failing than before. A later `--no-file-parallelism` diagnostic did not finish: Vitest reported that it could not connect to the browser server for 60 seconds, and the command was aborted. No proxy environment variables were present. This points to an unstable Vitest/Chromium localhost test-server path, but does not establish a source defect or a verified workaround. No frontend source was changed.
- `GOPROXY=file:///home/niko/go/pkg/mod/cache/download GOSUMDB=off make api-docs`: passed; Swag source/version metadata were already present in the local module cache, and generated OpenAPI files had no working-tree diff. No network access or new dependency installation occurred. The original `GOPROXY=off` error was a blocked module deprecation lookup, not a missing Swag source archive.
- 本节记录时 AC-A14 因浏览器门禁尚未通过而保持开放；§4.6 记录后续修复与全量门禁。当前 AC-A14 已验证通过；AC-A6 的 full-service live SIGHUP 验证及部署 profile/journald sign-off 仍待安全环境和平台资料。

### 4.5 Browser test infrastructure follow-up (2026-10-20)

- `web/vite.config.ts` now sets `test.browser.api.host` to `127.0.0.1`; installed Vitest 4.1.4 declarations confirm the option. `pnpm --dir web --offline exec tsc --project tsconfig.node.json` passed. The two suites that previously failed on browser-server connectivity passed directly (2 files / 25 tests).
- `pnpm --dir web --offline exec vitest run --browser.headless` completed 86 files / 502 tests: 82 files and 490 tests passed; 4 UI suites had 12 failures. The prior dynamic-import, iframe CORS and Vitest API connectivity errors did not recur. The remaining failures were locator/state/action failures in `config-drawer` (8), `language-switch` (1), `region-editor-dialog` (2), and `ping-dialog` (1). Vite also logged refused requests to the unstarted app API at `127.0.0.1:8000`; relation to the UI failures is not established.
- Re-ran only those four failing files serially with `--no-file-parallelism`: 4 files, 11 tests passed and the same 12 tests failed. This does not support file parallelism as the cause. Failure screenshots show the config drawer without the queried sidebar/layout radio controls; the language option was not present after opening its menu; region-editor state assertions remained unchanged; and the ping mutation mock was not called. These observations localize the residual failures outside Vitest's API connectivity path but do not establish their root cause or whether they predate this task.
- Source inspection explains the config-drawer failures: Sidebar and Layout sections use `max-md:hidden` (`web/src/components/config-drawer.tsx`), while Vitest Browser's default viewport is 414px wide. A desktop viewport for that suite is a plausible test-environment correction, but was not changed because it changes the suite's rendering environment beyond the approved host repair. The ping test also has a mock/API mismatch: `ping-dialog.test.tsx` provides `mutate`, while `PingDialog` calls `mutateAsync`. No test or component edits were made. The language-switch and region-editor failures remain without a confirmed root cause.
- No UI component, assertion, dependency, package script, or persistent parallelism setting was changed. Keep the narrow IPv4 Browser API fix; do not treat the remaining tests as fixed. AC-A14 remains open pending a green `make check`; live-service SIGHUP and deployment-profile sign-offs also remain outstanding.

### 4.6 授权后的浏览器套件修复与门禁复核（2026-10-20）

- `web/vite.config.ts` 保留 `test.browser.api.host = '127.0.0.1'`，并用 Vitest Browser instances 只给 `config-drawer.test.tsx` 与 `region-editor-dialog.test.tsx` 配置 Chromium 1280x896 桌面视口；默认实例排除这两份文件，同时展开 `configDefaults.exclude`，避免依赖目录中的测试被收集。配置采用 `vitest/config` 的 `defineConfig`，移除会触发 ESLint 的 triple-slash 类型引用。
- 根因与最小测试修复：配置抽屉的 Sidebar/Layout section 使用 `max-md:hidden`，区域编辑器工具栏在 414px 默认视口下被裁切；两个套件移至桌面 viewport。语言切换测试在重开菜单前等待旧菜单项卸载，避免退出动画期间的重复点击。Ping 测试 mock 改为提供已 resolve 的 `mutateAsync`，与组件真实调用匹配。区域编辑器测试等待 undo/freeze 控件启用后再点击；未改业务组件或 hook，也未放宽断言。
- 四个受影响文件串行运行：4 files / 23 tests passed。完整 `pnpm --dir web --offline test`：86 files / 502 tests passed。Chromium 输出 6 条 React `act(...)` 环境 console warning，但无失败。
- `pnpm --dir web --offline run lint`、`pnpm --dir web --offline run build`、`pnpm --dir web --offline exec tsc --project tsconfig.node.json` 均通过。全仓 `pnpm --dir web --offline run format:check` 报 37 个文件未格式化；本次改动的 `language-switch.test.tsx` 与 `vite.config.ts` 已格式化，4 个改动文件的定向 Prettier check 全通过，未格式化的 35 个既有文件未改。
- `TMPDIR=/var/tmp make native-test`：21/21 CTest 通过。`TMPDIR=/var/tmp GOPROXY=file:///home/niko/go/pkg/mod/cache/download GOSUMDB=off make api-docs` 通过，生成文件无 working-tree diff。`TMPDIR=/var/tmp GOPROXY=off make go-check` 通过 gofmt、vet、`cmd/...` 与 `internal/...` race tests 及 Go bridge 集成测试。
- 用户明确授权后，以离线 pnpm wrapper 运行 `TMPDIR=/var/tmp GOPROXY=file:///home/niko/go/pkg/mod/cache/download GOSUMDB=off make check PNPM=<offline-wrapper>`：全目标通过。`frontend-install` 执行 frozen-lockfile 安装，报告 lockfile up to date、resolution skipped，未访问网络；临时 wrapper 已清理。AC-A14 门禁已通过。
- 首次全套尝试因 per-instance `exclude` 未带 Vitest 默认排除而扫描了 `node_modules` 并超时；修正为 `...configDefaults.exclude` 后，完整套件通过 502/502。首次失败尝试未安装依赖；之后在用户授权下执行的安装由 `--offline` 强制离线，未下载依赖。
- 已只读 SSH 盘点 `rk3568`：Rockchip RK3568 EVB1 DDR4 V10、Debian 11 (Bullseye)、aarch64 kernel 5.10.226、systemd 247；systemd-journald active，`SystemMaxUse=100M`，当前 journal 占用 112.0 MiB。`/` 为 29G、已用 25G、剩余 2.7G（91%）；Timezone=Asia/Shanghai，NTP=yes 且已同步；未发现 Zhulong systemd unit。此记录仅是一个候选 RK profile，不构成预算 sign-off；journal 超出配置上限的观测和磁盘余量需确认，其他 RK/Ascend 部署类别仍待收集。完整服务 SIGHUP 尚未执行：启动可能改时区文件或调整 `CLOCK_REALTIME`，需先确认该板可用于服务启动/时间调整测试；本次 SSH 仅运行只读命令。

| 阶段 | 回滚动作 | ⚠️ 注意 |
| --- | --- | --- |
| 1-2（配置/logger） | 删除 `[logging]` 新键，恢复 `newLogger` 旧实现 | 新键全有零值默认，无需改代码 |
| 3（logctx） | 中间件与 `WriteError` 去掉 `logctx` 调用 | 纯附加字段，无副作用 |
| **4（删重复日志）** | 恢复 service 层 `logger.Error` | 只对确认不经过 `WriteError` 的失败恢复；同一请求的重复记录需修正归属 |
| 5（生产装配验证） | 恢复显式 `zap.NewNop()` 依赖 | 不把 constructor panic 作为回滚方式 |
| 6（SIGHUP） | 移除 `registerLogLevelSignal` | 无副作用 |
| 7（nativelog） | 不调用 `TryEnqueue`（尚无调用方） | 本任务内无 native 消费方 |

## 6. 完成标准

- [ ] `prd.md` 的 AC-A1 ~ AC-A14 全部满足
- [x] `design.md` §10 提出的父任务 `design.md` 修订已落实（C-3 由 `AsyncCore` 改为 `nativelog.Queue`）
- [x] 核对记录（§3.1）填写完整
- [x] `make check` 全绿
- [x] `logging-guidelines.md` 已更新
