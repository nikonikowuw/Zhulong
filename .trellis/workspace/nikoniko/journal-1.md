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

---

## 2026-10-06 边缘异构系统网络配置与两阶段安全回滚模块规划

**Task**: `10-06-system-network-config`
**Status**: Planning
**Package**: `backend`
**Branch**: `dev`

### Summary

针对面向极端边缘异构 Linux 设备（如 RK3588、Jetson、昇腾及通用工控机）的烛龙（Zhulong）应用，通过 `/grilling` 深入推演并完成了宿主机网络配置管理模块的完整规划。方案确立了底层 Linux 内核 Netlink/sysfs 跨平台状态读取、主流网络栈（NetworkManager / systemd-networkd / 自定义 Hook 脚本）自适应探测、防掉电两阶段回滚看门狗事务、跨 IP 一次性免密快速确认、单默认网关约束以及极端环境 CLI `--reset-network` 紧急救援机制。

### Main Changes

- **需求制定 (`prd.md`)**：确立纯操作系统网卡层边界，定义物理网卡过滤、当前访问网卡标记 (`is_current`)、单默认网关互斥、60s 回滚看门狗、一次性确认令牌与 CLI 紧急恢复验收标准。
- **技术设计 (`design.md`)**：
  - 设计 `internal/network` 模块架构与 `NetworkProvider` 接口驱动体系；
  - 设计基于 `<data-dir>/network_transaction.json` 的防掉电落盘状态机，在 Fx `OnStart` 生命周期执行开机自检与未确认事务自动回滚；
  - 设计延时异步生效与携带 `confirm_token` 的跨 IP 免密快速确认协议；
  - 设计 React 前端「系统设置」(`#settings`) 一级菜单与网络二级卡片看板。
- **实施计划 (`implement.md`)**：分为后端底层与 Provider、看门狗与服务编排、REST API 与 CLI 救援、前端界面与交互、全链路验证 5 个阶段。
- **上下文配置**：建立并通过 `implement.jsonl` (8 条规范) 与 `check.jsonl` (3 条质量规范) 上下文核验。

### Status

[OK] **Planning Completed**






## Session 10: 前端 UI 交互增强与监控渲染性能优化收尾
<!-- trellis-session: v=2 fp=876c9713d3e19736 -->

**Date**: 2026-10-06
**Task**: 前端 UI 交互增强与监控渲染性能优化收尾
**Package**: frontend
**Branch**: `dev`

### Summary

完成并归档前端 UI 优化任务，覆盖实时监控快捷操作与截图、摄像机筛选、控制台布局及仪表盘；补齐 PRD 验收状态。已有回归记录显示 lint、type-check、Vitest 与生产构建均通过。

### Git Commits

| Hash | Message |
|------|---------|
| `8de89cd` | feat(web): implement LivePlayer, FrontendStreamPool, and 1/4/9 live dashboard |
| `277f441` | feat(web): optimize live player controls, shortcuts, and camera list filters |
| `a5bb031` | feat(web): redesign layout to professional management console and add dashboard |
| `703d475` | refactor(web): move user nav and logout action from topbar to sidebar bottom |
| `260c1a1` | refactor(web): remove obsolete mock footer text and streamline system info |
| `8ec913f` | fix(web): enforce app viewport lock so topbar and sidebar stay fixed |
| `99037f1` | refactor(web): remove fake status card and redundant tags from sidebar |
| `c90e5b1` | fix(web): enforce standard 16:9 aspect-video ratio for live surveillance viewports |

### Status

[OK] **Completed**

---

## 2026-10-06 实时预览 UI 升级：媒体设备树与多分屏工作区

**Task**: `10-06-live-device-tree-layout`
**Status**: Completed
**Package**: `frontend`
**Branch**: `dev`

### Summary

完成实时监控页面交互升级为经典 VMS 架构：左侧固定全高媒体设备树（支持通道状态感知、模糊搜索与通道点播/拖拽）；右侧多分屏监控工作区。彻底移除了多余的模态弹窗、发光高亮边框和折叠按钮，支持纯粹的 HTML5 拖拽与单节点顺序填槽交互（满屏自动回绕至槽位 0），并在分屏模式切换时保持真实槽位编号。

### Main Changes

- **组件扩展**：新增 `LiveDeviceTreePanel`、`DeviceTreeNode`、`StreamTreeNode`，支持设备树渲染、实时在线统计与关键词搜索；
- **拖拽与点击点播**：实现基于 `application/json` 的 HTML5 原生拖拽与视口 Drop Target，支持通道直接拖入任意视口；
- **自顺延与槽位保持**：重构 `useLiveLayout`，支持单击通道自动顺延分配与满屏回绕至槽位 0，单屏模式保留当前聚焦槽位编号；
- **纯粹监控体验**：彻底移除空视口加号/弹窗诱导，移除扎眼的发光高亮环与多余的折叠按钮；
- **国际化与测试覆盖**：更新中英繁三语词条，编写并验证 28 个测试文件、108 个测试用例。

### Git Commits

| Hash | Message |
|------|---------|
| `612168a` | feat(web): implement live surveillance device tree with drag-and-drop and sequential loading |

### Status

[OK] **Completed**



## Session 11: 轻量级系统操作与安全审计日志模块全栈实现与代码规范精简
<!-- trellis-session: v=2 fp=b8639d53b1006d92 -->

**Date**: 2026-10-07
**Task**: 轻量级系统操作与安全审计日志模块全栈实现与代码规范精简
**Package**: backend
**Branch**: `dev`

### Summary

完成轻量级系统操作与安全审计日志模块全栈实现：涵盖 SQLite 迁移、后台异步批量写入与 5000 条 FIFO 淘汰、管理员认证与摄像头运维埋点、前端数据视图与多语言适配，并完成代码规范精简与全量测试验证。

### Main Changes

- 后端核心：新增 internal/audit 模块，支持 Channel 缓冲异步写入、定期批量持久化与 5000 条 FIFO 自动淘汰
- 审计埋点：在 auth 模块（init/login/logout）与 camera 模块（create/update/delete/toggle）接入安全审计事件并对敏感 URL 脱敏
- 前端特性：新增 features/audit 模块（结构化表格、多重筛选、详情展开、清空日志）并集成至主导航与全局哈希路由
- 规范精简：消除 App.tsx 与 AuditLogTable.tsx 中深层嵌套三元，优化 React Compiler 适配，提炼 Go 重复构建样板
- 任务规划：创建并提交网络配置、存储生命周期与对时服务三个后续规划任务的 PRD、方案与执行计划

### Git Commits

| Hash | Message |
|------|---------|
| `3a6a7ea` | feat(audit): implement lightweight system operation and security audit log |
| `8923983` | docs(task): record planning artifacts for system network, storage, and time services |

### Testing

- [OK] Go 全模块无缓存测试：python3 native/scripts/build.py go test -count=1 ./internal/... (全部通过)
- [OK] 前端静态检查与测试：npm run lint && npm run type-check && npm test (34 文件 124 测试全部通过)

### Status

[OK] **Completed**


## Session 12: 全面重构 Uber Fx 依赖注入与生命周期治理架构
<!-- trellis-session: v=2 fp=e8361872bee2617e -->

**Date**: 2026-10-07
**Task**: 全面重构 Uber Fx 依赖注入与生命周期治理架构
**Package**: backend
**Branch**: `dev`

### Summary

重构应用启动装配为模块化 Uber Fx 依赖图，统一 database.DBProvider 契约，解耦生命周期运行时状态机，完成代码规范精简与全量测试验证

### Main Changes

- 契约重构：定义 database.DBProvider 接口，改造 auth、camera、audit 模块 Store 构造函数，消灭松散的 func() *gorm.DB 闭包与运行时误调用风险
- 模块化解耦：拆解 internal/app/app.go 巨石 newServices 函数，重构为独立命名 fx.Module（databaseModule, engineModule, auditModule, authModule, cameraModule, httpModule, runtimeModule）
- 生命周期与回滚：lifecycleRuntime 采用 runtimeParams (fx.In) 解耦，保持严格启动顺序与逆序级联回滚机制
- 规范精简：收敛 cameraOut 暴露面并增强 nil 保护，统一三模块 Store 单测的表驱动 unready/nil 断言

### Git Commits

| Hash | Message |
|------|---------|
| `cdfc44f` | refactor(app): modularize uber fx architecture and domain db provider |

### Testing

- [OK] make go-check (gofmt, go vet, -race 并发测试, C++ RTSP 集成测试全量通过)
- [OK] npm run type-check --prefix web && npm test --prefix web (前端 124 单元测试全部通过)
- [OK] python3 native/scripts/build.py go build ./cmd/Zhulong (链接构建成功)

### Status

[OK] **Completed**


## Session 13: 边缘网络配置UI重构优化与Core目录忽略规则修复
<!-- trellis-session: v=2 fp=70de8f56c384357d -->

**Date**: 2026-10-08
**Task**: 边缘网络配置UI重构优化与Core目录忽略规则修复
**Package**: backend
**Branch**: `dev`

### Summary

完成边缘网络配置全链路交付与UI体验深化优化：补齐深浅色设计系统语义Token，升级硬件状态灯卡片与多IP展开折叠，实现表单失焦即时校验与无障碍焦点管理，强化看门狗两阶段回滚紧迫度交互与骨架屏；同时排查修复.gitignore中误杀core源码目录的规则缺陷，恢复并跟踪live流处理核心模块。

### Git Commits

| Hash | Message |
|------|---------|
| `9b35f7a` | feat(network): implement host network configuration with two-phase rollback watchdog |
| `970edbf` | fix(network): ensure non-nil slice serialization for network interfaces and rollback state |
| `609c4f9` | feat(web): enhance network settings UI with hardware card, inline validation, and skeleton loading |
| `d3613f5` | fix(build): refine core dump gitignore pattern to avoid ignoring source core directories |
| `206d18d` | feat(live): track live surveillance stream core modules and wire parser |

### Status

[OK] **Completed**


## Session 14: 系统单用户登录与初始化向导实现
<!-- trellis-session: v=2 fp=d6fb3c9d68477aa0 -->

**Date**: 2026-10-09
**Task**: 系统单用户登录与初始化向导实现
**Package**: backend
**Branch**: `dev`

### Summary

基于 shadcn-admin 与 Zhulong 后端单用户认证 API，完成首屏系统初始化探测、管理员初始化向导（InitForm）、常规密码登录（LoginForm）、路由鉴权守卫（_authenticated beforeLoad）及状态保持（auth-store）的全链路交付与精简重构；补充密码强度指示器、边缘暗夜机房设计语言与单元测试用例。

### Git Commits

| Hash | Message |
|------|---------|
| `d6ab440` | feat(auth): implement single-user authentication and edge setup wizard |

### Status

[OK] **Completed**


## Session 15: 前端实时预览能力与多分屏播放器集成
<!-- trellis-session: v=2 fp=8d27f271a9610588 -->

**Date**: 2026-10-09
**Task**: 前端实时预览能力与多分屏播放器集成
**Package**: frontend
**Branch**: `dev`

### Summary

基于 Jessibuca 与单例流连接池实现前端实时流预览大盘，支持 1/4/9/16 宫格自适应切换、全屏聚焦、离线测试图卡及快捷键防误触，并按照 simplify 规范完成代码精简重构；同时将 LanguageSwitch 多语言切换器挂载至全站主要功能页 Header。

### Main Changes

- 集成 Jessibuca 播放器与离线 SMPTE 彩条测试图卡，支持 WebCodecs/MSE/WASM 三级自适应解码
- 实现客户端单例流连接池 StreamConnectionPool，保证多窗口同摄像机复用一条 WebSocket 连接与引用计数自愈
- 实现 1/4/9/16 宫格布局与 useLiveGrid/useLiveShortcuts 状态机，支持单窗放大与表单态防误触
- 挂载 /live 独立路由并在 sidebar-data 中注册导航与三语翻译
- 基于 simplify 规范重构代码：消除深层嵌套三元运算符、提取静态常量表与统一 WebSocket URL 生成逻辑

### Git Commits

| Hash | Message |
|------|---------|
| `385299a` | feat(i18n): add LanguageSwitch to header across dashboard, apps, chats, tasks and users pages |
| `4e6b0f3` | feat(live): implement multi-cell live surveillance preview with jessibuca player and stream pool |

### Testing

- [OK] pnpm lint 静态检查 0 报错
- [OK] pnpm build (tsc -b && vite build) 生产编译打包成功
- [OK] pnpm vitest run src/features/live 全部 4 个测试套件 11 个用例全绿通过

### Status

[OK] **Completed**

### Next Steps

- 推进前端摄像头资产管理与状态大盘 (10-09-frontend-camera-management) 实现


## Session 16: 前端摄像头资产管理与状态大盘实现
<!-- trellis-session: v=2 fp=ca816999b1efd630 -->

**Date**: 2026-10-09
**Task**: 前端摄像头资产管理与状态大盘
**Package**: frontend
**Branch**: `dev`

### Summary

基于 satnaing/shadcn-admin 控制台规范实现高密度摄像机资产管理模块（`features/cameras`），挂载在 `/_authenticated/cameras` 强类型文件路由，包含 TanStack Table 数据大盘、URL 状态双向同步、新增/编辑/诊断/凭据查看弹窗集合、CAS revision 乐观锁、实时连通性探测等待遮罩、SSE 状态流就地局部更新与英/简/繁三语国际化支持。同时攻克了 Table 操作列在横向滚动时的双层伪元素无缝悬浮架构，彻底杜绝线框与 hover 透明穿透缺陷。

### Main Changes

- **契约与 API 基础设施**：编写 Zod Schema、REST API 客户端（CRUD/凭据/诊断）与 TanStack Query 钩子；挂载 `/api/v1/cameras/events` SSE 实时事件更新
- **三语国际化与导航注册**：在 `cameras` 命名空间提取三语字典并在 `sidebar-data.ts` 与 `common.json` 挂载导航项
- **高密度数据大盘与列定义**：按 `[选择] -> [设备 ID] -> [摄像机名称] -> [RTSP 流地址] -> [状态] -> [主流规格] -> [子流规格] -> [启用开关] -> [更新时间] -> [操作]` 排布；支持明文可用 RTSP 流一键复制
- **工业级 Sticky 悬浮操作列**：采用底层 100% 实心 `bg-card` 垫底 + 绝对定位伪元素淡入叠色的双层背景架构，消灭垂直竖线，确保 hover 颜色与整行完全一致且 0 透明穿透
- **对话框交互闭环与 shadcn-admin 原味重塑**：彻底剥离粗糙杂色嵌套卡片，按照 shadcn-admin 官方原味重塑创建/编辑与一键诊断弹窗；删除冗余的凭据查看弹窗；消除全量硬编码，补全三语翻译并修复 session 键名缺失
- **复选批量操作菜单与高危删除对齐**：完全对齐 `shadcn-admin`（tasks/users）官方批量操作浮条规范，提供 DropdownMenu 状态批量变更下拉菜单、批量诊断探针、内聚型高危二次确认弹窗（输入 `DELETE` 校验 + 破坏性 Warning Alert）与勾选自动重置
- **公共组件国际化脱敏（DataTableBulkActions & DataTableViewOptions）**：攻克底层通用批量操作栏中的硬编码英文 `' selected'` 与取消按钮硬编码文本，抽象并引入 `table` 与 `entities` 国际化字典体系；重构表格右上角 `View` 按钮与列显隐下拉菜单（`DataTableViewOptions`），实现按钮文案、菜单标头（Toggle columns）及各列选项标题的双重智能探测（显式 `columnLabels`、`meta.title` 与 i18n 候选键），彻底消灭列选项驼峰字段暴露与未翻译问题
- **表单交互与弹窗尺寸稳定性加固（CamerasActionDialog）**：修复提交触发探测时输入框因全量注入 `disabled={isPending}` 导致的 50% opacity 半透明劣化问题；将底层按钮文本从 25 个字的超长提示（`正在连接摄像机并探测音视频流规格...`）解耦为简洁精悍的 `正在探测...`（`min-w-24`），并将长耗时探测说明剥离至 Footer 侧边指示区，锁定 `w-full sm:max-w-lg` 几何尺寸，彻底消除提交过程中的弹窗突变撑宽与输入框变形跳动
- **规范回流**：将 Table Sticky 操作列双层伪元素防穿透最佳实践沉淀进 `.trellis/spec/frontend/development-guidelines.md`

### Testing

- [OK] `pnpm format:check` 代码风格 100% 合规
- [OK] `pnpm lint` 静态检查 0 报错，零 any
- [OK] `pnpm test` 全站 39 个测试套件 196 个用例全部通过
- [OK] `pnpm build` (tsc -b && vite build) 生产编译打包成功

### Status

[OK] **Completed**

## [2026-10-10 10:45] 边缘异构系统对时服务与硬件时钟同步 (10-07-system-time-service)

为面向极端边缘异构 Linux 设备（RK3588、NVIDIA Jetson、华为昇腾及工控机）提供工业级全链路系统对时与硬件 RTC 时钟同步能力。

### Main Changes

- **跨平台驱动抽象 (`ClockDriver`)**：
  - Linux 环境下通过系统调用 `clock_settime`（阶跃 Step）、`adjtimex`（频率平滑追赶 Slew，保护流媒体 PTS/DTS 单调性）、`/dev/rtc*` ioctl（读写 UTC RTC 硬件芯片）与原子软链接 `/etc/localtime` 驱动系统时区热重载；
  - 非 Linux / 开发机（macOS / CI）提供纯 Go 内存桩驱动 `StubClockDriver`，实现 100% 编译与单元测试解耦。
- **Go 原生 SNTP 客户端引擎 (`sntp.go`)**：
  - 纯 Go 实现 RFC 4330 SNTPv4 报文解析与收发，精确计算 RTT 与 ClockOffset（微秒级精度）；
  - 支持多上游服务器顺序降级探测与指数退避机制。
- **两阶段时钟安全状态机 (`statemachine.go`)**：
  - 冷启动状态：解除 Panic 门限，允许任意大偏差无条件 Step 恢复；
  - 稳态阶段：$|Offset| < 500\text{ms}$ 严格走 `adjtimex` 线性平滑追赶，绝不倒拨系统时钟；$|Offset| \ge 10\text{min}$ 触发连续 3 次探测采样复核，防止假时钟源暴冲。
- **断电开机自愈与数据持久化**：
  - SQLite 数据库版本化迁移脚本 `000005_create_system_time_configs`；
  - 集成 Uber Fx 生命周期：`OnStart` 阶段自检系统时间，当系统时钟落后于 `2026-01-01` 且硬件 RTC 有效时，自动从板载 RTC 同步并拉齐系统时钟，同时联动记录安全审计日志；`OnStop` 优雅停止轮询协程。
- **RESTful API 与 Swaggo 2.0 文档**：
  - 提供 `GET /api/v1/system/time`、`PUT /api/v1/system/time/config`、`POST /api/v1/system/time/sync`、`POST /api/v1/system/time/manual` 接口，严格遵循 `{code: "OK", message, data}` 信封规范并生成 Swagger 文档。
- **React 控制台前端 (`features/settings/time`)**：
  - 挂载于系统设置二级菜单「系统对时」（`/_authenticated/settings/time`）；
  - 提供毫秒级走秒时钟、NTP 对时状态指示灯、硬件 RTC 状态灯以及 `CAP_SYS_TIME` 权限预警横幅；
  - 双模式表单：NTP 自动网络对时（动态服务器池增删与轮询周期）与手动对时（**「一键同步浏览器时间」高亮快捷按钮**与本地时间选择器）；
  - 全量 IANA 时区选择器与英/简/繁三语国际化支持。

### Testing

- [OK] `go test -v -race ./internal/systemtime/...`：21 个单元测试 100% 通过，竞态检测通过
- [OK] `python3 native/scripts/build.py go test -race ./cmd/... ./internal/...`：全量后端测试通过
- [OK] `make api-docs`：Swagger 文档成功同步
- [OK] `npm test --prefix web -- --run`：前端 44 个测试套件 213 个用例全部通过
- [OK] `npm run lint --prefix web`：ESLint 0 错误
- [OK] `npm run build --prefix web`：生产构建打包成功
- [OK] `make go-check` & `make native-test`：静态检查与 Native C++ RTSP 桥接测试全绿

### Status

[OK] **Completed**



## Session 17: 边缘对时全栈实现、代码精简与 shadcn 状态看板规范加固
<!-- trellis-session: v=2 fp=b78ac15df6826b00 -->

**Date**: 2026-10-10
**Task**: 边缘对时全栈实现、代码精简与 shadcn 状态看板规范加固
**Package**: backend
**Branch**: `dev`

### Summary

为边缘 Linux 异构系统实现 Go 原生 SNTP 客户端、两阶段时钟安全状态机、跨平台 ClockDriver 与 RTC 自愈服务；挂载主侧边栏菜单并重构为纯正 shadcn-admin 状态徽标与一键浏览器同步看板。

### Main Changes

- 实现 Go 原生 RFC 4330 SNTPv4 客户端与微秒级 RTT/Offset 计算，支持顺序降级
- 设计两阶段时钟安全状态机：冷启动 Step、稳态 Slew（adjtimex）、防暴冲复核机制
- Linux ClockDriver 跨平台驱动抽象（系统调用、ioctl /dev/rtc*、/etc/localtime 软链接）与 macOS/CI 内存桩
- 集成 Uber Fx 生命周期：开机 RTC 自动回溯拉齐、时区联动与审计日志
- 在主侧边栏与 Cmd+K 搜索挂载系统对时入口，重构状态 Badge 对齐 shadcn-admin 原生设计规范

### Git Commits

| Hash | Message |
|------|---------|
| `725db8a` | feat(time): implement edge system time service, rtc sync, and settings dashboard |
| `9b7199b` | chore(task): plan 10-10-native-hardware-frame pipeline design |

### Testing

- [OK] go test -v -race ./internal/systemtime/... 21 个单元测试与竞态检测全绿
- [OK] npm test --prefix web 前端 44 个套件 213 个测试全量通过
- [OK] make go-check && make native-test CTest 与 Go RTSP 桥接测试全绿
- [OK] npm run lint && npm run build 生产打包成功，0 报错 0 警告

### Status

[OK] **Completed**


## Session 18: 实现统一异构硬件帧对象与多算法零拷贝管线设计
<!-- trellis-session: v=2 fp=f900018f14e505c1 -->

**Date**: 2026-10-10
**Task**: 实现统一异构硬件帧对象与多算法零拷贝管线设计
**Package**: backend
**Branch**: `dev`

### Summary

完成了跨异构芯片（Rockchip/Ascend/Jetson/CPU）统一 HardwareFrame 帧对象抽象，支持有效几何与垂直步长计算，实现 1:N 多算法并发只读零拷贝共享与 RAII 缓冲池防死锁回收机制，全项通过 CTest/ASan/TSan 测试与 Spec 归档

### Git Commits

| Hash | Message |
|------|---------|
| `ffce0c8` | feat(native): implement unified HardwareFrame abstraction and 1:N zero-copy lifecycle |

### Status

[OK] **Completed**


## Session 20: 边缘异构系统媒体存储配置与动态生命周期管理全栈实现
<!-- trellis-session: v=2 fp=da9d72200e5ce8ed -->

**Date**: 2026-10-10
**Task**: 边缘异构系统媒体存储配置与动态生命周期管理全栈实现
**Package**: backend
**Branch**: `dev`

### Summary

为烛龙系统提供媒体存储配置管理、Linux VFS statfs 水位监控、st_dev 外挂盘掉线防穿透校验、双水位滞后回差流控清理、极限容量停录熔断以及 Web 端容量看板与生命周期配置

### Git Commits

| Hash | Message |
|------|---------|
| `38fe121` | feat(storage): implement edge media storage configuration, telemetry, and lifecycle engine |

### Status

[OK] **Completed**


## Session 21: Native 节点化解码器接入与流水线架构重构
<!-- trellis-session: v=2 fp=8e41bf162d057da1 -->

**Date**: 2026-10-10
**Task**: Native 节点化解码器接入与流水线架构重构
**Package**: native
**Branch**: `dev`

### Summary

完成 Native 流水线架构解耦，实现通用 `BoundedQueue` 拥塞淘汰队列，抽象 `IDecodeNode` 并交付单流确定性 `FFmpegDecodeNode` 软解实现，完成 `Stream` 内按需激活异步解码线程与极浅帧队列流转，更新 Spec 规范并通过全项 CTest/ASan/TSan/Go-Bridge 测试验证。

### Main Changes

- **通用有界队列**：实现 `BoundedQueue<T>`，支持 `BLOCK` / `DROP_OLDEST` / `DROP_NEWEST` 淘汰策略、超时等待与协作式取消 `cancel()`
- **流水线架构解耦**：将 `engine.cpp` 拆解为 `subscription`（回调排空与订阅通道）、`stream`（物理流拓扑、抓流与解码线程编排）以及精简的 `engine`（流池管理、Reaper 与探测）
- **解码抽象与 FFmpeg 节点**：抽象 `IDecodeNode`，实现 `FFmpegDecodeNode` 单流确定性软解，输出装配 `release_fn` 的 `HardwareFrame`
- **按需激活流转**：当接入 `Zhulong_CONSUMER_AI` 时动态拉起 `Stream::decode_loop`，无 AI 消费者时自动休眠并清空帧队列，避免 CPU/NPU 资源浪费
- **测试与验证**：新增 `ZhulongDecoderTests` 覆盖 H.264 / H.265 真实 Annex B 关键帧与普通帧解码、有界队列丢弃测试，全项通过 CTest、ASan、TSan 以及 Go Bridge 回归

### Git Commits

| Hash | Message |
|------|---------|
| `d4d8773` | feat(native): decouple pipeline architecture and integrate IDecodeNode with FFmpeg backend |

### Status

[OK] **Completed**

