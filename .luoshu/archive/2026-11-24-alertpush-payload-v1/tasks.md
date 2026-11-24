# Tasks: 2026-11-24-alertpush-payload-v1

> 自底向上拓扑：契约/迁移 → 载荷构造 → 分发与发送器 → 配置校验与装配 → 前端 → 全量门禁。
> `internal/alertpush` 经 `record` 间接依赖 `internal/engine`，Go 测试一律走 native wrapper 且带 `-race`。
> 下文 `W` 为 `python3 native/scripts/build.py go`。

## 1. 契约与存量数据

- [x] 1.1 (deps: none) 新增迁移 `internal/database/migrations/000040_alert_filter_event_type_v1.up.sql` / `.down.sql`：用 `json_each`/`json_set` 仅改写 `alert_destinations.filter_json` 的 `$.event_types` 数组，`"face_recognition"`→`"face"`、`"plate_recognition"`→`"vehicle"`（down 反向）；在 `internal/database/store_test.go` 增加 up/down 对称用例（插入含旧名、新名与无过滤的三行，断言改写与回滚结果）（check: `go test -race ./internal/database/... -run 'TestMigration|TestAlertFilterEventTypeMigration'`）
- [x] 1.2 (deps: none) 新建 `internal/alertpush/payload.go`：v1 类型、`SchemaVersion`、事件类型与图片角色常量、`formatPayloadTime`、`externalEventType`、`Delivery`、`SourceResolver`（本任务只新增，不改 `Sender` 接口、不删旧类型，保证包可编译）；`payload_test.go` 覆盖 `externalEventType` 全取值（含未知值回退 `alarm`）与时间格式（check: `W test -race ./internal/alertpush -run 'TestExternalEventType|TestFormatPayloadTime'`）

## 2. 载荷构造

- [x] 2.1 (deps: 1.2) 在 `payload.go` 实现 `buildEventPayload(d Delivery, deviceID string, sentAt time.Time) EventPayload`：三类子对象映射、`null` 缺省策略、全零 box→null、`maps.Clone` 后剔除车辆外提键、`ev.Alarm==nil` 回退扁平字段；表驱动 `payload_test.go` 覆盖 alarm/face 匹配与未匹配/vehicle 有无车牌/空 device_id/nil SourceInfo，并含多协程并发对同一事件构造的 `-race` 用例（check: `W test -race ./internal/alertpush -run 'TestBuildEventPayload'`）
- [x] 2.2 (deps: 1.2) 实现 `buildHeartbeatEnvelope(snap, deviceID, seq, sentAt)` 与 `buildOfflineEnvelope(reason, deviceID, seq *uint64, sentAt *time.Time)`（LWT 传 nil）；用例断言 `heartbeat`/`offline` 子对象、`source=null`、`images=[]`、LWT 的 `seq`/`sent_at` 为 null（check: `W test -race ./internal/alertpush -run 'TestBuildHeartbeatEnvelope|TestBuildOfflineEnvelope'`）
- [x] 2.3 (deps: 2.1) 在 `sender.go` 新增 `resolveImageURLs`（与旧 `populateSignedURLs` 并存至 3.4）：按 scene→object→reference 产出带 `url`/`url_expires_at`/`size_bytes` 的 `[]ImageInfo`，签名/预签名失败的角色不出现，预签名命中时 `size_bytes=null` 且不打开远端对象；在 `presign_test.go` 新增角色顺序、失败剔除与 `size_bytes` 用例（check: `W test -race ./internal/alertpush -run 'TestResolveImageURLs|TestPresign'`）
- [x] 2.4 (deps: none) MQTT 内联预算配置：`model.go` 的 `mqttConfig.InlineMaxKB`（`inline_max_kb`，0→默认 500）、`responses.go` 的 `AlertDestinationConfigInfo.InlineMaxKB *int`（`inlineMaxKb`），在 `handler.go` 创建 / 更新 / 测试 / 响应路径透传并校验 `64..8192`（越界 422 `ALERT_DESTINATION_CONFIG_INVALID`），`dispatcher.go`/`service.go` 构造 MQTT sender 时传入 `MQTTSenderOptions.InlineMaxBytes`；`handler_test.go` 覆盖默认、边界值 63/64/8192/8193 与 webhook 忽略该字段（check: `W test -race ./internal/alertpush -run 'TestHandler|TestInlineMaxKB'`）

## 3. 分发与发送器

- [x] 3.1 (deps: 2.1) `Sender.Send` 签名改为 `Send(ctx, d Delivery) error`，两个发送器先以 `d.Event` 适配保持旧行为可编译；`dispatcher.go` 队列元素改为 `queuedEvent{ev, seq}`，`Dispatch` 匹配后先 `counterFor(id).Add(1)` 再入队；`Config.Source SourceResolver`，`runEvents` 出队后以独立 2s ctx 解析来源并构造 `Delivery`；过滤器使用 `externalEventType`；`dispatcher_test.go` 增加：溢出丢弃产生 seq 缺口、心跳与事件共用计数器无重复、resolver 阻塞不影响 `Stop` 收敛（check: `W test -race ./internal/alertpush -run 'TestDispatcher'`）
- [x] 3.2 (deps: 2.2, 2.3, 3.1) `sender_webhook.go`：JSON 模式与 multipart 模式（多 part `image_<role>`、载荷 `part` 字段、可选并带 `url`、所有已打开文件在各错误路径关闭）；`X-Zhulong-Event/Delivery/Schema` 头加入 `reservedHeaders` 并在事件与心跳请求上设置；重试复用同一 `id`/`seq`、`sent_at` 重算；`sender_webhook_test.go` 用 `httptest` 解析请求体与 multipart 断言（check: `W test -race ./internal/alertpush -run 'TestWebhook'`）
- [x] 3.3 (deps: 2.2, 2.3, 2.4, 3.1) `sender_mqtt.go`：base64 模式以 `InlineMaxBytes` 为原始字节总预算，按 object → reference → scene 装入（列表输出顺序仍为 scene → object → reference），读取用 `io.LimitReader(剩余+1)`，超预算回退 url，无 url 则剔除并 Warn 记录 `role`/`size_bytes`；删除常量 `maxMQTTBase64Bytes`；url 模式复用 `resolveImageURLs`；LWT（构造期生成 `id`）与优雅离线改用 `buildOfflineEnvelope`；更新 `sender_mqtt_test.go`：4K 场景（scene 2MB + object 60KB，预算 500KB）→ object 内联、scene 仅 url；无 `PublicBaseURL` 时 scene 不出现；预算恰好等于总和时全部内联（check: `W test -race ./internal/alertpush -run 'TestMQTT'`）
- [x] 3.4 (deps: 3.2, 3.3) 删除旧载荷类型（`AlarmPayload`、`HeartbeatPayload`、`*DataPayload`、`mqttOffline*Payload`）、`buildAlarmPayload`、`buildHeartbeatPayload`、`populateSignedURLs` 及其仅存的旧测试；`rg` 确认包内无残留引用（check: `W vet ./internal/alertpush && W test -race ./internal/alertpush`）

## 4. 配置校验与宿主装配

- [x] 4.1 (deps: 1.2) 过滤器 `event_types` 白名单 `alarm|face|vehicle`：在 `service.go`/`handler.go` 创建、更新、测试路径校验，新增 `ErrInvalidFilterEventType` 与稳定码 `ALERT_DESTINATION_EVENT_TYPE_INVALID`（`locale.go` 三语文案），`writeValidationError` 映射 422；`handler_test.go` 覆盖旧名 `face_recognition` 被拒、合法值通过（check: `W test -race ./internal/alertpush -run 'TestHandler|TestAlertDestinationFilter'`）
- [x] 4.2 (deps: 3.4) `service.go` 的 `sendTestProbe` 按 v1 构造 `Delivery`（alarm 子数据、`Seq: 0`），心跳探针走新信封；更新相关测试（check: `W test -race ./internal/alertpush`）
- [x] 4.3 (deps: 3.1, 4.2) `internal/app/adapters.go` 新增 `alertSourceResolver`（`camera.CameraService.Get` + `algorithm.Service.GetAlgorithm`，失败 Debug 日志并降级 null），`internal/app/app.go` 的 `provideAlertPush` 注入 `Config.Source`；`adapters_test.go` 覆盖查到 / 查不到 / 空 algorithmID（check: `W test -race ./internal/app -run 'TestAlertSourceResolver|TestProvideAlertPush'`）

## 5. 前端

- [x] 5.1 (deps: 4.1) `web/src/features/alert-push/components/form/destination-filters-section.tsx` 选项 `type` 改为 `face`/`vehicle`，`locales/{en,zh-Hans,zh-Hant}.json` 文案键 `eventTypes.face*`/`eventTypes.vehicle*` 重命名；更新受影响的 `alert-destination-dialog.test.tsx` 断言（check: `cd web && pnpm test -- src/features/alert-push`）
- [x] 5.2 (deps: 2.4, 5.1) MQTT 表单内联上限：`api/alert-push-api.ts` 与 `data/schema.ts` 增加 `inlineMaxKb`（Zod 整数 64..8192，可选，默认 500），`components/form/mqtt-config-section.tsx` 仅在 base64 模式显示该字段及 broker 上限说明（AWS IoT 128KB / Azure 256KB / EMQX 1MB），三语 `imageModes.base64Desc` 改为「小图优先内联，超限图片以 URL 提供（需配置 public_base_url），否则省略」；`schema.test.ts` 覆盖边界与默认、对话框测试覆盖字段显隐（check: `cd web && pnpm test -- src/features/alert-push`）

## 6. 收尾门禁

- [x] 6.1 (deps: 1.1, 4.3, 5.2) 若 handler godoc 有变更执行 `make api-docs`；全量门禁一次通过（check: `make check`）
