# auth 模块全面优化

## Goal

修复多字节密码导致 500、限流预留泄漏风险、Cookie/Middleware 契约隐患，收敛重复代码并补齐并发初始化与多字节密码测试。

## Requirements

### 缺陷修复

- R1 密码策略必须同时约束字符数与 bcrypt 的 72 字节上限；超限返回 422 `PASSWORD_TOO_LONG`，不得冒泡成 500。
- R2 未知用户名与错误密码必须消耗同样的限流额度与响应延迟，不得通过提前 return 泄露用户名存在性。
- R3 限流额度预留必须具有编译器/结构层面的兜底：任何早退路径都不能泄漏 reservation。

### 契约与安全

- R4 `logout` 不再由中间件做 URL 路径字符串匹配来跳过 Cookie 续写，改由路由声明意图。
- R5 `RequireAuth(nil)` 必须 fail-closed（全部 401），不得 panic。
- R6 `UNAUTHORIZED` 错误码在包内只保留一处定义。
- R7 会话 Cookie 的 `Secure=false` 必须显式注释其部署前提，不得是「看起来忘了写」。
- R8 Gin 上下文中的认证身份改用 `SessionUser{ID, Username}`，杜绝对 `UserResponse.CreatedAt` 零值的误读。

### 质量

- R9 消除重复：`UserResponse` 投影、限流窗口过滤、Cookie MaxAge 推导、注销身份解析。
- R10 禁止遮蔽标准库包名（`bytes`）。
- R11 测试改用 `errors.Is`，并把 `InitAndLoginFlow` 拆成子测试。
- R12 补齐并发初始化单用户、多字节密码、Attempt 幂等等回归测试。

### 文档

- R13 同步 `.trellis/spec/backend/authentication-guidelines.md`：Session TTL 实际默认 24h（非 7 天）、存在 `[auth].session_ttl`、新的限流 API 与密码字节规则。

## Acceptance Criteria

- [x] 64 个 CJK 字符的密码在 HTTP 上是 422 `PASSWORD_TOO_LONG`，且不落库
- [x] 8 个 CJK 字符的密码可以正常初始化
- [x] 8 并发 `InitAdmin` 恰好 1 次成功、`users` 表恰好 1 行
- [x] 未知用户名登录同样消耗 1 次额度并返回 `INVALID_CREDENTIALS`
- [x] `logout` 只发出 1 个 `Set-Cookie`，且为过期 Cookie
- [x] `go vet ./cmd/... ./internal/...` 与 `go test -race ./internal/auth ./internal/app` 通过
- [x] Swagger 产物已重新生成

## Constraints

- 不改路由路径、不改 `AuthService` 接口签名、不新增配置键、不改数据库 schema。
- Cookie 的 `Secure` 保持 `false`：本机明文 HTTP 部署，改 `true` 会直接导致登录失败。

## Notes

后续可选（本次未做）：多实例/多进程 Session 共享、`logout` 之外的绝对会话上限、限流器按 IP 的 `Retry-After` 响应头。
