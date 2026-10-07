# Host Runtime Contract

> Executable contract for the current no-hardware Go/CGO/C++ host lifecycle.

## Scenario: Build, Start, Serve, and Stop the Embedded Host

### 1. Scope / Trigger

- Trigger: changing CLI flags, host startup order, health/API envelopes, SQLite migrations, Vite embed output, or the C ABI lifecycle bridge.
- Scope: the current Go host only. It validates packaging and resource lifecycle, not camera business APIs, Go media subscriptions, inference, accelerator SDKs, or target-board support. Native-only RTSP capture is covered by the [ingestion contract](../native/ingestion-contract.md); the Go host still calls lifecycle APIs only.

### 2. Signatures

- Configuration: `config.toml` 配置 `http.address` (默认 `127.0.0.1:8080`), `data.directory` (默认 `./data`), 和 `logging.development`。
- Health: `GET /api/v1/health`; ready data is `{ "status": "ready", "components": { "database": "ready", "engine": "ready" } }`.
- Database: `database.New(dataDirectory string, logger *zap.Logger) *Store`, then `(*Store).OpenAndMigrate(ctx) error`, `Ready() bool`, and `Close() error`.
- Go native wrapper: `engine.New() *Engine`, `Start() error`, `Stop() error`, `Ready() bool`, and idempotent `Close() error`.
- C ABI: `Zhulong_engine_create(Zhulong_engine_h*)`, `Zhulong_engine_start(Zhulong_engine_h)`, `Zhulong_engine_stop(Zhulong_engine_h)`, and `Zhulong_engine_destroy(Zhulong_engine_h)`; statuses are fixed-width `int32_t` values.
- Build: explicitly prepare fixed FFmpeg sources with `make native-deps` (or import a local archive), then use `make check`, `make build`, and `make smoke`; `make api-docs` regenerates Swaggo output. Go commands using `internal/engine` go through `native/scripts/build.py go ...` to select isolated static dependencies and content-addressed Engine link inputs; bare Go commands do not supply them.

### 3. Contracts

- Every API JSON response includes `code`, `message`, and `data`. Success uses `code: "OK"`; errors use `data: null`. Only HTTP 422 field validation errors may add localized `details`.
- `GET /api/v1/health` returns HTTP 200 only after both database migrations and native startup complete; otherwise it returns HTTP 503 with the standard error envelope.
- `/api/*` and `/swagger/*` are reserved. Unknown API routes and unsupported API methods return an HTTP 404 JSON envelope and must not fall back to SPA HTML. `/` and extensionless client routes serve the embedded SPA.
- SQLite stores `<data-dir>/zhulong.db`; DSN settings are `_busy_timeout=5000`, `_foreign_keys=on`, `_journal_mode=WAL`. GORM uses one open/idle connection. Schema changes are embedded, versioned `internal/database/migrations/*.sql`; never use GORM `AutoMigrate`.
- Startup order is database open+migrate, native `Start`, then TCP bind and HTTP `Serve`. Any failed step cleans up resources acquired earlier before returning. Shutdown drains HTTP/Serve first, then closes the native handle, closes SQLite, and syncs Zap.
- Vite outputs to `internal/webui/dist` and clears that directory. A tracked `.keep` preserves the embed target before the first build, and the build plugin recreates it after Vite clears the output. Root `make build` builds frontend and native dependencies before the Go binary.
- The C++ side allocates and frees the opaque engine handle. Go owns only the handle value and serializes access; the current bridge passes no Go pointer to C. Every exported C ABI function isolates C++ exceptions; status-returning functions map failures to fixed codes, while destroy remains void.

### 4. Validation & Error Matrix

| Condition | Expected behavior |
| --- | --- |
| SQLite open or migration fails | Host startup fails; native startup and HTTP bind do not occur; opened DB resources close. |
| Native start fails | Host startup fails; database closes; HTTP does not bind. |
| TCP bind fails after DB/native start | Host startup fails; native and DB close; logger syncs. |
| Health requested before dependencies are ready | HTTP 503, `SERVICE_UNAVAILABLE`, `data: null`. |
| Unknown `/api/*` route or method | HTTP 404, `ROUTE_NOT_FOUND`, `data: null`; never HTML. |
| Unhandled internal error | HTTP 500, safe localized message, `data: null`; cause is logged server-side only. |
| Field validation failure | HTTP 422; include only localized `{field, code, message}` details and never echo rejected values. |

### 5. Good / Base / Bad Cases

- Good: run `make check && make smoke`; the smoke test starts the built binary with a temporary data directory and verifies health, embedded SPA routing, favicon, API 404 isolation, Swagger's required response fields, and graceful SIGTERM shutdown.
- Base: closing a not-started engine or an already closed database is idempotent and does not release resources twice.
- Bad: start HTTP before migrations, return raw SQLite/CGO errors in JSON, pass Go pointers to the current C ABI, or claim lifecycle readiness proves an active media stream, inference, or target-board compatibility.

### 6. Tests Required

- `internal/database`: assert embedded migration success, failed migration cleanup, and readiness/close behavior.
- `internal/engine` and `native/tests`: assert serialized/idempotent lifecycle, status handling, and that the public header compiles and links from C.
- `internal/app`: assert Fx graph validity, health and SPA/API routing, migration failure blocking HTTP, listener failure rollback, and request drain before native/DB close.
- `internal/httputil`: assert exact success/error envelope keys, `data: null`, localized 422 details, and internal-error redaction.
- `web`: assert loading/error/retry/ready UI, persisted language/theme behavior, and document language after locale changes.
- Root: `make check` passes formatting, vet, race tests, CTest, frontend lint/type-check/tests/audit/build; `make smoke` validates the built host end to end.

### 7. Wrong vs Correct

#### Wrong

```go
// Starting HTTP before migrations can expose an unusable or inconsistent store.
listener, err := net.Listen("tcp", config.HTTPAddress)
if err != nil {
    return err
}
if err := database.OpenAndMigrate(ctx); err != nil {
    return err
}
```

#### Correct

```go
if err := database.OpenAndMigrate(ctx); err != nil {
    return err
}
if err := native.Start(); err != nil {
    return errors.Join(err, database.Close())
}
listener, err := net.Listen("tcp", config.HTTPAddress)
if err != nil {
    return errors.Join(err, native.Close(), database.Close())
}
```

The production implementation centralizes this ordering and rollback in `internal/app/lifecycleRuntime`; do not duplicate startup logic in handlers or constructors.
