# 摄像机流转管道与调度状态时序缺陷设计方案

## 1. 架构与边界说明

本次修改聚焦在 `internal/camera` 包内部的流转与调度子系统，涉及以下组件：

```txt
┌────────────────────────────────────────────────────────────────────────┐
│ internal/camera                                                        │
│                                                                        │
│   StreamHub ──(singleflight.Group)──> AcquireStream / Subscribe        │
│      │  ▲                                                              │
│      │  └─ StreamDispatcher                                            │
│      │        ├─ RunPumpLoop ──(1s/KeyFrame throttled)──> StateRegistry│
│      │        └─ Close ─────────────────────────────────> EventHub     │
│      ▼                                                       │         │
│   EventHub <─────────────────────────────────────────────────┘         │
│      │                                                                 │
│      ▼                                                                 │
│   SSE Clients (/api/v1/cameras/events)                                 │
│                                                                        │
│   HealthScheduler ──(root ctx cancel)──> Probe Cancellation on Stop    │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 2. 关键设计与实现要点

### 2.1 活跃流收包证据节流刷新 (`StreamDispatcher`)

- **问题根源**：
  `StreamDispatcher.RunPumpLoop` 持续从 `sub.Next(ctx)` 消费包并广播，但未更新状态机证据。`HealthScheduler.monitorActiveStreams` 每 250ms 轮询活跃流，若 `now - LastCheckedAt > 4s`，则判定丢包并切为 `SessionStateReconnecting` 与 `HealthStateError`。
- **设计策略**：
  - 维护局部时间戳 `lastReportAt time.Time`。
  - 在遇到关键帧（`pkt.KeyFrame == true`）或距离上次上报时间超过 `1 * time.Second` 时：
    1. 调用 `d.hub.registry.RecordStreamSuccess(d.cameraID, 0, d.role, EvidencePacketActivity)`。
    2. 若当前流健康状态此前处于非 Online 状态（如刚从重连或错误中恢复），触发 `d.hub.BroadcastState(state)` 广播恢复事件；若本就是正常推流态，仅静默更新时间戳与计数，避免每秒向 SSE 客户端广播无状态变更的冗余事件。

### 2.2 StreamHub 并发拉流 Singleflight 防击穿 (`StreamHub`)

- **问题根源**：
  多协程并发调用 `GetOrCreateDispatcher` 时，在首次查 map 未命中后释放了锁，直接并发执行 DB 查询、解密 URI 以及 `engine.AcquireStream`，导致对同一路流重复建连。
- **设计策略**：
  - 引入 `golang.org/x/sync/singleflight.Group`。
  - 快速路径：先在 `h.mu` 读锁/互斥锁下读取已有分发器；若已存在且未关闭则直接返回。
  - 慢速路径：通过 `h.flight.Do(key, ...)` 执行拉起逻辑：
    - 在回调内部再次执行检查，确保 double-check。
    - 执行 DB 查询、解密、`h.engine.AcquireStream` 与 `Subscribe`。
    - 组装 `StreamDispatcher`，写入 `h.dispatchers[key]`。
    - 更新状态为 `SessionStateRunning` 并向 `EventHub` 广播。
    - 启动 `RunPumpLoop` 协程。
  - 任何并发请求在 `h.flight.Do` 阻塞等待，合并获取同一个分发器句柄，彻底杜绝拉流击穿。

### 2.3 SessionState 切换的实时 SSE 广播 (`StreamHub` & `StreamDispatcher`)

- **问题根源**：
  `StreamHub` 目前只持有了 `StateRegistry`，未持有 `EventHub`。会话切为 `running` 或 `idle` 时未调用 `EventHub.BroadcastChange`。
- **设计策略**：
  - `StreamHub` 增加 `eventHub *EventHub` 字段；`NewStreamHub` 接收 `eventHub`，同时更新 `internal/app/app.go` 中的依赖注入装配。
  - 在 `StreamHub` 暴露辅助方法 `broadcastState(state *CameraStateInfo)`。
  - 当流首次拉起成功置为 `SessionStateRunning` 时广播。
  - 当流所有客户端注销、分发器在 `Close()` 中切回 `SessionStateIdle` 时广播。

### 2.4 HealthScheduler 快速优雅停机 (`HealthScheduler`)

- **问题根源**：
  `checkStreamAsync` 中的探测上下文使用的是 `context.WithTimeout(context.Background(), DefaultProbeTimeout)`。如果系统执行 `Stop()` 时有处于网络挂起的探测，`s.wg.Wait()` 会最多阻塞 5 秒。
- **设计策略**：
  - 在 `HealthScheduler` 内部持有 `rootCtx context.Context` 与 `cancelRoot context.CancelFunc`。
  - 在 `Stop()` 开始时立即执行 `s.cancelRoot()`。
  - 异步探测使用 `ctx, cancel := context.WithTimeout(s.rootCtx, DefaultProbeTimeout)`。
  - `s.cancelRoot()` 触发后，所有正在握手或等待响应的连接被主动打断，`s.wg.Wait()` 瞬间返回，停机时延降至 <10ms。

---

## 3. 兼容性与错误处理

- **错误传递**：`singleflight.Do` 返回的错误透明透传给调用方；若拉流失败，不污染缓存，下次请求可正常重试。
- **CGO 与原生层安全**：无新增 CGO 调用；`StreamDispatcher` 关闭时按现有契约正常退订 `sub.Close()` 与 `stream.Close()`。
- **并发锁顺序**：`StreamHub.mu` 不与 `EventHub.mu` 发生反向嵌套锁定。
