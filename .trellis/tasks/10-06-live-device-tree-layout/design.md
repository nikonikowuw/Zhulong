# 实时预览 UI 升级：左侧媒体设备树与右侧分屏工作区 (Technical Design)

## 1. 架构总览与组件拓扑

升级后的组件层级与拓扑结构如下：

```txt
features/live/
├── components/
│   ├── LivePage.tsx                 # [重构] 顶层容器，自适应两栏布局 (左: 树面板，右: 分屏工作区)
│   ├── LiveDeviceTree/              # [新增] 左侧媒体设备树组件族
│   │   ├── LiveDeviceTreePanel.tsx  # 面板外壳：折叠切换、搜索过滤输入框、设备数指标
│   │   ├── DeviceTreeNode.tsx       # 摄像机一级节点：展开/折叠、健康状态小圆点、主/子码流列表
│   │   └── StreamTreeNode.tsx       # 码流二级节点：双击/点击播放、分辨率/角色、播放中视口指示
│   ├── LiveDashboard.tsx            # [重构] 右侧分屏工作区：聚焦状态栏、1/4/9 模式、全部清空、视口矩阵
│   ├── LiveViewport.tsx             # [优化] 增加 isSelected 激活边框光环与点击激活回调
│   ├── LivePlayer.tsx               # [保持] 核心 WebCodecs 渲染引擎不变
│   └── ...
├── hooks/
│   ├── useLiveLayout.ts             # [扩展] 增加 selectedSlotIndex, assignToSelected, clearAll, playingSlotsMap
│   └── useDeviceTreeFilter.ts       # [新增] 客户端即时搜索树节点匹配与展开状态派生
└── locales/
    ├── en.json
    ├── zh-Hans.json
    └── zh-Hant.json
```

---

## 2. 状态管理与数据流 (State & Data Flow)

### 2.1 视口布局与选中态扩展 (`useLiveLayout.ts`)

```typescript
export interface LiveLayoutState {
  mode: LayoutMode; // 1 | 4 | 9
  slots: Record<number, SlotBinding>;
  selectedSlotIndex?: number; // 0 <= index < mode
}

export interface UseLiveLayoutReturn {
  mode: LayoutMode;
  slots: Record<number, SlotBinding>;
  selectedSlotIndex: number;
  fullscreenSlot: number | null;
  setMode: (mode: LayoutMode) => void;
  selectSlot: (slotIndex: number) => void;
  assignSlot: (slotIndex: number, binding: SlotBinding) => void;
  assignToSelected: (binding: SlotBinding) => void;
  clearSlot: (slotIndex: number) => void;
  clearAllSlots: () => void;
  toggleRole: (slotIndex: number) => void;
  setFullscreenSlot: (slotIndex: number | null) => void;
  // 快速映射：用于设备树快速判断某 camera/role 是否正在被某个视口播放
  getPlayingSlot: (cameraId: string, role?: StreamRole) => number | null;
}
```

### 2.2 树过滤与展开派生 (`useDeviceTreeFilter.ts`)

遵循铁律第 6 条（状态就近与渲染派生，严禁在 `useEffect` 中监听输入并 `setState`）：
- 输入搜索关键字 `searchQuery: string`。
- 派生过滤列表 `filteredCameras`：匹配摄像头名称、ID 或流信息。
- 当有搜索输入时，自动默认展开所有匹配成功的节点；无搜索时支持手动展开/折叠。

---

## 3. 核心交互链路与规范

1. **视口聚焦联动**：
   - 用户点击右侧任一视口，调用 `selectSlot(index)`，视口外层增加高亮蓝框（如 `ring-2 ring-blue-500`）及高亮视口标号。
   - 切换 1/4/9 布局时，如果 `selectedSlotIndex >= mode`，自动将 `selectedSlotIndex` 夹紧为 `mode - 1`。
2. **点播分发逻辑 (`assignToSelected`)**：
   - 用户双击摄像机设备节点 -> 取该摄像机的主码流 (`main`) 装载。
   - 用户点击/双击流节点 -> 装载指定角色 (`main` 或 `sub`) 的流。
   - 目标视口决定：
     1. 若当前 `selectedSlotIndex` 视口存在且为空，装载入当前视口。
     2. 若当前 `selectedSlotIndex` 视口已满，优先寻找网格内第一个空视口（若有），并自动更新 `selectedSlotIndex` 为该空视口并装载。
     3. 若所有网格视口均已占满，直接覆盖替换当前 `selectedSlotIndex` 视口。
     4. 装载完成后，将 `selectedSlotIndex` 自动聚焦至下一个未分配的空视口（方便用户连续点播）。
3. **设备树展开/折叠持久化**：
   - 设备树左栏支持收起（折叠至细长切换条），宽度从 `w-64/w-72` 收起为 `w-0`（带动画），偏好持久化至 `localStorage` (`zhulong_live_tree_collapsed`)。

---

## 4. 边界处理与容错

1. **摄像机无流或离线**：
   - 离线设备在树中以深灰色/黄色提示，但仍允许点播（由播放器连接后提示断开/重连重试）。
   - 无配置流的设备节点标注“未配置码流”且不可点播。
2. **移动端响应式处理**：
   - 在桌面端（`>= lg`），左侧为固定/平滑展开的双栏；
   - 在平板/移动端（`< lg`），左侧为底部或侧边浮动 Drawer，配有背景遮罩，点播后自动关闭 Drawer。
3. **性能与渲染优化**：
   - `getPlayingSlot` 采用在渲染期间构建的 `Map<string, number>` 索引查询，设备节点比对开销为 O(1)。
   - 严格遵循 `requestAnimationFrame` 播放器渲染规范与 `VideoFrame.close()` 资源释放。
