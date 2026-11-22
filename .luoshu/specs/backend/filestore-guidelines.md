# 对象文件存储与可插拔后端契约（FileStore Guidelines）

> 所属层级：`internal/filestore` 及其在 Go 宿主的生命周期、投递与维护边界。

## 1. 设计目标与边界隔离

`internal/filestore` 为 Zhulong 宿主提供面向统一对象语义的抽象存储层，使录像、抓拍、底库人脸、空间底图与临时导出得以在 **LocalFS**、**MinIO** 与 **S3 兼容云对象存储**之间热切换，同时维持离线单机零外部依赖的默认路径。

- **CGO 与领域解耦**：`internal/filestore` 不得导入 `internal/engine`、`internal/camera` 或任何业务领域包；C++17 原生引擎（`native/`）不直接引入任何 HTTP/S3 客户端 SDK。
- **元数据与本机关键状态不进对象存储**：SQLite (`zhulong.db`)、凭据密钥 (`camera.key` / `media_url.key`)、TLS 证书、本地日志、`algorithms/` 运行包与正在录制中的本地暂存分片（`.part` / Spool）始终留存本地磁盘。
- **单二进制并发与并发热切换安全**：`filestore.Manager` 使用读写锁保护的活动槽位（`managerSlot`）与引用计数实现高并发读写及运行时在线切后端/热切换；读操作仅在获取槽位副本时持有轻量读锁并增加引用计数，已淘汰槽位在最后一个在途操作释放引用后才关闭清理，不在请求热路径持有粗粒度写锁。

## 2. 统一对象键命名空间（Unified Object Keys）

所有持久化媒体实体与跨层接口统一采用 `/` 分隔的相对对象键，禁止写入绝对磁盘路径：

| 命名空间前缀 | 典型用途 | 数据库记录约定 |
| --- | --- | --- |
| `snapshots/` | 识别、抓拍、告警截图（`recognitions` / `captures` / `alarms`） | `image_path` / `face_image_path` / `crop_path` 保存完整键 `snapshots/{cam}/{date}/...` |
| `recordings/` | 事件短视频录像片段（`event_clips`） | `path` 保存 `recordings/{cam}/{date}/evt-{id}.mp4` |
| `faces/` | 人员底库人脸模板（`person_faces`） | 数据库兼容既有裸文件名，接入层归一化映射为 `faces/{filename}` |
| `maps/` | 空间画布平面图（`canvas_maps`） | 数据库兼容既有文件名，接入层归一化映射为 `maps/{filename}` |
| `exports/` | 临时导出产物（若需归档清理） | 由导出域维护相对键 |

键校验由 `filestore.ValidateKey` 与 `filestore.ValidateObjectKeyForWrite` 统一负责：拒绝空键、前导 `/`、`.` / `..` 路径穿越段、反斜杠及空段，保证跨驱动一致性。

## 3. 核心接口与驱动实现

- **`filestore.Store`（最小公共合约）**：
  - `Backend() string`（`local` / `s3` / `memory`）
  - `Put(ctx, key, r, PutOptions) error`
  - `Open(ctx, key) (ReadSeekCloser, ObjectInfo, error)`
  - `Stat(ctx, key) (ObjectInfo, error)`
  - `Delete(ctx, key) error`
  - `Exists(ctx, key) (bool, error)`
  - `Health(ctx) (HealthStatus, error)`
- **可选能力扩展接口**：
  - `filestore.Presigner` & `filestore.PresignCapable`：`PresignGet(ctx, key, expiry)` 由 `S3Driver`、配置 `presignBase` 的 `MemoryDriver` 及 `Manager` 实现；`Manager` 与 `MemoryDriver` 通过 `SupportsPresign() bool` 反映当前生效主后端是否支持预签名（不支持时 `PresignGet` 返回包裹 `ErrPresignUnsupported` 的错误，供 `mediafile` 在 `local` 或 S3 未就绪时自动回落为 `proxy` 流传输）。
  - `filestore.ProtectionMarkerStore`：面向 LocalFS 驱动 `.lock` 文件或远端受保护状态契约，仅作用于本地存储清理保护与迁移适配。
- **内建驱动**：
  1. `LocalDriver`（`internal/filestore/local.go`）：基于 `os.OpenRoot` 作用域根（`RootDir` + `NamespaceRoots`）执行同根目录随机临时文件写入与 `Rename` 原子落盘，并结合 `st_dev` 检测外部磁盘掉盘穿透与系统盘兜底隔离。
  2. `S3Driver`（`internal/filestore/s3.go`）：基于 `github.com/minio/minio-go/v7` 实现 S3v4 兼容驱动，支持 Path-Style / Virtual-Hosted-Style、TLS 与 `InsecureSkipVerify`、桶前缀（`prefix`）自动拼接，以及基于 HTTP `Range` 的按需 `ReadSeekCloser`（支持 MP4 进度条拖放且避免整文件读入内存）。
  3. `MemoryDriver`（`internal/filestore/memory.go`）：仅供单元测试/契约测试使用的全内存 `Store`。

## 4. `filestore.Manager` 读写优先级与 Spool 模型

当后端切换为 `s3` 时，宿主同时协调 **Spool 本地缓冲** 与可选 **LocalFallback 历史回退**：

1. **读取优先级链（`Manager.Open` / `Manager.Stat`）**：
   - `spool`（处于异步上传排队中的新录制事件片段）
   - `primary`（当前启用的主驱动：S3 或 LocalFS）
   - `fallback`（当 `LocalFallback = true` 且 `primary` 返回 `ErrNotFound` 时回退读取原 `mediaDirectory` 与 `faces`/`maps` 命名空间）
2. **Spool + 异步上传（`internal/recording`）**：
   - 事件录像关闭前始终由引擎或本地暂存封装至 `SpoolDir`；收尾成为 `completed`/`truncated` 后标记 `upload_status = 'pending'`，异步工作池调用 `PutLocalFile` 流式上传至 S3。
   - 仅当远端写入校验成功后，方更新 SQLite `storage_backend = 's3'`、`upload_status = 'uploaded'` 并删除本地 Spool 源文件。宿主优雅停机前先完成尾部轮询并排空在途上传。
3. **媒体 HTTP 投递（`internal/mediafile` 与 `internal/alertpush`）**：
   - **默认 `proxy` 模式**：宿主继续通过 `OpenContent` 流式代理转发并保留 `http.ServeContent` `Range`、Cookie/签名URL 鉴权；对外不暴露 S3 内网地址。
   - **可选 `redirect` 模式**：仅当对象位于主远端存储（`!ExistsInSpool && ExistsInPrimary`）时签发 `302 Found` 预签名直链，并附加 `Cache-Control: no-store`；仍在 Spool 或仅存在于本地兜底目录的对象自动回落为宿主代理流式输出。

## 5. 远端生命周期清理与历史迁移

- **SQLite 索引驱动淘汰（严禁 `ListObjectsV2` 全桶扫）**：启用 S3 后，Cleaner 不扫描 S3 桶或 `.lock` 标记，而是通过 `storage_backend = 's3'` 索引列批量查出超期或超配额候选记录，执行 fail-closed `EvidenceGuard` 检查（排除 `alarms` 关联并校验 `ExistingAlarmIDs` 保护高价值证据），随后批量删对象并回写 SQLite（含 `pending_snapshot_deletes` 与 `RemoteClipReconciler`）。
- **异步历史迁移（`storage.migrate_remote`）**：基于 `asyncjob` 提供可选手动迁移任务，增量幂等复制本地 `recognitions`、`captures`、`alarms`、`event_clips`、`person_faces`、`canvas_maps` 到远端 S3 后再切换 `storage_backend = 's3'`，默认保留本地源文件以免干扰在线读。

## 6. 安全与配置脱敏红线

- **Secret 加密与只写不回显**：`S3Config.SecretAccessKey` 经宿主 AES-GCM `SecretCipher` 加密持久化至 SQLite；`GET /system/storage/config` 与 `PUT` 响应永远清空 `secretAccessKey`，仅返回布尔值 `hasSecretAccessKey`。更新配置时若 `secretAccessKey` 留空，保留已保存密文不变。
- **零日志与零错误回显泄漏**：Zap 日志、审计记录 `Detail`、与 `ConnectionTestResponse.ErrorReason` 及 `4xx/5xx` 错误包装均须剥离任何明文 Secret 及预签名查询参数（如 `X-Amz-Signature`、`X-Amz-Credential`、`X-Amz-Security-Token`）。
