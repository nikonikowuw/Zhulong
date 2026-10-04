# 领域语义错误与泛型 HTTP 端点重构 技术设计 (design.md)

## 1. 架构分层与职责边界

```txt
┌─────────────────────────────────────────────────────────────┐
│ 领域层 (Service / Domain)                                    │
│ - 纯业务逻辑，绝无 net/http 或 gin 依赖                       │
│ - 返回业务数据与 apperr.Error (附带语义 Kind + Code)           │
└──────────────────────────────┬──────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────┐
│ 泛型传输层包装 (internal/httputil)                           │
│ - HandleJSON[Req, Resp] / Handle[Resp]                      │
│ - 负责 c.ShouldBindJSON、validator 错误转 422 FieldDetail   │
│ - 提取 apperr.Kind 映射为 HTTP Status (401, 403, 412, 429...)│
│ - 提取 apperr.Code 自动走 i18n 多语言翻译                   │
│ - 成功自动封装 Response{Code: "OK", Message: "success", ...} │
└──────────────────────────────┬──────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────┐
│ 控制器层 (internal/auth/handler.go)                          │
│ - 专注协议胶水：读取上下文、设置/清除 Cookie、调用 Service   │
│ - 零 switch-case，零参数绑定样板代码                         │
└─────────────────────────────────────────────────────────────┘
```

## 2. 核心接口与数据结构

### 2.1 `internal/apperr`
```go
package apperr

type Kind uint8

const (
	KindInternal Kind = iota
	KindInvalid          // 422 Unprocessable Entity
	KindUnauthenticated  // 401 Unauthorized
	KindPermissionDenied // 403 Forbidden
	KindNotFound         // 404 Not Found
	KindConflict         // 409 Conflict (或 403 已初始化)
	KindPrecondition     // 412 Precondition Failed
	KindRateLimited      // 429 Too Many Requests
)

type Error struct {
	Kind    Kind
	Code    string
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

func (e *Error) Unwrap() error {
	return e.Err
}
```

### 2.2 `internal/httputil` 扩展
```go
// 映射 Kind 到 HTTP Status Code
func KindToHTTPStatus(k apperr.Kind) int {
	switch k {
	case apperr.KindInvalid:
		return http.StatusUnprocessableEntity
	case apperr.KindUnauthenticated:
		return http.StatusUnauthorized
	case apperr.KindPermissionDenied:
		return http.StatusForbidden
	case apperr.KindNotFound:
		return http.StatusNotFound
	case apperr.KindConflict:
		return http.StatusForbidden // 或 http.StatusConflict，与当前契约保持 403
	case apperr.KindPrecondition:
		return http.StatusPreconditionFailed
	case apperr.KindRateLimited:
		return http.StatusTooManyRequests
	default:
		return http.StatusInternalServerError
	}
}

// 泛型端点适配器
func HandleJSON[Req any, Resp any](
	fn func(c *gin.Context, req Req) (Resp, error),
) gin.HandlerFunc

func Handle[Resp any](
	fn func(c *gin.Context) (Resp, error),
) gin.HandlerFunc
```

### 2.3 验证错误下沉（`HandleBindError`）
将原本属于 `auth/handler.go` 中的 `handleBindError`、`jsonFieldPath`、`validationCode` 下沉到 `httputil/bind.go`：
- 使用反射自动读取结构体字段的 `json` tag，自动将字段名转为前端 JSON 属性名（如 `confirmPassword`）。
- 将 validator 的 Tag（如 `required`, `min`, `max` 等）统一转为 `REQUIRED`, `MIN_LENGTH`, `PASSWORD_TOO_SHORT` 等稳定代码。

## 3. 错误映射与契约一致性矩阵

| 业务场景 | 原实现错误 | `apperr.Kind` | HTTP Status | Code | 说明 |
| :--- | :--- | :--- | :---: | :--- | :--- |
| 未登录或会话失效 | `writeUnauthorized` | `KindUnauthenticated` | 401 | `UNAUTHORIZED` | 契约不变 |
| 用户名或密码错误 | `ErrInvalidCredentials` | `KindUnauthenticated` | 401 | `INVALID_CREDENTIALS` | 契约不变 |
| 已初始化再调用 init | `ErrAlreadyInitialized` | `KindConflict` / `KindPermissionDenied` | 403 | `SYSTEM_ALREADY_INITIALIZED` | 契约不变 |
| 系统未初始化就登录 | `ErrNotInitialized` | `KindPrecondition` | 412 | `SYSTEM_NOT_INITIALIZED` | 契约不变 |
| 登录失败次数超限 | `ErrTooManyAttempts` | `KindRateLimited` | 429 | `TOO_MANY_ATTEMPTS` | 契约不变 |
| 密码不匹配 | `ErrPasswordMismatch` | `KindInvalid` | 422 | `PASSWORD_MISMATCH` | 契约不变 |
| 密码长度少于8位 | `ErrPasswordTooShort` | `KindInvalid` | 422 | `PASSWORD_TOO_SHORT` | 契约不变 |
| 字段验证失败 | `validator.ValidationErrors` | `KindInvalid` | 422 | `VALIDATION_FAILED` | 契约不变 |
| 请求体超限 (1MB) | `http.MaxBytesError` | - | 413 | `PAYLOAD_TOO_LARGE` | 契约不变 |
| 数据库异常/随机数异常 | `gorm.Error` / `io.Error` | `KindInternal` | 500 | `INTERNAL_ERROR` | 契约不变，仅打日志 |

## 4. 兼容性与性能影响

- **性能开销**：
  - Go 泛型在编译期完成单态化（Monomorphization），运行时零反射与开销！
  - 减少了大量分散在 Handler 里的临时 interface 分配与代码体积。
- **平滑兼容**：
  - `httputil.WriteError` 优先判断 `*apperr.Error`，接着判断原有 `*AppError`，最后 fallback 到 500。
  - 现有使用 `httputil.WriteError` 的其他路由完全不受破坏。
