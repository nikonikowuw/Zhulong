# 领域语义错误与泛型 HTTP 端点重构 PRD

## 1. 目标与背景 (Goal & Background)

Zhulong 后端当前在 HTTP Handler 中存在较多重复的样板代码：
- 参数反序列化 `c.ShouldBindJSON`、参数校验失败转 `FieldDetail` 422 错误、`MaxBodyLimit` 413 超限处理等逻辑在每个接口中重复编写。
- 业务错误到 HTTP 状态码与响应体映射采用手工 `switch-case` + `errors.Is` 匹配，冗长且易漏。
- 为实现领域层（Service）与传输层（HTTP）彻底解耦，引入纯领域语义错误包 `internal/apperr`（基于 `Kind` 语义模型），并通过 `internal/httputil` 提供的 Go 1.18+ 泛型端点适配器（`HandleJSON` / `Handle`）统一承接输入校验与错误收敛，彻底消除 Handler 冗余样板代码。
- 以 `internal/auth` 作为首个重构落地的完整业务模块，并为后续 `camera`、`npu`、`device` 提供标准化底座。

## 2. 需求列表 (Requirements)

1. **纯领域语义错误定义 (`internal/apperr`)**：
   - 不依赖 `net/http` 或 `gin`，可被 Service、Domain 乃至未来的 gRPC / CLI 安全引用。
   - 提供通用语义类别 `Kind`（如 `KindInvalid`、`KindUnauthenticated`、`KindPermissionDenied`、`KindNotFound`、`KindConflict`、`KindRateLimited`、`KindPrecondition`、`KindInternal` 等）。
   - 定义 `Error` 结构体，承载 `Kind`、稳定机器错误码 `Code`、默认英文 `Message` 和底层脱敏用的 `Err`，实现标准 `error` 及 `Unwrap` 接口。
   - 提供语义明确的工厂方法（如 `Unauthenticated`, `PermissionDenied`, `Conflict`, `RateLimited`, `Precondition`, `Invalid` 等）。

2. **泛型端点适配器 (`internal/httputil`)**：
   - 提供 `HandleJSON[Req any, Resp any](fn func(*gin.Context, Req) (Resp, error)) gin.HandlerFunc`。
   - 提供 `Handle[Resp any](fn func(*gin.Context) (Resp, error)) gin.HandlerFunc`。
   - 在适配器内统一处理：
     - `c.ShouldBindJSON(&req)`。
     - 413 `http.MaxBytesError` 拦截。
     - 422 `validator.ValidationErrors` 字段小驼峰路径提取与稳定错误码映射。
     - 400 `INVALID_FORMAT` 格式错误。
     - 错误时自动触发 `WriteError(c, err)`。
     - 成功时自动封装标准信封 `Success(c, resp)`。
   - 升级 `WriteError`：
     - 使用 `errors.As` 自动识别 `*apperr.Error`，将 `Kind` 映射为对应的 HTTP 状态码。
     - 提取 `Code` 联动现有的 i18n 错误字典（若已注册则翻译，未注册回退默认 message）。
     - 底层 `Err` 仅记录于 Zap 日志并脱敏，对外部客户端保持安全。
     - 保留向后兼容性（对原有的 `*AppError` 与普通 `error` 继续生效）。

3. **重构 `internal/auth` 模块**：
   - 重构 `auth/service.go`：使认证业务失败返回统一包装的 `apperr.Error`（如 `ErrTooManyAttempts` -> 429 `TOO_MANY_ATTEMPTS`, `ErrInvalidCredentials` -> 401 `INVALID_CREDENTIALS`, `ErrAlreadyInitialized` -> 403 `SYSTEM_ALREADY_INITIALIZED` 等）。
   - 重构 `auth/handler.go`：
     - 使用 `httputil.HandleJSON` 改造 `initHandler` 与 `loginHandler`。
     - 使用 `httputil.Handle` 改造 `statusHandler` 与 `meHandler`。
     - 移除包内手写的 `handleBindError`、`jsonFieldPath`、`validationCode`（下沉至 `httputil`）。
     - 完全移除 Handler 内部的 `switch-case` 错误匹配代码。
     - 保留 `setSessionCookie` 与 `clearSessionCookie` 在 Handler 中的显式调用。

## 3. 约束条件 (Constraints)

- **接口契约 100% 保持一致**：所有 HTTP 响应 JSON 格式、HTTP 状态码、错误码（Code）、多语言消息（zh-Hans / zh-Hant / en）、422 details 结构、Cookie 属性（HttpOnly / SameSite=Lax / zhulong_session）保持不变。
- **单元测试不降级**：`internal/auth`、`internal/httputil`、`internal/app` 等现有测试必须全部通过，并补充针对 `apperr` 与泛型端点适配器的新测试。
- **架构解耦**：`internal/apperr` 不得依赖任何外部 web 框架或 `net/http`；`internal/auth/service.go` 绝不依赖 Gin。

## 4. 验收标准 (Acceptance Criteria)

- [x] `internal/apperr` 创建完成，单元测试覆盖 `Kind`、`Error`、`Unwrap` 及各种工厂方法。
- [x] `internal/httputil` 增加泛型适配器（`HandleJSON` 与 `Handle`），参数绑定与 422/413 统一抽象完成。
- [x] `internal/httputil.WriteError` 原生支持 `apperr.Error` 解包并映射 HTTP 状态码与 i18n 消息。
- [x] `internal/auth/handler.go` 完成重构，代码行数明显精简，无手写 `switch` 错误映射与 `handleBindError`。
- [x] `go test -v -race ./internal/...` 全部测试绿灯通过。
- [x] 验证 OpenAPI/Swagger 文档与 API 行为完全吻合。
