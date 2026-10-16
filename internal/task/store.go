package task

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
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

// CreateTask persists a new task entity without budget check.
func (s *Store) CreateTask(ctx context.Context, t *Task) error {
	if t == nil {
		return errors.New("task is nil")
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
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
	now := time.Now().UTC()
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	} else {
		task.CreatedAt = task.CreatedAt.UTC()
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = now
	} else {
		task.UpdatedAt = task.UpdatedAt.UTC()
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var algorithm struct {
			ID          string `gorm:"column:id"`
			ComputeCost int    `gorm:"column:compute_cost"`
		}
		if err := tx.Table("algorithms").Select("id, compute_cost").Where("id = ?", task.AlgorithmID).First(&algorithm).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}

		var allocations []struct {
			TaskID      string `gorm:"column:task_id"`
			SampleFPS   int    `gorm:"column:sample_fps"`
			ComputeCost *int   `gorm:"column:compute_cost"`
		}
		if err := tx.Table("analysis_tasks").
			Select("analysis_tasks.id AS task_id, analysis_tasks.sample_fps, algorithms.compute_cost").
			Joins("LEFT JOIN algorithms ON algorithms.id = analysis_tasks.algorithm_id").
			Where("analysis_tasks.enabled = ?", true).
			Scan(&allocations).Error; err != nil {
			return err
		}

		var total int64
		for _, allocation := range allocations {
			if allocation.ComputeCost == nil {
				return fmt.Errorf("enabled analysis task %q references a missing algorithm", allocation.TaskID)
			}
			total, err = addComputePoints(total, *allocation.ComputeCost, allocation.SampleFPS, maxPoints)
			if err != nil {
				return err
			}
		}
		if task.Enabled {
			if total, err = addComputePoints(total, algorithm.ComputeCost, task.SampleFPS, maxPoints); err != nil {
				return err
			}
		}
		return tx.Create(task).Error
	})
}

// UpdateTaskWithinBudget updates a task and checks budget against maxPoints.
func (s *Store) UpdateTaskWithinBudget(ctx context.Context, task *Task, maxPoints int64) error {
	if task == nil {
		return errors.New("task is nil")
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	task.UpdatedAt = time.Now().UTC()

	return db.Transaction(func(tx *gorm.DB) error {
		var algorithm struct {
			ID          string `gorm:"column:id"`
			ComputeCost int    `gorm:"column:compute_cost"`
		}
		if err := tx.Table("algorithms").Select("id, compute_cost").Where("id = ?", task.AlgorithmID).First(&algorithm).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}

		var allocations []struct {
			TaskID      string `gorm:"column:task_id"`
			SampleFPS   int    `gorm:"column:sample_fps"`
			ComputeCost *int   `gorm:"column:compute_cost"`
		}
		// Exclude current task from allocation sum
		if err := tx.Table("analysis_tasks").
			Select("analysis_tasks.id AS task_id, analysis_tasks.sample_fps, algorithms.compute_cost").
			Joins("LEFT JOIN algorithms ON algorithms.id = analysis_tasks.algorithm_id").
			Where("analysis_tasks.enabled = ? AND analysis_tasks.id != ?", true, task.ID).
			Scan(&allocations).Error; err != nil {
			return err
		}

		var total int64
		for _, allocation := range allocations {
			if allocation.ComputeCost == nil {
				return fmt.Errorf("enabled analysis task %q references a missing algorithm", allocation.TaskID)
			}
			total, err = addComputePoints(total, *allocation.ComputeCost, allocation.SampleFPS, maxPoints)
			if err != nil {
				return err
			}
		}
		if task.Enabled {
			if total, err = addComputePoints(total, algorithm.ComputeCost, task.SampleFPS, maxPoints); err != nil {
				return err
			}
		}
		return tx.Save(task).Error
	})
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

// GetTask returns a task by its primary key ID.
func (s *Store) GetTask(ctx context.Context, id string) (*Task, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var t Task
	if err := db.Where("id = ?", id).First(&t).Error; err != nil {
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
	if err := db.Where("camera_id = ? AND algorithm_id = ?", cameraID, algorithmID).First(&t).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &t, nil
}

// ListTasks returns all tasks ordered by created_at.
func (s *Store) ListTasks(ctx context.Context) ([]Task, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var list []Task
	if err := db.Order("created_at ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ListTasksByCamera returns tasks for a specific camera.
func (s *Store) ListTasksByCamera(ctx context.Context, cameraID string) ([]Task, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var list []Task
	if err := db.Where("camera_id = ?", cameraID).Order("created_at ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ListTasksByAlgorithm returns tasks for a specific algorithm.
func (s *Store) ListTasksByAlgorithm(ctx context.Context, algorithmID string) ([]Task, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var list []Task
	if err := db.Where("algorithm_id = ?", algorithmID).Order("created_at ASC").Find(&list).Error; err != nil {
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
	return db.Save(t).Error
}

// DeleteTask removes a task by ID.
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

// DeleteTasksByAlgorithm deletes all tasks associated with an algorithm.
func (s *Store) DeleteTasksByAlgorithm(ctx context.Context, algorithmID string) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	return db.Where("algorithm_id = ?", algorithmID).Delete(&Task{}).Error
}
