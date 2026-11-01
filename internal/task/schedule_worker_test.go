package task_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/task"
)

type recordedListener struct {
	mu     sync.Mutex
	events []string
}

func (l *recordedListener) OnTaskChanged(ctx context.Context, algorithmID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, algorithmID)
}

func (l *recordedListener) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.events)
}

func (l *recordedListener) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = nil
}

func TestScheduleWorkerTransition(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	insertMockAlgorithm(t, db, "algo_sched", "scheduled_detector", 10)

	// Task runs on Monday 08:00 - 18:00
	tsk := &task.Task{
		ID:       "task_sched_1",
		CameraID: "cam_sched_1",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{
				ID:           "sub_sched_1",
				AlgorithmID:  "algo_sched",
				Enabled:      true,
				SampleFPS:    5,
				ConfigJSON:   "{}",
				ScheduleJSON: `{"enabled": true, "days": {"monday": [{"start": "08:00", "end": "18:00"}]}}`,
			},
		},
	}
	if err := store.CreateTask(ctx, tsk); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	listener := &recordedListener{}
	worker := task.NewScheduleWorker(store, []task.TaskChangeListener{listener}, nil, 100*time.Millisecond)

	// Mock time: Monday 2026-10-19
	loc := time.UTC
	currentTime := time.Date(2026, 10, 19, 7, 59, 0, 0, loc)
	worker.SetNowFuncForTest(func() time.Time {
		return currentTime
	})

	// 1. Initial check at 07:59 (outside window) -> primes initial state, does not fire listener
	worker.Check(ctx)
	if listener.count() != 0 {
		t.Errorf("expected 0 events on initial prime, got %d", listener.count())
	}

	// 2. Advance time to 08:01 (transitions to active) -> must fire listener
	currentTime = time.Date(2026, 10, 19, 8, 1, 0, 0, loc)
	worker.Check(ctx)
	if listener.count() != 1 {
		t.Fatalf("expected 1 event on active transition, got %d", listener.count())
	}
	if listener.events[0] != "algo_sched" {
		t.Errorf("expected algorithm algo_sched, got %s", listener.events[0])
	}
	listener.reset()

	// 3. Time advances to 12:00 (still active) -> should NOT fire
	currentTime = time.Date(2026, 10, 19, 12, 0, 0, 0, loc)
	worker.Check(ctx)
	if listener.count() != 0 {
		t.Errorf("expected 0 events while staying active, got %d", listener.count())
	}

	// 4. Time advances to 18:01 (transitions to inactive) -> must fire listener
	currentTime = time.Date(2026, 10, 19, 18, 1, 0, 0, loc)
	worker.Check(ctx)
	if listener.count() != 1 {
		t.Fatalf("expected 1 event on inactive transition, got %d", listener.count())
	}
	if listener.events[0] != "algo_sched" {
		t.Errorf("expected algorithm algo_sched, got %s", listener.events[0])
	}
	listener.reset()

	// 5. Time advances to 20:00 (still inactive) -> should NOT fire
	currentTime = time.Date(2026, 10, 19, 20, 0, 0, 0, loc)
	worker.Check(ctx)
	if listener.count() != 0 {
		t.Errorf("expected 0 events while staying inactive, got %d", listener.count())
	}
}

func TestScheduleWorkerStartStop(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	worker := task.NewScheduleWorker(store, nil, nil, 10*time.Millisecond)

	worker.Start()
	time.Sleep(30 * time.Millisecond)
	worker.Stop()
}
