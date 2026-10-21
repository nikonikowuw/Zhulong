package logsafe

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Benchmark measurements recorded for the parent task (AC-C3). Environment:
//
//	go1.27.1 linux/amd64, go.uber.org/zap v1.28.0
//	11th Gen Intel(R) Core(TM) i5-11320H @ 3.20GHz (8 logical CPUs)
//
// Run with: go test ./internal/logsafe/ -bench . -benchmem
//
// There is deliberately no <1µs or 0-alloc/op assertion: these numbers describe
// the cost of an always-enabled safety boundary on one laptop-class CPU. They
// are inputs for a deployment decision, not a gate on correctness. Recorded
// figures live in .trellis/spec/backend/logging-guidelines.md.

// discardCore is a leaf core that accepts everything and writes nothing, so the
// benchmark isolates wrapper overhead from encoder and sink cost.
type discardCore struct{}

func (discardCore) Enabled(zapcore.Level) bool          { return true }
func (d discardCore) With([]zapcore.Field) zapcore.Core { return d }
func (d discardCore) Check(_ zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	return ce.AddCore(zapcore.Entry{}, d)
}
func (discardCore) Write(zapcore.Entry, []zapcore.Field) error { return nil }
func (discardCore) Sync() error                                { return nil }

func benchRun(b *testing.B, core zapcore.Core) {
	b.Helper()
	logger := zap.New(core)
	// One warm-up call keeps lazily initialized zap internals out of the loop.
	logger.Info("camera stream started")
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		logger.Info("camera stream started",
			zap.String("module", "camera.stream_hub"),
			zap.String("streamId", "01J8ZK3Q7Y8N9V0A2B3C4D5E6F"),
			zap.Int("attempt", 1),
			zap.Duration("latency_ms", 12*time.Millisecond),
			zap.Bool("established", true),
			zap.Error(errors.New("probe error")),
		)
	}
}

// BenchmarkDiscardPlain vs BenchmarkDiscardSanitizing isolates the wrapper's own
// cost: same leaf core, no encoder, no sink.
func BenchmarkDiscardPlain(b *testing.B)      { benchRun(b, discardCore{}) }
func BenchmarkDiscardSanitizing(b *testing.B) { benchRun(b, NewSanitizingCore(discardCore{})) }

// BenchmarkJSONPlain vs BenchmarkJSONSanitizing is the realistic comparison: a
// production JSON encoder writing to io.Discard.
func BenchmarkJSONPlain(b *testing.B) {
	benchRun(b, zapcore.NewCore(newBenchEncoder(), zapcore.AddSync(io.Discard), zapcore.DebugLevel))
}

func BenchmarkJSONSanitizing(b *testing.B) {
	benchRun(b, NewSanitizingCore(
		zapcore.NewCore(newBenchEncoder(), zapcore.AddSync(io.Discard), zapcore.DebugLevel),
	))
}

func newBenchEncoder() zapcore.Encoder {
	return zapcore.NewJSONEncoder(zapcore.EncoderConfig{
		TimeKey:        "ts",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      zapcore.OmitKey,
		MessageKey:     "msg",
		StacktraceKey:  zapcore.OmitKey,
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.EpochNanosTimeEncoder,
		EncodeDuration: zapcore.MillisDurationEncoder,
	})
}

// BenchmarkMessagePlain vs BenchmarkMessageSanitizing isolates the message path
// alone (Safe on the entry message, no fields).
func BenchmarkMessagePlain(b *testing.B) {
	logger := zap.New(discardCore{})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		logger.Info("camera stream started")
	}
}

func BenchmarkMessageSanitizing(b *testing.B) {
	logger := zap.New(NewSanitizingCore(discardCore{}))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		logger.Info("camera stream started")
	}
}

// BenchmarkOversizedFieldSanitizing is the bounded worst case: a field that must
// be reallocated and truncated by Safe.
func BenchmarkOversizedFieldSanitizing(b *testing.B) {
	logger := zap.New(NewSanitizingCore(discardCore{}))
	oversized := strings.Repeat("x", MaxSafeBytes*2)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		logger.Info("oversized", zap.String("payload", oversized))
	}
}

// BenchmarkWithSanitizing measures the With path, which must sanitize too.
func BenchmarkWithSanitizing(b *testing.B) {
	logger := zap.New(NewSanitizingCore(discardCore{}))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		logger.With(
			zap.String("module", "camera"),
			zap.String("clientIp", "192.168.1.77"),
		).Info("subscription opened")
	}
}

// BenchmarkSensitiveKeySanitizing measures a call that hits the replacement
// path.
func BenchmarkSensitiveKeySanitizing(b *testing.B) {
	logger := zap.New(NewSanitizingCore(discardCore{}))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		logger.Info("login", zap.String("password", "hunter2"))
	}
}

// BenchmarkOversizedFieldPlain is the baseline for
// BenchmarkOversizedFieldSanitizing: the same field without the wrapper, so the
// reported delta is the cost of Safe's rewrite and reallocation.
func BenchmarkOversizedFieldPlain(b *testing.B) {
	logger := zap.New(discardCore{})
	oversized := strings.Repeat("x", MaxSafeBytes*2)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		logger.Info("oversized", zap.String("payload", oversized))
	}
}
