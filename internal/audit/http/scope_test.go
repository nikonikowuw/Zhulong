package audithttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/httpmiddleware"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

// harness bundles a test router with the audit service backing it.
type harness struct {
	router  *gin.Engine
	service *audit.Service
}

// harnessConfig is the middleware arrangement a harness builds; options adjust
// it instead of forcing tests to re-implement the wiring.
type harnessConfig struct {
	recovery   gin.HandlerFunc
	middleware []gin.HandlerFunc
}

// harnessOption adjusts how newHarness wires the router.
type harnessOption func(*harnessConfig)

// withBodyLimit appends a body-limit middleware after the audit middleware, so a
// request rejected while its body is read is still audited. It also swaps in
// Gin's recovery: these tests never panic, so the panic observer is not needed
// and using it here would blur what the assertion covers.
func withBodyLimit(limit int64, errorCode string) harnessOption {
	return func(cfg *harnessConfig) {
		cfg.recovery = gin.Recovery()
		cfg.middleware = append(cfg.middleware, httpmiddleware.MaxBodyLimitWithLimitAndCode(limit, errorCode))
	}
}

func newHarness(
	t *testing.T,
	configure func(api *gin.RouterGroup, service *audit.Service),
	opts ...harnessOption,
) *harness {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbStore := database.New(t.TempDir(), zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	service := audit.NewService(audit.NewStore(dbStore), zap.NewNop(), audit.Config{
		BufferSize:    100,
		BatchSize:     10,
		FlushInterval: 5 * time.Millisecond,
		MaxEntries:    1000,
	})
	if err := service.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() {
		if err := service.Stop(context.Background()); err != nil {
			t.Logf("stop audit service: %v", err)
		}
		if err := dbStore.Close(); err != nil {
			t.Logf("close test database: %v", err)
		}
	})

	cfg := harnessConfig{
		recovery:   httpmiddleware.Recovery(zap.NewNop(), NewPanicObserver(service)),
		middleware: []gin.HandlerFunc{Middleware(service)},
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	router := gin.New()
	// Mirror production: this deployment has no reverse proxy, so forwarding
	// headers must not be trusted. Gin's default trusts 0.0.0.0/0 and ::/0.
	if err := router.SetTrustedProxies(nil); err != nil {
		t.Fatalf("SetTrustedProxies failed: %v", err)
	}
	router.Use(cfg.recovery)
	api := router.Group("/api/v1")
	api.Use(cfg.middleware...)
	configure(api, service)

	return &harness{router: router, service: service}
}

// records stops the service so the asynchronous queue is drained, then returns
// all persisted audit entries, newest first.
//
// Draining before reading keeps assertions deterministic: recording through the
// middleware is intentionally non-blocking, so entries may still be in flight
// immediately after the response is written.
func (h *harness) records(t *testing.T) []audit.AuditLog {
	t.Helper()
	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := h.service.Stop(stopCtx); err != nil {
		t.Fatalf("drain failed: %v", err)
	}
	items, _, err := h.service.List(context.Background(), audit.Filter{Limit: 100})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	return items
}

func do(router *gin.Engine, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestMiddlewareRecordsSuccessWithClientIP(t *testing.T) {
	h := newHarness(t, func(api *gin.RouterGroup, _ *audit.Service) {
		api.POST("/cameras", func(c *gin.Context) {
			Tag(c, audit.ActionCameraCreate, "camera:new", "created")
			httputil.Success(c, gin.H{"id": "new"})
		})
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/cameras", nil)
	req.RemoteAddr = "192.168.5.20:45678"
	// A forged forwarding header must be ignored: this deployment has no proxy.
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	req.Header.Set("X-Real-IP", "203.0.113.9")

	if w := do(h.router, req); w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	records := h.records(t)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	got := records[0]
	if got.IP != "192.168.5.20" {
		t.Fatalf("expected TCP source IP, got %q (forwarding headers must be ignored)", got.IP)
	}
	if got.Username != audit.UsernameAnonymous {
		t.Fatalf("expected %q for an unauthenticated request, got %q", audit.UsernameAnonymous, got.Username)
	}
	if got.Status != audit.StatusSuccess {
		t.Fatalf("expected success, got %q", got.Status)
	}
	if got.Action != audit.ActionCameraCreate || got.Target != "camera:new" {
		t.Fatalf("unexpected action/target: %q/%q", got.Action, got.Target)
	}
}

func TestMiddlewareSkipsRequestsWithoutTag(t *testing.T) {
	h := newHarness(t, func(api *gin.RouterGroup, _ *audit.Service) {
		api.GET("/cameras", func(c *gin.Context) {
			// Read-only endpoint: no Tag, so nothing is audited.
			httputil.Success(c, gin.H{"items": []string{}})
		})
	})

	if w := do(h.router, httptest.NewRequest(http.MethodGet, "/api/v1/cameras", nil)); w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	if records := h.records(t); len(records) != 0 {
		t.Fatalf("expected no records for an untagged request, got %d", len(records))
	}
}

func TestMiddlewareRecordsHTTPFailureWithSafeError(t *testing.T) {
	h := newHarness(t, func(api *gin.RouterGroup, _ *audit.Service) {
		api.POST("/cameras", func(c *gin.Context) {
			Tag(c, audit.ActionCameraCreate, "camera:x", "created")
			httputil.WriteError(c, httputil.NewError(
				http.StatusConflict, "CAMERA_ALREADY_EXISTS", "Camera already exists",
				// The internal cause must never reach the audit record.
				errLeaky))
		})
	})

	if w := do(h.router, httptest.NewRequest(http.MethodPost, "/api/v1/cameras", nil)); w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}

	records := h.records(t)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Status != audit.StatusFailed {
		t.Fatalf("expected failed status, got %q", records[0].Status)
	}
	if !strings.Contains(records[0].ErrorMsg, "CAMERA_ALREADY_EXISTS") {
		t.Fatalf("expected stable error code in summary, got %q", records[0].ErrorMsg)
	}
	if strings.Contains(records[0].ErrorMsg, errLeaky.Error()) {
		t.Fatalf("internal error leaked into audit error_msg: %q", records[0].ErrorMsg)
	}
}

func TestMiddlewareRecordsJSONBindFailure(t *testing.T) {
	h := newHarness(t, func(api *gin.RouterGroup, _ *audit.Service) {
		api.POST("/auth/login", TagMiddleware(audit.ActionAuthLogin, "user:", ""), func(c *gin.Context) {
			var body struct {
				Username string `json:"username" binding:"required"`
			}
			if err := c.ShouldBindJSON(&body); err != nil {
				httputil.HandleBindError(c, err)
				return
			}
			httputil.Success(c, gin.H{"ok": true})
		})
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	if w := do(h.router, req); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for a missing required field, got %d: %s", w.Code, w.Body.String())
	}

	records := h.records(t)
	if len(records) != 1 {
		t.Fatalf("expected a record for the declared action even on bind failure, got %d", len(records))
	}
	if records[0].Status != audit.StatusFailed {
		t.Fatalf("expected failed status, got %q", records[0].Status)
	}
	if records[0].ErrorMsg == "" {
		t.Fatal("expected a safe error summary")
	}
}

func TestMiddlewareRecordsDomainFailureOn2xx(t *testing.T) {
	h := newHarness(t, func(api *gin.RouterGroup, _ *audit.Service) {
		api.POST("/system/time/sync", func(c *gin.Context) {
			Tag(c, "time.ntp_sync", "system_time", "")
			// The response stays 200, but the domain operation failed.
			TagError(c, "NTP_SYNC_FAILED", "NTP synchronization failed")
			httputil.Success(c, gin.H{"syncStatus": gin.H{"state": "failed"}})
		})
	})

	if w := do(h.router, httptest.NewRequest(http.MethodPost, "/api/v1/system/time/sync", nil)); w.Code != http.StatusOK {
		t.Fatalf("expected the existing 200 contract to be preserved, got %d", w.Code)
	}

	records := h.records(t)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Status != audit.StatusFailed {
		t.Fatalf("expected failed status for a 2xx domain failure, got %q", records[0].Status)
	}
	if !strings.Contains(records[0].ErrorMsg, "NTP_SYNC_FAILED") {
		t.Fatalf("expected domain error code, got %q", records[0].ErrorMsg)
	}
}

func TestTagActionOverridesDeclaredAction(t *testing.T) {
	h := newHarness(t, func(api *gin.RouterGroup, _ *audit.Service) {
		// One route serves both update and toggle, decided by the body.
		api.PUT("/cameras/:id", TagMiddleware(audit.ActionCameraUpdate, "camera:", ""), func(c *gin.Context) {
			var body struct {
				Enabled *bool `json:"enabled"`
			}
			if err := c.ShouldBindJSON(&body); err != nil {
				httputil.HandleBindError(c, err)
				return
			}
			TagTarget(c, "camera:"+c.Param("id"))
			if body.Enabled != nil {
				TagAction(c, audit.ActionCameraToggle)
				TagDetail(c, "enabled=true")
			}
			httputil.Success(c, gin.H{"ok": true})
		})
	})

	req := httptest.NewRequest(http.MethodPut, "/api/v1/cameras/cam_9", strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	if w := do(h.router, req); w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	records := h.records(t)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Action != audit.ActionCameraToggle {
		t.Fatalf("expected runtime action override to %q, got %q", audit.ActionCameraToggle, records[0].Action)
	}
	if records[0].Target != "camera:cam_9" {
		t.Fatalf("expected updated target, got %q", records[0].Target)
	}
	if records[0].Detail != "enabled=true" {
		t.Fatalf("expected updated detail, got %q", records[0].Detail)
	}
}

func TestTagActorRefinesIdentityAndKeepsIP(t *testing.T) {
	h := newHarness(t, func(api *gin.RouterGroup, _ *audit.Service) {
		api.POST("/auth/login", TagMiddleware(audit.ActionAuthLogin, "user:", ""), func(c *gin.Context) {
			// Successful authentication supplies the configured account name.
			TagActor(c, "operator")
			TagTarget(c, "user:operator")
			httputil.Success(c, gin.H{"ok": true})
		})
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req.RemoteAddr = "10.9.8.7:1234"
	if w := do(h.router, req); w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	records := h.records(t)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Username != "operator" {
		t.Fatalf("expected the configured account name, got %q", records[0].Username)
	}
	if records[0].IP != "10.9.8.7" {
		t.Fatalf("expected the request IP to be preserved, got %q", records[0].IP)
	}
}

func TestFailedLoginKeepsAnonymousActor(t *testing.T) {
	h := newHarness(t, func(api *gin.RouterGroup, _ *audit.Service) {
		api.POST("/auth/login", TagMiddleware(audit.ActionAuthLogin, "user:", ""), func(c *gin.Context) {
			// The attempted username is only a target; the actor stays anonymous.
			TagTarget(c, "user:intruder")
			httputil.WriteError(c, httputil.NewError(
				http.StatusUnauthorized, "INVALID_CREDENTIALS", "Invalid credentials", nil))
		})
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req.RemoteAddr = "10.1.1.1:2222"
	if w := do(h.router, req); w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	records := h.records(t)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Username != audit.UsernameAnonymous {
		t.Fatalf("expected %q, got %q", audit.UsernameAnonymous, records[0].Username)
	}
	if records[0].IP != "10.1.1.1" {
		t.Fatalf("expected the request IP to be retained, got %q", records[0].IP)
	}
	if records[0].Target != "user:intruder" {
		t.Fatalf("expected the attempted username as target, got %q", records[0].Target)
	}
}

func TestPanicIsRecordedAsFailure(t *testing.T) {
	h := newHarness(t, func(api *gin.RouterGroup, _ *audit.Service) {
		api.POST("/cameras", func(c *gin.Context) {
			Tag(c, audit.ActionCameraCreate, "camera:panic", "created")
			panic("boom")
		})
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/cameras", nil)
	req.RemoteAddr = "10.2.3.4:5555"
	if w := do(h.router, req); w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}

	records := h.records(t)
	if len(records) != 1 {
		t.Fatalf("expected a failed record for the panic, got %d", len(records))
	}
	if records[0].Status != audit.StatusFailed {
		t.Fatalf("expected failed status, got %q", records[0].Status)
	}
	if records[0].IP != "10.2.3.4" {
		t.Fatalf("expected the request IP, got %q", records[0].IP)
	}
	if strings.Contains(records[0].ErrorMsg, "boom") {
		t.Fatalf("panic text leaked into audit error_msg: %q", records[0].ErrorMsg)
	}
	if records[0].ErrorMsg == "" {
		t.Fatal("expected a safe error summary for the panic")
	}
}

func TestPanicWithoutTagProducesNoRecord(t *testing.T) {
	h := newHarness(t, func(api *gin.RouterGroup, _ *audit.Service) {
		api.GET("/boom", func(c *gin.Context) {
			panic("untagged")
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/boom", nil)
	if w := do(h.router, req); w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}

	if records := h.records(t); len(records) != 0 {
		t.Fatalf("expected no record for an untagged panic, got %d", len(records))
	}
}

func TestMiddlewareIsRaceFree(t *testing.T) {
	h := newHarness(t, func(api *gin.RouterGroup, _ *audit.Service) {
		api.POST("/cameras", TagMiddleware(audit.ActionCameraCreate, "camera:", ""), func(c *gin.Context) {
			// Concurrent mutation of the scope from several goroutines must be
			// safe; the race detector is the assertion here.
			done := make(chan struct{})
			for i := 0; i < 4; i++ {
				go func() {
					defer func() { done <- struct{}{} }()
					TagDetail(c, "concurrent")
					TagTarget(c, "camera:race")
				}()
			}
			for i := 0; i < 4; i++ {
				<-done
			}
			httputil.Success(c, gin.H{"ok": true})
		})
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/cameras", nil)
	if w := do(h.router, req); w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// errLeaky stands in for an internal error that must never be persisted.
var errLeaky = errors.New("sqlite: UNIQUE constraint failed: cameras.rtsp_url")

// oversizedLoginPayload exceeds the 64-byte limit the body-limit tests install.
func oversizedLoginPayload() string {
	return `{"username":"` + strings.Repeat("a", 200) + `"}`
}

// bindBody fails while reading the body, which is where a chunked overflow
// surfaces: the ContentLength pre-check cannot reject it.
func bindBody(c *gin.Context) {
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		httputil.HandleBindError(c, err)
		return
	}
	httputil.Success(c, gin.H{"ok": true})
}

// TestChunkedBodyOverflowIsAudited covers the path that slips past the
// ContentLength pre-check: a request without a known length is only rejected
// once the body is read, which happens inside the handler after the tag was
// declared. That is a bind failure, so it must be recorded.
func TestChunkedBodyOverflowIsAudited(t *testing.T) {
	h := newHarness(t, func(api *gin.RouterGroup, _ *audit.Service) {
		api.POST("/auth/login", TagMiddleware(audit.ActionAuthLogin, "user:", ""), bindBody)
	}, withBodyLimit(64, "PAYLOAD_TOO_LARGE"))

	// ContentLength is -1, so the pre-check cannot reject the request; the
	// overflow only surfaces while binding.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(oversizedLoginPayload()))
	req.ContentLength = -1
	req.Header.Set("Content-Type", "application/json")
	if w := do(h.router, req); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d: %s", w.Code, w.Body.String())
	}

	records := h.records(t)
	if len(records) != 1 {
		t.Fatalf("expected the bind failure to be audited, got %d records", len(records))
	}
	if records[0].Status != audit.StatusFailed {
		t.Fatalf("expected failed status, got %q", records[0].Status)
	}
}

// TestChunkedBodyOverflowWithoutTagIsSkipped is the counterpart: a request that
// never declared an action stays unrecorded even when it is rejected.
func TestChunkedBodyOverflowWithoutTagIsSkipped(t *testing.T) {
	h := newHarness(t, func(api *gin.RouterGroup, _ *audit.Service) {
		api.POST("/untagged", bindBody)
	}, withBodyLimit(64, "PAYLOAD_TOO_LARGE"))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/untagged", strings.NewReader(oversizedLoginPayload()))
	req.ContentLength = -1
	req.Header.Set("Content-Type", "application/json")
	if w := do(h.router, req); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", w.Code)
	}

	if records := h.records(t); len(records) != 0 {
		t.Fatalf("expected no records for an untagged request, got %d", len(records))
	}
}

func TestTagEmptyActionIsIgnored(t *testing.T) {
	h := newHarness(t, func(api *gin.RouterGroup, _ *audit.Service) {
		api.POST("/empty-tag", func(c *gin.Context) {
			Tag(c, "", "camera:1", "ignored")
			TagAction(c, "")
			httputil.Success(c, gin.H{"ok": true})
		})
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/empty-tag", nil)
	if w := do(h.router, req); w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	if records := h.records(t); len(records) != 0 {
		t.Fatalf("expected empty actions not to emit records, got %d", len(records))
	}
}

func TestPanicObserverHandlesNilRequestAndNilInputs(t *testing.T) {
	store := database.New(t.TempDir(), zap.NewNop())
	if err := store.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Logf("close test database: %v", err)
		}
	})

	svc := audit.NewService(audit.NewStore(store), zap.NewNop(), audit.Config{
		BufferSize:    10,
		BatchSize:     5,
		FlushInterval: 5 * time.Millisecond,
		MaxEntries:    100,
	})
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	var nilObserver *panicObserver
	nilObserver.OnPanic(nil)

	observer := NewPanicObserver(svc)
	observer.OnPanic(nil)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(scopeKey, &scope{declared: true, action: audit.ActionCameraCreate, target: "camera:nilreq"})
	observer.OnPanic(c)

	if err := svc.Stop(context.Background()); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	items, total, err := svc.List(context.Background(), audit.Filter{Limit: 10})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("expected 1 failed audit record, got total=%d len=%d", total, len(items))
	}
	if items[0].Status != audit.StatusFailed || items[0].Username != audit.UsernameUnknown {
		t.Fatalf("unexpected record on nil request panic: %+v", items[0])
	}
}
