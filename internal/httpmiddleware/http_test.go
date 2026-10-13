package httpmiddleware

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
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

func TestMaxBodyLimitWithLimitAndCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const testLimit int64 = 64

	router := gin.New()
	router.Use(MaxBodyLimitWithLimitAndCode(testLimit, "FILE_TOO_LARGE"))
	router.POST("/upload", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	req := httptest.NewRequest(http.MethodPost, "/upload", nil)
	req.ContentLength = testLimit + 1
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for oversized upload, got %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), `"code":"FILE_TOO_LARGE"`) {
		t.Fatalf("expected FILE_TOO_LARGE response code, got %s", recorder.Body.String())
	}
}

func TestMaxBodyLimitWithLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const testLimit int64 = 64

	router := gin.New()
	router.Use(MaxBodyLimitWithLimit(testLimit))
	router.POST("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req1 := httptest.NewRequest(http.MethodPost, "/test", httptest.NewRequest(http.MethodPost, "/", nil).Body)
	req1.ContentLength = 32
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("expected 200 for payload within custom limit, got %d", rec1.Code)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/test", nil)
	req2.ContentLength = testLimit + 1
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for payload exceeding custom limit, got %d", rec2.Code)
	}
}

// TestPanicSkipsOuterMiddlewareContinuation documents why the audit layer cannot
// observe panics from its own c.Next() continuation.
//
// A panicking handler never advances gin's handler index, so the outer
// middleware's post-Next code is skipped entirely: its c.Next() unwinds by
// panic, Recovery resets the index to zero, and the resumed loop finds itself
// at handlers[0] and stops immediately.
func TestPanicSkipsOuterMiddlewareContinuation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var (
		continuationRan bool
		observedStatus  = -1
	)

	router := gin.New()
	router.Use(Recovery(zap.NewNop()))
	router.Use(func(c *gin.Context) {
		c.Next()
		// This must not run for a panicking handler.
		continuationRan = true
		observedStatus = c.Writer.Status()
	})
	router.POST("/boom", func(c *gin.Context) { panic("boom") })

	req := httptest.NewRequest(http.MethodPost, "/boom", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected the client to receive 500, got %d", recorder.Code)
	}
	if continuationRan {
		t.Fatalf("continuation unexpectedly ran with status %d; the documented gin behaviour changed", observedStatus)
	}
}

// TestPanicDeferredReadSeesDefaultStatus documents the second trap: reading the
// status from a deferred function does run, but reports the default 200 because
// no response header has been written when the panic unwinds. Recording that
// value would mark a crashed request as successful, which is worse than
// omitting it.
func TestPanicDeferredReadSeesDefaultStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)

	observedStatus := -1
	router := gin.New()
	router.Use(Recovery(zap.NewNop()))
	router.Use(func(c *gin.Context) {
		defer func() { observedStatus = c.Writer.Status() }()
		c.Next()
	})
	router.POST("/boom", func(c *gin.Context) { panic("boom") })

	req := httptest.NewRequest(http.MethodPost, "/boom", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected the client to receive 500, got %d", recorder.Code)
	}
	if observedStatus != http.StatusOK {
		t.Fatalf("expected the deferred read to observe the default 200, got %d", observedStatus)
	}
}

// TestRecoveryNotifiesPanicObserver asserts the supported mechanism: the
// observer runs after the 500 response is written and therefore sees the real
// status code.
func TestRecoveryNotifiesPanicObserver(t *testing.T) {
	gin.SetMode(gin.TestMode)

	observer := &recordingObserver{}
	router := gin.New()
	router.Use(Recovery(zap.NewNop(), observer))
	router.POST("/boom", func(c *gin.Context) { panic("boom") })

	req := httptest.NewRequest(http.MethodPost, "/boom", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if !observer.called {
		t.Fatal("expected the panic observer to be notified")
	}
	if observer.status != http.StatusInternalServerError {
		t.Fatalf("expected the observer to see 500, got %d", observer.status)
	}
}

// TestRecoveryWithoutObserverStillRecovers keeps the optional-observer contract
// explicit: existing single-argument call sites continue to work.
func TestRecoveryWithoutObserverStillRecovers(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(Recovery(zap.NewNop()))
	router.POST("/boom", func(c *gin.Context) { panic("boom") })

	req := httptest.NewRequest(http.MethodPost, "/boom", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), `"code":"INTERNAL_ERROR"`) {
		t.Fatalf("expected a sanitized INTERNAL_ERROR response, got %s", recorder.Body.String())
	}
}

type recordingObserver struct {
	called bool
	status int
}

func (o *recordingObserver) OnPanic(c *gin.Context) {
	o.called = true
	o.status = c.Writer.Status()
}
