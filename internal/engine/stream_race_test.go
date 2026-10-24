package engine

import (
	"sync"
	"testing"
)

// TestStreamCloseRace 验证高并发下 Stream.Close 与 activeAISub 读写不存在数据竞争。
func TestStreamCloseRace(t *testing.T) {
	e := New()
	if err := e.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer e.Close()

	var wg sync.WaitGroup
	for i := 0; i < 300; i++ {
		generation := generationForTest(e)
		s := &Stream{engine: e, id: 424242, consumerID: 7, generation: generation}
		aiSub := newAIResultSubscription(s, 424242, generation, 16)
		wg.Add(1)
		go func(s *Stream, sub *AIResultSubscription) {
			defer wg.Done()
			s.mu.Lock()
			s.activeAISub = sub
			s.mu.Unlock()
			s.onAISubClosed(sub)
		}(s, aiSub)
		_ = s.Close()
	}
	wg.Wait()
}
