# React 前端开发规范总览

> 基于 `satnaing/shadcn-admin` 脚手架（React 19 + Vite 8 + Tailwind CSS v4 + TanStack Router / Query / Table + Radix UI）的前端架构规范、所有者 12 条编码铁律、主题/国际化契约及质量门禁。

---

## 1. 规范索引

| 规范指南 | 核心内容 |
| --- | --- |
| [目录架构](./directory-structure.md) | `shadcn-admin` 拓扑、TanStack Router 文件路由、`features/` 业务切片、`components/` 分层与定制组件维护 |
| [跨语言命名规范](../naming-guidelines.md) | 文件名、React 组件、Hook、函数、变量和领域术语 |
| [编码铁律与开发指南](./development-guidelines.md) | 所有者 12 条编码铁律、TanStack Query 服务端状态、TanStack Table URL 驱动、表单/弹窗契约、流媒体监控 UX |
| [主题与三语国际化契约](./theme-and-i18n.md) | Tailwind v4 + OKLCH 语义主题、Light/Dark/System 模式、Font/Direction (RTL) 支持与英/简/繁三语契约 |
| [质量检验与测试规范](./quality-guidelines.md) | Vitest + Playwright 浏览器测试、TypeScript 强类型与零 any、Knip 死代码检测、Vite 构建与 Go 嵌入对接 |

---

## 2. 核心技术栈矩阵

| 领域 | 选型与版本 | 职责与规范要求 |
| :--- | :--- | :--- |
| **基础底座** | React 19 (`^19.2.5`) + Vite 8 (`^8.0.8`) | 采用现代化 React 运行时，支持模块自动分割与极速 HMR |
| **路由系统** | `@tanstack/react-router` (`^1.168.22`) | 采用基于文件系统的强类型路由（`src/routes/`），由 Vite 插件自动生成 `routeTree.gen.ts` |
| **样式体系** | Tailwind CSS v4 (`^4.2.2`) + `tw-animate-css` | 使用 `@import 'tailwindcss';` 与 `styles/theme.css` 的 OKLCH 语义设计变量，支持 `@custom-variant dark` |
| **组件库** | `shadcn/ui` (Radix UI 原语 + Lucide Icons) | 原子组件位于 `components/ui/`，部分组件具备 RTL 增强（禁止随意通过 CLI 暴力覆盖） |
| **服务端状态** | `@tanstack/react-query` (`^5.99.0`) | 100% 接管 API 数据缓存、自动重试、按需失效，严禁将接口数据同步至本地全局状态 |
| **数据密集表格**| `@tanstack/react-table` (`^8.21.3`) | 数据表格核心引擎，配合 `hooks/use-table-url-state.ts` 实现分页、排序、过滤与 URL Search 同步 |
| **表单与校验** | `react-hook-form` (`^7.72.1`) + `zod` (`^4.3.6`) | 强类型模式表单验证，配合 `components/ui/form` 提供无障碍错误提示 |
| **客户端状态** | `zustand` (`^5.0.12`) | 仅用于跨页面纯客户端状态（如 `auth-store` 用户鉴权会话凭证） |
| **上下文机制** | Theme / Layout / Font / Direction / Search | 提供深浅模式切换、折叠侧边栏、字体动态配置、LTR/RTL 文本方向与全局 Command 搜索 |
| **质量与测试** | Vitest (`^4.1.4`) + `@vitest/browser-playwright` | 在真实 Chromium 无头浏览器环境中运行组件交互测试；结合 ESLint 10 与 Knip |

---

## 3. 开发前检查清单 (Pre-Development Checklist)

- [ ] **路由文件对齐**：新增页面是否在 `src/routes/` 正确创建文件，且仅做 Loader/参数校验与视图包装？
- [ ] **业务逻辑切片**：核心业务组件、数据契约、私有 Hook 是否共置于 `src/features/<module>/`？
- [ ] **纯粹渲染与抽 Hook**：组件内是否仅保留声明式 UI 渲染？复杂请求与状态派生是否抽至自定义 Hook？
- [ ] **严格零 `any`**：Props、函数参数与返回值是否强类型定义？不可信输入是否经 Zod 校验？
- [ ] **服务端状态独占**：API 数据是否完全由 TanStack Query 托管？严禁复制同步到 Zustand 等客户端 Store！
- [ ] **表格 URL 状态同步**：后台列表的分页、排序、筛选参数是否通过 `useTableUrlState` 同步至 URL？
- [ ] **表单与弹窗统一契约**：表单是否使用 `react-hook-form` + Zod？弹窗开关是否使用 `use-dialog-state`？
- [ ] **OKLCH 语义色与逻辑类**：样式是否遵循 `theme.css` 语义 Token（严禁写死 HEX 色）？外/内边距是否使用逻辑类（`ps-*`, `pe-*`）以支持 RTL？
- [ ] **异步三态完备性**：所有异步交互是否完备处理了 **Loading（骨架屏/进度条）**、**Error（错误提示/重试）** 与 **Empty（空数据引导）**？
- [ ] **全文三语国际化**：所有可见文本是否包裹 `t(...)`？严禁在 JSX 中硬编码中英文字符串！

---

## 4. 质量验证基线

```bash
# 在 web/ 根目录下执行
pnpm lint            # ESLint 10 + TypeScript 静态分析 (含禁止 any 校验)
pnpm format:check    # Prettier 代码格式检查
pnpm knip            # Knip 依赖与无用导出/死代码分析
pnpm build           # tsc -b 强类型编译 + Vite 生产构建打包
pnpm test            # Vitest 浏览器无头模式测试 (--browser.headless via Playwright)
```
