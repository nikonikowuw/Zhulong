package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestValidateRTSPURI(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		wantErr bool
	}{
		{"valid standard", "rtsp://127.0.0.1:8554/live", false},
		{"valid with credentials", "rtsp://admin:secret@192.168.1.100:554/stream1", false},
		{"empty URI", "", true},
		{"embedded NUL", "rtsp://127.0.0.1:8554/live\x00extra", true},
		{"whitespace space", "rtsp://127.0.0.1:8554/ live", true},
		{"whitespace tab", "rtsp://127.0.0.1:8554/\tlive", true},
		{"whitespace newline", "rtsp://127.0.0.1:8554/live\n", true},
		{"fragment", "rtsp://127.0.0.1:8554/live#frag", true},
		{"unsupported scheme http", "http://127.0.0.1:8554/live", true},
		{"unsupported scheme rtmp", "rtmp://127.0.0.1:8554/live", true},
		{"missing host", "rtsp:///live", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRTSPURI(tt.uri)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateRTSPURI(%q) error = %v, wantErr %v", tt.uri, err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("expected ErrInvalidArgument, got %v", err)
			}
		})
	}
}

func TestValidateConsumer(t *testing.T) {
	if err := validateConsumer(0, ConsumerPreview); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for zero consumerID, got %v", err)
	}
	if err := validateConsumer(1, ConsumerKind(99)); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for invalid kind, got %v", err)
	}
	if err := validateConsumer(100, ConsumerPreview); err != nil {
		t.Fatalf("valid consumer rejected: %v", err)
	}
	if err := validateConsumer(101, ConsumerRecording); err != nil {
		t.Fatalf("valid consumer rejected: %v", err)
	}
	if err := validateConsumer(102, ConsumerAI); err != nil {
		t.Fatalf("valid consumer rejected: %v", err)
	}
}

func TestResolveTimeoutMs(t *testing.T) {
	// 默认 5000ms
	ms, err := resolveTimeoutMs(0, nil)
	if err != nil || ms != 5000 {
		t.Fatalf("expected default 5000ms, got %d (err: %v)", ms, err)
	}

	// 指定正数
	ms, err = resolveTimeoutMs(3*time.Second, nil)
	if err != nil || ms != 3000 {
		t.Fatalf("expected 3000ms, got %d", ms)
	}

	// 负数
	_, err = resolveTimeoutMs(-1*time.Second, nil)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for negative duration, got %v", err)
	}

	// 超过 24h
	_, err = resolveTimeoutMs(25*time.Hour, nil)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for >24h, got %v", err)
	}

	// Context Deadline 比设置更短
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	ms, err = resolveTimeoutMs(5*time.Second, ctx)
	if err != nil || ms > 250 {
		t.Fatalf("expected clamped timeout <= 250ms, got %d", ms)
	}
}

func TestRational(t *testing.T) {
	r := Rational{Num: 30, Den: 1}
	if r.Float64() != 30.0 {
		t.Fatalf("expected 30.0, got %f", r.Float64())
	}
	if r.String() != "30/1" {
		t.Fatalf("expected '30/1', got %s", r.String())
	}

	zeroDen := Rational{Num: 10, Den: 0}
	if zeroDen.Float64() != 0 {
		t.Fatalf("expected 0 for zero denominator, got %f", zeroDen.Float64())
	}
}

func TestErrorSanitization(t *testing.T) {
	sensitiveURL := "rtsp://admin:P@ssword123!@192.168.1.100:554/live"
	err := mapNativeStatus("probe", -11)
	errStr := err.Error()

	if strings.Contains(errStr, sensitiveURL) || strings.Contains(errStr, "P@ssword") {
		t.Fatalf("error text leaks sensitive credentials: %s", errStr)
	}

	if !errors.Is(err, ErrIO) {
		t.Fatalf("expected ErrIO, got %v", err)
	}
}
