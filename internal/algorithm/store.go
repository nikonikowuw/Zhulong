package algorithm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"gorm.io/gorm"
)

var (
	ErrNotFound = errors.New("record not found")
)

// Store handles database persistence for algorithms, analysis tasks, and face galleries.
type Store struct {
	dbProvider database.DBProvider
}

// NewStore creates a new Store instance.
func NewStore(dbProvider database.DBProvider) *Store {
	return &Store{dbProvider: dbProvider}
}

func (s *Store) db(ctx context.Context) (*gorm.DB, error) {
	if s.dbProvider == nil {
		return nil, errors.New("database provider is nil")
	}
	db := s.dbProvider.DB()
	if db == nil {
		return nil, errors.New("database is not ready")
	}
	return db.WithContext(ctx), nil
}

// --- Algorithm Operations ---

func (s *Store) CreateAlgorithm(ctx context.Context, algo *Algorithm) error {
	if algo == nil {
		return errors.New("algorithm is nil")
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if algo.CreatedAt.IsZero() {
		algo.CreatedAt = now
	} else {
		algo.CreatedAt = algo.CreatedAt.UTC()
	}
	if algo.UpdatedAt.IsZero() {
		algo.UpdatedAt = now
	} else {
		algo.UpdatedAt = algo.UpdatedAt.UTC()
	}
	return db.Create(algo).Error
}

func (s *Store) GetAlgorithm(ctx context.Context, id string) (*Algorithm, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var algo Algorithm
	if err := db.Where("id = ?", id).First(&algo).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &algo, nil
}

func (s *Store) GetAlgorithmByName(ctx context.Context, name string) (*Algorithm, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var algo Algorithm
	if err := db.Where("name = ?", name).First(&algo).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &algo, nil
}

func (s *Store) ListAlgorithms(ctx context.Context) ([]Algorithm, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var list []Algorithm
	if err := db.Order("created_at ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (s *Store) UpdateAlgorithm(ctx context.Context, algo *Algorithm) error {
	if algo == nil {
		return errors.New("algorithm is nil")
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	algo.UpdatedAt = time.Now().UTC()
	return db.Save(algo).Error
}

func (s *Store) UpdateAlgorithmStatus(ctx context.Context, id, status string) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	res := db.Model(&Algorithm{}).Where("id = ?", id).Updates(map[string]any{
		"status":     status,
		"updated_at": time.Now().UTC(),
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// RollbackAlgorithmVersion rolls back the algorithm's active version in the database
// to targetVersion, preserves failedVersion in previous_version, and marks status as ACTIVE.
func (s *Store) RollbackAlgorithmVersion(ctx context.Context, id, targetVersion, failedVersion string) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	res := db.Model(&Algorithm{}).Where("id = ?", id).Updates(map[string]any{
		"version":          targetVersion,
		"previous_version": failedVersion,
		"status":           StatusActive,
		"updated_at":       time.Now().UTC(),
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteAlgorithm(ctx context.Context, id string) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	res := db.Where("id = ?", id).Delete(&Algorithm{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Analysis Task Operations ---

func (s *Store) CreateTask(ctx context.Context, t *AnalysisTask) error {
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

func (s *Store) CreateTaskWithinBudget(ctx context.Context, task *AnalysisTask, maxPoints int64) error {
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
		var algorithm Algorithm
		if err := tx.Select("id", "compute_cost").Where("id = ?", task.AlgorithmID).First(&algorithm).Error; err != nil {
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

func (s *Store) GetTask(ctx context.Context, id string) (*AnalysisTask, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var t AnalysisTask
	if err := db.Where("id = ?", id).First(&t).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &t, nil
}

func (s *Store) GetTaskByCameraAndAlgorithm(ctx context.Context, cameraID, algorithmID string) (*AnalysisTask, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var t AnalysisTask
	if err := db.Where("camera_id = ? AND algorithm_id = ?", cameraID, algorithmID).First(&t).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &t, nil
}

func (s *Store) ListTasks(ctx context.Context) ([]AnalysisTask, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var list []AnalysisTask
	if err := db.Order("created_at ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (s *Store) ListTasksByCamera(ctx context.Context, cameraID string) ([]AnalysisTask, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var list []AnalysisTask
	if err := db.Where("camera_id = ?", cameraID).Order("created_at ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (s *Store) ListTasksByAlgorithm(ctx context.Context, algorithmID string) ([]AnalysisTask, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var list []AnalysisTask
	if err := db.Where("algorithm_id = ?", algorithmID).Order("created_at ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (s *Store) UpdateTask(ctx context.Context, t *AnalysisTask) error {
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

func (s *Store) DeleteTask(ctx context.Context, id string) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	res := db.Where("id = ?", id).Delete(&AnalysisTask{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteTasksByCamera(ctx context.Context, cameraID string) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	return db.Where("camera_id = ?", cameraID).Delete(&AnalysisTask{}).Error
}

func (s *Store) DeleteTasksByAlgorithm(ctx context.Context, algorithmID string) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	return db.Where("algorithm_id = ?", algorithmID).Delete(&AnalysisTask{}).Error
}
