# 人员/人脸 OpenAPI 下发实施计划 (Implementation Plan)

> 前置阅读：`prd.md`、`design.md`、`.trellis/spec/backend/{http-api-guidelines,database-guidelines,authentication-guidelines,middleware-guidelines,error-handling,dependency-injection,logging-guidelines}.md`、
> `.trellis/spec/frontend/{development-guidelines,theme-and-i18n,quality-guidelines}.md`、`.trellis/spec/architecture.md`。

## 0. 开工前已确认的决策（已写入 prd/design）

- [ ] `personId` 是外部业务键 `persons.person_id`；内部 ULID 不出现在 OpenAPI 响应。
- [ ] 图片/人脸校验统一 422（`INVALID_FILE_TYPE` / `UNSUPPORTED_IMAGE_FORMAT` / `NO_FACE_DETECTED`）；超 10 MB 413；超 10 张 409。
- [ ] `source` / `source_name` 记录首次录入来源，创建后不可变；最后修改方看审计 Actor。
- [ ] `?replace=true` 先提取/落新文件，再单事务替换；失败保留旧脸。
- [ ] Token 最多 10 个生效；名称 1–48 字符、活跃内唯一；软吊销、立即生效。
- [ ] OpenAPI 使用独立 `/api/v1/openapi` 路由组；不复用 `api` 组（1 MiB）或 `upload` 组（500 MiB）。
- [ ] 迁移编号开工时按当时最大版本顺延；`11-03-alert-push-multi-channel` 已声明 000026，先落地者占用。

## 1. 实施清单 (Ordered Checklist)

### 阶段一：数据库与模型

- [ ] `internal/person/model.go`：新增 `PersonSourceLocal` / `PersonSourceAPI` 常量；`Person` 增加 `Source` / `SourceName` 字段与 GORM tag；`PersonResponse` 增加 `source` / `sourceName`；`newPersonResponse` 复制。
- [ ] `person.Service.CreatePerson`：创建时显式写 `SourceLocal` / `SourceName=""`；不要依赖 DB 默认值。
- [ ] 其余 `Person` 构造点同步：`Store.CreatePersonImport`（CSV 导入）与 `face_import.go` 的 `newPerson` 也显式写 `SourceLocal`，避免 GORM 写入 `''`。
- [ ] 迁移 `0000NN_add_person_source.up.sql` / `.down.sql`（`TEXT NOT NULL DEFAULT`，无索引、无 CHECK）。例：alert-push 先落地时用 `000027`。
- [ ] `internal/apitoken/model.go`：`APIToken` GORM 模型、`TableName()`、审计 action 常量、错误哨兵（`ErrTokenNotFound`、`ErrTokenLimitReached`、`ErrTokenNameConflict`、`ErrUnauthorized`）。
- [ ] 迁移 `0000NN+1_create_api_tokens.up.sql` / `.down.sql`：表、`token_hash` 唯一索引、活跃 `name` 部分唯一索引。例：`000028`。
- [ ] `internal/database/migrations_test.go`（或现有 migration 测试文件）：两个迁移 up/down 对称；旧 `persons` 行默认 `LOCAL`；`api_tokens` 索引存在；回滚后表/列消失。

### 阶段二：`internal/apitoken`

- [ ] `token.go`：`GenerateToken`（32 字节 `crypto/rand`、`sk-` + hex、SHA-256、masked）；`normalizeTokenName`（trim、1–48、拒绝控制字符）。
- [ ] `store.go`：`CreateToken`（事务内 count+insert）、`ListActive`、`FindActiveByHash`、`Revoke`（幂等软删）、`TouchLastUsed`；唯一冲突分别映射名称/哈希。
- [ ] `service.go`：`CreateToken`、`ListTokens`、`RevokeToken`、`Authenticate`、`touchLastUsed`（60 秒合并 + 2s 后台 timeout）。
- [ ] `middleware.go`：`RequireBearer`、`parseBearer`（大小写不敏感、拒绝空/多值）、401 + `WWW-Authenticate`、DB 故障 500、审计 Actor 升级、`SetIdentity` / `GetIdentity` 类型化 accessor。
- [ ] `handler.go` / `requests.go` / `responses.go`：`GET/POST/DELETE /api-tokens`；创建响应 `Cache-Control: no-store`；`*Info` / `*Response` 命名；Swagger 注释。
- [ ] `locale.go`：`UNAUTHORIZED`（同文案重复注册，保证包独立测试）、`API_TOKEN_NOT_FOUND`、`API_TOKEN_LIMIT_REACHED`、`API_TOKEN_NAME_ALREADY_EXISTS`、`API_TOKEN_NAME_REQUIRED`、`API_TOKEN_NAME_TOO_LONG` 三语。
- [ ] 测试：生成格式、哈希/masked、并发 10 上限、同名活跃冲突、吊销幂等、有效/无效/吊销鉴权、DB 故障 500、`last_used_at` 合并、`-race`。
- [ ] 审计 action 跨层：`api_token.create` / `api_token.revoke` 同步 `web/src/features/audit/data/audit-actions.ts` 与三份 `actionNames`。

### 阶段三：`internal/person` 外部键能力

- [ ] `external.go`（或 `service.go`）：`normalizeExternalPersonID`（1–64、拒绝 `/ \ ? # %` 与控制字符）。
- [ ] `store.go`：`GetPersonByExternalIDWithFaces`（或复用 `GetPersonByExternalID` + `GetPerson`）。
- [ ] `service.go`：`UpsertPersonByExternalID(ctx, externalID, name, tokenName) (*Person, bool, error)`；命中更新姓名、未命中创建 API 来源、唯一冲突重读后重试。
- [ ] `service.go`：`AddFaceByExternalID(ctx, externalID, filename string, r io.Reader, replace bool) (*Face, bool, error)`：
  - 解析人员 → 校验格式/魔数 → 提取特征 → 写新文件 → 事务替换/追加 → 删旧文件 → 广播。
  - 失败路径删除新文件，保留旧数据。
- [ ] `store.go`：`ReplaceFaces`（事务内 select 旧脸 → delete → insert 新脸 → 更新 primary）、`ClearFaces`（select → delete，FK 清空 primary）、`AddFaceWithLimit`（事务内 count+insert）。
- [ ] `person.MaxFaceImageBytes` 导出；`person.MaxFacesPerPerson = 10` 统一常量；`face_import.go` 的 `maxFacesPerPerson` 改为引用。
- [ ] 现有 `Service.AddFaceWithReader` 改走 `Store.AddFaceWithLimit`；`face_import.go` 的 `enrollFaceRecord` 同样改走该方法，避免「先查数量再插入」的跨事务窗口；更新 handler/service 测试（第 11 张 409）。
- [ ] `service.go`：`ClearFacesByExternalID`、`DeletePersonByExternalID`、`DeleteFaceByExternalID`、`SetPrimaryFaceByExternalID`；跨人员脸统一 404。
- [ ] 错误哨兵：`ErrFaceLimitExceeded`、`ErrInvalidFileType`（如果希望与 `UNSUPPORTED_IMAGE_FORMAT` 区分）；映射到 `apperr`。
- [ ] 测试：upsert 创建/更新/并发、来源不变、10 张上限、替换成功/失败回滚、清空主照、主照提升、`-race`。

### 阶段四：`internal/openapi`

- [ ] `handler.go`：路由 `POST/GET/PUT/DELETE /persons`、`POST/DELETE /persons/:personId/faces`、`DELETE .../faces/:faceId`、`PUT .../primary-face`；组前缀 `/api/v1/openapi`。
- [ ] `requests.go` / `responses.go`：`UpsertPersonRequest`、`UpdatePersonRequest`、`SetPrimaryFaceRequest`、`PersonInfo`、`UpsertPersonResponse`、`FaceInfo`；不暴露内部 id / ownerRowId / imagePath。
- [ ] `errors.go`：哨兵 → HTTP/code 表驱动映射；`apperr.Invalid` 统一 422；`FACE_LIMIT_EXCEEDED` → 409；跨人员 faceId → 404。
- [ ] `limiter.go`：全局 8 / 单 Token 4；`Acquire`/`Release`；超限 429 + `Retry-After: 1`；map 归零即删。
- [ ] `handler.go`：审计 `audithttp.Tag`；添加 face 时记录数量/faceId，不写 Token/图片内容。
- [ ] `locale.go`：新错误码三语（`FACE_LIMIT_EXCEEDED` 已存在则复用；`TOO_MANY_REQUESTS` 可复用）。
- [ ] `handler_test.go`：状态码矩阵、跨人员 404、替换失败保留旧脸、审计 Actor、GalleryNotifier 序列、`-race`。
- [ ] Swagger：`@Tags openapi`、`@Security BearerAuth`、`@Router /openapi/...`；`make api-docs` 生成物更新。

### 阶段五：路由与装配

- [ ] `internal/app/router.go`：新增 `/api/v1/openapi` 平行组；中间件顺序 `AccessLog → MaxBodyLimitWithLimitAndCode(openapi.MaxRequestBodyBytes) → Audit → RequireBearer`。
- [ ] `internal/app/router.go` / `app.go`：`RouterParams` 增加 `APITokenService`、`OpenAPIRoutes`；新增 `openapi_routes` value group；`apitoken.Handler` 贡献 `protected_routes`。
- [ ] `internal/app/app.go`：`provideAPIToken`、`provideOpenAPI`；依赖 `person.Service`、`apitoken.Service`、`Config`、`Logger`。
- [ ] `internal/app/config.go`：如需可配置 `openapi.max_concurrent` / `openapi.per_token_concurrent`，加入默认值与严格校验；否则用常量。
- [ ] `internal/app/router_test.go` / `upload_router_test.go`：> 1 MiB 图片走 OpenAPI 返回 201；Session Cookie 不能访问 OpenAPI；Bearer 不能访问 protected；Audit 中间件确实覆盖 OpenAPI。
- [ ] `internal/app/runtime_test.go`：如新增生命周期，更新启停轨迹；`apitoken` 无后台 worker 则不需要。
- [ ] `Makefile`：`api-docs --dir` 增加 `internal/apitoken,internal/openapi`。
- [ ] `.trellis/spec/backend/dependency-injection.md` / `middleware-guidelines.md`：把「三路路由值组」与上传组清单更新为四路（实现完成后同步 spec）。

### 阶段六：前端

- [ ] `web/src/features/settings/api-tokens/`：`api/`（axios + Zod）、`hooks/`（TanStack Query）、`components/`（列表、创建、吊销确认）、`locales/` 三语、`index.tsx`。
- [ ] `web/src/routes/_authenticated/settings/api-tokens.tsx`；`web/src/features/settings/index.tsx` 增加 `KeyRound` 侧边栏项；`web/src/lib/i18n.ts` 三处注册 `apiTokens`；`web/src/locales/*/common.json` 增加 `nav.APITokens`。
- [ ] 创建弹窗：完整 Token 只存组件 state；关闭即清除；复制走 `copyToClipboard`；`lastUsedAt === null` 显示「从未使用」。
- [ ] `web/src/features/persons/data/schema.ts`：`source` / `sourceName`；`persons-columns-cells.tsx` 与 `persons-grid.tsx` 增加 Badge。
- [ ] 组件测试（真实无头浏览器）：创建→复制→吊销→列表刷新；非安全上下文剪贴板降级；API 人员徽标、LOCAL 不显示；Zod 解析新增字段。
- [ ] 运行生成器更新 `web/src/routeTree.gen.ts`（不手改）。

### 阶段七：全链路验证与门禁

- [ ] 后端测试（不依赖 engine 的包可直接跑）：
  - `go test -race ./internal/apitoken/... ./internal/openapi/... ./internal/person/...`
  - `python3 native/scripts/build.py go test -race ./internal/app/...`
- [ ] 迁移测试：`go test -race ./internal/database/...`
- [ ] 前端：`cd web && pnpm test -- src/features/settings/api-tokens src/features/persons && pnpm build`
- [ ] `make api-docs`（确认生成物包含 `/openapi/persons`、`/api-tokens`）
- [ ] `make frontend-check`
- [ ] `make check`
- [ ] 手工冒烟（有板/无板均可）：
  - 创建 Token → 用 curl 走 `POST /persons`、`POST .../faces`、`GET ...`、`DELETE ...`；
  - 吊销 Token → 下一次请求 401；
  - Web UI 人员列表看到来源徽标；
  - 日志中搜索原始 Token 字符串，确认零命中。

## 2. 验证命令 (Validation Commands)

```bash
# Token/OpenAPI/person 域（不依赖 internal/engine，可直接跑）
go test -race ./internal/apitoken/... ./internal/openapi/... ./internal/person/...

# app 路由与 Fx 装配间接依赖 internal/engine，必须走 native wrapper
python3 native/scripts/build.py go test -race ./internal/app/...

# 迁移 up/down
go test -race ./internal/database/...

# 前端：类型检查由 build 的 tsc -b 承担
cd web && pnpm test -- src/features/settings/api-tokens src/features/persons
cd web && pnpm build

# 文档与整库门禁
make api-docs
make frontend-check
make check
```

> 若 `internal/openapi` 的测试开始 import `internal/app`（例如复用真实 router），该包就间接依赖 engine，必须改用 `python3 native/scripts/build.py go test -race ./internal/openapi/...`。不要裸跑。

## 3. 不要做的事（写错就出事）

- ❌ 把 OpenAPI 路由挂进 `api.Group("/openapi")` 或 `api.Group("")`：会继承 1 MiB `MaxBodyLimit()`，10 MiB 照片直接 413，且无法事后放宽。
- ❌ 在 OpenAPI 组上挂 `auth.RequireAuth`：M2M Token 请求会被 Cookie Session 拦掉。
- ❌ 忘记给 OpenAPI 组挂 `audithttp.Middleware`：R4.2 会静默不生效。
- ❌ `?replace=true` 先删旧脸再提取：提取失败就把人员底库清空了。
- ❌ 在事务外删旧文件 / 先广播再提交：提交失败会留下「底库有、数据库没有」的孤儿。
- ❌ 把原始 Token、`token_hash`、`Authorization` 头写进日志、审计 detail/target、错误响应或前端持久化存储。
- ❌ 每个请求同步写 `last_used_at`：SQLite 单写者 + 请求 ctx 在响应后取消；必须 60 秒合并 + 后台 context。
- ❌ 复用 `PersonResponse` / `FaceResponse` 作为 OpenAPI 响应：会泄露内部 ULID、`ownerRowId`、`imagePath`。
- ❌ 复用 `CreatePersonRequest`：它的 `personId` 是可选的，OpenAPI 必须必填。
- ❌ 依赖数据库 `DEFAULT 'LOCAL'`：GORM `Create` 会写零值 `''`；在 Go 侧显式赋值。
- ❌ 把 Token 名称上限写成 64：`api_token:` + 64 = 74，超出审计 `username` 的 64 字符契约；本任务固定 48。
- ❌ 用 UUID 描述内部 ID：实际是 26 位 ULID。
- ❌ 用 400 表示坏图/无人脸：本任务统一 422，和现有 `AddFace` 的 `apperr.Invalid` 映射一致。
- ❌ 裸调 `navigator.clipboard.writeText`：明文 HTTP 下可能不存在；必须走 `web/src/lib/clipboard.ts`。
- ❌ 手改 `web/src/routeTree.gen.ts`。
- ❌ 修改已应用的迁移、使用 `AutoMigrate`、跳号/复用迁移号。

## 4. 风险与回滚点

- **迁移编号冲突**：与 `11-03-alert-push-multi-channel` 协调；先落地者占用 000026，另一个在未交付阶段改号。
- **提取器串行**：OpenAPI 与批量导入共享 `faceextract.Client`；4/8 并发门限 + 429 只控制排队，不提升吞吐。文档写清 p95 需板端实测。
- **明文 HTTP**：Token 可被同网段嗅探；交付文档要求可信专网，TLS 作为后续部署选项。
- **来源语义**：`source` 是首次录入来源，不回答「当前由谁托管」；需要时另加 `managed_by`。
- **旧接口不一致**：`SearchByImage` 坏图 400 保持现状，另立任务统一。
- **回滚**：移除 OpenAPI 路由与前端入口；执行迁移 down；Token 表删除不影响人员数据；`persons.source` 列可保留或按 down 删除。

## 5. 完成定义 (Definition of Done)

- [ ] `prd.md` / `design.md` / `implement.md` 与实现一致（表结构、迁移号、路由、错误码）。
- [ ] `make check` 通过；`make api-docs` 生成物包含新端点。
- [ ] 手工冒烟五条（创建 Token、person upsert、face add、replace 失败不丢旧脸、吊销后 401）全部通过。
- [ ] 日志中搜索原始 Token 字符串零命中。
- [ ] 人员列表来源徽标在真实浏览器中可见。
