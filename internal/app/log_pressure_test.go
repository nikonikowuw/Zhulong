package app

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// TestLogPressureOnlyRaisesTheFloor pins the composition rule: storage pressure may
// hide Debug/Info, but it must never promote a record the configured level already
// filtered out, or a storage emergency would silently re-enable noisy logging.
func TestLogPressureOnlyRaisesTheFloor(t *testing.T) {
	level := zap.NewAtomicLevelAt(zapcore.DebugLevel)
	pressure := newLogPressure(level)

	if !pressure.Enabled(zapcore.DebugLevel) {
		t.Fatal("debug must be visible with no pressure")
	}
	if !pressure.SetPressure(true) {
		t.Fatal("first activation must report a change")
	}
	if pressure.SetPressure(true) {
		t.Fatal("repeated activation must not report a change")
	}
	for name, testCase := range map[string]struct {
		level zapcore.Level
		want  bool
	}{
		"debug": {zapcore.DebugLevel, false},
		"info":  {zapcore.InfoLevel, false},
		"warn":  {zapcore.WarnLevel, true},
		"error": {zapcore.ErrorLevel, true},
	} {
		if got := pressure.Enabled(testCase.level); got != testCase.want {
			t.Errorf("%s enabled = %v, want %v under pressure", name, got, testCase.want)
		}
	}

	// A concurrent SIGHUP reload raising the configured level must not resurrect the
	// records pressure is hiding.
	level.SetLevel(zapcore.DebugLevel)
	if pressure.Enabled(zapcore.InfoLevel) {
		t.Fatal("level reload bypassed active pressure")
	}

	if !pressure.SetPressure(false) {
		t.Fatal("release must report a change")
	}
	if !pressure.Enabled(zapcore.DebugLevel) {
		t.Fatal("debug must be visible again after pressure is released")
	}

	level.SetLevel(zapcore.ErrorLevel)
	if pressure.Enabled(zapcore.WarnLevel) {
		t.Fatal("releasing pressure must not lower the configured level")
	}
}

func TestLogPressureReportsStateForEdgeLogging(t *testing.T) {
	pressure := newLogPressure(zap.NewAtomicLevelAt(zapcore.InfoLevel))
	if pressure.UnderPressure() {
		t.Fatal("new enabler reported pressure")
	}
	pressure.SetPressure(true)
	if !pressure.UnderPressure() {
		t.Fatal("UnderPressure did not reflect activation")
	}
}
