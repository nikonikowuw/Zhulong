# 摄像机配置管理与状态大盘前端 (实施计划)

## 0. 实施前门禁 (Pre-Implementation Gates)

- [x] 确认当前工作区干净，无未提交污染。
- [x] 确认已了解现存前端架构：React 19、Tailwind v4、Lucide 图标库、i18next 与 TanStack Query。

---

## 1. 类型定义、契约校验与 API 层

- [x] 在 `web/src/features/camera/types.ts` 定义：
  - `StreamInfo`（role, protocol, codec, width, height, fps, unmaskedUrl）与 Zod Schema；
  - `CameraStateInfo` / `StreamStateInfo`（health, session, degraded, stale, sequence）与 Zod Schema；
  - `Camera` 核心数据模型与列表响应 Schema；
  - `CreateCameraInput` 与 `UpdateCameraInput` 提交数据类型；
  - `DiagnoseResult` 诊断报告数据类型。
- [x] 在 `web/src/features/camera/utils/urlHelper.ts` 实现：
  - `maskRtspUrl(url: string): string` 密码脱敏；
  - `copyToClipboard(text: string): Promise<boolean>` 跨环境兼容剪贴板复制。
- [x] 在 `web/src/features/camera/api/cameraApi.ts` 实现：
  - `listCameras()` ➔ `GET /api/v1/cameras`
  - `createCamera(data)` ➔ `POST /api/v1/cameras`
  - `updateCamera(id, data)` ➔ `PUT /api/v1/cameras/:id`
  - `deleteCamera(id)` ➔ `DELETE /api/v1/cameras/:id`
  - `diagnoseCamera(id)` ➔ `POST /api/v1/cameras/:id/diagnose`
  - `getCredentials(id)` ➔ `GET /api/v1/cameras/:id/credentials`
- [x] 编写 `urlHelper.test.ts` 与 `cameraApi.test.ts` 基础单元测试。

---

## 2. 国际化多语言配置 (i18n)

- [x] 在 `web/src/features/camera/locales/` 分别编写：
  - `en.json`
  - `zh-Hans.json`
  - `zh-Hant.json`
- [x] 在 `web/src/shared/i18n/index.ts` 注册 `camera` 命名空间或合并至主翻译词典。

---

## 3. 数据流钩子与 SSE 实时同步

- [x] 在 `web/src/features/camera/hooks/useCameras.ts` 实现：
  - `useCamerasQuery()`
  - `useCreateCameraMutation()`（带探测错误解析）
  - `useUpdateCameraMutation()`（带 CAS 409 拦截）
  - `useDeleteCameraMutation()`
  - `useDiagnoseCameraMutation()`
- [x] 在 `web/src/features/camera/hooks/useCameraEvents.ts` 实现：
  - 基于 `EventSource` 订阅 `/api/v1/cameras/events`；
  - 监听 `snapshot` 与 `update` 事件，调用 `queryClient.setQueryData` 局部无缝更新缓存；
  - 指数退避重连机制与组件卸载自动断开清理。

---

## 4. 界面组件开发

- [x] 在 `web/src/features/camera/components/CameraDashboard.tsx` 实现统计指标卡片（全部/在线/离线/异常）。
- [x] 在 `web/src/features/camera/components/CameraCard.tsx` 实现设备卡片：
  - 状态指示灯与 Degraded/Stale 标签；
  - 快捷启用开关切换（Toggle）；
  - 主流/子流规格展示；
  - RTSP 地址明文显隐（👁️）与一键复制；
  - 操作按钮组（编辑、诊断、删除）。
- [x] 在 `web/src/features/camera/components/CameraFormModal.tsx` 实现新增与编辑弹窗：
  - 名称、主流地址、主流传输协议、子流地址、子流传输协议、启用开关；
  - 提交时的 3～5 秒探测 Loading 遮罩与进度文案；
  - 友好精准的表单校验与探测失败原因回显。
- [x] 在 `web/src/features/camera/components/CameraDiagnoseModal.tsx` 实现诊断报告对话框。
- [x] 在 `web/src/features/camera/components/DeleteConfirmModal.tsx` 实现删除安全确认对话框。
- [x] 在 `web/src/features/camera/components/CameraPage.tsx` 组装页面主体，包含头部标题、添加按钮、大盘指标卡与设备列表/卡片网格。

---

## 5. 全局导航与页面装配

- [x] 在 `web/src/App.tsx` 顶栏增加视图切换 Tab（「系统概览」与「摄像机」），支持 URL Hash 驱动与响应式适配。
- [x] 在 `web/src/styles.css` 补充相机卡片、徽标、表单弹窗等专属 CSS 类与毛玻璃动效。
- [x] 导出 `web/src/features/camera/index.ts`。

---

## 6. 自动化测试与质量门禁

- [x] 编写组件测试 `CameraPage.test.tsx`、`CameraFormModal.test.tsx`。
- [x] 运行前端类型检查：`cd web && npm run type-check`
- [x] 运行前端代码检查：`cd web && npm run lint`
- [x] 运行前端单元测试：`cd web && npm test`
- [x] 运行项目全局门禁：`make check`
- [x] 运行单二进制全栈冒烟测试：`make smoke`

---

## 回滚计划 (Rollback Strategy)

若新增前端模块或视图存在无法解决的兼容性或样式异常：
1. 还原 `web/src/App.tsx` 中的顶栏导航与视图分发；
2. 还原 `web/src/shared/i18n/` 注册；
3. 删除 `web/src/features/camera/` 目录；
4. 运行 `npm test && make check` 验证回滚干净。
