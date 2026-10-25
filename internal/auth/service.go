package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"go.uber.org/zap"
)

var (
	ErrAlreadyInitialized = apperr.PermissionDenied("SYSTEM_ALREADY_INITIALIZED", "system is already initialized", nil)
	ErrNotInitialized     = apperr.Precondition("SYSTEM_NOT_INITIALIZED", "system is not initialized", nil)
	ErrInvalidCredentials = apperr.Unauthenticated("INVALID_CREDENTIALS", "invalid username or password", nil)
	ErrPasswordMismatch   = apperr.Invalid("PASSWORD_MISMATCH", "passwords do not match", nil)
	ErrPasswordTooShort   = apperr.Invalid("PASSWORD_TOO_SHORT", "password must be at least 8 characters", nil)
	ErrPasswordTooLong    = apperr.Invalid("PASSWORD_TOO_LONG", "password is too long", nil)
	ErrTooManyAttempts    = apperr.RateLimited("TOO_MANY_ATTEMPTS", "too many failed login attempts", nil)
)

// AuthService defines the business operations for user authentication and session management.
type AuthService interface {
	GetStatus(ctx context.Context) (AuthStatusResponse, error)
	InitAdmin(ctx context.Context, req InitAdminRequest) (UserResponse, string, error)
	Login(ctx context.Context, clientIP string, req LoginRequest) (UserResponse, string, error)
	Logout(token string)
	GetCurrentUser(ctx context.Context, userID int64) (UserResponse, error)
	ValidateSession(token string) (SessionItem, bool)
	SessionTTL() time.Duration
}

const rateLimiterCleanupInterval = time.Minute

// RateLimiter tracks failed authentication attempts by client IP.
type RateLimiter struct {
	mu          sync.Mutex
	window      time.Duration
	maxAttempts int
	attempts    map[string][]time.Time
	inFlight    map[string]int
	lastCleanup time.Time
	nowFunc     func() time.Time
}

// Attempt is a reserved rate-limiter slot. Exactly one of Fail, Reset or Cancel
// must finish it; afterwards the attempt is inert, so Cancel can be deferred
// unconditionally as a safety net.
type Attempt struct {
	limiter  *RateLimiter
	ip       string
	finished bool
}

// NewRateLimiter creates an initialized rate limiter.
func NewRateLimiter(window time.Duration, maxAttempts int) *RateLimiter {
	if window <= 0 {
		window = 15 * time.Minute
	}
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	return &RateLimiter{
		window:      window,
		maxAttempts: maxAttempts,
		attempts:    make(map[string][]time.Time),
		inFlight:    make(map[string]int),
		nowFunc:     func() time.Time { return time.Now().UTC() },
	}
}

// Reserve claims one attempt slot for ip. It returns false when ip has already
// spent its failures, in which case nothing was reserved.
func (r *RateLimiter) Reserve(ip string) (*Attempt, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.nowFunc()
	r.pruneExpiredLocked(now)
	r.pruneLocked(ip, now)

	if len(r.attempts[ip])+r.inFlight[ip] >= r.maxAttempts {
		return nil, false
	}
	r.inFlight[ip]++
	return &Attempt{limiter: r, ip: ip}, true
}

// Fail finishes the attempt and records it as a credential failure.
func (a *Attempt) Fail() {
	a.finish(func(r *RateLimiter, ip string) {
		r.attempts[ip] = append(r.attempts[ip], r.nowFunc())
	})
}

// Reset finishes the attempt and clears the IP's failure history after a
// successful authentication.
func (a *Attempt) Reset() {
	a.finish(func(r *RateLimiter, ip string) {
		delete(r.attempts, ip)
	})
}

// Cancel finishes the attempt without recording an outcome. Infrastructure
// errors must not count as password failures, and a reservation that is never
// finished would lock the IP out for good.
func (a *Attempt) Cancel() {
	a.finish(nil)
}

func (a *Attempt) finish(record func(*RateLimiter, string)) {
	if a == nil {
		return
	}

	limiter := a.limiter
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	if a.finished {
		return
	}
	a.finished = true
	limiter.releaseLocked(a.ip)
	if record != nil {
		record(limiter, a.ip)
	}
}

// releaseLocked returns one reserved slot for ip.
func (r *RateLimiter) releaseLocked(ip string) {
	if r.inFlight[ip] <= 1 {
		delete(r.inFlight, ip)
		return
	}
	r.inFlight[ip]--
}

// pruneExpiredLocked sweeps every tracked IP at most once per cleanup interval.
func (r *RateLimiter) pruneExpiredLocked(now time.Time) {
	if !r.lastCleanup.IsZero() && now.Sub(r.lastCleanup) < rateLimiterCleanupInterval {
		return
	}

	for ip := range r.attempts {
		r.pruneLocked(ip, now)
	}
	r.lastCleanup = now
}

// pruneLocked drops the failures that fell outside ip's sliding window. The slice
// is filtered in place, so tracking an IP does not allocate per attempt.
func (r *RateLimiter) pruneLocked(ip string, now time.Time) {
	cutoff := now.Add(-r.window)
	attempts := r.attempts[ip]
	valid := attempts[:0]
	for _, attempt := range attempts {
		if attempt.After(cutoff) {
			valid = append(valid, attempt)
		}
	}

	if len(valid) == 0 {
		delete(r.attempts, ip)
		return
	}
	r.attempts[ip] = valid
}

type authService struct {
	userStore    UserStore
	sessionStore SessionStore
	limiter      *RateLimiter
	logger       *zap.Logger
}

// NewAuthService constructs an implementation of AuthService.
func NewAuthService(
	userStore UserStore,
	sessionStore SessionStore,
	limiter *RateLimiter,
	logger *zap.Logger,
) AuthService {
	if limiter == nil {
		limiter = NewRateLimiter(15*time.Minute, 5)
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &authService{
		userStore:    userStore,
		sessionStore: sessionStore,
		limiter:      limiter,
		logger:       logger,
	}
}

func (s *authService) GetStatus(ctx context.Context) (AuthStatusResponse, error) {
	hasUser, err := s.userStore.HasUser(ctx)
	if err != nil {
		return AuthStatusResponse{}, fmt.Errorf("check initialized status: %w", err)
	}
	return AuthStatusResponse{Initialized: hasUser}, nil
}

func (s *authService) InitAdmin(ctx context.Context, req InitAdminRequest) (UserResponse, string, error) {
	if req.Password != req.ConfirmPassword {
		return UserResponse{}, "", ErrPasswordMismatch
	}
	if err := validateNewPassword(req.Password); err != nil {
		return UserResponse{}, "", err
	}

	hasUser, err := s.userStore.HasUser(ctx)
	if err != nil {
		return UserResponse{}, "", fmt.Errorf("verify user existence: %w", err)
	}
	if hasUser {
		return UserResponse{}, "", ErrAlreadyInitialized
	}

	hash, err := hashPassword(req.Password)
	if err != nil {
		return UserResponse{}, "", fmt.Errorf("hash password: %w", err)
	}

	user := &User{
		Username:     req.Username,
		PasswordHash: hash,
	}
	// The store re-checks emptiness inside its transaction, so a concurrent
	// initialization loses here instead of creating a second administrator.
	if err := s.userStore.Create(ctx, user); err != nil {
		if errors.Is(err, ErrUserAlreadyExists) {
			return UserResponse{}, "", ErrAlreadyInitialized
		}
		return UserResponse{}, "", fmt.Errorf("create admin: %w", err)
	}

	token, err := s.sessionStore.Create(user.ID, user.Username)
	if err != nil {
		return UserResponse{}, "", fmt.Errorf("create initial session: %w", err)
	}

	s.logger.Info("administrator initialized successfully", zap.String("username", user.Username))
	return userResponse(user), token, nil
}

func (s *authService) Login(ctx context.Context, clientIP string, req LoginRequest) (UserResponse, string, error) {
	attempt, allowed := s.limiter.Reserve(clientIP)
	if !allowed {
		s.logger.Warn("login attempt rejected by rate limiter", zap.String("clientIP", clientIP))
		return UserResponse{}, "", ErrTooManyAttempts
	}
	// Fail and Reset already finish the attempt; a deferred Cancel only fires on an
	// unexpected return path, so a future edit cannot leak the reservation.
	defer attempt.Cancel()

	hasUser, err := s.userStore.HasUser(ctx)
	if err != nil {
		return UserResponse{}, "", fmt.Errorf("check initialized: %w", err)
	}
	if !hasUser {
		return UserResponse{}, "", ErrNotInitialized
	}

	user, err := s.userStore.GetByUsername(ctx, req.Username)
	if err != nil && !errors.Is(err, ErrUserNotFound) {
		return UserResponse{}, "", fmt.Errorf("get user for login: %w", err)
	}

	passwordHash := ""
	if user != nil {
		passwordHash = user.PasswordHash
	}
	// Always run the comparison: for an unknown account it burns the same time
	// against a throwaway hash instead of returning early.
	matched := verifyPassword(passwordHash, req.Password)
	if !matched || user == nil {
		attempt.Fail()
		return UserResponse{}, "", ErrInvalidCredentials
	}

	attempt.Reset()

	token, err := s.sessionStore.Create(user.ID, user.Username)
	if err != nil {
		return UserResponse{}, "", fmt.Errorf("create login session: %w", err)
	}

	s.logger.Info("user logged in successfully", zap.String("username", user.Username), zap.String("clientIP", clientIP))
	return userResponse(user), token, nil
}

func (s *authService) Logout(token string) {
	if token != "" {
		s.sessionStore.Delete(token)
	}
}

func (s *authService) GetCurrentUser(ctx context.Context, userID int64) (UserResponse, error) {
	user, err := s.userStore.GetByID(ctx, userID)
	if err != nil {
		return UserResponse{}, err
	}
	return userResponse(user), nil
}

func (s *authService) ValidateSession(token string) (SessionItem, bool) {
	return s.sessionStore.Get(token)
}

func (s *authService) SessionTTL() time.Duration {
	if s.sessionStore != nil {
		return s.sessionStore.TTL()
	}
	return DefaultSessionTTL
}

// userResponse projects a persisted account onto the client-facing response type.
func userResponse(user *User) UserResponse {
	return UserResponse{
		ID:        user.ID,
		Username:  user.Username,
		CreatedAt: user.CreatedAt,
	}
}
