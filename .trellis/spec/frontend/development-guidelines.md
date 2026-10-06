# React 前端编码铁律与开发指南

> 所有者 11 条前端编码铁律、状态管理职责划分与强类型规范。

---

## 所有者 11 条编码铁律 (11 Core Rules)

1. **纯函数组件与 Hooks**：严禁使用 Class 类组件。
2. **单一职责与按需拆分**：组件必须专注于单一 UI 职责。当出现独立子视图块（如复杂表单分区、多态弹窗区块）时按需拆分子组件，杜绝基于机械行数的生硬切片。
3. **渲染与逻辑彻底分离**：组件只做 UI 渲染；数据请求、复杂计算全部抽取至自定义 Hook。
4. **插槽组合优先于布尔配置**：优先使用 `children` 与 Slot Props（如 `<Card header={<Header />}>{children}</Card>`），避免堆砌 10 个布尔 Prop。
5. **TanStack Query 独占服务端状态**：API 数据 100% 由 TanStack Query 管理，**严禁复制进 Zustand/Redux**；Zustand 仅在有跨组件纯 UI 状态（如侧边栏折叠）时极克制使用。
6. **状态就近与渲染派生**：衍生数据直接在渲染期间计算，**严禁使用 `useEffect` 监听源数据并 `setState`**。
7. **严禁 `any`**：开启 TypeScript Strict；ESLint 拦截所有显式 `any`；未知输入使用 `unknown` + Zod 校验。
8. **拒绝无证据的过早优化**：严禁在无 React Profiler 性能卡顿证据前滥用 `memo`, `useMemo`, `useCallback`。
9. **列表 Key 必须业务唯一**：循环渲染必须使用后端全局唯一 ID（`key={cam.id}`），**严禁使用数组索引 `index` 作为 key**。
10. **语义化 HTML 与无障碍**：交互元素优先使用 `<button>`，严禁在无语义 `<div />` 上加 `onClick`；弹窗必须支持 `Esc` 退出与 Tab 循环。
11. **异步交互必须处理三态**：API 交互必须完备处理并展示：**Loading（加载中）**、**Error（失败重试）** 与 **Empty（空数据引导）**。

---

## 2. 实时视频监控与播放器最佳实践 (Live Surveillance & Media UX)

1. **WebCodecs 帧渲染节流与显存释放**：
   - 原生解码帧绘制应通过 `requestAnimationFrame` 调度；
   - 若前一帧未消耗即迎来新帧，必须在抛弃前调用 `oldFrame.close()` 释放 GPU 纹理与底层句柄；
   - 避免在每帧 `onFrame` 中高频调用 React `setState`，仅在分辨率或格式变更时派发状态更新。
2. **全局快捷键防误触守卫**：
   - 监控中心快捷键（如 `1` / `4` / `9` 宫格切换、`Esc` 退出全屏）必须判定 `document.activeElement`，严禁在 `input`, `textarea`, `select` 等聚焦环境下误触触发。
3. **列表客户端筛选与双态空提示**：
   - 客户端模糊检索与状态筛选计算逻辑必须抽取为自定义 Hook，并在渲染期间纯粹派生；
   - 必须明确区分「无设备添加（引导创建）」与「筛选无匹配结果（提供清除筛选重置按钮）」两类空状态。

---

## 3. 管理控制台布局与工业级 UI 规范 (Console Layout & Industrial UI Standards)

1. **控制台框架结构 (Console Architecture)**：
   - 采用标准管理系统架构：**左侧侧边栏 (`Sidebar`) + 顶部控制台顶栏 (`ConsoleTopbar`) + 全宽流式工作区 (`Fluid Workspace`)**。
   - 严禁使用 Landing Page 营销官网式的浮动光斑（如高斯模糊光球动效）、大号居中宣讲标语或 1200px 最大宽容器限制。
2. **多屏与大视口适配 (Viewport Utilization)**：
   - 工作区宽度应占满可用水平视口（`width: 100%`），为监控多路分屏矩阵（Live Grid）和设备拓扑列表预留充足展示空间。
   - 侧边栏必须支持一键展开与折叠（Icon-only 紧凑栏），持久化偏好至本地存储，并在小屏/移动设备提供抽屉（Drawer）交互。
3. **系统概览数据密度 (Dashboard Information Density)**：
   - 概览页（Overview）应以管理仪表盘呈现：涵盖关键运行指标卡片（设备资产总览、在线率、实时流传输路数、AI/硬件加速引擎状态、服务健康度）与运维快速通道。
