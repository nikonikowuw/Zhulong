package algorithm

import (
	"context"
	"errors"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algorunner"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"gorm.io/gorm"
)

var _ algorunner.LifecycleStore = (*Store)(nil)

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

// GetAlgorithmPackage implements algorunner.LifecycleStore.
func (s *Store) GetAlgorithmPackage(ctx context.Context, id string) (algorunner.PackageMeta, error) {
	algo, err := s.GetAlgorithm(ctx, id)
	if err != nil {
		return algorunner.PackageMeta{}, err
	}
	return algo.ToPackageMeta(), nil
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

// DeleteTasksByAlgorithm removes tasks associated with an algorithm upon uninstallation.
func (s *Store) DeleteTasksByAlgorithm(ctx context.Context, algorithmID string) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	return db.Exec("DELETE FROM analysis_tasks WHERE algorithm_id = ?", algorithmID).Error
}
