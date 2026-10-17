# 修复前端 cameras 模块缺陷与规范问题

## Goal

针对 `web/src/features/cameras/` 代码评审中发现的关键功能缺陷、跨层契约陷阱、数据虚构、Zod 运行时校验缺失、多处 i18n 硬编码及 RTL 规范违规进行系统性修复，提升模块可靠性与规范对齐度。

## Requirements

1. **表单与弹窗修复 (Form & Dialog Fixes)**
   - 修复 `CamerasActionDialog` 中 Radix `<Select>` 误用 `defaultValue` 导致编辑切换时回填失效的问题，改用受控属性 `value={field.value}`。
   - 修复跨层子流删除问题：当编辑已有子流的相机且取消勾选子流开关时，向后端明确发送清空意图（如传递 `{ role: 'sub', protocol: 'rtsp', rtspUrl: '', transport: 'tcp' }` 或与后端对齐清空逻辑），确保后端不会因为收到 `nil` 而对称保留原有子流。
   - 替换 `CamerasActionDialog` 中可能触发 React Compiler / ESLint 警告的 `form.watch`，改用官方推荐的 `useWatch({ control: form.control, name: 'hasSubStream' })`。
   - 修复 `CamerasDiagnoseDialog` 中对规格数据（分辨率、编码、帧率）的虚假伪造默认值（`1920x1080`、`H.264`、`25 fps`），在无有效探测数据时真实回退显示 `'-'`。并在弹窗打开/切换相机时重置诊断状态。

2. **契约防御与运行时校验 (Zod Runtime Validation)**
   - 在 `api/cameras-api.ts` 中，使用 `cameraListResponseSchema`、`cameraSchema`、`cameraCredentialsResponseSchema`、`diagnoseResponseSchema` 对 API 响应进行运行时验证与解包，拦截不可信输入。
   - 在 `hooks/use-camera-events.ts` 中，对 SSE 接收的 snapshot、change 以及通用 message 数据通过 Zod 进行安全解析（`safeParse`），防止格式异常导致未捕获错误。

3. **批量操作稳健性 (Bulk Actions Robustness)**
   - 将 `CamerasDataTableBulkActions` 中的 `Promise.all` 优化为部分容错（如 `Promise.allSettled`），或更友好的错误统计与 Toast 反馈，防止因单台失败导致全体中断且选择状态残留。

4. **三语国际化契约对齐 (i18n Compliance)**
   - 修复 `CamerasDeleteDialog` 中硬编码中文 `cancelBtnText='取消'`，改为 `t('actions.cancel')`。
   - 修复 `CamerasTable` 中网络失败与重试按钮的硬编码中文提示，使用 `t(...)` 并补全 `zh-Hans.json`、`en.json`、`zh-Hant.json`。
   - 修复 `CamerasDataTableBulkActions`、`CamerasMultiDeleteDialog`、`CamerasDeleteDialog` 中的所有硬编码英文 Toast。
   - 补全三语翻译文件对应的新增 key。

5. **样式与 RTL 规范 (Styling & RTL)**
   - 消除 `CamerasDataTableBulkActions` 中的物理外边距 `mr-2`，改为 RTL 逻辑外边距 `me-2`。
   - 消除 `CamerasTable` 和 `cameras-columns.tsx` 中物理类 `right-0` 与 `rounded-tl`，改为 `inset-e-0` / `rounded-ss`。
   - 改造 `CameraStatusBadge`，采用符合项目统一规范的 Dot-Indicator Outline 调色板与结构，成对适配深浅色。

6. **代码规范与整洁度 (Code Cleanliness)**
   - 解决 `cameras-columns.tsx` 中 7 处禁用 `rules-of-hooks` 的问题，将表头封装为独立组件。

## Acceptance Criteria

- [x] `CamerasActionDialog` 打开编辑时，传输协议 Select 正确受控回填，且编辑关闭子流时能成功将子流移除。
- [x] `CamerasDiagnoseDialog` 仅展示真实探测规格，无虚假默认值。
- [x] 所有 API 与 SSE 响应数据经 Zod 运行时安全校验。
- [x] 无任何未包裹 `t(...)` 的用户可见硬编码字符串。
- [x] 所有外边距与绝对定位均使用 RTL 逻辑类。
- [x] 状态徽标遵循 Dot-Indicator Outline 规范。
- [x] `pnpm lint` 零错误与零警告，前端测试 `pnpm test` 全部通过。
