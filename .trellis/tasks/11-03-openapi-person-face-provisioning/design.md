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
// JSON 子组另挂 1 MiB，见下。
openapiGroup := router.Group("/api/v1/openapi")
openapiGroup.Use(
    httpmiddleware.AccessLog(p.Logger),
    httpmiddleware.MaxBodyLimitWithLimitAndCode(openapi.MaxRequestBodyBytes, "FILE_TOO_LARGE"),
)
if p.AuditService != nil {
    openapiGroup.Use(audithttp.Middleware(p.AuditService))
}
if p.APITokenService != nil {
    openapiGroup.Use(apitoken.RequireBearer(p.APITokenService, p.Logger))
} else {
    // 防御：若未注入凭据服务，直接阻断，杜绝免密开放风险！
    openapiGroup.Use(func(c *gin.Context) {
        httputil.WriteError(c, apperr.Internal("INTERNAL_ERROR", "API token service unconfigured", nil))
        c.Abort()
    })
}
for _, r := range p.OpenAPIRoutes {
    r.RegisterRoutes(openapiGroup)
}
```

中间件顺序：`AccessLog` → body limit → audit → Bearer 鉴权 → handler。`audit` 必须在鉴权之前，才能让鉴权中间件把 Actor 从 `anonymous` 升级成 `api_token:<name>`；`audit` 的 `c.Next()` 之后会重新读取请求上下文中的 Actor。

`openapi.MaxRequestBodyBytes = person.MaxFaceImageBytes + 1<<20`（10 MiB + 1 MiB multipart 框架），与 `algorithm.MaxUploadRequestBodySize` 的取值方式一致。`person.MaxFaceImageBytes` 从现有未导出常量提升为导出常量，避免两处漂移。

**Gin 路由通配符命名一致性铁律**：Gin 底层前缀树要求同一 HTTP 方法下同一路径深度的命名参数必须完全一致。所有涉及该路径层级的参数名必须严格统一为 `:personId`，严防在个别路由中误写为 `:id`，否则 Gin 会在启动时直接 panic。

**JSON 端点不继承 11 MiB**：`/persons`、`/persons/:personId`、`/primary-face` 的请求体上限是 JSON 对象，11 MiB 既无必要也让错误语义混乱（超大 JSON 会得到 `413 FILE_TOO_LARGE`，而其余 `/api/v1` 端点返回 `PAYLOAD_TOO_LARGE`）。Gin 允许在已 `Group()` 的子组上再叠加中间件，因此：

```go
// 面档上传与 JSON 分开：JSON 子组叠 1 MiB，错误码沿用平台默认的 PAYLOAD_TOO_LARGE。
jsonGroup := openapiGroup.Group("")
jsonGroup.Use(httpmiddleware.MaxBodyLimit()) // 1 MiB, PAYLOAD_TOO_LARGE
jsonGroup.POST("/persons", h.Upsert)
jsonGroup.GET("/persons/:personId", h.Get)
jsonGroup.PUT("/persons/:personId", h.Update)
jsonGroup.DELETE("/persons/:personId", h.Delete)
jsonGroup.PUT("/persons/:personId/primary-face", h.SetPrimaryFace)

// 面档组：限流中间件仅挂载在 POST 面档录入路由上，绝不挂在子组上连带误伤 DELETE！
faces := openapiGroup.Group("/persons/:personId/faces")
faces.POST("", h.limiter.Middleware(), h.AddFace)
faces.DELETE("", h.ClearFaces)
faces.DELETE("/:faceId", h.DeleteFace)
```

`Group("")` 继承父组已挂的 `AccessLog` / `MaxBodyLimit(11 MiB)` / audit / Bearer，再追加自己的 1 MiB。因 Gin 按**声明顺序**执行，子组的 1 MiB 在父组 11 MiB 之后生效，实际约束取更小的那个，语义正确。如果不想依赖叠加语义，就把 `MaxBodyLimit()` 提前，用两个平行的 `router.Group("/api/v1/openapi")`，各自完整挂一遍中间件（成本是多几行）。

> ⚠️ 面档路径的 JSON 请求（例如误发 `Content-Type: application/json`）仍会走 11 MiB 限额；这属于客户端错误，handler 的绑定失败会返回 422，可接受。

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
- **`cmd/Zhulong/main.go` 加入 `relatedFiles`**：`securityDefinitions` 必须写在 `--generalInfo` 指定的文件里（见 §5.5）。

## 2. 持久化契约

### 2.1 迁移编号

仓库当前最大迁移为 `000025`，且 `11-03-alert-push-multi-channel` **仍是 planning、零代码**。因此本任务**直接定号**，不再用 `0000NN` 占位（占位号在实施文档里放太久，实现者会照抄）：

- `11-03-alert-push-multi-channel/design.md` §2.1 已明确占用 `000026_create_alert_destinations`，且该任务仍在 planning。因此**本任务取 `000027` / `000028`**，不抢占别人的编号，也不需要对方改号：
  - `000027_add_person_source`
  - `000028_create_api_tokens`
- 若开工时发现 `000027`/`000028` 已被其他分支合并，**按当时最大版本重新定号并同步本文件**，不留下 `NN` 占位符。
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
    name TEXT NOT NULL,                           -- 1..48 字符，全表唯一（含已吊销）
    token_hash TEXT NOT NULL,                     -- SHA-256(raw token)，64 位小写十六进制
    masked_key TEXT NOT NULL,                     -- 前 7 + "..." + 后 4
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at DATETIME,                        -- NULL = 从未使用
    revoked_at DATETIME                           -- NULL = 生效；软吊销
);

CREATE UNIQUE INDEX IF NOT EXISTS ux_api_tokens_token_hash
    ON api_tokens(token_hash);

-- 全表唯一，不是部分唯一索引：名称是 persons.source_name 与审计 Actor 的溯源键，
-- 允许吊销后复用会让历史记录指向重建后的另一个 Token。
CREATE UNIQUE INDEX IF NOT EXISTS ux_api_tokens_name
    ON api_tokens(name);

-- down
DROP INDEX IF EXISTS ux_api_tokens_name;
DROP INDEX IF EXISTS ux_api_tokens_token_hash;
DROP TABLE IF EXISTS api_tokens;
```

- 表只有 ≤10 条活跃记录，不建额外索引。
- `token_hash` 存 TEXT 而非 BLOB：便于现场排查（哈希本身不是秘密），也避免驱动层字节比较差异。
- `last_used_at` / `revoked_at` 用 `*time.Time`，JSON 为 `null`，前端无需区分零值。
- `revoked_at` 非空的行保留，用于 `source_name` 溯源与审计；列表默认只返回 `revoked_at IS NULL`。
- 名称全表唯一意味着吊销后**不能**同名重建。这是有意的取舍：溯源准确性优先于名称复用便利。需要复用名称时，管理员应先确认没有人员/审计记录依赖旧名称（UI 在创建失败时应提示这一点）。

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
   - `API_TOKEN_NAME_ALREADY_EXISTS`（409，**包括已吊销的同名记录**）
   - 哈希冲突则重试生成。
4. 返回 `APITokenInfo` + 原始 Token。

> ⚠️ **事务内只能使用 `tx`**。本仓库连接池固定 `MaxOpenConns=1`（`internal/database/store.go:140`），在 `db.Transaction(func(tx *gorm.DB) ...)` 闭包里再调用任何自己取 `s.db(ctx)` 的 store 方法，会为同一个唯一连接二次获取而**永久阻塞**（不是 `busy_timeout` 超时，整个进程的 DB 访问全部停摆，连 `/health` 都拿不到连接）。事务内的所有读写必须写成 `gorm.G[T](tx)...` 或 `tx.Model(...)`。同理适用于 `ReplaceFaces` / `AddFaceWithLimit` / `ClearFaces`。

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
- **停机安全与生命周期管理**：`context.WithoutCancel` 让刷新在请求结束后仍会跑。为了杜绝后台 Goroutine 逃逸与停机时 SQLite 已关闭的竞态调用，`apitoken.Service` 必须使用 `sync.WaitGroup` 追踪在途任务，并持有一个 `stopCh`：
  - 启动落库 goroutine 前先 `select` 检查 `stopCh`，已停止直接跳过；未停止则 `wg.Add(1)` 并在 goroutine 结束时 `defer wg.Done()`。
  - `Service.Close()` 时首先 `close(stopCh)` 阻断新任务，然后带短超时（如 500ms）执行 `wg.Wait()` 排空在途任务，确保在向后流转前所有写 DB 行为彻底停稳。
  - `apitoken.Service` 在 `internal/app/runtime.go` 的 `stopSequence()` 里**必须排在 `closeDatabase` 之前**（如置于 `closeAudit` 前后），实现真正的优雅停机。
- 进程重启后 map 为空，下一次请求最多写一次，可接受。

### 3.4 Bearer 中间件

```go
func RequireBearer(svc *Service, logger *zap.Logger) gin.HandlerFunc {
    return func(c *gin.Context) {
        raw, ok := parseBearer(c.GetHeader("Authorization"))
        if !ok {
            svc.recordRejected(c, apitoken.RejectMalformed)
            writeUnauthorized(c)
            return
        }
        identity, err := svc.Authenticate(c.Request.Context(), raw)
        if errors.Is(err, ErrUnauthorized) {
            // 只记“存在但无效”，不记 raw / hash / 前缀，避免把 67 字符令牌
            // 的任意片段写成可持续探测的取证。
            svc.recordRejected(c, apitoken.RejectUnknownOrRevoked)
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
- **失败鉴权必须留痕**：`audit` 中间件在 Bearer 之前，而 401 路径不会调用 `audithttp.Tag`（handler 根本没执行），所以伪造/吊销尝试默认**零审计记录**。`RequireBearer` 需直接调 `auditService.Record(...)` 写一条 `api_token.auth_rejected`，字段为 `{IP, Username: "anonymous", Action, Target: "openapi", Detail: "reject=<reason>"}`。Detail 只能是枚举值（`malformed` / `unknown_or_revoked`），**严禁**包含 Header 片段、token 前缀、哈希或完整 URL。这个调用不进入请求 ctx 的 Actor 链，避免污染后续审计。若 `AuditService` 为 nil（测试/降级），静默跳过。

### 3.5 Token 管理 HTTP

挂在 protected 组（Cookie Session）：

| Method | Path | 说明 |
| --- | --- | --- |
| GET | `/api/v1/api-tokens` | 生效列表 `{items: [...]}`，created_at DESC |
| POST | `/api/v1/api-tokens` | 创建；201；`Cache-Control: no-store` |
| DELETE | `/api/v1/api-tokens/:id` | 软吊销；幂等 |

响应类型（遵循 `http-api-guidelines.md` §4「命名约定」：顶层载荷 `*Response`、嵌套投影 `*Info`）：

```go
type APITokenInfo struct {
    ID         string     `json:"id"`
    Name       string     `json:"name"`
    MaskedKey  string     `json:"maskedKey"`
    CreatedAt  time.Time  `json:"createdAt"`
    LastUsedAt *time.Time `json:"lastUsedAt"`
}

// APITokenListResponse 是非分页列表的顶层载荷。数量物理上限 ≤10，不分页，
// 但仍返回 {items: []} 结构，且 items 恒为 []，绝不返回 null。
type APITokenListResponse struct {
    Items []APITokenInfo `json:"items"`
}

// APITokenCreateResponse 是创建成功的顶层载荷。内嵌 APITokenInfo 的字段会被
// 提升到顶层，同时追加一次性明文 token。
type APITokenCreateResponse struct {
    APITokenInfo
    Token string `json:"token"`
}

// APITokenRevokeResponse 是吊销成功的顶层载荷。
type APITokenRevokeResponse struct {
    Revoked bool `json:"revoked"`
}
```

`CreateAPITokenRequest{ Name string `json:"name" binding:"required"` }`；服务层再做 trim 与字符校验，返回 `VALIDATION_FAILED` 字段级 details 或稳定业务码。

审计 action：

```go
const (
    ActionTokenCreate       = "api_token.create"
    ActionTokenRevoke       = "api_token.revoke"
    ActionTokenAuthRejected = "api_token.auth_rejected"
)
```

同步 `web/src/features/audit/data/audit-actions.ts` 与三份 `actionNames`；`internal/audit/action_locales_test.go` 会扫描 `internal/**/*.go` 的 `Action*` 常量并强制 Go 常量 ⊆ 前端文案（正则不限定 `model.go`，写在 `apitoken` 包里同样生效，但常量名必须是 `Action` 前缀 + 带点的纯小写值）。

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
   - `DELETE FROM person_faces WHERE person_id = ?`（此时 `persons.primary_face_id` 会由 SQLite 外键约束 `ON DELETE SET NULL` 自动置空）。
   - `Create` 新脸（新记录落库，获得合法 ID）。
   - `UPDATE persons SET primary_face_id = ?, updated_at = ? WHERE id = ?`（指向刚插入的新脸 ID，外键有效）。
   - **严格外键顺序铁律**：必须遵循「先查 $\to$ 删旧 $\to$ 插新 $\to$ 更新主照」，严禁在插新前更新主照指针，否则会违反外键约束。
2. **事务内每一步只能用 `tx`**：连接池 `MaxOpenConns=1`，闭包里再调自己取 `s.db(ctx)` 的方法会永久阻塞（见 §3.2 警告）。
3. 事务提交后由 service：
   - 删除旧文件（`removeFaceFile`）。
   - 底库 IPC 优化：直接调用 1 次 `BroadcastDelete("", externalID)` 清空算法底库该人员历史人脸，紧接着调用 1 次 `BroadcastUpsert(new)` 载入新人脸。无需按旧脸数量循环发起多次 IPC 往返，既避免了网络/管道开销，又杜绝了底库残留中间态。
4. 事务失败：service 删除刚写入的新文件；旧脸与旧文件保持原样。

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

- 事务内 `Count` + `Create` + 首个脸设主照；`>= limit` 返回 `ErrFaceLimitExceeded`。同理事务内只用 `tx`。
- service 映射：`apperr.Conflict("FACE_LIMIT_EXCEEDED", ...)` → 409。
- 上限统一为 `person.MaxFacesPerPerson = 10`，单张录入、追加、批量导入共用；
  - `Service.AddFaceWithReader`（Web UI 单张录入）改为调用 `Store.AddFaceWithLimit`；
  - `face_import.go` 的 `enrollFaceRecord` 也改用同一方法/同一常量，去掉「先查数量再插入」的跨事务窗口；
  - **`enrollFaceRecord` 必须在 `errors.Is(err, ErrFaceLimitExceeded)` 时显式回频换回 `faceProcessError{code: "FACE_LIMIT_EXCEEDED"}`**，否则导入失败行会退化成泛化的 `FACE_ADD_FAILED`，前端文案错位（`web/src/features/persons/locales` 里两个 key 的文案不同）。
  - 第 11 张统一返回 409 `FACE_LIMIT_EXCEEDED`。
- `?replace=true` 不走上限判断（替换后只有 1 张）。
- ⚠️ **既有测试要同步改**：`internal/person/service_test.go:391` 的 `TestAddFace_IDsAreUniqueUnderConcurrency` 起 20 个 goroutine 并发录脸并断言**全部成功**；上限统一后会有 10 个 409。必须把它拆成两条：①ID 唯一性（用 `store.AddFace` 或 limit=20 绕过上限）；②上限并发（并发 20 次断言恰好 10 成功 / 10 个 `FACE_LIMIT_EXCEEDED`）。不改就是红灯。

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
  - `PERSON_ID_REQUIRED_FOR_ENROLLMENT` **不进入 OpenAPI 映射表**：OpenAPI 按 `personId` 定位人员，该分支永远走不到（它只影响 Web UI 按内部 ID 录脸的路径，且仍返回 409，保持既有契约）。
- ⚠️ **不得修改共享服务的错误码**：`person.Service.SetPrimaryFace` 对跨人员脸返回 `apperr.Invalid("INVALID_FACE_OWNER")`（422），`internal/person/service_test.go:521` 用 `requireDomainError(..., apperr.KindInvalid, "INVALID_FACE_OWNER")` 钉住了这个契约。Web UI 依赖它。OpenAPI 的 404 收敛必须写在 `internal/openapi` 的包装层（把返回的 `INVALID_FACE_OWNER` 重写成 `FACE_NOT_FOUND` 再交给 `httputil.WriteError`），而不是改 `person` 包。

### 4.8 扩展名校验策略

现有 `AddFaceWithReader` 的顺序是**先查 `allowedFaceImageExts`，再嗅探魔数**（`internal/person/service.go:637`）。第三方平台的 `curl -F image=@photo` 或管道上传经常没有文件后缀，硬门禁会把合法照片拒成 422 `INVALID_FILE_TYPE`，与 PRD R3.2 的意图相背。

实现上二选一，**本轮选 A**：

- **A（推荐）**：把「解析/嗅探/写盘」从 `AddFaceWithReader` 抽出一个内部函数，用参数控制是否要求扩展名。`AddFaceByExternalID` 传 `requireExt=false`，Web UI 的 `AddFaceWithReader` 保持 `requireExt=true`。落盘扩展名取嗅探结果（`.jpg` / `.png` / `.webp`），filename 只是弱提示。
- B：修改 `AddFaceWithReader` 语义为 sniff-only。不做：这会改变 Web UI 的现有错误行为（现有测试 `TestAddFace_RejectsInvalidFileType` 断言 `script.sh` 等被拒，改成 sniff-only 后 `script.sh` 内容如果是 JPEG 就会被接受，与“只允许 jpg/jpeg/png/webp”文案冲突）。

无论哪种，**内容魔数始终是唯一真正的判据**；扩展名只是给人看的提示。

## 5. `internal/openapi`

### 5.1 路由与处理器

`Handler.RegisterRoutes(rg *gin.RouterGroup)`（组前缀 `/api/v1/openapi`），但按 §1.2 拆两个限额层级：

```go
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
    // 面档组：继承组内的 11 MiB。限流器精准修饰 POST 录脸，绝不挂在子组连带影响 DELETE。
    faces := rg.Group("/persons/:personId/faces")
    faces.POST("", h.limiter.Middleware(), h.AddFace)
    faces.DELETE("", h.ClearFaces)
    faces.DELETE("/:faceId", h.DeleteFace)

    // JSON 端点：叠 1 MiB（PAYLOAD_TOO_LARGE），避免超大 JSON 报成 FILE_TOO_LARGE。
    // 变量不要叫 json：encoding/json 在同一个文件里天天要用。
    jsonGroup := rg.Group("")
    jsonGroup.Use(httpmiddleware.MaxBodyLimit())
    jsonGroup.POST("/persons", h.Upsert)
    jsonGroup.GET("/persons/:personId", h.Get)
    jsonGroup.PUT("/persons/:personId", h.Update)
    jsonGroup.DELETE("/persons/:personId", h.Delete)
    jsonGroup.PUT("/persons/:personId/primary-face", h.SetPrimaryFace)
}
```

- 所有 handler 只做绑定、调用 service、投影响应；不直接拼 store 调用。
- `personId` 从 `c.Param` 取，校验字符集后使用。
- **限流器必须早于绑定且不误伤删除**：`openapi.Limiter` 做成独立中间件，**精准挂在 `POST .../faces` 路由上**（见 §5.3），在 `c.ShouldBind` 解析 11 MiB multipart、占用内存之前就拒绝。绝不能作为 `faces` 组的通用中间件，否则当面档上传并发打满时，所有的清空人脸与单删人脸操作都会被 429 误杀！

### 5.2 请求/响应类型

```go
type UpsertPersonRequest struct {
    PersonID string `json:"personId" binding:"required"`
    Name     string `json:"name" binding:"required"`
}

// UpdatePersonRequest retains PersonID so a conflicting value fails loudly
// (409 PERSON_ID_IMMUTABLE) instead of being silently dropped by the JSON decoder
// (http-api-guidelines.md §4.4).
type UpdatePersonRequest struct {
    PersonID *string `json:"personId,omitempty" binding:"omitempty,max=64"`
    Name     string  `json:"name" binding:"required"`
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

// UpsertPersonResponse is the top-level payload of POST /persons. Created
// distinguishes 201 (true) from 200 (false).
type UpsertPersonResponse struct {
    PersonInfo
    Created bool `json:"created"`
}

// PersonDetailResponse is the top-level payload of GET/PUT /persons/{personId}.
// It deliberately does NOT reuse the name PersonResponse: internal/person has its own
// PersonResponse with a completely different (internal-facing) shape, and two types
// with the same name in a call graph is exactly how "which one goes over the wire"
// gets misread during review.
type PersonDetailResponse struct {
    PersonInfo
}

// FaceResponse is the top-level payload of POST /persons/{personId}/faces.
// There is no separate FaceInfo: a face is never nested inside a person projection
// here (PersonInfo carries faceIds, not face objects), so one type is enough.
type FaceResponse struct {
    FaceID    string    `json:"faceId"`
    IsPrimary bool      `json:"isPrimary"`
    CreatedAt time.Time `json:"createdAt"`
}

type ClearFacesResponse struct {
    DeletedFaces int `json:"deletedFaces"`
}

type DeleteFaceResponse struct {
    Deleted bool `json:"deleted"`
}

type DeletePersonResponse struct {
    Deleted bool `json:"deleted"`
}
```

- 类型名遵守 `*Request` / `*Response` / `*Info` 约定，不使用 `*DTO`。顶层载荷一律是 `*Response`，嵌套投影是 `*Info`。
- ⚠️ `openapi.PersonDetailResponse` 与 `person.PersonResponse` 不同名是**有意为之**：后者是内部面板投影（含 `id` / `faces[].ownerRowId`），复用同名会让「哪个类型出网」在 review 时读错。
- `faceIds` 恒为 `[]`，不返回 `null`。
- 所有时间 `UTC()` 后输出。
- `Source` 为 `"LOCAL"` / `"API"`；OpenAPI 创建的永远为 `API`，但对 `GET` 返回真实值。

### 5.3 过载限制

`openapi.Limiter`：

- 全局 `chan struct{}` 容量 `GlobalConcurrent`；每 Token `map[string]int` + `sync.Mutex`。
- `Acquire(tokenID) bool` / `Release(tokenID)`；map 计数归零时删除 key，避免无界增长。
- **精准挂载在 POST 面档录入端点上**（`faces.POST("", h.limiter.Middleware(), h.AddFace)`，`defer Release` 包住 `c.Next()`）：
  - 必须早于 handler 第一行的 `c.ShouldBind`，在解析 11 MiB multipart 落内存之前拦截并返回 429；
  - 严禁将 limiter 挂在包含 DELETE 的整个子组上，避免 DELETE 操作因 NPU/面档上传额度耗尽而被误报 429。
- 获取失败：`c.Header("Retry-After", "1")` **先写**，再 `httputil.WriteError`。`httputil.WriteError` 不设任何响应头，写在它后面无效。
- 该 limiter 只约束 OpenAPI 面档上传；Web UI 与批量导入不受影响。

#### 并发额度的取值（重要）

初稿写的是「全局 8 / 单 Token 4」。这个额度**不安全**，两个理由：

1. **提取器排队不可取消**：`internal/faceextract` 的串行点是 `c.mu.Lock()`，而锁等待**不看 ctx**（`Extract` 在 Lock 之后才 `ctx.Err()`）。`RequestTimeout=30s` 只约束单次往返。全局 8 意味着第 8 个请求要等前 7 个各最多 30s，最坏 ~3.5 分钟，而 `http.Server` 只有 `ReadHeaderTimeout`，没有 handler 超时——平台侧看到的是连接悬挂。批量导入和 OpenAPI 抢同一把锁，导入任务能把平台推送饿死。
2. **内存**：4096² 图 `DecodeToRGB24` 展开 ≈ 48 MiB RGB，加 11 MiB 请求体、`io.ReadAll` 整图副本、multipart 缓存，单请求峰值 ~65 MiB。全局 8 ≈ **520 MiB**，RK3568 常见 2 GiB（还要与 NPU 权重、视频缓冲共享），会直接触发 OOM。

因此：

- **全局并发 `3`、单 Token `1`**（串行客户端本来就不存在并行收益，多 Token 只是互相排队）。写进常量并在 `config.toml` 留可选覆盖位（不强制）。
- OpenAPI 的提取调用再包一层 `context.WithTimeout(30*time.Second)`，超时映射 429 `TOO_MANY_REQUESTS`（而不是 500，因为对平台而言“稍后重试”是正确指令）。
- design §9 与交付文档都需明确：**不承诺吞吐，只承诺并发上限与排队行为**；真实 p95 在板端实测后填入对接文档（AC11）。

### 5.4 错误映射

**不要用 `apperr.Kind` 推导状态码**。本项目里 `Kind` 与 HTTP 状态不是一一对应的，而且同一个 code 会因路径不同而映射不同状态码（例如 `FILE_TOO_LARGE`：`search-by-image` 在 handler 层特判成 413，而 `AddFace` 的 `apperr.Invalid("FILE_TOO_LARGE")` 自然落到 422）。OpenAPI 必须持有一张**显式 code → status 表**，Kind 只作兜底：

```go
// statusFor pins the outward status of every code the OpenAPI surface can emit.
// The Kind-based default in httputil.KindToHTTPStatus is a fallback for codes that
// are not listed here; it is not authoritative, because apperr.Invalid covers both
// "unusable upload" (422) and "file too large" (413) in this codebase.
func statusFor(code string) int {
    switch code {
    case "PERSON_NOT_FOUND", "FACE_NOT_FOUND":
        return http.StatusNotFound
    case "INVALID_FILE_TYPE", "UNSUPPORTED_IMAGE_FORMAT", "NO_FACE_DETECTED", "VALIDATION_FAILED":
        return http.StatusUnprocessableEntity
    case "FILE_TOO_LARGE", "PAYLOAD_TOO_LARGE":
        return http.StatusRequestEntityTooLarge
    case "FACE_LIMIT_EXCEEDED", "PERSON_ID_IMMUTABLE":
        return http.StatusConflict
    case "TOO_MANY_REQUESTS":
        return http.StatusTooManyRequests
    case "FEATURE_EXTRACT_FAILED", "DATABASE_ERROR", "INTERNAL_ERROR", "STORAGE_ERROR":
        return http.StatusInternalServerError
    }
    return 0 // fall back to httputil's Kind mapping
}
```

| `apperr` code | HTTP | 备注 |
| --- | ---: | --- |
| `PERSON_NOT_FOUND` / `FACE_NOT_FOUND` | 404 | 跨人员 faceId 也归入 `FACE_NOT_FOUND` |
| `INVALID_FILE_TYPE` / `UNSUPPORTED_IMAGE_FORMAT` / `NO_FACE_DETECTED` / `VALIDATION_FAILED` | 422 | 客户端输入不可用 |
| `FILE_TOO_LARGE` | 413 | **必须显式查表**：`apperr.Invalid` 默认是 422，不查表就是错的 |
| `FACE_LIMIT_EXCEEDED` / `PERSON_ID_IMMUTABLE` | 409 | |
| `TOO_MANY_REQUESTS` | 429 | 需额外写 `Retry-After: 1` |
| `FEATURE_EXTRACT_FAILED` / `DATABASE_ERROR` / `INTERNAL_ERROR` / `STORAGE_ERROR` | 500 | |

- `INVALID_FACE_OWNER` **不直接透传**：包装层重写成 `FACE_NOT_FOUND` + 404（见 §4.7）。
- `PERSON_ID_REQUIRED_FOR_ENROLLMENT` **不列入 OpenAPI 映射表**：接口按 `personId` 定位人员，该分支永不触发。
- 实现上写一个 `writeOpenAPIError(c, err)`：先把 `*apperr.Error` 拆成 `(code, message, cause)`，用 `statusFor` 得到状态码；非零则 `httputil.WriteError(c, httputil.NewError(status, code, message, cause))`，零则直接交给 `httputil.WriteError` 的默认 Kind 映射（它同时负责本地化文案）。**不要**复制一份 `httputil` 的本地化逻辑。

### 5.5 Swagger

- `@Tags openapi`
- `@Security BearerAuth`（每个端点）
- `@Router /openapi/persons [post]` 等，basePath 仍为 `/api/v1`。
- **`securityDefinitions` 必须写在 `cmd/Zhulong/main.go` 的通用注释块里**，紧挨已有的 `@title` / `@version` / `@BasePath`：

```go
// @title           Zhulong API
// @version         1.0
// @description     Local API for the Zhulong edge operations host.
// @BasePath        /api/v1
// @securityDefinitions.apikey BearerAuth
// @in              header
// @name            Authorization
// @description     M2M API token: "Bearer sk-<64 hex>". Only the /openapi routes accept it.
func main() { ... }
```

> ⚠️ swag 的 `ParseGeneralAPIInfo` **只解析 `--generalInfo` 指定的那一个文件**（这里是 `cmd/Zhulong/main.go`），且只认 `isGeneralAPIComment` 的注释块。写在 `internal/openapi` 的包注释里会被**静默忽略**：`@Security BearerAuth` 会指向不存在的 scheme，Swagger UI 的 Authorize 按钮不会出现，而且没有任何报错。
- `make api-docs --dir` 必须加入 `internal/apitoken,internal/openapi`；`make api-docs` 后**人工确认** `internal/apidocs/swagger.json` 的 `securityDefinitions.BearerAuth` 存在且 `/openapi/persons` 已收录。

## 6. 审计与 i18n

- 人员/人脸写操作复用 `person.*` action；`audithttp.Tag` 在 OpenAPI handler 中声明。
- Token 创建/吊销使用 `api_token.create` / `api_token.revoke`，Actor 为当前管理员。
- 鉴权失败写 `api_token.auth_rejected`（由 Bearer 中间件直接 `auditService.Record`，不经过 `audithttp.Tag`），Detail 只含枚举原因（`malformed` / `unknown_or_revoked`），**严禁**含 Header 片段、token 前缀或哈希。
- OpenAPI 请求的 Actor 由 Bearer 中间件升级为 `api_token:<name>`；`audit.ActorFromContext` 在 `c.Next()` 之后读取，能拿到升级后的值。
- 审计 `detail` 只写人脸数量、姓名、faceId 等；不写 Token、哈希、图片内容。
- **`?replace=true` 必须进 detail**：追加与替换在审计里不可区分就无法回溯“为什么这个人的旧脸没了”。面档录入手写 `audithttp.Tag(c, ActionPersonFaceAdd, target, "replace=true|false")`，用 `audithttp.TagDetail` 在已知结果后补上 faceId 与被删旧脸数（替换时）。
- 新错误码在 `internal/apitoken/locale.go` / `internal/openapi/locale.go` 注册三语；复用 `httputil` 与 `person` 已有码（`PERSON_NOT_FOUND`、`FACE_NOT_FOUND`、`FACE_LIMIT_EXCEEDED`、`NO_FACE_DETECTED`、`UNSUPPORTED_IMAGE_FORMAT`、`FILE_TOO_LARGE`）。`UNAUTHORIZED` 在 `apitoken` 包内也注册同文案，保证该包独立测试/独立使用时也有翻译（`RegisterMessages` 是合并语义）。
- `TOO_MANY_REQUESTS` 已由 `internal/clientlog/locale.go` 注册；但 `clientlog` 只被 `internal/app` 引用，`internal/openapi` 的独立单测不保证它被初始化。若不确定，在 `internal/openapi/locale.go` 同文案重复注册（`RegisterMessages` 是合并语义，不会报错）。

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
- 列表：`items` 直接渲染（≤10，不分页）；`lastUsedAt === null` 显示「从未使用」。可支持切换「显示已吊销」视图，帮助管理员看清历史占用的 Token 名称与吊销时间，避免视觉断层。
- 创建：成功后把完整 Token 留在组件 state；关闭弹窗即清除；重新打开列表不重新请求原始 Token。若名称冲突（`API_TOKEN_NAME_ALREADY_EXISTS`），UI 明确提示“该备注名已被历史凭据占用（含已吊销凭据），请更换新名称”。
- 复制：`copyToClipboard`；失败显示手动选择提示。
- 吊销：二次确认；成功后 invalidate query。
- 三语：`web/src/lib/i18n.ts` 三处资源注册 `apiTokens` 命名空间。

### 7.2 人员来源徽标

- `web/src/features/persons/data/schema.ts`：
  ```ts
  source: z.enum(['LOCAL', 'API']).default('LOCAL'),
  sourceName: z.string().default(''),
  ```
  增加 `.default()` 声明，不仅为前端提供极高的向后兼容容错韧性（防止历史或异常数据缺少字段导致白屏），同时自然兼容旧测试 fixture。
- `persons-columns-cells.tsx`：`PersonNameCell` 或独立 `PersonSourceCell` 渲染 `<Badge variant="outline">{sourceName}</Badge>`；`source === 'LOCAL'` 返回 `null`。
- `persons-grid.tsx` 同步。
- 不需要新增人员接口；`GET /api/v1/persons` 已返回扩展字段。
- ⚠️ **同步既有测试 fixture**：同步更新 `web/src/features/persons/data/schema.test.ts` 的 4 个用例及组件 mock 数据，显式验证 `source: 'API'` 与 `sourceName` 的解析与渲染。

### 7.3 路由树生成顺序（会卡构建）

`web/package.json` 的 `build` 是 `tsc -b && vite build`：`tsc -b` **先于** vite 的 TanStack Router 生成步骤。新增 `web/src/routes/_authenticated/settings/api-tokens.tsx` 后，若 `routeTree.gen.ts` 还没更新，`tsc -b` 会因为 `createFileRoute('/_authenticated/settings/api-tokens')` 的字面量不在已知 union 中而报错。

顺序必须是：

```bash
cd web && pnpm dev           # 起一次，生成 routeTree.gen.ts，然后 Ctrl-C
cd web && pnpm build         # 或直接 make frontend-build
```

不手改 `routeTree.gen.ts`。

### 7.4 测试

- 真实无头浏览器：创建 → 展示完整 Token → 复制 → 吊销 → 列表刷新。
- 非安全上下文：`navigator.clipboard` 缺失时走 `execCommand` 降级（复用 `clipboard.test.ts` 的模式）。
- 人员页：API 人员显示徽标、LOCAL 人员不显示；Zod 解析新增字段（见 §7.2 的 fixture 同步要求）。

## 8. 测试清单

### 后端

- `internal/database`：`000027` / `000028` 的 up/down 对称测试；`persons` 旧行默认 `LOCAL`；`api_tokens` 的 `token_hash` 与 `name` 两个唯一索引存在；down 后表/列与索引全部消失。
- `internal/apitoken`：
  - 生成格式 `sk-<64hex>`、哈希稳定、masked 前 7 后 4；
  - 创建第 11 个返回 409；并发创建恰好 10 个成功；
  - **同名（含已吊销）返回 409**；吊销后**不能**同名重建（与设计一致）；吊销幂等返回成功；
  - 有效/无效/吊销 Token 鉴权；DB 故障返回 500 而非 401；
  - **鉴权失败写 `api_token.auth_rejected`，且 Detail 不含任何凭据片段**；
  - `last_used_at` 60 秒合并；并发鉴权 `-race`；
  - `Close()` 幂等且通过 `sync.WaitGroup` 优雅等待排空在途落库任务；
- `internal/person`：
  - upsert 创建/更新/并发冲突；
  - `source` 只在创建时写；
  - 10 张上限（单张 + 批量导入），且**导入失败行的 code 仍是 `FACE_LIMIT_EXCEEDED`**；
  - `ReplaceFaces` 成功与失败回滚；外键顺序合规；底库广播单次清空+插入；事务内不嵌套取连接（`-race` + 超时保护，避免挂死时整个测试卡住）；
  - `ClearFaces` 清空主照与文件；
  - `DeleteFace` 主照提升不变；
  - **拆分 `TestAddFace_IDsAreUniqueUnderConcurrency`**：ID 唯一性用例不再断言 20 个全成功；新增上限用例断言恰好 10 成功 / 10 个 `FACE_LIMIT_EXCEEDED`。
- `internal/openapi`：
  - 所有错误码/状态码表驱动（**包括 `FILE_TOO_LARGE` → 413，而不是 `apperr.Invalid` 默认的 422**）；
  - 跨人员 faceId 404；
  - `?replace=true` 提取失败保留旧脸；
  - 审计 Actor `api_token:<name>`；`?replace` 两个值在审计 detail 里可区分；
  - GalleryNotifier 调用序列；
  - **无后缀合法图片（`image` 字段 filename 为 `photo`）返回 201**；
  - 429 响应携带 `Retry-After`；
  - 限流器在绑定前拒绝：超限请求不应消耗 multipart 解析成本；**面档上传限流不误伤 DELETE 端点**（并发 3 个慢速上传时，DELETE 依然 200/404 正常返回，不报 429）。
- `internal/app`：
  - `/api/v1/openapi` 的 > 1 MiB 上传返回 201；
  - **JSON 子组的超大请求体返回 413 `PAYLOAD_TOO_LARGE`（不是 `FILE_TOO_LARGE`）**；
  - `RouterParams.APITokenService == nil` 时 OpenAPI 路由自动阻断（500），绝不免密放行；
  - Session Cookie 不能访问 OpenAPI；
  - Bearer 不能访问 protected 组；
  - `runtime_test.go` 更新路由值组与 `apitoken.Close` 在 `stopSequence()` 中排在 SQLite 之前。

### 前端

- Token 列表/创建/复制/吊销。
- 来源徽标（含 `source.test.ts` 的 4 处 fixture 同步）。
- 三语 key 完整性。

## 9. 风险与回滚

- **迁移编号**：本任务定号 `000027` / `000028`（`000026` 由 `11-03-alert-push-multi-channel` 先行占用）。开工时若发现已被占用，按当时最大版本重定并回改本文，不留占位符。
- **提取器排除不可取消（主要风险）**：`internal/faceextract` 的串行点是 `c.mu.Lock()`，锁等待不看 ctx，`RequestTimeout` 只约束单次往返。全局并发 3 / 单 Token 1 是对“排队深度不可控”的补偿，不是吞吐承诺。**若后续要把排队也做成可取消，应另开一个改 `faceextract` 的任务（channel 信号量 + `select` on ctx.Done），不混在本任务里。**
- **内存峰值**：单面档请求 ~65 MiB（RGB 展开 48 MiB + body 11 MiB + 解码副本）；并发 3 ≈ 195 MiB，在 RK3568 2 GiB 上可接受但不宽裕。调大并发前先看可用内存。
- **明文 HTTP**：Token 可被同网段嗅探；交付文档（AC11）要求可信专网，TLS 作为后续部署选项。
- **名称不可回收**：吊销后不能同名重建，可能不符合运维直觉；UI 创建失败时必须把 `API_TOKEN_NAME_ALREADY_EXISTS` 解释为“该名称已被历史令牌占用（含已吊销）”。
- **来源语义**：`source` 是首次录入来源，不回答「当前由谁托管」；需要时另加 `managed_by`。
- **旧接口不一致**：`SearchByImage` 坏图 400 保持现状；本任务只统一人员/人脸下发接口为 422。`INVALID_FACE_OWNER` 在 Web UI 侧仍是 422，OpenAPI 侧包装成 404，两处不共享。
- **回滚**：删除新增路由与前端入口；执行迁移 down；已创建的 Token 行随表删除，不影响人员数据。`persons.source` 列可保留（默认值无害）或按 down 删除。
