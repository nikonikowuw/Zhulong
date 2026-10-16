# 技术设计方案: 重构 internal/algorithm 控制面分层与 apperr 错误收拢

## 1. 现状痛点与重构目标

当前 `internal/algorithm/handler.go` 存在严重的分层穿透问题：
1. **实体组装与 ID 生成越权**：`CreateTask` 端点在 Handler 内部手动做了参数别名归一化、必填校验、默认 FPS=5 / Config="{}" 填充，并直接调用 `idutil.New()` 生成实体 ID。一旦有其他非 HTTP 入口（如内部事件总线、定时任务、CLI）创建任务，这些业务逻辑无法复用。
2. **错误处理手工匹配（人工翻译官）**：`Install`、`Reset`、`Delete` 等端点包含大量针对 `ErrPlatformMismatch`、`ErrComputeBudgetExceeded` 等底层 sentinel error 的大 `switch` 匹配，违反了项目 `apperr` 统一定义与状态码自映射规范。
3. **响应信封违规**：`Install` 与 `CreateTask` 中裸写了 `c.JSON(http.StatusCreated, ...)`，违背了 `AGENTS.md` 规范。

**重构目标**：
- Handler 仅保留 HTTP 传输协议处理（绑定 DTO、调用 Service、处理 HTTP 响应）。
- 业务校验、默认值、ID 生成下沉至 Service 与领域模型。
- Service 错误统一输出为 `apperr.Error`，Handler 全面直连 `httputil.WriteError(c, err)`。
- 响应完全对齐 `httputil.SuccessWithStatus`。

---

## 2. 领域边界与契约设计

### 2.1 DTO 契约与归一化 (`CreateTaskRequest`)

保留前端已依赖的双键兼顾能力（驼峰与蛇形）：

```go
type CreateTaskRequest struct {
    ID             string `json:"id"`
    CameraID       string `json:"cameraId"`
    CameraIDAlt    string `json:"camera_id"`
    AlgorithmID    string `json:"algorithmId"`
    AlgorithmIDAlt string `json:"algorithm_id"`
    SampleFPS      int    `json:"sampleFps"`
    SampleFPSAlt   int    `json:"sample_fps"`
    ConfigJSON     string `json:"configJson"`
    ConfigJSONAlt  string `json:"config_json"`
}

// ToTask 将 HTTP 请求 DTO 转换为待处理的 AnalysisTask 实体骨架
func (req *CreateTaskRequest) ToTask() *AnalysisTask {
    camID := req.CameraID
    if camID == "" {
        camID = req.CameraIDAlt
    }
    algoID := req.AlgorithmID
    if algoID == "" {
        algoID = req.AlgorithmIDAlt
    }
    fps := req.SampleFPS
    if fps <= 0 {
        fps = req.SampleFPSAlt
    }
    cfg := req.ConfigJSON
    if cfg == "" {
        cfg = req.ConfigJSONAlt
    }
    return &AnalysisTask{
        ID:          req.ID,
        CameraID:    camID,
        AlgorithmID: algoID,
        SampleFPS:   fps,
        ConfigJSON:  cfg,
    }
}
```

### 2.2 Service 业务逻辑下沉 (`Service.CreateTask`)

在 `Service.CreateTask(ctx context.Context, t *AnalysisTask) error` 内完成真正的领域实体初始化与规则校验：

1. **基础必填校验**：若 `t.CameraID == ""` 或 `t.AlgorithmID == ""`，返回 `apperr.Invalid("VALIDATION_FAILED", "CameraID and AlgorithmID are required", nil)`。
2. **业务默认值初值化**：
   - 若 `t.SampleFPS <= 0`，归一化默认值 `t.SampleFPS = 5`；
   - 若 `t.ConfigJSON == ""`，默认值 `t.ConfigJSON = "{}"`；
   - 默认启用 `t.Enabled = true`；
   - 若 `t.ID == ""`，调用 `t.ID = idutil.New()`。
3. **领域规则与算力检查**：
   - 算法不存在：返回 `apperr.NotFound("ALGORITHM_NOT_FOUND", ...)`；
   - 配置 Schema 校验失败：返回 `apperr.Invalid("VALIDATION_FAILED", "Analysis task config is invalid", err)`；
   - Schema JSON 解析本身损坏：返回 `apperr.Internal("INTERNAL_ERROR", "Algorithm config schema is invalid", err)`；
   - 算力配额超限（>1000点）：返回 `apperr.Conflict("COMPUTE_BUDGET_EXCEEDED", err.Error(), err)`。

### 2.3 Service 全量业务错误标准化 (`apperr`)

对 `internal/algorithm/service.go` 的方法返回值进行梳理：

| 方法 | 原始错误 / 触发条件 | 重构后返回的 `apperr.Error` | HTTP 映射 |
| :--- | :--- | :--- | :--- |
| `GetAlgorithm` | `ErrNotFound` | `apperr.NotFound("ALGORITHM_NOT_FOUND", "Algorithm not found", err)` | 404 |
| `EnableAlgorithm` | `ErrNotFound` | `apperr.NotFound("ALGORITHM_NOT_FOUND", "Algorithm not found", err)` | 404 |
| `DisableAlgorithm` | `ErrNotFound` | `apperr.NotFound("ALGORITHM_NOT_FOUND", "Algorithm not found", err)` | 404 |
| `ResetAlgorithm` | `ErrAlgorithmNotInCrashLoop` | `apperr.Conflict("ALGORITHM_NOT_IN_CRASH_LOOP", "Algorithm is not in CRASH_LOOP", err)` | 409 |
| `DeleteAlgorithm` | 算法处于 ACTIVE/RUNNING | `apperr.Conflict("ALGORITHM_IS_ACTIVE", "Algorithm is active", err)` | 409 |
| `DeleteAlgorithm` | `ErrNotFound` | `apperr.NotFound("ALGORITHM_NOT_FOUND", "Algorithm not found", err)` | 404 |
| `DeleteTask` | `ErrNotFound` | `apperr.NotFound("TASK_NOT_FOUND", "Analysis task not found", err)` | 404 |
| `InstallAlgorithm` | `ErrVersionAlreadyInstalled` | `apperr.Conflict("ALGORITHM_VERSION_EXISTS", ...)` | 409 |
| `InstallAlgorithm` | 验包/解压/炸弹/平台不匹配等 | 标准封装为领域错误或 `apperr.Error`，支持 `httputil.WriteError` 直接分派 | 400/422 |

### 2.4 Handler 极致精简

重构后，`Handler` 中所有端点统一采用如下规范模式：

```go
func (h *Handler) CreateTask(c *gin.Context) {
    audithttp.Tag(c, ActionAlgorithmTaskCreate, "algorithm.task:", "Create analysis task")

    var req CreateTaskRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        httputil.WriteError(c, httputil.NewError(http.StatusBadRequest, "INVALID_REQUEST", "Invalid analysis task payload", err))
        return
    }

    task := req.ToTask()
    if err := h.service.CreateTask(c.Request.Context(), task); err != nil {
        httputil.WriteError(c, err)
        return
    }

    httputil.SuccessWithStatus(c, http.StatusCreated, task)
}
```

```go
func (h *Handler) Reset(c *gin.Context) {
    id := c.Param("id")
    audithttp.Tag(c, ActionAlgorithmReset, "algorithm:"+id, "Reset algorithm "+id)

    if err := h.service.ResetAlgorithm(c.Request.Context(), id); err != nil {
        httputil.WriteError(c, err)
        return
    }
    httputil.Success(c, gin.H{"status": StatusDisabled})
}
```

---

## 3. 测试与向后兼容保障

1. **测试用例透明兼容**：保持现有 `handler_test.go`、`error_codes_test.go`、`service_test.go` 的测试契约，特别是 `TestCreateTaskSnakeCaseAliases` 与 `TestCreateTaskRejectsNonJSONBody`。
2. **审计日志打标完整性**：所有 `audithttp.Tag` 保留且触发时机不变。
3. **消除所有裸 `c.JSON`**：全局检查并替换为 `httputil.SuccessWithStatus`。
