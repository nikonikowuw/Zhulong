package httpmiddleware

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestRequestIDPropagatesValidValueAndReplacesInvalidValue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestID())
	router.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, "%s|%s", GetRequestID(c), RequestIDFromContext(c.Request.Context()))
	})

	tests := []struct {
		name       string
		requestID  string
		wantID     string
		wantFormat bool
	}{
		{name: "valid incoming ID", requestID: "edge-node_1.abc", wantID: "edge-node_1.abc"},
		{name: "invalid incoming ID", requestID: "bad\nvalue", wantFormat: true},
		{name: "oversized incoming ID", requestID: string(make([]byte, 129)), wantFormat: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("X-Request-ID", test.requestID)
			router.ServeHTTP(recorder, request)

			gotID := recorder.Header().Get("X-Request-ID")
			if test.wantFormat && !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(gotID) {
				t.Fatalf("expected generated hex request ID, got %q", gotID)
			}
			if !test.wantFormat && gotID != test.wantID {
				t.Fatalf("expected request ID %q, got %q", test.wantID, gotID)
			}
			if recorder.Body.String() != gotID+"|"+gotID {
				t.Fatalf("Gin/standard context did not share request ID: body=%q header=%q", recorder.Body.String(), gotID)
			}
		})
	}
}

func TestAccessLogRecordsRequestMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core, logs := observer.New(zap.DebugLevel)
	router := gin.New()
	router.Use(RequestID(), AccessLog(zap.New(core)))
	router.GET("/health", func(c *gin.Context) {
		c.String(http.StatusBadRequest, "invalid")
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set("X-Request-ID", "request-123")
	router.ServeHTTP(recorder, request)

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("expected one access log entry, got %d", len(entries))
	}
	entry := entries[0]
	if entry.Level != zap.WarnLevel {
		t.Fatalf("expected warning for 400 response, got %s", entry.Level)
	}
	fields := entry.ContextMap()
	for name, want := range map[string]any{
		"requestId": "request-123",
		"method":    http.MethodGet,
		"path":      "/health",
		"status":    int64(http.StatusBadRequest),
		"bytes":     int64(len("invalid")),
	} {
		if got := fields[name]; got != want {
			t.Errorf("field %q: expected %#v, got %#v", name, want, got)
		}
	}
}

func TestRecoveryWritesSanitizedInternalError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Recovery(zap.NewNop()))
	router.GET("/panic", func(*gin.Context) { panic("private panic detail") })
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/panic", nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 response, got %d", recorder.Code)
	}
	if body := recorder.Body.String(); regexp.MustCompile(`private panic detail`).MatchString(body) {
		t.Fatalf("panic detail leaked to response: %s", body)
	}
}
