package record

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	// DefaultRetentionSweepInterval 是感知记录保留清理的默认巡检间隔。
	//
	// 6 小时是刻意的「慢」：保留天数以天为单位，用户改配置到实际生效有半天延迟
	// 完全可以接受；而这条路径要写 SQLite（与 AI 结果落库共用 WAL writer），
	// 跑得越勤对实时链路的干扰越大。
	DefaultRetentionSweepInterval = 6 * time.Hour

	// DefaultRetentionInitialDelay 是进程启动后第一轮清理前的延迟。
	//
	// 启动瞬间正在做迁移、算法 Runner 拉起、首次拉流与抓拍探针，这里没必要再抢 IO。
	// 也让「刚重启的机器」先稳定服务，再开始删东西。
	DefaultRetentionInitialDelay = 2 * time.Minute

	// DefaultRetentionBatchSize 是单条 DELETE 的批量上限。
	DefaultRetentionBatchSize = 2000

	// retentionBatchPause 是两批 DELETE 之间的让渡时长。
	// 与 storage.CleanerEngine 的批次让步同源：不让长期占用 SQLite 写锁。
	retentionBatchPause = 20 * time.Millisecond

	// retentionSweepTimeout 是单轮清理的总超时。
	retentionSweepTimeout = 5 * time.Minute

	// MaxRetentionDays 与 storage 的 snapshotsRetentionDays 上限保持一致。
	MaxRetentionDays = 730
)

// RetentionDaysProvider 提供抓拍（感知记录证据图）的保留天数。
//
// 实现方是 app 层对 storage.Service 的适配器：record 域不认识 StorageConfig，
// 也不需要认识 —— 它只关心「证据图还能活几天」。
// 返回 <= 0 表示保留策略未启用，此时清理一轮都不跑。
type RetentionDaysProvider interface {
	SnapshotRetentionDays(ctx context.Context) (int, error)
}

// SnapshotFileChecker reports whether a persisted snapshot reference still resolves to a file.
type SnapshotFileChecker interface {
	SnapshotFileExists(ctx context.Context, relativePath string) (bool, error)
}

// PruneResult 是一轮保留清理的结果。
type PruneResult struct {
	// Cutoff 是本次生效的截止时间（captured_at 早于它的普通识别/抓拍行可被清理）。
	Cutoff time.Time
	// 告警行属于高价值证据，不由自动保留清理删除；Alarms 始终为 0。
	Recognitions int64
	Alarms       int64
	Captures     int64
}

// Total 返回本轮共删除的行数。
func (r PruneResult) Total() int64 {
	return r.Recognitions + r.Alarms + r.Captures
}

// RetentionOptions 允许调用方覆盖巡检间隔与批量大小（主要用于测试）。
type RetentionOptions struct {
	// Interval 为两次巡检的间隔，<= 0 时使用 DefaultRetentionSweepInterval。
	Interval time.Duration
	// InitialDelay 为首轮巡检前的延迟，<= 0 时使用 DefaultRetentionInitialDelay。
	// ⚠️ 零值含义是「用默认值」而不是「立即」：配置项忘记填写时不得让启动瞬间跑清理。
	// 测试要即时巡检请传一个极小正值（如 time.Nanosecond）。
	InitialDelay time.Duration
	// BatchSize 为单条 DELETE 的上限，<= 0 时使用 DefaultRetentionBatchSize。
	BatchSize int
}

// RetentionPruner 按抓拍保留期清理已失去快照文件的普通感知记录行。
//
// 文件由 storage 按 TTL/水位策略删除；record 只删除已经不再引用任何快照文件的
// 普通识别与抓拍行。告警行和图片属于高价值证据，不参加自动 TTL 删除。
type RetentionPruner struct {
	store    *Store
	logger   *zap.Logger
	interval time.Duration
	delay    time.Duration
	batch    int

	mu          sync.Mutex
	provider    RetentionDaysProvider
	fileChecker SnapshotFileChecker
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	started     bool
}

// NewRetentionPruner 构造保留清理组件。
//
// ⚠️ 纯构造函数：不启动 goroutine、不读配置、不碰数据库。由 Start 拉起后台巡检，
// 与 Fx「构造函数必须纯粹」的约定一致。
func NewRetentionPruner(store *Store, logger *zap.Logger, opts ...RetentionOptions) *RetentionPruner {
	if logger == nil {
		logger = zap.NewNop()
	}

	interval := DefaultRetentionSweepInterval
	delay := DefaultRetentionInitialDelay
	batch := DefaultRetentionBatchSize

	if len(opts) > 0 {
		if opts[0].Interval > 0 {
			interval = opts[0].Interval
		}
		if opts[0].InitialDelay > 0 {
			delay = opts[0].InitialDelay
		}
		if opts[0].BatchSize > 0 {
			batch = opts[0].BatchSize
		}
	}

	return &RetentionPruner{
		store:    store,
		logger:   logger.Named("record.retention"),
		interval: interval,
		delay:    delay,
		batch:    batch,
	}
}

// SetRetentionDaysProvider 注入保留天数来源。
//
// 用 setter 而不是构造参数，理由与 SetSnapshotStore 相同：保留天数来自 storage 域，
// 把它塞进构造函数会让 record 域反过来依赖存储的生命周期顺序。
// 未注入时 PruneNow 是安全的空操作 —— 宁可不删，也不能靠猜的天数删证据。
func (p *RetentionPruner) SetRetentionDaysProvider(provider RetentionDaysProvider) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.provider = provider
}

// SetSnapshotFileChecker injects the safe media-file existence check.
func (p *RetentionPruner) SetSnapshotFileChecker(checker SnapshotFileChecker) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fileChecker = checker
}

// Start 拉起后台巡检。（幂等：重复调用只启动一个 goroutine）
func (p *RetentionPruner) Start(parent context.Context) {
	if p == nil || p.store == nil {
		return
	}
	if parent == nil {
		parent = context.Background()
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	p.cancel = cancel
	p.started = true

	p.wg.Add(1)
	go p.run(ctx)
}

// Stop 终止后台巡检并等待正在进行的批次收尾。（幂等）
//
// ⚠️ 被中断时已删除的行不会回滚（每批都是独立事务），这是可接受的：保留清理本来
// 就是幂等的，下轮会从剩下的过期行继续。
func (p *RetentionPruner) Stop() {
	if p == nil {
		return
	}

	p.mu.Lock()
	cancel := p.cancel
	p.cancel = nil
	p.started = false
	p.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	p.wg.Wait()
}

// PruneNow 立即执行一轮清理，返回各表删除行数。
//
// 未注入 provider、provider 返回 <= 0、或 store 为空时，直接返回零值结果与 nil 错误
// —— 「没有保留策略」不是错误，调用方无需区分。
func (p *RetentionPruner) PruneNow(ctx context.Context) (PruneResult, error) {
	if p == nil || p.store == nil {
		return PruneResult{}, nil
	}

	p.mu.Lock()
	provider := p.provider
	fileChecker := p.fileChecker
	p.mu.Unlock()

	if provider == nil {
		return PruneResult{}, nil
	}

	days, err := provider.SnapshotRetentionDays(ctx)
	if err != nil {
		return PruneResult{}, fmt.Errorf("read snapshot retention days: %w", err)
	}
	if days <= 0 {
		return PruneResult{}, nil
	}
	// 防御性收敛：配置校验已限制 1..730，这里兜住「未来新增配置源忘记校验」的情况。
	if days > MaxRetentionDays {
		days = MaxRetentionDays
	}

	if fileChecker == nil {
		return PruneResult{}, nil
	}

	cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
	result := PruneResult{Cutoff: cutoff}

	targets := []struct {
		table string
		count *int64
	}{
		{tableRecognitions, &result.Recognitions},
		{tableCaptures, &result.Captures},
	}

	for _, target := range targets {
		deleted, pruneErr := p.deleteExpiredRows(ctx, target.table, cutoff, fileChecker)
		*target.count = deleted
		if pruneErr != nil {
			return result, fmt.Errorf("prune %s: %w", target.table, pruneErr)
		}
	}

	return result, nil
}

func (p *RetentionPruner) deleteExpiredRows(
	ctx context.Context,
	table string,
	cutoff time.Time,
	checker SnapshotFileChecker,
) (int64, error) {
	var total int64
	var cursor *retentionCursor

	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}

		rows, err := p.store.retentionCandidatesBatch(ctx, table, cutoff, cursor, p.batch)
		if err != nil {
			return total, err
		}
		if len(rows) == 0 {
			return total, nil
		}

		ids := make([]string, 0, len(rows))
		for _, row := range rows {
			paths := []string{row.ImagePath, row.SecondaryPath}
			allMissing := true
			for _, path := range paths {
				if path == "" {
					continue
				}
				exists, err := checker.SnapshotFileExists(ctx, path)
				if err != nil {
					return total, fmt.Errorf("check snapshot %q for row %s: %w", path, row.ID, err)
				}
				if exists {
					allMissing = false
					break
				}
			}
			if allMissing {
				ids = append(ids, row.ID)
			}
		}

		deleted, err := p.store.deleteRetentionRows(ctx, table, cutoff, ids)
		if err != nil {
			return total, err
		}
		total += deleted
		last := rows[len(rows)-1]
		cursor = &retentionCursor{CapturedAt: last.CapturedAt, ID: last.ID}

		if len(rows) < p.batch {
			return total, nil
		}
		if err := sleepCtx(ctx, retentionBatchPause); err != nil {
			return total, err
		}
	}
}

// run 是后台巡检循环。首轮延迟后立即跑一次，之后按 interval 周期执行。
func (p *RetentionPruner) run(ctx context.Context) {
	defer p.wg.Done()

	if p.delay > 0 {
		if err := sleepCtx(ctx, p.delay); err != nil {
			return
		}
	}

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		p.sweep(ctx)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// sweep 执行一轮清理并记录结果，错误只记日志。
func (p *RetentionPruner) sweep(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, retentionSweepTimeout)
	defer cancel()

	result, err := p.PruneNow(ctx)
	if err != nil {
		// 停机导致的取消不是故障，不要污染日志。
		if parent.Err() != nil {
			return
		}
		p.logger.Warn("perception record retention sweep failed", zap.Error(err))
		return
	}
	if result.Total() == 0 {
		return
	}

	p.logger.Info("pruned expired perception records",
		zap.Time("cutoff", result.Cutoff),
		zap.Int64("recognitions", result.Recognitions),
		zap.Int64("alarms", result.Alarms),
		zap.Int64("captures", result.Captures),
		zap.Int64("total", result.Total()),
	)
}

// sleepCtx 在等待 d 的同时响应 ctx 取消。
func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
