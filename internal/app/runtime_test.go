package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"slices"
	"strings"
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

type storageLifecycleStub struct {
	events   *eventLog
	startErr error
}

func (s storageLifecycleStub) Start(context.Context) error {
	s.events.add("storage.start")
	return s.startErr
}

func (s storageLifecycleStub) Stop(context.Context) error {
	s.events.add("storage.stop")
	return nil
}

type recordContextStub struct {
	events  *eventLog
	ctxDone <-chan struct{}
}

func (r *recordContextStub) Start(ctx context.Context) error {
	r.ctxDone = ctx.Done()
	r.events.add("record.start")
	return nil
}

func (r *recordContextStub) Stop(context.Context) error {
	select {
	case <-r.ctxDone:
		r.events.add("record.context.cancelled")
	default:
		r.events.add("record.context.active")
	}
	r.events.add("record.stop")
	return nil
}

type snapshotLifecycleStub struct {
	events *eventLog
}

func (s snapshotLifecycleStub) Start() { s.events.add("snapshot.start") }
func (s snapshotLifecycleStub) Stop()  { s.events.add("snapshot.stop") }

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

// personLifecycleStub records person-domain start/stop so the shared shutdown
// order test can tell it apart from the audit domain.
type personLifecycleStub struct {
	events *eventLog
}

func (p personLifecycleStub) Start(context.Context) error {
	p.events.add("person.start")
	return nil
}

func (p personLifecycleStub) Stop(context.Context) error {
	p.events.add("person.stop")
	return nil
}

// asyncJobLifecycleStub records the background job worker. Handlers call domain
// services, so the worker must start after person and stop before person, the
// native engine and SQLite.
type asyncJobLifecycleStub struct {
	events *eventLog
}

func (a asyncJobLifecycleStub) Start(context.Context) error {
	a.events.add("asyncjobs.start")
	return nil
}

func (a asyncJobLifecycleStub) Stop(context.Context) error {
	a.events.add("asyncjobs.stop")
	return nil
}

// spatialLifecycleStub records spatial-domain start/stop: the maps directory is
// created there rather than in a constructor, so both lifecycle paths must reach it.
type spatialLifecycleStub struct {
	events *eventLog
}

func (s spatialLifecycleStub) Start(context.Context) error {
	s.events.add("spatial.start")
	return nil
}

func (s spatialLifecycleStub) Stop(context.Context) error {
	s.events.add("spatial.stop")
	return nil
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
		person:         personLifecycleStub{events: events},
		asyncJobs:      asyncJobLifecycleStub{events: events},
		server:         &http.Server{Addr: "127.0.0.1:0"},
		logger:         zap.NewNop(),
		databaseReady:  true,
		nativeReady:    true,
		cameraReady:    true,
		algorithmReady: true,
		personReady:    true,
		asyncJobsReady: true,
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
	want := []string{
		"http.shutdown",
		"camera.stop",
		"asyncjobs.stop",
		"algorithm.stop",
		"person.stop",
		"engine.close",
		"database.close",
	}
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
	// 后台任务执行体调用 person 与 native，必须在两者释放之前静默下来。
	if index("asyncjobs.stop") > index("person.stop") {
		t.Fatal("async jobs must stop before person: a running handler still calls person services")
	}
	if index("asyncjobs.stop") > index("engine.close") {
		t.Fatal("async jobs must stop before engine.close: a running handler may still use the native engine")
	}
}

// TestStartupRollbackSharesShutdownOrder 守护「只有一份停机顺序」。
//
// Start 失败回滚与正常 Stop 必须走同一条序列：它们曾经是两份手写清单，一次 listen
// 失败会把 algorithm 排在 camera 之前释放，而正常停机的顺序恰好相反。这里对同一组已
// 就绪的域分别跑两条路径，要求事件轨迹除去 HTTP 排空外逐项相等。
func TestStartupRollbackSharesShutdownOrder(t *testing.T) {
	build := func(events *eventLog) *lifecycleRuntime {
		return &lifecycleRuntime{
			database:       databaseStub{events: events},
			native:         engineStub{events: events},
			camera:         cameraStub{events: events},
			algorithm:      algorithmStub{events: events},
			audit:          auditStub{events: events},
			storage:        storageLifecycleStub{events: events},
			person:         personLifecycleStub{events: events},
			asyncJobs:      asyncJobLifecycleStub{events: events},
			record:         &recordContextStub{events: events},
			snapshot:       snapshotLifecycleStub{events: events},
			spatial:        spatialLifecycleStub{events: events},
			logger:         zap.NewNop(),
			databaseReady:  true,
			nativeReady:    true,
			cameraReady:    true,
			algorithmReady: true,
			auditReady:     true,
			storageReady:   true,
			personReady:    true,
			asyncJobsReady: true,
			recordReady:    true,
			snapshotReady:  true,
			spatialReady:   true,
		}
	}

	rollbackEvents := &eventLog{}
	runtime := build(rollbackEvents)
	if err := errors.Join(runtime.rollback(context.Background())...); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	stopEvents := &eventLog{}
	serveDone := make(chan error, 1)
	runtime = build(stopEvents)
	runtime.server = &http.Server{Addr: "127.0.0.1:0"}
	runtime.httpReady = true
	runtime.serveDone = serveDone
	runtime.shutdown = func(context.Context) error {
		stopEvents.add("http.shutdown")
		serveDone <- http.ErrServerClosed
		return nil
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	want, got := rollbackEvents.snapshot(), stopEvents.snapshot()
	if len(got) == 0 || got[0] != "http.shutdown" {
		t.Fatalf("shutdown must drain HTTP first: %v", got)
	}
	got = got[1:]
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rollback order diverged from shutdown order:\n rollback: %v\n shutdown: %v", want, got)
	}
}

// TestAsyncJobWorkerBracketsPerson 守护后台任务 worker 的启动/停止边界：
// handler 会调用 person 服务与 native 引擎，因此 worker 必须在 person 之后启动、
// 在 person 与 engine 之前停止。改坏这条边界会在真实板端变成 use-after-close。
func TestAsyncJobWorkerBracketsPerson(t *testing.T) {
	events := &eventLog{}
	server := &http.Server{Addr: "127.0.0.1:0"}
	runtime := &lifecycleRuntime{
		database:  databaseStub{events: events},
		native:    engineStub{events: events},
		person:    personLifecycleStub{events: events},
		asyncJobs: asyncJobLifecycleStub{events: events},
		server:    server,
		logger:    zap.NewNop(),
		listen: func(network, address string) (net.Listener, error) {
			events.add("http.listen")
			return net.Listen(network, address)
		},
		shutdown: server.Shutdown,
	}

	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	order := events.snapshot()
	index := func(event string) int {
		for i, got := range order {
			if got == event {
				return i
			}
		}
		t.Fatalf("event %q missing from lifecycle order %v", event, order)
		return -1
	}
	if index("person.start") > index("asyncjobs.start") {
		t.Fatal("async jobs must start after person: handlers call person services")
	}
	if index("asyncjobs.stop") > index("person.stop") {
		t.Fatal("async jobs must stop before person")
	}
	if index("asyncjobs.stop") > index("engine.close") {
		t.Fatal("async jobs must stop before the native engine is destroyed")
	}
}

// TestDegradedSubsystemStartIsRecordedAndStillStopped 守护降级启动语义：
// 可选域的 Start 失败既不能让整机启不了机，也不能被悄悄吞掉——它要进退化清单，
// 并且仍被标记 ready，因为只有该域自己的 Stop 能释放它申请了一半的资源。
func TestDegradedSubsystemStartIsRecordedAndStillStopped(t *testing.T) {
	events := &eventLog{}
	storageErr := errors.New("media root is not mounted")
	server := &http.Server{Addr: "127.0.0.1:0"}
	runtime := &lifecycleRuntime{
		database: databaseStub{events: events},
		native:   engineStub{events: events},
		storage:  storageLifecycleStub{events: events, startErr: storageErr},
		server:   server,
		logger:   zap.NewNop(),
		listen: func(network, address string) (net.Listener, error) {
			events.add("http.listen")
			return net.Listen(network, address)
		},
		shutdown: server.Shutdown,
	}

	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("a degraded subsystem must not abort the boot: %v", err)
	}
	if got, want := runtime.degraded, []string{"storage"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("degraded subsystems = %v, want %v", got, want)
	}
	if !runtime.storageReady {
		t.Fatal("a degraded subsystem must stay ready so Stop releases what it acquired")
	}

	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	want := []string{
		"database.open",
		"storage.start",
		"engine.start",
		"http.listen",
		"storage.stop",
		"engine.close",
		"database.close",
	}
	if got := events.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected degraded lifecycle: got %v, want %v", got, want)
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

func TestListenerFailureStopsRecordAndSnapshotWorkers(t *testing.T) {
	events := &eventLog{}
	record := &recordContextStub{events: events}
	runtime := &lifecycleRuntime{
		database: databaseStub{events: events},
		native:   engineStub{events: events},
		storage:  storageLifecycleStub{events: events},
		snapshot: snapshotLifecycleStub{events: events},
		record:   record,
		server:   &http.Server{Addr: "127.0.0.1:0"},
		logger:   zap.NewNop(),
		listen: func(string, string) (net.Listener, error) {
			events.add("http.listen")
			return nil, errors.New("address unavailable")
		},
	}

	if err := runtime.Start(context.Background()); err == nil {
		t.Fatal("expected listener startup error")
	}

	got, want := events.snapshot(), []string{
		"database.open",
		"storage.start",
		"snapshot.start",
		"record.start",
		"engine.start",
		"http.listen",
		"record.context.cancelled",
		"record.stop",
		"snapshot.stop",
		"storage.stop",
		"engine.close",
		"database.close",
	}
	if !reflect.DeepEqual(got, want) {
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
	healthErr  error
	lost       uint64
}

func (s *lifecycleLogSinkStub) Prepare() error {
	s.events.add("log.prepare")
	return s.prepareErr
}

func (s *lifecycleLogSinkStub) Health() (error, uint64)        { return s.healthErr, s.lost }
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

// TestLogSinkDegradationIsReportedAndDoesNotAbortStartup covers the policy split
// between configuration errors (fatal, see TestLogSinkPrepareFailureStopsStartup)
// and an unusable destination (degraded, host keeps recording).
func TestLogSinkDegradationIsReportedAndDoesNotAbortStartup(t *testing.T) {
	events := &eventLog{}
	server := &http.Server{Addr: "127.0.0.1:0"}
	sinkErr := errors.New("read-only file system")
	lifecycle := &lifecycleRuntime{
		database: databaseStub{events: events},
		native:   engineStub{events: events},
		server:   server,
		shutdown: server.Shutdown,
		logger:   zap.NewNop(),
		logSink:  &lifecycleLogSinkStub{events: events, healthErr: sinkErr},
		listen: func(network, address string) (net.Listener, error) {
			return net.Listen(network, address)
		},
	}
	if err := lifecycle.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !containsString(lifecycle.degraded, "logging") {
		t.Fatalf("degraded = %v, want logging reported", lifecycle.degraded)
	}
	if err := lifecycle.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// lockedBuffer is a zapcore.WriteSyncer whose contents other goroutines can read.
type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *lockedBuffer) Sync() error { return nil }

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

// TestStorageWriteStatePressureIsAppliedAndReleasedOnce proves the storage gate
// reaches the logger as an edge, and that the resulting reduction actually filters
// records instead of only announcing itself.
func TestStorageWriteStatePressureIsAppliedAndReleasedOnce(t *testing.T) {
	var logs lockedBuffer
	level := zap.NewAtomicLevelAt(zapcore.DebugLevel)
	pressure := newLogPressure(level)
	logger := zap.New(zapcore.NewCore(
		zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig()),
		zapcore.AddSync(&logs),
		pressure,
	))
	lifecycle := &lifecycleRuntime{
		logger:      logger.Named("host"),
		logLevel:    level,
		logPressure: pressure,
	}

	lifecycle.applyStorageWriteState(true)
	lifecycle.applyStorageWriteState(true)
	if !pressure.UnderPressure() {
		t.Fatal("storage emergency did not reduce log volume")
	}
	logger.Debug("debug-during-emergency")
	logger.Info("info-during-emergency")
	logger.Warn("warn-during-emergency")
	if got := strings.Count(logs.String(), "run log volume reduced"); got != 1 {
		t.Fatalf("reduction reported %d times, want 1: %s", got, logs.String())
	}
	if strings.Contains(logs.String(), "debug-during-emergency") || strings.Contains(logs.String(), "info-during-emergency") {
		t.Fatalf("debug/info survived an emergency stop: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "warn-during-emergency") {
		t.Fatalf("warn was filtered during an emergency stop: %s", logs.String())
	}

	lifecycle.applyStorageWriteState(false)
	lifecycle.applyStorageWriteState(false)
	if pressure.UnderPressure() {
		t.Fatal("storage recovery did not restore log volume")
	}
	if got := strings.Count(logs.String(), "run log volume restored"); got != 1 {
		t.Fatalf("restore reported %d times, want 1: %s", got, logs.String())
	}
	logger.Debug("debug-after-restore")
	if !strings.Contains(logs.String(), "debug-after-restore") {
		t.Fatalf("debug did not return after recovery: %s", logs.String())
	}
}

// gb28181Stub records the GB28181 subsystem's position in the startup and
// shutdown sequences.
type gb28181Stub struct {
	events   *eventLog
	startErr error
	stopErr  error
}

func (g gb28181Stub) Start(context.Context) error {
	g.events.add("gb28181.start")
	return g.startErr
}

func (g gb28181Stub) Stop(context.Context) error {
	g.events.add("gb28181.stop")
	return g.stopErr
}

// TestGB28181StopsBeforeCamera pins the teardown contract: SIP ingress and media
// leases must be released while camera and the native engine are still alive to
// serve the teardown. Reversing this order leaks ports and can free native state
// while a dialog still references it.
func TestGB28181StopsBeforeCamera(t *testing.T) {
	events := &eventLog{}
	serveDone := make(chan error, 1)
	runtime := &lifecycleRuntime{
		database:      databaseStub{events: events},
		native:        engineStub{events: events},
		camera:        cameraStub{events: events},
		gb28181:       gb28181Stub{events: events},
		server:        &http.Server{Addr: "127.0.0.1:0"},
		logger:        zap.NewNop(),
		databaseReady: true,
		nativeReady:   true,
		cameraReady:   true,
		gb28181Ready:  true,
		httpReady:     true,
		serveDone:     serveDone,
		shutdown: func(context.Context) error {
			events.add("http.shutdown")
			serveDone <- http.ErrServerClosed
			return nil
		},
	}

	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("unexpected stop error: %v", err)
	}
	want := []string{"http.shutdown", "gb28181.stop", "camera.stop", "engine.close", "database.close"}
	if got := events.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected shutdown order: got %v, want %v", got, want)
	}
}

// TestGB28181StartFailureIsDegradedNotFatal guards the operator's escape hatch: a
// bad stored config or a busy SIP port must not cost them the web console they
// need to fix it.
func TestGB28181StartFailureIsDegradedNotFatal(t *testing.T) {
	events := &eventLog{}
	server := &http.Server{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler()}
	runtime := &lifecycleRuntime{
		database: databaseStub{events: events},
		native:   engineStub{events: events},
		camera:   cameraStub{events: events},
		gb28181:  gb28181Stub{events: events, startErr: errors.New("sip port busy")},
		server:   server,
		logger:   zap.NewNop(),
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
		t.Fatalf("a GB28181 failure must not abort startup: %v", err)
	}
	if !slices.Contains(runtime.degraded, "gb28181") {
		t.Fatalf("expected gb28181 in the degraded list, got %v", runtime.degraded)
	}
	// stopSequence must still run it: a half-started subsystem owns resources.
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("unexpected stop error: %v", err)
	}
	if !slices.Contains(events.snapshot(), "gb28181.stop") {
		t.Fatalf("expected the degraded subsystem to be stopped, got %v", events.snapshot())
	}
}

func TestLifecycleRuntimeStartsAutoTLS(t *testing.T) {
	tempDir := t.TempDir()
	events := &eventLog{}
	server := &http.Server{
		Addr: "127.0.0.1:0",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
	}
	runtime := &lifecycleRuntime{
		database: databaseStub{events: events},
		native:   engineStub{events: events},
		server:   server,
		logger:   zap.NewNop(),
		config: Config{
			TLSMode: "auto",
			DataDir: tempDir,
		},
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
		t.Fatalf("runtime.Start with auto TLS failed: %v", err)
	}
	defer func() {
		_ = runtime.Stop(context.Background())
	}()

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: 3 * time.Second,
	}

	resp, err := client.Get(fmt.Sprintf("https://%s/", runtime.listener.Addr().String()))
	if err != nil {
		t.Fatalf("GET over HTTPS failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d", resp.StatusCode)
	}
}
