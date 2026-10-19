package task

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/nikonikowuw/Zhulong/internal/region"
	"go.uber.org/zap"
)

// RegionKey identifies a region configuration entry by camera and algorithm.
type RegionKey struct {
	CameraID    string
	AlgorithmID string
}

// RegionConfigCache holds an atomic snapshot of active region configurations indexed by (cameraID, algorithmID).
type RegionConfigCache struct {
	store    *Store
	logger   *zap.Logger
	snapshot atomic.Pointer[map[RegionKey]region.Config]
}

// NewRegionConfigCache constructs a new RegionConfigCache.
// ⚠️ Pure constructor: does not perform DB queries or open resources.
func NewRegionConfigCache(store *Store, logger *zap.Logger) *RegionConfigCache {
	if logger == nil {
		logger = zap.NewNop()
	}
	cache := &RegionConfigCache{
		store:  store,
		logger: logger,
	}
	empty := make(map[RegionKey]region.Config)
	cache.snapshot.Store(&empty)
	return cache
}

// ConfigFor returns the region configuration for the given camera and algorithm.
// If not found or if the task carries no custom configuration, returns (zero value, false).
func (c *RegionConfigCache) ConfigFor(cameraID, algorithmID string) (region.Config, bool) {
	snap := c.snapshot.Load()
	if snap == nil {
		return region.Config{}, false
	}
	cfg, ok := (*snap)[RegionKey{CameraID: cameraID, AlgorithmID: algorithmID}]
	return cfg, ok
}

// OnTaskChanged implements TaskChangeListener so the cache refreshes itself whenever a task
// is created, updated or deleted. Errors are logged here rather than propagated, because the
// task mutation that triggered the notification has already been committed.
func (c *RegionConfigCache) OnTaskChanged(ctx context.Context, _ string) {
	if err := c.Reload(ctx); err != nil {
		c.logger.Warn("failed to reload region config cache on task change", zap.Error(err))
	}
}

// Reload queries all active tasks from the store, compiles their region configurations,
// and atomically replaces the current snapshot.
func (c *RegionConfigCache) Reload(ctx context.Context) error {
	if c.store == nil {
		return nil
	}

	tasks, err := c.store.ListTasks(ctx)
	if err != nil {
		return fmt.Errorf("list tasks for region config cache: %w", err)
	}

	newMap := make(map[RegionKey]region.Config, len(tasks))
	for _, t := range tasks {
		// Only include enabled tasks with valid region configurations
		if !t.Enabled {
			continue
		}
		for _, algo := range t.Algorithms {
			if !algo.Enabled {
				continue
			}
			if algo.RegionConfigJSON == "" || algo.RegionConfigJSON == "{}" {
				continue
			}
			cfg, err := region.ParseConfig(algo.RegionConfigJSON)
			if err != nil {
				c.logger.Warn("skip invalid region config in cache reload",
					zap.String("task_id", t.ID),
					zap.String("camera_id", t.CameraID),
					zap.String("algorithm_id", algo.AlgorithmID),
					zap.Error(err),
				)
				continue
			}
			if !cfg.IsEmpty() {
				newMap[RegionKey{CameraID: t.CameraID, AlgorithmID: algo.AlgorithmID}] = cfg
			}
		}
	}

	c.snapshot.Store(&newMap)
	c.logger.Debug("region config cache reloaded", zap.Int("active_configs", len(newMap)))
	return nil
}
