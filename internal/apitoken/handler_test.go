package apitoken

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

func setupHandlerEnv(t *testing.T) (*gin.Engine, *Service, func()) {
	t.Helper()
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	tokenStore := NewStore(dbStore)
	tokenSvc := NewService(tokenStore, zap.NewNop())
	handler := NewHandler(tokenSvc, zap.NewNop())

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/v1"))

	cleanup := func() {
		if err := tokenSvc.Close(); err != nil {
			t.Errorf("close token service: %v", err)
		}
		if err := dbStore.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}
	return router, tokenSvc, cleanup
}

func TestHandler_CreateToken(t *testing.T) {
	router, _, cleanup := setupHandlerEnv(t)
	defer cleanup()

	// 1. Valid creation
	body := `{"name":"CampusAccess"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/api-tokens", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("expected Cache-Control: no-store, got %q", w.Header().Get("Cache-Control"))
	}

	var resp struct {
		Code string                 `json:"code"`
		Data APITokenCreateResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Code != "OK" {
		t.Errorf("expected code OK, got %q", resp.Code)
	}
	if resp.Data.Name != "CampusAccess" || resp.Data.Token == "" {
		t.Errorf("unexpected created token: %+v", resp.Data)
	}

	// 2. Conflict creation (same name)
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/api-tokens", bytes.NewBufferString(body))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict, got %d: %s", w2.Code, w2.Body.String())
	}

	// 3. Invalid format / missing name
	badReq := httptest.NewRequest(http.MethodPost, "/api/v1/api-tokens", bytes.NewBufferString(`{"name":"   "}`))
	badReq.Header.Set("Content-Type", "application/json")
	wBad := httptest.NewRecorder()
	router.ServeHTTP(wBad, badReq)

	if wBad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for empty name, got %d: %s", wBad.Code, wBad.Body.String())
	}
}

func TestHandler_ListAndRevoke(t *testing.T) {
	router, svc, cleanup := setupHandlerEnv(t)
	defer cleanup()
	ctx := context.Background()

	tok1, _, err := svc.CreateToken(ctx, "Token-1")
	if err != nil {
		t.Fatalf("CreateToken 1: %v", err)
	}
	_, _, err = svc.CreateToken(ctx, "Token-2")
	if err != nil {
		t.Fatalf("CreateToken 2: %v", err)
	}

	// List active
	req := httptest.NewRequest(http.MethodGet, "/api/v1/api-tokens", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	var listResp struct {
		Code string               `json:"code"`
		Data APITokenListResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal list response: %v", err)
	}
	if len(listResp.Data.Items) != 2 {
		t.Errorf("expected 2 active tokens, got %d", len(listResp.Data.Items))
	}

	// Revoke tok1
	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/api-tokens/"+tok1.ID, nil)
	delW := httptest.NewRecorder()
	router.ServeHTTP(delW, delReq)

	if delW.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for revoke, got %d: %s", delW.Code, delW.Body.String())
	}

	// Active list now has 1
	wAfter := httptest.NewRecorder()
	router.ServeHTTP(wAfter, httptest.NewRequest(http.MethodGet, "/api/v1/api-tokens", nil))
	if err := json.Unmarshal(wAfter.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal list response after revoke: %v", err)
	}
	if len(listResp.Data.Items) != 1 {
		t.Errorf("expected 1 active token after revoke, got %d", len(listResp.Data.Items))
	}

	// Include revoked query returns 2
	wInclude := httptest.NewRecorder()
	router.ServeHTTP(wInclude, httptest.NewRequest(http.MethodGet, "/api/v1/api-tokens?includeRevoked=true", nil))
	if err := json.Unmarshal(wInclude.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal list response with includeRevoked: %v", err)
	}
	if len(listResp.Data.Items) != 2 {
		t.Errorf("expected 2 tokens with includeRevoked=true, got %d", len(listResp.Data.Items))
	}

	// Revoke non-existent token returns 404
	delGhost := httptest.NewRequest(http.MethodDelete, "/api/v1/api-tokens/ghost_id", nil)
	delGhostW := httptest.NewRecorder()
	router.ServeHTTP(delGhostW, delGhost)

	if delGhostW.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for ghost token, got %d: %s", delGhostW.Code, delGhostW.Body.String())
	}
}
