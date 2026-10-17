# 技术设计文档 (Design) - 修复前端 cameras 模块缺陷与规范问题

## 1. 架构与边界

该任务属于前端 `web/src/features/cameras` 切片，涉及以下分层与组件协同：
- **REST 数据契约层** (`api/cameras-api.ts` & `data/schema.ts`)：接入 Axios 客户端与 Zod 运行时 Schema 校验。
- **实时事件订阅层** (`hooks/use-camera-events.ts`)：SSE 数据接收、反序列化防御与 TanStack Query 缓存就地 Patch。
- **表单与弹窗交互层** (`components/cameras-action-dialog.tsx`, `cameras-diagnose-dialog.tsx`, `cameras-delete-dialog.tsx`)：Radix UI 属性受控绑定、React Hook Form / Zod 校验、跨层子流删除协商。
- **数据表格展示与列单元格层** (`components/cameras-table.tsx`, `cameras-columns.tsx`, `cameras-columns-cells.tsx`)：TanStack Table 表头封装、Dot-Indicator 状态徽标、RTL 逻辑类对齐。
- **三语国际化资源层** (`locales/{zh-Hans,en,zh-Hant}.json`)：统一收拢所有硬编码错误信息与按钮文案。

## 2. 关键技术方案设计

### 2.1 修复 Select 属性与子流删除契约 (`cameras-action-dialog.tsx`)
1. **Select 受控修复**：
   - 将 `defaultValue={field.value}` 改为 `value={field.value}`。
   - 当表单 `form.reset(...)` 时，`<Select>` 视图同步更新。
2. **子流清空跨层通信**：
   - 编辑模式下：若 `hasSubStream` 为 `false` 且原设备存在子流，向后端 `updateCamera` 发送 `{ role: 'sub', protocol: 'rtsp', rtspUrl: '', transport: 'tcp' }`。
   - 后端 `internal/camera/service.go` 的 `req.SubStream != nil && RTSPURL == ""` 分支将跳过探测且不保留既有子流，使 DB 中的 Streams 仅保留主码流。
3. **消除 watch lint 警告**：
   - 替换 `form.watch('hasSubStream')` 为 `useWatch({ control: form.control, name: 'hasSubStream' })`。

### 2.2 诊断弹窗虚假数据兜底消除 (`cameras-diagnose-dialog.tsx`)
- 消除 `|| 'H.264'`、`|| '1920x1080'`、`|| '25 fps'` 臆造逻辑。
- 当 `width`、`height`、`fps`、`codec` 为 0 或空时，显示 `'-'`。
- 在弹窗关闭或 `currentRow` 切换时，调用 `diagnoseMutation.reset()` 避免脏数据闪烁。

### 2.3 Zod 运行时防御拦截 (`cameras-api.ts` & `use-camera-events.ts`)
- `camerasApi` 各方法在返回 `resp.data.data` 前通过对应 Schema 调用 `.parse()`（或 `.safeParse()` 降级保护）。
- `useCameraEvents` 中的 `e.data` 在 `JSON.parse` 后通过 `cameraStateInfoSchema` 或 `z.array(cameraStateInfoSchema)` 的 `safeParse` 校验后再写入 TanStack Query 缓存。

### 2.4 状态徽标 Dot-Indicator Outline 规范化 (`cameras-columns-cells.tsx`)
- 依据项目统一设计规范，重构 `CameraStatusBadge`：
  - `online`: `bg-emerald-500` + `border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400`
  - `offline`: `bg-zinc-400 dark:bg-zinc-500` + `border-zinc-500/30 bg-zinc-500/10 text-zinc-600 dark:text-zinc-400`
  - `error`: `bg-destructive animate-pulse` + `border-destructive/30 bg-destructive/10 text-destructive`
  - `unknown`: `bg-sky-400` + `border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-300`
- 内部包含 `size-1.5 rounded-full` 状态点与语义文本。

### 2.5 表头组件抽取与 Rules-of-Hooks 修复 (`cameras-columns.tsx`)
- 封装顶层纯函数组件 `<CameraColumnHeader column={column} titleKey={key} />`。
- 移除所有 7 处 `// eslint-disable-next-line react-hooks/rules-of-hooks`。

### 2.6 三语国际化与 RTL 属性修复
- 补齐 `locales/{zh-Hans,en,zh-Hant}.json` 中的缺失 key（批量状态更新错误、批量诊断失败、批量删除失败、表格加载失败提示、重试按钮、取消按钮等）。
- 将 `mr-2` 替换为 `me-2`；将 `sticky end-0 right-0` 替换为 `sticky inset-e-0`；将 `rounded-tl-[inherit]` 替换为 `rounded-ss-[inherit]`。
