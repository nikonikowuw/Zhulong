package algorunner

// OrphanCleanupReport summarises what a startup sweep reclaimed. Every field is
// what was actually acted on, so an operator reading the log can tell a no-op
// sweep from one that reaped real leftovers.
//
// The type lives in an untagged file on purpose: the Linux sweep fills every
// field and the other platforms fill none, but the shape is one contract. Two
// definitions under build tags drift the moment a field is added to one side.
type OrphanCleanupReport struct {
	// Killed lists the PIDs of orphaned runners that were terminated.
	Killed []int
	// RemovedSockets lists broker socket files that no live listener owned.
	RemovedSockets []string
	// RemovedShm lists /dev/shm segments no live process still mapped.
	RemovedShm []string
	// Skipped lists PIDs that matched the runner binary but are owned by a live
	// host (the current process or another running instance) and were left alone.
	Skipped []int
	// UnverifiedShm lists segments that were left in place because the sweep could
	// not prove no live process still held them (unreadable /proc state). They are
	// reported instead of deleted: a false "nobody holds it" answer deletes a
	// segment a running stream still maps.
	UnverifiedShm []string
	// UnverifiedSockets is the socket counterpart of UnverifiedShm.
	UnverifiedSockets []string
}

// emptyOrphanCleanupReport returns a report with initialised slices, so callers
// can append and serialise it without a nil check.
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

// recordSocket folds one socket decision into the report.
func (r *OrphanCleanupReport) recordSocket(sock string, outcome socketSweepResult) []error {
	switch {
	case outcome.removed:
		r.RemovedSockets = append(r.RemovedSockets, sock)
	case outcome.err != nil:
		return []error{outcome.err}
	case outcome.unverified:
		r.UnverifiedSockets = append(r.UnverifiedSockets, sock)
	}
	return nil
}

// mergeSocketBatch folds one socket batch into the report.
func (r *OrphanCleanupReport) mergeSocketBatch(batch sweepBatch) []error {
	r.RemovedSockets = append(r.RemovedSockets, batch.removed...)
	r.UnverifiedSockets = append(r.UnverifiedSockets, batch.unverified...)
	return batch.failures
}

// mergeShmBatch folds one shm batch into the report.
func (r *OrphanCleanupReport) mergeShmBatch(batch sweepBatch) []error {
	r.RemovedShm = append(r.RemovedShm, batch.removed...)
	r.UnverifiedShm = append(r.UnverifiedShm, batch.unverified...)
	return batch.failures
}

// socketSweepResult is the outcome of one socket-file decision.
type socketSweepResult struct {
	removed    bool
	unverified bool
	err        error
}

// sweepBatch is the outcome of one artefact batch sweep: what was removed, what
// was left because ownership could not be proven, and what failed.
type sweepBatch struct {
	removed    []string
	unverified []string
	failures   []error
}

// newSweepBatch returns a batch with initialised slices, mirroring
// emptyOrphanCleanupReport.
func newSweepBatch() sweepBatch {
	return sweepBatch{
		removed:    []string{},
		unverified: []string{},
		failures:   []error{},
	}
}
