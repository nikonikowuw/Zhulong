# 技术设计：审计核心解耦与 Gin HTTP 适配

## 1. 包边界

```text
internal/audit/          Actor、Entry、Service、Store；不导入 Gin
internal/audit/http/     Gin Middleware、Tag API、审计 HTTP Handler（package audithttp）；导入 internal/audit
internal/auth/           认证成功后把 Actor 注入 Request.Context；调用 HTTP Tag API
internal/app/            为 API 与 upload 路由组装审计中间件和 Service
```

目录为 `internal/audit/http`，但包名使用 `audithttp`，避免与 `net/http` 同名；否则 `net/http` 与适配包同时导入的文件必须起别名，且 swag 会生成 `http.AuditLogDTO` 这类易混淆定义名（现有为 `audit.AuditLogDTO`）。`internal/audit/http` 中的文件名保持 `handler.go` 等现有命名，仅包声明为 `package audithttp`。

现有 `internal/audit.Handler` 迁入 `internal/audit/http`。HTTP 适配包不得导入 `internal/auth`，从结构上避免 `auth -> audit/http -> auth` 循环；认证身份由 `auth.RequireAuth` 以 `audit.WithActor` 写入 Context。

## 2. Actor 与记录语义

`Actor` 位于核心包，字段为 `IP`、`Username`、`Source`。系统只有一个可配置登录账号；认证成功时 `Username` 可记录该账号名，供现有 `audit_logs` 字段和诊断使用，但不用于区分多个操作者，也不得硬编码为 `admin`。人类 HTTP 操作的主要归因字段是 `IP`。`Source` 仅用于请求期上下文，本次不加数据库列或 API 字段。系统事件使用 `Username: system`、`Source: system`、空 IP（无客户端来源）；不使用 `127.0.0.1` 冒充来源地址。`RecordEvent` 无 Actor 时使用 `unknown`，IP 留空，不推断系统身份；核心 `entryToLog` 对空用户名也使用 `unknown`，避免现有 `admin` 默认值造成误归因。IP 只能表明网络来源，NAT、动态地址和共享代理可能降低其对个人的区分能力。

认证中间件在 `c.Next()` 前设置请求 Context：

```go
actor := audit.Actor{Username: user.Username, IP: c.ClientIP(), Source: "web"}
c.Request = c.Request.WithContext(audit.WithActor(c.Request.Context(), actor))
```

HTTP Middleware 在请求开始时以 `anonymous` 初始化 Actor，并记录请求 IP。认证成功时保留唯一配置账号名；`TagActor` 可在登录/初始化成功后补充该用户名，并同步更新 Gin Tag 与 `c.Request.Context()`；来源 IP 始终保留为本次请求 IP。失败登录仍为 `anonymous`，尝试的用户名只可用于 `Target`。所有 `RecordEvent` 从请求 Context 取得最终 Actor。

Gin `ClientIP()` 只有在可信代理边界明确时才可用于记录真实客户端地址。Router 显式禁用未配置代理的转发头信任；需要反向代理的部署必须配置明确的可信 CIDR，不允许默认信任任意代理。

**本部署无反向代理**：边缘设备前端不存在反向代理，`gin.New()` 的默认 `trustedProxies` 为 `0.0.0.0/0` 与 `::/0`（gin v1.12.0 的 `defaultTrustedCIDRs`），即默认信任任意来源的 `X-Forwarded-For` / `X-Real-IP`，必须显式关闭：

```go
// newRouter 中 gin.New() 之后
if err := router.SetTrustedProxies(nil); err != nil {
    p.Logger.Warn("failed to disable trusted proxies", zap.Error(err))
}
```

`SetTrustedProxies(nil)` 使 `prepareTrustedCIDRs` 返回 `nil, nil`，`isTrustedProxy` 恒为 false，`ClientIP()` 退化为 TCP 对端地址（`RemoteIP()`）。⚠️ 注意 `ClientIP()` 内部存在「unix socket 永远 trusted」的豁免分支：本项目 `runtime.go` 使用 `r.listen("tcp", ...)`，当前不会触发；若将来新增 Unix Socket 访问方式，该豁免会绕过全部代理策略，需重新评估。

无反向代理同时意味着登录限流键（`authService.Login` 的 `clientIP`）与审计 IP 语义天然一致，无需为限流单独改用 `RemoteAddr`。

⚠️ 前提：审计以 IP 作为主要归因字段，成立的条件是 HTTP 服务实际对外监听（如 `0.0.0.0:port` 或局域网地址），而非仅 `127.0.0.1`。若部署配置为 loopback，所有请求的审计 IP 均为 `127.0.0.1`，IP 归因将失去区分能力——此时应视为部署配置问题，而非审计模块缺陷。

## 3. Gin 适配与路由挂载

`internal/audit/http`（package `audithttp`）提供 `Middleware(service)`, `Tag`, `TagMiddleware(action, target, detail)`, `TagAction(action)`, `TagTarget`, `TagDetail`, `TagActor` 和用于 2xx 领域失败的 `TagError(code, safeMessage)`。

**硬约束：只有 Handler 层可调用审计 API。** Service 层不得依赖 `*gin.Context`，不得导入 `internal/audit` 或 `internal/audit/http`。用户操作的审计声明一律归属 HTTP Handler；Service 保持纯粹（`Person Service` 删除 `recordAudit`/`failAudit` 即为此约束的落地）。系统事件的 Service 例外仅限窄接口 `SystemEventRecorder`（只含 `RecordSystem`，不接收 `*gin.Context`）。

**`TagAction(action)` 是必需的**，不能只靠静态 `TagMiddleware`。存在 action 依赖请求体才能确定的场景：

```go
// internal/camera/handler.go:203-206 —— update 与 toggle 共用同一路由
action := audit.ActionCameraUpdate
if req.Enabled != nil && req.Name == nil && req.MainStream == nil && req.SubStream == nil {
    action = audit.ActionCameraToggle
}
detail := updateAuditDetail(req) // detail 同样依赖请求体
```

`TagMiddleware` 必须在 `HandleJSON` 之前注册（保证绑定失败也有审计意图），因此它无法感知请求体。该场景必须由 Handler 在绑定成功后用 `TagAction` 改写 action，用 `TagDetail` 改写 detail。

Auth 的 `TagMiddleware` 必须在 `httputil.HandleJSON` 之前运行，确保 JSON 绑定失败也有审计意图；Auth 请求体成功解析后可用 `TagTarget` 更新目标。其他 Handler 在绑定/校验或业务副作用前调用 `Tag`。中间件只在存在有效 Tag 时写审计；HTTP `>= 400` 或安全错误元数据标记失败。未经审查的 `c.Errors` 只可作为失败信号，不直接序列化其原始文本。显式 `TagError` 可覆盖 2xx 请求的领域结果，例如 `SyncNow` 返回 `SyncStatus.State == failed`。目标 ID 可在业务结果产生后通过 `TagTarget` 更新。

### panic 路径（实测：`c.Next()` 后读状态不可用）

⚠️ **`c.Next()` 之后的收口代码在 panic 时不会执行**，必须专门设计。根因在 gin 的 handler 索引推进方式：

```go
// gin@v1.12.0/context.go:187
func (c *Context) Next() {
    c.index++
    for c.index < safeInt8(len(c.handlers)) {
        if c.handlers[c.index] != nil { c.handlers[c.index](c) }
        c.index++   // ← panic 时永不执行
    }
}
```

handler panic → `c.index++` 未执行 → 外层中间件的 `c.Next()` 直接 panic 冒泡 → `Recovery` 捕获后 `c.index = 0`，外层恢复循环时 `handlers[0]` 是它自身，条件不成立立即退出 → **收口代码被跳过**。

实测矩阵（gin v1.12.0，与项目同版本，最小复现验证）：

| 写法 | 客户端状态码 | 审计是否执行 | 审计观测到的状态 | 结论 |
| --- | --- | --- | --- | --- |
| `c.Next()` 之后读取 | 500 | ❌ 否 | -1（未读到） | 漏记 |
| `defer` 中读取 | 500 | ✅ 是 | **200** | ⚠️ 更差：panic 被误记为 success |
| 审计中间件在 `Recovery` 外层 | 500 | ✅ 是 | 500 | 正确，但违反 spec「Recovery 最外层」 |
| `Recovery` panic 回调通知审计 | 500 | ✅ 是 | 500 | ✅ 采用 |

`defer` 写法误记的原因：`responseWriter.reset()` 将 `status` 置为 `defaultStatus = http.StatusOK`（`response_writer.go:17,64`），panic 时尚未写响应头，`c.Writer.Status()` 返回 200。

**采用方案**：`internal/httpmiddleware.Recovery` 增加 panic 回调，审计层实现观察者。保持 `Recovery` 仍为最外层，不违反 `middleware-guidelines.md`。

```go
// internal/httpmiddleware —— 新增可选观察者，不引入 audit 依赖
// PanicObserver 在 Recovery 处理 panic 后、响应已写入时被调用。
type PanicObserver interface {
    OnPanic(c *gin.Context)
}

func Recovery(logger *zap.Logger, observers ...PanicObserver) gin.HandlerFunc {
    return gin.CustomRecovery(func(c *gin.Context, recovered any) {
        logger.Error("HTTP handler panic", zap.Any("panic", recovered), zap.Stack("stack"))
        httputil.WriteError(c, httputil.NewError(http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil))
        for _, ob := range observers {
            ob.OnPanic(c)
        }
    })
}
```

`internal/httpmiddleware` 只定义接口、不导入 audit 包，方向仍为 `app → httpmiddleware → httputil`，无反向依赖。由 `internal/app` 注入实现（`audithttp` 提供 `PanicObserver`），避免 `httpmiddleware → audit/http` 的非法依赖。回调触发时 `c.Writer.Status()` 已为 500（实测确认）。⚠️ `Recovery` 签名从单参变为可变参，调用方 `internal/app/router.go:60` 与 `httpmiddleware` 测试需同步更新（可变参兼容既有单参调用，但显式传参处应一并审阅）。

panic 请求的审计结果：状态 500 → `StatusFailed`，`error_msg` 使用安全摘要（如 `INTERNAL_ERROR`），不得写入 panic 原文或堆栈。

**body 超限的两条路径**（明确 PRD 的「路由执行前拒绝不纳入」）：

| 路径 | 触发点 | 是否审计 |
| --- | --- | --- |
| `ContentLength` 预检 | `MaxBodyLimitWithLimitAndCode`，在路由 Handler 之前 | 不审计（请求未进入已声明审计意图的 Handler） |
| `http.MaxBytesReader` 溢出 | `ContentLength` 为 -1（chunked）时穿透预检，在 `ShouldBindJSON` 阶段返回 `MaxBytesError` | 审计（Tag 已声明，属绑定失败） |

后者的 `MaxBytesError` 经 `httputil.HandleBindError` 转成 `PAYLOAD_TOO_LARGE` 并走 `WriteError`，因此适配层可通过安全错误元数据得到失败信号，无需特例处理。该路径只对已 Tag 的操作生效，未 Tag 请求仍然跳过。

`httputil.WriteError` 已统一处理业务和绑定错误，但不会填充 `c.Errors`。为让适配层得到可审计的错误信息，HTTP 错误写入器在 Gin Context 保存其已脱敏的 `Code` 与对外 `Message`，并通过 `httputil` accessor 读取；`internal/httputil` 不依赖 audit 包。审计优先使用该安全元数据；无安全元数据时写通用摘要，不写原始错误。

应用路由装配：

- 将审计中间件装到 API 公共组，使 Auth login/init/logout 与其他 API 写操作都被包装；未 Tag 的只读请求自动跳过。
- 独立 upload 组也挂载同一中间件，覆盖 Algorithm install。
- `newRouter` 在 `gin.New()` 后立即 `SetTrustedProxies(nil)`（见第 2 节），确保审计 IP 来自 TCP 连接来源。
- Auth 的受保护路径认证中间件负责在业务执行前更新标准 Context。
- 通过 `RouterParams` 注入 `*audit.Service`；`audit/http.Handler` 仍注册为受保护路由。
- `newRouter` 将 `audithttp` 提供的 panic 观察者传入 `httpmiddleware.Recovery(p.Logger, observer)`，使 panic 请求也能产生 failed 审计（见上文 panic 路径）。

## 4. 操作覆盖与明细策略

| 模块 | Tag 操作 |
| --- | --- |
| Auth | init、login、logout |
| Camera | create、update、delete、toggle |
| Algorithm | install、enable、disable、reset、delete、task create、task delete |
| Person | create、update、delete、face add、face delete |
| SystemTime | config update、NTP sync、manual set |
| Audit | clear 由专用同步路径记录，不再由异步 Middleware 重复记录 |

Tag 明细只能使用显式白名单字段。Auth init/login 不序列化请求体；人员/人脸操作不记录图像、特征原文或敏感路径。错误摘要使用 `httputil` 的对外安全信息；领域失败使用固定安全错误码/摘要。`formatDetail` 对不可序列化值返回安全占位或空详情，不影响业务请求。

⚠️ **不使用自动 diff（`RecordDiff` 类机制）**。本项目的敏感字段以 `json:"-"` 标记，但 `json:"-"` 只影响 JSON 序列化，**不阻止反射遍历**：

```go
// internal/camera/model.go:53
EncryptedURI   []byte `gorm:"column:encrypted_uri;not null" json:"-"`
// internal/person/model.go:47
FeatureVector  []byte `gorm:"column:feature_vector;not null" json:"-"`
// internal/person/model.go:46
ImagePath      string `gorm:"column:image_path;not null;default:''" json:"imagePath"`
```

基于反射的自动 diff 会读到 `EncryptedURI`（摄像机凭据密文）与 `FeatureVector`（人脸特征原文），一旦落库即为永久泄露（`MaxEntries=5000` 且无保留期限，不会自然淘汰）。且本项目为单账号系统，审计首要回答「来自哪个 IP」而非「旧值是什么」，自动 diff 只增加泄露面、不增加信息量。因此明细一律显式白名单构造。

Camera 是唯一需要**运行时改写 action** 的模块（update 与 toggle 共用路由）：先静态 `TagMiddleware(audit.ActionCameraUpdate, "camera:"+id, "")`，绑定成功后若判定为 toggle 则 `TagAction(audit.ActionCameraToggle)`，并用 `TagDetail(updateAuditDetail(req))` 补充摘要。

## 5. Service 与系统事件

**硬约束（重申）**：Service 层不得调用审计 API、不得持有 `*gin.Context`、不得导入 `internal/audit` 或 `internal/audit/http`。下表所有迁移均为此约束的落地。

- `Person Service` 删除 Auditor 参数、字段、`recordAudit`/`failAudit`；相关 Handler 负责 HTTP Tag。
- `SystemTime Service` 删除配置更新、手动操作等用户请求审计。保留本地窄接口 `SystemEventRecorder`（仅 `RecordSystem`），供后台周期 NTP、RTC 启动校时事件使用；`SyncNow` 的用户请求结果由 Handler 根据返回状态调用 `TagError`，避免误标成功和重复记账。
- `Storage Service` 熔断停机/恢复回调改用 `RecordSystem`；本任务不迁移 Storage 用户操作的现有记录。
- Camera、Algorithm Handler 移除 Auditor；Algorithm 所有纳入范围的写操作均声明 Tag。

## 6. 清空日志的持久化保证

新增专用 `ClearWithAudit(ctx, filter, entry)` 路径，不通过异步 `RecordEvent` 实现：

1. 从请求 Context 取得 Actor/IP，并构造 `audit.clear` Entry。
2. 在同一数据库事务中删除符合过滤条件的普通记录（`action != 'audit.clear'`），再插入本次 `audit.clear` 记录。
3. 只有事务提交后才返回清理成功；事务失败时不返回成功响应。

保留通用 `Clear` 供内部使用，但公开 DELETE Handler 必须使用 `ClearWithAudit`。测试需验证首次与连续清空后所有 `audit.clear` 记录仍存在，且日志含正确请求 IP 和删除数量；用户名为辅助字段。数据库不可用时不能承诺写入审计；此时接口必须返回失败，不能静默报告清空成功。

⚠️ 与 `Prune` 的冲突：现有 `Store.Prune` 按 `created_at ASC, id ASC` 无差别删除最旧记录，不区分 action。当总量超过 `MaxEntries`（默认 5000）时，最旧的 `audit.clear` 记录会被裁掉，导致「再次清空仍保留此前 `audit.clear`」的验收项在长时间运行后失败。因此 `Prune` 的删除子查询必须排除 `audit.clear`：

```sql
DELETE FROM audit_logs WHERE id IN (
  SELECT id FROM audit_logs WHERE action != 'audit.clear'
  ORDER BY created_at ASC, id ASC LIMIT ?
)
```

该改动只调整 Prune 的候选集，不改 Schema、不改 API，也不影响本任务范围外的既有行为语义（`audit.clear` 是唯一被豁免的 action）。

## 7. 兼容与风险

- 不改 `audit_logs` Schema、审计查询 DTO 或 SystemTime HTTP 响应格式。
- `Record(Entry)` 保留；迁移后的所有新 HTTP 路径显式提供来源 IP，或在无法取得时留空并使用 `unknown` 用户名；系统事件使用 `system` 用户名和空 IP，不依赖 SQLite/GORM 的 `admin` 默认值。
- 异步普通审计仍可能因队列满而丢弃，这是现有服务策略；只有 `ClearWithAudit` 使用同步事务作为强保证路径。
- Gin 适配与 HTTP Handler 移到子包会影响 app Fx 装配和审计 Handler 测试，需在迁移步骤中一并更新；包名 `audithttp` 同样影响导入别名与 swag 定义名。
- Swagger 生成（已实测验证）：`Makefile` 的 `api-docs` 用 `swag init --dir <目录列表>`，而 swag 内部通过 `filepath.Walk` 扫描目录，**会递归子目录**（swag v1.16.6 `parser.go` 的 `getAllGoFileInfo`）。因此新增 `internal/audit/http` **不需要**修改 `--dir` 列表。
- 同理已实测：通用（general API info）注释只从 `searchDirs[0]` 的 `mainAPIFile` 读取（`ParseAPIMultiSearchDir`），子包里的 `// @title` / `// @version` / `// @BasePath` 不会污染生成结果。迁移时仍建议不保留这类注释，但这是整洁性而非正确性要求。
- `internal/apidocs/docs.go` 由 `make api-docs` 生成（spec 禁止手改）；包移动后需重跑生成，定义名会从 `audit.AuditLogDTO` 变为 `audithttp.AuditLogDTO`，前端不依赖该定义名，属可接受变化。
- 可信代理策略改为显式关闭，属于全局 Router 行为变更；由于当前无反向代理，`ClientIP()` 的实际取值不变（仍是 TCP 对端），但不再接受请求头伪造。
- `Prune` 排除 `audit.clear` 后，极端情况下（`audit.clear` 累积到超过 `MaxEntries`）清理记录总数可能略超上限，属于可接受的取舍：审计自证的完整性优先于严格的容量上限。
- Storage 用户操作审计（本任务已修正的部分）：原先 Handler 用 `c.GetString("username")` 取账号名，但全库无对应的 `c.Set("username", ...)`，该表达式恒为空，于是每次都回退为 `admin`——对**每一条**用户操作伪造身份，比缺失更糟。现改为 `h.currentActor(c)` 从标准请求 Context 读取 `auth.RequireAuth` 写入的 `audit.Actor`，未认证时返回 `audit.UsernameAnonymous`，IP 始终取 `c.ClientIP()`。选择 `audit.FromContext` 而非 `auth.GetCurrentUser`，以避免 `storage ➔ auth` 依赖。
- 遗留（本任务范围外）：Storage 用户操作审计仍由 Service 内用 `s.auditor.Record(audit.Entry{...})` 直接记录（而非 Handler 层 Tag），且动作名仍为 `system.storage.*` 前缀——语义上它们是人为操作。动作重命名会影响库中已有记录的查询与前端 `AuditTable.tsx` 的筛选列表，应单开子任务并配套历史数据兼容策略。
- 规范同步（实现完成后执行，非本任务实现步骤）：`.trellis/spec/backend/directory-structure.md` 的目录树与依赖方向图需补上 `audit/http` 子包，使规范与代码一致；该文件当前明确标注「已实现部分必须与代码保持一致」。
- 连带修改（超出原 PRD 字面范围，属实现必需）：`internal/httpmiddleware.Recovery` 增加可变参 `PanicObserver`，`internal/app/router.go:60` 调用点与 `httpmiddleware` 测试同步更新。不改变 `Recovery` 最外层位置，不引入反向依赖；`internal/httpmiddleware` 只定义接口，`audithttp` 提供实现。
- `TagAction` 为新增 API（原设计只有 `TagTarget`/`TagDetail`/`TagActor`），用于 Camera update/toggle 共路由场景；无此 API 时 toggle 无法与 update 区分。
- panic 审计的固有局限：`PanicObserver` 只能覆盖被 `Recovery` 捕获的 handler panic；进程级崩溃（如 `os.Exit`、CGO 段错误、OOM kill）无法产生审计记录，属可接受边界。
