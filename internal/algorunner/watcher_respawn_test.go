package algorunner_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algorunner"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// triggerCrashPackageScript records each start, then crashes on demand. The crash
// is deferred so the test can change the installation between the successful first
// start and the respawn it is about to judge.
func triggerCrashPackageScript() string {
	return "#!/bin/sh\n" +
		"printf 'start\\n' >> \"$RUN_ARG_FILE\"\n" +
		"while [ ! -f \"$RUN_TRIGGER_FILE\" ]; do sleep 0.01; done\n" +
		"exit 1\n"
}

// newRespawnTestSupervisor installs the trigger-crash stub and wires the two file
// paths its script reads.
func newRespawnTestSupervisor(
	t *testing.T,
	algo algorunner.PackageMeta,
	opts algorunner.RecoveryOptions,
	logger *zap.Logger,
) (supervisor *algorunner.Supervisor, entry string, trigger string) {
	t.Helper()
	store := newMockLifecycleStore()
	store.packages[algo.ID] = algo

	argFile := filepath.Join(t.TempDir(), "runner-args")
	triggerFile := filepath.Join(t.TempDir(), "crash-trigger")
	t.Setenv("RUN_ARG_FILE", argFile)
	t.Setenv("RUN_TRIGGER_FILE", triggerFile)

	root := t.TempDir()
	stubPackageRootAt(t, root, algo, triggerCrashPackageScript())
	entry = filepath.Join(root, algo.ID, algo.Version, algo.ID)
	trigger = triggerFile

	supervisor = algorunner.NewSupervisor(
		store,
		algorunner.Runtime{AlgorithmsDir: root},
		opts,
		logger,
	)
	t.Cleanup(func() { supervisor.StopAll(2 * time.Second) })
	return supervisor, entry, triggerFile
}

func respawnTestOptions() algorunner.RecoveryOptions {
	opts := algorunner.DefaultRecoveryOptions()
	opts.InitialBackoff = 5 * time.Millisecond
	opts.MaxBackoff = 10 * time.Millisecond
	return opts
}

// crashNow makes the running stub exit non-zero.
func crashNow(t *testing.T, trigger string) {
	t.Helper()
	if err := os.WriteFile(trigger, []byte("crash\n"), 0o644); err != nil {
		t.Fatalf("write crash trigger: %v", err)
	}
}

// TestWatcherRespawnEndsSupervisionWhenEntryDisappears pins the "cfg error" half
// of respawn: an entry that vanished from the install dir cannot heal by retrying,
// so the watcher must park in CRASH_LOOP instead of hammering Start.
//
// This is the black-box view: the polarity is pinned from what it leaves behind.
// A giving-up branch flipped to "continues" respawns again — one crash per extra
// attempt — so this test rejects both a second "giving up restart" line and the
// circuit breaker the extra crashes eventually trip. The white-box counterpart
// (TestRespawnEntryLossEndsSupervisionWithoutCrashQuota) pins sm.IsCircuitBroken()
// and the loop's exit directly, which an external test package cannot reach.
func TestWatcherRespawnEndsSupervisionWhenEntryDisappears(t *testing.T) {
	algo := supervisorTestPackage()
	core, observed := observer.New(zapcore.WarnLevel)
	supervisor, entry, trigger := newRespawnTestSupervisor(t, algo, respawnTestOptions(), zap.New(core))

	ctx := context.Background()
	if err := supervisor.StartAlgorithm(ctx, algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}
	argFile := os.Getenv("RUN_ARG_FILE")
	waitForRecordedInvocations(t, argFile, 1)

	// The package becomes unusable while it runs: only the respawn can notice.
	if err := os.Remove(entry); err != nil {
		t.Fatalf("remove package entry: %v", err)
	}
	crashNow(t, trigger)

	waitForSupervisorStatus(t, supervisor, algo.ID, algorunner.StatusCrashLoop)

	// StopAll waits for the watcher loop (bounded), so afterwards nothing can
	// still be adding log lines.
	supervisor.StopAll(2 * time.Second)

	if got := len(readRecordedRunnerInvocations(t, argFile)); got != 1 {
		t.Fatalf("a package with no entry was restarted anyway: %d invocations", got)
	}
	if got := observed.FilterMessage("package entry became unusable, giving up restart").Len(); got != 1 {
		t.Fatalf("\"giving up restart\" was logged %d times, want exactly 1", got)
	}
	if got := observed.FilterMessage("algorithm entered CRASH_LOOP (circuit broken), halting auto-restart").Len(); got != 0 {
		t.Fatalf("giving up on an unusable entry burned crash quota and tripped the circuit breaker")
	}
}

// TestWatcherRespawnKeepsSupervisingWhenStartFails pins the "Start error" half of
// respawn: an entry that resolves (regular file) but cannot be executed is a
// transient spawn failure for this supervisor, which must keep supervising rather
// than quietly end (the loop would exit and the watcher would never retry).
func TestWatcherRespawnKeepsSupervisingWhenStartFails(t *testing.T) {
	algo := supervisorTestPackage()
	opts := respawnTestOptions()
	// A spawn failure also counts as a crash, so the quota must be wide enough for
	// the loop to demonstrably keep retrying inside the test window.
	opts.MaxCrashes = 100

	core, observed := observer.New(zapcore.WarnLevel)
	supervisor, entry, trigger := newRespawnTestSupervisor(t, algo, opts, zap.New(core))

	ctx := context.Background()
	if err := supervisor.StartAlgorithm(ctx, algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}
	argFile := os.Getenv("RUN_ARG_FILE")
	waitForRecordedInvocations(t, argFile, 1)

	// Replace the entry atomically with an executable the kernel refuses to exec
	// (no shebang): entry resolution still succeeds, only cmd.Start fails.
	junk := entry + ".junk"
	if err := os.WriteFile(junk, []byte("this is not an executable format\n"), 0o755); err != nil {
		t.Fatalf("write non-executable entry: %v", err)
	}
	if err := os.Rename(junk, entry); err != nil {
		t.Fatalf("install non-executable entry: %v", err)
	}
	crashNow(t, trigger)

	// Two spawn failures prove the loop did not end after the first one.
	deadline := time.Now().Add(5 * time.Second)
	restartFailures := 0
	for time.Now().Before(deadline) {
		restartFailures = 0
		for _, entry := range observed.All() {
			if entry.Message == "failed to restart runner process" {
				restartFailures++
			}
		}
		if restartFailures >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if restartFailures < 2 {
		t.Fatalf("supervision ended after %d spawn failure(s); loop must keep restarting", restartFailures)
	}
	if got := supervisor.GetStatus(algo.ID); got == algorunner.StatusCrashLoop || got == algorunner.StatusDisabled {
		t.Fatalf("a failed spawn was reported as %s", got)
	}
	if got := supervisor.GetStatus(algo.ID); got == algorunner.StatusRunning {
		t.Fatal("a package that never spawned is reported as RUNNING")
	}
}
