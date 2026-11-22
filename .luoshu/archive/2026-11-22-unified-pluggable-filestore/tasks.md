# Tasks: 2026-11-22-unified-pluggable-filestore

> 必须按自底向上依赖拓扑顺序排列，严禁前向依赖。每个任务需使用 `(deps: none)` 或 `(deps: <id>)` 标注前置依赖。

## 1. 存储核心抽象与配置底座

- [x] 1.1 (deps: none) 创建 `internal/filestore` 核心包：定义 `Store`、`ReadSeekCloser`、`Presigner`、`LocalMountInspector` 接口、统一逻辑 Key 校验（`ValidateKey`）、内存测试桩 `MemoryDriver` 及基于 `os.OpenRoot` / 原子写入 / `st_dev` 掉盘校验的 `LocalDriver`（验证：运行 `go test -race ./internal/filestore/...` 且确认 `go list -deps ./internal/filestore | grep -E "internal/(engine|camera)"` 无输出）。
- [x] 1.2 (deps: 1.1) 在 `internal/filestore` 实现 `S3Driver`（S3v4 协议，兼容 MinIO 及主流云对象存储）：实现 `Put`、`Stat`、`Delete`、`Exists`、`Health`、基于 HTTP Range 按需拉取的 `ReadSeekCloser`（禁止整文件缓存进内存）与 `PresignGet` 预签名能力，并用本地 mock S3 HTTP 服务覆盖 Range 边界与故障分支测试（验证：`go test -race ./internal/filestore/...` 全部通过）。
- [x] 1.3 (deps: 1.1, 1.2) 新增版本化 SQLite 迁移（`internal/database/migrations/000039_*.up.sql` 与 `.down.sql`）以支持 `event_clips` 上传/存储位置追踪；扩展 `internal/storage` 的 `StorageConfig`（支持 `local`/`s3`、S3 参数、`deliveryMode`、`spool` 策略）、`SecretCipher` 窄接口加密持久化、`TestConnection` 连通探测及 Spool/本地盘双模式 `EmergencyGate`（验证：`go test -race ./internal/database/... ./internal/storage/...` 通过）。
- [x] 1.4 (deps: 1.1, 1.3) 实现 `filestore.Manager`（支持运行时热切换与旧本地文件只读回落）及去扫盘化双模式清理逻辑（`local` 保持现有双水位回差；`s3` 通过 SQLite 索引 + `EvidenceGuard` 分批删除对象并触发 `pending_snapshot_deletes` 协调）；在 `internal/app` 完成 `EncryptedDataProbe` 与 `lifecycleRuntime.stopSequence()` 装配（验证：`go test -race ./internal/storage/... ./internal/app/... -run 'TestStorage|TestLifecycle|TestStartupRollback|TestAppDependencyGraph'` 通过）。

## 2. 业务域接入与异步 Spool 改造

- [x] 2.1 (deps: 1.1, 1.4) 改造 `internal/mediafile` 与 `internal/alertpush`：将 `mediafile` 迁至 `filestore.Store` 与 `Presigner`（移除 `*os.File` 暴露，保留四层防护、显式 `HEAD`、503 映射及只读零审计契约，支持 `proxy` 206 与可选 `redirect` 302）；`alertpush` 改用 `io.ReadSeekCloser` 读取媒体并适配统一预签名能力（验证：`go test -race ./internal/mediafile/... ./internal/alertpush/... ./internal/app/... -run 'TestMediaRoutes|TestSignedMedia'` 通过）。
- [x] 2.2 (deps: 1.1, 1.4) 统一业务资产读写删路径：将 `internal/record`（`SnapshotStore`）、`internal/person`（`faces/` 命名空间读写删及批量导入/重提，兼容历史纯文件名）与 `internal/spatial`（`maps/` 命名空间读写删及 SVG 沙盒响应头）全部迁至 `filestore.Store`（验证：`python3 native/scripts/build.py go test -race ./internal/record/... ./internal/person/... ./internal/spatial/...` 通过）。
- [x] 2.3 (deps: 1.3, 1.4, 2.1) 改造 `internal/recording` 对接 Local Spooling + 有界异步上传队列：保持 C++ `Fmp4Writer` 与 C ABI 不变，录制完成后当后端为远端时异步 `Put` 至对象存储并更新 `event_clips` 状态，上传前优先从 Spool 回落读取，并实现重启未上传孤儿修复与停机队列排空（验证：`python3 native/scripts/build.py go test -race ./internal/recording/... ./internal/app/...` 通过）。

## 3. API、异步迁移任务与前端设置界面

- [x] 3.1 (deps: 1.3, 1.4) 扩展 `internal/storage` HTTP Handler（配置读写掩码脱敏、`POST /system/storage/test` 兼容路径与 S3 连通性测试、可选基于 `asyncjob` 的本地历史资产向远端迁移任务触发端点），确认敏感字段零日志泄漏，并更新 `Makefile` 白名单与生成 Swagger 文档（验证：`make api-docs && go test -race ./internal/storage/... ./internal/asyncjob/... ./internal/apidocs/...` 通过）。
- [x] 3.2 (deps: 3.1) 更新前端存储设置切片（`web/src/features/settings/storage`）：新增存储后端选择、MinIO/S3 兼容云参数表单、Zod 校验、密钥掩码与留空保持语义、连通性测试交互、状态面板适配及三语 i18n（`en`/`zh-Hans`/`zh-Hant`），并补充真实浏览器测试（验证：`cd web && pnpm lint && pnpm build && pnpm test -- src/features/settings/storage` 通过）。

## 4. 全链路回归与规格核对

- [x] 4.1 (deps: 2.1, 2.2, 2.3, 3.1, 3.2) 运行全仓完整质量门禁与冒烟验证，核对 `.luoshu/specs/` 更新清单（含待人工批准的 `backend/filestore-guidelines.md`）与 `go.mod` 依赖合法性（验证：`make check && make smoke` 全部通过且 `git diff` 无脱敏/安全规范违例）。
