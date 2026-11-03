# 人员/人脸 OpenAPI 下发实施计划 (Implementation Plan)

> 前置阅读：`prd.md`、`design.md`、`.trellis/spec/backend/{http-api-guidelines,database-guidelines,authentication-guidelines,middleware-guidelines,error-handling,dependency-injection,logging-guidelines}.md`、
> `.trellis/spec/frontend/{development-guidelines,theme-and-i18n,quality-guidelines}.md`、`.trellis/spec/architecture.md`。

## 0. 开工前已确认的决策（已写入 prd/design）

- [x] `personId` 是外部业务键 `persons.person_id`；内部 ULID 不出现在 OpenAPI 响应。
- [x] 图片/人脸校验统一 422（`INVALID_FILE_TYPE` / `UNSUPPORTED_IMAGE_FORMAT` / `NO_FACE_DETECTED`）；超 10 MB 413；超 10 张 409。
- [x] `source` / `source_name` 记录首次录入来源，创建后不可变；最后修改方看审计 Actor。
- [x] `?replace=true` 先提取/落新文件，再单事务替换；失败保留旧脸。
- [x] Token 最多 10 个生效；名称 1–48 字符、**全表唯一（含已吊销）**；软吊销、立即生效。
- [x] OpenAPI 使用独立 `/api/v1/openapi` 路由组；JSON 端点在其下叠 1 MiB 子组，面档上传才用 11 MiB。
- [x] **迁移号已定死**：`000027_add_person_source`、`000028_create_api_tokens`。`000026` 由 `11-03-alert-push-multi-channel`（`000026_create_alert_destinations`）先行占用，本任务不抢。
- [x] OpenAPI 面档**只按内容魔数**判定格式，不要求文件后缀（`curl -F image=@photo` 必须可用）；Web UI 上传保持双检。
- [x] 不做姓名合并；同名不同源档案共存，由管理员手工清理。

## 1. 实施清单 (Ordered Checklist)

### 阶段一：数据库与模型

- [x] `internal/person/model.go`：新增 `PersonSourceLocal` / `PersonSourceAPI` 常量；`Person` 增加 `Source` / `SourceName` 字段与 GORM tag；`PersonResponse` 增加 `source` / `sourceName`；`newPersonResponse` 复制。
- [x] `person.Service.CreatePerson`：创建时显式写 `SourceLocal` / `SourceName=""`；不要依赖 DB 默认值。
- [x] 其余 `Person` 构造点同步：`Store.CreatePersonImport`（CSV 导入）与 `face_import.go` 的 `newPerson` 也显式写 `SourceLocal`，避免 GORM 写入 `''`。
- [x] 迁移 `000027_add_person_source.up.sql` / `.down.sql`（`TEXT NOT NULL DEFAULT`，无索引、无 CHECK）。
- [x] `internal/apitoken/model.go`：`APIToken` GORM 模型、`TableName()`、审计 action 常量（`api_token.create` / `api_token.revoke` / `api_token.auth_rejected`）、错误哨兵（`ErrTokenNotFound`、`ErrTokenLimitReached`、`ErrTokenNameConflict`、`ErrUnauthorized`）。
- [x] 迁移 `000028_create_api_tokens.up.sql` / `.down.sql`：表、`token_hash` 唯一索引、**`name` 全表唯一索引**（不是部分唯一索引）。
- [x] `internal/database/migrations_test.go`（或现有 migration 测试文件）：两个迁移 up/down 对称；旧 `persons` 行默认 `LOCAL`；`api_tokens` 索引存在；回滚后表/列消失。

### 阶段二：`internal/apitoken`

- [x] `token.go`：`GenerateToken`（32 字节 `crypto/rand`、`sk-` + hex、SHA-256、masked）；`normalizeTokenName`（trim、1–48、拒绝控制字符）。
- [x] `store.go`：`CreateToken`（事务内 count+insert）、`ListActive`、`FindActiveByHash`、`Revoke`（幂等软删）、`TouchLastUsed`；唯一冲突分别映射名称/哈希。
  - ⚠️ 事务内只能使用 `tx`；任何自己取 `s.db(ctx)` 的 store 调用在 `MaxOpenConns=1` 下会永久挂死。
- [x] `service.go`：`CreateToken`、`ListTokens`、`RevokeToken`、`Authenticate`、`touchLastUsed`（60 秒合并 + 2s 后台 timeout）、**通过 `sync.WaitGroup` 追踪在途落库并在 `Close()` 中优雅等待排空（500ms 超时）**。
- [x] `middleware.go`：`RequireBearer`、`parseBearer`（大小写不敏感、拒绝空/多值）、401 + `WWW-Authenticate`、DB 故障 500、审计 Actor 升级、`SetIdentity` / `GetIdentity` 类型化 accessor、**鉴权失败写 `api_token.auth_rejected`（Detail 只含枚举原因）**。
- [x] `handler.go` / `requests.go` / `responses.go`：`GET/POST/DELETE /api-tokens`；创建响应 `Cache-Control: no-store`；顶层响应类型 `APITokenListResponse` / `APITokenCreateResponse` / `APITokenRevokeResponse`，嵌套 `APITokenInfo`；Swagger 注释。
- [x] `locale.go`：`UNAUTHORIZED`（同文案重复注册，保证包独立测试）、`API_TOKEN_NOT_FOUND`、`API_TOKEN_LIMIT_REACHED`、`API_TOKEN_NAME_ALREADY_EXISTS`、`API_TOKEN_NAME_REQUIRED`、`API_TOKEN_NAME_TOO_LONG` 三语。
- [x] 测试：生成格式、哈希/masked、并发 10 上限、**同名（含已吊销）冲突**、吊销幂等、有效/无效/吊销鉴权、DB 故障 500、`last_used_at` 合并、`-race`、`Close()` 优雅等待排空。
- [x] 审计 action 跨层：`api_token.create` / `api_token.revoke` / `api_token.auth_rejected` 同步 `web/src/features/audit/data/audit-actions.ts` 与三份 `actionNames`。

### 阶段三：`internal/person` 外部键能力

- [x] `external.go`（或 `service.go`）：`normalizeExternalPersonID`（1–64、拒绝 `/ \ ? # %` 与控制字符）。
- [x] `store.go`：`GetPersonByExternalIDWithFaces`（或复用 `GetPersonByExternalID` + `GetPerson`）。
- [x] `service.go`：`UpsertPersonByExternalID(ctx, externalID, name, tokenName) (*Person, bool, error)`；命中更新姓名、未命中创建 API 来源、唯一冲突重读后重试。
- [x] `service.go`：`AddFaceByExternalID(ctx, externalID, filename string, r io.Reader, replace bool) (*Face, bool, error)`：
  - 解析人员 → 校验/嗅探 → 提取特征 → 写新文件 → 事务替换/追加 → 删旧文件 → 广播。
  - 失败路径删除新文件，保留旧数据。
- [x] **扩展名策略落地**：把「嗅探 → 提取 → 落盘 → 入库」从 `AddFaceWithReader` 抽成内部函数，加 `requireExt bool`。`AddFaceByExternalID` 传 `false`（落盘扩展名取嗅探结果），Web UI 的 `AddFaceWithReader` 保持 `true`。**不要**直接改 `AddFaceWithReader` 语义：现有 `TestAddFace_RejectsInvalidFileType` 断言 `script.sh` 被拒。
- [x] `store.go`：`ReplaceFaces`（事务内严格外键顺序：select 旧脸 → delete 旧脸 → insert 新脸 → 更新 primary）、`ClearFaces`（select → delete，FK 清空 primary）、`AddFaceWithLimit`（事务内 count+insert）。**事务内只用 `tx`**（`MaxOpenConns=1`，嵌套取连接会永久挂死）。
- [x] `service.go`：原子换脸广播优化：提交后由 service 执行单次 `BroadcastDelete("", externalID)` 清空底库 + 单次 `BroadcastUpsert(new)` 插入新脸，避免按旧脸循环调用 IPC。
- [x] `person.MaxFaceImageBytes` 导出；`person.MaxFacesPerPerson = 10` 统一常量；`face_import.go` 的 `maxFacesPerPerson` 改为引用。
- [x] 现有 `Service.AddFaceWithReader` 改走 `Store.AddFaceWithLimit`；`face_import.go` 的 `enrollFaceRecord` 同样改走该方法，避免「先查数量再插入」的跨事务窗口；**`enrollFaceRecord` 需把 `ErrFaceLimitExceeded` 翻回 `faceProcessError{code: "FACE_LIMIT_EXCEEDED"}`**，否则导入报告会误报成 `FACE_ADD_FAILED`。
- [x] **拆改既有测试 `internal/person/service_test.go:391` 的 `TestAddFace_IDsAreUniqueUnderConcurrency`**：它现在起 20 个 goroutine 并断言全部成功；上限统一后必有 10 个 409。拆为①ID 唯一性（用 `store.AddFace` 绕过上限）；②上限并发（断言 10 成功 + 10 个 `FACE_LIMIT_EXCEEDED`）。
- [x] `service.go`：`ClearFacesByExternalID`、`DeletePersonByExternalID`、`DeleteFaceByExternalID`、`SetPrimaryFaceByExternalID`；跨人员脸统一 404（**写在 `internal/openapi` 包装层，不改 `person.Service.SetPrimaryFace` 的 422 契约**，否则 `service_test.go:521` 与 Web UI 一起红）。
- [x] 错误哨兵：`ErrFaceLimitExceeded`、`ErrInvalidFileType`（如果希望与 `UNSUPPORTED_IMAGE_FORMAT` 区分）；映射到 `apperr`。
- [x] 测试：upsert 创建/更新/并发、来源不变、10 张上限（含导入失败行的 code）、替换成功/失败回滚、清空主照、主照提升、**无后缀合法图被接受**、`-race`。

### 阶段四：`internal/openapi`

- [x] `handler.go`：挂载 `/persons/:personId/faces` 面档端点（`POST`、`DELETE`、`DELETE /:faceId`），其余 JSON 端点挂在叠了 `httpmiddleware.MaxBodyLimit()` 的 `json` 子组；组前缀 `/api/v1/openapi`。所有路由参数名严格统称为 `:personId`。
- [x] `requests.go` / `responses.go`：`UpsertPersonRequest`、`UpdatePersonRequest`（**带 `PersonID *string`**）、`SetPrimaryFaceRequest`、`PersonInfo`、`UpsertPersonResponse`、`PersonDetailResponse`、`FaceResponse`、`ClearFacesResponse`、`DeleteFaceResponse`、`DeletePersonResponse`；不暴露内部 id / ownerRowId / imagePath。
- [x] `errors.go`：**显式 `statusFor(code)` 表**（不依赖 `apperr.Kind`）；`FILE_TOO_LARGE` → 413；`INVALID_FACE_OWNER` 重写成 `FACE_NOT_FOUND` + 404；`PERSON_ID_IMMUTABLE` → 409；不列 `PERSON_ID_REQUIRED_FOR_ENROLLMENT`（永不可达）。
- [x] `limiter.go`：**全局 3 / 单 Token 1**（不是 8/4，见 design §5.3 的内存与排队论证）；`Acquire`/`Release`；超限**先** `c.Header("Retry-After", "1")` 再 `WriteError`；map 归零即删；**精准作为中间件修饰 `POST .../faces`**，强制早于 `c.ShouldBind`，**严禁挂在 `faces` 子组连带误杀 DELETE**。
- [x] 提取调用包 `context.WithTimeout(30s)`，超时映射 429（不是 500）。
- [x] `handler.go`：审计 `audithttp.Tag`；面档录入 detail 必须含 `replace=true|false`（及替换时被删旧脸数），不写 Token/图片内容。
- [x] `locale.go`：新错误码三语（`FACE_LIMIT_EXCEEDED` 已存在则复用；`TOO_MANY_REQUESTS` 必须在本包重复注册，因为 `clientlog` 不保证被当前单测的包图初始化）。
- [x] `handler_test.go`：状态码矩阵（**含 `FILE_TOO_LARGE`→413**）、跨人员 404、替换失败保留旧脸、审计 Actor、GalleryNotifier 序列、**无后缀合法图 201**、`Retry-After` 存在、**面档并发打满时 DELETE 依然可用（不被 429 误杀）**、`-race`。
- [x] Swagger：`@Tags openapi`、`@Security BearerAuth`、`@Router /openapi/...`；**`securityDefinitions.apikey BearerAuth` 写在 `cmd/Zhulong/main.go` 的通用注释块**（不是包注释，否则静默丢失）；`make api-docs` 后人工确认 `swagger.json` 里有 `securityDefinitions.BearerAuth`。

### 阶段五：路由与装配

- [x] `internal/app/router.go`：新增 `/api/v1/openapi` 平行组；中间件顺序 `AccessLog → MaxBodyLimitWithLimitAndCode(openapi.MaxRequestBodyBytes) → Audit → RequireBearer`。若 `APITokenService == nil` 挂 500 阻断中间件，杜绝免密裸奔。所有 OpenAPI 路由参数名严格统称为 `:personId`。
- [x] `internal/app/router.go` / `app.go`：`RouterParams` 增加 `APITokenService`、`OpenAPIRoutes`；新增 `openapi_routes` value group；`apitoken.Handler` 贡献 `protected_routes`。
- [x] `internal/app/app.go`：`provideAPIToken`、`provideOpenAPI`；依赖 `person.Service`、`apitoken.Service`、`Config`、`Logger`。
- [x] `internal/app/router.go` 的 `RouterParams` 新增字段必须写成 `fx.In` 可选或带默认值，以不破坏现有 `router_test.go` 里的 `newRouter(RouterParams{Logger: ..., Assets: ...})` 字面量构造（空值组会得到 nil 切片，遍历时天然空转）。
- [x] `internal/app/config.go` / `cmd/Zhulong/config.go`：若开放 `openapi.max_concurrent` 覆盖，**两个文件都要改**（`fileConfig` 结构体 + `SetDefault`），否则 viper `UnmarshalExact` 会把新 key 当成未知键中止启动。不开放则用常量。
- [x] `internal/app/router_test.go` / `upload_router_test.go`：> 1 MiB 图片走 OpenAPI 返回 201；**超大 JSON 请求体返回 413 `PAYLOAD_TOO_LARGE`**；Session Cookie 不能访问 OpenAPI；Bearer 不能访问 protected；Audit 中间件确实覆盖 OpenAPI。
- [x] `internal/app/runtime.go`：`apitoken.Service.Close()` 需进 `stopSequence()`，**排在 SQLite 关闭之前**；`runtime_test.go` 的启停轨迹断言同步。
- [x] `Makefile`：`api-docs --dir` 增加 `internal/apitoken,internal/openapi`。
- [x] `.trellis/spec/backend/dependency-injection.md` / `middleware-guidelines.md`：把「三路路由值组」与上传组清单更新为四路，并补上「JSON 子组叠 1 MiB」的模式（实现完成后同步 spec）。

### 阶段六：前端

- [x] `web/src/features/settings/api-tokens/`：`api/`（axios + Zod）、`hooks/`（TanStack Query）、`components/`（列表、创建、吊销确认、支持查看已吊销凭据）、`locales/` 三语、`index.tsx`。
- [x] `web/src/routes/_authenticated/settings/api-tokens.tsx`；`web/src/features/settings/index.tsx` 增加 `KeyRound` 侧边栏项；`web/src/lib/i18n.ts` 三处注册 `apiTokens`；`web/src/locales/*/common.json` 增加 `nav.APITokens`。
- [x] 创建弹窗：完整 Token 只存组件 state；关闭即清除；复制走 `copyToClipboard`；`lastUsedAt === null` 显示「从未使用」；若名称冲突（`API_TOKEN_NAME_ALREADY_EXISTS`）明确提示该备注名已被历史凭据占用（含已吊销凭据）。
- [x] `web/src/features/persons/data/schema.ts`：`source: z.enum(['LOCAL', 'API']).default('LOCAL')` / `sourceName: z.string().default('')`；`persons-columns-cells.tsx` 与 `persons-grid.tsx` 增加 Badge。
- [x] **同步既有 fixture**：`web/src/features/persons/data/schema.test.ts` 的 4 个 `personSchema.parse(raw)` 用例，以及 `components/*.test.tsx` 里的 mock person 对象；显式验证 `source: 'API'`。
- [x] 组件测试（真实无头浏览器）：创建→复制→吊销→列表刷新；非安全上下文剪贴板降级；API 人员徽标、LOCAL 不显示；Zod 解析新增字段。
- [x] **路由树生成先于构建**：新 route 文件后先 `cd web && pnpm dev` 起一次生成 `routeTree.gen.ts`（Ctrl-C 退出），再 `pnpm build`。`tsc -b` 早于 vite 的路由生成，直接 build 会因 `createFileRoute` 字面量不在已知 union 里报错。不手改 `routeTree.gen.ts`。
- [x] **对接文档交付物**：新增 `docs/openapi/person-face.md`（鉴权与 `WWW-Authenticate`、`personId` 锚点与不可变、幂等约定、`?replace=true` 语义、完整错误码表、并发/排队与重试策略、仅限可信专网、p95 实测位）。设置页也需有「仅限可信专网」提示文案（三语）。

### 阶段七：全链路验证与门禁

- [x] 后端测试（不依赖 engine 的包可直接跑）：
  - `go test -race ./internal/apitoken/... ./internal/openapi/... ./internal/person/...`
  - `python3 native/scripts/build.py go test -race ./internal/app/...`
- [x] 迁移测试：`go test -race ./internal/database/...`
- [x] 前端：`cd web && pnpm test -- src/features/settings/api-tokens src/features/persons`；先 `pnpm dev` 生成路由树，再 `pnpm build`。
- [x] `make api-docs`（确认 `swagger.json` 包含 `/openapi/persons`、`/api-tokens`，**且 `securityDefinitions.BearerAuth` 存在**）
- [x] `make frontend-check`
- [x] `make check`
- [x] 手工冒烟（有板/无板均可）：
  - 创建 Token → 用 curl 走 `POST /persons`、`POST .../faces`、`GET ...`、`DELETE ...`；
  - **无后缀上传**：`curl -F image=@/tmp/photo`（文件名无扩展名）必须 201；
  - **10.5 MiB 图**：应 413 `FILE_TOO_LARGE`（不是 422）；
  - **超大 JSON**（`POST /persons` 带 >1 MiB body）：应 413 `PAYLOAD_TOO_LARGE`；
  - 吊销 Token → 下一次请求 401，且审计表里能看到 `api_token.auth_rejected`；
  - 快速并发发起 4 个面档请求（全局额度 3）→ 至少有请求返回 429 且带 `Retry-After`；**此时发起 DELETE 请求依然能正常响应（不被 429 误杀）**；
  - Web UI 人员列表看到来源徽标；
  - 日志与审计 detail 中搜索原始 Token 字符串，确认零命中。

## 2. 验证命令 (Validation Commands)

```bash
# Token/OpenAPI/person 域（不依赖 internal/engine，可直接跑）
go test -race ./internal/apitoken/... ./internal/openapi/... ./internal/person/...

# app 路由与 Fx 装配间接依赖 internal/engine，必须走 native wrapper
python3 native/scripts/build.py go test -race ./internal/app/...

# 迁移 up/down
go test -race ./internal/database/...

# 前端：类型检查由 build 的 tsc -b 承担（必须先起 pnpm dev 生成路由树）
cd web && pnpm dev          # 生成 routeTree.gen.ts 后 Ctrl-C
cd web && pnpm test -- src/features/settings/api-tokens src/features/persons
cd web && pnpm build

# 文档与整库门禁
make api-docs
# 人工确认：internal/apidocs/swagger.json 里存在 securityDefinitions.BearerAuth
# 且 paths 包含 /openapi/persons
make frontend-check
make check
```

> 若 `internal/openapi` 的测试开始 import `internal/app`（例如复用真实 router），该包就间接依赖 engine，必须改用 `python3 native/scripts/build.py go test -race ./internal/openapi/...`。不要裸跑。
>
> `internal/apitoken` / `internal/openapi` 的单测如果要用真实 sqlite，注意连接池是 1；写错事务嵌套的表现是**测试永久挂住**而不是失败。遇到 hang 先在 `db.Transaction` 里搜 `s.db(ctx)`。

## 3. 不要做的事（写错就出事）

- ❌ 把 OpenAPI 路由挂进 `api.Group("/openapi")` 或 `api.Group("")`：会继承 1 MiB `MaxBodyLimit()`，10 MiB 照片直接 413，且无法事后放宽。
- ❌ 把 11 MiB 限额套给 JSON 端点：超大 JSON 会被报成 `413 FILE_TOO_LARGE`（“文件太大”），而其余 `/api/v1` 端点返回 `PAYLOAD_TOO_LARGE`。JSON 子组必须叠 1 MiB `MaxBodyLimit()`。
- ❌ 用 `apperr.Kind` 推导 OpenAPI 状态码：`apperr.Invalid("FILE_TOO_LARGE")` 会得到 **422**，而 PRD/契约要求 **413**。必须查 `statusFor(code)` 显式表。
- ❌ 在 `db.Transaction` 闭包里调用任何自己再取 `s.db(ctx)` 的 store 方法：连接池 `MaxOpenConns=1`，嵌套获取会**永久挂死**（不是超时，是进程级 DB 死锁，连 `/health` 都拿不到连接）。事务内所有查询必须用 `tx`。
- ❌ 在 OpenAPI 子组上挂 `auth.RequireAuth`：M2M Token 请求会被 Cookie Session 拦掉。
- ❌ 忘记给 OpenAPI 组挂 `audithttp.Middleware`：R4.2 会静默不生效。
- ❌ `?replace=true` 先删旧脸再提取：提取失败就把人员底库清空了。
- ❌ 在事务外删旧文件 / 先广播再提交：提交失败会留下「底库有、数据库没有」的孤儿。
- ❌ 把原始 Token、`token_hash`、`Authorization` 头写进日志、审计 detail/target、错误响应或前端持久化存储；鉴权失败的审计 detail 只能写枚举原因（`malformed` / `unknown_or_revoked`）。
- ❌ 每个请求同步写 `last_used_at`：SQLite 单写者 + 请求 ctx 在响应后取消；必须 60 秒合并 + 后台 context。
- ❌ 复用 `PersonResponse` / `FaceResponse` 作为 OpenAPI 响应：会泄露内部 ULID、`ownerRowId`、`imagePath`。
- ❌ 复用 `CreatePersonRequest`：它的 `personId` 是可选的，OpenAPI 必须必填。
- ❌ 从 `UpdatePersonRequest` 里删掉 `personId` 字段：`ShouldBindJSON` 会静默丢弃它，返回 200 而值没变（`http-api-guidelines.md` §4.4）。保留字段 + 不一致返回 409 `PERSON_ID_IMMUTABLE`。
- ❌ 依赖数据库 `DEFAULT 'LOCAL'`：GORM `Create` 会写零值 `''`；在 Go 侧显式赋值。
- ❌ 把 Token 名称上限写成 64：`api_token:` + 64 = 74，超出审计 `username` 的 64 字符契约；本任务固定 48。
- ❌ 用 `UUID` 描述内部 ID：实际是 26 位 ULID。
- ❌ 用 400 表示坏图/无人脸：本任务统一 422，和现有 `AddFace` 的 `apperr.Invalid` 映射一致。
- ❌ 裸调 `navigator.clipboard.writeText`：明文 HTTP 下可能不存在；必须走 `web/src/lib/clipboard.ts`。
- ❌ 手改 `web/src/routeTree.gen.ts`。
- ❌ 修改已应用的迁移、使用 `AutoMigrate`、跳号/复用迁移号。
- ❌ 在 OpenAPI handler 里手写限流：`c.ShouldBind` 就能把 11 MiB multipart 落内存，限流必须是子组中间件。
- ❌ 把面档限流器挂在整个 `faces` 子组上：这会导致清空人脸与单删人脸端点在上传打满时被连带 429 误杀。限流器必须精准挂在 `POST` 录脸端点上。
- ❌ 停机时裸跑异步 `touchLastUsed` goroutine：必须用 `sync.WaitGroup` 优雅等待排空（500ms 超时），且在 `stopSequence` 中确保 `apitoken.Service.Close()` 排在 SQLite 关闭之前。
- ❌ 在 OpenAPI 路由组混用参数名（如 `:id` 与 `:personId`）：Gin 的前缀树会直接报通配符冲突 panic。
- ❌ 在 `APITokenService == nil` 时免密放行 OpenAPI 路由：测试或未配置注入时必须加 500 阻断中间件。
- ❌ 把 `Retry-After` 写在 `httputil.WriteError` 之后：`WriteError` 不会给你留时机，头已经写出了。
- ❌ 把 `securityDefinitions` 写进 `internal/openapi` 的包注释：swag 只解析 `--generalInfo` 那一个文件，会静默丢失。
- ❌ 为了让 OpenAPI 的 404 语义成立而改 `person.Service.SetPrimaryFace` 的 `INVALID_FACE_OWNER`：Web UI 靠那个 422，映射写在本任务的包装层。

## 4. 风险与回滚点

- **迁移编号**：本任务定号 `000027` / `000028`（`000026` 已由 `11-03-alert-push-multi-channel` 声明）。开工时若发现已被占用，按当时最大版本重定并回改 design.md。
- **提取器排队不可取消（主要风险）**：`internal/faceextract.Client` 的串行点是 `c.mu.Lock()`，锁等待不看 ctx（`Extract` 在 Lock 之后才 `ctx.Err()`），`RequestTimeout=30s` 只约束单次往返。全局并发 3 / 单 Token 1 是对“排队深度不可控”的补偿，不是吞吐承诺。若要让排队本身可取消，另开任务改 `faceextract`（channel 信号量 + `select on ctx.Done`），不混在本任务。
- **内存峰值**：单面档请求 ~65 MiB（RGB 48 MiB + body 11 MiB + 解码副本）；并发 3 ≈ 195 MiB，在 RK3568 2 GiB 上可接受但不宽裕。调大并发前先看可用内存。
- **名称不可回收**：吊销后不能同名重建，可能不符合运维直觉；UI 需把 `API_TOKEN_NAME_ALREADY_EXISTS` 解释为“该名称已被历史令牌占用（含已吊销）”。
- **明文 HTTP**：Token 可被同网段嗅探；交付文档要求可信专网，TLS 作为后续部署选项。
- **来源语义**：`source` 是首次录入来源，不回答「当前由谁托管」；需要时另加 `managed_by`。
- **旧接口不一致**：`SearchByImage` 坏图 400 保持现状，另立任务统一；`INVALID_FACE_OWNER` 在 Web UI 侧仍是 422，OpenAPI 侧包装成 404。
- **回滚**：移除 OpenAPI 路由与前端入口；执行迁移 down；Token 表删除不影响人员数据；`persons.source` 列可保留或按 down 删除。

## 5. 完成定义 (Definition of Done)

- [ ] `prd.md` / `design.md` / `implement.md` 与实现一致（表结构、迁移号 **000027/000028**、路由、错误码、并发额度 3/1）。
- [ ] `make check` 通过；`make api-docs` 生成物包含新端点且 `securityDefinitions.BearerAuth` 存在。
- [ ] 手工冒烟全部通过：创建 Token、person upsert、face add、**无后缀上传 201**、**10.5 MiB → 413 `FILE_TOO_LARGE`**、**超大 JSON → 413 `PAYLOAD_TOO_LARGE`**、replace 失败不丢旧脸、并发超额 → 429 + `Retry-After`、吊销后 401 且有 `api_token.auth_rejected` 审计。
- [ ] 日志与审计中搜索原始 Token 字符串零命中。
- [ ] 人员列表来源徽标在真实浏览器中可见。
- [ ] `docs/openapi/person-face.md` 已交付，且覆盖鉴权/幂等/replace 语义/错误码表/并发与重试/可信专网部署约束。
