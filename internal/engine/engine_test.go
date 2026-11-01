package engine

import (
	"context"
	"sync"
	"testing"
)

// generationForTest 在 e.mu 保护下读取代次，避免测试自身制造数据竞争。
func generationForTest(e *Engine) uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.generation
}

func TestEngineLifecycle(t *testing.T) {
	eng := New()
	if eng.Ready() {
		t.Fatal("new engine must not be ready")
	}

	if err := eng.Stop(); err != nil {
		t.Fatalf("Stop before Start: %v", err)
	}
	if err := eng.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !eng.Ready() {
		t.Fatal("engine should be ready after Start")
	}
	if err := eng.Start(); err != nil {
		t.Fatalf("repeated Start: %v", err)
	}
	if err := eng.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if eng.Ready() {
		t.Fatal("engine must not be ready after Stop")
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
	if err := eng.Start(); err != nil {
		t.Fatalf("Start after Close: %v", err)
	}
	if !eng.Ready() {
		t.Fatal("engine should be ready after restarting")
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("final Close: %v", err)
	}
}

func TestEngineConcurrentLifecycleCalls(t *testing.T) {
	eng := New()
	if err := eng.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := eng.Start(); err != nil {
				t.Errorf("Start: %v", err)
			}
			if err := eng.Stop(); err != nil {
				t.Errorf("Stop: %v", err)
			}
		}()
	}
	workers.Wait()

	if err := eng.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestEngineConcurrentCloseWithOperations(t *testing.T) {
	for i := 0; i < 20; i++ {
		eng := New()
		if err := eng.Start(); err != nil {
			t.Fatalf("Start: %v", err)
		}

		ctx := context.Background()
		var wg sync.WaitGroup
		wg.Add(3)

		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = eng.BroadcastAIMessage(ctx, 1, 10, []byte("payload"))
			}
		}()

		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = eng.setPublishRate(generationForTest(eng), 1, 30)
			}
		}()

		go func() {
			defer wg.Done()
			_ = eng.Close()
		}()

		wg.Wait()
		_ = eng.Close()
	}
}

func TestUnsubscribeDeferredOnNotRunning(t *testing.T) {
	e := New()
	if err := e.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	tokenDeleted := false
	deleteToken := func() {
		tokenDeleted = true
	}

	// 停机后底层返回 ERR_NOT_RUNNING，应当暂存 token 避免立即与在途 worker 竞争
	if err := e.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	_ = e.unsubscribe(generationForTest(e), 1, 1, deleteToken)
	if tokenDeleted {
		t.Fatal("expected deleteToken to be deferred during ERR_NOT_RUNNING")
	}

	// 最终 Close 会排空并执行 pendingTokens
	if err := e.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if !tokenDeleted {
		t.Fatal("expected deleteToken to be executed after engine stop/close")
	}
}
