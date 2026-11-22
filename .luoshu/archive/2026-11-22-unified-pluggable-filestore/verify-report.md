# Verify Report: 2026-11-22-unified-pluggable-filestore

- Verdict: PASS
- base_commit: 70fbc793af27f8dbfbe83d88a6d3de584c74df99
- Worktree: /home/niko/dev/Go/Zhulong

## Gate Results

`operations.verify.gates` in `.luoshu/config.yaml` is `[]` (exit `0` via `luoshu verify run`, `ready: true`, `gatesPassed: true`, `incompleteTaskIds: []`). All product verification suites and quality gates were executed directly in this verification session:

| Gate | Command | Status | Duration |
| --- | --- | --- | --- |
| Luoshu verify packet | `luoshu verify run 2026-11-22-unified-pluggable-filestore --include-diff --json` | PASS (exit 0; `gatesPassed: true`, `10/10` tasks complete) | < 1s |
| Luoshu change validate | `luoshu changes validate 2026-11-22-unified-pluggable-filestore` | PASS (exit 0; `valid (10/10 tasks complete)`) | < 1s |
| Luoshu specs validate | `luoshu specs validate` | PASS (exit 0; `40 files, 0 errors, 2 warnings`) | < 1s |
| Luoshu rules validate | `luoshu rules validate` | PASS (exit 0; `17 rules, 0 errors, 0 warnings`) | < 1s |
| Dependency boundary check | `go list -deps ./internal/filestore ./internal/mediafile \| grep -E 'internal/(engine\|camera)'` | PASS (exit 0; empty output, zero CGO/engine/camera coupling) | < 1s |
| Go unit tests (non-engine, `-race -count=1`) | `go test -race -count=1 ./internal/filestore/... ./internal/mediafile/... ./internal/database/... ./internal/storage/... ./internal/spatial/...` | PASS (`filestore` 1.700s, `mediafile` 1.198s, `database` 1.990s, `storage` 3.323s, `spatial` 5.186s) | ~14s |
| Go unit tests (engine-linked via wrapper, `-race -count=1`) | `python3 native/scripts/build.py go test -race -count=1 ./internal/alertpush/... ./internal/person/... ./internal/record/... ./internal/recording/... ./internal/app/...` | PASS (`alertpush` 6.647s, `person` 7.040s, `record` 13.414s, `recording` 2.640s, `app` 7.636s) | ~38s |
| Targeted frontend Chromium tests | `cd web && pnpm test -- src/features/settings/storage` | PASS (exit 0; `Test Files 3 passed (3)`, `Tests 21 passed (21)`) | 4.65s |
| Full repo check | `make check` (`frontend-check`, `pkg-drift-check`, `native-test`, `pkg-test`, `api-docs`, `go-check`) | PASS (exit 0; CTest `35/35` passed, Go bridge `8/8` passed in 2.248s, Prettier/ESLint/tsc passed, Chromium tests passed) | ~4m |
| Smoke test | `make smoke` | PASS (exit 0; `Smoke test passed: health, embedded SPA, API 404 isolation, disabled Swagger, graceful stop.`) | ~35s |

## Contract & Checklist Evaluation

### Static Verification Contract (`verify.md`)
- [PASS] V1（统一抽象与零越界边界）：`internal/filestore` 定义了统一存储原语与驱动实现（`types.go`、`local.go`、`s3.go`、`manager.go`、`memory.go`、`key.go`），实现 `Store`、`ReadSeekCloser`、`Presigner`、`LocalRootInspector` 等接口。`go list -deps ./internal/filestore ./internal/mediafile | grep -E 'internal/(engine|camera)'` 输出为空，完全无 CGO/`internal/engine`/`internal/camera` 依赖。`record`（`snapshot.go`）、`recording`（`upload.go`）、`person`（`service.go`）、`spatial`（`service.go`）、`mediafile`（`service.go`）、`alertpush`（`sender.go`）均统一通过 `filestore.Store` 访问业务资产，无裸路径拼接或越权读取。Go AST 静态检查确认新增与修改代码中函数参数数量均 ≤ 4，4+ 参数调用均采用每行一个参数且右括号独占一行格式。
- [PASS] V2（LocalFS 防护与默认兼容性）：`internal/filestore/local.go:615-755` 完整保留 `os.OpenRoot` 四层路径校验、符号链接逃逸拒绝及建/换句柄期 `st_dev` 掉盘穿透检测（`telemetry_linux.go` / `telemetry_nonlinux.go`）；写入操作严格使用同根临时文件加原子 `os.Rename`（`local.go:134-226,654-676`）。`DefaultStorageConfig` 保持 `backend="local"`、`deliveryMode="proxy"`、`mediaDirectory="data/media"`，旧配置与已有目录 100% 向后兼容，经 `local_test.go` 与 `remote_storage_test.go` 验证。
- [PASS] V3（S3 / MinIO / 云存储能力与内存安全）：`internal/filestore/s3.go` 基于 `minio-go/v7` 实现 S3v4 驱动，支持 Put、Stat、Delete、Exists、Health、Presigned GET 与按需 `ReadSeekCloser`（`*minio.Object` 按 HTTP Range 懒拉取，失败时及时关闭连接，禁止整文件加载进内存），通过本地 mock S3 测试（`s3_test.go`）。`mediafile.Handler` 在 `proxy` 模式下保留 200/206/416/503 语义且无审计日志；在 `redirect` 模式下对已在主 S3 上的对象签发带 `Cache-Control: no-store` 的 302 重定向；对 Spool、本地回退或本地后端自动回落为宿主代理流；且严格拦截 `/media/content/*` 对 `faces/`、`maps/` 的越权访问。
- [PASS] V4（Spool、熔断与生命周期顺序）：`git diff native/` 完全为空（原生 C++ 与 C ABI 零修改）。远端模式下 `recording.Service` 先将事件短视频封装落盘至本地高速 `SpoolDir`，再由有界工作池（`uploadWorkers=2`, `uploadQueueSize=64`）异步流式上传至 S3；远端故障或队列满时降级标记 `UploadStatusFailed` 留待恢复，不阻塞推理与实时录像，上传前 `Manager.Open` 优先从 Spool 本地回落读取。`EmergencyGate` 支持 `EvaluateSpool` 守住 Spool 容量阈值与系统盘水位线。`lifecycleRuntime.stopSequence()`（`internal/app/runtime.go:569-594`）在关停与回滚时按序先关停录像消费并排空上传队列，再安全关闭存储与数据库，经 `filestore_wiring_test.go` 验证。
- [PASS] V5（凭据加密、脱敏与 SQLite 迁移）：`internal/storage/store.go` 通过 `SecretCipher`（AES-GCM）对 S3 `SecretAccessKey` 加密持久化，在 `internal/app/app.go` 注册 `EncryptedDataProbe`；API 响应一律清空 Secret 并仅返回布尔值 `hasSecretAccessKey`，留空提交保持已有密文；日志与错误输出均通过 `sanitizeErrorText`、`redactSecretErr` 与 `sanitizeS3ErrorMessage` 脱敏；成对新增 `000039_event_clips_storage_state.up.sql` / `.down.sql`，未修改任何历史迁移。
- [PASS] V6（前端契约与全仓门禁）：前端 `web/src/features/settings/storage` 使用 Zod schema 强校验表单与响应，支持 `en` / `zh-Hans` / `zh-Hant` 三语国际化；真实 Chromium 浏览器测试（`storage-api.test.ts`、`storage-capacity-bar.test.tsx`、`storage-config-form.test.tsx`，共 21 项测试）全部通过。`Makefile` Swagger 白名单与 `internal/apidocs` 同步更新。全仓门禁 `make check`（前端检查、包漂移、CTest `35/35`、Go bridge `8/8`、包测试、API 文档、Go 检查）与 `make smoke` 全部退出 0，无任何跳过或弱化断言。
- [PASS] V7（Out of Scope 严守）：审查基于 `base_commit` 的所有 110 个变动文件与 git diff，未引入 7×24 连续录像；SQLite、凭据密钥、本地日志、算法包始终留存本地磁盘；原生 C++ 层未引入任何网络或云 SDK；未越界添加未经批准的规格文件（唯一新增规格 `backend/filestore-guidelines.md` 已在 `context.yaml` 中声明并批准）。

### `design.md` Review Focus 风险控制核对
- Risk 1 (CGO 链污染与驱动隔离)：`internal/filestore` 与 `internal/mediafile` 经 `go list -deps` 验证不含 `internal/(engine|camera)`，原生依赖完全隔离。
- Risk 2 (S3 大文件流式内存与连接泄漏)：`internal/filestore/s3.go` 采用 `*minio.Object` 按需 Range 读取，`Stat` 错误时通过 `closeReaderBestEffort(obj)` 显式关闭连接，防止 HTTP 连接泄漏与 OOM。
- Risk 3 (凭据与预签名 URL 脱敏)：`storage/errors.go`、`storage/service_admin.go`、`filestore/s3.go` 在所有日志记录、审计详情与错误返回前剥离密钥与 `X-Amz-*` 签名参数。
- Risk 4 (离线背压与 Spool 熔断)：异步队列满或远端离线时，本地 Spool 写入不阻塞；`EmergencyGate.EvaluateSpool` 守住磁盘配额红线；未上传状态在恢复时由 `RecoverPendingUploads` 协调重试。
- Risk 5 (直链重定向安全性)：`proxy` 模式保持默认；`redirect` 模式仅针对主 S3 上已存在的对象签发带 `Cache-Control: no-store` 的预签名直链，未就绪或 Spool 中文件自动回退代理。
- Risk 6 (空配置与迁移兼容性)：默认配置零破坏兼容已有本地 `data/media` 布局；版本化迁移支持空表及历史数据平滑升级。

### `operations.verify.checklist`
- [PASS] 逐项核对 `verify.md` 验收标准，确认无遗漏、跳过或弱化断言：V1 至 V7 逐条核验，无新增跳过用例，无断言弱化。
- [PASS] 检查基于 `base_commit` 的完整变更 `diff` 是否超出声明范围：所有 110 个变更与新增文件严格对应 `proposal.md` 与 `context.yaml` 声明范围。
- [PASS] 核对是否符合所有命中的 `rules/` 工程规范与挂载 `specs/`：命中规则（`go-host/code-style`、`go-host/http-contract`、`go-host/concurrency-and-errors`、`go-host/sensitive-logging`、`go-host/host-lifecycle`、`native/native-link-and-abi`、`data/media-storage-boundary`、`data/sqlite-persistence`、`web/frontend-api-boundary`、`web/frontend-browser-tests`）与挂载规格均已全量审计并符合。

## Rules & Specs Compliance

- **Matched Rules (`14` matched, all `trigger: glob`; `modelDecisionCandidates: []`)**:
  - `[PASS]` `go-host/code-style` (`priority: 100`): Go AST 校验确认新增与改动函数签名参数均 ≤ 4；所有 4+ 参数调用均符合每行一个参数且右括号独立成行的规范。
  - `[PASS]` `go-host/http-contract` (`priority: 100`): HTTP 响应统一封装于 `httputil` 信封；Swagger 注释、API 路由组与错误机器码映射完整对齐。
  - `[PASS]` `go-host/concurrency-and-errors` (`priority: 100`): 所有 Go 测试均开启 `-race` 并全部通过；长时协程与上传工作池支持上下文退出与生命周期安全排空。
  - `[PASS]` `go-host/sensitive-logging` (`priority: 100`): S3 敏感配置在日志、审计与对外接口中均完成脱敏和掩码。
  - `[PASS]` `go-host/host-lifecycle` (`priority: 100`): 统一在 `lifecycleRuntime.stopSequence()` 中编排录像、上传队列、存储与数据库的关停与回滚。
  - `[PASS]` `native/native-link-and-abi` (`priority: 20`): 原生引擎与 C ABI 零修改；所有跨层测试通过 native wrapper 编译运行。
  - `[PASS]` `data/media-storage-boundary` (`priority: 65`): 媒体与业务资产严格通过 `Store` 抽象与四层路径校验访问。
  - `[PASS]` `data/sqlite-persistence` (`priority: 100`): 严格使用成对 `.up.sql` / `.down.sql` 迁移，无 `AutoMigrate`。
  - `[PASS]` `web/frontend-api-boundary` & `web/frontend-browser-tests` (`priority: 100`): 前端切片遵循特性目录结构，通过 Zod 解析 API 数据，并在真实 Chromium 浏览器中完成测试验证。
- **Mounted Specs (`12` mounted + `1` approved new spec)**:
  - `[PASS]` `.luoshu/specs/backend/filestore-guidelines.md`: 规格准确描述了活动槽位读写锁机制与 S3 索引驱动清理逻辑，与实现完全对齐。
  - `[PASS]` 所有挂载规格（`architecture.md`、`backend/mediafile-guidelines.md`、`backend/storage-guidelines.md`、`backend/record-guidelines.md`、`backend/spatial-guidelines.md`、`backend/alertpush-guidelines.md`、`backend/dependency-injection.md`、`backend/http-api-guidelines.md`、`backend/database-guidelines.md`、`backend/openapi-and-asyncjob-guidelines.md`、`frontend/development-guidelines.md`、`guides/cross-layer-thinking-guide.md`）均符合。

## Findings

- [Note] `internal/person/service.go:170-177`, `internal/spatial/service.go:82-89`, `internal/mediafile/service.go:282-310` — 独立退化构造函数（`opts.FileStore == nil` 或历史兼容构造函数）中兜底创建的 `LocalDriver` / `Manager` 实例未对外暴露显式生命周期管理；生产运行中由 `internal/app/app.go` 依赖注入的全局共享 `*filestore.Manager` 负责实际管理，并在 `lifecycleRuntime.stopSequence()` 中由 `closeFileStore` 统一按序释放。
