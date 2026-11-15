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

// cLogLevelForTest 反向查表，用来验证等级映射无遗漏、无碰撞。
// Go 不允许在 _test.go 里 use cgo，所以这里只能做纯 Go 的表格往返校验。
func cLogLevelForTest(level zapcore.Level) int32 {
	for index, severity := range nativeSeverities {
		if severity.zap == level {
			return int32(index)
		}
	}
	return 1 // INFO 兜底
}

func TestNativeLogLevelMappings(t *testing.T) {
	levels := []struct {
		cLevel int32
		zap    zapcore.Level
		native nativelog.Level
	}{
		{cLevel: 0, zap: zapcore.DebugLevel, native: nativelog.Debug},
		{cLevel: 1, zap: zapcore.InfoLevel, native: nativelog.Info},
		{cLevel: 2, zap: zapcore.WarnLevel, native: nativelog.Warn},
		{cLevel: 3, zap: zapcore.ErrorLevel, native: nativelog.Error},
	}
	for _, test := range levels {
		if got := zapLevel(test.cLevel); got != test.zap {
			t.Errorf("zapLevel(%d) = %v, want %v", test.cLevel, got, test.zap)
		}
		if got := nativeLogLevel(test.cLevel); got != test.native {
			t.Errorf("nativeLogLevel(%d) = %v, want %v", test.cLevel, got, test.native)
		}
		if got := cLogLevelForTest(test.zap); got != test.cLevel {
			t.Errorf("cLogLevelForTest(%v) = %d, want %d", test.zap, got, test.cLevel)
		}
	}
	if got := zapLevel(99); got != zapcore.InfoLevel {
		t.Fatalf("invalid C level fallback = %v, want info", got)
	}
	if got := nativeLogLevel(99); got != nativelog.Info {
		t.Fatalf("invalid queue level fallback = %v, want info", got)
	}
	if got := cLogLevelForTest(zapcore.FatalLevel); got != 1 {
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

	module128 := strings.Repeat("m", 128)
	text2048 := strings.Repeat("x", 2048)

	// 以下 6 条都必须在跨界边界被拦下并计为 drop。
	invalidCalls := []struct {
		reason string
		call   logCallbackCall
	}{
		{reason: "empty module", call: logCallbackCall{}},
		{reason: "module exceeds maxLogModuleBytes", call: logCallbackCall{Module: []byte(strings.Repeat("m", 129))}},
		{
			reason: "text exceeds maxLogTextBytes",
			call:   logCallbackCall{Module: []byte("unit.module"), Text: []byte(strings.Repeat("x", 2049)), TextLen: 2049},
		},
		{reason: "null module pointer", call: logCallbackCall{NullModule: true}},
		{
			reason: "null text pointer with non-zero length",
			call:   logCallbackCall{Module: []byte("unit.module"), Text: []byte("x"), NullText: true, TextLen: 1},
		},
		{reason: "non utf8 text", call: logCallbackCall{Module: []byte("unit.module"), Text: []byte{0xff}, TextLen: 1}},
	}
	for _, test := range invalidCalls {
		t.Logf("invalid call: %s", test.reason)
		call := test.call
		call.Token = uintptr(handle)
		if call.Level == 0 {
			call.Level = 1
		}
		if call.StreamID == 0 {
			call.StreamID = 1
		}
		invokeNativeLogCallbackForTest(call)
	}

	// 以下 5 条应当入队。
	validCalls := []logCallbackCall{
		{Level: 1, StreamID: 42, Module: []byte("unit.module"), Text: []byte("copied text"), TextLen: 11},
		{Level: 3, StreamID: 99, Module: []byte(module128), Text: []byte(text2048), TextLen: 2048},
		{Level: 1, StreamID: 100, Module: []byte("unit.module")},
		{Level: 1, StreamID: 101, Module: []byte("unit.module"), NullText: true},
		{Level: 1, StreamID: 102, Module: []byte("x"), Text: []byte("y"), TextLen: 1},
	}
	for _, call := range validCalls {
		call.Token = uintptr(handle)
		invokeNativeLogCallbackForTest(call)
	}

	if got, want := queue.Dropped(), uint64(6); got != want {
		t.Fatalf(
			"invalid native callback records dropped = %d, want %d",
			got,
			want,
		)
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
	mismatchedFields := fields["module"] != "unit.module" ||
		fields["message"] != "copied text" ||
		fields["streamId"] != uint64(42)
	if mismatchedFields {
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

	var found bool
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
	for range b.N {
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
