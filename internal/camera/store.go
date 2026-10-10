package camera

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrCameraNotFound   = errors.New("camera not found")
	ErrRevisionConflict = errors.New("camera revision conflict")
	ErrDuplicateCamera  = errors.New("camera already exists")
)

// UpdateCameraParams bundles parameters for conditional camera updates.
type UpdateCameraParams struct {
	ID               string
	ExpectedRevision int64
	Name             *string
	Enabled          *bool
	Streams          []CameraStream
}

// CameraStore defines database persistence operations for cameras and their streams.
type CameraStore interface {
	Create(ctx context.Context, camera *Camera, streams []CameraStream) error
	GetByID(ctx context.Context, id string) (*Camera, error)
	List(ctx context.Context, limit, offset int) ([]Camera, int64, error)
	Update(ctx context.Context, params UpdateCameraParams) (*Camera, error)
	Delete(ctx context.Context, id string) error
	HasAnyEncryptedStreams(ctx context.Context) (bool, error)
	GetNextNumericID(ctx context.Context) (string, error)
}

type gormCameraStore struct {
	dbProvider database.DBProvider
}

// NewCameraStore constructs a CameraStore backed by GORM.
func NewCameraStore(dbProvider database.DBProvider) CameraStore {
	return &gormCameraStore{dbProvider: dbProvider}
}

func (s *gormCameraStore) db(ctx context.Context) (*gorm.DB, error) {
	if s.dbProvider == nil {
		return nil, errors.New("database provider is nil")
	}
	db := s.dbProvider.DB()
	if db == nil {
		return nil, errors.New("database is not ready")
	}
	return db.WithContext(ctx), nil
}

func (s *gormCameraStore) Create(ctx context.Context, camera *Camera, streams []CameraStream) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var existingCount int64
		if err := tx.Model(&Camera{}).Where("id = ?", camera.ID).Count(&existingCount).Error; err != nil {
			return fmt.Errorf("check existing camera: %w", err)
		}
		if existingCount > 0 {
			return ErrDuplicateCamera
		}

		now := time.Now().UTC()
		camera.CreatedAt = now
		camera.UpdatedAt = now
		if camera.Revision <= 0 {
			camera.Revision = 1
		}

		if err := tx.Create(camera).Error; err != nil {
			return fmt.Errorf("create camera record: %w", err)
		}

		for i := range streams {
			streams[i].CameraID = camera.ID
			streams[i].CreatedAt = now
			streams[i].UpdatedAt = now
		}

		if len(streams) > 0 {
			if err := tx.Create(&streams).Error; err != nil {
				return fmt.Errorf("create camera streams: %w", err)
			}
		}
		camera.Streams = streams
		return nil
	})
}

func (s *gormCameraStore) GetByID(ctx context.Context, id string) (*Camera, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}

	var camera Camera
	err = db.Preload("Streams").Where("id = ?", id).First(&camera).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrCameraNotFound
		}
		return nil, fmt.Errorf("get camera by id: %w", err)
	}
	return &camera, nil
}

func (s *gormCameraStore) List(ctx context.Context, limit, offset int) ([]Camera, int64, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, 0, err
	}

	var total int64
	if err := db.Model(&Camera{}).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count cameras: %w", err)
	}

	if limit <= 0 {
		limit = 20
	} else if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	var cameras []Camera
	err = db.Preload("Streams").
		Order("created_at DESC, id ASC").
		Limit(limit).
		Offset(offset).
		Find(&cameras).Error
	if err != nil {
		return nil, 0, fmt.Errorf("list cameras: %w", err)
	}

	return cameras, total, nil
}

func (s *gormCameraStore) Update(ctx context.Context, params UpdateCameraParams) (*Camera, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}

	var updatedCamera Camera
	err = db.Transaction(func(tx *gorm.DB) error {
		var current Camera
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", params.ID).First(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrCameraNotFound
			}
			return fmt.Errorf("find camera for update: %w", err)
		}

		if current.Revision != params.ExpectedRevision {
			return ErrRevisionConflict
		}

		updates := map[string]any{
			"revision":   current.Revision + 1,
			"updated_at": time.Now().UTC(),
		}
		if params.Name != nil {
			updates["name"] = *params.Name
		}
		if params.Enabled != nil {
			updates["enabled"] = *params.Enabled
		}

		res := tx.Model(&Camera{}).Where("id = ? AND revision = ?", params.ID, params.ExpectedRevision).Updates(updates)
		if res.Error != nil {
			return fmt.Errorf("update camera: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return ErrRevisionConflict
		}

		if params.Streams != nil {
			if err := tx.Where("camera_id = ?", params.ID).Delete(&CameraStream{}).Error; err != nil {
				return fmt.Errorf("delete old camera streams: %w", err)
			}

			now := time.Now().UTC()
			for i := range params.Streams {
				params.Streams[i].CameraID = params.ID
				params.Streams[i].CreatedAt = now
				params.Streams[i].UpdatedAt = now
			}
			if len(params.Streams) > 0 {
				if err := tx.Create(&params.Streams).Error; err != nil {
					return fmt.Errorf("insert updated camera streams: %w", err)
				}
			}
		}

		return tx.Preload("Streams").Where("id = ?", params.ID).First(&updatedCamera).Error
	})

	if err != nil {
		return nil, err
	}
	return &updatedCamera, nil
}

func (s *gormCameraStore) Delete(ctx context.Context, id string) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&Camera{}).Where("id = ?", id).Count(&count).Error; err != nil {
			return fmt.Errorf("check camera existence: %w", err)
		}
		if count == 0 {
			return ErrCameraNotFound
		}

		// Delete cascading streams first or rely on foreign key cascade
		if err := tx.Where("camera_id = ?", id).Delete(&CameraStream{}).Error; err != nil {
			return fmt.Errorf("delete camera streams: %w", err)
		}

		if err := tx.Where("id = ?", id).Delete(&Camera{}).Error; err != nil {
			return fmt.Errorf("delete camera: %w", err)
		}
		return nil
	})
}

func (s *gormCameraStore) HasAnyEncryptedStreams(ctx context.Context) (bool, error) {
	db, err := s.db(ctx)
	if err != nil {
		return false, err
	}

	var count int64
	err = db.Model(&CameraStream{}).Where("encrypted_uri IS NOT NULL AND length(encrypted_uri) > 0").Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("count encrypted streams: %w", err)
	}
	return count > 0, nil
}

func (s *gormCameraStore) GetNextNumericID(ctx context.Context) (string, error) {
	db, err := s.db(ctx)
	if err != nil {
		return "", err
	}

	var nextID int64
	err = db.Raw("SELECT COALESCE(MAX(CAST(id AS INTEGER)), 0) + 1 FROM cameras WHERE id NOT GLOB '*[^0-9]*' AND id != ''").Scan(&nextID).Error
	if err != nil {
		return "", fmt.Errorf("query next numeric id: %w", err)
	}

	return strconv.FormatInt(nextID, 10), nil
}
