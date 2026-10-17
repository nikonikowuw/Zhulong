package task

import (
	"context"
	"errors"
	"fmt"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/idutil"
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
	listener     TaskChangeListener
	logger       *zap.Logger
}

// NewService instantiates a new Task Service.
func NewService(
	store *Store,
	algoProvider AlgorithmSpecProvider,
	listener TaskChangeListener,
	logger *zap.Logger,
) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Service{
		store:        store,
		algoProvider: algoProvider,
		listener:     listener,
		logger:       logger,
	}
}

func (s *Service) notifyTaskChanged(ctx context.Context, algorithmID string) {
	if s.listener != nil && algorithmID != "" {
		s.listener.OnTaskChanged(ctx, algorithmID)
	}
}

// CreateTask registers a new analysis task with budget and schema validation.
func (s *Service) CreateTask(ctx context.Context, t *Task) error {
	if t == nil {
		return apperr.Invalid("INVALID_TASK_CONFIG", "task payload is nil", nil)
	}
	if t.CameraID == "" {
		return apperr.Invalid("VALIDATION_FAILED", "cameraId is required", nil)
	}
	if t.AlgorithmID == "" {
		return apperr.Invalid("VALIDATION_FAILED", "algorithmId is required", nil)
	}
	if t.ID == "" {
		t.ID = idutil.New()
	}
	if t.SampleFPS <= 0 {
		t.SampleFPS = DefaultSampleFPS
	}
	if _, err := ValidateSampleFPS(t.SampleFPS); err != nil {
		return err
	}
	if t.ConfigJSON == "" {
		t.ConfigJSON = "{}"
	}

	// Validate algorithm existence and schema
	if s.algoProvider != nil {
		algo, err := s.algoProvider.GetAlgorithmSpec(ctx, t.AlgorithmID)
		if err != nil {
			return apperr.NotFound("ALGORITHM_NOT_FOUND", fmt.Sprintf("algorithm %q not found", t.AlgorithmID), err)
		}
		t.AlgorithmID = algo.ID

		if err := validateTaskConfig(algo.ConfigSchemaJSON, t.ConfigJSON); err != nil {
			return err
		}
	}

	// Guard against duplicate task for camera and algorithm
	if existing, err := s.store.GetTaskByCameraAndAlgorithm(ctx, t.CameraID, t.AlgorithmID); err == nil && existing != nil {
		return apperr.Conflict("TASK_ALREADY_EXISTS", "task already exists for camera and algorithm", nil)
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
		zap.String("algorithm_id", t.AlgorithmID),
		zap.Int("sample_fps", t.SampleFPS),
	)

	s.notifyTaskChanged(ctx, t.AlgorithmID)
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

	req.ApplyTo(task)

	if _, err := ValidateSampleFPS(task.SampleFPS); err != nil {
		return nil, err
	}

	if s.algoProvider != nil {
		algo, err := s.algoProvider.GetAlgorithmSpec(ctx, task.AlgorithmID)
		if err != nil {
			return nil, apperr.NotFound("ALGORITHM_NOT_FOUND", fmt.Sprintf("algorithm %q not found", task.AlgorithmID), err)
		}
		if err := validateTaskConfig(algo.ConfigSchemaJSON, task.ConfigJSON); err != nil {
			return nil, err
		}
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
		zap.Int("sample_fps", task.SampleFPS),
	)

	s.notifyTaskChanged(ctx, task.AlgorithmID)
	return task, nil
}

// DeleteTask removes an analysis task and notifies the downstream listener.
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

	s.logger.Info("analysis task deleted", zap.String("id", id), zap.String("algorithm_id", task.AlgorithmID))
	s.notifyTaskChanged(ctx, task.AlgorithmID)
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

// DeleteTasksByCamera cleans up tasks when a camera is removed.
func (s *Service) DeleteTasksByCamera(ctx context.Context, cameraID string) error {
	tasks, err := s.store.ListTasksByCamera(ctx, cameraID)
	if err != nil {
		return err
	}
	if err := s.store.DeleteTasksByCamera(ctx, cameraID); err != nil {
		return err
	}
	visited := make(map[string]struct{})
	for _, t := range tasks {
		if _, ok := visited[t.AlgorithmID]; !ok {
			visited[t.AlgorithmID] = struct{}{}
			s.notifyTaskChanged(ctx, t.AlgorithmID)
		}
	}
	return nil
}

// DeleteTasksByAlgorithm cleans up tasks when an algorithm is uninstalled.
func (s *Service) DeleteTasksByAlgorithm(ctx context.Context, algorithmID string) error {
	if err := s.store.DeleteTasksByAlgorithm(ctx, algorithmID); err != nil {
		return err
	}
	s.notifyTaskChanged(ctx, algorithmID)
	return nil
}
