package task

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

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

// TaskSavedListener 按**任务/相机**粒度接收保存通知，与按算法的 TaskChangeListener 正交。
//
// 存在的理由：只改 crop 时不得重启算法子进程，但仍要把新视图应用给正在跑的 AI 流。
// 回调发生在写库成功之后；实现方必须自行脱钩请求 ctx（见 notifyTaskSaved）。
type TaskSavedListener interface {
	OnTaskSaved(ctx context.Context, taskID, cameraID string)
}

// Service manages the business lifecycle of video analysis tasks.
type Service struct {
	store        *Store
	algoProvider AlgorithmSpecProvider
	listeners    []TaskChangeListener
	logger       *zap.Logger

	// savedMu 保护 savedListener：它与构造期注入的 listeners 不同，是可运行时替换的
	// 单槽位（与 camera.StreamHub.SetTaskCropProvider 同一口径）。不加锁就是一条
	// 只有真并发调用才会暴露的 data race，而 -race 不会替我们捉住想象中的调用者。
	savedMu       sync.RWMutex
	savedListener TaskSavedListener
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

// SetTaskSavedListener 注册按任务粒度的保存通知；传 nil 可取消。
func (s *Service) SetTaskSavedListener(listener TaskSavedListener) {
	s.savedMu.Lock()
	defer s.savedMu.Unlock()
	s.savedListener = listener
}

// notifyTaskSaved 在写库成功后广播保存事件。
//
// ⚠️ 必须脱钩请求 ctx：写库已经提交，客户端断连导致的取消不应让“应用新 crop”静默失败。
func (s *Service) notifyTaskSaved(ctx context.Context, taskID, cameraID string) {
	s.savedMu.RLock()
	listener := s.savedListener
	s.savedMu.RUnlock()
	if listener == nil {
		return
	}
	listener.OnTaskSaved(context.WithoutCancel(ctx), taskID, cameraID)
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
	// crop 的校验与归一化收敛在这里：创建与按相机 upsert 都走 prepareTask，
	// 存储层的 null/缺省/[]/{} ≡ 不裁剪，整帧也归一化为 '{}'。
	crop, err := region.ParseCrop(t.CropJSON)
	if err != nil {
		return apperr.Invalid("INVALID_CROP_CONFIG", fmt.Sprintf("invalid crop config: %v", err), err)
	}
	// ParseCrop 内部已做 ValidateCrop（有限、范围、非退化、MinCropSide），此处不再重复。
	t.CropJSON = serializeCrop(crop)
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

	// 先应用视图，再重启算法子树：停止旧视图不必要地晚于新算法进程启动。
	s.notifyTaskSaved(ctx, t.ID, t.CameraID)
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

	// 深拷贝必须在 ApplyTo 之前：ApplyTo 原地改 Algorithms，改完就没有旧值可比较了。
	before := cloneTask(task)
	previousIDs := collectAlgorithmIDs(task)

	// 先校验后改内存：ApplyCrop 可能返回 INVALID_CROP_CONFIG，此时 task 是本地副本，不落库。
	if err := req.ApplyCrop(task); err != nil {
		return nil, err
	}
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

	s.notifyTaskSaved(ctx, task.ID, task.CameraID)
	// 仅 crop 变化 → 不重启算法子进程；其余任何语义变化保持现有行为。
	if !sameTaskSemantics(before, task) {
		s.notifyTasksChanged(ctx, append(previousIDs, collectAlgorithmIDs(task)...))
	}
	return task, nil
}

// SaveTask atomically creates or updates a channel task and its algorithms within budget.
func (s *Service) SaveTask(ctx context.Context, t *Task) error {
	if err := s.prepareTask(ctx, t); err != nil {
		return err
	}

	// 读旧值失败不阻断保存，但必须留痕：拿不到旧值时下面的比较只能按“新任务”处理
	// （即多发一次重启）。排查“保存一下就重启”时这行日志是关键证据。
	var existing *Task
	if found, loadErr := s.store.GetTaskByCamera(ctx, t.CameraID); loadErr != nil {
		s.logger.Debug("load task before save failed; treating as new task",
			zap.String("camera_id", t.CameraID), zap.Error(loadErr))
	} else if found != nil {
		existing = found
	}
	var previousIDs []string
	if existing != nil {
		previousIDs = collectAlgorithmIDs(existing)
	}

	if err := s.store.SaveTaskWithinBudget(ctx, t, MaxComputePoints); err != nil {
		return mapWriteError(err, "TASK_SAVE_FAILED", "failed to save analysis task")
	}

	s.notifyTaskSaved(ctx, t.ID, t.CameraID)
	if existing == nil || !sameTaskSemantics(existing, t) {
		s.notifyTasksChanged(ctx, append(previousIDs, collectAlgorithmIDs(t)...))
	}
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

	s.notifyTaskSaved(ctx, id, task.CameraID)
	s.notifyTasksChanged(ctx, collectAlgorithmIDs(task))
	return nil
}

// GetTask returns a task by its ID.
func (s *Service) GetTask(ctx context.Context, id string) (*Task, error) {
	task, err := s.loadTask(ctx, id)
	if err != nil {
		return nil, err
	}
	s.warnInvalidCrop(task)
	return task, nil
}

// GetTaskByCamera returns a task by its camera ID.
func (s *Service) GetTaskByCamera(ctx context.Context, cameraID string) (*Task, error) {
	task, err := s.store.GetTaskByCamera(ctx, cameraID)
	if err != nil {
		return nil, mapTaskError(err)
	}
	s.warnInvalidCrop(task)
	return task, nil
}

// ListTasks returns tasks matching optional camera or algorithm filters.
// cameraID 优先；两个过滤条件同时给出时 algorithmID 被忽略（单摄像头单任务的模型下没有交集语义）。
func (s *Service) ListTasks(ctx context.Context, cameraID, algorithmID string) ([]Task, error) {
	var (
		tasks []Task
		err   error
	)
	switch {
	case cameraID != "":
		tasks, err = s.store.ListTasksByCamera(ctx, cameraID)
	case algorithmID != "":
		tasks, err = s.store.ListTasksByAlgorithm(ctx, algorithmID)
	default:
		tasks, err = s.store.ListTasks(ctx)
	}
	if err != nil {
		return nil, err
	}
	for i := range tasks {
		s.warnInvalidCrop(&tasks[i])
	}
	return tasks, nil
}

// warnInvalidCrop 对“库里存在但解析失败”的 crop 配置记一次可定位的告警。
//
// 写入路径会拒绝非法配置，因此这只可能来自手工改库/降级回滚；读路径不得因此拒绝服务。
func (s *Service) warnInvalidCrop(task *Task) {
	if task == nil {
		return
	}
	if _, err := region.ParseCrop(task.CropJSON); err != nil {
		s.logger.Warn("analysis task carries invalid crop config",
			zap.String("task_id", task.ID),
			zap.String("camera_id", task.CameraID),
			zap.Error(err))
	}
}

// collectAlgorithmIDs 汇总给定任务下的所有算法 ID。
func collectAlgorithmIDs(tasks ...*Task) []string {
	ids := make([]string, 0, len(tasks))
	for _, t := range tasks {
		if t == nil {
			continue
		}
		for _, sub := range t.Algorithms {
			ids = append(ids, sub.AlgorithmID)
		}
	}
	return ids
}

// cloneTask 深拷贝任务聚合并把 Algorithms 切片与源对象隔离。
// TaskAlgorithm 只含标量与字符串，逐值拷贝即可；CropJSON 是 string，天然独立。
func cloneTask(t *Task) *Task {
	if t == nil {
		return nil
	}
	clone := *t
	clone.Algorithms = append([]TaskAlgorithm(nil), t.Algorithms...)
	return &clone
}

// sameTaskSemantics 比较“会进算法子进程的那部分任务语义”，用于决定是否需要重启算法子进程。
//
// 只比较 CameraID / Enabled / 算法集合（AlgorithmID、Enabled、SampleFPS、ConfigJSON、
// RegionConfigJSON、ScheduleJSON）。刻意忽略：
//   - CropJSON：只换视图，不换算法输入（走 TaskSavedListener 而不是重启）；
//   - Name：纯展示字段，从不外发给算法包，改名重启只是白白打断流；
//   - Task.ID / 子项 ID / CreatedAt / UpdatedAt：簿记字段，重建子项会换 ID 但语义不变。
//
// 保守方向是“不确定就当变了”：误重启只损失一次重连，漏重启会让配置静默不生效。
// 新增 TaskAlgorithm 字段时必须同步增到 sameAlgorithmSet 的比较列表里，否则新配置改完不生效。
func sameTaskSemantics(a, b *Task) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.CameraID != b.CameraID || a.Enabled != b.Enabled {
		return false
	}
	return sameAlgorithmSet(a.Algorithms, b.Algorithms)
}

func sameAlgorithmSet(a, b []TaskAlgorithm) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]TaskAlgorithm(nil), a...)
	bs := append([]TaskAlgorithm(nil), b...)
	sort.Slice(as, func(i, j int) bool { return as[i].AlgorithmID < as[j].AlgorithmID })
	sort.Slice(bs, func(i, j int) bool { return bs[i].AlgorithmID < bs[j].AlgorithmID })
	for i := range as {
		left, right := as[i], bs[i]
		if left.AlgorithmID != right.AlgorithmID ||
			left.Enabled != right.Enabled ||
			left.SampleFPS != right.SampleFPS ||
			left.ConfigJSON != right.ConfigJSON ||
			left.RegionConfigJSON != right.RegionConfigJSON ||
			left.ScheduleJSON != right.ScheduleJSON {
			return false
		}
	}
	return true
}
