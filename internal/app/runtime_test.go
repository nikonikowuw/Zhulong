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

	"go.uber.org/zap"
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
