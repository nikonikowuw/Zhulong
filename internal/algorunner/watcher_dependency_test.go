package algorunner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// installDependencyStubPackage writes script as pkg's entry executable inside root, mirroring
// the supervisor_helpers_test.go helpers for the white-box tests in this package.
func installDependencyStubPackage(t *testing.T, root string, pkg PackageMeta, script string) string {
	t.Helper()
	dir := filepath.Join(root, pkg.ID, pkg.Version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create package dir: %v", err)
	}
	entry := filepath.Join(dir, pkg.ID)
	if err := os.WriteFile(entry, []byte(script), 0o755); err != nil {
		t.Fatalf("write package entry: %v", err)
	}
	return entry
}

// countRecordedStarts returns how many times a stub wrote its "run" marker.
func countRecordedStarts(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read start marker: %v", err)
	}
	return len(strings.Fields(string(data)))
}

// waitForStarts blocks until the stub has started want times.
func waitForStarts(t *testing.T, path string, want int) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if countRecordedStarts(t, path) >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("stub started %d times, want at least %d", countRecordedStarts(t, path), want)
}

// dependencyFailureScript records one start, holds for hold, then exits with the
// broker-unavailable contract (stage record on stderr + exit code 5).
func dependencyFailureScript(hold string) string {
	return "#!/bin/sh\n" +
		"printf 'run\\n' >> \"$DEP_RUN_FILE\"\n" +
		hold +
		"printf '{\"status\":\"failed\",\"stage\":\"connect\"}\\n' >&2\n" +
		"exit 5\n"
}

// TestDependencyFailuresDoNotTripTheCrashBreaker is the end-to-end guard for the
// dependency branch: a runner that keeps exiting because its broker socket is gone
// must be restarted without ever spending crash quota.
//
// MaxCrashes is deliberately 1: a single RecordCrash would trip the breaker, so a
// single misclassified dependency exit turns this test into a CRASH_LOOP.
func TestDependencyFailuresDoNotTripTheCrashBreaker(t *testing.T) {
	const algoID = "dep_stub"
	algo := PackageMeta{ID: algoID, Name: algoID, Version: "1.0.0"}

	runFile := filepath.Join(t.TempDir(), "starts")
	t.Setenv("DEP_RUN_FILE", runFile)

	root := t.TempDir()
	installDependencyStubPackage(t, root, algo, dependencyFailureScript(""))

	opts := DefaultRecoveryOptions()
	opts.InitialBackoff = 5 * time.Millisecond
	opts.MaxBackoff = 10 * time.Millisecond
	opts.MaxCrashes = 1
	supervisor := NewSupervisor(nil, Runtime{AlgorithmsDir: root}, opts, zap.NewNop())
	t.Cleanup(func() { supervisor.StopAll(time.Second) })

	if err := supervisor.StartAlgorithm(context.Background(), algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}

	// One initial start plus at least two restarts.
	waitForStarts(t, runFile, 3)

	watcher := supervisor.watcherFor(algoID)
	if watcher == nil {
		t.Fatal("watcher disappeared while restarting a dependency failure")
	}
	if watcher.sm.IsCircuitBroken() {
		t.Fatal("dependency failures consumed crash quota: the circuit breaker tripped")
	}
	if got := supervisor.GetStatus(algoID); got == StatusCrashLoop {
		t.Fatal("dependency failures drove the supervisor into CRASH_LOOP")
	}
}

// TestDependencyLadderResetsAfterAStableRun pins the snapshot the fix exists for.
//
// The stub runs long enough to count as "the dependency was just lost", so every
// exit must start the ladder over: dependencyRetries stays at 1 instead of
// climbing while the process has already left StatusRunning (where
// RunningDuration reports 0 and the reset branch can never fire).
func TestDependencyLadderResetsAfterAStableRun(t *testing.T) {
	const algoID = "dep_stable_stub"
	algo := PackageMeta{ID: algoID, Name: algoID, Version: "1.0.0"}

	runFile := filepath.Join(t.TempDir(), "starts")
	t.Setenv("DEP_RUN_FILE", runFile)

	root := t.TempDir()
	installDependencyStubPackage(t, root, algo, dependencyFailureScript("sleep 0.05\n"))

	opts := DefaultRecoveryOptions()
	opts.InitialBackoff = 5 * time.Millisecond
	// MaxBackoff doubles as the stable-reset threshold: 20ms is far below the
	// stub's 50ms uptime, so every exit qualifies for a reset.
	opts.MaxBackoff = 20 * time.Millisecond
	supervisor := NewSupervisor(nil, Runtime{AlgorithmsDir: root}, opts, zap.NewNop())
	t.Cleanup(func() { supervisor.StopAll(time.Second) })

	if err := supervisor.StartAlgorithm(context.Background(), algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}

	waitForStarts(t, runFile, 3)

	watcher := supervisor.watcherFor(algoID)
	if watcher == nil {
		t.Fatal("watcher disappeared while restarting a dependency failure")
	}

	// dependencyRetries is owned by the supervise loop goroutine, so read it only
	// after that goroutine has provably exited.
	supervisor.StopAll(time.Second)
	select {
	case <-watcher.doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher loop did not stop")
	}
	if got := watcher.dependencyRetries; got > 1 {
		t.Fatalf("dependency retries reached %d after runs longer than the stable threshold; "+
			"the ladder is not being reset after a stable run", got)
	}
}

// TestDependencyLadderEscalatesWhenRunsStayShort is the reverse direction of
// TestDependencyLadderResetsAfterAStableRun: a stub that exits immediately never
// runs long enough to count as a stable run, so the ladder must keep climbing.
//
// MaxBackoff doubles as the stable-reset threshold, so the asserted 400ms is two
// orders of magnitude above the stub's ~ms uptime: the reset branch can never fire
// here, and an unconditional reset (or one behind a missing `if`) turns this test
// red by pinning dependencyRetries at 1 forever.
func TestDependencyLadderEscalatesWhenRunsStayShort(t *testing.T) {
	const algoID = "dep_fast_stub"
	algo := PackageMeta{ID: algoID, Name: algoID, Version: "1.0.0"}

	runFile := filepath.Join(t.TempDir(), "starts")
	t.Setenv("DEP_RUN_FILE", runFile)

	root := t.TempDir()
	installDependencyStubPackage(t, root, algo, dependencyFailureScript(""))

	opts := DefaultRecoveryOptions()
	// The dependency ladder clamps to MaxBackoff, so every restart below waits
	// exactly 400ms and the whole test stays far inside waitForStarts' deadline.
	opts.MaxBackoff = 400 * time.Millisecond
	supervisor := NewSupervisor(nil, Runtime{AlgorithmsDir: root}, opts, zap.NewNop())
	t.Cleanup(func() { supervisor.StopAll(time.Second) })

	if err := supervisor.StartAlgorithm(context.Background(), algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}

	// One initial start plus three restarts, i.e. three dependency exits.
	waitForStarts(t, runFile, 4)

	watcher := supervisor.watcherFor(algoID)
	if watcher == nil {
		t.Fatal("watcher disappeared while restarting a dependency failure")
	}

	// dependencyRetries is owned by the supervise loop goroutine, so read it only
	// after that goroutine has provably exited.
	supervisor.StopAll(time.Second)
	select {
	case <-watcher.doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher loop did not stop")
	}
	if got := watcher.dependencyRetries; got < 3 {
		t.Fatalf("dependency retries = %d after three short runs; the ladder is being reset "+
			"although no run reached the stable threshold", got)
	}
}

// dependencyCrashOnTriggerScript records one start, waits for the trigger file,
// then exits non-zero. The wait is what lets the test remove the package entry
// between the successful start and the respawn that judges it.
func dependencyCrashOnTriggerScript() string {
	return "#!/bin/sh\n" +
		"printf 'run\\n' >> \"$RESPAWN_RUN_FILE\"\n" +
		"while [ ! -f \"$RESPAWN_TRIGGER_FILE\" ]; do sleep 0.01; done\n" +
		"exit 1\n"
}

// TestRespawnEntryLossEndsSupervisionWithoutCrashQuota is the white-box
// counterpart of TestWatcherRespawnEndsSupervisionWhenEntryDisappears.
//
// It pins respawn's cfgErr polarity precisely: the supervise loop must exit, the
// circuit breaker must stay untouched (giving up must not spend crash quota), and
// exactly one "giving up restart" line may be logged. Returning supervisionContinues
// instead re-enters respawn, spends a crash per attempt and only ends once the
// breaker trips — which all three assertions reject, deterministically, because
// doneCh is awaited before anything is inspected.
func TestRespawnEntryLossEndsSupervisionWithoutCrashQuota(t *testing.T) {
	const algoID = "respawn_entry_gone"
	algo := PackageMeta{ID: algoID, Name: algoID, Version: "1.0.0"}

	runFile := filepath.Join(t.TempDir(), "starts")
	trigger := filepath.Join(t.TempDir(), "crash-trigger")
	t.Setenv("RESPAWN_RUN_FILE", runFile)
	t.Setenv("RESPAWN_TRIGGER_FILE", trigger)

	root := t.TempDir()
	entry := installDependencyStubPackage(t, root, algo, dependencyCrashOnTriggerScript())

	core, observed := observer.New(zapcore.WarnLevel)

	opts := DefaultRecoveryOptions()
	opts.InitialBackoff = 5 * time.Millisecond
	opts.MaxBackoff = 10 * time.Millisecond
	supervisor := NewSupervisor(nil, Runtime{AlgorithmsDir: root}, opts, zap.New(core))
	t.Cleanup(func() { supervisor.StopAll(time.Second) })

	if err := supervisor.StartAlgorithm(context.Background(), algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}
	watcher := supervisor.watcherFor(algoID)
	if watcher == nil {
		t.Fatal("watcher is not registered right after a successful start")
	}
	waitForStarts(t, runFile, 1)

	// The package becomes unusable while it runs: only the respawn can notice.
	if err := os.Remove(entry); err != nil {
		t.Fatalf("remove package entry: %v", err)
	}
	if err := os.WriteFile(trigger, []byte("crash\n"), 0o644); err != nil {
		t.Fatalf("write crash trigger: %v", err)
	}

	// The loop must end on its own: "give up" is the behaviour under test, so no
	// stop request may be what ends it.
	select {
	case <-watcher.doneCh:
	case <-time.After(5 * time.Second):
		t.Fatal("supervise loop did not end after the package entry disappeared")
	}
	supervisor.StopAll(time.Second)

	if watcher.sm.IsCircuitBroken() {
		t.Fatal("giving up on an unusable entry consumed crash quota and tripped the circuit breaker")
	}
	if got := observed.FilterMessage("package entry became unusable, giving up restart").Len(); got != 1 {
		t.Fatalf("\"giving up restart\" was logged %d times, want exactly 1", got)
	}
	if got := countRecordedStarts(t, runFile); got != 1 {
		t.Fatalf("a package with no entry was spawned again: %d starts", got)
	}
}

// dependencySuccessScript records one start and exits cleanly, so a spawned child
// is provable from the marker file alone.
func dependencySuccessScript() string {
	return "#!/bin/sh\n" +
		"printf 'run\\n' >> \"$SPAWN_MARKER_FILE\"\n" +
		"exit 0\n"
}

// newSpawnProbe builds an unsupervised watcher plus an unstarted process for a
// stub that records every start in a marker file.
func newSpawnProbe(t *testing.T) (watcher *workerWatcher, process *RunnerProcess, marker string) {
	t.Helper()
	const algoID = "spawn_probe"
	algo := PackageMeta{ID: algoID, Name: algoID, Version: "1.0.0"}

	marker = filepath.Join(t.TempDir(), "spawns")
	t.Setenv("SPAWN_MARKER_FILE", marker)

	root := t.TempDir()
	installDependencyStubPackage(t, root, algo, dependencySuccessScript())

	supervisor := NewSupervisor(nil, Runtime{AlgorithmsDir: root}, DefaultRecoveryOptions(), zap.NewNop())
	cfg, err := supervisor.processConfig(algo, nil)
	if err != nil {
		t.Fatalf("process config: %v", err)
	}
	return newWorkerWatcher(supervisor, algo, nil), NewRunnerProcess(cfg), marker
}

// TestStartProcessDoesNotSpawnAfterAStop pins startProcess's stop-request
// re-check: a watcher that was already asked to stop must report spawnStopped and
// leave no child behind.
func TestStartProcessDoesNotSpawnAfterAStop(t *testing.T) {
	watcher, process, marker := newSpawnProbe(t)

	if err := watcher.stopProcess(time.Second); err != nil {
		t.Fatalf("stop watcher: %v", err)
	}
	if outcome := watcher.startProcess(process); outcome != spawnStopped {
		t.Fatalf("startProcess on a stopped watcher = %v, want spawnStopped", outcome)
	}
	if got := watcher.currentStatus(); got != StatusDisabled {
		t.Fatalf("status after a refused spawn = %q, want %q", got, StatusDisabled)
	}
	// PID is 0 only while Start never ran: it is published by the spawn itself, so
	// this pins "no fork happened" without depending on how fast the child runs.
	if pid := process.PID(); pid != 0 {
		t.Fatalf("a stopped watcher forked a child anyway: pid %d", pid)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a stopped watcher spawned a child anyway: marker stat = %v", err)
	}
}

// TestStartProcessPublishesUnderStopMu pins the critical section itself: while
// stopMu is held, startProcess must not get as far as forking the child, because
// the publication has to happen under the same lock stopProcess takes.
func TestStartProcessPublishesUnderStopMu(t *testing.T) {
	watcher, process, marker := newSpawnProbe(t)

	watcher.stopMu.Lock()
	locked := true
	defer func() {
		if locked {
			watcher.stopMu.Unlock()
		}
	}()

	results := make(chan spawnOutcome, 1)
	go func() { results <- watcher.startProcess(process) }()

	select {
	case outcome := <-results:
		t.Fatalf("startProcess returned %v while stopMu was held", outcome)
	case <-time.After(200 * time.Millisecond):
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("startProcess forked a child before publishing under stopMu: marker stat = %v", err)
	}

	watcher.stopMu.Unlock()
	locked = false

	select {
	case outcome := <-results:
		if outcome != spawnStarted {
			t.Fatalf("startProcess after stopMu was released = %v, want spawnStarted", outcome)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("startProcess did not return after stopMu was released")
	}
}

// TestStartProcessStopsASpawnThatRacedAStop pins the post-Start complement stop:
// the one branch that reclaims a child spawned after the stop-request re-check but
// before RunnerProcess.Stop could see it.
//
// The window is reproduced deterministically by holding the process's own mu from
// the test: startProcess gets past its under-lock re-check and publishes the
// process, then blocks inside Start. That is exactly the state in which a
// concurrent Stop sees cmd == nil and returns nil, so only the complement stop can
// reap the child.
//
// "A child really was spawned" is proven by the published pid, not by a marker
// file the stub writes: the complement stop SIGTERMs the freshly forked child
// before the shell can execute its first statement, so the marker's absence says
// nothing about whether a fork happened. The pid, by contrast, is only set from a
// cmd that cmd.Start actually launched.
func TestStartProcessStopsASpawnThatRacedAStop(t *testing.T) {
	watcher, process, marker := newSpawnProbe(t)

	// Held across the whole window: Start cannot publish cmd while it is ours.
	process.mu.Lock()
	locked := true
	defer func() {
		if locked {
			process.mu.Unlock()
		}
	}()

	outcomes := make(chan spawnOutcome, 1)
	go func() { outcomes <- watcher.startProcess(process) }()

	// The publication happens under stopMu BEFORE Start is called, so observing it
	// proves the pre-spawn re-check already passed and the goroutine is now parked
	// on the process mu.
	deadline := time.Now().Add(10 * time.Second)
	for watcher.currentProcess() != process {
		if time.Now().After(deadline) {
			t.Fatal("startProcess never published the process")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the stub ran while Start was still blocked on the process mu: marker stat = %v", err)
	}

	// The stop lands while the child is (about to be) spawned and Stop cannot see
	// it: the complement stop is the only thing that can reap it.
	watcher.requestStop()
	process.mu.Unlock()
	locked = false

	select {
	case outcome := <-outcomes:
		if outcome != spawnStopped {
			t.Fatalf("startProcess racing a stop = %v, want spawnStopped", outcome)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("startProcess did not return after the process mu was released")
	}

	if got := watcher.currentStatus(); got != StatusDisabled {
		t.Fatalf("status after a stop raced the spawn = %q, want %q", got, StatusDisabled)
	}
	// The pre-spawn re-check had already passed when the process was published
	// above, so spawnStopped can only have come from the complement stop — and a
	// pid plus a closed done channel prove that branch really forked and reaped a
	// child. Without the complement stop this call returns spawnStarted instead.
	if pid := process.PID(); pid == 0 {
		t.Fatal("no child was ever spawned, so the complement stop is not what this test exercised")
	}
	select {
	case <-process.Done():
	default:
		t.Fatal("the raced child was never reaped: its done channel is still open")
	}
	if got := process.State(); got != StatusDisabled {
		t.Fatalf("raced child state = %q, want %q", got, StatusDisabled)
	}

	t.Cleanup(func() {
		if err := process.Stop(time.Second); err != nil {
			t.Errorf("stop raced runner: %v", err)
		}
	})
}
