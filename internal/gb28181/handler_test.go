package gb28181

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	audithttp "github.com/nikonikowuw/Zhulong/internal/audit/http"
	"github.com/nikonikowuw/Zhulong/internal/camera"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type configEnvelope struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Data    ConfigResponse `json:"data"`
}

func newTestRouter(t *testing.T) (*gin.Engine, *Runtime, *Store, func()) {
	return newTestRouterWithAddrs(t, fixedAddrs("192.168.1.10"))
}

func newTestRouterWithAddrs(t *testing.T, addrs InterfaceAddrs) (*gin.Engine, *Runtime, *Store, func()) {
	t.Helper()

	dbStore := database.New(t.TempDir(), zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	comp := NewComponents(ComponentsOptions{
		DBProvider: dbStore,
		Cipher:     fakeCipher{},
		Addrs:      addrs,
		Logger:     zap.NewNop(),
	})

	// Run the lifecycle so startupConfig reflects what a real boot would load.
	if err := comp.Lifecycle.Start(context.Background()); err != nil {
		t.Fatalf("lifecycle start failed: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	comp.ProtectedRoutes.RegisterRoutes(router.Group("/api/v1"))

	return router, comp.Runtime, comp.Store, func() {
		_ = comp.Lifecycle.Stop(context.Background())
		_ = dbStore.Close()
	}
}

func doJSON(t *testing.T, router *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func decodeEnvelope(t *testing.T, w *httptest.ResponseRecorder) configEnvelope {
	t.Helper()
	var env configEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("failed to decode envelope: %v (body: %s)", err, w.Body.String())
	}
	return env
}

func TestHandlerGetConfigDefaults(t *testing.T) {
	router, _, _, cleanup := newTestRouter(t)
	defer cleanup()

	w := doJSON(t, router, http.MethodGet, "/api/v1/gb28181/config", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	env := decodeEnvelope(t, w)
	if env.Code != "OK" {
		t.Fatalf("expected OK, got %s", env.Code)
	}
	if env.Data.Config.Enabled {
		t.Error("expected GB28181 to be disabled by default")
	}
	if env.Data.Runtime.State != RuntimeStateDisabled {
		t.Errorf("expected state %q, got %q", RuntimeStateDisabled, env.Data.Runtime.State)
	}
	if env.Data.Runtime.RestartRequired {
		t.Error("expected no restart requirement right after boot")
	}
	if env.Data.Runtime.MediaCapacity != 50 {
		t.Errorf("expected 50 playback pairs for the default range, got %d", env.Data.Runtime.MediaCapacity)
	}
}

func TestHandlerUpdateConfigMarksRestartRequired(t *testing.T) {
	router, runtime, _, cleanup := newTestRouter(t)
	defer cleanup()

	body := `{
		"enabled": true,
		"sipId": "34020000002000000001",
		"sipListen": "0.0.0.0:5060",
		"sipTransport": "both",
		"securityProfile": "standard",
		"mediaBindAddress": "0.0.0.0",
		"mediaAdvertisedIp": "",
		"rtpPortMin": 30000,
		"rtpPortMax": 30100,
		"requireRegisteredDevice": true
	}`
	w := doJSON(t, router, http.MethodPut, "/api/v1/gb28181/config", body)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	env := decodeEnvelope(t, w)
	if !env.Data.Config.Enabled {
		t.Fatal("expected the saved configuration to be enabled")
	}
	// The single-NIC rule fills the advertised address at save time so a stored
	// configuration is always bootable.
	if env.Data.Config.MediaAdvertisedIP != "192.168.1.10" {
		t.Errorf("expected the advertised address to be resolved at save time, got %q",
			env.Data.Config.MediaAdvertisedIP)
	}
	// Saving must not touch the running listener: the startup snapshot still has
	// enabled=false, so the UI can honestly say "restart required".
	if env.Data.Runtime.StartupConfig.Enabled {
		t.Error("the running configuration must not change on save")
	}
	if !env.Data.Runtime.RestartRequired {
		t.Error("expected restartRequired after saving a different target")
	}
	if runtime.Status().State != RuntimeStateDisabled {
		t.Errorf("expected the process to keep its disabled state, got %q", runtime.Status().State)
	}
}

func TestHandlerUpdateConfigRequiresExplicitAddressWhenInferenceIsAmbiguous(t *testing.T) {
	router, _, _, cleanup := newTestRouterWithAddrs(t, fixedAddrs("192.168.1.10", "10.0.0.5"))
	defer cleanup()

	body := `{"enabled":true,"sipId":"34020000002000000001","sipListen":"0.0.0.0:5060","sipTransport":"both","securityProfile":"standard","mediaBindAddress":"192.168.1.10","mediaAdvertisedIp":"","rtpPortMin":30000,"rtpPortMax":30100,"requireRegisteredDevice":true}`
	w := doJSON(t, router, http.MethodPut, "/api/v1/gb28181/config", body)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d (%s)", w.Code, w.Body.String())
	}
	if code := decodeEnvelope(t, w).Code; code != CodeAddressUnresolvable {
		t.Fatalf("expected %s, got %s (%s)", CodeAddressUnresolvable, code, w.Body.String())
	}
}

func TestHandlerUpdateConfigRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantCode string
	}{
		{
			name:     "reversed port range is a domain error",
			body:     `{"enabled": true, "sipId": "3402", "rtpPortMin": 30100, "rtpPortMax": 30000, "requireRegisteredDevice": true}`,
			wantCode: CodePortRangeInvalid,
		},
		{
			name:     "single port range cannot hold a pair",
			body:     `{"enabled": true, "sipId": "3402", "rtpPortMin": 30000, "rtpPortMax": 30000, "requireRegisteredDevice": true}`,
			wantCode: CodePortRangeInvalid,
		},
		{
			name:     "unsupported security profile is stopped by binding",
			body:     `{"enabled": true, "sipId": "3402", "rtpPortMin": 30000, "rtpPortMax": 30100, "securityProfile": "high", "requireRegisteredDevice": true}`,
			wantCode: "VALIDATION_FAILED",
		},
		{
			name:     "missing ports are stopped by binding",
			body:     `{"enabled": false}`,
			wantCode: "VALIDATION_FAILED",
		},
		{
			name:     "enabled without identity is a domain error",
			body:     `{"enabled": true, "rtpPortMin": 30000, "rtpPortMax": 30100, "requireRegisteredDevice": true}`,
			wantCode: CodeConfigInvalid,
		},
		{
			name:     "registered-device enforcement cannot be disabled",
			body:     `{"enabled": false, "rtpPortMin": 30000, "rtpPortMax": 30100, "requireRegisteredDevice": false}`,
			wantCode: CodeRegisteredDeviceRequired,
		},
		{
			name:     "registered-device enforcement is required",
			body:     `{"enabled": false, "rtpPortMin": 30000, "rtpPortMax": 30100}`,
			wantCode: "VALIDATION_FAILED",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			router, _, _, cleanup := newTestRouter(t)
			defer cleanup()

			w := doJSON(t, router, http.MethodPut, "/api/v1/gb28181/config", tc.body)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("expected 422, got %d (%s)", w.Code, w.Body.String())
			}
			env := decodeEnvelope(t, w)
			if env.Code != tc.wantCode {
				t.Fatalf("expected code %s, got %s (%s)", tc.wantCode, env.Code, w.Body.String())
			}
		})
	}
}

func TestHandlerUpsertDeviceRequiresStandardDeviceID(t *testing.T) {
	router, _, _, cleanup := newTestRouter(t)
	defer cleanup()

	w := doJSON(t, router, http.MethodPut, "/api/v1/gb28181/devices/device-1",
		`{"name":"测试设备","password":"secret"}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d (%s)", w.Code, w.Body.String())
	}
	if code := decodeEnvelope(t, w).Code; code != CodeDeviceIDInvalid {
		t.Fatalf("expected %s, got %s", CodeDeviceIDInvalid, code)
	}
}

func TestHandlerUpsertDeviceRequiresPasswordAndHidesIt(t *testing.T) {
	router, _, _, cleanup := newTestRouter(t)
	defer cleanup()

	// A new device without a password is rejected: an allowlist entry nobody can
	// authenticate against is worse than no entry at all.
	w := doJSON(t, router, http.MethodPut, "/api/v1/gb28181/devices/34020000001320000001", `{"name":"大厅"}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d (%s)", w.Code, w.Body.String())
	}
	if code := decodeEnvelope(t, w).Code; code != CodePasswordRequired {
		t.Fatalf("expected %s, got %s", CodePasswordRequired, code)
	}

	// With a password the write succeeds and the secret never comes back.
	w = doJSON(t, router, http.MethodPut, "/api/v1/gb28181/devices/34020000001320000001",
		`{"name":"大厅","password":"plaintext-digest","transport":"udp"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "plaintext-digest") {
		t.Fatal("the response leaked the plaintext password")
	}
	if strings.Contains(strings.ToLower(body), "password") {
		t.Fatalf("the response must not expose a password field: %s", body)
	}
	if strings.Contains(body, "aad:") {
		t.Fatal("the response leaked ciphertext")
	}

	var env struct {
		Code string         `json:"code"`
		Data DeviceResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("failed to decode device response: %v", err)
	}
	if env.Data.DeviceID != "34020000001320000001" {
		t.Fatalf("unexpected device id %q", env.Data.DeviceID)
	}
	if env.Data.State != DeviceStateOffline {
		t.Fatalf("expected a freshly provisioned device to be offline, got %q", env.Data.State)
	}
}

func TestHandlerListDevicesReturnsEmptyArray(t *testing.T) {
	router, _, _, cleanup := newTestRouter(t)
	defer cleanup()

	w := doJSON(t, router, http.MethodGet, "/api/v1/gb28181/devices", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	var env struct {
		Code string             `json:"code"`
		Data DeviceListResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("failed to decode device list: %v", err)
	}
	if env.Data.Items == nil {
		t.Fatal("items must be an empty array, never null")
	}
	if len(env.Data.Items) != 0 {
		t.Fatalf("expected no devices, got %d", len(env.Data.Items))
	}
}

func TestHandlerGetRuntimeCountsProvisionedDevices(t *testing.T) {
	router, _, _, cleanup := newTestRouter(t)
	defer cleanup()

	w := doJSON(t, router, http.MethodPut, "/api/v1/gb28181/devices/34020000001320000002",
		`{"name":"走廊","password":"another-secret"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}

	w = doJSON(t, router, http.MethodGet, "/api/v1/gb28181/runtime", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	var env struct {
		Code string                `json:"code"`
		Data RuntimeSnapshotStatus `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("failed to decode runtime snapshot: %v", err)
	}
	if env.Data.Devices.Total != 1 {
		t.Fatalf("expected one provisioned device, got %d", env.Data.Devices.Total)
	}
	// Online counts are wired in S1; a provisioned device is not registered yet.
	if env.Data.Devices.Online != 0 {
		t.Fatalf("expected zero online devices before S1, got %d", env.Data.Devices.Online)
	}
	if env.Data.PortPool.PairsTotal != 50 {
		t.Fatalf("expected 50 port pairs, got %d", env.Data.PortPool.PairsTotal)
	}
	// The protection counters are part of the contract the UI reads: an absent object
	// would leave a firing cap invisible in the console.
	var raw struct {
		Data struct {
			Ingress map[string]any `json:"ingress"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("failed to decode runtime snapshot: %v", err)
	}
	for _, key := range []string{"oversizedMessages", "rejectedConns", "inboundLimited", "outboundLimited", "activeConns"} {
		if _, ok := raw.Data.Ingress[key]; !ok {
			t.Fatalf("expected %q in the ingress counters, got %v", key, raw.Data.Ingress)
		}
	}
}

func TestHandlerUpdateConfigAppliesPortDefaultsWhenBothAreZero(t *testing.T) {
	router, _, _, cleanup := newTestRouter(t)
	defer cleanup()

	// The domain model documents "both zero means not provided", so the HTTP layer
	// must not reject that shape at bind time: otherwise the fallback is unreachable
	// and the two contracts disagree.
	body := `{
		"enabled": true,
		"sipId": "34020000002000000001",
		"rtpPortMin": 0,
		"rtpPortMax": 0,
		"requireRegisteredDevice": true
	}`
	w := doJSON(t, router, http.MethodPut, "/api/v1/gb28181/config", body)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	env := decodeEnvelope(t, w)
	if env.Data.Config.RTPPortMin != DefaultRTPPortMin || env.Data.Config.RTPPortMax != DefaultRTPPortMax {
		t.Fatalf("expected the default port range, got %d..%d",
			env.Data.Config.RTPPortMin, env.Data.Config.RTPPortMax)
	}
}

func TestHandlerUpdateConfigRejectsHalfZeroPortPair(t *testing.T) {
	router, _, _, cleanup := newTestRouter(t)
	defer cleanup()

	// A single zero is a real input error, not "not provided": defaulting it would
	// hand a client that asked for port 0 a working range and no error.
	body := `{
		"enabled": true,
		"sipId": "34020000002000000001",
		"rtpPortMin": 0,
		"rtpPortMax": 30010,
		"requireRegisteredDevice": true
	}`
	w := doJSON(t, router, http.MethodPut, "/api/v1/gb28181/config", body)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d (%s)", w.Code, w.Body.String())
	}
	if code := decodeEnvelope(t, w).Code; code != CodePortRangeInvalid {
		t.Fatalf("expected %s, got %s", CodePortRangeInvalid, code)
	}
}

func TestHandlerUpsertDeviceKeepsNameWhenOnlyPasswordChanges(t *testing.T) {
	router, _, _, cleanup := newTestRouter(t)
	defer cleanup()

	w := doJSON(t, router, http.MethodPut, "/api/v1/gb28181/devices/34020000001320000003",
		`{"name":"机房","password":"first-secret"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}

	// Rotating the Digest secret without restating the name must not blank it.
	w = doJSON(t, router, http.MethodPut, "/api/v1/gb28181/devices/34020000001320000003",
		`{"password":"second-secret"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	var env struct {
		Code string         `json:"code"`
		Data DeviceResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("failed to decode device response: %v", err)
	}
	if env.Data.Name != "机房" {
		t.Fatalf("expected the name to survive a password rotation, got %q", env.Data.Name)
	}
}

// TestHandlerStoreFailureReportsDatabaseError pins the code/status pairing: a
// repository failure must not be reported as a configuration error, because the
// frontend branches on the code and would tell the operator to fix a valid config.
func TestHandlerStoreFailureReportsDatabaseError(t *testing.T) {
	// A store without a database provider fails every read, which is exactly the
	// out-of-band condition this contract is about.
	store := NewStore(nil, nil)
	service := NewService(ServiceOptions{
		Configs: store,
		Devices: store,
		Runtime: NewRuntime(),
		Addrs:   fixedAddrs("192.168.1.10"),
	})
	handler := NewHandler(service, zap.NewNop())

	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/v1"))

	for _, path := range []string{"/api/v1/gb28181/config", "/api/v1/gb28181/devices"} {
		w := doJSON(t, router, http.MethodGet, path, "")
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("%s: expected 500, got %d (%s)", path, w.Code, w.Body.String())
		}
		env := decodeEnvelope(t, w)
		if env.Code != CodeDatabaseError {
			t.Fatalf("%s: expected %s, got %s", path, CodeDatabaseError, env.Code)
		}
		if strings.Contains(strings.ToLower(env.Message), "invalid") {
			t.Fatalf("%s: a store failure must not be described as an invalid config: %q",
				path, env.Message)
		}
	}
}

// TestHandlerAuditRecordsConcreteDeviceTarget guards the audit trail's essential
// field: which device was provisioned or had its secret rotated.
func TestHandlerAuditRecordsConcreteDeviceTarget(t *testing.T) {
	dbStore := database.New(t.TempDir(), zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	defer func() { _ = dbStore.Close() }()

	comp := NewComponents(ComponentsOptions{
		DBProvider: dbStore,
		Cipher:     fakeCipher{},
		Addrs:      fixedAddrs("192.168.1.10"),
		Logger:     zap.NewNop(),
	})

	auditSvc := audit.NewService(audit.NewStore(dbStore), zap.NewNop(), audit.Config{
		BufferSize: 100, BatchSize: 10, FlushInterval: 5 * time.Millisecond, MaxEntries: 1000,
	})
	if err := auditSvc.Start(context.Background()); err != nil {
		t.Fatalf("start audit service: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	api := router.Group("/api/v1")
	api.Use(audithttp.Middleware(auditSvc))
	comp.ProtectedRoutes.RegisterRoutes(api)

	const deviceID = "34020000001320000009"
	w := doJSON(t, router, http.MethodPut, "/api/v1/gb28181/devices/"+deviceID,
		`{"name":"门厅","password":"audited-secret"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := auditSvc.Stop(stopCtx); err != nil {
		t.Fatalf("drain audit service: %v", err)
	}
	entries, total, err := auditSvc.List(context.Background(), audit.Filter{Limit: 20})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected one audit entry, got %d", total)
	}
	entry := entries[0]
	if entry.Action != ActionDeviceUpsert {
		t.Fatalf("expected action %s, got %s", ActionDeviceUpsert, entry.Action)
	}
	if want := "gb28181_device:" + deviceID; entry.Target != want {
		t.Fatalf("expected target %q, got %q", want, entry.Target)
	}
	if strings.Contains(entry.Detail, "audited-secret") {
		t.Fatalf("the digest password leaked into the audit detail: %q", entry.Detail)
	}
}

type mockCameraCreator struct {
	db        *gorm.DB
	createdID string
	calls     int
	deleted   []string
}

func (m *mockCameraCreator) CreateGB28181Camera(ctx context.Context, params camera.CreateGB28181CameraParams) (*camera.CameraResponse, error) {
	m.calls++
	m.createdID = "cam_01JTEST0000000000000000"
	if m.db != nil {
		if err := m.db.Exec("INSERT INTO cameras (id, name, revision, created_at, updated_at) VALUES (?, ?, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)", m.createdID, params.Name).Error; err != nil {
			return nil, fmt.Errorf("insert camera fixture: %w", err)
		}
	}
	return &camera.CameraResponse{
		ID:   m.createdID,
		Name: params.Name,
		Streams: []camera.StreamResponse{
			{
				Protocol: camera.ProtocolGB28181,
				RTSPURL:  fmt.Sprintf("gb28181://%s/%s", params.DeviceID, params.ChannelID),
			},
		},
	}, nil
}

func (m *mockCameraCreator) Delete(ctx context.Context, id string) error {
	m.deleted = append(m.deleted, id)
	if m.db == nil {
		return nil
	}
	return m.db.Exec("DELETE FROM cameras WHERE id = ?", id).Error
}

type mockCatalogSyncer struct {
	store    ChannelStore
	channels []Channel
	err      error
}

func (m *mockCatalogSyncer) Sync(ctx context.Context, device Device) ([]Channel, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.store != nil {
		if err := m.store.SyncChannels(ctx, device.DeviceID, m.channels); err != nil {
			return nil, err
		}
	}
	return m.channels, nil
}

func TestHandlerChannelAndCatalogEndpoints(t *testing.T) {
	dbStore := database.New(t.TempDir(), zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	defer func() { _ = dbStore.Close() }()

	camCreator := &mockCameraCreator{db: dbStore.DB()}
	catSyncer := &mockCatalogSyncer{}

	comp := NewComponents(ComponentsOptions{
		DBProvider:    dbStore,
		Cipher:        fakeCipher{},
		CameraCreator: camCreator,
		CameraDeleter: camCreator,
		Catalog:       catSyncer,
		Logger:        zap.NewNop(),
	})
	catSyncer.store = comp.Store
	if err := comp.Lifecycle.Start(context.Background()); err != nil {
		t.Fatalf("lifecycle start failed: %v", err)
	}
	defer func() { _ = comp.Lifecycle.Stop(context.Background()) }()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	comp.ProtectedRoutes.RegisterRoutes(router.Group("/api/v1"))

	devID := "34020000001180000001"
	chanID := "34020000001310000001"

	// 1. Provision device
	w := doJSON(t, router, http.MethodPut, "/api/v1/gb28181/devices/"+devID, `{"name":"Device 1","password":"secret"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("provision device: status=%d body=%s", w.Code, w.Body.String())
	}

	// 2. List channels before sync -> empty list
	w = doJSON(t, router, http.MethodGet, "/api/v1/gb28181/devices/"+devID+"/channels", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list channels: status=%d body=%s", w.Code, w.Body.String())
	}
	var listResp struct {
		Data ChannelListResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal list channels: %v", err)
	}
	if len(listResp.Data.Items) != 0 {
		t.Fatalf("expected 0 channels, got %d", len(listResp.Data.Items))
	}

	// 3. Trigger sync while offline -> 422 GB28181_DEVICE_OFFLINE
	w = doJSON(t, router, http.MethodPost, "/api/v1/gb28181/devices/"+devID+"/catalog", "")
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for offline sync, got %d body=%s", w.Code, w.Body.String())
	}

	// 4. Mark device online and set syncer return
	db := dbStore.DB()
	remoteAddr := "192.168.1.100:5060"
	if err := db.Model(&Device{}).Where("device_id = ?", devID).Updates(map[string]any{
		"state":       "online",
		"remote_addr": remoteAddr,
	}).Error; err != nil {
		t.Fatalf("mark device online: %v", err)
	}

	w1, h1 := 1920, 1080
	catSyncer.channels = []Channel{
		{
			DeviceID:         devID,
			ChannelID:        chanID,
			Name:             "Gate Camera",
			ResolutionWidth:  &w1,
			ResolutionHeight: &h1,
		},
	}

	// 5. Trigger sync while online -> 200 OK
	w = doJSON(t, router, http.MethodPost, "/api/v1/gb28181/devices/"+devID+"/catalog", "")
	if w.Code != http.StatusOK {
		t.Fatalf("catalog sync online: status=%d body=%s", w.Code, w.Body.String())
	}
	var syncResp struct {
		Data SyncCatalogResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &syncResp); err != nil {
		t.Fatalf("unmarshal sync resp: %v", err)
	}
	if syncResp.Data.Total != 1 {
		t.Fatalf("expected total 1, got %d", syncResp.Data.Total)
	}

	// 6. List channels -> 1 channel
	w = doJSON(t, router, http.MethodGet, "/api/v1/gb28181/devices/"+devID+"/channels", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list channels: status=%d body=%s", w.Code, w.Body.String())
	}
	_ = json.Unmarshal(w.Body.Bytes(), &listResp)
	if len(listResp.Data.Items) != 1 || listResp.Data.Items[0].ChannelID != chanID {
		t.Fatalf("expected channel %s, got %+v", chanID, listResp.Data.Items)
	}
	if listResp.Data.Items[0].CameraID != nil {
		t.Fatalf("expected camera_id to be nil initially")
	}

	// 7. Add channel as camera -> creates camera
	w = doJSON(t, router, http.MethodPost, fmt.Sprintf("/api/v1/gb28181/devices/%s/channels/%s/camera", devID, chanID), "")
	if w.Code != http.StatusOK {
		t.Fatalf("add channel as camera: status=%d body=%s", w.Code, w.Body.String())
	}
	if camCreator.calls != 1 {
		t.Fatalf("expected 1 call to CameraCreator, got %d", camCreator.calls)
	}

	// 8. Add channel again -> 409 GB28181_CHANNEL_ALREADY_BOUND
	w = doJSON(t, router, http.MethodPost, fmt.Sprintf("/api/v1/gb28181/devices/%s/channels/%s/camera", devID, chanID), "")
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 conflict when adding already bound channel, got %d body=%s", w.Code, w.Body.String())
	}

	// 9. Delete device with bound camera -> 409 GB28181_CAMERA_BOUND
	w = doJSON(t, router, http.MethodDelete, "/api/v1/gb28181/devices/"+devID, "")
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 conflict when deleting device with bound camera, got %d body=%s", w.Code, w.Body.String())
	}

	// 10. Delete the camera (FK ON DELETE SET NULL clears the binding), then the device
	if err := camCreator.Delete(context.Background(), camCreator.createdID); err != nil {
		t.Fatalf("delete camera: %v", err)
	}
	ch, err := comp.Store.GetChannel(context.Background(), devID, chanID)
	if err != nil {
		t.Fatalf("get channel: %v", err)
	}
	if ch.CameraID != nil {
		t.Fatalf("expected camera binding cleared by ON DELETE SET NULL, got %v", *ch.CameraID)
	}
	w = doJSON(t, router, http.MethodDelete, "/api/v1/gb28181/devices/"+devID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("delete device: status=%d body=%s", w.Code, w.Body.String())
	}
}
