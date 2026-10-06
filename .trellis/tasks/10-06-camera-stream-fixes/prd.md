# 摄像机流转管道与调度状态时序缺陷修复

## Goal

修复摄像机流转管道中的四个关键缺陷：活跃流收包证据未上报导致的 4s 误判超时、StreamHub 并发拉流防击穿与资源浪费、Session 状态切换未向 SSE 广播、以及调度器停机时在途网络请求阻塞导致的慢关机。

## Requirements

1. **活跃流收包证据节流刷新 (Packet Activity Throttling)**：
   - 在 `StreamDispatcher.RunPumpLoop` 中消费到视频包时，必须刷新流状态的证据类型为 `EvidencePacketActivity` 并更新 `LastCheckedAt`。
   - 为避免 25～30 fps 逐包获取全局互斥锁引起竞争，必须采用节流上报策略：在遇到关键帧（`pkt.KeyFrame`）或距离上次上报超过 1 秒时上报一次。
   - 在视频流正常播放期间，后台活跃监控循环（`monitorActiveStreams`）不得误判 `packet_timeout`。

2. **StreamHub 并发拉流 Singleflight 保护 (Thundering Herd Protection)**：
   - 在多客户端（例如多标签页或多宫格视口）并发请求同一路码流（`${cameraId}:${role}`）时，通过并发协同（`singleflight.Group`）合并底层拉流请求。
   - 严禁并发触发多次底层 C++ Native `AcquireStream` 与 `Subscribe`，避免摄像机 RTSP 连接峰值冲击与多余句柄浪费。
   - 确保错误时能够正常向所有等待者返回错误，并在流关闭后能够安全重新按需拉流。

3. **Session 状态切换实时广播 (SessionState SSE Event Propagation)**：
   - `StreamHub` 与 `EventHub` 正确联动（在依赖注入中装配 `*EventHub`）。
   - 在首个客户端连入（切为 `SessionStateRunning`）与所有客户端断开流关闭（切为 `SessionStateIdle`）时，调用 `hub.BroadcastChange(state)` 将状态推送到 SSE 长连接客户端。
   - 前端大盘监控无需等待 60s 定时探活即可实时看到推流状态变化。

4. **HealthScheduler 优雅停机级联取消 (Graceful Stop Cascading)**：
   - `HealthScheduler` 内部维护可取消的 Context（`ctx, cancel`）。
   - 调用 `Stop()` 时立即取消该 Context，使所有进行中的异步 RTSP 连接探测立即释放，消除最长 5 秒的停机挂起等待。

5. **单元测试与竞态防护 (Test & Race Detector Compliance)**：
   - 增加针对性单元测试，覆盖收包证据更新、并发 Singleflight 拉流复用、SessionState SSE 广播及快速优雅停机。
   - 确保 `go test -race ./internal/camera/...` 全部通过，无数据竞态与内存泄漏。

## Acceptance Criteria

- [x] 活跃流拉流 4 秒以上，调度器不会将其误切为 `reconnecting` / `error`。
- [x] 并发 5 个 goroutine 同时调用 `GetOrCreateDispatcher` 请求同一路流，底层 `AcquireStream` 仅调用 1 次。
- [x] 客户端连入与断开时，`EventHub` 能接收到对应的 `SessionStateRunning` 与 `SessionStateIdle` 广播事件。
- [x] `HealthScheduler.Stop()` 能够在有在途探活请求时在 100ms 内快速完成退出。
- [x] 所有 `internal/camera` 的单元测试通过 `-race` 竞态检测。
