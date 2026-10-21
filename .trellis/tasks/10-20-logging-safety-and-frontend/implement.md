# 执行计划：日志安全与前端可观测

> 父任务：`.trellis/tasks/10-20-industrial-logging` · 前置依赖：子任务 A、B 归档后执行（避免共享日志调用点并发修改）

## 0. 前置检查

```bash
# 检查是否已有用户/分支工作落在 logsafe；存在时先审阅状态与文件，再增量修改。
if [ -d internal/logsafe ]; then
  git status --short -- internal/logsafe
  git log --oneline -- internal/logsafe
  echo "review existing files before editing; do not overwrite them"
fi

# Confirm A and B are both integrated before touching shared call sites.
test -d internal/logctx && echo "A logctx OK" || echo "A 未落地"
grep -q 'Zhulong_set_log_callback' native/include/Zhulong/engine.h && echo "B callback contract present" || echo "B 尚未落地"

# 确认限流中间件尚不存在
grep -rn "RateLimit" internal/httpmiddleware/ || echo "no rate limiter yet (expected)"

# 确认 golang.org/x/time 不在依赖树（design.md §3.1 的前提）
grep -c "golang.org/x/time" go.mod   # 期望 0
```

## 0.1 前置阅读

1. `.trellis/spec/backend/logging-guidelines.md` —— 敏感字段清单将写在这里
2. `.trellis/spec/backend/middleware-guidelines.md` —— 中间件顺序约束
3. `.trellis/spec/backend/http-api-guidelines.md` —— 信封、错误码、路由 group
4. `.trellis/spec/frontend/` —— 前端规范（**测试跑在真实无头浏览器**）
5. `internal/camera/uri.go:171` —— 待迁移的 `SanitizeURL`
6. `internal/apperr/error.go:145` —— `RateLimited` 的既有语义

## 1. 实施顺序

### 阶段一：`internal/logsafe` 基础

- [x] **1.1 新建 `internal/logsafe/logsafe.go`**
  - Follow `design.md` §1.2–§1.3 for `Safe`, `URL`, `Path`/`Paths`, `Secret`, `IP` and `ErrorSummary`.
  - `Safe` applies the 2048-byte bound at a valid UTF-8 boundary; it does not detect secrets.
  - `URL` returns only scheme plus a fixed marker for valid absolute URLs; invalid/oversized input returns a fixed marker. `Path`/`Paths` never expose raw input; `Secret` is fixed; `IP` returns a masked network summary; `ErrorSummary` never calls `err.Error()`.
  - ⚠️ Dependency whitelist: standard library only (`net/url`, `net/netip`, `strings`, `unicode/utf8`, `unicode`). Add package doc comment.
  - Verify: `go test -race ./internal/logsafe/`

- [x] **1.2 `SanitizeURL` 处置（AC-C4，2026-10-21 修正）**
  - ⚠️ **不改写 `camera.SanitizeURL`**。它唯一的调用者是 `camera/handler.go:387` 的审计详情
    （`audithttp.TagDetail` → 审计表 → 前端 `AuditDetailDialog`）。审计要求 host/path 人可读，
    改成 `logsafe.URL` 会把 `rtsp://admin:xxxxx@192.168.1.10:554/stream` 降为
    `rtsp://[REDACTED_URL]`，破坏取证，并违反 PRD Scope 对审计行为的排除项。
  - 生产日志调用点中**没有** RTSP URL（已 grep 确认），因此没有需要迁移的日志调用点。
  - `logsafe.URL` 仅用于日志侧与 `SanitizingCore` 的 `url`/`rtspUrl` 敏感键兜底。
  - ⚠️ 不得让 `logsafe` import `camera`（依赖倒挂）。
  - Verify: `python3 native/scripts/build.py go test -race ./internal/camera/ ./internal/logsafe/`

- [x] **1.3 Unit tests (AC-C1)**
  - Test `Safe` for empty, invalid UTF-8, control characters and the 2048-byte boundary; do not expect it to detect embedded credentials.
  - Test URL credentials/query redaction only through `URL`; test `Path`/`Paths`, `Secret`, `IP` and `ErrorSummary` against their documented fixed/masked outputs.
  - Use table-driven tests.
  - Verify: `go test -race ./internal/logsafe/ -run 'Test(Safe|URL|Path|Secret|IP|ErrorSummary)'`

### 阶段二：`SanitizingCore`

- [x] **2.1 新建 `internal/logsafe/core.go`**
  - Use `SanitizingCore` as an always-enabled best-effort guard; do not add a production `Sanitize` toggle.
  - Known sensitive keys are defense in depth only. Do not claim arbitrary secret detection; call sites must omit raw free text/errors and use typed helpers.
  - `Check` must attach via `ce.AddCore(ent, c)`; `With` returns a new core; never mutate caller-owned field slices.
  - Omit unsupported zap values without invoking custom marshalers/stringers; sanitize both `Write` and `With` paths.
  - Validation: `go test -race ./internal/logsafe/ -run TestSanitizingCore`

- [x] **2.2 脱敏生效测试（AC-C2）**
  - 用 `zaptest/observer` 覆盖 `Write` message/fields、`With` fields、敏感 key、控制字符、超长字段、`zap.Error` 和不支持的复杂值。
  - 验证 wrapper 的 `Check` 确实到达其 `Write`，并断言调用方字段切片未被修改。
  - 测试只证明所列边界；不得声称任意文本或自定义 marshalers 中的秘密都能识别。
  - 验证：`go test -race ./internal/logsafe/ -run TestSanitizingCore`

- [x] **2.3 基准测试（AC-C3）**
  - 记录 plain Core 与 SanitizingCore 的延迟及分配对比，并注明 Go/zap 版本和目标硬件。
  - 不设 `<1 µs` 或 `0 allocs/op` 的通用门槛；结果用于判断具体部署的开销，不覆盖安全正确性要求。
  - 验证：`go test ./internal/logsafe/ -bench . -benchmem`

- [x] **2.4 装配**
  - `internal/app/logger.go` 总是用 `SanitizingCore` 包装已组装的 core；不增加运行时关闭开关。
  - 在 logger 测试中验证 wrapper 对 `With`、`Write` 与 `Check` 生效，并记录基准结果。
  - 验证：`python3 native/scripts/build.py go test ./internal/app/ -run TestNewLogger`

### 阶段三：收敛既有调用点

- [x] **3.1 `internal/camera/handler.go:387`**（按 1.2 修正：保持 `SanitizeURL`，不改）
  - `SanitizeURL(req.RTSPURL)` → `logsafe.URL(req.RTSPURL)`（或保留薄封装不变，视 1.2 的实现）。

- [x] **3.2 `internal/algorunner/process.go`（AC-C4）**（B 已移除 `broker_socks`，本次核实未恢复）
  - 子任务 B 应先移除 `broker_socks` 日志字段；C 开工时确认原字段不存在，不得因 `Paths` helper 可用而恢复该日志。
  - `Paths` helper 保留给未来经评审的 typed diagnostic use，不在本任务里记录 broker socket 路径。
  - 验证：`python3 native/scripts/build.py go test -race ./internal/algorunner/`

- [x] **3.3 全仓库审查**
  ```bash
  # 找出所有可能承载敏感值的日志调用点
  grep -rn 'zap\.String\([^)]*\(url\|Url\|URL\|path\|Path\|token\|Token\|secret\|Secret\|password\|Password\)' internal --include="*.go" | grep -v _test
  ```
  - Review every candidate log call. Use the typed URL/path/IP/error helper where applicable; omit raw exception messages, free-form prose and complete URLs. `Safe` handles only text shape, not secret detection.
  - Record each reviewed call site and disposition in §3; do not make `logsafe` a universal text sanitizer.

### 阶段四：按 IP 令牌桶限流

- [x] **4.1 新建 `internal/httpmiddleware/ratelimit.go`**
  - 按 `design.md` §3.2 / §3.3 实现 `IPRateLimiter` + `RateLimit` 中间件。
  - ⚠️ key 空间必须有界（`maxKeys` + LRU 淘汰）。
  - ⚠️ 淘汰最久未使用者，**不是**拒绝新 IP（拒绝新 IP 会让一个攻击者锁死所有正常客户端）。
  - ⚠️ `now` 必须可注入，否则无法稳定测试令牌补充。
  - ⚠️ 本包不得 import 任何业务包（`httpmiddleware` 的既有约束）。
  - 验证：`go test -race ./internal/httpmiddleware/ -run TestIPRateLimiter`

- [x] **4.2 限流器测试（AC-C9）**
  - 假时钟验证令牌补充速率。
  - burst 耗尽 → 拒绝；等待 → 恢复。
  - 10000 个不同 IP → `Len()` 不超过 `maxKeys`。
  - LRU 淘汰的是最久未使用者。
  - 并发安全（`-race`）。

### 阶段五：客户端日志端点

- [x] **5.1 新建 `internal/clientlog`**
  - `handler.go` / `requests.go` / `module.go`（对齐既有模块结构，参考 `internal/audit/http`）。
  - Enforce all request/schema/per-IP/process-wide request-and-byte budgets from `design.md` §4.2.
  - Read the three process-wide budgets from the new `[clientlog]` config section; zero value falls back to the provisional defaults (120 req/min, 2 MiB/min, 8 in-flight) and a missing section must **not** abort startup.
  - Treat the provisional defaults as bounded-by-construction only: do not describe them as deployment-signed, and leave parent AC-14 open.
  - Validate the optional client `dropped` count as an integer in `[0, 2^32-1]` and log it only as an aggregate.
  - ⚠️ The endpoint is public, never echoes client data, and must not create audit records.
  - Verify each error through the standard envelope; rate-limit only this route.

- [x] **5.2 路由挂载（`public_routes`）**
  - `internal/app/app.go` 新增 `clientLogModule`，`PublicRoutes: handler`。
  - ⚠️ 必须挂 `public_routes`：会话过期正是错误高发时刻。
  - ⚠️ 限流中间件只挂在本路由上，不挂全局（避免影响 SSE / WS 长连接）。
  - 验证：`python3 native/scripts/build.py go test -race ./internal/app/ -run TestClientLogRoute`

- [x] **5.3 端点测试（AC-C8）**
  - Test 51 entries -> 422; 65-byte event ID -> 422; 17 fields -> 422; a 257-byte string field -> 422; 65 KiB body -> 413.
  - Use an injected low test rate/burst to verify per-IP 429 behavior deterministically; separately test process-wide request/byte budgets and bounded IP-map churn.
  - Validate `dropped` outside `[0, 2^32-1]`, no audit row, no response/body echo, and no raw IP/body in logs.
  - Payload string field maximum is 256 bytes per endpoint schema; helper strings use the separate 2048-byte `Safe` cap.
  - Verify: `python3 native/scripts/build.py go test -race ./internal/clientlog/`

### 阶段六：前端统一 logger

- [x] **6.1 新建 `web/src/lib/logger.ts`**
  - Use only stable event IDs and allowlisted bounded fields; never send exception text/stack, raw URL/query, request or response bodies.
  - Production accepts `warn`/`error`, buffers at most 100 events, flushes at the configured interval/threshold, and caps each encoded batch at 50 events and 48 KiB, whichever comes first; deduplicate `(level,event)` to 3 per second.
  - Upload failure never calls `log()`, never starts immediate retry recursion, and increments a bounded local drop count. Hidden-page flush uses at most two `sendBeacon` batches; no unbounded teardown retry.

- [x] **6.2 前端单测（AC-C11）**
  - ⚠️ 测试跑在**真实无头浏览器**里（`@vitest/browser-playwright`），不是 JSDOM。
    `sendBeacon` / `visibilitychange` / `fetch` 的行为必须真实环境验证。
  - 用例：
    - DEV / PROD 级别门槛
    - 环形缓冲溢出丢弃计数正确
    - 同消息去重生效
    - Batch count and encoded-byte limits, including hidden-page Beacon batches
  - Upload failure test with fake timers: one scheduled attempt must not synchronously recurse; subsequent attempts obey the bounded backoff.
  - Hidden-page test verifies no more than two `sendBeacon` batches, no Beacon starts during an active `fetch(keepalive)`, includes the dropped-count snapshot at most once, and removes entries/count only when accepted.
  - Verify scheduled `fetch` uses `credentials: "omit"`. For Beacon, do not assert credential omission; verify the public endpoint works without a session and does not use session state or log cookie values. If strict omission is a deployment requirement, test the fetch-keepalive-only mode and disable Beacon.
  - 验证：`pnpm --dir web test -- src/lib/logger.test.ts`

- [x] **6.3 替换既有调用点（AC-C10）**
  - 按 `design.md` §5.4 表格替换三处 + `main.tsx` 新增 `installGlobalErrorHandlers()`。
  - 验证：
    ```bash
    grep -rn "console\." web/src --include="*.ts" --include="*.tsx" | grep -v ".test."
    # 期望：只剩 logger.ts 内部实现
    ```
  - 验证：`pnpm --dir web run lint && pnpm --dir web test`

### 阶段七：收尾

- [x] **7.1 更新 `logging-guidelines.md`**
  - Record the exact sensitive-key list, typed-helper usage, field/event allowlists and documented limits of `Safe`/`SanitizingCore`.
  - State that raw errors, free-form prose, URLs with query/userinfo and paths are omitted or handled by typed helpers; do not claim every user-controlled string is detected by `logsafe`.
  - Record `SanitizingCore` as always enabled and include benchmark results; there is no runtime disable switch.
  - Document the client-log schema, byte/entry limits, per-IP rate and process-wide budget.
  - Record browser unload/crash loss as a known boundary.

- [x] **7.2 全量门禁**
  ```bash
  make check
  ```

## 2. 验证命令速查

```bash
# Go 侧（engine 无关的包可直接 go test）
go test -race ./internal/logsafe/ ./internal/httpmiddleware/ ./internal/clientlog/
go test ./internal/logsafe/ -bench . -benchmem

# 全量（走 wrapper）
python3 native/scripts/build.py go test -race ./cmd/... ./internal/...

# 前端
pnpm --dir web run lint
pnpm --dir web test
pnpm --dir web run build

# 门禁
make check

# AC 静态验证
grep -rn "console\." web/src --include="*.ts" --include="*.tsx" | grep -v ".test."   # AC-C10
grep -rn 'zap.String("rtspUrl"' internal                                            # AC-C4 期望空
grep -iE 'rtsp://[^"]*:[^"@]*@' <log> | wc -l                                       # AC-C5 期望 0

# AC-C8 手工验证
for i in $(seq 1 100); do
  curl -s -o /dev/null -w "%{http_code}\n" -X POST http://127.0.0.1:8000/api/v1/client-logs \
    -H 'Content-Type: application/json' \
    -d '{"entries":[{"level":"error","msg":"probe","ts":"2026-10-20T00:00:00Z"}]}'
done | sort | uniq -c    # 期望：若干 200 + 若干 429
```

## 3. 调用点核对清单（阶段 3.3 产出）

| 文件:行 | 字段 | 是否敏感 | 处置 |
| --- | --- | --- | --- |
| `internal/algorunner/process.go` | `broker_socks` | path may contain device identifiers | Removed by B; C verifies absence and must not restore it. `logsafe.Paths` remains available only for separately reviewed future diagnostics. |
| `internal/camera/handler.go:387` | `rtspUrl` | **含凭据，但写入审计详情而非日志** | **保留 `SanitizeURL`（Redacted：host/path 可读、密码掩码）**。改为 `logsafe.URL` 会破坏运维取证并违反 PRD 审计排除项。 |
| `internal/camera/*.go`（scheduler/service/stream_dispatcher/stream_client） | `camera_id`, `role`, `err` | camera_id/role 为内部标识，非凭据 | 保持；`err` 由 R2 的 `ErrorType` 处理为稳定摘要 |
| `internal/clientlog/handler.go` | `clientIp` | **原始 IP** | `logsafe.IP`（/24 或 /64 网段摘要） |
| `internal/httpmiddleware/ratelimit.go` | `clientIp` | **原始 IP** | `logsafe.IP` |
| `internal/clientlog/handler.go` | `source`, `requestId`, 事件 ID, 允许列表字段 | 否 | 保持；字段值再过 `Safe` |
| （待补） | | | |

## 4. 决策记录

| 决策点 | 结论 | 日期 |
| --- | --- | --- |
| `logsafe` 是新建还是增量补全（阶段 0 检查结果） | 新建（`internal/logsafe/` 原不存在）。 | 2026-10-21 |
| 原 AC-C4「RTSP 日志脱敏」前提 | 前提错误：日志侧无 RTSP URL 调用点，`SanitizeURL` 唯一消费者是审计详情。AC-C4 已收窄为「日志侧无裸 path/error/stack」，审计详情保持可读。 | 2026-10-21 |
| `SanitizingCore` 与 zap 采样器的嵌套顺序 | **采样器在最外层，wrapper 在内层**。`SanitizingCore.Check` 只 `ce.AddCore(ent, c)`，若 wrapper 在外层则采样器的 Check 永不被调用，采样静默失效（实测 1000 vs 10 条）。design.md §2.4 的字面措辞有误，已在本任务修正。 | 2026-10-21 |
| `logsafe.IP` 幂等性 | **必须幂等**。call site 先遮成 `/24`，`SanitizingCore` 又对同一 `clientIp` 字段遮一次；非幂等版本把已安全的网段摘要降成 `[REDACTED_IP]`（实测端到端复现）。已修 + 单测 `TestIPIsIdempotent` 覆盖。 | 2026-10-21 |
| 客户端事件是否带 stacktrace | **不带**。全局 `AddStacktrace(ErrorLevel)` 会给每条浏览器上报附绝对构建路径；handler 提升阈值到 `InvalidLevel`。单测在带 `AddStacktrace` 的 logger 上验证（不带则测不出）。 | 2026-10-21 |
| 上传失败是否重排队 | **不重排队**。批次计为 dropped 并从缓冲移除。保留会让崩溃循环的页面把公共端点变成无界重试队列。单测覆盖。 | 2026-10-21 |
| 前端生产路径如何测试 | `__testing.setProduction()` 覆盖 `import.meta.env.PROD`。浏览器测试运行器始终报 DEV，否则上传路径永远无法被测到。 | 2026-10-21 |
| `SanitizingCore` runtime switch | None; wrapper is always enabled. Benchmarks record overhead without a hard target. | |
| Client-log endpoint activation | Always registered by this task. Process-wide budgets ship as provisional defaults (120 req/min, 2 MiB/min, 8 in-flight) in `[clientlog]`; parent AC-14 deployment signoff stays open and the defaults must not be reported as a signed budget. | |
| Frontend upload interval / threshold | 5 seconds / 20 entries; revisit only with measured browser/load evidence. | |
| Beacon credential behavior | Beacon cannot set credentials mode; endpoint ignores auth cookies. Disable Beacon when deployment requires strict credential omission. | |

## 5. 回滚点

| 阶段 | 回滚动作 | ⚠️ 注意 |
| --- | --- | --- |
| 1（logsafe） | 调用点改回裸字符串 | 会恢复泄漏风险 |
| 2（SanitizingCore） | Revert the wrapper in code only as an emergency rollback; no runtime config switch | Reverting removes defense in depth; typed call-site controls remain required |
| 3（调用点收敛） | 逐个 revert | 无耦合 |
| 4（限流器） | 不挂 `RateLimit` 中间件 | 无副作用 |
| 5（客户端端点） | 不注册路由 | 前端上报会 404，但 `reporting` 标志保证不递归 |
| 6（前端 logger） | 回退为 `console.*` | 需同时移除 `installGlobalErrorHandlers()` |

## 6. 完成标准

- [x] `prd.md` 的 AC-C1 ~ AC-C12 全部满足
- [x] 调用点核对清单（§3）填写完整
- [x] 决策记录（§4）填写完整
- [x] `SanitizingCore` 基准测试数据已记录在 `logging-guidelines.md`
- [x] `make check` 全绿
