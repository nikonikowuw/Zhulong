# PRD: 重构 internal/algorithm 控制面分层与 apperr 错误收拢

## Goal

消除 `internal/algorithm` 控制面（`handler.go`）中的职责越权与代码异味，将分析任务的参数默认值、ID 生成、业务校验及错误分类统一收拢至 `Service` 与 `apperr` 体系，消除裸 `c.JSON` 响应，提升架构内聚度与代码可复用性。

---

## Requirements

### 1. 领域模型与参数默认值下沉至 Service
- `Handler.CreateTask` 不再承担领域实体的拼装、业务默认值赋初值（如默认 `SampleFPS = 5`、`ConfigJSON = "{}"`）以及任务全局唯一 ID 生成（`idutil.New()`）。
- `CreateTaskRequest` DTO 保留原有的 camelCase / snake_case 双键兼容解析，并提供参数归一化能力。
- 参数非空校验（如 `CameraID`、`AlgorithmID` 必填）和领域规则校验移入 Service，校验失败返回统一的 `apperr.Invalid("VALIDATION_FAILED", ...)`。

### 2. 统一错误语义体系（接入 `apperr`）
- `Service` 层面所有业务错误使用 `apperr.Error` 进行包装，明确 `apperr.Kind`（如 `KindInvalid`、`KindNotFound`、`KindConflict` 等）与稳定的机器码。
- `Handler` 彻底移除针对具体哨兵错误（Sentinel Error）的冗长大 `switch-case` 匹配，直接调用 `httputil.WriteError(c, err)`。
- 对外 HTTP 状态码、机器码和本地化文案必须与现有一致，保障 `locale.go` 字典匹配及向下兼容。

### 3. 规范化 HTTP 响应信封
- 替换 `Handler.Install` 与 `Handler.CreateTask` 中的裸 `c.JSON(http.StatusCreated, ...)` 为项目标准的 `httputil.SuccessWithStatus(c, http.StatusCreated, data)`。
- 所有成功响应统一遵循 `{code: "OK", message: "...", data: ...}` 信封规范。

### 4. 优化上传流与临时文件清理
- 整理 `Handler.Install` 中的 multipart 流读取与临时文件管理，确保在校验失败、错误返回及正常完成时均能可靠清理临时文件。

---

## Constraints

1. **零破坏性变更 (Zero Breaking Changes)**：外部 API 契约（HTTP 路由、参数别名、错误响应格式、状态码）必须与前端及既有测试保持 100% 兼容。
2. **并发与资源安全**：保持算力点数上限检查（1000 点）、Zip-Slip、解压炸弹防御等所有安全性约束。
3. **架构边界**：不引入不必要的额外抽象，代码行数精简，符合项目整体架构规范（如 `internal/camera`）。

---

## Acceptance Criteria

- [x] `handler.go` 中不再包含 `idutil.New()` 或硬编码的业务默认值，`CreateTask` 仅负责参数绑定与委托调用。
- [x] `handler.go` 中消除针对 `ErrPlatformMismatch`、`ErrComputeBudgetExceeded` 等 sentinel errors 的大 `switch` 匹配，统一委托给 `httputil.WriteError(c, err)`。
- [x] `handler.go` 中完全消除裸 `c.JSON(...)` 调用，全量替换为 `httputil.Success` / `httputil.SuccessWithStatus`。
- [x] 现有的所有错误状态码保持兼容（例如未安装返回 404，算力超限返回 409，平台不匹配返回 400，黑盒验包失败返回 422 等）。
- [x] `go test -race ./internal/algorithm/...` 100% 顺利通过，且无数据竞争。
- [x] `make check` 通过，确保前端和后端无破坏性影响。
