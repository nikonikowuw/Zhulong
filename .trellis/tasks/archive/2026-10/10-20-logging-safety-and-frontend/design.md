# 日志安全与前端可观测 — 技术设计

> 父任务：`.trellis/tasks/10-20-industrial-logging`

## 1. 脱敏与清理：`internal/logsafe`

### 1.1 依赖方向

```txt
internal/logsafe  ←── internal/camera
       ▲            ←── internal/clientlog
       │            ←── internal/app (SanitizingCore)
       │
  safe.go dependencies: net/url, net/netip, strings, unicode/utf8, unicode
  core.go additionally uses go.uber.org/zap/zapcore
  no dependency on internal/camera or any business package
```

`camera.SanitizeURL` remains a compatibility wrapper but the logging call site emits only `logsafe.URL` output. `algorunner` no longer emits raw `broker_socks`; `Paths` exists for typed future use.

### 1.2 API contract

`logsafe` exposes `Safe`, `URL`, `Path`/`Paths`, `Secret`, `IP` and `ErrorSummary`. `Safe` normalizes UTF-8, replaces control characters and enforces a byte limit. Typed helpers are required for security-sensitive values; no helper promises to identify arbitrary embedded secrets.

### 1.3 Behavior

`Safe` replaces invalid UTF-8 and controls, then truncates at a valid UTF-8 boundary with a bounded marker. `URL` accepts only an absolute parseable URL and returns its lower-cased scheme plus a fixed redaction marker; malformed, empty or oversized input returns a fixed marker. It never returns userinfo, host, path, query or fragment. `Path`/`Paths` never return source paths. `ErrorSummary` must not call `err.Error()`.

## 1.4 Security boundary

`Safe` only handles control characters, UTF-8 normalization and length (maximum 2048 bytes). It does not recognize secrets hidden in arbitrary text, base64 or concatenated strings. Therefore raw exception messages, arbitrary prose, URL queries, paths and custom object marshalers must not be logged. Typed URL/IP/path/error helpers and the client event schema are primary controls; `SanitizingCore` is defense in depth only.

---

## 2. 兜底：`SanitizingCore`

### 2.1 为什么不只依赖调用点

The Core is a **best-effort structural guard**, not a universal secret detector. It sanitizes known keys, plain string fields/messages and unsafe zap field types; application code still must avoid raw free-form values and use typed helpers.

### 2.2 Core behavior

| Input or lifecycle method | Contract |
| --- | --- |
| Known sensitive key | Replace value with a fixed marker; normalized key matching is defense in depth only. Keys include `password`, `passwd`, `pwd`, `token`, `accessToken`, `refreshToken`, `secret`, `clientSecret`, `jwtSecret`, `credential(s)`, `authorization`, `cookie`, `setCookie`, `apiKey`, `rtspUrl`, `url`, `dsn`, `path`, `filePath` and `socketPath`. |
| Plain string/message | Apply `Safe`; do not treat this as secret detection. |
| `clientIp` | Use the typed IP helper. |
| Error field | Emit only a stable presence/category summary; never call `Error()`. |
| Unsupported zap value | Emit a fixed omission marker without invoking application marshalers/stringers. |
| `With` | Sanitize fields before forwarding to the inner core. |
| `Write` | Do not mutate caller slices; copy only if replacement is required. Sanitize message as well as fields. |
| `Check` | Attach the wrapper with `ce.AddCore(ent, c)` so `Write` cannot bypass it. |
| `Sync` | Forward through the embedded inner core. |

The wrapper is always enabled in `newLogger`; benchmark it against an unwrapped Core without adding a production bypass switch. Keep the concrete zap methods in implementation and tests, not here.

A regression test must assert that `Check` routes `Write` through the wrapper, not the inner core.

### 2.3 Assembly

`newLogger` builds the encoder core, wraps it with `SanitizingCore`, and then applies sampling **on the outside** of the wrapper:

```txt
logger → sampler (optional) → SanitizingCore → zapcore.NewCore(encoder, sink, level)
```

The safety wrapper has no production disable switch.

⚠️ **Corrected 2026-10-21.** An earlier revision of this section said the wrapper is applied *after* optional sampling, i.e. wrapper outermost. That ordering is **wrong and silently breaks sampling**: a sampler makes its decision inside its own `Check`, and §2.2 requires `SanitizingCore.Check` to attach only itself (never delegate to the inner core's `Check`). With the wrapper outermost, the sampler's `Check` is therefore never consulted, so sampling appears configured while doing nothing.

Measured with 1000 identical `Info` calls and `First=1, Thereafter=100`:

| Ordering | Records written | Outcome |
| --- | --- | --- |
| wrapper outermost (sampler inside) | 1000 | sampling silently dead |
| sampler outermost (wrapper inside) | 10 | sampling works, sanitization still applied |

Sanitization is unaffected by this ordering because it happens in `Write`/`With`, which the sampler calls through to. An entry dropped by the sampler is never sanitized, but it is also never written anywhere, so no unsanitized data escapes.

Regression coverage: `internal/app` `TestNewLoggerSamplingStillApplies` and `TestNewLoggerSamplingIsExplicitOptIn`. Reverting to the literal old wording fails both.

---

## 3. 按 IP 令牌桶限流中间件

### 3.1 为什么自实现而不是引 `golang.org/x/time/rate`

`golang.org/x/time` **不在当前依赖树中**。为一个 40 行的令牌桶引入新依赖，
与项目「离线构建、依赖最小化」的取向不符。
`golang.org/x/sync` 虽在树中（indirect），但其 `semaphore` 不是令牌桶。

### 3.2 Design

Use a small in-repository token bucket because `golang.org/x/time` is not currently in the dependency tree. The per-IP map is bounded (default 1024 keys) and uses LRU eviction so address churn cannot grow memory without limit. An additional process-wide budget caps aggregate traffic across distinct IPs. Implementation stays in `httpmiddleware` and imports no business package.

### 3.3 Behavior

- Inject the clock so refill, burst exhaustion and recovery are deterministic in tests.
- Keep map/LRU operations under a short mutex; do not claim lock-free or strictly nonblocking behavior.
- At the key limit, evict the least-recently-used entry before admitting a new IP.
- Enforce the per-IP budget and the process-wide budget before handler execution; rejected requests use the standard 429 envelope.

### 3.4 Middleware placement

Register the limiter after `RequestID` and only on the client-log route. The endpoint's trusted-proxy configuration must ensure `c.ClientIP()` reflects the TCP peer rather than caller-supplied forwarding headers.

### 3.5 Tests

- 令牌补充速率正确（注入假时钟）
- burst 耗尽后拒绝，等待后恢复
- key 空间上限生效（AC-C9）
- LRU 淘汰的是最久未使用者，不是最新
- 并发安全（`-race`）

---

## 4. 客户端日志端点：`internal/clientlog`

### 4.1 契约

```txt
POST /api/v1/client-logs
Content-Type: application/json

{
  "dropped": 4,
  "entries": [
    { "level": "error", "msg": "server.error", "ts": "2026-10-20T10:00:00.000Z",
      "route": "cameras.list", "fields": { "status": 503, "code": "SERVICE_UNAVAILABLE" } }
  ]
}

→ 200 { "code": "OK", "message": "…", "data": null }
→ 413 FILE_TOO_LARGE
→ 422 VALIDATION_FAILED
→ 429 TOO_MANY_REQUESTS

`msg` is an event identifier, not prose. `route` is an allowlisted category, not a URL; query, fragment and dynamic path segments are forbidden.
```

### 4.2 Limits

| Limit | Value | Validation |
| --- | --- | --- |
| Entries/request | 50 | 422 |
| Event ID (`msg`) | 64 bytes, `^[a-z0-9_.-]+$` | 422 |
| Fields/entry | 16, allowlisted keys and scalar types only | 422 |
| String field value | 256 bytes and restricted token format | 422 |
| Request body | 64 KiB | 413 |
| Per-IP rate | 60 requests/minute, burst 10 | 429 |
| Per-IP key map | 1024 entries, LRU | bounded |
| Process-wide request/byte budget | provisional defaults: 120 req/minute, 2 MiB/minute | 429 |
| Max in-flight client-log handlers | provisional default: 8 | 429 (reject, do not queue) |
| Client-reported drop count | integer in `[0, 2^32-1]` | 422 |

**Provisional defaults (decided 2026-10-21, PRD "Resolved Decision")**: the three process-wide budgets live in a new `[clientlog]` config section (`process_requests_per_minute`, `process_bytes_per_minute`, `max_in_flight`). Zero value falls back to the defaults above; a missing section does **not** abort startup. These defaults are bounded-by-construction but are **not** deployment-signed: they are not a bandwidth or capacity conclusion for any target board, and parent AC-14 stays open. Changing the numbers after deployment-profile review is a config-only change, not an implementation change.

### 4.3 Route

The endpoint is public so it remains reachable when a session expires. Attach it through the existing `public_routes` value group, not `protected_routes`; the strict size/schema/IP/process budgets are mandatory because this creates an unauthenticated ingestion surface. The handler must ignore ambient session cookies for authorization and must never log cookie values.

### 4.4 Request handling

The handler uses the standard JSON binder and error envelope, validates each event ID, route category and allowlisted scalar field, then adds server-owned `source`, sanitized client IP and request ID fields before logging. It never echoes client-provided content. A top-level client `dropped` count is bounded and logged as an aggregate; it does not bypass event or request limits.

The endpoint is ingestion-only and must not create audit records. Keep that behavior covered by AC-C8.

---

## 5. 前端统一 logger

### 5.1 API contract

The frontend logger accepts only four levels, a finite event-ID union and fields from the finite keys `status`, `code`, `failureCount`, `errorName` and `route`. Each entry contains a timestamp and optional route category/fields. The API has one-time global handler installation, bounded flush and test-only reset; raw messages, exceptions, stacks and arbitrary objects are not accepted.

### 5.2 Bounded buffer and throttle

Production uses a 100-entry ring buffer, flushes every 5 seconds or at 20 pending entries, and deduplicates each `(level,event)` to 3 per second. Each encoded request is bounded to at most 50 entries and 48 KiB, whichever comes first. Overflow drops the oldest entry and increments a local dropped counter capped at `Number.MAX_SAFE_INTEGER`; wire snapshots are capped at `2^32-1`. The uploader is single-flight; failures update the counter directly and never call the logger, requeue failed payloads or start an immediate retry loop.

Event IDs and field keys are validated at runtime as well as by TypeScript. Field values are bounded tokens, not arbitrary exception text.

### 5.3 Flush behavior

Flushes are timer/threshold driven and single-flight. The serialized request body, including `dropped`, is at most 48 KiB and contains at most 50 entries. The drop-count snapshot is clamped to `[0, 2^32-1]`; scheduled and threshold flushes use same-origin POST with `credentials: "omit"` and `keepalive: true`. On success, remove the submitted entries and subtract only the submitted drop snapshot so drops accrued in flight remain pending. On failure, count submitted entries as dropped, do not requeue them or call `log()`, and wait for a later scheduled flush with bounded backoff rather than retrying immediately.

On `visibilitychange → hidden`, the fetch and Beacon paths share the single-flight gate. If a `fetch(keepalive)` is already active, do not start a competing Beacon flush. Otherwise send at most two batches with `navigator.sendBeacon`, each capped at 50 entries and 48 KiB. Put the current drop-count snapshot in the first batch only; subtract it only if `sendBeacon` accepts that batch. Remove a batch only when it returns true. If rejected, leave entries in memory and do not retry during teardown. Beacon acceptance is not server acknowledgement; browser crash, process kill or queue rejection can still lose the final batch. The Beacon API exposes no credentials-mode option, so this path cannot promise `credentials: "omit"`; browsers may apply ambient same-origin credential behavior. The public endpoint must ignore session cookies for authorization, perform no session-bound business action and never log cookies. If strict credential omission is required by the deployment profile, disable Beacon and use only `fetch(keepalive: true, credentials: "omit")`.

Scheduled `fetch` uploads omit credentials; all upload paths tolerate missing `Accept-Language` by using the server's default locale. Beacon uploads must work without a session cookie, but this API cannot guarantee that ambient same-origin credentials are omitted.

### 5.4 替换既有调用点

| File | Current behavior | New contract |
| --- | --- | --- |
| `web/src/lib/handle-server-error.ts:7` | DEV-only console of server error | `log('error', 'server.error', { status, code })`; never send response message |
| `web/src/main.tsx:37` | DEV query retry object dump | `log('debug', 'query.retry', { failureCount, status })` |
| `web/src/features/errors/general-error.tsx:25` | raw router error message | `log('error', 'router.error_boundary', { errorName: 'route_error' })` |
| `web/src/main.tsx` | no global capture | install handlers once; send only event ID/error name category, no message/stack |

---

## 6. 与子任务 A / B 的接口

| 接口 | 提供方 | 消费方 | 说明 |
| --- | --- | --- | --- |
| `logctx.Logger(ctx, base)` | A | C | 客户端日志 handler 用它带 `requestId` |
| `logsafe` | **C** | A, B | C supplies typed helpers after A/B finish; until then A/B omit sensitive/raw fields and must not add placeholders or assume the package exists. |
| `nativelog` 丢弃计数模式 | A | C | 前端上报的丢弃计数采用同一套「补齐上报」原则 |
| `httputil.BindJSON` / `HandleBindError` | 既有 | C | 沿用既有信封与本地化，不新造 |

`logsafe` is intentionally delivered after A/B. If an implementation already exists when C starts, inspect its history and diff and extend it incrementally; do not replace user or concurrent work wholesale. A/B must not add placeholder sanitizers or rely on a not-yet-delivered API.
