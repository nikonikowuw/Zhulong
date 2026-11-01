package systemtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// Worker cadence. Startup is delayed so the boot path can finish RTC healing and
// timezone application before the first probe competes for the clock.
const (
	workerStartupDelay = time.Second
	initialBackoff     = 30 * time.Second
	periodicQueryLimit = 8 * time.Second
)

// Start spawns the background NTP periodic worker.
func (s *TimeService) Start(ctx context.Context) error {
	s.workerMu.Lock()
	defer s.workerMu.Unlock()

	if s.cancel != nil {
		return nil
	}

	// The worker outlives the caller's context: Fx start hooks receive a context
	// that ends with the hook, so the worker gets its own cancel scope bounded by
	// Stop instead.
	workerCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.cancel = cancel
	s.doneCh = done

	go s.workerLoop(workerCtx, done)
	return nil
}

// Stop cleanly terminates the background NTP worker.
func (s *TimeService) Stop(ctx context.Context) error {
	s.workerMu.Lock()
	cancel := s.cancel
	done := s.doneCh
	s.cancel = nil
	s.doneCh = nil
	s.workerMu.Unlock()

	if cancel == nil || done == nil {
		return nil
	}

	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *TimeService) wakeWorker() {
	select {
	case s.wakeCh <- struct{}{}:
	default:
	}
}

// workerLoop probes once shortly after startup, then on schedule with exponential
// backoff after failures. done is captured by value so a later Start cannot make
// this goroutine close somebody else's channel.
func (s *TimeService) workerLoop(ctx context.Context, done chan struct{}) {
	defer close(done)

	startupTimer := time.NewTimer(workerStartupDelay)
	defer startupTimer.Stop()

	select {
	case <-ctx.Done():
		return
	case <-startupTimer.C:
		s.probeOnce(ctx)
	case <-s.wakeCh:
		s.probeOnce(ctx)
	}

	backoff := initialBackoff
	interval := s.getInterval(ctx)
	timer := time.NewTimer(interval)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.wakeCh:
			interval = s.getInterval(ctx)
			s.probeOnce(ctx)
			backoff = initialBackoff
			resetTimer(timer, interval)
		case <-timer.C:
			interval = s.getInterval(ctx)
			if s.probeOnce(ctx) {
				backoff = initialBackoff
				resetTimer(timer, interval)
				continue
			}
			backoff = min(backoff*2, interval)
			resetTimer(timer, backoff)
		}
	}
}

func (s *TimeService) getInterval(ctx context.Context) time.Duration {
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil || cfg.SyncIntervalSeconds <= 0 {
		return defaultSyncInterval
	}
	return time.Duration(cfg.SyncIntervalSeconds) * time.Second
}

// resetTimer re-arms t, draining a pending tick so a stale fire cannot shorten
// the new period.
func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}

// probeOnce runs one scheduled probe. It reports whether the probe was healthy;
// a skipped probe (manual mode, another sync in flight) counts as healthy so the
// worker does not back off for reasons it did not cause.
func (s *TimeService) probeOnce(ctx context.Context) bool {
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil || cfg.Mode != ModeNTP {
		return true
	}

	if !s.syncMu.TryLock() {
		s.logger.Debug("Skipping scheduled NTP probe: another sync is in progress")
		return true
	}
	defer s.syncMu.Unlock()

	s.sm.SetSyncing()
	queryCtx, cancel := context.WithTimeout(ctx, periodicQueryLimit)
	defer cancel()

	res, err := s.sntpClient.QueryPool(queryCtx, cfg.NTPServers, perServerTimeout)
	if err != nil {
		s.sm.ProcessSyncError(err)
		s.logger.Warn("Periodic NTP sync failed", zap.Error(err))
		s.recordProbeFailure(fmt.Sprintf("servers=%v", cfg.NTPServers), err)
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
	s.logRTCWriteFailure(outcome, "scheduled_probe")

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

// recordProbeFailure audits a probe failure only when the service enters the
// failed state. A sustained outage retries with backoff; each retry is a
// telemetry event, not a new auditable occurrence.
func (s *TimeService) recordProbeFailure(detail string, probeErr error) {
	if s.probeFailed.Swap(true) {
		return
	}
	s.recordSystem(ActionTimeNTPSyncFailed, "system_time", detail, probeErr)
}

// recordProbeSuccess audits the recovery edge only: the transition back from a
// failed state is worth one record, a healthy steady state is not.
func (s *TimeService) recordProbeSuccess() {
	if !s.probeFailed.Swap(false) {
		return
	}
	s.recordSystem(ActionTimeNTPRecovered, "system_time", "NTP synchronization recovered", nil)
}
