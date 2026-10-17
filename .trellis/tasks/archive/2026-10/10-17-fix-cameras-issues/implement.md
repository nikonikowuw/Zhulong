# 执行计划 (Implement) - 修复前端 cameras 模块缺陷与规范问题

## 1. 执行清单

- [x] **Step 1: 国际化词条补齐 (`locales/`)**
  - 在 `zh-Hans.json`, `en.json`, `zh-Hant.json` 中补齐 `toast.batchStatusUpdateFailed`, `toast.batchDiagnoseFailed`, `toast.batchDeleteFailed`, `table.loadError`, `actions.retry` 等词条。
  - 验证：文件 JSON 格式合法。

- [x] **Step 2: Zod 运行时校验接入 (`api/cameras-api.ts` & `hooks/use-camera-events.ts`)**
  - `cameras-api.ts`: 引入 schema 并调用 `.parse()`。
  - `use-camera-events.ts`: 采用 `safeParse` 过滤异常报文。
  - 验证：运行 `pnpm test -- src/features/cameras/api/cameras-api.test.ts`。

- [x] **Step 3: 表单与弹窗修复 (`components/cameras-action-dialog.tsx`, `cameras-diagnose-dialog.tsx`, `cameras-delete-dialog.tsx`)**
  - `cameras-action-dialog.tsx`: Select 改用 `value={field.value}`，`useWatch` 替换 `form.watch`，实现子流清空 payload。
  - `cameras-diagnose-dialog.tsx`: 清除臆造假数据，增加 mutation reset。
  - `cameras-delete-dialog.tsx`: 修复 `cancelBtnText` 硬编码。
  - 验证：运行 `pnpm test -- src/features/cameras/components/cameras-action-dialog.test.tsx`。

- [x] **Step 4: 样式规范、RTL 与 Dot-Indicator 改造 (`components/cameras-columns-cells.tsx`, `cameras-data-table-bulk-actions.tsx`, `cameras-table.tsx`, `cameras-columns.tsx`)**
  - `cameras-columns-cells.tsx`: 重构 `CameraStatusBadge` 为 Dot-Indicator Outline 风格。
  - `cameras-columns.tsx`: 提取 `CameraColumnHeader` 消除 rules-of-hooks disable；优化 RTL sticky 类。
  - `cameras-table.tsx`: 国际化错误信息与重试按钮；优化 RTL 类。
  - `cameras-data-table-bulk-actions.tsx`: 替换 `mr-2` 为 `me-2`；使用 `t(...)` 处理 toast 错误。
  - `cameras-multi-delete-dialog.tsx`: 使用 `t(...)` 处理 toast 错误。
  - 验证：运行 `pnpm test -- src/features/cameras`。

- [x] **Step 5: 全局质量门禁与回归检验**
  - 执行 `pnpm lint`。
  - 执行 `pnpm test`。
  - 验证：零错误，零警告，全通过。
