# 重构审计日志模块：声明式中间件与上下文闭环

## Goal

为单账号系统的状态变更建立一致、可追溯且不泄露秘密的审计记录；审计首要回答“请求来自哪个 IP”。系统只有一个可配置登录账号，用户名可作为辅助信息保留，但不需要用它区分多个操作者，也不应硬编码为 `admin`。

## Scope and Requirements

### 1. 核心审计契约

- 定义通用 `Actor`（`IP`、`Username`、`Source`），并提供 `WithActor`、`FromContext`；HTTP 请求的来源 IP 是主要归因字段。认证成功时可记录系统唯一配置账号的登录名；单账号场景不要求用用户名区分不同操作者，也不把用户名硬编码为 `admin`。
- 审计 API 仅由 Handler 层调用：Service 层不得持有 `*gin.Context`、不得导入审计包。用户操作的审计声明归属 HTTP Handler；后台事件的 Service 例外仅限仅含 `RecordSystem` 的窄接口。
- 审计明细一律显式白名单构造，**不使用基于反射的自动 diff**：`json:"-"` 只阻止 JSON 序列化，不阻止反射遍历，自动 diff 会把摄像机凭据密文与人脸特征原文写入审计表。
- 保留低层异步 `Record(Entry)`；增加 `RecordEvent(ctx, action, target, detail, err)` 与 `RecordSystem(action, target, detail, err)`。
- `RecordSystem` 明确使用 `Username: system`、`Source: system`、`IP: ""`；后台事件没有客户端 IP，不得伪造成 `127.0.0.1`。
- 缺少 Actor 的通用事件不得伪装为 `system` 或 `admin`；记录为 `unknown`。只有显式调用 `RecordSystem` 才表示系统事件。
- 本任务不改审计表结构；`Actor.Source` 在本版本用于上下文语义，不写入数据库或 API DTO。
- IP 是网络来源线索，不等同于个人身份；NAT、动态地址或共享代理可能使多个用户呈现相同 IP。本部署无反向代理，IP 直接取自 TCP 连接来源；该归因成立的前提是 HTTP 服务实际对外监听（而非仅绑定 `127.0.0.1`）。

### 2. 纳入审计的 HTTP 操作

仅审计下列状态变更操作；只读查询、健康检查、Swagger、静态资源不纳入本任务。

| 模块 | 纳入操作 |
| --- | --- |
| Auth | 初始化管理员、登录、登出 |
| Camera | 创建、更新、删除、启停切换 |
| Algorithm | 安装、启用、停用、重置、删除、创建分析任务、删除分析任务 |
| Person | 创建、更新、删除人员、添加人脸、删除人脸 |
| SystemTime | 更新配置、立即 NTP 同步、手动设置系统时间 |
| Audit | 清空日志 |

绑定失败或业务操作失败，只要该请求已声明审计意图，就必须留下失败记录。Auth init/login 必须在 `HandleJSON` 绑定请求体之前声明审计意图；其他纳入范围的 Handler 在参数校验/业务副作用前声明。被 API body-size 中间件的 `ContentLength` 预检在路由执行前拒绝的请求不纳入本任务；但 chunked 请求（无 Content-Length）穿透预检、在 JSON 绑定阶段被 `MaxBytesReader` 拒绝的情况属于绑定失败，已声明审计意图时必须记录失败。登录失败以 `anonymous` 为 Actor；尝试的用户名仅可作为目标标识，不得当作已认证身份。

### 3. HTTP Actor 与错误

- 请求 Actor 的 IP 必须从连接来源取得并传入标准请求 Context；用户名仅作为现有字段的辅助值，不能替代 IP，也不作为多用户身份验收条件。登录/初始化成功后可补充用户名。
- HTTP 审计记录真实连接来源 IP。本部署无反向代理，因此不信任 `X-Forwarded-For` 等转发头（需显式关闭 Gin 的默认转发头信任，见 design.md）。若将来引入可信代理，只有显式配置的可信代理才能提供转发客户端地址。
- HTTP 状态码 `>= 400` 表示失败；状态码为 2xx 但领域操作明确失败时，也必须记录失败。特别是立即 NTP 同步返回 `SyncStatus.State == failed` 时，审计结果为 failed，即使现有 HTTP 响应仍为 200；本任务不要求改变该响应契约。
- 被 Recovery 捕获的 handler panic 必须留下 failed 审计记录；`error_msg` 只用安全摘要（如 `INTERNAL_ERROR`），不得写入 panic 原文或堆栈。进程级崩溃（`os.Exit`、CGO 段错误、OOM kill）不在覆盖范围内。
- `error_msg` 仅使用安全的错误码/对外消息或显式提供的安全错误摘要。不得把原始 `c.Errors`、数据库错误或其他未脱敏内部错误直接写入审计记录。

### 4. 明细与隐私

- 明细采用字段白名单；不得序列化整个认证请求或其他未审查的请求结构体。
- 审计明细不得包含密码、确认密码、会话令牌、Cookie、上传文件内容、人脸图像/特征原文或底层内部错误。
- 目标 ID 和必要的非敏感变更摘要可以记录；序列化失败不得导致原业务请求失败。

### 5. 后台事件与 Service 边界

- Storage 紧急停机/恢复、后台 NTP 自动同步结果、启动时 RTC 校时结果使用 `RecordSystem`，其 IP 为空，表示没有客户端来源。
- Person Service 不再依赖审计器；用户操作审计归属 HTTP Handler。
- SystemTime Service 删除用户请求的审计副作用；为保留自动 NTP/RTC 后台事件，可依赖仅含 `RecordSystem` 的窄接口，不保留通用用户审计依赖。
- Storage 仅改造系统事件调用；其现有用户配置审计不在本任务重构范围内。

### 6. 清空日志

`DELETE /api/v1/audit/logs` 成功后必须在响应前持久化一条包含 Actor、IP、结果和删除数量的 `audit.clear` 记录。该端点的清理逻辑不得删除既有 `audit.clear` 记录；清理和本次自审计记录应作为一个持久化操作处理，避免依赖可丢弃的异步队列。容量裁剪（`Prune`）同样必须豁免 `audit.clear` 记录，否则「再次清空仍保留此前记录」在长时间运行后必然失败。

## Acceptance Criteria

- [x] 审计核心包不依赖 Gin；Gin Handler、中间件和 Tag API 位于独立 HTTP 适配包。
- [x] `WithActor`/`FromContext` 可用于普通 Go 调用；缺失 Actor 记录为 `unknown`，显式系统事件记录为 `system`，不发生默认 `admin` 误归因。
- [x] 所有表格列出的写操作均通过真实应用路由生成审计记录；公共 Auth、受保护路由和独立上传路由均覆盖；未打 Tag 的请求不记录。
- [x] 所有纳入范围的人为 HTTP 写操作都记录正确的客户端 IP（取自 TCP 连接来源，伪造 `X-Forwarded-For` / `X-Real-IP` 不生效）；认证成功时用户名为系统唯一配置账号名（若可取得），未认证请求使用 `anonymous`，不得硬编码或误归因为 `admin`。
- [x] 已有认证上下文可传递到 `Request.Context()`；登录失败不伪造已认证用户名，也不丢失请求 IP。
- [x] HTTP 错误记录安全且可判别；业务错误、校验错误、NTP 领域失败与被捕获的 panic 均被正确标为 failed，不将原始内部错误或 panic 堆栈写入 `error_msg`。
- [x] 审计详情测试证明密码、确认密码、Cookie/令牌、人脸原始数据与摄像机凭据密文不会落库（验证未引入自动 diff）。
- [x] Camera 的 update 与 toggle 能被区分记录（运行时改写 action）。
- [x] 后台 Storage、周期 NTP 和 RTC 校时记录为系统 Actor；SystemTime 的用户操作不再由 Service 重复记录。
- [x] 生产代码中不存在 `admin` 硬编码身份：任何归属不明的请求记录为 `anonymous` 或 `unknown`，Storage 用户操作记录真实认证账号名。
- [x] 成功清空后立即能查询到 `audit.clear`；再次清空仍保留此前及本次 `audit.clear` 记录，且容量裁剪不会删除 `audit.clear` 记录。
- [x] 数据库 Schema 与 API DTO 不变；全量后端测试通过。

## Out of Scope

- 只读 API 的访问审计、多用户身份归因、数据库迁移、审计日志签名/外部不可篡改存储。
- 自动 diff / 变更前后值对比（`RecordDiff` 类机制）：单账号系统无此需求，且与反射泄露风险相冲。
- 进程级崩溃（`os.Exit`、CGO 段错误、OOM kill）的审计：仅在 Recovery 可捕获的 panic 范围内。
- 外部审计后端（Kafka / ClickHouse / 集中式日志）：本项目为边缘单机，继续使用 SQLite `audit_logs`。
- Storage 用户配置/清理操作审计的**整体迁移**（即迁移到 Handler 层 Tag 并重命名 `system.storage.*` 动作）不在本任务范围。
  - ⚠️ 但「硬编码 `admin`」不属于可延期的遗留，已在本任务修正：原实现用 `c.GetString("username")`（全库无对应 `c.Set`，恒为空）再回退为 `admin`，会对**每条**用户操作伪造身份。现改为从请求 Context 读取 `auth.RequireAuth` 写入的真实 Actor，未认证时记为 `anonymous`。
  - 遗留部分仅剩：动作名仍为 `system.storage.*` 前缀、审计仍由 Service 内记录。动作重命名会影响历史记录查询与前端筛选列表，应单开子任务处理。
- 更改 SystemTime 同步接口的 HTTP 响应结构或状态码。
- 部署层监听地址变更：本任务不修改 `config.toml` 或 `http.address`；若设备仍仅绑定 `127.0.0.1`，IP 归因的区分能力受限属部署配置问题，需在验收时单独确认。
