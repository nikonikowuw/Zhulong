# React 前端质量门禁与测试规范

> 基于 `satnaing/shadcn-admin` 的质量门禁命令、Vitest 浏览器测试体系、零 any 审计与 Go 二进制嵌入对接契约。

---

## 1. 质量检验基线与门禁命令

在 `web/` 目录下，使用 `pnpm` 执行以下质量门禁指令：

```bash
pnpm lint            # ESLint 10 + TypeScript 静态分析 (含显式 any 拦截)
pnpm format:check    # Prettier 代码格式检查 (确保符合统一代码风格)
pnpm knip            # Knip 依赖、无用类型与无用导出/死代码分析
pnpm build           # tsc -b 强类型编译 + Vite 生产打包
pnpm test            # Vitest 浏览器无头模式测试 (--browser.headless via Playwright)
```

---

## 2. 自动化测试规范 (Vitest + Playwright Browser Runner)

脚手架采用 **真实无头浏览器驱动** 的现代化前端测试方案，彻底摆脱传统 JSDOM 对现代 CSS、真实渲染尺寸和复杂事件模拟失真的缺陷。

### 2.1 测试文件命名与共置原则
- 测试文件与被测目标**同目录共置**；
- 组件交互测试命名为 `[component-name].test.tsx`；
- 工具函数与 Hook 测试命名为 `[util-name].test.ts`；
- 严禁在 `src/test/` 下维护脱节的中心化测试大目录。

### 2.2 测试覆盖重点
1. **通用 Hooks 与状态驱动**：如 `useTableUrlState` 的 URL search 参数读写、分页计算与更新触发；
2. **表单验证与错误反馈**：测试基于 `react-hook-form` + `zod` 的必填项报错、非法格式拦截与提交数据形态；
3. **关键弹窗交互流**：测试 `confirm-dialog` 的二次确认、取消动作及外部 `use-dialog-state` 响应；
4. **表格与批量操作**：测试全选/反选、批量删除弹窗唤起与操作触发。

---

## 3. 严格类型约束与零 any 审计

1. **`@typescript-eslint/no-explicit-any: "error"`**：
   - CI 与本地 `pnpm lint` 对任何显式 `any` 直接报错拦截，绝不放行；
2. **外部不可信数据处理**：
   - 所有来自 HTTP API、WebSocket 或 LocalStorage 的外部输入一律标注为 `unknown`；
   - 必须通过 Zod Schema 进行运行时解析（`.parse()` 或 `.safeParse()`）转换成强类型对象后方可进入渲染树。

---

## 4. Vite 构建打包与 Go 二进制嵌入对接契约

系统遵循**方案 A：单主程序交付模型**（Go 二进制文件内嵌前端全部静态产物）：

1. **构建输出协同**：
   - `web/vite.config.ts` 打包输出产物至 `web/dist`；
   - Go 后端通过 `internal/webui/embed.go` 使用 `//go:embed all:dist` 进行静态资源嵌入；
   - 根级构建管道（`Makefile` / CI）负责确保前端完成构建后将产物同步至 Go 嵌入目录（或建立直接对接），严禁在没有前端最新产物时进行发布构建。
2. **SPA 路由回退保障**：
   - 前端采用 TanStack Router 浏览器历史路由（HTML5 History API）；
   - Go HTTP 静态文件服务器在面对非静态文件（非 `.js`, `.css`, `.png` 等）的 404 请求时，必须重写回退到 `index.html`，交由前端路由解析。
3. **构建产物纯净化**：
   - 打包产物严禁引用本地绝对路径；
   - 生产环境构建时必须剥离调试 Devtools（`TanStackRouterDevtools`, `ReactQueryDevtools` 在生产构建中自动剔除）。
