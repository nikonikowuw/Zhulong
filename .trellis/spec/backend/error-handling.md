# 错误处理与统一响应契约规范

> Go 内部错误传播、`AppError` 结构、三字段响应、后端 i18n 翻译与日志脱敏。

---

## 1. 核心设计原则

1. **唯一成功判定**：以 `code === "OK"` (或 `"ok"`) 为唯一业务成功准绳。
2. **后端主导 i18n 翻译**：错误提示信息的国际化直接由后端完成！Handler/中间件根据请求头 `Accept-Language` 自动将大写 `code` 翻译为对应语言的 `message`（支持 `en`, `zh-Hans`, `zh-Hant`）。前端直接通过 Toast 输出 `message`，无需在前端字典中二次映射维护。
3. **底层细节绝对脱敏**：CGO、NPU 驱动报错与 SQLite 锁冲突**仅记录于服务端 Zap 日志**；对外返回脱敏的 `code` 与友好 `message`，绝不裸露堆栈或 SQL。

---

## 2. 统一响应契约与 `AppError` 实现

```go
// internal/httputil/response.go
package httputil

import (
 "errors"
 "net/http"
 "github.com/gin-gonic/gin"
 "go.uber.org/zap"
)

type Response struct {
 Code    string `json:"code"`              // 成功固定 "ok"，失败为枚举
 Message string `json:"message"`           // 开发者友好的英文描述
 Data    any    `json:"data,omitempty"`    // 业务数据
 Details any    `json:"details,omitempty"` // 表单校验细节 (可选)
}

type AppError struct {
 Status  int    `json:"-"`
 Code    string `json:"code"`
 Message string `json:"message"`
 Details any    `json:"details,omitempty"`
 Err     error  `json:"-"`                 // ⚠️ 底层原始 error，绝不外泄
}

func (e *AppError) Error() string {
 if e.Err != nil { return e.Message + ": " + e.Err.Error() }
 return e.Message
}
func (e *AppError) Unwrap() error { return e.Err }

func NewError(status int, code, msg string, err error) *AppError {
 return &AppError{Status: status, Code: code, Message: msg, Err: err}
}

func OK(c *gin.Context, data any) {
 c.JSON(http.StatusOK, Response{Code: "ok", Message: "success", Data: data})
}

// Error 统一错误响应，包含日志拦截、脱敏与后端 i18n 翻译
func Error(c *gin.Context, err error) {
 var appErr *AppError
 if !errors.As(err, &appErr) {
  appErr = &AppError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "Internal server error", Err: err}
 }
 if appErr.Err != nil {
  zap.L().Error("request failed",
   zap.String("path", c.Request.URL.Path),
   zap.Int("status", appErr.Status),
   zap.String("code", appErr.Code),
   zap.Error(appErr.Err),
  )
 }

 // 根据请求头 Accept-Language 进行后端本地化翻译
 lang := c.GetHeader("Accept-Language")
 localizedMsg := TranslateMessage(lang, appErr.Code, appErr.Message)

 c.JSON(appErr.Status, Response{Code: appErr.Code, Message: localizedMsg, Details: appErr.Details})
}
```

---

## 3. 调用示范

```go
// Service 业务层：包装并脱敏
if errors.Is(err, gorm.ErrRecordNotFound) {
    return httputil.NewError(http.StatusNotFound, "CAMERA_NOT_FOUND", "Camera not found", err)
}
if err != nil {
    return httputil.NewError(http.StatusInternalServerError, "DATABASE_ERROR", "Database operation failed", err)
}

// Handler 表现层：一行收敛
if err := h.service.DeleteCamera(c.Request.Context(), id); err != nil {
    httputil.Error(c, err)
    return
}
httputil.OK(c, nil)
```
