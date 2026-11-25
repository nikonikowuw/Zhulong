package alertpush

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"github.com/nikonikowuw/Zhulong/internal/record"
)

// postDestination creates a destination through the real router and returns the
// response code, envelope code and decoded destination (when successful).
func postDestination(
	t *testing.T,
	router *gin.Engine,
	reqBody CreateAlertDestinationRequest,
) (int, string, AlertDestinationResponse) {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/alert-destinations",
		bytes.NewReader(mustMarshalJSON(t, reqBody)),
	)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	return decodeDestinationResponse(t, w)
}

func putDestination(
	t *testing.T,
	router *gin.Engine,
	id string,
	reqBody UpdateAlertDestinationRequest,
) (int, string, AlertDestinationResponse) {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/alert-destinations/"+id,
		bytes.NewReader(mustMarshalJSON(t, reqBody)),
	)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	return decodeDestinationResponse(t, w)
}

func decodeDestinationResponse(
	t *testing.T,
	w *httptest.ResponseRecorder,
) (int, string, AlertDestinationResponse) {
	t.Helper()
	var envelope httputil.Response
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v (%s)", err, w.Body.String())
	}
	var dest AlertDestinationResponse
	if w.Code == http.StatusOK {
		data, err := json.Marshal(envelope.Data)
		if err != nil {
			t.Fatalf("marshal data: %v", err)
		}
		if err := json.Unmarshal(data, &dest); err != nil {
			t.Fatalf("unmarshal destination: %v", err)
		}
	}
	return w.Code, envelope.Code, dest
}

func mqttCreateRequest(name string, inlineMaxKB *int) CreateAlertDestinationRequest {
	return CreateAlertDestinationRequest{
		Name:        name,
		ChannelType: ChannelTypeMQTT,
		ImageMode:   ImageModeBase64,
		Config: AlertDestinationConfigInfo{
			Broker:      stringPtr("tcp://broker.local:1883"),
			Topic:       stringPtr("zhulong/events"),
			InlineMaxKB: inlineMaxKB,
		},
	}
}

func TestInlineMaxKB(t *testing.T) {
	router, _, cleanup := setupTestRouter(t)
	defer cleanup()

	tests := []struct {
		name     string
		value    *int
		wantCode int
		wantKB   int
	}{
		{name: "absent uses default", value: nil, wantCode: http.StatusOK, wantKB: DefaultInlineMaxKB},
		{name: "explicit zero rejected", value: intPtr(0), wantCode: http.StatusUnprocessableEntity},
		{name: "below minimum", value: intPtr(63), wantCode: http.StatusUnprocessableEntity},
		{name: "minimum", value: intPtr(64), wantCode: http.StatusOK, wantKB: 64},
		{name: "maximum", value: intPtr(8192), wantCode: http.StatusOK, wantKB: 8192},
		{name: "above maximum", value: intPtr(8193), wantCode: http.StatusUnprocessableEntity},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, envCode, dest := postDestination(
				t,
				router,
				mqttCreateRequest("mqtt-inline-"+string(rune('a'+i)), tt.value),
			)
			if code != tt.wantCode {
				t.Fatalf("status = %d (%s), want %d", code, envCode, tt.wantCode)
			}
			if tt.wantCode != http.StatusOK {
				if envCode != "VALIDATION_FAILED" {
					t.Fatalf("code = %q, want VALIDATION_FAILED", envCode)
				}
				return
			}
			if dest.Config.InlineMaxKB == nil || *dest.Config.InlineMaxKB != tt.wantKB {
				t.Fatalf("inlineMaxKb = %v, want %d", dest.Config.InlineMaxKB, tt.wantKB)
			}
		})
	}
}

func TestInlineMaxKB_UpdateRoundTripAndWebhookIgnores(t *testing.T) {
	router, _, cleanup := setupTestRouter(t)
	defer cleanup()

	code, envCode, created := postDestination(t, router, mqttCreateRequest("mqtt-update", nil))
	if code != http.StatusOK {
		t.Fatalf("create status = %d (%s)", code, envCode)
	}

	code, _, updated := putDestination(t, router, created.ID, UpdateAlertDestinationRequest{
		Config: &AlertDestinationConfigInfo{InlineMaxKB: intPtr(256)},
	})
	if code != http.StatusOK || updated.Config.InlineMaxKB == nil || *updated.Config.InlineMaxKB != 256 {
		t.Fatalf("update status = %d inlineMaxKb = %v, want 256", code, updated.Config.InlineMaxKB)
	}

	code, envCode, _ = putDestination(t, router, created.ID, UpdateAlertDestinationRequest{
		Config: &AlertDestinationConfigInfo{InlineMaxKB: intPtr(0)},
	})
	if code != http.StatusUnprocessableEntity || envCode != "VALIDATION_FAILED" {
		t.Fatalf("zero update status = %d code = %q, want 422 VALIDATION_FAILED", code, envCode)
	}

	code, envCode, _ = putDestination(t, router, created.ID, UpdateAlertDestinationRequest{
		Config: &AlertDestinationConfigInfo{InlineMaxKB: intPtr(9000)},
	})
	if code != http.StatusUnprocessableEntity || envCode != "VALIDATION_FAILED" {
		t.Fatalf("out-of-range update status = %d code = %q, want 422 VALIDATION_FAILED", code, envCode)
	}

	code, envCode, webhook := postDestination(t, router, CreateAlertDestinationRequest{
		Name:        "webhook-inline",
		ChannelType: ChannelTypeWebhook,
		ImageMode:   ImageModeMultipart,
		Config: AlertDestinationConfigInfo{
			URL:         stringPtr("https://example.com/hook"),
			InlineMaxKB: intPtr(1),
		},
	})
	if code != http.StatusOK {
		t.Fatalf("webhook create status = %d (%s), inlineMaxKb must be ignored", code, envCode)
	}
	if webhook.Config.InlineMaxKB != nil {
		t.Fatalf("webhook inlineMaxKb = %v, want omitted", *webhook.Config.InlineMaxKB)
	}
}

func TestAlertDestinationFilterEventTypes(t *testing.T) {
	router, _, cleanup := setupTestRouter(t)
	defer cleanup()

	tests := []struct {
		name       string
		eventTypes []string
		wantCode   int
		wantEnv    string
	}{
		{name: "v1 short names accepted", eventTypes: []string{"alarm", "face", "vehicle"}, wantCode: http.StatusOK},
		{name: "empty accepted", eventTypes: []string{}, wantCode: http.StatusOK},
		{
			name:       "legacy face_recognition rejected",
			eventTypes: []string{"face_recognition"},
			wantCode:   http.StatusUnprocessableEntity,
			wantEnv:    CodeAlertEventTypeInvalid,
		},
		{
			name:       "legacy plate_recognition rejected",
			eventTypes: []string{"alarm", "plate_recognition"},
			wantCode:   http.StatusUnprocessableEntity,
			wantEnv:    CodeAlertEventTypeInvalid,
		},
		{
			name:       "unknown rejected",
			eventTypes: []string{"heartbeat"},
			wantCode:   http.StatusUnprocessableEntity,
			wantEnv:    CodeAlertEventTypeInvalid,
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := mqttCreateRequest("filter-"+string(rune('a'+i)), nil)
			req.Filter = &AlertDestinationFilterInfo{EventTypes: tt.eventTypes}
			code, envCode, dest := postDestination(t, router, req)
			if code != tt.wantCode {
				t.Fatalf("status = %d (%s), want %d", code, envCode, tt.wantCode)
			}
			if tt.wantEnv != "" && envCode != tt.wantEnv {
				t.Fatalf("code = %q, want %q", envCode, tt.wantEnv)
			}
			if code == http.StatusOK && len(dest.Filter.EventTypes) != len(tt.eventTypes) {
				t.Fatalf("filter = %v, want %v", dest.Filter.EventTypes, tt.eventTypes)
			}
		})
	}

	code, _, created := postDestination(t, router, mqttCreateRequest("filter-update", nil))
	if code != http.StatusOK {
		t.Fatalf("create status = %d", code)
	}
	code, envCode, _ := putDestination(t, router, created.ID, UpdateAlertDestinationRequest{
		Filter: &AlertDestinationFilterInfo{EventTypes: []string{"face_recognition"}},
	})
	if code != http.StatusUnprocessableEntity || envCode != CodeAlertEventTypeInvalid {
		t.Fatalf("update with legacy name: status = %d code = %q, want 422 %s", code, envCode, CodeAlertEventTypeInvalid)
	}
}

func TestFilterMatchesExternalEventType(t *testing.T) {
	filter := &destinationFilter{EventTypes: []string{EventTypeFace}}
	if !filter.Matches(externalEventType(record.EventTypeFaceRecognition), "", "") {
		t.Fatal("face filter must match face_recognition events")
	}
	if filter.Matches(externalEventType(record.EventTypePlateRecognition), "", "") {
		t.Fatal("face filter must not match plate events")
	}
}

func TestHandlerTestEndpointSendsV1AlarmProbe(t *testing.T) {
	var (
		gotBody    []byte
		gotHeaders http.Header
	)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := new(bytes.Buffer)
		if _, err := buf.ReadFrom(r.Body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		gotBody = buf.Bytes()
		gotHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	router, _, cleanup := setupTestRouter(t)
	defer cleanup()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/alert-destinations/test",
		bytes.NewReader(mustMarshalJSON(t, TestAlertDestinationRequest{
			ChannelType: ChannelTypeWebhook,
			ImageMode:   ImageModeURL,
			Config:      AlertDestinationConfigInfo{URL: stringPtr(target.URL + "/events")},
		})),
	)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	var probe EventPayload
	if err := json.Unmarshal(gotBody, &probe); err != nil {
		t.Fatalf("unmarshal probe: %v (%s)", err, gotBody)
	}
	probeOK := probe.SchemaVersion == SchemaVersion && probe.EventType == EventTypeAlarm &&
		probe.Seq == 0 && probe.ID != "" && probe.ID != "01TESTALARM0000000000000" &&
		probe.Alarm != nil &&
		probe.Alarm.Type == "test_ping" &&
		probe.Camera != nil && probe.Camera.ID == "test_cam"
	if !probeOK {
		t.Fatalf("unexpected probe payload: %s", gotBody)
	}
	if gotHeaders.Get(headerZhulongEvent) != EventTypeAlarm || gotHeaders.Get(headerZhulongSchema) != "1" {
		t.Fatalf("unexpected probe headers: %v", gotHeaders)
	}
	if gotHeaders.Get(headerZhulongTimestamp) == "" {
		t.Fatalf("expected %s header on webhook probe", headerZhulongTimestamp)
	}
}

func TestMQTTUpdatePreservesCustomClientIDAndValidatesFields(t *testing.T) {
	router, _, cleanup := setupTestRouter(t)
	defer cleanup()

	createReq := CreateAlertDestinationRequest{
		Name:        "MQTT Custom",
		ChannelType: ChannelTypeMQTT,
		ImageMode:   ImageModeBase64,
		Config: AlertDestinationConfigInfo{
			Broker:        stringPtr("ssl://broker.local:8883"),
			Topic:         stringPtr("zhulong/events"),
			ClientID:      stringPtr("edge-custom-01"),
			Username:      stringPtr("ops-user"),
			TLSSkipVerify: boolPtr(true),
		},
	}
	code, envCode, created := postDestination(t, router, createReq)
	if code != http.StatusOK {
		t.Fatalf("create failed: %d (%s)", code, envCode)
	}
	if created.Config.ClientID == nil || *created.Config.ClientID != "edge-custom-01" {
		t.Fatalf("clientId = %v, want edge-custom-01", created.Config.ClientID)
	}
	if created.Config.Username == nil || *created.Config.Username != "ops-user" {
		t.Fatalf("username = %v, want ops-user", created.Config.Username)
	}
	if created.Config.TLSSkipVerify == nil || !*created.Config.TLSSkipVerify {
		t.Fatalf("tlsSkipVerify = %v, want true", created.Config.TLSSkipVerify)
	}

	// 1. Partial PUT updating name only must preserve custom ClientID, Username, and TLSSkipVerify.
	code, envCode, updated := putDestination(t, router, created.ID, UpdateAlertDestinationRequest{
		Name: stringPtr("MQTT Renamed"),
	})
	if code != http.StatusOK {
		t.Fatalf("partial name update failed: %d (%s)", code, envCode)
	}
	if updated.Config.ClientID == nil || *updated.Config.ClientID != "edge-custom-01" {
		t.Fatalf("custom clientId was clobbered on name update: got %v", updated.Config.ClientID)
	}

	// 2. Partial PUT updating inlineMaxKb must keep TLSSkipVerify=true, Username, and ClientID intact.
	code, envCode, updated = putDestination(t, router, created.ID, UpdateAlertDestinationRequest{
		Config: &AlertDestinationConfigInfo{
			InlineMaxKB: intPtr(256),
		},
	})
	if code != http.StatusOK {
		t.Fatalf("partial config update failed: %d (%s)", code, envCode)
	}
	if updated.Config.ClientID == nil || *updated.Config.ClientID != "edge-custom-01" {
		t.Fatalf("custom clientId lost on partial config update: got %v", updated.Config.ClientID)
	}
	if updated.Config.Username == nil || *updated.Config.Username != "ops-user" {
		t.Fatalf("username lost on partial config update: got %v", updated.Config.Username)
	}
	if updated.Config.TLSSkipVerify == nil || !*updated.Config.TLSSkipVerify {
		t.Fatalf("tlsSkipVerify reset on partial config update: got %v", updated.Config.TLSSkipVerify)
	}

	// 3. Explicit empty strings clear username and revert clientId to auto-derived "z<id[4:]>".
	code, envCode, cleared := putDestination(t, router, created.ID, UpdateAlertDestinationRequest{
		Config: &AlertDestinationConfigInfo{
			Username:      stringPtr(""),
			ClientID:      stringPtr(""),
			TLSSkipVerify: boolPtr(false),
		},
	})
	if code != http.StatusOK {
		t.Fatalf("clear update failed: %d (%s)", code, envCode)
	}
	if cleared.Config.Username != nil {
		t.Fatalf("username = %v, want cleared (nil)", *cleared.Config.Username)
	}
	wantAutoID := "z" + created.ID[4:]
	if cleared.Config.ClientID == nil || *cleared.Config.ClientID != wantAutoID {
		t.Fatalf("clientId = %v, want auto-derived %q", cleared.Config.ClientID, wantAutoID)
	}
	if cleared.Config.TLSSkipVerify == nil || *cleared.Config.TLSSkipVerify {
		t.Fatalf("tlsSkipVerify = %v, want false", cleared.Config.TLSSkipVerify)
	}

	// 4. Invalid MQTT update fields must be rejected with 422 VALIDATION_FAILED.
	invalidQoS := uint8(3)
	invalidUpdates := []struct {
		name string
		cfg  AlertDestinationConfigInfo
	}{
		{name: "broker with userinfo", cfg: AlertDestinationConfigInfo{Broker: stringPtr("tcp://user:pass@broker.local:1883")}},
		{name: "clientId > 23 chars", cfg: AlertDestinationConfigInfo{ClientID: stringPtr("client-id-longer-than-23")}},
		{name: "keepAlive 0", cfg: AlertDestinationConfigInfo{KeepAliveSeconds: intPtr(0)}},
		{name: "keepAlive below 5", cfg: AlertDestinationConfigInfo{KeepAliveSeconds: intPtr(4)}},
		{name: "keepAlive above 300", cfg: AlertDestinationConfigInfo{KeepAliveSeconds: intPtr(301)}},
		{name: "qos > 2", cfg: AlertDestinationConfigInfo{QoS: &invalidQoS}},
	}
	for _, tc := range invalidUpdates {
		t.Run(tc.name, func(t *testing.T) {
			cfgCopy := tc.cfg
			st, codeStr, _ := putDestination(t, router, created.ID, UpdateAlertDestinationRequest{
				Config: &cfgCopy,
			})
			if st != http.StatusUnprocessableEntity || codeStr != "VALIDATION_FAILED" {
				t.Fatalf("%s: status=%d code=%q, want 422 VALIDATION_FAILED", tc.name, st, codeStr)
			}
		})
	}
}

func TestHandlerReservedHeadersParity(t *testing.T) {
	router, _, cleanup := setupTestRouter(t)
	defer cleanup()

	reserved := []string{
		"User-Agent",
		"Accept",
		"Transfer-Encoding",
		"X-Zhulong-Schema",
		"X-Zhulong-Timestamp",
		"X-Zhulong-Signature",
	}
	for _, hdr := range reserved {
		req := CreateAlertDestinationRequest{
			Name:        "Reserved-" + hdr,
			ChannelType: ChannelTypeWebhook,
			ImageMode:   ImageModeMultipart,
			Config:      AlertDestinationConfigInfo{URL: stringPtr("https://example.com/hook")},
			Secret: &AlertDestinationSecretInfo{
				Headers: map[string]string{hdr: "bad"},
			},
		}
		code, envCode, _ := postDestination(t, router, req)
		if code != http.StatusUnprocessableEntity || envCode != "VALIDATION_FAILED" {
			t.Fatalf("header %q: status=%d code=%q, want 422 VALIDATION_FAILED", hdr, code, envCode)
		}
	}
}

func TestHandlerMaxDestinationsLimitEnforced(t *testing.T) {
	router, _, cleanup := setupTestRouter(t)
	defer cleanup()

	for i := range MaxDestinations {
		code, envCode, _ := postDestination(t, router, mqttCreateRequest("dest-limit-"+string(rune('A'+i/26))+string(rune('a'+i%26)), nil))
		if code != http.StatusOK {
			t.Fatalf("create #%d failed: %d (%s)", i+1, code, envCode)
		}
	}

	code, envCode, _ := postDestination(t, router, mqttCreateRequest("dest-limit-overflow", nil))
	if code != http.StatusUnprocessableEntity || envCode != CodeAlertMaxDestinationsReached {
		t.Fatalf("33rd destination: status=%d code=%q, want 422 %s", code, envCode, CodeAlertMaxDestinationsReached)
	}
}

func TestHandlerValidationParityAcrossCreateUpdateAndTest(t *testing.T) {
	router, _, cleanup := setupTestRouter(t)
	defer cleanup()

	longName := string(bytes.Repeat([]byte("a"), MaxDestinationNameLength+1))

	// 1. Create rejects overlong name, invalid authType, and out-of-range timeoutSeconds.
	createInvalid := []struct {
		name string
		req  CreateAlertDestinationRequest
	}{
		{
			name: "name exceeds 64 chars",
			req:  mqttCreateRequest(longName, nil),
		},
		{
			name: "webhook invalid authType",
			req: CreateAlertDestinationRequest{
				Name:        "bad-auth",
				ChannelType: ChannelTypeWebhook,
				ImageMode:   ImageModeMultipart,
				Config: AlertDestinationConfigInfo{
					URL:      stringPtr("https://example.com/hook"),
					AuthType: stringPtr("digest"),
				},
			},
		},
		{
			name: "webhook timeoutSeconds 0",
			req: CreateAlertDestinationRequest{
				Name:        "bad-timeout-0",
				ChannelType: ChannelTypeWebhook,
				ImageMode:   ImageModeMultipart,
				Config: AlertDestinationConfigInfo{
					URL:            stringPtr("https://example.com/hook"),
					TimeoutSeconds: intPtr(0),
				},
			},
		},
		{
			name: "webhook timeoutSeconds 31",
			req: CreateAlertDestinationRequest{
				Name:        "bad-timeout-31",
				ChannelType: ChannelTypeWebhook,
				ImageMode:   ImageModeMultipart,
				Config: AlertDestinationConfigInfo{
					URL:            stringPtr("https://example.com/hook"),
					TimeoutSeconds: intPtr(31),
				},
			},
		},
	}
	for _, tc := range createInvalid {
		t.Run("create/"+tc.name, func(t *testing.T) {
			code, envCode, _ := postDestination(t, router, tc.req)
			if code != http.StatusUnprocessableEntity || envCode != "VALIDATION_FAILED" {
				t.Fatalf("status=%d code=%q, want 422 VALIDATION_FAILED", code, envCode)
			}
		})
	}

	// Create a valid bearer webhook to test Update and existing-target /test parity.
	var receivedAuth string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	code, envCode, created := postDestination(t, router, CreateAlertDestinationRequest{
		Name:        "Valid Bearer Hook",
		ChannelType: ChannelTypeWebhook,
		ImageMode:   ImageModeMultipart,
		Config: AlertDestinationConfigInfo{
			URL:            stringPtr(target.URL + "/events"),
			AuthType:       stringPtr(AuthTypeBearer),
			TimeoutSeconds: intPtr(5),
		},
		Secret: &AlertDestinationSecretInfo{
			Token: stringPtr("tok-abc"),
		},
	})
	if code != http.StatusOK {
		t.Fatalf("create valid webhook: %d (%s)", code, envCode)
	}

	// 2. Update rejects blank name, overlong name, invalid authType, and out-of-range timeoutSeconds.
	updateInvalid := []struct {
		name string
		req  UpdateAlertDestinationRequest
	}{
		{name: "blank name", req: UpdateAlertDestinationRequest{Name: stringPtr("   ")}},
		{name: "overlong name", req: UpdateAlertDestinationRequest{Name: stringPtr(longName)}},
		{
			name: "invalid authType",
			req:  UpdateAlertDestinationRequest{Config: &AlertDestinationConfigInfo{AuthType: stringPtr("oauth")}},
		},
		{
			name: "timeoutSeconds 0",
			req:  UpdateAlertDestinationRequest{Config: &AlertDestinationConfigInfo{TimeoutSeconds: intPtr(0)}},
		},
		{
			name: "timeoutSeconds 35",
			req:  UpdateAlertDestinationRequest{Config: &AlertDestinationConfigInfo{TimeoutSeconds: intPtr(35)}},
		},
	}
	for _, tc := range updateInvalid {
		t.Run("update/"+tc.name, func(t *testing.T) {
			st, env, _ := putDestination(t, router, created.ID, tc.req)
			if st != http.StatusUnprocessableEntity || env != "VALIDATION_FAILED" {
				t.Fatalf("status=%d code=%q, want 422 VALIDATION_FAILED", st, env)
			}
		})
	}

	// 3. Existing-target /test rejects switching bearer -> basic without credentials and out-of-range timeout.
	testInvalid := []struct {
		name string
		req  TestAlertDestinationRequest
	}{
		{
			name: "switch authType to basic without creds",
			req: TestAlertDestinationRequest{
				ID:     created.ID,
				Config: AlertDestinationConfigInfo{AuthType: stringPtr(AuthTypeBasic)},
			},
		},
		{
			name: "invalid timeoutSeconds",
			req: TestAlertDestinationRequest{
				ID:     created.ID,
				Config: AlertDestinationConfigInfo{TimeoutSeconds: intPtr(99)},
			},
		},
		{
			name: "draft invalid authType",
			req: TestAlertDestinationRequest{
				ChannelType: ChannelTypeWebhook,
				ImageMode:   ImageModeMultipart,
				Config: AlertDestinationConfigInfo{
					URL:      stringPtr(target.URL + "/events"),
					AuthType: stringPtr("bogus"),
				},
			},
		},
	}
	for _, tc := range testInvalid {
		t.Run("test/"+tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/alert-destinations/test",
				bytes.NewReader(mustMarshalJSON(t, tc.req)),
			)
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status=%d (%s), want 422", w.Code, w.Body.String())
			}
		})
	}

	// 4. Existing-target /test switching bearer -> none must strip old bearer token before sending.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/alert-destinations/test",
		bytes.NewReader(mustMarshalJSON(t, TestAlertDestinationRequest{
			ID:     created.ID,
			Config: AlertDestinationConfigInfo{AuthType: stringPtr(AuthTypeNone)},
		})),
	)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("test bearer->none status=%d: %s", w.Code, w.Body.String())
	}
	if receivedAuth != "" {
		t.Fatalf("switching authType to none on /test leaked Authorization=%q", receivedAuth)
	}
}

func TestHandlerDraftTestDoesNotPolluteLiveDestinationStatus(t *testing.T) {
	okTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer okTarget.Close()

	failTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer failTarget.Close()

	router, comp, cleanup := setupTestRouter(t)
	defer cleanup()
	if err := comp.Dispatcher.Start(t.Context()); err != nil {
		t.Fatalf("Dispatcher.Start: %v", err)
	}
	defer func() {
		if err := comp.Dispatcher.Stop(t.Context()); err != nil {
			t.Errorf("Dispatcher.Stop: %v", err)
		}
	}()

	code, envCode, created := postDestination(t, router, CreateAlertDestinationRequest{
		Name:        "Live Webhook",
		ChannelType: ChannelTypeWebhook,
		ImageMode:   ImageModeMultipart,
		Config: AlertDestinationConfigInfo{
			URL:            stringPtr(okTarget.URL + "/events"),
			AuthType:       stringPtr(AuthTypeNone),
			TimeoutSeconds: intPtr(5),
		},
	})
	if code != http.StatusOK {
		t.Fatalf("create failed: %d (%s)", code, envCode)
	}

	// 1. Saved-target quick test (ID only, no draft overrides) updates live status to connected.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/alert-destinations/test",
		bytes.NewReader(mustMarshalJSON(t, TestAlertDestinationRequest{ID: created.ID})),
	)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("saved quick test status=%d: %s", w.Code, w.Body.String())
	}
	st := comp.Dispatcher.GetStatus(created.ID, true)
	if st.State != StateConnected || st.ConsecutiveFailures != 0 {
		t.Fatalf("expected connected status after saved quick test, got %+v", st)
	}

	// 2. Edit-form draft test pointing to a failing URL must NOT pollute the live worker's status.
	wDraft := httptest.NewRecorder()
	reqDraft := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/alert-destinations/test",
		bytes.NewReader(mustMarshalJSON(t, TestAlertDestinationRequest{
			ID:          created.ID,
			ChannelType: ChannelTypeWebhook,
			ImageMode:   ImageModeMultipart,
			Config: AlertDestinationConfigInfo{
				URL:            stringPtr(failTarget.URL + "/events"),
				AuthType:       stringPtr(AuthTypeNone),
				TimeoutSeconds: intPtr(5),
			},
		})),
	)
	reqDraft.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(wDraft, reqDraft)
	if wDraft.Code != http.StatusOK {
		t.Fatalf("draft test status=%d: %s", wDraft.Code, wDraft.Body.String())
	}

	afterDraft := comp.Dispatcher.GetStatus(created.ID, true)
	if afterDraft.State != StateConnected || afterDraft.ConsecutiveFailures != 0 {
		t.Fatalf("draft test polluted live destination status: got %+v", afterDraft)
	}
}

func TestHandlerExistingMQTTTestValidatesClientID(t *testing.T) {
	router, _, cleanup := setupTestRouter(t)
	defer cleanup()

	code, envCode, created := postDestination(t, router, mqttCreateRequest("mqtt-test-cid", nil))
	if code != http.StatusOK {
		t.Fatalf("create failed: %d (%s)", code, envCode)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/alert-destinations/test",
		bytes.NewReader(mustMarshalJSON(t, TestAlertDestinationRequest{
			ID:          created.ID,
			ChannelType: ChannelTypeMQTT,
			Config: AlertDestinationConfigInfo{
				ClientID: stringPtr("client-id-exceeds-23-chars"),
			},
		})),
	)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for overlong clientId on existing MQTT /test, got %d: %s", w.Code, w.Body.String())
	}
	assertValidationDetail(t, w, "config.clientId", "MAX_LENGTH")
}

func TestHandlerWebhookTLSSkipVerifyRoundTrip(t *testing.T) {
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer tlsServer.Close()

	router, _, cleanup := setupTestRouter(t)
	defer cleanup()

	code, envCode, created := postDestination(t, router, CreateAlertDestinationRequest{
		Name:        "Self-Signed Hook",
		ChannelType: ChannelTypeWebhook,
		ImageMode:   ImageModeMultipart,
		Config: AlertDestinationConfigInfo{
			URL:           stringPtr(tlsServer.URL + "/events"),
			TLSSkipVerify: boolPtr(true),
		},
	})
	if code != http.StatusOK {
		t.Fatalf("create failed: %d (%s)", code, envCode)
	}
	if created.Config.TLSSkipVerify == nil || !*created.Config.TLSSkipVerify {
		t.Fatalf("expected tlsSkipVerify=true on create response, got %v", created.Config.TLSSkipVerify)
	}

	// Partial update omitting tlsSkipVerify must preserve true.
	code, _, renamed := putDestination(t, router, created.ID, UpdateAlertDestinationRequest{
		Name: stringPtr("Self-Signed Hook Renamed"),
	})
	if code != http.StatusOK || renamed.Config.TLSSkipVerify == nil || !*renamed.Config.TLSSkipVerify {
		t.Fatalf("partial update clobbered tlsSkipVerify: code=%d val=%v", code, renamed.Config.TLSSkipVerify)
	}

	// Test saved destination against self-signed TLS endpoint must succeed.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/alert-destinations/test",
		bytes.NewReader(mustMarshalJSON(t, TestAlertDestinationRequest{ID: created.ID})),
	)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("test status=%d: %s", w.Code, w.Body.String())
	}
	var env httputil.Response
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	resMap, ok := env.Data.(map[string]any)
	if !ok || resMap["ok"] != true {
		t.Fatalf("expected ok=true probing self-signed webhook with tlsSkipVerify=true, got %+v", env.Data)
	}

	// Explicit false disables tlsSkipVerify.
	code, _, cleared := putDestination(t, router, created.ID, UpdateAlertDestinationRequest{
		Config: &AlertDestinationConfigInfo{TLSSkipVerify: boolPtr(false)},
	})
	if code != http.StatusOK || cleared.Config.TLSSkipVerify == nil || *cleared.Config.TLSSkipVerify {
		t.Fatalf("explicit false failed to clear tlsSkipVerify: code=%d val=%v", code, cleared.Config.TLSSkipVerify)
	}
}

func TestSignedMediaHandlerConfigurableRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	signer, err := NewURLSigner(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("NewURLSigner: %v", err)
	}
	handler := NewSignedMediaHandlerWithOptions(SignedMediaHandlerOptions{
		Signer:     signer,
		RatePerMin: 60,
		RateBurst:  2,
	})

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/v1"))

	// First 2 requests consume the burst (returning 403 for missing sig); 3rd hits 429 rate limit.
	for i := range 3 {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/media/signed/snapshots/cam_1/a.jpg", nil)
		req.RemoteAddr = "203.0.113.10:12345"
		router.ServeHTTP(w, req)
		if i < 2 && w.Code != http.StatusForbidden {
			t.Fatalf("request %d status = %d, want 403 before burst exhausted", i+1, w.Code)
		}
		if i == 2 && w.Code != http.StatusTooManyRequests {
			t.Fatalf("request 3 status = %d, want 429 after configured burst=2 exhausted", w.Code)
		}
	}
}
