//go:build !linux

package algorunner

import (
	"context"

	"go.uber.org/zap"
)

// OrphanCleanupReport summarises what a startup sweep reclaimed.
type OrphanCleanupReport struct {
	Killed         []int
	RemovedSockets []string
	RemovedShm     []string
	Skipped        []int
}

// ReapOrphanRunners is a no-op on platforms without a /proc filesystem. The
// Linux implementation walks /proc to identify reparented runners and the
// artefacts they left behind; there is no portable equivalent that could do so
// without guessing, and guessing here would risk killing an unrelated process.
func ReapOrphanRunners(ctx context.Context, runnerPath string, logger *zap.Logger) (OrphanCleanupReport, error) {
	return OrphanCleanupReport{}, nil
}
