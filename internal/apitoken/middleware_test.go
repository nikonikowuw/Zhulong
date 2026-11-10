package apitoken

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func setupMiddlewareEnv(t *testing.T) (*Service, *audit.Service, func()) {
	t.Helper()
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	tokenStore := NewStore(dbStore)
	tokenSvc := NewService(tokenStore, zap.NewNop())

	auditStore := audit.NewStore(dbStore)
	auditSvc := audit.NewService(auditStore, zap.NewNop(), audit.Config{
		BufferSize:    128,
		FlushInterval: 10 * time.Millisecond,
		BatchSize:     1,
	})
	if err := auditSvc.Start(context.Background()); err != nil {
		t.Fatalf("start audit service: %v", err)
	}

	cleanup := func() {
		if err := tokenSvc.Close(); err != nil {
			t.Errorf("close token service: %v", err)
		}
		if err := auditSvc.Stop(context.Background()); err != nil {
			t.Errorf("stop audit service: %v", err)
		}
		if err := dbStore.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}
	return tokenSvc, auditSvc, cleanup
}

func TestRequireBearer_Valid(t *testing.T) {
	tokenSvc, auditSvc, cleanup := setupMiddlewareEnv(t)
	defer cleanup()
	ctx := context.Background()

	tok, raw, err := tokenSvc.CreateToken(ctx, "Middleware-Platform")
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	router := gin.New()
	router.Use(RequireBearer(tokenSvc, zap.NewNop(), auditSvc))
	var capturedIdentity Identity
	var capturedActor audit.Actor
	router.GET("/test", func(c *gin.Context) {
		if id, ok := GetIdentity(c); ok {
			capturedIdentity = id
		}
		if actor, ok := audit.FromContext(c.Request.Context()); ok {
			capturedActor = actor
		}
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if capturedIdentity.ID != tok.ID || capturedIdentity.Name != "Middleware-Platform" {
		t.Errorf("unexpected identity: %+v", capturedIdentity)
	}
	if capturedActor.Username != "api_token:Middleware-Platform" || capturedActor.Source != audit.SourceAPI {
		t.Errorf("unexpected actor: %+v", capturedActor)
	}
}

func TestRequireBearer_MissingOrMalformedHeader(t *testing.T) {
	tokenSvc, auditSvc, cleanup := setupMiddlewareEnv(t)
	defer cleanup()

	router := gin.New()
	router.Use(RequireBearer(tokenSvc, zap.NewNop(), auditSvc))
	router.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	tests := []struct {
		name       string
		authHeader string
	}{
		{name: "missing header", authHeader: ""},
		{name: "wrong scheme", authHeader: "Basic dXNlcjpwYXNz"},
		{name: "empty bearer", authHeader: "Bearer   "},
		{name: "no space", authHeader: "Bearertoken123"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", w.Code)
			}
			if w.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Errorf("missing or incorrect WWW-Authenticate header: %q", w.Header().Get("WWW-Authenticate"))
			}
		})
	}
}

func TestRequireBearer_UnknownOrRevokedAuditNoLeaks(t *testing.T) {
	tokenSvc, auditSvc, cleanup := setupMiddlewareEnv(t)
	defer cleanup()
	ctx := context.Background()

	fakeSecret := "sk-c65298078e42f97eb80d247b72a90e8d20a0a2cb61b2823c507861cbf16260c9"

	router := gin.New()
	router.Use(RequireBearer(tokenSvc, zap.NewNop(), auditSvc))
	router.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+fakeSecret)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	// Wait briefly for the asynchronous audit logger to flush
	time.Sleep(100 * time.Millisecond)

	logs, _, err := auditSvc.List(ctx, audit.Filter{Action: ActionTokenAuthRejected})
	if err != nil {
		t.Fatalf("query audit logs: %v", err)
	}
	if len(logs) == 0 {
		t.Fatalf("expected auth_rejected audit entry to be recorded")
	}

	lastEntry := logs[0]
	if lastEntry.Action != ActionTokenAuthRejected {
		t.Errorf("expected action %q, got %q", ActionTokenAuthRejected, lastEntry.Action)
	}
	if lastEntry.Detail != "reject=unknown_or_revoked" {
		t.Errorf("expected detail 'reject=unknown_or_revoked', got %q", lastEntry.Detail)
	}

	// Verify no credential leaks in audit fields
	for _, l := range logs {
		if l.Detail == fakeSecret || l.Target == fakeSecret {
			t.Fatalf("SECURITY VIOLATION: raw token leaked into audit entry: %+v", l)
		}
	}
}
