# 轻量级系统操作与安全审计日志模块 (Lightweight Audit Log)

## Goal

为单用户边缘视频智能分析设备（烛龙 Zhulong）提供一套轻量级、低开销的全链路操作与安全审计机制。
在保障存储安全（防 Flash/eMMC 磨损与爆盘）的前提下，完整记录管理员认证、配置变更和系统关键事件，并在 Web 管理控制台中提供友好的历史溯源与排障界面。

## Scope

本次任务采用**全链路端到端交付（选项 A）**：
1. **Go 后端**：新增 `internal/audit` 模块（SQLite 迁移、GORM 模型与 Store、异步非阻塞写入、FIFO 自动淘汰、查询/清理 API、认证与摄像头管理业务埋点、单元测试与集成测试）。
2. **React 前端**：新增 `web/src/features/audit` 特性模块，并在全局侧边栏和主路由中集成「审计日志」视图（支持分页列表、多维筛选、详情抽屉/模态框、国际化多语言）。

## Target Scenarios

1. **管理员运维溯源**：在摄像头配置被修改、流异常中断或推理参数变更时，可快速在控制台回溯操作历史和参数前后详情。
2. **安全合规与异常检测**：记录管理员登录成功/失败、登出、密码修改及请求来源 IP，便于识别未授权访问或撞库探测。
3. **低资源占用与防爆盘**：面向嵌入式/边缘盒子运行环境，采用受控容量（如上限 5,000 条，超量自动淘汰最旧记录）和缓冲异步写入，不阻塞主业务。

## Requirements

### 1. 核心模型与存储
- 在 SQLite 中新增 `audit_logs` 表，通过 `golang-migrate` 版本化迁移（`000004_create_audit_logs_table.up.sql` / `.down.sql`）。
- 字段结构：
  - `id`: 主键，自增整数。
  - `created_at`: 时间戳 (UTC)。
  - `ip`: 客户端 IP 字符串。
  - `username`: 操作人标识（当前为单用户 admin，保留扩展性）。
  - `action`: 操作动作标识（例如 `auth.login`, `auth.logout`, `auth.init`, `camera.create`, `camera.update`, `camera.delete`, `camera.toggle`）。
  - `target`: 目标资源标识（例如 `camera:1`, `system`, `user:admin`）。
  - `detail`: JSON 格式详情或变更前后差异摘要。
  - `status`: 执行结果状态（`success` 或 `failed`）。
  - `error_msg`: 错误信息或失败原因摘要。
- **存储保护与自动淘汰 (FIFO)**：设置最大容量阈值（默认 5,000 条），写入触发或后台维护时自动清理最旧记录，防止 SQLite 文件无限增长。

### 2. 后端服务与路由 (Go)
- 新增 `internal/audit` 包，通过 Uber Fx 集成到主服务。
- 采用非阻塞异步缓冲写入（如带缓冲 channel 的 background worker），写日志失败不影响主业务流。
- 路由挂载至 `protected_routes`：
  - `GET /api/v1/audit/logs`：分页查询审计记录，支持按 `action`、`status`、时间范围 (`start_time`, `end_time`) 过滤，返回总数与列表。
  - `DELETE /api/v1/audit/logs`：支持管理员清空或按条件清理历史日志。
- 业务埋点接入：
  - **Auth 模块**：管理员初始化 (`auth.init`)、登录成功与登录失败 (`auth.login`)、登出 (`auth.logout`)。
  - **Camera 模块**：摄像头创建 (`camera.create`)、更新 (`camera.update`)、删除 (`camera.delete`)、启用/停用状态变更。

### 3. 前端交互与展示 (React)
- 新增 `features/audit` 目录结构：
  - `api/auditApi.ts`：对接审计 API。
  - `types.ts`：审计数据类型定义。
  - `components/AuditLogPage.tsx`：主页面容器。
  - `components/AuditLogTable.tsx`：日志表格，带状态 Badge、操作类型徽标、时间格式化。
  - `components/AuditLogFilterBar.tsx`：支持按动作类型和状态过滤、刷新与清理日志。
  - `components/AuditLogDetailModal.tsx`：查看完整参数与 detail JSON 详情。
- 导航与路由集成：
  - 更新 `Sidebar.tsx`，在导航项中新增「操作审计」入口（使用 `ScrollText` 或 `ClipboardList` 图标）。
  - 更新 `App.tsx`，支持 `#audit` 路由哈希切换并渲染页面。
- 国际化支持：
  - 在 `en.json`、`zh-Hans.json`、`zh-Hant.json` 中补齐审计模块的所有文本。

## Acceptance Criteria

- [ ] 数据库迁移：`000004_create_audit_logs_table.up.sql` 及 down 脚本正确就绪，SQLite 升级回滚验证通过。
- [ ] 异步写入与容量保护：在写入量达到阈值后，自动剔除最旧日志，数据库条数保持在安全限额内。
- [ ] 认证审计：登录成功、登录失败（记录原因）及登出均能准确捕获客户端 IP 并记录。
- [ ] 摄像头审计：摄像头的增、删、改能记录目标名称/ID 与关键修改参数。
- [ ] 后端测试覆盖：包含 Store 读写、容量淘汰、API Handler 及埋点调用的单元/集成测试。
- [ ] 前端界面体验：Web 控制台侧边栏能正常进入「操作审计」，可流畅进行分页、筛选、详情查看，三种语言文本完整。
