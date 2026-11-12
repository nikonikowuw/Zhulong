# 实施计划：外部平台存活上报与推送双能力通道

## 1. 有序实施清单 (Ordered Implementation Checklist)

### Step 1: 数据库迁移与持久层演进（`internal/database` & `internal/alertpush`）
- [ ] 新增 `internal/database/migrations/000035_add_alert_destination_capabilities.up.sql` 与对称表重建 `.down.sql`；
- [ ] 在 `internal/database/store_test.go` 增加 `000035` 迁移对称性与存量行默认值（`events_enabled=1, heartbeat_enabled=0`）单测；
- [ ] 扩展 `internal/alertpush/model.go`、`store.go`：`AlertDestination` 增加 `EventsEnabled` / `HeartbeatEnabled`，`webhookConfig` / `webhookSecret` / `mqttConfig` 增加存活配置与 `HeartbeatURL` 密文字段。

### Step 2: 子模块轻量统计接口与宿主快照适配器（`internal/algorunner`、`internal/task`、`internal/app`）
- [ ] 在 `internal/algorunner/supervisor.go` 新增只读 `SummaryCounts() (active, failed int)` 及单测；
- [ ] 在 `internal/task` 新增 `CountEnabledTasks(ctx context.Context) (int, error)` 及单测；
- [ ] 在 `internal/app/adapters.go` 实现统一的设备健康快照组装器（聚合 `systeminfo`、`storage`、`camera`、`algorunner`、`task`、`engine`、`LogSink` 与 `alertpush.public_base_url`，统一计算 `status` 与稳定 `reasons`），编写降级判定表驱动单测。

### Step 3: `internal/alertpush` 双轨调度、同源防护与 MQTT LWT
- [ ] 扩展 `sender.go`、`sender_webhook.go`、`sender_mqtt.go`：
  - 定义 `HeartbeatSnapshot` / `HeartbeatPayload`（`snake_case`，空切片序列化为 `[]`）；
  - Webhook 支持 `HeartbeatURL` 路由覆盖与单次无重试 POST；
  - MQTT 支持 `HeartbeatTopic` 路由覆盖、`SetWill`（`connection_lost`）与 `Close()` 500ms 有界 `graceful_shutdown` 发布；
- [ ] 扩展 `service.go`、`requests.go`、`responses.go`、`handler.go`、`locale.go`：
  - 支持三类能力组合校验、纯存活目标豁免 `public_base_url`、`sameOrigin` 防凭据跨站泄露校验；
  - 适配 `Test`：纯存活目标发送真实即时 `heartbeat` 探测；
  - 编辑回显含 `heartbeatUrl` 时确保 `Cache-Control: no-store`；
- [ ] 扩展 `dispatcher.go`：
  - `Dispatch` 跳过 `!w.eventsEnabled` 目标；
  - `destinationWorker` 增加带微抖动的独立心跳循环、按目标持久单调 `seq`、连续失败日志降噪及 `device_id` 漂移触发 MQTT LWT 重建；
  - `computeFingerprint` 纳入新增字段；
- [ ] 补齐 `internal/alertpush/*_test.go`（能力组合、跨源拦截、双轨不阻塞、MQTT LWT/优雅下线、零审计污染）。

### Step 4: `internal/openapi` 状态下拉端点与文档契约
- [ ] 在 `internal/openapi` 新增 `GET /status`（`/api/v1/openapi/status`）与 `DeviceStatusResponse`（`camelCase`，`reasons` 恒非 `nil`），严禁挂载 `audithttp.Tag` 或录脸 `UploadLimiter`；
- [ ] 在 `internal/app/app.go` 将宿主状态适配器同时接线至 `alertpush` 与 `openapi`；
- [ ] 新增 `docs/openapi/device-status.md`（同步说明 `GET /api/v1/openapi/status` 契约），更新 `internal/openapi/contract_test.go` 覆盖新路由与文档扫描；
- [ ] 运行 `make api-docs` 同步 `internal/apidocs/`，补齐 `internal/openapi/handler_test.go` 与 `internal/app/openapi_wiring_test.go`（含 401/200、空数组契约、限流隔离与零审计新增断言）。

### Step 5: 前端 `web/src/features/alert-push` 重构与测试
- [ ] 更新类型与 Zod Schema（`web/src/features/alert-push/types/` & 表单 schema）：纳入 `eventsEnabled`、`heartbeatEnabled`、`heartbeatIntervalSeconds`、`heartbeatUrl`、`heartbeatTopic`、`lwtEnabled`，实现双关拦截与前端同源即时校验；
- [ ] 更新新建/编辑弹窗（`alert-destination-dialog.tsx` 与 `form/` 子组件）：拆分为基础连接、感知事件开关区、设备存活上报开关区；
- [ ] 更新目标列表行（`alert-destination-table-row.tsx`）：展示能力标签，纯存活目标队列列显示 `—`；
- [ ] 更新 `zh-Hans.json`、`zh-Hant.json`、`en.json` 三语文案，明确区分 MQTT 协议层 KeepAlive 与业务层设备存活上报间隔；
- [ ] 补充/更新 `alert-destination-dialog.test.tsx` 浏览器模式测试。

---

## 2. 验证命令与质量门禁 (Validation Commands)

> ⚠️ 注意：`internal/app` 间接依赖 `internal/engine`，必须经 `native/scripts/build.py` 包装执行，严禁跑裸 `go test`！

```bash
# 1. 数据库迁移对称性与默认值测试
go test -race ./internal/database -run TestMigration000035

# 2. 告警推送、任务、Runner 与 OpenAPI 单测
go test -race ./internal/alertpush/... ./internal/openapi/... ./internal/algorunner/... ./internal/task/...

# 3. Swagger 文档生成与契约校验（必须在跑 openapi 契约测试前重生成）
make api-docs
go test -race ./internal/openapi -run TestContract

# 4. 组合根接线与快照聚合单测（走 native wrapper）
python3 native/scripts/build.py go test -race ./internal/app -run "TestOpenAPI|TestStatusSnapshot|TestAlertPush"

# 5. 前端类型检查、Lint 与真实无头浏览器测试
cd web && pnpm check && pnpm test -- src/features/alert-push

# 6. 全量门禁与内嵌静态资源同步核对
make check
```

---

## 3. 风险文件与回滚点 (Risky Files & Rollback Points)

- **`internal/database/migrations/000035_*`**：尚未合入交付前可调整，但必须通过 `.up.sql -> .down.sql -> .up.sql` 往返验证且不丢失存量行与索引。
- **`internal/alertpush/dispatcher.go`**：涉及并发 Worker 生命周期与 `sender.Close()`，必须确保 `run()` 与 `runHeartbeatLoop()` 全部汇合后再关闭 `Sender`（`-race` 必过）。
- **`internal/openapi/contract_test.go` & `internal/apidocs/`**：新增 `/openapi/status` 后若漏跑 `make api-docs` 或未同步 `docs/openapi/`，契约单测会直接失败，将其作为 Step 4 的硬门禁。
