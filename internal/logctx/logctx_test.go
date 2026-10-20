package logctx

import (
	"context"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestFieldsAndAccessors(t *testing.T) {
	if fields := Fields(context.Background()); fields != nil {
		t.Fatalf("Fields without correlation data = %#v, want nil", fields)
	}
	ctx := WithRequestID(context.Background(), "req-1")
	ctx = WithTraceID(ctx, "trace-2")
	ctx = WithStreamID(ctx, "stream-3")
	if RequestID(ctx) != "req-1" || TraceID(ctx) != "trace-2" || StreamID(ctx) != "stream-3" {
		t.Fatalf("correlation values were not retained")
	}
	if got := len(Fields(ctx)); got != 3 {
		t.Fatalf("Fields returned %d values, want 3", got)
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
		t.Fatalf("logger name = %q, want person.service", entry[0].LoggerName)
	}
	if got := entry[0].ContextMap()["requestId"]; got != "req-42" {
		t.Fatalf("requestId = %#v, want req-42", got)
	}
}

func BenchmarkLoggerNoFields(b *testing.B) {
	base := zap.NewNop().Named("benchmark.service")
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Logger(ctx, base)
	}
}

func BenchmarkLoggerWithRequestID(b *testing.B) {
	base := zap.NewNop().Named("benchmark.service")
	ctx := WithRequestID(context.Background(), "req-42")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Logger(ctx, base)
	}
}
