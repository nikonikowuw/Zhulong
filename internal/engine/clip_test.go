package engine

import (
	"context"
	"errors"
	"testing"
)

func TestEngineClipABIAndValidation(t *testing.T) {
	eng := New()
	if err := eng.Start(); err != nil {
		t.Fatalf("Engine.Start: %v", err)
	}
	defer func() {
		if err := eng.Close(); err != nil {
			t.Fatalf("Engine.Close: %v", err)
		}
	}()

	ctx := context.Background()

	// Empty poll on fresh engine returns empty slice without error.
	events, err := eng.PollClipEvents(ctx, 16)
	if err != nil {
		t.Fatalf("PollClipEvents(16): %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}

	// Zero/negative max short-circuits cleanly.
	events, err = eng.PollClipEvents(ctx, 0)
	if err != nil || len(events) != 0 {
		t.Fatalf("PollClipEvents(0): events=%v err=%v", events, err)
	}

	// Stop still allows polling drained events before Close.
	if err := eng.Stop(); err != nil {
		t.Fatalf("Engine.Stop: %v", err)
	}
	if _, err := eng.PollClipEvents(ctx, 8); err != nil {
		t.Fatalf("PollClipEvents after Stop: %v", err)
	}
}

func TestStreamClipValidationErrors(t *testing.T) {
	ctx := context.Background()

	eng := New()
	stream := &Stream{engine: eng, id: 1, generation: 1}
	if err := stream.ConfigureRecorder(ctx, RecorderConfig{PreMs: 5000}); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("unstarted engine ConfigureRecorder: got %v, want ErrNotRunning", err)
	}
	if err := stream.ConfigureRecorder(ctx, RecorderConfig{PreMs: -1}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("negative pre_ms: got %v, want ErrInvalidArgument", err)
	}
	if _, err := stream.TriggerClip(ctx, ClipTriggerRequest{}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty path: got %v, want ErrInvalidArgument", err)
	}
	if _, err := stream.TriggerClip(ctx, ClipTriggerRequest{Path: "relative/clip.mp4"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("relative path: got %v, want ErrInvalidArgument", err)
	}
	if _, err := eng.PollClipEvents(nil, 4); !errors.Is(err, ErrInvalidArgument) { //nolint:staticcheck // testing nil context guard
		t.Fatalf("nil context PollClipEvents: got %v, want ErrInvalidArgument", err)
	}
}
