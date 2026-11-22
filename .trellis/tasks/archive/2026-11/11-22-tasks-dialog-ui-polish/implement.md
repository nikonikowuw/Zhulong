# Implementation Plan: Optimize tasks ChannelTaskDialog UI & calibration UX

## Ordered Execution Checklist

1. **R5 · 改造区域编辑器撤销栈以保护 Crop 与跨算法历史**
   - 修改 `web/src/features/tasks/hooks/use-region-editor.ts`：引入 `EditorHistoryEntry`（区分 `region` + `scopeKey` 与 `crop`）、延迟归类提交与按作用域查找撤销。
   - 修改 `web/src/features/tasks/hooks/use-channel-task-drafts.ts`：向 `useRegionEditor` 与 `reset()` 传入当前 `activeAlgoId` 作为 `scopeKey`。
   - 补充测试：`web/src/features/tasks/hooks/use-channel-task-drafts.test.ts`（或新建/扩充 `use-region-editor.test.ts`）验证：同算法混合顺序撤销、切算法后 Crop 仍可撤销且不覆盖新算法区域、切回原算法后区域仍可撤销、切相机清空历史。

2. **R2 · 修复 ROI / MASK 重叠绘制事件抢占**
   - 修改 `web/src/features/tasks/components/region-canvas.tsx`：非 `select` 工具下对已有 ROI/MASK 矩形应用 `pointer-events-none` 并隐藏缩放手柄。
   - 补充测试：`web/src/features/tasks/components/region-canvas.test.tsx` 验证在已有 ROI 矩形内部使用 `+ MASK` / `+ ROI` 工具拖拽时可新建矩形而不拖动旧矩形。

3. **R3 · 保存校验失败自动切换至参数 Tab 与非法 JSON 直达源码模式**
   - 修改 `web/src/features/tasks/hooks/use-algo-config-form.ts`：当输入配置无法解析为合法 JSON 对象时自动定位至 `'json'` 模式。
   - 修改 `web/src/features/tasks/components/channel-task-algo-panel.tsx`：支持受控 `activeTab` / `onTabChange`。
   - 修改 `web/src/features/tasks/components/channel-task-dialog.tsx`：托管 `activeTab`，校验失败时联动 `selectActiveAlgo(issue.algoId)` + `setActiveTab('params')`。
   - 补充测试：`web/src/features/tasks/components/channel-task-dialog.test.tsx` 验证停留在「分时布防计划」Tab 点击保存触发 Schema 错误或非法 JSON 错误时自动切回参数表单/JSON 源码。

4. **R4 · 弹窗内区域编辑键盘快捷键**
   - 修改 `web/src/features/tasks/components/channel-task-dialog.tsx`（可抽纯 helper 检测可编辑焦点）：支持 `Ctrl/Cmd+Z` 撤销及 `Delete`/`Backspace` 删除选中矩形或 `crop` 窗口。
   - 补充测试：`web/src/features/tasks/components/channel-task-dialog.test.tsx` 验证快捷键生效及聚焦文本框/输入框时不误触发。

5. **R1 · 弹窗窄屏响应式布局优化**
   - 修改 `web/src/features/tasks/components/channel-task-dialog.tsx`、`channel-task-dialog-header.tsx`、`channel-task-budget-meter.tsx`、`channel-task-region-viewport.tsx`、`region-editor-toolbar.tsx`、`channel-task-algo-panel.tsx`：实现 `< lg` 垂直分栏、算力条全断点可见与 Header 自适应换行，保持 `lg+` 原有桌面双栏外观。
   - 补充测试：在 `channel-task-dialog.test.tsx` 校验窄屏视口下预算条可见与布局无横向溢出。

6. **质量检查与验收**
   - 定向格式化、Lint 与单测（`web/src/features/tasks`）。
   - 收尾执行完整前端门禁 `make frontend-check`（含 `format:check`、`lint`、`test`、`build`）。

## Validation Commands

迭代期定向检查（在 `web/` 下）：
```bash
pnpm exec prettier --check "src/features/tasks/**/*.{ts,tsx}"
pnpm exec eslint "src/features/tasks/**/*.{ts,tsx}"
pnpm test -- src/features/tasks
```

收尾全量门禁（仓库根目录）：
```bash
make frontend-check
```

## Risky Files & Rollback Points

- `web/src/features/tasks/hooks/use-region-editor.ts`：核心撤销栈逻辑；需确保不破坏 `tripwire-canvas.test.tsx` 与 `use-channel-task-drafts.test.ts` 的现有契约。
- `web/src/features/tasks/components/region-canvas.tsx`：指针穿透逻辑；需保证 `tool === 'select'` 选中/平移/缩放与 `tool === 'crop'` 移动裁剪框行为均不受影响。
- `web/src/features/tasks/components/channel-task-dialog.tsx`：键盘事件与受控 Tab 逻辑；需严守输入焦点隔离，避免拦截文本框 `Ctrl+Z` / `Backspace`。
