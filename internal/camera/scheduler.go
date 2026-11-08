package camera

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
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

// PacketActivitySource 报告某一路流的收包节拍是否由 Go 侧直接观测。
//
// 只有预览分发器在 Go 侧逐包可见。AI 分析流暴露给 Go 的是**推理结果**，
// 而结果会被运动门控吞掉（画面没动静就没有结果）——把「结果到达」当成收包证据，
// 等于给一个完全健康的流定时判死刑：现场实测 4 秒一次假超时，会话永远停在
// reconnecting，而同一时刻 broker 仍在按 sample_fps 稳定投递帧。
type PacketActivitySource interface {
	HasPacketActivitySource(cameraID, role string) bool
}

// HealthScheduler runs periodic lightweight health checks on idle streams and monitors packet health on active streams.
type HealthScheduler struct {
	store          CameraStore
	cipher         Cipher
	inFlight       map[string]bool
	describeClient *DescribeClient
	logger         *zap.Logger
	semaphore      chan struct{}
	hub            *EventHub
	activitySource PacketActivitySource
	stopCh         chan struct{}
	registry       *StateRegistry
	wg             sync.WaitGroup
	interval       time.Duration
	inFlightMu     sync.Mutex
	stopped        atomic.Bool
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

// SetPacketActivitySource wires the authority that knows which streams expose a
// Go-side packet arrival signal. Without it the active-stream monitor falls back
// to treating every running stream as packet-driven, which misfires on AI-only
// cameras (see PacketActivitySource).
func (s *HealthScheduler) SetPacketActivitySource(src PacketActivitySource) {
	s.activitySource = src
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

func (s *HealthScheduler) newProbeContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	go func() {
		select {
		case <-s.stopCh:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

func (s *HealthScheduler) runScheduledChecks() {
	ctx, cancel := s.newProbeContext(15 * time.Second)
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

			// 只有 Go 侧真能看见包节拍的流才做 4 秒超时判定。
			// AI 分析流的活性由 Native 流状态负责：read idle 超时会把流置为 FAILED，
			// 订阅随即报错并触发重连——那是可复现的证据，不是猜。
			if s.activitySource != nil && !s.activitySource.HasPacketActivitySource(camState.CameraID, role) {
				continue
			}

			// If last packet is older than 4s, trigger timeout
			if streamState.LastCheckedAt == nil || now.Sub(*streamState.LastCheckedAt) <= DefaultActivePacketTimeout {
				continue
			}

			s.logger.Warn("active stream packet timeout detected",
				zap.String("camera_id", camState.CameraID),
				zap.String("role", role),
				zap.Duration("elapsed", now.Sub(*streamState.LastCheckedAt)),
			)
			newState := s.registry.RecordStreamFailure(StreamFailureParams{
				CameraID:         camState.CameraID,
				Role:             role,
				Reason:           "packet_timeout",
				ErrorMessage:     "no video packets received for >4s",
				ExpectedRevision: camState.Revision,
				IsMediaSession:   true,
			})
			s.hub.BroadcastChange(newState)
		}
	}
}

func (s *HealthScheduler) checkStreamAsync(cam *Camera, stream *CameraStream) {
	key := cam.ID + ":" + stream.Role

	s.inFlightMu.Lock()
	if s.inFlight[key] {
		s.inFlightMu.Unlock()
		return
	}
	s.inFlight[key] = true
	s.inFlightMu.Unlock()

	s.wg.Go(func() {
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

		ctx, cancel := s.newProbeContext(DefaultProbeTimeout)
		defer cancel()

		s.checkStream(ctx, cam, stream)
	})
}

func (s *HealthScheduler) checkStream(ctx context.Context, cam *Camera, stream *CameraStream) {
	// Only execute Describe probe on idle streams (active streams are monitored via packet activity)
	if state, ok := s.registry.GetState(cam.ID); ok && state.Streams != nil {
		if st := state.Streams[stream.Role]; st != nil && st.Session == SessionStateRunning {
			return
		}
	}

	// Only RTSP sources have an endpoint to DESCRIBE. A GB28181 channel is reachable
	// through SIP/SDP only after an INVITE, so probing it here would report a
	// permanent uri_error and broadcast that false state every interval. Leaving the
	// state untouched keeps it "unknown", which is the truth until the native GB input
	// (S4) gives this protocol its own health evidence.
	if stream.Protocol != ProtocolRTSP {
		s.logger.Debug("skipping describe probe for non-rtsp stream",
			zap.String("camera_id", cam.ID),
			zap.String("role", stream.Role),
			zap.String("protocol", stream.Protocol),
		)
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
		newState := s.registry.RecordStreamFailure(StreamFailureParams{
			CameraID:         cam.ID,
			Role:             stream.Role,
			Reason:           "decryption_error",
			ErrorMessage:     "unable to decrypt credentials",
			ExpectedRevision: cam.Revision,
		})
		s.hub.BroadcastChange(newState)
		return
	}

	connInfo, err := ParseAndNormalizeRTSP(string(rawURI), stream.Transport)
	if err != nil {
		newState := s.registry.RecordStreamFailure(StreamFailureParams{
			CameraID:         cam.ID,
			Role:             stream.Role,
			Reason:           "uri_error",
			ErrorMessage:     "invalid RTSP URI",
			ExpectedRevision: cam.Revision,
		})
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

		newState := s.registry.RecordStreamFailure(StreamFailureParams{
			CameraID:         cam.ID,
			Role:             stream.Role,
			Reason:           reason,
			ErrorMessage:     err.Error(),
			ExpectedRevision: cam.Revision,
		})
		s.hub.BroadcastChange(newState)
		return
	}

	if res != nil && res.StatusCode == 200 {
		newState := s.registry.RecordStreamSuccess(
			cam.ID,
			cam.Revision,
			stream.Role,
			EvidenceRTSPDescribe,
		)
		s.hub.BroadcastChange(newState)
	}
}

func (s *HealthScheduler) jitterInterval() time.Duration {
	var b [8]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		return s.interval
	}
	randVal := binary.LittleEndian.Uint64(b[:])
	// +/- 20% jitter
	jitterPercent := 0.8 + 0.4*(float64(randVal%1000)/1000.0)
	return time.Duration(float64(s.interval) * jitterPercent)
}
