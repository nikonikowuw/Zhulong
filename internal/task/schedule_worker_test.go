package task

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type recordedListener struct {
	mu     sync.Mutex
	events []string
}

func (l *recordedListener) OnTaskChanged(_ context.Context, algorithmID string) {
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

func setupScheduleWorkerStore(t *testing.T) (*Store, *gorm.DB) {
	t.Helper()
	dbStore := database.New(t.TempDir(), zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() {
		if err := dbStore.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	return NewStore(dbStore), dbStore.DB()
}

func TestScheduleWorkerTransition(t *testing.T) {
	store, db := setupScheduleWorkerStore(t)
	ctx := context.Background()

	err := db.Exec(`INSERT INTO algorithms (
		id, name, version, task_type, category, target_arch, target_device,
		compute_cost, memory_mb, description, manifest_json, config_schema_json,
		status, previous_version, created_at, updated_at
	) VALUES (
		'algo_sched', 'scheduled_detector', '1.0.0', 'detection', 'safety', 'x86_64', 'cpu', 10, 64,
		'', '{}', '{}', 'ACTIVE', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
	)`).Error
	if err != nil {
		t.Fatalf("insert mock algorithm: %v", err)
	}

	tsk := &Task{
		ID:       "task_sched_1",
		CameraID: "cam_sched_1",
		Enabled:  true,
		Algorithms: []TaskAlgorithm{
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
	worker := NewScheduleWorker(
		store,
		[]TaskChangeListener{listener},
		nil,
		100*time.Millisecond,
	)

	// Monday 2026-10-19
	currentTime := time.Date(2026, 10, 19, 7, 59, 0, 0, time.UTC)
	worker.nowFunc = func() time.Time {
		return currentTime
	}

	// 1. Initial check at 07:59 (outside window) primes initial state without firing listener.
	worker.check(ctx)
	if listener.count() != 0 {
		t.Errorf("expected 0 events on initial prime, got %d", listener.count())
	}

	// 2. Advance time to 08:01 (transitions to active) -> fires listener.
	currentTime = time.Date(2026, 10, 19, 8, 1, 0, 0, time.UTC)
	worker.check(ctx)
	if listener.count() != 1 {
		t.Fatalf("expected 1 event on active transition, got %d", listener.count())
	}
	if listener.events[0] != "algo_sched" {
		t.Errorf("expected algorithm algo_sched, got %s", listener.events[0])
	}
	listener.reset()

	// 3. Time advances to 12:00 (still active) -> does not fire.
	currentTime = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	worker.check(ctx)
	if listener.count() != 0 {
		t.Errorf("expected 0 events while staying active, got %d", listener.count())
	}

	// 4. Time advances to 18:01 (transitions to inactive) -> fires listener.
	currentTime = time.Date(2026, 10, 19, 18, 1, 0, 0, time.UTC)
	worker.check(ctx)
	if listener.count() != 1 {
		t.Fatalf("expected 1 event on inactive transition, got %d", listener.count())
	}
	if listener.events[0] != "algo_sched" {
		t.Errorf("expected algorithm algo_sched, got %s", listener.events[0])
	}
	listener.reset()

	// 5. Time advances to 20:00 (still inactive) -> does not fire.
	currentTime = time.Date(2026, 10, 19, 20, 0, 0, 0, time.UTC)
	worker.check(ctx)
	if listener.count() != 0 {
		t.Errorf("expected 0 events while staying inactive, got %d", listener.count())
	}
}

func TestScheduleWorkerStartStop(t *testing.T) {
	store, _ := setupScheduleWorkerStore(t)

	worker := NewScheduleWorker(
		store,
		nil,
		nil,
		10*time.Millisecond,
	)

	worker.Start()
	time.Sleep(30 * time.Millisecond)
	worker.Stop()
}
