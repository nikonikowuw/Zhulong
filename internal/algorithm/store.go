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

	// 包内哨兵：都是装配顺序或调用方式错误，调用方只需向上传播。
	errNilAlgorithm     = errors.New("algorithm is nil")
	errNoDBProvider     = errors.New("database provider is nil")
	errDatabaseNotReady = errors.New("database is not ready")
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
		return nil, errNoDBProvider
	}
	db := s.dbProvider.DB()
	if db == nil {
		return nil, errDatabaseNotReady
	}
	return db.WithContext(ctx), nil
}

// --- Algorithm Operations ---

func (s *Store) CreateAlgorithm(ctx context.Context, algo *Algorithm) error {
	if algo == nil {
		return errNilAlgorithm
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
	return gorm.G[Algorithm](db).Create(ctx, algo)
}

func (s *Store) GetAlgorithm(ctx context.Context, id string) (*Algorithm, error) {
	return s.findAlgorithmBy(ctx, "id = ?", id)
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
	return s.findAlgorithmBy(ctx, "name = ?", name)
}

// findAlgorithmBy 是 GetAlgorithm / GetAlgorithmByName 的唯一实现。两者的差别
// 只有一个查询列，各写一份会让 ErrNotFound 的映射多出第二个版本。
func (s *Store) findAlgorithmBy(ctx context.Context, where string, arg any) (*Algorithm, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	algo, err := gorm.G[Algorithm](db).Where(where, arg).First(ctx)
	if err != nil {
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
	// Initialized, not nil: an empty list must serialize to [] rather than null.
	list, err := gorm.G[Algorithm](db).Order("created_at ASC").Find(ctx)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []Algorithm{}
	}
	return list, nil
}

func (s *Store) UpdateAlgorithm(ctx context.Context, algo *Algorithm) error {
	if algo == nil {
		return errNilAlgorithm
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	algo.UpdatedAt = time.Now().UTC()
	return db.Save(algo).Error
}

func (s *Store) UpdateAlgorithmStatus(ctx context.Context, id, status string) error {
	return s.updateAlgorithmColumns(ctx, id, map[string]any{"status": status})
}

// RollbackAlgorithmVersion rolls back the algorithm's active version in the database
// to targetVersion, preserves failedVersion in previous_version, and marks status as ACTIVE.
func (s *Store) RollbackAlgorithmVersion(ctx context.Context, id, targetVersion, failedVersion string) error {
	return s.updateAlgorithmColumns(ctx, id, map[string]any{
		"version":          targetVersion,
		"previous_version": failedVersion,
		"status":           StatusActive,
	})
}

// updateAlgorithmColumns 对单条算法记录做列级更新，并统一维护 updated_at。
// 零行受影响即视为记录不存在：这是状态写入路径唯一的 ErrNotFound 来源。
//
// 注意：columns 会在原地写入 updated_at，调用方传入的字面量 map 不应复用。
func (s *Store) updateAlgorithmColumns(ctx context.Context, id string, columns map[string]any) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	columns["updated_at"] = time.Now().UTC()
	res := db.Model(&Algorithm{}).Where("id = ?", id).Updates(columns)
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
	rows, err := gorm.G[Algorithm](db).Where("id = ?", id).Delete(ctx)
	if err != nil {
		return err
	}
	if rows == 0 {
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
	return db.Exec("DELETE FROM task_algorithms WHERE algorithm_id = ?", algorithmID).Error
}
