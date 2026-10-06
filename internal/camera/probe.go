package camera

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/engine"
)

var (
	ErrProbeTimeout           = errors.New("stream probe timed out")
	ErrUnsupportedVideoFormat = errors.New("unsupported video format or invalid dimensions")
)

// StreamProbeResult contains verified metadata extracted from a successful probe.
type StreamProbeResult struct {
	Codec          string
	Width          int
	Height         int
	FPSNumerator   int
	FPSDenominator int
}

// VideoProber abstracts the underlying engine probing capability.
type VideoProber interface {
	Probe(ctx context.Context, uri string, options engine.StreamOptions) (engine.VideoInfo, error)
}

// ProbeService manages atomic validation of main and optional sub streams.
type ProbeService struct {
	prober VideoProber
}

// NewProbeService creates a new ProbeService.
func NewProbeService(prober VideoProber) *ProbeService {
	return &ProbeService{prober: prober}
}

// ValidateStreams probes main and optional sub streams concurrently within a shared timeout budget.
// Both streams must succeed for validation to pass; any failure causes the entire validation to fail.
func (s *ProbeService) ValidateStreams(
	ctx context.Context,
	mainInfo *StreamConnectionInfo,
	subInfo *StreamConnectionInfo,
	timeout time.Duration,
) (mainRes *StreamProbeResult, subRes *StreamProbeResult, err error) {
	if mainInfo == nil {
		return nil, nil, apperr.New(apperr.KindInvalid, "CAMERA_INVALID_PARAM", "Main stream configuration is required", nil)
	}

	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var wg sync.WaitGroup
	var mainErr, subErr error

	wg.Add(1)
	go func() {
		defer wg.Done()
		mainRes, mainErr = s.probeSingleStream(probeCtx, mainInfo)
	}()

	if subInfo != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			subRes, subErr = s.probeSingleStream(probeCtx, subInfo)
		}()
	}

	wg.Wait()

	if mainErr != nil {
		return nil, nil, s.mapProbeError(mainErr, "main")
	}
	if subErr != nil {
		return nil, nil, s.mapProbeError(subErr, "sub")
	}

	return mainRes, subRes, nil
}

func (s *ProbeService) probeSingleStream(ctx context.Context, info *StreamConnectionInfo) (*StreamProbeResult, error) {
	if s.prober == nil {
		return nil, errors.New("video prober is not configured")
	}

	trans := engine.TransportTCP
	if strings.ToLower(info.Transport) == TransportUDP {
		trans = engine.TransportUDP
	}

	opts := engine.StreamOptions{
		Transport:   trans,
		OpenTimeout: 5 * time.Second,
		IdleTimeout: 5 * time.Second,
	}

	infoResult, err := s.prober.Probe(ctx, info.NormalizedURI, opts)
	if err != nil {
		return nil, err
	}

	// Validate dimensions
	if infoResult.Width <= 0 || infoResult.Height <= 0 {
		return nil, fmt.Errorf("%w: invalid dimensions %dx%d", ErrUnsupportedVideoFormat, infoResult.Width, infoResult.Height)
	}

	// Validate codec
	codecStr := ""
	switch infoResult.Codec {
	case engine.CodecH264:
		codecStr = "h264"
	case engine.CodecH265:
		codecStr = "h265"
	default:
		return nil, fmt.Errorf("%w: unsupported codec %v", ErrUnsupportedVideoFormat, infoResult.Codec)
	}

	// Handle FPS: if unknown or zero, record 0/1 without guessing
	num := int(infoResult.FPS.Num)
	den := int(infoResult.FPS.Den)
	if num <= 0 || den <= 0 {
		num = 0
		den = 1
	}

	return &StreamProbeResult{
		Codec:          codecStr,
		Width:          infoResult.Width,
		Height:         infoResult.Height,
		FPSNumerator:   num,
		FPSDenominator: den,
	}, nil
}

func (s *ProbeService) mapProbeError(err error, role string) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return apperr.New(
			apperr.KindInvalid,
			"CAMERA_CONNECT_TIMEOUT",
			fmt.Sprintf("Connection to %s stream timed out", role),
			err,
		)
	}

	errStr := strings.ToLower(err.Error())
	if strings.Contains(errStr, "auth") || strings.Contains(errStr, "401") || strings.Contains(errStr, "credentials") {
		return apperr.New(
			apperr.KindInvalid,
			"CAMERA_AUTH_FAILED",
			fmt.Sprintf("Authentication failed for %s stream", role),
			err,
		)
	}

	if errors.Is(err, ErrUnsupportedVideoFormat) || strings.Contains(errStr, "unsupported") {
		return apperr.New(
			apperr.KindInvalid,
			"CAMERA_UNSUPPORTED",
			fmt.Sprintf("Unsupported video format or dimensions for %s stream", role),
			err,
		)
	}

	return apperr.New(
		apperr.KindInvalid,
		"CAMERA_PROBE_FAILED",
		fmt.Sprintf("Failed to probe %s stream: connection or stream error", role),
		err,
	)
}
