package app

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"testing/fstest"
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
	router := newRouter(zap.NewNop(), readinessStub(true), readinessStub(true), assets)

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
	router := newRouter(zap.NewNop(), readinessStub(false), readinessStub(true), http.FS(fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("shell")},
	}))
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

func contains(value, part string) bool { return strings.Contains(value, part) }
