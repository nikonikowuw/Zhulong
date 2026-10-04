package httputil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSuccessResponseUsesRequiredEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	context.Request.Header.Set("Accept-Language", "zh-Hans")

	Success(context, map[string]string{"status": "ok"})

	var body map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body) != 3 {
		t.Fatalf("expected exactly 3 response fields, got %v", body)
	}
	if string(body["code"]) != `"OK"` || string(body["message"]) != `"成功"` {
		t.Fatalf("unexpected response metadata: %s", recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Language"); got != "zh-Hans" {
		t.Fatalf("expected Content-Language zh-Hans, got %q", got)
	}
}
