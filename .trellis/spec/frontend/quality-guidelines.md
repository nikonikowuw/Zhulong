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

### 1.1 与 `make frontend-check` 的对应关系

仓库根 `Makefile` 的前端目标固定使用 **pnpm**（仓库只提供 `pnpm-lock.yaml`，`npm ci` 因缺
`package-lock.json` 必然失败）。目标与上表的映射：

| Makefile 目标 | 实际执行 | 说明 |
| --- | --- | --- |
| `make frontend-install` | `pnpm --dir web install --frozen-lockfile` | 先 `command -v pnpm` 显式探测，缺工具即失败（不静默跳过） |
| `make frontend-build` | `pnpm --dir web run build` | 同上表的 `pnpm build` |
| `make frontend-check` | `lint` + `test` + `build` | `type-check` **不是**独立 script：`build` 的 `tsc -b` 已含类型检查 |

⚠️ **两条容易踩错的约束**：

1. **`type-check` / `audit` 不是 `web/package.json` 的 script**。不要为了「对齐旧 Makefile」而新增空 script——
   类型检查由 `build` 承担，`audit` 见下条。
2. **`audit` 不在 `frontend-check` 内**。生产依赖存在既有安全债（`pnpm audit --prod` 曾报 52 项，
   含 `axios` 的 critical），全量 `pnpm audit` 报 91 项。保留在门禁中会让整个前端检查永久变红，
   从而掩盖 `lint`/`test`/`build` 的真实回归。安全审计作为独立议题跟踪，
   **不得**把它当作「已通过」或「已接受」。

⚠️ `pnpm` 的目录参数是 `--dir <DIR>`（或 `-C`），**不是** npm 的 `--prefix`。

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

### 2.3 断言 effect 产出的状态必须重试等待（`vi.waitFor`）

⚠️ **症状**：`expect(onStateChange).toHaveBeenCalledWith('live')` 偶发失败且收到的是 `'loading'`；单文件跑必过，全量并行跑间歇失败（实测全量 1/6 失败、单文件 0/10）。

**原因**：父级回调由子组件的 `useEffect([state])` 在**提交之后** flush，而驱动该状态的数据常来自 `act` 之外的路径（如 rAF 渲染循环里的 `onFrameDrawn`）。`await expect.element(...).toBeInTheDocument()` 只等到 DOM 出现，**不代表 effect 已 flush** —— 两者不在同一拍。

```typescript
// ❌ 同步断言：DOM 就位不等于 effect 已跑完
await expect.element(screen.getByTestId('overlay-content')).toBeInTheDocument()
expect(onStateChange).toHaveBeenCalledWith('live')

// ✅ 重试等待，直到 effect 真正 flush
await vi.waitFor(() => {
  expect(onStateChange).toHaveBeenCalledWith('live')
})
```

**边界**：初始挂载态（`no-camera` / `loading` / render 前就设好 `status` 的 `unavailable`）由 `render()` 的 `act` 包裹 flush，同步断言安全；**由 act 之外数据流驱动**的状态迁移必须重试等待。

⚠️ **不得用测试等待掩盖产品缺陷**：若某状态本可由 props 在 render 期间同步派生，却回传给了 `useEffect`，那应改渲染派生而非给测试加等待 —— 此时 `vi.waitFor` 才是掩盖问题。

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
