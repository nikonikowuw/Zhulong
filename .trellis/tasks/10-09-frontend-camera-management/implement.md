# 前端摄像头资产管理与状态大盘 (Implementation Plan)

## 1. 实施计划概览

本任务在前端基于 `satnaing/shadcn-admin` 规范构建完整的摄像机资产管理模块（`features/cameras`），涵盖数据大盘、表单交互、一键诊断、明文凭据安全复制、SSE 实时状态同步与三语国际化。

---

## 2. 详细执行清单 (Execution Checklist)

### Phase 1: 契约模型与 API / Hook 基础设施
- [x] **1.1 数据模式与 Zod 校验** (`web/src/features/cameras/data/schema.ts`)
  - 定义 `StreamSchema`、`CameraSchema`、`CameraFormSchema`、`DiagnoseResponseSchema` 及相关 TypeScript 类型。
  - 编写 URL 掩码脱敏与 IP 提取工具函数。
- [x] **1.2 强类型 API 客户端** (`web/src/features/cameras/api/cameras-api.ts`)
  - 实现 `fetchCameras(params)`、`fetchCamera(id)`、`createCamera(data)`、`updateCamera(id, data)`、`deleteCamera(id)`、`fetchCameraCredentials(id)` 与 `diagnoseCamera(id)`。
- [x] **1.3 TanStack Query & SSE 状态钩子** (`web/src/features/cameras/hooks/`)
  - `use-cameras.ts`：封装列表 Query 与各项 Mutation（自动联动 QueryClient 缓存失效与乐观更新）。
  - `use-camera-events.ts`：挂载 `/api/v1/cameras/events` SSE，接收 `snapshot` / `update` 事件就地局部 patch 缓存。

### Phase 2: 三语国际化资源与侧边栏导航集成
- [x] **2.1 多语言字典建立** (`web/src/features/cameras/locales/`)
  - 编写 `en.json`、`zh-Hans.json`、`zh-Hant.json`，完整覆盖列表头、状态标签、指标卡、表单字段、验证错误提示、诊断项。
  - 在 `web/src/lib/i18n.ts` 中注册 `cameras` 命名空间。
- [x] **2.2 全局导航与侧边栏注册**
  - 在 `web/src/locales/{en,zh-Hans,zh-Hant}/common.json` 的 `nav` 添加 `"Cameras"`。
  - 在 `web/src/components/layout/data/sidebar-data.ts` 的 "General" 分组下挂载 `Cameras`（图标使用 `Video` 或 `Camera`）。

### Phase 3: UI 核心组件与交互弹窗
- [x] **3.1 上下文状态与弹窗管理中心** (`web/src/features/cameras/components/`)
  - `cameras-provider.tsx`：提供 Dialog 开关状态与聚焦的当前行数据。
  - （根据架构评审已移除顶部冗余卡片，将首屏纵向空间 100% 留给数据表格）。
- [x] **3.2 数据表格引擎与列定义**
  - `cameras-columns.tsx`：定义选择框、名称与 IP、正交健康 Badge、流规格（主流/子流）、启停 Switch、操作下拉菜单。
  - `cameras-table.tsx`：对接 `useTableUrlState`，集成搜索、分页、列筛选与骨架屏。
  - `cameras-data-table-row-actions.tsx`：行快捷菜单（编辑、诊断、查看凭据、删除）。
  - `cameras-data-table-bulk-actions.tsx`：批量删除操作栏。
- [x] **3.3 交互对话框集合**
  - `cameras-action-dialog.tsx`：新增/编辑弹窗（集成 3-5 秒探测等待遮罩、高亮错误回显、CAS revision 保护）。
  - `cameras-diagnose-dialog.tsx`：即时连通性诊断报告弹窗（展示探测耗时、SDP 状态、轨道详情）。
  - `cameras-credentials-dialog.tsx`：明文凭据安全查看与一键复制弹窗。
  - `cameras-delete-dialog.tsx` 与 `cameras-multi-delete-dialog.tsx`：安全删除二次确认对话框。
  - `cameras-dialogs.tsx`：统一入口挂载所有对话框。

### Phase 4: 路由挂载与综合集成验证
- [x] **4.1 文件路由与页面装配**
  - `web/src/features/cameras/index.tsx`：主业务组件装配。
  - `web/src/routes/_authenticated/cameras/index.tsx`：挂载 TanStack Router 强类型路由与参数验证。
- [x] **4.2 自动化测试与质量门禁验证**
  - 为核心工具函数、Zod 校验模式、API 请求与核心组件编写单元/交互测试。
  - 执行 `pnpm lint`、`pnpm build`（强类型编译）与 `pnpm test` 验证。

---

## 3. 验证命令与质量门禁

```bash
cd web
pnpm lint            # 校验无 ESLint 报错，严格零 any
pnpm build           # TypeScript 类型检查 (tsc -b) 与 Vite 构建打包
pnpm test            # 组件与 Hook 自动化测试
```

---

## 4. 回滚点与风险控制

- **CAS Revision 并发冲突**：若编辑提交遭遇 HTTP 409，弹出明确提示并触发 Query 重新获取最新数据，不覆盖其他端配置；
- **探测超时/网络异常**：表单探测超时后端自动返回 422/400，前端保持表单编辑态并展示友好原因，不丢失已填写数据；
- **SSE 优雅降级**：若 SSE 断开，内置指数退避重连，并在重连成功后发起一次静默刷新基线，保证数据最终一致。
