package alertpush

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"go.uber.org/zap"

	"github.com/nikonikowuw/Zhulong/internal/record"
)

type mockSender struct {
	mu           sync.Mutex
	received     []record.AlarmEvent
	deliveries   []Delivery
	heartbeatSeq []uint64
	delay        time.Duration
	err          error
	hbErr        error
	closed       atomic.Bool
}

func (m *mockSender) Send(ctx context.Context, d Delivery) error {
	if m.delay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.delay):
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.received = append(m.received, d.Event)
	m.deliveries = append(m.deliveries, d)
	return nil
}

func (m *mockSender) SendHeartbeat(ctx context.Context, _ HeartbeatSnapshot, seq uint64) error {
	if m.delay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.delay):
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hbErr != nil {
		return m.hbErr
	}
	m.heartbeatSeq = append(m.heartbeatSeq, seq)
	return nil
}

func (m *mockSender) Close() error {
	m.closed.Store(true)
	return nil
}

func (m *mockSender) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.received)
}

func (m *mockSender) hbCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.heartbeatSeq)
}

func TestDispatcherFaultIsolation(t *testing.T) {
	d := NewDispatcher(DispatcherOptions{Logger: zap.NewNop()})
	ctx := context.Background()
	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() {
		if err := d.Stop(ctx); err != nil {
			t.Errorf("Stop failed: %v", err)
		}
	}()

	fastSender := &mockSender{}
	slowSender := &mockSender{delay: 2 * time.Second}

	fastWorker := newDestinationWorker(destinationWorkerOptions{
		dest:        &AlertDestination{ID: "fast", ChannelType: ChannelTypeWebhook},
		sender:      fastSender,
		filter:      destinationFilter{},
		fingerprint: "fp1",
		logger:      zap.NewNop(),
	})
	slowWorker := newDestinationWorker(destinationWorkerOptions{
		dest:        &AlertDestination{ID: "slow", ChannelType: ChannelTypeWebhook},
		sender:      slowSender,
		filter:      destinationFilter{},
		fingerprint: "fp2",
		logger:      zap.NewNop(),
	})

	d.mu.Lock()
	d.workers["fast"] = fastWorker
	d.workers["slow"] = slowWorker
	d.mu.Unlock()

	fastWorker.start()
	slowWorker.start()

	ev := record.AlarmEvent{
		ID:         "01J8ZTEST01",
		CameraID:   "cam_01",
		CapturedAt: time.Now().UTC(),
	}

	start := time.Now()
	d.Dispatch(ev)

	// fast sender 应该在 100ms 内收到，不受 slow sender 2s 延迟影响
	deadline := time.Now().Add(500 * time.Millisecond)
	for fastSender.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if fastSender.count() != 1 {
		t.Fatalf("expected fast sender to receive event quickly, elapsed=%v", time.Since(start))
	}
}

func TestDispatcherQueueDropOldest(t *testing.T) {
	d := NewDispatcher(DispatcherOptions{Logger: zap.NewNop()})
	ctx := context.Background()
	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() {
		if err := d.Stop(ctx); err != nil {
			t.Errorf("Stop failed: %v", err)
		}
	}()

	// 不启动 worker 消费，纯测试队列填满丢最旧
	sender := &mockSender{}
	w := newDestinationWorker(destinationWorkerOptions{
		dest:        &AlertDestination{ID: "drop_test", ChannelType: ChannelTypeWebhook},
		sender:      sender,
		filter:      destinationFilter{},
		fingerprint: "fp",
		logger:      zap.NewNop(),
	})

	d.mu.Lock()
	d.workers["drop_test"] = w
	d.mu.Unlock()

	// 队列容量 16，连续投递 20 条
	for range 20 {
		d.Dispatch(record.AlarmEvent{ID: "ev"})
	}

	if len(w.queue) != 16 {
		t.Fatalf("expected queue length 16, got %d", len(w.queue))
	}
	if w.dropped.Load() != 4 {
		t.Fatalf("expected 4 dropped events, got %d", w.dropped.Load())
	}
}

func TestDispatcherStopSafe(t *testing.T) {
	d := NewDispatcher(DispatcherOptions{Logger: zap.NewNop()})
	ctx := context.Background()

	// 未 Start 调用 Dispatch 不 panic
	d.Dispatch(record.AlarmEvent{ID: "safe"})

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if err := d.Stop(ctx); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// 停止后调用 Dispatch 不 panic
	d.Dispatch(record.AlarmEvent{ID: "safe_after_stop"})
}

func TestDispatcherReconcilePreservesUnchangedWorker(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	dest := &AlertDestination{
		ID:          "dest_reconcile_test",
		Name:        "Webhook 1",
		ChannelType: ChannelTypeWebhook,
		Enabled:     true,
		ImageMode:   ImageModeURL,
		ConfigJSON:  `{"auth_type":"none","timeout_seconds":5}`,
		FilterJSON:  `{}`,
	}
	secBytes, err := store.EncryptSecret(dest.ID, &webhookSecret{URL: "http://127.0.0.1:9999/hook"})
	if err != nil {
		t.Fatalf("EncryptSecret failed: %v", err)
	}
	dest.SecretCiphertext = secBytes
	if err := store.Create(ctx, dest); err != nil {
		t.Fatalf("Create destination failed: %v", err)
	}

	d := NewDispatcher(DispatcherOptions{
		Store:  store,
		Signer: NewEmptyURLSigner(),
		Config: Config{PublicBaseURL: "http://127.0.0.1:8000"},
		Logger: zap.NewNop(),
	})
	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start dispatcher failed: %v", err)
	}
	defer func() {
		if err := d.Stop(ctx); err != nil {
			t.Errorf("Stop failed: %v", err)
		}
	}()

	d.mu.RLock()
	initialWorker, ok := d.workers[dest.ID]
	d.mu.RUnlock()
	if !ok || initialWorker == nil {
		t.Fatalf("expected initial worker to be created on start")
	}

	// 再次触发 reconcile，目标配置未改变
	d.reconcile()

	d.mu.RLock()
	currentWorker := d.workers[dest.ID]
	d.mu.RUnlock()

	// 核心断言：worker 指针恒定，没有被粗暴销毁替换
	if currentWorker != initialWorker {
		t.Fatalf("expected worker instance to be preserved on unchanged reconcile")
	}
}

func TestDispatcherDualTrackHeartbeatAndEventsDisabled(t *testing.T) {
	d := NewDispatcher(DispatcherOptions{Logger: zap.NewNop()})
	ctx := context.Background()
	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() {
		if err := d.Stop(ctx); err != nil {
			t.Errorf("Stop failed: %v", err)
		}
	}()

	hbOnlySender := &mockSender{}
	hbOnlyWorker := newDestinationWorker(destinationWorkerOptions{
		dest: &AlertDestination{
			ID:               "hb_only",
			ChannelType:      ChannelTypeWebhook,
			EventsEnabled:    false,
			HeartbeatEnabled: true,
		},
		sender:            hbOnlySender,
		filter:            destinationFilter{},
		fingerprint:       "fp_hb_only",
		heartbeatInterval: 20 * time.Millisecond,
		noJitter:          true,
		seqCounter:        d.counterFor("hb_only"),
		logger:            zap.NewNop(),
	})

	d.mu.Lock()
	d.workers["hb_only"] = hbOnlyWorker
	d.mu.Unlock()
	hbOnlyWorker.start()

	// Dispatching alarms must NEVER enqueue or deliver to a heartbeat-only destination.
	for range 25 {
		d.Dispatch(record.AlarmEvent{ID: "ev", CameraID: "cam_01"})
	}
	if len(hbOnlyWorker.queue) != 0 || hbOnlyWorker.dropped.Load() != 0 {
		t.Fatalf("expected 0 queued and 0 dropped on heartbeat-only worker, got queued=%d dropped=%d",
			len(hbOnlyWorker.queue), hbOnlyWorker.dropped.Load())
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for hbOnlySender.hbCount() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hbOnlySender.hbCount() < 2 {
		t.Fatalf("expected at least 2 heartbeats delivered, got %d", hbOnlySender.hbCount())
	}

	hbOnlySender.mu.Lock()
	seq0, seq1 := hbOnlySender.heartbeatSeq[0], hbOnlySender.heartbeatSeq[1]
	hbOnlySender.mu.Unlock()
	if seq0 != 1 || seq1 != 2 {
		t.Fatalf("expected monotonic seq 1, 2, got %d, %d", seq0, seq1)
	}

	st := d.GetStatus("hb_only", true)
	if st.State != StateConnected || st.LastSuccessAt == nil {
		t.Fatalf("expected heartbeat success to mark destination connected, got %+v", st)
	}
}

func TestDispatcherHeartbeatFailureDoesNotIncrementDropped(t *testing.T) {
	failSender := &mockSender{hbErr: context.DeadlineExceeded}
	w := newDestinationWorker(destinationWorkerOptions{
		dest: &AlertDestination{
			ID:               "hb_fail",
			ChannelType:      ChannelTypeWebhook,
			EventsEnabled:    true,
			HeartbeatEnabled: true,
		},
		sender:            failSender,
		filter:            destinationFilter{},
		fingerprint:       "fp_hb_fail",
		heartbeatInterval: 25 * time.Millisecond,
		noJitter:          true,
		logger:            zap.NewNop(),
	})
	w.start()
	defer w.stop()

	deadline := time.Now().Add(500 * time.Millisecond)
	for w.getStatus().State != StateFailed && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	st := w.getStatus()
	if st.State != StateFailed || st.LastError != "timeout" {
		t.Fatalf("expected failed/timeout status from failing heartbeat, got %+v", st)
	}
	if st.Dropped != 0 {
		t.Fatalf("heartbeat failure must never increment dropped counter, got %d", st.Dropped)
	}
}

type atomicIdentity struct {
	val atomic.Value
}

func (a *atomicIdentity) DeviceID() string {
	v, ok := a.val.Load().(string)
	if !ok {
		return ""
	}
	return v
}

func TestDispatcherMQTTHotReloadNoSessionKickOrFalseOffline(t *testing.T) {
	brokerURL, cleanupBroker := startInProcessBroker(t)
	defer cleanupBroker()

	store, cleanupStore := setupTestStore(t)
	defer cleanupStore()

	subClient := mqtt.NewClient(
		mqtt.NewClientOptions().AddBroker(brokerURL).SetClientID("test-sub-reload"),
	)
	if tok := subClient.Connect(); tok.Wait() && tok.Error() != nil {
		t.Fatalf("subscriber connect failed: %v", tok.Error())
	}
	defer subClient.Disconnect(100)

	msgCh := make(chan []byte, 8)
	if tok := subClient.Subscribe("zhulong/status/reload", 1, func(_ mqtt.Client, msg mqtt.Message) {
		cp := append([]byte(nil), msg.Payload()...)
		msgCh <- cp
	}); tok.Wait() && tok.Error() != nil {
		t.Fatalf("subscribe failed: %v", tok.Error())
	}

	identity := &atomicIdentity{}
	identity.val.Store("box-v1")

	ctx := context.Background()
	clientID := "zhulong-reload-01"
	cfgBytes, err := json.Marshal(mqttConfig{
		Broker:                   brokerURL,
		Topic:                    "zhulong/events/reload",
		HeartbeatTopic:           "zhulong/status/reload",
		HeartbeatIntervalSeconds: 15,
		QoS:                      1,
		KeepAliveSeconds:         30,
	})
	if err != nil {
		t.Fatalf("marshal mqtt config: %v", err)
	}
	secBytes, err := store.EncryptSecret("01J8ZRELOAD000000000000001", &mqttSecret{})
	if err != nil {
		t.Fatalf("encrypt secret: %v", err)
	}
	if err := store.Create(ctx, &AlertDestination{
		ID:               "01J8ZRELOAD000000000000001",
		Name:             "MQTT Reload Target",
		ChannelType:      ChannelTypeMQTT,
		Enabled:          true,
		EventsEnabled:    false,
		HeartbeatEnabled: true,
		ImageMode:        ImageModeURL,
		MQTTClientID:     &clientID,
		ConfigJSON:       string(cfgBytes),
		SecretCiphertext: secBytes,
		FilterJSON:       "{}",
	}); err != nil {
		t.Fatalf("store.Create: %v", err)
	}

	d := NewDispatcher(DispatcherOptions{
		Store: store,
		Config: Config{
			DeviceID: identity,
		},
		Logger: zap.NewNop(),
	})
	if err := d.Start(ctx); err != nil {
		t.Fatalf("Dispatcher.Start: %v", err)
	}

	waitStatusMsg := func(label string) map[string]any {
		t.Helper()
		select {
		case raw := <-msgCh:
			out := map[string]any{}
			if unmarshalErr := json.Unmarshal(raw, &out); unmarshalErr != nil {
				t.Fatalf("%s: unmarshal payload: %v", label, unmarshalErr)
			}
			return out
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: timed out waiting for status message", label)
			return nil
		}
	}

	// 1. Initial worker connects and emits seq=1 heartbeat with box-v1.
	first := waitStatusMsg("initial heartbeat")
	if first["event_type"] != EventTypeHeartbeat || first["device_id"] != "box-v1" {
		t.Fatalf("unexpected first message: %+v", first)
	}

	// 2. Rename device_id and reconcile: old worker retires quietly (NO offline LWT or
	// graceful_shutdown), replacement worker connects with same ClientID and emits seq=2!
	identity.val.Store("box-v2")
	d.reconcile()

	second := waitStatusMsg("post-reload heartbeat")
	if second["event_type"] != EventTypeHeartbeat {
		t.Fatalf("hot-reload must NOT emit false offline message; got %+v", second)
	}
	if second["device_id"] != "box-v2" || second["seq"] != float64(2) {
		t.Fatalf("expected seq=2 with updated device_id=box-v2, got %+v", second)
	}

	// 3. Actual Dispatcher.Stop emits graceful_shutdown offline notice.
	if err := d.Stop(ctx); err != nil {
		t.Fatalf("Dispatcher.Stop: %v", err)
	}
	offline := waitStatusMsg("shutdown offline")
	offlineInfo, _ := offline["offline"].(map[string]any)
	isGracefulOffline := offline["event_type"] == EventTypeOffline &&
		offlineInfo != nil && offlineInfo["reason"] == OfflineReasonGracefulShutdown
	if !isGracefulOffline {
		t.Fatalf("expected graceful_shutdown offline on Stop, got %+v", offline)
	}
}

type budgetAwareSender struct {
	mu          sync.Mutex
	eventBudget time.Duration
	hbBudget    time.Duration
	eventRemain time.Duration
	hbRemain    time.Duration
	eventDoneCh chan struct{}
	hbDoneCh    chan struct{}
	eventOnce   sync.Once
	hbOnce      sync.Once
}

func (s *budgetAwareSender) EventSendBudget() time.Duration     { return s.eventBudget }
func (s *budgetAwareSender) HeartbeatSendBudget() time.Duration { return s.hbBudget }

func (s *budgetAwareSender) Send(ctx context.Context, _ Delivery) error {
	if dl, ok := ctx.Deadline(); ok {
		s.mu.Lock()
		s.eventRemain = time.Until(dl)
		s.mu.Unlock()
	}
	s.eventOnce.Do(func() { close(s.eventDoneCh) })
	return nil
}

func (s *budgetAwareSender) SendHeartbeat(ctx context.Context, _ HeartbeatSnapshot, _ uint64) error {
	if dl, ok := ctx.Deadline(); ok {
		s.mu.Lock()
		s.hbRemain = time.Until(dl)
		s.mu.Unlock()
	}
	s.hbOnce.Do(func() { close(s.hbDoneCh) })
	return nil
}

func (s *budgetAwareSender) Close() error { return nil }

func TestDispatcherUsesSenderTimeoutBudgets(t *testing.T) {
	d := NewDispatcher(DispatcherOptions{Logger: zap.NewNop()})
	ctx := context.Background()
	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		if err := d.Stop(ctx); err != nil {
			t.Errorf("Stop: %v", err)
		}
	}()

	sender := &budgetAwareSender{
		eventBudget: 25 * time.Second,
		hbBudget:    18 * time.Second,
		eventDoneCh: make(chan struct{}),
		hbDoneCh:    make(chan struct{}),
	}
	w := newDestinationWorker(destinationWorkerOptions{
		dest: &AlertDestination{
			ID:               "budget_worker",
			ChannelType:      ChannelTypeWebhook,
			EventsEnabled:    true,
			HeartbeatEnabled: true,
		},
		sender:            sender,
		heartbeatInterval: 50 * time.Millisecond,
		noJitter:          true,
		fingerprint:       "fp-budget",
		logger:            zap.NewNop(),
	})

	d.mu.Lock()
	d.workers[w.id] = w
	d.mu.Unlock()
	w.start()

	d.Dispatch(record.AlarmEvent{ID: "ev-budget", CameraID: "cam-1"})

	select {
	case <-sender.eventDoneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event delivery")
	}
	select {
	case <-sender.hbDoneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for heartbeat delivery")
	}

	sender.mu.Lock()
	eventRemain := sender.eventRemain
	hbRemain := sender.hbRemain
	sender.mu.Unlock()

	if eventRemain <= 20*time.Second {
		t.Fatalf("event ctx budget was truncated: remaining=%v, want >20s", eventRemain)
	}
	if hbRemain <= 14*time.Second {
		t.Fatalf("heartbeat ctx budget was truncated: remaining=%v, want >14s", hbRemain)
	}

	// Plain sender without timeoutBudgets keeps the 10s default.
	plain := newDestinationWorker(destinationWorkerOptions{
		dest:   &AlertDestination{ID: "plain", ChannelType: ChannelTypeWebhook},
		sender: &mockSender{},
		logger: zap.NewNop(),
	})
	if plain.eventSendBudget() != defaultSendBudget || plain.heartbeatSendBudget() != defaultSendBudget {
		t.Fatalf("expected defaultSendBudget=%v for plain sender", defaultSendBudget)
	}
}

func TestDispatcherStatusTracksAttemptFailureAndRecovery(t *testing.T) {
	sender := &mockSender{err: context.DeadlineExceeded}
	w := newDestinationWorker(destinationWorkerOptions{
		dest:        &AlertDestination{ID: "status_track", ChannelType: ChannelTypeWebhook, EventsEnabled: true},
		sender:      sender,
		fingerprint: "fp-status",
		logger:      zap.NewNop(),
	})
	w.start()
	defer w.stop()

	w.enqueue(record.AlarmEvent{ID: "ev-fail-1"})
	w.enqueue(record.AlarmEvent{ID: "ev-fail-2"})

	deadline := time.Now().Add(time.Second)
	for w.getStatus().ConsecutiveFailures < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	st := w.getStatus()
	if st.State != StateFailed || st.ConsecutiveFailures != 2 {
		t.Fatalf("expected failed with 2 consecutive failures, got %+v", st)
	}
	if st.LastAttemptAt == nil || st.LastFailureAt == nil || st.LastSuccessAt != nil {
		t.Fatalf("expected LastAttemptAt & LastFailureAt set and LastSuccessAt nil, got %+v", st)
	}

	// Recover on next delivery: consecutiveFailures resets to 0 and LastSuccessAt is populated.
	sender.mu.Lock()
	sender.err = nil
	sender.mu.Unlock()
	w.enqueue(record.AlarmEvent{ID: "ev-ok"})

	deadline = time.Now().Add(time.Second)
	for w.getStatus().State != StateConnected && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	recovered := w.getStatus()
	if recovered.State != StateConnected || recovered.ConsecutiveFailures != 0 || recovered.LastSuccessAt == nil {
		t.Fatalf("expected recovered connected status with 0 failures, got %+v", recovered)
	}
}

func TestDispatcherMQTTConnectionCallbacksUpdateState(t *testing.T) {
	brokerURL, rawCleanupBroker := startInProcessBroker(t)
	var closeBrokerOnce sync.Once
	cleanupBroker := func() { closeBrokerOnce.Do(rawCleanupBroker) }
	defer cleanupBroker()

	sender, err := NewMQTTSender(MQTTSenderOptions{
		ID:        "mqtt-cb",
		ImageMode: ImageModeBase64,
		Broker:    brokerURL,
		ClientID:  "zhulong-cb-test",
		Topic:     "zhulong/events",
		KeepAlive: 10 * time.Second,
		Logger:    zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("NewMQTTSender: %v", err)
	}

	w := newDestinationWorker(destinationWorkerOptions{
		dest: &AlertDestination{
			ID:               "mqtt-cb",
			ChannelType:      ChannelTypeMQTT,
			EventsEnabled:    true,
			HeartbeatEnabled: false,
		},
		sender:      sender,
		fingerprint: "fp-cb",
		logger:      zap.NewNop(),
	})
	if st := w.getStatus(); st.State != StateConnecting {
		t.Fatalf("initial MQTT state = %q, want connecting", st.State)
	}
	w.start()
	defer w.stop()

	// Without sending any event or heartbeat, OnConnect must transition connecting -> connected.
	deadline := time.Now().Add(3 * time.Second)
	for w.getStatus().State != StateConnected && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if st := w.getStatus(); st.State != StateConnected {
		t.Fatalf("expected MQTT state connected via OnConnect hook, got %+v", st)
	}

	// Closing the broker must trigger ConnectionLost -> failed.
	cleanupBroker()
	deadline = time.Now().Add(3 * time.Second)
	for w.getStatus().State != StateFailed && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if st := w.getStatus(); st.State != StateFailed || st.LastError == "" {
		t.Fatalf("expected MQTT state failed after broker stop, got %+v", st)
	}
}

func TestDispatcherConcurrentStopAndRemoveState(t *testing.T) {
	d := NewDispatcher(DispatcherOptions{Logger: zap.NewNop()})
	ctx := context.Background()
	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// 4 workers each taking 300ms to drain one event; concurrent Stop must finish well below 1.2s.
	for i := range 4 {
		id := "slow-stop-" + string(rune('a'+i))
		w := newDestinationWorker(destinationWorkerOptions{
			dest:        &AlertDestination{ID: id, ChannelType: ChannelTypeWebhook, EventsEnabled: true},
			sender:      &mockSender{delay: 300 * time.Millisecond},
			fingerprint: "fp-" + id,
			seqCounter:  d.counterFor(id),
			logger:      zap.NewNop(),
		})
		w.enqueue(record.AlarmEvent{ID: "drain-" + id})
		d.mu.Lock()
		d.workers[id] = w
		d.mu.Unlock()
		w.start()
	}

	// Ensure RemoveDestinationState removes cached stats and sequence entries.
	d.UpdateTestResult("deleted-id", true, "")
	if got := d.counterFor("deleted-id").Add(1); got != 1 {
		t.Fatalf("expected initial seq=1, got %d", got)
	}
	d.RemoveDestinationState("deleted-id")

	d.statsMu.RLock()
	_, hasStat := d.stats["deleted-id"]
	d.statsMu.RUnlock()
	d.seqMu.Lock()
	_, hasSeq := d.seqMap["deleted-id"]
	d.seqMu.Unlock()
	if hasStat || hasSeq {
		t.Fatalf("expected RemoveDestinationState to clear stats=%v seq=%v", hasStat, hasSeq)
	}

	start := time.Now()
	if err := d.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed >= 900*time.Millisecond {
		t.Fatalf("expected concurrent Stop across 4 workers (<900ms), took %v", elapsed)
	}
}

func TestDispatcherStopBoundsSlowInFlightSend(t *testing.T) {
	d := NewDispatcher(DispatcherOptions{Logger: zap.NewNop()})
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	startedCh := make(chan struct{})
	slowSender := &blockingBudgetSender{
		eventBudget: 92 * time.Second,
		startedCh:   startedCh,
	}
	w := newDestinationWorker(destinationWorkerOptions{
		dest:        &AlertDestination{ID: "slow-92s", ChannelType: ChannelTypeWebhook, EventsEnabled: true},
		sender:      slowSender,
		fingerprint: "fp-slow",
		logger:      zap.NewNop(),
	})
	d.mu.Lock()
	d.workers[w.id] = w
	d.mu.Unlock()
	w.start()

	d.Dispatch(record.AlarmEvent{ID: "ev-slow"})
	select {
	case <-startedCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for slow send to begin")
	}

	start := time.Now()
	if err := d.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed > retirementDrainTimeout+1500*time.Millisecond {
		t.Fatalf("Stop took %v, expected bounded by retirementDrainTimeout (%v)", elapsed, retirementDrainTimeout)
	}
}

type blockingBudgetSender struct {
	eventBudget time.Duration
	startedCh   chan struct{}
	once        sync.Once
}

func (s *blockingBudgetSender) EventSendBudget() time.Duration     { return s.eventBudget }
func (s *blockingBudgetSender) HeartbeatSendBudget() time.Duration { return defaultSendBudget }
func (s *blockingBudgetSender) Close() error                       { return nil }
func (s *blockingBudgetSender) SendHeartbeat(context.Context, HeartbeatSnapshot, uint64) error {
	return nil
}

func (s *blockingBudgetSender) Send(ctx context.Context, _ Delivery) error {
	s.once.Do(func() { close(s.startedCh) })
	<-ctx.Done()
	return ctx.Err()
}

func TestDispatcherReconcilePreservesCumulativeStatusAndCapacity(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	dest := &AlertDestination{
		ID:               "dest_stats_preserve",
		Name:             "Webhook Stats",
		ChannelType:      ChannelTypeWebhook,
		Enabled:          true,
		EventsEnabled:    true,
		HeartbeatEnabled: false,
		ImageMode:        ImageModeURL,
		ConfigJSON:       `{"auth_type":"none","timeout_seconds":5}`,
		FilterJSON:       `{}`,
	}
	secBytes, err := store.EncryptSecret(dest.ID, &webhookSecret{URL: "http://127.0.0.1:9999/hook"})
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	dest.SecretCiphertext = secBytes
	if err := store.Create(ctx, dest); err != nil {
		t.Fatalf("store.Create: %v", err)
	}

	d := NewDispatcher(DispatcherOptions{
		Store:  store,
		Signer: NewEmptyURLSigner(),
		Config: Config{PublicBaseURL: "http://127.0.0.1:8000"},
		Logger: zap.NewNop(),
	})
	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		if stopErr := d.Stop(ctx); stopErr != nil {
			t.Errorf("Stop: %v", stopErr)
		}
	}()

	d.mu.RLock()
	origWorker := d.workers[dest.ID]
	d.mu.RUnlock()
	origWorker.totalDispatched.Store(7)
	origWorker.dropped.Store(3)
	origWorker.recordSuccess()
	origWorker.recordFailure("timeout")

	// Update config to force worker replacement in reconcile.
	dest.ConfigJSON = `{"auth_type":"none","timeout_seconds":8}`
	if err := store.Update(ctx, dest); err != nil {
		t.Fatalf("store.Update: %v", err)
	}
	d.reconcile()

	st := d.GetStatus(dest.ID, true)
	if st.TotalDispatched != 7 || st.Dropped != 3 || st.QueueCapacity != workerQueueCapacity {
		t.Fatalf("status counters not preserved across reconcile: %+v", st)
	}
	if st.LastSuccessAt == nil || st.LastFailureAt == nil || st.LastAttemptAt == nil {
		t.Fatalf("status timestamps not preserved across reconcile: %+v", st)
	}
	if st.State != StateUnverified || st.ConsecutiveFailures != 0 || st.LastError != "" {
		t.Fatalf("health state should reset on config reload, got %+v", st)
	}
}

func TestClassifyErrorRedirectReasons(t *testing.T) {
	if got := classifyError(ErrCrossOriginRedirect); got != "cross_origin_redirect" {
		t.Fatalf("classifyError(ErrCrossOriginRedirect) = %q, want cross_origin_redirect", got)
	}
	if got := classifyError(ErrWebhookRedirectMethodChanged); got != "redirect_blocked" {
		t.Fatalf("classifyError(ErrWebhookRedirectMethodChanged) = %q, want redirect_blocked", got)
	}
	if got := classifyError(ErrWebhookTooManyRedirects); got != "redirect_blocked" {
		t.Fatalf("classifyError(ErrWebhookTooManyRedirects) = %q, want redirect_blocked", got)
	}
}
