# 单用户认证开发规范

> SQLite 单用户凭据、纯内存 Cookie 会话、认证 API 与前端认证态的可执行约定。

## Scenario: 单用户初始化、认证与会话

### 1. Scope / Trigger

- Trigger: 新增或修改管理员初始化、登录、登出、当前用户查询、受保护 API 或认证前端状态时。
- 适用范围：单设备、单管理员用户；不包含 RBAC、密码找回或跨重启会话恢复。
- 凭据持久化在 SQLite；Session 与登录失败计数仅保存在进程内存中，不创建 `sessions` 表。

### 2. Signatures

- `AuthService`：
  - `GetStatus(ctx context.Context) (AuthStatusResponse, error)`
  - `InitAdmin(ctx context.Context, req InitAdminRequest) (UserResponse, string, error)`
  - `Login(ctx context.Context, clientIP string, req LoginRequest) (UserResponse, string, error)`
  - `Logout(token string)`
  - `GetCurrentUser(ctx context.Context, userID int64) (UserResponse, error)`
  - `ValidateSession(token string) (SessionItem, bool)`
- `MemorySessionStore`：`Create(userID int64, username string) (string, error)`、`Get(token string) (SessionItem, bool)`、`Delete(token string)`；构造函数为 `NewMemorySessionStore(ttl time.Duration)`。Token 使用 32 字节 CSPRNG 随机数据并以十六进制编码。
- 登录限流：`RateLimiter.Reserve(ip string) (*Attempt, bool)` 原子预留额度；`Attempt` 必须由 `Fail()`（记录密码失败）、`Reset()`（认证成功并清空历史）或 `Cancel()`（基础设施错误，不计失败）之一收尾。三个方法共享一次性守卫，重复调用为 no-op，因此 `Login` 里的 `defer attempt.Cancel()` 可无条件兜底。
- REST 路由挂载在 `/api/v1/auth`：`GET /status`、`POST /init`、`POST /login`、`POST /logout`、`GET /me`。
- SQLite 只增加 `users(id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT UNIQUE, password_hash TEXT, created_at DATETIME, updated_at DATETIME)`；迁移必须版本化，不得使用 `AutoMigrate`。
- 唯一的配置键是 `[auth].session_ttl`（Go duration 或 `d` 后缀，默认 `24h`），由 `app.ParseSessionTTL` 解析并以 `NewMemorySessionStore(config.SessionTTL)` 注入；不存在其他认证相关环境变量。

### 3. Contracts

- `GET /status` 返回 `{ "initialized": boolean }`。无用户时仅允许初始化；已有用户时初始化必须返回 `403 SYSTEM_ALREADY_INITIALIZED`。
- 初始化请求为 `{ "username": string, "password": string, "confirmPassword": string }`。用户名长度为 3–32 **字符**，密码长度为 8–64 **字符**，确认密码必须匹配；密码使用 `bcrypt.DefaultCost` 哈希后持久化，成功后创建管理员并建立会话。
- 密码策略必须同时满足字符数与字节数：bcrypt 的输入上限是 **72 字节**，而请求绑定按 rune 计数，所以 64 个 CJK 字符（192 字节）能通过 `max=64` 却无法哈希。策略收敛在 `validateNewPassword`，超限返回 422 `PASSWORD_TOO_LONG`，**不得**让 `GenerateFromPassword` 的错误冒泡成 500。
- 登录请求为 `{ "username": string, "password": string }`。成功后设置 `zhulong_session` Cookie 并返回不含密码哈希的用户响应类型（`UserResponse`）。
- 会话 Cookie 属性为 `HttpOnly; SameSite=Lax; Path=/`，`MaxAge` 恒等于 `[auth].session_ttl`（默认 `24h`）。`Secure` 故意为 `false`：本机以明文 HTTP 提供 SPA 与 API，浏览器会丢弃 Secure Cookie 导致登录完全失效；**在前面终结 TLS 的部署必须把 `cookie.go` 里的 `sessionCookieSecure` 改为 `true`**。Session 仅在内存中，进程重启后所有会话失效。
- 有效 Session 采用滑动过期：`Get` 成功即续期，`RequireAuth` 同步续写 Cookie 的 `MaxAge`。`logout` 路由必须用 `withoutCookieRenewal()` 标记，让中间件跳过续写；**不得**用 URL 路径字符串匹配来判断是否续期。
- `POST /logout` 与 `GET /me` 必须通过认证中间件；登出删除对应内存 Session 并清除 Cookie。`/me` 返回从用户 Store 读取的当前用户响应类型（`UserResponse`），包括 `createdAt`。
- 登录按客户端 IP 使用 15 分钟滑动窗口限制最多 5 次失败。并发请求必须先原子预留额度；数据库或 Session 基础设施错误取消预留，不计为密码失败。过期 IP 记录需周期性清理。
- 未知用户名与错误密码必须消耗同样的额度、返回同样的延迟：`verifyPassword` 在无 hash 时对 `timingEqualizerHash` 做一次同 cost 的 bcrypt 比较，不得提前 return 泄露用户名是否存在。
- 前端请求必须设置 `credentials: "include"` 并发送当前 `Accept-Language`。认证初始化依次检查 status、再查询 me；受保护 API 的 401 由共享客户端通知全局认证状态清理。认证文本位于 feature-localized 的 `en`、`zh-Hans`、`zh-Hant` 语言包。
- API 错误统一遵守 `{ code, message, data: null }`；仅 422 字段校验响应可含 `details`。请求体超过 `MaxAPIRequestBodyBytes = 1 << 20` 字节返回 413 `PAYLOAD_TOO_LARGE`。

### 4. Validation & Error Matrix

| 条件 | HTTP | 错误码 / 行为 |
| --- | ---: | --- |
| 系统未初始化 | 200 | `initialized: false`；允许初始化 |
| 已存在管理员，再次初始化 | 403 | `SYSTEM_ALREADY_INITIALIZED` |
| 初始化字段无效或确认密码不匹配 | 422 | `VALIDATION_FAILED`、`PASSWORD_TOO_SHORT` 或 `PASSWORD_MISMATCH`，字段路径使用 JSON 名称 |
| 密码超过 64 字符或 72 字节 | 422 | `PASSWORD_TOO_LONG`（无 `details`，与 `PASSWORD_MISMATCH` 同类业务错误） |
| 尚未初始化时登录 | 412 | `SYSTEM_NOT_INITIALIZED` |
| 用户名或密码错误 | 401 | `INVALID_CREDENTIALS`，增加该 IP 的失败次数 |
| 同 IP 失败次数达到上限 | 429 | `TOO_MANY_ATTEMPTS` |
| 未登录或 Session 已失效 | 401 | `UNAUTHORIZED` |
| API 请求体超限 | 413 | `PAYLOAD_TOO_LARGE` |
| 数据库或随机数生成故障 | 500 | `INTERNAL_ERROR`；底层错误仅写服务端日志，不得伪装成凭据错误或返回给客户端 |

认证错误文案由后端按 `Accept-Language` 本地化；前端展示后端 `message`，不重复翻译业务错误码。

### 5. Good / Base / Bad Cases

- Good: 初始化通过事务确认 users 为空后插入 bcrypt 哈希；创建随机 Session 并将明文 Token 仅放入 HttpOnly Cookie。
- Base: 服务重启后内存 Session 消失，`/me` 返回 401，前端回到登录态；无需清理磁盘会话记录。
- Bad: 将明文密码或 Session 写入数据库/日志，给 logout 或 me 暴露无鉴权路由，或把数据库故障统一转换为 `INVALID_CREDENTIALS`。

### 6. Tests Required

- Migration tests: assert `users` exists with unique username and no `sessions` table; verify migration rollback.
- Service/store tests: assert bcrypt hash is persisted instead of plaintext; initialization is single-user under concurrent attempts (并发 goroutine 断言恰好 1 次成功 + `users` 表恰好 1 行); database errors remain distinguishable from invalid credentials; unknown username and wrong password both consume one attempt; 多字节密码（64 CJK 字符）返回 `PASSWORD_TOO_LONG` 而非 500，且不落库。
- Session/limiter tests: assert token randomness and expiry, logout invalidation, concurrent admission never exceeds the attempt limit, `Attempt` 收尾幂等，expired IP entries are pruned.
- Handler tests: assert cookie flags, 403 on repeated initialization, 401 on protected logout/me, JSON field paths and stable validation codes, localized errors, and populated `/me.createdAt`.
- Router tests: assert oversized known-length and streaming API bodies return JSON 413 rather than SPA HTML.
- Frontend tests: assert status-to-login/init routing, Cookie credential inclusion, global 401 state invalidation, and login/init/logout transitions.

### 7. Wrong vs Correct

#### Wrong

```go
// Path sniffing: any rename or trailing slash silently stops the renewal skip,
// and the middleware has to know about a business route.
if c.Request.Method != http.MethodPost || !strings.HasSuffix(c.Request.URL.Path, "/logout") {
    setSessionCookie(c, token, maxAge)
}
```

#### Correct

```go
// Routing declares the intent; the middleware never parses URLs.
authGroup.POST("/logout", withoutCookieRenewal(), RequireAuth(service), h.logoutHandler)
```

#### Wrong

```go
// The reserved slot is released by hand on each path, so one forgotten return
// leaks the reservation and locks the IP out for good.
if !limiter.Allow(ip) {
    return ErrTooManyAttempts
}
if err != nil {
    limiter.Cancel(ip)
    return err
}
limiter.RecordFailure(ip)
```

#### Correct

```go
// Reserve hands back a lease whose completion is idempotent, so the deferred
// Cancel is a safety net rather than a second release.
attempt, ok := limiter.Reserve(ip)
if !ok {
    return ErrTooManyAttempts
}
defer attempt.Cancel()

if err != nil {
    return err // infrastructure failure: reservation released, no failure recorded
}
if !verifyPassword(hash, req.Password) {
    attempt.Fail()
    return ErrInvalidCredentials
}
attempt.Reset() // successful authentication clears the IP's history
```

The service must also call `Reset(ip)` after successful authentication and must not persist the session token.

---

## Scenario: M2M Bearer Token（OpenAPI 机器鉴权）

### 1. Scope / Trigger

- Trigger：新增/修改面向外部平台的 OpenAPI 鉴权（`internal/apitoken`、`/api/v1/openapi/*`）。
- 它与单用户 Session 认证是**两条互斥通道**：同一个路由组只能挂其中一条。OpenAPI 组挂 `RequireAuth` 会让所有 M2M 请求 401，隐蔽且无日志。

### 2. Signatures

- `apitoken.GenerateToken() (raw, hash, masked string, err error)`
- `apitoken.Service.CreateToken(ctx, rawName string) (*APIToken, plain string, err error)`
- `apitoken.Service.Authenticate(ctx, rawToken string) (Identity, error)`
- `apitoken.RequireBearer(svc *Service, logger *zap.Logger, auditSvc *audit.Service) gin.HandlerFunc`
- `apitoken.SetIdentity(c, id)` / `apitoken.GetIdentity(c) (Identity, bool)`

### 3. Contracts

- **明文只出现一次**：32 字节 CSPRNG，`sk-` + hex；只在创建响应里返回，且响应头 `Cache-Control: no-store`。持久化只存 SHA-256 哈希与 masked 前缀。
- **任何出口都不得带明文**：数据库、日志、审计 `detail`/`target`、错误响应、前端持久化存储都禁止出现原始 Token、`token_hash`、`Authorization` 头。
- **名称 1–48 字符、全表唯一（含已吊销）**：上限 48 是审计契约倒推的——`api_token:` 前缀 10 字符 + 名称 ≤ 64；吊销后名称不回收，UI 必须把冲突解释为“已被历史令牌占用”。
- **生效上限 10 个**，吊销是软删且立即生效。
- **`last_used_at` 合并写**：60 秒窗口 + 后台 context（2s 超时）；`Close()` 用 `sync.WaitGroup` 排空在途落库（500ms），且必须排在 SQLite 关闭之前。
- **失败语义**：鉴权失败一律 401 + `WWW-Authenticate: Bearer`；数据库故障返回 500，不得降级成 401（那会把基础设施故障说成“凭据错”）。
- **审计**：成功时 Actor 升级为 Token 身份（`api_token:<name>`、`SourceAPI`）；失败写 `api_token.auth_rejected`，`detail` 只放枚举原因（`malformed` / `unknown_or_revoked`）。

### 4. Validation & Error Matrix

| 条件 | 结果 |
| --- | --- |
| 头缺失 / 非 `Bearer` / 空值 / 多值 | 401 `UNAUTHORIZED` |
| 未知或已吊销 Token | 401 `UNAUTHORIZED` + `api_token.auth_rejected(unknown_or_revoked)` |
| Token 格式合法但库查询失败 | 500 `INTERNAL_ERROR` |
| 名称重复（含已吊销） | 409 `API_TOKEN_NAME_ALREADY_EXISTS` |
| 名称缺失 / 超长 / 含控制字符 | 422 `API_TOKEN_NAME_REQUIRED` / `API_TOKEN_NAME_TOO_LONG` |
| 生效 Token 已达 10 | 409 `API_TOKEN_LIMIT_REACHED` |

### 5. Good / Base / Bad Cases

- Good：M2M 请求走 `openapi_routes` 独立组，`RequireBearer` 在责任链最前，审计中间件随后拿到升级后的 Actor。
- Base：未配置 `APITokenService`（测试或裁剪构建）时用 500 阻断中间件，**绝不免密放行**。
- Bad：把 `RequireAuth`（Cookie Session）挂到 OpenAPI 组 → M2M 全灭，且表现为“凭据无效”。
- Bad：每个请求同步 `UPDATE last_used_at` → SQLite 单写者下把整个读请求队列拖垮。

### 6. Tests Required

- 生成格式、哈希不可逆、masked 前缀不含完整明文。
- 并发创建第 11 个 → 409；同名（含已吊销）→ 409；吊销幂等。
- 鉴权矩阵：有效 / 未知 / 已吊销 / 格式坏 / 库故障；全程 `-race`。
- `last_used_at` 60s 合并窗口；`Close()` 排空后无遗留 goroutine。
- 在日志与审计 detail 中搜索原始 Token 字符串 → 零命中。

### 7. Wrong vs Correct

#### Wrong

```go
// 请求路径上同步写库：SQLite 单写者 + 响应后 ctx 立刻取消，
// 每次请求都排队一次写，且大部分写入被 ctx 取消浪费。
if err := s.store.TouchLastUsed(c.Request.Context(), id); err != nil {
    s.logger.Warn("touch failed", zap.Error(err))
}
```

#### Correct

```go
// 合并写入：60s 窗口内的重复命中直接丢弃，退役时由 Close() 排空。
s.touchLastUsed(id) // 内部用 context.WithoutCancel + WithTimeout 2s
```
