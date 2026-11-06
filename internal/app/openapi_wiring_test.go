package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/nikonikowuw/Zhulong/internal/apitoken"
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

	limiter := openapi.NewUploadLimiter()
	openAPIHandler := openapi.NewHandler(personSvc, limiter)
	tokenHandler := apitoken.NewHandler(tokenSvc, zap.NewNop())

	router := newRouter(RouterParams{
		Logger:          zap.NewNop(),
		Database:        dbStore,
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
}
