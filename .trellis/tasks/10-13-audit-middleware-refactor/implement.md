# 执行计划：审计日志模块重构

## 目标

按 PRD 建立 Gin 无关的审计核心、独立 Gin 适配层、完整写操作覆盖，并以客户端 IP 作为单用户系统的人为操作主要归因；同时实现安全错误传播和强制留存的清空自审计。不改数据库 Schema 或 SystemTime HTTP 响应格式。

## Step 1：核心审计服务与身份语义

- [ ] 1.1 在 `internal/audit` 实现 `Actor`、`WithActor`、`FromContext`。
- [ ] 1.2 实现 `RecordEvent`、`RecordSystem`；无 Actor 与空 Entry 用户名记录为 `unknown`，系统身份只由 `RecordSystem` 显式提供。
- [ ] 1.3 实现安全明细格式化；序列化失败不能影响业务调用。
- [ ] 1.4 保留 `Record` 与 `RecordSync` 兼容性；Source 不进入持久化模型。
- [ ] 1.5 添加核心服务测试：Context 往返、未知 Actor、系统 Actor 的空 IP、HTTP Actor IP 优先保留、成功/失败状态、安全明细和异步投递。
- **验证**：`go test -race ./internal/audit/...`

## Step 2：拆分 Gin 适配包与错误元数据

- [ ] 2.1 新建 `internal/audit/http`（包名 `audithttp`，避免与 `net/http` 撞名），迁移 Gin 审计 Handler，并实现 Middleware 与 Tag API；核心 `internal/audit` 不导入 Gin。`Makefile` 的 `api-docs` 无需改动（swag 递归扫描目录）。
- [ ] 2.2 增加 `TagMiddleware(action, target, detail)`、`TagAction(action)`、`TagError(code, safeMessage)`；TagActor 同步更新 Gin Tag 与 `Request.Context()`。`TagAction` 用于绑定后才能确定 action 的场景（Camera update/toggle）。
- [ ] 2.3 在 `httputil.WriteError` 保存已脱敏错误码/对外消息，并提供 Context accessor；审计适配器不得把原始 `c.Errors` 或内部错误直接落库。
- [ ] 2.4 为 2xx 领域失败提供显式审计结果通道；SystemTime `SyncNow` 根据 `SyncStatus.State == failed` 标记失败，同时保持现有 HTTP 响应。
- [ ] 2.5 测试 2xx、4xx、5xx、JSON 绑定错误、安全错误消息、TagError、无 Tag 跳过、TagTarget/TagDetail/TagAction/TagActor 与竞态安全；额外覆盖 chunked 请求穿透 `ContentLength` 预检后被 `MaxBytesReader` 拒绝的路径（已 Tag 则记录 failed，未 Tag 则跳过）。
- [ ] 2.6 为 `internal/httpmiddleware.Recovery` 增加可变参 `PanicObserver`（只定义接口，不导入 audit）；实现 panic 审计测试，断言 panic 请求产生 `StatusFailed` 且 `error_msg` 为安全摘要，同时保持 `Recovery` 最外层不违反 middleware 规范。
- [ ] 2.7 回归验证 `c.Next()` 之后与 `defer` 两种写法在 panic 下均不可用（前者不执行、后者读到 200），确保实现未回退到这两种写法。
- **验证**：`go test -race ./internal/audit/... ./internal/audit/http/... ./internal/httputil/...`
- **边界检查**：`go list -f '{{join .Imports "\n"}}' ./internal/audit` 输出不得包含 Gin。

## Step 3：清空日志强制自审计

- [ ] 3.1 在 Store/Service 增加 `ClearWithAudit`，在一个 DB 事务中删除非 `audit.clear` 记录并插入本次成功事件。
- [ ] 3.2 `Handler.ClearLogs` 使用 `ClearWithAudit`，从标准 Context 读取 Actor/IP，不再依赖异步 Middleware 生成唯一清空记录。
- [ ] 3.3 `Store.Prune` 的删除子查询增加 `WHERE action != 'audit.clear'`，保证清理自审计记录不被容量裁剪吃掉（否则与「再次清空仍保留此前 `audit.clear`」验收冲突）。
- [ ] 3.4 测试首次清空、带 `before` 清空、连续清空、删除计数、请求 IP，以及数据库事务失败时不得返回成功。
- [ ] 3.5 测试 Prune 与 `audit.clear` 的共存：写入超过 `MaxEntries` 的普通记录并保留至少一条 `audit.clear`，触发 Prune 后断言 `audit.clear` 仍存在。
- **验证**：`go test -race -run 'TestClearLogs|TestPrune' ./internal/audit/... ./internal/audit/http/...`

## Step 4：应用路由与 Fx 装配

- [ ] 4.1 将 `*audit.Service` 注入 Router；审计 Middleware 覆盖 API 公共组和独立 upload 组，未 Tag 请求不产生审计记录。
- [ ] 4.1a 将 `audithttp` 提供的 panic 观察者传入 `httpmiddleware.Recovery(p.Logger, observer)`，并同步更新 `httpmiddleware` 测试。
- [ ] 4.2 在 `newRouter` 的 `gin.New()` 之后调用 `router.SetTrustedProxies(nil)`：本部署无反向代理，gin v1.12.0 默认信任 `0.0.0.0/0` 与 `::/0` 的转发头，必须显式关闭；失败时记录 warn 日志。
- [ ] 4.3 Auth `RequireAuth` 在下游 Handler/Service 执行前设置请求来源 IP 和辅助 Actor 字段；无反向代理时无需为登录限流单独改用 `RemoteAddr`。
- [ ] 4.4 更新 `provideAudit`、RouterParams、审计 Handler 注册（导入别名 `audithttp`）与相关 Fx 测试。
- [ ] 4.5 添加真实 Router 集成测试：公共 login/init（含 JSON 绑定失败）、受保护 logout、普通受保护写操作、Algorithm upload；验证每条人为操作记录的 IP、唯一配置账号名（认证成功时）与路由覆盖。
- [ ] 4.6 添加转发头忽略回归测试：伪造 `X-Forwarded-For` / `X-Real-IP` 请求头，断言审计记录的 IP 为 TCP 来源地址（httptest 的 RemoteAddr），而非请求头值。
- **验证**：`go test -race ./internal/app/... ./internal/auth/... ./internal/audit/... ./internal/audit/http/...`

## Step 5：迁移业务 Handler 与后台事件

- [ ] 5.1 Auth：在 `HandleJSON` 前用 `TagMiddleware` 声明 init/login；解析成功后更新目标，成功后记录唯一配置账号名；logout 使用 Tag；失败登录记录 anonymous 和来源 IP；不得硬编码 `admin`，明细不包含密码或确认密码。
- [ ] 5.2 Camera：Create/Update/Delete 使用 Tag；Update/Toggle 共用路由场景先用静态 `TagMiddleware` 声明 update，绑定后用 `TagAction` 改写为 toggle 并 `TagDetail` 补充摘要；移除 Handler Auditor 与 `recordAudit`。
- [ ] 5.3 Algorithm：Install/Enable/Disable/Reset/Delete/Task Create/Delete 使用 Tag；Install 覆盖独立 upload 路由及早期失败路径；移除 Handler Auditor。
- [ ] 5.4 Person：Create/Update/Delete/AddFace/DeleteFace 使用 Tag；Service 删除 Auditor 与审计样板；更新构造函数、Fx 和单测。
- [ ] 5.5 SystemTime：UpdateConfig/SyncNow/ManualSet 使用 Tag；SyncNow 检查领域同步状态；Service 删除用户操作审计，后台 NTP/RTC 仅通过窄 `SystemEventRecorder.RecordSystem` 记录。
- [ ] 5.6 Storage：紧急停机/恢复回调改用 `RecordSystem`；保留其余既有用户操作记录。
- [ ] 5.7 各 Handler 测试验证完整写操作清单、失败路径、明细脱敏和无重复记录。
- **验证**：`go test -race ./internal/auth/... ./internal/camera/... ./internal/algorithm/... ./internal/person/... ./internal/systemtime/... ./internal/storage/...`

## Step 6：全量质量门禁

- [ ] 6.1 检查核心包无 Gin 依赖、Auth 与 Gin 适配无 import cycle、API 与 upload 路由均覆盖。
- [ ] 6.2 运行全量测试与服务器构建。
- [ ] 6.3 对照 PRD 写操作矩阵逐项检查无漏项、无重复项；执行敏感字段审计回归测试。
- [ ] 6.4 检查 Prune 豁免 `audit.clear`、`SetTrustedProxies(nil)` 与包名 `audithttp` 三项决策均已落地且被测试覆盖。
- [ ] 6.4a 检查硬约束：Service 层无 `internal/audit` / `internal/audit/http` 导入、无 `*gin.Context` 参数（`go list` + grep 双向验证）。
- [ ] 6.4b 检查明细白名单：审计明细测试证明未出现 `EncryptedURI` / `FeatureVector` / `ImagePath` / 密码 / 令牌内容（验证未引入自动 diff）。
- [ ] 6.5 重跑 `make api-docs`，确认 `/audit/logs` 路由与 DTO 定义仍在 `internal/apidocs/docs.go` 中。
- **验证**：`go test -race ./...`
- **验证**：`go build -o /dev/null ./cmd/Zhulong`
- **验证**：`go list ./internal/audit/... ./internal/auth ./internal/app`

## 回滚点

- 核心 API 与 Gin 适配拆包分步提交，必要时可分别回滚。
- 业务模块按包迁移；保持 `Record(Entry)` 兼容，未迁移调用方可临时继续使用。
- 清空事务实现独立测试；若事务实现未通过强留存测试，不启用新的 DELETE 路径。

## 启动前检查

- [ ] 用户审阅 PRD、设计与本实施计划。
- [ ] `implement.jsonl` 与 `check.jsonl` 保持有效且包含对应后端规范。
- [ ] 用户确认进入实现阶段后，才运行 `task.py start`。
