# 实时视频播放器组件与多路宫格看板 (实施计划)

## 0. 实施前门禁 (Pre-Implementation Gates)

- [x] 确认工作区代码干净，无未追踪未提交文件。
- [x] 确认现有前端测试与构建全部绿灯（`npm test && npm run build`）。

---

## 1. 协议解包与核心类型 (Core & Protocol)

- [x] 在 `web/src/features/live/core/types.ts` 定义：
  - `ParsedPacket`（codec, isKeyFrame, pts, dts, payload）；
  - `ConnectionStatus`（`connecting` | `connected` | `reconnecting` | `disconnected` | `error`）；
  - `StreamTelemetry`（fps, bitrateKbps, latencyMs, droppedFrames）；
  - `LayoutMode`（1 | 4 | 9）；
  - `SlotBinding`（cameraId, role, streamInfo?）；
  - `RoiBox` 与 `RoiOverlayConfig`。
- [x] 在 `web/src/features/live/core/wireParser.ts` 实现：
  - `parseZlm1Packet(buffer: ArrayBuffer): ParsedPacket`；
  - `extractNalus(payload: Uint8Array): Uint8Array[]`；
  - 非法封包校验（长度 <24 或 Magic 异常报错）。
- [x] 编写 `wireParser.test.ts` 单元测试（验证 24 字节头大端序字段提取与错误处理）。

---

## 2. 前端单例流连接池 (FrontendStreamPool)

- [x] 在 `web/src/features/live/core/streamPool.ts` 实现：
  - 单例 `FrontendStreamPool` 类；
  - `subscribe(cameraId, role, onPacket, onStatus)` 建立/复用 WS 并增加 `refCount`；
  - `unsubscribe(cameraId, role, onPacket, onStatus)` 减少 `refCount`；
  - `refCount === 0` 时启动 3000ms 延迟断开计时器（Grace Timer）；
  - 意外断线自动退避重连；
  - 导出单例实例 `streamPool`。
- [x] 编写 `streamPool.test.ts` 单元测试（验证同一流多重订阅只建单连接、引用计数归零延迟断开与防抖复用）。

---

## 3. WebCodecs 解码器与流订阅钩子 (Decoder & Hook)

- [x] 在 `web/src/features/live/core/decoder.ts` 封装：
  - WebCodecs `VideoDecoder` 初始化与自适应参数配置（H.264 / H.265）；
  - `decodePacket(packet: ParsedPacket, onFrame: (frame: VideoFrame) => void)`；
  - 队列深度监控与丢帧追帧策略；
  - 优雅释放与资源关闭。
- [x] 在 `web/src/features/live/hooks/useLiveStream.ts` 实现：
  - 监听流池数据包并更新 Telemetry 统计（FPS、码率、连接状态）；
  - 控制 WebCodecs 解码并在每帧输出时触发 Canvas 绘制；
  - 视口挂载/卸载自动处理订阅生命周期。

---

## 4. 播放器、遥测与 ROI 标注组件 (Player & Visual Components)

- [x] 在 `web/src/features/live/components/LiveTelemetryHud.tsx` 实现悬浮状态与性能指示层。
- [x] 在 `web/src/features/live/components/RoiOverlayCanvas.tsx` 实现透明 AI 目标检测框叠加画布。
- [x] 在 `web/src/features/live/components/LivePlayer.tsx` 整合 Canvas、WebCodecs 解码器、无画面加载态与不支持降级提示。
- [x] 在 `web/src/features/live/components/LiveViewport.tsx` 封装单槽位：
  - 顶部悬浮工具栏（摄像机名称、Main/Sub 切换、单槽位全屏、解绑）；
  - 空视口占位引导（"+" 点击展开指派面板）；
  - 设备离线/禁用遮罩提示。

---

## 5. 宫格监控看板与多视口编排 (Dashboard & Layout)

- [x] 在 `web/src/features/live/hooks/useLiveLayout.ts` 实现：
  - 布局模式（1/4/9）状态及持久化至 `localStorage`；
  - 视口分配表（`Record<number, SlotBinding>`）管理；
  - 快速指派摄像机与清空槽位操作。
- [x] 在 `web/src/features/live/components/LiveDashboard.tsx` 实现网格渲染器与顶部控制栏（1/4/9 切换、全屏控制）。
- [x] 在 `web/src/features/live/components/LivePage.tsx` 整合整个实时预览视图。
- [x] 在 `web/src/features/live/index.ts` 统一对外导出。

---

## 6. 全局导航集成与国际化 (App & i18n)

- [x] 在 `web/src/features/live/locales/` 编写 `en.json`、`zh-Hans.json`、`zh-Hant.json`。
- [x] 在 `web/src/shared/i18n/index.ts` 挂载 `live` 命名空间。
- [x] 在 `web/src/App.tsx` 顶栏增加「实时预览 (Live)」导航 Tab，支持 `#/live` 同步与响应式分发。

---

## 7. 自动化测试与质量门禁 (Verification)

- [x] 编写前端单元测试：
  - `wireParser.test.ts`
  - `streamPool.test.ts`
  - `LiveDashboard.test.tsx`
  - `LivePlayer.test.tsx`
- [x] 执行全套质量门禁：
  - `npm run type-check`
  - `npm run lint`
  - `npm test`
  - `make check`
  - `make smoke`
