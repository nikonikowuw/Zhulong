# 前端摄像头资产管理与状态大盘 (PRD)

## 1. 目标与定位 (Goal)

在 Web 前端基于 `satnaing/shadcn-admin` 规范构建工业级、高可靠的摄像机资产配置管理与实时状态监控模块（`features/cameras`），完整对接后端 `/api/v1/cameras` 核心业务 API 与 `/api/v1/cameras/events` SSE 实时事件流通道：
1. **资产大盘与多维状态呈现**：基于 TanStack Table 呈现高密度设备列表，展示设备基本信息、主/子码流参数（分辨率、编码格式、帧率、传输协议）、正交健康状态（Online / Offline / Error / Unknown 与 Degraded / Stale 标识）与会话状态（Running / Idle / Starting / Error）。
2. **凭据安全与现场调试工具**：支持安全脱敏显示 RTSP 地址，提供管理员明文凭据查看弹窗/显隐与一键复制功能，方便现场调试（VLC/ffplay）。
3. **闭环配置管理**：提供新增、编辑与删除表单。创建/修改流地址时呈现后端 3～5 秒原子探测门禁的 Loading 态与细粒度失败反馈；编辑时基于 CAS `revision` 乐观锁避免并发覆写。
4. **一键即时诊断 (Diagnose)**：支持对单台设备发起即时网络与流探测，弹窗直观展示 SDP 解析、关键帧探测、网络连通性及耗时详情。
5. **SSE 实时状态同步**：接入 `/api/v1/cameras/events`，通过 `snapshot` 初始化全量基线，`update` 增量刷新单机健康与流状态，无需频繁短轮询。
6. **三语国际化与响应式适配**：严格对齐 `i18next` 体系，支持 `zh-Hans`（简体中文）、`zh-Hant`（繁体中文）和 `en`（英文）无缝切换；遵循 OKLCH 语义色与暗黑模式。

---

## 2. 需求规范 (Requirements)

### 2.1 路由与导航集成 (Route & Navigation)
- **R1.1 路由注册**：在 `src/routes/_authenticated/cameras/index.tsx` 挂载页面，受 `AuthGuard` 守卫保护。
- **R1.2 侧边栏导航**：在 `src/components/layout/data/sidebar-data.ts` 的 "General" 分组下新增 "Cameras"（摄像机管理）导航项，并配置对应图标与三语词条。
- **R1.3 URL 状态持久化**：表格分页（`page`、`pageSize`）、排序（`sort`）与搜索筛选词（`search`）双向绑定至 URL Query 参数。

### 2.2 资产列表与状态展示 (Camera Table & Status Display)
- **R2.1 首屏高密度视口**：移除顶部重复指标卡，直接以 TanStack Table 铺满首屏，最大化单屏可见设备行数。
- **R2.2 核心数据表格 (Data Table)**：
  - **基础信息**：ID（系统唯一设备标识，首位展示）、摄像机名称（独立纯净展示）、真实可用 RTSP 流地址（替代单一 IP，带完整流 Tooltip 与一键复制真实流按钮，方便 VLC / ffplay 直连调试）、更新时间。
  - **快速开关**：列表内提供 Switch 切换启用/禁用状态，即时调用更新接口。
  - **健康状态徽标 (Health Badge)**：
    - `online`（绿色常亮）：正常连通并推流/待机；
    - `offline`（灰色）：设备离线或网络不可达；
    - `error`（红色警告）：探测或拉流异常，悬浮 Tooltip 显示具体 `reason`；
    - `unknown`（黄色）：未探测或初始状态；
    - 组合标识：`degraded`（子流异常橙色角标）、`stale`（心跳超期警告标）。
  - **媒体流规格**：主流与子流分辨率（如 `1920x1080`）、编码格式（`H.264` / `H.265`）、帧率（`25 fps`）、传输模式（`TCP` / `UDP`）。
- **R2.3 三态完备**：优雅的骨架屏（Skeleton Loading）、空数据引导（Empty State）与请求失败重试引导（Error State）。

### 2.3 资产创建与编辑表单 (Create & Edit Flow)
- **R3.1 表单字段与校验 (Zod)**：
  - 摄像机名称（必填，1～64 字符）；
  - 主流 RTSP URL（必填，验证 URL 格式及 `rtsp://` 协议）；
  - 主流传输协议（默认 TCP，支持切换 UDP）；
  - 子流 RTSP URL（选填，合法的 `rtsp://` 地址）；
  - 子流传输协议（选填，默认 TCP）；
  - 启用状态（默认开启）。
- **R3.2 原子探测等待反馈**：
  - 提交创建/修改时，按钮进入探测 Loading 态（文案提示「正在连接摄像机并探测音视频流 (3-5s)...」）；
  - 探测失败（后端 400/422）时保持表单展开，精准高亮回显错误详情；
  - 探测成功即完成入库，触发列表数据失效并弹出成功 Toast。
- **R3.3 CAS Revision 并发控制**：
  - 编辑时携带当前 `revision`；
  - 若遇并发更新冲突（HTTP 409），弹出冲突确认并引导用户刷新最新数据。

### 2.4 凭据安全与一键复制 (Credentials Utility)
- **R4.1 明文显隐与凭据抽屉/弹窗**：列表与详情中默认对 RTSP 密码进行脱敏处理；提供「查看凭据」功能，调用 `GET /api/v1/cameras/:id/credentials` 获取明文 URL。
- **R4.2 一键安全复制**：提供一键复制可用明文 RTSP URL（含用户名密码）到剪贴板，并提供即时 Tooltip/Toast 反馈。

### 2.5 一键主动诊断与删除确认 (Diagnose & Delete)
- **R5.1 手动连通性诊断 (Diagnose)**：
  - 行操作菜单提供「一键诊断」，调用 `POST /api/v1/cameras/:id/diagnose`；
  - 弹窗展示诊断报告：探测总耗时、SDP 协商状态、主/子流关键帧与音视频轨道探测信息。
- **R5.2 删除二次确认**：
  - 弹出二次确认对话框，警示将同步终止拉流管道及下游 AI 推理管道，避免误删。

### 2.6 SSE 实时状态流转 (Real-Time State Sync)
- **R6.1 EventSource 长连接**：挂载全局/页面级 `useCameraEvents`，监听 `/api/v1/cameras/events`；
- **R6.2 增量局部刷新**：
  - 接收 `snapshot` 初始化全量状态；
  - 接收 `update` 自动更新对应摄像机行状态，无需触发全量 Table 重刷。

### 2.7 三语国际化与无障碍设计 (i18n & a11y)
- **R7.1 词条完整性**：所有表单标签、错误提示、状态枚举、表格列名均提取至 `locales/{en,zh-Hans,zh-Hant}/cameras.json`（或 `common.json` 扩展命名空间）；
- **R7.2 规范与无障碍**：遵循 Radix UI 键盘导航与无障碍语义，暗黑/明亮主题自动适配。

---

## 3. 验收标准 (Acceptance Criteria)

- [x] **AC-1 (路由与导航)**：已登录用户可在侧边栏点击 "Cameras" 进入 `/cameras`，页面 URL 参数支持分页与筛选同步，未登录重定向至登录页。
- [x] **AC-2 (列表与状态呈现)**：完整渲染摄像机资产列表，包含名称、IP、健康状态 Badge（Online/Offline/Error/Unknown + Degraded/Stale）、会话状态及主流/子流规格参数。
- [x] **AC-3 (新增与编辑流程)**：支持新增与编辑摄像机，表单包含主流必填校验、子流选填、传输协议切换；提交时具备 3-5 秒探测 Loading；编辑时携带 revision CAS，成功后列表即时更新。
- [x] **AC-4 (凭据安全与复制)**：支持获取与展示明文 RTSP 凭据，支持一键复制到剪贴板并提示成功。
- [x] **AC-5 (一键诊断与删除确认)**：支持调用诊断接口并在 Dialog 中展示时延与流诊断信息；删除操作具有二次确认弹窗。
- [x] **AC-6 (SSE 实时状态同步)**：接入 SSE 事件流，设备状态变更（上线、离线、推流）在无需手动刷新页面的情况下自动局部响应。
- [x] **AC-7 (质量与国际化门禁)**：三语（en, zh-Hans, zh-Hant）无硬编码；`pnpm lint`、`pnpm build`（tsc 强类型检查）与测试全量通过。
