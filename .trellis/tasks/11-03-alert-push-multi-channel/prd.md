# 告警上报模块与多通道分发 (Alert Push & Multi-Channel Dispatch)

## Goal

在 Zhulong 边缘智能宿主中构建高性能、高可靠的告警推送模块（`internal/alertpush`），支持在 Web 管理界面可视化配置多个上报目标（HTTP Webhook 与 MQTT），按算法包与相机维度细粒度分流，提供「局域网签名 URL」与「跨网随包图片（Multipart 文件流 / Base64 内嵌）」两种图片传输方式，目标增删改零重启热重载，并支持一键连通性测试。

本阶段**只推送 `alarms` 表的告警**（`detection` / `classification` 两类推理任务产生），不推送识别记录与普通抓拍，也不做历史补发。

---

## Background & Confirmed Facts

1. **告警产生链路**：
   - 算法推理结果由黑盒 Runner 经 FrameBroker/SHM 输出至 `internal/camera/stream_hub.go`（`StreamHub` 只有**一个** `SetAIConsumer` 槽位，不能也不需要为推送再占一个消费者）；
   - `internal/record/service.go` 的 `ProcessInferenceResult` 是**唯一**的告警生产者，负责跟踪去重、冷却降频（Cooldown）、`alarms` 落库与抓拍任务登记；
   - 告警记录在 `CreateAlarmBatch` 成功后数据最完整（算法名/版本快照、坐标框、置信度、相对图片路径），这是推送的唯一切入点。
2. **抓拍是异步的（决定了推送时机）**：
   - `record.Service` 先把**预填的相对路径**写库，再把抓拍任务投进 `SnapshotStore` 的**全局容量 32、单 worker** 有界队列；`os.WriteFile` 不是原子操作；
   - `.trellis/spec/backend/record-guidelines.md` 明确：非空路径**不保证**文件已存在，队列满/写门关闭时路径可能被异步清空；
   - 一个抓拍任务可能只写成功全幅图或裁剪图中的一张；不能用 job 级成功布尔值推断每条路径都可读，事件必须携带逐输出就绪状态；
   - 因此「落库后立刻读图」会随机拿到不存在或写了一半的 JPEG，`multipart` / `base64` 模式必须等抓拍完成信号（见 R2）。
3. **媒体读取与鉴权边界**：
   - 图片只能按**媒体根内相对路径**（`snapshots/...`）存储，读取必须经 `internal/mediafile` 的四层防护（路径形态、`os.Root`、`st_dev`、首段白名单），**禁止自行拼接绝对路径 `os.Open`**；
   - 现有内容端点是 `/api/v1/media/content/*`，挂在 `protected_routes` 上，鉴权**只认** `zhulong_session` Cookie —— 外部平台无法直接拉取；
   - 因此 `url` 模式必须由宿主签发**有时效的签名 URL**，走新增的公开只读路由（见 R7），并依赖部署配置的对外基地址。
4. **凭据存储既有安全模型**：
   - 相机与 GB28181 凭据均以 AES-256-GCM 加密落库，密钥文件为 `<data-dir>/camera.key`（0600），`camera.LazyCipher` 明确导出给其他域复用；
   - `camera.LifecycleManager.AddEncryptedDataProbe` 用于阻止「密钥缺失但仍有密文」时重建密钥毁数据；
   - GB28181 的对外响应**从不返回**密码，更新请求用 `*string` 区分「未提供=保持」。
5. **工程约束**：
   - 数据库迁移版本目前至 `000025`，新增表必须写 `000026_create_alert_destinations.up.sql` / `.down.sql`，严禁 AutoMigrate；
   - 长驻子系统必须接入 `internal/app/runtime.go` 的显式启停序列（`stopSequence` 唯一真源），构造期保持纯粹；
   - 严格配置（`UnmarshalExact`）：新增 `config.toml` 键必须同步 `cmd/Zhulong/config.go` 与 `internal/app/config.go`；
   - 外部接口遵守 `httputil` 信封、`apperr` 错误码与三语本地化；类型名用 `*Request` / `*Response` / `*Info` / `*Status`。

---

## Requirements

### R1: 持久化与数据模型
- 新增 `alert_destinations` 表（迁移 `000026`），字段：ULID 主键（`TEXT`，26 字符）、`name`（唯一）、`channel_type`（`webhook`/`mqtt`）、`enabled`、`image_mode`（`url`/`multipart`/`base64`）、`mqtt_client_id`（仅 MQTT，持久化且唯一）、`config_json`（非敏感配置：鉴权方式、超时等）、`secret_ciphertext`（敏感密文：Webhook URL、鉴权凭据、附加 Header 值、MQTT 密码）、`filter_json`、`created_at` / `updated_at`（UTC `DATETIME`）。
- 敏感凭据（MQTT 密码、Webhook 鉴权 token/密码、附加 Header 值、**Webhook URL**）**加密落库**：复用 `camera.LazyCipher` 的 AES-256-GCM，AAD 绑定 `alert-destination:<id>`；装配期注册 `AddEncryptedDataProbe`，`HasExistingEncryptedData` 覆盖本表。Webhook URL 之所以加密：钉钉/飞书/企微等平台把 token 直接内嵌在 URL 查询串里。
- 查询接口**永不返回**凭据明文或掩码：只返回 `secretSet`（webhook 另给 header 名称列表）；URL 例外——返回给已认证管理员用于编辑（口径同相机 `rtspUrl`），响应加 `Cache-Control: no-store`。
- 更新语义：字段**缺省=保持**、显式空值=清除、非空=替换；切换鉴权方式时按合并后的生效值校验。
- 敏感值不得进入日志；URL 只经 `internal/logsafe` 记录。

### R2: 告警事件与异步分发中枢
- 在 `internal/record` 定义只读事件与监听者接口（避免 `record` 反向依赖 `alertpush`）：
  - `AlarmEvent`：告警 ID、相机、算法 ID/名称/版本、类型、分数、归一化框、额外属性、PTS、抓拍时间、全幅图与裁剪图相对路径，以及分别表示两者就绪状态的 `ImageReady` / `CropReady`；
  - `AlarmListener { OnAlarm(ctx, AlarmEvent) }`；`record.Service.SetAlarmListener` 在装配期设置，启动后不可变，未设置时为零开销。
- **事件只在 `CreateAlarmBatch` 成功后发出，且每条告警恰好一次**；发出时机由抓拍完成信号决定（`SnapshotStore` 增加逐输出结果观察者）。`ImageReady` 专指全幅图，`CropReady` 专指裁剪图；每个标记只由对应文件写入结果决定，未就绪的对应路径必须清空，不能因另一张图写成功而生成死链。队列满、写门关闭、存储不可用或 5s 等待超时均按两张图都未就绪发出。
- 监听回调不得在推理消费 goroutine 上执行阻塞操作；`Dispatcher.Dispatch` 只做规则匹配与非阻塞入队。
- 每个启用的目标拥有独立有界队列与工作协程，目标间彻底故障隔离；队列满时丢弃最旧消息并计数，绝不反压主流水线。
- 停机时禁止在关闭后的 Dispatcher 上 panic 或阻塞；未等到抓拍完成的告警允许丢弃（库中记录与图片仍在）。

### R3: 细粒度过滤引擎
- 每个目标支持 `algorithm_ids` / `camera_ids`，留空表示全量匹配。
- `algorithm_ids` 匹配算法包 ID（非版本号）。告警的 `algorithm_id` 可能为 NULL（算法已卸载），NULL 只命中空过滤器；`filter_json` 中已不存在的相机/算法 ID 永不命中，且不自动清理。

### R4: HTTP Webhook 发送器
- `url` 模式：`application/json`，图片字段为**绝对签名 URL**（见 R7），无 `public_base_url` 配置时该模式在保存阶段即被拒绝（422）。
- `multipart` 模式：`multipart/form-data`，`payload` 部分为 JSON、`image` 部分为 JPEG 文件流（从 `mediafile` 流式读取，不整块进内存）。`image` 固定传全幅图；全幅图未就绪时只发送 `payload`，不附加图片部分。裁剪图不通过 multipart 传输。
- **鉴权方式可配置**：`none` / `bearer`（`Authorization: Bearer <token>`）/ `basic`（用户名+密码）；另有附加静态 Header（如 `X-API-Key`、租户 ID），但 `Authorization`、`Content-Type`、`Content-Length`、`Transfer-Encoding`、`Connection`、`Host` 为保留头，配置时 422 拒绝。平台把 token 放在 URL 里时无需额外配置（URL 整体加密）。
- `basic` + `http://` 明文传凭据：保存时告警、UI 提示，不阻断局域网部署。
- 每目标超时（1–30s），最多 3 次发送尝试（首次发送加至多 2 次重试），网络错误/5xx/429 的两次重试间隔为 200ms、800ms 并加入抖动；4xx 不重试；multipart 每次重试重建 body。
- 图片按各自路径的就绪状态处理：`url` 模式可独立发送已就绪的全幅图或裁剪图 URL；`multipart` / `base64` 固定只携带全幅图，全幅图缺失时降级为纯元数据推送。任何模式都不得发送指向不存在文件的 URL 或图片字节。
- 请求体 HMAC 签名、OAuth2 client credentials、mTLS 不在本阶段。

### R5: MQTT 发送器
- 客户端库固定 `github.com/eclipse/paho.mqtt.golang` **≥ v1.5.1**（v1.5.0 含 GO-2025-4173 / CVE-2025-10543）。
- 支持 Broker URL、ClientID（缺省生成 23 字符 ASCII 字母数字 ID，值为 `"z" + ULID[4:]` 并持久化；唯一索引冲突时重新生成目标 ULID。自定义值限制为 1–23 位 ASCII 字母数字，所有 MQTT 目标含禁用目标均唯一，冲突返回 422）、用户名/密码、Topic（禁止 `+`/`#`）、QoS 0/1、KeepAlive、可选 TLS 跳过校验（默认关闭并告警）。
- 仅支持 `url` 与 `base64` 两种图片模式；`base64` 全幅图上限 500KB，超出时不附加 Base64、将外部载荷的 `image_ready` 置为 `false` 并计数；内部 `AlarmEvent.ImageReady` 仍表示磁盘上的全幅图写入成功。
- 断线重连期间的离线策略显式定义（有界队列、丢最旧、计数），不得假定客户端库自动缓冲；QoS1 为至少一次投递，用 `alarm_id` 作为幂等键并写入契约文档。

### R6: CRUD、热重载与连通性测试 API
- REST 接口挂 `protected_routes`：列表、创建、更新、删除、启用、禁用、测试。
- 保存/开关**立即返回**，后台异步 reconcile；目标状态（`unverified`/`connecting`/`connected`/`failed`/`disabled`）与 `lastError`、`lastSuccessAt`、`dropped`、`queued` 随列表返回。Webhook 是无状态 HTTP 目标：配置变更后为 `unverified`，首次成功测试或实际投递后为 `connected`；MQTT 建连期间为 `connecting`，连接成功后为 `connected`。失败后为 `failed`，恢复成功后更新为 `connected`。已禁用目标始终显示 `disabled`；对其执行测试只返回测试结果，不启用目标或改变该状态。
- reconcile 串行化、在锁外建立连接；按「解密后的完整配置」指纹判定变更，变更/删除的旧连接优雅关闭，新增/变更的目标重建。
- `POST /alert-destinations/test` 支持「草稿配置」与「已保存目标 + 不重传凭据」，超时上限 10s，返回稳定原因码（`dns` / `connection_refused` / `timeout` / `tls` / `auth` / `http_status` / `not_connected` / `publish_failed`），不泄露目标响应体。
- 写操作纳入审计（`alertpush.create|update|delete|enable|disable|test`），新增错误码进模块 `locale.go`（三语）。

### R7: 对外图片访问（签名 URL，新增，属本任务范围）
- 新增公开只读路由 `GET|HEAD /api/v1/media/signed/*path?exp=<unix>&sig=<base64url>`：HMAC-SHA256（key = `<data-dir>/media_url.key`，32 字节，0600，首次启动生成；丢失只影响未过期 URL，不影响数据），签名覆盖 `v1\n<相对路径>\n<exp>`，常量时间比较；过期返回 403 `MEDIA_URL_EXPIRED`，伪造返回 403 `MEDIA_URL_INVALID`。
- 字节服务复用 `mediafile` 的 `ServeContent`（四层防护 + Range + HEAD），签名路由不得绕开路径校验；路径首段仍限 `snapshots`。
- 新增配置：
  ```toml
  [alertpush]
  # 对外可达的宿主基地址，如 http://10.0.0.12:8000；留空时 url 模式不可用。
  public_base_url = ""
  # 签名图片 URL 有效期（1m–168h）。
  signed_url_ttl = "24h"
  ```
- 公开路由需挂 IP 限流（参考 `httpmiddleware.RateLimit`），不记审计。
- **接收方零配置**：签名由宿主单方计算，第三方平台只需对 `image_url` 发起普通 GET（不带 Cookie/密钥）；`media_url.key` 永不出宿主。
- `url` 模式的 URL 在发送时生成，**不得入库**（媒体规范禁止把 URL 固化进数据库）。

### R8: 前端管理界面
- 系统设置侧边栏新增「告警推送」（`BellRing`），路由 `/_authenticated/settings/alert-push`，特性切片 `web/src/features/settings/alert-push/`。
- 目标列表：名称、协议 Badge、图片模式 Badge、启停 Switch、状态（连接态/最近错误；Webhook 未测试前显示 `unverified`）、操作（编辑/测试/删除）；数量有物理上限（≤32），按规范声明为不分页特例。
- 配置对话框：`react-hook-form` + `zod` 判别联合（按 `channelType`）；图片模式选项按协议过滤；过滤区多选算法包与相机；**Webhook 鉴权方式选择（none/bearer/basic）与附加 Header 编辑器**（`Authorization` 不可选）；凭据字段写密不回显（留空保持、显式清除）；「测试连接」展示 pending 与稳定原因码。
- 三语（zh-Hans / zh-Hant / en），依次注册 `web/src/lib/i18n.ts` 命名空间与 `web/src/locales/*/common.json` 导航文案；所有目标状态（含 `unverified`）均有三语文案。
- 状态徽章必须使用 `@/lib/status-palette` 的 Dot-Indicator Outline 范式。

---

## Acceptance Criteria

- [ ] **AC1（迁移与 CRUD）**：`000026` up 建表、down 清空；表内 `name` 唯一，`mqtt_client_id` 对所有 MQTT 目标（含禁用目标）唯一；缺省 ClientID 为 23 位 ASCII 字母数字，非法长度/字符或自定义 ID 冲突返回 422；CRUD 全部走信封，非法协议/图片模式组合返回 422。
- [ ] **AC2（URL 模式）**：触发告警后目标收到 `application/json`；全幅图与裁剪图分别仅在各自就绪时带绝对签名 URL，接收方无需 Cookie 即可取到对应磁盘字节；全幅图失败但裁剪图成功时只返回 `crop_url`，反向部分失败也不返回死链；篡改签名与过期 URL 均返回 403。
- [ ] **AC3（Multipart 模式）**：`payload` + 全幅 `image` 两段；`image` 字节与磁盘全幅 JPEG 完全一致，抓拍延迟 1s 的用例仍成立；全幅图未就绪或读取失败时只发送 `payload`、不发送裁剪图或死链，并令 `image_ready=false`。
- [ ] **AC4（MQTT Base64 模式）**：目标 Topic 收到消息；全幅 `image_base64` 解码后与磁盘 JPEG 完全一致；全幅图未就绪或超过 500KB 时 `image_ready=false` 且无 `image_base64`。裁剪图不通过 base64 传输。
- [ ] **AC5（细粒度过滤）**：目标 A 绑定算法 1、目标 B 绑定算法 2；算法 1 告警只到 A；算法已卸载（`algorithm_id` 为 NULL）的告警只命中空过滤器目标。
- [ ] **AC6（故障隔离）**：配置一个挂起 5s 的 Webhook 与一个不可达 Broker，主推理落库耗时不受影响（对照基线），另一正常目标仍在 100ms 内收到。
- [ ] **AC7（热重载与测试）**：修改配置/开关后保存接口在 500ms 内返回（即使 Broker 不可达），新配置立即生效；Webhook 在首次成功测试或投递前显示 `unverified`，MQTT 建连时显示 `connecting`，失败显示 `failed` 并带稳定原因；已启用目标测试成功/失败会同步更新状态，已禁用目标测试不改变 `disabled`；测试在 10s 内返回明确原因码。
- [ ] **AC8（凭据安全）**：数据库内 URL 与凭据均为密文；API 不返回 token/密码/Header 值；所有返回 Webhook URL 的响应（列表、创建、更新、启用、禁用）均有 `Cache-Control: no-store`；更新时缺省字段保持原值、显式空值清除；日志中无凭据与完整 URL。
- [ ] **AC9（Webhook 鉴权）**：`bearer` 目标收到 `Authorization: Bearer ...`；`basic` 目标收到正确的 Basic 凭据；附加 Header 生效；保留头配置返回 422；切换鉴权方式但缺凭据返回 422；响应不含 token/密码/Header 值，数据库内 URL 与凭据均为密文。
- [ ] **AC10（质量门禁）**：`python3 native/scripts/build.py go test -race ./internal/alertpush/... ./internal/record/...` 通过；`cd web && pnpm test -- src/features/settings/alert-push && pnpm build` 通过；`make check` 通过。

## Out of Scope

- 私有非标硬件协议（海康 ISUP / 大华主动注册）；
- C++ 原生引擎层的任何网络推送（纯 Go 宿主层 + React 前端）；
- 识别记录 / 普通抓拍的推送，以及历史告警补发；
- 外部重型消息队列（Kafka / RabbitMQ 集群）；
- Webhook 请求体 HMAC 签名、OAuth2 client credentials、mTLS（本阶段只做静态凭据：none/bearer/basic/附加 Header）；
- 推送内容模板化 / 可编排规则引擎（本阶段载荷结构固定并带 `schema_version`）。
