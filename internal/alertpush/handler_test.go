package alertpush

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

func setupTestRouter(t *testing.T) (*gin.Engine, Components, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	store, cleanup := setupTestStore(t)
	tempDir := t.TempDir()

	comp := NewComponents(Options{
		DBProvider: store.dbProvider,
		Cipher:     fakeCipher{},
		DataDir:    tempDir,
		Config: Config{
			PublicBaseURL: "http://10.0.0.12:8000",
			SignedURLTTL:  1 * time.Hour,
		},
		Logger: zap.NewNop(),
	})

	router := gin.New()
	api := router.Group("/api/v1")
	comp.ProtectedRoutes.RegisterRoutes(api)
	comp.SignedMediaRoutes.RegisterRoutes(api)

	return router, comp, cleanup
}

func TestHandlerCRUDAndCacheControl(t *testing.T) {
	router, comp, cleanup := setupTestRouter(t)
	defer cleanup()
	_ = comp.Dispatcher.Start(context.Background())
	defer comp.Dispatcher.Stop(context.Background())

	// 1. Create Webhook Destination
	reqBody := CreateAlertDestinationRequest{
		Name:        "Test Webhook",
		ChannelType: ChannelTypeWebhook,
		ImageMode:   ImageModeURL,
		Config: AlertDestinationConfigInfo{
			URL:      "https://example.com/webhook",
			AuthType: AuthTypeBearer,
		},
		Secret: &AlertDestinationSecretInfo{
			Token: stringPtr("bearer-token-123"),
		},
	}
	bodyBytes, _ := json.Marshal(reqBody)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/alert-destinations", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("expected Cache-Control: no-store on create response, got %q", cc)
	}

	var createResp httputil.Response
	_ = json.Unmarshal(w.Body.Bytes(), &createResp)
	dataBytes, _ := json.Marshal(createResp.Data)
	var created AlertDestinationResponse
	_ = json.Unmarshal(dataBytes, &created)

	if created.Name != "Test Webhook" || created.ID == "" {
		t.Fatalf("unexpected created response: %+v", created)
	}
	if !created.SecretSet {
		t.Error("expected SecretSet true")
	}

	// 2. List Destinations
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodGet, "/api/v1/alert-destinations", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list expected 200, got %d", w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("expected Cache-Control: no-store on list response, got %q", cc)
	}

	// 3. Update Destination
	updBody := UpdateAlertDestinationRequest{
		Name: stringPtr("Renamed Webhook"),
	}
	bodyBytes, _ = json.Marshal(updBody)
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPut, "/api/v1/alert-destinations/"+created.ID, bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("update expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("expected Cache-Control: no-store on update response, got %q", cc)
	}

	// 4. Disable and Enable
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, "/api/v1/alert-destinations/"+created.ID+"/disable", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("disable expected 200, got %d", w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("expected Cache-Control: no-store on disable response, got %q", cc)
	}

	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, "/api/v1/alert-destinations/"+created.ID+"/enable", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("enable expected 200, got %d", w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("expected Cache-Control: no-store on enable response, got %q", cc)
	}

	// 5. Delete Destination
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodDelete, "/api/v1/alert-destinations/"+created.ID, nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete expected 200, got %d", w.Code)
	}
}

func TestHandlerReservedHeaderForbidden(t *testing.T) {
	router, _, cleanup := setupTestRouter(t)
	defer cleanup()

	reqBody := CreateAlertDestinationRequest{
		Name:        "Reserved Header Test",
		ChannelType: ChannelTypeWebhook,
		ImageMode:   ImageModeMultipart,
		Config: AlertDestinationConfigInfo{
			URL:      "https://example.com/webhook",
			AuthType: AuthTypeNone,
		},
		Secret: &AlertDestinationSecretInfo{
			Headers: map[string]string{
				"Authorization": "custom-auth-not-allowed",
			},
		},
	}
	bodyBytes, _ := json.Marshal(reqBody)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/alert-destinations", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for reserved header, got %d: %s", w.Code, w.Body.String())
	}
}

func stringPtr(s string) *string {
	return &s
}
