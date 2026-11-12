package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/nikonikowuw/Zhulong/internal/apitoken"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/openapi"
	"github.com/nikonikowuw/Zhulong/internal/person"
	"go.uber.org/zap"
)

type dummyExtractor struct{}

func (dummyExtractor) ExtractFaceFeature(ctx context.Context, imgBytes []byte) ([]float32, error) {
	return make([]float32, 512), nil
}

func TestOpenAPIAndTokenWiring(t *testing.T) {
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	defer dbStore.Close()

	tokenStore := apitoken.NewStore(dbStore)
	tokenSvc := apitoken.NewService(tokenStore, zap.NewNop())
	defer tokenSvc.Close()

	personStore := person.NewStore(dbStore)
	personSvc := person.NewService(personStore, tempDir, zap.NewNop(), person.Options{Extractor: dummyExtractor{}})

	auditStore := audit.NewStore(dbStore)
	auditSvc := audit.NewService(auditStore, zap.NewNop(), audit.DefaultConfig())
	if err := auditSvc.Start(context.Background()); err != nil {
		t.Fatalf("auditSvc.Start: %v", err)
	}
	t.Cleanup(func() {
		if stopErr := auditSvc.Stop(context.Background()); stopErr != nil {
			t.Errorf("auditSvc.Stop: %v", stopErr)
		}
	})

	statusAdapter := &statusSnapshotAdapter{
		publicBaseURL:         "http://10.0.0.50:8000",
		allowSoftwareFallback: true,
	}

	limiter := openapi.NewUploadLimiter()
	openAPIHandler := openapi.NewHandler(personSvc, limiter, openapi.WithStatusProvider(statusAdapter))
	tokenHandler := apitoken.NewHandler(tokenSvc, zap.NewNop())

	router := newRouter(RouterParams{
		Logger:          zap.NewNop(),
		Database:        dbStore,
		AuditService:    auditSvc,
		APITokenService: tokenSvc,
		ProtectedRoutes: []RouteRegistrar{tokenHandler},
		OpenAPIRoutes:   []RouteRegistrar{openAPIHandler},
		Assets:          http.FS(fstest.MapFS{}),
	})

	// 1. Unauthenticated request to openapi -> 401
	reqUnauth := httptest.NewRequest(http.MethodGet, "/api/v1/openapi/persons/EMP-TEST", nil)
	wUnauth := httptest.NewRecorder()
	router.ServeHTTP(wUnauth, reqUnauth)
	if wUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated openapi call, got %d", wUnauth.Code)
	}
	if wUnauth.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("expected WWW-Authenticate: Bearer, got %q", wUnauth.Header().Get("WWW-Authenticate"))
	}

	// 2. Create valid token
	tok, rawToken, err := tokenSvc.CreateToken(context.Background(), "WiringTestPlatform")
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	// 3. Authenticated openapi upsert -> 201 Created
	upsertBody := `{"name":"Wiring User"}`
	reqAuth := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/openapi/persons/EMP-WIRE-1",
		bytes.NewBufferString(upsertBody),
	)
	reqAuth.Header.Set("Authorization", "Bearer "+rawToken)
	reqAuth.Header.Set("Content-Type", "application/json")
	wAuth := httptest.NewRecorder()
	router.ServeHTTP(wAuth, reqAuth)
	if wAuth.Code != http.StatusCreated {
		t.Fatalf("expected 201 for authenticated openapi upsert, got %d: %s", wAuth.Code, wAuth.Body.String())
	}

	// 4. Verify person source in DB
	p, err := personSvc.GetPersonByExternalID(context.Background(), "EMP-WIRE-1")
	if err != nil {
		t.Fatalf("GetPersonByExternalID: %v", err)
	}
	if p.Source != person.PersonSourceAPI || p.SourceName != tok.Name {
		t.Errorf("unexpected source tracking: %s / %s", p.Source, p.SourceName)
	}

	// 5. GET /api/v1/openapi/status with valid token -> 200 OK & ZERO audit_logs rows for status polling!
	for range 3 {
		reqStatus := httptest.NewRequest(http.MethodGet, "/api/v1/openapi/status", nil)
		reqStatus.Header.Set("Authorization", "Bearer "+rawToken)
		wStatus := httptest.NewRecorder()
		router.ServeHTTP(wStatus, reqStatus)
		if wStatus.Code != http.StatusOK {
			t.Fatalf("expected 200 OK on GET /openapi/status, got %d: %s", wStatus.Code, wStatus.Body.String())
		}
	}

	// Stop and flush audit service queue before inspecting rows so async writes cannot hide!
	if err := auditSvc.Stop(context.Background()); err != nil {
		t.Fatalf("auditSvc.Stop: %v", err)
	}
	actions := []string{}
	if err := dbStore.DB().Table("audit_logs").Pluck("action", &actions).Error; err != nil {
		t.Fatalf("pluck audit_logs actions: %v", err)
	}
	// Step 1's unauthenticated call emits 1 api_token.auth_rejected; the 3 valid status polls emit 0!
	if len(actions) != 1 || actions[0] != "api_token.auth_rejected" {
		t.Fatalf("GET /openapi/status must never pollute audit_logs; got actions=%v", actions)
	}

	// 6. Revoked token calling GET /api/v1/openapi/status -> 401 + WWW-Authenticate: Bearer
	if err := tokenSvc.RevokeToken(context.Background(), tok.ID); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	reqRevoked := httptest.NewRequest(http.MethodGet, "/api/v1/openapi/status", nil)
	reqRevoked.Header.Set("Authorization", "Bearer "+rawToken)
	wRevoked := httptest.NewRecorder()
	router.ServeHTTP(wRevoked, reqRevoked)
	if wRevoked.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 after token revoke, got %d", wRevoked.Code)
	}
	if gotHdr := wRevoked.Header().Get("WWW-Authenticate"); gotHdr != "Bearer" {
		t.Errorf("expected WWW-Authenticate: Bearer on revoked status call, got %q", gotHdr)
	}
}
