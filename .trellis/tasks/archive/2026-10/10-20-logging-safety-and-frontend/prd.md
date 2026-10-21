# 日志安全与前端可观测

> 父任务：`.trellis/tasks/10-20-industrial-logging` · 前置依赖：子任务 A 与 B 归档后执行

## Goal

补齐日志安全边界与前端可观测：

1. **敏感数据最小化**：已知凭据字段使用显式 typed sanitizer；未知自由文本和原始 error 不承诺由通用 Core 完全识别，默认不写入日志。
2. **日志结构完整**：用户可控值经过控制字符清理/长度约束；安全字段由 schema allowlist 约束，不能注入额外日志行。
3. **前端错误可观测**：只上报稳定事件 ID 与 allowlisted fields，生产错误经限流批量送达后端；不上传任意异常正文。

## 背景

现状（已勘察）：

| 位置 | 现状 |
| --- | --- |
| `internal/camera/uri.go:171` | `SanitizeURL` 存在，全仓库只被调用 1 次（`camera/handler.go:387`）—— 该调用点是**审计详情**，不是日志（见下方更正） |
| `internal/algorunner/process.go:292` | `zap.Strings("broker_socks", ...)` 未脱敏 |
| 全局 | 无统一脱敏入口，无日志注入防护，无敏感字段黑名单 |
| `web/src/lib/handle-server-error.ts:7` | `if (import.meta.env.DEV) console.log(error)` —— **生产环境静默丢弃** |
| `web/src/main.tsx:37` | 裸 `console.log`，仅 DEV |
| `web/src/features/errors/general-error.tsx:25` | 裸 `console.error`，无上报 |
| 全局 | 前端无统一 logger，无错误上报通道 |
| 后端 | 无客户端日志接收端点，无速率限制中间件（`apperr.KindRateLimited` 已存在但仅 SSE 用过） |

全仓库前端生产代码只有 3 处 `console.*`，其中 2 处在 DEV-only 分支里。

### 勘察更正（2026-10-21，阶段一实施后复核）

原表把「`SanitizeURL` 只被调用 1 次」当成**日志漏用脱敏**的证据，这个推断是**反的**。实测：

```bash
grep -rn 'zap\.[A-Za-z]*([^)]*\(rtsp\|RTSP\|url\|Url\|URL\)' internal --include="*.go" | grep -v _test
# → 空
```

- 生产日志调用点里**没有任何 RTSP URL**，因此不存在「裸 URL 被写进日志」的泄漏面。
- `SanitizeURL` 唯一的调用者是 `camera/handler.go:387` 的 `streamAuditDetail` → `audithttp.TagDetail`，
  产出**存进审计表**并经前端 `AuditDetailDialog`（「查看完整请求上下文」）展示给运维。
- 因此该处**必须保留人可读的 host/path**；换成 `logsafe.URL` 会把
  `rtsp://admin:xxxxx@192.168.1.10:554/stream` 降为 `rtsp://[REDACTED_URL]`，运维无法判断是哪台摄像机出事。
- 这同时违反本 PRD Scope 的排除项「审计日志（`internal/audit`）的行为变更」。

结论：`logsafe.URL` 用于**日志**侧（含未来调用点与 `SanitizingCore` 的 `url`/`rtspUrl` 敏感键兜底），
不改动审计详情。AC-C4 / AC-C5 已按此收窄。

## Scope

**包含**：

- 新建 `internal/logsafe`：typed helpers（`Safe` / `URL` / `Path` / `Paths` / `Secret` / `IP` / `ErrorSummary`）和字段 allowlist
- 新建 `SanitizingCore`：仅作为结构与控制字符/长度兜底，不承诺识别任意自由文本中的秘密
- 收敛既有调用点：`camera/handler.go` 的 RTSP URL 不记录原文；`algorunner/process.go` 的 broker socket 路径不输出原文
- 新建 `internal/clientlog`：`POST /api/v1/client-logs`，schema allowlist、速率/并发限制与批量校验
- 新建业务无关的限流中间件（按 IP + process-wide budget）
- 前端新建 `web/src/lib/logger.ts`：有界 ring buffer、稳定事件 ID、allowlisted fields、批量上报
- 替换前端裸 `console.*`，不得向端点发送原始 exception message/stack/URL query
- 更新 `logging-guidelines.md`，写明安全 helper 的范围与不提供绝对秘密检测保证

**不包含**：

- 恢复 FFmpeg 诊断日志（见子任务 B 的排除项；那是独立的安全姿态决策）
- 前端日志的 UI 查询页面
- 日志的加密存储 / 签名（边缘设备上收益低于成本）
- 审计日志（`internal/audit`）的行为变更

## Requirements

### R1 显式日志安全 API（`internal/logsafe`）

- `Safe(s)` 仅清理控制字符并按字节上限截断；它**不是**凭据/URL detector。
- `URL(raw)` 对有效 URL 隐藏整个 userinfo，并移除 query/fragment；解析失败或输入超限时返回固定 `[REDACTED_URL]`，绝不回显原文。
- `Path(s)` / `Paths` 不输出原始路径，仅返回固定占位或经明确 allowlist 的类别；路径中的账号、设备 ID 不可通用判断为安全。
- `Secret(s)` 始终返回固定占位符。
- `IP(raw)` 返回经验证的网段摘要（IPv4 /24、IPv6 /64）；非法输入返回固定占位。
- `ErrorSummary(err)` 只输出稳定错误类型/分类，不输出 `err.Error()` 原文。
- `Fields` 按 allowlist 处理结构化字段；未知 key 与复杂值丢弃或替换为固定占位。
- 所有 helper 对空串、超长、畸形 UTF-8 安全，不 panic；不得依赖 `internal/camera`。

### R2 Defensive Core (best-effort boundary)

- `SanitizingCore` wraps `zapcore.Core` and must sanitize both `Write` fields/message and fields passed through `With` before forwarding them to the inner core.
- Sensitive exact keys are replaced, `StringType` values/messages get control-character and length cleanup, `ErrorType` is reduced to `ErrorSummary`, and arbitrary `ObjectMarshaler`/`ArrayMarshaler`/`Reflect`/`Namespace` values are omitted or replaced with a fixed marker.
- It does **not** inspect arbitrary text for every possible secret, nor prove safety for application-defined marshalers. Call sites must use typed helpers and avoid raw errors/free text.
- Must not mutate caller-owned field slices. `Check` must add the wrapper core, `With` returns a new wrapper, `Sync` forwards.
- Benchmark realistic log calls and report overhead/allocations on target hardware. There is no hard `<1µs` or zero-allocation promise; safety and correctness take precedence.

### R3 Injection and field-safety policy

- `Safe` prevents raw CR/LF/control injection and bounds field size; it does not perform semantic credential redaction.
- URL/secret/path/error fields must use their typed helper; unknown free-form user or exception text is not logged.
- SanitizingCore is only defense in depth for known key classes, control characters, string size and unsafe zap field types.
- Tests cover message text, `With` fields, `Write` fields, `zap.Error`, complex marshalers, newline injection and malformed URLs. They must prove both the sanitized output and the documented limits.
- JSON encoding escapes newline; console encoding is not the security boundary. The contract is that values are sanitized/omitted before encoding, not that every logger encoder behaves identically.

### R4 Frontend logger

- `web/src/lib/logger.ts` supports stable event IDs (for example `server.error`, `router.error_boundary`, `query.retry`) and typed allowlisted fields; no arbitrary exception message, stack, URL query or request body.
- Production accepts `warn` / `error`, queues in a bounded ring and batches to backend; each upload is capped at 50 entries and 48 KiB encoded JSON, below the server's 64 KiB body limit.
- Scheduled and threshold uploads use `fetch` with `credentials: "omit"`. `navigator.sendBeacon` has no credentials-mode option and may follow browser ambient same-origin credential behavior; the endpoint must not require or use session cookies for authorization, must perform no session-bound business action, and must never log cookies. If a deployment requires strict credential omission, disable Beacon and use `fetch({ keepalive: true, credentials: "omit" })` only.
- Buffer max 100; overflow drops oldest and counts. Batch every 5s or 20 entries, max 50 per request.
- Deduplicate `(level,event)` to at most 3 per second; upload failure never calls the logger recursively.
- Global handlers report only error name/class and stable source event, not `Error.message` or stack.
- Replace the three current console sites and install global handlers once.

### R5 Client log endpoint (`internal/clientlog`)

- `POST /api/v1/client-logs` is mounted in `public_routes` because it must work after session expiry.
- Payload schema requires `msg` to be a stable event ID matching `^[a-z0-9_.-]{1,64}$`; it is not prose. `route` is an allowlisted route category, not a URL; query, fragment and dynamic path segments are forbidden. Fields are scalar and restricted to a documented key allowlist. An optional top-level `dropped` count is an integer in `[0, 2^32-1]`.
- Reject, do not truncate-and-log, invalid event IDs/fields. Body content is never echoed.
- Hard limits: max 50 entries/request; msg/event ID max 64 bytes; max 16 allowlisted fields/entry; string field max 256 bytes; body max 64 KiB; per-IP 60 requests/minute (burst 10), limiter key cap 1024; plus process-wide request/byte budget configured from deployment profile.
- Emit only stable event IDs, allowlisted typed fields, `source=web`, server requestId and masked client IP. Never log client-provided raw IP, URL or arbitrary values.
- No audit record; handler does not call `audithttp.Tag`. Rate-limit events contain no body details.

### R6 Rate limiting middleware

- `internal/httpmiddleware` implements a bounded, concurrency-safe token bucket with injected clock and bounded key space. It imports no business package.
- The public client-log route uses both per-IP and process-wide budgets so IP churn cannot bypass all protection.
- IP is derived from the trusted TCP peer (`SetTrustedProxies(nil)`); do not trust forwarded headers.
- Exceeding a budget returns `httputil.WriteError` with `apperr.KindRateLimited`; logs contain route/requestId and no raw body or IP.
- LRU eviction bounds memory but is not a substitute for the global budget. Thresholds require deployment-profile review before acceptance.

## Acceptance Criteria

- [x] **AC-C1 helpers**: valid URL hides userinfo and query/fragment; malformed/oversized URL returns a fixed redaction marker. Path/IP/ErrorSummary cover empty, malformed, long and control-character inputs.
- [x] **AC-C2 Core contract**: tests cover message, `Write` fields, `With` fields, `ErrorType`, complex fields, newline and caller-slice immutability; unsupported arbitrary application marshalers/content are omitted.
- [x] **AC-C3 performance report**: benchmarks record plain-vs-sanitizing Core latency and allocations on supported target(s); there is no fixed `<1µs` or zero-allocation threshold.
- [x] **AC-C4 call-site safety** (narrowed 2026-10-21): no raw broker socket path, arbitrary error text or exception stack is written by any log call site; there is no raw RTSP URL log call site to sanitize, and the single `SanitizeURL` consumer is human-readable *audit* detail that must stay readable. Call-site inventory records dispositions, including the RTSP-vs-audit split.
- [x] **AC-C5 leakage regression**: valid RTSP credentials and malformed credential-shaped input never appear in collected **logs**; assert exact logger output (observer), not grep alone. Audit detail is explicitly out of scope here — it intentionally keeps a partially-readable URL with the password masked.
- [ ] **AC-C6 frontend upload** — ⚠️ synthetic browser test only; real page-unload sendBeacon not exercised on a live browser session or target board.: production browser error emits a stable allowlisted event within 5s; exception text/stack/query are absent.
- [ ] **AC-C7 frontend limits** — ⚠️ buffer/dedup/byte caps covered by synthetic test; real crash-loop drop rate unmeasured.: 10000 generated errors stay within buffer, dedup, entry/encoded-byte batch limits and backend budgets; drop counts are observable.
- [x] **AC-C8 endpoint limits**: per-IP and process-wide over-budget requests return 429; response does not echo body; no audit row is created; no raw IP/body is logged.
- [x] **AC-C9 limiter boundedness**: 10000 distinct source IPs leave the per-IP map within its configured maximum.
- [x] **AC-C10 frontend console**: only `logger.ts` contains production console calls.
- [x] **AC-C11 browser tests**: `pnpm --dir web test` passes in the real headless browser.
- [x] **AC-C12 project gate**: `make check` passes.

## Constraints

- Frontend tests run in the real headless browser; validate `sendBeacon`/`fetch(keepalive)` there, not in JSDOM.
- The client-log route is unauthenticated. Entry, field, body, per-IP and process-wide budgets are mandatory; the process-wide budget requires numeric deployment-profile signoff before release and parent AC-14 acceptance.
- `internal/logsafe` does not import `internal/camera`; `SanitizingCore` is always enabled and is not a universal secret detector.
- `internal/httpmiddleware` imports no business packages. Do not add a frontend dependency.
- Business paths do not panic. Raw exception messages, arbitrary user prose and stack traces are not uploaded.

## Risks

| Risk | Impact | Mitigation |
| --- | --- | --- |
| Public endpoint DoS | Edge device overload | Body/entry/field limits, per-IP LRU budget, process-wide budget and load test; global threshold is a release decision |
| Limiter key growth | Memory exhaustion | Capped key space and LRU test |
| Generic sanitizer misses a secret in prose | Credential leakage | No raw prose, error messages, URL queries or stacks; typed helpers and allowlisted event fields are primary controls |
| Frontend upload recursion | Self-amplifying request loop | Upload failure path never calls logger |
| Core overhead | Added latency/allocations | Record target benchmarks; no runtime disable switch |
| Last browser batch loss | Missing final events | Use sendBeacon/keepalive; document browser crash/kill/queue overflow as a limit |
| A/B logging call-site overlap | Merge conflicts | C follows A and B; update call-site inventory against the final implementation |

## Resolved Decision (2026-10-21)

C 的 client-log route 是 public/unauthenticated。仓库当前 `Config` 与 `config.toml` 未定义 process-wide request、byte 或 in-flight budget；现有需求仅确定 per-IP `60 requests/minute`、burst `10`、body `64 KiB`，而 aggregate budget 要从 deployment profile 注入并在发布前签核。

**决策（已由产品/部署确认，方案 A）**：process-wide budget 采用**可配置 provisional defaults**，缺失时以默认值启动，不拒绝启动。默认值：

| 预算 | provisional 默认 | 语义 |
| --- | --- | --- |
| process-wide requests | `120 / minute` | 覆盖单一控制台约每 5 秒一次上报的批次，并对多 IP churn 施加总量上限 |
| process-wide bytes | `2 MiB / minute` | 按请求体实际字节计，与 `64 KiB` body 上限独立 |
| max in-flight handlers | `8` | 并发执行中的 client-log handler 上限 |

配置落在新增 `[clientlog]` 节（`process_requests_per_minute` / `process_bytes_per_minute` / `max_in_flight`），全部零值即回落上述默认。

⚠️ **这些默认值未经部署签核。** 它们只表示「有界」而非「符合某部署画像的性能/带宽结论」。因此：

- 父任务 AC-14 的数值预算签核**保持 open**，不因本决策关闭；
- 不接受将 provisional 默认值表述为已签核预算；
- 部署画像复核后调整数值不构成实现变更，只需改 `config.toml` 与本文档。

放弃的方案：要求配置显式提供且缺失即拒绝启动（更安全，但会使既有 `config.toml` 升级即 breaking）；仅做 per-IP 限制、总量延后（会使 AC-C8 的 process-wide 429 无法验收）。
