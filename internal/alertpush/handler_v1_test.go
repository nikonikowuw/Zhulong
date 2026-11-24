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
			Broker:      "tcp://broker.local:1883",
			Topic:       "zhulong/events",
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
				if envCode != CodeAlertDestinationConfigInvalid {
					t.Fatalf("code = %q, want %q", envCode, CodeAlertDestinationConfigInvalid)
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
		Config: &AlertDestinationConfigInfo{InlineMaxKB: intPtr(9000)},
	})
	if code != http.StatusUnprocessableEntity || envCode != CodeAlertDestinationConfigInvalid {
		t.Fatalf("out-of-range update status = %d code = %q, want 422", code, envCode)
	}

	code, envCode, webhook := postDestination(t, router, CreateAlertDestinationRequest{
		Name:        "webhook-inline",
		ChannelType: ChannelTypeWebhook,
		ImageMode:   ImageModeMultipart,
		Config: AlertDestinationConfigInfo{
			URL:         "https://example.com/hook",
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
			Config:      AlertDestinationConfigInfo{URL: target.URL + "/events"},
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
		probe.Seq != nil && *probe.Seq == 0 && probe.Alarm != nil &&
		probe.Alarm.AlarmType != nil && *probe.Alarm.AlarmType == "test_ping" &&
		probe.Source != nil && probe.Source.Camera.ID == "test_cam"
	if !probeOK {
		t.Fatalf("unexpected probe payload: %s", gotBody)
	}
	if gotHeaders.Get(headerZhulongEvent) != EventTypeAlarm || gotHeaders.Get(headerZhulongSchema) != "1" {
		t.Fatalf("unexpected probe headers: %v", gotHeaders)
	}
}
