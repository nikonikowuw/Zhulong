# 执行计划：轻量级系统操作与安全审计日志模块 (Implementation Plan)

## 任务拆解与执行顺序

### 阶段一：数据库迁移与后端核心模块 (internal/audit)
- [ ] **1.1 编写 SQLite 迁移脚本**
  - 创建 `internal/database/migrations/000004_create_audit_logs_table.up.sql`
  - 创建 `internal/database/migrations/000004_create_audit_logs_table.down.sql`
  - 验证方式：`python3 native/scripts/build.py go test ./internal/database/...`
- [ ] **1.2 实现 Audit 数据模型与 Store**
  - 编写 `internal/audit/model.go`（GORM 模型、查询参数、分页响应结构）
  - 编写 `internal/audit/store.go`（支持 Create、List 分页与过滤、Prune 容量淘汰、Clear 清空）
  - 编写 `internal/audit/store_test.go`（验证 CRUD、分页以及淘汰逻辑）
  - 验证方式：`python3 native/scripts/build.py go test ./internal/audit/... -run TestStore`
- [ ] **1.3 实现 AuditService 异步写入通道与生命周期管理**
  - 编写 `internal/audit/service.go`（含带缓冲 channel、后台 worker、批量写入、优雅退出 Drain）
  - 编写 `internal/audit/service_test.go`（验证并发写入、非阻塞性与停机保存）
  - 验证方式：`python3 native/scripts/build.py go test ./internal/audit/... -run TestService`
- [ ] **1.4 实现 HTTP API 与路由集成**
  - 编写 `internal/audit/handler.go`（GET /api/v1/audit/logs, DELETE /api/v1/audit/logs）
  - 编写 `internal/audit/handler_test.go`
  - 验证方式：`python3 native/scripts/build.py go test ./internal/audit/... -run TestHandler`

### 阶段二：系统装配与业务模块埋点
- [ ] **2.1 Uber Fx 模块装配**
  - 在 `internal/app/app.go` 中注册 `audit.Service`、`audit.Handler`，并将 `audit.Handler` 挂载至 `ProtectedRoutes`。
  - 注册生命周期钩子以启动与关闭 audit worker。
- [ ] **2.2 认证模块埋点 (internal/auth)**
  - 在 `auth.Handler` 中埋点：`auth.init`、`auth.login`（成功/失败及客户端 IP）、`auth.logout`。
  - 完善/更新相关测试。
- [ ] **2.3 摄像头管理埋点 (internal/camera)**
  - 在 `camera.Handler` / `camera.Service` 中埋点：`camera.create`、`camera.update`、`camera.delete`、`camera.toggle`。
  - 验证方式：`python3 native/scripts/build.py go test ./internal/app/... ./internal/auth/... ./internal/camera/...`

### 阶段三：前端审计特性模块与界面开发 (web/src/features/audit)
- [ ] **3.1 客户端 API 与类型定义**
  - 创建 `web/src/features/audit/types.ts`（Zod 模式校验与 TS 类型定义）
  - 创建 `web/src/features/audit/api/auditApi.ts`（GET /api/v1/audit/logs 与 DELETE /api/v1/audit/logs）
- [ ] **3.2 国际化词条扩充**
  - 在 `web/src/features/audit/locales/` 以及全局各语言包中添加中英双语词条（动作类型友好名称、表格列、筛选提示、确认弹窗）
- [ ] **3.3 组件开发**
  - 编写 `AuditLogFilterBar.tsx`（动作、状态筛选与刷新控制）
  - 编写 `AuditLogTable.tsx`（结构化表格、Badge 状态、时间格式化、分页控制）
  - 编写 `AuditLogDetailModal.tsx`（JSON 明细展开查看）
  - 编写 `AuditLogPage.tsx`（页面入口与数据加载挂载）
- [ ] **3.4 全局导航与路由挂载**
  - 更新 `web/src/shared/components/layout/Sidebar.tsx` 添加 `audit` 导航菜单项
  - 更新 `web/src/App.tsx` 增加 `#audit` 哈希路由支持与视图切换
  - 验证方式：`npm run type-check --prefix web && npm test --prefix web`

### 阶段四：端到端质量验证与收尾
- [ ] **4.1 全栈静态检查与测试套件**
  - 运行 `python3 native/scripts/build.py go test -race ./cmd/... ./internal/...`
  - 运行 `npm run lint --prefix web && npm run type-check --prefix web && npm test --prefix web`
- [ ] **4.2 编译与产物验证**
  - 执行 `make build` 或 `python3 native/scripts/build.py go build ./cmd/Zhulong` 验证二进制链接与资源打包无误。

## 回滚与风险控制 (Rollback Strategy)
- 数据库变更：提供无损的 `down.sql` 迁移，若审计功能需要降级，可安全执行 `migrate down` 且不影响用户和摄像头主表。
- 异步隔离：所有业务埋点操作均采用非阻塞投递，审计系统的任何异常/满载都不会波及音视频流与用户登录主逻辑。
