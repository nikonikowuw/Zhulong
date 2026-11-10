package algorunner

import (
	"sync"
	"time"

	"go.uber.org/zap"
)

// RollbackGuard manages observation period for newly upgraded packages.
type RollbackGuard struct {
	pkgID           string
	newVersion      string
	previousVersion string
	startedAt       time.Time
	guardWindow     time.Duration
	observedDone    bool
	mu              sync.Mutex
	logger          *zap.Logger
}

// RollbackGuardOptions identifies the freshly upgraded package and the window
// during which a crash still triggers a rollback.
//
// The version fields are named rather than positional on purpose: NewVersion and
// PreviousVersion are both plain strings, so an argument swap would invert the
// rollback direction with no compile error.
type RollbackGuardOptions struct {
	PackageID       string
	NewVersion      string
	PreviousVersion string
	// GuardWindow <= 0 falls back to DefaultRollbackGuardWindow.
	GuardWindow time.Duration
	Logger      *zap.Logger
}

// NewRollbackGuard creates a guard to observe newly upgraded package stability.
func NewRollbackGuard(opts RollbackGuardOptions) *RollbackGuard {
	logger := opts.Logger
	if logger == nil {
		logger = zap.NewNop()
	}

	guardWindow := opts.GuardWindow
	if guardWindow <= 0 {
		guardWindow = DefaultRollbackGuardWindow
	}

	return &RollbackGuard{
		pkgID:           opts.PackageID,
		newVersion:      opts.NewVersion,
		previousVersion: opts.PreviousVersion,
		startedAt:       time.Now(),
		guardWindow:     guardWindow,
		logger:          logger.With(zap.String("package", opts.PackageID)),
	}
}

// ShouldRollback returns true if a crash occurred during the initial observation window
// and a valid previous version exists.
func (g *RollbackGuard) ShouldRollback(crashTime time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.observedDone || g.previousVersion == "" {
		return false
	}
	// One decision per guard: record it once, then let the elapsed check decide.
	g.observedDone = true

	elapsed := crashTime.Sub(g.startedAt)
	if elapsed > g.guardWindow {
		return false
	}
	g.logger.Warn("new package version crashed within observation window, triggering rollback",
		zap.String("new_version", g.newVersion),
		zap.String("rollback_to_version", g.previousVersion),
		zap.Duration("elapsed", elapsed),
	)
	return true
}

// MarkStable marks the observation window as successfully completed without rollback.
func (g *RollbackGuard) MarkStable() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.observedDone = true
}

// PreviousVersion returns the fallback version. It is immutable after
// construction, so it needs no lock.
func (g *RollbackGuard) PreviousVersion() string {
	return g.previousVersion
}
