package httputil

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestWriteErrorSanitizesInternalCause(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/v1/private", nil)
	context.Request.Header.Set("Accept-Language", "zh-Hant")
	WriteError(context, NewError(http.StatusInternalServerError, "INTERNAL_ERROR", "unsafe detail", errors.New("secret database path")))

	var body map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body) != 3 {
		t.Fatalf("expected exactly 3 response fields, got %v", body)
	}
	if string(body["data"]) != "null" {
		t.Fatalf("expected null data, got %s", body["data"])
	}
	if strings.Contains(recorder.Body.String(), "secret database path") || strings.Contains(recorder.Body.String(), "unsafe detail") {
		t.Fatalf("internal error leaked to response: %s", recorder.Body.String())
	}
	if string(body["message"]) != `"伺服器內部錯誤"` {
		t.Fatalf("message was not localized: %s", recorder.Body.String())
	}
}

func TestValidationErrorIncludesOnlyLocalizedFieldDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name            string
		acceptLanguage  string
		contentLanguage string
		message         string
		detailMessage   string
	}{
		{name: "English", acceptLanguage: "en", contentLanguage: "en", message: "Request validation failed", detailMessage: "This field is required"},
		{name: "Simplified Chinese", acceptLanguage: "zh-CN,zh;q=0.9", contentLanguage: "zh-Hans", message: "请求参数校验失败", detailMessage: "此字段为必填项"},
		{name: "Traditional Chinese", acceptLanguage: "zh-Hant", contentLanguage: "zh-Hant", message: "請求參數驗證失敗", detailMessage: "此欄位為必填項"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodPost, "/api/v1/example", nil)
			context.Request.Header.Set("Accept-Language", test.acceptLanguage)

			WriteError(context, NewValidationError("untranslated top message", []FieldDetail{{
				Field:   "name",
				Code:    "REQUIRED",
				Message: "untranslated detail message",
			}}))

			var body Response
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("expected 422 status, got %d", recorder.Code)
			}
			if body.Code != "VALIDATION_FAILED" || body.Message != test.message || body.Data != nil {
				t.Fatalf("unexpected localized response: %+v", body)
			}
			if len(body.Details) != 1 || body.Details[0].Field != "name" || body.Details[0].Code != "REQUIRED" || body.Details[0].Message != test.detailMessage {
				t.Fatalf("unexpected localized validation details: %+v", body.Details)
			}
			if strings.Contains(recorder.Body.String(), "untranslated") {
				t.Fatalf("untranslated caller messages leaked to response: %s", recorder.Body.String())
			}
			if got := recorder.Header().Get("Content-Language"); got != test.contentLanguage {
				t.Fatalf("expected Content-Language %q, got %q", test.contentLanguage, got)
			}
			if !strings.Contains(strings.Join(recorder.Header().Values("Vary"), ","), "Accept-Language") {
				t.Fatalf("expected Vary: Accept-Language, got %v", recorder.Header().Values("Vary"))
			}
		})
	}
}

func TestUnknownErrorUsesLocalizedGenericMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/v1/resource", nil)
	context.Request.Header.Set("Accept-Language", "zh-Hant")
	WriteError(context, NewError(http.StatusConflict, "RESOURCE_BUSY", "untranslated internal message", nil))

	var body Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "RESOURCE_BUSY" || body.Message != "無法完成此請求" {
		t.Fatalf("unexpected localized fallback: %+v", body)
	}
	if strings.Contains(recorder.Body.String(), "untranslated internal message") {
		t.Fatalf("untranslated message leaked to response: %s", recorder.Body.String())
	}
}

func TestUnknownValidationCodeUsesLocalizedGenericMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/v1/example", nil)
	context.Request.Header.Set("Accept-Language", "zh-Hans")
	WriteError(context, NewValidationError("untranslated top message", []FieldDetail{{
		Field:   "fps",
		Code:    "FUTURE_RULE",
		Message: "untranslated detail message",
	}}))

	var body Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Details) != 1 || body.Details[0].Message != "输入值无效" {
		t.Fatalf("unexpected localized validation fallback: %+v", body.Details)
	}
	if strings.Contains(recorder.Body.String(), "untranslated") {
		t.Fatalf("untranslated validation text leaked to response: %s", recorder.Body.String())
	}
}

func TestPayloadTooLargeErrorIsLocalized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/init", nil)
	context.Request.Header.Set("Accept-Language", "zh-Hant")

	WritePayloadTooLarge(context)

	var body Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if recorder.Code != http.StatusRequestEntityTooLarge || body.Code != "PAYLOAD_TOO_LARGE" || body.Message != "請求內容過大" || body.Data != nil {
		t.Fatalf("unexpected payload-too-large response: status=%d body=%+v", recorder.Code, body)
	}
}

func TestNonValidationErrorOmitsDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/v1/missing", nil)

	WriteError(context, NewError(http.StatusNotFound, "ROUTE_NOT_FOUND", "Route not found", nil))

	var body map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body) != 3 {
		t.Fatalf("expected details to be omitted, got %s", recorder.Body.String())
	}
	if string(body["data"]) != "null" {
		t.Fatalf("expected null data, got %s", body["data"])
	}
}

func TestRegisterMessages_ConcurrentAndLocalized(t *testing.T) {
	RegisterMessages(map[string]map[string]string{
		"CUSTOM_TEST_CODE": {
			"en":      "Custom test message",
			"zh-Hans": "自定义测试消息",
			"zh-Hant": "自訂測試訊息",
		},
	})

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	context.Request.Header.Set("Accept-Language", "zh-Hant")

	WriteError(context, NewError(http.StatusBadRequest, "CUSTOM_TEST_CODE", "raw message", nil))

	var body Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "CUSTOM_TEST_CODE" || body.Message != "自訂測試訊息" {
		t.Fatalf("expected registered custom message, got %+v", body)
	}
}
