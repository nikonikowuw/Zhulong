package camera

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/engine"
)

type mockProber struct {
	probeFunc func(ctx context.Context, uri string, options engine.StreamOptions) (engine.VideoInfo, error)
}

func (m *mockProber) Probe(ctx context.Context, uri string, options engine.StreamOptions) (engine.VideoInfo, error) {
	if m.probeFunc != nil {
		return m.probeFunc(ctx, uri, options)
	}
	return engine.VideoInfo{}, errors.New("not implemented")
}

func TestProbeServiceValidateStreamsSuccessWithUnknownFPS(t *testing.T) {
	prober := &mockProber{
		probeFunc: func(ctx context.Context, uri string, options engine.StreamOptions) (engine.VideoInfo, error) {
			return engine.VideoInfo{
				Codec:  engine.CodecH264,
				Width:  1920,
				Height: 1080,
				FPS:    engine.Rational{Num: 0, Den: 0}, // unknown FPS
			}, nil
		},
	}

	service := NewProbeService(prober)
	mainInfo := &StreamConnectionInfo{NormalizedURI: "rtsp://192.168.1.10/main"}

	mainRes, subRes, err := service.ValidateStreams(context.Background(), mainInfo, nil, 2*time.Second)
	if err != nil {
		t.Fatalf("ValidateStreams failed: %v", err)
	}

	if subRes != nil {
		t.Fatalf("expected nil subRes, got: %+v", subRes)
	}

	if mainRes.Codec != "h264" || mainRes.Width != 1920 || mainRes.Height != 1080 {
		t.Fatalf("unexpected main stream probe result: %+v", mainRes)
	}
	if mainRes.FPSNumerator != 0 || mainRes.FPSDenominator != 1 {
		t.Fatalf("expected unknown FPS 0/1, got %d/%d", mainRes.FPSNumerator, mainRes.FPSDenominator)
	}
}

func TestProbeServiceAtomicFailureIfSubStreamFails(t *testing.T) {
	prober := &mockProber{
		probeFunc: func(ctx context.Context, uri string, options engine.StreamOptions) (engine.VideoInfo, error) {
			if uri == "rtsp://192.168.1.10/main" {
				return engine.VideoInfo{
					Codec:  engine.CodecH264,
					Width:  1920,
					Height: 1080,
					FPS:    engine.Rational{Num: 25, Den: 1},
				}, nil
			}
			return engine.VideoInfo{}, errors.New("401 unauthorized")
		},
	}

	service := NewProbeService(prober)
	mainInfo := &StreamConnectionInfo{NormalizedURI: "rtsp://192.168.1.10/main"}
	subInfo := &StreamConnectionInfo{NormalizedURI: "rtsp://192.168.1.10/sub"}

	mainRes, subRes, err := service.ValidateStreams(context.Background(), mainInfo, subInfo, 2*time.Second)
	if err == nil {
		t.Fatalf("expected failure when sub stream fails, but succeeded: main=%+v sub=%+v", mainRes, subRes)
	}

	var appErr *apperr.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("expected apperr.Error, got: %T", err)
	}
	if appErr.Code != "CAMERA_AUTH_FAILED" {
		t.Fatalf("expected code CAMERA_AUTH_FAILED, got: %s", appErr.Code)
	}
}

func TestProbeServiceRejectsInvalidDimensions(t *testing.T) {
	prober := &mockProber{
		probeFunc: func(ctx context.Context, uri string, options engine.StreamOptions) (engine.VideoInfo, error) {
			return engine.VideoInfo{
				Codec:  engine.CodecH264,
				Width:  0,
				Height: 0,
			}, nil
		},
	}

	service := NewProbeService(prober)
	mainInfo := &StreamConnectionInfo{NormalizedURI: "rtsp://192.168.1.10/main"}

	_, _, err := service.ValidateStreams(context.Background(), mainInfo, nil, 2*time.Second)
	if err == nil {
		t.Fatal("expected failure on zero dimensions")
	}

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "CAMERA_UNSUPPORTED" {
		t.Fatalf("expected CAMERA_UNSUPPORTED, got: %v", err)
	}
}
