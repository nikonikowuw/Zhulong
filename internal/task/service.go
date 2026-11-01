package task

import (
	"context"
	"errors"
	"fmt"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/region"
	"github.com/nikonikowuw/Zhulong/internal/schedule"
	"go.uber.org/zap"
)

// MaxComputePoints is the total compute points allocated across all active tasks.
const MaxComputePoints int64 = 1000

// ErrTaskAlgorithmRequired 表示本次写入会让任务不绑定任何算法。
//
// 通道聚合根的意义由算法子项承载，零子项的主表行是「看不见却存在」的脏数据：
// 它不产生明细行、不占算力，却会让统计看板与通道卡片按「有任务」渲染。
// 清空整个通道必须走 DELETE，而不是把算法列表置空。
var ErrTaskAlgorithmRequired = errors.New("analysis task must bind at least one algorithm")

// AlgorithmSpec contains read-only algorithm metadata needed for task validation.
type AlgorithmSpec struct {
	ID               string
	Name             string
	ConfigSchemaJSON string
	ComputeCost      int
	Status           string
}

// AlgorithmSpecProvider resolves algorithm metadata for task operations.
type AlgorithmSpecProvider interface {
	GetAlgorithmSpec(ctx context.Context, idOrName string) (*AlgorithmSpec, error)
}

// TaskChangeListener receives notifications when tasks are modified, created, or deleted.
type TaskChangeListener interface {
	OnTaskChanged(ctx context.Context, algorithmID string)
}

// Service manages the business lifecycle of video analysis tasks.
type Service struct {
	store        *Store
	algoProvider AlgorithmSpecProvider
	listeners    []TaskChangeListener
	logger       *zap.Logger
}

// NewService instantiates a new Task Service.
// Every listener is notified on task changes; nil entries are ignored.
func NewService(
	store *Store,
	algoProvider AlgorithmSpecProvider,
	listeners []TaskChangeListener,
	logger *zap.Logger,
) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Service{
		store:        store,
		algoProvider: algoProvider,
		listeners:    listeners,
		logger:       logger,
	}
}

// notifyTaskChanged 通知单个算法；空 ID 已在 notifyTasksChanged 里过滤。
func (s *Service) notifyTaskChanged(ctx context.Context, algorithmID string) {
	for _, listener := range s.listeners {
		if listener != nil {
			listener.OnTaskChanged(ctx, algorithmID)
		}
	}
}

// notifyTasksChanged 广播算法变更，空 ID 与重复 ID 都会被过滤。
func (s *Service) notifyTasksChanged(ctx context.Context, algorithmIDs []string) {
	seen := make(map[string]struct{}, len(algorithmIDs))
	for _, id := range algorithmIDs {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		s.notifyTaskChanged(ctx, id)
	}
}

// prepareTask 校验必填字段，并就地归一化 + 校验算法子项。
//
// 任务主键不在这里补全：upsert 需要先按 camera_id 命中既有行，由 Store 在事务内决定沿用还是新建。
func (s *Service) prepareTask(ctx context.Context, t *Task) error {
	if t == nil {
		return apperr.Invalid("INVALID_TASK_CONFIG", "task payload is nil", nil)
	}
	if t.CameraID == "" {
		return apperr.Invalid("VALIDATION_FAILED", "cameraId is required", nil)
	}
	return s.normalizeAndValidateAlgorithms(ctx, t.Algorithms)
}

// normalizeAndValidateAlgorithms 就地归一化算法子项（默认帧率/空 JSON、名字换成规格 ID）
// 并校验唯一性、帧率、区域策略与参数 schema。
//
// 空列表是**非法配置**而不是“清空”：保存时先删子项、再无人回填，会留下一行
// algorithms 为空的主表记录（存量脏数据就是这么来的）。清空通道请调用 DeleteTask。
func (s *Service) normalizeAndValidateAlgorithms(ctx context.Context, algorithms []TaskAlgorithm) error {
	if len(algorithms) == 0 {
		return apperr.Invalid(
			"TASK_ALGORITHM_REQUIRED",
			"analysis task must bind at least one algorithm; delete the task to clear the channel",
			ErrTaskAlgorithmRequired,
		)
	}

	seen := make(map[string]struct{}, len(algorithms))
	for i := range algorithms {
		sub := &algorithms[i]
		if sub.AlgorithmID == "" {
			return apperr.Invalid(
				"VALIDATION_FAILED",
				"algorithmId is required for task algorithm item",
				nil,
			)
		}
		if _, ok := seen[sub.AlgorithmID]; ok {
			return apperr.Conflict(
				"DUPLICATE_ALGORITHM",
				fmt.Sprintf("duplicate algorithm %q in task", sub.AlgorithmID),
				nil,
			)
		}
		seen[sub.AlgorithmID] = struct{}{}

		if sub.SampleFPS == 0 {
			sub.SampleFPS = DefaultSampleFPS
		}
		if _, err := ValidateSampleFPS(sub.SampleFPS); err != nil {
			return err
		}

		if sub.ConfigJSON == "" {
			sub.ConfigJSON = defaultConfigJSON
		}
		if sub.RegionConfigJSON == "" {
			sub.RegionConfigJSON = defaultConfigJSON
		}
		if sub.ScheduleJSON == "" {
			sub.ScheduleJSON = defaultConfigJSON
		}

		if _, err := region.ParseConfig(sub.RegionConfigJSON); err != nil {
			return apperr.Invalid(
				"INVALID_REGION_CONFIG",
				fmt.Sprintf("invalid region config for %q: %v", sub.AlgorithmID, err),
				err,
			)
		}

		if _, err := schedule.Parse(sub.ScheduleJSON); err != nil {
			return apperr.Invalid(
				"INVALID_SCHEDULE_CONFIG",
				fmt.Sprintf("invalid schedule config for %q: %v", sub.AlgorithmID, err),
				ErrInvalidScheduleConfig,
			)
		}

		if err := s.validateAlgorithmSpec(ctx, sub); err != nil {
			return err
		}
	}
	return nil
}

// validateAlgorithmSpec 用算法包元数据校正算法 ID，并校验参数 JSON 是否匹配参数规范。
func (s *Service) validateAlgorithmSpec(ctx context.Context, sub *TaskAlgorithm) error {
	if s.algoProvider == nil {
		return nil
	}
	spec, err := s.algoProvider.GetAlgorithmSpec(ctx, sub.AlgorithmID)
	if err != nil {
		return apperr.NotFound(
			"ALGORITHM_NOT_FOUND",
			fmt.Sprintf("algorithm %q not found", sub.AlgorithmID),
			err,
		)
	}
	sub.AlgorithmID = spec.ID
	return validateTaskConfig(spec.ConfigSchemaJSON, sub.ConfigJSON)
}

// ensureCameraIsFree 在建任务前给出 409 的快速失败；并发冲突由 Store 的唯一约束兜底。
func (s *Service) ensureCameraIsFree(ctx context.Context, cameraID string) error {
	existing, err := s.store.GetTaskByCamera(ctx, cameraID)
	switch {
	case err == nil && existing != nil:
		return apperr.Conflict("TASK_ALREADY_EXISTS", "task already exists for camera", nil)
	case err != nil && !errors.Is(err, ErrNotFound):
		return apperr.Internal("INTERNAL_ERROR", "failed to check existing task", err)
	}
	return nil
}

// mapTaskError 把 Store 的查询错误映射成对外的 404 / 500。
func mapTaskError(err error) error {
	if errors.Is(err, ErrNotFound) {
		return apperr.NotFound("TASK_NOT_FOUND", "analysis task not found", err)
	}
	return apperr.Internal("INTERNAL_ERROR", "failed to query task", err)
}

// mapWriteError 把 Store 的写入错误映射成对外错误；fallbackCode/fallbackMessage 用于兜底 500。
func mapWriteError(err error, fallbackCode, fallbackMessage string) error {
	switch {
	case errors.Is(err, ErrComputeBudgetExceeded):
		return apperr.Conflict("COMPUTE_BUDGET_EXCEEDED", "total compute budget exceeded", err)
	case errors.Is(err, ErrTaskAlreadyExists):
		return apperr.Conflict("TASK_ALREADY_EXISTS", "task already exists for camera", err)
	case errors.Is(err, ErrNotFound):
		return apperr.NotFound("ALGORITHM_NOT_FOUND", "referenced algorithm not found", err)
	}
	return apperr.Internal(fallbackCode, fallbackMessage, err)
}

// loadTask 读取任务并把 Store 错误映射成对外错误。
func (s *Service) loadTask(ctx context.Context, id string) (*Task, error) {
	task, err := s.store.GetTask(ctx, id)
	if err != nil {
		return nil, mapTaskError(err)
	}
	return task, nil
}

// CreateTask registers a new analysis task with budget and schema validation.
func (s *Service) CreateTask(ctx context.Context, t *Task) error {
	if err := s.prepareTask(ctx, t); err != nil {
		return err
	}
	if err := s.ensureCameraIsFree(ctx, t.CameraID); err != nil {
		return err
	}
	if err := s.store.CreateTaskWithinBudget(ctx, t, MaxComputePoints); err != nil {
		return mapWriteError(err, "TASK_CREATE_FAILED", "failed to persist analysis task")
	}

	s.logger.Info("analysis task created",
		zap.String("id", t.ID),
		zap.String("camera_id", t.CameraID),
		zap.Int("algorithm_count", len(t.Algorithms)),
	)

	s.notifyTasksChanged(ctx, collectAlgorithmIDs(t))
	return nil
}

// UpdateTask applies changes to an existing analysis task and checks budget.
func (s *Service) UpdateTask(ctx context.Context, id string, req *UpdateTaskRequest) (*Task, error) {
	if id == "" {
		return nil, apperr.Invalid("VALIDATION_FAILED", "task id is required", nil)
	}
	if req == nil {
		return nil, apperr.Invalid("INVALID_REQUEST", "update payload is nil", nil)
	}

	task, err := s.loadTask(ctx, id)
	if err != nil {
		return nil, err
	}

	previousIDs := collectAlgorithmIDs(task)
	req.ApplyTo(task)

	if err := s.normalizeAndValidateAlgorithms(ctx, task.Algorithms); err != nil {
		return nil, err
	}
	if err := s.store.SaveTaskWithinBudget(ctx, task, MaxComputePoints); err != nil {
		return nil, mapWriteError(err, "TASK_UPDATE_FAILED", "failed to update task")
	}

	s.logger.Info("analysis task updated",
		zap.String("id", task.ID),
		zap.Bool("enabled", task.Enabled),
		zap.Int("algorithm_count", len(task.Algorithms)),
	)

	s.notifyTasksChanged(ctx, append(previousIDs, collectAlgorithmIDs(task)...))
	return task, nil
}

// SaveTask atomically creates or updates a channel task and its algorithms within budget.
func (s *Service) SaveTask(ctx context.Context, t *Task) error {
	if err := s.prepareTask(ctx, t); err != nil {
		return err
	}

	var previousIDs []string
	if existing, err := s.store.GetTaskByCamera(ctx, t.CameraID); err == nil && existing != nil {
		previousIDs = collectAlgorithmIDs(existing)
	}

	if err := s.store.SaveTaskWithinBudget(ctx, t, MaxComputePoints); err != nil {
		return mapWriteError(err, "TASK_SAVE_FAILED", "failed to save analysis task")
	}

	s.notifyTasksChanged(ctx, append(previousIDs, collectAlgorithmIDs(t)...))
	return nil
}

// DeleteTask removes an analysis task and notifies downstream listeners.
func (s *Service) DeleteTask(ctx context.Context, id string) error {
	if id == "" {
		return apperr.Invalid("VALIDATION_FAILED", "task id is required", nil)
	}
	task, err := s.loadTask(ctx, id)
	if err != nil {
		return err
	}

	if err := s.store.DeleteTask(ctx, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return apperr.NotFound("TASK_NOT_FOUND", "analysis task not found", err)
		}
		return apperr.Internal("TASK_DELETE_FAILED", "failed to delete task", err)
	}

	s.logger.Info("analysis task deleted",
		zap.String("id", id),
		zap.Int("algorithms", len(task.Algorithms)),
	)

	s.notifyTasksChanged(ctx, collectAlgorithmIDs(task))
	return nil
}

// GetTask returns a task by its ID.
func (s *Service) GetTask(ctx context.Context, id string) (*Task, error) {
	return s.loadTask(ctx, id)
}

// GetTaskByCamera returns a task by its camera ID.
func (s *Service) GetTaskByCamera(ctx context.Context, cameraID string) (*Task, error) {
	task, err := s.store.GetTaskByCamera(ctx, cameraID)
	if err != nil {
		return nil, mapTaskError(err)
	}
	return task, nil
}

// ListTasks returns tasks matching optional camera or algorithm filters.
// cameraID 优先；两个过滤条件同时给出时 algorithmID 被忽略（单摄像头单任务的模型下没有交集语义）。
func (s *Service) ListTasks(ctx context.Context, cameraID, algorithmID string) ([]Task, error) {
	switch {
	case cameraID != "":
		return s.store.ListTasksByCamera(ctx, cameraID)
	case algorithmID != "":
		return s.store.ListTasksByAlgorithm(ctx, algorithmID)
	}
	return s.store.ListTasks(ctx)
}

// collectAlgorithmIDs 汇总给定任务下的所有算法 ID。
func collectAlgorithmIDs(tasks ...*Task) []string {
	ids := make([]string, 0, len(tasks))
	for _, t := range tasks {
		for _, sub := range t.Algorithms {
			ids = append(ids, sub.AlgorithmID)
		}
	}
	return ids
}
