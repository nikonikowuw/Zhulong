# Design: 2026-11-22-unified-pluggable-filestore

## 架构与实现方案

### 1. 分层定位与包依赖约束

为守住 `internal/mediafile` 禁止拖入 CGO `engine` 链路（`go test -race` 可裸跑）以及单向依赖铁律：

- 新增底层基础设施包 `internal/filestore`，**仅依赖标准库、S3 客户端库及 `internal/apperr` / `zap`**，严禁导入 `internal/engine`、`internal/camera` 或任何上层业务包。
- `storage` 凭据加密通过窄接口 `SecretCipher`（`Encrypt(string) (string, error)` / `Decrypt(string) (string, error)`）由 `internal/app` 适配器注入 `camera.LazyCipher`，避免 `storage` / `filestore` 依赖 `camera`。

### 2. 统一逻辑 Key 与核心抽象（`internal/filestore`）

放弃暴露 `*os.File` 与绝对路径，全库（`recognitions`、`captures`、`alarms`、`event_clips`、`person_faces`、`canvas_maps`）统一持久化相对逻辑 Key，格式为 `{namespace}/{subpath}`：

- 媒体域命名空间：`snapshots/`、`recordings/`、`exports/`（继续由 `/api/v1/media/*` 暴露）。
- 业务资产命名空间：`faces/`、`maps/`（仍分别由 `person` 与 `spatial` 鉴权端点暴露；`mediafile` 保持拒绝非媒体域前缀，维持权限隔离）。

核心接口设计：

- **`Store`**：
  - `Put(ctx context.Context, key string, r io.Reader, size int64, opts PutOptions) error`
  - `Open(ctx context.Context, key string) (ReadSeekCloser, ObjectInfo, error)`（支持 `io.Reader`、`io.Seeker`、`io.Closer`）
  - `Stat(ctx context.Context, key string) (ObjectInfo, error)`
  - `Delete(ctx context.Context, key string) error`
  - `Exists(ctx context.Context, key string) (bool, error)`
  - `Health(ctx context.Context) (HealthStatus, error)`
- **可选能力接口（Capability Interfaces）**：
  - `Presigner`：`PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)`
  - `LocalMountInspector`：供本地驱动上报 `st_dev` / 挂载点 / 可用空间遥测。
- **统一 Key 校验（`ValidateKey`）**：保留原 `mediafile/resolver.go` 第一层与第四层防护——拒绝空值、NUL 字节、`>512` 字节、绝对路径、`.`/`..` 段、非法字符集及未知命名空间。

### 3. 双驱动与 Range Seek 实现

1. **`LocalDriver`**：
   - 读取/删除继续使用 `os.OpenRoot(root)`（含相对软链放行、绝对/逃逸软链拒绝、以及建/换句柄时的一次性 `st_dev` 掉盘穿透检测）。
   - 写入统一通过同目录临时文件 + 原子 `os.Rename` 落盘，消除半截文件问题；允许按命名空间或根配置映射（兼容独立 `facesDir` 历史配置）。
2. **`S3Driver`（覆盖 MinIO 与兼容 S3 协议的阿里云 OSS / 腾讯云 COS / AWS S3）**：
   - 使用标准 S3v4 客户端，支持自定义 `Endpoint`、`Region`、`Bucket`、`KeyPrefix`、`UsePathStyle`（MinIO 常用）及 TLS 配置。
   - **按需 Range `ReadSeekCloser`**：基于 `HeadObject`/`GetObject` 的 `Range: bytes=start-end` 封装按需分块读取器实现 `io.ReadSeeker`，保证 `http.ServeContent` 处理 MP4 拖拽播放（206 Partial Content）时**不把整个视频读入内存**，且复用现有 Range 边界行为（如 `bytes=abc`→416、`bytes=-0`→206 零体）。
   - `PresignGet` 生成短时效签名直链，配合配置的 `deliveryMode`（`proxy` | `redirect`）：默认 `proxy` 由宿主按现有认证链路中转；选择 `redirect` 时 `/media/content/*` 返回 `302/307` 将大视频流量卸载到 MinIO/云端。

### 4. 配置热切换、密钥安全与存量兼容

- **配置结构扩展（`StorageConfig`）**：
  - 保留原有 `MediaDirectory` 及水位/保留策略字段，新增 `Backend`（`"local"` 默认 | `"s3"`）、`DeliveryMode`（`"proxy"` 默认 | `"redirect"`）、`SpoolDirectory`、`SpoolMaxMB` 与 `S3` 结构（`Endpoint`、`Region`、`Bucket`、`Prefix`、`UsePathStyle`、`UseTLS`、`AccessKeyID`、`SecretAccessKeyCiphertext`、`QuotaGB`）。
  - 对外 JSON 响应与审计详情仅输出脱敏配置（`HasSecretAccessKey: bool`、隐藏真实 Key）；更新配置时如未提交新 `secretAccessKey` 则保留原密文。
  - 启动期在 `camera.LifecycleManager` 注册 `storage` 密文探测，防止密钥丢失后被静默重置。
- **`filestore.Manager`（热切换与历史只读回落）**：
  - 每次获取激活驱动通过读写锁读取快照，配置变更（`UpdateConfig`）时构造新驱动并原子替换，旧 `LocalDriver` 的 `os.Root` 句柄仅在锁保护下安全替换，在途已打开请求不受影响。
  - **切换不致历史 404 策略**：支持可选的历史本地只读回落开关（当主后端为 `s3` 且 `Get/Stat` 返回 `NotFound` 时，若本地旧 `MediaDirectory`/`facesDir`/`mapsDir` 存在该 Key 则只读回落），并可触发基于 `internal/asyncjob` 的后台存量迁移作业将本地资产批量同步到对象存储。

### 5. 事件录像 Local Spooling 与去扫盘化清理

1. **事件录像（`internal/recording` + C++ `Fmp4Writer`）**：
   - C++ `Zhulong_stream_clip_trigger` 保持接收本地绝对路径不变：当后端为 `local` 时直接指向目标路径（零额外拷贝）；当后端为 `s3` 时写入 `<spoolDir>/recordings/...`。
   - 新增迁移为 `event_clips` 增加 `storage_backend` 与 `upload_status`（`local` / `pending` / `uploaded` / `failed`）列。`PollOnce` 收到片段完成事件后将其投入有界异步上传队列 `Put` 至对象存储；上传成功并落库后删除本地 Spool 副本；上传完成前读取自动回落 Spool。
   - 重启 `RepairLeftovers` 处理中断的 `pending` 片段，停机时在 `stopSequence()` 中先排空上传队列再停止 `storage` 与 `database`。
2. **生命周期清理与熔断（`internal/storage`）**：
   - `local` 模式：保持现有双水位回差清理、`statfs` 探测与 `st_dev` 掉盘熔断。
   - `s3` 模式：取消对远端全桶 `WalkDir` 与 `.lock` 旁车对象检查；过期清理与配额淘汰统一由 SQLite 索引分批查询（经 `EvidenceGuard` 保护 `alarms`）后调 `filestore.Delete`，再经现有 `pending_snapshot_deletes` outbox 协调删库；`EmergencyGate` 转而监控本地 `SpoolDirectory` 磁盘剩余空间与上传队列背压，本地盘触碰红线时触发停写保护 SQLite WAL。

### 6. 业务域与前端整合

- **`mediafile`**：`Service` 改为依赖 `filestore.Store`，移除 `*os.File` 返回值（改为 `filestore.ReadSeekCloser`）；`Handler` 保留四层路径防护（非媒体命名空间仍拒绝 `422 MEDIA_KIND_INVALID`）、显式 `HEAD`、503 映射与无审计记录契约，并按 `DeliveryMode` 支持 `proxy` / `redirect`。
- **`record.SnapshotStore`**：写盘改为先检查 `EmergencyGate`，编码完成后调用 `filestore.Put`（`local` 后端保持 `.lock` 旁车兼容，远端后端依赖 DB 保护）。
- **`person` & `spatial`**：`faces/` 与 `maps/` 读写删统一经 `filestore`（`person` 兼容历史纯文件名 Key 到 `faces/` 前缀的映射）；`spatial` SVG 保留 `Content-Security-Policy` 沙盒头与 `X-Content-Type-Options: nosniff`。
- **`alertpush`**：附件/Base64 改用 `io.ReadSeekCloser`；支持对允许的 Key 生成签名 URL（当启用 S3 预签名时可配置外发签名链接或继续走宿主 `/media/signed/*`）。
- **前端（`web/src/features/settings/storage`）**：表单增加后端类型切换（本地存储 / MinIO 与 S3 兼容云存储）、S3 参数与交付模式控件、`TestConnection` 按钮与错误反馈、三语 i18n，并使用真实无头浏览器单测验证请求/校验契约。

## 风险与审查重点（Review Focus）

1. **CGO 链条污染风险**：`internal/filestore` 与 `internal/mediafile` 绝对禁止直接或间接引入 `internal/engine` / `internal/camera`；务必验证 `go list -deps ./internal/filestore ./internal/mediafile` 不含 CGO 媒体引擎，且可裸跑 `go test -race`。
2. **S3 大视频流 Range Seek 内存泄漏风险**：`S3Driver.Open` 返回的 `ReadSeekCloser` 严禁全量 `io.ReadAll` 缓存视频，必须采用按需 Range 请求 + 及时关闭底层 HTTP `Body` 防止连接泄漏与边缘设备 OOM。
3. **凭据与签名 URL 脱敏风险**：`SecretAccessKey` 与 Presigned URL Query（含 `X-Amz-Signature`）严禁写入 Zap 日志、审计详情（`audit.Entry.Detail`）或 API 错误消息。
4. **断网与背压雪崩风险**：远端 MinIO/OSS 故障时，异步上传队列与 `SnapshotStore` 必须保持有界非阻塞，不得拖垮 `StreamHub` 推理消费与录像线程；Spool 满触发 `EmergencyGate` 熔断必须保留已录片段可读权限。
5. **浏览器直连（`redirect` 模式）兼容边界**：内网自签证书或无 CORS 配置的 MinIO 在 `redirect` 模式下可能被浏览器拦截，UI 需以 `proxy` 为安全默认值并明确提示前置条件。
6. **存量平滑升级**：已应用 SQLite 迁移不可修改，新迁移（`000039_*`）必须包含成对 `.up.sql` / `.down.sql` 且对历史空配置默认无缝兼容 `local` 后端。
