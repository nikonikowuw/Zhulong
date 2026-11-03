# 人员/人脸 OpenAPI 下发技术设计 (Design Document)

> 前置阅读：`prd.md`、`.trellis/spec/backend/{http-api-guidelines,database-guidelines,authentication-guidelines,middleware-guidelines,error-handling,dependency-injection}.md`、
> `.trellis/spec/frontend/{development-guidelines,theme-and-i18n}.md`、`.trellis/spec/architecture.md`。

## 1. 架构拓扑与边界

### 1.1 模块划分

新增两个包，避免把「凭据」和「业务资源」揉在一起：

| 包 | 职责 |
| --- | --- |
| `internal/apitoken` | `api_tokens` 表、Token 生成/哈希/吊销、`last_used_at` 合并、Bearer 中间件、Token 管理 HTTP（protected） |
| `internal/openapi` | `/api/v1/openapi/*` 人员/人脸处理器、对外响应投影、错误映射、过载限制 |
| `internal/person`（扩展） | 外部业务键 upsert、按 `person_id` 查询、原子换脸/清空、10 张上限、`source` 字段 |
| `internal/app` | 第四个路由值组 `openapi_routes`、Fx 装配、生命周期（`apitoken` 无需后台 worker） |

依赖方向：`openapi` → `person`、`apitoken`；`apitoken` → `audit`、`httputil`；`person` 不感知 Token。Token 名称通过方法参数传入 `person`，避免反向依赖。

### 1.2 路由组（关键）

OpenAPI 面档上传必须独立于 `/api/v1` 普通组，原因见 `middleware-guidelines.md §1`：Gin 在 `Group()` 时求值中间件，无法事后放宽 `MaxBodyLimit()`。

`internal/app/router.go` 新增第四个平行组：

```go
// /api/v1 普通组仍是 1 MiB；OpenAPI 面档需要 10 MiB 图片 + multipart 框架余量。
openapiGroup := router.Group("/api/v1/openapi")
openapiGroup.Use(
    httpmiddleware.AccessLog(p.Logger),
    httpmiddleware.MaxBodyLimitWithLimitAndCode(openapi.MaxRequestBodyBytes, "FILE_TOO_LARGE"),
)
if p.AuditService != nil {
    openapiGroup.Use(audithttp.Middleware(p.AuditService))
}
openapiGroup.Use(apitoken.RequireBearer(p.APITokenService, p.Logger))
for _, r := range p.OpenAPIRoutes {
    r.RegisterRoutes(openapiGroup)
}
```

中间件顺序：`AccessLog` → body limit → audit → Bearer 鉴权 → handler。`audit` 必须在鉴权之前，才能让鉴权中间件把 Actor 从 `anonymous` 升级成 `api_token:<name>`；`audit` 的 `c.Next()` 之后会重新读取请求上下文中的 Actor。

`openapi.MaxRequestBodyBytes = person.MaxFaceImageBytes + 1<<20`（10 MiB + 1 MiB multipart 框架），与 `algorithm.MaxUploadRequestBodySize` 的取值方式一致。`person.MaxFaceImageBytes` 从现有未导出常量提升为导出常量，避免两处漂移。

同一组内的 JSON 端点也会继承 11 MiB 上限。它们都有 Bearer 鉴权、没有列表上传，MVP 接受这个折中；如果后续要收紧，可再拆一个 1 MiB 的 JSON 子组。

### 1.3 Fx 装配

- `RouterParams` 增加：
  - `APITokenService *apitoken.Service`
  - `OpenAPIRoutes []RouteRegistrar` `group:"openapi_routes"`
- `internal/app/app.go` 新增 provider：
  - `apitoken.NewComponents(dbProvider, logger)` → `Store` / `Service` / `Handler` / `Middleware`；
  - `openapi.NewComponents(person.Service, apitoken.Service, logger)` → `Handler`。
- `apitoken.Handler` 贡献 `protected_routes`（Token 管理）；`openapi.Handler` 贡献 `openapi_routes`。
- 同步更新 `.trellis/spec/backend/dependency-injection.md` 的「三路路由值组」为四路；`router_test.go` 断言 `/api/v1/openapi/*` 未登录不被 Session 中间件拦截、而由 Bearer 中间件处理。
- `make api-docs --dir` 增加 `internal/apitoken,internal/openapi`。

## 2. 持久化契约

### 2.1 迁移编号

仓库当前最大迁移为 `000025`；本任务需要两个迁移。`11-03-alert-push-multi-channel/design.md` 已声明占用 `000026`，因此：

- **开工时按当时最大版本顺延**；先合入者占用 `000026`，另一任务顺延。
- 本文用 `0000NN_add_person_source` / `0000NN+1_create_api_tokens` 占位；例如 alert-push 先落地时，本任务实际使用 `000027` / `000028`。
- 未交付的迁移可以就地改号；已应用的迁移绝不改正文。

### 2.2 `persons` 来源字段

```sql
-- up
ALTER TABLE persons ADD COLUMN source TEXT NOT NULL DEFAULT 'LOCAL';
ALTER TABLE persons ADD COLUMN source_name TEXT NOT NULL DEFAULT '';

-- down
ALTER TABLE persons DROP COLUMN source_name;
ALTER TABLE persons DROP COLUMN source;
```

- 不建索引：`source` 只有两个值，选择性极低；当前列表也不按来源过滤。
- 不使用 `CHECK`：SQLite 的 `ALTER TABLE ADD COLUMN` 对约束支持有限，且既有人员表经历过重建；枚举校验收敛在 Go 侧。
- GORM 模型：

```go
const (
    PersonSourceLocal = "LOCAL"
    PersonSourceAPI   = "API"
)

type Person struct {
    // ...
    Source     string `gorm:"column:source;not null" json:"-"`
    SourceName string `gorm:"column:source_name;not null" json:"-"`
}
```

⚠️ **创建路径必须显式赋值**，不要依赖数据库默认值：GORM `Create` 会把零值 `''` 写进 SQL，除非模型带 `default` tag；而 `default` tag 与迁移里的 `DEFAULT` 又是两套语义。所有 `Person` 构造点都要显式写：`Service.CreatePerson`、`Store.CreatePersonImport`（CSV 导入）、`face_import.go` 的 `newPerson` 写 `LOCAL`；OpenAPI upsert 创建时写 `API` + Token 名称。

### 2.3 `api_tokens`

```sql
-- up
CREATE TABLE IF NOT EXISTS api_tokens (
    id TEXT PRIMARY KEY NOT NULL,                 -- ULID，26 字符
    name TEXT NOT NULL,                           -- 1..48 字符，活跃记录内唯一
    token_hash TEXT NOT NULL,                     -- SHA-256(raw token)，64 位小写十六进制
    masked_key TEXT NOT NULL,                     -- 前 7 + "..." + 后 4
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at DATETIME,                        -- NULL = 从未使用
    revoked_at DATETIME                           -- NULL = 生效；软吊销
);

CREATE UNIQUE INDEX IF NOT EXISTS ux_api_tokens_token_hash
    ON api_tokens(token_hash);

CREATE UNIQUE INDEX IF NOT EXISTS ux_api_tokens_active_name
    ON api_tokens(name)
    WHERE revoked_at IS NULL;

-- down
DROP INDEX IF EXISTS ux_api_tokens_active_name;
DROP INDEX IF EXISTS ux_api_tokens_token_hash;
DROP TABLE IF EXISTS api_tokens;
```

- 表只有 ≤10 条活跃记录，不建额外索引。
- `token_hash` 存 TEXT 而非 BLOB：便于现场排查（哈希本身不是秘密），也避免驱动层字节比较差异。
- `last_used_at` / `revoked_at` 用 `*time.Time`，JSON 为 `null`，前端无需区分零值。
- `revoked_at` 非空的行保留，用于 `source_name` 溯源与审计；列表默认只返回 `revoked_at IS NULL`。

## 3. `internal/apitoken`

### 3.1 生成与存储

```go
func GenerateToken() (raw, hash, masked string, err error) {
    buf := make([]byte, 32)
    if _, err = rand.Read(buf); err != nil {
        return "", "", "", fmt.Errorf("read token entropy: %w", err)
    }
    raw = "sk-" + hex.EncodeToString(buf)
    sum := sha256.Sum256([]byte(raw))
    hash = hex.EncodeToString(sum[:])
    masked = raw[:7] + "..." + raw[len(raw)-4:]
    return raw, hash, masked, nil
}
```

- 高熵秘密用 SHA-256 足够，不需要 bcrypt/Argon2；token 查询本身就是按哈希索引命中。
- 生成后若 `token_hash` 唯一冲突（概率可忽略），重试至多 3 次，不把冲突暴露给客户端。
- 原始 Token 只在 `CreateToken` 返回值中出现一次，不落库、不写审计、不进日志。

### 3.2 创建、上限与命名

`Service.CreateToken(ctx, name string) (*TokenCreateResult, error)`：

1. `normalizeTokenName`：trim、1–48 字符、拒绝控制字符。
2. 事务内 `CountActiveTokens`；`>= 10` → `apperr.Conflict("API_TOKEN_LIMIT_REACHED", ...)`。
3. 插入；唯一索引冲突按 `name` / `token_hash` 分别映射：
   - `API_TOKEN_NAME_ALREADY_EXISTS`（409）
   - 哈希冲突则重试生成。
4. 返回 `APITokenInfo` + 原始 Token。

数据库连接池固定 `MaxOpenConns=1`，事务内的 count+insert 已串行化；仍然必须把两者放在同一事务，避免未来连接数调整后出现竞态。

### 3.3 鉴权与 `last_used_at`

```go
func (s *Service) Authenticate(ctx context.Context, raw string) (Identity, error) {
    hash := sha256Hex(raw)
    token, err := s.store.FindActiveByHash(ctx, hash)
    if errors.Is(err, ErrNotFound) {
        return Identity{}, ErrUnauthorized
    }
    if err != nil {
        return Identity{}, err // 500，不是 401
    }
    s.touchLastUsed(token.ID)
    return Identity{ID: token.ID, Name: token.Name}, nil
}
```

`touchLastUsed`：

- `sync.Mutex` + `map[string]time.Time`；同一 Token 60 秒内只写一次。
- 第一次命中时启动一个短生命周期 goroutine：`context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)`，执行 `UPDATE api_tokens SET last_used_at=? WHERE id=?`。
- 失败只 `logger.Warn`，不影响请求。
- 进程重启后 map 为空，下一次请求最多写一次，可接受。

### 3.4 Bearer 中间件

```go
func RequireBearer(svc *Service, logger *zap.Logger) gin.HandlerFunc {
    return func(c *gin.Context) {
        raw, ok := parseBearer(c.GetHeader("Authorization"))
        if !ok {
            writeUnauthorized(c)
            return
        }
        identity, err := svc.Authenticate(c.Request.Context(), raw)
        if errors.Is(err, ErrUnauthorized) {
            writeUnauthorized(c)
            return
        }
        if err != nil {
            httputil.WriteError(c, apperr.Internal("INTERNAL_ERROR", "Internal server error", err))
            return
        }
        c.Request = c.Request.WithContext(audit.WithActor(c.Request.Context(), audit.Actor{
            IP:       c.ClientIP(),
            Username: "api_token:" + identity.Name,
            Source:   audit.SourceAPI,
        }))
        SetIdentity(c, identity)
        c.Next()
    }
}
```

- `parseBearer`：大小写不敏感匹配 `Bearer`，trim 后非空，拒绝多值/空值。
- `writeUnauthorized`：401 `UNAUTHORIZED` + `WWW-Authenticate: Bearer`，不区分缺失/格式/不存在/吊销。
- `audit.SourceAPI = "api"` 新增常量；不持久化，仅用于请求上下文诊断。
- 私有 key + 导出 `GetIdentity(c) (Identity, bool)`；禁止裸字符串 `c.Set/c.Get`。
- 不读取 Cookie，不接受 query token。

### 3.5 Token 管理 HTTP

挂在 protected 组（Cookie Session）：

| Method | Path | 说明 |
| --- | --- | --- |
| GET | `/api/v1/api-tokens` | 生效列表 `{items: [...]}`，created_at DESC |
| POST | `/api/v1/api-tokens` | 创建；201；`Cache-Control: no-store` |
| DELETE | `/api/v1/api-tokens/:id` | 软吊销；幂等 |

响应类型：

```go
type APITokenInfo struct {
    ID         string     `json:"id"`
    Name       string     `json:"name"`
    MaskedKey  string     `json:"maskedKey"`
    CreatedAt  time.Time  `json:"createdAt"`
    LastUsedAt *time.Time `json:"lastUsedAt"`
}

type APITokenCreateResponse struct {
    APITokenInfo
    Token string `json:"token"`
}
```

`CreateAPITokenRequest{ Name string `json:"name" binding:"required"` }`；服务层再做 trim 与字符校验，返回 `VALIDATION_FAILED` 字段级 details 或稳定业务码。

审计 action：

```go
const (
    ActionTokenCreate = "api_token.create"
    ActionTokenRevoke = "api_token.revoke"
)
```

同步 `web/src/features/audit/data/audit-actions.ts` 与三份 `actionNames`；`internal/audit/action_locales_test.go` 会强制 Go 常量 ⊆ 前端文案。

## 4. `internal/person` 扩展

### 4.1 来源字段投影

`PersonResponse` 增加：

```go
Source     string `json:"source"`
SourceName string `json:"sourceName"`
```

`newPersonResponse` 直接复制；前端 `personSchema` 增加 `source: z.enum(['LOCAL', 'API'])` 与 `sourceName: z.string()`。这是增量字段，旧前端不会崩。

### 4.2 按外部键 Upsert

```go
func (s *Service) UpsertPersonByExternalID(
    ctx context.Context, externalID, name, tokenName string,
) (p *Person, created bool, err error)
```

流程：

1. `normalizeExternalPersonID`：trim、1–64、拒绝 `/ \ ? # %` 与控制字符。
2. `normalizeName` 复用现有规则。
3. `store.GetPersonByExternalID`：
   - 命中：若姓名不同，调用现有 `UpdatePerson`（内部 id）以复用 gallery 重新发布；姓名相同不写库。`source`/`source_name` 不变。
   - 未命中：`createPerson` 写 `Source: API`、`SourceName: tokenName`；如果 `ErrDuplicatePersonID`（并发 upsert），重读后走更新分支。
4. 返回前统一按内部 id 调 `GetPerson`（含 Faces 摘要），保证 create / update / no-op 三条路径的响应形状一致。
5. 不使用裸 `INSERT ... ON CONFLICT` 直接返回，因为更新姓名必须触发 `publishPersonFaces` 重新广播；两步流程 + 唯一冲突重试足够，且实现简单。
6. 不修改 `person_id`（不可变）。

### 4.3 按外部键查询

- `GetPersonByExternalID` 返回 `*Person`（不含 Faces）；处理器再按内部 id 调 `GetPerson` 拿 `Faces` 摘要，或者新增 `Store.GetPersonWithFacesByExternalID`。
- 对外投影只取 `personId`、`name`、`source`、`sourceName`、`primaryFaceId`、`faceIds`、时间戳。
- `faceIds` 从 `Faces` 按 `created_at ASC, id ASC` 生成；不返回 `ownerRowId` / `imagePath`。

### 4.4 原子换脸

新增 store 方法：

```go
// ReplaceFaces 在单事务内删除该人员全部旧脸并插入新脸；返回被删除的旧脸（用于删文件/广播）。
func (s *Store) ReplaceFaces(ctx context.Context, personRowID string, face *Face) (removed []Face, err error)
```

实现要点：

1. `db.Transaction`：
   - `Select` 旧脸（`id`、`image_path`）——必须先取，删完就没了。
   - `DELETE FROM person_faces WHERE person_id = ?`。
   - `Create` 新脸。
   - `UPDATE persons SET primary_face_id = ?, updated_at = ? WHERE id = ?`。
2. 事务提交后由 service：
   - 删除旧文件（`removeFaceFile`）。
   - 逐旧脸 `BroadcastDelete(faceID, personID)`；再 `BroadcastUpsert(new)`。
3. 事务失败：service 删除刚写入的新文件；旧脸与旧文件保持原样。

service 方法：

```go
func (s *Service) AddFaceByExternalID(
    ctx context.Context, externalID, filename string, r io.Reader, replace bool,
) (face *Face, isPrimary bool, err error)
```

顺序必须是：解析人员 → 校验/嗅探 → 提取特征 → 写新文件 → 事务替换/追加 → 清理与广播。**不得先删旧脸再提取**。事务提交后重新读取 `primary_face_id`，据此返回 `isPrimary`。

### 4.5 追加与 10 张上限

现有 `Store.AddFace` 不检查数量；新增：

```go
var ErrFaceLimitExceeded = errors.New("face count limit exceeded")

func (s *Store) AddFaceWithLimit(ctx context.Context, face *Face, limit int) error
```

- 事务内 `Count` + `Create` + 首个脸设主照；`>= limit` 返回 `ErrFaceLimitExceeded`。
- service 映射：`apperr.Conflict("FACE_LIMIT_EXCEEDED", ...)` → 409。
- 上限统一为 `person.MaxFacesPerPerson = 10`，单张录入、追加、批量导入共用；
  - `Service.AddFaceWithReader`（Web UI 单张录入）改为调用 `Store.AddFaceWithLimit`；
  - `face_import.go` 的 `enrollFaceRecord` 也改用同一方法/同一常量，去掉「先查数量再插入」的跨事务窗口；
  - 第 11 张统一返回 409 `FACE_LIMIT_EXCEEDED`。
- `?replace=true` 不走上限判断（替换后只有 1 张）。

### 4.6 清空人脸

```go
func (s *Store) ClearFaces(ctx context.Context, personRowID string) ([]Face, error)
```

- 事务内先 `Select` 全部脸，再 `DELETE WHERE person_id = ?`；`persons.primary_face_id` 由 `ON DELETE SET NULL` 自动清空（`foreign_keys=on` 已确认）。
- service 提交后删文件，并 `BroadcastDelete("", externalID)` 一次清空底库。
- 人员不存在返回 `ErrNotFound`；已无脸返回空切片、无错误。

### 4.7 删除/主照的外部键包装

- `DeletePersonByExternalID` / `DeleteFaceByExternalID` / `SetPrimaryFaceByExternalID` 先解析内部 id，再调用现有方法。
- 对外错误映射：
  - 人员不存在 → 404 `PERSON_NOT_FOUND`
  - 脸不存在或不属于该人员 → 404 `FACE_NOT_FOUND`（不再暴露 `INVALID_FACE_OWNER` 的 422）
  - `PERSON_ID_REQUIRED_FOR_ENROLLMENT` 保持 409

## 5. `internal/openapi`

### 5.1 路由与处理器

`Handler.RegisterRoutes(rg *gin.RouterGroup)`（组前缀 `/api/v1/openapi`）：

```go
group := rg.Group("/persons")
group.POST("", h.Upsert)
group.GET("/:personId", h.Get)
group.PUT("/:personId", h.Update)
group.DELETE("/:personId", h.Delete)
group.POST("/:personId/faces", h.AddFace)
group.DELETE("/:personId/faces", h.ClearFaces)
group.DELETE("/:personId/faces/:faceId", h.DeleteFace)
group.PUT("/:personId/primary-face", h.SetPrimaryFace)
```

- `POST .../faces` 注册在 OpenAPI 组内，不能再挂到 upload 组，避免继承 500 MiB 限额。
- 所有 handler 只做绑定、调用 service、投影响应；不直接拼 store 调用。
- `personId` 从 `c.Param` 取，校验字符集后使用。

### 5.2 请求/响应类型

```go
type UpsertPersonRequest struct {
    PersonID string `json:"personId" binding:"required"`
    Name     string `json:"name" binding:"required"`
}

type UpdatePersonRequest struct {
    Name string `json:"name" binding:"required"`
}

type SetPrimaryFaceRequest struct {
    FaceID string `json:"faceId" binding:"required"`
}

type PersonInfo struct {
    PersonID      string     `json:"personId"`
    Name          string     `json:"name"`
    Source        string     `json:"source"`
    SourceName    string     `json:"sourceName"`
    PrimaryFaceID *string    `json:"primaryFaceId"`
    FaceIDs       []string   `json:"faceIds"`
    CreatedAt     time.Time  `json:"createdAt"`
    UpdatedAt     time.Time  `json:"updatedAt"`
}

type UpsertPersonResponse struct {
    PersonInfo
    Created bool `json:"created"`
}

type FaceInfo struct {
    FaceID    string    `json:"faceId"`
    IsPrimary bool      `json:"isPrimary"`
    CreatedAt time.Time `json:"createdAt"`
}
```

- 类型名遵守 `*Request` / `*Response` / `*Info` 约定，不使用 `*DTO`。
- `faceIds` 恒为 `[]`，不返回 `null`。
- 所有时间 `UTC()` 后输出。

### 5.3 过载限制

`openapi.Limiter`：

- 全局 `chan struct{}` 容量 8；每 Token `map[string]int` + `sync.Mutex`。
- `Acquire(tokenID) bool` / `Release(tokenID)`；map 计数归零时删除 key，避免无界增长。
- 获取失败：429 `TOO_MANY_REQUESTS` + `Retry-After: 1`。
- `defer Release` 必须在 handler 内，且覆盖所有返回路径。
- 该 limiter 只约束 OpenAPI；Web UI 与批量导入不受影响。

### 5.4 错误映射

`person.Service` 负责把提取器哨兵（`ErrNoFaceDetected` / `ErrUnsupportedImageFormat`）与新增的 `ErrFaceLimitExceeded` 收敛成 `apperr.Error`；`openapi` 对已带稳定 code 的 `apperr.Error` 直接 `httputil.WriteError`，只对少量包装层错误做补充映射。

| `apperr` code | HTTP |
| --- | ---: |
| `PERSON_NOT_FOUND` / `FACE_NOT_FOUND` | 404 |
| `INVALID_FILE_TYPE` / `UNSUPPORTED_IMAGE_FORMAT` / `NO_FACE_DETECTED` | 422 |
| `FILE_TOO_LARGE` | 413 |
| `FACE_LIMIT_EXCEEDED` / `PERSON_ID_REQUIRED_FOR_ENROLLMENT` | 409 |
| `TOO_MANY_REQUESTS` | 429 |
| `FEATURE_EXTRACT_FAILED` / `DATABASE_ERROR` / `INTERNAL_ERROR` | 500 |

`apperr.Invalid` 默认映射 422，不要为了坏图单独写 400；`SearchByImage` 的 400 历史行为不在本任务修改。

### 5.5 Swagger

- `@Tags openapi`
- `@Security BearerAuth`；在 `internal/app/main.go` 或 docs 初始化处补充 securityDefinitions（如现有文档没有，则在 `internal/openapi` 的包注释中声明）。
- `@Router /openapi/persons [post]` 等，basePath 仍为 `/api/v1`。
- `make api-docs` 必须重新生成 `internal/apidocs`；`--dir` 加入 `internal/apitoken,internal/openapi`。

## 6. 审计与 i18n

- 人员/人脸写操作复用 `person.*` action；`audithttp.Tag` 在 OpenAPI handler 中声明。
- Token 创建/吊销使用 `api_token.create` / `api_token.revoke`，Actor 为当前管理员。
- OpenAPI 请求的 Actor 由 Bearer 中间件升级为 `api_token:<name>`；`audit.ActorFromContext` 在 `c.Next()` 之后读取，能拿到升级后的值。
- 审计 `detail` 只写人脸数量、姓名、faceId 等；不写 Token、哈希、图片内容。
- 新错误码在 `internal/apitoken/locale.go` / `internal/openapi/locale.go` 注册三语；复用 `httputil` 与 `person` 已有码（`PERSON_NOT_FOUND`、`FACE_NOT_FOUND`、`FACE_LIMIT_EXCEEDED`、`NO_FACE_DETECTED`、`UNSUPPORTED_IMAGE_FORMAT`、`FILE_TOO_LARGE`）。`UNAUTHORIZED` 在 `apitoken` 包内也注册同文案，保证该包独立测试/独立使用时也有翻译（`RegisterMessages` 是合并语义）。
- `TOO_MANY_REQUESTS` 已由 `internal/clientlog/locale.go` 注册；若担心包初始化顺序，在 `internal/openapi/locale.go` 同文案重复注册（`RegisterMessages` 是合并语义，不会报错）。

## 7. 前端

### 7.1 Token 管理

目录：`web/src/features/settings/api-tokens/`

```
api/api-tokens-api.ts        # axios + Zod
hooks/use-api-tokens.ts      # TanStack Query
components/api-tokens-table.tsx
components/api-token-create-dialog.tsx
components/api-token-revoke-dialog.tsx
locales/{en,zh-Hans,zh-Hant}.json
index.tsx
```

- 路由：`web/src/routes/_authenticated/settings/api-tokens.tsx`，调用 `createFileRoute`；运行 `pnpm dev`/`pnpm build` 让 `routeTree.gen.ts` 生成，不手改。
- 侧边栏：`web/src/features/settings/index.tsx` 增加 `KeyRound` 入口；`web/src/locales/*/common.json` 增加 `nav.APITokens`。
- 列表：`items` 直接渲染（≤10，不分页）；`lastUsedAt === null` 显示「从未使用」。
- 创建：成功后把完整 Token 留在组件 state；关闭弹窗即清除；重新打开列表不重新请求原始 Token。
- 复制：`copyToClipboard`；失败显示手动选择提示。
- 吊销：二次确认；成功后 invalidate query。
- 三语：`web/src/lib/i18n.ts` 三处资源注册 `apiTokens` 命名空间。

### 7.2 人员来源徽标

- `web/src/features/persons/data/schema.ts`：`source: z.enum(['LOCAL', 'API'])`、`sourceName: z.string()`。
- `persons-columns-cells.tsx`：`PersonNameCell` 或独立 `PersonSourceCell` 渲染 `<Badge variant="outline">{sourceName}</Badge>`；`source === 'LOCAL'` 返回 `null`。
- `persons-grid.tsx` 同步。
- 不需要新增人员接口；`GET /api/v1/persons` 已返回扩展字段。

### 7.3 测试

- 真实无头浏览器：创建 → 展示完整 Token → 复制 → 吊销 → 列表刷新。
- 非安全上下文：`navigator.clipboard` 缺失时走 `execCommand` 降级（复用 `clipboard.test.ts` 的模式）。
- 人员页：API 人员显示徽标、LOCAL 人员不显示；Zod 解析新增字段。

## 8. 测试清单

### 后端

- `internal/database`：两个迁移的 up/down 对称测试；`persons` 旧行默认 `LOCAL`；`api_tokens` 索引存在。
- `internal/apitoken`：
  - 生成格式 `sk-<64hex>`、哈希稳定、masked 前 7 后 4；
  - 创建第 11 个返回 409；并发创建恰好 10 个成功；
  - 同名活跃 Token 返回 409；吊销后可复用名称；
  - 有效/无效/吊销 Token 鉴权；DB 故障返回 500 而非 401；
  - `last_used_at` 60 秒合并；并发鉴权 `-race`。
- `internal/person`：
  - upsert 创建/更新/并发冲突；
  - `source` 只在创建时写；
  - 10 张上限（单张 + 批量导入）；
  - `ReplaceFaces` 成功与失败回滚；
  - `ClearFaces` 清空主照与文件；
  - `DeleteFace` 主照提升不变。
- `internal/openapi`：
  - 所有错误码/状态码表驱动；
  - 跨人员 faceId 404；
  - `?replace=true` 提取失败保留旧脸；
  - 审计 Actor `api_token:<name>`；
  - GalleryNotifier 调用序列。
- `internal/app`：
  - `/api/v1/openapi` 的 > 1 MiB 上传返回 201；
  - Session Cookie 不能访问 OpenAPI；
  - Bearer 不能访问 protected 组；
  - `runtime_test.go` 更新路由值组。

### 前端

- Token 列表/创建/复制/吊销。
- 来源徽标。
- 三语 key 完整性。

## 9. 风险与回滚

- **迁移编号冲突**：与 `11-03-alert-push-multi-channel` 协调；先落地者占用 `000026`，另一个顺延。未交付迁移可改号。
- **提取器串行**：OpenAPI 请求与批量导入共享单客户端；高并发会排队。用 4/8 并发门限 + 429 控制，不承诺吞吐。
- **明文 HTTP**：Token 可被同网段嗅探；交付文档要求可信专网，TLS 作为后续部署选项。
- **来源语义**：`source` 是首次录入来源，不回答「当前由谁托管」；需要时另加 `managed_by`。
- **旧接口不一致**：`SearchByImage` 坏图 400 保持现状；本任务只统一人员/人脸下发接口为 422。
- **回滚**：删除新增路由与前端入口；执行迁移 down；已创建的 Token 行随表删除，不影响人员数据。`persons.source` 列可保留（默认值无害）或按 down 删除。
