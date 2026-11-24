# Tasks: 2026-11-24-alertpush-delivery-reliability

> Quick Path：修复 `internal/alertpush` 三项投递可靠性问题（Webhook 超时预算截断、MQTT Connect 超时误判成功与持锁阻塞 Close、`publishBytes` 泄漏 `time.After` Timer）。
> 下文 `W` 为 `python3 native/scripts/build.py go`。

## 1. 实施任务

- [x] 1.1 (deps: none) 在 `internal/alertpush/sender.go`、`internal/alertpush/sender_webhook.go`、`internal/alertpush/dispatcher.go` 引入包内可选接口 `timeoutBudgets`（`EventSendBudget()` / `HeartbeatSendBudget()`），由 `WebhookSender` 按 `3*timeout + 2s`（事件）与 `max(10s, timeout+1s)`（心跳）提供预算，`destinationWorker.runEvents` / `runHeartbeats` 使用该预算（未实现时回退默认 10s）；在 `internal/alertpush/dispatcher_test.go` 与 `internal/alertpush/sender_webhook_test.go` 增加预算计算与慢响应不被 10s 截断的单测（check: `W test -race -count=1 ./internal/alertpush -run 'TestWebhookSender.*Budget|TestDispatcher.*Budget'`）
- [x] 1.2 (deps: none) 修复 `internal/alertpush/sender_mqtt.go`：`MQTTSender` 新增 `closeCh` 与可配 `ConnectTimeout`（默认 5s）；`Connect()` 在释放 `s.mu` 后等待 `token.Done()` / `closeCh` / `time.NewTimer(connectTimeout)`，超时返回包裹 `ErrMQTTNotConnected` 的错误，且 `Close()` 不再被在飞连接阻塞；将 `publishBytes` 中的 `time.After(publishTimeout)` 替换为 `time.NewTimer` + `defer timer.Stop()`；在 `internal/alertpush/sender_mqtt_test.go` 增加连接超时返回错误、在飞连接时 `Close()` 立即返回及 publish 超时测试（check: `W test -race -count=1 ./internal/alertpush -run 'TestMQTT'`）
- [x] 1.3 (deps: 1.1, 1.2) 全量运行 `internal/alertpush` 静态检查与 `-race` 测试，确认无数据竞争与回归（check: `gofmt -l internal/alertpush && W vet ./internal/alertpush && W test -race -count=1 ./internal/alertpush`）
