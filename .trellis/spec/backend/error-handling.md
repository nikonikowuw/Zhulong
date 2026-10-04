# 错误处理与统一响应契约规范

> Go 内部错误传播、`AppError` 结构、统一响应信封、后端 i18n 翻译与日志脱敏。

---

## 1. 核心设计原则

1. **唯一成功判定**：以 `code == "OK"` 为唯一业务成功准绳。
2. **后端主导 i18n 翻译**：根据 `Accept-Language`（`en`、`zh-Hans`、`zh-Hant`）翻译顶层错误 `message` 和字段校验 `details[].message`；前端可直接展示。
3. **底层细节绝对脱敏**：CGO、NPU 驱动报错与 SQLite 锁冲突只记录于服务端 Zap 日志；响应不得包含堆栈、SQL、原始输入或底层错误信息。

---

## 2. 统一响应契约与 `AppError`

每个 JSON 响应必须包含 `code`、`message`、`data`。HTTP 422 字段校验错误可附加 `details`；所有错误响应的 `data` 为 JSON `null`。非校验错误不得输出 `details`。

```go
// internal/httputil/response.go
package httputil

type FieldDetail struct {
 Field   string `json:"field"`   // 请求 JSON 中的稳定字段路径
 Code    string `json:"code"`    // 稳定机器码，如 REQUIRED、MIN_VALUE
 Message string `json:"message"` // 按 Accept-Language 翻译后的文案
}

type Response struct {
 Code    string        `json:"code"`
 Message string        `json:"message"`
 Data    any           `json:"data"`             // 错误时显式编码为 null
 Details []FieldDetail `json:"details,omitempty"` // 仅 HTTP 422 字段校验错误
}

type AppError struct {
 Status  int
 Code    string
 Message string
 Details []FieldDetail
 Err     error // 只供服务端日志使用
}

func NewError(status int, code, message string, err error) *AppError
func NewValidationError(message string, details []FieldDetail) *AppError
```

成功响应示例：

```json
{ "code": "OK", "message": "success", "data": { "id": "cam_01" } }
```

一般错误响应示例：

```json
{ "code": "CAMERA_NOT_FOUND", "message": "摄像机不存在", "data": null }
```

字段校验错误响应示例：

```json
{
  "code": "VALIDATION_FAILED",
  "message": "请求参数校验失败",
  "data": null,
  "details": [
    { "field": "fps", "code": "MIN_VALUE", "message": "帧率不能小于 1" }
  ]
}
```

响应编码必须始终包含 `data`，不能使用 `omitempty`。仅 `details` 使用 `omitempty`；字段细节的 `message` 必须匹配请求语言，`field` 指向 JSON 字段路径而非 Go 结构体字段名。

错误写回时，应从 `AppError` 生成公开状态、稳定 `code` 与已翻译文案，并把底层 `Err` 脱敏后写入 Zap。不得直接序列化 `AppError` 或底层错误。

---

## 3. 调用示范

```go
if errors.Is(err, gorm.ErrRecordNotFound) {
    httputil.WriteError(c, httputil.NewError(http.StatusNotFound, "CAMERA_NOT_FOUND", "Camera not found", err))
    return
}
if err != nil {
    httputil.WriteError(c, httputil.NewError(http.StatusInternalServerError, "DATABASE_ERROR", "Database operation failed", err))
    return
}
httputil.Success(c, result)
```

---

## Scenario: Field-Level Validation Details

### 1. Scope / Trigger

- Trigger: a JSON request fails field validation and the handler returns HTTP 422.
- Purpose: let clients associate server-side validation feedback with the submitted control without parsing a generic message.

### 2. Signatures

- `Response`: `code`, `message`, `data`, optional `details`.
- `FieldDetail`: `field`, `code`, localized `message`.
- `NewValidationError(message string, details []FieldDetail) *AppError`.

### 3. Contracts

- Error `data` is always `null`.
- `details` is omitted except for HTTP 422 field validation errors.
- `field` is a stable JSON field path; `code` is a stable machine-readable validation code; `message` is localized from `Accept-Language`.
- Do not include rejected values, credentials, database/driver/CGO errors, or stack traces.

### 4. Validation & Error Matrix

| Condition | HTTP | Body behavior |
| --- | --- | --- |
| Valid request | 2xx | `data` contains the result; omit `details`. |
| Field validation failure | 422 | `data: null`; include one or more localized `details`. |
| Missing resource | 404 | `data: null`; omit `details`. |
| Internal failure | 5xx | `data: null`; omit `details`; log sanitized context and internal cause server-side. |

### 5. Good / Base / Bad Cases

- Good: `fps` violates a minimum; return `field: "fps"`, stable code `MIN_VALUE`, and a localized message without echoing the submitted value.
- Base: a non-validation 404 contains the standard three required fields and no `details`.
- Bad: return the rejected password, SQL error, or C++ exception text in `details` or `message`.

### 6. Tests Required

- Assert success and ordinary error responses contain exactly the three required fields.
- Assert 422 responses include `data: null` and correctly shaped `details`.
- Assert `details[].message` follows `Accept-Language` and does not contain rejected input.
- Assert non-422 errors omit `details` and internal errors are not returned to clients.

### 7. Wrong vs Correct

#### Wrong

```json
{ "code": "VALIDATION_FAILED", "message": "bad input", "data": null, "details": [{ "field": "password", "message": "wrong value: secret123" }] }
```

#### Correct

```json
{ "code": "VALIDATION_FAILED", "message": "Invalid request", "data": null, "details": [{ "field": "password", "code": "MIN_LENGTH", "message": "Password is too short" }] }
```

---

## 4. 模块化 i18n 字典注册规范 (Catalog Registry)

为防止 `internal/httputil` 膨胀为包含全系统业务错误码的单体杂物箱，采用注册式目录模式：

1. **平台通用字典**：`internal/httputil/locale.go` 仅保留通用 HTTP 状态与表单校验码（`INTERNAL_ERROR`、`ROUTE_NOT_FOUND`、`VALIDATION_FAILED`、`REQUIRED` 等），并通过读写锁保证并发安全。
2. **业务专属字典**：各业务模块（如 `internal/auth`、`internal/camera`）在自己的 `locale.go` 中维护本模块的错误码三语映射，并在 `init()` 阶段调用 `httputil.RegisterMessages(map[string]map[string]string{...})`。
3. **高内聚与自包含**：业务模块重构或废弃时，对应语言字典随包一并删除，杜绝平台基础包遗留死文案。
