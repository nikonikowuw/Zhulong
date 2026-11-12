# 外部平台存活上报与推送双能力通道

## Goal

补齐 Zhulong 面向外部上级业务中台与运维监控平台的**北向设备存活与健康状态闭环**（主动上报 Push + 专网按需下拉 Pull）：

1. 在 `internal/alertpush` 与 `web/src/features/alert-push` 中，将每个「推送目标（`AlertDestination`）」演进为**「单份连接凭据 + 正交双能力开关（感知事件推送 / 设备存活上报）」**：多平台接入时每个平台只配一次连接与密钥，按需勾选「仅推事件」「仅报存活」或「事件+存活双开」，彻底消除静默期“零告警 ≠ 在线”的盲区与 NAT/4G 单向网络穿透难题；
2. MQTT 通道在同一条 Broker 长连接上支持分流状态 Topic 与 **LWT（遗嘱消息）**，实现拔电/断网秒级离线感知与优雅停机通知；同时在无告警时段持续刷新推送目标自身的 `connected` / `failed` 状态；
3. 在 `internal/openapi` 同步提供只读端点 **`GET /api/v1/openapi/status`**（Bearer Token 鉴权），供专网内已持有 API 凭据的平台零配置按需下拉同一份权威设备健康与工作负载摘要。

---

## Background & Confirmed Facts

1. **北向接口现状与缺口**：
   - `internal/openapi/handler.go:40` 仅提供人员与人脸档案接口（`/api/v1/openapi/persons/*`，`apitoken.RequireBearer` 守护），无设备状态查询端点；
   - `internal/openapi/contract_test.go:93,120,137` 强制要求所有 `/openapi/*` 路由同时对齐 Swaggo 生成文档（`internal/apidocs/`）与 `docs/openapi/` 下的第三方对接指南，且空集合必须序列化为 `[]` 而非 `null`（`contract_test.go:296`）；
   - `internal/app/router.go:123,205` 的公开 `GET /api/v1/health` 仅返回数据库/引擎/日志/解码就绪态，不含 `device_id`、相机在线数、存储写门与温度负载；
   - `internal/alertpush/dispatcher.go:130` 仅由 `record.AlarmEvent` 触发推送；`internal/alertpush/sender_mqtt.go:83` 未配置 `SetWill` 遗嘱。
2. **推送目标持久化与加密边界**：
   - 表结构见 `internal/database/migrations/000030_create_alert_destinations.up.sql`；仓库最新迁移为 `000034_add_feature_vector_to_recognitions.up.sql`，本次迁移编号为 `000035`；
   - 敏感凭据（Webhook URL、Token、密码、附加 Header 值、MQTT 密码）经 `camera.LazyCipher`（AES-256-GCM，AAD = `alert-destination:<id>`）存入 `secret_ciphertext`，并注册 `CameraManager.AddEncryptedDataProbe`（`internal/app/app.go:958`）；
   - `internal/alertpush/service.go:308` 当前只要 `image_mode == "url"` 即强制要求非空 `alertpush.public_base_url`，纯存活目标不发图片时需豁免该限制。
3. **调度、鉴权与前端现状**：
   - `destinationWorker`（`internal/alertpush/dispatcher.go:400`）维护容量 16 的有界事件队列，由 `computeFingerprint`（`dispatcher.go:497`）驱动零重启热重载；
   - `apitoken.RequireBearer`（`internal/apitoken/middleware.go:30`）仅在鉴权失败时写 `api_token.auth_rejected` 审计，鉴权成功的请求若未调用 `audithttp.Tag` 则不写审计表；`touchLastUsed`（`internal/apitoken/service.go:147`）已按每 Token 60s 节流合并落库；
   - 前端特性位于 `web/src/features/alert-push/`（路由 `web/src/routes/_authenticated/settings/alert-push.tsx`）；三语文案已将 MQTT 协议级 `keep_alive_seconds` 译为「心跳周期」（`web/src/features/alert-push/locales/zh-Hans.json:74`），应用层存活功能须使用「设备存活上报」与「上报间隔」避免混淆。
4. **现成零阻塞遥测数据源**：
   - 设备标识：`systeminfo.IdentityService.DeviceID()`（`internal/systeminfo/identity.go`）；
   - 硬件与温度：`systeminfo.Service.GetHardwareStatus`（`internal/systeminfo/service.go:292`，2s 内存缓存）；
   - 存储与写门：`storage.Service.GetStatus` / `CanWrite`（`internal/storage/service.go:212,474`，TTL 内存缓存）；
   - 摄像机状态：`camera.StateRegistry.GetAllStates`（`internal/camera/state.go:122`，内存态）；
   - 解码与宿主就绪：`engine.Engine.GetDecodeStats` / `Ready`、`database.Store.Ready`、`LogSink.Health`（`internal/app/router.go:40,205`）；
   - 算法与任务：`algorunner.Supervisor`（`internal/algorunner/supervisor.go` 内存 watcher 状态）与 `task` 启用任务计数。
5. **架构与审计红线**：
   - `.trellis/spec/backend/logging-guidelines.md` §13：**严禁心跳型审计**——周期性主动上报与 `GET /api/v1/openapi/status` 被动拉取均严禁写入 `audit_logs`；
   - 依赖单向无环：`internal/alertpush` 与 `internal/openapi` 均不得直接导入 `camera` / `task` / `algorunner` / `storage` / `systeminfo` / `engine`，统一通过消费侧只读接口由组合根 `internal/app` 装配注入。

---

## Requirements

### R1: 推送目标双能力模型与数据库迁移（`000035`）
- 新增迁移 `000035_add_alert_destination_capabilities.up.sql` / `.down.sql`，为 `alert_destinations` 增加列：
  - `events_enabled BOOLEAN NOT NULL DEFAULT 1`：是否推送 AI 感知事件（行为告警 / 人脸识别 / 车辆车牌）；
  - `heartbeat_enabled BOOLEAN NOT NULL DEFAULT 0`：是否周期上报设备存活与健康状态摘要。
- **存量零破坏兼容**：升级前已有行自动获得 `events_enabled = true, heartbeat_enabled = false`，行为与推送载荷保持完全不变。
- **组合与前置校验**：
  - 允许「仅事件」「仅存活」「事件+存活双开」三种组合；`events_enabled` 与 `heartbeat_enabled` 同时为 `false` 时返回 422 `ALERT_DESTINATION_CONFIG_INVALID`；
  - 当 `events_enabled = false`（纯存活目标）时，跳过 `image_mode == "url"` 对 `alertpush.public_base_url` 的必填校验；请求未显式提供合法 `imageMode` 时默认落盘 `'url'` 以满足表级 CHECK 约束。

### R2: 同凭据路由分流与跨源防泄密
- **HTTP Webhook 通道**：
  - `config_json` 扩展 `heartbeat_interval_seconds`（默认 `60`，合法范围 `15 ~ 3600` 秒）；
  - `secret_ciphertext`（`webhookSecret`）扩展可选的 `heartbeat_url`：
    - 双开时：`heartbeat_url` 留空则心跳 POST 至主 `url`；非空则心跳 POST 至 `heartbeat_url`，并复用同一套鉴权（`none`/`bearer`/`basic`）与自定义 Headers；
    - 仅存活（`events_enabled = false`）时：若 `heartbeat_url` 留空则直接以主 `url` 为心跳接收地址；
  - **同源防泄密校验**：当 `heartbeat_url` 非空且目标配置了鉴权或自定义请求头（`auth_type != "none"` 或 `len(headers) > 0`）时，校验 `heartbeat_url` 与主 `url` 必须**同源（scheme + host + port 完全一致）**，否则返回 422 拒绝保存，防止存储的凭据在后续编辑中被跨站发往第三方地址；
  - `heartbeat_url` 加密落库，回显接口必带 `Cache-Control: no-store`，日志输出必经 `logsafe.URL` 脱敏。
- **MQTT 通道**：
  - `config_json` 扩展 `heartbeat_interval_seconds`（默认 `60`，范围 `15 ~ 3600` 秒）、可选 `heartbeat_topic`（禁止 `+`/`#` 通配符）与 `lwt_enabled`（默认 `true`，仅在 `heartbeat_enabled = true` 时生效）；
  - 双开时共用**单条** MQTT 连接与同一 `mqtt_client_id`；`heartbeat_topic` 非空时心跳与离线报文发往 `heartbeat_topic`，留空则发往主 `topic`；仅存活时同样以 `heartbeat_topic`（非空）或主 `topic` 为状态主题。

### R3: 双轨隔离调度与状态机联动
- 在 `destinationWorker` 中将事件与心跳拆为共享同一 `Sender` 的两条独立执行轨：
  - **事件轨**：仅当 `events_enabled = true` 时参与 `Dispatcher.Dispatch` 匹配与容量 16 的有界队列消费（保持既有重试与退役排空语义）；
  - **心跳轨**：当 `heartbeat_enabled = true` 时启动独立协程，首包在启动后 `0 ~ min(2s, interval/10)` 随机微抖动后发出，后续按 `heartbeat_interval_seconds` ± 5% 抖动触发，防止多机共振；
  - **最新快照即发即弃**：到点实时采集快照并执行单次有界超时发送，绝不进入 `AlarmEvent` 队列、失败不重试补发历史心跳、不累加 `dropped`。
- 心跳发送结果同步刷新目标的 `state`（`connected` / `failed`）、`lastError` 与 `lastSuccessAt`。
- `computeFingerprint` 纳入 `events_enabled`、`heartbeat_enabled` 及全部新增配置与密文，确保修改后零重启立即重建 Worker。

### R4: MQTT LWT 遗嘱与优雅上下线通知
- 当 MQTT 目标 `heartbeat_enabled = true` 且 `lwt_enabled = true` 时：
  - 连接前在 `mqtt.ClientOptions` 注册 `SetWill(statusTopic, payload, qos, false)`，载荷为 `{"device_id":"...","event_type":"offline","reason":"connection_lost"}`（不带伪造时间戳；`device_id` 修改后可通过触发 Dispatcher 热重载刷新连接遗嘱）；
  - Worker 停止或宿主优雅关闭（`Close()`）断开连接前，在 `500ms` 超时内主动向 `statusTopic` 发布一包 `{"id":"...","device_id":"...","event_type":"offline","reported_at":"...","reason":"graceful_shutdown"}` 后执行 `Disconnect`。

### R5: 权威状态快照与主动推送载荷（`snake_case`）
- 组合根 `internal/app` 统一组装设备健康与工作负载快照（单次耗时上限 `200ms`，子模块暂不可用时安全降级填零值，严禁暴露任何 RTSP URI、凭据或宿主机绝对路径）。
- 主动推送载荷对齐 `AlarmPayload` 使用 **`snake_case`** JSON（单包 < 1KB）：
  - 顶层：`id`（ULID）、`device_id`、`event_type`（`"heartbeat"`）、`seq`（进程启动起单调递增 `uint64`，从 `1` 开始）、`reported_at`（UTC RFC3339Nano）、`status`（`"ready"` | `"degraded"`）、`reasons`（稳定降级码数组，空时为 `[]`）；
  - `host`：`arch`、`uptime_seconds`、`public_base_url`（未配置时省略）；
  - `resources`：`cpu_usage_percent`、`memory_used_percent`、`npu_supported`、`npu_load_percent`、`max_temperature_c`；
  - `storage`：`status`（`healthy`/`warning`/`cleaning`/`emergency_stopped`/`error`）、`writable`、`used_percent`、`free_mb`；
  - `workload`：`cameras`（`enabled`/`online`/`offline`/`degraded`）、`tasks`（`enabled`）、`runners`（`active`/`failed`）、`decode`（`backend`/`hardware_available`/`active_sessions`/`max_sessions`）。
- **统一降级判定规则（`status` & `reasons`）**：
  - 默认 `"ready"`；命中任一条件则 `status = "degraded"` 并按固定顺序追加稳定码：
    - 存储状态为 `emergency_stopped` / `error` 或 `!writable` → `"storage_unavailable"`；
    - 已启用摄像机存在 `offline` → `"camera_offline"`；
    - 算法 Runner 存在熔断失败态 → `"runner_failed"`；
    - 强制硬解但硬件不可用 → `"decode_degraded"`；
    - 日志异步落盘降级 → `"log_degraded"`。

### R6: 连通性测试适配（`POST /api/v1/alert-destinations/test`）
- 请求含 `eventsEnabled` / `heartbeatEnabled`（缺省兼容 `eventsEnabled = true`）；
- `eventsEnabled = true` 时保持发送 `test_ping` 模拟告警；`eventsEnabled = false && heartbeatEnabled = true` 时改为向心跳目标地址/Topic 发送一包真实即时 `heartbeat` 快照校验连通性。

### R7: OpenAPI 被动状态拉取端点（`GET /api/v1/openapi/status`）
- 在 `internal/openapi` 挂载 `GET /status`（完整路径 `/api/v1/openapi/status`），复用路由组既有的 `apitoken.RequireBearer` 认证与 1 MiB 请求体上限，**不挂载录脸限流器**，确保录脸并发满载时状态探针仍可正常响应。
- 响应遵循统一信封 `httputil.Response{data=DeviceStatusResponse}`，字段命名对齐 OpenAPI REST 契约采用 **`camelCase`**（与主动推送共享同一份底层状态快照数据：`deviceId`、`reportedAt`、`status`、`reasons`、`host`、`resources`、`storage`、`workload`；所有切片如 `reasons` 为空时序列化为 `[]` 而非 `null`）。
- 当状态提供者未初始化时返回 `503 SERVICE_UNAVAILABLE`；当业务子域处于降级态（如相机离线、存储熔断）时仍返回 **HTTP 200**，由载荷内 `status: "degraded"` 与 `reasons` 承载诊断信息。
- 新增第三方对接文档 `docs/openapi/device-status.md`（或扩充现有 OpenAPI 指南并同步更新 `internal/openapi/contract_test.go`），运行 `make api-docs` 保持 Swaggo 2.0 规范与路由契约测试 100% 一致。

### R8: 审计与日志降噪纪律
- 目标配置写操作（`alertpush.create|update|delete|enable|disable|test`）以及非法 Bearer Token 拒绝（`api_token.auth_rejected`）保留审计；
- **后台周期心跳发送与合法的 `GET /api/v1/openapi/status` 调用严禁调用 `audithttp.Tag` 或写入 `audit_logs`**；
- 周期心跳连续发送失败时实施日志边缘触发降噪（首次失败记 `Warn`、恢复成功记 `Info`、连续相同错误原因记 `Debug`），防止离线目标刷爆边缘日志盘。

### R9: 前端推送目标界面演进（`web/src/features/alert-push`）
- 列表表格增加能力标签展示（「事件推送」「存活上报 · `60s`」）；纯存活目标的队列列显示 `—`；
- 新建/编辑弹窗拆分为「连接与凭据」「感知事件推送（`eventsEnabled`）」「设备存活上报（`heartbeatEnabled`）」三个清晰分区：
  - 关闭 `eventsEnabled` 时隐藏图片模式与事件/算法/摄像机过滤器；
  - 开启 `heartbeatEnabled` 时展示「上报间隔（15–3600 秒，默认 60）」、Webhook 可选「独立存活接收 URL」（带前端同源校验）或 MQTT 可选「独立状态 Topic」与「断线遗嘱通知（LWT）」开关；
  - 双能力同时关闭时表单校验拦截并提示；
- 补齐 `zh-Hans` / `zh-Hant` / `en` 三语文案，明确区分 MQTT 协议层「保活间隔 (KeepAlive)」与业务层「设备存活上报间隔」。

---

## Acceptance Criteria

- [ ] **AC1（迁移与存量兼容）**：`000035` 迁移 up/down 对称通过；升级前已有 `alert_destinations` 记录默认 `events_enabled = true, heartbeat_enabled = false`，原有事件推送行为不变。
- [ ] **AC2（能力组合与安全校验）**：支持创建与编辑「仅事件」「仅存活」「双开」目标；双关返回 422；未配置 `alertpush.public_base_url` 时「仅存活」目标可正常保存并上报；带凭据 Webhook 配置跨源 `heartbeatUrl` 返回 422。
- [ ] **AC3（Webhook 主动存活上报与分流）**：开启存活上报的目标按周期收到 `event_type == "heartbeat"` 的 `snake_case` JSON，`seq` 单调递增、`device_id` 实时准确；配置 `heartbeatUrl` 时事件与心跳分别发往各自 URL 且携带鉴权头；`heartbeatUrl` 密文落库且响应含 `Cache-Control: no-store`。
- [ ] **AC4（MQTT 单连接分流与 LWT 遗嘱）**：双开 MQTT 目标仅建立 1 条连接；事件与心跳按各自 Topic（或默认同 Topic）投递；优雅关闭发送 `reason: "graceful_shutdown"` 的 `offline` 报文，异常断连由 Broker 发出 `reason: "connection_lost"` 遗嘱。
- [ ] **AC5（双轨隔离与测试适配）**：事件队列满或消费阻塞时心跳仍准点发出且不计入 `dropped`；纯存活目标不收 `AlarmEvent`，且点击「测试连通性」时发送真实即时 `heartbeat` 载荷探测成功。
- [ ] **AC6（OpenAPI 被动状态拉取与契约一致性）**：未带/无效/已吊销 Bearer Token 访问 `GET /api/v1/openapi/status` 返回 401 + `WWW-Authenticate: Bearer`；有效 Token 返回 200 与 `camelCase` `DeviceStatusResponse`，空 `reasons` 序列化为 `[]`；录脸限流打满时该端点不被 429 拦截；`internal/openapi` 下全部 Swagger 与文档契约单测通过。
- [ ] **AC7（健康聚合与脱敏）**：模拟相机离线、存储熔断等异常时，Push 与 Pull 均返回 `status: "degraded"` 及对应 `reasons`；两处载荷均不含任何 RTSP URI、密码、Token 或宿主机绝对路径。
- [ ] **AC8（零心跳审计污染）**：多轮周期心跳上报及多次合法调用 `GET /api/v1/openapi/status` 后，`audit_logs` 表无新增记录。
- [ ] **AC9（前端组件测试与全仓门禁）**：前端三语与弹窗/表格浏览器单测通过，`make check` 全绿通过。

---

## Out of Scope

- 经 MQTT 或 Webhook 接收上级平台下发的反向运维控制指令（如远程重启、固件升级、任务配置修改）；
- 海康 ISUP / 大华主动注册等厂商私有协议；
- 历史心跳落库与离线补发。
