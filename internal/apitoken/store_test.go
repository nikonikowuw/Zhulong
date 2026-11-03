package apitoken

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

func newTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	store := NewStore(dbStore)
	return store, func() { _ = dbStore.Close() }
}

func TestStore_CreateTokenAndLimit(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Create 10 tokens
	for i := 1; i <= MaxActiveTokens; i++ {
		tok := &APIToken{
			ID:        fmt.Sprintf("id_%02d", i),
			Name:      fmt.Sprintf("Token-%02d", i),
			TokenHash: fmt.Sprintf("hash_%02d", i),
			MaskedKey: "sk-test...1234",
			CreatedAt: time.Now().UTC(),
		}
		if err := store.CreateToken(ctx, tok); err != nil {
			t.Fatalf("CreateToken #%d failed: %v", i, err)
		}
	}

	// 11th token should fail with ErrTokenLimitReached
	overflow := &APIToken{
		ID:        "id_overflow",
		Name:      "Token-Overflow",
		TokenHash: "hash_overflow",
		MaskedKey: "sk-test...1234",
		CreatedAt: time.Now().UTC(),
	}
	err := store.CreateToken(ctx, overflow)
	if !errors.Is(err, ErrTokenLimitReached) {
		t.Fatalf("expected ErrTokenLimitReached, got %v", err)
	}

	// Revoke one token, then creation should succeed
	if err := store.Revoke(ctx, "id_01"); err != nil {
		t.Fatalf("Revoke id_01 failed: %v", err)
	}
	if err := store.CreateToken(ctx, overflow); err != nil {
		t.Fatalf("expected 10th active token creation to succeed after revocation, got %v", err)
	}
}

func TestStore_NameConflictAcrossEntireTable(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	tok1 := &APIToken{
		ID:        "id_1",
		Name:      "UniqueName",
		TokenHash: "hash_1",
		MaskedKey: "sk-test...1234",
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateToken(ctx, tok1); err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Active duplicate
	tok2 := &APIToken{
		ID:        "id_2",
		Name:      "UniqueName",
		TokenHash: "hash_2",
		MaskedKey: "sk-test...1234",
		CreatedAt: time.Now().UTC(),
	}
	err := store.CreateToken(ctx, tok2)
	if !errors.Is(err, ErrTokenNameConflict) {
		t.Fatalf("expected ErrTokenNameConflict for active token, got %v", err)
	}

	// Soft revoke tok1
	if err := store.Revoke(ctx, "id_1"); err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}

	// Name conflict should still trigger even when the previous token is revoked!
	err = store.CreateToken(ctx, tok2)
	if !errors.Is(err, ErrTokenNameConflict) {
		t.Fatalf("expected ErrTokenNameConflict even after revocation, got %v", err)
	}
}

func TestStore_ListAndFind(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	tok := &APIToken{
		ID:        "id_alpha",
		Name:      "Alpha",
		TokenHash: "hash_alpha",
		MaskedKey: "sk-alph...1234",
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateToken(ctx, tok); err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	found, err := store.FindActiveByHash(ctx, "hash_alpha")
	if err != nil {
		t.Fatalf("FindActiveByHash failed: %v", err)
	}
	if found.ID != "id_alpha" {
		t.Errorf("expected id_alpha, got %q", found.ID)
	}

	// Revoke
	if err := store.Revoke(ctx, "id_alpha"); err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}

	// Idempotent revoke
	if err := store.Revoke(ctx, "id_alpha"); err != nil {
		t.Fatalf("Idempotent Revoke failed: %v", err)
	}

	// FindActive should fail now
	_, err = store.FindActiveByHash(ctx, "hash_alpha")
	if !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("expected ErrTokenNotFound after revocation, got %v", err)
	}

	// ListTokens
	activeList, err := store.ListTokens(ctx, false)
	if err != nil {
		t.Fatalf("ListTokens(false) failed: %v", err)
	}
	if len(activeList) != 0 {
		t.Errorf("expected 0 active tokens, got %d", len(activeList))
	}

	allList, err := store.ListTokens(ctx, true)
	if err != nil {
		t.Fatalf("ListTokens(true) failed: %v", err)
	}
	if len(allList) != 1 {
		t.Errorf("expected 1 token in full list, got %d", len(allList))
	}
}

func TestStore_TouchLastUsed(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	tok := &APIToken{
		ID:        "id_touch",
		Name:      "TouchMe",
		TokenHash: "hash_touch",
		MaskedKey: "sk-touc...1234",
		CreatedAt: time.Now().UTC(),
	}
	if err := store.CreateToken(ctx, tok); err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	targetTime := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	if err := store.TouchLastUsed(ctx, "id_touch", targetTime); err != nil {
		t.Fatalf("TouchLastUsed failed: %v", err)
	}

	list, err := store.ListTokens(ctx, true)
	if err != nil {
		t.Fatalf("ListTokens failed: %v", err)
	}
	if len(list) != 1 || list[0].LastUsedAt == nil {
		t.Fatalf("expected last_used_at to be populated")
	}
	if !list[0].LastUsedAt.Equal(targetTime) {
		t.Errorf("expected %v, got %v", targetTime, list[0].LastUsedAt)
	}
}
