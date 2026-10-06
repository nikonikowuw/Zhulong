# 实施计划与步骤：前端 UI 交互增强与监控渲染性能优化

## 阶段一：Live 实时监控与播放器交互增强

- [ ] **Step 1.1: 完善键盘快捷键系统 (`LiveDashboard.tsx`)**
  - 支持数字键 `1`, `4`, `9` 快速切换宫格模式。
  - 增加输入框/聚焦元素防误触守卫。
  - 编写快捷键单元测试 (`LiveDashboard.test.tsx`)。
- [ ] **Step 1.2: 实现视口画面快照截图与 Telemetry 控制 (`LivePlayer.tsx`, `LiveViewport.tsx`)**
  - 在 `LivePlayer` 内部/回调实现 Canvas 快照捕获逻辑并下载 PNG。
  - `LiveViewport` 悬浮工具栏新增快照截图按钮与 Telemetry 开关按钮。
  - 更新多语言词条（英/简/繁）。
  - 编写快照与工具栏组件交互测试。
- [ ] **Step 1.3: WebCodecs 帧渲染 RAF 节流调度优化 (`useLiveStream.ts`)**
  - 接入 `requestAnimationFrame` 调度 Canvas 绘制。
  - 保证非消耗帧或丢弃帧的 `frame.close()` 严格执行。

## 阶段二：摄像机资产列表搜索、多维筛选与排序

- [ ] **Step 2.1: 实现筛选与排序 Hook (`useCameraFilter.ts`)**
  - 编写输入搜索匹配、健康状态过滤与排序纯计算逻辑。
  - 编写 `useCameraFilter.test.ts` 单元测试。
- [ ] **Step 2.2: 实现搜索与筛选工具栏组件 (`CameraFilterBar.tsx`)**
  - 搜索输入框（带清除图标与快捷重置）。
  - 状态筛选 Pills（全部/在线/离线/异常）。
  - 排序下拉选择框。
  - 补齐多语言词条（英/简/繁）。
- [ ] **Step 2.3: 实现友好空状态组件 (`CameraEmptyState.tsx`)**
  - 搜索结果为空时的提示与「清除筛选」按钮。
- [ ] **Step 2.4: 在 `CameraPage.tsx` 中集成并验证**
  - 替换原有简单的列表渲染，接入 `useCameraFilter`、`CameraFilterBar` 与 `CameraEmptyState`。
  - 编写组件集成测试 (`CameraPage.test.tsx`)。

## 阶段三：全面检验与质量门禁

- [ ] **Step 3.1: 静态检查与无 `any` 强类型验证**
  - 运行 `npm run lint`。
  - 运行 `npm run type-check`。
- [ ] **Step 3.2: 运行完整自动化测试套件**
  - 运行 `npm run test`，确保所有测试 100% 通过。
- [ ] **Step 3.3: 运行生产构建验证**
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
