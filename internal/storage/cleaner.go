package storage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// ErrCleanupInProgress is returned when a cleanup cycle is already running.
var ErrCleanupInProgress = errors.New("cleanup is already in progress")

// PruneTrigger names why a cleanup cycle started. It decides whether the high
// watermark gate applies: a manual or emergency run proceeds unconditionally,
// an automatic watermark run only when usage has reached the high watermark.
type PruneTrigger string

// Prune triggers recorded in CleanupSummary.TriggerReason.
const (
	PruneTriggerManual    PruneTrigger = "manual"
	PruneTriggerWatermark PruneTrigger = "watermark"
	PruneTriggerEmergency PruneTrigger = "emergency"
)

// PruneStatus is the outcome code reported in CleanupSummary.TargetStatus.
type PruneStatus string

// Prune outcomes reported in CleanupSummary.TargetStatus.
const (
	PruneStatusCompleted  PruneStatus = "completed"
	PruneStatusSkippedLow PruneStatus = "skipped_below_high_watermark"
)

// PruneStopReason explains why a cycle returned, especially when it deleted 0 files.
type PruneStopReason string

// Stop reasons reported in CleanupSummary.StopReason.
const (
	PruneStopTargetReached       PruneStopReason = "target_reached"
	PruneStopCandidatesExhausted PruneStopReason = "candidates_exhausted"
	PruneStopContextCanceled     PruneStopReason = "context_canceled"
	PruneStopSkippedBelowHigh    PruneStopReason = "skipped_below_high_watermark"
)

// EvidenceGuard reports whether a snapshot ID belongs to a high-value alarm
// record that must never be deleted by automatic capacity eviction.
//
// It is deliberately a function type rather than a dependency on the record
// domain: storage only knows "this ID is protected", not why.
type EvidenceGuard func(ctx context.Context, ids []string) (map[string]bool, error)

const (
	defaultPruneBatchSize  = 25
	defaultPruneBatchSleep = 50 * time.Millisecond
	defaultActiveFileGrace = 2 * time.Minute
	// lockMarkerSuffix names the sidecar file that protects a media file.
	lockMarkerSuffix = ".lock"
	// lockedNameInfix additionally protects files whose name carries the marker.
	lockedNameInfix = "_locked"
)

type candidateFile struct {
	path    string
	size    int64
	modTime time.Time
}

type pruneKind uint8

const (
	unitExport pruneKind = iota
	unitRecording
	unitSnapshot
)

// pruneUnit is an atomic deletion unit. A snapshot has one ID shared by its
// main/crop/face files; recordings and exports are only grouped for deletion.
type pruneUnit struct {
	kind      pruneKind
	id        string
	files     []candidateFile
	size      int64
	oldest    time.Time
	newest    time.Time
	protected bool
}

// pruneScope is the shared context of a single prune cycle. Phase 1 does not
// consult stopWhen; Phase 2 does.
type pruneScope struct {
	mediaDir      string
	stopWhen      func(*FSStats) bool
	evidenceGuard EvidenceGuard
}

// pruneOutcome is the aggregate result of deleting one phase.
type pruneOutcome struct {
	deleted            int
	freed              int64
	protectedFiles     int
	protectedBytes     int64
	deletedSnapshotIDs []string
	stopped            bool
	canceled           bool
}

// CleanerEngine coordinates watermark hysteresis pruning and throttled file deletion.
type CleanerEngine struct {
	running    atomic.Bool
	logger     *zap.Logger
	inspector  PathInspector
	batchSize  int
	batchSleep time.Duration

	activeFileGrace time.Duration
	capacityProbe   func(path string) (*FSStats, error)

	guardMu       sync.RWMutex
	evidenceGuard EvidenceGuard

	// fileRemover is an optional hook for testing.
	fileRemover func(path string) error
}

// CleanerOptions allows customization of batching, throttling, activity grace,
// and the lightweight capacity probe.
//
// The zero value keeps every default. BatchSleep and ActiveFileGrace are
// pointers so "not set" (nil) can be told apart from an explicit zero: a caller
// that only tunes BatchSize must not silently lose the ext4 journal-friendly
// throttle or the active-file guard.
type CleanerOptions struct {
	BatchSize       int
	BatchSleep      *time.Duration
	ActiveFileGrace *time.Duration
	CapacityProbe   func(path string) (*FSStats, error)
}

// NewCleanerEngine creates a new hysteresis cleaner engine.
func NewCleanerEngine(inspector PathInspector, logger *zap.Logger, opts ...CleanerOptions) *CleanerEngine {
	if logger == nil {
		logger = zap.NewNop()
	}

	engine := &CleanerEngine{
		logger:          logger,
		inspector:       inspector,
		batchSize:       defaultPruneBatchSize,
		batchSleep:      defaultPruneBatchSleep,
		activeFileGrace: defaultActiveFileGrace,
		fileRemover:     os.Remove,
	}
	if inspector != nil {
		// Tests construct the engine with their mock inspector; production wiring
		// overrides this with the statfs-only probe via CleanerOptions.
		engine.capacityProbe = inspector.InspectFS
	}

	for _, opt := range opts {
		if opt.BatchSize > 0 {
			engine.batchSize = opt.BatchSize
		}
		if opt.BatchSleep != nil {
			engine.batchSleep = *opt.BatchSleep
		}
		if opt.ActiveFileGrace != nil {
			engine.activeFileGrace = *opt.ActiveFileGrace
		}
		if opt.CapacityProbe != nil {
			engine.capacityProbe = opt.CapacityProbe
		}
	}

	return engine
}

// IsCleaning reports whether a background or manual cleanup run is currently executing.
func (c *CleanerEngine) IsCleaning() bool {
	return c.running.Load()
}

// SetEvidenceGuard installs the optional alarm-evidence guard.
func (c *CleanerEngine) SetEvidenceGuard(guard EvidenceGuard) {
	if c == nil {
		return
	}
	c.guardMu.Lock()
	c.evidenceGuard = guard
	c.guardMu.Unlock()
}

func (c *CleanerEngine) currentEvidenceGuard() EvidenceGuard {
	if c == nil {
		return nil
	}
	c.guardMu.RLock()
	defer c.guardMu.RUnlock()

	return c.evidenceGuard
}

// RunPrune executes a prune cycle based on the configured watermarks and
// retention policies.
//
// Phase 1 (TTL-expired exports/recordings/snapshots) always runs to completion;
// Phase 2 (unexpired recordings/snapshots) only runs while its stop predicate is
// not satisfied. This keeps "TTL is a ceiling" and "don't over-delete under
// pressure" from contradicting each other.
func (c *CleanerEngine) RunPrune(
	ctx context.Context,
	mediaDir string,
	cfg *StorageConfig,
	trigger PruneTrigger,
) (*CleanupSummary, error) {
	if c == nil || c.inspector == nil {
		return nil, errors.New("storage cleaner is not configured")
	}
	if cfg == nil {
		return nil, errors.New("storage config is nil")
	}
	if !c.running.CompareAndSwap(false, true) {
		return nil, ErrCleanupInProgress
	}
	defer c.running.Store(false)

	startTime := time.Now()
	now := startTime.UTC()
	summary := &CleanupSummary{TriggerReason: trigger}

	stats, err := c.inspector.InspectFS(mediaDir)
	if err != nil {
		return nil, fmt.Errorf("inspect filesystem before prune: %w", err)
	}

	usage := usagePercent(stats)
	if trigger == PruneTriggerWatermark && usage < cfg.HighWatermarkPercent {
		summary.StopReason = PruneStopSkippedBelowHigh
		return finishSummary(summary, startTime, PruneStatusSkippedLow), nil
	}

	c.logger.Info("starting storage cleanup cycle",
		zap.String("reason", string(trigger)),
		zap.Int("current_usage", usage),
		zap.Int("target_low", cfg.LowWatermarkPercent),
		zap.Uint64("free_bytes", stats.FreeBytes),
	)

	exportUnits := c.collectExportUnits(mediaDir, time.Duration(cfg.ExportsRetentionHours)*time.Hour, now)
	snapshotExpired, snapshotUnexpired := c.collectGroupedUnits(
		mediaDir,
		"snapshots",
		unitSnapshot,
		time.Duration(cfg.SnapshotsRetentionDays)*24*time.Hour,
		now,
	)
	recordingExpired, recordingUnexpired := c.collectGroupedUnits(
		mediaDir,
		"recordings",
		unitRecording,
		time.Duration(cfg.RecordingsRetentionDays)*24*time.Hour,
		now,
	)

	scope := pruneScope{
		mediaDir:      mediaDir,
		stopWhen:      c.stopPredicate(cfg, stats.TotalBytes, trigger),
		evidenceGuard: c.currentEvidenceGuard(),
	}

	// Phase 1: all expired media, regardless of capacity stop.
	phase1 := make([]pruneUnit, 0, len(exportUnits)+len(recordingExpired)+len(snapshotExpired))
	phase1 = append(phase1, exportUnits...)
	phase1 = append(phase1, recordingExpired...)
	phase1 = append(phase1, snapshotExpired...)
	phase1Outcome := c.deleteUnits(ctx, phase1, scope, false)
	summary.mergeOutcome(phase1Outcome)

	if phase1Outcome.canceled || ctx.Err() != nil {
		summary.StopReason = PruneStopContextCanceled
	}

	if summary.StopReason == "" {
		if trigger == PruneTriggerEmergency && emergencyReserveInfeasible(cfg, stats.TotalBytes) {
			// Deleting unexpired media cannot make free >= minFree on this
			// volume. Do not wipe the disk in a futile attempt; the gate stays
			// latched and the startup warning points at the bad configuration.
			summary.StopReason = PruneStopCandidatesExhausted
			c.logger.Warn("emergency reserve is not feasible for this volume; skipping capacity eviction",
				zap.Int64("emergency_stop_min_mb", cfg.EmergencyStopMinMB),
				zap.Uint64("volume_total_bytes", stats.TotalBytes),
			)
		} else {
			// Phase 2: capacity pressure, with a pre-check before the first unit.
			phase2 := make([]pruneUnit, 0, len(recordingUnexpired)+len(snapshotUnexpired))
			phase2 = append(phase2, recordingUnexpired...)
			phase2 = append(phase2, snapshotUnexpired...)
			outcome := c.deleteUnits(ctx, phase2, scope, true)
			summary.mergeOutcome(outcome)
			switch {
			case outcome.canceled:
				summary.StopReason = PruneStopContextCanceled
			case outcome.stopped:
				summary.StopReason = PruneStopTargetReached
			default:
				summary.StopReason = PruneStopCandidatesExhausted
			}
		}
	}

	summary = finishSummary(summary, startTime, PruneStatusCompleted)

	c.logger.Info("finished storage cleanup cycle",
		zap.String("reason", string(trigger)),
		zap.Int("deleted_files", summary.DeletedFiles),
		zap.Int64("freed_bytes", summary.FreedBytes),
		zap.Int("protected_files", summary.ProtectedFiles),
		zap.Int64("protected_bytes", summary.ProtectedBytes),
		zap.String("stop_reason", string(summary.StopReason)),
		zap.Int64("duration_ms", summary.DurationMs),
	)

	return summary, nil
}

func (c *CleanerEngine) stopPredicate(cfg *StorageConfig, totalBytes uint64, trigger PruneTrigger) func(*FSStats) bool {
	if trigger == PruneTriggerEmergency {
		return emergencyTarget(cfg, totalBytes)
	}

	return lowWatermarkTarget(cfg.LowWatermarkPercent)
}

func (s *CleanupSummary) mergeOutcome(outcome pruneOutcome) {
	s.DeletedFiles += outcome.deleted
	s.FreedBytes += outcome.freed
	s.ProtectedFiles += outcome.protectedFiles
	s.ProtectedBytes += outcome.protectedBytes
	s.DeletedSnapshotIDs = append(s.DeletedSnapshotIDs, outcome.deletedSnapshotIDs...)
}

// finishSummary stamps the cycle outcome, duration, and completion time.
func finishSummary(summary *CleanupSummary, startTime time.Time, status PruneStatus) *CleanupSummary {
	summary.DurationMs = time.Since(startTime).Milliseconds()
	summary.FinishedAt = time.Now().UTC()
	summary.TargetStatus = status

	return summary
}

func lowWatermarkTarget(low int) func(*FSStats) bool {
	if low < 0 {
		low = 0
	}
	if low > 100 {
		low = 100
	}

	return func(s *FSStats) bool {
		return s != nil && usagePercent(s) <= low
	}
}

// emergencyTarget must cover both gate trigger branches:
//  1. FreeBytes < EmergencyStopMinMB
//  2. usagePercent >= EmergencyStopPercent
//
// A free-only stop is not enough: a percent-triggered emergency can already
// satisfy the free target while usage is still above the emergency line, and
// the gate would never clear. Stopping below the high watermark also avoids an
// immediate warning-cycle chain.
func emergencyReserveInfeasible(cfg *StorageConfig, totalBytes uint64) bool {
	return totalBytes > 0 && uint64(cfg.EmergencyStopMinMB)*1024*1024*5/4 >= totalBytes
}

func emergencyTarget(cfg *StorageConfig, totalBytes uint64) func(*FSStats) bool {
	freeTarget := uint64(cfg.EmergencyStopMinMB) * 1024 * 1024 * 5 / 4
	if emergencyReserveInfeasible(cfg, totalBytes) {
		return func(*FSStats) bool { return true }
	}
	if cap := totalBytes * 9 / 10; cap > 0 && freeTarget > cap {
		freeTarget = cap
	}

	usageTarget := cfg.HighWatermarkPercent
	if usageTarget < 1 || usageTarget > 100 {
		usageTarget = 100
	}

	return func(s *FSStats) bool {
		return s != nil && s.FreeBytes >= freeTarget && usagePercent(s) < usageTarget
	}
}

func (c *CleanerEngine) stopReached(scope pruneScope) (bool, error) {
	if scope.stopWhen == nil {
		return false, nil
	}
	if c.capacityProbe == nil {
		return false, errors.New("capacity probe is not configured")
	}

	stats, err := c.capacityProbe(scope.mediaDir)
	if err != nil {
		return false, err
	}
	if stats == nil {
		return false, errors.New("capacity probe returned nil stats")
	}

	return scope.stopWhen(stats), nil
}

// deleteUnits removes one phase of units in batches. phase 1 passes
// checkStop=false; phase 2 passes true.
func (c *CleanerEngine) deleteUnits(
	ctx context.Context,
	units []pruneUnit,
	scope pruneScope,
	checkStop bool,
) pruneOutcome {
	var outcome pruneOutcome

	if checkStop {
		reached, err := c.stopReached(scope)
		if err != nil {
			c.logger.Warn("capacity stop probe failed; refusing to delete more media",
				zap.Error(err),
			)
			// A failed probe is not "target reached": keep the stop reason at
			// candidates_exhausted so the service backs off instead of resetting.
			return outcome
		}
		if reached {
			outcome.stopped = true
			return outcome
		}
	}

	batchSize := c.batchSize
	if batchSize <= 0 {
		batchSize = defaultPruneBatchSize
	}

	for start := 0; start < len(units); start += batchSize {
		end := start + batchSize
		if end > len(units) {
			end = len(units)
		}
		batch := units[start:end]
		guardProtected := c.guardBatch(ctx, batch, scope.evidenceGuard)

		for _, unit := range batch {
			if ctx.Err() != nil {
				outcome.canceled = true
				return outcome
			}

			if unit.kind != unitExport && (unit.protected || guardProtected[unit.id] || c.unitProtected(unit)) {
				outcome.protectedFiles += len(unit.files)
				outcome.protectedBytes += unit.size
				continue
			}
			if len(unit.files) == 0 {
				continue
			}

			allGone := true
			for _, file := range unit.files {
				err := c.fileRemover(file.path)
				switch {
				case err == nil:
					outcome.deleted++
					outcome.freed += file.size
				case os.IsNotExist(err):
					// Already gone; the unit can still be safely removed from the DB.
				default:
					allGone = false
					c.logger.Warn("failed to delete storage candidate file",
						zap.String("path", file.path),
						zap.Error(err),
					)
				}
			}

			// Best effort: drop an empty parent directory. Siblings normally keep
			// it alive, so the error is deliberately discarded.
			dirs := make(map[string]struct{}, len(unit.files))
			for _, file := range unit.files {
				dirs[filepath.Dir(file.path)] = struct{}{}
			}
			for dir := range dirs {
				_ = os.Remove(dir)
			}

			if allGone && unit.kind == unitSnapshot {
				outcome.deletedSnapshotIDs = appendUnique(outcome.deletedSnapshotIDs, unit.id)
			}

			if !checkStop {
				continue
			}
			reached, err := c.stopReached(scope)
			if err != nil {
				c.logger.Warn("capacity stop probe failed after deleting a unit",
					zap.Error(err),
				)
				return outcome
			}
			if reached {
				outcome.stopped = true
				return outcome
			}
		}

		if c.batchSleep > 0 {
			select {
			case <-time.After(c.batchSleep):
			case <-ctx.Done():
				outcome.canceled = true
				return outcome
			}
		}
	}

	return outcome
}

func (c *CleanerEngine) guardBatch(ctx context.Context, batch []pruneUnit, guard EvidenceGuard) map[string]bool {
	protected := make(map[string]bool)
	if guard == nil {
		return protected
	}

	ids := make([]string, 0, len(batch))
	for _, unit := range batch {
		if unit.kind == unitSnapshot && !unit.protected && len(unit.files) > 0 {
			ids = append(ids, unit.id)
		}
	}
	if len(ids) == 0 {
		return protected
	}

	matched, err := guard(ctx, ids)
	if err != nil {
		// Fail closed: an unavailable alarm database must not turn into deleting
		// historical evidence.
		c.logger.Error("snapshot evidence guard failed; protecting the batch",
			zap.Int("ids", len(ids)),
			zap.Error(err),
		)
		for _, id := range ids {
			protected[id] = true
		}
		return protected
	}
	for id, isProtected := range matched {
		if isProtected {
			protected[id] = true
		}
	}

	return protected
}

func (c *CleanerEngine) unitProtected(unit pruneUnit) bool {
	for _, file := range unit.files {
		if isCleanupProtected(filepath.Base(file.path), file.path) {
			return true
		}
	}

	return false
}

func appendUnique(ids []string, id string) []string {
	if id == "" {
		return ids
	}
	if slices.Contains(ids, id) {
		return ids
	}

	return append(ids, id)
}

// collectExportUnits collects expired exports, oldest first. Exports are exempt
// from .lock protection: they are user-requested temporary clips.
func (c *CleanerEngine) collectExportUnits(mediaDir string, ttl time.Duration, now time.Time) []pruneUnit {
	threshold := now.Add(-ttl)
	units := make([]pruneUnit, 0)

	walkMediaFiles(filepath.Join(mediaDir, "exports"), func(path string, d fs.DirEntry) {
		info, err := d.Info()
		if err != nil || !info.ModTime().Before(threshold) {
			return
		}

		file := candidateFile{path: path, size: info.Size(), modTime: info.ModTime()}
		units = append(units, pruneUnit{
			kind:   unitExport,
			files:  []candidateFile{file},
			size:   file.size,
			oldest: file.modTime,
			newest: file.modTime,
		})
	})

	return sortUnitsOldestFirst(units)
}

// collectGroupedUnits groups snapshots or recordings by (directory, base
// prefix) and splits them into expired and unexpired queues.
func (c *CleanerEngine) collectGroupedUnits(
	mediaDir string,
	subdir string,
	kind pruneKind,
	ttl time.Duration,
	now time.Time,
) (expired []pruneUnit, unexpired []pruneUnit) {
	cutoff := now.Add(-ttl)
	groups := make(map[string]*pruneUnit)

	walkMediaFiles(filepath.Join(mediaDir, subdir), func(path string, d fs.DirEntry) {
		name := d.Name()
		info, err := d.Info()
		if err != nil {
			return
		}

		dir := filepath.Dir(path)
		prefix := mediaPrefix(name)
		if prefix == "" {
			return
		}
		key := dir + "\x00" + prefix

		unit := groups[key]
		if unit == nil {
			unit = &pruneUnit{kind: kind, id: prefix}
			groups[key] = unit
		}

		if strings.HasSuffix(name, lockMarkerSuffix) {
			// A lock sidecar protects the whole group but is not a media file.
			unit.protected = true
			return
		}

		file := candidateFile{path: path, size: info.Size(), modTime: info.ModTime()}
		unit.files = append(unit.files, file)
		unit.size += file.size
		if unit.oldest.IsZero() || file.modTime.Before(unit.oldest) {
			unit.oldest = file.modTime
		}
		if unit.newest.IsZero() || file.modTime.After(unit.newest) {
			unit.newest = file.modTime
		}
		if isCleanupProtected(name, path) {
			unit.protected = true
		}
	})

	expired = make([]pruneUnit, 0)
	unexpired = make([]pruneUnit, 0)
	for _, unit := range groups {
		if len(unit.files) == 0 {
			continue
		}
		if c.activeFileGrace > 0 && now.Sub(unit.newest) < c.activeFileGrace {
			// A group with a live file (snapshot being written or recording being
			// appended) is not a deletion candidate yet.
			continue
		}
		if unit.newest.Before(cutoff) {
			expired = append(expired, *unit)
			continue
		}
		unexpired = append(unexpired, *unit)
	}

	return sortUnitsOldestFirst(expired), sortUnitsOldestFirst(unexpired)
}

// mediaPrefix derives the grouping key from a file name:
// "01J8XYZ_crop.jpg" → "01J8XYZ"; "alarm.jpg.lock" → "alarm".
func mediaPrefix(name string) string {
	stripped := strings.TrimSuffix(name, lockMarkerSuffix)
	ext := filepath.Ext(stripped)
	base := strings.TrimSuffix(stripped, ext)
	prefix := strings.SplitN(base, "_", 2)[0]

	return strings.TrimSpace(prefix)
}

// isCleanupProtected reports whether a media file must survive cleanup.
//
// Two markers count: a name carrying the ".lock" suffix or the "_locked" infix,
// and a sibling "<name>.lock" sidecar that an operator or the alert pipeline
// dropped next to the evidence.
func isCleanupProtected(name, path string) bool {
	if strings.HasSuffix(name, lockMarkerSuffix) || strings.Contains(name, lockedNameInfix) {
		return true
	}

	// Anything other than a clean "not exists" protects the file: a permission
	// error while looking for the marker must not turn into deleting evidence.
	_, err := os.Lstat(path + lockMarkerSuffix)
	return !os.IsNotExist(err)
}

// walkMediaFiles visits every regular file under root.
//
// Walk errors are deliberately ignored: a missing or unreadable subtree simply
// contributes no candidates, and the prune cycle reports its own outcome.
func walkMediaFiles(root string, visit func(path string, d fs.DirEntry)) {
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}

		visit(path, d)
		return nil
	})
}

// sortUnitsOldestFirst orders units by their oldest member mtime.
func sortUnitsOldestFirst(units []pruneUnit) []pruneUnit {
	slices.SortStableFunc(units, func(a, b pruneUnit) int {
		return a.oldest.Compare(b.oldest)
	})

	return units
}
