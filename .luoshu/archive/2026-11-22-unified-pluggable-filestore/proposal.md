# Proposal: 2026-11-22-unified-pluggable-filestore

## 变更背景与目标

### 现状问题

1. **读写割裂**：`internal/mediafile` 仅负责本地媒体根目录（`recordings`/`snapshots`/`exports`）的只读字节传输，且 `OpenContent` 直接向调用方返回 `*os.File`；写入与删除则分散在 `record.SnapshotStore`、`recording.Service`、`person.Service`、`spatial.Service` 各自手写 `os.*` 调用。
2. **POSIX 强耦合**：`storage.StorageConfig`、`CleanerEngine`（全盘 `filepath.WalkDir` + `.lock` 旁车文件 + `statfs`）以及 `mediafile.resolver`（`os.OpenRoot` + `st_dev` 掉盘检测）均直接假设底层是本地文件系统，无法支持用户选择 MinIO 或云对象存储（阿里云 OSS / 腾讯云 COS / AWS S3）。
3. **跨根分裂与重复实现**：人脸底库（`<data-dir>/faces`）与空间底图（`<data-dir>/maps`）游离在 `mediafile` 与 `storage` 之外，各自重复实现文件打开与 HTTP 响应（如 `spatial` 直接使用 `http.ServeFile`），并导致前端 `web/src/lib/media-url.ts` 与 `alertpush` 签名外链存在路径前缀分裂。

### 变更目标

- 建立统一、可插拔的对象语义文件存储抽象层 `internal/filestore`，让用户可在 **LocalFS**、**MinIO** 及 **S3 兼容云对象存储** 之间自由选择并热切换。
- 将 `snapshots`、`recordings`、`exports`、`faces`、`maps` 五类持久化业务资产纳入统一存储抽象（保留各自的权限与访问路径隔离），同时严守 Zhulong **边缘本地优先（Local-First）** 约束：断网不丢录像、C++ 原生零网络耦合、不因远端抖动阻断 AI 推理消费与写保护熔断。

## In Scope（本次包含）

1. **统一存储抽象与双驱动实现（`internal/filestore`）**：
   - 定义核心接口 `Store`（`Put` / `Open` / `Stat` / `Delete` / `Exists` / `Health`）、逻辑 Key 校验与可选能力接口（`Presigner`、`LocalPathResolver`）。
   - 提供 `LocalDriver`（保留 `os.OpenRoot` 防逃逸、原子写入与 `st_dev` 掉盘检测）和 `S3Driver`（基于 S3v4 协议同时覆盖 MinIO 与兼容云对象存储，支持 HTTP Range 按需 Seek 与 Presigned URL）。
   - 提供支持配置热切换与旧本地资产只读回落的 `Manager`。
2. **存储配置扩展、密钥安全与生命周期重构（`internal/storage` + 数据库迁移）**：
   - 扩展 `StorageConfig` 支持后端类型选择（`local` / `s3`）、S3/MinIO 参数、交付模式（`proxy` / `redirect`）与本地 Spool 上限。
   - S3 `SecretAccessKey` 复用 `camera.LazyCipher`（`camera.key`）加密落库，注册 `EncryptedDataProbe` 且对外脱敏。
   - 新增版本化 SQLite 迁移支持记录片段上传状态及存量读回落。
   - 重构 `CleanerEngine` 与 `EmergencyGate`：本地模式维持双水位 + `statfs`；S3 模式按 SQLite 索引与 `EvidenceGuard` 执行删除，并对本地 Spool 目录执行磁盘红线熔断。
3. **业务域接入与异步 Spool 架构（Go 各域）**：
   - `mediafile` 与 `alertpush` 迁移至 `filestore.Store`（解耦 `*os.File`，支持 `proxy` 206 Range 与可选 `redirect` 302）。
   - `record.SnapshotStore`、`person`（`faces/`）、`spatial`（`maps/`）迁至 `filestore` 读写删，保留既有鉴权隔离与 SVG CSP 沙盒头。
   - `recording` 维持 C++ `Fmp4Writer` 写本地高速 Spool，完成后通过有界异步队列上传至对象存储并支持重启孤儿修复。
4. **前端存储设置页与 Swagger 文档同步**：
   - 扩展 `web/src/features/settings/storage` 支持后端切换、MinIO/云参数表单（含 Zod 校验与三语文案）、连通性探测（`/system/storage/test`）与脱敏展示。
   - 同步 `Makefile` `api-docs` 生成物。

## Out of Scope（不在本次范围内）

1. **连续（7×24）视频录像**：不在本次范围内，仅覆盖既有抓拍图与事件录像片段。
2. **C++ 原生层直连对象存储**：严禁在 `native/` C++ 代码中引入 S3 SDK 或修改 `native/include/Zhulong/engine.h` C ABI；原生片段录制仍只写本地文件。
3. **宿主核心运行时状态外置**：SQLite 数据库（`zhulong.db`）、加密密钥（`camera.key` / `media_url.key`）、TLS 证书、运行日志及原生算法包（`algorithms/`）仍强制保存在本地宿主目录，不接入 `filestore`。
4. **特定云厂商专有私有协议定制**：本次通过通用 S3v4 协议适配 MinIO 及云对象存储（OSS/COS/S3 兼容模式），不集成各云厂商专有 STS/CDN 刷新/私有 SDK。
5. **多边缘节点共享存储集群与分布式锁**：仍聚焦单台边缘宿主的存储后端可插拔能力。

## 影响范围（模块 / 契约 / 既有 Specs 或 Rules）

- **新增 Go 模块**：`internal/filestore`（无 CGO 依赖）。
- **改造 Go 模块**：`internal/storage`、`internal/mediafile`、`internal/record`、`internal/recording`、`internal/person`、`internal/spatial`、`internal/alertpush`、`internal/database/migrations`、`internal/app`、`internal/apidocs`。
- **改造前端切片**：`web/src/features/settings/storage/**`（按需更新相关国际化与校验测试）与 `web/src/features/audit/{data/audit-actions.ts,locales/*.json}`（补充 `storage.test_connection` 与 `storage.migrate_remote` 审计动作映射及三语文案）。
- **配置与构建**：`Makefile`（如涉及新增文档包需核对 `api-docs --dir`）、`go.mod`（引入最小 S3v4 客户端依赖，需审查许可与体积）、`web/package.json` 与 `web/run-browser-tests.mjs`（为解决全量 196 文件浏览器门禁在单次 Chromium 会话中的资源上限而提供按单 worker 分片的默认全量运行器，保留单文件与 `--shard` 直通）。
- **关联 Specs（待归档阶段审查更新）**：`architecture.md`、`backend/directory-structure.md`、`backend/storage-guidelines.md`、`backend/mediafile-guidelines.md`、`backend/record-guidelines.md`、`backend/spatial-guidelines.md`、`backend/alertpush-guidelines.md`、`backend/dependency-injection.md`，以及拟议新规格 `backend/filestore-guidelines.md`（需人工批准）。
