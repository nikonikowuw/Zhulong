# 算法包任务驱动按需启停与启用幂等性技术设计

## 1. 架构拓扑与交互模型

```txt
┌──────────────┐          POST /tasks (enabled=true)
│  Web / API   ├─────────────────────────────────────────┐
└──────┬───────┘                                         │
       │ POST /algorithms/{id}/enable                    │
       ▼                                                 ▼
┌───────────────────┐                          ┌──────────────────┐
│ algorithm.Service │                          │   task.Service   │
└──────┬────────────┘                          └─────────┬────────┘
       │                                                 │ OnTaskChanged
       ▼                                                 ▼
┌───────────────────┐    SyncBrokerSubscriptions       ┌──────────────────┐
│   algorunner.     │ ◄─────────────────────────────── │ appBrokerPath-   │
│   Supervisor      │   (paths = [broker1, ...])       │ Resolver         │
└──────┬────────────┘                                  └──────────────────┘
       │
       │ len(paths) > 0: exec zhulong_algo_runner --broker ...
       │ len(paths) == 0: 返回 ErrNoBrokerSockets，保持 Standby (0 进程)
       ▼
┌──────────────────────────────┐
│  zhulong_algo_runner 子进程  │
└──────────────────────────────┘
```

---

## 2. 状态机迁移规范

算法拥有两层状态：
1. **持久化状态（DB `algorithms.status`）**：`DISABLED`、`ACTIVE`、`CRASH_LOOP`
2. **运行时监管状态（Supervisor Live Status）**：`DISABLED`（无活跃 watcher）、`STARTING`、`RUNNING`、`CRASHED`、`CRASH_LOOP`、`ROLLING_BACK`

### 2.1 任务驱动下的组合状态矩阵

| DB 状态 | 活跃任务数 (`len(brokerSocks)`) | Supervisor 状态 | 物理子进程 | 表现与语义 |
| :--- | :---: | :---: | :---: | :--- |
| `DISABLED` | 任意 | `DISABLED` | 无 | 管理员显式禁用，不参与分析调度 |
| `ACTIVE` | **0** | `DISABLED` | **无** | **待命就绪（Standby）**：允许调度，但无输入流，0 资源占用 |
| `ACTIVE` | **> 0** | `RUNNING` | 1 组进程 | **运行分析中**：常驻消费各相机 FrameBroker 共享内存 |
| `CRASH_LOOP` | 任意 | `CRASH_LOOP` | 无 | **熔断**：异常频发保护，需显式调用 `/reset` 恢复至 `DISABLED` |

---

## 3. 详细设计与关键契约

### 3.1 `algorunner.Supervisor` 改造

#### 1) 导出哨兵错误
```go
var (
    ErrAlgorithmNotInCrashLoop = errors.New("algorithm is not in crash loop")
    ErrAlgorithmAlreadyRunning = errors.New("algorithm worker is already running or supervised")
    ErrNoBrokerSockets         = errors.New("cannot start runner without broker sockets")
)
```

#### 2) `StartAlgorithm` 拦截 0 broker
在解析出 `brokerSocks` 之后且尚未加写锁注册 watcher 之前：
```go
if s.resolver != nil && len(brokerSocks) == 0 {
    return ErrNoBrokerSockets
}
```
*注：当 `s.resolver == nil` 时（如独立单测环境），保持原行为以兼容 mock 脚本。*

#### 3) `RestartAlgorithm` 平滑回退待命态
在任务变更触发重启时，如果最新解析出的 `brokerSocks` 为空：
- 调用 `StopAlgorithm(ctx, algoID)` 停止既有子进程；
- 将 DB 状态恢复写回 `StatusActive`（`StopAlgorithm` 会写入 `DISABLED`，必须修正）；
- 返回 `nil`，完成向待命态的平滑过渡。

---

### 3.2 `algorithm.Service` 改造

#### 1) `EnableAlgorithm` 幂等与冲突拦截
```go
func (s *Service) EnableAlgorithm(ctx context.Context, id string) error {
    algo, err := s.store.GetAlgorithm(ctx, id)
    if err != nil {
        if errors.Is(err, ErrNotFound) {
            return apperr.NotFound("ALGORITHM_NOT_FOUND", "Algorithm not found", err)
        }
        return apperr.Internal("INTERNAL_ERROR", fmt.Sprintf("get algorithm: %v", err), err)
    }

    liveStatus := s.supervisor.GetStatus(id)
    if liveStatus == StatusCrashLoop {
        return apperr.Conflict("ALGORITHM_IN_CRASH_LOOP", "Algorithm is in CRASH_LOOP, please reset it first", nil)
    }
    if liveStatus == StatusRunning || liveStatus == StatusStarting {
        if algo.Status != StatusActive {
            _ = s.store.UpdateAlgorithmStatus(ctx, id, StatusActive)
        }
        return nil // 幂等成功
    }

    err = s.supervisor.StartAlgorithm(ctx, algo.ToPackageMeta())
    if err != nil {
        if errors.Is(err, algorunner.ErrNoBrokerSockets) {
            // 待命状态：更新 DB 为 ACTIVE，正常返回
            s.logger.Info("algorithm enabled in standby state (no active tasks)", zap.String("id", id))
            if err := s.store.UpdateAlgorithmStatus(ctx, id, StatusActive); err != nil {
                return apperr.Internal("INTERNAL_ERROR", fmt.Sprintf("update algorithm status: %v", err), err)
            }
            return nil
        }
        if errors.Is(err, algorunner.ErrAlgorithmAlreadyRunning) {
            return nil // 并发幂等成功
        }
        return apperr.Internal("INTERNAL_ERROR", fmt.Sprintf("start runner supervisor: %v", err), err)
    }

    if err := s.store.UpdateAlgorithmStatus(ctx, id, StatusActive); err != nil {
        return apperr.Internal("INTERNAL_ERROR", fmt.Sprintf("update algorithm status: %v", err), err)
    }
    return nil
}
```

#### 2) `OnTaskChanged` 驱动待命拉起与回收
在 `restartAlgorithmNow(algorithmID)` 中：
```go
func (s *Service) restartAlgorithmNow(algorithmID string) {
    algo, err := s.store.GetAlgorithm(context.Background(), algorithmID)
    if err != nil || algo.Status != StatusActive {
        return
    }

    liveStatus := s.supervisor.GetStatus(algorithmID)
    if liveStatus == StatusRunning || liveStatus == StatusStarting {
        if err := s.supervisor.RestartAlgorithm(context.Background(), algorithmID); err != nil {
            s.logger.Warn("failed to restart algorithm after task change",
                zap.String("algorithm_id", algorithmID),
                zap.Error(err),
            )
        }
    } else if liveStatus == StatusDisabled {
        // 从待命态自动拉起
        if err := s.supervisor.StartAlgorithm(context.Background(), algo.ToPackageMeta()); err != nil {
            if !errors.Is(err, algorunner.ErrNoBrokerSockets) {
                s.logger.Warn("failed to start standby algorithm after task change",
                    zap.String("algorithm_id", algorithmID),
                    zap.Error(err),
                )
            }
        }
    }
}
```

---

## 4. 边界与防御

1. **DB 状态与实时状态一致性**：当算法处于 Standby 态时，DB 为 `ACTIVE`，Supervisor 为 `DISABLED`。在 `ListAlgorithms` 与 `GetAlgorithm` 中，原逻辑为 `if liveStatus != StatusDisabled { algo.Status = liveStatus }`，因此当 `liveStatus == StatusDisabled` 时，透出给前端的依然是 DB 状态 `ACTIVE`，前端 Switch 保持高亮开启，行为与语义完全自洽。
2. **并发栅栏**：`Service.EnableAlgorithm` 与 `OnTaskChanged` 均通过 `Supervisor.StartAlgorithm` 内的快慢路径双重读写锁保证并发注册原子性，不会发生同一算法重复拉起两个进程的问题。
