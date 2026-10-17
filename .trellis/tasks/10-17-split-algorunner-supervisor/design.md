# Technical Design: 拆分子进程看护与容灾自愈为独立 algorunner 模块

## 1. 架构目标与分层重塑

本次重构确立新的模块职责边界：

```
+-------------------------------------------------------------+
|                      internal/app                           |
|         (Uber Fx 装配, 生命周期生命线, 路由挂载)              |
+-------------------------------------------------------------+
      |                                              |
      v                                              v
+-----------------------------+          +-----------------------------+
|     internal/algorithm      |          |     internal/algorunner     |
|   - 静态制品包管理 (Upload)   | -------->|   - 子进程看护 (Supervisor)   |
|   - 验包解包 (Verifier)     | (接口调用)|   - 进程 IPC/Socket 探测    |
|   - 数据库元数据 (Store)     |          |   - 容灾自愈 (CrashLoop, 退避)|
|   - HTTP 控制面 API         |          |   - 自动回滚守卫 (Guard)      |
+-----------------------------+          +-----------------------------+
               \                                /
                \                              /
                 v                            v
          +------------------------------------------+
          |           internal/algoipc               |
          |       (16-byte 二进制帧协议编解码)        |
          +------------------------------------------+
                 |                            |
                 v                            v
          +------------------------------------------+
          |           internal/aitypes               |
          |           (通用 AI 推理模型)             |
          +------------------------------------------+
```

---

## 2. 接口解耦设计

### 2.1 `algorunner.AlgorithmPackage` 与 `LifecycleStore`
在 `internal/algorunner` 中定义所需的数据契约与接口，不直接依赖 `algorithm.Algorithm` 或 `algorithm.Store`：

```go
package algorunner

import (
	"context"
)

// PackageMeta represents the minimal algorithm package metadata needed by the runner supervisor.
type PackageMeta struct {
	ID              string
	Target          string
	Version         string
	PreviousVersion string
	Status          string
}

// LifecycleStore provides package lookup and status updates for the supervisor.
type LifecycleStore interface {
	GetAlgorithmPackage(ctx context.Context, id string) (PackageMeta, error)
	UpdateAlgorithmStatus(ctx context.Context, id string, status string) error
	RollbackAlgorithmVersion(ctx context.Context, id string, version, previousVersion string) error
}
```

### 2.2 `algorunner.Supervisor` 核心契约
```go
package algorunner

type Supervisor struct {
    // 依赖注入 Store 抽象接口，彻底解耦
    store               LifecycleStore
    algoDir             string
    runnerPath          string
    resolver            BrokerSubscriptionResolver
    gallerySyncProvider GallerySyncProvider
    galleryBroadcaster  *GalleryBroadcaster
    logger              *zap.Logger
    // ...
}
```

### 2.3 `algorithm.Store` 适配
让 `internal/algorithm.Store` 实现 `algorunner.LifecycleStore`：
- `GetAlgorithmPackage`: 查库并映射为 `algorunner.PackageMeta`
- `UpdateAlgorithmStatus`: 更新状态
- `RollbackAlgorithmVersion`: 回滚版本

---

## 3. 文件组织与迁移规划

1. **新建 `internal/algorunner`**：
   - `model.go`：定义 `PackageMeta`、`Status*` 常量、`LifecycleStore` 接口、`BrokerSubscriptionResolver` 等契约。
   - `recovery.go` / `recovery_test.go`：退避状态机、熔断与回滚守卫。
   - `process.go` / `process_test.go`：子进程管理、IPC 握手、`GalleryBroadcaster`。
   - `supervisor.go` / `supervisor_test.go`：多进程看护调度器。
2. **清理 `internal/algorithm`**：
   - 删除 `internal/algorithm/recovery.go`、`recovery_test.go`。
   - 删除 `internal/algorithm/process.go`、`process_test.go`。
   - 删除 `internal/algorithm/supervisor.go`、`supervisor_test.go`。
   - `internal/algorithm/service.go`：调用 `algorunner.Supervisor` 控制启停。
3. **更新 `internal/app/app.go`**：
   - `NewSupervisor` 从 `algorunner.NewSupervisor` 实例化。
   - Fx 依赖流注入。
