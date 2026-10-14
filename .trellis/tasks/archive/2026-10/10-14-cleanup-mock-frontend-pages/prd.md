# 清理未实现功能的Mock前端页面

## Goal

彻底清理当前 Web 前端系统中残留的来自脚手架模板（Shadcn Admin）且无对应后端支持的 Mock 页面、路由及导航入口。消除电商/第三方假数据与无效链接，精简并对齐 Zhulong 边缘智能运维宿主的真实业务能力（摄像头、实时流、人员底库、审计日志、网络、时间、存储、系统外观），确保类型检查、路由树生成、构建与测试全部平稳通过。

## Requirements

### 1. 彻底清理无对应后端支撑的独立 Mock 模块与路由
- **下线业务目录与路由**：
  - 任务看板：删除 `web/src/features/tasks/` 及 `web/src/routes/_authenticated/tasks/index.tsx`
  - 应用中心：删除 `web/src/features/apps/` 及 `web/src/routes/_authenticated/apps/index.tsx`
  - 消息聊天：删除 `web/src/features/chats/` 及 `web/src/routes/_authenticated/chats/index.tsx`
  - 用户管理：删除 `web/src/features/users/` 及 `web/src/routes/_authenticated/users/index.tsx`（当前系统为单机 root 管理员模式，无多用户 CRUD）
  - 帮助中心：删除 `web/src/features/help-center/` 及 `web/src/routes/_authenticated/help-center/index.tsx`
  - 第三方鉴权：删除 `web/src/routes/clerk/` 全量目录，清理 `@clerk/react` 相关资产与依赖
- **下线非边缘场景的认证衍生页面**：
  - 删除 `web/src/routes/(auth)/sign-in-2.tsx` (双栏登录 demo)
  - 删除 `web/src/routes/(auth)/sign-up.tsx` (公网注册)
  - 删除 `web/src/routes/(auth)/forgot-password.tsx` (找回密码)
  - 删除 `web/src/routes/(auth)/otp.tsx` (OTP 验证)
  - 同步删除 `web/src/features/auth/` 下对应的 `sign-in-2.tsx`、`sign-up/`、`forgot-password/`、`otp/` 目录与测试

### 2. 重构与精简设置中心 (`/settings/*`)
- **移除无后端对应表单**：
  - 删除 `Profile`（个人社交/简介）、`Account`（生日/脱节语言表单）、`Notifications`（邮件订阅）、`Display`（界面展示选项）对应的 feature 代码与路由：
    - `web/src/features/settings/profile/`
    - `web/src/features/settings/account/`
    - `web/src/features/settings/notifications/`
    - `web/src/features/settings/display/`
    - `web/src/routes/_authenticated/settings/account.tsx`
    - `web/src/routes/_authenticated/settings/notifications.tsx`
    - `web/src/routes/_authenticated/settings/display.tsx`
- **重定向与收敛**：
  - `/settings` 根路由 (`web/src/routes/_authenticated/settings/index.tsx`) 自动重定向至首个可用设置项 `/settings/appearance`。
  - 设置中心侧边栏 (`web/src/features/settings/index.tsx` 及 `sidebar-nav.tsx`) 仅展示实际功能：
    - `Appearance` (外观主题切换)
    - `Network` (网络接口与看门狗配置)
    - `Time` (系统时间与 NTP)
    - `Storage` (存储分区与生命周期清理策略)

### 3. 全局导航与侧边栏数据瘦身
- 更新 `web/src/components/layout/data/sidebar-data.ts`：
  - 移除全部已废弃的二级与一级 Mock 菜单项（Tasks、Apps、Chats、Users、Clerk、Pages -> Auth/Errors、Help Center）。
  - 保留并收敛为两大导航分组：
    1. **核心业务 (General)**:
       - 首页仪表盘 (`/`, `LayoutDashboard`)
       - 摄像头管理 (`/cameras`, `Video`)
       - 人员与底库 (`/persons`, `UserCheck`)
       - 实时监控画廊 (`/live`, `Monitor`)
       - 审计日志 (`/audit`, `ScrollText`)
    2. **系统设置 (System)**:
       - 包含 Appearance、Network、Time、Storage 四大子项。
  - 清理模板假数据：移除 `satnaingdev@gmail.com` 及 Acme 团队配置，代之以边缘设备宿主（Zhulong Edge）语义。

### 4. 头部与用户操作栏（NavUser / ProfileDropdown）去 Mock 化
- 移除 `Upgrade to Pro`、`Billing`、`New Team` 等 SaaS 模板残留。
- 仅保留符合边缘单机运维的必要功能：用户信息显示、系统设置跳转、退出登录。

### 5. 首页仪表盘 (`/`) 电商 Mock 数据清理
- 移除 Total Revenue、Subscriptions、Sales、Recent Sales (Olivia Martin 等) 电商假数据组件与展示。
- 将 Dashboard 改造为清晰、美观的边缘宿主状态概览页（展示系统核心功能入口与状态概况卡片）。

### 6. 命令搜索与测试适配
- 调整 `web/src/context/search-provider.test.tsx` 中的导航测试用例，将原先针对 `Tasks` 和 `Settings Account` 的测试更新为现有真实路径（如 `Cameras` 和 `Settings Network`）。
- 确保重新生成的 `web/src/routeTree.gen.ts` 准确反映所有路由变更。

## Acceptance Criteria

- [x] 1. 业务与路由文件清理：`tasks`、`apps`、`chats`、`users`、`clerk`、`help-center` 以及 auth 衍生页 (`sign-in-2`, `sign-up`, `forgot-password`, `otp`) 和 settings mock 页 (`profile`, `account`, `notifications`, `display`) 全部移除。
- [x] 2. 侧边栏及导航：侧边栏、设置侧栏仅包含系统真实存在的有效页面与入口，无任何点击报 404 或展示脱节假数据的死链接。
- [x] 3. 首页仪表盘：首页 `/` 不再包含任何电商销售额及假客户假订单数据。
- [x] 4. 路由树同步：重新生成 `routeTree.gen.ts`，无残留悬挂路由，路由强类型匹配无报错。
- [x] 5. 质量保证：`pnpm build`（TypeScript 编译 + Vite 生产打包）成功；`pnpm lint` 静态分析 0 error；受影响的测试用例全部绿灯通过。
