# 执行计划: 重构 internal/algorithm 控制面分层与 apperr 错误收拢

## 实施步骤

### Step 1: DTO 封装与 ToTask 转换器定义
- 在 `internal/algorithm/handler.go` 中为 `CreateTaskRequest` 添加 `ToTask() *AnalysisTask` 方法。
- 将驼峰/蛇形别名合并逻辑移入 `ToTask()`。
- **验证项**：`go test -race ./internal/algorithm -run TestCreateTaskSnakeCaseAliases`

### Step 2: 领域模型与默认值、ID 生成下沉到 Service
- 在 `internal/algorithm/service.go` 的 `CreateTask` 中添加：
  - `t.CameraID` 和 `t.AlgorithmID` 必填校验，返回 `apperr.Invalid("VALIDATION_FAILED", ...)`
  - `t.SampleFPS <= 0` 赋默认值 `5`
  - `t.ConfigJSON == ""` 赋默认值 `"{}"`
  - `t.Enabled = true`
  - `t.ID == ""` 调用 `idutil.New()`
- 将 `CreateTask` 内部的配置解析与算力越限错误标准化为 `apperr.Error`（保留底层 error cause）。
- **验证项**：`go test -race ./internal/algorithm -run TestServiceCreateTask`

### Step 3: Service 全面接入 apperr 错误体系
- 在 `internal/algorithm/service.go` 中，更新：
  - `GetAlgorithm`: 未找到返回 `apperr.NotFound("ALGORITHM_NOT_FOUND", ...)`
  - `EnableAlgorithm` / `DisableAlgorithm`: 未找到返回 `apperr.NotFound("ALGORITHM_NOT_FOUND", ...)`
  - `ResetAlgorithm`: 不处于 CRASH_LOOP 返回 `apperr.Conflict("ALGORITHM_NOT_IN_CRASH_LOOP", ...)`
  - `DeleteAlgorithm`: 处于活跃状态返回 `apperr.Conflict("ALGORITHM_IS_ACTIVE", ...)`，未找到返回 `apperr.NotFound`
  - `DeleteTask`: 未找到返回 `apperr.NotFound("TASK_NOT_FOUND", ...)`
  - `InstallAlgorithm`: 对验包与解压抛出的业务哨兵错误进行标准化包装
- **验证项**：`go test -race ./internal/algorithm -run TestHandlerAnalysisTasks`

### Step 4: 重构 Handler 层，消除大 switch 与裸 c.JSON
- 在 `internal/algorithm/handler.go` 中：
  - 将 `CreateTask` 简化为 `ShouldBindJSON -> req.ToTask() -> service.CreateTask -> httputil.SuccessWithStatus`。
  - 将 `Install`、`Reset`、`Delete`、`DeleteTask`、`Get` 等方法中的人工 `errors.Is` 匹配移除，统一直接调用 `httputil.WriteError(c, err)`。
  - 将 `Install` 和 `CreateTask` 中的 `c.JSON(http.StatusCreated, ...)` 全量替换为 `httputil.SuccessWithStatus(c, http.StatusCreated, ...)`.
- **验证项**：
  - `go test -race ./internal/algorithm/...`
  - `go test -race ./internal/algorithm -run TestHandlerListAndInstall`
  - `go test -race ./internal/algorithm -run TestAlgorithmErrorCodesAreLocalized`

### Step 5: 全量回归与跨模块校验
- 运行 `go test -race ./internal/algorithm/...`
- 运行 `python3 native/scripts/build.py go test -race ./internal/camera -run TestCameraCreate`
- 运行前端单测 `cd web && pnpm test -- src/features/algorithms/api/algorithms-api.test.ts`
- 运行 `make check` 验证代码规范与全局编译

---

## 回滚计划
若发生意外破坏或行为不一致，由于本任务均为内部重构，使用 `git checkout -- internal/algorithm/` 即可安全回滚至修改前状态。
