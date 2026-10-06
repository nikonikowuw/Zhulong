# 技术设计方案：前端 UI 交互增强与监控渲染性能优化

## 1. 架构总览与分层设计

本次优化严格遵循 Feature-Sliced Design（FSD）分层契约与前端规范（无 `any`、服务端状态独占、组件单一职责）：

```txt
web/src/
├── features/
│   ├── live/
│   │   ├── components/
│   │   │   ├── LiveDashboard.tsx      # [增强] 1/4/9 快捷键监听、布局切换与全局快捷键守卫
│   │   │   ├── LiveViewport.tsx       # [增强] 顶部悬浮工具栏增加快照截图、Telemetry 显隐切换
│   │   │   ├── LivePlayer.tsx         # [增强] 支持 Snapshot 捕获接口、RAF 帧渲染节流调度
│   │   │   └── ...
│   │   └── hooks/
│   │       ├── useLiveStream.ts       # [增强] RAF 帧生命周期托管与显存防挤压
│   │       └── ...
│   ├── camera/
│   │   ├── components/
│   │   │   ├── CameraPage.tsx         # [集成] 组合 FilterBar 与空状态
│   │   │   ├── CameraFilterBar.tsx    # [新增] 搜索输入框、状态筛选 Pills、排序下拉菜单
│   │   │   ├── CameraEmptyState.tsx   # [新增] 搜索无结果空状态与一键重置
│   │   │   └── ...
│   │   └── hooks/
│   │       ├── useCameraFilter.ts     # [新增] 纯客户端状态筛选、搜索模糊匹配与排序计算 Hook
│   │       └── ...
```

---

## 2. 详细设计与实现契约

### 2.1 监控键盘快捷键系统 (`LiveDashboard.tsx`)
- **键盘监听器**：在 `window` 级别监听 `keydown` 事件。
- **守卫条件**：
  ```ts
  const isInputActive = ['INPUT', 'TEXTAREA', 'SELECT'].includes(
    (document.activeElement?.tagName || '').toUpperCase()
  );
  if (isInputActive) return;
  ```
- **映射规则**：
  - 按键 `'1'` -> `setMode(1)`
  - 按键 `'4'` -> `setMode(4)`
  - 按键 `'9'` -> `setMode(9)`
  - 按键 `'Escape'` -> 退出单视口放大全屏 (`fullscreenSlot !== null`)

### 2.2 视口快照截图 (Canvas Snapshot) 与 Telemetry 开关
- **快照截图原理**：
  - 由 `LiveViewport` 向 `LivePlayer` 发起快照触发，或通过 `LivePlayer` 暴露的句柄调用 `canvas.toBlob('image/png')`。
  - 生成对象 URL `URL.createObjectURL(blob)`，创建隐藏临时 `<a download="snapshot_{cameraId}_{timestamp}.png">` 触发本地下载。
  - 自动释放 Blob URL，捕获过程包含错误兜底提示。
- **Telemetry 显隐切换**：
  - 每个 `LiveViewport` 维护内部 `showTelemetry` 状态（默认 `true`），通过悬浮工具栏中的遥测按钮切换。

### 2.3 WebCodecs 帧渲染 RAF 节流调度 (`useLiveStream.ts` / `LivePlayer.tsx`)
- **帧丢弃与显存生命周期防护**：
  - 当视频解码速率达到 30fps/60fps 时，若屏幕刷新率未就绪，多余的同步帧绘制可能导致无谓的 DOM 消耗与重绘排队。
  - 接入 `requestAnimationFrame`：若前一帧尚未完成绘制，下一帧抵达时，若缓冲存在未消耗帧，必须先调用 `oldFrame.close()` 释放，保证内存与 GPU 纹理句柄不发生泄漏。

### 2.4 摄像机列表搜索与筛选 (`useCameraFilter.ts` + `CameraFilterBar.tsx`)
- **客户端筛选状态模型**：
  ```ts
  export type CameraHealthFilter = 'all' | 'online' | 'offline' | 'abnormal';
  export type CameraSortOption = 'name_asc' | 'name_desc' | 'health_priority' | 'updated_desc';

  export interface CameraFilterState {
    keyword: string;
    healthFilter: CameraHealthFilter;
    sortOption: CameraSortOption;
  }
  ```
- **状态筛选规则**：
  - `online`: `c.enabled && c.health === 'online'`
  - `offline`: `c.enabled && c.health === 'offline'`
  - `abnormal`: `c.enabled && (c.health === 'error' || c.degraded || c.stale)`
- **搜索规则**：
  - 匹配 `name.toLowerCase().includes(kw)` 或 `id.toLowerCase().includes(kw)`。
- **排序规则**：
  - `name_asc` / `name_desc`: 字符串拼音/字母比较。
  - `health_priority`: 异常优先 (abnormal > online > offline)。
  - `updated_desc`: 最新更新优先。

### 2.5 国际化支持 (i18n)
在 `features/live/locales/` 和 `features/camera/locales/` 中新增键值：
- `live.snapshot`: 快照截图
- `live.snapshotSuccess`: 截图已保存
- `live.toggleTelemetry`: 切换遥测指标
- `camera.filter.all`: 全部状态
- `camera.filter.online`: 在线
- `camera.filter.offline`: 离线
- `camera.filter.abnormal`: 异常/降级
- `camera.filter.searchPlaceholder`: 搜索摄像机名称或 ID...
- `camera.sort.nameAsc`: 名称 (A-Z)
- `camera.sort.nameDesc`: 名称 (Z-A)
- `camera.sort.health`: 状态优先
- `camera.emptySearchTitle`: 未找到匹配的摄像机
- `camera.emptySearchDesc`: 请尝试调整搜索关键词或重置筛选条件
- `camera.clearFilters`: 清除筛选

### 2.6 管理控制台架构与仪表盘设计 (`app` & `systemStatus`)
- **布局分层设计**：
  ```txt
  ConsoleLayout
  ├── Sidebar (固定左侧，width: 240px，折叠时 64px)
  │   ├── Brand (Logo + "Zhulong Console" + 折叠按钮)
  │   ├── NavMenu (概览 / 实时监控 / 设备管理)
  │   └── Footer (硬件运行环境与版本号、折叠切换)
  └── MainLayout (flex-1 flex-col min-w-0)
      ├── Topbar (粘性顶部，高 52px)
      │   ├── Left: 面包屑 / 页面标题 + 实时状态徽标 (如 "在线: 2/3")
      │   └── Right: 全屏按钮 + 刷新 + 语言 + 主题 + 用户
      ├── FluidWorkspace (flex-1 p-4 md:p-6 min-h-0 overflow-auto)
      │   └── Active Page (Overview / Live / Cameras)
      └── ConsoleFooter (简洁系统状态信息)
  ```
- **工作区自适应策略**：
  - 去除 `.workspace { max-width: 1200px; margin: 0 auto; }`。
  - 对于 `/live` 监控页面，提供接近 100% 宽度和自适应纵向高度的视口网格，支持侧边栏折叠以最大化监视空间。
  - 对于 `/cameras` 设备页面，支持流式自适应网格与搜索栏。
- **系统概览仪表盘 (Overview Dashboard)**：
  - 核心 KPI Cards：
    - 设备总数 / 在线率统计（总数、在线、离线、告警数）
    - 实时流传输与路数状态
    - NPU / AI 推理引擎就绪状态
    - 系统服务运行健康度
  - 健康度细节列表（Host / Database / Engine）
  - 快捷操作工具卡（直达多路监控、添加设备、一键诊断）
- **去营销化视觉设计**：
  - 彻底移除 `styles.css` 中的 `.ambient-background` 和 `.ambient-orb` 及其 keyframe 动效。
  - 采用现代专业控制台样式：清晰的边框、工控仪表风格的暗色/亮色对比度、微阴影、规整的网格间距。

---

## 3. 风险与降级措施

| 潜在风险 | 预防与解决手段 |
| :--- | :--- |
| **Canvas 跨域污损 (Tainted Canvas)** | 画面来自纯二进制 WebCodecs `VideoFrame` 直接绘制在同源 Canvas，不存在 CORS 跨域限制，`toBlob` 安全 |
| **内存/显存泄漏** | RAF 节流中如有未绘制的新旧帧替换，必须严格且立即调用 `frame.close()` |
| **快捷键与其他操作冲突** | 监听时严格判定 `document.activeElement`，输入框、Modal 激活状态下自动屏蔽 1/4/9 快捷键 |
| **移动端侧边栏遮挡** | 采用响应式设计：小屏幕（<768px）折叠为抽屉式（Drawer）覆盖层，点击外部背景自动关闭 |
| **大屏监控空间不足** | 侧边栏支持一键收起为紧凑图标栏（64px），实时监控视图自动撑满剩余宽度 |
