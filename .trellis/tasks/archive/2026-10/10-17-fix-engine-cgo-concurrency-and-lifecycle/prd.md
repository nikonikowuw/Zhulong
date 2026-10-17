# 修复原生引擎 CGO 桥接并发安全与生命周期缺陷

## Goal

全面修复 `internal/engine/` 中存在的并发数据竞争（Data Race）、引擎关闭与并发 CGO 调用的生命周期悬挂（Use-After-Free 风险）、未排空状态下提前注销 `cgo.Handle` 导致 panic、AI 结果流缺少流异常中断感知机制，以及图像解码防御性门限等规范缺陷。

## Requirements

1. **消除 `Stream.Close` 与订阅注册的数据竞争**
   - 在 `Stream.Close()` 内原子地在 `s.mu` 临界区内读取并置空 `s.activeSub` 与 `s.activeAISub`，彻底消除 `-race` 报告的数据竞争。
2. **保障 `Engine.Close()` 与并发 CGO 调用的生命周期安全**
   - 保证在 `Engine.Close()` 调用 `C.Zhulong_engine_destroy(handle)` 销毁底座前，等待所有正在执行的并发 CGO 调用安全退出。
   - 处于已停止或正在关闭状态时，新的 CGO 调用必须被拦截并快速返回 `ErrNotRunning`，防止向已释放或正在析构的 Native 引擎句柄发起调用。
3. **安全排空注销 `cgo.Handle` 令牌**
   - 针对 `unsubscribe` 和 `unsubscribeAIResults` 在 Native 停机时返回 `Zhulong_ERR_NOT_RUNNING` 的情况，严禁在 Native 工作线程未完全 join 排空前注销 `cgo.Handle`。
   - 所有在 Native 停机期间产生的未排空 token，收敛至引擎停机后统一安全注销，防止回调在残余生命周期内触发 `misuse of an invalid Handle` panic。
4. **增强 `AIResultSubscription` 运行态流失败感知**
   - 为 `AIResultSubscription` 引入物理流运行态监控（或对等的状态联动机制），当底层 RTSP 物理流进入 `StreamFailed` 时及时关闭订阅并透传失败错误，避免消费端永驻阻塞在 `Next()`。
5. **规整协议门限与输入防御**
   - `ExtractFaceFeature` 增加最大图像分辨率保护（如 4096×4096），防止特制小文件大尺寸的图像炸弹在内存中分配超大切片引发 OOM。
   - `BroadcastAIMessage` 统一引用 `algoipc.MaxIpcPayloadSize`，消除硬编码数值与协议契约的潜在漂移。
   - 为 `errors.go` 中所有导出的哨兵错误提供完整的 `errors.Is` 匹配单元测试。

## Acceptance Criteria

- [x] `go test -race ./internal/engine/...` 100% 通过且无任何数据竞争告警。
- [x] 并发调用 `Stream.Close()` 与 `SubscribeAIResults` 时，数据竞争探针完全消除。
- [x] 并发调用 `Engine.Close()` 与状态查询/下行广播等操作时，无内存非法访问与 CGO 崩溃。
- [x] `AIResultSubscription` 在物理流状态变为 `StreamFailed` 时能及时感知退出并返回对应错误。
- [x] `ExtractFaceFeature` 对超限分辨率图片能立即返回 `ErrInvalidArgument` 且不进行大块内存分配。
- [x] `BroadcastAIMessage` 正确遵循 `algoipc.MaxIpcPayloadSize` 门限。
- [x] `make go-check` 验证通过。
