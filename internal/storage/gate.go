package storage

import (
	"sync"
	"sync/atomic"

	"go.uber.org/zap"
)

// EmergencyGate guards against filesystem exhaustion by regulating write permissions.
type EmergencyGate struct {
	mu          sync.RWMutex
	logger      *zap.Logger
	canWrite    atomic.Bool
	isEmergency atomic.Bool
	reason      string

	onEmergencyStop   func(reason string)
	onEmergencyResume func()
	observers         []func(blocked bool)
}

// gateTransition carries the state changes one evaluation produced.
//
// Evaluation mutates the gate under its mutex, but callbacks and observers must
// run with the mutex released: a listener is allowed to call back into the gate.
// Collecting the edges into a value keeps that ordering explicit instead of
// relying on the registration order of defers.
type gateTransition struct {
	writeBlocked *bool
	stopReason   string
	onStop       func(string)
	onResume     func()
}

// markBlocked latches a write-block edge. Only a gate that was previously
// writable produces one, so steady state reports nothing.
func (t *gateTransition) markBlocked(canWrite *atomic.Bool) {
	if canWrite.Swap(false) {
		blocked := true
		t.writeBlocked = &blocked
	}
}

// markAllowed latches a write-resume edge for a previously blocked gate.
func (t *gateTransition) markAllowed(canWrite *atomic.Bool) {
	if !canWrite.Swap(true) {
		blocked := false
		t.writeBlocked = &blocked
	}
}

// fire delivers the collected edges. Callers must hold no gate lock.
func (t gateTransition) fire(g *EmergencyGate) {
	if t.onStop != nil {
		t.onStop(t.stopReason)
	}
	if t.onResume != nil {
		t.onResume()
	}
	if t.writeBlocked != nil {
		g.publishWriteState(*t.writeBlocked)
	}
}

// NewEmergencyGate creates an EmergencyGate instance initially allowing writes.
func NewEmergencyGate(logger *zap.Logger) *EmergencyGate {
	if logger == nil {
		logger = zap.NewNop()
	}
	g := &EmergencyGate{
		logger: logger,
	}
	g.canWrite.Store(true)
	return g
}

// SetCallbacks configures lifecycle event listeners for gate state changes.
func (g *EmergencyGate) SetCallbacks(onStop func(string), onResume func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.onEmergencyStop = onStop
	g.onEmergencyResume = onResume
}

// RegisterObserver adds a listener for filesystem write-block transitions. Unlike
// SetCallbacks it is additive on purpose: the audit recorder and the host logger
// both need the same transition, and a second SetCallbacks call would silently
// replace the first.
//
// Observers run with the gate unlocked and must not block; the storage service
// calls back into the gate from its own callback path.
func (g *EmergencyGate) RegisterObserver(observer func(blocked bool)) {
	if observer == nil {
		return
	}
	g.mu.Lock()
	g.observers = append(g.observers, observer)
	g.mu.Unlock()
}

// CanWrite reports atomically whether video recorders and file sinks are allowed to create new files.
func (g *EmergencyGate) CanWrite() bool {
	return g.canWrite.Load()
}

// IsEmergency reports whether the gate is in an emergency write-stopped state.
func (g *EmergencyGate) IsEmergency() bool {
	return g.isEmergency.Load()
}

// Reason returns the description of why writes are suspended.
func (g *EmergencyGate) Reason() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.reason
}

// BlockWrites suspends writes when storage telemetry itself is unavailable, for
// example when the media root can no longer be stat-ed.
//
// It shares the emergency state with Evaluate, so a later healthy probe clears
// the block and fires the resume callback exactly once. Without this, an
// uninspectable media root would report canWrite=false in the status payload while
// the gate kept letting writers create files.
func (g *EmergencyGate) BlockWrites(reason string) {
	g.mu.Lock()
	prevEmergency := g.isEmergency.Swap(true)

	var transition gateTransition
	transition.markBlocked(&g.canWrite)
	g.reason = reason
	if !prevEmergency {
		transition.onStop = g.onEmergencyStop
		transition.stopReason = reason
	}
	g.mu.Unlock()

	g.logger.Error("storage gate: writes blocked; telemetry unavailable", zap.String("reason", reason))
	transition.fire(g)
}

// publishWriteState reports a write-block transition. It must be called with the
// gate mutex released, because observers may call back into the gate.
func (g *EmergencyGate) publishWriteState(blocked bool) {
	g.mu.RLock()
	observers := append([]func(bool){}, g.observers...)
	g.mu.RUnlock()
	for _, observer := range observers {
		observer(blocked)
	}
}

// Evaluate checks the current filesystem stats against configured emergency thresholds.
func (g *EmergencyGate) Evaluate(stats *FSStats, cfg *StorageConfig, isDismounted bool) StorageHealthStatus {
	health, transition := g.evaluateLocked(stats, cfg, isDismounted)
	transition.fire(g)

	return health
}

// evaluateLocked mutates the gate and reports which edges fired. The caller owns
// delivering them after the mutex is released.
func (g *EmergencyGate) evaluateLocked(
	stats *FSStats,
	cfg *StorageConfig,
	isDismounted bool,
) (StorageHealthStatus, gateTransition) {
	var transition gateTransition

	g.mu.Lock()
	defer g.mu.Unlock()

	if stats == nil || cfg == nil {
		transition.markBlocked(&g.canWrite)
		g.reason = "storage telemetry unavailable"
		return StatusError, transition
	}
	if isDismounted {
		transition.markBlocked(&g.canWrite)
		g.reason = "external mount point disconnected (fallthrough hazard)"
		g.logger.Error("storage gate: dismount fallthrough hazard detected; writes blocked",
			zap.String("path", stats.MountPoint),
			zap.Uint64("device_id", stats.DeviceID),
		)
		return StatusError, transition
	}

	if stats.IsReadOnly {
		transition.markBlocked(&g.canWrite)
		g.reason = "filesystem is mounted read-only"
		g.logger.Error("storage gate: filesystem is read-only", zap.String("mount", stats.MountPoint))
		return StatusError, transition
	}

	usage := usagePercent(stats)

	minFreeBytes := uint64(cfg.EmergencyStopMinMB) * 1024 * 1024
	emergencyTriggered := usage >= cfg.EmergencyStopPercent || stats.FreeBytes < minFreeBytes

	if emergencyTriggered {
		prevEmergency := g.isEmergency.Swap(true)
		transition.markBlocked(&g.canWrite)

		reason := "insufficient disk space: free space below minimum safe margin"
		if usage >= cfg.EmergencyStopPercent {
			reason = "insufficient disk space: usage exceeded emergency limit"
		}
		g.reason = reason

		if !prevEmergency {
			g.logger.Warn("storage gate: emergency write stop activated",
				zap.Int("usage_percent", usage),
				zap.Uint64("free_bytes", stats.FreeBytes),
				zap.Int64("min_free_mb", cfg.EmergencyStopMinMB),
			)
			transition.onStop = g.onEmergencyStop
			transition.stopReason = reason
		}
		return StatusEmergencyStopped, transition
	}

	// Space is within safe zone; check if we are recovering from emergency stop
	wasEmergency := g.isEmergency.Swap(false)
	transition.markAllowed(&g.canWrite)
	g.reason = ""

	if wasEmergency {
		g.logger.Info("storage gate: emergency write stop cleared; resuming normal operation",
			zap.Int("usage_percent", usage),
			zap.Uint64("free_bytes", stats.FreeBytes),
		)
		transition.onResume = g.onEmergencyResume
	}

	if usage >= cfg.HighWatermarkPercent {
		return StatusWarning, transition
	}

	return StatusHealthy, transition
}
