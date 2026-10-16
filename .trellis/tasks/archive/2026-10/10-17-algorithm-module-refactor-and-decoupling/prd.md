# PRD: 重构 internal/algorithm 模块瘦身与能力解耦

## Goal

彻底遵循单一职责原则 (SRP) 和开放封闭原则 (OCP)，将 `internal/algorithm` 聚焦收敛为：
**算法包制品资产管理（解压、验包、版本管理、持久化）** 与 **Runner 沙箱子进程运维（拉起、监控、退避重试、自动回滚）**。
通过分阶段解耦，剥离残留的分析任务管理、下沉 IPC 协议层、提取公共 AI 推理模型、将人脸底库业务广播回归 `internal/person` 领域，彻底解除跨域依赖倒挂并降低包维护复杂度。

---

## Background & Motivation

在当前的边缘视觉计算系统中，`internal/algorithm` 演进过程中沉淀了过多不同维度的能力，形成了典型的“上帝模块”坏味道：

1. **Task 职责冗余分裂**：在前期架构演进中已创建了独立的 `internal/task` 模块，但 `algorithm` 包内仍遗留了完整的 Task CRUD、算力预算检查代理（`store.go`）、旧端点处理（`handler.go`）与 DTO 模型（`requests.go`），形成双轨维护包袱。
2. **跨域依赖倒挂**：推理结果模型（`InferenceResult`、`DetectionItem`、`PersonItem` 等）定义在 `algorithm/result.go` 中，导致流分发中心 `internal/camera` 和报警记录域 `internal/record` 仅仅为了处理结果数据结构，就不得不反向强依赖 `internal/algorithm`。
3. **通信协议与运维混杂**：16 字节定长 IPC 二进制协议头编解码、64KB 边界防御与版本魔数直接杂糅在子进程看护（`process.go`）中。
4. **特定业务污染通用底座**：人脸特征底库的同步与广播（`GalleryBroadcaster`、`GallerySyncProvider`）被硬编码进通用算法 Supervisor 中。通用执行容器不应感知人脸特定数据结构，否则未来新增车牌、行为分析等底库时将破坏 OCP 原则。

---

## Detailed Requirements

### 1. Phase 1: 彻底清理 Task 冗余代码并统一路由委托
- **清理持久化层 (`store.go`)**：移除 `CreateTask`, `CreateTaskWithinBudget`, `GetTask`, `ListTasks`, `UpdateTask`, `DeleteTask` 等代理方法。
- **清理服务层 (`service.go`)**：移除 `CreateTask`, `UpdateTask`, `DeleteTask`, `ListTasks` 等任务调度方法，仅保留实现 `task.AlgorithmSpecProvider` 和 `task.TaskChangeListener` 所需的轻量契约。
- **清理控制面 (`handler.go`, `requests.go`)**：
  - 移除 `algorithm.Handler` 中的任务端点。
  - 兼容端点 `/api/v1/algorithms/tasks` 改由 `task.Handler` 直接挂载或在 `internal/app` 层统一映射委托。
  - 删除 `requests.go` 中无用的 Task 请求结构体。

### 2. Phase 2: 提取公共 AI 推理数据契约 (`internal/aitypes`)
- 新建轻量领域契约包 `internal/aitypes`：
  - 迁移 `InferenceResult`, `ResultType`, `DetectionItem`, `ClassificationItem`, `PersonItem`, `FaceSubItem`, `FaceCandidate`。
  - 提供干净的 JSON 解析与校验辅助方法。
- **解耦上层依赖**：
  - `internal/camera/stream_hub.go` 改为依赖 `aitypes.InferenceResult`。
  - `internal/record` 改为依赖 `aitypes.InferenceResult`。
  - 消除 `camera` / `record` ➔ `algorithm` 的依赖箭头。

### 3. Phase 3: 下沉 IPC 二进制通信协议 (`internal/algoipc`)
- 新建纯协议包 `internal/algoipc`：
  - 定义 16 字节固定帧头：`IpcHeader`（Magic `0x5A484C47`、MsgType、Version、PayloadSize、Sequence）。
  - 常量收敛：`MaxIpcPayloadSize` (64KB)、`MsgResult`、`MsgGallery*`。
  - 提供报文封装 `EncodeIpcMessage` 与头部解析 `DecodeIpcHeader`。
- `internal/algorithm/process.go` 及相关 IPC 契约测试全部切换至 `internal/algoipc`。

### 4. Phase 4: 人脸底库广播逻辑解耦
- 将 `GalleryBroadcaster`、`GallerySyncItem`、`GalleryUpsertPayload` 等迁移至 `internal/person`（或其子包）。
- `algorithm.Supervisor` / `RunnerProcess` 仅暴露泛化 IPC 消息发送通道（`BroadcastAIMessage(msgType uint16, payload []byte) error`）。
- 由 `internal/app` 装配时，让 `person` 模块持有专用广播器并注入泛化通道，实现底库业务与通用算法监督彻底解耦。

---

## Constraints

1. **外部 API 完全兼容**：现有 `/api/v1/algorithms/tasks` 及 `/api/v1/tasks` 的 HTTP 行为、状态码、响应体格式与审计日志记录保持 100% 兼容。
2. **IPC 二进制契约完全一致**：与 C++ 原生 Runner（`zhulong_algo_runner`）的 16 字节头部、64KB 限制、消息类型 ID 完全保持对齐（通过 `ipc_contract_test.go` 验证）。
3. **单向依赖铁律**：模块依赖方向必须单向流动：`app` ➔ `task` / `algorithm` / `person` / `camera` / `record` ➔ `aitypes` / `algoipc` ➔ `database` / `httputil`。严禁环形依赖。
4. **并发与资源安全**：防抖重启机制、状态机转换逻辑与数据竞争测试（`-race`）必须保持严格绿灯。

---

## Acceptance Criteria
 
- [x] `internal/algorithm` 中所有冗余 Task 代码彻底剥离，包内行数大幅精简，职责收敛为包管理与进程运维。
- [x] 兼容路由 `/api/v1/algorithms/tasks` 统一委托至 `task` 领域，相关自动化测试通过。
- [x] 成功建立 `internal/aitypes`，`camera` 和 `record` 不再导入 `internal/algorithm`。
- [x] 成功建立 `internal/algoipc`，IPC 协议编解码逻辑下沉并独立。
- [x] 人脸底库同步逻辑解耦，`Supervisor` 与 `person` 之间边界清晰，领域解耦。
- [x] `python3 native/scripts/build.py go test -race ./internal/...` 全量通过，无 race condition。
- [x] `make go-check` 与 `make native-test` (20/20 CTest) 全量通过。
