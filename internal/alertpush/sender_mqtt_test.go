package alertpush

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/nikonikowuw/Zhulong/internal/record"
)

func startInProcessBroker(t *testing.T) (string, func()) {
	t.Helper()

	server := mochi.New(nil)
	if err := server.AddHook(new(auth.AllowHook), nil); err != nil {
		t.Fatalf("failed to add auth hook: %v", err)
	}

	// Pick random free port
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to pick free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatalf("failed to close temp listener: %v", err)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	tcp := listeners.NewTCP(listeners.Config{
		ID:      "test-listener",
		Address: addr,
	})
	if err := server.AddListener(tcp); err != nil {
		t.Fatalf("failed to add listener: %v", err)
	}

	go func() {
		if err := server.Serve(); err != nil {
			return
		}
	}()

	time.Sleep(50 * time.Millisecond)

	return "tcp://" + addr, func() {
		if err := server.Close(); err != nil {
			t.Errorf("broker close failed: %v", err)
		}
	}
}

func TestMQTTSenderBase64AndURL(t *testing.T) {
	brokerURL, cleanup := startInProcessBroker(t)
	defer cleanup()

	subOpts := mqtt.NewClientOptions().AddBroker(brokerURL).SetClientID("test-subscriber")
	subClient := mqtt.NewClient(subOpts)
	if token := subClient.Connect(); token.Wait() && token.Error() != nil {
		t.Fatalf("subscriber connect failed: %v", token.Error())
	}
	defer subClient.Disconnect(100)

	var mu sync.Mutex
	var received []byte
	subClient.Subscribe("zhulong/alerts/cam01", 1, func(_ mqtt.Client, msg mqtt.Message) {
		mu.Lock()
		defer mu.Unlock()
		received = msg.Payload()
	})

	fakeMedia := &fakeMediaService{
		files: map[string][]byte{
			"snapshots/cam_01/2026-11-03/01J8Z.jpg": []byte("mqtt-jpeg-image-bytes-777"),
		},
	}

	sender, err := NewMQTTSender(MQTTSenderOptions{
		ID:        "mqtt_01",
		ImageMode: ImageModeBase64,
		Broker:    brokerURL,
		ClientID:  "zhulong-test-client-1",
		Topic:     "zhulong/alerts/cam01",
		QoS:       1,
		Media:     fakeMedia,
	})
	if err != nil {
		t.Fatalf("NewMQTTSender: %v", err)
	}
	defer sender.Close()

	ev := record.AlarmEvent{
		ID:         "01J8ZMQTT01",
		CameraID:   "cam_01",
		AlarmType:  "intrusion",
		Score:      0.92,
		ImagePath:  "snapshots/cam_01/2026-11-03/01J8Z.jpg",
		ImageReady: true,
		CapturedAt: time.Now().UTC(),
	}

	if err := sender.Send(context.Background(), Delivery{Event: ev}); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := len(received)
		mu.Unlock()
		if got > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	payloadBytes := received
	mu.Unlock()

	if len(payloadBytes) == 0 {
		t.Fatal("expected message received on subscriber, got none")
	}

	var payload EventPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatalf("unmarshal payload failed: %v", err)
	}
	if len(payload.Images) != 1 || payload.Images[0].Role != ImageRoleScene || payload.Images[0].Base64 == nil {
		t.Fatalf("expected inlined scene image, got %+v", payload.Images)
	}
	if payload.SchemaVersion != SchemaVersion || payload.Alarm == nil {
		t.Fatalf("unexpected envelope: %+v", payload)
	}
	decoded, err := base64.StdEncoding.DecodeString(*payload.Images[0].Base64)
	if err != nil {
		t.Fatalf("base64 decode failed: %v", err)
	}
	if string(decoded) != "mqtt-jpeg-image-bytes-777" {
		t.Errorf("decoded bytes mismatch: got %q", string(decoded))
	}
}

func TestMQTTSenderBase64OversizedFallback(t *testing.T) {
	brokerURL, cleanup := startInProcessBroker(t)
	defer cleanup()

	subOpts := mqtt.NewClientOptions().AddBroker(brokerURL).SetClientID("test-subscriber-oversize")
	subClient := mqtt.NewClient(subOpts)
	if token := subClient.Connect(); token.Wait() && token.Error() != nil {
		t.Fatalf("subscriber connect failed: %v", token.Error())
	}
	defer subClient.Disconnect(100)

	var mu sync.Mutex
	var received []byte
	subClient.Subscribe("zhulong/alerts/cam02", 0, func(_ mqtt.Client, msg mqtt.Message) {
		mu.Lock()
		defer mu.Unlock()
		received = msg.Payload()
	})

	// 501KB > 500KB 上限
	oversizedBytes := make([]byte, 501*1024)
	fakeMedia := &fakeMediaService{
		files: map[string][]byte{
			"snapshots/cam_02/2026-11-03/big.jpg": oversizedBytes,
		},
	}

	sender, err := NewMQTTSender(MQTTSenderOptions{
		ID:        "mqtt_02",
		ImageMode: ImageModeBase64,
		Broker:    brokerURL,
		ClientID:  "zhulong-test-client-2",
		Topic:     "zhulong/alerts/cam02",
		QoS:       0,
		Media:     fakeMedia,
	})
	if err != nil {
		t.Fatalf("NewMQTTSender: %v", err)
	}
	defer sender.Close()

	ev := record.AlarmEvent{
		ID:         "01J8ZMQTT02",
		CameraID:   "cam_02",
		ImagePath:  "snapshots/cam_02/2026-11-03/big.jpg",
		ImageReady: true,
		CapturedAt: time.Now().UTC(),
	}

	if err := sender.Send(context.Background(), Delivery{Event: ev}); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := len(received)
		mu.Unlock()
		if got > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	payloadBytes := received
	mu.Unlock()

	var payload EventPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatalf("unmarshal payload failed: %v", err)
	}
	if len(payload.Images) != 0 {
		t.Errorf("expected oversized image without url to be omitted, got %+v", payload.Images)
	}
}

func TestMQTTSenderWildcardValidation(t *testing.T) {
	_, err := NewMQTTSender(MQTTSenderOptions{
		Broker: "tcp://127.0.0.1:1883",
		Topic:  "alerts/+/wildcard",
	})
	if err != ErrMQTTWildcardTopic {
		t.Fatalf("expected ErrMQTTWildcardTopic, got %v", err)
	}
}

func TestMQTTSenderRespectsContextCancel(t *testing.T) {
	brokerURL, cleanup := startInProcessBroker(t)
	defer cleanup()

	sender, err := NewMQTTSender(MQTTSenderOptions{
		Broker:   brokerURL,
		ClientID: "test-sender-cancel",
		Topic:    "zhulong/alerts/cancel",
		QoS:      1,
	})
	if err != nil {
		t.Fatalf("NewMQTTSender failed: %v", err)
	}
	defer sender.Close()

	if err := sender.Connect(); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 预先取消 context

	ev := record.AlarmEvent{
		ID:         "01J8ZCANCEL01",
		CameraID:   "cam_cancel",
		CapturedAt: time.Now().UTC(),
	}

	start := time.Now()
	err = sender.Send(ctx, Delivery{Event: ev})
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("expected immediate return on canceled context, took %v", elapsed)
	}
}

func TestMQTTSenderHeartbeatAndGracefulOffline(t *testing.T) {
	brokerURL, cleanup := startInProcessBroker(t)
	defer cleanup()

	subOpts := mqtt.NewClientOptions().AddBroker(brokerURL).SetClientID("test-sub-hb")
	subClient := mqtt.NewClient(subOpts)
	if token := subClient.Connect(); token.Wait() && token.Error() != nil {
		t.Fatalf("subscriber connect failed: %v", token.Error())
	}
	defer subClient.Disconnect(100)

	msgCh := make(chan []byte, 4)
	if tok := subClient.Subscribe("zhulong/status/box01", 1, func(_ mqtt.Client, msg mqtt.Message) {
		cp := append([]byte(nil), msg.Payload()...)
		msgCh <- cp
	}); tok.Wait() && tok.Error() != nil {
		t.Fatalf("subscribe failed: %v", tok.Error())
	}

	identity := &fakeIdentity{value: "box-01"}
	sender, err := NewMQTTSender(MQTTSenderOptions{
		ID:                "mqtt_hb_01",
		ImageMode:         ImageModeURL,
		SkipImageURLCheck: true,
		Broker:            brokerURL,
		ClientID:          "zhulong-hb-01",
		Topic:             "zhulong/events/box01",
		HeartbeatEnabled:  true,
		HeartbeatTopic:    "zhulong/status/box01",
		LWTEnabled:        true,
		QoS:               1,
		NextSeq:           func() uint64 { return 99 },
		Identity:          identity,
	})
	if err != nil {
		t.Fatalf("NewMQTTSender: %v", err)
	}

	snap := HeartbeatSnapshot{
		ReportedAt: time.Now().UTC(),
		Status:     "ready",
		Reasons:    []string{},
	}
	if err := sender.SendHeartbeat(context.Background(), snap, 3); err != nil {
		t.Fatalf("SendHeartbeat failed: %v", err)
	}

	select {
	case raw := <-msgCh:
		var hb EventPayload
		if err := json.Unmarshal(raw, &hb); err != nil {
			t.Fatalf("unmarshal heartbeat: %v", err)
		}
		headerOK := hb.EventType == EventTypeHeartbeat && hb.Seq != nil && *hb.Seq == 3 &&
			hb.DeviceID != nil && *hb.DeviceID == "box-01" && hb.Heartbeat != nil
		if !headerOK {
			t.Fatalf("unexpected mqtt heartbeat: %+v", hb)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for mqtt heartbeat on heartbeat_topic")
	}

	// Verify hasStaleWillDeviceID detects live device_id change
	if sender.hasStaleWillDeviceID() {
		t.Fatal("expected hasStaleWillDeviceID=false before identity change")
	}
	identity.value = "box-01-renamed"
	if !sender.hasStaleWillDeviceID() {
		t.Fatal("expected hasStaleWillDeviceID=true after identity change")
	}

	// Clean Close() should publish graceful_shutdown offline notice before disconnecting
	if err := sender.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	select {
	case raw := <-msgCh:
		var offline EventPayload
		if err := json.Unmarshal(raw, &offline); err != nil {
			t.Fatalf("unmarshal graceful offline: %v", err)
		}
		if offline.EventType != EventTypeOffline || offline.Offline == nil ||
			offline.Offline.Reason != OfflineReasonGracefulShutdown {
			t.Fatalf("unexpected graceful offline payload: %+v", offline)
		}
		fieldsPopulated := offline.DeviceID != nil && *offline.DeviceID == "box-01-renamed" &&
			offline.SentAt != nil && offline.Seq != nil && *offline.Seq == 99 &&
			offline.ID != ""
		if !fieldsPopulated {
			t.Fatalf("expected populated id/device_id/seq/sent_at on graceful offline: %+v", offline)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for graceful offline MQTT message")
	}
}

func TestMQTTSenderHeartbeatTopicWildcardRejected(t *testing.T) {
	_, err := NewMQTTSender(MQTTSenderOptions{
		Broker:           "tcp://127.0.0.1:1883",
		Topic:            "zhulong/events",
		HeartbeatEnabled: true,
		HeartbeatTopic:   "zhulong/status/#",
	})
	if err != ErrMQTTWildcardTopic {
		t.Fatalf("expected ErrMQTTWildcardTopic for heartbeat_topic, got %v", err)
	}
}

// startSilentTCPListener accepts connections and never responds, simulating a
// blackholed MQTT broker that stalls the CONNACK handshake.
func startSilentTCPListener(t *testing.T) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen silent tcp: %v", err)
	}
	var mu sync.Mutex
	conns := []net.Conn{}
	go func() {
		for {
			c, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	return "tcp://" + ln.Addr().String(), func() {
		if closeErr := ln.Close(); closeErr != nil {
			t.Errorf("close silent listener: %v", closeErr)
		}
		mu.Lock()
		for _, c := range conns {
			if cErr := c.Close(); cErr != nil && !errors.Is(cErr, net.ErrClosed) {
				t.Errorf("close accepted conn: %v", cErr)
			}
		}
		mu.Unlock()
	}
}

func TestMQTTSenderConnectTimeoutReturnsError(t *testing.T) {
	brokerURL, cleanup := startSilentTCPListener(t)
	defer cleanup()

	sender, err := NewMQTTSender(MQTTSenderOptions{
		ID:             "mqtt_timeout",
		ImageMode:      ImageModeBase64,
		Broker:         brokerURL,
		ClientID:       "zhulong-timeout-1",
		Topic:          "zhulong/alerts/timeout",
		ConnectTimeout: 150 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewMQTTSender: %v", err)
	}
	defer func() {
		if closeErr := sender.Close(); closeErr != nil {
			t.Errorf("Close: %v", closeErr)
		}
	}()

	start := time.Now()
	connectErr := sender.Connect()
	elapsed := time.Since(start)
	if !errors.Is(connectErr, ErrMQTTNotConnected) {
		t.Fatalf("expected ErrMQTTNotConnected on connect timeout, got %v", connectErr)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Connect() took %v, expected prompt timeout near 150ms", elapsed)
	}
}

func TestMQTTSenderCloseUnblocksInFlightConnect(t *testing.T) {
	brokerURL, cleanup := startSilentTCPListener(t)
	defer cleanup()

	sender, err := NewMQTTSender(MQTTSenderOptions{
		ID:             "mqtt_close_race",
		ImageMode:      ImageModeBase64,
		Broker:         brokerURL,
		ClientID:       "zhulong-close-1",
		Topic:          "zhulong/alerts/close",
		ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMQTTSender: %v", err)
	}

	connectErrCh := make(chan error, 1)
	go func() {
		connectErrCh <- sender.Connect()
	}()

	time.Sleep(50 * time.Millisecond)

	closeStart := time.Now()
	if closeErr := sender.Close(); closeErr != nil {
		t.Fatalf("Close: %v", closeErr)
	}
	if elapsed := time.Since(closeStart); elapsed > 500*time.Millisecond {
		t.Fatalf("Close() blocked for %v while Connect() was in flight", elapsed)
	}

	select {
	case connectErr := <-connectErrCh:
		if connectErr == nil {
			t.Fatal("expected non-nil error from Connect() after Close()")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Connect() did not unblock promptly after Close()")
	}
}

func TestMQTTSenderEnsureConnectedRespectsContext(t *testing.T) {
	brokerURL, cleanup := startSilentTCPListener(t)
	defer cleanup()

	sender, err := NewMQTTSender(MQTTSenderOptions{
		ID:             "mqtt_ctx_connect",
		ImageMode:      ImageModeBase64,
		Broker:         brokerURL,
		ClientID:       "zhulong-ctx-conn-1",
		Topic:          "zhulong/alerts/ctx",
		ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMQTTSender: %v", err)
	}
	defer func() {
		if closeErr := sender.Close(); closeErr != nil {
			t.Errorf("Close: %v", closeErr)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	start := time.Now()
	sendErr := sender.Send(ctx, Delivery{
		Event: record.AlarmEvent{ID: "01CTX", CapturedAt: time.Now().UTC()},
	})
	elapsed := time.Since(start)
	if !errors.Is(sendErr, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", sendErr)
	}
	if elapsed > time.Second {
		t.Fatalf("Send ignored ctx deadline during connect: elapsed=%v", elapsed)
	}

	hbCtx, hbCancel := context.WithCancel(context.Background())
	hbCancel()
	hbErr := sender.SendHeartbeat(hbCtx, HeartbeatSnapshot{ReportedAt: time.Now().UTC()}, 1)
	if !errors.Is(hbErr, context.Canceled) {
		t.Fatalf("expected context.Canceled on heartbeat, got %v", hbErr)
	}
}

func TestMQTTSenderSecureSchemeDetection(t *testing.T) {
	cases := []struct {
		broker string
		want   bool
	}{
		{broker: "ssl://broker.local:8883", want: true},
		{broker: "tls://broker.local:8883", want: true},
		{broker: "tcps://broker.local:8883", want: true},
		{broker: "wss://broker.local:8084/mqtt", want: true},
		{broker: "WSS://broker.local:8084/mqtt", want: true},
		{broker: "TLS://broker.local:8883", want: true},
		{broker: "mqtts://broker.local:8883", want: true},
		{broker: "tcp://broker.local:1883", want: false},
		{broker: "ws://broker.local:8083/mqtt", want: false},
	}
	for _, tc := range cases {
		if got := isSecureMQTTBroker(tc.broker); got != tc.want {
			t.Errorf("isSecureMQTTBroker(%q) = %v, want %v", tc.broker, got, tc.want)
		}
	}
}

type mockMQTTToken struct {
	done chan struct{}
	err  error
}

func newDoneMQTTToken(err error) *mockMQTTToken {
	ch := make(chan struct{})
	close(ch)
	return &mockMQTTToken{done: ch, err: err}
}

func (m *mockMQTTToken) Wait() bool                     { return true }
func (m *mockMQTTToken) WaitTimeout(time.Duration) bool { return true }
func (m *mockMQTTToken) Done() <-chan struct{}          { return m.done }
func (m *mockMQTTToken) Error() error                   { return m.err }

type flakyMQTTClient struct {
	mqtt.Client
	failCount int
	calls     int
	payloads  [][]byte
}

func (f *flakyMQTTClient) IsConnected() bool { return true }

func (f *flakyMQTTClient) Publish(_ string, _ byte, _ bool, payload any) mqtt.Token {
	f.calls++
	if raw, ok := payload.([]byte); ok {
		f.payloads = append(f.payloads, append([]byte(nil), raw...))
	}
	if f.calls <= f.failCount {
		return newDoneMQTTToken(errors.New("transient broker disconnect"))
	}
	return newDoneMQTTToken(nil)
}

func TestMQTTSenderRetriesTransientPublishFailure(t *testing.T) {
	sender, err := NewMQTTSender(MQTTSenderOptions{
		ID:        "mqtt_retry",
		ImageMode: ImageModeBase64,
		Broker:    "tcp://127.0.0.1:1883",
		ClientID:  "zhulong-retry-1",
		Topic:     "zhulong/alerts/retry",
		QoS:       1,
	})
	if err != nil {
		t.Fatalf("NewMQTTSender: %v", err)
	}
	fakeClient := &flakyMQTTClient{failCount: 2}
	sender.client = fakeClient

	ev := record.AlarmEvent{
		ID:         "01MQTTRETRY",
		CameraID:   "cam_01",
		CapturedAt: time.Now().UTC(),
	}
	if err := sender.Send(context.Background(), Delivery{Event: ev, Seq: 12}); err != nil {
		t.Fatalf("Send should succeed on 3rd attempt, got %v", err)
	}
	if fakeClient.calls != 3 {
		t.Fatalf("publish attempts = %d, want 3", fakeClient.calls)
	}
	for i, raw := range fakeClient.payloads {
		var p EventPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatalf("attempt %d unmarshal: %v", i, err)
		}
		if p.ID != "01MQTTRETRY" || p.Seq == nil || *p.Seq != 12 {
			t.Fatalf("attempt %d changed id/seq: %+v", i, p)
		}
	}
}
