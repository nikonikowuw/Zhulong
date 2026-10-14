# 实施计划：清理未实现功能的Mock前端页面

## 执行步骤

### Step 1: 清理独立 Mock 业务 Feature 与路由
- [x] 删除以下路由文件：
  - `web/src/routes/_authenticated/tasks/index.tsx`
  - `web/src/routes/_authenticated/apps/index.tsx`
  - `web/src/routes/_authenticated/chats/index.tsx`
  - `web/src/routes/_authenticated/users/index.tsx`
  - `web/src/routes/_authenticated/help-center/index.tsx`
  - `web/src/routes/clerk/`（全量目录）
- [x] 删除以下业务 feature 目录及组件：
  - `web/src/features/tasks/`
  - `web/src/features/apps/`
  - `web/src/features/chats/`
  - `web/src/features/users/`
  - `web/src/features/help-center/`
  - `web/src/assets/clerk-logo.tsx` 与 `web/src/assets/clerk-full-logo.tsx`

### Step 2: 清理非边缘场景的认证衍生页面
- [x] 删除以下认证路由文件：
  - `web/src/routes/(auth)/sign-in-2.tsx`
  - `web/src/routes/(auth)/sign-up.tsx`
  - `web/src/routes/(auth)/forgot-password.tsx`
  - `web/src/routes/(auth)/otp.tsx`
- [x] 删除对应 feature 文件：
  - `web/src/features/auth/forgot-password/`
  - `web/src/features/auth/otp/`
  - `web/src/features/auth/sign-up/`
  - `web/src/features/auth/sign-in/sign-in-2.tsx`

### Step 3: 重构设置中心路由与导航
- [x] 删除设置中的 Mock 子项路由：
  - `web/src/routes/_authenticated/settings/account.tsx`
  - `web/src/routes/_authenticated/settings/notifications.tsx`
  - `web/src/routes/_authenticated/settings/display.tsx`
- [x] 删除设置中的 Mock 子项 feature：
  - `web/src/features/settings/profile/`
  - `web/src/features/settings/account/`
  - `web/src/features/settings/notifications/`
  - `web/src/features/settings/display/`
- [x] 修改 `web/src/routes/_authenticated/settings/index.tsx`：实现重定向至 `/settings/appearance`
- [x] 修改 `web/src/features/settings/index.tsx`：更新 `sidebarNavItems`，仅保留 Appearance、Network、Time、Storage

### Step 4: 清理全局侧边栏与头部操作栏
- [x] 更新 `web/src/components/layout/data/sidebar-data.ts`：
  - 侧边栏仅保留 `General` 业务（Dashboard、Cameras、Persons、Live View、Audit Logs）与 `System` 设置
  - 移除假用户信息 `satnaing` 和假团队切换数据，更替为 Zhulong 边缘设备品牌
- [x] 更新 `web/src/components/layout/nav-user.tsx` 与 `web/src/components/profile-dropdown.tsx`：
  - 移除 `Upgrade to Pro`、`Billing`、`New Team`、`Notifications`、`Account` 等脱节菜单项
  - 统一保留设置跳转与退出登录
- [x] 更新 `web/src/components/layout/team-switcher.tsx`：品牌展示调整为边缘宿主标识

### Step 5: 改造首页 Dashboard 概览
- [x] 清理 `web/src/features/dashboard/components/recent-sales.tsx`、`analytics.tsx`、`analytics-chart.tsx` 等电商假数据
- [x] 重构 `web/src/features/dashboard/index.tsx` 与 `overview.tsx`：呈现边缘智能设备基础概览（摄像头在线状态、监控入口、存储与网络健康状态）

### Step 6: 适配测试用例
- [x] 修改 `web/src/context/search-provider.test.tsx`：将原测试中的 `Tasks` 和 `Settings Account` 换为真实入口 `Cameras` 和 `Settings Network`

### Step 7: 路由树更新与质量门禁验证
- [x] 重新生成 `web/src/routeTree.gen.ts`
- [x] 运行 `pnpm build`（`tsc -b && vite build`），确保编译与打包零错误
- [x] 运行 `pnpm lint`，确保代码风格与无用 import 检查通过
- [x] 运行相关测试套件验证
