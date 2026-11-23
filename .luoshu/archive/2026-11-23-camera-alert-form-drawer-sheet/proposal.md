# Proposal: 2026-11-23-camera-alert-form-drawer-sheet

## 背景与目标

当前系统中，摄像头新增/编辑（`CamerasActionDialog`）与告警推送通道配置（`AlertDestinationDialog`）均使用传统的**居中模态弹窗（Dialog）**展示。这在实际边缘运维场景中带来了明显的体验痛点：

1. **遮挡背景参考信息**：居中模态弹窗开启后，全屏暗色遮罩阻断了底层数据表格。运维人员在新增或修改摄像头（RTSP 流地址格式、子网 IP 规则、通道命名）或告警目标（Webhook URL 域名/端口、MQTT Broker 地址、认证参数）时，无法对照列表中已有的同类设备，容易出现记忆断层或填错配置。
2. **长表单垂直空间挤压**：尤其在告警推送配置中，包含通道类型卡片选择、Webhook Headers 键值对动态增删、事件过滤选择等较长表单项，居中弹窗在 1080p 笔记本等设备上极易产生上下贴边和弹窗内部双重滚动条。

**本次变更目标**：
将摄像头资产与告警推送通道的新增/编辑表单载体从居中模态弹窗（`Dialog`）迁移为**右侧抽屉（Sheet / Drawer）**，利用 100vh 垂直滚动流向优化长表单排版，同时保持左侧资产列表可见，支持运维人员“对照已有资产进行配置”，提升表单填写流畅度与准确率。

## In Scope（本次包含）

- **摄像头模块（Cameras）**：
  - 将 `CamerasActionDialog` 容器由 `Dialog` 迁移为 `Sheet`（保持右侧滑出，响应式宽度 `sm:max-w-xl md:max-w-2xl`，支持 100vh 自适应高度与底部吸附操作栏）。
  - 保留所有现存业务逻辑：RTSP 流连通性测试探针、GB28181 禁用 RTSP 编辑、表单 Zod 校验、乐观更新与错误回显。
  - 更新摄像头模块对应的单元与组件测试用例（`cameras-action-dialog.test.tsx` 等），确保真实浏览器测试全绿。
- **告警推送模块（Alert Push）**：
  - 将 `AlertDestinationDialog` 容器由 `Dialog` 迁移为 `Sheet`（右侧滑出，响应式宽度 `sm:max-w-xl md:max-w-2xl`）。
  - 保留所有现存业务逻辑：Webhook / MQTT 通道类型卡片切换、Header 动态增删、过滤规则设置、连通性提交与错误处理。
  - 更新告警推送模块对应的单元与组件测试用例（`alert-destination-dialog.test.tsx` 等），确保真实浏览器测试全绿。
- **组件一致性与命名对齐**：
  - 导出符合规范的抽屉命名组件或保持向后兼容导出，确保外部页面调用（如 `cameras-dialogs.tsx`、`alert-push-page.tsx`）平滑过渡。

## Out of Scope（明确不做）

- **通道任务配置（Tasks / `channel-task-dialog.tsx`）**：保持现状，后续作为独立的“全屏工作台”专项演进，不塞入本次抽屉改造。
- **人员库管理（Persons）与 API Tokens、GB28181 设备增改**：字段较少、轻量原子操作，本次保持居中弹窗现状。
- **破坏性删除确认（`*-delete-dialog.tsx`）**：严格保持居中 `AlertDialog`，维持危险阻断属性。
- **后端接口及数据结构**：不新增、不修改任何后端 Go API 端点、HTTP 信封或请求参数。
- **表单数据模型（Schema）**：不增加、减少或重命名字段，维持现有的 Zod Schema。

## 影响范围（模块 / 接口契约 / Specs & Rules）

- **涉及文件**：
  - `web/src/features/cameras/components/cameras-action-dialog.tsx`
  - `web/src/features/cameras/components/cameras-action-dialog.test.tsx`
  - `web/src/features/cameras/components/cameras-dialogs.tsx`（核查后无需改动）
  - `web/src/features/alert-push/components/alert-destination-dialog.tsx`
  - `web/src/features/alert-push/components/alert-destination-dialog.test.tsx`
  - `web/src/features/alert-push/components/form/destination-form.tsx`（仅布局：滚动区 + `SheetFooter`）
  - `web/src/features/alert-push/components/alert-push-page.tsx`（核查后无需改动）
- **挂载规格与规则**：
  - Specs: `frontend/index.md`, `frontend/development-guidelines.md`, `frontend/quality-guidelines.md`
  - Rules: `web/frontend-api-boundary`, `web/frontend-browser-tests`, `web/frontend-route-boundary`
