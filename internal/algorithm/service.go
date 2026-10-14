package algorithm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"
)

const MaxComputePoints int64 = 1000

var (
	ErrAlgorithmActive          = errors.New("algorithm is currently active and cannot be deleted")
	ErrComputeBudgetExceeded    = errors.New("total compute cost exceeds 1000 points budget")
	ErrCameraAlreadyBound       = errors.New("camera already bound to this algorithm")
	ErrAlgorithmAlreadyExisting = errors.New("algorithm with this name already exists")
)

// Service provides high-level business lifecycle management for algorithms.
type Service struct {
	store      *Store
	verifier   *Verifier
	supervisor *Supervisor
	installDir string
	logger     *zap.Logger
	mu         sync.Mutex

	// restartDebounce 合并同一算法包在短窗口内的连续任务变更，
	// 避免批量建 N 个任务触发 N 次进程重启（design.md §7.4）。
	restartMu       sync.Mutex
	restartTimers   map[string]*time.Timer
	restartPending  map[string]struct{}
	restartDebounce time.Duration
}

// defaultRestartDebounce 是任务变更触发算法包重启的默认合并窗口。
const defaultRestartDebounce = 200 * time.Millisecond

// NewService creates a new Service instance.
func NewService(store *Store, verifier *Verifier, supervisor *Supervisor, installDir string, logger *zap.Logger) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Service{
		store:           store,
		verifier:        verifier,
		supervisor:      supervisor,
		installDir:      installDir,
		logger:          logger.Named("algorithm_service"),
		restartTimers:   make(map[string]*time.Timer),
		restartPending:  make(map[string]struct{}),
		restartDebounce: defaultRestartDebounce,
	}
}

// InstallAlgorithm handles archive extraction, static/dynamic verification, and DB registration.
func (s *Service) InstallAlgorithm(ctx context.Context, archivePath string) (*Algorithm, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	algo, err := s.verifier.StageAndVerify(ctx, archivePath, s.installDir)
	if err != nil {
		return nil, fmt.Errorf("verify and stage algorithm: %w", err)
	}
	discardStagedVersion := func() {
		if removeErr := os.RemoveAll(algo.InstallDir(s.installDir)); removeErr != nil {
			s.logger.Warn("failed to remove unregistered algorithm version", zap.String("path", algo.InstallDir(s.installDir)), zap.Error(removeErr))
		}
	}

	// Check if an algorithm with same name/ID already exists
	existing, err := s.store.GetAlgorithm(ctx, algo.ID)
	if err == nil && existing != nil {
		algo.PreviousVersion = existing.Version
		if err := s.store.UpdateAlgorithm(ctx, algo); err != nil {
			discardStagedVersion()
			return nil, fmt.Errorf("update existing algorithm: %w", err)
		}
		s.logger.Info("upgraded existing algorithm",
			zap.String("id", algo.ID),
			zap.String("new_version", algo.Version),
			zap.String("prev_version", algo.PreviousVersion),
		)

		// If the previous version is active or supervised, transition the worker process
		// under the supervision of RollbackGuard.
		if s.supervisor != nil && (s.supervisor.GetStatus(existing.ID) == StatusRunning || existing.Status == StatusActive) {
			if upgradeErr := s.supervisor.UpgradeAlgorithm(ctx, *algo, *existing); upgradeErr != nil {
				s.logger.Warn("failed to transition runner process to upgraded version",
					zap.String("id", algo.ID),
					zap.Error(upgradeErr),
				)
			}
		}

		return algo, nil
	}
	if !errors.Is(err, ErrNotFound) {
		discardStagedVersion()
		return nil, fmt.Errorf("check existing algorithm: %w", err)
	}

	if err := s.store.CreateAlgorithm(ctx, algo); err != nil {
		discardStagedVersion()
		return nil, fmt.Errorf("save algorithm to database: %w", err)
	}

	s.logger.Info("installed new algorithm",
		zap.String("id", algo.ID),
		zap.String("version", algo.Version),
		zap.String("task_type", algo.TaskType),
	)

	return algo, nil
}

// ListAlgorithms returns all registered algorithms.
func (s *Service) ListAlgorithms(ctx context.Context) ([]Algorithm, error) {
	algos, err := s.store.ListAlgorithms(ctx)
	if err != nil {
		return nil, err
	}
	// Enrich with live supervisor status
	for i := range algos {
		liveStatus := s.supervisor.GetStatus(algos[i].ID)
		if liveStatus != StatusDisabled {
			algos[i].Status = liveStatus
		}
	}
	return algos, nil
}

// GetAlgorithm returns an algorithm by ID.
func (s *Service) GetAlgorithm(ctx context.Context, id string) (*Algorithm, error) {
	algo, err := s.store.GetAlgorithm(ctx, id)
	if err != nil {
		return nil, err
	}
	liveStatus := s.supervisor.GetStatus(algo.ID)
	if liveStatus != StatusDisabled {
		algo.Status = liveStatus
	}
	return algo, nil
}

// EnableAlgorithm starts the runner subprocess and updates DB status to ACTIVE.
func (s *Service) EnableAlgorithm(ctx context.Context, id string) error {
	algo, err := s.store.GetAlgorithm(ctx, id)
	if err != nil {
		return err
	}

	if err := s.supervisor.StartAlgorithm(ctx, *algo); err != nil {
		return fmt.Errorf("start runner supervisor: %w", err)
	}

	return s.store.UpdateAlgorithmStatus(ctx, id, StatusActive)
}

// DisableAlgorithm stops the runner subprocess and updates DB status to DISABLED.
func (s *Service) DisableAlgorithm(ctx context.Context, id string) error {
	if err := s.supervisor.StopAlgorithm(ctx, id); err != nil {
		return fmt.Errorf("stop runner supervisor: %w", err)
	}
	return s.store.UpdateAlgorithmStatus(ctx, id, StatusDisabled)
}

// ResetAlgorithm resets an algorithm from CRASH_LOOP back to DISABLED so it can be re-enabled.
func (s *Service) ResetAlgorithm(ctx context.Context, id string) error {
	return s.supervisor.ResetAlgorithm(ctx, id)
}

// DeleteAlgorithm stops and uninstalls an algorithm from disk and database.
func (s *Service) DeleteAlgorithm(ctx context.Context, id string) error {
	algo, err := s.store.GetAlgorithm(ctx, id)
	if err != nil {
		return err
	}

	status := s.supervisor.GetStatus(id)
	switch status {
	case StatusRunning, StatusStarting, StatusRollingBack:
		return ErrAlgorithmActive
	}
	if err := s.supervisor.StopAlgorithm(ctx, id); err != nil {
		return fmt.Errorf("stop algorithm before deletion: %w", err)
	}

	// Delete associated analysis tasks
	if err := s.store.DeleteTasksByAlgorithm(ctx, algo.ID); err != nil {
		s.logger.Warn("failed to delete associated tasks for algorithm", zap.String("id", algo.ID), zap.Error(err))
	}

	// Remove files
	algoBaseDir := filepath.Join(s.installDir, algo.ID)
	if err := os.RemoveAll(algoBaseDir); err != nil && !os.IsNotExist(err) {
		s.logger.Warn("failed to remove algorithm directory on disk", zap.String("path", algoBaseDir), zap.Error(err))
	}

	return s.store.DeleteAlgorithm(ctx, id)
}

// CreateTask registers an analysis task on a camera stream, validating compute budget and schema.
func (s *Service) CreateTask(ctx context.Context, t *AnalysisTask) error {
	algo, err := s.store.GetAlgorithm(ctx, t.AlgorithmID)
	if err != nil {
		// Fallback to name lookup if caller provided name instead of ID
		if algoByName, errName := s.store.GetAlgorithmByName(ctx, t.AlgorithmID); errName == nil {
			algo = algoByName
			t.AlgorithmID = algo.ID
		} else {
			return fmt.Errorf("algorithm %q: %w", t.AlgorithmID, err)
		}
	}

	if err := validateTaskConfig(algo.ConfigSchemaJSON, t.ConfigJSON); err != nil {
		return err
	}

	if err := s.store.CreateTaskWithinBudget(ctx, t, MaxComputePoints); err != nil {
		return err
	}

	// D5：任务增删改需要让该算法包重新解析 --broker 列表。
	s.scheduleAlgorithmRestart(t.AlgorithmID)
	return nil
}

// UpdateTask persists an analysis task change and refreshes the owning algorithm's
// broker subscriptions (D5).
func (s *Service) UpdateTask(ctx context.Context, t *AnalysisTask) error {
	if t == nil {
		return fmt.Errorf("%w: task is nil", ErrInvalidTaskConfig)
	}
	if _, err := ValidateSampleFPS(t.SampleFPS); err != nil {
		return err
	}
	algo, err := s.store.GetAlgorithm(ctx, t.AlgorithmID)
	if err != nil {
		return fmt.Errorf("algorithm %q: %w", t.AlgorithmID, err)
	}
	if err := validateTaskConfig(algo.ConfigSchemaJSON, t.ConfigJSON); err != nil {
		return err
	}
	if err := s.store.UpdateTask(ctx, t); err != nil {
		return err
	}
	s.scheduleAlgorithmRestart(t.AlgorithmID)
	return nil
}

// DeleteTask removes an analysis task and refreshes the owning algorithm's
// broker subscriptions (D5).
func (s *Service) DeleteTask(ctx context.Context, id string) error {
	task, err := s.store.GetTask(ctx, id)
	if err != nil {
		return err
	}

	if err := s.store.DeleteTask(ctx, id); err != nil {
		return err
	}

	s.scheduleAlgorithmRestart(task.AlgorithmID)
	return nil
}

// scheduleAlgorithmRestart 合并短窗口内的连续任务变更，然后对该算法包执行
// StopAlgorithm + 重新解析 + StartAlgorithm。
//
// ⚠️ 重启失败不阻塞 HTTP 响应：与既有 supervisor 的「有界恢复」语义一致，
// 仅记录告警（含原因），由 supervisor 的状态机自行退避与熔断。
func (s *Service) scheduleAlgorithmRestart(algorithmID string) {
	if s.supervisor == nil || algorithmID == "" {
		return
	}

	s.restartMu.Lock()
	s.restartPending[algorithmID] = struct{}{}
	if s.restartTimers == nil {
		s.restartTimers = make(map[string]*time.Timer)
	}
	if timer, ok := s.restartTimers[algorithmID]; ok {
		timer.Stop()
	}
	window := s.restartDebounce
	if window <= 0 {
		window = defaultRestartDebounce
	}
	s.restartTimers[algorithmID] = time.AfterFunc(window, func() {
		s.restartMu.Lock()
		delete(s.restartTimers, algorithmID)
		_, pending := s.restartPending[algorithmID]
		delete(s.restartPending, algorithmID)
		s.restartMu.Unlock()
		if !pending {
			return
		}
		s.restartAlgorithmNow(algorithmID)
	})
	s.restartMu.Unlock()
}

// FlushPendingRestarts 立即执行所有待处理的算法包重启。
// 供测试与停机前收敛使用，避免 debounce 窗口内的变更被丢弃。
func (s *Service) FlushPendingRestarts() {
	if s.supervisor == nil {
		return
	}

	s.restartMu.Lock()
	pending := make([]string, 0, len(s.restartPending))
	for id := range s.restartPending {
		pending = append(pending, id)
		if timer, ok := s.restartTimers[id]; ok {
			timer.Stop()
		}
		delete(s.restartTimers, id)
	}
	s.restartPending = make(map[string]struct{})
	s.restartMu.Unlock()

	for _, id := range pending {
		s.restartAlgorithmNow(id)
	}
}

func (s *Service) restartAlgorithmNow(algorithmID string) {
	if s.supervisor.GetStatus(algorithmID) != StatusRunning {
		// 包未运行：下次 EnableAlgorithm 时自然解析，无需重启。
		return
	}
	if err := s.supervisor.RestartAlgorithm(context.Background(), algorithmID); err != nil {
		s.logger.Warn("failed to restart algorithm after task change",
			zap.String("algorithm_id", algorithmID),
			zap.Error(err),
		)
	}
}

// ListTasks returns analysis tasks for a given camera or all tasks if cameraID is empty.
func (s *Service) ListTasks(ctx context.Context, cameraID string) ([]AnalysisTask, error) {
	if cameraID != "" {
		return s.store.ListTasksByCamera(ctx, cameraID)
	}
	return s.store.ListTasks(ctx)
}

// StopAll stops all active subprocesses.
func (s *Service) StopAll(timeout time.Duration) {
	s.supervisor.StopAll(timeout)
}
