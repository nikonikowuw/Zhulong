package algorunner_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algorunner"
)

// TestGetStatusNotBlockedByBrokerResolution reproduces R3: resolving broker
// socket paths performs DB/subscription IO and must not run while holding the
// supervisor write lock. While StartAlgorithm is stuck inside the resolver,
// GetStatus (which takes an RLock) must still return promptly.
func TestGetStatusNotBlockedByBrokerResolution(t *testing.T) {
	store := newMockLifecycleStore()
	algo := supervisorTestPackage()
	store.packages[algo.ID] = algo

	resolver := newBlockingBrokerPathResolver([]string{"/tmp/r3_broker.sock"})
	supervisor := newTestSupervisor(t, store, algo, algorunner.DefaultRecoveryOptions())
	supervisor.SetBrokerSubscriptionResolver(resolver)
	t.Cleanup(func() {
		resolver.unblock()
		supervisor.StopAll(time.Second)
	})

	startErr := make(chan error, 1)
	go func() { startErr <- supervisor.StartAlgorithm(context.Background(), algo) }()

	// Wait until StartAlgorithm is genuinely blocked inside the resolver.
	select {
	case <-resolver.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("StartAlgorithm never reached the broker resolver")
	}

	statusDone := make(chan string, 1)
	go func() { statusDone <- supervisor.GetStatus(algo.ID) }()

	select {
	case <-statusDone:
		// Good: GetStatus returned while resolution was still in flight.
	case <-time.After(200 * time.Millisecond):
		t.Fatal("GetStatus blocked while StartAlgorithm was resolving broker paths (R3: resolver IO must not run under s.mu)")
	}

	resolver.unblock()
	if err := <-startErr; err != nil {
		t.Fatalf("StartAlgorithm failed: %v", err)
	}
}

// TestConcurrentStartAlgorithmRegistersOnce verifies the second validation added
// by R3: when two StartAlgorithm calls race for the same ID (both pass the fast
// existence check, both resolve broker paths outside the lock), exactly one must
// win registration and the other must report "already running".
func TestConcurrentStartAlgorithmRegistersOnce(t *testing.T) {
	store := newMockLifecycleStore()
	algo := supervisorTestPackage()
	store.packages[algo.ID] = algo

	resolver := newBlockingBrokerPathResolver([]string{"/tmp/r3_race.sock"})
	supervisor := newTestSupervisor(t, store, algo, algorunner.DefaultRecoveryOptions())
	supervisor.SetBrokerSubscriptionResolver(resolver)
	t.Cleanup(func() {
		resolver.unblock()
		supervisor.StopAll(time.Second)
	})

	ctx := context.Background()
	results := make([]error, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = supervisor.StartAlgorithm(ctx, algo)
		}(i)
	}
	close(start)

	// Both goroutines must be inside the resolver before we release, so the
	// second registration genuinely races the first (the whole point of R3).
	for i := 0; i < 2; i++ {
		select {
		case <-resolver.entered:
		case <-time.After(3 * time.Second):
			t.Fatalf("only %d StartAlgorithm call(s) reached the resolver", i)
		}
	}
	resolver.unblock()
	wg.Wait()

	successes := 0
	alreadyRunning := 0
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case err != nil && strings.Contains(err.Error(), "already running"):
			alreadyRunning++
		default:
			t.Fatalf("unexpected StartAlgorithm error: %v", err)
		}
	}
	if successes != 1 || alreadyRunning != 1 {
		t.Fatalf("want one success and one 'already running', got %d/%d (%v)",
			successes, alreadyRunning, results)
	}
}

func TestSupervisorResetAllowsRestartAfterCrashLoop(t *testing.T) {
	store := newMockLifecycleStore()
	algo := supervisorTestPackage()
	store.packages[algo.ID] = algo

	runnerScript := crashingPackageScript()
	options := algorunner.DefaultRecoveryOptions()
	options.InitialBackoff = 5 * time.Millisecond
	options.MaxBackoff = 10 * time.Millisecond
	options.MaxCrashes = 1
	supervisor := newTestSupervisorWithRoot(t, store, stubPackageRoot(t, algo, runnerScript), options)
	t.Cleanup(func() {
		supervisor.StopAll(time.Second)
	})

	ctx := context.Background()
	if err := supervisor.StartAlgorithm(ctx, algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}
	waitForSupervisorStatus(t, supervisor, algo.ID, algorunner.StatusCrashLoop)

	if err := supervisor.ResetAlgorithm(ctx, algo.ID); err != nil {
		t.Fatalf("reset crashed algorithm: %v", err)
	}
	if got := supervisor.GetStatus(algo.ID); got != algorunner.StatusDisabled {
		t.Fatalf("expected disabled status after reset, got %s", got)
	}
	if err := supervisor.StartAlgorithm(ctx, algo); err != nil {
		t.Fatalf("start algorithm after reset: %v", err)
	}
	if err := supervisor.StopAlgorithm(ctx, algo.ID); err != nil {
		t.Fatalf("stop algorithm after restart: %v", err)
	}
}

// TestSupervisorCrashRestartReResolvesBrokerList pins R6: the crash-restart
// branch must re-resolve the broker list (the task set may have changed since
// the initial start) instead of reusing the frozen list captured at start time.
func TestSupervisorCrashRestartReResolvesBrokerList(t *testing.T) {
	store := newMockLifecycleStore()
	algo := supervisorTestPackage()
	store.packages[algo.ID] = algo

	argFile := filepath.Join(t.TempDir(), "runner-args")
	t.Setenv("RUN_ARG_FILE", argFile)
	runnerScript := argRecordingPackageScript()

	options := algorunner.DefaultRecoveryOptions()
	options.InitialBackoff = 5 * time.Millisecond
	options.MaxBackoff = 10 * time.Millisecond
	supervisor := newTestSupervisorWithRoot(t, store, stubPackageRoot(t, algo, runnerScript), options)

	initialSocks := []string{"/tmp/zhulong_broker_1.sock"}
	newSocks := []string{"/tmp/zhulong_broker_2.sock", "/tmp/zhulong_broker_3.sock"}
	resolver := newMutableBrokerPathResolver(initialSocks)
	supervisor.SetBrokerSubscriptionResolver(resolver)
	t.Cleanup(func() {
		supervisor.StopAll(2 * time.Second)
	})

	ctx := context.Background()
	if err := supervisor.StartAlgorithm(ctx, algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}
	waitForRecordedInvocations(t, argFile, 1)

	first := readRecordedRunnerInvocations(t, argFile)
	if got := brokerFlagsIn(first[0]); !reflect.DeepEqual(got, initialSocks) {
		t.Fatalf("first start broker flags = %v, want %v", got, initialSocks)
	}

	// The task set changes, then the runner crashes. The restart must observe the
	// NEW broker list, not the one frozen at start time.
	resolver.Set(newSocks)

	pid := findRunnerPID(t, algo.ID)
	if pid <= 0 {
		t.Fatalf("could not locate runner pid for %s", algo.ID)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill -9 runner pid %d: %v", pid, err)
	}

	waitForRecordedInvocations(t, argFile, 2)
	second := readRecordedRunnerInvocations(t, argFile)
	gotSecond := brokerFlagsIn(second[1])
	t.Logf("first invocation --broker=%v; second invocation --broker=%v", brokerFlagsIn(first[0]), gotSecond)
	if !reflect.DeepEqual(gotSecond, newSocks) {
		t.Fatalf("restart broker flags = %v, want %v (R6: crash restart must re-resolve the broker list)",
			gotSecond, newSocks)
	}
}

// TestSupervisorCrashRestartFallsBackWhenResolveFails pins the fallback half of
// R6: when re-resolution fails during a crash restart, the supervisor must fall
// back to the cached broker list and keep the runner alive rather than degrading
// into CRASH_LOOP.
func TestSupervisorCrashRestartFallsBackWhenResolveFails(t *testing.T) {
	store := newMockLifecycleStore()
	algo := supervisorTestPackage()
	store.packages[algo.ID] = algo

	argFile := filepath.Join(t.TempDir(), "runner-args")
	t.Setenv("RUN_ARG_FILE", argFile)
	runnerScript := argRecordingPackageScript()

	options := algorunner.DefaultRecoveryOptions()
	options.InitialBackoff = 5 * time.Millisecond
	options.MaxBackoff = 10 * time.Millisecond
	supervisor := newTestSupervisorWithRoot(t, store, stubPackageRoot(t, algo, runnerScript), options)

	cachedSocks := []string{"/tmp/zhulong_broker_keep.sock"}
	resolver := newMutableBrokerPathResolver(cachedSocks)
	supervisor.SetBrokerSubscriptionResolver(resolver)
	t.Cleanup(func() {
		supervisor.StopAll(2 * time.Second)
	})

	ctx := context.Background()
	if err := supervisor.StartAlgorithm(ctx, algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}
	waitForRecordedInvocations(t, argFile, 1)

	// Resolution starts failing before the crash restart.
	resolver.SetError(errors.New("broker resolution unavailable"))

	pid := findRunnerPID(t, algo.ID)
	if pid <= 0 {
		t.Fatalf("could not locate runner pid for %s", algo.ID)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill -9 runner pid %d: %v", pid, err)
	}

	waitForRecordedInvocations(t, argFile, 2)
	second := readRecordedRunnerInvocations(t, argFile)
	if got := brokerFlagsIn(second[1]); !reflect.DeepEqual(got, cachedSocks) {
		t.Fatalf("restart broker flags = %v, want cached %v (fallback must reuse the last good list)", got, cachedSocks)
	}

	// Give the crash accounting a moment, then assert no circuit breaker.
	time.Sleep(50 * time.Millisecond)
	if got := supervisor.GetStatus(algo.ID); got == algorunner.StatusCrashLoop {
		t.Fatalf("algorithm entered CRASH_LOOP after a resolve failure; fallback must keep it running")
	}
}

// TestSupervisorCrashRestartKeepsUsableBrokerList keeps the anti-exit-5 intent
// of the original test (a crash restart must still hand the runner a usable
// broker list so it does not exit 5) while no longer asserting that the list is
// the frozen one: under R6 the list is re-resolved on every restart, and with a
// static resolver that re-resolution yields the same usable set.
func TestSupervisorCrashRestartKeepsUsableBrokerList(t *testing.T) {
	store := newMockLifecycleStore()
	algo := supervisorTestPackage()
	store.packages[algo.ID] = algo

	argFile := filepath.Join(t.TempDir(), "runner-args")
	t.Setenv("RUN_ARG_FILE", argFile)
	runnerScript := argRecordingPackageScript()

	options := algorunner.DefaultRecoveryOptions()
	options.InitialBackoff = 5 * time.Millisecond
	options.MaxBackoff = 10 * time.Millisecond
	supervisor := newTestSupervisorWithRoot(t, store, stubPackageRoot(t, algo, runnerScript), options)

	wantSocks := []string{"/tmp/zhulong_broker_7.sock", "/tmp/zhulong_broker_9.sock"}
	supervisor.SetBrokerSubscriptionResolver(&mockBrokerPathResolver{paths: wantSocks})
	t.Cleanup(func() {
		supervisor.StopAll(2 * time.Second)
	})

	ctx := context.Background()
	if err := supervisor.StartAlgorithm(ctx, algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}

	waitForRecordedInvocations(t, argFile, 1)

	first := readRecordedRunnerInvocations(t, argFile)
	if got := brokerFlagsIn(first[0]); !reflect.DeepEqual(got, wantSocks) {
		t.Fatalf("first start broker flags = %v, want %v", got, wantSocks)
	}

	pid := findRunnerPID(t, algo.ID)
	if pid <= 0 {
		t.Fatalf("could not locate runner pid for %s", algo.ID)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill -9 runner pid %d: %v", pid, err)
	}

	waitForRecordedInvocations(t, argFile, 2)
	second := readRecordedRunnerInvocations(t, argFile)
	if got := brokerFlagsIn(second[1]); !reflect.DeepEqual(got, wantSocks) {
		t.Fatalf("restart broker flags = %v, want a usable list %v", got, wantSocks)
	}

	if got := supervisor.GetStatus(algo.ID); got == algorunner.StatusCrashLoop {
		t.Fatalf("algorithm entered CRASH_LOOP after a single kill -9; restart lost its broker list")
	}
}

func TestSupervisorBrokerPathResolverAndRestart(t *testing.T) {
	store := newMockLifecycleStore()
	algo := supervisorTestPackage()
	store.packages[algo.ID] = algo

	runnerScript := idlePackageScript()
	root := stubPackageRoot(t, algo, runnerScript)
	supervisor := newTestSupervisorWithRoot(t, store, root, algorunner.DefaultRecoveryOptions())
	resolver := &mockBrokerPathResolver{
		paths: []string{"/tmp/mock_b1.sock", "/tmp/mock_b2.sock"},
	}
	supervisor.SetBrokerSubscriptionResolver(resolver)
	t.Cleanup(func() {
		supervisor.StopAll(time.Second)
	})

	ctx := context.Background()
	if err := supervisor.StartAlgorithm(ctx, algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}
	if got := supervisor.GetStatus(algo.ID); got != algorunner.StatusRunning {
		t.Fatalf("expected status running, got %s", got)
	}

	if err := supervisor.RestartAlgorithm(ctx, algo.ID); err != nil {
		t.Fatalf("restart algorithm failed: %v", err)
	}
	if got := supervisor.GetStatus(algo.ID); got != algorunner.StatusRunning {
		t.Fatalf("expected status running after restart, got %s", got)
	}

	if err := supervisor.StopAlgorithm(ctx, algo.ID); err != nil {
		t.Fatalf("stop algorithm: %v", err)
	}
}

func TestSupervisorStartFailsWhenBrokerResolutionFails(t *testing.T) {
	store := newMockLifecycleStore()
	algo := supervisorTestPackage()
	store.packages[algo.ID] = algo

	resolveErr := errors.New("camera lookup exploded")
	supervisor := newTestSupervisor(t, store, algo, algorunner.DefaultRecoveryOptions())
	supervisor.SetBrokerSubscriptionResolver(&failingBrokerPathResolver{err: resolveErr})
	t.Cleanup(func() {
		supervisor.StopAll(time.Second)
	})

	err := supervisor.StartAlgorithm(context.Background(), algo)
	if err == nil {
		t.Fatal("expected StartAlgorithm to fail when broker path resolution fails")
	}
	if !errors.Is(err, resolveErr) {
		t.Fatalf("expected the resolution error to be wrapped, got %v", err)
	}
	if got := supervisor.GetStatus(algo.ID); got != algorunner.StatusDisabled {
		t.Fatalf("expected no watcher to be registered, got status %s", got)
	}
}

func TestSupervisorRestartPreservesActiveStatus(t *testing.T) {
	store := newMockLifecycleStore()
	algo := supervisorTestPackage()
	store.packages[algo.ID] = algo

	supervisor := newTestSupervisor(t, store, algo, algorunner.DefaultRecoveryOptions())
	t.Cleanup(func() {
		supervisor.StopAll(time.Second)
	})

	ctx := context.Background()
	if err := store.UpdateAlgorithmStatus(ctx, algo.ID, algorunner.StatusActive); err != nil {
		t.Fatalf("mark algorithm active: %v", err)
	}
	if err := supervisor.StartAlgorithm(ctx, algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}
	if err := supervisor.RestartAlgorithm(ctx, algo.ID); err != nil {
		t.Fatalf("restart algorithm: %v", err)
	}

	stored, err := store.GetAlgorithmPackage(ctx, algo.ID)
	if err != nil {
		t.Fatalf("reload algorithm: %v", err)
	}
	if stored.Status != algorunner.StatusActive {
		t.Fatalf("restart must preserve ACTIVE, got %s", stored.Status)
	}
	if got := supervisor.GetStatus(algo.ID); got != algorunner.StatusRunning {
		t.Fatalf("expected running status after restart, got %s", got)
	}
}
