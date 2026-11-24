# 告警多目标推送与签名媒体规范

> `internal/alertpush` 的推送通道、对外载荷 v1 契约、安全脱敏、GORM 布尔零值陷阱及 HMAC-SHA256 签名媒体访问边界。

---

## 1. 架构与配置热生效

- **通道与图片模式**：支持 `webhook` 与 `mqtt` 两种通道类型，以及 `url`、`multipart`、`base64` 三种图片载荷模式（受各通道能力校验约束）。
- **设备身份与来源实时读取**：
  - `Dispatcher` 通过 `DeviceIDProvider` 与 `HeartbeatSnapshotProvider` 在每次发送/心跳时动态读取宿主身份与运行快照，**不在构造期缓存**，确保修改系统设备标识后无需重建目标即可生效。
  - 来源元数据通过消费侧接口 `SourceResolver`（由 `internal/app` 适配器注入，alertpush 不反向依赖 `camera` / `algorithm`）在发送时动态解析摄像头名称与算法信息；查找失败降级为 `null` 字段，不得因元数据缺失丢弃事件。
- **保留请求头与跨源校验**：自定义 Webhook Header 禁止覆盖保留系统头（含 `X-Zhulong-Event`、`X-Zhulong-Delivery`、`X-Zhulong-Schema` 等，`ErrReservedHeaderUsed`），系统路由头在自定义头之后写入；若配置了认证凭据，心跳地址（`heartbeat_url`）必须与主推送 `url` 同源（`ErrCrossOriginHeartbeatURL`），防止凭据跨源泄露。
- **过滤器事件类型白名单**：`filter.event_types` 仅接受外部短名 `{alarm, face, vehicle}`（`ErrInvalidFilterEventType`），与 `record.EventType` 内部取值隔离并由 alertpush 映射（存量配置迁移见 `000040_alert_filter_event_type_v1`）。

---

## 2. 对外载荷契约 v1

- **统一信封（`schema_version: 1`）**：感知事件（`alarm` / `face` / `vehicle`）、心跳（`heartbeat`）、离线通知（`offline`）共用 `EventPayload`；可选标量始终输出为 `null`，且仅携带一个与 `event_type` 同名的业务子对象；时间戳固定为 UTC 毫秒精度（`2006-01-02T15:04:05.000Z`）。
- **序号与丢包可观测性**：每个推送目标维护单调递增 `seq` 计数器；事件在**入队时**分配（队列溢出丢弃会留下可观测缺口），心跳与优雅离线在发送时分配；MQTT LWT 离线消息的 `seq` 与 `sent_at` 为 `null`。
- **业务子对象与缺省策略**：
  - `alarm{alarm_type, score, box, attributes}`
  - `face{matched, person, similarity, box, attributes}`（未匹配时 `person` 与 `similarity` 为 `null`）
  - `vehicle{plate, vehicle_type, vehicle_color, score, box, attributes}`（外提键 `plate_number`、`plate_color`、`vehicle_type`、`vehicle_color` 不在 `attributes` 中重复）
  - 无检测框时 `box` 为 `null`，严禁输出 `[0,0,0,0]`。
- **图片列表 `images[]` 与模式一致性**：按固定顺序 `scene → object → reference` 输出 `{role, mime, size_bytes, url, url_expires_at, part, base64}`，无法经任一载体交付的图片整项省略；`url`、`multipart`、`base64` 三种模式均覆盖全部可用图片角色。
- **MQTT `base64` 内联预算（`inline_max_kb`）**：范围 `64..8192` KiB（默认 `500`，按编码前原始字节计），按 `object → reference → scene`（小图优先）扣减预算；超预算图片回退 `url`，无可用 URL 时省略并记录 Warn 日志。

---

## 3. 持久化与密码学边界

1. **布尔列禁止 GORM `default:true` 标签**：
   - `AlertDestination.EventsEnabled` 与 `HeartbeatEnabled` 在数据库迁移中为 `INTEGER NOT NULL DEFAULT 1`，但 GORM 结构体标签**严禁声明 `default:true`**（见 [database-guidelines.md](./database-guidelines.md) §5.7）。否则显式创建 `false` 时 GORM 会省略该列，导致 SQLite 静默写回 `1`。
   - 更新布尔字段时使用 `Select(...)` 显式列出列名。
2. **密文与配置隔离**：`SecretCiphertext` 使用宿主加密密钥存储，持久化模型全部标记 `json:"-"`，绝不经 API 透传或明文写入日志。
3. **心跳间隔边界**：允许范围为 `15s ~ 3600s`（默认 `60s`），优雅停机与断线分别发出明确的离线原因码（`graceful_shutdown` / `connection_lost`）。

---

## 4. 签名媒体访问（`/api/v1/media/signed/*path`）

- **签名密钥隔离**：`URLSigner` 使用独立 32 字节密钥文件 `media_url.key`（权限 `0600`），构造函数保持纯内存创建（`NewEmptyURLSigner()`），密钥加载收敛在 `Start` 阶段。
- **仅限 `snapshots/` 相对路径**：`URLSigner.Sign` 硬性拦截非 `snapshots/` 前缀的路径（`ErrPathNotAllowed`），防止签名能力被滥用于读取底图、配置或其他目录。
- **无 Cookie 鉴权的公开端点限流**：`SignedMediaHandler` 挂载于 `public_routes`，必须叠加按 IP 限流中间件（120 req/min，burst 8），验签通过后再委托 `mediafile.Handler` 按四层安全路径防护流式输出字节（严禁绕过 `os.Root`）。

---

## 5. 生命周期依赖

- `alertPush.Start` 为可降级启动，须在 `camera` 密钥与 `storage`/`mediafile` 就绪后启动；
- 停机时 `alertPush.Stop` 必须先于 `record`、`snapshot` 与 `storage`（见 [dependency-injection.md](./dependency-injection.md)），防止推送 worker 在媒体根目录或记录库关闭后继续读取图片。
