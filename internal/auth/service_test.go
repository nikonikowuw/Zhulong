package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestAuthService_InitAndStatus(t *testing.T) {
	dbStore := setupTestDB(t)
	userStore := NewUserStore(dbStore)
	sessionStore := NewMemorySessionStore(1 * time.Hour)
	svc := NewAuthService(userStore, sessionStore, nil, zap.NewNop())
	ctx := context.Background()

	// Initial status
	status, err := svc.GetStatus(ctx)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.Initialized {
		t.Fatal("expected uninitialized status initially")
	}

	// Mismatched password
	_, _, err = svc.InitAdmin(ctx, InitAdminRequest{
		Username:        "admin",
		Password:        "secret123",
		ConfirmPassword: "differentPassword",
	})
	if err != ErrPasswordMismatch {
		t.Fatalf("expected ErrPasswordMismatch, got %v", err)
	}

	// Password too short
	_, _, err = svc.InitAdmin(ctx, InitAdminRequest{
		Username:        "admin",
		Password:        "short",
		ConfirmPassword: "short",
	})
	if err != ErrPasswordTooShort {
		t.Fatalf("expected ErrPasswordTooShort, got %v", err)
	}

	// Successful init
	userResp, token, err := svc.InitAdmin(ctx, InitAdminRequest{
		Username:        "admin",
		Password:        "password123",
		ConfirmPassword: "password123",
	})
	if err != nil {
		t.Fatalf("InitAdmin: %v", err)
	}
	if userResp.Username != "admin" || token == "" {
		t.Fatalf("unexpected user response: %+v, token: %s", userResp, token)
	}

	// Status should now be initialized
	status, err = svc.GetStatus(ctx)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if !status.Initialized {
		t.Fatal("expected initialized status after init")
	}

	// Second init attempt must be rejected with ErrAlreadyInitialized
	_, _, err = svc.InitAdmin(ctx, InitAdminRequest{
		Username:        "admin2",
		Password:        "password123",
		ConfirmPassword: "password123",
	})
	if err != ErrAlreadyInitialized {
		t.Fatalf("expected ErrAlreadyInitialized, got %v", err)
	}
}

func TestRateLimiter_ConcurrentAdmissionsAreReserved(t *testing.T) {
	const (
		maxAttempts = 5
		callers     = 100
	)
	limiter := NewRateLimiter(time.Minute, maxAttempts)
	start := make(chan struct{})
	admitted := make(chan struct{}, callers)
	var wait sync.WaitGroup

	for i := 0; i < callers; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			if limiter.Allow("192.0.2.1") {
				admitted <- struct{}{}
			}
		}()
	}
	close(start)
	wait.Wait()
	close(admitted)

	reserved := 0
	for range admitted {
		reserved++
	}
	if reserved != maxAttempts {
		t.Fatalf("expected %d concurrent reservations, got %d", maxAttempts, reserved)
	}
	for i := 0; i < reserved; i++ {
		limiter.RecordFailure("192.0.2.1")
	}
	if limiter.Allow("192.0.2.1") {
		t.Fatal("expected IP to be blocked after the failed-attempt limit")
	}
}

func TestRateLimiter_CleansExpiredEntriesAcrossIPs(t *testing.T) {
	now := time.Now().UTC()
	limiter := NewRateLimiter(time.Minute, 5)
	limiter.nowFunc = func() time.Time { return now }

	for _, ip := range []string{"192.0.2.1", "192.0.2.2"} {
		if !limiter.Allow(ip) {
			t.Fatalf("expected %s to be admitted", ip)
		}
		limiter.RecordFailure(ip)
	}

	now = now.Add(rateLimiterCleanupInterval + time.Minute)
	if !limiter.Allow("192.0.2.3") {
		t.Fatal("expected a new IP to be admitted")
	}
	if len(limiter.attempts) != 0 {
		t.Fatalf("expected expired entries to be removed, got %v", limiter.attempts)
	}
	limiter.Cancel("192.0.2.3")
}

type userLookupErrorStore struct {
	UserStore
	err error
}

func (s userLookupErrorStore) HasUser(context.Context) (bool, error) {
	return true, nil
}

func (s userLookupErrorStore) GetByUsername(context.Context, string) (*User, error) {
	return nil, s.err
}

func TestAuthService_LoginPropagatesUserStoreFailures(t *testing.T) {
	cause := errors.New("database unavailable")
	limiter := NewRateLimiter(time.Minute, 1)
	service := NewAuthService(
		userLookupErrorStore{err: cause},
		NewMemorySessionStore(time.Hour),
		limiter,
		zap.NewNop(),
	)

	_, _, err := service.Login(context.Background(), "192.0.2.1", LoginRequest{Username: "admin", Password: "password123"})
	if !errors.Is(err, cause) || errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected wrapped database failure, got %v", err)
	}
	if !limiter.Allow("192.0.2.1") {
		t.Fatal("database failure should release the reserved attempt")
	}
	limiter.Cancel("192.0.2.1")
}

func TestAuthService_LoginAndRateLimiter(t *testing.T) {
	dbStore := setupTestDB(t)
	userStore := NewUserStore(dbStore)
	sessionStore := NewMemorySessionStore(1 * time.Hour)
	limiter := NewRateLimiter(15*time.Minute, 3) // 3 attempts for test
	svc := NewAuthService(userStore, sessionStore, limiter, zap.NewNop())
	ctx := context.Background()

	// Login before init returns ErrNotInitialized
	_, _, err := svc.Login(ctx, "127.0.0.1", LoginRequest{
		Username: "admin",
		Password: "password123",
	})
	if err != ErrNotInitialized {
		t.Fatalf("expected ErrNotInitialized, got %v", err)
	}

	// Init
	_, _, err = svc.InitAdmin(ctx, InitAdminRequest{
		Username:        "admin",
		Password:        "password123",
		ConfirmPassword: "password123",
	})
	if err != nil {
		t.Fatalf("InitAdmin: %v", err)
	}

	// Incorrect password attempt 1
	_, _, err = svc.Login(ctx, "127.0.0.1", LoginRequest{
		Username: "admin",
		Password: "wrongpassword",
	})
	if err != ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}

	// Incorrect password attempt 2
	_, _, err = svc.Login(ctx, "127.0.0.1", LoginRequest{
		Username: "admin",
		Password: "wrongpassword",
	})
	if err != ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}

	// Incorrect password attempt 3 -> triggers limiter on next try
	_, _, err = svc.Login(ctx, "127.0.0.1", LoginRequest{
		Username: "admin",
		Password: "wrongpassword",
	})
	if err != ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}

	// Attempt 4 from same IP should be blocked by rate limiter
	_, _, err = svc.Login(ctx, "127.0.0.1", LoginRequest{
		Username: "admin",
		Password: "password123",
	})
	if err != ErrTooManyAttempts {
		t.Fatalf("expected ErrTooManyAttempts, got %v", err)
	}

	// Different IP should still be allowed to login with valid credentials
	userResp, token, err := svc.Login(ctx, "192.168.1.100", LoginRequest{
		Username: "admin",
		Password: "password123",
	})
	if err != nil {
		t.Fatalf("Login from different IP: %v", err)
	}
	if userResp.Username != "admin" || token == "" {
		t.Fatalf("unexpected login response: %+v", userResp)
	}

	// Validate session
	sessionItem, ok := svc.ValidateSession(token)
	if !ok || sessionItem.Username != "admin" {
		t.Fatalf("ValidateSession failed: %+v", sessionItem)
	}

	// Logout
	svc.Logout(token)
	_, ok = svc.ValidateSession(token)
	if ok {
		t.Fatal("session should be invalidated after logout")
	}
}
