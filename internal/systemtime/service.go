package systemtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/audit"
	"go.uber.org/zap"
)

var (
	// ErrManualMode occurs when attempting NTP sync while in manual mode.
	ErrManualMode = errors.New("cannot perform ntp sync while in manual mode")
	// ErrSyncInProgress occurs when an NTP sync is already active.
	ErrSyncInProgress = errors.New("ntp sync is already in progress")
)

// TimeService provides business orchestration for system time management.
type TimeService struct {
	repo       Repository
	driver     ClockDriver
	sntpClient SNTPClient
	sm         *TimeStateMachine
	logger     *zap.Logger
	auditor    *audit.Service

	workerMu sync.Mutex
	cancel   context.CancelFunc
	doneCh   chan struct{}
	wakeCh   chan struct{}
	syncMu   sync.Mutex
}

// NewTimeService constructs a new TimeService.
func NewTimeService(
	repo Repository,
	driver ClockDriver,
	sntpClient SNTPClient,
	logger *zap.Logger,
) *TimeService {
	if logger == nil {
		logger = zap.NewNop()
	}
	if sntpClient == nil {
		sntpClient = NewSNTPClient()
	}
	if driver == nil {
		driver = NewDefaultClockDriver()
	}

	sm := NewTimeStateMachine(driver)

	return &TimeService{
		repo:       repo,
		driver:     driver,
		sntpClient: sntpClient,
		sm:         sm,
		logger:     logger,
		wakeCh:     make(chan struct{}, 1),
	}
}

// SetAuditor binds the audit logging service.
func (s *TimeService) SetAuditor(auditor *audit.Service) {
	s.auditor = auditor
}

// GetStatus returns the complete system time and hardware status.
func (s *TimeService) GetStatus(ctx context.Context) (*SystemTimeStatus, error) {
	sysTime, err := s.driver.GetSystemTime()
	if err != nil {
		sysTime = time.Now()
	}

	tz, err := s.driver.GetTimezone()
	if err != nil || tz == "" {
		tz = "UTC"
	}

	cfg, servers, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("get time config: %w", err)
	}

	return &SystemTimeStatus{
		CurrentTime:         sysTime,
		Timezone:            tz,
		Mode:                cfg.Mode,
		NTPServers:          servers,
		SyncIntervalSeconds: cfg.SyncIntervalSeconds,
		SyncStatus:          s.sm.GetSyncStatus(),
		RTCStatus:           s.driver.GetRTCStatus(),
		HasPermission:       s.driver.HasClockPermission(),
	}, nil
}

// UpdateConfig updates time configuration (mode, NTP servers, interval, timezone).
func (s *TimeService) UpdateConfig(ctx context.Context, req UpdateConfigRequest) (*SystemTimeStatus, error) {
	// Validate timezone identifier
	if _, err := time.LoadLocation(req.Timezone); err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w", req.Timezone, err)
	}

	// Apply timezone to system
	if err := s.driver.ApplyTimezone(req.Timezone); err != nil {
		s.logger.Warn("Failed to apply timezone to system", zap.String("tz", req.Timezone), zap.Error(err))
	}

	// Save configuration to repository
	_, _, err := s.repo.UpdateConfig(ctx, req.Mode, req.NTPServers, req.SyncIntervalSeconds, req.Timezone)
	if err != nil {
		return nil, fmt.Errorf("update time config: %w", err)
	}

	// Record audit event
	if s.auditor != nil {
		s.auditor.Record(audit.Entry{
			Action: ActionTimeUpdateConfig,
			Target: "system_time",
			Detail: fmt.Sprintf("mode=%s, servers=%v, interval=%ds, tz=%s",
				req.Mode, req.NTPServers, req.SyncIntervalSeconds, req.Timezone),
			Status: audit.StatusSuccess,
		})
	}

	// Wake worker to apply new schedule or perform probe
	s.wakeWorker()

	return s.GetStatus(ctx)
}

// SyncNow triggers an immediate NTP probe and updates the system clock.
func (s *TimeService) SyncNow(ctx context.Context) (*SystemTimeStatus, error) {
	cfg, servers, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("get config: %w", err)
	}

	if cfg.Mode != ModeNTP {
		return nil, ErrManualMode
	}

	if !s.syncMu.TryLock() {
		return nil, ErrSyncInProgress
	}
	defer s.syncMu.Unlock()

	s.sm.SetSyncing()
	s.logger.Info("Starting manual NTP sync probe", zap.Strings("servers", servers))

	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	res, err := s.sntpClient.QueryPool(queryCtx, servers, 3*time.Second)
	if err != nil {
		s.sm.ProcessSyncError(err)
		s.logger.Warn("Manual NTP sync failed", zap.Error(err))

		if s.auditor != nil {
			s.auditor.Record(audit.Entry{
				Action:   ActionTimeNTPSync,
				Target:   "system_time",
				Detail:   fmt.Sprintf("servers=%v", servers),
				Status:   audit.StatusFailed,
				ErrorMsg: err.Error(),
			})
		}
		return s.GetStatus(ctx)
	}

	if err := s.sm.ProcessNTPResult(res); err != nil && !errors.Is(err, ErrPanicReview) {
		s.logger.Warn("Failed to apply NTP result in state machine", zap.Error(err))
	}

	if s.auditor != nil {
		s.auditor.Record(audit.Entry{
			Action: ActionTimeNTPSync,
			Target: "system_time",
			Detail: fmt.Sprintf("server=%s, offset=%v, rtt=%v, stratum=%d",
				res.Server, res.Offset, res.RTT, res.Stratum),
			Status: audit.StatusSuccess,
		})
	}

	return s.GetStatus(ctx)
}

// SetManualTime manually forces system clock and RTC synchronization.
func (s *TimeService) SetManualTime(ctx context.Context, targetTime time.Time) (*SystemTimeStatus, error) {
	if err := s.sm.ProcessManualTime(targetTime); err != nil {
		if s.auditor != nil {
			s.auditor.Record(audit.Entry{
				Action:   ActionTimeManualSet,
				Target:   "system_time",
				Detail:   fmt.Sprintf("target=%s", targetTime.Format(time.RFC3339Nano)),
				Status:   audit.StatusFailed,
				ErrorMsg: err.Error(),
			})
		}
		return nil, fmt.Errorf("process manual time: %w", err)
	}

	if s.auditor != nil {
		s.auditor.Record(audit.Entry{
			Action: ActionTimeManualSet,
			Target: "system_time",
			Detail: fmt.Sprintf("target=%s", targetTime.Format(time.RFC3339Nano)),
			Status: audit.StatusSuccess,
		})
	}

	return s.GetStatus(ctx)
}

// OnBootCheck performs cold-boot RTC time restoration and timezone initialization.
func (s *TimeService) OnBootCheck(ctx context.Context) error {
	s.logger.Info("Performing system time on-boot checks...")

	// 1. Permission check
	if !s.driver.HasClockPermission() {
		s.logger.Warn("Zhulong lacks CAP_SYS_TIME / root permissions; clock modifications will fail")
	}

	// 2. Cold-boot RTC healing check
	sysTime, err := s.driver.GetSystemTime()
	if err == nil && sysTime.Before(BuildEpoch) {
		s.logger.Warn("System clock is earlier than build epoch; checking hardware RTC...",
			zap.Time("sysTime", sysTime), zap.Time("buildEpoch", BuildEpoch))

		rtcTime, rtcErr := s.driver.ReadRTC()
		if rtcErr == nil && rtcTime.After(BuildEpoch) {
			s.logger.Info("Restoring system clock from hardware RTC", zap.Time("rtcTime", rtcTime))
			if setErr := s.driver.SetRealtime(rtcTime); setErr != nil {
				s.logger.Error("Failed to heal system time from RTC", zap.Error(setErr))
			} else {
				s.logger.Info("Successfully restored system clock from RTC")
				if s.auditor != nil {
					s.auditor.Record(audit.Entry{
						Action: ActionTimeRTCHeal,
						Target: "system_time",
						Detail: fmt.Sprintf("restored system time to %s from RTC", rtcTime.Format(time.RFC3339)),
						Status: audit.StatusSuccess,
					})
				}
			}
		} else {
			s.logger.Warn("Hardware RTC is unavailable or invalid; unable to heal on boot", zap.Error(rtcErr))
		}
	}

	// 3. Load persisted timezone configuration
	cfg, _, err := s.repo.GetConfig(ctx)
	if err == nil && cfg.Timezone != "" {
		if err := s.driver.ApplyTimezone(cfg.Timezone); err != nil {
			s.logger.Warn("Failed to apply saved timezone on boot", zap.String("tz", cfg.Timezone), zap.Error(err))
		} else {
			s.logger.Info("Applied system timezone on boot", zap.String("tz", cfg.Timezone))
		}
	}

	return nil
}

// Start spawns the background NTP periodic worker.
func (s *TimeService) Start(ctx context.Context) error {
	s.workerMu.Lock()
	defer s.workerMu.Unlock()

	if s.cancel != nil {
		return nil
	}

	workerCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.doneCh = make(chan struct{})

	go s.workerLoop(workerCtx)
	return nil
}

// Stop cleanly terminates the background NTP worker.
func (s *TimeService) Stop(ctx context.Context) error {
	s.workerMu.Lock()
	cancel := s.cancel
	done := s.doneCh
	s.cancel = nil
	s.workerMu.Unlock()

	if cancel != nil {
		cancel()
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (s *TimeService) wakeWorker() {
	select {
	case s.wakeCh <- struct{}{}:
	default:
	}
}

func (s *TimeService) workerLoop(ctx context.Context) {
	defer close(s.doneCh)

	// Short startup delay before first background probe (1s)
	initTimer := time.NewTimer(time.Second)
	select {
	case <-ctx.Done():
		initTimer.Stop()
		return
	case <-initTimer.C:
		s.probeOnce(ctx)
	case <-s.wakeCh:
		initTimer.Stop()
		s.probeOnce(ctx)
	}

	backoff := 30 * time.Second
	timer := time.NewTimer(s.getInterval(ctx))
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.wakeCh:
			s.probeOnce(ctx)
			backoff = 30 * time.Second
			resetTimer(timer, s.getInterval(ctx))
		case <-timer.C:
			interval := s.getInterval(ctx)
			if s.probeOnce(ctx) {
				backoff = 30 * time.Second
				resetTimer(timer, interval)
			} else {
				backoff = backoff * 2
				if backoff > interval {
					backoff = interval
				}
				resetTimer(timer, backoff)
			}
		}
	}
}

func (s *TimeService) getInterval(ctx context.Context) time.Duration {
	cfg, _, err := s.repo.GetConfig(ctx)
	if err == nil && cfg.SyncIntervalSeconds > 0 {
		return time.Duration(cfg.SyncIntervalSeconds) * time.Second
	}
	return 15 * time.Minute
}

func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}

func (s *TimeService) probeOnce(ctx context.Context) bool {
	cfg, servers, err := s.repo.GetConfig(ctx)
	if err != nil || cfg.Mode != ModeNTP {
		return true
	}

	if !s.syncMu.TryLock() {
		return false
	}
	defer s.syncMu.Unlock()

	s.sm.SetSyncing()
	queryCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	res, err := s.sntpClient.QueryPool(queryCtx, servers, 3*time.Second)
	if err != nil {
		s.sm.ProcessSyncError(err)
		s.logger.Warn("Periodic NTP sync failed", zap.Error(err))
		return false
	}

	if err := s.sm.ProcessNTPResult(res); err != nil && !errors.Is(err, ErrPanicReview) {
		s.logger.Warn("Failed to process periodic NTP result", zap.Error(err))
		return false
	}
	return true
}
