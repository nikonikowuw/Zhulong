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
	count, err := gorm.G[User](db).Count(ctx, "*")
	if err != nil {
		return false, fmt.Errorf("count users: %w", err)
	}
	return count > 0, nil
}

func (s *gormUserStore) GetByUsername(ctx context.Context, username string) (*User, error) {
	return s.firstUser(ctx, "username = ?", username)
}

func (s *gormUserStore) GetByID(ctx context.Context, id int64) (*User, error) {
	return s.firstUser(ctx, "id = ?", id)
}

// firstUser runs a single-row lookup and translates GORM's sentinel into
// ErrUserNotFound, so callers never import gorm just to classify a miss.
func (s *gormUserStore) firstUser(ctx context.Context, where string, args ...any) (*User, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	user, err := gorm.G[User](db).Where(where, args...).First(ctx)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user where %s: %w", where, err)
	}
	return &user, nil
}

func (s *gormUserStore) Create(ctx context.Context, user *User) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		count, err := gorm.G[User](tx).Count(ctx, "*")
		if err != nil {
			return fmt.Errorf("count existing users: %w", err)
		}
		if count > 0 {
			return ErrUserAlreadyExists
		}
		now := time.Now().UTC()
		user.CreatedAt = now
		user.UpdatedAt = now
		if err := gorm.G[User](tx).Create(ctx, user); err != nil {
			return fmt.Errorf("insert user: %w", err)
		}
		return nil
	})
}
