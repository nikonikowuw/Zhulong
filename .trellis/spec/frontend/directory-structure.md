# React 前端目录架构规范

> 基于 `satnaing/shadcn-admin` 脚手架的目录拓扑、TanStack Router 文件路由边界、特性切片（Feature Slices）与组件分层契约。

---

## 1. 真实工程目录拓扑 (Repository Layout)

```text
web/
  src/
    assets/                           # 品牌图标、SVG 与插画资产
    components/                       # 全局通用组件体系
      data-table/                     # TanStack Table 表格构件 (toolbar, pagination, column-header, bulk-actions 等)
      layout/                         # 控制台框架布局构件 (app-sidebar, header, main, nav-group, nav-user, top-nav 等)
        data/sidebar-data.ts          # 导航菜单与分组结构定义
      ui/                             # shadcn/ui 原子组件 (button, dialog, input, sheet, sidebar, table 等)
      confirm-dialog.tsx              # 通用操作二次确认模态窗
      password-input.tsx              # 密码显示/隐藏专用输入框
      command-menu.tsx                # 全局 Ctrl+K 快捷命令栏
    config/                           # 字体与全局配置项 (fonts.ts 等)
    context/                          # 全局 UI 状态上下文 (theme-provider, layout-provider, font-provider, direction-provider, search-provider)
    features/                         # 业务领域切片 (Feature Slices)
      <feature-name>/                 # 业务功能模块 (如 camera, live, playback, users, settings 等)
        components/                   # 模块私有 UI 组件
        data/                         # 模块 mock 数据、字段 schema 定义
        hooks/                        # 模块专属 Hooks (封装 TanStack Query 与数据派生)
        api/                          # 强类型 REST 请求函数与 Zod DTO
        types.ts                      # 模块私有类型定义
        index.tsx                     # 模块主入口视图 / 公共门面组件
      errors/                         # 401, 403, 404, 500, 503 等标准错误视图
    hooks/                            # 通用自定义 Hooks (use-mobile, use-dialog-state, use-table-url-state)
    lib/                              # 基础通用工具库 (utils.ts, cookies.ts, handle-server-error.ts 等)
    routes/                           # TanStack Router 文件系统路由层
      __root.tsx                      # 根路由 (注入 QueryClient, Toaster, Devtools 等)
      _authenticated/                 # 鉴权路由组 (挂载 AuthenticatedLayout 框架)
        route.tsx                     # 鉴权布局注入点
        index.tsx                     # 控制台仪表盘首页 (/)
        <feature>/                    # 业务路由入口 (如 tasks/index.tsx, users/index.tsx)
      (auth)/                         # 认证独立路由组 (sign-in.tsx, sign-up.tsx, otp.tsx 等)
      (errors)/                       # 错误页面路由组 (401.tsx, 404.tsx, 500.tsx 等)
    stores/                           # Zustand 客户端状态 (auth-store.ts 等)
    styles/                           # 样式定义 (index.css, theme.css - 基于 Tailwind CSS v4 & OKLCH)
    test-utils/                       # 单元/组件测试通用工具库
    routeTree.gen.ts                  # ⚠️ TanStack Router 自动生成的路由树定义 (严禁手动修改)
    main.tsx                          # 应用根挂载入口
```

---

## 2. 路由层与特性层分离契约 (Route vs Feature Boundary)

为保证代码高可维护性与测试独立性，路由层与业务特性层必须遵循**极简接入契约**：

1. **`src/routes/` 仅做薄封装 (Thin Route)**：
   - 职责限于：URL 路径声明、路由级鉴权守卫（`beforeLoad`）、Search Params 强类型校验（`validateSearch`）与挂载 Feature 主视图；
   - **严禁**在 `routes/` 文件中直接书写百行以上的复杂表单、接口请求或内联 UI 细节。

   ```typescript
   // src/routes/_authenticated/camera/index.tsx
   import { createFileRoute } from '@tanstack/react-router'
   import { z } from 'zod'
   import { CameraPage } from '@/features/camera'

   const cameraSearchSchema = z.object({
     page: z.number().catch(1),
     pageSize: z.number().catch(10),
     search: z.string().optional(),
   })

   export const Route = createFileRoute('/_authenticated/camera/')({
     validateSearch: (search) => cameraSearchSchema.parse(search),
     component: CameraPage,
   })
   ```

2. **`src/features/<feature>/` 承载核心业务**：
   - 所有的业务交互、表格组装、弹窗控制、TanStack Query 逻辑全部闭环在 `features/<feature>` 内部；
   - 模块对外通过 `index.tsx` 暴露清晰的页面入口组件（如 `CameraPage`）或跨模块共享组件。

---

## 3. UI 原子组件保护铁律 (`components/ui/`)

`satnaing/shadcn-admin` 脚手架针对 **RTL（从右至左语言支持）** 与 **后台控制台密集交互** 对原生 shadcn/ui 组件进行了增强与深度微调：

- **受保护的定制组件**：`sidebar`, `dialog`, `sheet`, `table`, `select`, `dropdown-menu`, `command`, `alert-dialog`, `calendar`, `switch`, `scroll-area`, `sonner`, `separator` 等。
- ⚠️ **禁止随意覆盖更新**：严禁执行 `npx shadcn@latest add <component> --overwrite` 机械覆盖已有组件！
- 若因上游缺陷确需升级某一原子组件，必须先比对 Git Diff，将原组件中的 RTL 适配（如 `dir` 属性处理、位置判定）和样式微调合并回新版中。

---

## 4. 模块导入与别名铁律

1. **统一使用 `@/` 别名**：在 `tsconfig.json` 与 `vite.config.ts` 中配置 `@/` 指向 `src/`。严禁出现两级以上的相对路径导入（如 `../../../../components/ui/button`）。
2. **跨模块公共导出边界**：
   - 跨业务特性引用必须通过对应特性的门面入口：
     ```typescript
     import { CameraCard, type Camera } from '@/features/camera' // ✅ 正确
     // ❌ 严禁穿透导入私有路径: '@/features/camera/components/CameraCard'
     ```
3. **禁止内部反向循环引用**：
   - 特性内部文件相互引用时使用相对路径（如 `./components/CameraCard`），**严禁**在内部通过 `from './index'` 或 `from '@/features/camera'` 反向导入自身，防止打包时产生循环依赖（Circular Dependency）。
