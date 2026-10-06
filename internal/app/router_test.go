package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/auth"
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
