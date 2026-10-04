# 单用户登录与初始化模块 PRD

## Goal

为 Zhulong 系统构建轻量、安全的前后端一体单用户身份认证体系。在系统首次运行无账号时提供友好的 Web 初始化引导流程，并在后续运行中提供密码哈希校验、纯内存轻量会话保持、API 鉴权与防爆破安全防护。

---

## Confirmed Facts & Technical Constraints

1. **凭据存储模式**：
   - 采用 SQLite 数据库持久化单用户凭据（仅 `users` 表，单用户记录）。
   - 首次启动若检测到无任何用户记录，系统进入未初始化状态（`initialized = false`）。
2. **会话机制（简单优先，杜绝过度设计）**：
   - 会话状态纯**内存管理**（In-Memory Session Store），零磁盘 I/O，不写 SQLite，保护嵌入式 Flash 介质寿命。
   - 基于 32 字节 CSPRNG 安全随机 Token，下发 `HttpOnly; SameSite=Lax; Path=/` Cookie。
   - 服务重启会话自然失效，要求重新登录（符合工控/设备类系统的安全常识）。
3. **后端技术基准**：
   - Go + Gin 框架，标准错误与响应封装在 `internal/httputil`。
   - 密码哈希：必须采用 `golang.org/x/crypto/bcrypt` 单向加密，哈希成本设定为适中标准（`bcrypt.DefaultCost`）。
   - 语言环境：遵循三语国际化（`en`, `zh-Hans`, `zh-Hant`）。
4. **前端技术基准**：
   - React + TypeScript + Tailwind CSS，使用 `@/` 路径别名。
   - 全局响应式状态管理（Auth Context / Hook），自动管理初始化检查、登录态检查与 401 全局失效拦截。
5. **安全防线**：
   - 杜绝明文存储密码，密码校验使用恒定时间比较。
   - 接口防爆破：在内存层限制 IP 错误尝试频次（如 5 次错误锁定 15 分钟）。
   - 初始化接口安全防劫持：`POST /api/v1/auth/init` 仅在系统未初始化（无用户）时开放，一旦初始化成功后立即硬性拦截并返回 403 Forbidden。

---

## User Flow & Requirements

### Flow 1: 首次启动与初始化 (Setup Flow)
1. 客户端访问 WebUI 时，先请求 `GET /api/v1/auth/status`。
2. 若返回 `{ "initialized": false }`，前端自动呈现「系统首次初始化 / 设置管理员」卡片。
3. 用户输入管理员用户名（默认推荐 `admin`，允许自定义）与初始密码（至少 8 位），并确认密码。
4. 前端调用 `POST /api/v1/auth/init`。
5. 后端校验密码长度与系统初始化状态；校验通过后将管理员凭据写入 SQLite，同时在内存建立会话并下发 Cookie。
6. 前端收到成功响应后，自动更新全局认证态为已登录，无缝进入系统主界面。

### Flow 2: 常规登录 (Login Flow)
1. 客户端访问 WebUI 时，`GET /api/v1/auth/status` 返回 `{ "initialized": true }`。
2. 前端请求 `GET /api/v1/auth/me` 尝试复用 Cookie 恢复会话：
   - 会话有效：直接进入系统主页。
   - 会话无效或未登录：展示「系统登录」界面。
3. 用户输入用户名和密码，点击登录，前端调用 `POST /api/v1/auth/login`。
4. 后端核对用户名并使用 `bcrypt.CompareHashAndPassword` 校验密码；若匹配成功，在内存创建 Session，刷新/下发 Session Cookie 并返回用户信息。
5. 若密码错误，后端记录失败计数，超过上限则触发限流；返回本地化的错误提示。

### Flow 3: 注销退出 (Logout Flow)
1. 用户在顶部栏点击「退出登录」。
2. 前端调用 `POST /api/v1/auth/logout`。
3. 后端使内存中的 Session 失效并清除响应 Cookie (`Max-Age: -1`)。
4. 前端清空本地用户状态，切换回登录界面。

### Flow 4: 会话失效与 401 拦截
1. 任何受保护 API 返回 HTTP 401 (`UNAUTHORIZED`) 时，前端网络层捕获该响应。
2. 触发全局退出事件，清空当前用户信息，并弹出登录失效提示，引导重新登录。

---

## API Contract (REST @ `/api/v1/auth`)

| 路径 | 方法 | 鉴权要求 | 描述 |
| :--- | :--- | :--- | :--- |
| `/api/v1/auth/status` | `GET` | 公开 | 查询系统初始化状态 (`initialized: boolean`) |
| `/api/v1/auth/init` | `POST` | 仅未初始化开放 | 首次创建单用户管理员并直接建立会话 |
| `/api/v1/auth/login` | `POST` | 公开 (限流) | 用户名密码登录并在内存建立会话、下发 Cookie |
| `/api/v1/auth/logout` | `POST` | 需已登录 | 销毁内存会话并清除 Cookie |
| `/api/v1/auth/me` | `GET` | 需已登录 | 获取当前已认证用户信息 |

所有受保护业务路由统一挂载 `RequireAuth` 中间件。

---

## Out of Scope
- 多用户并发管理、用户角色分配 (RBAC)。
- 数据库 `sessions` 表（采用更轻量的纯内存会话存储）。
- 邮箱找回密码、短信验证码等外部通信链路。
- 密码过期间隔强制轮换。

---

## Acceptance Criteria

- [x] **数据库迁移**：SQLite 成功执行迁移脚本，只建立 `users` 表存储单用户凭据，不产生冗余会话表。
- [x] **内存会话机制**：基于 Go 内存管理 Session，零数据库 I/O 开销，支持超时自动清理。
- [x] **系统初始化控制**：未建账号时 `status` 为 `false`，仅允许调用 `init`；一旦建有账号，`init` 永久返回 403 冲突状态。
- [x] **安全认证机制**：密码经 bcrypt 哈希存储，登录端点具备密码错误防爆破限流能力。
- [x] **Cookie 会话管理**：Session Cookie 配置 `HttpOnly` 和 `SameSite=Lax`，登出能正常使内存会话作废。
- [x] **后端认证中间件**：未登录访问受保护业务接口时，一律被中间件拦截并返回标准 401 JSON 错误。
- [x] **前端初始化向导**：系统未初始化时呈现清晰美观的向导表单，输入合法性校验无误后能一键初始化并登入。
- [x] **前端登录与登出**：登录表单交互流畅，支持 Enter 提交与错误提示；登出按钮操作简明。
- [x] **401 全局拦截**：会话过期或被踢出时，前端统一捕获 401 并切回登录状态。
- [x] **国际化覆盖**：登录、初始化界面的文本与后端返回的认证错误消息均支持三语（中文简体、繁体、英文）。
- [x] **质量校验**：Go 核心模块单元测试通过，前端 lint / build 验证通过。
