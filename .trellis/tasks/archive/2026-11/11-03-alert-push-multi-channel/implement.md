# 告警上报模块与多通道分发实施计划 (Implementation Plan)

> 前置阅读：`mediafile-guidelines.md`（取图与 URL 契约）、`authentication-guidelines.md`（媒体鉴权边界）、
> `record-guidelines.md`（抓拍时序）、`database-guidelines.md`、`error-handling.md`、`logging-guidelines.md`、
> `dependency-injection.md`、`http-api-guidelines.md`、`frontend/development-guidelines.md`、`frontend/theme-and-i18n.md`。

## 0. 开工前必须确认的决策（已写入 design.md）

- [ ] `url` 模式使用**签名 URL**（新增公开媒体路由），不是裸 `/media/content/*`；
- [ ] 图片就绪由 `SnapshotStore` 完成回调驱动，而不是落库后立刻读盘；
- [ ] 全幅图与裁剪图按输出项分别跟踪就绪，不用 job 级写入计数推断路径有效；
- [ ] 凭据复用 `camera.LazyCipher` 加密并注册 `EncryptedDataProbe`，API 不回传；
- [ ] 热重载异步化：保存接口不等连接；
- [ ] 本阶段只推 `alarms`，不做历史补发。

## 1. 实施清单 (Ordered Checklist)

### 阶段一：契约、配置与媒体基础

- [ ] `internal/alertpush/model.go`：GORM 模型（含独立的 `MQTTClientID` 列）、`WebhookConfig` / `MQTTConfig` / `DestinationFilter`、状态枚举、`TableName`。
- [ ] 迁移 `internal/database/migrations/000030_create_alert_destinations.up.sql` / `.down.sql`（`TEXT` / `DATETIME` / ULID、`name` 唯一索引、MQTT `client_id` 独立列与部分唯一索引；无 `enabled` 索引）。
- [ ] `internal/alertpush/store.go`：CRUD、启用列表、MQTT ClientID 持久化与唯一冲突映射、`EncryptSecret` / `DecryptSecret`（AAD=`alert-destination:<id>`）、`HasExistingEncryptedData`；更新操作严格使用 `Select("enabled", ...)` 或 map 防布尔零值丢失；更新时置空 `clientId` 派生默认值冲突返回 422；Webhook URL 与鉴权凭据一起进密文，`config_json` 只留非敏感配置；`store_test.go` 覆盖迁移往返、默认 ID 唯一、唯一索引冲突。
- [ ] `internal/alertpush/signing.go`：`media_url.key`（32B/0600/原子写）、`Sign(relPath, exp)` / `Verify`；`signing_test.go`（过期、篡改、路径）。
- [ ] 配置：`cmd/Zhulong/config.go` 增加 `[alertpush] public_base_url` / `signed_url_ttl` 与默认值；`internal/app/config.go` 增加字段与校验；`config.toml` 增注释；`config_test.go` 覆盖 URL/TTL 边界。
- [ ] `internal/mediafile`：把 `GetContent` 的字节服务抽成可复用的 `Handler.ServeContent(c, relPath)`（保持四层防护与 Range/HEAD 不变）；`handler_test.go` 加回归。
- [ ] `internal/record`：新增 `events.go`（`AlarmEvent` / `AlarmListener` / `SnapshotJobObserver`）；observer 返回逐输出路径写入结果，`SnapshotStore.SetJobObserver` + `run` 全路径回调；`service_test.go` / `snapshot_test.go` 覆盖全幅/裁剪图部分写入。
- [ ] `internal/record`：`Service.SetAlarmListener`、`pendingAlarms`（上限 256）、5s 兜底 sweeper、`Start(ctx)` / `Stop()`，并在 `recordLifecycleImpl` 中接线；测试：恰好一次、延迟 1s 仍可读、全幅或裁剪单独失败时仅清空对应路径、队列满立即两图未就绪、超时兜底、批量失败不触发。

### 阶段二：发送器

- [ ] `go get github.com/eclipse/paho.mqtt.golang@v1.5.1`（**不得低于 v1.5.1**）并添加 `github.com/mochi-mqtt/server/v2` 测试依赖。
- [ ] `internal/alertpush/sender.go`：`Sender` / `Event` / `ImageRef` / `MediaReader` 窄接口。
- [ ] `sender_webhook.go`：JSON（全幅/裁剪图按各自状态签名 URL）、multipart 固定发送全幅图（利用 `FileInfo.Size()` 计算确切 `Content-Length` 防 chunked，未就绪则不带 image part）、鉴权头（`none`/`bearer`/`basic` + 附加 headers）、最多 3 次发送尝试（首次 + 至多 2 次重试，网络/5xx/429，4xx 不重试）、每目标 Transport、响应体 drain、ctx 取消，每次重试必须确保旧文件句柄 `defer file.Close()`；`sender_webhook_test.go`（`httptest`：字节一致、重试次数、超时、无图降级、部分图就绪、三种鉴权、保留头 422）。
- [ ] `sender_mqtt.go`：连接选项、缺省 ClientID=`"z" + ULID[4:]`（23 字符 ASCII 字母数字，目标 ULID 冲突时重新生成并持久化）；自定义 ID 限 1–23 位 ASCII 字母数字并校验所有已保存 MQTT 目标（包括禁用目标）唯一，冲突 422；QoS 0/1、发布超时、离线有界队列（丢最旧+计数）、base64 仅传全幅图（500KB 上限，超限时外部 `image_ready=false` 且不附加图片，`io.LimitReader`）；`sender_mqtt_test.go` 使用进程内 broker（`github.com/mochi-mqtt/server/v2`，仅测试依赖，支持 v3.1.1）。
- [ ] 校验：协议 × 图片模式矩阵（`webhook` 允许 `url|multipart`，`mqtt` 允许 `url|base64`）、URL/Topic/QoS/超时/KeepAlive；自定义 MQTT ClientID 限 1–23 位 ASCII 字母数字，并对所有已保存 MQTT 目标（包括禁用目标）唯一，冲突返回 422；更新时若置空 `clientId` 派生默认值与其他目标冲突亦返回 422；Webhook 鉴权生效值（`bearer` 有 token、`basic` 有用户名密码）、保留头拒绝、URL 禁止 userinfo、`basic + http` 告警。
- [ ] 保存时规范化密文：切到 `none` 清掉 token/用户名/密码，`headers` 保留；切换 `auth_type` 缺凭据返回 422；MQTT `config.clientId` 在更新中缺省/为空时重置为派生默认值，未提供整个 `config` 时保持原值。

### 阶段三：过滤与调度

- [ ] `filter.go`：空=全量；`AlgorithmID=""` 只命中空过滤器；悬空 ID 永不命中；`filter_test.go` 表驱动。
- [ ] `dispatcher.go`：
  - 入口非阻塞、关闭后安全返回；纯内存匹配与 `trySend`，严禁在抓拍 worker 回调线程上执行解密与 I/O；
  - 每目标队列 16、丢最旧、`dropped` 计数、限流日志；
  - 每目标 worker：Webhook 初始 `unverified`，成功测试/投递后 `connected`；MQTT `connecting` → `connected`/`failed`；已启用目标测试同步状态，禁用目标维持 `disabled`；
  - 目标退役先停止入队，在独立 3s deadline 内排空，超时后取消在途发送并丢弃余项，再关闭连接。
  - `RequestReload`（容量 1 合并）、串行 reconcile，指纹覆盖 `mqtt_client_id`、锁外连接、原子换表；
  - `Stats(id)` 供列表接口读取状态；
  - `dispatcher_test.go`：隔离、丢弃、重载差量、排空先于取消、排空超时与关闭语义；Webhook 空闲时保持 `unverified`。
- [ ] `service.go`：Store + Dispatcher + `Test`（草稿/已存目标复用凭据）、原因码映射。

### 阶段四：HTTP 接口与装配

- [ ] `requests.go` / `responses.go`：严格命名（`*Request` / `*Response` / `*Info` / `*Status`），列表 `{items: []}` 不为 `null`。
- [ ] `handler.go`：CRUD、enable/disable、test、签名媒体路由（`GET|HEAD /media/signed/*path`，IP 限流，显式 HEAD）；所有返回含 Webhook URL 的响应设 `Cache-Control: no-store`；Swagger 注释齐全。
- [ ] `handler_test.go`：对列表、创建、更新、启用、禁用响应断言 `Cache-Control: no-store`，并验证 URL 不出现在测试/删除响应。
- [ ] `locale.go`：`init()` 注册三语错误码；`handler_test.go` 覆盖 422/403/404/冲突与审计留痕。
- [ ] 审计动作跨层登记：`internal/alertpush/model.go` 常量（保持领域内聚，跨包单测自动覆盖）+ `web/src/features/audit/data/audit-actions.ts` + `web/src/features/audit/locales/{zh-Hans,zh-Hant,en}.json` 的 `actionNames`（Go 与前端守卫测试会双向校验）。
- [ ] `internal/app/app.go`：`provideAlertPush`（依赖 `DBProvider` / `*camera.LazyCipher` / `*camera.LifecycleManager` / `*mediafile.Service` / `*mediafile.Handler` / `*record.Service` / `Config`），注册 probe、`SetAlarmListener`，贡献 `protected_routes` 与 `public_routes`。
- [ ] `internal/app/runtime.go`：`alertpush` 启停接口、`degradedStart`、`stopSequence` 插入位置（`closeAlgorithm` 之后、`closeRecordAndSnapshot` 之前）；更新 `runtime_test.go` 启停轨迹断言。
- [ ] `Makefile` 的 `api-docs --dir` 增加 `internal/alertpush`。

### 阶段五：前端

- [ ] `web/src/features/settings/alert-push/`：`api/`（Zod schema + axios 函数）、`hooks/`（TanStack Query）、`components/`（列表、对话框、过滤多选、测试连接）、`locales/`（三语）、`index.tsx`；列表状态徽章通过数据映射对齐 `@/lib/status-palette` 4 级语义。
- [ ] 路由 `web/src/routes/_authenticated/settings/alert-push.tsx`；侧边栏项加在 `web/src/features/settings/index.tsx`（`BellRing`）。
- [ ] i18n：`web/src/lib/i18n.ts` 注册 `alertPush` 命名空间；`web/src/locales/*/common.json` 增加 `nav.AlertPush`。
- [ ] 表单：zod 判别联合、协议切换清空不兼容字段、图片模式按协议过滤、**Webhook 鉴权方式选择（none/bearer/basic）+ 附加 Header 编辑器**、凭据写密不回显（留空保持/显式清除）、`basic + http` 风险提示、测试连接 pending/错误原因。
- [ ] 组件测试（真实无头浏览器）：校验、协议切换、启停、Webhook `unverified` 状态与测试状态变化、测试失败渲染、提交体不含占位符；`unverified` 状态文案三语齐全。

### 阶段六：全链路验证与门禁

- [ ] `python3 native/scripts/build.py go test -race ./internal/alertpush/... ./internal/record/... ./internal/mediafile/... ./internal/app/...`
- [ ] `cd web && pnpm test -- src/features/settings/alert-push && pnpm build`
- [ ] `make api-docs`（确认 `internal/apidocs` 生成物包含 alertpush）
- [ ] `make frontend-build`（内嵌 SPA 副本更新）
- [ ] `make check`

### 阶段七：统一事件与主机标识（增量，已完成）

- [x] `internal/record/events.go`：`AlarmEvent` 扩展为统一事件（`EventType` + `Alarm`/`Face`/`Vehicle` 子载荷），`recognitions` 与 `captures(vehicle)` 与 `alarms` 共用 `pendingAlarms` + `SnapshotJobObserver`；`dispatchOrEnqueueEvent` 收敛三条投递路径。
- [x] `record.Service.OnSnapshotJobDone` 放开到 `alarms` / `recognitions` / `captures` 三张表。
- [x] `internal/alertpush/sender.go`：`AlarmPayload` 改为统一信封（顶层 `id` / `device_id` / `event_type` / `camera_id` / `captured_at` / 图片字段 + 业务子对象），移除 `hostname`、`pts`、`algorithm_*`、`schema_version`、`event`；子对象保留开放 `attributes` 字典。
- [x] `filter.go`：`DestinationFilter.EventTypes`，`Matches(eventType, algorithmID, cameraID)` 三条件 AND。
- [x] 迁移 `000031_create_system_settings`（单行表 + `CHECK (id = 1)` 种子行）；`internal/systeminfo/identity.go` 提供 `IdentityService`（首次启动生成 `zhulong-<12hex>`、内存缓存、`DeviceID()` 实现 `alertpush.DeviceIDProvider`）。
- [x] `GET|PUT /api/v1/system/identity`（`SYSTEM_IDENTITY_INVALID` 422、审计 `system.identity.update`、三语本地化）；发送端每次发送读取缓存，改号即时生效。
- [x] 前端：`settings/system` 页顶部设备标识卡片（自动生成/自定义 Badge、编辑保存、Toast）；`alert-push` 增加事件类型订阅 Badge 与列表事件标签；三语词条 + 审计动作登记。
- [x] 测试：`internal/systeminfo/identity_test.go`、`handler_test.go` 的 identity 用例、`internal/database/store_test.go` 的迁移 31 对称性、`sender_webhook_test.go` 校验 `device_id` 随 provider 变化、`web/src/features/settings/system/api/system-api.test.ts`。

## 2. 验证命令 (Validation Commands)

```bash
# 后端单测与数据竞争：alertpush 间接依赖 internal/engine，必须走 native wrapper
python3 native/scripts/build.py go test -race ./internal/alertpush/... ./internal/record/...
python3 native/scripts/build.py go test -race ./internal/mediafile/... ./internal/app/...

# 前端：web 没有 type-check script，类型检查由 build 的 tsc -b 承担
cd web && pnpm test -- src/features/settings/alert-push
cd web && pnpm build

# 整库门禁
make api-docs
make frontend-build
make check
```

## 3. 不要做的事（写错就出事）

- ❌ 用 `filepath.Join(mediaRoot, relPath)` 自行读图 —— 必须走 `mediafile.Service.OpenContent`（四层防护 + 热切换媒体根）。
- ❌ 在推理消费 goroutine 或抓拍 worker goroutine 里读文件 / 做 base64 / 凭据解密 / 同步网络发送（单 worker 抓拍队列将被直接拖死）。
- ❌ 在更新数据库时使用裸 `db.Updates(dest)` 导致 `enabled: false` 等布尔零值静默丢失。
- ❌ 发送未定长流式 Multipart 退化为 `Transfer-Encoding: chunked`，导致局域网/私有网关拒绝。
- ❌ 在 `StreamHub.SetAIConsumer` 上再挂一个消费者（只有一个槽位）。
- ❌ 落库后立刻推送 multipart/base64 —— 抓拍是异步的，非空路径不代表文件存在；也不得用任一输出写成功推断其他输出就绪。
- ❌ 把 `config_json`、secret、原始 Header、带凭据的 URL 写进日志（用 `internal/logsafe`）。
- ❌ 响应里回传 token/密码/Header 值或 `******` 掩码；请求里把掩码当密码存库（URL 例外：返回给已认证管理员并 `no-store`）。
- ❌ 保存接口同步连接 Broker / Webhook。
- ❌ 先取消 worker 的发送 context 再声称排空队列；排空必须使用独立 deadline，超时后才取消在途发送。
- ❌ 用 `http.DefaultClient`，或对 4xx 重试，或不重建 multipart body 就重试。
- ❌ 把图片 URL 固化进数据库（媒体规范禁止；URL 只在发送时生成）。
- ❌ 给 `enabled` 建索引、用 `AutoMigrate`、修改已应用迁移。

## 4. 风险与回滚点

- **抓拍慢或部分写入**：observer 按输出报告结果 + 5s 兜底；各图片路径独立清空/发送，无图降级并计数，不发坏 URL。
- **内存**：事件只含路径；队列 16 × 目标数 ≤32；base64 单张 ≤500KB。
- **Broker 包大小**：base64 约 667KB，可能触及默认 1MB 限制，测试与文档标注。
- **时钟**：签名 URL 依赖宿主时钟，NTP 未同步时提示。
- **回滚**：UI 一键禁用；必要时执行 `000030` down；删除 `media_url.key` 只影响未过期 URL。
