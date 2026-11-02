package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

func newTestService(t *testing.T) (AuthService, *database.Store, *RateLimiter) {
	t.Helper()
	dbStore := setupTestDB(t)
	limiter := NewRateLimiter(15*time.Minute, 5)
	service := NewAuthService(
		NewUserStore(dbStore),
		NewMemorySessionStore(1*time.Hour),
		limiter,
		zap.NewNop(),
	)
	return service, dbStore, limiter
}

func TestAuthService_InitAndStatus(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	status, err := svc.GetStatus(ctx)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.Initialized {
		t.Fatal("expected uninitialized status initially")
	}

	if _, _, err := svc.InitAdmin(ctx, InitAdminRequest{
		Username:        "admin",
		Password:        "secret123",
		ConfirmPassword: "differentPassword",
	}); !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("expected ErrPasswordMismatch, got %v", err)
	}

	if _, _, err := svc.InitAdmin(ctx, InitAdminRequest{
		Username:        "admin",
		Password:        "short",
		ConfirmPassword: "short",
	}); !errors.Is(err, ErrPasswordTooShort) {
		t.Fatalf("expected ErrPasswordTooShort, got %v", err)
	}

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
	if userResp.CreatedAt.IsZero() {
		t.Fatalf("expected createdAt to be populated, got %+v", userResp)
	}

	status, err = svc.GetStatus(ctx)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if !status.Initialized {
		t.Fatal("expected initialized status after init")
	}

	if _, _, err := svc.InitAdmin(ctx, InitAdminRequest{
		Username:        "admin2",
		Password:        "password123",
		ConfirmPassword: "password123",
	}); !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("expected ErrAlreadyInitialized, got %v", err)
	}
}

// A password can satisfy the rune-based request contract and still exceed the
// byte limit bcrypt can hash; the service must reject it as a client error
// instead of forwarding a hash failure as an internal error.
func TestAuthService_InitAdminRejectsPasswordBeyondBcryptLimit(t *testing.T) {
	svc, dbStore, _ := newTestService(t)
	ctx := context.Background()
	password := strings.Repeat("密", 64) // 64 runes, 192 bytes

	_, _, err := svc.InitAdmin(ctx, InitAdminRequest{
		Username:        "admin",
		Password:        password,
		ConfirmPassword: password,
	})
	if !errors.Is(err, ErrPasswordTooLong) {
		t.Fatalf("expected ErrPasswordTooLong, got %v", err)
	}

	users, err := userCount(dbStore)
	if err != nil {
		t.Fatalf("count users: %v", err)
	}
	if users != 0 {
		t.Fatalf("rejected password must not create an account, got %d users", users)
	}
}

func TestAuthService_InitAdminAcceptsMultibyteWithinLimit(t *testing.T) {
	svc, _, _ := newTestService(t)
	password := strings.Repeat("密", 8) // 8 runes, 24 bytes: inside both bounds

	if _, _, err := svc.InitAdmin(context.Background(), InitAdminRequest{
		Username:        "admin",
		Password:        password,
		ConfirmPassword: password,
	}); err != nil {
		t.Fatalf("InitAdmin with multibyte password: %v", err)
	}
}

// Initialization must stay single-user even when several callers race: the store
// re-checks emptiness inside its insert transaction.
func TestAuthService_ConcurrentInitKeepsSingleUser(t *testing.T) {
	svc, dbStore, _ := newTestService(t)
	const callers = 8

	errs := make([]error, callers)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for i := range callers {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			<-start
			_, _, errs[i] = svc.InitAdmin(context.Background(), InitAdminRequest{
				Username:        fmt.Sprintf("admin%d", i),
				Password:        "password123",
				ConfirmPassword: "password123",
			})
		}(i)
	}
	close(start)
	wait.Wait()

	succeeded := 0
	for i, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case !errors.Is(err, ErrAlreadyInitialized):
			t.Errorf("caller %d: expected ErrAlreadyInitialized, got %v", i, err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("expected exactly one successful initialization, got %d", succeeded)
	}

	users, err := userCount(dbStore)
	if err != nil {
		t.Fatalf("count users: %v", err)
	}
	if users != 1 {
		t.Fatalf("expected exactly one persisted user, got %d", users)
	}
}

func TestRateLimiter_ConcurrentReservationsAreBounded(t *testing.T) {
	const (
		maxAttempts = 5
		callers     = 100
	)
	limiter := NewRateLimiter(time.Minute, maxAttempts)
	start := make(chan struct{})
	admitted := make(chan *Attempt, callers)
	var wait sync.WaitGroup

	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			if attempt, ok := limiter.Reserve("192.0.2.1"); ok {
				admitted <- attempt
			}
		}()
	}
	close(start)
	wait.Wait()
	close(admitted)

	reserved := 0
	for attempt := range admitted {
		reserved++
		// Every admission is a real failed login here, so it must also consume the
		// window budget once the slot is released.
		attempt.Fail()
	}
	if reserved != maxAttempts {
		t.Fatalf("expected %d concurrent reservations, got %d", maxAttempts, reserved)
	}
	if attempt, ok := limiter.Reserve("192.0.2.1"); ok {
		attempt.Cancel()
		t.Fatal("expected IP to be blocked after the failed-attempt limit")
	}
}

func TestRateLimiter_CleansExpiredEntriesAcrossIPs(t *testing.T) {
	now := time.Now().UTC()
	limiter := NewRateLimiter(time.Minute, 5)
	limiter.nowFunc = func() time.Time { return now }

	for _, ip := range []string{"192.0.2.1", "192.0.2.2"} {
		attempt, ok := limiter.Reserve(ip)
		if !ok {
			t.Fatalf("expected %s to be admitted", ip)
		}
		attempt.Fail()
	}

	now = now.Add(rateLimiterCleanupInterval + time.Minute)
	attempt, ok := limiter.Reserve("192.0.2.3")
	if !ok {
		t.Fatal("expected a new IP to be admitted")
	}
	attempt.Cancel()

	if len(limiter.attempts) != 0 {
		t.Fatalf("expected expired entries to be removed, got %v", limiter.attempts)
	}
}

// Attempt is idempotent, so the deferred Cancel in Login is a safety net rather
// than a second release.
func TestAttempt_FinishIsIdempotent(t *testing.T) {
	limiter := NewRateLimiter(time.Minute, 2)

	attempt, ok := limiter.Reserve("192.0.2.1")
	if !ok {
		t.Fatal("expected first reservation to succeed")
	}
	attempt.Fail()
	attempt.Cancel()
	attempt.Reset()

	if len(limiter.attempts["192.0.2.1"]) != 1 {
		t.Fatalf("expected exactly one recorded failure, got %v", limiter.attempts["192.0.2.1"])
	}
	if limiter.inFlight["192.0.2.1"] != 0 {
		t.Fatalf("expected the slot to be released once, got %d", limiter.inFlight["192.0.2.1"])
	}
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

	attempt, ok := limiter.Reserve("192.0.2.1")
	if !ok {
		t.Fatal("database failure should release the reserved attempt")
	}
	attempt.Cancel()
}

// An unknown username and a wrong password must be indistinguishable to the
// client, including how much of the attempt budget they consume.
func TestAuthService_UnknownUsernameConsumesAttempt(t *testing.T) {
	limiter := NewRateLimiter(time.Minute, 2)
	dbStore := setupTestDB(t)
	svc := NewAuthService(
		NewUserStore(dbStore),
		NewMemorySessionStore(time.Hour),
		limiter,
		zap.NewNop(),
	)
	ctx := context.Background()

	if _, _, err := svc.InitAdmin(ctx, InitAdminRequest{
		Username:        "admin",
		Password:        "password123",
		ConfirmPassword: "password123",
	}); err != nil {
		t.Fatalf("InitAdmin: %v", err)
	}

	credentials := []LoginRequest{
		{Username: "ghost", Password: "password123"},
		{Username: "admin", Password: "wrongpassword"},
	}
	for _, req := range credentials {
		if _, _, err := svc.Login(ctx, "192.0.2.9", req); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("login as %q: expected ErrInvalidCredentials, got %v", req.Username, err)
		}
	}

	if _, _, err := svc.Login(ctx, "192.0.2.9", LoginRequest{
		Username: "admin",
		Password: "password123",
	}); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("expected ErrTooManyAttempts after both failures, got %v", err)
	}
}

func TestAuthService_LoginAndRateLimiter(t *testing.T) {
	dbStore := setupTestDB(t)
	userStore := NewUserStore(dbStore)
	sessionStore := NewMemorySessionStore(1 * time.Hour)
	limiter := NewRateLimiter(15*time.Minute, 3) // 3 attempts for test
	svc := NewAuthService(
		userStore,
		sessionStore,
		limiter,
		zap.NewNop(),
	)
	ctx := context.Background()

	if _, _, err := svc.Login(ctx, "127.0.0.1", LoginRequest{
		Username: "admin",
		Password: "password123",
	}); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("expected ErrNotInitialized, got %v", err)
	}

	if _, _, err := svc.InitAdmin(ctx, InitAdminRequest{
		Username:        "admin",
		Password:        "password123",
		ConfirmPassword: "password123",
	}); err != nil {
		t.Fatalf("InitAdmin: %v", err)
	}

	for attempt := 1; attempt <= 3; attempt++ {
		if _, _, err := svc.Login(ctx, "127.0.0.1", LoginRequest{
			Username: "admin",
			Password: "wrongpassword",
		}); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: expected ErrInvalidCredentials, got %v", attempt, err)
		}
	}

	// The fourth attempt from the same IP is rejected before the password check.
	if _, _, err := svc.Login(ctx, "127.0.0.1", LoginRequest{
		Username: "admin",
		Password: "password123",
	}); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("expected ErrTooManyAttempts, got %v", err)
	}

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

	sessionItem, ok := svc.ValidateSession(token)
	if !ok || sessionItem.Username != "admin" {
		t.Fatalf("ValidateSession failed: %+v", sessionItem)
	}

	svc.Logout(token)
	if _, ok := svc.ValidateSession(token); ok {
		t.Fatal("session should be invalidated after logout")
	}
}
