package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/nativelog"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *eventLog) add(event string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event)
}

func (l *eventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

type databaseStub struct {
	events  *eventLog
	openErr error
}

func (d databaseStub) OpenAndMigrate(context.Context) error {
	d.events.add("database.open")
	return d.openErr
}

func (d databaseStub) Close() error {
	d.events.add("database.close")
	return nil
}

type engineStub struct {
	events   *eventLog
	startErr error
}

func (e engineStub) Start() error {
	e.events.add("engine.start")
	return e.startErr
}

func (e engineStub) Close() error {
	e.events.add("engine.close")
	return nil
}

type lifecycleLogAwareEngineStub struct {
	events *eventLog
	queue  *nativelog.Queue
}

func (e *lifecycleLogAwareEngineStub) Start() error {
	e.events.add("engine.start")
	return nil
}

func (e *lifecycleLogAwareEngineStub) Stop() error {
	e.events.add("native.stop")
	return nil
}

func (e *lifecycleLogAwareEngineStub) Close() error {
	e.events.add("native.close")
	e.queue.TryEnqueue(nativelog.Record{
		Level:   nativelog.Info,
		Module:  "pipeline.engine",
		Message: "destroy_event",
		SeenAt:  time.Now(),
	})
	return nil
}

func (e *lifecycleLogAwareEngineStub) SetNativeLogSink(queue *nativelog.Queue) error {
	e.events.add("native.log.register")
	e.queue = queue
	return nil
}

func (e *lifecycleLogAwareEngineStub) CloseNativeLogSink() error {
	e.events.add("native.log.unregister")
	return nil
}

type lifecycleRecordingCore struct {
	events *eventLog
}

func (c *lifecycleRecordingCore) Enabled(zapcore.Level) bool        { return true }
func (c *lifecycleRecordingCore) With([]zapcore.Field) zapcore.Core { return c }
func (c *lifecycleRecordingCore) Check(entry zapcore.Entry, checked *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	return checked.AddCore(entry, c)
}
func (c *lifecycleRecordingCore) Write(entry zapcore.Entry, _ []zapcore.Field) error {
	if entry.LoggerName == "native.log" {
		c.events.add("native.log.record")
	}
	return nil
}
func (c *lifecycleRecordingCore) Sync() error {
	c.events.add("logger.sync")
	return nil
}

type cameraStub struct {
	events    *eventLog
	cipherErr error
	startErr  error
}

func (c cameraStub) InitCipher(context.Context) error {
	c.events.add("camera.cipher")
	return c.cipherErr
}

func (c cameraStub) Start(context.Context) error {
	c.events.add("camera.start")
	return c.startErr
}

func (c cameraStub) Stop() {
	c.events.add("camera.stop")
}

// algorithmStub records the algorithm supervisor stop in the shutdown sequence.
type algorithmStub struct {
	events   *eventLog
	startErr error
	stopErr  error
}

func (a algorithmStub) Start(context.Context) error {
	a.events.add("algorithm.start")
	return a.startErr
}

func (a algorithmStub) Stop(context.Context) error {
	a.events.add("algorithm.stop")
	return a.stopErr
}

// TestAIShutdownOrderPrecedesNativeClose 守护跨层停机顺序：
// camera（AI 消费者与 StreamHub）→ algorithm（runner 子进程）→ native（broker 宿主）。
// 该顺序是「broker 先于 native 停止」的载体（见 dependency-injection.md §2 与 design.md §3.1）：
// 顺序被改坏时必须在这里失败，而不是在真实板端产生 use-after-free。
func TestAIShutdownOrderPrecedesNativeClose(t *testing.T) {
	events := &eventLog{}
	serveDone := make(chan error, 1)
	runtime := &lifecycleRuntime{
		database:       databaseStub{events: events},
		native:         engineStub{events: events},
		camera:         cameraStub{events: events},
		algorithm:      algorithmStub{events: events},
		server:         &http.Server{Addr: "127.0.0.1:0"},
		logger:         zap.NewNop(),
		databaseReady:  true,
		nativeReady:    true,
		cameraReady:    true,
		algorithmReady: true,
		httpReady:      true,
		serveDone:      serveDone,
		shutdown: func(ctx context.Context) error {
			events.add("http.shutdown")
			serveDone <- http.ErrServerClosed
			return nil
		},
	}

	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("unexpected stop error: %v", err)
	}

	order := events.snapshot()
	want := []string{"http.shutdown", "camera.stop", "algorithm.stop", "engine.close", "database.close"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("unexpected AI shutdown order: got %v, want %v", order, want)
	}

	// 显式断言三条相对顺序，让失败信息直接指向被破坏的约束。
	index := func(event string) int {
		for i, got := range order {
			if got == event {
				return i
			}
		}
		t.Fatalf("event %q missing from shutdown order %v", event, order)
		return -1
	}
	if index("camera.stop") > index("algorithm.stop") {
		t.Fatal("camera must stop before algorithm: AI consumers must disappear before runner subprocesses")
	}
	if index("algorithm.stop") > index("engine.close") {
		t.Fatal("algorithm must stop before engine.close: runner clients must be drained before the broker host is destroyed")
	}
}

func TestMigrationFailurePreventsHTTPListener(t *testing.T) {
	events := &eventLog{}
	migrationErr := errors.New("migration rejected")
	server := &http.Server{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler()}
	runtime := &lifecycleRuntime{
		database: databaseStub{events: events, openErr: migrationErr},
		native:   engineStub{events: events},
		server:   server,
		logger:   zap.NewNop(),
		listen: func(string, string) (net.Listener, error) {
			events.add("http.listen")
			return nil, errors.New("listener must not be created")
		},
	}

	err := runtime.Start(context.Background())
	if !errors.Is(err, migrationErr) {
		t.Fatalf("expected migration error, got %v", err)
	}
	if runtime.listener != nil {
		t.Fatal("listener was created despite migration failure")
	}
	if got, want := events.snapshot(), []string{"database.open", "database.close"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected startup/rollback order: got %v, want %v", got, want)
	}
}

func TestShutdownDrainsHTTPBeforeClosingNativeAndDatabase(t *testing.T) {
	events := &eventLog{}
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	shutdownStarted := make(chan struct{})
	server := &http.Server{
		Addr: "127.0.0.1:0",
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			close(requestStarted)
			<-releaseRequest
			writer.WriteHeader(http.StatusNoContent)
		}),
	}
	runtime := &lifecycleRuntime{
		database: databaseStub{events: events},
		native:   engineStub{events: events},
		server:   server,
		logger:   zap.NewNop(),
		listen: func(network, address string) (net.Listener, error) {
			events.add("http.listen")
			return net.Listen(network, address)
		},
		shutdown: func(ctx context.Context) error {
			events.add("http.shutdown")
			close(shutdownStarted)
			return server.Shutdown(ctx)
		},
	}

	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	responseDone := make(chan error, 1)
	go func() {
		response, err := http.Get(fmt.Sprintf("http://%s/", runtime.listener.Addr()))
		if err != nil {
			responseDone <- err
			return
		}
		responseDone <- response.Body.Close()
	}()
	select {
	case <-requestStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP request did not reach the server")
	}

	stopDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		stopDone <- runtime.Stop(ctx)
	}()
	select {
	case <-shutdownStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP shutdown was not started")
	}
	select {
	case err := <-stopDone:
		t.Fatalf("Stop returned before the active HTTP request drained: %v", err)
	default:
	}
	if got, want := events.snapshot(), []string{"database.open", "engine.start", "http.listen", "http.shutdown"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("resources closed before HTTP drain: got %v, want %v", got, want)
	}

	close(releaseRequest)
	select {
	case err := <-responseDone:
		if err != nil {
			t.Fatalf("HTTP response: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP request did not drain")
	}
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not finish")
	}

	if got, want := events.snapshot(), []string{"database.open", "engine.start", "http.listen", "http.shutdown", "engine.close", "database.close"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected shutdown order: got %v, want %v", got, want)
	}
}

func TestListenerFailureRollsBackNativeAndDatabase(t *testing.T) {
	events := &eventLog{}
	listenErr := errors.New("address unavailable")
	runtime := &lifecycleRuntime{
		database: databaseStub{events: events},
		native:   engineStub{events: events},
		server:   &http.Server{Addr: "127.0.0.1:0"},
		logger:   zap.NewNop(),
		listen: func(string, string) (net.Listener, error) {
			events.add("http.listen")
			return nil, listenErr
		},
	}

	if err := runtime.Start(context.Background()); !errors.Is(err, listenErr) {
		t.Fatalf("expected listener error, got %v", err)
	}
	if got, want := events.snapshot(), []string{"database.open", "engine.start", "http.listen", "engine.close", "database.close"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected startup rollback order: got %v, want %v", got, want)
	}
}

func TestCameraInitCipherFailureRollsBackNativeAndDatabase(t *testing.T) {
	events := &eventLog{}
	cipherErr := errors.New("key missing with encrypted streams")
	runtime := &lifecycleRuntime{
		database: databaseStub{events: events},
		native:   engineStub{events: events},
		camera:   cameraStub{events: events, cipherErr: cipherErr},
		server:   &http.Server{Addr: "127.0.0.1:0"},
		logger:   zap.NewNop(),
		listen: func(string, string) (net.Listener, error) {
			events.add("http.listen")
			return nil, errors.New("listener must not be called")
		},
	}

	err := runtime.Start(context.Background())
	if !errors.Is(err, cipherErr) {
		t.Fatalf("expected cipher error, got %v", err)
	}
	if runtime.listener != nil {
		t.Fatal("listener was created despite camera cipher failure")
	}
	if got, want := events.snapshot(), []string{"database.open", "engine.start", "camera.cipher", "engine.close", "database.close"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected startup/rollback order: got %v, want %v", got, want)
	}
}

func TestCameraShutdownOrderAfterHTTPDrain(t *testing.T) {
	events := &eventLog{}
	serveDone := make(chan error, 1)
	runtime := &lifecycleRuntime{
		database:      databaseStub{events: events},
		native:        engineStub{events: events},
		camera:        cameraStub{events: events},
		server:        &http.Server{Addr: "127.0.0.1:0"},
		logger:        zap.NewNop(),
		databaseReady: true,
		nativeReady:   true,
		cameraReady:   true,
		httpReady:     true,
		serveDone:     serveDone,
		shutdown: func(ctx context.Context) error {
			events.add("http.shutdown")
			serveDone <- http.ErrServerClosed
			return nil
		},
	}

	err := runtime.Stop(context.Background())
	if err != nil {
		t.Fatalf("unexpected stop error: %v", err)
	}
	if got, want := events.snapshot(), []string{"http.shutdown", "camera.stop", "engine.close", "database.close"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected shutdown order: got %v, want %v", got, want)
	}
}

type auditStub struct {
	events   *eventLog
	startErr error
	stopErr  error
}

func (a auditStub) Start(context.Context) error {
	a.events.add("audit.start")
	return a.startErr
}

func (a auditStub) Stop(context.Context) error {
	a.events.add("audit.stop")
	return a.stopErr
}

func TestAuditShutdownOrderAfterHTTPDrain(t *testing.T) {
	events := &eventLog{}
	serveDone := make(chan error, 1)
	runtime := &lifecycleRuntime{
		database:      databaseStub{events: events},
		native:        engineStub{events: events},
		camera:        cameraStub{events: events},
		audit:         auditStub{events: events},
		server:        &http.Server{Addr: "127.0.0.1:0"},
		logger:        zap.NewNop(),
		databaseReady: true,
		nativeReady:   true,
		cameraReady:   true,
		auditReady:    true,
		httpReady:     true,
		serveDone:     serveDone,
		shutdown: func(ctx context.Context) error {
			events.add("http.shutdown")
			serveDone <- http.ErrServerClosed
			return nil
		},
	}

	err := runtime.Stop(context.Background())
	if err != nil {
		t.Fatalf("unexpected stop error: %v", err)
	}
	if got, want := events.snapshot(), []string{"http.shutdown", "camera.stop", "audit.stop", "engine.close", "database.close"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected shutdown order with audit: got %v, want %v", got, want)
	}
}

func TestNativeLogLifecycleDrainsAfterDestroyAndBeforeLoggerSync(t *testing.T) {
	events := &eventLog{}
	core := &lifecycleRecordingCore{events: events}
	logger := zap.New(core)
	queue, err := nativelog.NewQueue(16, logger)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Addr: "127.0.0.1:0"}
	runtime := &lifecycleRuntime{
		database:  databaseStub{events: events},
		native:    &lifecycleLogAwareEngineStub{events: events},
		nativeLog: queue,
		algorithm: algorithmStub{events: events},
		server:    server,
		logger:    logger,
		listen: func(network, address string) (net.Listener, error) {
			events.add("http.listen")
			return net.Listen(network, address)
		},
		shutdown: func(ctx context.Context) error {
			events.add("http.shutdown")
			return server.Shutdown(ctx)
		},
	}

	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := queue.Pending(); got != 0 {
		t.Fatalf("native log queue pending after Stop = %d, want 0", got)
	}

	order := events.snapshot()
	index := func(event string) int {
		t.Helper()
		for i, got := range order {
			if got == event {
				return i
			}
		}
		t.Fatalf("event %q missing from lifecycle trace %v", event, order)
		return -1
	}
	for _, pair := range [][2]string{
		{"native.log.register", "engine.start"},
		{"http.shutdown", "algorithm.stop"},
		{"algorithm.stop", "native.stop"},
		{"native.stop", "native.close"},
		{"native.close", "native.log.unregister"},
		{"native.close", "native.log.record"},
		{"native.log.record", "logger.sync"},
	} {
		if index(pair[0]) >= index(pair[1]) {
			t.Fatalf("lifecycle order violated: %s must precede %s: %v", pair[0], pair[1], order)
		}
	}
}

type lifecycleLogSinkStub struct {
	events     *eventLog
	prepareErr error
	closeErr   error
}

func (s *lifecycleLogSinkStub) Prepare() error {
	s.events.add("log.prepare")
	return s.prepareErr
}

func (s *lifecycleLogSinkStub) Health() (error, uint64)        { return nil, 0 }
func (s *lifecycleLogSinkStub) Write(data []byte) (int, error) { return len(data), nil }
func (s *lifecycleLogSinkStub) Sync() error                    { return nil }

func (s *lifecycleLogSinkStub) Close() error {
	s.events.add("log.close")
	return s.closeErr
}

func TestLogSinkPreparedBeforeServicesAndClosedAfterShutdown(t *testing.T) {
	events := &eventLog{}
	server := &http.Server{Addr: "127.0.0.1:0"}
	runtime := &lifecycleRuntime{
		database: databaseStub{events: events},
		native:   engineStub{events: events},
		server:   server,
		shutdown: server.Shutdown,
		logger:   zap.NewNop(),
		logSink:  &lifecycleLogSinkStub{events: events},
		listen: func(network, address string) (net.Listener, error) {
			events.add("http.listen")
			return net.Listen(network, address)
		},
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	want := []string{"log.prepare", "database.open", "engine.start", "http.listen", "engine.close", "database.close", "log.close"}
	if got := events.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected log sink lifecycle: got %v, want %v", got, want)
	}
}

func TestLogSinkPrepareFailureStopsStartupAndClosesSink(t *testing.T) {
	events := &eventLog{}
	prepareErr := errors.New("log path unavailable")
	runtime := &lifecycleRuntime{
		database: databaseStub{events: events},
		native:   engineStub{events: events},
		server:   &http.Server{Addr: "127.0.0.1:0"},
		logger:   zap.NewNop(),
		logSink:  &lifecycleLogSinkStub{events: events, prepareErr: prepareErr},
	}
	if err := runtime.Start(context.Background()); !errors.Is(err, prepareErr) {
		t.Fatalf("expected log sink startup error, got %v", err)
	}
	if got, want := events.snapshot(), []string{"log.prepare", "log.close"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("services started or sink was not closed after prepare failure: got %v, want %v", got, want)
	}
	if err := runtime.Start(context.Background()); err == nil || err.Error() != "application is already started" {
		t.Fatalf("second Start after failed preparation = %v, want already-started error", err)
	}
	if got, want := events.snapshot(), []string{"log.prepare", "log.close"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("retry after failed preparation restarted services: got %v, want %v", got, want)
	}
}
