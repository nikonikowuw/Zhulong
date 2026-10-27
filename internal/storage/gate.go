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
	wasWritable := g.canWrite.Swap(false)
	g.reason = reason
	onStop := g.onEmergencyStop
	g.mu.Unlock()

	g.logger.Error("storage gate: writes blocked; telemetry unavailable", zap.String("reason", reason))

	if !prevEmergency && onStop != nil {
		onStop(reason)
	}
	if wasWritable {
		g.publishWriteState(true)
	}
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
	var stopCallback func(string)
	var resumeCallback func()
	var stopReason string
	var writeState *bool

	// block and allow latch the write-block state so observers hear about edges
	// only. Evaluate runs on every storage tick; a record per tick would be the
	// volume the logger is being asked to shed.
	block := func() {
		if g.canWrite.Swap(false) {
			value := true
			writeState = &value
		}
	}
	allow := func() {
		// Swap returns the previous value: only a previously blocked gate is a
		// transition worth reporting.
		if !g.canWrite.Swap(true) {
			value := false
			writeState = &value
		}
	}

	// This defer is registered before the unlock defer so listeners run after the
	// lock is released; reordering the two would deadlock any listener that calls
	// back into the gate.
	defer func() {
		if stopCallback != nil {
			stopCallback(stopReason)
		}
		if resumeCallback != nil {
			resumeCallback()
		}
		if writeState != nil {
			g.publishWriteState(*writeState)
		}
	}()

	g.mu.Lock()
	defer g.mu.Unlock()

	if stats == nil || cfg == nil {
		block()
		g.reason = "storage telemetry unavailable"
		return StatusError
	}
	if isDismounted {
		block()
		g.reason = "external mount point disconnected (fallthrough hazard)"
		g.logger.Error("storage gate: dismount fallthrough hazard detected; writes blocked",
			zap.String("path", stats.MountPoint),
			zap.Uint64("device_id", stats.DeviceID),
		)
		return StatusError
	}

	if stats.IsReadOnly {
		block()
		g.reason = "filesystem is mounted read-only"
		g.logger.Error("storage gate: filesystem is read-only", zap.String("mount", stats.MountPoint))
		return StatusError
	}

	usage := percentUsed(stats.UsedBytes, stats.TotalBytes)

	minFreeBytes := uint64(cfg.EmergencyStopMinMB) * 1024 * 1024
	emergencyTriggered := usage >= cfg.EmergencyStopPercent || stats.FreeBytes < minFreeBytes

	if emergencyTriggered {
		prevEmergency := g.isEmergency.Swap(true)
		block()

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
			stopCallback = g.onEmergencyStop
			stopReason = reason
		}
		return StatusEmergencyStopped
	}

	// Space is within safe zone; check if we are recovering from emergency stop
	wasEmergency := g.isEmergency.Swap(false)
	allow()
	g.reason = ""

	if wasEmergency {
		g.logger.Info("storage gate: emergency write stop cleared; resuming normal operation",
			zap.Int("usage_percent", usage),
			zap.Uint64("free_bytes", stats.FreeBytes),
		)
		resumeCallback = g.onEmergencyResume
	}

	if usage >= cfg.HighWatermarkPercent {
		return StatusWarning
	}

	return StatusHealthy
}
