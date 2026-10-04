package engine

import (
	"sync"
	"testing"
)

func TestEngineLifecycle(t *testing.T) {
	engine := New()
	if engine.Ready() {
		t.Fatal("new engine must not be ready")
	}

	if err := engine.Stop(); err != nil {
		t.Fatalf("Stop before Start: %v", err)
	}
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !engine.Ready() {
		t.Fatal("engine should be ready after Start")
	}
	if err := engine.Start(); err != nil {
		t.Fatalf("repeated Start: %v", err)
	}
	if err := engine.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if engine.Ready() {
		t.Fatal("engine must not be ready after Stop")
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
	if err := engine.Start(); err != nil {
		t.Fatalf("Start after Close: %v", err)
	}
	if !engine.Ready() {
		t.Fatal("engine should be ready after restarting")
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("final Close: %v", err)
	}
}

func TestEngineConcurrentLifecycleCalls(t *testing.T) {
	engine := New()
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := engine.Start(); err != nil {
				t.Errorf("Start: %v", err)
			}
			if err := engine.Stop(); err != nil {
				t.Errorf("Stop: %v", err)
			}
		}()
	}
	workers.Wait()

	if err := engine.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
