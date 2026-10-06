# Journal - nikoniko (Part 1)

> AI development session journal
> Started: 2026-10-04

---

## Session 1: Repository and Trellis bootstrap
<!-- trellis-session: v=2 fp=baa2d89853e492a7 -->

**Date**: 2026-10-04
**Task**: Repository and Trellis bootstrap
**Branch**: `dev`

### Summary

Committed repository/Go setup and Trellis workflow/specs separately; corrected the TypeScript ignore rule and stale guidance link, then archived Bootstrap Guidelines.

### Git Commits

| Hash | Message |
|------|---------|

### Status

[OK] **Completed**

## Session 2: Apple-Style UI Overhaul and Auth UX Refactoring
<!-- trellis-session: v=2 fp=2006807dd10f453a -->

**Date**: 2026-10-05
**Task**: Apple-Style UI Overhaul and Auth UX Refactoring
**Package**: frontend
**Branch**: `feat/project-skeleton`

### Summary

Rebuilt system theme layout as an edge-to-edge macOS console with GPU dynamic aurora background, refactored login and setup wizards with Remember Me and onBlur validation

### Main Changes

- Transformed topbar into edge-to-edge frosted glass console header with minimal brand logo
- Added GPU-accelerated macOS Aurora dynamic ambient background with 24px dot matrix grid
- Refactored LoginForm and InitForm into compact macOS-styled dialogs
- Added Remember Me persistence and onBlur field-level validation with real-time error clearance

### Git Commits

| Hash | Message |
|------|---------|
| `813ec4b` | feat(frontend): overhaul Apple-style theme layout and authentication UX |

### Testing

- [OK] pnpm --prefix web test (37 passed across validation and auth flows)
- [OK] pnpm --prefix web type-check && pnpm --prefix web lint && pnpm --prefix web build
- [OK] go test ./...

### Status

[OK] **Completed**

### Next Steps

- Review and commit remaining backend error-handling changes when ready

## Session 3: 项目骨架、认证收尾与媒体接入
<!-- trellis-session: v=2 fp=1e9dac347825b643 -->

**Date**: 2026-10-05
**Task**: 项目骨架、认证收尾与媒体接入
**Package**: backend
**Branch**: `dev`

### Summary

完成项目骨架与单用户认证任务归档；提交认证错误解耦及全栈开发入口，规划并实现静态 FFmpeg RTSP 原生接入。make check 与 smoke 已通过；实际目标交叉编译和板端验证仍未完成，Native 子任务保持进行中。

### Git Commits

| Hash | Message |
| ------ | --------- |
| `233a482` | refactor(auth): decouple domain errors with apperr, add generic endpoints, and remove rememberMe |
| `47a5b27` | build: add air live reload configuration and fullstack dev targets |
| `e84fc3d` | chore(task): add planning artifacts for media ingestion pipelines |
| `5abbe4d` | chore(task): update 10-05-native-ffmpeg-ingestion planning status |
| `0080930` | feat(native): implement C++ media engine with static FFmpeg RTSP ingestion |

### Status

[OK] **Completed**

---

**Date**: 2026-10-05
**Task**: Go/CGO 媒体桥接与订阅生命周期 (`10-05-go-cgo-media-bridge`)
**Package**: backend
**Branch**: `dev`

### Summary

完成媒体接入阶段二首个交付：实现 `internal/engine/` 中的纯 Go 门面与 CGO 跨语言桥接。

1. 封装 Native 视频流探测（`Probe`），采用独立私有 Native Engine 隔离取消，支持最多 4 路并发控制与 context deadline 收敛。
2. 封装物理 RTSP 流获取与复用（`Acquire` / `Release` / `Status`），支持连接池复用与 8 秒宽限期语义。
3. 实现按需视频包订阅（`Subscribe` / `Next`），在 CGO 回调中通过借用指针与 `unsafe.Slice` 深拷贝到 Go-owned 内存，配合有界包数与字节队列（默认 32 包 / 16 MiB 上限）提供明确的 `ErrBackpressure` 终态。
4. 保证 `cgo.Handle` 生命周期安全：仅在 Native unsubscribe 同步排空（Drain）完成后执行 `handle.Delete()`，杜绝悬空指针访问。
5. 编写测试运行器 `native/tests/run_go_bridge_tests.py` 并接入 `make go-check`，在真实 loopback RTSP 服务桩上验证 H.264/H.265、TCP/UDP、超大参数集、慢订阅背压隔离与敏感凭据脱敏。全栈 `make check`、`make smoke` 及 `GOEXPERIMENT=cgocheck2` 均 100% 通过。

### Status

[OK] **Ready for commit review**

## Session 4: Go/CGO 媒体桥接与订阅生命周期落地与审查收尾
<!-- trellis-session: v=2 fp=03ae9bff29ab9c43 -->

**Date**: 2026-10-05
**Task**: Go/CGO 媒体桥接与订阅生命周期落地与审查收尾
**Package**: backend
**Branch**: `dev`

### Summary

完成 internal/engine 中的纯 Go 门面与 CGO 跨语言桥接，通过真实 RTSP 回环与全栈质量门禁，并完成并发边界与内存泄露修复收尾与子任务归档。

### Main Changes

- 实现 Probe、Acquire、Status、Subscribe、Next 等 Go 原生媒体接入与订阅接口
- 修复 cgo.Handle 在 ERR_NOT_FOUND 终态下的释放，杜绝内部句柄泄露
- 修复 Probe 并发状态读取的 Data Race 与 TOCTOU 竞态
- 修复 Acquire 在底层调用返回后的代次与停止状态二次校验，杜绝孤儿流
- 优化 Subscription.Next 出队置空 Packet{}，避免 GC 内存切片驻留

### Git Commits

| Hash | Message |
|------|---------|
| `8a6a887` | feat(engine): implement Go/CGO media bridge and packet subscription lifecycle |
| `dfae13e` | chore(task): update task planning and journal for 10-05-go-cgo-media-bridge |

### Testing

- [OK] make go-check：通过 Go vet、race 竞态测试及真实 RTSP 服务桩集成测试（run_go_bridge_tests.py）
- [OK] make check：通过前端代码检查/单元测试/构建、Native 单元与集成测试、Swagger 文档生成与 Go 质量门禁

### Status

[OK] **Completed**

### Next Steps

- 启动父任务下的摄像机业务与四态管理独立子任务



## Session 6: 摄像机生命周期管理与健康度检测落地
<!-- trellis-session: v=2 fp=6d81cfdcc1e7c9f7 -->

**Date**: 2026-10-06
**Task**: 摄像机生命周期管理与健康度检测落地
**Package**: backend
**Branch**: `dev`

### Summary

Session summary was not supplied.

### Main Changes

### Summary

完成 `10-05-camera-lifecycle` 摄像机全生命周期后端实现：

1. **SQLite 迁移与存储**：增加 `000003_create_cameras_tables.up.sql`/`down.sql`，提供 `cameras` 与 `camera_streams` 外键级联存储，实现带 Revision CAS 乐观锁检测的 GORM Store。
2. **凭据安全与加密**：实现基于 AES-256-GCM 与 AAD (`v1:<camera_id>:<role>`) 的密钥管理器，强制 0600 文件权限，库内存在密文但密钥缺失时阻断启动。
3. **RTSP 鉴权与轻量 DESCRIBE 探活**：集成 `github.com/icholy/digest v1.2.0` 低层 API 适配 RTSP Digest 鉴权（支持 MD5/SHA-256、qop=auth 与 opaque="" 规范化），实现单连接轻量 DESCRIBE 探测，绝不发送 SETUP/PLAY/RTP。
4. **主子流共享原子探测门禁**：入库与修改时并发验证主子流，共享 5s 预算，容忍未知 FPS（0/1）并拒绝非法分辨率与非 H.264/H.265 编码，任一流失败整笔配置不生效。
5. **正交状态模型与分级调度器**：严格解耦 `enabled`、`health`（unknown/online/offline/error）与 `session`（idle/starting/running/reconnecting/error）；活跃流采用 4s 收包超时判定；空闲流采用 30s 周期（±20% 抖动，8 并发有界信号量）轻量 DESCRIBE 探活，支持 3 次连续失败转 offline 与 240s stale 判定。
6. **SSE 事件流**：实现有界队列缓冲、单调增量序号、原子初始快照、慢消费者驱逐及 16 连接上限控制。
7. **REST API、i18n 与 Swagger**：实现摄像机 CRUD、管理员明文凭据安全查询、诊断与 SSE 端点，提供中/英/繁三语错误码支持，完成 Swagger 文档更新。
8. **Uber Fx 装配与安全生命周期**：在 `internal/app` 完成装配与优雅停机顺序（HTTP 排空 ➔ 停止调度与关闭 SSE ➔ 释放 Native ➔ 关闭 SQLite ➔ 同步日志），测试验证启动失败逆序回滚。
9. **规范沉淀**：建立 `.trellis/spec/backend/camera-guidelines.md` 规范文档，并在架构与后端规范索引中注册。

### Main Changes

- `internal/database/migrations/000003_create_cameras_tables.*`：摄像机表与流表迁移
- `internal/database/`：删除 `base_model.go`，避免对领域 Model 的隐式类型绑定和包耦合
- `internal/auth/user.go`：改为显式自包含定义，与 `camera` 及主流 Go 社区实践保持一致
- `internal/app/`：重构 `newRouter` 与 Fx 装配，引入 `RouteRegistrar` 与 Value Groups 多重绑定，解耦全局路由器
- `internal/camera/`：包含 crypto、store、uri、rtsp_auth、rtsp_describe、probe、state、scheduler、events、service、handler、locale、requests、responses 及完备测试；遵循地道 Go 规范，实体模型显式定义，已认证管理员直接返回完整明文 RTSP URL（含账密与参数），数据库保留 AES-GCM 密文存储，日志保持脱敏
- `internal/app/`：注入 Camera 模块，更新 Fx 装配与生命周期顺序，完善单元测试
- `.trellis/spec/backend/camera-guidelines.md`：建立摄像机业务与生命周期规范

### Testing

- [OK] `make go-check`：Go vet、race 竞态测试、真实 RTSP 回环桥接测试全部通过
- [OK] `make native-test`：纯 C ABI、引擎生命周期与 Native 集成测试全部通过
- [OK] `make check`：前端规范、Native 测试、Swagger 生成与 Go 检查全量通过
- [OK] `make smoke`：单二进制运行冒烟测试（Health、SPA、API 404 隔离、Swagger、优雅停机）通过

### Status

[OK] **Completed**

### Next Steps

- 规划与实现 WebSocket 视频流分发子任务


### Git Commits

| Hash | Message |
|------|---------|
| `6929c71` | feat(camera): implement camera lifecycle, AES-GCM encryption, and health scheduling |
| `9c65162` | docs(spec): document camera lifecycle, encryption, and dual-stream gate |
| `68e9d20` | docs(task): update acceptance criteria to checked for 10-05-camera-lifecycle |

### Status

[OK] **Completed**


## Session 7: WebSocket 媒体流分发与按需订阅 (10-06-websocket-streaming) 实施与归档
<!-- trellis-session: v=2 fp=05028726f3d9613c -->

**Date**: 2026-10-06
**Task**: WebSocket 媒体流分发与按需订阅 (10-06-websocket-streaming) 实施与归档
**Package**: backend
**Branch**: `dev`

### Summary

完成了媒体接入子任务 10-06-websocket-streaming 的全链路实现与门禁验证：引入 gorilla/websocket，实现 ZLM1 24 字节大端序二进制 Wire Protocol 封包与解包；构建了单源多端分发的 StreamDispatcher 与 GOP 关键帧秒开缓存；封装了支持按需 CGO 借用、0 消费者释放与 8s 宽限期防抖的 StreamHub；挂载了经过认证保护的 WebSocket 端点与 Swagger 2.0 文档；装配进 Uber Fx 并保证 HTTP 优雅排空后逆序释放底层资源。全量通过 make go-check、make native-test、make check 与 make smoke 门禁，顺利归档任务。

### Git Commits

| Hash | Message |
|------|---------|
| `2c95fd6` | feat(camera): implement WebSocket video streaming, ZLM1 protocol, and on-demand StreamHub |
| `e60b54e` | docs(spec): document WebSocket streaming, ZLM1 wire protocol, and StreamHub lifecycle |
| `0f75165` | docs(task): record task planning and implementation for 10-06-websocket-streaming |

### Status

[OK] **Completed**


## Session 8: 实现摄像机配置管理与实时大盘前端 (10-06-camera-management-ui)
<!-- trellis-session: v=2 fp=c18de453cf42f1e7 -->

**Date**: 2026-10-06
**Task**: 实现摄像机配置管理与实时大盘前端 (10-06-camera-management-ui)
**Package**: frontend
**Branch**: `dev`

### Summary

构建摄像机配置管理与状态大盘前端模块，实现顶栏视图导航、正交健康大盘卡片、RTSP 凭据掩码显隐与安全复制、3~5s 原子探测反馈表单与 SSE 实时状态同步。

### Main Changes

- 实现全局顶栏导航切换（系统概览 vs 摄像机管理），与 URL Hash 状态双向绑定
- 封装 features/camera 业务域：Zod 强类型契约、REST API 客户端与 useCameras TanStack Query 钩子
- 实现 useCameraEvents SSE 实时事件监听，支持快照初始化与单机增量无感刷新
- 开发 CameraDashboard 指标大盘、CameraCard 设备卡片、CameraFormModal 探测表单、诊断与删除确认模态框
- 实现 RTSP 密码掩码显示与 👁️ 显隐切换，以及跨浏览器兼容的一键安全复制完整明文地址
- 完善 en、zh-Hans、zh-Hant 完整三语国际化词典，并通过全套 Vitest 单元测试

### Git Commits

| Hash | Message |
|------|---------|
| `98182b0` | feat(web): implement camera management view, dashboard, and SSE real-time sync |
| `0cef146` | docs(spec): document camera management frontend and real-time SSE specifications |
| `71e7242` | docs(task): record task planning and implementation for 10-06-camera-management-ui |

### Testing

- [OK] npm run type-check: TypeScript 严格类型检查 0 错误
- [OK] npm run lint: ESLint 代码风格检查 0 错误
- [OK] npm test: 16 个测试套件 65 项单元测试 100% 通过
- [OK] make check: 前端生产构建、Native C++ 测试、Go vet/test -race 全量门禁通过
- [OK] make smoke: 单二进制生产交付冒烟测试通过

### Status

[OK] **Completed**

### Next Steps

- 推进 10-05-media-ingestion 的实时预览看板与播放器组件（10-06-live-player-grid 或 Step 3.1/3.3）


## Session 9: 实现实时视频播放器组件与多路监控宫格看板 (10-06-live-player-grid)
<!-- trellis-session: v=2 fp=38a9ce7fbe35d0ae -->

**Date**: 2026-10-06
**Task**: 实现实时视频播放器组件与多路监控宫格看板 (10-06-live-player-grid)
**Package**: frontend
**Branch**: `dev`

### Summary

构建纯前端低延迟视频播放与多路监控大盘，实现 ZLM1 协议解包器、FrontendStreamPool 引用计数与 3s 防抖连接池、WebCodecs 极低延迟硬件解码渲染、1/4/9 宫格看板、ROI 覆盖层与三级导航整合。

### Main Changes

- 实现 ZLM1 协议解包器与 Annex B NALU 提取器，严格解析 24 字节大端序帧头
- 实现 FrontendStreamPool 单例流连接池，支持多视口单例复用、引用计数与 3s Grace Period 防抖延迟释放
- 基于原生 WebCodecs VideoDecoder 与 Canvas 开发极低延迟 LivePlayer 播放器
- 开发 1/4/9 宫格监控看板、视口指派、主子流切换、单槽位全屏展开与 localStorage 持久化
- 实现 RoiOverlayCanvas 目标检测覆盖层与 LiveTelemetryHud 实时帧率/码率悬浮层
- 顶栏导航增加实时监控 (Live) 入口，支持 Hash 路由同步与 en/zh-Hans/zh-Hant 完整三语

### Git Commits

| Hash | Message |
|------|---------|
| `8de89cd` | feat(web): implement LivePlayer, FrontendStreamPool, and 1/4/9 live dashboard |
| `7fb247f` | docs(spec): document live player and multi-grid surveillance specifications |
| `4163f03` | docs(task): record task planning and implementation for 10-06-live-player-grid |

### Testing

- [OK] npm run type-check: TypeScript 严格类型检查 0 错误
- [OK] npm run lint: ESLint 与 React 19 Compiler 检查 0 警告 0 错误
- [OK] npm test: 21 个测试套件 82 项单元测试 100% 通过
- [OK] make check: 前端生产构建、Native C++ 测试、Go vet/test -race 全量门禁通过
- [OK] make smoke: 单二进制生产交付冒烟测试通过

### Status

[OK] **Completed**

### Next Steps

- 推进 10-05-media-ingestion 的端到端流转与物理流复用集成验收 (Step 4.1/4.2) 或 Native FFmpeg 接入 (10-05-native-ffmpeg-ingestion)

---

## 2026-10-06 摄像机流转管道与调度状态时序缺陷修复

**Task**: `10-06-camera-stream-fixes`
**Status**: Completed
**Package**: `backend`
**Branch**: `dev`

### Summary

针对 `internal/camera` 模块的 code review 结果，修复了四个关键并发与状态机时序缺陷：活跃流收包证据未上报导致的 4s 误判超时、StreamHub 并发拉流防击穿、Session 状态切换未向 SSE 广播、以及调度器停机时网络阻塞导致的慢退出。

### Main Changes

- **活跃流收包证据节流刷新 (`stream_dispatcher.go`)**：在 `RunPumpLoop` 消费到视频帧时，遇到关键帧或间隔 ≥1s 节流上报 `EvidencePacketActivity` 刷新 `LastCheckedAt`，彻底解决活跃流被误杀超时问题。
- **并发 Singleflight 保护 (`stream_hub.go`)**：使用 `golang.org/x/sync/singleflight` 协同合并对同一路码流的并发拉流请求，杜绝多视口/多用户同时进入时对摄像机重复发起物理 RTSP 握手与 C++ 句柄开销。
- **SessionState 实时 SSE 广播 (`stream_hub.go`, `stream_dispatcher.go`)**：StreamHub 注入 `EventHub`，流在 `running` 与 `idle` 切换时实时广播 `change` 事件，前端大盘推流徽标秒级同步。
- **HealthScheduler 快速优雅停机 (`scheduler.go`, `rtsp_describe.go`)**：调度器引入全局级联取消 Context；并在 `DescribeClient.Describe` 中通过后台 watchdog 协程监听上下文取消打断阻塞的 socket，停机时延从 5s 降至 <100ms。
- **装配与测试**：更新 `internal/app/app.go` 依赖注入，补充 Singleflight 并发测试、SSE 广播测试、节流收包测试与快速停机测试，通过全量 `-race` 竞态检测。

### Git Commits

| Hash | Message |
|------|---------|
| `385c990` | fix(camera): resolve packet timeout false alarm, add singleflight, and fix session broadcast |
| `71af22d` | docs(spec): document packet activity throttling and singleflight streaming |
| `9695458` | docs(task): record task planning and implementation for 10-06-camera-stream-fixes |

### Status

[OK] **Completed**

---

## 2026-10-06 管理控制台 Console 布局重构与管理仪表盘升级

全面重构前端 UI 体系，彻底去除 SaaS 产品介绍/营销落地页（Landing Page）风格，蜕变为专业高密度的安防与设备管理控制台（Management Console）。

### Main Changes

- **经典管理后台架构 (`Sidebar.tsx`, `ConsoleTopbar.tsx`, `App.tsx`)**：
  - 采用固定/可收缩左侧边栏（`Sidebar`），支持 240px 展开 / 64px 紧凑图标模式，偏好自动保存至 `localStorage`，并在小屏设备下支持抽屉式遮罩交互。
  - 统一控制台顶部操作栏（`ConsoleTopbar`），集成模块面包屑、全局在线设备状态徽章、全屏模式切换、手动数据刷新、三语语言选择与主题切换。
  - 彻底解开工作区 `max-width: 1200px` 限制，采用全宽自适应流式工作台（Fluid Workspace），使实时监控多路矩阵与设备卡片网格能够充分利用大屏宽度。
- **视觉彻底去营销化 (`styles.css`)**：
  - 移除 `.ambient-background` 与 4 个浮动毛玻璃大光斑（`.ambient-orb-1~4`）及其 Keyframe 动画，显著降低 GPU 与合成器额外开销。
  - 规范现代工业质感配色彩板，采用 Slate/Zinc 高对比度、精炼边框与紧凑状态指示灯。
- **系统概览升级为综合管理仪表盘 (`OverviewDashboard.tsx`)**：
  - 增加 4 大核心运行 KPI 卡片：摄像机资产总览、实时视频流就绪通道、AI 原生推理引擎加速态、主机服务就绪态。
  - 融合组件健康详情面板（Host / Database / Native Engine），并提供直达实时监控、设备管理与流诊断的快速通道。
- **三语国际化与测试覆盖**：
  - 同步补齐 `en.json`、`zh-Hans.json`、`zh-Hant.json` 中的控制台相关词条。
  - 为 `Sidebar`、`ConsoleTopbar` 与 `OverviewDashboard` 补全单元与交互测试，全量 25 个测试套件（93 个测试用例）100% 通过。

### Git Commits

| Hash | Message |
|------|---------|
| `a5bb031` | feat(web): redesign layout to professional management console and add dashboard |
| `8fff639` | docs(spec): document console layout and dashboard guidelines |
| `2f3f4a1` | docs(task): update planning and journal for management console layout redesign |
| `703d475` | refactor(web): move user nav and logout action from topbar to sidebar bottom |
| `260c1a1` | refactor(web): remove obsolete mock footer text and streamline system info |
| `8ec913f` | fix(web): enforce app viewport lock so topbar and sidebar stay fixed |
| `99037f1` | refactor(web): remove fake status card and redundant tags from sidebar |
| `c90e5b1` | fix(web): enforce standard 16:9 aspect-video ratio for live surveillance viewports |

### Testing

- [OK] `npm run type-check`: TypeScript 严格类型检查无任何 `any`
- [OK] `npm run lint`: ESLint 0 错误 0 警告
- [OK] `npm run test`: Vitest 25 个测试文件 93 个测试用例全部通过
- [OK] `npm run build`: Vite 生产环境构建打包成功
- [OK] `python3 native/scripts/build.py go test ./...`: 后端与 CGO 原生引擎回归测试全部通过


