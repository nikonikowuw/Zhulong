package app

import (
	"sync/atomic"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// logPressure raises the effective level while the media filesystem is in an
// emergency write stop.
//
// Log volume is the only thing the host can shed without losing evidence: during
// an emergency stop the storage gate is already refusing to create recording and
// snapshot files, so letting Debug/Info keep consuming the same budget defeats
// the gate. The pressure flag only ever *raises* the floor — it can hide events,
// never promote one the configured level already filtered out — so a SIGHUP level
// reload and storage pressure can coexist without either stomping the other.
//
// It is deliberately an enabler rather than an AtomicLevel mutation: raising the
// configured level would have to be undone with a remembered previous value, and
// a concurrent reload would make that memory wrong.
type logPressure struct {
	configured zap.AtomicLevel
	active     atomic.Bool
}

func newLogPressure(configured zap.AtomicLevel) *logPressure {
	return &logPressure{configured: configured}
}

// Enabled implements zapcore.LevelEnabler.
func (p *logPressure) Enabled(level zapcore.Level) bool {
	if p.active.Load() && level < zapcore.WarnLevel {
		return false
	}
	return p.configured.Enabled(level)
}

// SetPressure reports whether the state changed, so callers can log the edge
// once instead of every probe cycle.
func (p *logPressure) SetPressure(active bool) bool {
	return p.active.Swap(active) != active
}

// UnderPressure reports whether log volume is currently reduced.
func (p *logPressure) UnderPressure() bool {
	return p.active.Load()
}
