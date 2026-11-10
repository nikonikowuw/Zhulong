package apitoken

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

func newTestService(t *testing.T) (*Service, func()) {
	t.Helper()
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	store := NewStore(dbStore)
	svc := NewService(store, zap.NewNop())
	cleanup := func() {
		if err := svc.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
		if err := dbStore.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}
	return svc, cleanup
}

func TestService_CreateToken_SuccessAndLimit(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()

	// Single creation
	tok, raw, err := svc.CreateToken(ctx, "Platform-1")
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}
	if tok.Name != "Platform-1" {
		t.Errorf("expected Platform-1, got %q", tok.Name)
	}
	if tok.TokenHash != hashToken(raw) {
		t.Errorf("token hash mismatch")
	}

	// Concurrent creation racing for the 10 limit
	const workers = 15
	var wg sync.WaitGroup
	var mu sync.Mutex
	var successCount int
	var conflictCount int

	for i := 2; i <= workers+1; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, _, cErr := svc.CreateToken(ctx, fmt.Sprintf("Worker-%02d", idx))
			mu.Lock()
			defer mu.Unlock()
			if cErr == nil {
				successCount++
				return
			}
			var appErr *apperr.Error
			if errors.As(cErr, &appErr) && appErr.Code == "API_TOKEN_LIMIT_REACHED" {
				conflictCount++
				return
			}
			t.Errorf("unexpected error: %v", cErr)
		}(i)
	}
	wg.Wait()

	// Platform-1 was 1, so 9 more should succeed out of 15, and 6 should hit limit
	if successCount != 9 {
		t.Errorf("expected 9 concurrent successes, got %d", successCount)
	}
	if conflictCount != 6 {
		t.Errorf("expected 6 limit conflicts, got %d", conflictCount)
	}
}

func TestService_NameConflict(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()

	tok, _, err := svc.CreateToken(ctx, "Test-Same-Name")
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Immediate duplicate
	_, _, err = svc.CreateToken(ctx, "Test-Same-Name")
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "API_TOKEN_NAME_ALREADY_EXISTS" {
		t.Fatalf("expected API_TOKEN_NAME_ALREADY_EXISTS, got %v", err)
	}

	// Duplicate after trim
	_, _, err = svc.CreateToken(ctx, "  Test-Same-Name  ")
	if !errors.As(err, &appErr) || appErr.Code != "API_TOKEN_NAME_ALREADY_EXISTS" {
		t.Fatalf("expected API_TOKEN_NAME_ALREADY_EXISTS after trim, got %v", err)
	}

	// Soft revoke the token
	if err := svc.RevokeToken(ctx, tok.ID); err != nil {
		t.Fatalf("RevokeToken failed: %v", err)
	}

	// Should still reject reuse of revoked token name
	_, _, err = svc.CreateToken(ctx, "Test-Same-Name")
	if !errors.As(err, &appErr) || appErr.Code != "API_TOKEN_NAME_ALREADY_EXISTS" {
		t.Fatalf("expected API_TOKEN_NAME_ALREADY_EXISTS even after token revocation, got %v", err)
	}
}

func TestService_Authenticate(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()

	tok, raw, err := svc.CreateToken(ctx, "Auth-Token")
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Valid authentication
	identity, err := svc.Authenticate(ctx, raw)
	if err != nil {
		t.Fatalf("Authenticate failed: %v", err)
	}
	if identity.ID != tok.ID || identity.Name != tok.Name {
		t.Errorf("unexpected identity: %+v", identity)
	}

	// Invalid token
	_, err = svc.Authenticate(ctx, "sk-invalid0000000000000000000000000000000000000000000000000000000000000000")
	if !errors.Is(err, errUnauthorized) {
		t.Fatalf("expected errUnauthorized for bad token, got %v", err)
	}

	// Revoked token
	if err := svc.RevokeToken(ctx, tok.ID); err != nil {
		t.Fatalf("RevokeToken failed: %v", err)
	}
	_, err = svc.Authenticate(ctx, raw)
	if !errors.Is(err, errUnauthorized) {
		t.Fatalf("expected errUnauthorized for revoked token, got %v", err)
	}
}

func TestService_Close_GracefulDrain(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()

	_, raw, err := svc.CreateToken(ctx, "Drain-Token")
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Run concurrent Authenticate calls
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.Authenticate(ctx, raw); err != nil {
				t.Errorf("Authenticate failed: %v", err)
			}
		}()
	}
	wg.Wait()

	// Calling Close multiple times must be idempotent and drain without panic
	if err := svc.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if err := svc.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}
}

func TestService_ConcurrentCloseRace(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()

	_, raw, err := svc.CreateToken(ctx, "Race-Token")
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	var wg sync.WaitGroup
	stopCallers := make(chan struct{})

	// Spawn continuous authentication callers to aggressively trigger touchLastUsed
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopCallers:
					return
				default:
					if _, err := svc.Authenticate(ctx, raw); err != nil {
						t.Errorf("Authenticate failed: %v", err)
					}
				}
			}
		}()
	}

	// Concurrently close the service while callers are active
	time.Sleep(10 * time.Millisecond)
	if err := svc.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	close(stopCallers)
	wg.Wait()
}

func TestService_InvalidTokenName(t *testing.T) {
	svc, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()

	_, _, err := svc.CreateToken(ctx, "Bad\nName")
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "API_TOKEN_NAME_INVALID" {
		t.Fatalf("expected API_TOKEN_NAME_INVALID, got %v", err)
	}
}
