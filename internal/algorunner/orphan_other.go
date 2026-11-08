//go:build !linux

package algorunner

import (
	"context"

	"go.uber.org/zap"
)

// OrphanCleanupReport summarises what a startup sweep reclaimed.
//
// The fields mirror the Linux implementation one for one, including the ones this
// platform never fills in: a caller doing a portable sweep must be able to read
// UnverifiedShm / UnverifiedSockets without a build tag of its own.
type OrphanCleanupReport struct {
	// Killed lists the PIDs of orphaned runners that were terminated. Always empty
	// here: without /proc there is no way to identify an orphan safely.
	Killed []int
	// RemovedSockets lists broker socket files that no live listener owned.
	// Always empty here.
	RemovedSockets []string
	// RemovedShm lists /dev/shm segments no live process still mapped. Always empty
	// here: /dev/shm is a Linux concept.
	RemovedShm []string
	// Skipped lists PIDs that matched the runner binary but are owned by a live
	// host. Always empty here.
	Skipped []int
	// UnverifiedShm lists segments left in place because ownership could not be
	// proven. Always empty here.
	UnverifiedShm []string
	// UnverifiedSockets is the socket counterpart of UnverifiedShm. Always empty
	// here.
	UnverifiedSockets []string
}

// ReapOrphanRunners is a no-op on platforms without a /proc filesystem. The
// Linux implementation walks /proc to identify reparented runners and the
// artefacts they left behind; there is no portable equivalent that could do so
// without guessing, and guessing here would risk killing an unrelated process.
func ReapOrphanRunners(ctx context.Context, algorithmsDir string, logger *zap.Logger) (OrphanCleanupReport, error) {
	return emptyOrphanCleanupReport(), nil
}

func emptyOrphanCleanupReport() OrphanCleanupReport {
	return OrphanCleanupReport{
		Killed:            []int{},
		RemovedSockets:    []string{},
		RemovedShm:        []string{},
		Skipped:           []int{},
		UnverifiedShm:     []string{},
		UnverifiedSockets: []string{},
	}
}
