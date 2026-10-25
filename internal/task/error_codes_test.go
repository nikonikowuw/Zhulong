package task_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
)

// taskErrorCodes lists every error code the task HTTP layer can emit.
// Each one must resolve to its own localized catalog entry; otherwise WriteError
// degrades the message to the generic REQUEST_FAILED text.
//
// INTERNAL_ERROR 与 VALIDATION_FAILED 来自 httputil 的内置目录，其余由 task/locale.go 注册。
var taskErrorCodes = []string{
	"TASK_NOT_FOUND",
	"TASK_ALREADY_EXISTS",
	"TASK_CREATE_FAILED",
	"TASK_UPDATE_FAILED",
	"TASK_DELETE_FAILED",
	"TASK_SAVE_FAILED",
	"DUPLICATE_ALGORITHM",
	"TASK_ALGORITHM_REQUIRED",
	"INVALID_TASK_CONFIG",
	"INVALID_REGION_CONFIG",
	"COMPUTE_BUDGET_EXCEEDED",
	"ALGORITHM_NOT_FOUND",
	"INVALID_REQUEST",
	"VALIDATION_FAILED",
	"INTERNAL_ERROR",
}

var genericFallbackMessages = map[string]string{
	"en":      "The request could not be completed",
	"zh-Hans": "无法完成请求",
	"zh-Hant": "無法完成此請求",
}

// TestTaskErrorCodesAreLocalized 断言本包会发出的每个错误码都有独立文案，
// 且没有任何一个回退到通用 REQUEST_FAILED 文本。
func TestTaskErrorCodesAreLocalized(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, code := range taskErrorCodes {
		for _, locale := range []string{"en", "zh-Hans", "zh-Hant"} {
			t.Run(code+"/"+locale, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				context, _ := gin.CreateTestContext(recorder)
				context.Request = httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
				context.Request.Header.Set("Accept-Language", locale)

				// The raw cause must never reach the client.
				httputil.WriteError(context, httputil.NewError(
					http.StatusConflict,
					code,
					"raw internal detail: database is locked",
					nil,
				))

				var body httputil.Response
				if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if body.Code != code {
					t.Fatalf("expected code %q, got %q", code, body.Code)
				}
				if body.Message == "" {
					t.Fatalf("expected localized message for %q in %q", code, locale)
				}
				if body.Message == genericFallbackMessages[locale] {
					t.Errorf("code %q fell back to the generic message in %q", code, locale)
				}
				if body.Message == "raw internal detail: database is locked" {
					t.Errorf("code %q leaked the raw internal message", code)
				}
			})
		}
	}
}
