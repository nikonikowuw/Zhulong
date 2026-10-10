package storage

import (
	"sync"
	"sync/atomic"

	"go.uber.org/zap"
)

// EmergencyGate guards against filesystem exhaustion by regulating write permissions.
type EmergencyGate struct {
	mu           sync.RWMutex
	logger       *zap.Logger
	canWrite     atomic.Bool
	isEmergency  atomic.Bool
	isDismounted atomic.Bool
	reason       string

	onEmergencyStop   func(reason string)
	onEmergencyResume func()
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

// Evaluate checks the current filesystem stats against configured emergency thresholds.
func (g *EmergencyGate) Evaluate(stats *FSStats, cfg *StorageConfig, isDismounted bool) (health StorageHealthStatus) {
	var stopCallback func(string)
	var resumeCallback func()
	var stopReason string

	defer func() {
		if stopCallback != nil {
			stopCallback(stopReason)
		}
		if resumeCallback != nil {
			resumeCallback()
		}
	}()

	g.mu.Lock()
	defer g.mu.Unlock()

	if stats == nil || cfg == nil {
		g.canWrite.Store(false)
		g.reason = "storage telemetry unavailable"
		return StatusError
	}

	if isDismounted {
		g.isDismounted.Store(true)
		g.canWrite.Store(false)
		g.reason = "external mount point disconnected (fallthrough hazard)"
		g.logger.Error("storage gate: dismount fallthrough hazard detected; writes blocked",
			zap.String("path", stats.MountPoint),
			zap.Uint64("device_id", stats.DeviceID),
		)
		return StatusError
	}
	g.isDismounted.Store(false)

	if stats.IsReadOnly {
		g.canWrite.Store(false)
		g.reason = "filesystem is mounted read-only"
		g.logger.Error("storage gate: filesystem is read-only", zap.String("mount", stats.MountPoint))
		return StatusError
	}

	var usagePercent int
	if stats.TotalBytes > 0 {
		usagePercent = int((stats.UsedBytes * 100) / stats.TotalBytes)
	}

	minFreeBytes := uint64(cfg.EmergencyStopMinMB) * 1024 * 1024
	emergencyTriggered := usagePercent >= cfg.EmergencyStopPercent || stats.FreeBytes < minFreeBytes

	if emergencyTriggered {
		prevEmergency := g.isEmergency.Swap(true)
		g.canWrite.Store(false)
		reason := "insufficient disk space: "
		if usagePercent >= cfg.EmergencyStopPercent {
			reason += "usage exceeded emergency limit"
		} else {
			reason += "free space below minimum safe margin"
		}
		g.reason = reason

		if !prevEmergency {
			g.logger.Warn("storage gate: emergency write stop activated",
				zap.Int("usage_percent", usagePercent),
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
	g.canWrite.Store(true)
	g.reason = ""

	if wasEmergency {
		g.logger.Info("storage gate: emergency write stop cleared; resuming normal operation",
			zap.Int("usage_percent", usagePercent),
			zap.Uint64("free_bytes", stats.FreeBytes),
		)
		resumeCallback = g.onEmergencyResume
	}

	if usagePercent >= cfg.HighWatermarkPercent {
		return StatusWarning
	}

	return StatusHealthy
}
