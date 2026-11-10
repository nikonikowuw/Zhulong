package algorunner

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// newInternalTestSupervisor builds a supervisor without a store or install root:
// the white-box tests below only exercise the watcher registry.
func newInternalTestSupervisor(store LifecycleStore) *Supervisor {
	return NewSupervisor(
		store,
		Runtime{},
		DefaultRecoveryOptions(),
		zap.NewNop(),
	)
}

// stubLifecycleStore is a minimal in-memory LifecycleStore for white-box tests.
type stubLifecycleStore struct {
	mu       sync.Mutex
	statuses map[string]string
	writes   []string
}

func newStubLifecycleStore() *stubLifecycleStore {
	return &stubLifecycleStore{statuses: make(map[string]string)}
}

func (s *stubLifecycleStore) GetAlgorithmPackage(ctx context.Context, id string) (PackageMeta, error) {
	return PackageMeta{ID: id}, nil
}

func (s *stubLifecycleStore) UpdateAlgorithmStatus(ctx context.Context, id string, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statuses[id] = status
	s.writes = append(s.writes, status)
	return nil
}

func (s *stubLifecycleStore) RollbackAlgorithmVersion(
	ctx context.Context,
	id string,
	targetVersion string,
	failedVersion string,
) error {
	return nil
}

func (s *stubLifecycleStore) statusOf(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.statuses[id]
	return st, ok
}

// TestRetireWatcherReportsWhetherItRemoved pins the contract that makes the
// status-write guard sound: retireWatcher reports whether it actually removed
// the caller's watcher, and never disturbs a replacement watcher.
func TestRetireWatcherReportsWhetherItRemoved(t *testing.T) {
	supervisor := newInternalTestSupervisor(nil)

	const algoID = "algo_retire"
	w1 := &workerWatcher{supervisor: supervisor, algo: PackageMeta{ID: algoID}}
	supervisor.watchers[algoID] = w1

	if !supervisor.retireWatcher(algoID, w1) {
		t.Fatal("retireWatcher must report true when it owns the slot")
	}
	if _, exists := supervisor.watchers[algoID]; exists {
		t.Fatal("retireWatcher must remove the owned watcher")
	}

	w2 := &workerWatcher{supervisor: supervisor, algo: PackageMeta{ID: algoID}}
	supervisor.watchers[algoID] = w2

	if supervisor.retireWatcher(algoID, w1) {
		t.Fatal("retireWatcher must report false when a different watcher owns the slot")
	}
	if supervisor.watchers[algoID] != w2 {
		t.Fatal("retireWatcher must leave the replacement watcher registered")
	}
}

// TestStopAlgorithmDoesNotDisableAReplacedWatcher reproduces R1: StopAlgorithm
// captures its watcher, and while it is stopping that watcher a rollback/upgrade
// re-registers a different, live watcher under the same ID. The stop call must
// not persist DISABLED, because the running process is now owned by the new
// watcher (persisting DISABLED would leave the DB saying "stopped" while the
// process runs).
func TestStopAlgorithmDoesNotDisableAReplacedWatcher(t *testing.T) {
	store := newStubLifecycleStore()
	supervisor := newInternalTestSupervisor(store)

	const algoID = "algo_replaced"
	w1 := &workerWatcher{
		supervisor: supervisor,
		algo:       PackageMeta{ID: algoID},
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
		status:     StatusRunning,
	}
	supervisor.watchers[algoID] = w1

	stopErr := make(chan error, 1)
	go func() { stopErr <- supervisor.StopAlgorithm(context.Background(), algoID) }()

	// Wait until StopAlgorithm has captured w1 and entered stopProcess, then
	// swap in a replacement watcher before letting the stop proceed. Waiting on
	// stopCh is deterministic: requestStop closes it only after the initial map
	// lookup has already happened.
	select {
	case <-w1.stopCh:
	case <-time.After(2 * time.Second):
		t.Fatal("StopAlgorithm never reached stopProcess")
	}

	w2 := &workerWatcher{
		supervisor: supervisor,
		algo:       PackageMeta{ID: algoID},
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
		status:     StatusRunning,
	}
	supervisor.mu.Lock()
	supervisor.watchers[algoID] = w2
	supervisor.mu.Unlock()

	close(w1.doneCh)
	if err := <-stopErr; err != nil {
		t.Fatalf("StopAlgorithm returned error: %v", err)
	}

	if got, ok := store.statusOf(algoID); ok {
		t.Fatalf("StopAlgorithm must not write a status when it did not retire its own watcher; wrote %q", got)
	}
	if supervisor.watchers[algoID] != w2 {
		t.Fatalf("the live replacement watcher must remain registered")
	}
}

// TestStopAlgorithmDisablesWhenItRetiresItsOwnWatcher is the positive-path
// counterpart to TestStopAlgorithmDoesNotDisableAReplacedWatcher: when
// StopAlgorithm really does remove its own watcher, it MUST persist DISABLED
// (AC2). Without this, a regression that made retireWatcher always report false
// would silently stop persisting the stop, and the negative-path test alone
// would still pass.
func TestStopAlgorithmDisablesWhenItRetiresItsOwnWatcher(t *testing.T) {
	store := newStubLifecycleStore()
	supervisor := newInternalTestSupervisor(store)

	const algoID = "algo_owned_stop"
	w := &workerWatcher{
		supervisor: supervisor,
		algo:       PackageMeta{ID: algoID},
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
		status:     StatusRunning,
	}
	supervisor.watchers[algoID] = w

	stopErr := make(chan error, 1)
	go func() { stopErr <- supervisor.StopAlgorithm(context.Background(), algoID) }()

	// Wait until StopAlgorithm has captured w and entered stopProcess (which
	// closes stopCh), then let the watcher exit.
	select {
	case <-w.stopCh:
	case <-time.After(2 * time.Second):
		t.Fatal("StopAlgorithm never reached stopProcess")
	}
	close(w.doneCh)
	if err := <-stopErr; err != nil {
		t.Fatalf("StopAlgorithm returned error: %v", err)
	}

	if got, ok := store.statusOf(algoID); !ok || got != StatusDisabled {
		t.Fatalf("StopAlgorithm must persist DISABLED when it retires its own watcher; status=%q present=%v", got, ok)
	}
	if _, exists := supervisor.watchers[algoID]; exists {
		t.Fatalf("StopAlgorithm must remove the watcher it retired")
	}
}

// TestRestartAlgorithmWithoutStoreReturnsSentinel pins the nil-store guard: a
// supervisor built without a store cannot reload the package, and callers must be
// able to classify that outcome instead of matching message text.
func TestRestartAlgorithmWithoutStoreReturnsSentinel(t *testing.T) {
	supervisor := newInternalTestSupervisor(nil)

	err := supervisor.RestartAlgorithm(context.Background(), "algo_no_store")
	if !errors.Is(err, errNoLifecycleStore) {
		t.Fatalf("RestartAlgorithm without a store = %v, want errNoLifecycleStore", err)
	}
}

// TestStopAlgorithmWrapsContextCancellation pins the %w on the watcher wait: a
// caller whose context expired must be able to test the cause with errors.Is, and
// the message must still say which step was abandoned. The watcher's done channel
// is never closed, so the wait really does depend on the context.
func TestStopAlgorithmWrapsContextCancellation(t *testing.T) {
	supervisor := newInternalTestSupervisor(nil)

	const algoID = "algo_stop_wait"
	w := &workerWatcher{
		supervisor: supervisor,
		algo:       PackageMeta{ID: algoID},
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
		status:     StatusRunning,
	}
	supervisor.watchers[algoID] = w

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := supervisor.StopAlgorithm(ctx, algoID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("StopAlgorithm = %v, want a wrapped context.Canceled", err)
	}
	if !strings.Contains(err.Error(), "wait for algorithm") {
		t.Fatalf("StopAlgorithm error %q does not name the step it gave up on", err)
	}
	if _, exists := supervisor.watchers[algoID]; !exists {
		t.Fatal("a stop that never observed its watcher finish must not retire it")
	}
}

// TestStopAlgorithmReturnsSpawnedStopFailure pins the join of the complement-stop
// failure into StopAlgorithm's result.
//
// The complement stop in startProcess runs after w.stopProcess has already
// returned nil, so without this join StopAlgorithm reports a clean stop for a
// runner that survived SIGTERM and SIGKILL. The failure is injected directly
// because a runner that ignores SIGKILL cannot be produced without mocking the
// signal ladder; the propagation is what this test pins.
func TestStopAlgorithmReturnsSpawnedStopFailure(t *testing.T) {
	supervisor := newInternalTestSupervisor(nil)

	const algoID = "algo_spawned_stop_failure"
	w := &workerWatcher{
		supervisor: supervisor,
		algo:       PackageMeta{ID: algoID},
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
		status:     StatusRunning,
	}
	supervisor.watchers[algoID] = w

	stopErr := make(chan error, 1)
	go func() { stopErr <- supervisor.StopAlgorithm(context.Background(), algoID) }()

	// Wait until StopAlgorithm has captured w and entered stopProcess, i.e. until
	// its own stopErr is settled as nil.
	select {
	case <-w.stopCh:
	case <-time.After(2 * time.Second):
		t.Fatal("StopAlgorithm never reached stopProcess")
	}

	w.setSpawnedStopError(errProcessNotTerminated)
	close(w.doneCh)

	if err := <-stopErr; !errors.Is(err, errProcessNotTerminated) {
		t.Fatalf("StopAlgorithm = %v, want the complement-stop failure to surface", err)
	}
	if _, exists := supervisor.watchers[algoID]; exists {
		t.Fatal("StopAlgorithm must still retire the watcher it stopped")
	}
}
