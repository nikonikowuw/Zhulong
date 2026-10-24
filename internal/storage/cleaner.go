package storage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// ErrCleanupInProgress is returned when a cleanup cycle is already running.
var ErrCleanupInProgress = errors.New("cleanup is already in progress")

// Prune trigger reasons. They are recorded in CleanupSummary.TriggerReason and
// decide whether the high watermark gate is bypassed.
const (
	PruneTriggerManual    = "manual"
	PruneTriggerWatermark = "watermark"
)

// Prune outcome codes reported in CleanupSummary.TargetStatus.
const (
	PruneStatusCompleted  = "completed"
	PruneStatusSkippedLow = "skipped_below_high_watermark"
)

const (
	defaultPruneBatchSize  = 25
	defaultPruneBatchSleep = 50 * time.Millisecond
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

// pruneResult is the outcome of deleting one candidate batch.
type pruneResult struct {
	deleted int
	freed   int64
}

// pruneScope is the shared context of a single prune cycle: the volume being
// cleaned and the watermark its batches stop at.
type pruneScope struct {
	mediaDir     string
	lowWatermark int
}

// CleanerEngine coordinates watermark hysteresis pruning and throttled file deletion.
type CleanerEngine struct {
	running    atomic.Bool
	logger     *zap.Logger
	inspector  PathInspector
	batchSize  int
	batchSleep time.Duration

	// fileRemover is an optional hook for testing.
	fileRemover func(path string) error
}

// CleanerOptions allows customization of batching and throttling.
type CleanerOptions struct {
	BatchSize  int
	BatchSleep time.Duration
}

// NewCleanerEngine creates a new hysteresis cleaner engine.
func NewCleanerEngine(inspector PathInspector, logger *zap.Logger, opts ...CleanerOptions) *CleanerEngine {
	if logger == nil {
		logger = zap.NewNop()
	}

	engine := &CleanerEngine{
		logger:      logger,
		inspector:   inspector,
		batchSize:   defaultPruneBatchSize,
		batchSleep:  defaultPruneBatchSleep,
		fileRemover: os.Remove,
	}

	if len(opts) > 0 {
		if opts[0].BatchSize > 0 {
			engine.batchSize = opts[0].BatchSize
		}
		if opts[0].BatchSleep >= 0 {
			engine.batchSleep = opts[0].BatchSleep
		}
	}

	return engine
}

// IsCleaning reports whether a background or manual cleanup run is currently executing.
func (c *CleanerEngine) IsCleaning() bool {
	return c.running.Load()
}

// RunPrune executes a prune cycle based on the configured watermarks and
// retention policies.
//
// triggerReason decides whether the high watermark gate applies: a manual run
// proceeds unconditionally, an automatic one only when usage has reached the
// high watermark.
func (c *CleanerEngine) RunPrune(
	ctx context.Context,
	mediaDir string,
	cfg *StorageConfig,
	triggerReason string,
) (*CleanupSummary, error) {
	if !c.running.CompareAndSwap(false, true) {
		return nil, ErrCleanupInProgress
	}
	defer c.running.Store(false)

	startTime := time.Now()
	summary := &CleanupSummary{TriggerReason: triggerReason}

	stats, err := c.inspector.InspectFS(mediaDir)
	if err != nil {
		return nil, fmt.Errorf("inspect filesystem before prune: %w", err)
	}

	usage := percentUsed(stats.UsedBytes, stats.TotalBytes)
	isManual := triggerReason == PruneTriggerManual

	// Automatic watermark triggers must not delete anything below the high mark.
	if !isManual && usage < cfg.HighWatermarkPercent {
		return finishSummary(summary, startTime, PruneStatusSkippedLow), nil
	}

	c.logger.Info("starting storage cleanup cycle",
		zap.String("reason", triggerReason),
		zap.Int("current_usage", usage),
		zap.Int("target_low", cfg.LowWatermarkPercent),
	)

	scope := pruneScope{mediaDir: mediaDir, lowWatermark: cfg.LowWatermarkPercent}

	// Step 1: collect candidates.
	exportsCandidates := c.collectExports(mediaDir, time.Duration(cfg.ExportsRetentionHours)*time.Hour)
	snapshotsCandidates := c.collectSnapshots(mediaDir, time.Duration(cfg.SnapshotsRetentionDays)*24*time.Hour)
	recordingsCandidates := c.collectRecordings(mediaDir, time.Duration(cfg.RecordingsRetentionDays)*24*time.Hour)

	// Step 2: expired exports first.
	result := c.deleteBatch(ctx, exportsCandidates, scope)
	summary.DeletedFiles += result.deleted
	summary.FreedBytes += result.freed

	// Step 3: AI 抓拍按 TTL 清理。放在 exports 之后、recordings 之前：抓拍图在
	// 「证据价值」排序上低于导出（人工产物）而高于常规录像，与 storage-guidelines
	// §4 的删除顺序一致。
	if ctx.Err() == nil {
		result = c.deleteBatch(ctx, snapshotsCandidates, scope)
		summary.DeletedFiles += result.deleted
		summary.FreedBytes += result.freed
	}

	// Step 4: only fall back to recordings while still above the low watermark.
	if ctx.Err() == nil && (isManual || c.isAboveLowWatermark(mediaDir, cfg.LowWatermarkPercent)) {
		result = c.deleteBatch(ctx, recordingsCandidates, scope)
		summary.DeletedFiles += result.deleted
		summary.FreedBytes += result.freed
	}

	summary = finishSummary(summary, startTime, PruneStatusCompleted)

	c.logger.Info("finished storage cleanup cycle",
		zap.Int("deleted_files", summary.DeletedFiles),
		zap.Int64("freed_bytes", summary.FreedBytes),
		zap.Int64("duration_ms", summary.DurationMs),
	)

	return summary, nil
}

// finishSummary stamps the cycle outcome, duration, and completion time.
func finishSummary(summary *CleanupSummary, startTime time.Time, status string) *CleanupSummary {
	summary.DurationMs = time.Since(startTime).Milliseconds()
	summary.FinishedAt = time.Now().UTC()
	summary.TargetStatus = status

	return summary
}

func (c *CleanerEngine) isAboveLowWatermark(mediaDir string, lowWatermark int) bool {
	stats, err := c.inspector.InspectFS(mediaDir)
	if err != nil || stats.TotalBytes == 0 {
		return false
	}

	return percentUsed(stats.UsedBytes, stats.TotalBytes) > lowWatermark
}

func (c *CleanerEngine) deleteBatch(ctx context.Context, files []candidateFile, scope pruneScope) pruneResult {
	var result pruneResult

	for i, f := range files {
		if ctx.Err() != nil {
			break
		}

		err := c.fileRemover(f.path)
		switch {
		case err == nil:
			result.deleted++
			result.freed += f.size
		case !os.IsNotExist(err):
			c.logger.Warn("failed to delete storage candidate file",
				zap.String("path", f.path),
				zap.Error(err),
			)
		}

		// Best effort: drop the parent directory once it is empty. This fails
		// whenever siblings remain, which is the normal case, so the error is
		// deliberately discarded.
		_ = os.Remove(filepath.Dir(f.path))

		// Throttling: yield CPU and ext4 journal locks every batch.
		if (i+1)%c.batchSize != 0 {
			continue
		}

		if c.batchSleep > 0 {
			select {
			case <-time.After(c.batchSleep):
			case <-ctx.Done():
				return result
			}
		}

		// Check whether the volume already fell back below the low watermark.
		if !c.isAboveLowWatermark(scope.mediaDir, scope.lowWatermark) {
			c.logger.Debug("reached below low watermark; stopping batch delete early",
				zap.Int("deleted", result.deleted),
			)
			break
		}
	}

	return result
}

// collectExports collects exports older than ttl, oldest first.
func (c *CleanerEngine) collectExports(mediaDir string, ttl time.Duration) []candidateFile {
	threshold := time.Now().Add(-ttl)

	candidates := []candidateFile{}
	walkMediaFiles(filepath.Join(mediaDir, "exports"), func(path string, d fs.DirEntry) {
		info, err := d.Info()
		if err != nil || !info.ModTime().Before(threshold) {
			return
		}

		candidates = append(candidates, candidateFile{
			path:    path,
			size:    info.Size(),
			modTime: info.ModTime(),
		})
	})

	return sortOldestFirst(candidates)
}

// collectSnapshots collects AI snapshot images older than ttl.
//
// 与 recordings 的区别：
//   - 不参与水印驱动的「最旧优先」删除：抓拍总量远小于录像，TTL 到期才清；
//   - 带 .lock / _locked 保护标记的文件一律跳过（与 recordings 同一铁律），
//     这样告警证据可以被人为锁定而不被清理流程误杀。
func (c *CleanerEngine) collectSnapshots(mediaDir string, ttl time.Duration) []candidateFile {
	threshold := time.Now().Add(-ttl)

	candidates := []candidateFile{}
	walkMediaFiles(filepath.Join(mediaDir, "snapshots"), func(path string, d fs.DirEntry) {
		if isCleanupProtected(d.Name(), path) {
			return
		}

		info, err := d.Info()
		if err != nil || !info.ModTime().Before(threshold) {
			return
		}

		candidates = append(candidates, candidateFile{
			path:    path,
			size:    info.Size(),
			modTime: info.ModTime(),
		})
	})

	return sortOldestFirst(candidates)
}

// collectRecordings collects recordings for FIFO eviction, retention-expired
// files first and then the oldest remaining ones.
func (c *CleanerEngine) collectRecordings(mediaDir string, ttl time.Duration) []candidateFile {
	expiredThreshold := time.Now().Add(-ttl)

	expired := []candidateFile{}
	normal := []candidateFile{}
	walkMediaFiles(filepath.Join(mediaDir, "recordings"), func(path string, d fs.DirEntry) {
		if isCleanupProtected(d.Name(), path) {
			return
		}

		info, err := d.Info()
		if err != nil {
			return
		}

		item := candidateFile{
			path:    path,
			size:    info.Size(),
			modTime: info.ModTime(),
		}
		if info.ModTime().Before(expiredThreshold) {
			expired = append(expired, item)
			return
		}

		normal = append(normal, item)
	})

	// Expired first, then oldest normal recordings.
	return append(sortOldestFirst(expired), sortOldestFirst(normal)...)
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

// sortOldestFirst orders candidates so the oldest file is evicted first.
func sortOldestFirst(candidates []candidateFile) []candidateFile {
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].modTime.Before(candidates[j].modTime)
	})

	return candidates
}
