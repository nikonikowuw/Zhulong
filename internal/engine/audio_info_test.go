package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestAudioInfoUnknownStreamIsNotFound 钉住「未知流 → NOT_FOUND」这条映射。
//
// 上层要区分两件事：「这路流不存在 / 已经没了」与「这路流存在但没有音轨」。
// 前者必须能被 errors.Is(err, ErrNotFound) 识别，后者是 HasAudio=false 的成功返回。
// 两者一旦混淆，前端只能把「流消失」画成一个灰色的喇叭。
func TestAudioInfoUnknownStreamIsNotFound(t *testing.T) {
	e := New()
	if err := e.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer e.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	e.mu.Lock()
	gen := e.generation
	e.mu.Unlock()

	if _, err := e.getAudioInfo(gen, 1<<40); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unknown stream, got %v", err)
	}
	_ = ctx
}

// TestAudioInfoRejectsStaleGeneration 钉住代次校验：引擎重启后旧句柄不得再查。
func TestAudioInfoRejectsStaleGeneration(t *testing.T) {
	e := New()
	if err := e.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer e.Close()

	e.mu.Lock()
	gen := e.generation
	e.mu.Unlock()

	if _, err := e.getAudioInfo(gen+1, 1); !errors.Is(err, ErrStaleResource) {
		t.Fatalf("expected ErrStaleResource for stale generation, got %v", err)
	}
}
