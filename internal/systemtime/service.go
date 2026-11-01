package systemtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"go.uber.org/zap"
)

var (
	// ErrManualMode occurs when attempting NTP sync while in manual mode. The
	// HTTP status follows from the Kind (precondition failed), not from the
	// handler guessing.
	ErrManualMode = apperr.Precondition(
		"MANUAL_MODE_ACTIVE",
		"Cannot perform NTP sync while in manual mode",
		nil,
	)
	// ErrSyncInProgress occurs when an NTP sync is already active.
	ErrSyncInProgress = apperr.Conflict(
		"SYNC_IN_PROGRESS",
		"NTP synchronization is already in progress",
		nil,
	)
)

const (
	// defaultSyncInterval mirrors the persisted default: one source of truth for
	// the shipped polling cadence.
	defaultSyncInterval = time.Duration(defaultSyncIntervalSeconds) * time.Second
	perServerTimeout    = 3 * time.Second
	manualQueryTimeout  = 10 * time.Second
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

	// syncMu serialises clock writes between the manual endpoint and the
	// background worker; TryLock is the "already syncing" signal.
	syncMu sync.Mutex

	// probeFailed is the edge detector that keeps a prolonged network outage
	// from writing one failed audit record (plus one recovery record) per
	// backoff retry. Only the failure/recovery *transitions* are auditable; the
	// retries between them are telemetry.
	probeFailed atomic.Bool
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

	return &TimeService{
		repo:       repo,
		driver:     driver,
		sntpClient: sntpClient,
		sm:         NewTimeStateMachine(driver),
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
//
// It must be called before Start: the background worker reads the field without
// locking, matching the wiring order in NewTimeComponents.
func (s *TimeService) SetSystemEventRecorder(recorder SystemEventRecorder) {
	s.recorder = recorder
}

// GetStatus returns the complete system time and hardware status.
func (s *TimeService) GetStatus(ctx context.Context) (*SystemTimeStatus, error) {
	sysTime, err := s.driver.GetSystemTime()
	if err != nil {
		// Reporting no time at all is worse than reporting the process's own
		// view: the endpoint stays useful and the real cause goes to the log.
		s.logger.Warn("Failed to read kernel system time", zap.Error(err))
		sysTime = time.Now()
	}

	tz, err := s.driver.GetTimezone()
	if err != nil || tz == "" {
		s.logger.Debug("Failed to read system timezone; reporting UTC", zap.Error(err))
		tz = "UTC"
	}

	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, apperr.Internal("DATABASE_ERROR", "Database operation failed",
			fmt.Errorf("get time config: %w", err))
	}

	return &SystemTimeStatus{
		CurrentTime:         sysTime,
		Timezone:            tz,
		Mode:                cfg.Mode,
		NTPServers:          cfg.NTPServers,
		SyncIntervalSeconds: cfg.SyncIntervalSeconds,
		SyncStatus:          s.sm.GetSyncStatus(),
		RTCStatus:           s.driver.GetRTCStatus(),
		HasPermission:       s.driver.HasClockPermission(),
	}, nil
}

// UpdateConfig updates time configuration (mode, NTP servers, interval, timezone).
func (s *TimeService) UpdateConfig(ctx context.Context, req UpdateConfigRequest) (*SystemTimeStatus, error) {
	if _, err := time.LoadLocation(req.Timezone); err != nil {
		return nil, apperr.Invalid("INVALID_TIME_CONFIG", "Invalid time configuration",
			fmt.Errorf("timezone %q: %w", req.Timezone, err))
	}

	// Persist before touching the system clock: a failed write must not leave the
	// machine on a timezone that will silently revert on the next boot.
	if _, err := s.repo.UpdateConfig(ctx, TimeConfigUpdate(req)); err != nil {
		// A failed write is a server-side fault, not a bad request: reporting it as
		// an invalid configuration is what let SQLite text reach the client.
		return nil, apperr.Internal("DATABASE_ERROR", "Database operation failed",
			fmt.Errorf("update time config: %w", err))
	}

	// A timezone that the driver refuses is not worth failing the request: the
	// configuration is stored and the operator sees the warning in the log.
	if err := s.driver.ApplyTimezone(req.Timezone); err != nil {
		s.logger.Warn("Failed to apply timezone to system",
			zap.String("timezone", req.Timezone), zap.Error(err))
	}

	// Apply the new schedule (or probe immediately) without waiting an interval.
	s.wakeWorker()

	return s.GetStatus(ctx)
}

// SyncNow triggers an immediate NTP probe and updates the system clock.
func (s *TimeService) SyncNow(ctx context.Context) (*SystemTimeStatus, error) {
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, apperr.Internal("DATABASE_ERROR", "Database operation failed",
			fmt.Errorf("get config: %w", err))
	}

	if cfg.Mode != ModeNTP {
		return nil, ErrManualMode
	}

	if !s.syncMu.TryLock() {
		return nil, ErrSyncInProgress
	}
	defer s.syncMu.Unlock()

	s.sm.SetSyncing()
	s.logger.Info("Starting manual NTP sync probe", zap.Strings("servers", cfg.NTPServers))

	queryCtx, cancel := context.WithTimeout(ctx, manualQueryTimeout)
	defer cancel()

	res, err := s.sntpClient.QueryPool(queryCtx, cfg.NTPServers, perServerTimeout)
	if err != nil {
		s.sm.ProcessSyncError(err)
		s.logger.Warn("Manual NTP sync failed", zap.Error(err))
		// The caller reports the domain outcome through the audit middleware,
		// which keeps a single record per request and can mark a 200 response
		// as failed when the sync state says so.
		return s.GetStatus(ctx)
	}

	outcome, stepErr := s.sm.ProcessNTPResult(res)
	if stepErr != nil && !errors.Is(stepErr, ErrPanicReview) {
		s.logger.Warn("Failed to apply NTP result in state machine", zap.Error(stepErr))
	}
	s.logRTCWriteFailure(outcome, "manual_sync")

	return s.GetStatus(ctx)
}

// SetManualTime manually forces system clock and RTC synchronization.
func (s *TimeService) SetManualTime(ctx context.Context, targetTime time.Time) (*SystemTimeStatus, error) {
	outcome, err := s.sm.ProcessManualTime(targetTime)
	if err != nil {
		return nil, apperr.Internal("SET_TIME_FAILED", "Failed to set system time",
			fmt.Errorf("process manual time: %w", err))
	}
	s.logRTCWriteFailure(outcome, "manual_set")

	return s.GetStatus(ctx)
}

// OnBootCheck performs cold-boot RTC time restoration and timezone initialization.
func (s *TimeService) OnBootCheck(ctx context.Context) error {
	s.logger.Info("Performing system time on-boot checks...")

	if !s.driver.HasClockPermission() {
		s.logger.Warn("Zhulong lacks CAP_SYS_TIME / root permissions; clock modifications will fail")
	}

	s.healFromRTCOnBoot()
	s.applySavedTimezoneOnBoot(ctx)

	return nil
}

// healFromRTCOnBoot restores the system clock from the RTC chip when the kernel
// clock is still older than the build epoch, i.e. the device booted without
// network and the coin cell is the only surviving time source.
func (s *TimeService) healFromRTCOnBoot() {
	sysTime, err := s.driver.GetSystemTime()
	if err != nil || !sysTime.Before(buildEpoch) {
		return
	}

	s.logger.Warn("System clock is earlier than build epoch; checking hardware RTC...",
		zap.Time("sysTime", sysTime), zap.Time("buildEpoch", buildEpoch))

	rtcTime, err := s.driver.ReadRTC()
	if err != nil || !rtcTime.After(buildEpoch) {
		s.logger.Warn("Hardware RTC is unavailable or invalid; unable to heal on boot", zap.Error(err))
		return
	}

	s.logger.Info("Restoring system clock from hardware RTC", zap.Time("rtcTime", rtcTime))
	if err := s.driver.SetRealtime(rtcTime); err != nil {
		s.logger.Error("Failed to heal system time from RTC", zap.Error(err))
		return
	}

	s.logger.Info("Successfully restored system clock from RTC")
	s.recordSystem(ActionTimeRTCHeal, "system_time",
		fmt.Sprintf("restored system time to %s from RTC", rtcTime.Format(time.RFC3339)), nil)
}

func (s *TimeService) applySavedTimezoneOnBoot(ctx context.Context) {
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil || cfg.Timezone == "" {
		return
	}

	if err := s.driver.ApplyTimezone(cfg.Timezone); err != nil {
		s.logger.Warn("Failed to apply saved timezone on boot",
			zap.String("timezone", cfg.Timezone), zap.Error(err))
		return
	}
	s.logger.Info("Applied system timezone on boot", zap.String("timezone", cfg.Timezone))
}

// logRTCWriteFailure surfaces a hardware RTC write-back failure. The wall clock
// already moved, so the probe is not failed, but a stale RTC means the next cold
// boot starts from the wrong time.
func (s *TimeService) logRTCWriteFailure(outcome SyncOutcome, operation string) {
	if outcome.RTCErr == nil {
		return
	}
	s.logger.Warn("Failed to persist time to hardware RTC",
		zap.String("operation", operation), zap.Error(outcome.RTCErr))
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
