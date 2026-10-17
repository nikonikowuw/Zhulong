# 算法包任务驱动按需启停与启用幂等性修复

## Goal

实现算法运行器（Runner）由分析任务（Task）驱动的按需惰性启停，杜绝无视频流分析任务时强行拉起子进程造成的虚假崩溃与 CRASH_LOOP 熔断，并修复 `/algorithms/{id}/enable` 接口的幂等性与错误码映射。

## 背景与问题

1. **无 Broker 运行器自杀与虚假熔断**：
   - 原生 runner（`zhulong_algo_runner`）是数据流驱动的推理宿主，要求至少一个 `--broker <sock_path>` 共享内存流；若无 broker，打印 Usage 并退出。
   - 当前 `Service.EnableAlgorithm` 无论是否有任务绑定，都会无条件调用 `s.supervisor.StartAlgorithm`。
   - 在任务数为 0 时，`brokerSocks` 为空切片，Supervisor 依然拉起 runner。Runner 秒退后被 Go 侧误判为“异常崩溃”，触发退避重启状态机，反复重启直至触发熔断进入 `CRASH_LOOP`，造成 CPU/日志浪费与状态死锁。
2. **Enable 接口缺乏幂等性且错误码降级为 500**：
   - 若算法处于运行中或崩溃退避/熔断中，Supervisor 的 `s.watchers` 中仍存在该 ID，再次调用 `/enable` 会返回裸字符串 `errors.New("algorithm worker is already running or supervised")`。
   - `Service.EnableAlgorithm` 缺乏业务特判，将其一律包装为 `apperr.Internal("INTERNAL_ERROR", ...)`（HTTP 500），导致正常的重复请求或状态冲突变成服务端内部错误。

## Requirements

### R1 — 任务驱动的惰性按需启停 (Task-Driven Lazy Startup)
- 算法的数据库状态 `ACTIVE` 仅代表「管理员授权启用/允许调度」，不再强行捆绑底层常驻子进程。
- 当算法当前无已启用的分析任务（`len(brokerSocks) == 0`）时，`StartAlgorithm` 与 `EnableAlgorithm` 不得拉起 Runner 子进程，杜绝无输入流时的虚假崩溃。
- 此时算法在数据库中标记为 `ACTIVE`，处于无常驻进程的待命状态（Standby）。

### R2 — 任务变更动态驱动 Runner 进程伸缩 (Dynamic Lifecycle Transition)
- 当分析任务被创建或启用（触发 `TaskChangeListener.OnTaskChanged`）：
  - 若对应算法已为 `ACTIVE` 但因无任务处于待命态，自动拉起 Runner 子进程并挂载对应 broker sockets。
  - 若对应算法已在运行，执行防抖热重启更新 `--broker` 列表。
- 当分析任务被删除或停用（触发 `TaskChangeListener.OnTaskChanged`）：
  - 若对应算法的活跃任务数降为 0，优雅停止 Runner 子进程释放系统资源，平滑回退至待命态。
  - 数据库中算法状态保持为 `ACTIVE`，不得误标为 `DISABLED`，也不得触发崩溃告警。

### R3 — `EnableAlgorithm` 接口幂等性与错误码规范化
- 若算法已处于 `RUNNING` 或 `STARTING`，调用 `POST /api/v1/algorithms/{id}/enable` 应幂等返回 `200 OK`，状态返回 `ACTIVE`。
- 若算法处于 `CRASH_LOOP` 熔断状态，调用 `POST /api/v1/algorithms/{id}/enable` 应返回 `409 Conflict`（机器码 `ALGORITHM_IN_CRASH_LOOP`），明确指引先重置。
- `internal/algorunner` 导出哨兵错误 `ErrNoBrokerSockets` 与 `ErrAlgorithmAlreadyRunning`，供上层做精准类型判断。

### R4 — 宿主开机自启兼容
- `algorithmLifecycleImpl.Start` 宿主启动时，遍历 `ACTIVE` 算法并按相同逻辑拉起：有活跃任务的拉起子进程，无活跃任务的保持待命态，杜绝宿主开机时无任务算法批量熔断。

## Acceptance Criteria

- [ ] **0 任务启用**：在无任何任务绑定的情况下调用 `POST /api/v1/algorithms/{id}/enable`，返回 200 OK，DB 状态为 `ACTIVE`，不拉起 runner 子进程，无崩溃日志，无 `CRASH_LOOP` 熔断。
- [ ] **添加任务自动拉起**：为处于待命态的 `ACTIVE` 算法创建并启用分析任务后，系统自动拉起 runner 子进程并连接 broker，Supervisor 状态变为 `RUNNING`。
- [ ] **清空任务自动回收**：将正在运行算法的任务全部删除或禁用后，runner 进程被优雅停止并回收资源，算法在 DB 中保持 `ACTIVE`，不产生崩溃或重启日志。
- [ ] **重复启用幂等**：对正在运行的算法再次调用 `POST /api/v1/algorithms/{id}/enable`，返回 200 OK，不返回 500。
- [ ] **熔断状态拦截**：对处于 `CRASH_LOOP` 的算法调用 `POST /api/v1/algorithms/{id}/enable`，返回 409 Conflict 与 `ALGORITHM_IN_CRASH_LOOP` 错误码。
- [ ] **测试验证**：`internal/algorunner` 与 `internal/algorithm` 全部单元测试（含并发竞争 `-race`）通过。
