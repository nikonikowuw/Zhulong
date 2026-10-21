package systemtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

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
	recorder   SystemEventRecorder

	workerMu sync.Mutex
	cancel   context.CancelFunc
	doneCh   chan struct{}
	wakeCh   chan struct{}
	syncMu   sync.Mutex

	// auditMu guards lastProbeFailed, the edge detector that keeps a prolonged
	// network outage from writing one failed audit record (plus one recovery
	// record) per backoff retry. Only the failure/recovery *transitions* are
	// auditable; the retries between them are telemetry.
	auditMu         sync.Mutex
	lastProbeFailed bool
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

// SystemEventRecorder records background system events. It is deliberately
// narrow: this service never records user-requested operations, which are
// audited by the HTTP handler instead.
type SystemEventRecorder interface {
	RecordSystem(action, target, detail string, opErr error)
}

// SetSystemEventRecorder binds the recorder used for background events.
func (s *TimeService) SetSystemEventRecorder(recorder SystemEventRecorder) {
	s.recorder = recorder
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

	// The user-requested configuration change is audited by the HTTP handler.
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
		// The caller reports the domain outcome through the audit middleware,
		// which keeps a single record per request and can mark a 200 response
		// as failed when the sync state says so.
		return s.GetStatus(ctx)
	}

	if _, err := s.sm.ProcessNTPResult(res); err != nil && !errors.Is(err, ErrPanicReview) {
		s.logger.Warn("Failed to apply NTP result in state machine", zap.Error(err))
	}

	return s.GetStatus(ctx)
}

// SetManualTime manually forces system clock and RTC synchronization.
func (s *TimeService) SetManualTime(ctx context.Context, targetTime time.Time) (*SystemTimeStatus, error) {
	if err := s.sm.ProcessManualTime(targetTime); err != nil {
		return nil, fmt.Errorf("process manual time: %w", err)
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
				if s.recorder != nil {
					s.recorder.RecordSystem(ActionTimeRTCHeal, "system_time",
						fmt.Sprintf("restored system time to %s from RTC", rtcTime.Format(time.RFC3339)), nil)
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
		s.recordProbeFailure(fmt.Sprintf("servers=%v", servers), err)
		return false
	}

	outcome, stepErr := s.sm.ProcessNTPResult(res)

	// A driver-level failure (no CAP_SYS_TIME, RTC missing, syscall error) is an
	// outage in the same sense a network failure is: debounce it through the same
	// edge detector so a retry loop cannot flood the table.
	if stepErr != nil && !errors.Is(stepErr, ErrPanicReview) {
		s.logger.Warn("Failed to apply NTP result in state machine", zap.Error(stepErr))
		s.recordProbeFailure(fmt.Sprintf("server=%s, offset=%v", res.Server, res.Offset), stepErr)
		return false
	}

	s.recordProbeSuccess()

	// Every probe is telemetry. Only the outcomes that move the clock or cross a
	// safety threshold are events. Keeping the steady-state slew out of the audit
	// trail is what stops a 15-minute heartbeat from pruning the operations an
	// operator actually needs to trace.
	s.logger.Debug("NTP probe ok",
		zap.String("server", res.Server),
		zap.Duration("offset", res.Offset),
		zap.Duration("rtt", res.RTT),
		zap.Uint8("stratum", res.Stratum),
		zap.String("action", outcome.Kind.String()))

	switch outcome.Kind {
	case ActionStepped:
		s.recordSystem(ActionTimeClockStep, "system_time",
			fmt.Sprintf("server=%s, step=%v, offset=%v, rtt=%v",
				res.Server, outcome.Step, res.Offset, res.RTT), nil)
	case ActionPanicReview:
		// A clock offset past the panic threshold is a high-risk condition. It must
		// be audited as failed even though the probe itself succeeded, otherwise
		// the record claims success for the exact situation that needs attention.
		s.logger.Warn("NTP offset exceeds panic threshold", zap.Error(stepErr))
		s.recordSystem(ActionTimeClockStep, "system_time",
			fmt.Sprintf("server=%s, offset=%v, review=%d/%d",
				res.Server, res.Offset, outcome.Review, PanicRequiredChecks), stepErr)
	}

	return true
}

// recordSystem forwards a background event to the recorder, tolerating a nil
// recorder: the recorder is an optional dependency bound via
// SetSystemEventRecorder, and tests exercise the service without one.
func (s *TimeService) recordSystem(action, target, detail string, opErr error) {
	if s.recorder == nil {
		return
	}
	s.recorder.RecordSystem(action, target, detail, opErr)
}

// recordProbeFailure audits a probe failure only when the service enters the
// failed state. A sustained outage retries with backoff; each retry is a
// telemetry event, not a new auditable occurrence.
func (s *TimeService) recordProbeFailure(detail string, probeErr error) {
	s.auditMu.Lock()
	firstFailure := !s.lastProbeFailed
	s.lastProbeFailed = true
	s.auditMu.Unlock()

	if !firstFailure {
		return
	}
	s.recordSystem(ActionTimeNTPSyncFailed, "system_time", detail, probeErr)
}

// recordProbeSuccess audits the recovery edge only: the transition back from a
// failed state is worth one record, a healthy steady state is not.
func (s *TimeService) recordProbeSuccess() {
	s.auditMu.Lock()
	wasFailing := s.lastProbeFailed
	s.lastProbeFailed = false
	s.auditMu.Unlock()

	if !wasFailing {
		return
	}
	s.recordSystem(ActionTimeNTPRecovered, "system_time", "NTP synchronization recovered", nil)
}
