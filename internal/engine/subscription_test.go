package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

// newTestSubscription 构造一个不绑定物理流的订阅，用于验证队列语义。
func newTestSubscription(t *testing.T, limits subscriptionLimits) *Subscription {
	t.Helper()

	validated, err := limits.validate()
	if err != nil {
		t.Fatalf("validate limits: %v", err)
	}
	return &Subscription{
		limits: validated,
		core:   newQueueCore[Packet](func(pkt Packet) int { return len(pkt.Data) }),
	}
}

func TestSubscriptionQueueAndNext(t *testing.T) {
	sub := newTestSubscription(t, subscriptionLimits{
		maxPackets:       10,
		maxPacketBytes:   1024,
		maxBufferedBytes: 4096,
	})

	payload := []byte("test_h264_payload")

	// 派发包
	sub.onPacketData(inboundPacket{
		Codec:    CodecH264,
		PTS:      1000,
		DTS:      1000,
		HasPTS:   true,
		HasDTS:   true,
		TimeBase: Rational{Num: 1, Den: 90000},
		KeyFrame: true,
		Data:     payload,
		Size:     len(payload),
	})

	// 修改原始 payload，验证深拷贝隔离
	payload[0] = 'X'

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	pkt, err := sub.Next(ctx)
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}

	if pkt.Codec != CodecH264 {
		t.Fatalf("expected H264 codec, got %v", pkt.Codec)
	}
	if pkt.PTS != 1000 || pkt.DTS != 1000 {
		t.Fatalf("unexpected PTS/DTS: %d/%d", pkt.PTS, pkt.DTS)
	}
	if !pkt.HasPTS || !pkt.HasDTS || !pkt.KeyFrame {
		t.Fatalf("unexpected flags: hasPTS=%v, hasDTS=%v, keyFrame=%v", pkt.HasPTS, pkt.HasDTS, pkt.KeyFrame)
	}
	if string(pkt.Data) != "test_h264_payload" {
		t.Fatalf("deep copy failed, expected 'test_h264_payload', got %q", string(pkt.Data))
	}
}

func TestSubscriptionBackpressureMaxPackets(t *testing.T) {
	sub := newTestSubscription(t, subscriptionLimits{
		maxPackets:       2,
		maxPacketBytes:   1024,
		maxBufferedBytes: 4096,
	})

	raw := []byte("frame")
	frame := inboundPacket{Codec: CodecH264, TimeBase: Rational{Num: 1, Den: 90000}, Data: raw, Size: len(raw)}

	sub.onPacketData(frame)
	sub.onPacketData(frame)

	if sub.Err() != nil {
		t.Fatalf("unexpected error before overflow: %v", sub.Err())
	}

	// 第 3 包应当触发背压
	sub.onPacketData(frame)

	if !errors.Is(sub.Err(), ErrBackpressure) {
		t.Fatalf("expected ErrBackpressure, got %v", sub.Err())
	}

	// Next 消费完积压后，最后应当返回背压终态
	ctx := context.Background()
	_, _ = sub.Next(ctx)
	_, _ = sub.Next(ctx)
	_, err := sub.Next(ctx)
	if !errors.Is(err, ErrBackpressure) {
		t.Fatalf("expected ErrBackpressure after draining queue, got %v", err)
	}
}

func TestSubscriptionBackpressureMaxBufferedBytes(t *testing.T) {
	sub := newTestSubscription(t, subscriptionLimits{
		maxPackets:       8,
		maxPacketBytes:   100,
		maxBufferedBytes: 250,
	})

	frame := inboundPacket{Codec: CodecH264, TimeBase: Rational{Num: 1, Den: 90000}, Data: make([]byte, 100), Size: 100}
	sub.onPacketData(frame)
	sub.onPacketData(frame)
	if sub.Err() != nil {
		t.Fatalf("byte budget should hold two packets: %v", sub.Err())
	}

	// 第 3 包让总字节数越过 250 的预算
	sub.onPacketData(frame)
	if !errors.Is(sub.Err(), ErrBackpressure) {
		t.Fatalf("expected ErrBackpressure from byte budget, got %v", sub.Err())
	}
}

func TestSubscriptionPacketTooLarge(t *testing.T) {
	sub := newTestSubscription(t, subscriptionLimits{
		maxPackets:       10,
		maxPacketBytes:   100,
		maxBufferedBytes: 1000,
	})

	huge := make([]byte, 200)
	sub.onPacketData(inboundPacket{
		Codec:    CodecH264,
		TimeBase: Rational{Num: 1, Den: 90000},
		Data:     huge,
		Size:     len(huge),
	})

	if !errors.Is(sub.Err(), ErrPacketTooLarge) {
		t.Fatalf("expected ErrPacketTooLarge, got %v", sub.Err())
	}
}

func TestSubscriptionNextContextCancellation(t *testing.T) {
	sub := newTestSubscription(t, subscriptionLimits{
		maxPackets:       10,
		maxPacketBytes:   1024,
		maxBufferedBytes: 4096,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := sub.Next(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

func TestSubscriptionCloseIdempotency(t *testing.T) {
	sub := newTestSubscription(t, subscriptionLimits{
		maxPackets:       10,
		maxPacketBytes:   1024,
		maxBufferedBytes: 4096,
	})

	if err := sub.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}
	if err := sub.Close(); err != nil {
		t.Fatalf("repeated Close failed: %v", err)
	}

	select {
	case <-sub.Done():
	default:
		t.Fatal("Done() must be closed after Close()")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := sub.Next(ctx)
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("expected ErrClosed from Next after Close, got %v", err)
	}
}

func TestSubscriptionCloseDropsQueuedPackets(t *testing.T) {
	sub := newTestSubscription(t, subscriptionLimits{
		maxPackets:       10,
		maxPacketBytes:   1024,
		maxBufferedBytes: 4096,
	})

	raw := []byte("frame")
	sub.onPacketData(inboundPacket{Codec: CodecH264, TimeBase: Rational{Num: 1, Den: 90000}, Data: raw, Size: len(raw)})

	if err := sub.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// 关闭后不得再交付陈旧帧
	_, err := sub.Next(context.Background())
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("expected ErrClosed instead of stale packet, got %v", err)
	}
}

func TestSubscriptionInvalidPacket(t *testing.T) {
	sub := newTestSubscription(t, subscriptionLimits{
		maxPackets:       10,
		maxPacketBytes:   1024,
		maxBufferedBytes: 4096,
	})

	// 模拟 data == nil 但 size > 0 的脏包视图
	sub.onPacket(nil) // nil packet 不做处理
	if sub.Err() != nil {
		t.Fatalf("unexpected error after nil packet: %v", sub.Err())
	}

	// 传入非法尺寸
	sub.onPacketData(inboundPacket{Codec: CodecH264, TimeBase: Rational{Num: 1, Den: 90000}, Size: -1})
	if !errors.Is(sub.Err(), ErrPacketTooLarge) {
		t.Fatalf("expected ErrPacketTooLarge for negative size, got %v", sub.Err())
	}
}

func TestSubscriptionControlLoopContextCancelled(t *testing.T) {
	sub := newTestSubscription(t, subscriptionLimits{
		maxPackets:       10,
		maxPacketBytes:   1024,
		maxBufferedBytes: 4096,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sub.runControlLoop(ctx)

	if !errors.Is(sub.Err(), context.Canceled) {
		t.Fatalf("expected context.Canceled in sub.Err(), got %v", sub.Err())
	}
}

func TestSubscriptionLimitsValidation(t *testing.T) {
	tests := []struct {
		name    string
		limits  subscriptionLimits
		wantErr bool
		want    subscriptionLimits
	}{
		{
			name:   "zero means defaults",
			limits: subscriptionLimits{},
			want: subscriptionLimits{
				maxPackets:       DefaultMaxPackets,
				maxPacketBytes:   DefaultMaxPacketBytes,
				maxBufferedBytes: DefaultMaxBufferedBytes,
			},
		},
		{
			name:    "max packets over hard limit",
			limits:  subscriptionLimits{maxPackets: HardLimitMaxPackets + 1},
			wantErr: true,
		},
		{
			name:    "max packet bytes over hard limit",
			limits:  subscriptionLimits{maxPacketBytes: HardLimitMaxPacketBytes + 1},
			wantErr: true,
		},
		{
			name:    "max buffered bytes over hard limit",
			limits:  subscriptionLimits{maxBufferedBytes: HardLimitMaxBufferedBytes + 1},
			wantErr: true,
		},
		{
			name:    "negative value rejected",
			limits:  subscriptionLimits{maxPackets: -1},
			wantErr: true,
		},
		{
			// 合法：等价于「单包超过总预算即触发背压」，用于按总字节强制限流。
			name:   "byte budget smaller than a single packet",
			limits: subscriptionLimits{maxPacketBytes: 8192, maxBufferedBytes: 4096},
			want: subscriptionLimits{
				maxPackets:       DefaultMaxPackets,
				maxPacketBytes:   8192,
				maxBufferedBytes: 4096,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.limits.validate()
			if test.wantErr {
				if !errors.Is(err, ErrInvalidArgument) {
					t.Fatalf("validate() error = %v, want ErrInvalidArgument", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("validate() = %+v, want %+v", got, test.want)
			}
		})
	}
}
