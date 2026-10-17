# PRD: 拆分子进程看护与容灾自愈为独立 algorunner 模块

## Goal

彻底将**动态子进程沙箱与自愈调度 (Runtime Supervisor)** 从 `internal/algorithm` 中拆分出去，
建立专职的 `internal/algorunner` 模块。使 `internal/algorithm` 收敛为纯粹的**静态制品资产管理中心 (Package/Asset)**，
消除进程级底层运维代码与上层业务数据管理的混杂。

---

## Background & Motivation

在上一阶段的重构中，我们移除了 Task 业务与跨模块模型倒挂。但 `internal/algorithm` 内部依然包含两套不同层级的系统：
1. **静态资产管理层**：算法包上传、tar 解压、动态库与 manifest 校验（`verifier.go`）、版本管理、持久化与 HTTP API。
2. **动态运行时与自愈层**：
   - `recovery.go`：通用的容灾恢复状态机、指数退避（Exponential Backoff）、崩溃环路熔断检测（CrashLoopBackoff）、自动回滚守卫。
   - `process.go`：子进程生命周期管理、Unix Domain Socket 连接探测与握手、下行控制消息广播通道。
   - `supervisor.go`：子进程守护器、退出监听、自动恢复/回滚策略联动、Broker 多流订阅拓扑同步。

将进程运维与恢复逻辑留在 `algorithm` 中，导致：
- 算法领域关注点过载，单测耦合度高；
- 高价值的子进程看护与恢复状态机无法作为纯粹的运行时调度组件被清晰维护或扩展。

---

## Detailed Requirements

### 1. 新建 `internal/algorunner` 模块
- 将以下文件及测试完整迁移至 `internal/algorunner`：
  - `recovery.go` / `recovery_test.go`（纯状态机与退避逻辑，零外部存储依赖）
  - `process.go` / `process_test.go`（Runner 子进程看护与 UDS 通信）
  - `supervisor.go` / `supervisor_test.go`（Supervisor 主控逻辑）
- 下沉依赖：`internal/algorunner` 仅依赖 `internal/algoipc`、`internal/aitypes`、标准库及基础工具库（`zap` 等）。

### 2. 接口抽象解耦（严禁反向依赖 `algorithm`）
- `Supervisor` 目前对 `algorithm.Store` 存在 3 处调用（获取包信息、更新状态、回滚版本）。
- 在 `algorunner` 中定义最小化接口：
  ```go
  type AlgorithmLifecycleStore interface {
      GetAlgorithmPackage(ctx context.Context, algoID string) (PackageMeta, error)
      UpdateAlgorithmStatus(ctx context.Context, algoID string, status string) error
      RollbackAlgorithmVersion(ctx context.Context, algoID string, version, previousVersion string) error
  }
  ```
- 由 `internal/algorithm.Store` 实现该接口，并在 `internal/app` 层完成依赖注入。

### 3. `internal/algorithm` 瘦身与收敛
- `internal/algorithm` 移除 `process.go`、`recovery.go`、`supervisor.go`。
- `algorithm.Service` 依赖 `algorunner.Supervisor` 接口或实例处理启停、回滚触发与状态感知。
- `internal/algorithm` 代码行数进一步减少约 1500+ 行，成为纯粹的资产管理服务。

### 4. 依赖注入与路由层装配 (`internal/app`)
- 更新 `internal/app/app.go` 中的 Uber Fx 组件装配：
  - `algorunner.NewSupervisor` 独立注入为运行时依赖；
  - 维持 `GalleryBroadcaster` 与原生引擎桥接的正确生命周期绑定。

---

## Constraints

1. **零外部行为破损**：C++ 原生 Runner（`zhulong_algo_runner`）的拉起参数、socket 探测、心跳、IPC 通信协议保持 100% 不变。
2. **严格单向依赖**：`app` ➔ `algorithm` ➔ `algorunner` ➔ `algoipc` / `aitypes`。严禁环形依赖。
3. **并发与竞态安全**：全量 Go 测试必须通过 `-race`，严禁数据竞争。
4. **Native CTest 保持 100% 绿灯**。

---

## Acceptance Criteria

- [ ] 成功创建 `internal/algorunner` 包，并将 `recovery.go`、`process.go`、`supervisor.go` 及其单测迁移至该包。
- [ ] `internal/algorunner` 零反向依赖 `internal/algorithm`，对包元数据与状态更新使用接口解耦。
- [ ] `internal/algorithm` 彻底剥离进程管理代码，只聚焦于资产管理与验证。
- [ ] `internal/app` 完成 `algorunner` 的 Fx 依赖注入装配。
- [ ] `make native-test` (20/20 CTest) 全量通过。
- [ ] `make go-check`（包含 go vet、-race、Go Bridge 集成测试）全量通过。
- [ ] 架构规范 `.trellis/spec/backend/directory-structure.md` 登记 `algorunner`。
