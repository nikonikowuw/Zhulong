# 设计摘要：前端音频（C · Frontend）

> 完整设计见父任务 `design.md` §4 / §6.3。本文件只列实现契约。

## 时钟（唯一权威）

```
videoDueMs(ptsUs) = clock.wallMs + (ptsUs - clock.ptsUS) / 1000 + targetLatencyMs   // 100 ms
audioDueMs(ptsUs) = videoDueMs(ptsUs)   // 同一条时间轴，无额外偏移
```

| 情况 | 行为 |
| :--- | :--- |
| `clock === null` | 最多缓存 100 ms，超时丢弃 |
| `due < now - 250 ms` | 陈旧，丢弃并计数 |
| `due > now + 400 ms` | 视为跳变：`flush()` 后以本包为新锚点，从 `now + preRoll` 起播 |
| 环深 > 250 ms | 丢旧帧 |
| 环深 < 40 ms | 插 10 ms 淡入淡出静音（不硬切） |

重锚三处：解禁/建连后首包、音频帧 `flags & DISCONTINUITY`、视频 `onResync`。

## 音频通道客户端

- `audio-channel.ts` 只管传输：`connect({cameraId, role}) → onInfo / onPacket / onError / onClose`；重连带退避（100 ms → 1 s），静音/切格立即 `close()`；
- 帧头解析失败 → 丢帧计数，不抛到 UI；
- 收到 `error` 帧 → 置灰 + 文案后关闭，不重连（避免报错循环）；
- 不缓存历史帧：解禁后只从实时边缘起播。

## 播放后端

- 统一 AudioWorklet 环形缓冲，**不要**每包新建 `AudioBufferSourceNode`（50 节点/秒）。
- Worklet 用 `new URL('./xxx-worklet.ts', import.meta.url)`；`addModule()` 异步，首帧投递等它就绪。
- G.711 直接在 Worklet 内做线性重采样到 `AudioContext.sampleRate`。
- AAC 必须带 `description = audioInfo.config`（RTSP 送裸 AU，无 ADTS 头）。

## 状态归属

- 静音与焦点只存在 `useLiveGrid`（`cells[].isMuted` 唯一真相源），不新建全局 store。
- `LivePlayer` 由 `isMuted` prop 的 effect 驱动；命令式 `setMute` 只保留为 mock 实现。
- `AudioContext` 只在第一次解禁手势里创建/resume。

## 降级矩阵

| 条件 | 行为 |
| :--- | :--- |
| `present = false` / 无 `audio_info` | 不建连接，喇叭置灰 |
| `error` 帧（`NO_AUDIO` / `UNSUPPORTED`） | 置灰 + 文案，视频继续 |
| 非安全上下文 / 无 `AudioDecoder`（AAC） | 禁用音频轨，视频照常 |
| AudioWorklet 不可用 | 退回 `AudioBufferSourceNode` 排期 + WARN |

## 测试环境提醒

前端测试跑真实无头 Chromium：`AudioContext` 需要用户手势，用 `userEvent` / `page.click` 触发；不要假设 `resume()` 无需激活即可工作。
