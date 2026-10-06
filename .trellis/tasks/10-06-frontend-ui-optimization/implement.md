# 实施计划与步骤：前端 UI 交互增强与监控渲染性能优化

## 阶段一：Live 实时监控与播放器交互增强

- [x] **Step 1.1: 完善键盘快捷键系统 (`LiveDashboard.tsx`)**
  - 支持数字键 `1`, `4`, `9` 快速切换宫格模式。
  - 增加输入框/聚焦元素防误触守卫。
  - 编写快捷键单元测试 (`LiveDashboard.test.tsx`)。
- [x] **Step 1.2: 实现视口画面快照截图与 Telemetry 控制 (`LivePlayer.tsx`, `LiveViewport.tsx`)**
  - 在 `LivePlayer` 内部/回调实现 Canvas 快照捕获逻辑并下载 PNG。
  - `LiveViewport` 悬浮工具栏新增快照截图按钮与 Telemetry 开关按钮。
  - 更新多语言词条（英/简/繁）。
  - 编写快照与工具栏组件交互测试。
- [x] **Step 1.3: WebCodecs 帧渲染 RAF 节流调度优化 (`useLiveStream.ts`)**
  - 接入 `requestAnimationFrame` 调度 Canvas 绘制。
  - 保证非消耗帧或丢弃帧的 `frame.close()` 严格执行。

## 阶段二：摄像机资产列表搜索、多维筛选与排序

- [x] **Step 2.1: 实现筛选与排序 Hook (`useCameraFilter.ts`)**
  - 编写输入搜索匹配、健康状态过滤与排序纯计算逻辑。
  - 编写 `useCameraFilter.test.ts` 单元测试。
- [x] **Step 2.2: 实现搜索与筛选工具栏组件 (`CameraFilterBar.tsx`)**
  - 搜索输入框（带清除图标与快捷重置）。
  - 状态筛选 Pills（全部/在线/离线/异常）。
  - 排序下拉选择框。
  - 补齐多语言词条（英/简/繁）。
- [x] **Step 2.3: 实现友好空状态组件 (`CameraEmptyState.tsx`)**
  - 搜索结果为空时的提示与「清除筛选」按钮。
- [x] **Step 2.4: 在 `CameraPage.tsx` 中集成并验证**
  - 替换原有简单的列表渲染，接入 `useCameraFilter`、`CameraFilterBar` 与 `CameraEmptyState`。
  - 编写组件集成测试 (`CameraPage.test.tsx`)。

## 阶段三：管理控制台布局与组件重构 (`app`)

- [x] **Step 3.1: 创建管理系统侧边栏组件 (`Sidebar.tsx`)**
  - 支持品牌展示（Logo、Zhulong Console 控制台标号）。
  - 支持菜单导航（系统概览、实时监控、设备管理）。
  - 支持展开/收起（折叠至 64px 图标栏），支持 `localStorage` 记住偏好。
  - 支持移动端汉堡菜单抽屉模式与遮罩交互。
  - 侧边栏底部包含运行环境标识与折叠按钮。
- [x] **Step 3.2: 重构顶部控制台操作栏 (`ConsoleTopbar.tsx` / `App.tsx`)**
  - 整合当前模块面包屑、实时状态徽章（如总设备数/在线数）。
  - 集成全屏切换、全局刷新、多语言切换、主题切换、用户面板。
- [x] **Step 3.3: 重构工作区容器为全宽自适应流式布局 (`FluidWorkspace`)**
  - 废弃 `max-width: 1200px` 限制，使监控大屏和数据列表占满可用空间。
  - 适配移动端与桌面端自适应滚动。

## 阶段四：系统概览重构为管理仪表盘 (`OverviewDashboard.tsx`)

- [x] **Step 4.1: 开发概览核心指标卡片 (Metric Cards)**
  - 设备接入与在线率指标（总数、在线、离线）。
  - 活跃流媒体与并发管道指标。
  - AI NPU/推理加速状态指标。
  - 系统服务就绪度指标。
- [x] **Step 4.2: 整合组件健康与快捷操作入口**
  - 嵌入并升级现有 HealthPanel（Host/DB/Engine 运行状态）。
  - 增加快捷操作入口（直达监控视盘、添加摄像头、推流网络诊断）。
- [x] **Step 4.3: 补齐仪表盘与控制台多语言词条**
  - 更新 `en.json`、`zh-Hans.json`、`zh-Hant.json`。

## 阶段五：样式清理与视觉去营销化

- [x] **Step 5.1: 彻底清理 `styles.css`**
  - 移除 `.ambient-background` 与 4 个 `.ambient-orb` 的 CSS 及关键帧动画。
  - 优化控制台主色彩规范，强化工业/管理系统边界线与对比度。
  - 移除冗余的营销式大标题与展示页副标题样式。
- [x] **Step 5.2: 优化各子页面（Live / Camera）在全宽下的自适应呈现**
  - 确保 Live 监控多宫格在全宽下自适应铺满。
  - 确保 Camera 资产列表在全宽下排版合理。

## 阶段六：全面检验与质量门禁

- [x] **Step 6.1: 静态检查与无 `any` 强类型验证**
  - 运行 `npm run lint`。
  - 运行 `npm run type-check`。
- [x] **Step 6.2: 运行完整自动化测试套件**
  - 编写/更新新布局组件与 Dashboard 测试。
  - 运行 `npm run test`，确保所有测试 100% 通过。
- [x] **Step 6.3: 运行生产构建验证**
  - 运行 `npm run build`，确保 Vite 生产构建无报错。

## 验证命令与检查点

```bash
cd web
npm run lint
npm run type-check
npm run test
npm run build
```

## 回滚策略
- 如遇到任何破坏性变更或测试失败，使用 git 恢复对应模块。
