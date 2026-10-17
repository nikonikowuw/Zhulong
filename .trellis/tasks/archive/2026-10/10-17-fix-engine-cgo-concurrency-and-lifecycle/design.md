# 技术方案设计：修复原生引擎 CGO 桥接并发安全与生命周期缺陷

## 1. 架构与边界

本设计聚焦于 Go 宿主层 `internal/engine` 与 Native C ABI（`native/include/Zhulong/engine.h`）之间的桥接与状态机管理：
- `Engine`：持有底层 `C.Zhulong_engine_h` 不透明句柄，控制引擎生命周期。
- `Stream`：消费者物理流引用包装。
- `Subscription` / `AIResultSubscription`：视频压缩包与 AI 结构化结果订阅。

## 2. 核心缺陷设计与修复策略

### 2.1 读写锁保护底层句柄生命周期（防止 UAF）

**现状风险**：
各 CGO 调用点（如 `getStreamStatus`, `subscribe`, `unsubscribe`, `getBrokerPath`, `setPublishRate`, `releaseStream`, `BroadcastAIMessage`）在 `e.mu` 内读取 `handle := e.handle`，释放锁后跨入 CGO 调用。在此期间，若有并发 goroutine 调用 `Engine.Close()`，`C.Zhulong_engine_destroy(handle)` 会在底层直接 `delete engine`，导致在途 CGO 调用访问野指针。

**解决方案**：
在 `Engine` 中引入 `handleMu sync.RWMutex`：
1. **执行读操作**：任何需要向 C 传递 `e.handle` 的操作在发起前获取 `handleMu.RLock()`，在 CGO 调用彻底返回后 `defer handleMu.RUnlock()`。在获取 `RLock()` 之后，再加 `e.mu.Lock()` 检查 `e.handle != nil && e.running && !e.stopping` 及代次 `generation`。若校验失败，立即释放读锁并返回 `ErrNotRunning` 或 `ErrStaleResource`。
2. **执行销毁写操作**：`Engine.Close()` 在调用 `stopInternal()` 停机完成后，获取 `handleMu.Lock()`（排空所有在途 CGO 调用），然后安全执行 `C.Zhulong_engine_destroy(handle)` 并将 `e.handle = nil`，最后释放写锁。
3. **闭环保证**：绝无任何 CGO 调用能在 `Zhulong_engine_destroy` 期间或之后执行。

### 2.2 `Stream.Close` 原子读取订阅对象（消除 Data Race）

**现状风险**：
`Stream.Close` 在 `s.mu.Lock()` 临界区内仅读取了 `sub := s.activeSub`，但在临界区外读取 `s.activeAISub`。而 `onAISubClosed` 与 `SubscribeAIResults` 在 `s.mu` 保护下对 `s.activeAISub` 写入，构成并发数据竞争。

**解决方案**：
在 `Stream.Close` 的 `closeOnce.Do` 闭包中，将两个订阅引用的读取与清空全部纳入 `s.mu.Lock()`：
```go
s.mu.Lock()
s.closed = true
sub := s.activeSub
aiSub := s.activeAISub
s.activeSub = nil
s.activeAISub = nil
s.mu.Unlock()

if sub != nil {
    _ = sub.Close()
}
if aiSub != nil {
    _ = aiSub.Close()
}
```

### 2.3 `cgo.Handle` 延迟安全回收（杜绝停机 Panic）

**现状风险**：
Native 引擎在 `Engine::stop()` 期间先置 `running = false`，随后逐一 `join()` 物理流工作线程与 `ai_subscriptions`。若退订调用在此期间发生，Native 抛出 `Zhulong_ERR_NOT_RUNNING`。Go 侧若立即调用 `deleteToken()`，正在汇合过程中的工作线程若仍有瞬时残余回调触发，调用 `h.Value()` 会触发 `panic("misuse of an invalid Handle")`。

**解决方案**：
1. **即时排空注销**：仅在 Native 返回 `Zhulong_OK`（Native 保证同步排空）或 `Zhulong_ERR_NOT_FOUND`（底层已无此流/订阅，断无残留回调）时，立即调用 `deleteToken()`。
2. **停机排空注销**：当 Native 返回 `Zhulong_ERR_NOT_RUNNING` 时，将 `deleteToken` 闭包暂存至 `Engine.pendingTokens` 队列。
3. **汇合收口**：在 `Engine.stopInternal()` 中，当 `C.Zhulong_engine_stop(handle)` 执行完毕后（所有 Native Worker 线程已全部 join 汇合且无回调可能），统一排空并执行 `e.pendingTokens` 中的所有注销函数。

### 2.4 `AIResultSubscription` 运行态流断线与失败感知

**现状风险**：
`AIResultSubscription` 缺乏流状态轮询闭环。当物理 RTSP 流因网络中断进入 `StreamFailed` 状态时，消费者的 `Next(ctx)` 将一直阻塞，无法感知流已失败。

**解决方案**：
为 `AIResultSubscription` 配备对等的 `runControlLoop`（周期 250ms），通过 `s.stream.Status(ctx)` 监控底层物理流状态：
- 当检测到底层流状态为 `StreamFailed` 时，置位 `s.err = status.Error`，将订阅置为 `closed`，关闭 `s.done` 并唤醒正在 `Next` 阻塞的读取协程。
- 当 `ctrlCtx.Done()` 或 `s.done` 触发时，优雅退出监控循环并关闭订阅。

### 2.5 输入防御与协议契约同步

1. **图像炸弹防御**：
   在 `ExtractFaceFeature` 中定义常量 `MaxFaceImageDimension = 4096`。在获取 `img.Bounds()` 后，校验 `w > MaxFaceImageDimension || h > MaxFaceImageDimension`，超限立即返回 `ErrInvalidArgument`，防止巨额像素切片分配。
2. **IPC 最大载荷常量复用**：
   `BroadcastAIMessage` 引入 `github.com/nikonikowuw/Zhulong/internal/algoipc`，载荷上限校验使用 `algoipc.MaxIpcPayloadSize`，消除硬编码。
3. **哨兵错误测试补全**：
   在测试中针对所有导出的 `Err*` 哨兵对象增加 `errors.Is(mapNativeStatus(..., code), Err...)` 覆盖，确保状态码契约稳定。
