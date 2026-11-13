package task

import (
	"context"
	"sync"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/schedule"
	"go.uber.org/zap"
)

type taskAlgorithmKey struct {
	taskID      string
	algorithmID string
}

// ScheduleWorker periodically checks active tasks and broadcasts OnTaskChanged
// whenever any task algorithm transitions between active and inactive schedule windows.
type ScheduleWorker struct {
	store         *Store
	listeners     []TaskChangeListener
	logger        *zap.Logger
	checkInterval time.Duration
	nowFunc       func() time.Time

	mu         sync.Mutex
	started    bool
	lastActive map[taskAlgorithmKey]bool
	stopCh     chan struct{}
	cancel     context.CancelFunc
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
		lastActive:    make(map[taskAlgorithmKey]bool),
	}
}

// Start launches the periodic checking loop in a background goroutine.
// Repeated calls while already running are no-ops.
func (w *ScheduleWorker) Start() {
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		return
	}
	w.started = true
	stopCh := make(chan struct{})
	w.stopCh = stopCh
	stopCtx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.wg.Add(1)
	w.mu.Unlock()

	go func() {
		defer w.wg.Done()

		// Prime the status map on startup
		w.tick(stopCtx)

		// Align to the next checkInterval wall-clock boundary to eliminate phase lag
		now := w.nowFunc()
		intervalNano := w.checkInterval.Nanoseconds()
		if intervalNano > 0 {
			remainder := now.UnixNano() % intervalNano
			alignDelay := time.Duration(intervalNano - remainder)
			if alignDelay < 100*time.Millisecond {
				alignDelay += w.checkInterval
			}

			timer := time.NewTimer(alignDelay)
			select {
			case <-stopCh:
				timer.Stop()
				return
			case <-timer.C:
				w.tick(stopCtx)
			}
		}

		ticker := time.NewTicker(w.checkInterval)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				w.tick(stopCtx)
			}
		}
	}()
}

// Stop cleanly terminates the worker loop and cancels any in-flight check.
func (w *ScheduleWorker) Stop() {
	w.mu.Lock()
	if !w.started {
		w.mu.Unlock()
		return
	}
	w.started = false
	if w.cancel != nil {
		w.cancel()
		w.cancel = nil
	}
	if w.stopCh != nil {
		close(w.stopCh)
		w.stopCh = nil
	}
	w.mu.Unlock()
	w.wg.Wait()
}

func (w *ScheduleWorker) tick(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	w.check(ctx)
}

// check evaluates all enabled tasks against the current time and notifies listeners of any flipped algorithm states.
func (w *ScheduleWorker) check(ctx context.Context) {
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
	currentActive := make(map[taskAlgorithmKey]bool, len(tasks))
	changedAlgos := make(map[string]struct{})
	for i := range tasks {
		tsk := &tasks[i]
		if !tsk.Enabled {
			continue
		}
		for j := range tsk.Algorithms {
			algo := &tsk.Algorithms[j]
			if !algo.Enabled {
				continue
			}

			isActive := true
			sched, err := schedule.Parse(algo.ScheduleJSON)
			if err != nil {
				w.logger.Warn(
					"invalid schedule config in schedule worker; treating as always active",
					zap.String("task_id", tsk.ID),
					zap.String("algorithm_id", algo.AlgorithmID),
					zap.Error(err),
				)
			} else if sched != nil {
				isActive = sched.IsActiveAt(now)
			}

			key := taskAlgorithmKey{taskID: tsk.ID, algorithmID: algo.AlgorithmID}
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
		w.logger.Info(
			"task algorithm schedule transitioned",
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
