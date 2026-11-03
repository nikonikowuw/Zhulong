# 告警上报模块与多通道分发技术设计 (Design Document)

## 1. 架构拓扑与系统边界

> 术语：UI 文案称「告警推送通道」，代码与数据模型统一用 `Destination`（`alert_destinations`）；正文统一用「目标」，两者同义。

### 1.1 数据流（含抓拍完成信号）

```
                [ C++ Native Engine / FrameBroker ]
                               │  (SHM 帧)
                               ▼
                   internal/camera.StreamHub
                               │  (aitypes.InferenceResult, 唯一 AI consumer)
                               ▼
                    internal/record.Service
      ┌────────────────────────┴─────────────────────────┐
      ▼                                                  ▼
目标跟踪/降频 + alarms 落库                    登记抓拍任务（相对路径先入库）
(CreateAlarmBatch)                                      │
      │                                                  ▼
      │                                     record.SnapshotStore（全局队列 32 / 单 worker）
      │                                                  │ 写完 / 失败
      │                                                  ▼
      │                                     SnapshotJobObserver.OnSnapshotJobDone
      │                                                  │
      └──────────────► pendingAlarms（按 alarmID 去重，5s 兜底 sweeper）
                               │  恰好一次
                               ▼
                    record.AlarmEvent → AlarmListener
                               │
                               ▼
                   internal/alertpush.Dispatcher
                     （规则匹配 + 每目标有界队列）
                               │
        ┌──────────────────────┼──────────────────────┐
        ▼ 独立 worker           ▼ 独立 worker           ▼ 独立 worker
  Webhook(url)            Webhook(multipart)      MQTT(base64)
        │                        │                      │
        └──── mediafile.Service.OpenContent（四层防护）──┘
                               │
                               ▼
                 外部平台 / Broker（网络出口仅在 Go 宿主）
```

### 1.2 跨模块契约

- **`internal/record` ➔ `internal/alertpush`**：`record` 定义 `AlarmEvent` 与 `AlarmListener`，`alertpush` 单向依赖 `record`；装配期由 `internal/app` 调 `record.Service.SetAlarmListener`，避免循环引用。
- **`record` ➔ `SnapshotStore`**：新增 `SnapshotJobObserver` 接口与 `SetJobObserver`。回调签名为 `OnSnapshotJobDone(table, id string, result SnapshotJobResult)`；`SnapshotJobResult.Outputs` 为逐输出的 `{RelPath, Written}` 列表。`SnapshotStore.run` 在**所有退出路径**（成功 / 写门关闭 / 取根失败 / 抓拍失败 / 零写入）回调一次；提前失败时所有输出均标记未写入。回调运行在抓拍 worker goroutine，必须非阻塞。
- **`alertpush` ➔ `mediafile`**：只使用 `mediafile.Service.OpenContent`（读取字节）与新增的 `Handler.ServeContent`（签名路由复用字节服务），不自行解析媒体根。
- **不新增 AI consumer**：`StreamHub.SetAIConsumer` 只有一个槽位，推送不得占用。

### 1.3 组件划分

| 文件 | 职责 |
| --- | --- |
| `model.go` | GORM 模型、配置/过滤 JSON 结构、状态枚举 |
| `store.go` | `alert_destinations` CRUD、凭据加解密、启用列表 |
| `signing.go` | `media_url.key` 管理、`Sign` / `Verify`（HMAC-SHA256） |
| `filter.go` | `algorithm_ids` / `camera_ids` 纯函数匹配 |
| `sender.go` | `Sender` 接口、`Event`、`ImageRef` |
| `sender_webhook.go` | JSON / Multipart 发送、重试矩阵 |
| `sender_mqtt.go` | paho 长连接、QoS、base64 |
| `dispatcher.go` | 匹配、每目标队列与 worker、热重载状态机 |
| `service.go` | Store + Dispatcher + 测试连接聚合 |
| `handler.go` / `requests.go` / `responses.go` | CRUD、测试 |
| `signed_media.go` | 公开签名媒体路由（校验后转 `mediafile.Handler.ServeContent`） |
| `locale.go` | 模块错误码三语注册 |

---

## 2. 持久化契约

### 2.1 迁移 `000026_create_alert_destinations`

```sql
-- up
CREATE TABLE IF NOT EXISTS alert_destinations (
    id TEXT PRIMARY KEY NOT NULL,                                   -- ULID，26 字符
    name TEXT NOT NULL,
    channel_type TEXT NOT NULL CHECK (channel_type IN ('webhook', 'mqtt')),
    enabled BOOLEAN NOT NULL DEFAULT 1,
    image_mode TEXT NOT NULL DEFAULT 'url' CHECK (image_mode IN ('url', 'multipart', 'base64')),
    mqtt_client_id TEXT,                                             -- MQTT only; generated/persisted for stable uniqueness
    config_json TEXT NOT NULL DEFAULT '{}',                         -- non-sensitive config, excludes mqtt_client_id
    secret_ciphertext BLOB,                                         -- AES-256-GCM(JSON)，AAD = alert-destination:<id>
    filter_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS ux_alert_destinations_name ON alert_destinations(name);
CREATE UNIQUE INDEX IF NOT EXISTS ux_alert_destinations_mqtt_client_id
    ON alert_destinations(mqtt_client_id)
    WHERE channel_type = 'mqtt' AND mqtt_client_id IS NOT NULL;

-- down
DROP INDEX IF EXISTS ux_alert_destinations_mqtt_client_id;
DROP INDEX IF EXISTS ux_alert_destinations_name;
DROP TABLE IF EXISTS alert_destinations;
```

- 不建 `enabled` 索引：表只有几十行，布尔索引无选择性。
- `updated_at` 由 GORM 维护；`created_at`/`updated_at` 走 UTC。
- 表结构、GORM tag、本文档三处必须一致（`database-guidelines.md §1`）。

### 2.2 配置 JSON（非敏感）

```go
// WebhookConfig, stored in config_json when channel_type = "webhook"
//
// ⚠️ URL 不在这里：平台 webhook 常把 token 内嵌在 URL 查询串里
// （钉钉 access_token、飞书 hook id 等），因此 URL 与凭据一起进 secret_ciphertext；
// 对外仍以 config.url 呈现（已认证管理员可见，口径同相机 RTSP URL）。
type WebhookConfig struct {
    AuthType       string `json:"auth_type"`       // none | bearer | basic
    TimeoutSeconds int    `json:"timeout_seconds"` // 1..30
}

// MQTTConfig, non-secret MQTT settings; client_id is persisted in mqtt_client_id, not config_json
type MQTTConfig struct {
    Broker           string `json:"broker"`             // tcp:// | ssl:// | tls://
    Username         string `json:"username,omitempty"`
    Topic            string `json:"topic"`              // 禁止 + / #
    QoS              uint8  `json:"qos"`                // 0 | 1
    KeepAliveSeconds int    `json:"keep_alive_seconds"` // 0 => 30
    TLSSkipVerify    bool   `json:"tls_skip_verify"`    // 默认 false，UI 显示风险提示
}

// DestinationFilter, stored in filter_json
type DestinationFilter struct {
    AlgorithmIDs []string `json:"algorithm_ids,omitempty"` // 空 = 全部；包 ID，不是版本号
    CameraIDs    []string `json:"camera_ids,omitempty"`    // 空 = 全部
}
```

保存前归一化默认值（webhook `auth_type=none`、超时 5s；MQTT QoS 0、KeepAlive 30s、`tls_skip_verify=false`），保证库内配置、UI 与发送行为一致。MQTT `client_id` 单独存入 `mqtt_client_id`，缺省值为 `"z" + ULID[4:]`（23 位 ASCII 字母数字）；如果唯一索引冲突，则为创建操作重新生成 ULID 并重试。自定义 ClientID 限 1–23 位 ASCII 字母数字，与所有已保存 MQTT 目标（包括禁用目标）唯一；更新时排除当前目标，冲突返回 422 `ALERT_DESTINATION_CONFIG_INVALID`。更新请求中 `config` / `filter` 为指针：缺省=保持，提供=整体替换（不做字段级合并）；更新时 `config.clientId` 缺省或为空表示重置为该目标的派生默认值。

**存储布局 ≠ API 形状**：对外的 `config.url` 在落库时拆进 `secret_ciphertext`（与凭据同密文），读取时拼回响应；`config_json` 只保存 `auth_type` 与超时等非敏感项。

### 2.3 凭据密文

```go
// 加密前的明文 JSON（只保留当前生效的字段）
type webhookSecret struct {
    URL      string            `json:"url"`                // http/https，禁止 userinfo
    Token    string            `json:"token,omitempty"`    // auth_type = bearer
    Username string            `json:"username,omitempty"` // auth_type = basic
    Password string            `json:"password,omitempty"` // auth_type = basic
    Headers  map[string]string `json:"headers,omitempty"`  // 附加静态头（可空）
}

type mqttSecret struct {
    Password string `json:"password,omitempty"`
}
```

**Webhook 鉴权方式（`auth_type`）**：

| 取值 | 发送行为 | 必填凭据 |
| --- | --- | --- |
| `none` | 不加 `Authorization` | — |
| `bearer` | `Authorization: Bearer <token>` | `token` |
| `basic` | `Authorization: Basic base64(username:password)`（等价 `req.SetBasicAuth`） | `username` + `password` |

- `Authorization` 是**保留头**：禁止出现在附加 `headers` 里，避免两个真源；同时禁止 `Content-Type`、`Content-Length`、`Transfer-Encoding`、`Connection`、`Host`（发送器自己负责）。
- 附加 `headers` 用于 `X-API-Key`、租户 ID 等静态头，值同样加密。
- 平台把 token 放在 URL 里（钉钉/飞书/企微）属于 URL 自身语义，无需额外配置；URL 整体加密落库。
- `basic` + `http://` 会在局域网明文传凭据：保存时告警、UI 提示，不阻断（很多现场就是纯 HTTP 局域网）。
- 请求体 HMAC 签名、OAuth2 client credentials、mTLS **不在本阶段**（见 PRD Out of Scope）。

**规范化与校验**：
- 保存时只持久化当前 `auth_type` 用到的字段：切到 `none` 会清掉 `token`/`username`/`password`；`headers` 与登录方式无关，始终保留。
- 生效值校验：`bearer` 令牌为空或 `basic` 用户名/密码为空 → 422 `ALERT_DESTINATION_CONFIG_INVALID`；切换 `auth_type` 时必须提供或已存在对应凭据。
- Webhook URL 解析失败、scheme 非 http/https、含 userinfo → 422；MQTT broker URL 同样禁止 userinfo（凭据用 `username`/`password` 字段）。

- 加密：`camera.LazyCipher.Encrypt(json, "alert-destination:"+id)`；读取时同 AAD 解密。
- 装配期 `camera.LifecycleManager.AddEncryptedDataProbe(alertpushStore)`；`HasExistingEncryptedData` = `SELECT COUNT(*) FROM alert_destinations WHERE secret_ciphertext IS NOT NULL`。漏注册会让 `camera.key` 在误删后被重建，直接毁掉所有目标凭据。
- **凭据只进不出**：响应给 `secretSet`（webhook 另给 `headerNames`），不回传 token/密码/Header 值、不回传掩码。
- **URL 例外**：`url` 存在密文里但会返回给已认证管理员（编辑需要，口径同相机 `rtspUrl`）；任何包含该 URL 的列表、创建、更新、启用、禁用响应都必须设置 `Cache-Control: no-store`，日志只记目标 ID。
- 更新语义：

  | 请求字段 | 行为 |
  | --- | --- |
  | 字段缺省（`nil` / 缺键） | 保持原值 |
  | `token: ""` / `password: ""` / `username: ""` / `headers: {}` | 清除对应凭据 |
  | 非空值 | 全量替换并重新加密 |

  前端禁止把占位符（`******`）当值回传；后端也不识别掩码字符串。

### 2.4 新增配置键

```toml
[alertpush]
# 对外可达的宿主基地址，例如 http://10.0.0.12:8000。
# 留空时 image_mode = "url" 的目标拒绝保存（422 ALERT_PUBLIC_BASE_URL_MISSING）。
public_base_url = ""
# 签名图片 URL 有效期，1m–168h。
signed_url_ttl = "24h"
```

- `cmd/Zhulong/config.go` 新增 `[alertpush]` 结构并 `SetDefault`；`internal/app/config.go` 增加 `AlertPushPublicBaseURL` / `AlertPushSignedURLTTL` 与校验（URL 解析、scheme 限 http/https、去掉尾部 `/`；TTL 在 1m–168h）。
- 不提供每目标覆盖：一个宿主只有一个对外地址，多地址场景由反向代理承担。

---

## 3. 事件与对外载荷契约

### 3.1 `record.AlarmEvent`

```go
type AlarmEvent struct {
    ID               string
    CameraID         string
    AlgorithmID      string // 可能为空（来源未标注/算法已卸载）
    AlgorithmName    string
    AlgorithmVersion string
    AlarmType        string
    Score            float64
    Box              [4]float64 // 归一化
    Extra            map[string]any
    PTS              int64
    CapturedAt       time.Time // UTC
    ImagePath        string    // 全幅图相对路径；ImageReady=false 时为空
    CropPath         string    // 裁剪图相对路径；CropReady=false 时为空
    ImageReady       bool      // 仅表示全幅图已完整写入
    CropReady        bool      // 仅表示裁剪图已完整写入
}

type AlarmListener interface {
    OnAlarm(ctx context.Context, ev AlarmEvent)
}
```

- `ImageReady=false` / `CropReady=false` 时由 `record` 分别清空对应路径。两张图的状态独立；不得用 job 的 `writtenCount > 0` 推导两条路径都有效。
- 事件是值拷贝，监听者可以保留；不暴露 GORM 模型。

### 3.2 对外载荷（外部契约，snake_case，独立于内部 API）

```json
{
  "schema_version": 1,
  "event": "alarm",
  "hostname": "zhulong-edge-01",
  "alarm_id": "01J8Z...",
  "camera_id": "cam_01",
  "algorithm_id": "safety_helmet",
  "algorithm_name": "安全帽佩戴检测",
  "algorithm_version": "1.2.0",
  "alarm_type": "no_helmet",
  "score": 0.94,
  "box": [0.32, 0.15, 0.45, 0.38],
  "pts": 128491024,
  "captured_at": "2026-11-03T08:30:15.123Z",
  "extra": {},
  "image_ready": true,
  "image_url": "http://10.0.0.12:8000/api/v1/media/signed/snapshots/cam_01/2026-11-03/01J8Z.jpg?exp=1762248000&sig=...",
  "crop_url": "http://10.0.0.12:8000/api/v1/media/signed/snapshots/cam_01/2026-11-03/01J8Z_crop.jpg?exp=1762248000&sig=...",
  "image_base64": null
}
```

- `hostname` 由推送进程 `os.Hostname()` 取得（与 `systeminfo` 同源），不引入对 `systeminfo` 服务的依赖；取得失败时为空串。
- `image_ready` 表示外部载荷能否通过当前图片模式提供全幅图，不是内部文件写入标志。`url` 模式下仅在全幅图就绪时签发 `image_url`；`multipart` 模式下仅在 image part 成功构造时为 true；MQTT `base64` 模式下仅在读取成功且不超过 500KB 时为 true。`crop_url` 独立按裁剪图就绪状态签发，不得由 `image_ready` 推导；未提供的图片字段为 `null`。
- `image_base64` 仅在 MQTT `base64` 模式且读取成功、大小不超过 500KB 时填充；缺图、读取失败或超限时不附加 Base64 且将外部载荷 `image_ready` 置为 `false`。multipart 模式的 JSON `payload` 中 `image_base64` 始终为 `null`。multipart 与 MQTT base64 均只传全幅图，裁剪图只在 `url` 模式下作为 `crop_url` 提供。
- `captured_at` 统一 UTC RFC3339Nano；`box` 为归一化坐标；`algorithm_id` 为空时序列化为 `""`。
- 载荷结构变更必须递增 `schema_version` 并在本文档记录。

### 3.3 签名 URL

```
GET|HEAD /api/v1/media/signed/<相对媒体路径>?exp=<unix 秒>&sig=<base64url(HMAC-SHA256)>
签名内容: "v1\n" + 相对路径 + "\n" + exp(十进制)
密钥: <data-dir>/media_url.key，32 字节，0600，首次启动生成
```

- 签名与校验使用**规范化相对路径**（去掉 `/media/signed/` 前缀后的前导 `/`，与 `OpenContent` 入参一致）；校验端额外要求首段为 `snapshots`（`recordings` / `exports` 永不被签名），避免签名能力被扩展到其他根目录。
- 校验：`exp` 必须在未来且不超过签发时 `signed_url_ttl`（服务端签发时保证，校验端只查未过期）；`hmac.Equal` 常量时间比较。
- 通过后调用 `mediafile.Handler.ServeContent`，路径形态/白名单/`os.Root` 校验仍在 `mediafile` 内完成 —— 签名只能授权「已知路径」，不能越狱。
- 错误：403 `MEDIA_URL_INVALID` / `MEDIA_URL_EXPIRED`，其余错误沿用 mediafile 的 404/422。
- 公开路由挂 `httpmiddleware.RateLimit`（每 IP 120 req/min、并发 8）并显式注册 `HEAD`。
- 密钥丢失/轮换只会让未过期 URL 失效，不影响数据；无需 probe 注册。

---

## 4. 并发模型与容灾

### 4.1 图片就绪（record 侧）

1. `CreateAlarmBatch` 成功 → 把 `Alarm` 放入 `pendingAlarms[alarmID]`（mu 保护，硬上限 256），登记抓拍任务。
2. `SnapshotStore` 回调 `OnSnapshotJobDone("alarms", id, result)`；`result` 包含每个输出路径及其写入成功标志。`record` 据此分别设置 `ImageReady` / `CropReady`，并清空对应失败路径。
3. 队列拒绝 / `snapshots == nil` / 超过 256 → 立即以 `ImageReady=false, CropReady=false` 发出，不等待。
4. 兜底 sweeper：500ms tick，超过 5s 未完成的条目按 `ImageReady=false, CropReady=false` 发出并删除（防止抓拍 worker 卡死导致告警永不推送）。
5. `Stop` 取消 sweeper 并把 `accepting` 置 false；此后 `OnSnapshotJobDone` 变为空操作，未完成条目直接丢弃（此时 Dispatcher 已先停，库与图片仍在）。

热路径成本：一次 map 写入 + 一次非阻塞入队，无 JSON 序列化、无文件 I/O。

### 4.2 Dispatcher

- 入口：`Dispatch(ev)` 只在调用方 goroutine 做过滤匹配与 `trySend`，永不阻塞；未 `Start` 或已关闭时直接丢弃并计数，不缓存、不 panic。
- 每目标：`chan Event` 容量 16；满时**丢最旧**（非阻塞收一条再发），`dropped` 计数；日志限流（参考 `logthrottle` 思路）。
- 每目标一个 worker。目标退役（停机或热重载）时先停止新事件入队；worker 使用独立的最多 3s 排空上下文处理已排队事件，完成或超时后才取消在途发送并丢弃剩余队列，最后关闭连接。停机父 context 的取消不得提前取消排空 context。
- 目标数量硬上限 32（创建时 422），内存上界 ≈ 目标数 × 队列深度 × 事件大小（事件只含路径，不含图片字节）。

### 4.3 热重载状态机

```
保存/开关/删除 ──► DB 提交 ──► RequestReload()（容量 1 的合并信号，立即返回）
                                   │
                                   ▼
                         reconcile（串行，单 goroutine）
      1. 读取 enabled 列表，解密凭据，构建「期望目标」快照
      2. 指纹 = SHA-256(channel_type|image_mode|mqtt_client_id|config_json|filter_json|明文 secret)
      3. 差量：指纹变化或已删除 → cancel + Close（锁外）
      4. 新增/变化 → 建 Sender、起 worker；MQTT 状态 = connecting 并异步建连，Webhook 状态 = unverified（无持久连接可探测）
      5. 原子替换 active map；状态写入 statsStore
```

- 连接/发布失败只更新该目标的 `state=failed` + `lastError`，不回滚 DB、不影响其他目标、不阻塞保存接口。
- 状态：`disabled | unverified | connecting | connected | failed`。Webhook 配置变更后为 `unverified`，首次成功测试或实际投递后为 `connected`；失败为 `failed`，下一次成功恢复为 `connected`。MQTT 建连/断线重连期间为 `connecting`，Broker 连接成功后为 `connected`，失败为 `failed`。针对已保存且启用的目标，测试成功/失败同步更新状态与 `lastSuccessAt` / `lastError`；禁用目标测试不改变 `disabled`，草稿测试不改变持久目标状态。
- 不假设「毫秒级」：保存接口的目标是 < 500ms 返回，连接建立由后台完成。

### 4.4 发送器

```go
type Sender interface {
    Send(ctx context.Context, ev Event, img ImageRef) error
    Close() error
}

type ImageRef struct {
    RelPath     string
    CropRelPath string
    ImageReady  bool
    CropReady   bool
    Media       MediaReader // 生产实现是 *mediafile.Service
}

type MediaReader interface {
    OpenContent(ctx context.Context, relPath string) (*os.File, fs.FileInfo, error)
}
```

- Webhook 重试矩阵：网络错误 / 5xx / 429 → 最多 3 次发送尝试（首次 + 至多 2 次重试），退避间隔 0.2s、0.8s 并带抖动；4xx 立即失败；`ctx.Done()` 中止。multipart 每次尝试重新 `OpenContent`。
- HTTP 客户端每目标独立 `http.Transport`（连接池上限 4）、禁用 `http.DefaultClient`；重定向最多 3 跳；跨主机重定向 Go 自动剥离敏感头；响应体 `io.Copy(io.Discard, io.LimitReader(...))` 后关闭以复用连接。
- MQTT：`SetAutoReconnect(true)` + `SetConnectRetry(true)` + `SetConnectRetryInterval(2s)`；发布用 `token.WaitTimeout(publishTimeout)`；离线写队列入有界 ring（丢最旧、计数），不得依赖库的隐式缓冲（行为以测试为准）。QoS1 为至少一次，`alarm_id` 幂等键写入载荷说明。
- 图片读取：url 模式分别按 `ImageReady` / `CropReady` 签发全幅图和裁剪图 URL；multipart 与 MQTT base64 固定只传全幅图，裁剪图不随包传输。multipart 仅在全幅图成功打开并可附加时添加 `image` part，否则只发 `payload` 并将外部 `image_ready=false`；base64 用 `io.LimitReader(f, 500KB+1)`，超限则不附加 Base64、将外部 `image_ready=false` 并计数。对应文件未就绪或读取失败时不得生成其 URL / 文件部分 / Base64。

---

## 5. 安全

| 面 | 措施 |
| --- | --- |
| 凭据落库 | AES-256-GCM + AAD 绑定目标 ID；复用 `camera.key`；注册 `EncryptedDataProbe`；**Webhook URL 也加密**（常内嵌平台 token） |
| 凭据出站 | 响应不回传 token/密码/Header 值；请求三态（保持/清除/替换）；不识别掩码占位符；`Authorization` 等保留头禁止出现在附加 headers |
| 日志 | 不记录 `config_json` / secret / 原始 Header；URL 用 `logsafe.URL`，错误用 `logsafe.ErrorSummary`；`http.Client` 的错误串会带 URL，必须经 logsafe |
| 公开路由 | 仅签名媒体路由；HMAC + 过期 + 限流；不暴露列目录/元信息端点 |
| 出站目标 | 仅 http/https；允许私网地址（局域网平台是主用例）；日志/审计只记录目标 ID 与结果，不记录 URL、主机或凭据；`tls_skip_verify` 默认 false，开启时界面与日志都告警 |
| 自签证书 | 宿主 `tls_mode=auto` 时 `url` 模式的 HTTPS 会被接收方拒绝，文档与 UI 明确提示：使用 `off` 或真实证书 |

---

## 6. HTTP API 契约

| 方法 | 路径 | 请求 | 响应 |
| --- | --- | --- | --- |
| GET | `/api/v1/alert-destinations` | — | `AlertDestinationListResponse`（`{items: []}`，不分页：≤32） |
| POST | `/api/v1/alert-destinations` | `CreateAlertDestinationRequest` | `AlertDestinationResponse` |
| PUT | `/api/v1/alert-destinations/:id` | `UpdateAlertDestinationRequest` | `AlertDestinationResponse` |
| DELETE | `/api/v1/alert-destinations/:id` | — | 空 data |
| POST | `/api/v1/alert-destinations/:id/enable` | — | `AlertDestinationResponse` |
| POST | `/api/v1/alert-destinations/:id/disable` | — | `AlertDestinationResponse` |
| POST | `/api/v1/alert-destinations/test` | `TestAlertDestinationRequest` | `TestAlertDestinationResponse` |

类型命名（`http-api-guidelines.md §4`）：

```go
type AlertDestinationResponse struct {
    ID          string
    Name        string
    ChannelType string
    Enabled     bool
    ImageMode   string
    Config      AlertDestinationConfigInfo
    Filter      AlertDestinationFilterInfo
    SecretSet   bool
    HeaderNames []string            // 仅 webhook
    Status      AlertDestinationStatus
}

type AlertDestinationStatus struct {
    State         string     // disabled|unverified|connecting|connected|failed
    LastError     string     // 稳定原因码，非原始错误
    LastSuccessAt *time.Time
    Dropped       int64
    Queued        int64
}

type AlertDestinationConfigInfo struct {
    URL              string `json:"url,omitempty"`        // 已认证管理员可见；DB 存密文
    AuthType         string `json:"authType,omitempty"`    // webhook: none|bearer|basic
    TimeoutSeconds   *int   `json:"timeoutSeconds,omitempty"`
    Broker           string `json:"broker,omitempty"`
    ClientID         string `json:"clientId,omitempty"`
    Username         string `json:"username,omitempty"`
    Topic            string `json:"topic,omitempty"`
    QoS              *uint8 `json:"qos,omitempty"`
    KeepAliveSeconds *int   `json:"keepAliveSeconds,omitempty"`
    TLSSkipVerify    bool   `json:"tlsSkipVerify,omitempty"`
}

type AlertDestinationFilterInfo struct {
    AlgorithmIDs []string `json:"algorithmIds"`
    CameraIDs    []string `json:"cameraIds"`
}

// 仅出现在请求里（测试连接）；响应永不包含。
type AlertDestinationSecretInfo struct {
    Token    *string           `json:"token,omitempty"`    // bearer
    Username *string           `json:"username,omitempty"` // basic
    Password *string           `json:"password,omitempty"` // basic / mqtt
    Headers  map[string]string `json:"headers,omitempty"`  // 附加头，不含 Authorization
}

type TestAlertDestinationRequest struct {
    ID          string // 可选：已保存目标，Secret 缺省时用库内值
    ChannelType string
    ImageMode   string
    Config      AlertDestinationConfigInfo
    Secret      *AlertDestinationSecretInfo
}
```

**存储 JSON 用 snake_case，对外 API 用 camelCase**（项目既有约定）；两者之间的转换集中在 `ToResponse` / `ToConfig`，禁止把存储结构直接序列化给前端。

- `CreateAlertDestinationRequest` / `UpdateAlertDestinationRequest` 均含 `name` / `config` / `filter` / `secret` 四块；`secret` 为写密对象，按 §2.3 三态处理，创建时 `bearer`/`basic` 必须提供对应凭据。
- MQTT API 的 `config.clientId` 与 `mqtt_client_id` 列双向映射；该字段不写入 `config_json`，变更参与 reconcile 指纹。
- 所有返回 `AlertDestinationResponse` 的接口（列表、创建、更新、启用、禁用）均带 `Cache-Control: no-store`，因为响应含解密后的 webhook URL；不返回 URL 的测试/删除响应不依赖该字段。
- 更新请求**不包含** `enabled`：开关只走 `enable` / `disable`，避免两个写入真源。
- 列表沿用「极小上限不分页」特例，但结构保持 `{items: [...]}`，空数组不为 `null`。
- 测试接口返回 200 + `{ok, latencyMs, reason}`；`reason` 是固定枚举（`dns`/`connection_refused`/`timeout`/`tls`/`auth`/`http_status`/`not_connected`/`publish_failed`），UI 负责本地化；服务端不返回目标响应体与原始错误。
- 错误码：`ALERT_DESTINATION_NOT_FOUND`、`ALERT_DESTINATION_NAME_CONFLICT`、`ALERT_DESTINATION_CONFIG_INVALID`、`ALERT_IMAGE_MODE_UNSUPPORTED`、`ALERT_PUBLIC_BASE_URL_MISSING`、`MEDIA_URL_INVALID`、`MEDIA_URL_EXPIRED`，三语注册在 `internal/alertpush/locale.go`。
- 写操作 `audithttp.Tag` 动作：`alertpush.create|update|delete|enable|disable|test`（`internal/audit/model.go` 增加常量）。
- 审计动作是**跨层契约**：新增动作必须同时登记 `web/src/features/audit/data/audit-actions.ts` 与 `web/src/features/audit/locales/{zh-Hans,zh-Hant,en}.json` 的 `actionNames`；Go 侧与前端各有守卫测试，漏一处会让审计列表回退成裸 key。

---

## 7. 装配与生命周期

```go
// internal/app/app.go
type alertPushOut struct {
    fx.Out

    Dispatcher      *alertpush.Dispatcher
    Handler         *alertpush.Handler
    Lifecycle       alertPushLifecycle
    ProtectedRoutes RouteRegistrar `group:"protected_routes"`
    PublicRoutes    RouteRegistrar `group:"public_routes"`
}

type alertPushParams struct {
    fx.In

    DBProvider    database.DBProvider
    CameraCipher  *camera.LazyCipher
    CameraManager *camera.LifecycleManager
    MediaService  *mediafile.Service
    MediaHandler  *mediafile.Handler
    RecordService *record.Service
    Config        Config
    Logger        *zap.Logger
}

func provideAlertPush(p alertPushParams) alertPushOut {
    comp := alertpush.NewComponents(alertpush.Options{
        DBProvider:   p.DBProvider,
        Cipher:       p.CameraCipher, // 复用 camera.key
        MediaService: p.MediaService, // OpenContent：发送器读图
        MediaHandler: p.MediaHandler, // ServeContent：签名路由复用字节服务
        Config: alertpush.Config{
            PublicBaseURL: p.Config.AlertPushPublicBaseURL,
            SignedURLTTL:  p.Config.AlertPushSignedURLTTL,
        },
        Logger: p.Logger,
    })
    p.CameraManager.AddEncryptedDataProbe(comp.Store) // 装配期，先于 InitCipher
    p.RecordService.SetAlarmListener(comp.Dispatcher)
    return alertPushOut{
        Dispatcher:      comp.Dispatcher,
        Handler:         comp.Handler,
        Lifecycle:       comp.Lifecycle,
        ProtectedRoutes: comp.ProtectedRoutes,
        PublicRoutes:    comp.SignedMediaRoutes, // 公开签名媒体路由
    }
}
```

启动/停止顺序（`internal/app/runtime.go`）：

- **启动**：`camera.InitCipher` / `camera.Start` 之后（要解密凭据），`algorithm.Start` 之前（AI 结果开始流动前就绪）。启动失败按可降级子系统处理（`degradedStart`），不阻断管理面。
- **停止**：`stopSequence` 中位于 `closeAlgorithm` 之后、`closeRecordAndSnapshot` 之前 —— 先确保不再产生新告警，再停止接收并排空，最后才停 record/抓拍。
- 新增 `alertPushReady` 标志并纳入 `TestStartupRollbackSharesShutdownOrder` 的事件轨迹。

---

## 8. 前端

- 目录：`web/src/features/settings/alert-push/{api,hooks,components,locales,index.tsx}`；路由 `web/src/routes/_authenticated/settings/alert-push.tsx`；侧边栏项加在 `web/src/features/settings/index.tsx`（`BellRing`）。
- i18n：`web/src/lib/i18n.ts` 注册 `alertPush` 命名空间（三语 import），`web/src/locales/{zh-Hans,zh-Hant,en}/common.json` 增加 `nav.AlertPush`；`SUPPORTED_LANGUAGES` 不变。
- 列表：目标数 ≤32，声明为不分页特例；`@/components/ui/card` 或 `@/components/ui/table` 组合，状态/协议/图片模式徽章统一用 `@/lib/status-palette` 的 Dot-Indicator Outline 范式（含 `dark:`）；实现 `unverified`、`connecting`、`connected`、`failed`、`disabled` 状态文案。
- 表单：`react-hook-form` + `@hookform/resolvers/zod` + `@/components/ui/form`，zod 判别联合按 `channelType` 校验；`imageMode` 选项按协议过滤；Webhook 鉴权方式为 `none|bearer|basic` 单选，`bearer` 显示 token、`basic` 显示用户名/密码；附加 Header 编辑器禁止 `Authorization` 等保留头；密码/token/Header 值为写密字段（占位显示「已设置」），留空表示保持、勾选「清除」显式清除。
- 「测试连接」：保存前可用草稿配置测试；pending 期间禁用按钮；失败展示本地化原因码；不关闭对话框。
- 交互三态（loading/error/empty）与删除二次确认遵循现有 `confirm-dialog` 模式。

---

## 9. 测试策略

| 层 | 覆盖 |
| --- | --- |
| `store` | 迁移往返、唯一名冲突、三态凭据更新、`HasExistingEncryptedData`、密文不可明文读取 |
| `signing` | 签发/校验、过期、篡改、路径越狱被 mediafile 拒绝 |
| `filter` | 空=全量、算法/相机组合、`AlgorithmID=""` 只命中空过滤器 |
| `sender_webhook` | JSON 绝对 URL、multipart 字节一致、500→重试成功、400→不重试、超时、响应体不进日志；bearer/basic/none 三种鉴权头正确；保留头 422；切鉴权方式缺凭据 422 |
| `sender_mqtt` | 进程内 Broker（`github.com/mochi-mqtt/server/v2`，仅测试依赖，支持 v3.1.1）：连接、QoS1 发布、base64 解码一致、ClientID 长度/字符校验、生成与自定义 ID 唯一性、ClientID 变化触发重载、无图降级 |
| `dispatcher` | 单目标挂起不影响其他目标、丢最旧与计数、重载差量关闭旧连接、退役时先排空最多 3s 再取消、关闭后 Dispatch 不阻塞不 panic |
| `record` 集成 | 告警落库后监听恰好一次；抓拍延迟 1s 仍可读；全幅/裁剪图分别覆盖成功、失败及部分写入，失败路径清空；队列满与 5s 兜底两张图都未就绪；批量插入失败不触发 |
| `handler` | CRUD 校验矩阵、协议×图片模式 422、测试接口原因码、审计留痕、信封与三语；所有返回 URL 的响应均断言 `Cache-Control: no-store` |
| `app` | 签名路由免登录可取字节、未签名内容路由仍 401、启停顺序轨迹 |
| 前端 | 真实无头浏览器：表单校验、协议切换字段、启停、Webhook 未验证状态、测试成功/失败状态变化、测试连接错误渲染、凭据不被回传 |

## 10. 已定决策（不可轻改）

1. **签名密钥独立为 `<data-dir>/media_url.key`**，不复用 `camera.key`、不入库。
   理由：URL 签名密钥失效半径有界（单图 + TTL），删除/轮换只影响未过期 URL；而 `camera.key` 受
   `EncryptedDataProbe` 保护，缺失时会被拒绝重建，媒体 URL 不应被静态凭据密钥的生命周期连带卡死。
2. **`url` 模式要求全局 `[alertpush] public_base_url`**，不做自动推导、不做每目标覆盖。
   理由：监听地址 ≠ 对外地址（`127.0.0.1` 默认、多网卡、反代、4G），推导必然是猜；留空时保存即 422。
3. **接收方零配置**：签名是 bearer 凭证，平台只需普通 GET，不需要密钥、不需要验签；风险用
   「单张图 + TTL」圈定，而不是靠平台配合。
4. **Webhook 鉴权 = `none | bearer | basic` + 附加静态 Header**；`Authorization` 是保留头，
   不允许出现在附加 headers 里。请求体 HMAC 签名 / OAuth2 / mTLS 不在本阶段（流式 multipart
   无法在写 header 前完成整体签名）。URL 与凭据一同加密，因为平台 token 常内嵌在 URL 查询串。

## 11. 风险与回滚

- **抓拍慢于推送**：由 observer + 5s 兜底保证不必等待；图片缺失时只推元数据，绝不发指向不存在文件的 URL。
- **内存**：事件只含路径；每目标队列 16；base64 单张 ≤500KB，目标数 ≤32。
- **移动网络流量**：base64 模式单条约 667KB，建议在 UI 提示；不提供自动降采样（保真优先）。
- **Broker 限制**：base64 载荷可能超过 Broker 默认 1MB 包上限，测试与文档中说明。
- **时钟**：签名 URL 依赖宿主时钟；`systemtime` 未同步时 URL 可能提前过期，NTP 未同步时 UI 提示。
- **回滚**：前端一键禁用目标；必要时执行 `000026` down；删除 `media_url.key` 仅使未过期 URL 失效。
