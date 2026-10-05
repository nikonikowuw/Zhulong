# 单用户登录与初始化模块实施计划 (implement.md)

## 实施步骤与有序检查清单

### Phase 1: 后端存储与数据库迁移

- [x] **1.1 新增 SQLite 迁移文件**
  - 创建 `internal/database/migrations/000002_create_users_table.up.sql`（仅 `users` 表）。
  - 创建 `internal/database/migrations/000002_create_users_table.down.sql`。
  - 验证：运行现有 `database_test.go` 确保迁移自动应用成功。

### Phase 2: 后端认证核心模块 (`internal/auth`)

- [x] **2.1 实体模型与请求/响应 DTO**
  - 抽离 `internal/database/base_model.go`（`BaseModel` 提供 `int64 ID` 与 UTC 时间戳）。
  - 实现 `internal/auth/user.go`：嵌入 `database.BaseModel` 的 GORM 单用户数据库实体模型。
  - 实现 `internal/auth/requests.go`：入参 DTO（`InitAdminRequest`, `LoginRequest`）及 Gin binding 校验规则。
  - 实现 `internal/auth/responses.go`：出参 DTO（`AuthStatusResponse`, `UserResponse`）。
- [x] **2.2 持久化与内存会话**
  - 实现 `internal/auth/store.go`：`UserStore` 接口与 SQLite GORM 实现。
  - 实现 `internal/auth/session.go`：`MemorySessionStore`（纯内存、CSPRNG 32 字节随机 Token、`RWMutex` 保护、自动 TTL 回收）。
  - 验证：编写 `internal/auth/store_test.go` 与 `internal/auth/session_test.go`。
- [x] **2.3 业务服务与防爆破限流**
  - 实现 `internal/auth/service.go`：`AuthService` 接口与实现（bcrypt 散列、密码校验、初始化锁、内存滑动窗口限流器）。
  - 验证：编写 `internal/auth/service_test.go`（覆盖成功/失败密码比对、限流封锁与并发测试）。
- [x] **2.4 HTTP 处理器与鉴权中间件**
  - 实现 `internal/auth/middleware.go`：`RequireAuth` 中间件与类型安全的 `CurrentUser` 上下文存取器。
  - 实现 `internal/auth/handler.go`：`status`, `init`, `login`, `logout`, `me` 处理函数与 `RegisterRoutes`。
  - 验证：编写 `internal/auth/handler_test.go` 覆盖完整的 HTTP 端点流转与 401 拦截。

### Phase 3: 后端主应用装配与国际化

- [x] **3.1 扩展全局错误码字典**
  - 在 `internal/httputil/locale.go` 中补充 `UNAUTHORIZED`、`INVALID_CREDENTIALS`、`SYSTEM_ALREADY_INITIALIZED`、`SYSTEM_NOT_INITIALIZED`、`TOO_MANY_ATTEMPTS`、`PASSWORD_MISMATCH` 等本地化消息。
- [x] **3.2 装配至 `internal/app`**
  - 将 `auth` 模块集成到 Fx 依赖图与 Gin 路由引擎。
  - 验证：执行 `go test -v ./...` 确保所有后端单元测试 100% 通过。

### Phase 4: 前端网络层与国际化适配

- [x] **4.1 调整基础 HTTP 客户端**
  - 修改 `web/src/shared/api/client.ts`：请求开启 `credentials: "include"`，增加全局 401 状态监听通知机制。
  - 验证：运行 `npm test` 或 `pnpm test` 保证现有测试用例不被破坏。
- [x] **4.2 完善三语语言包**
  - 在 `web/src/shared/i18n/locales/` 的 `zh-Hans.json`, `zh-Hant.json`, `en.json` 中添加认证相关的文案（登录、初始化、登出、输入校验等）。

### Phase 5: 前端认证功能切片 (`features/auth`)

- [x] **5.1 封装 API 与上下文**
  - 编写 `web/src/features/auth/api/authApi.ts` 与 `types.ts`。
  - 编写 `web/src/features/auth/context/`（`authContext.ts`, `AuthProvider.tsx`, `index.ts`）与 Hook `useAuth.ts`。
- [x] **5.2 编写 UI 门禁与表单组件**
  - 编写 `InitForm.tsx`：管理员初始化卡片（带密码强度与二次确认校验）。
  - 编写 `LoginForm.tsx`：登录卡片（带用户名密码输入、回车提交、错误提示）。
  - 编写 `AuthGuard.tsx`：根据 `AuthContext` 状态决定展示加载态、InitForm、LoginForm 还是子应用。
- [x] **5.3 改造主界面顶栏与根入口**
  - 在主导航栏添加当前登录用户信息与「退出登录」按钮。
  - 在 `App.tsx` 中接入 `AuthGuard`。
  - 验证：为 `LoginForm`、`InitForm` 与 `AuthGuard` 编写 React 单元测试。

### Phase 6: 全链路回归与质量检验

- [x] **6.1 后端质量门禁**
  - 执行 `go test -race ./...` 确保无并发竞态与回归。
  - 执行 `make go-check` 确保符合代码规范与编译检查。
- [x] **6.2 前端质量门禁**
  - 在 `web/` 执行 `pnpm run lint` 零错误警告。
  - 在 `web/` 执行 `pnpm test` 确保 20 个测试用例全部通过。
  - 在 `web/` 执行 `pnpm build` 确保 TypeScript 类型与生产打包成功。

---

## 验证清单 (Validation Commands)

```bash
# 1. 验证后端全部测试（含竞态检测）
go test -race -v ./...

# 2. 验证前端单元测试
cd web && pnpm test --run

# 3. 验证前端构建
cd web && pnpm build
```

## 回滚策略 (Rollback Points)

- 数据库变更：如遇异常，执行 `000002_create_users_table.down.sql` 回滚迁移版本。
- 代码隔离：`internal/auth` 与 `web/src/features/auth` 均采用特性切片隔离，若发生异常可独立重构而不影响现有的 `camera` 或 `systemStatus`。
