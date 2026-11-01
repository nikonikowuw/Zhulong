package task

import (
	"context"
	"sync"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/schedule"
	"go.uber.org/zap"
)

// ScheduleWorker periodically checks active tasks and broadcasts OnTaskChanged
// whenever any task algorithm transitions between active and inactive schedule windows.
type ScheduleWorker struct {
	store         *Store
	listeners     []TaskChangeListener
	logger        *zap.Logger
	checkInterval time.Duration
	nowFunc       func() time.Time

	mu         sync.Mutex
	lastActive map[string]bool // key: "taskID#algoID"
	stopCh     chan struct{}
	wg         sync.WaitGroup
}

// NewScheduleWorker constructs a new ScheduleWorker.
func NewScheduleWorker(
	store *Store,
	listeners []TaskChangeListener,
	logger *zap.Logger,
	checkInterval time.Duration,
) *ScheduleWorker {
	if logger == nil {
		logger = zap.NewNop()
	}
	if checkInterval <= 0 {
		checkInterval = 30 * time.Second
	}
	return &ScheduleWorker{
		store:         store,
		listeners:     listeners,
		logger:        logger,
		checkInterval: checkInterval,
		nowFunc:       time.Now,
		lastActive:    make(map[string]bool),
		stopCh:        make(chan struct{}),
	}
}

// SetNowFuncForTest overrides the time source for testing purposes.
func (w *ScheduleWorker) SetNowFuncForTest(fn func() time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.nowFunc = fn
}

// Start launches the periodic checking loop in a background goroutine.
func (w *ScheduleWorker) Start() {
	w.mu.Lock()
	if w.stopCh == nil {
		w.stopCh = make(chan struct{})
	}
	stopCh := w.stopCh
	w.mu.Unlock()

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(w.checkInterval)
		defer ticker.Stop()

		// Prime the status map on startup
		w.tick()

		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				w.tick()
			}
		}
	}()
}

func (w *ScheduleWorker) tick() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	w.Check(ctx)
}

// Stop cleanly terminates the worker loop.
func (w *ScheduleWorker) Stop() {
	w.mu.Lock()
	if w.stopCh != nil {
		close(w.stopCh)
		w.stopCh = nil
	}
	w.mu.Unlock()
	w.wg.Wait()
}

// Check evaluates all enabled tasks against the current time and notifies listeners of any flipped algorithm states.
func (w *ScheduleWorker) Check(ctx context.Context) {
	if w.store == nil {
		return
	}

	tasks, err := w.store.ListTasks(ctx)
	if err != nil {
		w.logger.Warn("schedule worker failed to list tasks", zap.Error(err))
		return
	}

	w.mu.Lock()
	now := w.nowFunc()
	currentActive := make(map[string]bool)
	changedAlgos := make(map[string]struct{})
	for _, tsk := range tasks {
		if !tsk.Enabled {
			continue
		}
		for _, algo := range tsk.Algorithms {
			if !algo.Enabled {
				continue
			}

			sched, err := schedule.Parse(algo.ScheduleJSON)
			isActive := true
			if err == nil && sched != nil {
				isActive = sched.IsActiveAt(now)
			}

			key := tsk.ID + "#" + algo.AlgorithmID
			currentActive[key] = isActive

			prevActive, found := w.lastActive[key]
			if found && prevActive != isActive {
				changedAlgos[algo.AlgorithmID] = struct{}{}
			}
		}
	}
	w.lastActive = currentActive
	w.mu.Unlock()

	for algoID := range changedAlgos {
		w.logger.Info("task algorithm schedule transitioned",
			zap.String("algorithm_id", algoID),
			zap.Time("transition_time", now),
		)
		for _, l := range w.listeners {
			if l != nil {
				l.OnTaskChanged(ctx, algoID)
			}
		}
	}
}
