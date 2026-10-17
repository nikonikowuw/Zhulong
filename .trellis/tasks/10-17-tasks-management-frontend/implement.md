# Implementation Plan: 通道多算法分析任务管理 (Analysis Tasks Frontend)

## 实施阶段与步骤清单

### 阶段 1：数据模型、API 客户端与单元测试
- [x] 1.1 数据 Schema 定义：`web/src/features/tasks/data/schema.ts`
  - 定义 `taskSchema`、`taskListResponseSchema`、`taskFormSchema`、`Task`、`ChannelTaskGroup` 等类型。
- [x] 1.2 RESTful API 客户端：`web/src/features/tasks/api/tasks-api.ts`
  - 实现 `getTasks`、`getTask`、`createTask`、`updateTask`、`deleteTask`。
- [x] 1.3 API 单元测试：`web/src/features/tasks/api/tasks-api.test.ts`
  - 验证命令：`cd web && pnpm test -- src/features/tasks/api/tasks-api.test.ts`

### 阶段 2：状态管理、通道聚合与差量同步 Hook
- [x] 2.1 基础 React Query 钩子：`web/src/features/tasks/hooks/use-tasks.ts`
  - 查询、单任务启停、单任务更新与删除，附带全局缓存失效。
- [x] 2.2 全局算力预算派生钩子：`web/src/features/tasks/hooks/use-task-compute-budget.ts`
  - 纯计算派生：计算所有 enabled 任务的累计算力占用与百分比。
- [x] 2.3 商业级通道聚合 Hook：`web/src/features/tasks/hooks/use-channel-tasks.ts`
  - 聚合 `Camera` ⇄ 多 `Algorithm` 映射，计算单通道算力与活跃状态。
- [x] 2.4 通道多算法差量批量同步 Hook：`web/src/features/tasks/hooks/use-sync-channel-algorithms.ts`
  - 计算 draft 与 existing 的差量，执行 POST/PUT/DELETE 批量事务并捕获错误。
- [x] 2.5 编写通道聚合与算力计算单测：`web/src/features/tasks/hooks/use-channel-tasks.test.ts`
  - 验证命令：`cd web && pnpm test -- src/features/tasks/hooks/use-channel-tasks.test.ts`

### 阶段 3：多语言字典准备 (i18n)
- [x] 3.1 编写三语本地化字典：
  - `web/src/features/tasks/locales/zh-Hans.json`
  - `web/src/features/tasks/locales/zh-Hant.json`
  - `web/src/features/tasks/locales/en.json`
- [x] 3.2 在 `web/src/lib/i18n.ts` 中注册 `tasks` 命名空间。

### 阶段 4：核心 UI 组件构建
- [x] 4.1 状态 Badge：`web/src/features/tasks/components/task-status-badge.tsx`
  - 严格采用 Dot-Indicator Outline 范式与成对暗黑适配。
- [x] 4.2 算力预算看板：`web/src/features/tasks/components/tasks-compute-budget-card.tsx`
  - 1,000 点动态进度条与多维统计。
- [x] 4.3 商业级通道卡片视图：`web/src/features/tasks/components/channel-tasks-view.tsx`
  - 呈现摄像头卡片，内嵌多算法徽标、通道算力、快速配置入口。
- [x] 4.4 扁平任务明细表格：`web/src/features/tasks/components/tasks-table.tsx`
  - 传统排查表格，带 URL 同步与行内 Switch。
- [x] 4.5 通道多算法统一配置弹窗：`web/src/features/tasks/components/channel-task-dialog.tsx`
  - 算法多选复选列表、按需展开 FPS 滑块、实时通道与平台算力预估、差量保存。
- [x] 4.6 任务删除确认弹窗：`web/src/features/tasks/components/task-delete-dialog.tsx`
- [x] 4.7 页面主入口：`web/src/features/tasks/index.tsx`
  - 视图切换（通道卡片 ⇋ 任务表格）、Header、弹窗调度。

### 阶段 5：路由与侧边栏导航集成
- [x] 5.1 创建 TanStack Router 路由文件：`web/src/routes/_authenticated/tasks/index.tsx`
- [x] 5.2 触发路由插件生成 `web/src/routeTree.gen.ts`
- [x] 5.3 在 `web/src/components/layout/data/sidebar-data.ts` 中挂载 `/tasks`。

### 阶段 6：UI 组件测试与全局质量门禁
- [x] 6.1 编写组件与视图测试：
  - `web/src/features/tasks/components/channel-tasks-view.test.tsx`
  - `web/src/features/tasks/components/task-status-badge.test.tsx`
- [x] 6.2 运行前端测试与类型校验：
  - `cd web && pnpm test`
  - `cd web && pnpm run build`
  - `cd web && pnpm lint`
- [x] 6.3 运行全项目质量门禁：
  - `make check`

---

## 验证与门禁命令 (Review Gates)

```bash
# 1. 运行任务模块专门测试
cd web && pnpm test -- src/features/tasks/

# 2. 类型检查与前端编译
cd web && pnpm run build

# 3. 前端代码规范检查
cd web && pnpm lint

# 4. 全局项目质量门禁
make check
```

## 回滚策略 (Rollback Points)
如果页面或路由出现异常：
1. 移除 `web/src/routes/_authenticated/tasks/index.tsx` 并还原 `routeTree.gen.ts`；
2. 还原 `web/src/components/layout/data/sidebar-data.ts` 与 `web/src/lib/i18n.ts`；
3. 不影响后端任何持久化与底层服务。
