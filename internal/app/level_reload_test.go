package app

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestLogLevelReloaderReadsUpdatedConfigOnSIGHUP(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	writeReloadConfig(t, path, "info")
	level := zap.NewAtomicLevelAt(zapcore.InfoLevel)
	core, logs := observer.New(level)
	logger := zap.New(core)
	reloader := newLogLevelReloader(path, DefaultConfig(), level, logger)
	reloader.Start()
	defer reloader.Stop()

	logger.Debug("debug-before-reload")
	if got := logs.FilterMessage("debug-before-reload").Len(); got != 0 {
		t.Fatalf("debug event logged before SIGHUP changed the level: got %d", got)
	}

	pid := os.Getpid()
	writeReloadConfig(t, path, "debug")
	sendReloadSignal(t)
	waitForLevel(t, level, zapcore.DebugLevel)
	if os.Getpid() != pid {
		t.Fatal("process ID changed during level reload")
	}
	logger.Debug("debug-after-reload")
	if got := logs.FilterMessage("debug-after-reload").Len(); got != 1 {
		t.Fatalf("debug event count after SIGHUP: got %d, want 1", got)
	}

	writeReloadConfig(t, path, "fatal")
	sendReloadSignal(t)
	waitForWarnings(t, logs, 1)
	if got := level.Level(); got != zapcore.DebugLevel {
		t.Fatalf("invalid level changed active level to %s", got)
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove config: %v", err)
	}
	sendReloadSignal(t)
	waitForWarnings(t, logs, 2)
	if got := level.Level(); got != zapcore.DebugLevel {
		t.Fatalf("missing file changed active level to %s", got)
	}
}

// TestLogLevelReloaderReportsRestartOnlyPolicyChanges covers the other half of the
// reload contract: the level applies in place, while a sink/encoder edit cannot, and
// must say so rather than appearing to have taken effect.
func TestLogLevelReloaderReportsRestartOnlyPolicyChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	config := DefaultConfig()
	config.Logging.Format = "json"
	writeReloadPolicyConfig(t, path, "[logging]\nlevel = \"info\"\nformat = \"json\"\n")

	level := zap.NewAtomicLevelAt(zapcore.InfoLevel)
	core, logs := observer.New(zapcore.DebugLevel)
	reloader := newLogLevelReloader(path, config, level, zap.New(core))
	reloader.Start()
	defer reloader.Stop()

	// A level-only edit must not claim a restart is needed.
	writeReloadPolicyConfig(t, path, "[logging]\nlevel = \"debug\"\nformat = \"json\"\n")
	sendReloadSignal(t)
	waitForLevel(t, level, zapcore.DebugLevel)
	if got := logs.FilterMessage("logging policy changed on disk; restart required to apply").Len(); got != 0 {
		t.Fatalf("level-only reload reported a restart requirement: %d", got)
	}

	writeReloadPolicyConfig(t, path, "[logging]\nlevel = \"debug\"\nformat = \"console\"\n")
	sendReloadSignal(t)
	waitForMessage(t, logs, "logging policy changed on disk; restart required to apply")
	entry := logs.FilterMessage("logging policy changed on disk; restart required to apply").All()[0]
	fields := entry.ContextMap()
	changed, ok := fields["fields"].([]any)
	if !ok || len(changed) != 1 || changed[0] != "logging.format" {
		t.Fatalf("restart-required fields = %#v, want only logging.format", fields["fields"])
	}
	if got := level.Level(); got != zapcore.DebugLevel {
		t.Fatalf("active level = %s after a policy edit, want debug retained", got)
	}
}

func writeReloadPolicyConfig(t *testing.T, path, contents string) {
	t.Helper()
	temporary := path + ".next"
	if err := os.WriteFile(temporary, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config replacement: %v", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		t.Fatalf("replace config: %v", err)
	}
}

func waitForMessage(t *testing.T, logs *observer.ObservedLogs, message string) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		if logs.FilterMessage(message).Len() > 0 {
			return
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatalf("did not receive %q within the timeout", message)
		}
	}
}

func writeReloadConfig(t *testing.T, path, level string) {
	t.Helper()
	temporary := path + ".next"
	contents := "[logging]\nlevel = \"" + level + "\"\n"
	if err := os.WriteFile(temporary, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config replacement: %v", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		t.Fatalf("replace config: %v", err)
	}
}

func sendReloadSignal(t *testing.T) {
	t.Helper()
	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("find current process: %v", err)
	}
	if err := process.Signal(syscall.SIGHUP); err != nil {
		t.Fatalf("send SIGHUP: %v", err)
	}
}

func waitForLevel(t *testing.T, level zap.AtomicLevel, want zapcore.Level) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		if level.Level() == want {
			return
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatalf("level did not change to %s; remains %s", want, level.Level())
		}
	}
}

func waitForWarnings(t *testing.T, logs *observer.ObservedLogs, count int) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		if logs.FilterLevelExact(zap.WarnLevel).Len() >= count {
			return
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatalf("received %d warnings, want at least %d", logs.Len(), count)
		}
	}
}
