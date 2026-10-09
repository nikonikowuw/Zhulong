# 执行计划：前端实时预览能力与多分屏播放器集成

## 1. 实施阶段分解 (Implementation Phases)

### Phase 1: 基础契约、类型与单例流连接池
- [x] **Step 1.1**: 创建 `web/src/features/live/types/index.ts`，定义分屏布局模式（1/4/9/16）、单元格状态、摄像机项、流连接状态与统计指标接口。
- [x] **Step 1.2**: 实现客户端单例流连接池 `web/src/features/live/services/stream-pool.ts`，支持基于 `cameraId` 的多订阅者广播与引用计数（$0 \rightarrow 1$ 建连，$N \rightarrow 0$ 延时销毁）。
- [x] **Step 1.3**: 实现 `useStreamConnection` Hook，封装组件生命周期与连接池订阅同步。

### Phase 2: 播放器核心封装与自适应降级
- [x] **Step 2.1**: 集成 Jessibuca 静态资源与动态加载服务 `web/src/features/live/services/jessibuca-loader.ts`，支持 WebCodecs / MSE / WASM 三级解码模式。
- [x] **Step 2.2**: 实现 `web/src/features/live/components/jessibuca-player.tsx`，具备 DOM 容器自适应、生命周期显存释放及错误捕获。
- [x] **Step 2.3**: 实现 `web/src/features/live/components/mock-stream-canvas.tsx` 动态 SMPTE 彩条与毫秒级时钟测试图卡，保证在无真实视频流时的离线闭环调试能力。
- [x] **Step 2.4**: 实现 `web/src/features/live/components/live-stream-stats.tsx` 悬浮 OSD 监视浮窗（FPS、分辨率、码率、硬解/软解标识）。

### Phase 3: 多画面网格与交互控制
- [x] **Step 3.1**: 实现 `useLiveGrid` 状态机 Hook，管理 1/4/9/16 宫格状态、当前激活窗口、全屏聚焦切换及单元格与摄像机映射。
- [x] **Step 3.2**: 实现 `useLiveShortcuts` 快捷键守卫 Hook，支持 `1`/`4`/`9`/`Esc` 键，严格在输入框聚焦态静默屏蔽。
- [x] **Step 3.3**: 实现单个分屏单元 `web/src/features/live/components/live-player-cell.tsx`，完备处理空窗、加载、错误与播放状态，集成截图、静音、单窗放大等操作。
- [x] **Step 3.4**: 实现摄像机侧边栏 `web/src/features/live/components/live-camera-sidebar.tsx`，展示设备状态并支持一键分配至当前窗口。
- [x] **Step 3.5**: 实现顶部控制栏 `web/src/features/live/components/live-toolbar.tsx` 与网格排布容器 `web/src/features/live/components/live-grid.tsx`。

### Phase 4: 页面聚合、路由注册与导航集成
- [x] **Step 4.1**: 编写 `web/src/features/live/index.tsx` 聚合页面，整合大屏流式视口。
- [x] **Step 4.2**: 创建 TanStack Router 强类型路由 `web/src/routes/_authenticated/live/index.tsx`。
- [x] **Step 4.3**: 在 `web/src/components/layout/data/sidebar-data.ts` 中注册实时预览导航项，使用 `Monitor` 图标并配置快捷入口及多语言词条。

### Phase 5: 验证与质量门禁
- [x] **Step 5.1**: 运行 `pnpm lint`，确保严格零 `any`、无未使用变量。
- [x] **Step 5.2**: 运行 `pnpm build`（`tsc -b && vite build`），验证路由树生成与强类型编译无报错。
- [x] **Step 5.3**: 逐项核验 AC-1 ～ AC-6 验收标准。

---

## 2. 验证命令集 (Validation Commands)

```bash
# 1. 切换至前端工作目录
cd web

# 2. TypeScript 静态检查与 Vite 构建
pnpm build

# 3. ESLint 规范检验
pnpm lint

# 4. 代码格式一致性检查
pnpm format:check

# 5. Vitest 测试
pnpm vitest run src/features/live
```
