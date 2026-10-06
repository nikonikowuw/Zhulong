# 实时视频播放器组件与多路宫格看板 (Technical Design)

## 1. 架构总览与分层设计

本设计在 `web/src/features/live/` 建立纯前端低延迟流媒体播放与多路监控大盘业务域：

```txt
web/src/
├── App.tsx                              # 全局导航栏升级：支持 Overview / Live / Cameras 三级切换
├── features/
│   └── live/
│       ├── core/
│       │   ├── types.ts                 # 媒体包、解码器状态、宫格配置类型定义
│       │   ├── wireParser.ts            # ZLM1 二进制头解析与 Annex B 提取
│       │   ├── streamPool.ts            # WebSocket 客户端连接池 (单例引用计数与 3s 延迟释放)
│       │   └── decoder.ts               # WebCodecs VideoDecoder 生命周期管理
│       ├── hooks/
│       │   ├── useLiveStream.ts         # 视口流订阅 Hook (连接状态、统计指标、包派发)
│       │   └── useLiveLayout.ts         # 宫格布局与槽位持久化 Hook (localStorage)
│       ├── components/
│       │   ├── LivePage.tsx             # 实时预览一级主视图页面
│       │   ├── LiveDashboard.tsx        # 1/4/9 宫格自适应监控大盘
│       │   ├── LiveViewport.tsx         # 单个宫格视口容器 (操作栏、控制徽标、状态遮罩)
│       │   ├── LivePlayer.tsx           # WebCodecs + Canvas 极速播放渲染器
│       │   ├── LiveTelemetryHud.tsx     # 帧率、码率、时延悬浮遥测信息
│       │   └── RoiOverlayCanvas.tsx     # AI 目标检测框等比绘制透明覆盖层
│       ├── locales/                     # en / zh-Hans / zh-Hant 三语配置
│       └── index.ts                     # 模块公共导出
```

---

## 2. 核心机制详细设计

### 2.1 ZLM1 协议解包器 (`wireParser.ts`)

二进制消息结构为 24 字节大端序头 + Annex B NALU 载荷：

```txt
0                   1                   2                   3
0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|       Magic: 'Z' 'L' 'M' '1' (0x5A4C4D31)                     |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|  Codec (0x01/2)| Flags (0x01/2/4)|      Reserved (0x0000)     |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                   PTS (int64 big-endian, 90kHz)               |
|                                                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                   DTS (int64 big-endian, 90kHz)               |
|                                                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                   Annex B NALU Payload (Bytes 24..N)          |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

1. **头校验**：当数据长度小于 24 字节或魔数不匹配 `0x5A4C4D31` 时抛出异常或丢弃；
2. **字段提取**：
   - `codec`: `0x01` 映射为 `'h264'`，`0x02` 映射为 `'h265'`；
   - `isKeyFrame`: `(flags & 0x01) !== 0`；
   - `pts`: 90kHz 时间戳（毫秒转换：`(pts * 1000) / 90000`）；
   - `payload`: `new Uint8Array(buffer, 24)`；
3. **NALU 扫描**：按 Annex B 起始码（`0x00000001` 或 `0x000001`）提取参数集（SPS / PPS / VPS），供解码器初始化和描述符装配。

### 2.2 前端单例流连接池 (`FrontendStreamPool`)

```txt
┌─────────────────────────────────────────────────────────────┐
│                 FrontendStreamPool (全局单例)                │
└──────────────────────────────▲──────────────────────────────┘
                               │ 管理
        ┌──────────────────────┴──────────────────────┐
        ▼                                             ▼
  StreamEntry (cam1:main)                       StreamEntry (cam2:sub)
  - ws: WebSocket                               - ws: WebSocket
  - refCount: 2                                 - refCount: 0 (Grace 3s)
  - listeners: Set<onPacket>                    - listeners: empty
  - status: 'connected'                         - graceTimer: active
```

1. **键值隔离**：键格式为 `${cameraId}:${role}`；
2. **多视口复用**：当 4 宫格中有两个视口选择了同一相机的同一路流（例如主流），`refCount` 递增至 2，共用同一物理 WebSocket；
3. **3 秒延迟防抖关闭**：当一个视口切走或关闭时，`refCount` 降至 0，此时**不立即断开 WS**，而是启动 3000ms 定时器。若在 3 秒内同一流被再次分配，直接取消定时器并复用现有连接；超时后才真正执行 `ws.close()`；
4. **指数退避重连**：当连接意外断开且 `refCount > 0` 时，启动自适应退避重连（1s ➔ 2s ➔ 4s ➔ 8s ➔ 最大 15s），避免雪崩。

### 2.3 极速播放内核 (`LivePlayer` & `WebCodecsDecoder`)

1. **WebCodecs 解码管线**：
   - 构造 `new VideoDecoder({ output: handleFrame, error: handleError })`；
   - 收到首个关键帧时，解析并配置解码器：`decoder.configure({ codec: 'avc1.640028' | 'hvc1.1.6.L93.B0', optimizeForLatency: true })`；
   - 将收到的 NALU 包装为 `new EncodedVideoChunk({ type: isKeyFrame ? 'key' : 'delta', timestamp: ptsUs, data: payload })` 输入 `decoder.decode()`；
   - 在 `output(videoFrame)` 回调中，利用 Canvas 2D 上下文直接执行 `ctx.drawImage(videoFrame, 0, 0, canvas.width, canvas.height)`，随后立即调用 `videoFrame.close()` 释放 GPU 显存。
2. **降级与环境探测**：
   - 在检测到当前浏览器或安全上下文（非 HTTPS / localhost）禁用 `VideoDecoder` 时，展示友好的不支持指引。
3. **追帧与丢包处理**：
   - 维持解码队列深度监控，当队列堆积超过 5 帧时执行追帧丢弃，确保端到端延迟控制在 150ms 以内。

### 2.4 1 / 4 / 9 宫格监控看板 (`LiveDashboard`)

1. **布局模式**：
   - 1 宫格：全尺寸主视口（`grid-cols-1 grid-rows-1`）；
   - 4 宫格：四分屏（`grid-cols-2 grid-rows-2`）；
   - 9 宫格：九分屏（`grid-cols-3 grid-rows-3`）；
2. **视口交互状态**：
   - **空槽位**：点击弹出选择设备弹窗，展示在线摄像机列表与主/子流选项；
   - **已绑定槽位**：悬浮工具栏呈现摄像机名称、Main/Sub 切换药丸按钮、单视口独立全屏切换按钮、解绑按钮；
   - **离线遮罩**：若设备离线或被禁用，视口显示半透明毛玻璃离线警告，停止建立无意义的 WS 尝试。
3. **本地状态持久化 (`useLiveLayout`)**：
   - 使用 `localStorage.getItem('zhulong_live_layout')` 记忆用户的槽位绑定表与布局模式，刷新或重新进入保持上次监控排布。

### 2.5 AI 检测框覆盖层 (`RoiOverlayCanvas`)

- 作为透明 Canvas 覆盖在 `LivePlayer` 之上，尺寸与播放画布严格 1:1 对齐；
- 接收归一化坐标的目标框数组，使用 `requestAnimationFrame` 绘制平滑矩形框与标签，为后续 AI 视觉模块奠定基础。

---

## 3. UI/UX 视觉体系与动效设计

- 延续 Apple 极简暗色/浅色科技美学；
- 视口工具栏采用高斯模糊半透明背景（`backdrop-blur-md bg-black/40 text-white`）；
- 状态指示灯（在线绿色呼吸、正在连接黄色旋转指示、异常警示红）；
- 全局键盘快捷键支持：`Esc` 退出视口全屏，数字键 `1` / `4` / `9` 切换宫格布局。
