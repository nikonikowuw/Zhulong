# Verify Contract: 2026-11-24-alertpush-delivery-reliability

> Propose 阶段确立的静态验收契约。Verify 阶段仅对照本契约与 `base_commit`（`544f56a`）diff 输出结论到 `verify-report.md`，严禁修改本契约或业务源码。

## 验收标准

- [ ] V1（Webhook 事件/心跳超时不被 10s 截断）：`WebhookSender` 实现 `EventSendBudget()`（≥ `3*timeout + 2s`）与 `HeartbeatSendBudget()`（≥ `max(10s, timeout+1s)`）；`destinationWorker.runEvents` 与 `runHeartbeats` 按 `sender` 声明的预算创建请求超时上下文，未实现接口的 sender 维持默认 `10s`。有单测覆盖 `timeout=15s`/`30s` 预算值及慢重试不被 10s 截断。
- [ ] V2（MQTT `Connect()` 超时不返回 nil 且不持锁阻塞 `Close()`）：`MQTTSender.Connect()` 在不持有 `s.mu` 的状态下等待连接完成，超过 `connectTimeout`（默认 5s）返回非 nil 错误（`errors.Is(err, ErrMQTTNotConnected)` 为 true），不再静默返回 `nil`；连接挂起期间调用 `Close()` 能在远小于 `connectTimeout` 的时间内返回并解除挂起的 `Connect()`。有 `-race` 单测覆盖超时错误与并发 `Close()`。
- [ ] V3（`publishBytes` 消除 `time.After` 泄漏）：`internal/alertpush/sender_mqtt.go` 中不再出现 `time.After`，`publishBytes` 使用 `time.NewTimer` + `defer timer.Stop()` 并在超时时返回包裹 `ErrMQTTPublishFailed` 的错误。
- [ ] V4（代码规范与全包通过）：`gofmt -l internal/alertpush` 输出为空，`python3 native/scripts/build.py go vet ./internal/alertpush` 与 `python3 native/scripts/build.py go test -race -count=1 ./internal/alertpush` 全部通过，无跳过或弱化既有断言。

## 负向边界（不得发生）

- 不得修改对外载荷格式、HTTP 路由、数据库迁移、前端代码、或 `internal/alertpush` 以外的任何业务包。
- 不得改变既有重试次数（3 次）、退避曲线、队列容量（16）与溢出丢最旧策略、以及停机排空 3s deadline。
- 不得引入 `_ = fn()` 吞错、业务路径 `panic` 或新增 `context.Context` 结构体字段。

## 验证命令

```bash
gofmt -l internal/alertpush
python3 native/scripts/build.py go vet ./internal/alertpush
python3 native/scripts/build.py go test -race -count=1 ./internal/alertpush
```
