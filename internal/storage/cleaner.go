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
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

var (
	// ErrCleanupInProgress is returned when a cleanup cycle is already running.
	ErrCleanupInProgress = errors.New("cleanup is already in progress")
)

type candidateFile struct {
	path    string
	size    int64
	modTime time.Time
}

// CleanerEngine coordinates watermark hysteresis pruning and throttled file deletion.
type CleanerEngine struct {
	mu         sync.Mutex
	running    atomic.Bool
	logger     *zap.Logger
	inspector  PathInspector
	batchSize  int
	batchSleep time.Duration

	// Optional hook for testing
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
	batchSize := 25
	batchSleep := 50 * time.Millisecond
	if len(opts) > 0 {
		if opts[0].BatchSize > 0 {
			batchSize = opts[0].BatchSize
		}
		if opts[0].BatchSleep >= 0 {
			batchSleep = opts[0].BatchSleep
		}
	}

	return &CleanerEngine{
		logger:      logger,
		inspector:   inspector,
		batchSize:   batchSize,
		batchSleep:  batchSleep,
		fileRemover: os.Remove,
	}
}

// IsCleaning reports whether a background or manual cleanup run is currently executing.
func (c *CleanerEngine) IsCleaning() bool {
	return c.running.Load()
}

// RunPrune executes a prune cycle based on configured watermarks and retention policies.
// If force is true, it proceeds even if usage has not touched high watermark (e.g. manual trigger).
func (c *CleanerEngine) RunPrune(ctx context.Context, mediaDir string, cfg *StorageConfig, triggerReason string) (*CleanupSummary, error) {
	if !c.running.CompareAndSwap(false, true) {
		return nil, ErrCleanupInProgress
	}
	defer c.running.Store(false)

	startTime := time.Now()
	summary := &CleanupSummary{
		TriggerReason: triggerReason,
	}

	stats, err := c.inspector.InspectFS(mediaDir)
	if err != nil {
		return nil, fmt.Errorf("inspect filesystem before prune: %w", err)
	}

	var currentUsagePercent int
	if stats.TotalBytes > 0 {
		currentUsagePercent = int((stats.UsedBytes * 100) / stats.TotalBytes)
	}

	isManual := triggerReason == "manual"
	// For automatic watermark triggers, only activate when usage >= HighWatermarkPercent
	if !isManual && currentUsagePercent < cfg.HighWatermarkPercent {
		summary.DurationMs = time.Since(startTime).Milliseconds()
		summary.FinishedAt = time.Now().UTC()
		summary.TargetStatus = "skipped_below_high_watermark"
		return summary, nil
	}

	c.logger.Info("starting storage cleanup cycle",
		zap.String("reason", triggerReason),
		zap.Int("current_usage", currentUsagePercent),
		zap.Int("target_low", cfg.LowWatermarkPercent),
	)

	// Step 1: Collect candidates
	exportsCandidates := c.collectExports(mediaDir, time.Duration(cfg.ExportsRetentionHours)*time.Hour)
	recordingsCandidates := c.collectRecordings(mediaDir, time.Duration(cfg.RecordingsRetentionDays)*24*time.Hour)

	// Step 2: Delete expired exports first
	deleted, freed := c.deleteBatch(ctx, exportsCandidates, mediaDir, cfg.LowWatermarkPercent)
	summary.DeletedFiles += deleted
	summary.FreedBytes += freed

	// Step 3: If still above low watermark (or manual), delete expired/oldest recordings
	if ctx.Err() == nil && (isManual || c.isAboveLowWatermark(mediaDir, cfg.LowWatermarkPercent)) {
		recDeleted, recFreed := c.deleteBatch(ctx, recordingsCandidates, mediaDir, cfg.LowWatermarkPercent)
		summary.DeletedFiles += recDeleted
		summary.FreedBytes += recFreed
	}

	summary.DurationMs = time.Since(startTime).Milliseconds()
	summary.FinishedAt = time.Now().UTC()
	summary.TargetStatus = "completed"

	c.logger.Info("finished storage cleanup cycle",
		zap.Int("deleted_files", summary.DeletedFiles),
		zap.Int64("freed_bytes", summary.FreedBytes),
		zap.Int64("duration_ms", summary.DurationMs),
	)

	return summary, nil
}

func (c *CleanerEngine) isAboveLowWatermark(mediaDir string, lowWatermarkPercent int) bool {
	stats, err := c.inspector.InspectFS(mediaDir)
	if err != nil || stats.TotalBytes == 0 {
		return false
	}
	usage := int((stats.UsedBytes * 100) / stats.TotalBytes)
	return usage > lowWatermarkPercent
}

func (c *CleanerEngine) deleteBatch(ctx context.Context, files []candidateFile, mediaDir string, lowWatermark int) (int, int64) {
	deletedCount := 0
	var freedBytes int64

	for i, f := range files {
		if ctx != nil && ctx.Err() != nil {
			break
		}

		err := c.fileRemover(f.path)
		if err == nil {
			deletedCount++
			freedBytes += f.size
		} else if !os.IsNotExist(err) {
			c.logger.Warn("failed to delete storage candidate file", zap.String("path", f.path), zap.Error(err))
		}

		// Clean up parent directory if empty
		_ = os.Remove(filepath.Dir(f.path))

		// Throttling: yield CPU and ext4 journal locks every batch
		if (i+1)%c.batchSize == 0 {
			if c.batchSleep > 0 {
				select {
				case <-time.After(c.batchSleep):
				case <-ctx.Done():
					return deletedCount, freedBytes
				}
			}
			// Check if we already reached below low watermark
			if !c.isAboveLowWatermark(mediaDir, lowWatermark) {
				c.logger.Debug("reached below low watermark; stopping batch delete early", zap.Int("deleted", deletedCount))
				break
			}
		}
	}

	return deletedCount, freedBytes
}

func (c *CleanerEngine) collectExports(mediaDir string, ttl time.Duration) []candidateFile {
	exportsDir := filepath.Join(mediaDir, "exports")
	now := time.Now()
	threshold := now.Add(-ttl)

	var candidates []candidateFile
	_ = filepath.WalkDir(exportsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		// Any export older than TTL is a candidate
		if info.ModTime().Before(threshold) {
			candidates = append(candidates, candidateFile{
				path:    path,
				size:    info.Size(),
				modTime: info.ModTime(),
			})
		}
		return nil
	})

	// Oldest first
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].modTime.Before(candidates[j].modTime)
	})

	return candidates
}

func (c *CleanerEngine) collectRecordings(mediaDir string, ttl time.Duration) []candidateFile {
	recordingsDir := filepath.Join(mediaDir, "recordings")
	now := time.Now()
	expiredThreshold := now.Add(-ttl)

	var expiredCandidates []candidateFile
	var normalCandidates []candidateFile

	_ = filepath.WalkDir(recordingsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		// Skip locked recordings (explicit safety protection)
		name := d.Name()
		if strings.HasSuffix(name, ".lock") || strings.Contains(name, "_locked") {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		item := candidateFile{
			path:    path,
			size:    info.Size(),
			modTime: info.ModTime(),
		}

		if info.ModTime().Before(expiredThreshold) {
			expiredCandidates = append(expiredCandidates, item)
		} else {
			normalCandidates = append(normalCandidates, item)
		}
		return nil
	})

	// Sort oldest first (FIFO)
	sort.Slice(expiredCandidates, func(i, j int) bool {
		return expiredCandidates[i].modTime.Before(expiredCandidates[j].modTime)
	})
	sort.Slice(normalCandidates, func(i, j int) bool {
		return normalCandidates[i].modTime.Before(normalCandidates[j].modTime)
	})

	// Expired first, then oldest normal recordings
	return append(expiredCandidates, normalCandidates...)
}
