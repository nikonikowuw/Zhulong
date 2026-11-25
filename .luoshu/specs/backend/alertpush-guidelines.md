# 告警多目标推送与签名媒体规范

> `internal/alertpush` 的推送通道、对外载荷 v1 契约、安全脱敏、GORM 布尔零值陷阱及 HMAC-SHA256 签名媒体访问边界。

---

## 1. 架构与配置热生效

- **通道与图片模式**：支持 `webhook` 与 `mqtt` 两种通道类型，以及 `url`、`multipart`、`base64` 三种图片载荷模式（受各通道能力校验约束）。
- **设备身份与来源实时读取**：
  - `Dispatcher` 通过 `DeviceIDProvider` 与 `HeartbeatSnapshotProvider` 在每次发送/心跳时动态读取宿主身份与运行快照，**不在构造期缓存**，确保修改系统设备标识后无需重建目标即可生效；同时 `systeminfo.IdentityService` 在变更时经宿主注册的回调主动触发 `Dispatcher.RequestReload()`，立即刷新 MQTT LWT（心跳前仍保留 `hasStaleWillDeviceID` 兜底检查）。
  - 来源元数据通过消费侧接口 `SourceResolver`（由 `internal/app` 适配器注入，alertpush 不反向依赖 `camera` / `algorithm`）在发送时动态解析摄像头名称与算法信息；查找失败降级为 `null` 字段，不得因元数据缺失丢弃事件。
- **保留请求头与跨源校验**：自定义 Webhook Header 禁止覆盖保留系统头（前后端统一：`accept`、`authorization`、`connection`、`content-length`、`content-type`、`host`、`transfer-encoding`、`user-agent` 及 `X-Zhulong-Event`、`X-Zhulong-Delivery`、`X-Zhulong-Schema`，`ErrReservedHeaderUsed`），系统路由头在自定义头之后写入；若配置了认证凭据，心跳地址（`heartbeat_url`）必须与主推送 `url` 同源（`ErrCrossOriginHeartbeatURL`），防止凭据跨源泄露；HTTP 客户端拦截跨源重定向、超过 3 次重定向以及导致 POST 降级为 GET 的 301/302/303 重定向，且不重试。
- **输入校验一致性**：Create/Update/Test 共享统一的字段校验规则（名称非空且 ≤64 字符、Webhook `authType` 白名单、`timeoutSeconds` 范围 `1..30`、鉴权凭据归一化与必填校验、MQTT Broker/Topic/QoS/KeepAlive/ClientID/InlineMaxKB 校验），越界或非法值一律返回 422 而非静默忽略。
- **过滤器事件类型白名单**：`filter.event_types` 仅接受外部短名 `{alarm, face, vehicle}`（`ErrInvalidFilterEventType`），与 `record.EventType` 内部取值隔离并由 alertpush 映射（存量配置迁移见 `000040_alert_filter_event_type_v1`）。
- **MQTT 局部更新、TLS 与连接状态可观测性**：
  - `AlertDestinationConfigInfo` 的可选字段（`clientId`、`username`、`tlsSkipVerify`、`heartbeatTopic`、`heartbeatUrl` 等）使用指针区分「省略（保持原值）」与「显式清空/关闭」；局部更新不覆写自定义 `clientId`；安全连接按小写 scheme 识别 `ssl`/`tls`/`tcps`/`wss`/`mqtts` 并应用 TLS 配置；事件发布对瞬断/发布失败支持最多 3 次有界重试（建连超时与取消不重试），且 `ensureConnected` 透传 `ctx`。
  - `MQTTSender` 经 Paho `OnConnect`/`ConnectionLost` 回调实时维护 `AlertDestinationStatus`（含 `state`、`lastError`、`lastAttemptAt`、`lastSuccessAt`、`lastFailureAt`、`consecutiveFailures`、`totalDispatched`、`dropped`、`queued`、`queueCapacity`）；配置热重载时保留历史累计计数与时间戳并重置健康态。

---

## 2. 对外载荷契约 v1

- **轻量化分型信封（`schema_version: 1`）**：
  - 感知事件（`EventPayload`，`event_type ∈ {alarm, face, vehicle}`）、存活心跳（`HeartbeatPayload`，`event_type: "heartbeat"`）与离线通知（`OfflinePayload`，`event_type: "offline"`）各自独立结构，避免心跳/离线携带无意义的 `source`/`captured_at`/`images`；所有可选空字段均省略（`omitempty`），时间戳固定为 UTC 毫秒精度（`2006-01-02T15:04:05.000Z`）。
  - 感知事件将 `camera{id, name?}` 与 `algorithm{id, name?, version?}` 直接平铺在顶层，并仅携带一个与 `event_type` 同名的业务子对象。
- **序号与丢包可观测性**：每个推送目标维护单调递增 `seq` 计数器；事件在**入队时**分配（队列溢出丢弃会留下可观测缺口），心跳与优雅离线在发送时分配（若配置了独立的 `heartbeat_url`/`heartbeat_topic` 分流，单侧流可见因另一轨消费产生的跨轨序号跳进，属既定目标级单调序列语义）；连通性探测（告警及仅心跳探测）统一使用 `seq: 0`，不占用真实序列空间；MQTT LWT 离线消息省略 `seq` 与 `sent_at`。
- **业务子对象与缺省策略**：
  - `alarm{type?, score, box?, attributes?}`
  - `face{matched, person?, similarity?, box?, attributes?}`（未匹配时省略 `person` 与 `similarity`）
  - `vehicle{plate?, type?, color?, score, box?, attributes?}`（外提键 `plate_number`、`plate_color`、`vehicle_type`、`vehicle_color` 不在 `attributes` 中重复）
  - 无检测框时省略 `box`，严禁输出 `[0,0,0,0]`；空 `attributes` 直接省略。
- **图片对象 `images{scene?, object?, reference?}` 与单载体原则**：按角色键直接索引 `{mime, size_bytes?, url?, expires_at?, part?, base64?}`，仅输出实际生效的载体字段，无任何可交付图片时省略 `images`；`url`、`multipart`、`base64` 三种模式均覆盖全部可用图片角色（`multipart` 读图失败或 `base64` 超预算时才回退 `url`，否则不冗余签发 URL）。其中 `reference`（人脸底库参考图）自动将生产库裸文件名规范化为 `faces/<filename>` 并经独立受控的 `GalleryMedia` 只读适配器读取/预签名，MIME 与附件后缀按实际扩展名确定性输出（支持 `image/jpeg`、`image/png`、`image/webp`）。
- **MQTT `base64` 内联预算（`inline_max_kb`）**：范围 `64..8192` KiB（默认 `500`，按编码前原始字节计），按 `object → reference → scene`（小图优先）扣减预算；超预算图片回退 `url`，无可用 URL 时省略并记录 Warn 日志。

---

## 3. 持久化与密码学边界

1. **布尔列禁止 GORM `default:true` 标签**：
   - `AlertDestination.Enabled`、`EventsEnabled` 与 `HeartbeatEnabled` 在数据库迁移中带有默认真值，但 GORM 结构体标签**严禁声明 `default:true`**（见 [database-guidelines.md](./database-guidelines.md) §5.7）。否则显式创建 `false` 时 GORM 会省略该列，导致 SQLite 静默写回 `1`。
   - 更新布尔字段时使用 `Select(...)` 显式列出列名；新增目标数量上限（`MaxDestinations = 32`）在 `Store.CreateWithLimit` 事务内原子校验。
2. **密文与配置隔离**：`SecretCiphertext` 使用宿主加密密钥存储，持久化模型全部标记 `json:"-"`，绝不经 API 透传或明文写入日志。
3. **心跳间隔边界**：允许范围为 `15s ~ 3600s`（默认 `60s`），优雅停机与断线分别发出明确的离线原因码（`graceful_shutdown` / `connection_lost`）。

---

## 4. 签名媒体访问（`/api/v1/media/signed/*path`）

- **签名密钥隔离**：`URLSigner` 使用独立 32 字节密钥文件 `media_url.key`（权限 `0600`），构造函数保持纯内存创建（`NewEmptyURLSigner()`），密钥加载收敛在 `Start` 阶段。
- **受控白名单路径**：`URLSigner.Sign` 与 `Verify` 仅放行 `snapshots/`（抓拍证据）及单层 `faces/<filename>`（人脸底库参考图），硬性拦截 `recordings/`、`exports/`、`maps/`、嵌套子目录或其他路径（`ErrPathNotAllowed`）。**严禁**将 `faces/` 并入 `mediafile` 白名单或经 `/api/v1/media/content/*` 暴露。
- **无 Cookie 鉴权的公开端点限流**：`SignedMediaHandler` 挂载于 `public_routes`，必须叠加按 IP 限流中间件（120 req/min，burst 8）；验签通过后，`snapshots/` 委托 `mediafile.Handler` 流式输出，`faces/<filename>` 经由 `filestore.Manager` 注入的隔离 `GalleryMedia` 适配器（同样走 `os.Root` 四层防护与 S3 预签名跳转）只读流式输出。

---

## 5. 生命周期依赖

- `alertPush.Start` 为可降级启动，须在 `camera` 密钥与 `storage`/`mediafile` 就绪后启动；
- 停机时 `alertPush.Stop` 必须先于 `record`、`snapshot` 与 `storage`（见 [dependency-injection.md](./dependency-injection.md)），防止推送 worker 在媒体根目录或记录库关闭后继续读取图片；`Dispatcher.Stop` 与 `reconcile` 并发停止退役 worker，且 `runEvents` 的在飞发送与排空共享 `retirementDrainTimeout`（3s）硬上界，防止慢重试击穿宿主 60s 停机预算。
