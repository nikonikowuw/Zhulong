package algorithm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"github.com/nikonikowuw/Zhulong/internal/task"
	"go.uber.org/zap"
)

var (
	ErrAlgorithmActive          = errors.New("algorithm is currently active and cannot be deleted")
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

// normalizeInstallError translates package verification and extraction errors into standard API errors.
func normalizeInstallError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrPlatformMismatch):
		return httputil.NewError(http.StatusBadRequest, "ALGO_PLATFORM_MISMATCH", "Platform mismatch", err)
	case errors.Is(err, ErrUnsupportedArchive):
		return httputil.NewError(http.StatusBadRequest, "UNSUPPORTED_ARCHIVE_FORMAT", "Unsupported archive format", err)
	case errors.Is(err, ErrZipSlip):
		return httputil.NewError(http.StatusBadRequest, "ZIP_SLIP_ATTACK_DETECTED", "Zip-slip attack detected", err)
	case errors.Is(err, ErrDecompressionBombSize), errors.Is(err, ErrDecompressionBombFile):
		return httputil.NewError(http.StatusBadRequest, "DECOMPRESSION_BOMB_DETECTED", "Decompression bomb detected", err)
	case errors.Is(err, ErrVerificationFailed), errors.Is(err, ErrResultTypeMismatch):
		return apperr.Invalid("ALGO_VERIFICATION_FAILED", "Verification failed", err)
	case errors.Is(err, ErrInvalidManifestToken):
		return httputil.NewError(http.StatusBadRequest, "INVALID_MANIFEST_TOKEN", err.Error(), err)
	case errors.Is(err, ErrVersionAlreadyInstalled):
		return apperr.Conflict("ALGORITHM_VERSION_EXISTS", "Algorithm version is already installed", err)
	case errors.Is(err, ErrInvalidConfigSchema):
		return apperr.Invalid("ALGO_VERIFICATION_FAILED", "Invalid algorithm configuration schema", err)
	default:
		return httputil.NewError(http.StatusBadRequest, "ALGO_INSTALL_FAILED", err.Error(), err)
	}
}

// InstallAlgorithmFromStream streams an uploaded package archive into a managed temporary file,
// verifies and stages it, registers it in the database, and guarantees temporary file cleanup.
func (s *Service) InstallAlgorithmFromStream(ctx context.Context, r io.Reader, limit int64) (*Algorithm, error) {
	if limit <= 0 {
		limit = MaxUploadSize
	}
	tmpPath, err := SaveUploadStreamWithLimit(r, "", limit)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.Is(err, ErrFileTooLarge) || errors.As(err, &maxBytesErr) {
			return nil, httputil.NewError(http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "Uploaded file exceeds 500MB", err)
		}
		return nil, apperr.Internal("INTERNAL_ERROR", "Failed to save upload", err)
	}
	defer func() {
		if rmErr := os.Remove(tmpPath); rmErr != nil && !os.IsNotExist(rmErr) {
			s.logger.Warn("failed to remove temporary upload file", zap.String("path", tmpPath), zap.Error(rmErr))
		}
	}()

	return s.InstallAlgorithm(ctx, tmpPath)
}

// InstallAlgorithm handles archive extraction, static/dynamic verification, and DB registration.
func (s *Service) InstallAlgorithm(ctx context.Context, archivePath string) (*Algorithm, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	algo, err := s.verifier.StageAndVerify(ctx, archivePath, s.installDir)
	if err != nil {
		return nil, normalizeInstallError(fmt.Errorf("verify and stage algorithm: %w", err))
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
			return nil, normalizeInstallError(fmt.Errorf("update existing algorithm: %w", err))
		}
		s.logger.Info("upgraded existing algorithm",
			zap.String("id", algo.ID),
			zap.String("new_version", algo.Version),
			zap.String("prev_version", algo.PreviousVersion),
		)

		// If the previous version is active or supervised, transition the worker process
		// under the supervision of RollbackGuard.
		if s.supervisor != nil && (s.supervisor.GetStatus(existing.ID) == StatusRunning || existing.Status == StatusActive) {
			if upgradeErr := s.supervisor.UpgradeAlgorithm(ctx, algo.ToPackageMeta(), existing.ToPackageMeta()); upgradeErr != nil {
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
		return nil, normalizeInstallError(fmt.Errorf("check existing algorithm: %w", err))
	}

	if err := s.store.CreateAlgorithm(ctx, algo); err != nil {
		discardStagedVersion()
		return nil, normalizeInstallError(fmt.Errorf("save algorithm to database: %w", err))
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
		return nil, apperr.Internal("INTERNAL_ERROR", "Failed to list algorithms", err)
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
		if errors.Is(err, ErrNotFound) {
			return nil, apperr.NotFound("ALGORITHM_NOT_FOUND", "Algorithm not found", err)
		}
		return nil, apperr.Internal("INTERNAL_ERROR", "Failed to get algorithm", err)
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
		if errors.Is(err, ErrNotFound) {
			return apperr.NotFound("ALGORITHM_NOT_FOUND", "Algorithm not found", err)
		}
		return apperr.Internal("INTERNAL_ERROR", fmt.Sprintf("get algorithm: %v", err), err)
	}

	liveStatus := s.supervisor.GetStatus(id)
	if liveStatus == StatusCrashLoop {
		return apperr.Conflict("ALGORITHM_IN_CRASH_LOOP", "Algorithm is in CRASH_LOOP, please reset it first", nil)
	}
	if liveStatus == StatusRunning || liveStatus == StatusStarting {
		// 已经在运行中，幂等成功并确保 DB 为 ACTIVE
		if algo.Status != StatusActive {
			_ = s.store.UpdateAlgorithmStatus(ctx, id, StatusActive)
		}
		return nil
	}

	if err := s.supervisor.StartAlgorithm(ctx, algo.ToPackageMeta()); err != nil {
		if errors.Is(err, ErrNoBrokerSockets) {
			// 无活跃任务：平滑进入待命态 (Standby)，写 ACTIVE 到 DB
			s.logger.Info("algorithm enabled in standby state (no active tasks)", zap.String("id", id))
			if err := s.store.UpdateAlgorithmStatus(ctx, id, StatusActive); err != nil {
				return apperr.Internal("INTERNAL_ERROR", fmt.Sprintf("update algorithm status: %v", err), err)
			}
			return nil
		}
		if errors.Is(err, ErrAlgorithmAlreadyRunning) {
			// 并发注册竞争赢家已拉起，幂等成功
			if algo.Status != StatusActive {
				_ = s.store.UpdateAlgorithmStatus(ctx, id, StatusActive)
			}
			return nil
		}
		return apperr.Internal("INTERNAL_ERROR", fmt.Sprintf("start runner supervisor: %v", err), err)
	}

	if err := s.store.UpdateAlgorithmStatus(ctx, id, StatusActive); err != nil {
		return apperr.Internal("INTERNAL_ERROR", fmt.Sprintf("update algorithm status: %v", err), err)
	}
	return nil
}

// DisableAlgorithm stops the runner subprocess and updates DB status to DISABLED.
func (s *Service) DisableAlgorithm(ctx context.Context, id string) error {
	if _, err := s.store.GetAlgorithm(ctx, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return apperr.NotFound("ALGORITHM_NOT_FOUND", "Algorithm not found", err)
		}
		return apperr.Internal("INTERNAL_ERROR", fmt.Sprintf("get algorithm: %v", err), err)
	}

	if err := s.supervisor.StopAlgorithm(ctx, id); err != nil {
		return apperr.Internal("INTERNAL_ERROR", fmt.Sprintf("stop runner supervisor: %v", err), err)
	}
	if err := s.store.UpdateAlgorithmStatus(ctx, id, StatusDisabled); err != nil {
		return apperr.Internal("INTERNAL_ERROR", fmt.Sprintf("update algorithm status: %v", err), err)
	}
	return nil
}

// ResetAlgorithm resets an algorithm from CRASH_LOOP back to DISABLED so it can be re-enabled.
func (s *Service) ResetAlgorithm(ctx context.Context, id string) error {
	if err := s.supervisor.ResetAlgorithm(ctx, id); err != nil {
		if errors.Is(err, ErrAlgorithmNotInCrashLoop) {
			return apperr.Conflict("ALGORITHM_NOT_IN_CRASH_LOOP", "Algorithm is not in CRASH_LOOP", err)
		}
		return apperr.Internal("INTERNAL_ERROR", fmt.Sprintf("reset algorithm: %v", err), err)
	}
	return nil
}

// DeleteAlgorithm stops and uninstalls an algorithm from disk and database.
func (s *Service) DeleteAlgorithm(ctx context.Context, id string) error {
	algo, err := s.store.GetAlgorithm(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return apperr.NotFound("ALGORITHM_NOT_FOUND", "Algorithm not found", err)
		}
		return apperr.Internal("INTERNAL_ERROR", fmt.Sprintf("get algorithm: %v", err), err)
	}

	status := s.supervisor.GetStatus(id)
	switch status {
	case StatusRunning, StatusStarting, StatusRollingBack:
		return apperr.Conflict("ALGORITHM_IS_ACTIVE", "Algorithm is active", ErrAlgorithmActive)
	}
	if err := s.supervisor.StopAlgorithm(ctx, id); err != nil {
		return apperr.Internal("INTERNAL_ERROR", fmt.Sprintf("stop algorithm before deletion: %v", err), err)
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

	if err := s.store.DeleteAlgorithm(ctx, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return apperr.NotFound("ALGORITHM_NOT_FOUND", "Algorithm not found", err)
		}
		return apperr.Internal("INTERNAL_ERROR", fmt.Sprintf("delete algorithm from db: %v", err), err)
	}
	return nil
}

// ScheduleAlgorithmRestart schedules a debounced restart for the algorithm runner.
func (s *Service) ScheduleAlgorithmRestart(algorithmID string) {
	s.scheduleAlgorithmRestart(algorithmID)
}

// OnTaskChanged implements task.TaskChangeListener.
func (s *Service) OnTaskChanged(ctx context.Context, algorithmID string) {
	s.ScheduleAlgorithmRestart(algorithmID)
}

// GetAlgorithmSpec implements task.AlgorithmSpecProvider.
func (s *Service) GetAlgorithmSpec(ctx context.Context, idOrName string) (*task.AlgorithmSpec, error) {
	algo, err := s.store.GetAlgorithm(ctx, idOrName)
	if err != nil {
		if algoByName, errName := s.store.GetAlgorithmByName(ctx, idOrName); errName == nil {
			algo = algoByName
		} else {
			return nil, err
		}
	}
	return &task.AlgorithmSpec{
		ID:               algo.ID,
		Name:             algo.Name,
		ConfigSchemaJSON: algo.ConfigSchemaJSON,
		ComputeCost:      algo.ComputeCost,
		Status:           algo.Status,
	}, nil
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
	algo, err := s.store.GetAlgorithm(context.Background(), algorithmID)
	if err != nil || algo.Status != StatusActive {
		// 算法未启用或不存在：不拉起进程
		return
	}

	liveStatus := s.supervisor.GetStatus(algorithmID)
	if liveStatus == StatusRunning || liveStatus == StatusStarting {
		// 运行中：执行 RestartAlgorithm（若任务变为空，Supervisor.RestartAlgorithm 会优雅停止并恢复 ACTIVE）
		if err := s.supervisor.RestartAlgorithm(context.Background(), algorithmID); err != nil {
			s.logger.Warn("failed to restart algorithm after task change",
				zap.String("algorithm_id", algorithmID),
				zap.Error(err),
			)
		}
	} else if liveStatus == StatusDisabled {
		// 处于待命态（Standby）：新任务加入，尝试从待命态拉起 Runner
		if err := s.supervisor.StartAlgorithm(context.Background(), algo.ToPackageMeta()); err != nil {
			if !errors.Is(err, ErrNoBrokerSockets) && !errors.Is(err, ErrAlgorithmAlreadyRunning) {
				s.logger.Warn("failed to start standby algorithm after task change",
					zap.String("algorithm_id", algorithmID),
					zap.Error(err),
				)
			}
		}
	}
}

// StopAll stops all active subprocesses.
func (s *Service) StopAll(timeout time.Duration) {
	s.supervisor.StopAll(timeout)
}
