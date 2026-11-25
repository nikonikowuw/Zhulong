package algorunner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algoipc"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

type recordedBroadcast struct {
	streamID uint64
	msgType  uint16
	payload  []byte
}

// fakeBroadcaster records every push so the test can assert stream routing and
// payload framing without a real FrameBroker.
type fakeBroadcaster struct {
	mu      sync.Mutex
	records []recordedBroadcast
	err     error
	onPush  func(streamID uint64, msgType uint16, payload []byte)
}

func (f *fakeBroadcaster) BroadcastAIMessage(
	_ context.Context,
	streamID uint64,
	msgType uint16,
	payload []byte,
) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	cp := append([]byte(nil), payload...)
	f.records = append(f.records, recordedBroadcast{streamID: streamID, msgType: msgType, payload: cp})
	if f.onPush != nil {
		f.onPush(streamID, msgType, cp)
	}
	return 1, nil
}

func (f *fakeBroadcaster) snapshot() []recordedBroadcast {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recordedBroadcast, len(f.records))
	copy(out, f.records)
	return out
}

func (f *fakeBroadcaster) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = nil
}

func TestTaskConfigBroadcasterRoutesPerStream(t *testing.T) {
	broadcaster := &fakeBroadcaster{}
	b := NewTaskConfigBroadcaster(broadcaster, zap.NewNop())

	configs := map[uint64]string{
		7: `{"threshold":0.55}`,
		3: `{"threshold":0.9,"top_k":5}`,
	}
	if err := b.BroadcastAlgorithm(context.Background(), "face_recognition", configs); err != nil {
		t.Fatalf("BroadcastAlgorithm: %v", err)
	}

	if len(broadcaster.records) != 2 {
		t.Fatalf("expected one push per stream, got %d", len(broadcaster.records))
	}
	// Deterministic order (insertion-sorted stream ids) is what makes the log sequence
	// and this assertion stable across runs.
	if broadcaster.records[0].streamID != 3 || broadcaster.records[1].streamID != 7 {
		t.Fatalf("streams pushed out of order: %d then %d",
			broadcaster.records[0].streamID, broadcaster.records[1].streamID)
	}
	for _, rec := range broadcaster.records {
		if rec.msgType != algoipc.MsgTaskConfig {
			t.Fatalf("message type = %d, want MsgTaskConfig", rec.msgType)
		}
		want := configs[rec.streamID]
		if got := string(rec.payload[algoipc.TaskConfigHeaderSize:]); got != want {
			t.Fatalf("stream %d config = %q, want %q", rec.streamID, got, want)
		}
		if got := string(rec.payload[4 : 4+len("face_recognition")]); got != "face_recognition" {
			t.Fatalf("stream %d algorithm id = %q", rec.streamID, got)
		}
	}
}

func TestTaskConfigBroadcasterSkipsInvalidJSON(t *testing.T) {
	broadcaster := &fakeBroadcaster{}
	core, observed := observer.New(zapcore.WarnLevel)
	b := NewTaskConfigBroadcaster(broadcaster, zap.New(core))

	configs := map[uint64]string{
		1: `{"threshold":0.5}`,
		2: `threshold=0.5`, // malformed: must not reach the package
	}
	if err := b.BroadcastAlgorithm(context.Background(), "face_recognition", configs); err != nil {
		t.Fatalf("BroadcastAlgorithm: %v", err)
	}
	if len(broadcaster.records) != 1 || broadcaster.records[0].streamID != 1 {
		t.Fatalf("malformed config must be skipped, got %+v", broadcaster.records)
	}

	// The skip shares its message and its algorithm_id/stream_id fields with a real
	// failure — but it must not carry an error, and it must not become firstErr.
	skips := observed.FilterMessage("skipping task config that is not valid JSON").All()
	if len(skips) != 1 {
		t.Fatalf("skipped configs logged %d time(s), want exactly 1: %+v", len(skips), observed.All())
	}
	fields := skips[0].ContextMap()
	if fields["stream_id"] != uint64(2) || fields["algorithm_id"] != "face_recognition" {
		t.Fatalf("skip log fields = %+v", fields)
	}
	if _, hasError := fields["error"]; hasError {
		t.Fatalf("a skipped config was logged as an error: %+v", fields)
	}
}

func TestTaskConfigBroadcasterPropagatesFirstError(t *testing.T) {
	sentinel := errors.New("broker gone")
	broadcaster := &fakeBroadcaster{err: sentinel}
	b := NewTaskConfigBroadcaster(broadcaster, zap.NewNop())

	// A single stream failing must not stop the others, but the caller still learns
	// that something failed (it logs once per algorithm, not once per stream).
	err := b.BroadcastAlgorithm(context.Background(), "face_recognition", map[uint64]string{
		1: `{}`,
		2: `{}`,
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected the broadcast error to surface, got: %v", err)
	}
}

func TestTaskConfigBroadcasterNoopWithoutConfigs(t *testing.T) {
	broadcaster := &fakeBroadcaster{}
	b := NewTaskConfigBroadcaster(broadcaster, zap.NewNop())

	if err := b.BroadcastAlgorithm(context.Background(), "face_recognition", nil); err != nil {
		t.Fatalf("nil configs must be a no-op: %v", err)
	}
	if len(broadcaster.records) != 0 {
		t.Fatalf("no push expected, got %d", len(broadcaster.records))
	}
}

// staticTaskConfigProvider returns a fixed map, recording the algorithm it was
// asked about.
type staticTaskConfigProvider struct {
	configs map[uint64]string
	err     error
	asked   []string
}

func (p *staticTaskConfigProvider) TaskConfigsFor(_ context.Context, algorithmID string) (map[uint64]string, error) {
	p.asked = append(p.asked, algorithmID)
	return p.configs, p.err
}

// TestSupervisorPushTaskConfigs pins the pre-spawn seeding: without it a package
// that starts while no push is in flight would run on compiled defaults forever.
func TestSupervisorPushTaskConfigs(t *testing.T) {
	broadcaster := &fakeBroadcaster{}
	provider := &staticTaskConfigProvider{configs: map[uint64]string{5: `{"threshold":0.7}`}}
	s := &Supervisor{logger: zap.NewNop()}
	s.SetTaskConfig(provider, NewTaskConfigBroadcaster(broadcaster, zap.NewNop()))

	s.pushTaskConfigs("face_recognition")

	if len(provider.asked) != 1 || provider.asked[0] != "face_recognition" {
		t.Fatalf("provider called with %v", provider.asked)
	}
	if len(broadcaster.records) != 1 || broadcaster.records[0].streamID != 5 {
		t.Fatalf("expected one push to stream 5, got %+v", broadcaster.records)
	}
}

// TestSupervisorPushTaskConfigsWithoutWiringIsNoop keeps the pre-spawn hook safe
// for hosts (and tests) that never call SetTaskConfig.
func TestSupervisorPushTaskConfigsWithoutWiringIsNoop(t *testing.T) {
	s := &Supervisor{logger: zap.NewNop()}
	s.pushTaskConfigs("face_recognition") // must not panic or block
}

type staticTaskRegionProvider struct {
	staticTaskConfigProvider
	regions map[uint64]string
	err     error
}

func (p *staticTaskRegionProvider) TaskRegionsFor(_ context.Context, _ string) (map[uint64]string, error) {
	return p.regions, p.err
}

// loggedMessage reports whether message was emitted at least once.
func loggedMessage(observed *observer.ObservedLogs, message string) bool {
	for _, entry := range observed.All() {
		if entry.Message == message {
			return true
		}
	}
	return false
}

func TestSupervisorPushRegionsAndUnchangedParams(t *testing.T) {
	broadcaster := &fakeBroadcaster{}
	tripwires := `{"tripwires":[{"id":"entry","name":"入口",` +
		`"points":[[0,0.5],[1,0.5]],"direction":"both"}]}`
	provider := &staticTaskRegionProvider{
		staticTaskConfigProvider: staticTaskConfigProvider{
			configs: map[uint64]string{5: `{"threshold":0.7}`},
		},
		regions: map[uint64]string{5: tripwires},
	}
	s := &Supervisor{logger: zap.NewNop()}
	s.SetTaskConfig(provider, NewTaskConfigBroadcaster(broadcaster, zap.NewNop()))
	s.pushTaskConfigs("face")
	if len(broadcaster.records) != 2 {
		t.Fatalf("want region and params, got %+v", broadcaster.records)
	}
	for _, record := range broadcaster.records {
		var want string
		switch record.msgType {
		case algoipc.MsgTaskRegions:
			want = provider.regions[5]
		case algoipc.MsgTaskConfig:
			want = provider.configs[5]
		default:
			t.Fatalf("unexpected type %d", record.msgType)
		}
		if got := string(record.payload[algoipc.TaskConfigHeaderSize:]); got != want {
			t.Fatalf("payload %q != %q", got, want)
		}
	}
	broadcaster.records = nil
	if err := s.taskConfigCast.BroadcastRegions(context.Background(), "face", map[uint64]string{5: `{}`}); err != nil {
		t.Fatal(err)
	}
	if string(broadcaster.records[0].payload[algoipc.TaskConfigHeaderSize:]) != "{}" {
		t.Fatal("clear snapshot missing")
	}
}

// TestSupervisorPushTaskConfigsLogsRegionResolveFailure pins the first half of the
// two-message contract: a region lookup that fails names the lookup and pushes
// nothing (a stale or missing snapshot must not reach a broker as if resolved).
func TestSupervisorPushTaskConfigsLogsRegionResolveFailure(t *testing.T) {
	core, observed := observer.New(zapcore.WarnLevel)
	broadcaster := &fakeBroadcaster{}
	provider := &staticTaskRegionProvider{
		staticTaskConfigProvider: staticTaskConfigProvider{err: nil},
		err:                      errors.New("region lookup failed"),
	}
	s := &Supervisor{logger: zap.New(core)}
	s.SetTaskConfig(provider, NewTaskConfigBroadcaster(broadcaster, zap.NewNop()))

	s.pushTaskConfigs("face")

	if len(broadcaster.records) != 0 {
		t.Fatalf("a failed region lookup broadcast something: %+v", broadcaster.records)
	}
	if !loggedMessage(observed, "failed to resolve task regions") {
		t.Fatalf("missing resolve-failure log; observed: %+v", observed.All())
	}
	if loggedMessage(observed, "failed to push task regions") {
		t.Fatalf("a resolve failure was reported as a push failure: %+v", observed.All())
	}
}

// TestSupervisorPushTaskConfigsLogsRegionPushFailure pins the second half: a
// snapshot that resolved but could not be delivered must be reported as a push
// failure, so an operator does not go looking at the region query.
func TestSupervisorPushTaskConfigsLogsRegionPushFailure(t *testing.T) {
	core, observed := observer.New(zapcore.WarnLevel)
	broadcaster := &fakeBroadcaster{err: errors.New("broker gone")}
	provider := &staticTaskRegionProvider{regions: map[uint64]string{5: `{}`}}
	s := &Supervisor{logger: zap.New(core)}
	s.SetTaskConfig(provider, NewTaskConfigBroadcaster(broadcaster, zap.NewNop()))

	s.pushTaskConfigs("face")

	if !loggedMessage(observed, "failed to push task regions") {
		t.Fatalf("missing push-failure log; observed: %+v", observed.All())
	}
	if loggedMessage(observed, "failed to resolve task regions") {
		t.Fatalf("a push failure was reported as a resolve failure: %+v", observed.All())
	}
}

type dynamicTaskConfigProvider struct {
	mu          sync.Mutex
	regions     map[uint64]string
	configs     map[uint64]string
	regionCalls int
	configCalls int
}

func (p *dynamicTaskConfigProvider) set(regions, configs map[uint64]string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.regions = regions
	p.configs = configs
}

func (p *dynamicTaskConfigProvider) TaskRegionsFor(_ context.Context, _ string) (map[uint64]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.regionCalls++
	out := make(map[uint64]string, len(p.regions))
	for k, v := range p.regions {
		out[k] = v
	}
	return out, nil
}

func (p *dynamicTaskConfigProvider) TaskConfigsFor(_ context.Context, _ string) (map[uint64]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.configCalls++
	out := make(map[uint64]string, len(p.configs))
	for k, v := range p.configs {
		out[k] = v
	}
	return out, nil
}

type dynamicBrokerResolver struct {
	mu    sync.Mutex
	socks []string
	err   error
}

func (r *dynamicBrokerResolver) set(socks []string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.socks = append([]string(nil), socks...)
	r.err = err
}

func (r *dynamicBrokerResolver) SyncBrokerSubscriptions(_ context.Context, _ string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	return append([]string(nil), r.socks...), nil
}

// TestWatcherRespawnRePushesTaskRegionsAndConfigs verifies that when a worker
// respawns (either after a camera drop/reconnect dependency failure or an
// ordinary crash), both MSG_TASK_REGIONS and MSG_TASK_CONFIG are re-seeded into
// the freshly resolved FrameBroker BEFORE the replacement runner starts.
func TestWatcherRespawnRePushesTaskRegionsAndConfigs(t *testing.T) {
	cases := []struct {
		name       string
		exitAction string
	}{
		{
			name:       "camera reconnect dependency exit",
			exitAction: "printf '{\"status\":\"info\",\"stage\":\"broker_disconnected\"}\\n' >&2\nexit 5\n",
		},
		{
			name:       "ordinary worker crash",
			exitAction: "exit 1\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const algoID = "face_recognition"
			algo := PackageMeta{ID: algoID, Name: algoID, Version: "1.0.0"}

			tmpDir := t.TempDir()
			runFile := filepath.Join(tmpDir, "starts")
			triggerFile := filepath.Join(tmpDir, "trigger")
			t.Setenv("RESPAWN_CFG_RUN_FILE", runFile)
			t.Setenv("RESPAWN_CFG_TRIGGER_FILE", triggerFile)

			// First start exits once the trigger appears; subsequent starts stay alive until SIGTERM.
			script := "#!/bin/sh\n" +
				"trap 'exit 0' TERM INT\n" +
				"printf 'run\\n' >> \"$RESPAWN_CFG_RUN_FILE\"\n" +
				"count=$(wc -l < \"$RESPAWN_CFG_RUN_FILE\")\n" +
				"if [ \"$count\" -eq 1 ]; then\n" +
				"  while [ ! -f \"$RESPAWN_CFG_TRIGGER_FILE\" ]; do sleep 0.01; done\n" +
				"  " + tc.exitAction +
				"fi\n" +
				"while true; do sleep 0.05; done\n"

			root := t.TempDir()
			installDependencyStubPackage(t, root, algo, script)

			opts := DefaultRecoveryOptions()
			opts.InitialBackoff = 5 * time.Millisecond
			opts.MaxBackoff = 10 * time.Millisecond
			supervisor := NewSupervisor(nil, Runtime{AlgorithmsDir: root}, opts, zap.NewNop())
			t.Cleanup(func() { supervisor.StopAll(2 * time.Second) })

			resolver := &dynamicBrokerResolver{socks: []string{"/tmp/zhulong_broker_5.sock"}}
			provider := &dynamicTaskConfigProvider{}
			provider.set(
				map[uint64]string{5: `{"regions":[{"id":"r1"}]}`},
				map[uint64]string{5: `{"threshold":0.45}`},
			)

			var sawSecondStartBeforeReconnectPush atomic.Bool
			broadcaster := &fakeBroadcaster{
				onPush: func(streamID uint64, _ uint16, _ []byte) {
					if streamID == 9 && countRecordedStarts(t, runFile) >= 2 {
						sawSecondStartBeforeReconnectPush.Store(true)
					}
				},
			}
			supervisor.SetBrokerSubscriptionResolver(resolver)
			supervisor.SetTaskConfig(provider, NewTaskConfigBroadcaster(broadcaster, zap.NewNop()))

			if err := supervisor.StartAlgorithm(context.Background(), algo); err != nil {
				t.Fatalf("StartAlgorithm: %v", err)
			}
			waitForStarts(t, runFile, 1)

			// Simulate camera drop & reconnect creating a fresh FrameBroker with stream ID 9.
			broadcaster.reset()
			wantRegion := `{"tripwires":[{"id":"gate","points":[[0,0.5],[1,0.5]]}]}`
			wantConfig := `{"threshold":0.62,"top_k":5}`
			resolver.set([]string{"/tmp/zhulong_broker_9.sock"}, nil)
			provider.set(
				map[uint64]string{9: wantRegion},
				map[uint64]string{9: wantConfig},
			)

			if err := os.WriteFile(triggerFile, []byte("go\n"), 0o644); err != nil {
				t.Fatalf("write trigger: %v", err)
			}
			waitForStarts(t, runFile, 2)

			if sawSecondStartBeforeReconnectPush.Load() {
				t.Fatal("task configs/regions for stream 9 were pushed after the respawned process had already started")
			}

			records := broadcaster.snapshot()
			var gotRegion, gotConfig string
			for _, rec := range records {
				if rec.streamID != 9 {
					t.Fatalf("unexpected push to stale stream %d during respawn", rec.streamID)
				}
				body := string(rec.payload[algoipc.TaskConfigHeaderSize:])
				switch rec.msgType {
				case algoipc.MsgTaskRegions:
					gotRegion = body
				case algoipc.MsgTaskConfig:
					gotConfig = body
				}
			}
			if gotRegion != wantRegion {
				t.Fatalf("respawn MSG_TASK_REGIONS = %q, want %q (records=%+v)", gotRegion, wantRegion, records)
			}
			if gotConfig != wantConfig {
				t.Fatalf("respawn MSG_TASK_CONFIG = %q, want %q (records=%+v)", gotConfig, wantConfig, records)
			}
		})
	}
}

// TestWatcherRespawnSkipsStaleTaskConfigPushWhenBrokerRefreshFails ensures that
// when broker re-resolution fails (camera still offline, falling back to cached
// stale sockets), respawn does not push task configs to the stale broker, and
// pushes cleanly as soon as a subsequent refresh succeeds with the recovered stream.
func TestWatcherRespawnSkipsStaleTaskConfigPushWhenBrokerRefreshFails(t *testing.T) {
	const algoID = "face_recognition"
	algo := PackageMeta{ID: algoID, Name: algoID, Version: "1.0.0"}

	tmpDir := t.TempDir()
	runFile := filepath.Join(tmpDir, "starts")
	trigger1 := filepath.Join(tmpDir, "trigger1")
	trigger2 := filepath.Join(tmpDir, "trigger2")
	t.Setenv("STALE_RUN_FILE", runFile)
	t.Setenv("STALE_TRIGGER_1", trigger1)
	t.Setenv("STALE_TRIGGER_2", trigger2)

	script := "#!/bin/sh\n" +
		"trap 'exit 0' TERM INT\n" +
		"printf 'run\\n' >> \"$STALE_RUN_FILE\"\n" +
		"count=$(wc -l < \"$STALE_RUN_FILE\")\n" +
		"if [ \"$count\" -eq 1 ]; then\n" +
		"  while [ ! -f \"$STALE_TRIGGER_1\" ]; do sleep 0.01; done\n" +
		"  printf '{\"status\":\"failed\",\"stage\":\"connect\"}\\n' >&2\n" +
		"  exit 5\n" +
		"fi\n" +
		"if [ \"$count\" -eq 2 ]; then\n" +
		"  while [ ! -f \"$STALE_TRIGGER_2\" ]; do sleep 0.01; done\n" +
		"  printf '{\"status\":\"failed\",\"stage\":\"connect\"}\\n' >&2\n" +
		"  exit 5\n" +
		"fi\n" +
		"while true; do sleep 0.05; done\n"

	root := t.TempDir()
	installDependencyStubPackage(t, root, algo, script)

	opts := DefaultRecoveryOptions()
	opts.InitialBackoff = 5 * time.Millisecond
	opts.MaxBackoff = 10 * time.Millisecond
	supervisor := NewSupervisor(nil, Runtime{AlgorithmsDir: root}, opts, zap.NewNop())
	t.Cleanup(func() { supervisor.StopAll(2 * time.Second) })

	resolver := &dynamicBrokerResolver{socks: []string{"/tmp/zhulong_broker_5.sock"}}
	provider := &dynamicTaskConfigProvider{}
	provider.set(
		map[uint64]string{5: `{"regions":[{"id":"old"}]}`},
		map[uint64]string{5: `{"threshold":0.4}`},
	)
	broadcaster := &fakeBroadcaster{}
	supervisor.SetBrokerSubscriptionResolver(resolver)
	supervisor.SetTaskConfig(provider, NewTaskConfigBroadcaster(broadcaster, zap.NewNop()))

	if err := supervisor.StartAlgorithm(context.Background(), algo); err != nil {
		t.Fatalf("StartAlgorithm: %v", err)
	}
	waitForStarts(t, runFile, 1)

	// Camera is offline: refresh fails and falls back to cached socket.
	broadcaster.reset()
	resolver.set(nil, errors.New("camera offline"))
	if err := os.WriteFile(trigger1, []byte("go\n"), 0o644); err != nil {
		t.Fatalf("write trigger1: %v", err)
	}
	waitForStarts(t, runFile, 2)

	if got := broadcaster.snapshot(); len(got) != 0 {
		t.Fatalf("expected no task config push when broker refresh failed, got %+v", got)
	}

	// Camera recovers with stream ID 12: next respawn refreshes and re-seeds both messages.
	resolver.set([]string{"/tmp/zhulong_broker_12.sock"}, nil)
	provider.set(
		map[uint64]string{12: `{"regions":[{"id":"recovered"}]}`},
		map[uint64]string{12: `{"threshold":0.71}`},
	)
	if err := os.WriteFile(trigger2, []byte("go\n"), 0o644); err != nil {
		t.Fatalf("write trigger2: %v", err)
	}
	waitForStarts(t, runFile, 3)

	records := broadcaster.snapshot()
	var sawRegion, sawConfig bool
	for _, rec := range records {
		if rec.streamID != 12 {
			t.Fatalf("unexpected stream %d after recovery", rec.streamID)
		}
		if rec.msgType == algoipc.MsgTaskRegions {
			sawRegion = true
		}
		if rec.msgType == algoipc.MsgTaskConfig {
			sawConfig = true
		}
	}
	if !sawRegion || !sawConfig {
		t.Fatalf("expected both MSG_TASK_REGIONS and MSG_TASK_CONFIG for recovered stream 12, got %+v", records)
	}
}

type blockingTaskRegionProvider struct {
	mu            sync.Mutex
	blockOnSecond bool
	calls         int
	entered       chan struct{}
}

func (p *blockingTaskRegionProvider) TaskRegionsFor(ctx context.Context, _ string) (map[uint64]string, error) {
	p.mu.Lock()
	p.calls++
	shouldBlock := p.blockOnSecond && p.calls >= 2
	p.mu.Unlock()
	if !shouldBlock {
		return map[uint64]string{1: `{}`}, nil
	}
	select {
	case p.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (p *blockingTaskRegionProvider) TaskConfigsFor(_ context.Context, _ string) (map[uint64]string, error) {
	return map[uint64]string{1: `{}`}, nil
}

// TestWatcherRespawnCancelsTaskConfigPushOnStop guarantees StopAlgorithm does not
// stall for taskConfigPushTimeout (5s) when a stop arrives mid-push during respawn,
// and does not spawn a replacement process after cancellation.
func TestWatcherRespawnCancelsTaskConfigPushOnStop(t *testing.T) {
	const algoID = "stop_mid_push"
	algo := PackageMeta{ID: algoID, Name: algoID, Version: "1.0.0"}

	tmpDir := t.TempDir()
	runFile := filepath.Join(tmpDir, "starts")
	triggerFile := filepath.Join(tmpDir, "trigger")
	t.Setenv("STOP_PUSH_RUN_FILE", runFile)
	t.Setenv("STOP_PUSH_TRIGGER_FILE", triggerFile)

	script := "#!/bin/sh\n" +
		"trap 'exit 0' TERM INT\n" +
		"printf 'run\\n' >> \"$STOP_PUSH_RUN_FILE\"\n" +
		"while [ ! -f \"$STOP_PUSH_TRIGGER_FILE\" ]; do sleep 0.01; done\n" +
		"exit 1\n"

	root := t.TempDir()
	installDependencyStubPackage(t, root, algo, script)

	opts := DefaultRecoveryOptions()
	opts.InitialBackoff = 5 * time.Millisecond
	opts.MaxBackoff = 10 * time.Millisecond
	supervisor := NewSupervisor(nil, Runtime{AlgorithmsDir: root}, opts, zap.NewNop())
	t.Cleanup(func() { supervisor.StopAll(2 * time.Second) })

	provider := &blockingTaskRegionProvider{
		blockOnSecond: true,
		entered:       make(chan struct{}, 1),
	}
	supervisor.SetTaskConfig(provider, NewTaskConfigBroadcaster(&fakeBroadcaster{}, zap.NewNop()))

	if err := supervisor.StartAlgorithm(context.Background(), algo); err != nil {
		t.Fatalf("StartAlgorithm: %v", err)
	}
	waitForStarts(t, runFile, 1)

	if err := os.WriteFile(triggerFile, []byte("crash\n"), 0o644); err != nil {
		t.Fatalf("write trigger: %v", err)
	}

	select {
	case <-provider.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("respawn never entered pushTaskConfigs")
	}

	started := time.Now()
	if err := supervisor.StopAlgorithm(context.Background(), algoID); err != nil {
		t.Fatalf("StopAlgorithm: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("StopAlgorithm took %v; task config push was not cancelled promptly", elapsed)
	}
	if got := countRecordedStarts(t, runFile); got != 1 {
		t.Fatalf("expected no respawn after cancellation, got %d starts", got)
	}
}
