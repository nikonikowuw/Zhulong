package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSubscriptionQueueAndNext(t *testing.T) {
	sub := &Subscription{
		maxPackets:       10,
		maxPacketBytes:   1024,
		maxBufferedBytes: 4096,
		notify:           make(chan struct{}, 1),
		done:             make(chan struct{}),
	}

	payload := []byte("test_h264_payload")

	// 派发包
	sub.onPacketData(
		1, // H264
		1000,
		1000,
		true,
		true,
		Rational{Num: 1, Den: 90000},
		true,
		payload,
		len(payload),
	)

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
	sub := &Subscription{
		maxPackets:       2,
		maxPacketBytes:   1024,
		maxBufferedBytes: 4096,
		notify:           make(chan struct{}, 1),
		done:             make(chan struct{}),
	}

	raw := []byte("frame")

	sub.onPacketData(1, 0, 0, false, false, Rational{Num: 1, Den: 90000}, false, raw, len(raw))
	sub.onPacketData(1, 0, 0, false, false, Rational{Num: 1, Den: 90000}, false, raw, len(raw))

	if sub.Err() != nil {
		t.Fatalf("unexpected error before overflow: %v", sub.Err())
	}

	// 第 3 包应当触发背压
	sub.onPacketData(1, 0, 0, false, false, Rational{Num: 1, Den: 90000}, false, raw, len(raw))

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

func TestSubscriptionPacketTooLarge(t *testing.T) {
	sub := &Subscription{
		maxPackets:       10,
		maxPacketBytes:   100,
		maxBufferedBytes: 1000,
		notify:           make(chan struct{}, 1),
		done:             make(chan struct{}),
	}

	huge := make([]byte, 200)
	sub.onPacketData(1, 0, 0, false, false, Rational{Num: 1, Den: 90000}, false, huge, len(huge))

	if !errors.Is(sub.Err(), ErrPacketTooLarge) {
		t.Fatalf("expected ErrPacketTooLarge, got %v", sub.Err())
	}
}

func TestSubscriptionNextContextCancellation(t *testing.T) {
	sub := &Subscription{
		maxPackets:       10,
		maxPacketBytes:   1024,
		maxBufferedBytes: 4096,
		notify:           make(chan struct{}, 1),
		done:             make(chan struct{}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := sub.Next(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

func TestSubscriptionCloseIdempotency(t *testing.T) {
	sub := &Subscription{
		maxPackets:       10,
		maxPacketBytes:   1024,
		maxBufferedBytes: 4096,
		notify:           make(chan struct{}, 1),
		done:             make(chan struct{}),
	}

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

func TestSubscriptionInvalidPacket(t *testing.T) {
	sub := &Subscription{
		maxPackets:       10,
		maxPacketBytes:   1024,
		maxBufferedBytes: 4096,
		notify:           make(chan struct{}, 1),
		done:             make(chan struct{}),
	}

	// 模拟 data == nil 但 size > 0 的脏包视图
	sub.onPacket(nil) // nil packet 不做处理
	if sub.Err() != nil {
		t.Fatalf("unexpected error after nil packet: %v", sub.Err())
	}

	// 传入非法尺寸
	sub.onPacketData(1, 0, 0, false, false, Rational{Num: 1, Den: 90000}, false, nil, -1)
	if !errors.Is(sub.Err(), ErrPacketTooLarge) {
		t.Fatalf("expected ErrPacketTooLarge for negative size, got %v", sub.Err())
	}
}

func TestSubscriptionControlLoopContextCancelled(t *testing.T) {
	sub := &Subscription{
		maxPackets:       10,
		maxPacketBytes:   1024,
		maxBufferedBytes: 4096,
		notify:           make(chan struct{}, 1),
		done:             make(chan struct{}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sub.runControlLoop(ctx)

	if !errors.Is(sub.Err(), context.Canceled) {
		t.Fatalf("expected context.Canceled in sub.Err(), got %v", sub.Err())
	}
}
