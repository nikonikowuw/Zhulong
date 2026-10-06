# 实时视频播放器组件与多路宫格看板 (PRD)

## 1. 目标与定位 (Goal)

在 Web 前端构建高吞吐、极低延迟（<150ms）的纯前端实时视频播放体系与专业安防多路监控大盘（`features/live`），打通后端 WebSocket ZLM1 二进制流管道：
1. **ZLM1 协议解包器 (`wireParser.ts`)**：高效解析 24 字节大端序固定头（Magic `0x5A4C4D31`、Codec、Flags、90kHz PTS/DTS）并切分 Annex B NALU 载荷。
2. **前端流连接池 (`FrontendStreamPool`)**：基于客户端引用计数的单例 WebSocket 连接池，保证同一相机同一路流在不同视口/多宫格复用同一个 WS 连接，视口计数归零时采用 3 秒防抖延迟断开，杜绝布局切换震荡。
3. **极低延迟播放内核 (`LivePlayer.tsx`)**：以原生 WebCodecs (`VideoDecoder` + `<canvas>`) 为核心内核，实现 H.264 / H.265 硬件加速解码与亚秒级即时渲染；支持关键帧快速秒开与断网自动重连；在不支持的环境优雅降级并展示能力诊断。
4. **1 / 4 / 9 宫格实时监控大盘 (`LiveDashboard.tsx` ➔ `#/live`)**：提供 1 宫格（聚焦视口）、4 宫格（标准巡检）、9 宫格（全局大盘）自适应布局；支持视口点选分配摄像机、主/子流自由切换、视口独立全屏、空位快捷指派，并持久化视口绑定配置至 `localStorage`。
5. **AI 目标检测覆盖层 (`RoiOverlayCanvas.tsx`)**：提供等比缩放的透明标注 Canvas，为后续目标检测框（Bounding Box）与置信度标签渲染提供标准化插槽。
6. **顶栏导航与国际化**：在 `App.tsx` 扩展「实时监控 (Live)」一级入口，与 URL Hash (`#/live`) 状态双向同步；完整支持 `en`、`zh-Hans`、`zh-Hant` 三语无缝切换。

---

## 2. 需求规范 (Requirements)

### 2.1 协议解析与连接池 (Protocol & Pool)
- **R1.1 ZLM1 解包**：严格校验 4 字节魔数 `0x5A4C4D31`，提取 Codec（`0x01`=H.264, `0x02`=H.265）、Flags（KeyFrame、HasPTS、HasDTS）、PTS/DTS（64 位大端序，90kHz 时基）及后续 Annex B 视频包。
- **R1.2 客户端流池复用**：`FrontendStreamPool` 以 `${cameraId}:${role}` 为唯一键。多视口重复绑定同路流时仅开启单条物理 WebSocket；各视口通过发布/订阅广播分发数据包。
- **R1.3 延迟防抖释放**：视口卸载使 `refCount` 降为 0 时，启动 3 秒延迟计时器；若 3 秒内无新视口接续订阅，才真正关闭 WebSocket 连接，避免用户在 1/4/9 宫格切换时触发频繁断连与重握手。
- **R1.4 状态反馈与重连**：连接池向观察者广播连接状态（`connecting`、`connected`、`reconnecting`、`disconnected`、`error`），并在网络中断时执行指数退避重连（1s ➔ 2s ➔ 4s ➔ 最大 15s）。

### 2.2 播放器与解码内核 (LivePlayer)
- **R2.1 播放内核调度**：
  - 首选 WebCodecs：利用 `VideoDecoder` 解析关键帧参数（SPS/PPS/VPS）并解码每一帧，渲染至 2D Canvas；
  - 降级机制：若浏览器未启用或不支持 WebCodecs，展示友好的解码能力提示与诊断指引。
- **R2.2 首帧秒开与延迟追帧**：结合后端 GOP 缓存，一旦收到含 SPS/PPS 的关键帧立即解码上屏；当网络阻塞恢复后，丢弃过期陈旧帧以维持低延迟（<150ms）。
- **R2.3 状态与遥测 HUD**：视口支持轻量遥测悬浮层，显示实时帧率（FPS）、分辨率、码率及解码状态，可手动开关隐藏。

### 2.3 多路宫格看板 (LiveDashboard)
- **R3.1 布局切换**：支持 1 宫格 (1x1)、4 宫格 (2x2)、9 宫格 (3x3) 快捷切换，响应式自适应屏幕高宽比。
- **R3.2 视口操作菜单**：
  - 空视口：展示 "+" 占位符与快速选择在线摄像机下拉菜单；
  - 已绑定视口：顶部悬浮展示设备名称、主/子流切换徽标（Main / Sub）、单个视口全屏展开按钮、移除摄像机按钮；
  - 离线提示：若绑定的摄像机离线或未启用，显示离线警示遮罩。
- **R3.3 布局配置记忆**：将用户在宫格中的槽位绑定（槽位索引 ➔ `{ cameraId, role }`）与当前布局模式（1/4/9）存储在 `localStorage`，刷新页面自动恢复。

### 2.4 ROI 标注层与视觉规范 (RoiOverlay & UX)
- **R4.1 几何自适应**：`RoiOverlayCanvas` 始终覆盖在视频画布之上，根据视频原始宽高比与容器实际尺寸保持严格几何映射。
- **R4.2 全局导航集成**：顶栏分段控制器增加「实时预览」，与「系统概览」、「摄像机管理」并列，URL hash 对应 `#/live`。

---

## 3. 验收标准 (Acceptance Criteria)

- [x] **AC-1 (ZLM1 协议解包与 Annex B 提取)**：`wireParser.ts` 严格解析 24 字节大端序头（Magic `0x5A4C4D31`, Codec `0x01/0x02`, Flags, PTS/DTS 90kHz 时基），切分有效 NALU 载荷，非法封包抛出/返回明确错误并丢弃。
- [x] **AC-2 (前端流连接池单例复用与防抖)**：`FrontendStreamPool` 针对每个 `${cameraId}:${role}` 维护 WebSocket 单例与 `refCount`；同一流多视口共享单条物理连接；`refCount` 归零后执行 3 秒防抖断开，重连支持指数退避。
- [x] **AC-3 (WebCodecs 极低延迟播放内核与渲染)**：`LivePlayer` 首选利用 WebCodecs (`VideoDecoder`) 解码 H.264/H.265 并绘制至 Canvas；支持关键帧秒开、丢帧自适应追帧、FPS/比特率实时测算；环境不支持时提供友好降级指引。
- [x] **AC-4 (1 / 4 / 9 多路自适应宫格看板)**：`/live` 路由提供 1 / 4 / 9 宫格切换；支持视口分配摄像机、主/子流切换、视口单独全屏、移除视口、空视口占位引导；视口分配持久化至 `localStorage`。
- [x] **AC-5 (顶栏三级导航与路由同步)**：`App.tsx` 顶栏提供 Overview / Live / Cameras 三级导航，与 `window.location.hash` 保持双向同步，未登录状态严格拦截保护。
- [x] **AC-6 (ROI 检测框覆盖层预留)**：实现 `RoiOverlayCanvas.tsx`，与播放器画布等比对齐，支持未来 AI 目标框、区域多边形与置信度标签绘制。
- [x] **AC-7 (全量国际化与质量门禁)**：补齐 `en` / `zh-Hans` / `zh-Hant` 词典；全量通过 `npm run type-check`、`npm run lint`、`npm test`、`make check` 与 `make smoke`。
