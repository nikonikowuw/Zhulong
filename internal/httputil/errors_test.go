package httputil

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/logctx"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestWriteErrorSanitizesInternalCause(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/v1/private", nil)
	context.Request.Header.Set("Accept-Language", "zh-Hant")
	WriteError(
		context,
		NewError(
			http.StatusInternalServerError,
			"INTERNAL_ERROR",
			"unsafe detail",
			errors.New("secret database path"),
		),
	)

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
	hasLeakedInternalCause := strings.Contains(recorder.Body.String(), "secret database path") ||
		strings.Contains(recorder.Body.String(), "unsafe detail")
	if hasLeakedInternalCause {
		t.Fatalf("internal error leaked to response: %s", recorder.Body.String())
	}
	if string(body["message"]) != `"伺服器內部錯誤"` {
		t.Fatalf("message was not localized: %s", recorder.Body.String())
	}
}

func TestWriteErrorLogsUnexpectedFiveHundredsOnceAndIgnoresCausedFourHundreds(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core, observed := observer.New(zap.DebugLevel)
	restore := zap.ReplaceGlobals(zap.New(core))
	defer restore()

	write := func(requestID string, status int, code string, cause error) {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		request := httptest.NewRequest(http.MethodGet, "/api/v1/resource", nil)
		request = request.WithContext(logctx.WithRequestID(request.Context(), requestID))
		ctx.Request = request
		WriteError(
			ctx,
			NewError(
				status,
				code,
				"safe",
				cause,
			),
		)
	}

	write("req-cause", http.StatusInternalServerError, "DATABASE_ERROR", errors.New("database unavailable"))
	write("req-empty", http.StatusInternalServerError, "INTERNAL_ERROR", nil)
	write("req-client", http.StatusBadRequest, "INVALID_INPUT", errors.New("bad input"))

	entries := observed.All()
	if len(entries) != 2 {
		t.Fatalf("got %d Error events, want exactly 2 for the unexpected 5xx responses", len(entries))
	}
	want := []struct {
		requestID string
		code      string
		status    int64
	}{
		{requestID: "req-cause", code: "DATABASE_ERROR", status: http.StatusInternalServerError},
		{requestID: "req-empty", code: "INTERNAL_ERROR", status: http.StatusInternalServerError},
	}
	for i, entry := range entries {
		if entry.Level != zap.ErrorLevel {
			t.Errorf("entry %d level = %s, want error", i, entry.Level)
		}
		fields := entry.ContextMap()
		hasMismatch := fields["requestId"] != want[i].requestID ||
			fields["code"] != want[i].code ||
			fields["status"] != int64(want[i].status)
		if hasMismatch {
			t.Errorf(
				"entry %d fields = %#v, want requestId=%q code=%q status=%d",
				i,
				fields,
				want[i].requestID,
				want[i].code,
				want[i].status,
			)
		}
	}
	if got := entries[0].ContextMap()["error"]; got != "database unavailable" {
		t.Fatalf("cause field = %#v, want database unavailable", got)
	}
	if _, present := entries[1].ContextMap()["error"]; present {
		t.Fatalf("cause-free 5xx unexpectedly included an error field: %#v", entries[1].ContextMap())
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
		{
			name:            "English",
			acceptLanguage:  "en",
			contentLanguage: "en",
			message:         "Request validation failed",
			detailMessage:   "This field is required",
		},
		{
			name:            "Simplified Chinese",
			acceptLanguage:  "zh-CN,zh;q=0.9",
			contentLanguage: "zh-Hans",
			message:         "请求参数校验失败",
			detailMessage:   "此字段为必填项",
		},
		{
			name:            "Traditional Chinese",
			acceptLanguage:  "zh-Hant",
			contentLanguage: "zh-Hant",
			message:         "請求參數驗證失敗",
			detailMessage:   "此欄位為必填項",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodPost, "/api/v1/example", nil)
			context.Request.Header.Set("Accept-Language", test.acceptLanguage)

			WriteError(
				context,
				NewValidationError("untranslated top message", []FieldDetail{
					{
						Field:   "name",
						Code:    "REQUIRED",
						Message: "untranslated detail message",
					},
				}),
			)

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
			hasInvalidDetail := len(body.Details) != 1 ||
				body.Details[0].Field != "name" ||
				body.Details[0].Code != "REQUIRED" ||
				body.Details[0].Message != test.detailMessage
			if hasInvalidDetail {
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
	WriteError(
		context,
		NewError(
			http.StatusConflict,
			"RESOURCE_BUSY",
			"untranslated internal message",
			nil,
		),
	)

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
	WriteError(
		context,
		NewValidationError("untranslated top message", []FieldDetail{
			{
				Field:   "fps",
				Code:    "FUTURE_RULE",
				Message: "untranslated detail message",
			},
		}),
	)

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
	isUnexpectedResponse := recorder.Code != http.StatusRequestEntityTooLarge ||
		body.Code != "PAYLOAD_TOO_LARGE" ||
		body.Message != "請求內容過大" ||
		body.Data != nil
	if isUnexpectedResponse {
		t.Fatalf("unexpected payload-too-large response: status=%d body=%+v", recorder.Code, body)
	}
}

func TestNonValidationErrorOmitsDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/v1/missing", nil)

	WriteError(
		context,
		NewError(
			http.StatusNotFound,
			"ROUTE_NOT_FOUND",
			"Route not found",
			nil,
		),
	)

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

	WriteError(
		context,
		NewError(
			http.StatusBadRequest,
			"CUSTOM_TEST_CODE",
			"raw message",
			nil,
		),
	)

	var body Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "CUSTOM_TEST_CODE" || body.Message != "自訂測試訊息" {
		t.Fatalf("expected registered custom message, got %+v", body)
	}
}

func TestSafeErrorFromContext(t *testing.T) {
	// nil context
	if _, found := SafeErrorFromContext(nil); found {
		t.Fatalf("expected found=false for nil context")
	}

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	// Context with no error set
	if _, found := SafeErrorFromContext(c); found {
		t.Fatalf("expected found=false when no error is set")
	}

	// Context after WriteError
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil)
	c.Request.Header.Set("Accept-Language", "en")
	WriteError(
		c,
		NewError(
			http.StatusNotFound,
			"ROUTE_NOT_FOUND",
			"internal route error",
			nil,
		),
	)

	safe, found := SafeErrorFromContext(c)
	if !found {
		t.Fatalf("expected found=true after WriteError")
	}
	if safe.Code != "ROUTE_NOT_FOUND" {
		t.Fatalf("expected code ROUTE_NOT_FOUND, got %s", safe.Code)
	}
	if safe.Message != "Route not found" {
		t.Fatalf("expected message 'Route not found', got %s", safe.Message)
	}

	// Corrupted or empty code key
	c2, _ := gin.CreateTestContext(recorder)
	c2.Set(errorCodeKey, "")
	if _, found := SafeErrorFromContext(c2); found {
		t.Fatalf("expected found=false when code is empty string")
	}

	c3, _ := gin.CreateTestContext(recorder)
	c3.Set(errorCodeKey, 12345)
	if _, found := SafeErrorFromContext(c3); found {
		t.Fatalf("expected found=false when code is non-string")
	}
}

func TestAppError_ErrorAndUnwrap(t *testing.T) {
	cause := errors.New("underlying cause")
	appErr := NewError(
		http.StatusInternalServerError,
		"INTERNAL_ERROR",
		"internal message",
		cause,
	)

	if appErr.Error() != "underlying cause" {
		t.Fatalf("expected error string to be underlying cause, got %q", appErr.Error())
	}
	if !errors.Is(appErr, cause) {
		t.Fatalf("expected errors.Is to match unwrapped cause")
	}
	if appErr.Unwrap() != cause {
		t.Fatalf("expected Unwrap() to return cause")
	}

	errWithoutCause := NewError(
		http.StatusBadRequest,
		"INVALID_FORMAT",
		"invalid format",
		nil,
	)
	if errWithoutCause.Error() != "INVALID_FORMAT" {
		t.Fatalf("expected Error() to return Code when cause is nil, got %q", errWithoutCause.Error())
	}
	if errWithoutCause.Unwrap() != nil {
		t.Fatalf("expected Unwrap() to return nil")
	}
}

func TestLookupMessageAndResolveLocale(t *testing.T) {
	if loc := ResolveLocale("zh-CN,zh;q=0.9"); loc != "zh-Hans" {
		t.Fatalf("expected zh-Hans, got %s", loc)
	}
	if loc := ResolveLocale("zh-TW,zh-HK;q=0.9"); loc != "zh-Hant" {
		t.Fatalf("expected zh-Hant, got %s", loc)
	}
	if loc := ResolveLocale("en-US,en;q=0.8"); loc != "en" {
		t.Fatalf("expected en, got %s", loc)
	}
	if loc := ResolveLocale("fr-FR"); loc != "en" {
		t.Fatalf("expected fallback en for unsupported locale, got %s", loc)
	}
	if loc := ResolveLocale(""); loc != "en" {
		t.Fatalf("expected fallback en for empty header, got %s", loc)
	}

	if msg, ok := LookupMessage("OK", "en"); !ok || msg != "Success" {
		t.Fatalf("expected OK -> Success, got %q (ok=%v)", msg, ok)
	}
	if msg, ok := LookupMessage("OK", "zh-Hans"); !ok || msg != "成功" {
		t.Fatalf("expected OK -> 成功, got %q (ok=%v)", msg, ok)
	}
	if _, ok := LookupMessage("NON_EXISTENT_CODE", "en"); ok {
		t.Fatalf("expected ok=false for non-existent code")
	}
}
