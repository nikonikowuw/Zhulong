package logctx

import (
	"context"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

var benchSink *zap.Logger

func TestFieldsAndAccessors(t *testing.T) {
	if fields := Fields(nil); fields != nil {
		t.Fatalf("Fields(nil) = %#v, want nil", fields)
	}
	if fields := Fields(context.Background()); fields != nil {
		t.Fatalf("Fields without correlation data = %#v, want nil", fields)
	}

	ctx := WithRequestID(context.Background(), "req-1")
	ctx = WithTraceID(ctx, "trace-2")
	ctx = WithStreamID(ctx, "stream-3")

	if got := RequestID(ctx); got != "req-1" {
		t.Fatalf("RequestID(ctx) = %q, want %q", got, "req-1")
	}
	if got := TraceID(ctx); got != "trace-2" {
		t.Fatalf("TraceID(ctx) = %q, want %q", got, "trace-2")
	}
	if got := StreamID(ctx); got != "stream-3" {
		t.Fatalf("StreamID(ctx) = %q, want %q", got, "stream-3")
	}

	fields := Fields(ctx)
	if got := len(fields); got != 3 {
		t.Fatalf("Fields returned %d values, want 3", got)
	}
}

func TestAccessorsEdgeCases(t *testing.T) {
	// Nil context cases
	if got := RequestID(nil); got != "" {
		t.Fatalf("RequestID(nil) = %q, want empty", got)
	}
	if got := TraceID(nil); got != "" {
		t.Fatalf("TraceID(nil) = %q, want empty", got)
	}
	if got := StreamID(nil); got != "" {
		t.Fatalf("StreamID(nil) = %q, want empty", got)
	}

	if got := WithRequestID(nil, "req-1"); got != nil {
		t.Fatalf("WithRequestID(nil, ...) = %#v, want nil", got)
	}
	if got := WithTraceID(nil, "trace-1"); got != nil {
		t.Fatalf("WithTraceID(nil, ...) = %#v, want nil", got)
	}
	if got := WithStreamID(nil, "stream-1"); got != nil {
		t.Fatalf("WithStreamID(nil, ...) = %#v, want nil", got)
	}

	// Empty ID cases: should return original context unmodified
	baseCtx := context.Background()
	if got := WithRequestID(baseCtx, ""); got != baseCtx {
		t.Fatalf("WithRequestID(baseCtx, \"\") did not return baseCtx")
	}
	if got := WithTraceID(baseCtx, ""); got != baseCtx {
		t.Fatalf("WithTraceID(baseCtx, \"\") did not return baseCtx")
	}
	if got := WithStreamID(baseCtx, ""); got != baseCtx {
		t.Fatalf("WithStreamID(baseCtx, \"\") did not return baseCtx")
	}
}

func TestLoggerPreservesNamedPrefixAndContextFields(t *testing.T) {
	core, entries := observer.New(zap.InfoLevel)
	base := zap.New(core).Named("person.service")
	ctx := WithRequestID(context.Background(), "req-42")
	Logger(ctx, base).Info("operation")

	entry := entries.TakeAll()
	if len(entry) != 1 {
		t.Fatalf("got %d log entries, want 1", len(entry))
	}
	if entry[0].LoggerName != "person.service" {
		t.Fatalf("logger name = %q, want %q", entry[0].LoggerName, "person.service")
	}
	if got := entry[0].ContextMap()["requestId"]; got != "req-42" {
		t.Fatalf("requestId = %q, want %q", got, "req-42")
	}
}

func TestLoggerFallbackAndEmptyContext(t *testing.T) {
	// Nil base should fallback to zap.L()
	logger := Logger(context.Background(), nil)
	if logger == nil {
		t.Fatal("Logger with nil base returned nil, want non-nil zap.L()")
	}

	// Empty context should return base directly
	base := zap.NewNop()
	if got := Logger(context.Background(), base); got != base {
		t.Fatalf("Logger with empty context = %p, want base %p", got, base)
	}
}

func BenchmarkLoggerNoFields(b *testing.B) {
	base := zap.NewNop().Named("benchmark.service")
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchSink = Logger(ctx, base)
	}
}

func BenchmarkLoggerWithRequestID(b *testing.B) {
	base := zap.NewNop().Named("benchmark.service")
	ctx := WithRequestID(context.Background(), "req-42")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchSink = Logger(ctx, base)
	}
}
