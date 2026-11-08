package task

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/nikonikowuw/Zhulong/internal/region"
	"go.uber.org/zap"
)

type regionKey struct {
	cameraID    string
	algorithmID string
}

// RegionConfigCache holds an atomic snapshot of active region configurations indexed by (cameraID, algorithmID).
type RegionConfigCache struct {
	store    *Store
	logger   *zap.Logger
	snapshot atomic.Pointer[map[regionKey]region.Config]
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
	empty := make(map[regionKey]region.Config)
	cache.snapshot.Store(&empty)
	return cache
}

// ConfigFor returns the region configuration for the given camera and algorithm.
// If not found or if the task carries no custom configuration, returns (zero value, false).
func (c *RegionConfigCache) ConfigFor(cameraID, algorithmID string) (region.Config, bool) {
	// 构造函数必然写入非 nil 快照，这里无需再判空。
	snap := c.snapshot.Load()
	cfg, ok := (*snap)[regionKey{cameraID: cameraID, algorithmID: algorithmID}]
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
//
// 契约：全量重建（每次任务变更都重新扫一遍启用中的任务）。
// 缓存规模与任务数同级、变更频率低，因此不做增量 diff。
func (c *RegionConfigCache) Reload(ctx context.Context) error {
	if c.store == nil {
		return nil
	}

	tasks, err := c.store.ListTasks(ctx)
	if err != nil {
		return fmt.Errorf("list tasks for region config cache: %w", err)
	}

	newMap := make(map[regionKey]region.Config, len(tasks))
	for _, t := range tasks {
		if !t.Enabled {
			continue
		}
		for _, algo := range t.Algorithms {
			if !algo.Enabled {
				continue
			}
			// ParseConfig 把空串与 "{}" 都视作"无自定义区域策略"，随后由 IsEmpty 统一过滤。
			cfg, err := region.ParseConfig(algo.RegionConfigJSON)
			if err != nil {
				c.logger.Warn(
					"skip invalid region config in cache reload",
					zap.String("task_id", t.ID),
					zap.String("camera_id", t.CameraID),
					zap.String("algorithm_id", algo.AlgorithmID),
					zap.Error(err),
				)
				continue
			}
			if !cfg.IsEmpty() {
				newMap[regionKey{cameraID: t.CameraID, algorithmID: algo.AlgorithmID}] = cfg
			}
		}
	}

	c.snapshot.Store(&newMap)
	c.logger.Debug("region config cache reloaded", zap.Int("active_configs", len(newMap)))
	return nil
}
