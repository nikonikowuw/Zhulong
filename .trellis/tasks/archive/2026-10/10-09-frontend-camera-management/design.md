# 前端摄像头资产管理与状态大盘 (Technical Design)

## 1. 架构总览与目录分层

基于 `satnaing/shadcn-admin` 官方设计系统与布局规范，前端在 `web/src/features/cameras/` 下实现高内聚的资产管理业务切片，并在 `web/src/routes/_authenticated/cameras/index.tsx` 挂载强类型文件路由。

### 1.1 页面标准布局契约 (Aligned with shadcn-admin)
页面结构严格遵循 shadcn-admin 标准控制台页面拓扑：
- `<Header fixed>`：集成 `<Search>`、`<ThemeSwitch>`、`<LanguageSwitch>`、`<ProfileDropdown>`；
- `<Main>`：全宽流式工作区（`@container/content`）；
  - **页面标题区**：左侧展示大标题（`text-2xl font-bold tracking-tight`）与副描述说明（`text-muted-foreground`），右侧为 `<CamerasPrimaryButtons>`（主行动点「+ 新增摄像机」与刷新）；
  - **核心数据表格**：`<CamerasTable>` 独占首屏高信息密度工作区，集成通用 `<DataTableToolbar>`（搜索过滤、状态多选筛选、列显隐配置）+ 密集数据行 + `<DataTablePagination>`（页码切换、每页条数）；
  - **批量操作浮条**：选中行时底部浮起 `<CamerasDataTableBulkActions>`；
  - **模态交互组**：`<CamerasDialogs>` 统一受控挂载所有弹窗。

```txt
web/src/
├── routes/_authenticated/
│   └── cameras/
│       └── index.tsx                    # TanStack Router 路由包装与参数校验
├── features/
│   └── cameras/
│       ├── api/
│       │   └── cameras-api.ts           # 强类型 REST 请求与契约交互 (CRUD / Credentials / Diagnose)
│       ├── components/
│       │   ├── cameras-action-dialog.tsx# 创建与编辑弹窗 (带 3-5s 探测 Loading 遮罩与 CAS 并发处理)
│       │   ├── cameras-columns.tsx      # TanStack Table 列定义 (Badge, Switch, 规格, 行操作)
│       │   ├── cameras-credentials-dialog.tsx # 明文 RTSP 凭据查看与一键复制对话框
│       │   ├── cameras-data-table-bulk-actions.tsx # 批量操作 (批量删除/批量启用/禁用)
│       │   ├── cameras-data-table-row-actions.tsx  # 单行操作菜单 (编辑 / 诊断 / 凭据 / 删除)
│       │   ├── cameras-delete-dialog.tsx# 单设备安全删除二次确认
│       │   ├── cameras-diagnose-dialog.tsx # 一键主动探测诊断报告弹窗 (SDP/耗时/轨道信息)
│       │   ├── cameras-dialogs.tsx      # 集中弹窗管理挂载点
│       │   ├── cameras-multi-delete-dialog.tsx # 批量删除二次确认
│       │   ├── cameras-primary-buttons.tsx# 头部操作按钮 (新增摄像机 / 刷新)
│       │   ├── cameras-provider.tsx     # Context 状态中心 (Dialog 开关、当前操作行引用)
│       │   └── cameras-table.tsx        # 核心数据表格 (Toolbar + Table + Pagination)
│       ├── data/
│       │   └── schema.ts                # Zod 模式定义 (Camera, Stream, Form, API 响应)
│       ├── hooks/
│       │   ├── use-camera-events.ts     # SSE 实时事件订阅 (/cameras/events snapshot & update)
│       │   └── use-cameras.ts           # TanStack Query 封装 (列表拉取、创建、修改、删除 Mutation)
│       ├── locales/
│       │   ├── en.json                  # 英文三语词条
│       │   ├── zh-Hans.json             # 简体中文三语词条
│       │   └── zh-Hant.json             # 繁体中文三语词条
│       └── index.tsx                    # 模块主导出视图 (Header + Main + Table + Dialogs)
```

---

## 2. 状态管理与双轨流转设计 (REST + SSE)

```txt
┌────────────────────────────────────────────────────────┐
│ TanStack Query Cache: ['cameras', { page, pageSize, search }]│
└────────▲──────────────────────────────────────▲────────┘
         │ 1. 列表获取 / 分页 / 检索              │ 2. 实时增量更新
┌────────┴───────────────┐              ┌───────┴──────────────┐
│ camerasApi.getCameras  │              │ useCameraEvents(SSE) │
│ (GET /api/v1/cameras)  │              │ (/cameras/events)    │
└────────────────────────┘              └──────────────────────┘
                                                    │
                                        ┌───────────┴──────────┐
                                        │ event: snapshot      │
                                        │ (全量对齐状态基线)   │
                                        │ event: update        │
                                        │ (原地局部 patch 行)  │
                                        └──────────────────────┘
```

1. **表格与 URL 状态驱动**：
   - 使用 `@/hooks/use-table-url-state` 驱动分页（`page`）、分页大小（`pageSize`）、排序（`sort`）与搜索词（`search`）；
   - URL 参数变更自动触发 TanStack Query 查询重拉取；
2. **SSE 实时状态增强 (`useCameraEvents`)**：
   - 挂载 `GET /api/v1/cameras/events`（自动携带认证 Cookie）；
   - 当接收到 `event: update` 时，使用 `queryClient.setQueriesData({ queryKey: ['cameras'] }, ...)` 精准局部更新缓存中对应 `camera.id` 的 `health`、`session`、`degraded`、`stale` 及运行时指标，实现毫秒级 UI 状态流转而不引起表格整页闪烁；
   - 支持 SSE 连接断开时的指数退避自动重连（1s ➔ 2s ➔ 4s ➔ 最大 15s），重连恢复后触发查询刷新以保证数据最终一致。

---

## 3. 接口与契约映射 (Backend ⇄ Frontend)

### 3.1 数据模型对齐 (Schema Alignment)

```typescript
// features/cameras/data/schema.ts
export const streamSchema = z.object({
  id: z.number(),
  role: z.enum(['main', 'sub']),
  protocol: z.string().default('rtsp'),
  rtspUrl: z.string(),
  transport: z.enum(['tcp', 'udp']).default('tcp'),
  codec: z.string().optional().default(''),
  width: z.number().optional().default(0),
  height: z.number().optional().default(0),
  fps: z.number().nullable().optional(),
  fpsString: z.string().optional().default(''),
})

export const cameraSchema = z.object({
  id: z.string(),
  name: z.string(),
  enabled: z.boolean(),
  revision: z.number(),
  health: z.enum(['online', 'offline', 'error', 'unknown']).default('unknown'),
  session: z.enum(['running', 'idle', 'starting', 'error', 'reconnecting']).default('idle'),
  degraded: z.boolean().default(false),
  stale: z.boolean().default(false),
  reason: z.string().optional().default(''),
  lastCheckedAt: z.string().nullable().optional(),
  lastSuccessAt: z.string().nullable().optional(),
  streams: z.array(streamSchema).default([]),
  createdAt: z.string(),
  updatedAt: z.string(),
})

export const cameraFormSchema = z.object({
  name: z.string().min(1, 'cameras.validation.nameRequired').max(64),
  enabled: z.boolean().default(true),
  mainRtspUrl: z.string().min(1, 'cameras.validation.mainRtspRequired').regex(/^rtsp:\/\//i, 'cameras.validation.rtspInvalid'),
  mainTransport: z.enum(['tcp', 'udp']).default('tcp'),
  hasSubStream: z.boolean().default(false),
  subRtspUrl: z.string().optional().refine((val) => !val || /^rtsp:\/\//i.test(val), {
    message: 'cameras.validation.rtspInvalid',
  }),
  subTransport: z.enum(['tcp', 'udp']).default('tcp'),
})
```

---

## 4. 关键交互与边界异常控制

### 4.1 3～5 秒原子探测门禁与 Loading 态
- 创建与更新提交时，后端会主动连接摄像机执行 RTSP Describe 与音视频轨道探测；
- 前端在提交按钮显示 Spinning 状态并展示提示「正在探测音视频流连通性与参数 (3-5s)...」；
- 若探测超时或凭据错误，后端返回 HTTP 400/422，表单保持展开，捕获响应中的错误信息并高亮回显对应流地址字段；
- 探测通过返回 HTTP 201/200，触发 Toast 通知并自动关闭 Dialog。

### 4.2 CAS Revision 并发冲突处理
- 更新摄像机提交 `PUT /api/v1/cameras/:id` 时携带读取时的 `revision`；
- 若被其他管理员修改导致 HTTP 409 冲突，前端捕获错误并提示「配置已被其他用户更新，请刷新后重试」，提供一键拉取最新数据按钮。

### 4.3 RTSP 凭据安全与开箱即用一键复制
- 表格列提供独立的「RTSP 流地址」列，内嵌快捷复制按钮（点击即刻复制真实完整可用 RTSP 地址并弹出 Toast 反馈，支持 Tooltip 查看完整地址，确保 VLC / ffplay 直连开箱即用）；
- 列排布顺序优化对齐硬件资产大盘最佳实践：`[选择] -> [设备 ID] -> [摄像机名称] -> [RTSP 流地址] -> [状态] -> [主流规格] -> [子流规格] -> [启用开关] -> [更新时间] -> [操作]`；
- 点击「查看凭据」弹窗调用 `GET /api/v1/cameras/:id/credentials`，展示解密后的明文地址，并提供一键复制明文凭据。

### 4.4 一键即时诊断 (Diagnose)
- 列表操作菜单提供「一键诊断」，调用 `POST /api/v1/cameras/:id/diagnose`；
- 弹窗展示诊断详情：时延（ms）、SDP 解析详情、主/子流关键帧与音视频轨道探测状态。

---

## 5. 三语国际化与主题规范

- 严格遵循项目 `lib/i18n.ts` 命名空间管理，在 `features/cameras/locales/` 下维护 `en.json`、`zh-Hans.json`、`zh-Hant.json`；
- 在 `locales/{en,zh-Hans,zh-Hant}/common.json` 的 `nav` 字段中注册 `"Cameras": "摄像机管理"`；
- 遵循 OKLCH 语义色，状态指示灯与 Badge 采用：
  - `online`: 语义成功绿（`text-green-600 bg-green-500/10 border-green-500/20`）
  - `offline`: 语义静音灰（`text-muted-foreground bg-muted`）
  - `error`: 语义破坏红（`text-destructive bg-destructive/10 border-destructive/20`）
  - `degraded`: 语义警告橙（`text-amber-600 bg-amber-500/10 border-amber-500/20`）
  - `stale`: 探活超期描边（`text-slate-500 border-dashed`）
