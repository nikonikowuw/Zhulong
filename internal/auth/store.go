package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"gorm.io/gorm"
)

var (
	// ErrUserNotFound is returned when no user matches the query.
	ErrUserNotFound = errors.New("user not found")
	// ErrUserAlreadyExists is returned when trying to create an administrator when one already exists.
	ErrUserAlreadyExists = errors.New("user already exists")
)

// UserStore defines persistence operations for user credentials.
type UserStore interface {
	HasUser(ctx context.Context) (bool, error)
	GetByUsername(ctx context.Context, username string) (*User, error)
	GetByID(ctx context.Context, id int64) (*User, error)
	Create(ctx context.Context, user *User) error
}

type gormUserStore struct {
	dbProvider database.DBProvider
}

// NewUserStore constructs a UserStore backed by GORM.
func NewUserStore(dbProvider database.DBProvider) UserStore {
	return &gormUserStore{dbProvider: dbProvider}
}

func (s *gormUserStore) db(ctx context.Context) (*gorm.DB, error) {
	if s.dbProvider == nil {
		return nil, errors.New("database provider is nil")
	}
	db := s.dbProvider.DB()
	if db == nil {
		return nil, errors.New("database is not ready")
	}
	return db.WithContext(ctx), nil
}

func (s *gormUserStore) HasUser(ctx context.Context) (bool, error) {
	db, err := s.db(ctx)
	if err != nil {
		return false, err
	}
	var count int64
	if err := db.Model(&User{}).Count(&count).Error; err != nil {
		return false, fmt.Errorf("count users: %w", err)
	}
	return count > 0, nil
}

func (s *gormUserStore) GetByUsername(ctx context.Context, username string) (*User, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var user User
	err = db.Where("username = ?", username).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("get user by username: %w", err)
	}
	return &user, nil
}

func (s *gormUserStore) GetByID(ctx context.Context, id int64) (*User, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var user User
	err = db.Where("id = ?", id).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("get user by id: %w", err)
	}
	return &user, nil
}

func (s *gormUserStore) Create(ctx context.Context, user *User) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&User{}).Count(&count).Error; err != nil {
			return fmt.Errorf("count existing users: %w", err)
		}
		if count > 0 {
			return ErrUserAlreadyExists
		}
		now := time.Now().UTC()
		user.CreatedAt = now
		user.UpdatedAt = now
		if err := tx.Create(user).Error; err != nil {
			return fmt.Errorf("insert user: %w", err)
		}
		return nil
	})
}
