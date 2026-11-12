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
	if err := comp.Dispatcher.Start(context.Background()); err != nil {
		t.Fatalf("Dispatcher.Start failed: %v", err)
	}
	defer func() {
		if err := comp.Dispatcher.Stop(context.Background()); err != nil {
			t.Errorf("Dispatcher.Stop failed: %v", err)
		}
	}()

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
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("marshal request failed: %v", err)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alert-destinations", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("expected Cache-Control: no-store on create response, got %q", cc)
	}

	var createResp httputil.Response
	if err := json.Unmarshal(w.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	dataBytes, err := json.Marshal(createResp.Data)
	if err != nil {
		t.Fatalf("marshal response data failed: %v", err)
	}
	var created AlertDestinationResponse
	if err := json.Unmarshal(dataBytes, &created); err != nil {
		t.Fatalf("unmarshal created destination failed: %v", err)
	}

	if created.Name != "Test Webhook" || created.ID == "" {
		t.Fatalf("unexpected created response: %+v", created)
	}
	if !created.SecretSet {
		t.Error("expected SecretSet true")
	}

	// 2. List Destinations
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/alert-destinations", nil)
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
	bodyBytes, err = json.Marshal(updBody)
	if err != nil {
		t.Fatalf("marshal update request failed: %v", err)
	}
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/v1/alert-destinations/"+created.ID, bytes.NewReader(bodyBytes))
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
	req = httptest.NewRequest(http.MethodPost, "/api/v1/alert-destinations/"+created.ID+"/disable", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("disable expected 200, got %d", w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("expected Cache-Control: no-store on disable response, got %q", cc)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/alert-destinations/"+created.ID+"/enable", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("enable expected 200, got %d", w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("expected Cache-Control: no-store on enable response, got %q", cc)
	}

	// 5. Delete Destination
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/alert-destinations/"+created.ID, nil)
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
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("marshal request failed: %v", err)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alert-destinations", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for reserved header, got %d: %s", w.Code, w.Body.String())
	}
}

func stringPtr(s string) *string {
	return &s
}

func boolPtr(b bool) *bool {
	return &b
}

func intPtr(v int) *int {
	return &v
}

func mustMarshalJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return b
}

func TestHandlerCapabilitiesValidationAndHeartbeatOnlyBypass(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, cleanup := setupTestStore(t)
	defer cleanup()

	// Deliberately configure empty PublicBaseURL to verify heartbeat-only bypass!
	comp := NewComponents(Options{
		DBProvider: store.dbProvider,
		Cipher:     fakeCipher{},
		DataDir:    t.TempDir(),
		Config: Config{
			PublicBaseURL: "",
			SignedURLTTL:  1 * time.Hour,
		},
		Logger: zap.NewNop(),
	})
	if err := comp.Dispatcher.Start(context.Background()); err != nil {
		t.Fatalf("Dispatcher.Start failed: %v", err)
	}
	t.Cleanup(func() {
		if err := comp.Dispatcher.Stop(context.Background()); err != nil {
			t.Errorf("Dispatcher.Stop: %v", err)
		}
	})

	router := gin.New()
	api := router.Group("/api/v1")
	comp.ProtectedRoutes.RegisterRoutes(api)

	// 1. Both capabilities disabled -> 422 ALERT_DESTINATION_CONFIG_INVALID
	bothOff := mustMarshalJSON(t, CreateAlertDestinationRequest{
		Name:             "Both Off",
		ChannelType:      ChannelTypeWebhook,
		EventsEnabled:    boolPtr(false),
		HeartbeatEnabled: boolPtr(false),
		Config:           AlertDestinationConfigInfo{URL: "https://example.com/hook"},
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alert-destinations", bytes.NewReader(bothOff))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 when both capabilities disabled, got %d: %s", w.Code, w.Body.String())
	}

	// 2. Events enabled + URL mode without PublicBaseURL -> 422 ALERT_PUBLIC_BASE_URL_MISSING
	eventsURLNoPublic := mustMarshalJSON(t, CreateAlertDestinationRequest{
		Name:             "Events URL Missing PublicBaseURL",
		ChannelType:      ChannelTypeWebhook,
		EventsEnabled:    boolPtr(true),
		HeartbeatEnabled: boolPtr(true),
		ImageMode:        ImageModeURL,
		Config:           AlertDestinationConfigInfo{URL: "https://example.com/hook"},
	})
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/alert-destinations", bytes.NewReader(eventsURLNoPublic))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 when events+url lacks public_base_url, got %d", w.Code)
	}

	// 3. Heartbeat-only destination bypasses empty PublicBaseURL and succeeds -> 200 OK!
	hbOnly := mustMarshalJSON(t, CreateAlertDestinationRequest{
		Name:             "Heartbeat Only Target",
		ChannelType:      ChannelTypeWebhook,
		EventsEnabled:    boolPtr(false),
		HeartbeatEnabled: boolPtr(true),
		Config: AlertDestinationConfigInfo{
			URL:                      "https://example.com/events",
			HeartbeatURL:             stringPtr("https://example.com/heartbeat"),
			AuthType:                 AuthTypeBearer,
			HeartbeatIntervalSeconds: intPtr(30),
		},
		Secret: &AlertDestinationSecretInfo{
			Token: stringPtr("tok-123"),
		},
	})
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/alert-destinations", bytes.NewReader(hbOnly))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for heartbeat-only without public_base_url, got %d: %s", w.Code, w.Body.String())
	}

	// 4. Cross-origin heartbeatUrl with Bearer credentials -> 422!
	crossOrigin := mustMarshalJSON(t, CreateAlertDestinationRequest{
		Name:             "Cross Origin Leak Attempt",
		ChannelType:      ChannelTypeWebhook,
		EventsEnabled:    boolPtr(false),
		HeartbeatEnabled: boolPtr(true),
		Config: AlertDestinationConfigInfo{
			URL:          "https://api.example.com/events",
			HeartbeatURL: stringPtr("https://evil.other.org/heartbeat"),
			AuthType:     AuthTypeBearer,
		},
		Secret: &AlertDestinationSecretInfo{
			Token: stringPtr("tok-123"),
		},
	})
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/alert-destinations", bytes.NewReader(crossOrigin))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for cross-origin heartbeatUrl with credentials, got %d: %s", w.Code, w.Body.String())
	}

	// 5. Out-of-range heartbeatIntervalSeconds (<15s) -> 422!
	badInterval := mustMarshalJSON(t, CreateAlertDestinationRequest{
		Name:             "Too Fast Heartbeat",
		ChannelType:      ChannelTypeWebhook,
		EventsEnabled:    boolPtr(false),
		HeartbeatEnabled: boolPtr(true),
		Config: AlertDestinationConfigInfo{
			URL:                      "https://example.com/hb",
			HeartbeatIntervalSeconds: intPtr(5),
		},
	})
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/alert-destinations", bytes.NewReader(badInterval))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for heartbeatIntervalSeconds=5, got %d", w.Code)
	}
}

func TestHandlerTestEndpointHeartbeatOnlySendsHeartbeat(t *testing.T) {
	gotBody := []byte{}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hb-probe" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		buf := new(bytes.Buffer)
		if _, err := buf.ReadFrom(r.Body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		gotBody = buf.Bytes()
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	router, _, cleanup := setupTestRouter(t)
	defer cleanup()

	probeReq := mustMarshalJSON(t, TestAlertDestinationRequest{
		ChannelType:      ChannelTypeWebhook,
		EventsEnabled:    boolPtr(false),
		HeartbeatEnabled: boolPtr(true),
		Config: AlertDestinationConfigInfo{
			URL:          target.URL + "/unused-events",
			HeartbeatURL: stringPtr(target.URL + "/hb-probe"),
		},
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alert-destinations/test", bytes.NewReader(probeReq))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on test endpoint, got %d: %s", w.Code, w.Body.String())
	}

	var hb HeartbeatPayload
	if err := json.Unmarshal(gotBody, &hb); err != nil {
		t.Fatalf("unmarshal test heartbeat payload: %v", err)
	}
	if hb.EventType != EventTypeHeartbeat || hb.Seq != 1 {
		t.Fatalf("expected test endpoint to send event_type=heartbeat seq=1, got %+v", hb)
	}

	// Unknown saved destination ID on /test must answer 404 (not 422).
	missingIDReq := mustMarshalJSON(t, TestAlertDestinationRequest{
		ID: "01MISSINGDEST0000000000000",
	})
	wMissing := httptest.NewRecorder()
	reqMissing := httptest.NewRequest(http.MethodPost, "/api/v1/alert-destinations/test", bytes.NewReader(missingIDReq))
	reqMissing.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(wMissing, reqMissing)
	if wMissing.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown destination ID on /test, got %d: %s", wMissing.Code, wMissing.Body.String())
	}

	// Main URL containing userinfo on /test must be rejected with 422.
	userinfoReq := mustMarshalJSON(t, TestAlertDestinationRequest{
		ChannelType:      ChannelTypeWebhook,
		EventsEnabled:    boolPtr(false),
		HeartbeatEnabled: boolPtr(true),
		Config: AlertDestinationConfigInfo{
			URL: "https://user:secret@example.com/hb",
		},
	})
	wUserinfo := httptest.NewRecorder()
	reqUserinfo := httptest.NewRequest(http.MethodPost, "/api/v1/alert-destinations/test", bytes.NewReader(userinfoReq))
	reqUserinfo.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(wUserinfo, reqUserinfo)
	if wUserinfo.Code != http.StatusUnprocessableEntity {
		t.Fatalf(
			"expected 422 for main URL with userinfo on /test, got %d: %s",
			wUserinfo.Code,
			wUserinfo.Body.String(),
		)
	}
}

func TestHandlerResolveExistingMQTTTestTargetAppliesOverrides(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	secBytes, err := store.EncryptSecret("01J8ZMQTTTEST0000000000001", &mqttSecret{Password: "stored-pass"})
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	clientID := "zMQTTTEST0000000000001"
	savedCfg := mustMarshalJSON(t, mqttConfig{
		Broker:           "tcp://127.0.0.1:1883",
		Topic:            "zhulong/old",
		QoS:              0,
		KeepAliveSeconds: 30,
		TLSSkipVerify:    false,
	})
	if err := store.Create(ctx, &AlertDestination{
		ID:               "01J8ZMQTTTEST0000000000001",
		Name:             "Saved MQTT",
		ChannelType:      ChannelTypeMQTT,
		Enabled:          true,
		EventsEnabled:    true,
		HeartbeatEnabled: false,
		ImageMode:        ImageModeBase64,
		MQTTClientID:     &clientID,
		ConfigJSON:       string(savedCfg),
		SecretCiphertext: secBytes,
		FilterJSON:       "{}",
	}); err != nil {
		t.Fatalf("store.Create: %v", err)
	}

	h := NewHandler(nil, store, zap.NewNop())
	var qos uint8 = 1
	keepAlive := 45
	dest, _, err := h.resolveExistingTestTarget(ctx, TestAlertDestinationRequest{
		ID:               "01J8ZMQTTTEST0000000000001",
		EventsEnabled:    boolPtr(false),
		HeartbeatEnabled: boolPtr(true),
		Config: AlertDestinationConfigInfo{
			Broker:           "ssl://127.0.0.1:8883",
			Topic:            "zhulong/events",
			HeartbeatTopic:   stringPtr("zhulong/status"),
			QoS:              &qos,
			KeepAliveSeconds: &keepAlive,
			TLSSkipVerify:    true,
			LWTEnabled:       boolPtr(false),
		},
	})
	if err != nil {
		t.Fatalf("resolveExistingTestTarget: %v", err)
	}

	var merged mqttConfig
	if err := json.Unmarshal([]byte(dest.ConfigJSON), &merged); err != nil {
		t.Fatalf("unmarshal merged mqttConfig: %v", err)
	}
	overridesApplied := merged.QoS == 1 && merged.KeepAliveSeconds == 45 && merged.TLSSkipVerify
	if !overridesApplied || merged.effectiveLWTEnabled() {
		t.Fatalf("expected MQTT test overrides (qos/keepAlive/tlsSkipVerify/lwtEnabled) to apply, got %+v", merged)
	}
}
