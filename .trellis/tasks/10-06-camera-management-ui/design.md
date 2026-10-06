# 摄像机配置管理与状态大盘前端 (Technical Design)

## 1. 架构总览与目录分层

本设计在 `web/src/features/camera/` 下封装独立的摄像机管理业务域，并对全局应用导航进行平滑重构：

```txt
web/src/
├── App.tsx                              # 全局导航栏重构：系统概览 vs 摄像机管理 Tab 切换
├── features/
│   └── camera/
│       ├── api/
│       │   └── cameraApi.ts             # 强类型 REST 调用与 Zod 契约校验
│       ├── hooks/
│       │   ├── useCameras.ts            # TanStack Query 钩子 (列表、新建、更新、删除、诊断)
│       │   └── useCameraEvents.ts       # SSE 长连接钩子 (snapshot 快照与 update 增量更新)
│       ├── components/
│       │   ├── CameraPage.tsx           # 摄像机管理主视图页面 (头部操作栏 + 状态概览卡片 + 列表)
│       │   ├── CameraDashboard.tsx      # 统计指示卡 (全部/在线/离线/异常)
│       │   ├── CameraCard.tsx           # 单设备卡片 (状态徽标、主/子流规格、凭据显隐与操作菜单)
│       │   ├── CameraFormModal.tsx      # 新增/编辑弹窗 (带 3-5s 探测 Loading 状态与错误回显)
│       │   ├── CameraDiagnoseModal.tsx  # 主动诊断结果对话框 (SDP/丢包/网络时延指标)
│       │   └── DeleteConfirmModal.tsx   # 删除安全二次确认对话框
│       ├── utils/
│       │   ├── urlHelper.ts             # RTSP 脱敏、明文还原与安全剪贴板复制
│       │   └── statusHelper.ts          # 正交状态到语义化视觉徽标的映射器
│       ├── types.ts                     # 摄像机数据类型与 Zod Schemas
│       ├── locales/                     # 模块级三语配置 (en / zh-Hans / zh-Hant)
│       └── index.ts                     # 模块公共导出
```

---

## 2. 状态管理与数据流转设计

### 2.1 双轨状态同步机制 (REST + SSE)

```txt
┌────────────────────────────────────────────────────────┐
│ React Query Cache: ['cameras']                         │
└────────▲──────────────────────────────────────▲────────┘
         │ 1. 初始加载 / 手动变更刷新             │ 2. 实时增量更新
┌────────┴───────────────┐              ┌───────┴──────────────┐
│ cameraApi.listCameras  │              │ useCameraEvents(SSE) │
│ (GET /api/v1/cameras)  │              │ (/cameras/events)    │
└────────────────────────┘              └──────────────────────┘
                                                    │
                                        ┌───────────┴──────────┐
                                        │ event: snapshot      │
                                        │ (全量同步状态字典)    │
                                        │ event: update        │
                                        │ (局部更新单机健康/流)│
                                        └──────────────────────┘
```

1. **初始挂载与基线**：页面进入时，通过 React Query 发起 `GET /api/v1/cameras` 获取全量摄像机实体数据（包含凭据、规格与当前快照）。
2. **SSE 实时流转 (`useCameraEvents`)**：
   - 建立 `/api/v1/cameras/events` 连接，支持 Cookie 自动携带；
   - 收到 `event: snapshot` 时，更新本地所有摄像机的 `state` 字段；
   - 收到 `event: update` 时，使用 `queryClient.setQueryData` 原地更新对应 ID 摄像机的 `state`（包含 `health`、`session`、`degraded`、`stale`），无需发起多余 HTTP 请求；
   - 断线时内置指数退避重连（1s ➔ 2s ➔ 4s ➔ 最大 15s），连接重建后静默刷新基线。

---

## 3. 核心交互与安全设计

### 3.1 3～5 秒原子探测门禁交互
- 新增摄像机提交时，表单触发 `POST /api/v1/cameras`。
- 按钮展示微动动画并提示文案：`t("camera.probingNotice")`（「正在探测摄像机网络连通性与码流规格...」）。
- **成功**：后端 201 返回新建实体，自动入库，关闭弹窗，弹出 Toast，列表即时渲染。
- **失败**：后端返回 422/400 并带有业务错误码（如 `STREAM_PROBE_FAILED`、`RTSP_AUTH_FAILED`、`CONNECTION_TIMEOUT`），表单保持打开，高亮错误流地址并清晰展示本地化错误原因。

### 3.2 RTSP 凭据安全显隐与一键复制 (`urlHelper.ts`)
- **掩码规则**：对于形如 `rtsp://admin:passwd123@192.168.1.100:554/live` 的地址，默认脱敏为 `rtsp://admin:••••••••@192.168.1.100:554/live`。
- **明文切换**：每个流右侧提供 👁️（Eye / EyeOff）图标，点击局部切换显隐状态。
- **一键复制**：点击复制按钮复制未脱敏明文地址至剪贴板，按钮右侧呈现 2 秒的绿色微标打勾反馈。在不支持 `navigator.clipboard` 的受限环境中平稳降级为 `document.execCommand('copy')`。

### 3.3 CAS Revision 乐观锁冲突保护
- 编辑摄像机时提交当前对象的 `revision`。
- 若后端返回 HTTP 409（`CAMERA_REVISION_CONFLICT`），界面提示「此摄像机已被其他管理员修改，请刷新后重试」，并提供一键刷新按钮。

---

## 4. UI/UX 视觉体系与组件规约

统一使用现有 Apple SF 科技设计语言：
- **统计大盘 (Dashboard Cards)**：顶部 4 列精简指标卡（总数、在线、离线、异常），使用轻量卡片描边与半透明背景。
- **状态灯徽标 (Semantic Badges)**：
  - `online`：小绿圆点 + "在线" 标签（`var(--positive)` 柔和发光）；
  - `offline`：中灰圆点 + "离线" 标签；
  - `error`：绯红圆点 + "异常" 标签；
  - `degraded`：琥珀色标签（"子流降级"）；
  - `stale`：淡灰描边标签（"探活超期"）。
- **模态弹窗 (Modals)**：深层毛玻璃遮罩（`backdrop-filter: blur(16px)`），流畅的弹性缩放进入动画（Scale & Fade），键盘 `Escape` 键随时关闭，支持点击遮罩退出（提交中除外）。

---

## 5. 模块导出与应用集成

- 在 `web/src/features/camera/index.ts` 暴露 `CameraPage`。
- 在 `web/src/App.tsx` 顶栏增加视图导航（`Overview` / `Cameras`），支持自适应响应式切换。
