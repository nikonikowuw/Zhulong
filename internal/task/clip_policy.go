package task

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/schedule"
	"go.uber.org/zap"
)

type clipPolicySnapshot struct {
	policies map[regionKey]ClipConfig
	// recorderPreMS 是每个相机所有启用录像的任务前段的最大值（毫秒）。
	recorderPreMS map[string]int64
}

// ClipPolicyCache holds an atomic snapshot of enabled clip-recording policies indexed by
// (cameraID, algorithmID), plus the per-camera recorder pre-roll derived from them.
//
// 与 RegionConfigCache 同构：全量重建、单条解析失败只 warn 跳过、实现 TaskChangeListener。
// 只收录「任务启用 + 算法启用 + clip.enabled」的项；其余视为关闭。
type ClipPolicyCache struct {
	store    *Store
	logger   *zap.Logger
	reloadMu sync.Mutex
	snapshot atomic.Pointer[clipPolicySnapshot]
	onReload atomic.Pointer[func(context.Context)]
}

// NewClipPolicyCache constructs a new ClipPolicyCache.
// ⚠️ Pure constructor: does not perform DB queries or open resources.
func NewClipPolicyCache(store *Store, logger *zap.Logger) *ClipPolicyCache {
	if logger == nil {
		logger = zap.NewNop()
	}
	cache := &ClipPolicyCache{store: store, logger: logger}
	cache.snapshot.Store(&clipPolicySnapshot{
		policies:      map[regionKey]ClipConfig{},
		recorderPreMS: map[string]int64{},
	})
	return cache
}

// SetOnReload registers a hook invoked after every successful Reload (e.g. the app layer
// wires StreamHub.RefreshRecorders here; task does not depend on camera). nil clears it.
// The hook runs synchronously on the reloading goroutine and must not call Reload.
func (c *ClipPolicyCache) SetOnReload(fn func(ctx context.Context)) {
	if fn == nil {
		c.onReload.Store(nil)
		return
	}
	c.onReload.Store(&fn)
}

// PolicyFor returns the enabled clip policy for the given camera and algorithm.
// Returns (zero value, false) when recording is disabled or the pair is unknown.
func (c *ClipPolicyCache) PolicyFor(cameraID, algorithmID string) (ClipConfig, bool) {
	snap := c.snapshot.Load()
	cfg, ok := snap.policies[regionKey{cameraID: cameraID, algorithmID: algorithmID}]
	return cfg, ok
}

// RecorderFor returns the recorder pre-roll for a camera: the maximum PreSeconds (in ms)
// over its enabled clip policies. enabled=false means no buffer should be allocated.
func (c *ClipPolicyCache) RecorderFor(cameraID string) (preMs int64, enabled bool) {
	snap := c.snapshot.Load()
	preMs, enabled = snap.recorderPreMS[cameraID]
	return preMs, enabled
}

// OnTaskChanged implements TaskChangeListener. Errors are logged rather than propagated,
// because the task mutation that triggered the notification has already been committed.
func (c *ClipPolicyCache) OnTaskChanged(ctx context.Context, _ string) {
	if err := c.Reload(ctx); err != nil {
		c.logger.Warn("failed to reload clip policy cache on task change", zap.Error(err))
	}
}

// Reload queries all tasks, compiles enabled clip policies, atomically replaces the
// snapshot and then invokes the optional reload hook.
func (c *ClipPolicyCache) Reload(ctx context.Context) error {
	if c.store == nil {
		return nil
	}

	c.reloadMu.Lock()
	tasks, err := c.store.ListTasks(ctx)
	if err != nil {
		c.reloadMu.Unlock()
		return fmt.Errorf("list tasks for clip policy cache: %w", err)
	}

	next := &clipPolicySnapshot{
		policies:      make(map[regionKey]ClipConfig),
		recorderPreMS: make(map[string]int64),
	}
	now := time.Now()
	for i := range tasks {
		t := &tasks[i]
		if !t.Enabled {
			continue
		}
		for j := range t.Algorithms {
			algo := &t.Algorithms[j]
			if !algo.Enabled {
				continue
			}
			sched, schedErr := schedule.Parse(algo.ScheduleJSON)
			if schedErr == nil && sched != nil && !sched.IsActiveAt(now) {
				continue
			}
			cfg, err := ParseClipConfig(algo.ClipConfigJSON)
			if err != nil {
				c.logger.Warn(
					"skip invalid clip config in cache reload",
					zap.String("task_id", t.ID),
					zap.String("camera_id", t.CameraID),
					zap.String("algorithm_id", algo.AlgorithmID),
					zap.Error(err),
				)
				continue
			}
			if !cfg.Enabled {
				continue
			}
			next.policies[regionKey{cameraID: t.CameraID, algorithmID: algo.AlgorithmID}] = cfg
			if pre := cfg.PreMS(); pre > next.recorderPreMS[t.CameraID] {
				next.recorderPreMS[t.CameraID] = pre
			}
		}
	}
	c.snapshot.Store(next)
	c.reloadMu.Unlock()

	c.logger.Debug(
		"clip policy cache reloaded",
		zap.Int("active_policies", len(next.policies)),
		zap.Int("recording_cameras", len(next.recorderPreMS)),
	)
	if hook := c.onReload.Load(); hook != nil {
		(*hook)(ctx)
	}
	return nil
}
