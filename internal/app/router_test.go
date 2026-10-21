package app

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/auth"
	"github.com/nikonikowuw/Zhulong/internal/clientlog"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/httpmiddleware"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type readinessStub bool

func (r readinessStub) Ready() bool { return bool(r) }

func TestAppDependencyGraph(t *testing.T) {
	restoreLogger := zap.ReplaceGlobals(zap.NewNop())
	defer restoreLogger()
	if err := fx.ValidateApp(fx.Supply(DefaultConfig()), Module, fx.NopLogger); err != nil {
		t.Fatalf("validate Fx dependency graph: %v", err)
	}
}

func TestRouterSeparatesAPIAndSPA(t *testing.T) {
	gin.SetMode(gin.TestMode)
	assets := http.FS(fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<!doctype html><title>shell</title>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("window.app = true")},
	})
	router := newRouter(RouterParams{
		Logger:        zap.NewNop(),
		DatabaseReady: readinessStub(true),
		NativeReady:   readinessStub(true),
		Assets:        assets,
	})

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantBody   string
	}{
		{name: "root SPA", path: "/", wantStatus: http.StatusOK, wantBody: "<!doctype html>"},
		{name: "client route", path: "/settings/network", wantStatus: http.StatusOK, wantBody: "<!doctype html>"},
		{name: "static asset", path: "/assets/app.js", wantStatus: http.StatusOK, wantBody: "window.app"},
		{name: "missing API route", path: "/api/v1/missing", wantStatus: http.StatusNotFound, wantBody: `"code":"ROUTE_NOT_FOUND"`},
		{name: "missing static asset", path: "/assets/missing.js", wantStatus: http.StatusNotFound, wantBody: "404 page not found"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			router.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", test.wantStatus, recorder.Code, recorder.Body.String())
			}
			if !contains(recorder.Body.String(), test.wantBody) {
				t.Fatalf("expected body to contain %q, got %s", test.wantBody, recorder.Body.String())
			}
			if test.path == "/api/v1/missing" && contains(recorder.Body.String(), "<!doctype html>") {
				t.Fatal("unmatched API route returned SPA HTML")
			}
		})
	}
}

func TestHealthRequiresInitializedDependencies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := newRouter(RouterParams{
		Logger:        zap.NewNop(),
		DatabaseReady: readinessStub(false),
		NativeReady:   readinessStub(true),
		Assets: http.FS(fstest.MapFS{
			"index.html": &fstest.MapFile{Data: []byte("shell")},
		}),
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 before startup, got %d", recorder.Code)
	}
	if contains(recorder.Body.String(), "shell") || !contains(recorder.Body.String(), `"data":null`) {
		t.Fatalf("unexpected health error response: %s", recorder.Body.String())
	}
}

func TestUnknownErrorIsWrappedForClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	httputil.WriteError(context, errors.New("private detail"))
	if recorder.Code != http.StatusInternalServerError || contains(recorder.Body.String(), "private detail") {
		t.Fatalf("unexpected error response: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

type authStubService struct {
	auth.AuthService
}

func (a authStubService) GetStatus(ctx context.Context) (auth.AuthStatusResponse, error) {
	return auth.AuthStatusResponse{Initialized: false}, nil
}

func (a authStubService) SessionTTL() time.Duration {
	return time.Hour
}

func TestRouterMountsAuthEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authStub := authStubService{}
	router := newRouter(RouterParams{
		Logger:        zap.NewNop(),
		DatabaseReady: readinessStub(true),
		NativeReady:   readinessStub(true),
		AuthService:   authStub,
		PublicRoutes:  []RouteRegistrar{auth.NewHandler(authStub)},
		Assets: http.FS(fstest.MapFS{
			"index.html": &fstest.MapFile{Data: []byte("shell")},
		}),
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/status", nil)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200 on /api/v1/auth/status, got %d", recorder.Code)
	}
	if !contains(recorder.Body.String(), `"initialized":false`) {
		t.Fatalf("unexpected body: %s", recorder.Body.String())
	}
}

func TestRouterRejectsOversizedAPIRequestBodies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authStub := authStubService{}
	router := newRouter(RouterParams{
		Logger:        zap.NewNop(),
		DatabaseReady: readinessStub(true),
		NativeReady:   readinessStub(true),
		AuthService:   authStub,
		PublicRoutes:  []RouteRegistrar{auth.NewHandler(authStub)},
		Assets: http.FS(fstest.MapFS{
			"index.html": &fstest.MapFile{Data: []byte("shell")},
		}),
	})
	body := `{"padding":"` + strings.Repeat("a", int(httpmiddleware.MaxAPIRequestBodyBytes)) + `"}`

	for _, knownLength := range []bool{true, false} {
		name := "unknown content length"
		if knownLength {
			name = "known content length"
		}
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/init", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			if !knownLength {
				request.ContentLength = -1
			}
			router.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusRequestEntityTooLarge || !contains(recorder.Body.String(), `"code":"PAYLOAD_TOO_LARGE"`) {
				t.Fatalf("expected localized 413 error response, got %d: %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

type dummyModularRegistrar struct {
	path string
}

func (d dummyModularRegistrar) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET(d.path, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"module": d.path})
	})
}

func TestRouterModularRouteRegistration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authStub := authStubService{}
	router := newRouter(RouterParams{
		Logger:        zap.NewNop(),
		DatabaseReady: readinessStub(true),
		NativeReady:   readinessStub(true),
		AuthService:   authStub,
		PublicRoutes: []RouteRegistrar{
			dummyModularRegistrar{path: "/public-demo"},
		},
		ProtectedRoutes: []RouteRegistrar{
			dummyModularRegistrar{path: "/protected-demo"},
		},
		Assets: http.FS(fstest.MapFS{
			"index.html": &fstest.MapFile{Data: []byte("shell")},
		}),
	})

	// Public route should be reachable without auth
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "/api/v1/public-demo", nil)
	router.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK || !contains(w1.Body.String(), `"module":"/public-demo"`) {
		t.Fatalf("unexpected public modular route response: %d, %s", w1.Code, w1.Body.String())
	}

	// Protected route should require auth (401 unauthorized when unauthenticated)
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/protected-demo", nil)
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on protected modular route without session, got %d", w2.Code)
	}
}

func contains(value, part string) bool { return strings.Contains(value, part) }

// auditRouterFixture builds a real router backed by a live audit service, so the
// integration tests exercise the actual middleware wiring rather than a stub.
func auditRouterFixture(t *testing.T) (*gin.Engine, *audit.Service, *auth.AuthService) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dataDir := t.TempDir()
	dbStore := database.New(dataDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	auditSvc := audit.NewService(audit.NewStore(dbStore), zap.NewNop(), audit.Config{
		BufferSize: 100, BatchSize: 10, FlushInterval: 5 * time.Millisecond, MaxEntries: 1000,
	})
	if err := auditSvc.Start(context.Background()); err != nil {
		t.Fatalf("start audit service: %v", err)
	}
	t.Cleanup(func() { _ = auditSvc.Stop(context.Background()) })

	authSvc := auth.NewAuthService(auth.NewUserStore(dbStore), auth.NewMemorySessionStore(time.Hour), nil, zap.NewNop())
	authHandler := auth.NewHandler(authSvc)

	assets := http.FS(fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("shell")},
	})

	router := newRouter(RouterParams{
		Logger:        zap.NewNop(),
		DatabaseReady: readinessStub(true),
		NativeReady:   readinessStub(true),
		AuthService:   authSvc,
		AuditService:  auditSvc,
		// Auth registers its own protected sub-routes (logout, me) with an
		// inline RequireAuth, so it is mounted once as a public group.
		PublicRoutes: []RouteRegistrar{authHandler},
		Assets:       assets,
	})
	return router, auditSvc, &authSvc
}

// drainAuditRouter stops the service so queued entries are persisted, then lists them.
func drainAuditRouter(t *testing.T, svc *audit.Service) []audit.AuditLog {
	t.Helper()
	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := svc.Stop(stopCtx); err != nil {
		t.Fatalf("drain audit service: %v", err)
	}
	items, _, err := svc.List(context.Background(), audit.Filter{Limit: 50})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}
	return items
}

// TestRouterAuditIgnoresForwardingHeaders is the regression guard for the
// deployment assumption: with no reverse proxy, the recorded IP must be the TCP
// source. Gin's default trusts 0.0.0.0/0 and ::/0, so this fails loudly if
// SetTrustedProxies(nil) is ever dropped from the router.
func TestRouterAuditIgnoresForwardingHeaders(t *testing.T) {
	router, auditSvc, _ := auditRouterFixture(t)

	body := bytes.NewReader([]byte(`{"username":"admin","password":"password123","confirmPassword":"password123"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/init", body)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.168.3.3:9999"
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	req.Header.Set("X-Real-IP", "203.0.113.9")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	entries := drainAuditRouter(t, auditSvc)
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(entries))
	}
	if entries[0].IP != "192.168.3.3" {
		t.Fatalf("expected the TCP source IP, got %q: forwarding headers must not be trusted", entries[0].IP)
	}
}

// TestRouterAuditCoversPublicAndProtectedRoutes walks the auth lifecycle through
// the real router: the public init/login routes and the protected logout route.
func TestRouterAuditCoversPublicAndProtectedRoutes(t *testing.T) {
	router, auditSvc, _ := auditRouterFixture(t)

	initBody := bytes.NewReader([]byte(`{"username":"admin","password":"password123","confirmPassword":"password123"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/init", initBody)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.20.30.40:1234"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("init failed: %d %s", w.Code, w.Body.String())
	}

	loginBody := bytes.NewReader([]byte(`{"username":"admin","password":"password123"}`))
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", loginBody)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.20.30.40:1234"
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()

	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.RemoteAddr = "10.20.30.40:1234"
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("logout failed: %d %s", w.Code, w.Body.String())
	}

	entries := drainAuditRouter(t, auditSvc)
	if len(entries) != 3 {
		t.Fatalf("expected 3 audit entries, got %d: %+v", len(entries), entries)
	}

	seen := make(map[string]audit.AuditLog, len(entries))
	for _, e := range entries {
		seen[e.Action] = e
		if e.IP != "10.20.30.40" {
			t.Errorf("expected the request IP on %s, got %q", e.Action, e.IP)
		}
	}
	for _, action := range []string{audit.ActionAuthInit, audit.ActionAuthLogin, audit.ActionAuthLogout} {
		entry, ok := seen[action]
		if !ok {
			t.Errorf("missing audit entry for %s", action)
			continue
		}
		if entry.Status != audit.StatusSuccess {
			t.Errorf("expected success for %s, got %q", action, entry.Status)
		}
		if entry.Username != "admin" {
			t.Errorf("expected the configured account name on %s, got %q", action, entry.Username)
		}
	}
}

// TestRouterAuditRecordsLoginBindFailure asserts a malformed body on a public
// route still produces a failed record, because the intent was declared before
// binding.
func TestRouterAuditRecordsLoginBindFailure(t *testing.T) {
	router, auditSvc, _ := auditRouterFixture(t)

	body := bytes.NewReader([]byte(`{}`))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.5.5.5:2222"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", w.Code, w.Body.String())
	}

	entries := drainAuditRouter(t, auditSvc)
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit entry for the bind failure, got %d", len(entries))
	}
	if entries[0].Status != audit.StatusFailed {
		t.Fatalf("expected failed status, got %q", entries[0].Status)
	}
	if entries[0].Username != audit.UsernameAnonymous {
		t.Fatalf("expected %q, got %q", audit.UsernameAnonymous, entries[0].Username)
	}
	if entries[0].IP != "10.5.5.5" {
		t.Fatalf("expected the request IP to survive a bind failure, got %q", entries[0].IP)
	}
}

// TestRouterAuditSkipsUntaggedRequests keeps the read-only endpoints out of the
// audit trail.
func TestRouterAuditSkipsUntaggedRequests(t *testing.T) {
	router, auditSvc, _ := auditRouterFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/status", nil)
	req.RemoteAddr = "10.6.6.6:3333"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	if entries := drainAuditRouter(t, auditSvc); len(entries) != 0 {
		t.Fatalf("expected no audit entries for a read-only request, got %+v", entries)
	}
}

// TestRouterMountsClientLogsAsPublicRoute proves the client-log ingestion
// endpoint is reachable without a session. That is the whole reason it exists:
// browser errors peak exactly when the session has expired, so a protected
// mount would discard the evidence we most need.
func TestRouterMountsClientLogsAsPublicRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := clientlog.NewHandler(zap.NewNop(), nil)
	router := newRouter(RouterParams{
		Logger:        zap.NewNop(),
		DatabaseReady: readinessStub(true),
		NativeReady:   readinessStub(true),
		// AuthService is present, so RequireAuth guards protected routes. The
		// client-log route must still be reachable without any session.
		AuthService:  authStubService{},
		PublicRoutes: []RouteRegistrar{handler},
		Assets: http.FS(fstest.MapFS{
			"index.html": &fstest.MapFile{Data: []byte("shell")},
		}),
	})

	body := `{"entries":[{"level":"error","msg":"server.error","route":"cameras.list","fields":{"status":503}}]}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/client-logs", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unauthenticated POST /api/v1/client-logs = %d, want 200; body=%s",
			recorder.Code, recorder.Body.String())
	}
}
