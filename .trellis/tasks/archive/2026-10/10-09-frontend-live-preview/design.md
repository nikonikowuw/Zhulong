# 技术设计：前端实时预览能力与多分屏播放器集成

## 1. 架构总览 (Architecture Overview)

```txt
┌────────────────────────────────────────────────────────────────────────┐
│                        React 19 Frontend (/live)                       │
│                                                                        │
│   ┌──────────────────────────────────────────────────────────────┐     │
│   │ LiveToolbar (1/4/9/16 宫格切换 · 全局大屏 · 一键静音 · 布局持久化)│     │
│   └──────────────────────────────────────────────────────────────┘     │
│   ┌───────────────────────────┐  ┌───────────────────────────────┐     │
│   │ LiveCameraSidebar (资产列表)│  │ LiveGrid (网格布局容器)       │     │
│   │ - 在线/离线状态指示        │  │ ┌─────────────┐ ┌───────────┐ │     │
│   │ - 拖拽/双击快速分配到窗口   │  │ │ Cell 0 (选空)│ │ Cell 1 (A) │ │     │
│   │ - 检索与过滤               │  │ ├─────────────┤ ├───────────┤ │     │
│   └───────────────────────────┘  │ │ Cell 2 (A)  │ │ Cell 3 (B) │ │     │
│                                  │ └─────────────┘ └───────────┘ │     │
│                                  └───────────────┬───────────────┘     │
│                                                  │                     │
│                                                  ▼                     │
│               ┌────────────────────────────────────────────────┐       │
│               │ Frontend StreamConnectionPool (客户端单例连接池) │       │
│               │ - Camera A: refCount = 2 (Cell 1, Cell 2 共享) │       │
│               │ - Camera B: refCount = 1 (Cell 3 独占)         │       │
│               └───────────────────────────┬────────────────────┘       │
└───────────────────────────────────────────┼────────────────────────────┘
                                            │ 复用一条 WebSocket 连接
                                            ▼
                          ┌────────────────────────────────────┐
                          │  Backend WebSocket Stream Hub /   │
                          │  Mock Canvas Loopback Worker       │
                          └────────────────────────────────────┘
```

---

## 2. 模块划分与目录拓扑 (Module Topology)

遵循 `shadcn-admin` 规范，所有业务代码内聚在 `web/src/features/live/`：

```txt
web/
├── public/
│   └── vendor/
│       └── jessibuca/              # 离线自包含解码引擎 (js + wasm + worker)
├── src/
│   ├── features/
│   │   └── live/
│   │       ├── components/
│   │       │   ├── live-grid.tsx                 # 1/4/9/16 网格容器，支持活动窗口聚焦
│   │       │   ├── live-player-cell.tsx          # 单分屏容器（加载/报错/播放状态 + 悬浮控制条）
│   │       │   ├── live-camera-sidebar.tsx       # 摄像机侧边栏（快速绑定分屏）
│   │       │   ├── live-toolbar.tsx              # 顶部工具栏（宫格预设、全屏、状态监视）
│   │       │   ├── live-stream-stats.tsx         # 悬浮 OSD 信息浮窗（FPS、分辨率、硬解模式）
│   │       │   ├── jessibuca-player.tsx          # Jessibuca 播放器 React 封装
│   │       │   └── mock-stream-canvas.tsx        # 离线测试图卡与演示渲染器
│   │       ├── hooks/
│   │       │   ├── use-live-grid.ts              # 宫格状态机（布局预设、单元格映射、焦点）
│   │       │   ├── use-stream-connection.ts      # 单例流连接 Hook（生命周期绑定与状态同步）
│   │       │   └── use-live-shortcuts.ts         # 快捷键守卫（1/4/9/Esc，输入框防误触）
│   │       ├── services/
│   │       │   ├── stream-pool.ts                # 客户端单例流连接池（Map + 引用计数）
│   │       │   └── jessibuca-loader.ts           # 离线 Jessibuca 静态资源懒加载器
│   │       ├── types/
│   │       │   └── index.ts                      # 领域类型定义
│   │       └── index.tsx                         # Live 顶层页面组织入口
│   └── routes/
│       └── _authenticated/
│           └── live/
│               └── index.tsx                     # TanStack Router 强类型路由注册
```

---

## 3. 核心设计与数据流 (Key Designs)

### 3.1 单例流复用连接池 (Stream Connection Pool)

严格落实需求 R7.3：**单例流复用纯在前端维护，避免向同一摄像机建立多条重复 WebSocket 连接**。

```typescript
// services/stream-pool.ts
export interface StreamSubscriber {
  id: string
  onFrame?: (data: Uint8Array) => void
  onStatusChange: (status: StreamStatus, error?: string) => void
  onStatsUpdate: (stats: StreamStats) => void
}

export class StreamConnectionPool {
  private static instance: StreamConnectionPool
  private connections = new Map<string, ManagedStream>()

  // 获取或建立流连接
  subscribe(cameraId: string, subscriber: StreamSubscriber): () => void {
    let conn = this.connections.get(cameraId)
    if (!conn) {
      conn = this.createConnection(cameraId)
      this.connections.set(cameraId, conn)
    }

    conn.subscribers.set(subscriber.id, subscriber)
    conn.refCount++

    // 返回取消订阅函数
    return () => {
      conn.subscribers.delete(subscriber.id)
      conn.refCount--
      if (conn.refCount <= 0) {
        // 延时防抖销毁 (5s Grace Period)
        this.scheduleCleanup(cameraId)
      }
    }
  }
}
```

### 3.2 Jessibuca 三级自适应播放器封装

针对 H.264/H.265 码流，封装符合 React 19 生命周期规范的播放组件：
1. **DOM 容器绑定与 Resize 响应**：使用 `ResizeObserver` 保持画布纵横比与高清适配；
2. **三级降级配置**：
   ```javascript
   const options = {
     container: domElement,
     videoBuffer: 0.2, // 200ms 极低缓冲延迟
     isResize: false,
     useWCS: true,     // WebCodecs 优先
     useMSE: true,     // MSE 次选
     autoWasm: true,   // WASM 兜底
     demuxUseWorker: true,
     operateBtns: { fullscreen: false, screenshot: false, play: false, audio: false }, // 统一采用外部自定义 UI
     forceNoOffscreen: false,
   }
   ```
3. **显存与生命周期安全**：
   - 组件 `unmount` 或流地址切换时，强制执行 `player.destroy()`；
   - 彻底解除全局 EventListener 与 Worker 引用，防止内存泄漏。

### 3.3 离线 Mock 测试图卡回路

在后端媒体 WebSocket 分发尚未就绪或网络隔离环境下：
- 提供 `mock-stream-canvas.tsx`；
- 利用 Canvas 2D 绘制 SMPTE 电视测试色条、动态秒表毫秒时钟、正弦波动态扫描线与摄像机名称水印；
- 精准模拟 25fps 帧率节流调度（通过 `requestAnimationFrame` 计算 $\Delta t$），供开发者和演示用户无需真实视频流即可完整校验 1/4/9 宫格切换、性能开销与全屏交互。

### 3.4 快捷键守卫策略

```typescript
export function useLiveShortcuts({ onLayoutChange, onResetFocus }: ShortcutOptions) {
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      // 守卫：表单输入、弹窗开启态静默拦截
      const activeEl = document.activeElement
      const isInput = activeEl instanceof HTMLInputElement ||
                      activeEl instanceof HTMLTextAreaElement ||
                      activeEl?.getAttribute('contenteditable') === 'true'
      if (isInput) return

      if (e.key === '1') onLayoutChange(1)
      else if (e.key === '4') onLayoutChange(4)
      else if (e.key === '9') onLayoutChange(9)
      else if (e.key === 'Escape') onResetFocus()
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [onLayoutChange, onResetFocus])
}
```

---

## 4. 边界处理与容错 (Edge Cases & Resilience)

1. **单窗全屏与多窗复用**：当用户双击放大 Cell 1 时，其他窗口进入隐藏状态，此时单例流连接保持不断开，恢复网格时无缝继续播放，无需重新握手；
2. **摄像机离线与重连**：当 WebSocket 收到断开事件，单例池启动指数退避重连（1s $\rightarrow$ 2s $\rightarrow$ 4s $\rightarrow$ 8s），Cell 界面显示「重连中 (2/5)」并提供「立即重试」按钮；
3. **显存保护与帧率降级**：在 9 宫格或 16 宫格高负载排布下，如客户端显存紧张，优先关闭非聚焦窗口的平滑后处理滤镜，确保主核不丢包。
