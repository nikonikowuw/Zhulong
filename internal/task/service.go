package task

import (
	"context"
	"errors"
	"fmt"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/idutil"
	"github.com/nikonikowuw/Zhulong/internal/region"
	"go.uber.org/zap"
)

// MaxComputePoints is the total compute points allocated across all active tasks.
const MaxComputePoints int64 = 1000

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

func (s *Service) notifyTaskChanged(ctx context.Context, algorithmID string) {
	if algorithmID == "" {
		return
	}
	for _, listener := range s.listeners {
		if listener != nil {
			listener.OnTaskChanged(ctx, algorithmID)
		}
	}
}

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

func (s *Service) validateAlgorithms(ctx context.Context, algorithms []TaskAlgorithm) error {
	seen := make(map[string]struct{}, len(algorithms))
	for i := range algorithms {
		sub := &algorithms[i]
		if sub.AlgorithmID == "" {
			return apperr.Invalid("VALIDATION_FAILED", "algorithmId is required for task algorithm item", nil)
		}
		if _, ok := seen[sub.AlgorithmID]; ok {
			return apperr.Conflict("DUPLICATE_ALGORITHM", fmt.Sprintf("duplicate algorithm %q in task", sub.AlgorithmID), nil)
		}
		seen[sub.AlgorithmID] = struct{}{}

		if sub.SampleFPS <= 0 {
			sub.SampleFPS = DefaultSampleFPS
		}
		if _, err := ValidateSampleFPS(sub.SampleFPS); err != nil {
			return err
		}

		if sub.ConfigJSON == "" {
			sub.ConfigJSON = "{}"
		}
		if sub.RegionConfigJSON == "" {
			sub.RegionConfigJSON = "{}"
		}

		if _, err := region.ParseConfig(sub.RegionConfigJSON); err != nil {
			return apperr.Invalid("INVALID_REGION_CONFIG", fmt.Sprintf("invalid region config for %q: %v", sub.AlgorithmID, err), err)
		}

		if s.algoProvider != nil {
			spec, err := s.algoProvider.GetAlgorithmSpec(ctx, sub.AlgorithmID)
			if err != nil {
				return apperr.NotFound("ALGORITHM_NOT_FOUND", fmt.Sprintf("algorithm %q not found", sub.AlgorithmID), err)
			}
			sub.AlgorithmID = spec.ID

			if err := validateTaskConfig(spec.ConfigSchemaJSON, sub.ConfigJSON); err != nil {
				return err
			}
		}
	}
	return nil
}

// CreateTask registers a new analysis task with budget and schema validation.
func (s *Service) CreateTask(ctx context.Context, t *Task) error {
	if t == nil {
		return apperr.Invalid("INVALID_TASK_CONFIG", "task payload is nil", nil)
	}
	if t.CameraID == "" {
		return apperr.Invalid("VALIDATION_FAILED", "cameraId is required", nil)
	}
	if t.ID == "" {
		t.ID = idutil.New()
	}

	if err := s.validateAlgorithms(ctx, t.Algorithms); err != nil {
		return err
	}

	// Guard against duplicate task for camera (1 Camera : 1 Task)
	if existing, err := s.store.GetTaskByCamera(ctx, t.CameraID); err == nil && existing != nil {
		return apperr.Conflict("TASK_ALREADY_EXISTS", "task already exists for camera", nil)
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return apperr.Internal("INTERNAL_ERROR", "failed to check existing task", err)
	}

	if err := s.store.CreateTaskWithinBudget(ctx, t, MaxComputePoints); err != nil {
		if errors.Is(err, ErrComputeBudgetExceeded) {
			return apperr.Conflict("COMPUTE_BUDGET_EXCEEDED", "total compute budget exceeded", err)
		}
		if errors.Is(err, ErrNotFound) {
			return apperr.NotFound("ALGORITHM_NOT_FOUND", "referenced algorithm not found", err)
		}
		return apperr.Internal("TASK_CREATE_FAILED", "failed to persist analysis task", err)
	}

	s.logger.Info("analysis task created",
		zap.String("id", t.ID),
		zap.String("camera_id", t.CameraID),
		zap.Int("algorithm_count", len(t.Algorithms)),
	)

	var algoIDs []string
	for _, sub := range t.Algorithms {
		algoIDs = append(algoIDs, sub.AlgorithmID)
	}
	s.notifyTasksChanged(ctx, algoIDs)
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

	task, err := s.store.GetTask(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, apperr.NotFound("TASK_NOT_FOUND", "analysis task not found", err)
		}
		return nil, apperr.Internal("INTERNAL_ERROR", "failed to query task", err)
	}

	var oldAlgoIDs []string
	for _, sub := range task.Algorithms {
		oldAlgoIDs = append(oldAlgoIDs, sub.AlgorithmID)
	}

	req.ApplyTo(task)

	if err := s.validateAlgorithms(ctx, task.Algorithms); err != nil {
		return nil, err
	}

	if err := s.store.UpdateTaskWithinBudget(ctx, task, MaxComputePoints); err != nil {
		if errors.Is(err, ErrComputeBudgetExceeded) {
			return nil, apperr.Conflict("COMPUTE_BUDGET_EXCEEDED", "total compute budget exceeded", err)
		}
		return nil, apperr.Internal("TASK_UPDATE_FAILED", "failed to update task", err)
	}

	s.logger.Info("analysis task updated",
		zap.String("id", task.ID),
		zap.Bool("enabled", task.Enabled),
		zap.Int("algorithm_count", len(task.Algorithms)),
	)

	allAlgoIDs := append(oldAlgoIDs, "")
	for _, sub := range task.Algorithms {
		allAlgoIDs = append(allAlgoIDs, sub.AlgorithmID)
	}
	s.notifyTasksChanged(ctx, allAlgoIDs)
	return task, nil
}

// SaveTask atomically creates or updates a channel task and its algorithms within budget.
func (s *Service) SaveTask(ctx context.Context, t *Task) error {
	if t == nil {
		return apperr.Invalid("INVALID_TASK_CONFIG", "task payload is nil", nil)
	}
	if t.CameraID == "" {
		return apperr.Invalid("VALIDATION_FAILED", "cameraId is required", nil)
	}
	if t.ID == "" {
		t.ID = idutil.New()
	}

	if err := s.validateAlgorithms(ctx, t.Algorithms); err != nil {
		return err
	}

	var oldAlgoIDs []string
	if existing, err := s.store.GetTaskByCamera(ctx, t.CameraID); err == nil && existing != nil {
		for _, sub := range existing.Algorithms {
			oldAlgoIDs = append(oldAlgoIDs, sub.AlgorithmID)
		}
	}

	if err := s.store.SaveTaskWithinBudget(ctx, t, MaxComputePoints); err != nil {
		if errors.Is(err, ErrComputeBudgetExceeded) {
			return apperr.Conflict("COMPUTE_BUDGET_EXCEEDED", "total compute budget exceeded", err)
		}
		if errors.Is(err, ErrNotFound) {
			return apperr.NotFound("ALGORITHM_NOT_FOUND", "referenced algorithm not found", err)
		}
		return apperr.Internal("TASK_SAVE_FAILED", "failed to save analysis task", err)
	}

	allAlgoIDs := append(oldAlgoIDs, "")
	for _, sub := range t.Algorithms {
		allAlgoIDs = append(allAlgoIDs, sub.AlgorithmID)
	}
	s.notifyTasksChanged(ctx, allAlgoIDs)
	return nil
}

// DeleteTask removes an analysis task and notifies downstream listeners.
func (s *Service) DeleteTask(ctx context.Context, id string) error {
	task, err := s.store.GetTask(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return apperr.NotFound("TASK_NOT_FOUND", "analysis task not found", err)
		}
		return apperr.Internal("INTERNAL_ERROR", "failed to query task", err)
	}

	if err := s.store.DeleteTask(ctx, id); err != nil {
		return apperr.Internal("TASK_DELETE_FAILED", "failed to delete task", err)
	}

	s.logger.Info("analysis task deleted", zap.String("id", id), zap.Int("algorithms", len(task.Algorithms)))

	var algoIDs []string
	for _, sub := range task.Algorithms {
		algoIDs = append(algoIDs, sub.AlgorithmID)
	}
	s.notifyTasksChanged(ctx, algoIDs)
	return nil
}

// GetTask returns a task by its ID.
func (s *Service) GetTask(ctx context.Context, id string) (*Task, error) {
	task, err := s.store.GetTask(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, apperr.NotFound("TASK_NOT_FOUND", "analysis task not found", err)
		}
		return nil, apperr.Internal("INTERNAL_ERROR", "failed to query task", err)
	}
	return task, nil
}

// GetTaskByCamera returns a task by its camera ID.
func (s *Service) GetTaskByCamera(ctx context.Context, cameraID string) (*Task, error) {
	task, err := s.store.GetTaskByCamera(ctx, cameraID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, apperr.NotFound("TASK_NOT_FOUND", "analysis task not found", err)
		}
		return nil, apperr.Internal("INTERNAL_ERROR", "failed to query task", err)
	}
	return task, nil
}

// ListTasks returns tasks matching optional camera or algorithm filters.
func (s *Service) ListTasks(ctx context.Context, cameraID, algorithmID string) ([]Task, error) {
	if cameraID != "" {
		return s.store.ListTasksByCamera(ctx, cameraID)
	}
	if algorithmID != "" {
		return s.store.ListTasksByAlgorithm(ctx, algorithmID)
	}
	return s.store.ListTasks(ctx)
}
