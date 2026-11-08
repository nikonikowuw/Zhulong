package algorithm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/aitypes"
	"github.com/nikonikowuw/Zhulong/internal/algorunner"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"github.com/nikonikowuw/Zhulong/internal/task"
	"go.uber.org/zap"
)

var (
	ErrAlgorithmActive          = errors.New("algorithm is currently active and cannot be deleted")
	ErrAlgorithmAlreadyExisting = errors.New("algorithm with this name already exists")
)

// internalErrorCode 是服务端内部失败的统一机器码，文案由 httputil 按
// Accept-Language 本地化，因此 message 只作为无 locale 条目时的兜底。
const internalErrorCode = "INTERNAL_ERROR"

// Service provides high-level business lifecycle management for algorithms.
type Service struct {
	store      *Store
	verifier   *Verifier
	supervisor *algorunner.Supervisor
	rt         algorunner.Runtime
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
func NewService(
	store *Store,
	verifier *Verifier,
	supervisor *algorunner.Supervisor,
	rt algorunner.Runtime,
	logger *zap.Logger,
) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Service{
		store:           store,
		verifier:        verifier,
		supervisor:      supervisor,
		rt:              rt,
		logger:          logger.Named("algorithm.service"),
		restartTimers:   make(map[string]*time.Timer),
		restartPending:  make(map[string]struct{}),
		restartDebounce: defaultRestartDebounce,
	}
}

// wrapLoadError 把 store 的读取失败统一成 API 错误。读取算法是几乎所有写
// 操作的第一步，分散写会让 ALGORITHM_NOT_FOUND 的文案与机器码逐渐漂移。
func wrapLoadError(err error) error {
	if errors.Is(err, ErrNotFound) {
		return apperr.NotFound("ALGORITHM_NOT_FOUND", "Algorithm not found", err)
	}
	return apperr.Internal(internalErrorCode, "Failed to get algorithm", err)
}

// loadAlgorithm 读取算法并把 store 错误映射成 API 错误。
func (s *Service) loadAlgorithm(ctx context.Context, id string) (*Algorithm, error) {
	algo, err := s.store.GetAlgorithm(ctx, id)
	if err != nil {
		return nil, wrapLoadError(err)
	}
	return algo, nil
}

// markActiveBestEffort 在 runner 已就绪后把 DB 状态收敛为 ACTIVE。
//
// 进程已经起来了，所以写入失败不该改变 HTTP 结果；但必须留痕：module.go 的
// 开机 auto-start 只看 StatusActive，静默失败会让算法在下一次重启后再也不起来。
func (s *Service) markActiveBestEffort(ctx context.Context, algo *Algorithm) {
	if algo.Status == StatusActive {
		return
	}
	if err := s.store.UpdateAlgorithmStatus(ctx, algo.ID, StatusActive); err != nil {
		s.logger.Warn("runner is up but the ACTIVE status could not be persisted; "+
			"the algorithm will not auto-start after a host restart",
			zap.String("algorithm_id", algo.ID),
			zap.Error(err),
		)
	}
}

// normalizeInstallError translates package verification and extraction errors into standard API errors.
func normalizeInstallError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrPlatformTargetRetired):
		return httputil.NewError(
			http.StatusBadRequest,
			"PLATFORM_TARGET_CPU_RETIRED",
			err.Error(),
			err,
		)
	case errors.Is(err, ErrPlatformMismatch):
		return httputil.NewError(
			http.StatusBadRequest,
			"ALGO_PLATFORM_MISMATCH",
			"Platform mismatch",
			err,
		)
	case errors.Is(err, ErrUnsupportedArchive):
		return httputil.NewError(
			http.StatusBadRequest,
			"UNSUPPORTED_ARCHIVE_FORMAT",
			"Unsupported archive format",
			err,
		)
	case errors.Is(err, ErrZipSlip):
		return httputil.NewError(
			http.StatusBadRequest,
			"ZIP_SLIP_ATTACK_DETECTED",
			"Zip-slip attack detected",
			err,
		)
	case errors.Is(err, ErrDecompressionBombSize), errors.Is(err, ErrDecompressionBombFile):
		return httputil.NewError(
			http.StatusBadRequest,
			"DECOMPRESSION_BOMB_DETECTED",
			"Decompression bomb detected",
			err,
		)
	case errors.Is(err, ErrVerificationFailed), errors.Is(err, ErrResultTypeMismatch):
		return apperr.Invalid("ALGO_VERIFICATION_FAILED", "Verification failed", err)
	case errors.Is(err, ErrInvalidManifestToken):
		return httputil.NewError(
			http.StatusBadRequest,
			"INVALID_MANIFEST_TOKEN",
			err.Error(),
			err,
		)
	case errors.Is(err, ErrVersionAlreadyInstalled):
		return apperr.Conflict("ALGORITHM_VERSION_EXISTS", "Algorithm version is already installed", err)
	case errors.Is(err, ErrInvalidConfigSchema):
		return apperr.Invalid("ALGO_VERIFICATION_FAILED", "Invalid algorithm configuration schema", err)
	default:
		return httputil.NewError(
			http.StatusBadRequest,
			"ALGO_INSTALL_FAILED",
			err.Error(),
			err,
		)
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
			return nil, httputil.NewError(
				http.StatusRequestEntityTooLarge,
				"FILE_TOO_LARGE",
				"Uploaded file exceeds 500MB",
				err,
			)
		}
		return nil, apperr.Internal(internalErrorCode, "Failed to save upload", err)
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

	algo, err := s.verifier.StageAndVerify(ctx, archivePath, s.rt.AlgorithmsDir)
	if err != nil {
		return nil, normalizeInstallError(fmt.Errorf("verify and stage algorithm: %w", err))
	}
	discardStagedVersion := func() {
		dir := s.rt.PackageDir(algo.ID, algo.Version)
		if removeErr := os.RemoveAll(dir); removeErr != nil {
			s.logger.Warn("failed to remove unregistered algorithm version",
				zap.String("path", dir),
				zap.Error(removeErr),
			)
		}
	}

	// Check if an algorithm with same name/ID already exists
	existing, err := s.store.GetAlgorithm(ctx, algo.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		discardStagedVersion()
		return nil, normalizeInstallError(fmt.Errorf("check existing algorithm: %w", err))
	}

	if existing != nil {
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
		// runnerLive 先判 nil：supervisor 是可缺席依赖（见 scheduleAlgorithmRestart）。
		runnerLive := s.supervisor != nil && s.supervisor.GetStatus(existing.ID) == StatusRunning
		previouslyEnabled := existing.Status == StatusActive
		if runnerLive || previouslyEnabled {
			upgradeErr := s.supervisor.UpgradeAlgorithm(
				ctx,
				algo.ToPackageMeta(),
				existing.ToPackageMeta(),
			)
			if upgradeErr != nil {
				s.logger.Warn("failed to transition runner process to upgraded version",
					zap.String("id", algo.ID),
					zap.Error(upgradeErr),
				)
			}
		}

		return algo, nil
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
		return nil, apperr.Internal(internalErrorCode, "Failed to list algorithms", err)
	}
	// Enrich with live supervisor status
	if s.supervisor != nil {
		for i := range algos {
			liveStatus := s.supervisor.GetStatus(algos[i].ID)
			if liveStatus != StatusDisabled {
				algos[i].Status = liveStatus
			}
		}
	}
	return algos, nil
}

// GetAlgorithm returns an algorithm by ID.
func (s *Service) GetAlgorithm(ctx context.Context, id string) (*Algorithm, error) {
	algo, err := s.loadAlgorithm(ctx, id)
	if err != nil {
		return nil, err
	}
	if s.supervisor != nil {
		liveStatus := s.supervisor.GetStatus(algo.ID)
		if liveStatus != StatusDisabled {
			algo.Status = liveStatus
		}
	}
	return algo, nil
}

// EnableAlgorithm starts the runner subprocess and updates DB status to ACTIVE.
func (s *Service) EnableAlgorithm(ctx context.Context, id string) error {
	algo, err := s.loadAlgorithm(ctx, id)
	if err != nil {
		return err
	}
	if s.supervisor == nil {
		return apperr.Internal(internalErrorCode, "Runner supervisor is not initialized", nil)
	}

	liveStatus := s.supervisor.GetStatus(id)
	if liveStatus == StatusCrashLoop {
		return apperr.Conflict(
			"ALGORITHM_IN_CRASH_LOOP",
			"Algorithm is in CRASH_LOOP, please reset it first",
			nil,
		)
	}
	if liveStatus == StatusRunning || liveStatus == StatusStarting {
		// 已经在运行中，幂等成功并确保 DB 为 ACTIVE
		s.markActiveBestEffort(ctx, algo)
		return nil
	}

	if err := s.supervisor.StartAlgorithm(ctx, algo.ToPackageMeta()); err != nil {
		return s.handleStartFailure(ctx, algo, err)
	}

	if err := s.store.UpdateAlgorithmStatus(ctx, id, StatusActive); err != nil {
		return apperr.Internal(internalErrorCode, "Failed to update algorithm status", err)
	}
	return nil
}

// handleStartFailure 把 StartAlgorithm 的失败分成三类：可降级为「待命态」的成功、
// 幂等成功、以及真正的错误。返回 nil 表示 HTTP 层应视为成功。
func (s *Service) handleStartFailure(ctx context.Context, algo *Algorithm, startErr error) error {
	switch {
	case errors.Is(startErr, algorunner.ErrNoBrokerSockets):
		// 无活跃任务：平滑进入待命态 (Standby)，写 ACTIVE 到 DB
		s.logger.Info("algorithm enabled in standby state (no active tasks)", zap.String("id", algo.ID))
		if err := s.store.UpdateAlgorithmStatus(ctx, algo.ID, StatusActive); err != nil {
			return apperr.Internal(internalErrorCode, "Failed to update algorithm status", err)
		}
		return nil
	case errors.Is(startErr, algorunner.ErrAlgorithmAlreadyRunning):
		// 并发注册竞争赢家已拉起，幂等成功
		s.markActiveBestEffort(ctx, algo)
		return nil
	case algorunner.IsEntryError(startErr):
		// 包目录里没有可执行的包根入口（旧 .so 形态，或归档被裁剪过）。
		// 明确报出来，且**不注册 watcher**：没有进程就没有退避重试，
		// 运维重新安装/重建后再次启用即可。
		s.logger.Warn("algorithm package has no runnable entry",
			zap.String("algorithm_id", algo.ID),
			zap.Error(startErr),
		)
		return apperr.Invalid(
			"ALGO_ENTRY_INVALID",
			"Algorithm package has no runnable executable, reinstall it",
			startErr,
		)
	default:
		return apperr.Internal(internalErrorCode, "Failed to start package supervisor", startErr)
	}
}

// DisableAlgorithm stops the runner subprocess and updates DB status to DISABLED.
func (s *Service) DisableAlgorithm(ctx context.Context, id string) error {
	if _, err := s.loadAlgorithm(ctx, id); err != nil {
		return err
	}
	if s.supervisor == nil {
		return apperr.Internal(internalErrorCode, "Runner supervisor is not initialized", nil)
	}

	if err := s.supervisor.StopAlgorithm(ctx, id); err != nil {
		return apperr.Internal(internalErrorCode, "Failed to stop runner supervisor", err)
	}
	if err := s.store.UpdateAlgorithmStatus(ctx, id, StatusDisabled); err != nil {
		return apperr.Internal(internalErrorCode, "Failed to update algorithm status", err)
	}
	return nil
}

// ResetAlgorithm resets an algorithm from CRASH_LOOP back to DISABLED so it can be re-enabled.
func (s *Service) ResetAlgorithm(ctx context.Context, id string) error {
	if s.supervisor == nil {
		return apperr.Internal(internalErrorCode, "Runner supervisor is not initialized", nil)
	}
	if err := s.supervisor.ResetAlgorithm(ctx, id); err != nil {
		if errors.Is(err, algorunner.ErrAlgorithmNotInCrashLoop) {
			return apperr.Conflict("ALGORITHM_NOT_IN_CRASH_LOOP", "Algorithm is not in CRASH_LOOP", err)
		}
		return apperr.Internal(internalErrorCode, "Failed to reset algorithm", err)
	}
	return nil
}

// DeleteAlgorithm stops and uninstalls an algorithm from disk and database.
func (s *Service) DeleteAlgorithm(ctx context.Context, id string) error {
	algo, err := s.loadAlgorithm(ctx, id)
	if err != nil {
		return err
	}

	if s.supervisor != nil {
		status := s.supervisor.GetStatus(id)
		switch status {
		case StatusRunning, StatusStarting, StatusRollingBack:
			return apperr.Conflict("ALGORITHM_IS_ACTIVE", "Algorithm is active", ErrAlgorithmActive)
		}
		if err := s.supervisor.StopAlgorithm(ctx, id); err != nil {
			return apperr.Internal(internalErrorCode, "Failed to stop algorithm before deletion", err)
		}
	}

	// Delete associated analysis tasks
	if err := s.store.DeleteTasksByAlgorithm(ctx, algo.ID); err != nil {
		s.logger.Warn("failed to delete associated tasks for algorithm",
			zap.String("id", algo.ID),
			zap.Error(err),
		)
	}

	// Remove files
	algoBaseDir := s.rt.IDDir(algo.ID)
	if err := os.RemoveAll(algoBaseDir); err != nil && !os.IsNotExist(err) {
		s.logger.Warn("failed to remove algorithm directory on disk",
			zap.String("path", algoBaseDir),
			zap.Error(err),
		)
	}

	if err := s.store.DeleteAlgorithm(ctx, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return apperr.NotFound("ALGORITHM_NOT_FOUND", "Algorithm not found", err)
		}
		return apperr.Internal(internalErrorCode, "Failed to delete algorithm from database", err)
	}
	return nil
}

// OnTaskChanged implements task.TaskChangeListener.
func (s *Service) OnTaskChanged(ctx context.Context, algorithmID string) {
	s.scheduleAlgorithmRestart(algorithmID)
}

// GetAlgorithmSpec implements task.AlgorithmSpecProvider.
func (s *Service) GetAlgorithmSpec(ctx context.Context, idOrName string) (*task.AlgorithmSpec, error) {
	algo, err := s.store.GetAlgorithm(ctx, idOrName)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		algoByName, errName := s.store.GetAlgorithmByName(ctx, idOrName)
		if errName != nil {
			return nil, errName
		}
		algo = algoByName
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
	switch liveStatus {
	case StatusRunning, StatusStarting:
		// 运行中：执行 RestartAlgorithm（若任务变为空，
		// Supervisor.RestartAlgorithm 会优雅停止并恢复 ACTIVE）
		if err := s.supervisor.RestartAlgorithm(context.Background(), algorithmID); err != nil {
			s.logger.Warn("failed to restart algorithm after task change",
				zap.String("algorithm_id", algorithmID),
				zap.Error(err),
			)
		}
	case StatusDisabled:
		// 处于待命态（Standby）：新任务加入，尝试从待命态拉起 Runner
		if err := s.supervisor.StartAlgorithm(context.Background(), algo.ToPackageMeta()); err != nil {
			switch {
			case algorunner.IsEntryError(err):
				s.logger.Error("standby algorithm has no runnable entry, leaving it disabled",
					zap.String("algorithm_id", algorithmID),
					zap.Error(err),
				)
			case errors.Is(err, algorunner.ErrNoBrokerSockets),
				errors.Is(err, algorunner.ErrAlgorithmAlreadyRunning):
				// 待命或并发拉起已成功，无需警告
			default:
				s.logger.Warn("failed to start standby algorithm after task change",
					zap.String("algorithm_id", algorithmID),
					zap.Error(err),
				)
			}
		}
	}
}

// CancelPendingRestarts cancels all pending debounce restart timers.
func (s *Service) CancelPendingRestarts() {
	s.restartMu.Lock()
	defer s.restartMu.Unlock()

	for id, timer := range s.restartTimers {
		if timer != nil {
			timer.Stop()
		}
		delete(s.restartTimers, id)
	}
	s.restartPending = make(map[string]struct{})
}

// StopAll stops all active subprocesses and cancels pending restart timers.
func (s *Service) StopAll(timeout time.Duration) {
	s.CancelPendingRestarts()
	if s.supervisor != nil {
		s.supervisor.StopAll(timeout)
	}
}

// TrialInference runs a one-shot inference on an uploaded static image against an installed, disabled algorithm.
func (s *Service) TrialInference(
	ctx context.Context,
	algorithmID string,
	r io.Reader,
) (*AlgorithmTrialResponse, error) {
	algo, err := s.loadAlgorithm(ctx, algorithmID)
	if err != nil {
		return nil, err
	}

	switch algo.Status {
	case StatusDisabled:
		// allowed
	case StatusActive:
		return nil, apperr.Conflict(
			"ALGORITHM_IS_ACTIVE",
			"Algorithm is active; please disable it before running trial",
			nil,
		)
	case StatusCrashLoop:
		return nil, apperr.Conflict(
			"ALGORITHM_IN_CRASH_LOOP",
			"Algorithm is in crash loop; please reset breaker before running trial",
			nil,
		)
	default:
		return nil, apperr.Conflict(
			"ALGORITHM_IS_ACTIVE",
			fmt.Sprintf("Algorithm status %q is not allowed for trial inference", algo.Status),
			nil,
		)
	}

	// Decode and convert image to temp PPM file
	trialImg, err := writeTrialImagePPM(r)
	if err != nil {
		switch {
		case errors.Is(err, ErrImageDimensionsTooLarge):
			return nil, apperr.Invalid("IMAGE_DIMENSIONS_TOO_LARGE", err.Error(), err)
		case errors.Is(err, ErrInvalidImageFile):
			return nil, apperr.Invalid("INVALID_IMAGE_FILE", err.Error(), err)
		default:
			return nil, apperr.Internal(internalErrorCode, "Failed to prepare image for trial", err)
		}
	}
	defer trialImg.Close()

	pkgDir := s.rt.PackageDir(algo.ID, algo.Version)
	entryPath, err := algorunner.ResolveEntry(pkgDir, algo.ID)
	if err != nil {
		return nil, apperr.Internal("ALGO_ENTRY_INVALID", "Algorithm entry executable not found", err)
	}

	timeout := 15 * time.Second
	tCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(
		tCtx,
		entryPath,
		"--verify",
		"--sample",
		trialImg.Path,
	)
	cmd.Dir = pkgDir
	cmd.SysProcAttr = algorunner.PackageSysProcAttr()
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	if err := cmd.Run(); err != nil {
		if errors.Is(tCtx.Err(), context.DeadlineExceeded) {
			return nil, httputil.NewError(
				http.StatusGatewayTimeout,
				"TRIAL_TIMEOUT",
				"Trial inference execution timed out",
				err,
			)
		}
		return nil, apperr.Internal(
			"TRIAL_EXECUTION_FAILED",
			fmt.Sprintf("Trial execution failed: %v (%s)", err, stderrBuf.String()),
			err,
		)
	}

	report, err := parseVerificationReport(stdoutBuf.String())
	if err != nil {
		return nil, apperr.Internal("TRIAL_EXECUTION_FAILED", err.Error(), err)
	}

	if report.Status != "passed" {
		return nil, apperr.Internal(
			"TRIAL_EXECUTION_FAILED",
			fmt.Sprintf("Algorithm trial reported status %q: %s", report.Status, report.Error),
			nil,
		)
	}
	if !report.SampleDecoded {
		return nil, apperr.Internal(
			"TRIAL_EXECUTION_FAILED",
			"Algorithm failed to decode trial image and fell back to synthetic frame",
			nil,
		)
	}

	resp := &AlgorithmTrialResponse{
		CostMs:          report.CostMs,
		Width:           trialImg.Width,
		Height:          trialImg.Height,
		TaskType:        algo.TaskType,
		Count:           report.Count,
		Detections:      []aitypes.DetectionItem{},
		Classifications: []aitypes.ClassificationItem{},
		Persons:         []aitypes.PersonItem{},
		Vehicles:        []aitypes.VehicleItem{},
	}

	if report.Result == nil {
		return resp, nil
	}

	if report.Result.TaskType != "" {
		resp.TaskType = report.Result.TaskType
	}
	if len(report.Result.Detections) > 0 {
		resp.Detections = report.Result.Detections
	}
	if len(report.Result.Classifications) > 0 {
		resp.Classifications = report.Result.Classifications
	}
	if len(report.Result.Persons) > 0 {
		resp.Persons = report.Result.Persons
	}
	if len(report.Result.Vehicles) > 0 {
		resp.Vehicles = report.Result.Vehicles
	}

	return resp, nil
}
