package task

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/idutil"
	"github.com/nikonikowuw/Zhulong/internal/schedule"
	"gorm.io/gorm"
)

var (
	// ErrNotFound 表示按 ID 或 camera_id 均未命中分析任务。
	ErrNotFound = errors.New("analysis task not found")

	// ErrTaskAlreadyExists 表示该摄像头已存在分析任务。camera_id 上的唯一约束是权威判定，
	// 单事务内的预检查只是快速失败。
	ErrTaskAlreadyExists = errors.New("analysis task already exists for camera")

	// ErrComputeBudgetExceeded 表示本次写入后总算力点会超过预算池上限。
	ErrComputeBudgetExceeded = errors.New("compute budget exceeded")

	// ErrInvalidScheduleConfig 表示时间计划 JSON 解析失败或时间范围非法。
	ErrInvalidScheduleConfig = errors.New("invalid schedule configuration")

	errStoreUnavailable = errors.New("analysis task store database is unavailable")
	errNilTask          = errors.New("analysis task is nil")
)

// Store handles persistence operations for analysis tasks.
type Store struct {
	dbProvider database.DBProvider
}

// NewStore creates a new Task Store instance.
func NewStore(dbProvider database.DBProvider) *Store {
	return &Store{dbProvider: dbProvider}
}

func (s *Store) db(ctx context.Context) (*gorm.DB, error) {
	if s.dbProvider == nil {
		return nil, errStoreUnavailable
	}
	db := s.dbProvider.DB()
	if db == nil {
		return nil, errStoreUnavailable
	}
	return db.WithContext(ctx), nil
}

// initTaskTimestamps 把主表与算法子项的时间戳统一收敛到 UTC，缺省时填当前时间。
func initTaskTimestamps(t *Task) {
	now := time.Now().UTC()
	t.CreatedAt = normalizeTimestamp(t.CreatedAt, now)
	t.UpdatedAt = normalizeTimestamp(t.UpdatedAt, now)
	for i := range t.Algorithms {
		t.Algorithms[i].CreatedAt = normalizeTimestamp(t.Algorithms[i].CreatedAt, now)
		t.Algorithms[i].UpdatedAt = normalizeTimestamp(t.Algorithms[i].UpdatedAt, now)
	}
}

func normalizeTimestamp(value, fallback time.Time) time.Time {
	if value.IsZero() {
		return fallback
	}
	return value.UTC()
}

// assignIDs 补全主表与算法子项的业务主键，并把父子关联钉死。
func assignIDs(t *Task) {
	if t.ID == "" {
		t.ID = idutil.New()
	}
	for i := range t.Algorithms {
		if t.Algorithms[i].ID == "" {
			t.Algorithms[i].ID = idutil.New()
		}
		t.Algorithms[i].TaskID = t.ID
	}
}

// CreateTask writes a task and its algorithms without any compute-budget check.
//
// 生产写入路径必须使用 CreateTaskWithinBudget 或 SaveTaskWithinBudget；本方法只服务于
// 种子数据与测试这类"无预算语义"的场景（例如引用尚未落库的算法行）。
func (s *Store) CreateTask(ctx context.Context, t *Task) error {
	if t == nil {
		return errNilTask
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	initTaskTimestamps(t)
	assignIDs(t)
	if err := gorm.G[Task](db).Create(ctx, t); err != nil {
		return fmt.Errorf("create analysis task: %w", err)
	}
	return nil
}

// CreateTaskWithinBudget creates a task ensuring total compute points do not exceed maxPoints.
func (s *Store) CreateTaskWithinBudget(ctx context.Context, task *Task, maxPoints int64) error {
	if task == nil {
		return errNilTask
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	initTaskTimestamps(task)

	return db.Transaction(func(tx *gorm.DB) error {
		assignIDs(task)
		if err := validateComputeBudget(
			tx,
			task,
			"",
			maxPoints,
		); err != nil {
			return err
		}
		if err := gorm.G[Task](tx).Create(ctx, task); err != nil {
			// camera_id 唯一约束只有在并发建同摄像头任务时才会暴露：事务外的预检查不是权威，
			// 这里靠一次探针查询归因，而不是匹配 SQLite 的错误字符串。
			if taken, probeErr := cameraTaskTaken(ctx, tx, task.CameraID); probeErr == nil && taken {
				return ErrTaskAlreadyExists
			}
			return fmt.Errorf("create analysis task: %w", err)
		}
		return nil
	})
}

// SaveTaskWithinBudget upserts a channel task and syncs its algorithm rows inside one transaction,
// rejecting the write when the total compute points would exceed maxPoints.
//
// 定位顺序：显式 ID 优先，其次按 camera_id（单摄像头单任务）。
// 两种路径都不会把任务挪到已被其它任务占用的摄像头上。
func (s *Store) SaveTaskWithinBudget(ctx context.Context, task *Task, maxPoints int64) error {
	if task == nil {
		return errNilTask
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	initTaskTimestamps(task)

	return db.Transaction(func(tx *gorm.DB) error {
		existing, err := locateTask(ctx, tx, task)
		if err != nil {
			return err
		}
		if err := ensureCameraAvailable(ctx, tx, task, existing); err != nil {
			return err
		}

		excludeID := ""
		if existing != nil {
			excludeID = existing.ID
			task.ID = existing.ID
			task.CreatedAt = existing.CreatedAt
		}
		assignIDs(task)

		if err := validateComputeBudget(
			tx,
			task,
			excludeID,
			maxPoints,
		); err != nil {
			return err
		}

		now := time.Now().UTC()
		task.UpdatedAt = now
		if err := writeTaskRow(ctx, tx, task, existing != nil); err != nil {
			return err
		}
		return syncTaskAlgorithms(ctx, tx, task, now)
	})
}

// locateTask 按显式 ID（优先）或 camera_id 定位待更新的既有任务，未命中返回 (nil, nil)。
func locateTask(ctx context.Context, tx *gorm.DB, task *Task) (*Task, error) {
	g := gorm.G[Task](tx).Scopes()
	if task.ID != "" {
		g = g.Where("id = ?", task.ID)
	} else {
		g = g.Where("camera_id = ?", task.CameraID)
	}
	existing, err := g.First(ctx)
	switch {
	case err == nil:
		return &existing, nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		return nil, nil
	}
	return nil, fmt.Errorf("locate analysis task: %w", err)
}

// ensureCameraAvailable 拦住"一对多"写入：目标 camera_id 已被另一个任务占用。
func ensureCameraAvailable(ctx context.Context, tx *gorm.DB, task *Task, existing *Task) error {
	owner, err := gorm.G[Task](tx).Where("camera_id = ?", task.CameraID).First(ctx)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return nil
	case err != nil:
		return fmt.Errorf("locate camera task owner: %w", err)
	case existing != nil && owner.ID == existing.ID:
		return nil
	}
	return ErrTaskAlreadyExists
}

// writeTaskRow 写主表行。更新走 map 形式，否则 enabled=false 会被 GORM 零值机制省略。
func writeTaskRow(ctx context.Context, tx *gorm.DB, task *Task, update bool) error {
	if !update {
		parent := *task
		parent.Algorithms = nil
		if err := gorm.G[Task](tx).Create(ctx, &parent); err != nil {
			return fmt.Errorf("insert analysis task: %w", err)
		}
		return nil
	}
	err := tx.Model(&Task{}).Where("id = ?", task.ID).Updates(map[string]any{
		"camera_id":  task.CameraID,
		"enabled":    task.Enabled,
		"name":       task.Name,
		"crop_json":  task.CropJSON,
		"updated_at": task.UpdatedAt,
	}).Error
	if err != nil {
		return fmt.Errorf("update analysis task: %w", err)
	}
	return nil
}

// syncTaskAlgorithms 先删除已移除的算法子项，再 upsert 保留下来的与新增的子项。
func syncTaskAlgorithms(ctx context.Context, tx *gorm.DB, task *Task, now time.Time) error {
	keepIDs := make([]string, 0, len(task.Algorithms))
	for _, sub := range task.Algorithms {
		keepIDs = append(keepIDs, sub.AlgorithmID)
	}

	prune := gorm.G[TaskAlgorithm](tx).Where("task_id = ?", task.ID)
	if len(keepIDs) > 0 {
		prune = prune.Where("algorithm_id NOT IN ?", keepIDs)
	}
	if _, err := prune.Delete(ctx); err != nil {
		return fmt.Errorf("prune removed task algorithms: %w", err)
	}

	for i := range task.Algorithms {
		if err := upsertTaskAlgorithm(ctx, tx, &task.Algorithms[i], now); err != nil {
			return err
		}
	}
	return nil
}

// upsertTaskAlgorithm 按 (task_id, algorithm_id) upsert 单条子项，并保留既有业务主键与创建时间。
func upsertTaskAlgorithm(ctx context.Context, tx *gorm.DB, sub *TaskAlgorithm, now time.Time) error {
	existing, err := gorm.G[TaskAlgorithm](tx).
		Where("task_id = ? AND algorithm_id = ?", sub.TaskID, sub.AlgorithmID).
		First(ctx)
	switch {
	case err == nil:
		sub.ID = existing.ID
		sub.CreatedAt = existing.CreatedAt
		sub.UpdatedAt = now
		columns := map[string]any{
			"enabled":            sub.Enabled,
			"sample_fps":         sub.SampleFPS,
			"config_json":        sub.ConfigJSON,
			"region_config_json": sub.RegionConfigJSON,
			"schedule_json":      sub.ScheduleJSON,
			"updated_at":         sub.UpdatedAt,
		}
		err := tx.Model(&TaskAlgorithm{}).Where("id = ?", sub.ID).Updates(columns).Error
		if err != nil {
			return fmt.Errorf("update task algorithm %q: %w", sub.AlgorithmID, err)
		}
		return nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		// 主键已由 assignIDs 在事务开头补全（syncTaskAlgorithms 只处理已登记的子项）。
		sub.CreatedAt = now
		sub.UpdatedAt = now
		if err := gorm.G[TaskAlgorithm](tx).Create(ctx, sub); err != nil {
			return fmt.Errorf("insert task algorithm %q: %w", sub.AlgorithmID, err)
		}
		return nil
	}
	return fmt.Errorf("locate task algorithm %q: %w", sub.AlgorithmID, err)
}

// cameraTaskTaken 报告该摄像头是否已存在分析任务。
func cameraTaskTaken(ctx context.Context, tx *gorm.DB, cameraID string) (bool, error) {
	count, err := gorm.G[Task](tx).Where("camera_id = ?", cameraID).Count(ctx, "*")
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// validateComputeBudget 累加"其它启用中的任务"与"本次任务的启用子项"所需算力点，
// 并按时间切片校验瞬时峰值。
func validateComputeBudget(tx *gorm.DB, task *Task, excludeTaskID string, maxPoints int64) error {
	items, err := otherTasksSchedulableItems(tx, excludeTaskID)
	if err != nil {
		return err
	}
	if task.Enabled {
		for _, sub := range task.Algorithms {
			if !sub.Enabled {
				continue
			}
			if sub.SampleFPS < 0 {
				return fmt.Errorf("%w: budget limit is %d", ErrComputeBudgetExceeded, maxPoints)
			}
			var algorithm struct {
				ID          string `gorm:"column:id"`
				ComputeCost int    `gorm:"column:compute_cost"`
			}
			err := tx.Table("algorithms").
				Select("id, compute_cost").
				Where("id = ?", sub.AlgorithmID).
				First(&algorithm).Error
			switch {
			case errors.Is(err, gorm.ErrRecordNotFound):
				return ErrNotFound
			case err != nil:
				return err
			}
			if algorithm.ComputeCost < 0 {
				return fmt.Errorf("%w: budget limit is %d", ErrComputeBudgetExceeded, maxPoints)
			}
			sched, _ := schedule.Parse(sub.ScheduleJSON)
			items = append(items, schedule.SchedulableItem{
				Points:   int64(algorithm.ComputeCost) * int64(sub.SampleFPS),
				Schedule: sched,
			})
		}
	}

	peak := schedule.CalculatePeakPoints(items)
	if peak > maxPoints {
		return fmt.Errorf("%w: budget limit is %d", ErrComputeBudgetExceeded, maxPoints)
	}
	return nil
}

// otherTasksSchedulableItems 汇总除 excludeTaskID 之外、所有启用中任务的算力调度项。
func otherTasksSchedulableItems(tx *gorm.DB, excludeTaskID string) ([]schedule.SchedulableItem, error) {
	query := tx.Table("task_algorithms").
		Select(
			"task_algorithms.id AS sub_id, task_algorithms.sample_fps, "+
				"algorithms.compute_cost, task_algorithms.schedule_json",
		).
		Joins("JOIN analysis_tasks ON analysis_tasks.id = task_algorithms.task_id").
		Joins("JOIN algorithms ON algorithms.id = task_algorithms.algorithm_id").
		Where("analysis_tasks.enabled = ? AND task_algorithms.enabled = ?", true, true)

	if excludeTaskID != "" {
		query = query.Where("analysis_tasks.id != ?", excludeTaskID)
	}

	var allocations []struct {
		SubID        string `gorm:"column:sub_id"`
		SampleFPS    int    `gorm:"column:sample_fps"`
		ComputeCost  int    `gorm:"column:compute_cost"`
		ScheduleJSON string `gorm:"column:schedule_json"`
	}
	if err := query.Scan(&allocations).Error; err != nil {
		return nil, err
	}

	items := make([]schedule.SchedulableItem, 0, len(allocations))
	for _, allocation := range allocations {
		sched, _ := schedule.Parse(allocation.ScheduleJSON)
		items = append(items, schedule.SchedulableItem{
			Points:   int64(allocation.ComputeCost) * int64(allocation.SampleFPS),
			Schedule: sched,
		})
	}
	return items, nil
}

// GetTask returns a task by its primary key ID with algorithms preloaded.
func (s *Store) GetTask(ctx context.Context, id string) (*Task, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	t, err := gorm.G[Task](db).Preload("Algorithms", nil).Where("id = ?", id).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &t, nil
}

// GetTaskByCamera returns a task by its camera ID with algorithms preloaded.
func (s *Store) GetTaskByCamera(ctx context.Context, cameraID string) (*Task, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	t, err := gorm.G[Task](db).Preload("Algorithms", nil).Where("camera_id = ?", cameraID).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &t, nil
}

// ListTasks returns all tasks ordered by created_at with algorithms preloaded.
func (s *Store) ListTasks(ctx context.Context) ([]Task, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	list, err := gorm.G[Task](db).Preload("Algorithms", nil).Order("created_at ASC").Find(ctx)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []Task{}
	}
	return list, nil
}

// ListTasksByCamera returns tasks for a specific camera with algorithms preloaded.
func (s *Store) ListTasksByCamera(ctx context.Context, cameraID string) ([]Task, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	list, err := gorm.G[Task](db).Preload("Algorithms", nil).
		Where("camera_id = ?", cameraID).
		Order("created_at ASC").
		Find(ctx)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []Task{}
	}
	return list, nil
}

// ListTasksByAlgorithm returns tasks containing a specific algorithm with algorithms preloaded.
func (s *Store) ListTasksByAlgorithm(ctx context.Context, algorithmID string) ([]Task, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	list := make([]Task, 0)
	err = db.Preload("Algorithms").
		Joins("JOIN task_algorithms ON task_algorithms.task_id = analysis_tasks.id").
		Where("task_algorithms.algorithm_id = ?", algorithmID).
		Distinct().
		Order("analysis_tasks.created_at ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

// DeleteTask removes a task by ID (cascades to task_algorithms).
func (s *Store) DeleteTask(ctx context.Context, id string) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	rows, err := gorm.G[Task](db).Where("id = ?", id).Delete(ctx)
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}
