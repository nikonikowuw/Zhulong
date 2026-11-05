package alertpush

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
	_ = server.AddHook(new(auth.AllowHook), nil)

	// Pick random free port
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to pick free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	tcp := listeners.NewTCP(listeners.Config{
		ID:      "test-listener",
		Address: addr,
	})
	if err := server.AddListener(tcp); err != nil {
		t.Fatalf("failed to add listener: %v", err)
	}

	go func() {
		_ = server.Serve()
	}()

	time.Sleep(50 * time.Millisecond)

	return "tcp://" + addr, func() {
		_ = server.Close()
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

	if err := sender.Send(context.Background(), ev); err != nil {
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

	var payload AlarmPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatalf("unmarshal payload failed: %v", err)
	}
	if !payload.ImageReady || payload.ImageBase64 == nil {
		t.Fatalf("expected ImageReady true and base64 populated, got ready=%v", payload.ImageReady)
	}
	decoded, err := base64.StdEncoding.DecodeString(*payload.ImageBase64)
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

	if err := sender.Send(context.Background(), ev); err != nil {
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

	var payload AlarmPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatalf("unmarshal payload failed: %v", err)
	}
	if payload.ImageReady {
		t.Error("expected ImageReady=false when image exceeds 500KB")
	}
	if payload.ImageBase64 != nil {
		t.Error("expected ImageBase64 to be nil when image exceeds 500KB")
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
	err = sender.Send(ctx, ev)
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("expected immediate return on canceled context, took %v", elapsed)
	}
}
