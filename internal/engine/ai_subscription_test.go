package engine

import (
	"context"
	"testing"
	"time"
)

func TestAIResultSubscription_DropOldest(t *testing.T) {
	sub := newAIResultSubscription(nil, 1, 1, 3)

	// 推入 5 条数据到容量为 3 的队列
	sub.onResult(1, []byte(`{"frame":1}`))
	sub.onResult(1, []byte(`{"frame":2}`))
	sub.onResult(1, []byte(`{"frame":3}`))
	sub.onResult(1, []byte(`{"frame":4}`))
	sub.onResult(1, []byte(`{"frame":5}`))

	// 应该丢弃前 2 条
	if dropped := sub.DroppedCount(); dropped != 2 {
		t.Fatalf("expected 2 dropped results, got %d", dropped)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// 消费剩下的 3 条：frame 3, 4, 5
	expected := []string{`{"frame":3}`, `{"frame":4}`, `{"frame":5}`}
	for _, exp := range expected {
		got, err := sub.Next(ctx)
		if err != nil {
			t.Fatalf("Next failed: %v", err)
		}
		if string(got) != exp {
			t.Errorf("expected %s, got %s", exp, string(got))
		}
	}

	// 此时队列应为空，关闭订阅后 Next 应返回 ErrClosed
	if err := sub.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	_, err := sub.Next(ctx)
	if err != ErrClosed {
		t.Errorf("expected ErrClosed after Close, got: %v", err)
	}
}

func TestAIResultSubscription_ContextCancel(t *testing.T) {
	sub := newAIResultSubscription(nil, 1, 1, 3)
	defer sub.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := sub.Next(ctx)
	if err != context.DeadlineExceeded {
		t.Errorf("expected DeadlineExceeded, got: %v", err)
	}
}

func TestAIResultSubscription_ControlLoopCancel(t *testing.T) {
	sub := newAIResultSubscription(nil, 1, 1, 3)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sub.runControlLoop(ctx)

	if sub.Err() != context.Canceled {
		t.Errorf("expected context.Canceled in sub.Err(), got %v", sub.Err())
	}
}
