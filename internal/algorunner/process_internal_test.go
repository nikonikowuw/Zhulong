package algorunner

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"go.uber.org/zap"
)

// writeExecutableScript installs body as an executable shell script and returns
// its path. The internal lifecycle tests need a real child process: the sentinels
// they pin are only produced by the os/exec and signal paths.
func writeExecutableScript(t *testing.T, name, body string) string {
	t.Helper()
	script := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return script
}

// TestRunnerProcessSecondStartReturnsAlreadyRunning pins the double-start guard:
// a second Start on a live runner must be refused with the sentinel, and must not
// disturb the child the first call published.
func TestRunnerProcessSecondStartReturnsAlreadyRunning(t *testing.T) {
	script := writeExecutableScript(t, "idle_runner",
		"#!/bin/sh\ntrap 'exit 0' TERM INT\nwhile true; do sleep 0.05; done\n")

	process := NewRunnerProcess(ProcessConfig{
		AlgorithmID:    "dup_pkg",
		ExecutablePath: script,
		Logger:         zap.NewNop(),
	})
	if err := process.Start(); err != nil {
		t.Fatalf("first Start failed: %v", err)
	}
	t.Cleanup(func() {
		if err := process.Stop(2 * time.Second); err != nil {
			t.Errorf("stop runner: %v", err)
		}
	})

	pid := process.PID()
	err := process.Start()
	if !errors.Is(err, errProcessAlreadyRunning) {
		t.Fatalf("second Start = %v, want errProcessAlreadyRunning", err)
	}
	if got := process.State(); got != StatusRunning {
		t.Fatalf("state after the refused start = %q, want %q", got, StatusRunning)
	}
	if got := process.PID(); got != pid {
		t.Fatalf("second Start replaced the live child: pid %d -> %d", pid, got)
	}
}

// TestLastRunDurationSurvivesExit pins the snapshot the dependency ladder reads.
//
// RunningDuration's contract is left alone: it reports 0 once the process leaves
// StatusRunning. LastRunDuration must still report the finished run's uptime,
// because that is exactly when the supervise loop asks whether a dependency
// failure starts a fresh backoff ladder.
func TestLastRunDurationSurvivesExit(t *testing.T) {
	script := writeExecutableScript(t, "long_run_runner",
		"#!/bin/sh\ntrap 'exit 0' TERM INT\nwhile true; do sleep 0.05; done\n")

	process := NewRunnerProcess(ProcessConfig{
		AlgorithmID:    "run_time_pkg",
		ExecutablePath: script,
		Logger:         zap.NewNop(),
	})
	if err := process.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() {
		if err := process.Stop(2 * time.Second); err != nil {
			t.Errorf("stop runner: %v", err)
		}
	})

	time.Sleep(100 * time.Millisecond)

	// Kill the whole process group so the child exits without a requested stop,
	// i.e. the way a crashed dependency exit reaches recordExit.
	if err := syscall.Kill(-process.PID(), syscall.SIGKILL); err != nil {
		t.Fatalf("kill runner process group: %v", err)
	}
	select {
	case <-process.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not exit after SIGKILL")
	}

	if got := process.RunningDuration(); got != 0 {
		t.Fatalf("RunningDuration after exit = %v, want 0 (its documented semantics must not change)", got)
	}
	if got := process.LastRunDuration(); got < 50*time.Millisecond {
		t.Fatalf("LastRunDuration after exit = %v, want the finished run's ~100ms uptime", got)
	}
}

// TestRunnerProcessStopReturnsNotTerminated pins the exhausted SIGKILL ladder.
//
// The child is spawned and reaped normally, then its done channel is replaced by
// one that is never closed. That is precisely the state Stop must report as
// errProcessNotTerminated: the command is gone, but the lifecycle has no evidence
// that the process group released its resources, so silence would be a lie.
func TestRunnerProcessStopReturnsNotTerminated(t *testing.T) {
	script := writeExecutableScript(t, "already_exited_runner", "#!/bin/sh\nexit 0\n")

	process := NewRunnerProcess(ProcessConfig{
		AlgorithmID:    "not_terminated_pkg",
		ExecutablePath: script,
		Logger:         zap.NewNop(),
	})
	if err := process.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	select {
	case <-process.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stub child did not exit")
	}

	process.mu.Lock()
	process.doneCh = make(chan struct{}) // never closed on purpose
	process.mu.Unlock()

	err := process.Stop(20 * time.Millisecond)
	if !errors.Is(err, errProcessNotTerminated) {
		t.Fatalf("Stop = %v, want errProcessNotTerminated", err)
	}
}
