package task

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/idutil"
	"gorm.io/gorm"
)

var (
	ErrNotFound              = errors.New("task not found")
	ErrComputeBudgetExceeded = errors.New("compute budget exceeded")
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
		return nil, errors.New("dbProvider is nil")
	}
	db := s.dbProvider.DB()
	if db == nil {
		return nil, errors.New("database not initialized")
	}
	return db.WithContext(ctx), nil
}

// initTaskTimestamps ensures CreatedAt and UpdatedAt are set in UTC.
func initTaskTimestamps(t *Task) {
	now := time.Now().UTC()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	} else {
		t.CreatedAt = t.CreatedAt.UTC()
	}
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = now
	} else {
		t.UpdatedAt = t.UpdatedAt.UTC()
	}
	for i := range t.Algorithms {
		if t.Algorithms[i].CreatedAt.IsZero() {
			t.Algorithms[i].CreatedAt = now
		} else {
			t.Algorithms[i].CreatedAt = t.Algorithms[i].CreatedAt.UTC()
		}
		if t.Algorithms[i].UpdatedAt.IsZero() {
			t.Algorithms[i].UpdatedAt = now
		} else {
			t.Algorithms[i].UpdatedAt = t.Algorithms[i].UpdatedAt.UTC()
		}
	}
}

// CreateTask persists a new task entity without budget check.
func (s *Store) CreateTask(ctx context.Context, t *Task) error {
	if t == nil {
		return errors.New("task is nil")
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	initTaskTimestamps(t)
	if t.ID == "" {
		t.ID = idutil.New()
	}
	for i := range t.Algorithms {
		if t.Algorithms[i].ID == "" {
			t.Algorithms[i].ID = idutil.New()
		}
		t.Algorithms[i].TaskID = t.ID
	}
	return db.Create(t).Error
}

// CreateTaskWithinBudget creates a task ensuring total compute points do not exceed maxPoints.
func (s *Store) CreateTaskWithinBudget(ctx context.Context, task *Task, maxPoints int64) error {
	if task == nil {
		return errors.New("task is nil")
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	initTaskTimestamps(task)

	return db.Transaction(func(tx *gorm.DB) error {
		if task.ID == "" {
			task.ID = idutil.New()
		}
		for i := range task.Algorithms {
			if task.Algorithms[i].ID == "" {
				task.Algorithms[i].ID = idutil.New()
			}
			task.Algorithms[i].TaskID = task.ID
		}

		if err := validateComputeBudget(tx, task, "", maxPoints); err != nil {
			return err
		}
		return tx.Create(task).Error
	})
}

// UpdateTaskWithinBudget updates a task and checks budget against maxPoints.
func (s *Store) UpdateTaskWithinBudget(ctx context.Context, task *Task, maxPoints int64) error {
	if task == nil {
		return errors.New("task is nil")
	}
	return s.SaveTaskWithinBudget(ctx, task, maxPoints)
}

// SaveTaskWithinBudget saves or updates a channel task and its algorithms atomically within compute budget.
func (s *Store) SaveTaskWithinBudget(ctx context.Context, task *Task, maxPoints int64) error {
	if task == nil {
		return errors.New("task is nil")
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	initTaskTimestamps(task)

	return db.Transaction(func(tx *gorm.DB) error {
		// Locate existing record by ID or CameraID
		var existing Task
		var excludeID string
		errFind := tx.Where("id = ? OR camera_id = ?", task.ID, task.CameraID).First(&existing).Error
		if errFind == nil {
			excludeID = existing.ID
			task.ID = existing.ID
			task.CreatedAt = existing.CreatedAt
		} else if !errors.Is(errFind, gorm.ErrRecordNotFound) {
			return errFind
		}

		if task.ID == "" {
			task.ID = idutil.New()
		}

		for i := range task.Algorithms {
			task.Algorithms[i].TaskID = task.ID
		}

		if err := validateComputeBudget(tx, task, excludeID, maxPoints); err != nil {
			return err
		}

		now := time.Now().UTC()
		task.UpdatedAt = now

		if excludeID != "" {
			if err := tx.Model(&Task{}).Where("id = ?", task.ID).Updates(map[string]interface{}{
				"camera_id":  task.CameraID,
				"enabled":    task.Enabled,
				"name":       task.Name,
				"updated_at": task.UpdatedAt,
			}).Error; err != nil {
				return err
			}
		} else {
			parentOnly := *task
			parentOnly.Algorithms = nil
			if err := tx.Create(&parentOnly).Error; err != nil {
				return err
			}
		}

		// Delete removed algorithms
		keepAlgoIDs := make([]string, 0, len(task.Algorithms))
		for _, a := range task.Algorithms {
			keepAlgoIDs = append(keepAlgoIDs, a.AlgorithmID)
		}
		if len(keepAlgoIDs) > 0 {
			if err := tx.Where("task_id = ? AND algorithm_id NOT IN ?", task.ID, keepAlgoIDs).Delete(&TaskAlgorithm{}).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Where("task_id = ?", task.ID).Delete(&TaskAlgorithm{}).Error; err != nil {
				return err
			}
		}

		// Upsert algorithms
		for i := range task.Algorithms {
			sub := &task.Algorithms[i]
			var existingSub TaskAlgorithm
			errSub := tx.Where("task_id = ? AND algorithm_id = ?", task.ID, sub.AlgorithmID).First(&existingSub).Error
			if errSub == nil {
				sub.ID = existingSub.ID
				sub.CreatedAt = existingSub.CreatedAt
				sub.UpdatedAt = now
				if err := tx.Model(&TaskAlgorithm{}).Where("id = ?", sub.ID).Updates(map[string]interface{}{
					"enabled":            sub.Enabled,
					"sample_fps":         sub.SampleFPS,
					"config_json":        sub.ConfigJSON,
					"region_config_json": sub.RegionConfigJSON,
					"updated_at":         sub.UpdatedAt,
				}).Error; err != nil {
					return err
				}
			} else if errors.Is(errSub, gorm.ErrRecordNotFound) {
				if sub.ID == "" {
					sub.ID = idutil.New()
				}
				sub.CreatedAt = now
				sub.UpdatedAt = now
				if err := tx.Create(sub).Error; err != nil {
					return err
				}
			} else {
				return errSub
			}
		}

		return nil
	})
}

func validateComputeBudget(tx *gorm.DB, task *Task, excludeTaskID string, maxPoints int64) error {
	query := tx.Table("task_algorithms").
		Select("task_algorithms.id AS sub_id, task_algorithms.sample_fps, algorithms.compute_cost").
		Joins("JOIN analysis_tasks ON analysis_tasks.id = task_algorithms.task_id").
		Joins("JOIN algorithms ON algorithms.id = task_algorithms.algorithm_id").
		Where("analysis_tasks.enabled = ? AND task_algorithms.enabled = ?", true, true)

	if excludeTaskID != "" {
		query = query.Where("analysis_tasks.id != ?", excludeTaskID)
	}

	var allocations []struct {
		SubID       string `gorm:"column:sub_id"`
		SampleFPS   int    `gorm:"column:sample_fps"`
		ComputeCost *int   `gorm:"column:compute_cost"`
	}
	if err := query.Scan(&allocations).Error; err != nil {
		return err
	}

	var total int64
	for _, allocation := range allocations {
		if allocation.ComputeCost == nil {
			return fmt.Errorf("enabled analysis task algorithm %q references a missing algorithm", allocation.SubID)
		}
		var err error
		total, err = addComputePoints(total, *allocation.ComputeCost, allocation.SampleFPS, maxPoints)
		if err != nil {
			return err
		}
	}

	if task.Enabled {
		for _, sub := range task.Algorithms {
			if !sub.Enabled {
				continue
			}
			var algorithm struct {
				ID          string `gorm:"column:id"`
				ComputeCost int    `gorm:"column:compute_cost"`
			}
			if err := tx.Table("algorithms").Select("id, compute_cost").Where("id = ?", sub.AlgorithmID).First(&algorithm).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrNotFound
				}
				return err
			}
			var err error
			total, err = addComputePoints(total, algorithm.ComputeCost, sub.SampleFPS, maxPoints)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func addComputePoints(total int64, computeCost, sampleFPS int, maxPoints int64) (int64, error) {
	if total < 0 || total > maxPoints || computeCost < 0 || sampleFPS < 0 {
		return 0, fmt.Errorf("%w: budget limit is %d", ErrComputeBudgetExceeded, maxPoints)
	}
	cost := int64(computeCost)
	fps := int64(sampleFPS)
	if fps > 0 && cost > (maxPoints-total)/fps {
		return 0, fmt.Errorf("%w: budget limit is %d", ErrComputeBudgetExceeded, maxPoints)
	}
	return total + cost*fps, nil
}

// GetTask returns a task by its primary key ID with algorithms preloaded.
func (s *Store) GetTask(ctx context.Context, id string) (*Task, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var t Task
	if err := db.Preload("Algorithms").Where("id = ?", id).First(&t).Error; err != nil {
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
	var t Task
	if err := db.Preload("Algorithms").Where("camera_id = ?", cameraID).First(&t).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &t, nil
}

// GetTaskByCameraAndAlgorithm returns a task matching the camera and algorithm.
func (s *Store) GetTaskByCameraAndAlgorithm(ctx context.Context, cameraID, algorithmID string) (*Task, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var t Task
	err = db.Preload("Algorithms").
		Joins("JOIN task_algorithms ON task_algorithms.task_id = analysis_tasks.id").
		Where("analysis_tasks.camera_id = ? AND task_algorithms.algorithm_id = ?", cameraID, algorithmID).
		First(&t).Error
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
	var list []Task
	if err := db.Preload("Algorithms").Order("created_at ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ListTasksByCamera returns tasks for a specific camera with algorithms preloaded.
func (s *Store) ListTasksByCamera(ctx context.Context, cameraID string) ([]Task, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var list []Task
	if err := db.Preload("Algorithms").Where("camera_id = ?", cameraID).Order("created_at ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ListTasksByAlgorithm returns tasks containing a specific algorithm with algorithms preloaded.
func (s *Store) ListTasksByAlgorithm(ctx context.Context, algorithmID string) ([]Task, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var list []Task
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

// UpdateTask updates a task entity directly.
func (s *Store) UpdateTask(ctx context.Context, t *Task) error {
	if t == nil {
		return errors.New("task is nil")
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	t.UpdatedAt = time.Now().UTC()
	return db.Session(&gorm.Session{FullSaveAssociations: true}).Save(t).Error
}

// DeleteTask removes a task by ID (cascades to task_algorithms).
func (s *Store) DeleteTask(ctx context.Context, id string) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	res := db.Where("id = ?", id).Delete(&Task{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteTasksByCamera deletes all tasks associated with a camera.
func (s *Store) DeleteTasksByCamera(ctx context.Context, cameraID string) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	return db.Where("camera_id = ?", cameraID).Delete(&Task{}).Error
}

// DeleteTasksByAlgorithm deletes algorithm bindings from task_algorithms.
func (s *Store) DeleteTasksByAlgorithm(ctx context.Context, algorithmID string) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	return db.Where("algorithm_id = ?", algorithmID).Delete(&TaskAlgorithm{}).Error
}
