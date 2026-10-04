package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrAlreadyInitialized = errors.New("system is already initialized")
	ErrNotInitialized     = errors.New("system is not initialized")
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrPasswordMismatch   = errors.New("passwords do not match")
	ErrPasswordTooShort   = errors.New("password must be at least 8 characters")
	ErrTooManyAttempts    = errors.New("too many failed login attempts")
)

// AuthService defines the business operations for user authentication and session management.
type AuthService interface {
	GetStatus(ctx context.Context) (AuthStatusResponse, error)
	InitAdmin(ctx context.Context, req InitAdminRequest) (UserResponse, string, error)
	Login(ctx context.Context, clientIP string, req LoginRequest) (UserResponse, string, error)
	Logout(token string)
	GetCurrentUser(ctx context.Context, userID int64) (UserResponse, error)
	ValidateSession(token string) (SessionItem, bool)
}

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

const rateLimiterCleanupInterval = time.Minute

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

// Allow reserves one slot when the given IP is below its failed-attempt limit.
func (r *RateLimiter) Allow(ip string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.nowFunc()
	r.pruneExpiredLocked(now)

	cutoff := now.Add(-r.window)
	times := r.attempts[ip]
	valid := times[:0]
	for _, attempt := range times {
		if attempt.After(cutoff) {
			valid = append(valid, attempt)
		}
	}
	if len(valid) == 0 {
		delete(r.attempts, ip)
	} else {
		r.attempts[ip] = valid
	}

	if len(valid)+r.inFlight[ip] >= r.maxAttempts {
		return false
	}
	r.inFlight[ip]++
	return true
}

// RecordFailure completes a reserved attempt and records its failure.
func (r *RateLimiter) RecordFailure(ip string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finishAttemptLocked(ip)
	r.attempts[ip] = append(r.attempts[ip], r.nowFunc())
}

// Reset completes a reserved attempt and clears the IP's failed-attempt history.
func (r *RateLimiter) Reset(ip string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finishAttemptLocked(ip)
	delete(r.attempts, ip)
}

func (r *RateLimiter) Cancel(ip string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finishAttemptLocked(ip)
}

func (r *RateLimiter) finishAttemptLocked(ip string) {
	if r.inFlight[ip] <= 1 {
		delete(r.inFlight, ip)
		return
	}
	r.inFlight[ip]--
}

func (r *RateLimiter) pruneExpiredLocked(now time.Time) {
	if !r.lastCleanup.IsZero() && now.Sub(r.lastCleanup) < rateLimiterCleanupInterval {
		return
	}

	cutoff := now.Add(-r.window)
	for ip, attempts := range r.attempts {
		valid := attempts[:0]
		for _, attempt := range attempts {
			if attempt.After(cutoff) {
				valid = append(valid, attempt)
			}
		}
		if len(valid) == 0 {
			delete(r.attempts, ip)
		} else {
			r.attempts[ip] = valid
		}
	}
	r.lastCleanup = now
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
	if len(req.Password) < 8 {
		return UserResponse{}, "", ErrPasswordTooShort
	}

	hasUser, err := s.userStore.HasUser(ctx)
	if err != nil {
		return UserResponse{}, "", fmt.Errorf("verify user existence: %w", err)
	}
	if hasUser {
		return UserResponse{}, "", ErrAlreadyInitialized
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return UserResponse{}, "", fmt.Errorf("hash password: %w", err)
	}

	user := &User{
		Username:     req.Username,
		PasswordHash: string(hash),
	}
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
	return UserResponse{
		ID:        user.ID,
		Username:  user.Username,
		CreatedAt: user.CreatedAt,
	}, token, nil
}

func (s *authService) Login(ctx context.Context, clientIP string, req LoginRequest) (UserResponse, string, error) {
	if !s.limiter.Allow(clientIP) {
		s.logger.Warn("login attempt rejected by rate limiter", zap.String("clientIP", clientIP))
		return UserResponse{}, "", ErrTooManyAttempts
	}

	hasUser, err := s.userStore.HasUser(ctx)
	if err != nil {
		s.limiter.Cancel(clientIP)
		return UserResponse{}, "", fmt.Errorf("check initialized: %w", err)
	}
	if !hasUser {
		s.limiter.Cancel(clientIP)
		return UserResponse{}, "", ErrNotInitialized
	}

	user, err := s.userStore.GetByUsername(ctx, req.Username)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			s.limiter.RecordFailure(clientIP)
			return UserResponse{}, "", ErrInvalidCredentials
		}
		s.limiter.Cancel(clientIP)
		return UserResponse{}, "", fmt.Errorf("get user for login: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		s.limiter.RecordFailure(clientIP)
		return UserResponse{}, "", ErrInvalidCredentials
	}

	s.limiter.Reset(clientIP)

	token, err := s.sessionStore.Create(user.ID, user.Username)
	if err != nil {
		return UserResponse{}, "", fmt.Errorf("create login session: %w", err)
	}

	s.logger.Info("user logged in successfully", zap.String("username", user.Username), zap.String("clientIP", clientIP))
	return UserResponse{
		ID:        user.ID,
		Username:  user.Username,
		CreatedAt: user.CreatedAt,
	}, token, nil
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
	return UserResponse{
		ID:        user.ID,
		Username:  user.Username,
		CreatedAt: user.CreatedAt,
	}, nil
}

func (s *authService) ValidateSession(token string) (SessionItem, bool) {
	return s.sessionStore.Get(token)
}
