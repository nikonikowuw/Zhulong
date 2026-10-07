# 实施计划与步骤：实时预览 UI 升级

## 阶段一：Hook 与状态管理层升级

- [x] **Step 1.1: 扩展 `useLiveLayout.ts`**
  - 新增 `selectedSlotIndex` 状态管理与自动边界夹紧。
  - 新增 `selectSlot`、`assignToSelected`、`clearAllSlots` 方法。
  - 实现 `getPlayingSlot` 快速映射查找辅助函数。
  - 更新并编写 `useLiveLayout.test.ts` 单元测试。
- [x] **Step 1.2: 实现 `useDeviceTreeFilter.ts`**
  - 纯函数/派生 Hook：支持即时按名称、ID 过滤摄像机列表与展开节点集。
  - 编写 `useDeviceTreeFilter.test.ts` 单元测试。

## 阶段二：左侧媒体设备树组件族实现

- [x] **Step 2.1: 实现 `StreamTreeNode.tsx`**
  - 码流二级通道节点：展示码流角色（MAIN / SUB）、分辨率/帧率、推流运行状态。
  - 播放状态指示：若该流正在某个视口播放，展示 `[视口 N]` 徽章。
  - 点击/双击触发播放装载回调。
- [x] **Step 2.2: 实现 `DeviceTreeNode.tsx`**
  - 摄像机一级设备节点：展开/折叠三角箭头、设备名称、在线/离线/降级状态小圆点。
  - 双击设备节点默认装载主码流。
  - 嵌套渲染 `StreamTreeNode`。
- [x] **Step 2.3: 实现 `LiveDeviceTreePanel.tsx`**
  - 顶部控制区：设备资产标题、总数/在线率指标、折叠收起按钮、即时搜索输入框。
  - 列表展示：三态处理（Loading 骨架屏、Error 提示、Empty 空数据引导/搜索无结果引导）。
  - 响应式处理：支持抽屉（Drawer）模式与侧边停靠模式。
  - 编写组件单元测试 `LiveDeviceTreePanel.test.tsx`。

## 阶段三：右侧多分屏监控工作区与视口聚焦增强

- [x] **Step 3.1: 升级 `LiveViewport.tsx`**
  - 新增 `isSelected` 属性与 `onSelect` 回调。
  - 选中聚焦态样式：高亮光环与序号标牌高亮（如 `ring-2 ring-blue-500`）。
  - 空视口占位引导更新：提示「点击聚焦此视口，从左侧双击流快速接入」。
  - 保留原有悬浮工具栏、全屏与弹窗分配兼容性。
- [x] **Step 3.2: 升级 `LiveDashboard.tsx`**
  - 顶部增加聚焦状态提示（如当前激活视口序号、流名称）。
  - 顶部增加一键清空全部视口按钮。
  - 宫格网格点击视口触发选中激活。
  - 编写/更新 `LiveDashboard.test.tsx` 单元测试。

## 阶段四：LivePage 两栏组装与全宽流式布局适配

- [x] **Step 4.1: 重构 `LivePage.tsx`**
  - 将 `LiveDeviceTreePanel` 与 `LiveDashboard` 组合为左树右屏的两栏交互架构。
  - 左侧支持宽度平滑折叠/展开，展开状态持久化至 `localStorage`。
  - 移动端浮动抽屉与遮罩集成。

## 阶段五：国际化多语言词条补充

- [x] **Step 5.1: 补充三语词条**
  - 更新 `web/src/features/live/locales/en.json`。
  - 更新 `web/src/features/live/locales/zh-Hans.json`。
  - 更新 `web/src/features/live/locales/zh-Hant.json`。

## 阶段六：全量验证与质量门禁

- [x] **Step 6.1: 静态检查与无 any 强类型校验**
  - 运行 `npm run lint`。
  - 运行 `npm run type-check`。
- [x] **Step 6.2: 自动化测试套件执行**
  - 运行 `npm run test`，确保全部测试用例通过。
- [x] **Step 6.3: Vite 生产构建验证**
  - 运行 `npm run build`，确保生产打包零告警零报错。

---

## 验证命令与检查点

```bash
cd web
npm run lint
npm run type-check
npm run test
npm run build
```

## 回滚策略
- 如遇到意外问题，通过 Git 回退修改并重置任务。
