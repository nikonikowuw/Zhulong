package camera

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

const (
	DefaultSchedulerInterval = 60 * time.Second
	DefaultProbeConcurrency  = 4
	ActiveMonitorInterval    = 250 * time.Millisecond
)

// HealthScheduler runs periodic lightweight health checks on idle streams and monitors packet health on active streams.
type HealthScheduler struct {
	store          CameraStore
	cipher         Cipher
	registry       *StateRegistry
	hub            *EventHub
	describeClient *DescribeClient
	logger         *zap.Logger

	interval   time.Duration
	semaphore  chan struct{}
	inFlightMu sync.Mutex
	inFlight   map[string]bool
	stopCh     chan struct{}
	stopped    atomic.Bool
	wg         sync.WaitGroup
}

// NewHealthScheduler constructs a HealthScheduler.
func NewHealthScheduler(
	store CameraStore,
	cipher Cipher,
	registry *StateRegistry,
	hub *EventHub,
	describeClient *DescribeClient,
	logger *zap.Logger,
) *HealthScheduler {
	if logger == nil {
		logger = zap.NewNop()
	}
	if describeClient == nil {
		describeClient = NewDescribeClient()
	}

	return &HealthScheduler{
		store:          store,
		cipher:         cipher,
		registry:       registry,
		hub:            hub,
		describeClient: describeClient,
		logger:         logger.Named("camera.scheduler"),
		interval:       DefaultSchedulerInterval,
		semaphore:      make(chan struct{}, DefaultProbeConcurrency),
		inFlight:       make(map[string]bool),
		stopCh:         make(chan struct{}),
	}
}

// Start loads persisted cameras, initializes their state, and begins the scheduling and monitoring loops.
func (s *HealthScheduler) Start(ctx context.Context) error {
	cameras, _, err := s.store.List(ctx, 100, 0)
	if err != nil {
		return fmt.Errorf("load cameras for scheduler: %w", err)
	}

	for i := range cameras {
		s.registry.InitCameraState(&cameras[i])
	}

	s.wg.Add(2)
	go s.idleCheckLoop()
	go s.activeMonitorLoop()

	s.logger.Info("camera health scheduler started", zap.Int("camera_count", len(cameras)))
	return nil
}

// Stop cleanly terminates scheduler loops and waits for in-flight probes to drain.
func (s *HealthScheduler) Stop() {
	if !s.stopped.CompareAndSwap(false, true) {
		return
	}
	close(s.stopCh)
	s.wg.Wait()
	s.logger.Info("camera health scheduler stopped")
}

// TriggerCheck immediately executes a health check on the specified camera and returns the resulting state.
func (s *HealthScheduler) TriggerCheck(ctx context.Context, cameraID string) (*CameraStateInfo, error) {
	cam, err := s.store.GetByID(ctx, cameraID)
	if err != nil {
		return nil, err
	}
	if !cam.Enabled {
		state, _ := s.registry.GetState(cameraID)
		return state, nil
	}

	for _, stream := range cam.Streams {
		s.checkStream(ctx, cam, &stream)
	}

	state, _ := s.registry.GetState(cameraID)
	return state, nil
}

func (s *HealthScheduler) idleCheckLoop() {
	defer s.wg.Done()

	ticker := time.NewTicker(s.jitterInterval())
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.runScheduledChecks()
			ticker.Reset(s.jitterInterval())
		}
	}
}

func (s *HealthScheduler) activeMonitorLoop() {
	defer s.wg.Done()

	ticker := time.NewTicker(ActiveMonitorInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.monitorActiveStreams()
		}
	}
}

func (s *HealthScheduler) runScheduledChecks() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cameras, _, err := s.store.List(ctx, 100, 0)
	if err != nil {
		s.logger.Warn("failed to query cameras during scheduled check", zap.Error(err))
		return
	}

	for i := range cameras {
		cam := &cameras[i]
		if !cam.Enabled {
			continue
		}

		for j := range cam.Streams {
			stream := &cam.Streams[j]
			s.checkStreamAsync(cam, stream)
		}
	}
}

func (s *HealthScheduler) monitorActiveStreams() {
	now := time.Now().UTC()
	states := s.registry.GetAllStates()

	for _, camState := range states {
		if !camState.Enabled {
			continue
		}

		for role, streamState := range camState.Streams {
			// Only monitor active streams with session running
			if streamState.Session != SessionStateRunning {
				continue
			}

			// If last packet is older than 4s, trigger timeout
			if streamState.LastCheckedAt != nil && now.Sub(*streamState.LastCheckedAt) > DefaultActivePacketTimeout {
				s.logger.Warn("active stream packet timeout detected",
					zap.String("camera_id", camState.CameraID),
					zap.String("role", role),
					zap.Duration("elapsed", now.Sub(*streamState.LastCheckedAt)),
				)
				newState := s.registry.RecordStreamFailure(
					camState.CameraID,
					camState.Revision,
					role,
					"packet_timeout",
					"no video packets received for >4s",
					true, // isMediaSession = true
				)
				s.hub.BroadcastChange(newState)
			}
		}
	}
}

func (s *HealthScheduler) checkStreamAsync(cam *Camera, stream *CameraStream) {
	key := fmt.Sprintf("%s:%s", cam.ID, stream.Role)

	s.inFlightMu.Lock()
	if s.inFlight[key] {
		s.inFlightMu.Unlock()
		return
	}
	s.inFlight[key] = true
	s.inFlightMu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.inFlightMu.Lock()
			delete(s.inFlight, key)
			s.inFlightMu.Unlock()
		}()

		select {
		case s.semaphore <- struct{}{}:
			defer func() { <-s.semaphore }()
		case <-s.stopCh:
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), DefaultProbeTimeout)
		defer cancel()

		s.checkStream(ctx, cam, stream)
	}()
}

func (s *HealthScheduler) checkStream(ctx context.Context, cam *Camera, stream *CameraStream) {
	// Only execute Describe probe on idle streams (active streams are monitored via packet activity)
	state, ok := s.registry.GetState(cam.ID)
	if ok && state.Streams[stream.Role] != nil && state.Streams[stream.Role].Session == SessionStateRunning {
		return
	}

	// Decrypt URI
	aad := MakeAAD(cam.ID, stream.Role)
	rawURI, err := s.cipher.Decrypt(stream.EncryptedURI, aad)
	if err != nil {
		s.logger.Error("decrypt stream URI failed for health check",
			zap.String("camera_id", cam.ID),
			zap.String("role", stream.Role),
			zap.Error(err),
		)
		newState := s.registry.RecordStreamFailure(cam.ID, cam.Revision, stream.Role, "decryption_error", "unable to decrypt credentials", false)
		s.hub.BroadcastChange(newState)
		return
	}

	connInfo, err := ParseAndNormalizeRTSP(string(rawURI), stream.Transport)
	if err != nil {
		newState := s.registry.RecordStreamFailure(cam.ID, cam.Revision, stream.Role, "uri_error", "invalid RTSP URI", false)
		s.hub.BroadcastChange(newState)
		return
	}

	// Run Describe probe
	res, err := s.describeClient.Describe(ctx, connInfo, DefaultProbeTimeout)
	if err != nil {
		reason := "probe_error"
		switch {
		case errors.Is(err, ErrAuthRejected), errors.Is(err, ErrCredentialsRequired):
			reason = "auth_failed"
		case errors.Is(err, ErrRTSPTimeout):
			reason = "connect_timeout"
		case errors.Is(err, ErrStreamNotFound):
			reason = "stream_not_found"
		}

		newState := s.registry.RecordStreamFailure(cam.ID, cam.Revision, stream.Role, reason, err.Error(), false)
		s.hub.BroadcastChange(newState)
		return
	}

	if res != nil && res.StatusCode == 200 {
		newState := s.registry.RecordStreamSuccess(cam.ID, cam.Revision, stream.Role, EvidenceRTSPDescribe)
		s.hub.BroadcastChange(newState)
	}
}

func (s *HealthScheduler) jitterInterval() time.Duration {
	var b [8]byte
	_, _ = rand.Read(b[:])
	randVal := binary.LittleEndian.Uint64(b[:])
	// +/- 20% jitter
	jitterPercent := 0.8 + 0.4*(float64(randVal%1000)/1000.0)
	return time.Duration(float64(s.interval) * jitterPercent)
}
