# 技术设计：清理未实现功能的Mock前端页面

## 1. 现状与问题分析

Zhulong 项目前端基于 `satnaing/shadcn-admin` 模板搭建。模板初始携带了大量 SaaS 演示模块（如电商销售面板、即时通讯、应用商店、任务看板、多用户体系、外部 Clerk 鉴权、找回密码与多栏登录）。
然而 Zhulong 的系统定位是**本地优先的边缘运维宿主（单机二进制 + 嵌入式 SPA + 边缘流媒体与算法处理）**。这些未接后端的假页面存在以下危害：
1. **混淆用户预期**：误导用户以为存在相关后端能力（如聊天、多用户注册、云端应用集成）。
2. **潜在报错风险**：部分页面操作可能触发未捕获请求或在后续维护中产生死链接。
3. **增加编译与维护负担**：增加打包体积和维护成本，阻碍类型分析与死代码检测。

## 2. 变更范围与拓扑重塑

### 2.1 路由层级对比

```
重构前 routes/                             重构后 routes/
├── (auth)/                                ├── (auth)/
│   ├── forgot-password.tsx  [DELETE]      │   └── sign-in.tsx            [KEEP]
│   ├── otp.tsx              [DELETE]      ├── (errors)/                  [KEEP]
│   ├── sign-in-2.tsx        [DELETE]      ├── _authenticated/
│   ├── sign-in.tsx          [KEEP]        │   ├── audit/index.tsx        [KEEP]
│   └── sign-up.tsx          [DELETE]      │   ├── cameras/index.tsx      [KEEP]
├── clerk/                   [DELETE ALL]  │   ├── live/index.tsx         [KEEP]
├── (errors)/                [KEEP]        │   ├── persons/index.tsx      [KEEP]
└── _authenticated/                        │   ├── index.tsx (Dashboard)  [REFACTOR]
    ├── apps/                [DELETE]      │   ├── settings/
    ├── chats/               [DELETE]      │   │   ├── appearance.tsx     [KEEP]
    ├── help-center/         [DELETE]      │   │   ├── network.tsx        [KEEP]
    ├── tasks/               [DELETE]      │   │   ├── storage.tsx        [KEEP]
    ├── users/               [DELETE]      │   │   ├── time.tsx           [KEEP]
    ├── audit/               [KEEP]        │   │   ├── index.tsx          [REDIRECT]
    ├── cameras/             [KEEP]        │   │   └── route.tsx          [KEEP]
    ├── live/                [KEEP]        │   ├── errors/                [KEEP]
    ├── persons/             [KEEP]        │   └── route.tsx              [KEEP]
    ├── index.tsx            [REFACTOR]    └── __root.tsx                 [KEEP]
    └── settings/
        ├── account.tsx      [DELETE]
        ├── display.tsx      [DELETE]
        ├── notifications.tsx[DELETE]
        ├── appearance.tsx   [KEEP]
        ├── network.tsx      [KEEP]
        ├── storage.tsx      [KEEP]
        ├── time.tsx         [KEEP]
        ├── index.tsx        [REDIRECT]
        └── route.tsx        [KEEP]
```

### 2.2 业务 Feature 目录清理

直接删除 `web/src/features/` 下对应的无用目录：
- `web/src/features/tasks/`
- `web/src/features/apps/`
- `web/src/features/chats/`
- `web/src/features/users/`
- `web/src/features/help-center/`
- `web/src/features/auth/forgot-password/`
- `web/src/features/auth/otp/`
- `web/src/features/auth/sign-up/`
- `web/src/features/auth/sign-in/sign-in-2.tsx`
- `web/src/features/settings/profile/`
- `web/src/features/settings/account/`
- `web/src/features/settings/notifications/`
- `web/src/features/settings/display/`

### 2.3 设置中心 `/settings` 路由收敛设计

原 `/settings` 路由渲染 `features/settings/profile` 表单。删除 profile 后：
- 在 `web/src/routes/_authenticated/settings/index.tsx` 中使用 TanStack Router 的 `redirect`：
  ```ts
  export const Route = createFileRoute('/_authenticated/settings/')({
    beforeLoad: () => {
      throw redirect({
        to: '/settings/appearance',
        replace: true,
      })
    },
  })
  ```
- 设置侧边栏导航（`features/settings/index.tsx` 和 `sidebar-nav.tsx`）剔除 Profile、Account、Notifications、Display，仅包含：
  1. `Appearance` (外观设置，主题、字体)
  2. `Network` (网络配置与看门狗)
  3. `Time` (系统时间与时区)
  4. `Storage` (存储空间与生命周期清理)

### 2.4 全局侧边栏（AppSidebar）与品牌标识

- **侧边栏分组**：
  - `General`:
    - `Dashboard` (`/`)
    - `Cameras` (`/cameras`)
    - `Persons` (`/persons`)
    - `Live View` (`/live`)
    - `Audit Logs` (`/audit`)
  - `System`:
    - `Settings` (可折叠或直接链接至 `/settings/appearance`)
- **品牌与多团队下拉替换**：
  - 将 `TeamSwitcher` 中的 Acme 模板数据替换为统一的设备标识：
    - 设备名：`Zhulong Edge`
    - 副标：`边缘运维宿主`
- **用户信息与下拉菜单清理**：
  - `NavUser` 与 `ProfileDropdown`：删除 `Upgrade to Pro`、`Billing`、`New Team`，保留统一的 `Settings`（指向 `/settings/appearance`）和 `Sign out`。

### 2.5 首页 Dashboard 概览重塑

- 移除原先与边缘运维无关的电商卡片（`Total Revenue`, `Recent Sales` 等）。
- 改造为纯净的边缘系统状态卡片：
  - 核心模块快捷导航（摄像头、视频监控、人员底库、系统网络、存储健康）。
  - 设备运行状态指示（保留美观卡片风格，采用 OKLCH 语义色，支持深浅双模）。

### 2.6 测试用例与类型树适配

- `web/src/context/search-provider.test.tsx` 中原先依赖 `Tasks` 和 `Settings Account` 的文本定位测试，改为断言现存的 `Cameras` 和 `Settings Network`。
- 执行路由树自动生成与编译验证。

## 3. 风险与缓解对策

| 风险点 | 影响度 | 缓解对策 |
| :--- | :--- | :--- |
| 删除路由导致 `routeTree.gen.ts` 类型错乱 | 高 | 删除文件后通过 Vite / TSR 重新生成 `routeTree.gen.ts`，并以 `pnpm build` 进行全量类型校验 |
| 其他组件隐式导入被删 feature | 中 | 采用全仓 grep 确认无遗留引用；运行 `pnpm lint` 发现未解析导入 |
| 测试因依赖被删菜单项报错 | 中 | 提前定位 `search-provider.test.tsx` 等依赖导航的测试，一并迁移至有效业务模块 |
