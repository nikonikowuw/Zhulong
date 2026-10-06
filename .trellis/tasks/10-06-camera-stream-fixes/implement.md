# 摄像机流转管道与调度状态时序缺陷执行计划

## Execution Checklist

- [x] 1. **实现收包证据节流刷新 (`stream_dispatcher.go`)**
  - 在 `RunPumpLoop` 内部维护 `lastReport time.Time`。
  - 在遇到关键帧或时间差 >= 1s 时调用 `d.hub.recordPacketActivity(d.cameraID, d.role)`。
  - 确保仅在状态由异常恢复为 Online 时触发 SSE 广播，平稳运行时不刷屏。

- [x] 2. **改造 StreamHub 增加 Singleflight 并接入 EventHub (`stream_hub.go`)**
  - 结构体增加 `eventHub *EventHub` 和 `flight singleflight.Group`。
  - `NewStreamHub` 增加 `eventHub *EventHub` 参数。
  - 改造 `GetOrCreateDispatcher`：快速读锁 -> `flight.Do` 单飞合并并发拉流 -> 状态切为 `SessionStateRunning` 并广播。
  - 在 `StreamDispatcher.Close` 释放时切为 `SessionStateIdle` 并广播。

- [x] 3. **更新应用装配依赖 (`internal/app/app.go`)**
  - 在 `newServices` 中为 `camera.NewStreamHub` 传入已初始化的 `hub *EventHub`。

- [x] 4. **优化 HealthScheduler 优雅停机级联 (`scheduler.go`)**
  - 结构体增加 `rootCtx context.Context` 与 `cancelRoot context.CancelFunc`。
  - 在 `Stop()` 开头调用 `s.cancelRoot()`。
  - 异步探测采用 `context.WithTimeout(s.rootCtx, DefaultProbeTimeout)`。
  - 并在 `DescribeClient.Describe` 中通过 goroutine + `conn.SetDeadline(time.Now())` 监听 `dialCtx.Done()` 打断阻塞 socket。

- [x] 5. **编写与更新单元测试套件**
  - 在 `stream_hub_test.go` 中增加并发多协程同时拉流的 Singleflight 去重测试。
  - 增加流启停时向 `EventHub` 成功广播变化的验证测试。
  - 验证长时间推流下活跃监控不会误杀超时。
  - 验证 `scheduler.Stop()` 在有耗时探活时可毫秒级快速退出。

- [x] 6. **运行全套竞态检测与语法静态分析**
  - `python3 native/scripts/build.py go test -race -v -count=1 ./internal/camera/...`
  - `python3 native/scripts/build.py go vet ./internal/camera/... ./internal/app/...`
