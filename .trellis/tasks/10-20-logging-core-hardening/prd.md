# Go 日志核心加固

> 父任务：`.trellis/tasks/10-20-industrial-logging`

## Goal

在**不改动原生层与前端**的前提下，把 Go 侧的运行日志从「开发期够用」提升到「现场可排障」：

1. 日志策略（级别 / 格式 / 输出 / 采样 / 轮转）全部可配置；生产默认不采样，队列/输出过载可有界丢弃但必须可观察，文件占用有上界。
2. 一次失败请求的 access 事件与 error 事件以 `requestId` 关联；意外 5xx 只产生一条 Error，access 事件本身不记 Error。
3. 日志噪声与命名规范统一；生产装配缺失必需 logger 时显式返回启动错误，测试可以显式注入 `zap.NewNop()`。
4. 交付 `internal/logctx`（契约 C-1）与有界 `nativelog.Queue`（契约 C-3），供子任务 B 消费。

## Scope

**包含**：

- `internal/app/logger.go` 重写 + `internal/app/config.go` / `cmd/Zhulong/config.go` / `config.toml` 配置扩展
- 新增 `internal/logctx` 包
- `internal/httpmiddleware/http.go`：`RequestID` 写入 `logctx`，`AccessLog` 分流长连接
- `internal/httputil/errors.go`：错误日志带上 `requestId`
- service 层重复日志清理（按规则）
- `Named(...)` 命名统一
- 构造函数 `zap.NewNop()` 兜底 → 启动期 fail fast
- SIGHUP 运行期调级
- `nativelog.Queue` 骨架（契约 C-3，本任务只交付骨架与测试，子任务 B 接入）
- 前端 `web/src/features/**` 无改动

**不包含**：

- C ABI / C++ / runner 日志（→ 子任务 B）
- 脱敏器与日志注入防护（→ 子任务 C）
- 前端 logger（→ 子任务 C）
- 业务审计日志（`internal/audit`）行为变更
- OTel / 指标体系

## Requirements

### R1 日志配置化

- `[logging]` 段扩展：`development` / `level` / `format` / `output` / `[logging.sampling]` (`enabled`/`initial`/`thereafter`) / `[logging.rotation]` (`enabled`/`max_size_mb`/`max_backups`/`max_age_days`/`compress`/`max_total_size_mb`)。
- `level` 仅允许 `debug|info|warn|error`，用白名单解析；其他 zap 级别也拒绝。
- `format` 仅允许 `json|console`；默认随 `development`。
- 默认输出为 `stderr`。文件输出必须启用有界轮转且显式配置正数 `max_total_size_mb`；`rotation.enabled=false` 或预算缺失/无效时选择文件输出应启动失败，禁止无限追加。总量预算覆盖活动文件、归档和轮转/压缩临时文件。journald 作为明确部署 profile 的宿主托管方案，具体配额待平台确认。
- 采样默认关闭。显式启用时 `initial` / `thereafter` 必须为正数，否则启动失败。
- `[logging]` 新键必须向后兼容：既有只含 `development` 的 `config.toml` 必须原样可用。
- viper `UnmarshalExact` 严格模式要求 `config.toml.example` 与 `fileConfig` 同步更新。
- SIGHUP 必须使用解析出的配置文件路径重新读取 `logging.level`，仅对该键生效；配置读取/校验失败时保留最后有效级别。

### R2 时间戳与编码约定

- 时间戳统一 **UTC ISO8601**（`zapcore.ISO8601TimeEncoder` 包一层 UTC 转换）。
- `duration` 统一毫秒（`zapcore.MillisDurationEncoder`）。
- JSON 编码下 level 用小写；console 编码下用大写彩色。
- zap 自身的内部错误（编码失败、写失败）走独立 stderr（`zap.ErrorOutput`），不污染结构化流。

### R3 采样策略显式化

- 生产默认 **关闭**采样。
- 若 `sampling.enabled=true`，必须由配置显式给出 `initial` / `thereafter`（缺省 100/100）。
- **必须**在 `logging-guidelines.md` 中记录「zap `NewProductionConfig` 默认开启采样」这一事实，
  以及本项目的选择与理由。

### R4 落盘与轮转

- `stderr` 为默认；systemd profile 可由 journald 托管，但部署文档必须给出 host journal 总预算及 Zhulong 的实际资源边界。该 profile 尚未确认。
- `output` 为文件路径时必须 `rotation.enabled=true`；关闭轮转的文件输出配置应在启动时拒绝。
- 轮转失败不得无限追加原文件；将 sink 标记为故障并写入可用的独立诊断通道，错误状态/丢失计数可观察。
- 不承诺突然断电时零丢失；说明 `Sync()` 与设备持久化策略能提供的实际保证。

### R5 运行期调级（SIGHUP）

- 收到 `SIGHUP` 后从实际配置文件重新读取 `logging.level`，并通过 `zap.AtomicLevel` 立即生效。
- 只允许调级，不允许换 sink / 编码。
- 只接受 `debug|info|warn|error`；解析或读取失败保留当前级别并记 Warn，不得 panic、不得退出。
- 测试必须修改磁盘上的配置文件再发送信号；仅验证 PID 不变不算调级成功。

### R6 请求与流关联（契约 C-1）

- 新增 `internal/logctx`，API 见 `design.md` §3.1，依赖白名单：`context` + `go.uber.org/zap`。
- `httpmiddleware.RequestID` 在 `c.Request.Context()` 上写入 `requestId`；`RequestIDFromContext` 保留为兼容薄封装。
- `httputil.WriteError` 的错误事件带上 `requestId`。
- 业务层可从 `ctx` 取到带关联字段的 logger，且 `Named(...)` 前缀不丢。
- 长生命周期事件用 `streamId` 等业务 ID；HTTP `requestId` 不自动传播到后续流事件。

### R7 错误日志归属

- 请求路径内的意外 5xx 只由 `WriteError` 记录一条 Error；access event 只记录状态、请求 ID 与耗时，不使用 Error 级别。
- `WriteError` 对意外 5xx 即使没有 cause 也记录唯一 Error；预期 4xx 不因 `Err != nil` 自动升级为 Error。
- 删除 service 层调用前确认错误确实经过 `WriteError`；不经过 HTTP 边界的后台循环错误仍由所属组件记录。
- 规则必须写进 `.trellis/spec/backend/logging-guidelines.md`。

### R8 AccessLog 长连接分流

- SSE（`Content-Type: text/event-stream`）与 WebSocket（`Upgrade: websocket`）连接不把连接时长记作 `latency_ms`。普通请求耗时字段为 `latency_ms`。
- 记录 `stream.started` / `stream.closed` 两条事件；`stream.closed` 带 `session_ms`。事件语义与握手失败/连接未建立场景必须有测试。
- 普通 API access 事件统一使用非 Error 级别；5xx 的唯一 Error 由 R7 的错误事件承担。

### R9 命名与装配一致性

- `Named(...)` 统一为 `<domain>.<layer>` 点分隔。
  已知需修正：`person_handler` → `person.handler`、`algorithm_handler` → `algorithm.handler`、
  `person_service` → `person.service`、`algorithm_service` → `algorithm.service`、`record.lifecycle` 等。
- 生产装配不得把缺失 logger 静默降级为 `zap.NewNop()`；Fx/provider 应返回启动错误，不得 panic。测试可显式传 `zap.NewNop()`。
- `cmd/Zhulong/main.go` 的 `-reset-network` 分支复用 `newLogger` 的构造逻辑，消除重复实现。
- `main.go` 配置加载失败使用明确的 bootstrap stderr 诊断；该通道与初始化后的 JSON 应用日志流分开定义。

### R10 原生日志队列骨架（契约 C-3）

> **规划期修订**：原定为通用 `AsyncCore`（包装 `zapcore.Core`）。规划期核实 zap v1.28.0 后发现
> **`zapcore.Field.Clone()` 不存在**，通用异步 core 无法安全保留 `[]zapcore.Field`；
> 且 C ABI 日志回调本就不携带结构化字段。故修订为只搬运预格式化字符串的 `nativelog.Queue`。
> 完整理由与接口见 `design.md` §9。

- 交付 `nativelog.Queue`：有界、非阻塞入队，独立 goroutine 消费；所有级别的丢弃都计数并可观察。
- 队列 API 必须在 Stop 后拒绝入队且不 panic；不能关闭一个仍可能被 producer 写入的 channel。
- `TryEnqueue` 只承诺入队路径非阻塞；CGO 字符串复制会分配，整体 callback 的时间/分配由子任务 B 实测。
- 本任务只交付队列与测试，不接入 native（子任务 B 接入）。

## Acceptance Criteria

- [ ] **AC-A1 配置生效**：`level=debug` 时 debug 日志落盘；`level=error` 时 info/warn 被过滤；
      非法 `level` 导致启动失败并给出明确错误信息。
- [ ] **AC-A2 向后兼容**：仅含 `[logging] development=false` 的旧 `config.toml` 可正常启动。
- [ ] **AC-A3 requestId 关联**：必失败请求的 access event 与 error event 都带同一 requestId，含稳定 code/status；access event 不记 Error。
- [ ] **AC-A4 单一 Error**：意外 DB 5xx 恰有一条 Error；无 cause 的 5xx 仍记录；携带 cause 的预期 4xx 不记 Error。
- [ ] **AC-A5 采样默认关闭**：未过载时生产配置下突发 1000 条同级别日志，写入记录数 = 1000。
- [ ] **AC-A6 运行期调级**：修改磁盘上的配置文件再发 `SIGHUP`，新级别生效且 PID 不变；非法级别保持最后有效级别。
- [ ] **AC-A7 落盘有界**：文件输出必须启用轮转并配置正数 `max_total_size_mb`；配置拒绝无限追加或未设硬预算；活动文件、归档及压缩临时文件总字节数不超过预算，轮转故障不继续无限追加。
- [ ] **AC-A8 长连接不污染**：普通请求使用 `latency_ms`；SSE / WebSocket 不产生连接时长 `latency_ms`；start/end 事件字段与握手失败行为有测试。
- [ ] **AC-A9 生产无静默 logger fallback**：Fx 缺少必需 logger 时返回启动错误；测试明确注入 Nop logger。
- [ ] **AC-A10 命名统一**：`Named` 结果全部为 `<domain>.<layer>` 点分隔形式。
- [ ] **AC-A11 队列非阻塞与停机安全**：队列满和 Stop 后入队均立即返回、不 panic；丢弃数准确，Stop 可报告未排空数量。
- [ ] **AC-A12 关联字段基准**：无字段时 `Fields(ctx)` 返回 nil；`Logger(ctx, base)` 无字段路径基准记录分配情况。
- [ ] **AC-A13 UTC 时间戳**：日志时间戳按 UTC 编码；持续时间字段名明确 `_ms`。
- [x] **AC-A14 门禁通过**：`make check` 全绿。

## Constraints

- `internal/logctx` 的 import 白名单：`context`、`go.uber.org/zap`。
  **不得**依赖 `httputil` / `httpmiddleware` / `gin` —— 这是打破循环依赖的核心。
- 新增 Go 依赖需评估联网影响。若选 lumberjack 路线，需同步更新 `AGENTS.md` 中
  「`make native-deps` 是唯一联网步骤」的表述。
- 不修改 `internal/audit` 的任何行为。
- 不修改 C ABI / `native/**`。
- 测试与源码同目录；Go 的 `-race` 强制。
- 涉及 `internal/engine` 的测试必须走 `native/scripts/build.py` wrapper。
- `make go-check` 限定 `./cmd/... ./internal/...`，不要用 `./...`。

## 风险

| 风险 | 缓解 |
| --- | --- |
| 删重复日志时漏看 `apperr.Err` 为空 → 日志全丢 | 先加 `WriteError` 回归测试，再逐点核对 `apperr.Internal` 调用 |
| `logctx` 被后续开发者引入 `gin` 依赖 | 在包 doc comment 中写明依赖白名单；code review 核对 |
| 时间戳 UTC 化破坏既有 grep 脚本 | 改动前 `grep` 仓库内解析日志的脚本 |
| SIGHUP 与配置热加载竞态 | 只读 `level` 单项；解析失败保持当前级别 |
| fail-fast 改造影响既有测试 | 测试中显式注入真实 logger 或 `zaptest`，不依赖 nil 兜底 |
