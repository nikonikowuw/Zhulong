package auth

import (
	"context"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *database.Store {
	t.Helper()
	store := database.New(t.TempDir(), zap.NewNop())
	if err := store.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func TestGORMUserStore_HasUserAndCreate(t *testing.T) {
	dbStore := setupTestDB(t)
	userStore := NewUserStore(dbStore)
	ctx := context.Background()

	hasUser, err := userStore.HasUser(ctx)
	if err != nil {
		t.Fatalf("HasUser initial: %v", err)
	}
	if hasUser {
		t.Fatal("expected no user in fresh database")
	}

	user := &User{
		Username:     "admin",
		PasswordHash: "hashed_secret",
	}
	if err := userStore.Create(ctx, user); err != nil {
		t.Fatalf("Create user: %v", err)
	}
	if user.ID == 0 {
		t.Fatal("expected user ID to be set after creation")
	}

	hasUser, err = userStore.HasUser(ctx)
	if err != nil {
		t.Fatalf("HasUser after create: %v", err)
	}
	if !hasUser {
		t.Fatal("expected user to exist after creation")
	}

	// Single-user constraint: creating a second user must fail with ErrUserAlreadyExists
	secondUser := &User{
		Username:     "second_admin",
		PasswordHash: "another_hash",
	}
	err = userStore.Create(ctx, secondUser)
	if err != ErrUserAlreadyExists {
		t.Fatalf("expected ErrUserAlreadyExists, got %v", err)
	}
}

func TestGORMUserStore_GetAndModify(t *testing.T) {
	dbStore := setupTestDB(t)
	userStore := NewUserStore(dbStore)
	ctx := context.Background()

	// Not found checks
	_, err := userStore.GetByUsername(ctx, "nonexistent")
	if err != ErrUserNotFound {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
	_, err = userStore.GetByID(ctx, 999)
	if err != ErrUserNotFound {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}

	user := &User{
		Username:     "admin",
		PasswordHash: "old_hash",
	}
	if err := userStore.Create(ctx, user); err != nil {
		t.Fatalf("Create user: %v", err)
	}

	fetchedByName, err := userStore.GetByUsername(ctx, "admin")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	if fetchedByName.ID != user.ID || fetchedByName.PasswordHash != "old_hash" {
		t.Fatalf("unexpected fetched user: %+v", fetchedByName)
	}

	fetchedByID, err := userStore.GetByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if fetchedByID.Username != "admin" {
		t.Fatalf("unexpected username: %s", fetchedByID.Username)
	}
}

type mockNilDBProvider struct{}

func (mockNilDBProvider) DB() *gorm.DB { return nil }

func TestGORMUserStore_UnreadyDatabase(t *testing.T) {
	ctx := context.Background()

	for name, store := range map[string]UserStore{
		"unready": NewUserStore(mockNilDBProvider{}),
		"nil":     NewUserStore(nil),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := store.HasUser(ctx); err == nil {
				t.Errorf("%s: expected error on HasUser, got nil", name)
			}
			if _, err := store.GetByUsername(ctx, "admin"); err == nil {
				t.Errorf("%s: expected error on GetByUsername, got nil", name)
			}
			if _, err := store.GetByID(ctx, 1); err == nil {
				t.Errorf("%s: expected error on GetByID, got nil", name)
			}
			if err := store.Create(ctx, &User{Username: "test"}); err == nil {
				t.Errorf("%s: expected error on Create, got nil", name)
			}
		})
	}
}
