package engine

import (
	"context"
	"errors"
	"runtime/cgo"
	"strings"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/nativelog"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestNativeLogLevelMappings(t *testing.T) {
	levels := []struct {
		cLevel int32
		zap    zapcore.Level
		native nativelog.Level
	}{
		{0, zapcore.DebugLevel, nativelog.Debug},
		{1, zapcore.InfoLevel, nativelog.Info},
		{2, zapcore.WarnLevel, nativelog.Warn},
		{3, zapcore.ErrorLevel, nativelog.Error},
	}
	for _, test := range levels {
		if got := zapLevel(test.cLevel); got != test.zap {
			t.Errorf("zapLevel(%d) = %v, want %v", test.cLevel, got, test.zap)
		}
		if got := nativeLogLevel(test.cLevel); got != test.native {
			t.Errorf("nativeLogLevel(%d) = %v, want %v", test.cLevel, got, test.native)
		}
		if got := cLogLevel(test.zap); got != test.cLevel {
			t.Errorf("cLogLevel(%v) = %d, want %d", test.zap, got, test.cLevel)
		}
	}
	if got := zapLevel(99); got != zapcore.InfoLevel {
		t.Fatalf("invalid C level fallback = %v, want info", got)
	}
	if got := nativeLogLevel(99); got != nativelog.Info {
		t.Fatalf("invalid queue level fallback = %v, want info", got)
	}
	if got := cLogLevel(zapcore.FatalLevel); got != 1 {
		t.Fatalf("unsupported zap level fallback = %d, want C info (1)", got)
	}
}

func TestNativeLogTrampolineCopiesAndValidatesBounds(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	queue, err := nativelog.NewQueue(16, zap.New(core))
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.Start(); err != nil {
		t.Fatal(err)
	}
	handle := cgo.NewHandle(&nativeLogBridge{queue: queue})
	defer handle.Delete()

	invokeNativeLogCallbackForTest(uintptr(handle), 1, 42, []byte("unit.module"), false, []byte("copied text"), false, 11)
	invokeNativeLogCallbackForTest(uintptr(handle), 1, 1, nil, false, nil, false, 0) // empty module is invalid
	invokeNativeLogCallbackForTest(uintptr(handle), 1, 1, []byte(strings.Repeat("m", 129)), false, nil, false, 0)
	invokeNativeLogCallbackForTest(uintptr(handle), 1, 1, []byte("unit.module"), false, []byte(strings.Repeat("x", 2049)), false, 2049)
	invokeNativeLogCallbackForTest(uintptr(handle), 1, 1, nil, true, nil, false, 0)
	invokeNativeLogCallbackForTest(uintptr(handle), 1, 1, []byte("unit.module"), false, []byte("x"), true, 1)
	invokeNativeLogCallbackForTest(uintptr(handle), 1, 1, []byte("unit.module"), false, []byte{0xff}, false, 1)

	module128 := strings.Repeat("m", 128)
	text2048 := strings.Repeat("x", 2048)
	invokeNativeLogCallbackForTest(uintptr(handle), 3, 99, []byte(module128), false, []byte(text2048), false, 2048)
	invokeNativeLogCallbackForTest(uintptr(handle), 1, 100, []byte("unit.module"), false, nil, false, 0)
	invokeNativeLogCallbackForTest(uintptr(handle), 1, 101, []byte("unit.module"), false, nil, true, 0)
	invokeNativeLogCallbackForTest(uintptr(handle), 1, 102, []byte("x"), false, []byte("y"), false, 1)

	if got, want := queue.Dropped(), uint64(6); got != want {
		t.Fatalf("invalid native callback records dropped = %d, want %d", got, want)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if pending, err := queue.Stop(ctx); err != nil || pending != 0 {
		t.Fatalf("queue Stop = (%d, %v), want (0, nil)", pending, err)
	}

	entries := observed.FilterMessage("native event").All()
	if len(entries) != 5 {
		t.Fatalf("queued valid records = %d, want 5", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["module"] != "unit.module" || fields["message"] != "copied text" || fields["streamId"] != uint64(42) {
		t.Fatalf("copied native record fields = %#v", fields)
	}
	if fields["seenAt"] == nil {
		t.Fatal("native record missing seenAt timestamp")
	}
}

func TestNativeLogCABILifecycle(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	queue, err := nativelog.NewQueue(16, zap.New(core))
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.Start(); err != nil {
		t.Fatal(err)
	}

	eng := New()
	if err := eng.SetNativeLogSink(queue); err != nil {
		t.Fatalf("SetNativeLogSink: %v", err)
	}
	if err := eng.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := eng.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := eng.CloseNativeLogSink(); err != nil {
		t.Fatalf("CloseNativeLogSink: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := queue.Stop(ctx); err != nil {
		t.Fatalf("queue Stop: %v", err)
	}

	found := false
	for _, entry := range observed.All() {
		fields := entry.ContextMap()
		if fields["module"] == "pipeline.engine" && fields["message"] == "engine_started" {
			if _, hasStreamID := fields["streamId"]; hasStreamID {
				t.Fatalf("stream ID 0 should be omitted from lifecycle record: %#v", fields)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("native engine callback record not observed: %+v", observed.All())
	}
}

func TestNativeLogSinkRejectsLiveEngine(t *testing.T) {
	queue, err := nativelog.NewQueue(16, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	eng := New()
	if err := eng.Start(); err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	if err := eng.SetNativeLogSink(queue); !errors.Is(err, ErrBusy) {
		t.Fatalf("registration with live engine = %v, want ErrBusy", err)
	}
}

func BenchmarkNativeLogTrampoline(b *testing.B) {
	queue, err := nativelog.NewQueue(256, zap.NewNop())
	if err != nil {
		b.Fatal(err)
	}
	if err := queue.Start(); err != nil {
		b.Fatal(err)
	}
	handle := cgo.NewHandle(&nativeLogBridge{queue: queue})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		invokeNativeLogCallbackStaticForBenchmark(uintptr(handle))
	}
	b.StopTimer()
	handle.Delete()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := queue.Stop(ctx); err != nil {
		b.Fatal(err)
	}
}
