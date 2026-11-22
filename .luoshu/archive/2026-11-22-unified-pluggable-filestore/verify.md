# Verify Contract: 2026-11-22-unified-pluggable-filestore

> Propose 阶段确立的静态验收契约。Verify 阶段仅对照本契约与 `base_commit` diff 输出结论到 `verify-report.md`，不直接修改本契约或业务源码。

## 验收标准

- [ ] V1（统一抽象与零越界边界）：`internal/filestore` 定义统一对象原语与驱动实现（`local`、`s3`），无 CGO/`internal/engine`/`internal/camera` 依赖（`go list -deps ./internal/filestore ./internal/mediafile` 不可出现 `internal/(engine|camera)`）；`record`、`recording`、`person`、`spatial`、`mediafile`、`alertpush` 均通过统一契约访问业务资产，无新增裸路径拼接或绕过校验的文件读取。
- [ ] V2（LocalFS 防护与默认兼容性）：`local` 驱动保留 `os.OpenRoot` 四层路径校验、相对/绝对符号链接逃逸拒绝、建/换句柄期 `st_dev` 掉盘穿透检测、以及 `StorageConfig` 对已有配置与 `data/media` 的零破坏向后兼容。
- [ ] V3（S3 / MinIO / 云存储能力与内存安全）：`S3Driver`（用可重复本地 mock S3 服务验证）支持写入、删除、存在性检查、Presigned URL 及基于 HTTP Range 的按需 `ReadSeekCloser`（不将大文件整段读入内存），`mediafile.Handler` 在 `proxy` 模式下保持原有 206/416/HEAD/503 语义，`redirect` 模式正确签发 302/307，且 `media/content/*` 仍禁止越权访问 `faces/`、`maps/` 与无审计记录。
- [ ] V4（Spool、熔断与生命周期顺序）：`native/` C++ 与 C ABI 无网络耦合改动；`recording` 在远端后端下通过本地高速 Spool 落盘后异步上传，断网/远端故障不阻塞推理消费与原生录像，上传前可读回落；`EmergencyGate` 在 `local` 下守住 `statfs` 水位线并在远端模式下守住 Spool 目录容量红线；`lifecycleRuntime.stopSequence()` 在关停与启动回滚时按序排空上传队列后释放存储与数据库。
- [ ] V5（凭据加密、脱敏与 SQLite 迁移）：S3 `SecretAccessKey` 经宿主密钥加密持久化且在 `camera.LifecycleManager` 注册了 `EncryptedDataProbe`；所有 API 响应、审计详情与日志均不出现明文 Secret 或预签名凭据；新增迁移采用成对递增 `.up.sql` / `.down.sql` 且未修改已应用历史迁移。
- [ ] V6（前端契约与全仓门禁）：前端 `web/src/features/settings/storage` 使用 Zod 解析与三语 i18n，完成真实无头浏览器交互/校验断言；`Makefile` Swagger 白名单与 `internal/apidocs` 同步更新；全仓门禁 `make check`（含 `frontend-check`、`pkg-drift-check`、`native-test`、`pkg-test`、`api-docs`、`go-check`）与 `make smoke` 通过且无断言跳过或弱化。
- [ ] V7（Out of Scope 严守）：`diff` 中未引入 7×24 连续录像、未将 SQLite/密钥/日志/算法包迁出本地目录、未在 C++ 层引入网络/云 SDK、未擅自写入未经批准的新规格文件。
