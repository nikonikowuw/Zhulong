package httpmiddleware

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
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
	request := httptest.NewRequest(http.MethodGet, "/health?token=secret", nil)
	request.Header.Set("X-Request-ID", "request-123")
	router.ServeHTTP(recorder, request)

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("expected one access log entry, got %d", len(entries))
	}
	entry := entries[0]
	if entry.Level != zap.InfoLevel {
		t.Fatalf("expected access events to remain info level for 400 responses, got %s", entry.Level)
	}
	fields := entry.ContextMap()
	for name, want := range map[string]any{
		"requestId": "request-123",
		"method":    http.MethodGet,
		"route":     "/health",
		"status":    int64(http.StatusBadRequest),
		"bytes":     int64(len("invalid")),
	} {
		if got := fields[name]; got != want {
			t.Errorf("field %q: expected %#v, got %#v", name, want, got)
		}
	}
	if _, exists := fields["latency_ms"]; !exists {
		t.Fatalf("missing latency_ms from access log: %#v", fields)
	}
	if _, exists := fields["path"]; exists {
		t.Fatalf("raw path should not be logged: %#v", fields)
	}
	if strings.Contains(entry.Message, "secret") {
		t.Fatalf("query string leaked into access event: %q", entry.Message)
	}
}

func TestAccessLogAndWriteErrorShareRequestIDAndSingleErrorOwnership(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core, observed := observer.New(zap.DebugLevel)
	logger := zap.New(core)
	restore := zap.ReplaceGlobals(logger)
	defer restore()

	router := gin.New()
	router.Use(Recovery(logger), RequestID(), AccessLog(logger))
	router.GET("/api/v1/records/:id", func(c *gin.Context) {
		httputil.WriteError(c, httputil.NewError(http.StatusInternalServerError, "DATABASE_ERROR", "safe", nil))
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/records/42?token=private", nil)
	request.Header.Set("X-Request-ID", "req-failure-42")
	router.ServeHTTP(recorder, request)

	entries := observed.All()
	if len(entries) != 2 {
		t.Fatalf("got %d events, want one error and one access event", len(entries))
	}
	if entries[0].Level != zap.ErrorLevel || entries[0].Message != "HTTP request failed" {
		t.Fatalf("first event is not the single WriteError event: %+v", entries[0])
	}
	if entries[1].Level != zap.InfoLevel || entries[1].Message != "HTTP request" {
		t.Fatalf("access event must remain Info: %+v", entries[1])
	}
	for i, entry := range entries {
		fields := entry.ContextMap()
		if fields["requestId"] != "req-failure-42" {
			t.Errorf("entry %d requestId = %#v", i, fields["requestId"])
		}
		if fields["status"] != int64(http.StatusInternalServerError) {
			t.Errorf("entry %d status = %#v", i, fields["status"])
		}
	}
	if fields := entries[0].ContextMap(); fields["code"] != "DATABASE_ERROR" {
		t.Errorf("error code = %#v", fields["code"])
	}
	accessFields := entries[1].ContextMap()
	if accessFields["route"] != "/api/v1/records/:id" || accessFields["latency_ms"] == nil {
		t.Errorf("access event lacks route template/latency_ms: %#v", accessFields)
	}
	if _, exists := accessFields["path"]; exists {
		t.Errorf("access event must not log raw path: %#v", accessFields)
	}
}

func TestRecoveryLeavesErrorOwnershipToWriteError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core, observed := observer.New(zap.DebugLevel)
	logger := zap.New(core)
	restore := zap.ReplaceGlobals(logger)
	defer restore()

	router := gin.New()
	router.Use(Recovery(logger), RequestID(), AccessLog(logger))
	router.GET("/panic", func(*gin.Context) { panic("private panic detail") })
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/panic", nil)
	request.Header.Set("X-Request-ID", "req-panic")
	router.ServeHTTP(recorder, request)

	entries := observed.All()
	if len(entries) != 2 {
		t.Fatalf("panic must produce one WriteError event and one access event, got %+v", entries)
	}
	if entries[0].Level != zap.ErrorLevel || entries[0].Message != "HTTP request failed" {
		t.Fatalf("panic must produce exactly one WriteError event, got %+v", entries)
	}
	if entries[1].Level != zap.InfoLevel || entries[1].Message != "HTTP request" {
		t.Fatalf("panic access event must remain Info: %+v", entries[1])
	}
	fields := entries[0].ContextMap()
	if fields["requestId"] != "req-panic" || fields["code"] != "INTERNAL_ERROR" {
		t.Fatalf("panic error event lacks stable correlation: %#v", fields)
	}
	if fields["panic"] != "private panic detail" || fields["stack"] == "" {
		t.Fatalf("single panic error event lacks internal panic detail and stack: %#v", fields)
	}
	accessFields := entries[1].ContextMap()
	if accessFields["requestId"] != "req-panic" || accessFields["route"] != "/panic" || accessFields["status"] != int64(http.StatusInternalServerError) || accessFields["latency_ms"] == nil {
		t.Fatalf("panic access event lacks safe request metadata: %#v", accessFields)
	}
	if _, exists := accessFields["path"]; exists {
		t.Fatalf("panic access event must not log a raw path: %#v", accessFields)
	}
}

func TestAccessLogSSEEstablishedAndFailedHandshake(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name        string
		established bool
	}{
		{name: "established", established: true},
		{name: "failed handshake", established: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			core, observed := observer.New(zap.DebugLevel)
			router := gin.New()
			router.Use(RequestID(), AccessLog(zap.New(core)))
			router.GET("/api/v1/cameras/events", func(c *gin.Context) {
				if test.established {
					c.Header("Content-Type", "text/event-stream; charset=utf-8")
					c.String(http.StatusOK, "event: ready\\n\\n")
					return
				}
				c.String(http.StatusServiceUnavailable, "unavailable")
			})
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/api/v1/cameras/events", nil)
			request.Header.Set("X-Request-ID", "req-sse")
			router.ServeHTTP(recorder, request)

			entries := observed.All()
			if len(entries) != 2 || entries[0].Message != "stream.started" || entries[1].Message != "stream.closed" {
				t.Fatalf("unexpected SSE events: %+v", entries)
			}
			var streamID string
			for i, entry := range entries {
				fields := entry.ContextMap()
				if i == 0 || !test.established {
					if fields["requestId"] != "req-sse" {
						t.Fatalf("stream request event %d lost requestId: %#v", i, fields)
					}
				} else if _, present := fields["requestId"]; present {
					t.Fatalf("established stream event inherited HTTP requestId: %#v", fields)
				}
				id, ok := fields["streamId"].(string)
				if !ok || id == "" || (streamID != "" && id != streamID) {
					t.Fatalf("stream event %d lacks a stable streamId: %#v", i, fields)
				}
				streamID = id
			}
			fields := entries[1].ContextMap()
			if fields["established"] != test.established {
				t.Fatalf("established = %#v, want %t", fields["established"], test.established)
			}
			if test.established {
				if fields["session_ms"] == nil || fields["latency_ms"] != nil {
					t.Fatalf("successful SSE must use session_ms only: %#v", fields)
				}
			} else if fields["latency_ms"] == nil || fields["session_ms"] != nil {
				t.Fatalf("failed SSE handshake must use latency_ms only: %#v", fields)
			}
		})
	}
}

func TestAccessLogClosesPanickedSSERequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core, observed := observer.New(zap.DebugLevel)
	logger := zap.New(core)
	router := gin.New()
	router.Use(Recovery(logger), RequestID(), AccessLog(logger))
	router.GET("/api/v1/cameras/events", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream; charset=utf-8")
		panic("stream panic")
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/cameras/events?token=private", nil)
	request.Header.Set("X-Request-ID", "req-stream-panic")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	entries := observed.All()
	if len(entries) != 3 || entries[0].Message != "stream.started" || entries[1].Level != zap.ErrorLevel || entries[1].Message != "HTTP request failed" || entries[2].Message != "stream.closed" {
		t.Fatalf("panicked SSE must emit start, one error, and close events: %+v", entries)
	}
	if entries[1].ContextMap()["requestId"] != "req-stream-panic" {
		t.Fatalf("panic error lost request ID: %#v", entries[1].ContextMap())
	}
	fields := entries[2].ContextMap()
	if fields["requestId"] != "req-stream-panic" || fields["route"] != "/api/v1/cameras/events" || fields["status"] != int64(http.StatusInternalServerError) || fields["established"] != false || fields["latency_ms"] == nil || fields["session_ms"] != nil {
		t.Fatalf("panicked SSE close event has invalid lifecycle metadata: %#v", fields)
	}
	if _, exists := fields["path"]; exists {
		t.Fatalf("panicked SSE close event logged a raw path: %#v", fields)
	}
}

func TestAccessLogWebSocketEstablishedAndFailedHandshake(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core, observed := observer.New(zap.DebugLevel)
	router := gin.New()
	router.Use(RequestID(), AccessLog(zap.New(core)))
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	router.GET("/cameras/:id/ws", func(c *gin.Context) {
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err == nil {
			_ = conn.Close()
		}
	})
	server := httptest.NewServer(router)
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/cameras/cam-1/ws"
	conn, response, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"X-Request-ID": []string{"req-websocket"}})
	if err != nil {
		server.Close()
		t.Fatalf("WebSocket handshake: %v (response=%v)", err, response)
	}
	_ = conn.Close()
	server.Close()

	entries := observed.All()
	if len(entries) != 2 || entries[0].Message != "stream.started" || entries[1].Message != "stream.closed" {
		t.Fatalf("unexpected WebSocket events: %+v", entries)
	}
	var streamID string
	for i, entry := range entries {
		fields := entry.ContextMap()
		if i == 0 {
			if fields["requestId"] != "req-websocket" {
				t.Fatalf("stream start event lost requestId: %#v", fields)
			}
		} else if _, present := fields["requestId"]; present {
			t.Fatalf("established stream event inherited HTTP requestId: %#v", fields)
		}
		id, ok := fields["streamId"].(string)
		if !ok || id == "" || (streamID != "" && id != streamID) {
			t.Fatalf("stream event %d lacks a stable streamId: %#v", i, fields)
		}
		streamID = id
	}
	fields := entries[1].ContextMap()
	if fields["established"] != true || fields["session_ms"] == nil || fields["latency_ms"] != nil {
		t.Fatalf("successful WebSocket must use session_ms only: %#v", fields)
	}

	failedCore, failedLogs := observer.New(zap.DebugLevel)
	failedRouter := gin.New()
	failedRouter.Use(RequestID(), AccessLog(zap.New(failedCore)))
	failedRouter.GET("/cameras/:id/ws", func(*gin.Context) {})
	failedResponse := httptest.NewRecorder()
	failedRequest := httptest.NewRequest(http.MethodGet, "/cameras/cam-2/ws", nil)
	failedRequest.Header.Set("X-Request-ID", "req-failed-websocket")
	failedRouter.ServeHTTP(failedResponse, failedRequest)
	failedEntries := failedLogs.All()
	if len(failedEntries) != 2 || failedEntries[1].Message != "stream.closed" {
		t.Fatalf("failed WebSocket handshake events: %+v", failedEntries)
	}
	failedFields := failedEntries[1].ContextMap()
	if failedFields["requestId"] != "req-failed-websocket" {
		t.Fatalf("failed handshake access event lost requestId: %#v", failedFields)
	}
	if id, ok := failedFields["streamId"].(string); !ok || id == "" {
		t.Fatalf("failed stream event lacks streamId: %#v", failedFields)
	}
	if failedFields["established"] != false || failedFields["latency_ms"] == nil || failedFields["session_ms"] != nil {
		t.Fatalf("failed WebSocket handshake fields: %#v", failedFields)
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
