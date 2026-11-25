package alertpush

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/nikonikowuw/Zhulong/internal/audit"
	audithttp "github.com/nikonikowuw/Zhulong/internal/audit/http"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/httpmiddleware"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
)

// minimalMQTTBroker accepts MQTT 3.1.1 CONNECT / PUBLISH / DISCONNECT packets over TCP
// and captures CONNECT credentials (username, password, keepAlive) and PUBLISH topics.
type minimalMQTTBroker struct {
	ln        net.Listener
	mu        sync.Mutex
	connects  []capturedMQTTConnect
	publishes []string
	wg        sync.WaitGroup
}

type capturedMQTTConnect struct {
	clientID  string
	username  string
	password  string
	keepAlive uint16
}

func startMinimalMQTTBroker(t *testing.T) *minimalMQTTBroker {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen mock mqtt broker: %v", err)
	}
	b := &minimalMQTTBroker{ln: ln}
	b.wg.Add(1)
	go b.acceptLoop()
	t.Cleanup(func() {
		_ = ln.Close()
		b.wg.Wait()
	})
	return b
}

func (b *minimalMQTTBroker) addr() string {
	return "tcp://" + b.ln.Addr().String()
}

func (b *minimalMQTTBroker) acceptLoop() {
	defer b.wg.Done()
	for {
		conn, err := b.ln.Accept()
		if err != nil {
			return
		}
		b.wg.Add(1)
		go func(c net.Conn) {
			defer b.wg.Done()
			defer func() { _ = c.Close() }()
			b.handleConn(c)
		}(conn)
	}
}

func (b *minimalMQTTBroker) handleConn(conn net.Conn) {
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(conn)
	for {
		hdr, err := r.ReadByte()
		if err != nil {
			return
		}
		remLen, err := readMQTTRemainingLength(r)
		if err != nil {
			return
		}
		payload := make([]byte, remLen)
		if _, err := io.ReadFull(r, payload); err != nil {
			return
		}
		pktType := hdr >> 4
		switch pktType {
		case 1: // CONNECT
			captured := parseMQTTConnectPacket(payload)
			b.mu.Lock()
			b.connects = append(b.connects, captured)
			b.mu.Unlock()
			// CONNACK: return code 0 (accepted)
			if _, err := conn.Write([]byte{0x20, 0x02, 0x00, 0x00}); err != nil {
				return
			}
		case 3: // PUBLISH
			qos := (hdr >> 1) & 0x03
			if len(payload) >= 2 {
				topicLen := int(binary.BigEndian.Uint16(payload[0:2]))
				if 2+topicLen <= len(payload) {
					topic := string(payload[2 : 2+topicLen])
					b.mu.Lock()
					b.publishes = append(b.publishes, topic)
					b.mu.Unlock()
					if qos == 1 && 2+topicLen+2 <= len(payload) {
						pktID := payload[2+topicLen : 2+topicLen+2]
						_, _ = conn.Write([]byte{0x40, 0x02, pktID[0], pktID[1]})
					}
				}
			}
		case 12: // PINGREQ
			_, _ = conn.Write([]byte{0xD0, 0x00})
		case 14: // DISCONNECT
			return
		}
	}
}

func readMQTTRemainingLength(r *bufio.Reader) (int, error) {
	mult := 1
	val := 0
	for {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		val += int(b&0x7f) * mult
		if (b & 0x80) == 0 {
			return val, nil
		}
		mult *= 128
	}
}

func parseMQTTConnectPacket(p []byte) capturedMQTTConnect {
	readStr := func(pos int) (string, int) {
		if pos+2 > len(p) {
			return "", len(p)
		}
		n := int(binary.BigEndian.Uint16(p[pos : pos+2]))
		pos += 2
		if pos+n > len(p) {
			return "", len(p)
		}
		return string(p[pos : pos+n]), pos + n
	}
	// Protocol Name + Protocol Level (1 byte) + Connect Flags (1 byte) + KeepAlive (2 bytes)
	_, pos := readStr(0)
	if pos+4 > len(p) {
		return capturedMQTTConnect{}
	}
	flags := p[pos+1]
	keepAlive := binary.BigEndian.Uint16(p[pos+2 : pos+4])
	pos += 4

	var out capturedMQTTConnect
	out.keepAlive = keepAlive
	out.clientID, pos = readStr(pos)
	if flags&0x04 != 0 { // will flag
		_, pos = readStr(pos)
		_, pos = readStr(pos)
	}
	if flags&0x80 != 0 { // username flag
		out.username, pos = readStr(pos)
	}
	if flags&0x40 != 0 { // password flag
		out.password, _ = readStr(pos)
	}
	return out
}

func (b *minimalMQTTBroker) snapshot() ([]capturedMQTTConnect, []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := make([]capturedMQTTConnect, len(b.connects))
	copy(c, b.connects)
	p := make([]string, len(b.publishes))
	copy(p, b.publishes)
	return c, p
}

// TestHandlerTestCrossOriginWebhookWipesStoredSecrets pins U2 (P0): overriding a saved
// webhook URL to a different origin during /test must never leak stored credentials,
// signingSecret, or custom headers to the new host.
func TestHandlerTestCrossOriginWebhookWipesStoredSecrets(t *testing.T) {
	var (
		mu              sync.Mutex
		trustedAuth     string
		trustedCustom   string
		trustedSig      string
		evilAuth        string
		evilCustom      string
		evilSig         string
		evilHitCount    int
		trustedHitCount int
	)
	trusted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		trustedAuth = r.Header.Get("Authorization")
		trustedCustom = r.Header.Get("X-Secret-Header")
		trustedSig = r.Header.Get(headerZhulongSignature)
		trustedHitCount++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer trusted.Close()

	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		evilAuth = r.Header.Get("Authorization")
		evilCustom = r.Header.Get("X-Secret-Header")
		evilSig = r.Header.Get(headerZhulongSignature)
		evilHitCount++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer evil.Close()

	router, _, cleanup := setupTestRouter(t)
	defer cleanup()

	code, envCode, created := postDestination(t, router, CreateAlertDestinationRequest{
		Name:        "Trusted Hook",
		ChannelType: ChannelTypeWebhook,
		ImageMode:   ImageModeMultipart,
		Config: AlertDestinationConfigInfo{
			URL:      stringPtr(trusted.URL + "/events"),
			AuthType: stringPtr(AuthTypeBearer),
		},
		Secret: &AlertDestinationSecretInfo{
			Token:         stringPtr("super-secret-token"),
			SigningSecret: stringPtr("hmac-signing-key"),
			Headers:       map[string]string{"X-Secret-Header": "top-secret-val"},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("create failed: %d (%s)", code, envCode)
	}

	// 1. Overriding URL to a different origin while keeping authType=bearer without new token
	// must wipe the stored token and fail with 422 secret.token REQUIRED (evil never contacted).
	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/alert-destinations/test",
		bytes.NewReader(mustMarshalJSON(t, TestAlertDestinationRequest{
			ID:     created.ID,
			Config: AlertDestinationConfigInfo{URL: stringPtr(evil.URL + "/steal")},
		})),
	)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("cross-origin bearer override status=%d, want 422 (%s)", w.Code, w.Body.String())
	}
	assertValidationDetail(t, w, "secret.token", "REQUIRED")

	// 2. Overriding URL to a different origin AND switching authType=none must NOT leak
	// stored custom headers or signingSecret to evil.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/alert-destinations/test",
		bytes.NewReader(mustMarshalJSON(t, TestAlertDestinationRequest{
			ID: created.ID,
			Config: AlertDestinationConfigInfo{
				URL:      stringPtr(evil.URL + "/steal"),
				AuthType: stringPtr(AuthTypeNone),
			},
		})),
	)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("cross-origin none override status=%d (%s)", w.Code, w.Body.String())
	}
	mu.Lock()
	if evilHitCount != 1 {
		t.Fatalf("evilHitCount = %d, want 1", evilHitCount)
	}
	if evilAuth != "" || evilCustom != "" || evilSig != "" {
		t.Fatalf("stored secrets leaked cross-origin: auth=%q custom=%q sig=%q", evilAuth, evilCustom, evilSig)
	}
	mu.Unlock()

	// 3. Same-origin path override MUST still reuse stored token, custom header, and signingSecret.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/alert-destinations/test",
		bytes.NewReader(mustMarshalJSON(t, TestAlertDestinationRequest{
			ID:     created.ID,
			Config: AlertDestinationConfigInfo{URL: stringPtr(trusted.URL + "/events-v2")},
		})),
	)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("same-origin override status=%d (%s)", w.Code, w.Body.String())
	}
	mu.Lock()
	if trustedHitCount != 1 || trustedAuth != "Bearer super-secret-token" ||
		trustedCustom != "top-secret-val" || !strings.HasPrefix(trustedSig, "sha256=") {
		t.Fatalf(
			"same-origin override lost credentials: hits=%d auth=%q custom=%q sig=%q",
			trustedHitCount,
			trustedAuth,
			trustedCustom,
			trustedSig,
		)
	}
	mu.Unlock()
}

// TestHandlerTestMQTTBrokerOverrideWipesPasswordAndKeepAlive pins U2 + U21 + U26:
// end-to-end MQTT /test respects KeepAlive and wipes stored password on broker change.
func TestHandlerTestMQTTBrokerOverrideWipesPasswordAndKeepAlive(t *testing.T) {
	brokerA := startMinimalMQTTBroker(t)
	brokerB := startMinimalMQTTBroker(t)

	router, _, cleanup := setupTestRouter(t)
	defer cleanup()

	code, envCode, created := postDestination(t, router, CreateAlertDestinationRequest{
		Name:        "Saved MQTT Broker",
		ChannelType: ChannelTypeMQTT,
		ImageMode:   ImageModeBase64,
		Config: AlertDestinationConfigInfo{
			Broker:           stringPtr(brokerA.addr()),
			Topic:            stringPtr("zhulong/alerts"),
			Username:         stringPtr("edge-user"),
			KeepAliveSeconds: intPtr(45),
		},
		Secret: &AlertDestinationSecretInfo{
			Password: stringPtr("mqtt-secret-pass"),
		},
	})
	if code != http.StatusOK {
		t.Fatalf("create mqtt failed: %d (%s)", code, envCode)
	}

	// 1. Saved quick test connects to brokerA with stored password and configured keepAlive=45.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/alert-destinations/test",
		bytes.NewReader(mustMarshalJSON(t, TestAlertDestinationRequest{
			ID:          created.ID,
			ChannelType: ChannelTypeMQTT,
		})),
	)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("saved mqtt test status=%d (%s)", w.Code, w.Body.String())
	}
	connA, pubA := brokerA.snapshot()
	if len(connA) != 1 || connA[0].password != "mqtt-secret-pass" || connA[0].keepAlive != 45 {
		t.Fatalf("brokerA unexpected connect: %+v", connA)
	}
	if len(pubA) != 1 || pubA[0] != "zhulong/alerts" {
		t.Fatalf("brokerA unexpected publishes: %+v", pubA)
	}

	// 2. Overriding broker to brokerB without new password MUST wipe stored password.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/alert-destinations/test",
		bytes.NewReader(mustMarshalJSON(t, TestAlertDestinationRequest{
			ID:          created.ID,
			ChannelType: ChannelTypeMQTT,
			Config:      AlertDestinationConfigInfo{Broker: stringPtr(brokerB.addr())},
		})),
	)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("broker override test status=%d (%s)", w.Code, w.Body.String())
	}
	connB, _ := brokerB.snapshot()
	if len(connB) != 1 {
		t.Fatalf("expected 1 connect to brokerB, got %d", len(connB))
	}
	if connB[0].password != "" {
		t.Fatalf("stored MQTT password leaked to overridden brokerB: %q", connB[0].password)
	}

	// 3. Overriding broker to brokerB WITH an explicit new password sends only the new password.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/alert-destinations/test",
		bytes.NewReader(mustMarshalJSON(t, TestAlertDestinationRequest{
			ID:          created.ID,
			ChannelType: ChannelTypeMQTT,
			Config:      AlertDestinationConfigInfo{Broker: stringPtr(brokerB.addr())},
			Secret:      &AlertDestinationSecretInfo{Password: stringPtr("explicit-new-pass")},
		})),
	)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("broker override with new pass status=%d (%s)", w.Code, w.Body.String())
	}
	connB, _ = brokerB.snapshot()
	if len(connB) != 2 || connB[1].password != "explicit-new-pass" {
		t.Fatalf("expected explicit new password on brokerB, got %+v", connB)
	}
}

// TestHandlerSSRFAndMQTTSchemePolicyProductionDefault pins U4: production default
// (AllowLoopback=false) blocks loopback, link-local metadata, and invalid MQTT schemes,
// while permitting RFC1918 private LAN addresses.
func TestHandlerSSRFAndMQTTSchemePolicyProductionDefault(t *testing.T) {
	router, _, cleanup := setupTestRouterWithConfig(t, Config{
		PublicBaseURL: "http://10.0.0.12:8000",
		SignedURLTTL:  time.Hour,
		AllowLoopback: false,
	})
	defer cleanup()

	blockedWebhooks := []string{
		"http://127.0.0.1:8000/api/v1/health",
		"http://localhost:8000/hook",
		"http://[::1]:8080/hook",
		"http://169.254.169.254/latest/meta-data/",
		"http://0.0.0.0:8080/hook",
	}
	for i, raw := range blockedWebhooks {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/alert-destinations",
			bytes.NewReader(mustMarshalJSON(t, CreateAlertDestinationRequest{
				Name:        "ssrf-wh-" + string(rune('a'+i)),
				ChannelType: ChannelTypeWebhook,
				ImageMode:   ImageModeMultipart,
				Config:      AlertDestinationConfigInfo{URL: stringPtr(raw)},
			})),
		)
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("url %q: status=%d, want 422 (%s)", raw, w.Code, w.Body.String())
		}
		assertValidationDetail(t, w, "config.url", CodeAlertURLBlocked)
	}

	// Unsupported MQTT schemes blocked with ALERT_MQTT_SCHEME_UNSUPPORTED.
	for i, broker := range []string{"http://broker.local:1883", "file://broker.local/tmp", "gopher://broker.local:70"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/alert-destinations",
			bytes.NewReader(mustMarshalJSON(t, CreateAlertDestinationRequest{
				Name:        "bad-scheme-" + string(rune('a'+i)),
				ChannelType: ChannelTypeMQTT,
				ImageMode:   ImageModeBase64,
				Config: AlertDestinationConfigInfo{
					Broker: stringPtr(broker),
					Topic:  stringPtr("zhulong/events"),
				},
			})),
		)
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("broker %q: status=%d, want 422 (%s)", broker, w.Code, w.Body.String())
		}
		assertValidationDetail(t, w, "config.broker", CodeAlertMQTTSchemeUnsupported)
	}

	// Loopback / link-local MQTT brokers blocked with ALERT_URL_BLOCKED.
	for i, broker := range []string{"tcp://127.0.0.1:1883", "tcp://169.254.169.254:1883"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/alert-destinations",
			bytes.NewReader(mustMarshalJSON(t, CreateAlertDestinationRequest{
				Name:        "ssrf-mqtt-" + string(rune('a'+i)),
				ChannelType: ChannelTypeMQTT,
				ImageMode:   ImageModeBase64,
				Config: AlertDestinationConfigInfo{
					Broker: stringPtr(broker),
					Topic:  stringPtr("zhulong/events"),
				},
			})),
		)
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("broker %q: status=%d, want 422 (%s)", broker, w.Code, w.Body.String())
		}
		assertValidationDetail(t, w, "config.broker", CodeAlertURLBlocked)
	}

	// RFC1918 private LAN addresses MUST remain allowed for edge appliances.
	code, envCode, _ := postDestination(t, router, CreateAlertDestinationRequest{
		Name:        "LAN Webhook",
		ChannelType: ChannelTypeWebhook,
		ImageMode:   ImageModeMultipart,
		Config:      AlertDestinationConfigInfo{URL: stringPtr("http://192.168.1.50:8080/hook")},
	})
	if code != http.StatusOK {
		t.Fatalf("LAN webhook rejected: %d (%s)", code, envCode)
	}
	code, envCode, _ = postDestination(t, router, CreateAlertDestinationRequest{
		Name:        "LAN MQTT",
		ChannelType: ChannelTypeMQTT,
		ImageMode:   ImageModeBase64,
		Config: AlertDestinationConfigInfo{
			Broker: stringPtr("tcp://10.0.0.20:1883"),
			Topic:  stringPtr("zhulong/events"),
		},
	})
	if code != http.StatusOK {
		t.Fatalf("LAN mqtt rejected: %d (%s)", code, envCode)
	}
}

// TestHandlerTestEndpointRateLimit pins U4: /alert-destinations/test enforces dedicated IP rate limiting.
func TestHandlerTestEndpointRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, cleanup := setupTestStore(t)
	defer cleanup()

	svc := NewService(ServiceOptions{
		Store: store,
		Config: Config{
			PublicBaseURL: "http://10.0.0.12:8000",
			AllowLoopback: true,
		},
		Logger: zap.NewNop(),
	})
	tightLimiter := httpmiddleware.NewIPRateLimiter(httpmiddleware.RateLimitConfig{
		PerMinute:                60,
		Burst:                    2,
		ProcessRequestsPerMinute: 120,
		ProcessMaxInFlight:       8,
		PerIPCode:                CodeAlertTestRateLimited,
		BudgetExhaustedCode:      CodeAlertTestRateLimited,
		ConcurrencyLimitCode:     CodeAlertTestRateLimited,
		LogScope:                 "alert push test",
	})
	h := NewHandlerWithTestLimiter(svc, store, tightLimiter, zap.NewNop())
	router := gin.New()
	h.RegisterRoutes(router.Group("/api/v1"))

	for i := range 3 {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/alert-destinations/test",
			bytes.NewReader(mustMarshalJSON(t, TestAlertDestinationRequest{ID: "01MISSING00000000000000000"})),
		)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept-Language", "zh-Hans")
		req.RemoteAddr = "198.51.100.42:55555"
		router.ServeHTTP(w, req)

		if i < 2 && w.Code != http.StatusNotFound {
			t.Fatalf("req %d status=%d, want 404 before burst exhausted", i+1, w.Code)
		}
		if i == 2 {
			if w.Code != http.StatusTooManyRequests {
				t.Fatalf("req 3 status=%d, want 429 (%s)", w.Code, w.Body.String())
			}
			var env httputil.Response
			if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
				t.Fatalf("unmarshal 429 envelope: %v", err)
			}
			if env.Code != CodeAlertTestRateLimited || !strings.Contains(env.Message, "测试") {
				t.Fatalf("unexpected 429 envelope: %+v", env)
			}
		}
	}
}

// TestHandlerWebhookSigningSecretRoundTripAndNoPlaintextLeak pins U12 + U21:
// SigningSecret persists encrypted, signs outbound probes, supports keep/clear on update,
// guards cross-origin heartbeatUrl, and never appears in any response JSON.
func TestHandlerWebhookSigningSecretRoundTripAndNoPlaintextLeak(t *testing.T) {
	var (
		mu      sync.Mutex
		lastTS  string
		lastSig string
		lastRaw []byte
	)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		lastTS = r.Header.Get(headerZhulongTimestamp)
		lastSig = r.Header.Get(headerZhulongSignature)
		lastRaw = body
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	router, _, cleanup := setupTestRouter(t)
	defer cleanup()

	const (
		plainUser    = "basic-secret-user"
		plainPass    = "basic-secret-pass"
		plainSignKey = "super-hmac-key-2026"
		plainHdrVal  = "header-secret-value"
	)

	// Create Webhook with Basic Auth + SigningSecret + Custom Header.
	wCreate := httptest.NewRecorder()
	reqCreate := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/alert-destinations",
		bytes.NewReader(mustMarshalJSON(t, CreateAlertDestinationRequest{
			Name:        "Signed Basic Hook",
			ChannelType: ChannelTypeWebhook,
			ImageMode:   ImageModeMultipart,
			Config: AlertDestinationConfigInfo{
				URL:      stringPtr(target.URL + "/events"),
				AuthType: stringPtr(AuthTypeBasic),
			},
			Secret: &AlertDestinationSecretInfo{
				Username:      stringPtr(plainUser),
				Password:      stringPtr(plainPass),
				SigningSecret: stringPtr(plainSignKey),
				Headers:       map[string]string{"X-Api-Key": plainHdrVal},
			},
		})),
	)
	reqCreate.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(wCreate, reqCreate)
	if wCreate.Code != http.StatusOK {
		t.Fatalf("create status=%d: %s", wCreate.Code, wCreate.Body.String())
	}
	assertBodyContainsNoSecrets(t, wCreate.Body.String(), plainUser, plainPass, plainSignKey, plainHdrVal)

	_, _, created := decodeDestinationResponse(t, wCreate)
	if !created.SecretSet || !created.SigningSecretSet {
		t.Fatalf("expected secretSet=true & signingSecretSet=true, got %+v", created)
	}

	// List must also never leak plaintext credentials.
	wList := httptest.NewRecorder()
	router.ServeHTTP(wList, httptest.NewRequest(http.MethodGet, "/api/v1/alert-destinations", nil))
	if wList.Code != http.StatusOK {
		t.Fatalf("list status=%d", wList.Code)
	}
	assertBodyContainsNoSecrets(t, wList.Body.String(), plainUser, plainPass, plainSignKey, plainHdrVal)

	// Saved /test must emit valid X-Zhulong-Timestamp + X-Zhulong-Signature.
	wTest := httptest.NewRecorder()
	reqTest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/alert-destinations/test",
		bytes.NewReader(mustMarshalJSON(t, TestAlertDestinationRequest{ID: created.ID})),
	)
	reqTest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(wTest, reqTest)
	if wTest.Code != http.StatusOK {
		t.Fatalf("test status=%d: %s", wTest.Code, wTest.Body.String())
	}
	mu.Lock()
	wantSig := computeWebhookSignature(plainSignKey, lastTS, lastRaw)
	if lastTS == "" || lastSig != wantSig {
		t.Fatalf("signature mismatch: ts=%q got=%q want=%q", lastTS, lastSig, wantSig)
	}
	mu.Unlock()

	// Partial update omitting signingSecret preserves it; explicit "" clears it.
	code, _, kept := putDestination(t, router, created.ID, UpdateAlertDestinationRequest{
		Name: stringPtr("Signed Basic Hook v2"),
	})
	if code != http.StatusOK || !kept.SigningSecretSet || !kept.SecretSet {
		t.Fatalf("partial update lost signingSecret/basic creds: code=%d resp=%+v", code, kept)
	}

	code, _, cleared := putDestination(t, router, created.ID, UpdateAlertDestinationRequest{
		Secret: &AlertDestinationSecretInfo{SigningSecret: stringPtr("")},
	})
	if code != http.StatusOK || cleared.SigningSecretSet {
		t.Fatalf("clearing signingSecret failed: code=%d signingSecretSet=%v", code, cleared.SigningSecretSet)
	}

	// Cross-origin heartbeatUrl with ONLY signingSecret configured (authType=none) must be rejected.
	wCross := httptest.NewRecorder()
	reqCross := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/alert-destinations",
		bytes.NewReader(mustMarshalJSON(t, CreateAlertDestinationRequest{
			Name:             "Cross Origin SigningSecret",
			ChannelType:      ChannelTypeWebhook,
			EventsEnabled:    boolPtr(false),
			HeartbeatEnabled: boolPtr(true),
			Config: AlertDestinationConfigInfo{
				URL:          stringPtr("https://api.example.com/events"),
				HeartbeatURL: stringPtr("https://other.example.org/hb"),
				AuthType:     stringPtr(AuthTypeNone),
			},
			Secret: &AlertDestinationSecretInfo{
				SigningSecret: stringPtr("hmac-only"),
			},
		})),
	)
	reqCross.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(wCross, reqCross)
	if wCross.Code != http.StatusUnprocessableEntity {
		t.Fatalf("cross-origin heartbeat with signingSecret status=%d, want 422", wCross.Code)
	}
	assertValidationDetail(t, wCross, "config.heartbeatUrl", CodeAlertHeartbeatCrossOrigin)
}

func assertBodyContainsNoSecrets(t *testing.T, body string, secrets ...string) {
	t.Helper()
	for _, s := range secrets {
		if strings.Contains(body, s) {
			t.Fatalf("response JSON leaked plaintext secret %q in %s", s, body)
		}
	}
}

// TestHandlerValidationBypassesAndPartialUpdateCompliance pins U5, U15, U23, U26.
func TestHandlerValidationBypassesAndPartialUpdateCompliance(t *testing.T) {
	router, comp, cleanup := setupTestRouter(t)
	defer cleanup()

	// 1. Create with authType=none + stray secret.token normalizes away token (secretSet=false).
	code, envCode, wh := postDestination(t, router, CreateAlertDestinationRequest{
		Name:             "None Auth Hook",
		ChannelType:      ChannelTypeWebhook,
		EventsEnabled:    boolPtr(true),
		HeartbeatEnabled: boolPtr(true),
		ImageMode:        ImageModeMultipart,
		Config: AlertDestinationConfigInfo{
			URL:                      stringPtr("  https://origin-a.example.com/events  "),
			HeartbeatURL:             stringPtr("https://origin-a.example.com/hb"),
			AuthType:                 stringPtr(AuthTypeNone),
			HeartbeatIntervalSeconds: intPtr(30),
		},
		Secret: &AlertDestinationSecretInfo{
			Token: stringPtr("stray-token-should-be-cleared"),
		},
		Filter: &AlertDestinationFilterInfo{
			EventTypes:   []string{"alarm", "alarm", "face"},
			AlgorithmIDs: []string{"algo1", "algo1"},
			CameraIDs:    []string{"cam1", "cam1"},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("create failed: %d (%s)", code, envCode)
	}
	if wh.SecretSet {
		t.Fatal("stray token on authType=none was not stripped on create")
	}
	if wh.Config.URL == nil || *wh.Config.URL != "https://origin-a.example.com/events" {
		t.Fatalf("URL not trimmed: got %v", wh.Config.URL)
	}
	if len(wh.Filter.EventTypes) != 2 || len(wh.Filter.AlgorithmIDs) != 1 || len(wh.Filter.CameraIDs) != 1 {
		t.Fatalf("filter slices were not deduplicated: %+v", wh.Filter)
	}

	// 2. Pure rename MUST NOT re-encrypt SecretCiphertext (U23).
	before, err := comp.Store.GetByID(t.Context(), wh.ID)
	if err != nil {
		t.Fatalf("GetByID before rename: %v", err)
	}
	code, envCode, _ = putDestination(t, router, wh.ID, UpdateAlertDestinationRequest{
		Name: stringPtr("None Auth Hook Renamed"),
	})
	if code != http.StatusOK {
		t.Fatalf("rename failed: %d (%s)", code, envCode)
	}
	after, err := comp.Store.GetByID(t.Context(), wh.ID)
	if err != nil {
		t.Fatalf("GetByID after rename: %v", err)
	}
	if !bytes.Equal(before.SecretCiphertext, after.SecretCiphertext) {
		t.Fatal("pure rename re-encrypted SecretCiphertext unnecessarily")
	}

	// 3. U5: Turning heartbeatEnabled=false while moving URL to a new origin with Bearer creds
	// and omitting heartbeatUrl must succeed (no spurious cross-origin 422 from hidden heartbeatUrl).
	code, envCode, moved := putDestination(t, router, wh.ID, UpdateAlertDestinationRequest{
		HeartbeatEnabled: boolPtr(false),
		Config: &AlertDestinationConfigInfo{
			URL:      stringPtr("https://origin-b.example.com/events"),
			AuthType: stringPtr(AuthTypeBearer),
		},
		Secret: &AlertDestinationSecretInfo{Token: stringPtr("new-bearer")},
	})
	if code != http.StatusOK || moved.HeartbeatEnabled {
		t.Fatalf("U5 disable heartbeat + change URL failed: code=%d (%s) resp=%+v", code, envCode, moved)
	}

	// 4. Immutable channelType change on PUT must be explicitly rejected with 422.
	wImm := httptest.NewRecorder()
	reqImm := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/alert-destinations/"+wh.ID,
		bytes.NewReader(mustMarshalJSON(t, UpdateAlertDestinationRequest{
			ChannelType: stringPtr(ChannelTypeMQTT),
		})),
	)
	reqImm.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(wImm, reqImm)
	if wImm.Code != http.StatusUnprocessableEntity {
		t.Fatalf("channelType change status=%d, want 422", wImm.Code)
	}
	assertValidationDetail(t, wImm, "channelType", CodeAlertChannelTypeImmutable)

	// 5. Explicit empty strings on required fields in PUT must return 422 (never silently ignored 200).
	emptyFieldCases := []struct {
		name      string
		req       UpdateAlertDestinationRequest
		wantField string
	}{
		{
			name:      "empty imageMode",
			req:       UpdateAlertDestinationRequest{ImageMode: stringPtr("")},
			wantField: "imageMode",
		},
		{
			name:      "empty webhook url",
			req:       UpdateAlertDestinationRequest{Config: &AlertDestinationConfigInfo{URL: stringPtr("")}},
			wantField: "config.url",
		},
		{
			name:      "empty webhook authType",
			req:       UpdateAlertDestinationRequest{Config: &AlertDestinationConfigInfo{AuthType: stringPtr("")}},
			wantField: "config.authType",
		},
	}
	for _, tc := range emptyFieldCases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(
				http.MethodPut,
				"/api/v1/alert-destinations/"+wh.ID,
				bytes.NewReader(mustMarshalJSON(t, tc.req)),
			)
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("%s: status=%d, want 422 (%s)", tc.name, w.Code, w.Body.String())
			}
			assertValidationDetail(t, w, tc.wantField, "REQUIRED")
		})
	}

	// 6. Custom header validation: blank key, RFC 7230 invalid char, CRLF value, duplicate case-insensitive, >32 entries.
	tooManyHeaders := make(map[string]string, MaxCustomHeaders+1)
	for i := range MaxCustomHeaders + 1 {
		tooManyHeaders["X-Custom-"+string(rune('A'+i/26))+string(rune('a'+i%26))] = "v"
	}
	headerCases := []struct {
		name     string
		headers  map[string]string
		wantCode string
	}{
		{name: "blank key", headers: map[string]string{"   ": "val"}, wantCode: CodeAlertHeaderInvalid},
		{name: "space in key", headers: map[string]string{"Bad Key": "val"}, wantCode: CodeAlertHeaderInvalid},
		{name: "CRLF in value", headers: map[string]string{"X-Test": "ok\r\nX-Evil: 1"}, wantCode: CodeAlertHeaderInvalid},
		{name: "duplicate case-insensitive", headers: map[string]string{"X-Dup": "1", "x-dup": "2"}, wantCode: CodeAlertHeaderDuplicate},
		{name: "too many headers", headers: tooManyHeaders, wantCode: CodeAlertHeaderLimitExceeded},
	}
	for _, tc := range headerCases {
		t.Run("headers/"+tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(
				http.MethodPut,
				"/api/v1/alert-destinations/"+wh.ID,
				bytes.NewReader(mustMarshalJSON(t, UpdateAlertDestinationRequest{
					Secret: &AlertDestinationSecretInfo{Headers: tc.headers},
				})),
			)
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("headers %s: status=%d, want 422", tc.name, w.Code)
			}
			assertValidationDetail(t, w, "secret.headers", tc.wantCode)
		})
	}

	// 7. /test with heartbeatIntervalSeconds=0 when heartbeatEnabled=true must return 422.
	wTestZero := httptest.NewRecorder()
	reqTestZero := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/alert-destinations/test",
		bytes.NewReader(mustMarshalJSON(t, TestAlertDestinationRequest{
			ChannelType:      ChannelTypeWebhook,
			EventsEnabled:    boolPtr(false),
			HeartbeatEnabled: boolPtr(true),
			Config: AlertDestinationConfigInfo{
				URL:                      stringPtr("https://example.com/hb"),
				HeartbeatIntervalSeconds: intPtr(0),
			},
		})),
	)
	reqTestZero.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(wTestZero, reqTestZero)
	if wTestZero.Code != http.StatusUnprocessableEntity {
		t.Fatalf("/test heartbeatIntervalSeconds=0 status=%d, want 422", wTestZero.Code)
	}
	assertValidationDetail(t, wTestZero, "config.heartbeatIntervalSeconds", "MIN_VALUE")

	// 8. MQTT explicit empty password clears secretSet; distinct 409 codes for name vs clientId conflicts.
	code, _, mqtt1 := postDestination(t, router, CreateAlertDestinationRequest{
		Name:        "MQTT Creds",
		ChannelType: ChannelTypeMQTT,
		ImageMode:   ImageModeBase64,
		Config: AlertDestinationConfigInfo{
			Broker:   stringPtr("tcp://broker.local:1883"),
			Topic:    stringPtr("zhulong/events"),
			ClientID: stringPtr("unique-cid-01"),
		},
		Secret: &AlertDestinationSecretInfo{Password: stringPtr("mqtt-pass-1")},
	})
	if code != http.StatusOK || !mqtt1.SecretSet {
		t.Fatalf("create mqtt1 failed: code=%d secretSet=%v", code, mqtt1.SecretSet)
	}
	code, _, mqttCleared := putDestination(t, router, mqtt1.ID, UpdateAlertDestinationRequest{
		Secret: &AlertDestinationSecretInfo{Password: stringPtr("")},
	})
	if code != http.StatusOK || mqttCleared.SecretSet {
		t.Fatalf("expected mqtt password cleared: code=%d secretSet=%v", code, mqttCleared.SecretSet)
	}
	// Duplicate name -> 409 ALERT_DESTINATION_NAME_CONFLICT
	code, envCode, _ = postDestination(t, router, mqttCreateRequest("MQTT Creds", nil))
	if code != http.StatusConflict || envCode != CodeAlertDestinationNameConflict {
		t.Fatalf("name conflict: code=%d env=%q", code, envCode)
	}
	// Duplicate clientId -> 409 ALERT_DESTINATION_CLIENT_ID_CONFLICT
	dupCIDReq := mqttCreateRequest("MQTT Different Name", nil)
	dupCIDReq.Config.ClientID = stringPtr("unique-cid-01")
	code, envCode, _ = postDestination(t, router, dupCIDReq)
	if code != http.StatusConflict || envCode != CodeAlertDestinationClientIDConflict {
		t.Fatalf("clientId conflict: code=%d env=%q", code, envCode)
	}
}

// TestServiceDirectDefenseInDepthAndShortIDGuard pins U15 + U22 + U7.
func TestServiceDirectDefenseInDepthAndShortIDGuard(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := t.Context()

	dispatcher := NewDispatcher(DispatcherOptions{
		Store:  store,
		Config: Config{PublicBaseURL: "http://10.0.0.12:8000", AllowLoopback: true},
		Logger: zap.NewNop(),
	})
	svc := NewService(ServiceOptions{
		Store:      store,
		Dispatcher: dispatcher,
		Config:     Config{PublicBaseURL: "http://10.0.0.12:8000", AllowLoopback: true},
		Logger:     zap.NewNop(),
	})

	// 1. Direct Service.Create rejects reserved header even without going through Handler.
	err := svc.Create(ctx, &AlertDestination{
		Name:          "Direct Reserved",
		ChannelType:   ChannelTypeWebhook,
		Enabled:       true,
		EventsEnabled: true,
		ImageMode:     ImageModeMultipart,
		ConfigJSON:    `{"auth_type":"none","timeout_seconds":5}`,
	}, &webhookSecret{
		URL:     "https://example.com/hook",
		Headers: map[string]string{"X-Zhulong-Signature": "spoof"},
	})
	if !errors.Is(err, ErrReservedHeaderUsed) {
		t.Fatalf("expected ErrReservedHeaderUsed from Service.Create, got %v", err)
	}

	// 2. Direct Service.Create rejects empty MQTT ConfigJSON (never silently succeeds).
	err = svc.Create(ctx, &AlertDestination{
		Name:          "Direct Empty MQTT",
		ChannelType:   ChannelTypeMQTT,
		Enabled:       true,
		EventsEnabled: true,
		ImageMode:     ImageModeBase64,
		ConfigJSON:    "",
	}, &mqttSecret{})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig for empty MQTT ConfigJSON, got %v", err)
	}

	// 3. Direct Service.Update with short ID (< 4 chars) does NOT panic when clearing ClientID (U22).
	secBytes, err := store.EncryptSecret("m1", &mqttSecret{})
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	shortDest := &AlertDestination{
		ID:               "m1",
		Name:             "Short ID MQTT",
		ChannelType:      ChannelTypeMQTT,
		Enabled:          true,
		EventsEnabled:    true,
		ImageMode:        ImageModeBase64,
		ConfigJSON:       `{"broker":"tcp://broker.local:1883","topic":"a/b","keep_alive_seconds":30}`,
		SecretCiphertext: secBytes,
		FilterJSON:       `{}`,
	}
	if err := store.Create(ctx, shortDest); err != nil {
		t.Fatalf("store.Create short ID: %v", err)
	}
	emptyCID := ""
	if err := svc.Update(ctx, shortDest, UpdateParams{ClientID: &emptyCID}); err != nil {
		t.Fatalf("Service.Update short ID failed: %v", err)
	}
	if shortDest.MQTTClientID == nil || *shortDest.MQTTClientID != "m1" {
		t.Fatalf("short ID fallback clientId = %v, want m1", shortDest.MQTTClientID)
	}

	// 4. Service.Delete + Dispatcher reconcile prunes stats permanently (U7).
	if err := dispatcher.Start(ctx); err != nil {
		t.Fatalf("dispatcher.Start: %v", err)
	}
	defer func() { _ = dispatcher.Stop(ctx) }()

	if err := svc.Delete(ctx, "m1"); err != nil {
		t.Fatalf("svc.Delete: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		dispatcher.mu.RLock()
		_, hasWorker := dispatcher.workers["m1"]
		dispatcher.mu.RUnlock()
		if !hasWorker {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	dispatcher.statsMu.RLock()
	_, leaked := dispatcher.stats["m1"]
	dispatcher.statsMu.RUnlock()
	if leaked {
		t.Fatal("deleted destination entry leaked in dispatcher.stats")
	}
}

// TestHandlerAuditLoggingCoverage pins U16: bind failures are audited, Create records ULID,
// saved /test records destination:<id>, and failed connectivity tests record status=failed.
func TestHandlerAuditLoggingCoverage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dbStore := database.New(t.TempDir(), zap.NewNop())
	if err := dbStore.OpenAndMigrate(t.Context()); err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	defer func() { _ = dbStore.Close() }()

	auditStore := audit.NewStore(dbStore)
	auditSvc := audit.NewService(auditStore, zap.NewNop(), audit.DefaultConfig())
	if err := auditSvc.Start(t.Context()); err != nil {
		t.Fatalf("auditSvc.Start: %v", err)
	}

	failTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failTarget.Close()

	comp := NewComponents(Options{
		DBProvider: dbStore,
		Cipher:     fakeCipher{},
		DataDir:    t.TempDir(),
		Config: Config{
			PublicBaseURL: "http://10.0.0.12:8000",
			SignedURLTTL:  time.Hour,
			AllowLoopback: true,
		},
		Logger: zap.NewNop(),
	})

	router := gin.New()
	api := router.Group("/api/v1")
	api.Use(audithttp.Middleware(auditSvc))
	comp.ProtectedRoutes.RegisterRoutes(api)

	// 1. Malformed JSON on POST /alert-destinations must still produce a failed audit row.
	wBad := httptest.NewRecorder()
	reqBad := httptest.NewRequest(http.MethodPost, "/api/v1/alert-destinations", strings.NewReader("{bad json"))
	reqBad.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(wBad, reqBad)
	if wBad.Code != http.StatusBadRequest {
		t.Fatalf("malformed json status=%d, want 400", wBad.Code)
	}

	// 2. Valid Create must record target=destination:<ULID> and status=success.
	code, _, created := postDestination(t, router, CreateAlertDestinationRequest{
		Name:        "Audit Hook",
		ChannelType: ChannelTypeWebhook,
		ImageMode:   ImageModeMultipart,
		Config:      AlertDestinationConfigInfo{URL: stringPtr(failTarget.URL + "/events")},
	})
	if code != http.StatusOK {
		t.Fatalf("create status=%d", code)
	}

	// 3. Saved /test against failing target returns HTTP 200 ok=false but MUST audit status=failed + destination:<id>.
	wTest := httptest.NewRecorder()
	reqTest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/alert-destinations/test",
		bytes.NewReader(mustMarshalJSON(t, TestAlertDestinationRequest{ID: created.ID})),
	)
	reqTest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(wTest, reqTest)
	if wTest.Code != http.StatusOK {
		t.Fatalf("test status=%d", wTest.Code)
	}

	// Flush audit queue before inspecting database rows.
	if err := auditSvc.Stop(t.Context()); err != nil {
		t.Fatalf("auditSvc.Stop: %v", err)
	}
	logs, _, err := auditStore.List(t.Context(), audit.Filter{Limit: 20})
	if err != nil {
		t.Fatalf("auditStore.List: %v", err)
	}
	if len(logs) < 3 {
		t.Fatalf("expected at least 3 audit logs, got %d: %+v", len(logs), logs)
	}

	var sawBadCreate, sawGoodCreate, sawFailedTest bool
	for _, row := range logs {
		if row.Action == ActionAlertDestinationCreate && row.Status == audit.StatusFailed {
			sawBadCreate = true
		}
		if row.Action == ActionAlertDestinationCreate && row.Status == audit.StatusSuccess &&
			row.Target == "destination:"+created.ID {
			sawGoodCreate = true
		}
		if row.Action == ActionAlertDestinationTest && row.Status == audit.StatusFailed &&
			row.Target == "destination:"+created.ID && row.ErrorMsg != "" {
			sawFailedTest = true
		}
	}
	if !sawBadCreate || !sawGoodCreate || !sawFailedTest {
		t.Fatalf(
			"audit assertions failed: badCreate=%v goodCreate=%v failedTest=%v (rows=%+v)",
			sawBadCreate,
			sawGoodCreate,
			sawFailedTest,
			logs,
		)
	}
}

// TestHandlerToResponseLogsDecryptionFailure pins U15: toResponse must log an Error with
// destination_id (never silently swallow) when ciphertext decryption fails.
func TestHandlerToResponseLogsDecryptionFailure(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	core, recorded := observer.New(zapcore.ErrorLevel)
	logger := zap.New(core)
	h := NewHandler(nil, store, logger)

	dest := &AlertDestination{
		ID:               "01CORRUPTCIPHER00000000001",
		Name:             "Corrupt Cipher",
		ChannelType:      ChannelTypeWebhook,
		Enabled:          true,
		EventsEnabled:    true,
		ImageMode:        ImageModeMultipart,
		ConfigJSON:       `{"auth_type":"none","timeout_seconds":5}`,
		SecretCiphertext: []byte("corrupted-ciphertext"),
		FilterJSON:       `{}`,
	}
	resp, err := h.toResponse(dest, AlertDestinationStatus{State: StateUnverified})
	if err != nil {
		t.Fatalf("toResponse returned error: %v", err)
	}
	if resp.Config.URL != nil || resp.SecretSet {
		t.Fatalf("unexpected decrypted data on corrupt ciphertext: %+v", resp)
	}
	entries := recorded.FilterMessage("failed to decrypt webhook destination secret").All()
	if len(entries) != 1 || entries[0].ContextMap()["destination_id"] != dest.ID {
		t.Fatalf("expected 1 error log with destination_id=%s, got %+v", dest.ID, entries)
	}
}
