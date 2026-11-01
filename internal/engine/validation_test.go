package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algoipc"
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
	invalid := []struct {
		name     string
		consumer Consumer
	}{
		{"zero ID", Consumer{ID: 0, Kind: ConsumerPreview}},
		{"unknown kind", Consumer{ID: 1, Kind: ConsumerKind(99)}},
	}
	for _, c := range invalid {
		t.Run(c.name, func(t *testing.T) {
			if err := validateConsumer(c.consumer); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("expected ErrInvalidArgument for %s, got %v", c.name, err)
			}
		})
	}

	valid := []Consumer{
		{ID: 100, Kind: ConsumerPreview},
		{ID: 101, Kind: ConsumerRecording},
		{ID: 102, Kind: ConsumerAI},
	}
	for _, consumer := range valid {
		if err := validateConsumer(consumer); err != nil {
			t.Fatalf("valid consumer %+v rejected: %v", consumer, err)
		}
	}
}

func TestNormalizeTimeoutMs(t *testing.T) {
	cases := []struct {
		name    string
		in      time.Duration
		want    uint32
		wantErr error
	}{
		{"zero uses default", 0, defaultTimeoutMs, nil},
		{"positive preserved", 3 * time.Second, 3000, nil},
		// 截断后归零必须捉升到 1ms：0 在 ABI 上表示「用底层默认超时」。
		{"sub-millisecond rounds up", 300 * time.Microsecond, 1, nil},
		{"negative rejected", -time.Second, 0, ErrInvalidArgument},
		// >maxAllowedTimeout（停机阻塞上界）必须拒绝，否则优雅停机会被拖死。
		{"over max rejected", 3 * time.Minute, 0, ErrInvalidArgument},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := normalizeTimeoutMs(c.in)
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("expected %v, got %v", c.wantErr, err)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("expected %dms, got %d (err: %v)", c.want, got, err)
			}
		})
	}
}

func TestClampToDeadline(t *testing.T) {
	// 无 deadline 的 ctx 保持原值。
	if got, err := clampToDeadline(context.Background(), 5000); err != nil || got != 5000 {
		t.Fatalf("expected 5000ms for ctx without deadline, got %d (err: %v)", got, err)
	}

	// deadline 比设置更短时收敛。
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if got, err := clampToDeadline(ctx, 5_000); err != nil || got > 250 {
		t.Fatalf("expected clamped timeout <= 250ms, got %d (err: %v)", got, err)
	}

	// deadline 比设置更宽时不得放大。
	wideCtx, cancelWide := context.WithTimeout(context.Background(), time.Minute)
	defer cancelWide()
	if got, err := clampToDeadline(wideCtx, 300); err != nil || got != 300 {
		t.Fatalf("expected 300ms preserved, got %d (err: %v)", got, err)
	}

	// 已过期的 deadline 必须立即报错。
	expired, cancelExpired := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancelExpired()
	time.Sleep(time.Millisecond)
	if _, err := clampToDeadline(expired, 5_000); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded for expired ctx, got %v", err)
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

func TestSentinelErrorsMatching(t *testing.T) {
	cases := []struct {
		code   StatusCode
		target error
	}{
		{StatusInvalidArgument, ErrInvalidArgument},
		{StatusOutOfMemory, ErrOutOfMemory},
		{StatusUnsupported, ErrUnsupported},
		{StatusNotFound, ErrNotFound},
		{StatusDuplicate, ErrDuplicate},
		{StatusConfigConflict, ErrConfigConflict},
		{StatusTimeout, ErrTimeout},
		{StatusNotRunning, ErrNotRunning},
		{StatusBusy, ErrBusy},
		{StatusIO, ErrIO},
		{StatusCancelled, ErrCancelled},
		{StatusBufferTooSmall, ErrBufferTooSmall},
		{StatusInternal, ErrInternal},
		{StatusClosed, ErrClosed},
		{StatusBackpressure, ErrBackpressure},
		{StatusPacketTooLarge, ErrPacketTooLarge},
		{StatusStaleResource, ErrStaleResource},
		{StatusSnapshotUnavailable, ErrSnapshotUnavailable},
	}

	for _, c := range cases {
		err := mapNativeStatus("test", int32(c.code))
		if !errors.Is(err, c.target) {
			t.Errorf("expected mapNativeStatus code %d to match sentinel %v, got %v", c.code, c.target, err)
		}
	}
}

// TestSentinelCodesAreUnique 钉死「一个哨兵一个状态码」的不变量。
//
// NativeError.Is 只比 Code，两个哨兵共享同一码会让 errors.Is 在不相干的
// 两类失败之间互相为真（历史问题：ErrSnapshotUnavailable 曾复用 StatusUnsupported）。
func TestSentinelCodesAreUnique(t *testing.T) {
	sentinels := []*NativeError{
		ErrClosed, ErrBackpressure, ErrPacketTooLarge, ErrStaleResource,
		ErrInvalidArgument, ErrTimeout, ErrCancelled, ErrNotRunning,
		ErrDuplicate, ErrConfigConflict, ErrBusy, ErrNotFound,
		ErrUnsupported, ErrIO, ErrOutOfMemory, ErrBufferTooSmall,
		ErrInternal, ErrSnapshotUnavailable,
	}

	seen := make(map[StatusCode]string, len(sentinels))
	for _, item := range sentinels {
		name := item.Message
		if previous, exists := seen[item.Code]; exists {
			t.Fatalf("status code %d is shared by %q and %q", item.Code, previous, name)
		}
		seen[item.Code] = name

		if want := statusMessage(item.Code); item.Message != want {
			t.Errorf("sentinel %q message %q drifted from statusMessages: %q", name, item.Message, want)
		}
	}
}

// TestSnapshotUnavailableDoesNotAliasUnsupported 验证两类失败不再互相匹配。
func TestSnapshotUnavailableDoesNotAliasUnsupported(t *testing.T) {
	wrapped := fmt.Errorf("snapshot: %w", ErrSnapshotUnavailable)
	if errors.Is(wrapped, ErrUnsupported) {
		t.Fatal("ErrSnapshotUnavailable must not match ErrUnsupported")
	}

	unsupported := mapNativeStatus("snapshot", int32(StatusUnsupported))
	if errors.Is(unsupported, ErrSnapshotUnavailable) {
		t.Fatal("generic unsupported error must not match ErrSnapshotUnavailable")
	}
}

func TestBroadcastAIMessageValidation(t *testing.T) {
	e := New()
	ctx := context.Background()

	// 1. 超限载荷 (> algoipc.MaxIpcPayloadSize) 必须返回 ErrInvalidArgument
	tooLargePayload := make([]byte, algoipc.MaxIpcPayloadSize+1)
	err := e.BroadcastAIMessage(ctx, 1, 10, tooLargePayload)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for payload exceeding limit, got %v", err)
	}

	// 2. 引擎未启动时返回 ErrNotRunning
	validPayload := make([]byte, algoipc.MaxIpcPayloadSize)
	err = e.BroadcastAIMessage(ctx, 1, 10, validPayload)
	if !errors.Is(err, ErrNotRunning) {
		t.Fatalf("expected ErrNotRunning for unstarted engine, got %v", err)
	}
}

// TestPlanTokenRelease 钉死 cgo.Handle 的释放时机决策。
//
// 关键不变量：只有「原生已确认排空」或「订阅本就不存在」才允许立即 Delete；
// 其余状态（含 CALLBACK_CONTEXT 之类未预期错误）一律推迟到停机汇合之后。
func TestPlanTokenRelease(t *testing.T) {
	cases := []struct {
		name   string
		status int32
		want   releaseTiming
	}{
		{"success", int32(StatusOK), releaseNow},
		{"subscription gone", int32(StatusNotFound), releaseNow},
		{"engine stopping", int32(StatusNotRunning), releaseAfterStop},
		{"callback context", int32(StatusCallbackContext), releaseAfterStop},
		{"busy", int32(StatusBusy), releaseAfterStop},
		{"unknown code", -777, releaseAfterStop},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := planTokenRelease(c.status); got != c.want {
				t.Fatalf("planTokenRelease(%d) = %v, want %v", c.status, got, c.want)
			}
		})
	}
}
