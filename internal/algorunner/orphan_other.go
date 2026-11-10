//go:build !linux

package algorunner

import (
	"context"

	"go.uber.org/zap"
)

// ReapOrphanRunners is a no-op on platforms without a /proc filesystem. The
// Linux implementation walks /proc to identify reparented runners and the
// artefacts they left behind; there is no portable equivalent that could do so
// without guessing, and guessing here would risk killing an unrelated process.
//
// The report shape lives in orphan_report.go, not here: the fields are one
// contract across platforms and must not be copied per build tag.
func ReapOrphanRunners(ctx context.Context, algorithmsDir string, logger *zap.Logger) (OrphanCleanupReport, error) {
	return emptyOrphanCleanupReport(), nil
}
