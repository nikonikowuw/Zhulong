package clientlog

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/httpmiddleware"
	"github.com/nikonikowuw/Zhulong/internal/logsafe"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// newTestServer builds the route with an observable logger and no rate limiting,
// so schema and body limits can be tested independently of budgets.
func newTestServer(t *testing.T) (*gin.Engine, *observer.ObservedLogs) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	core, logs := observer.New(zapcore.DebugLevel)
	// Mirror the production option set, so global policy interactions (notably
	// AddStacktrace) are actually exercised by these tests.
	logger := zap.New(core, zap.AddStacktrace(zapcore.ErrorLevel))

	router := gin.New()
	router.Use(httpmiddleware.RequestID())
	api := router.Group("/api/v1")
	handler := NewHandler(logger, nil)
	handler.RegisterRoutes(api)
	return router, logs
}

func post(t *testing.T, router *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/client-logs", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func validEntry() map[string]any {
	return map[string]any{
		"level": "error",
		"msg":   "server.error",
		"ts":    "2026-10-20T10:00:00.000Z",
		"route": "cameras.list",
		"fields": map[string]any{
			"status": 503,
			"code":   "SERVICE_UNAVAILABLE",
		},
	}
}

func payload(entries ...map[string]any) string {
	if len(entries) == 0 {
		entries = []map[string]any{validEntry()}
	}
	body, _ := json.Marshal(map[string]any{"entries": entries})
	return string(body)
}

func TestIngestAcceptsValidBatch(t *testing.T) {
	router, logs := newTestServer(t)
	recorder := post(t, router, payload())

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Code != "OK" {
		t.Fatalf("envelope code = %q, want OK", envelope.Code)
	}

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("log records = %d, want 1", len(entries))
	}
	context := entries[0].ContextMap()
	if context["source"] != sourceWeb {
		t.Fatalf("source = %v, want %q", context["source"], sourceWeb)
	}
	if context["routeCategory"] != "cameras.list" {
		t.Fatalf("routeCategory = %v", context["routeCategory"])
	}
	if _, ok := context["status"]; !ok {
		t.Fatal("expected allowlisted field status in log context")
	}
}

func TestIngestRejectsTooManyEntries(t *testing.T) {
	router, _ := newTestServer(t)
	entries := make([]map[string]any, 51)
	for i := range entries {
		entries[i] = validEntry()
	}
	recorder := post(t, router, payload(entries...))
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", recorder.Code)
	}
}

func TestIngestRejectsOversizedEventID(t *testing.T) {
	router, _ := newTestServer(t)
	entry := validEntry()
	entry["msg"] = strings.Repeat("a", 65)
	if recorder := post(t, router, payload(entry)); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", recorder.Code)
	}
}

// TestIngestRejectsProseEventID pins the contract that msg is an event
// identifier, not free text. Prose must be refused rather than logged.
func TestIngestRejectsProseEventID(t *testing.T) {
	router, logs := newTestServer(t)
	entry := validEntry()
	entry["msg"] = "failed to load cameras"
	recorder := post(t, router, payload(entry))
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", recorder.Code)
	}
	for _, record := range logs.All() {
		if strings.Contains(record.Message, "failed to load cameras") {
			t.Fatal("rejected prose leaked into the log")
		}
	}
}

func TestIngestRejectsTooManyFields(t *testing.T) {
	router, _ := newTestServer(t)
	entry := validEntry()
	fields := map[string]any{}
	for i := 0; i < 17; i++ {
		fields[fmt.Sprintf("status%d", i)] = i
	}
	entry["fields"] = fields
	if recorder := post(t, router, payload(entry)); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", recorder.Code)
	}
}

func TestIngestRejectsOversizedFieldValue(t *testing.T) {
	router, _ := newTestServer(t)
	entry := validEntry()
	entry["fields"] = map[string]any{"code": strings.Repeat("x", 257)}
	if recorder := post(t, router, payload(entry)); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", recorder.Code)
	}
}

func TestIngestRejectsOversizedBody(t *testing.T) {
	router, _ := newTestServer(t)
	entry := validEntry()
	entry["fields"] = map[string]any{"code": strings.Repeat("x", 200)}
	// Build a body beyond 64 KiB while staying valid JSON structurally.
	var builder strings.Builder
	builder.WriteString(`{"entries":[`)
	for i := 0; i < 400; i++ {
		if i > 0 {
			builder.WriteString(",")
		}
		builder.WriteString(payload(entry)[len(`{"entries":[`) : len(payload(entry))-len(`]}`)])
	}
	builder.WriteString(`]}`)
	if builder.Len() <= int(MaxRequestBodyBytes) {
		t.Fatalf("test body is only %d bytes; must exceed %d", builder.Len(), MaxRequestBodyBytes)
	}
	recorder := post(t, router, builder.String())
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", recorder.Code)
	}
}

func TestIngestRejectsUnknownField(t *testing.T) {
	router, _ := newTestServer(t)
	entry := validEntry()
	entry["fields"] = map[string]any{"password": "hunter2"}
	recorder := post(t, router, payload(entry))
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "hunter2") {
		t.Fatal("response echoed client field value")
	}
}

func TestIngestRejectsNonScalarField(t *testing.T) {
	router, _ := newTestServer(t)
	entry := validEntry()
	entry["fields"] = map[string]any{"code": map[string]any{"nested": "value"}}
	if recorder := post(t, router, payload(entry)); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", recorder.Code)
	}
}

func TestIngestRejectsRouteWithQueryOrPath(t *testing.T) {
	router, _ := newTestServer(t)
	for _, route := range []string{
		"cameras.list?page=2",
		"cameras/123",
		"cameras.list#frag",
	} {
		entry := validEntry()
		entry["route"] = route
		if recorder := post(t, router, payload(entry)); recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("route %q: status = %d, want 422", route, recorder.Code)
		}
	}
}

func TestIngestRejectsDroppedOutOfRange(t *testing.T) {
	router, _ := newTestServer(t)
	for _, dropped := range []string{"4294967296", "-1"} {
		body := fmt.Sprintf(`{"dropped":%s,"entries":[%s]}`, dropped, entryJSON(t))
		recorder := post(t, router, body)
		if recorder.Code != http.StatusUnprocessableEntity && recorder.Code != http.StatusBadRequest {
			t.Fatalf("dropped=%s: status = %d, want 4xx", dropped, recorder.Code)
		}
	}
}

func entryJSON(t *testing.T) string {
	t.Helper()
	body, err := json.Marshal(validEntry())
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	return string(body)
}

// TestIngestDoesNotEchoBody pins the rule that the endpoint never reflects
// client-supplied content, on both success and failure.
func TestIngestDoesNotEchoBody(t *testing.T) {
	router, _ := newTestServer(t)
	secret := "super-secret-value"
	entry := validEntry()
	entry["fields"] = map[string]any{"code": secret}

	ok := post(t, router, payload(entry))
	if strings.Contains(ok.Body.String(), secret) {
		t.Fatal("success response echoed client content")
	}

	bad := validEntry()
	bad["msg"] = secret + " with spaces"
	failed := post(t, router, payload(bad))
	if strings.Contains(failed.Body.String(), secret) {
		t.Fatal("error response echoed client content")
	}
}

// TestIngestLogsMaskedClientIP verifies the raw address never reaches the log.
func TestIngestLogsMaskedClientIP(t *testing.T) {
	router, logs := newTestServer(t)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/client-logs", strings.NewReader(payload()))
	request.Header.Set("Content-Type", "application/json")
	request.RemoteAddr = "203.0.113.77:54321"
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	for _, record := range logs.All() {
		context := record.ContextMap()
		if clientIP, ok := context["clientIp"]; ok {
			if clientIP == "203.0.113.77" {
				t.Fatal("logged the raw client IP")
			}
			if clientIP != "203.0.113.0/24" {
				t.Fatalf("clientIp = %v, want masked network", clientIP)
			}
		}
	}
}

// TestIngestControlCharactersAreFlattened proves a field value cannot inject a
// second log line, even though JSON already escapes CRLF for the JSON encoder.
func TestIngestControlCharactersAreFlattened(t *testing.T) {
	router, logs := newTestServer(t)
	entry := validEntry()
	entry["fields"] = map[string]any{"code": "line1\r\nline2"}
	post(t, router, payload(entry))

	for _, record := range logs.All() {
		for key, value := range record.ContextMap() {
			text, ok := value.(string)
			if !ok {
				continue
			}
			if strings.ContainsAny(text, "\r\n") {
				t.Fatalf("field %s contains a raw newline: %q", key, text)
			}
		}
	}
}

// TestIngestCreatesNoAuditRecord documents that the ingestion path has no audit
// hook at all: the handler never calls audithttp.Tag and this package does not
// import internal/audit. A 200 response therefore cannot produce an audit row.
func TestIngestCreatesNoAuditRecord(t *testing.T) {
	router, _ := newTestServer(t)
	if recorder := post(t, router, payload()); recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
}

// TestRateLimitedEndpointReturns429 covers the budget path end to end, including
// the standard envelope and the absence of any body echo.
func TestRateLimitedEndpointReturns429(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limiter := httpmiddleware.NewIPRateLimiter(httpmiddleware.RateLimitConfig{
		PerMinute: 60, Burst: 1, MaxKeys: 8,
	})
	handler := NewHandler(zap.NewNop(), limiter)

	router := gin.New()
	router.Use(httpmiddleware.RequestID())
	handler.RegisterRoutes(router.Group("/api/v1"))

	first := post(t, router, payload())
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200", first.Code)
	}
	second := post(t, router, payload())
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want 429", second.Code)
	}
	var envelope struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode 429 envelope: %v", err)
	}
	if envelope.Code != "TOO_MANY_REQUESTS" {
		t.Fatalf("code = %q, want TOO_MANY_REQUESTS", envelope.Code)
	}
	if envelope.Message == "" {
		t.Fatal("429 envelope has no localized message")
	}
}

func TestIngestRejectsEmptyEntries(t *testing.T) {
	router, _ := newTestServer(t)
	recorder := post(t, router, `{"entries":[]}`)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", recorder.Code)
	}
}

func TestIngestRejectsUnknownTopLevelField(t *testing.T) {
	router, _ := newTestServer(t)
	body := fmt.Sprintf(`{"entries":[%s],"unexpected":"value"}`, entryJSON(t))
	recorder := post(t, router, body)
	if recorder.Code != http.StatusUnprocessableEntity && recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 4xx", recorder.Code)
	}
}

func TestValidateWarnLevel(t *testing.T) {
	entries, _, err := validate([]byte(payload(map[string]any{"level": "warn", "msg": "query.retry"})))
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if entries[0].Level != levelWarn {
		t.Fatalf("level = %q, want warn", entries[0].Level)
	}
}

func TestValidateRejectsInfoLevel(t *testing.T) {
	if _, _, err := validate([]byte(payload(map[string]any{"level": "info", "msg": "a.b"}))); err == nil {
		t.Fatal("expected info level to be rejected")
	}
}

// TestIngestLogsCarryNoServerStacktrace pins that client-reported events never
// get the global ErrorLevel stacktrace. A browser report has no meaningful
// server stack, and attaching one would leak absolute build paths into logs on
// every client error — the exact class of path disclosure this task forbids.
const logsafeIPMarker = logsafe.IPMarker

func TestIngestLogsCarryNoServerStacktrace(t *testing.T) {
	router, logs := newTestServer(t)
	if recorder := post(t, router, payload()); recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if len(logs.All()) == 0 {
		t.Fatal("expected at least one log record")
	}
	for _, record := range logs.All() {
		stack := record.Stack
		if stack != "" {
			t.Fatalf("client event carries a server stacktrace: %q", stack)
		}
	}
}

// TestIngestFallsBackToRemoteAddrForClientIP covers the case where gin's
// ClientIP() returns empty: the raw socket address must still be masked rather
// than collapsing to the "could not parse" marker, which would hide the fact
// that address resolution is broken.
func TestIngestFallsBackToRemoteAddrForClientIP(t *testing.T) {
	router, logs := newTestServer(t)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/client-logs", strings.NewReader(payload()))
	request.Header.Set("Content-Type", "application/json")
	// Simulate a context with no resolved client IP but a real socket peer.
	request.RemoteAddr = "198.51.100.23:4242"
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	found := false
	for _, record := range logs.All() {
		if value, ok := record.ContextMap()["clientIp"]; ok {
			found = true
			if value == logsafeIPMarker {
				t.Fatal("client IP fell back to the unparseable marker instead of masking")
			}
		}
	}
	if !found {
		t.Fatal("no clientIp field was logged")
	}
}
