# 实施记录：前端音频（C · Frontend）

> 对应父任务 `11-03-dual-track-preview` 的 Phase 3 + Phase 4。
> 本文记录**已交付的切片、验证命令的实际结果、与设计稿的偏差**。

## 0. 本轮范围（切片 C3-a：传输层 + G.711）

**已交付**：`audio-channel.ts`（音频 WS 客户端）、`g711-decoder.test.ts`、`stream-pool.ts` 的文本帧分支、
`use-stream-connection.ts` 的 `streamInfo` 出口。

**未交付（不在本轮任务范围）**：`LiveAudioPlayer` / AudioWorklet / AAC `AudioDecoder` /
`PresentationScheduler.clockSnapshot()` / `useLiveGrid` 音频焦点 / i18n 文案。
上述各项属于 C3-b 与 C4，本轮**没有**任何代码。

## 1. 已完成的改动

### 1.1 `api/live-camera-api.ts`

- 抽出 `wsBase()`：协议（`ws:` / `wss:`）与主机的推导只有一份。
  视频与音频两条 WS 必须落同源 —— 同源才带 cookie，网关鉴权/限流才按同一条会话计数。
- 新增 `getCameraAudioWsUrl(cameraId, streamType)` →
  `${wsBase()}/api/v1/cameras/${id}/streams/${role}/audio/ws`，与 `getCameraStreamWsUrl` 并排。
  后者行为不变（只是改为调用 `wsBase()`）。

### 1.2 `types/index.ts`

新增 `StreamAudioCapability` / `StreamInfo`；`StreamSubscriber` 加可选 `onStreamInfo`；
`ManagedStream` 加可选 `lastStreamInfo`（缓存最近一次能力帧）。
**`ParsedPacket` 与 ZLM2 相关类型一行未动。**

### 1.3 `services/stream-pool.ts`（最小改动）

- `ws.onmessage` 开头插入 `typeof event.data === 'string'` 分支 → `parseStreamInfo()` →
  缓存到 `stream.lastStreamInfo` → 广播 `onStreamInfo` → **`return`**。
  `ArrayBuffer` 分支（含 fps/码率统计、`parseZlm2Packet`、`onFrame`/`onPacket` 广播）**一行未改**。
- `subscribe()` 里在已有的 `onStatusChange` / `onStatsUpdate` 补发之后，增加
  `lastStreamInfo` 补发 —— 否则「先连上、后挂订阅者」（StrictMode 双挂载、分屏切格重建）永远收不到能力帧。
- 新增模块级 `parseStreamInfo(raw): StreamInfo | null`：非 JSON / `type !== 'stream_info'` /
  缺 `audio` / `audio.present` 非 boolean 一律返回 `null`（静默忽略）。
  不得只验 `type` 就采信 `audio`：`present` 直接决定 UI 点亮还是置灰喇叭。

### 1.4 `services/audio-channel.ts`（新增）

导出：`AUDIO_WIRE_MAGIC` / `AUDIO_WIRE_HEADER_SIZE` / `AUDIO_CODEC_*` / `AUDIO_FLAG_*` /
`AUDIO_CLOSE_*` 常量，`parseAudioFrame()`，`connectAudioChannel()`。

- URL 走 `getCameraAudioWsUrl`；`binaryType = 'arraybuffer'`（不设这一条二进制帧会以 Blob 到达）。
- 文本帧：`audio_info` → `onInfo` + `state='open'` + `retries=0`；`error` →
  `terminate()` + `socket.close()` + `onFatal(code, reason)`，**不重连**；其余文本帧静默忽略。
- 二进制帧：`DataView` 解析 20 字节大端头。`pts_us` 用 `getBigInt64(12)` 再 `Number()`
  （用 `getUint32` 只取低 32 位会在 71.6 分钟后静默回绕）。magic ≠ `0x5A4C4131` 或长度 < 20 →
  `droppedFrames++` 后返回，**不抛**，后续帧仍能处理。
- 重连：仅对非致命关闭。退避 `100 → 200 → 400 → 800 → 1000`（封顶 1s），
  每次乘 `0.5 + Math.random() * 0.5` 抖动。
- `close()`：置 `closed = true` → 清定时器 → 关 socket。**唯一**关闭判据是这个 `closed` 标志，
  不是 `ws.readyState` —— `close()` 之后旧 socket 的 `onclose` 仍会异步到达，
  靠 readyState 分不清「对端关的」和「我关的」，会照样重连。
- 关闭码：`4404` → `onFatal('NO_AUDIO')` 且不重连；`4001/4002/4003/4500` → `onDisconnect(code)` 后重连；
  其它码（1000、1006…）→ 直接重连，不报 `onDisconnect`。
- 不缓存历史帧。解析失败计数是普通 number 字段（`channel.droppedFrames`），未接入遥测系统。

### 1.5 `hooks/use-stream-connection.ts`

- 新增可选 `onStreamInfo`，用 `onStreamInfoRef` 转发（同 `onPacketRef` 模式），**未进依赖数组**。
- 新增 `streamInfo: StreamInfo | null` 状态并返回。
- effect cleanup 里 `setStreamInfo(null)`：换机 / 切码流后能力帧属于上一条流，留着会让新格子的喇叭状态错一轮。

### 1.6 测试

- `g711-decoder.test.ts`（新增，19 例）：μ-law / A-law 具体码字、256 项全量奇对称、
  256 项值域、`g711TableFor`、`decodeG711` 的缓冲复用/不足/空负载契约。
- `audio-channel.test.ts`（新增，14 例）：`vi.stubGlobal('WebSocket', FakeWebSocket)`，
  不连真后端。覆盖 `audio_info`→`onInfo`、AAC 的 ASC 透传、int64 `pts_us`
  （`-9000000123` 与 `2^32 + 12345` 两个值）、magic 错/长度 19 丢帧不抛且后续帧照常、
  `error` 帧→`onFatal`+不重连（推进 5s 断言无新实例）、4001→退避重连、4404→`onFatal`+不重连、
  1006→静默重连、`close()` 后推 5s 无重连、`close()` 取消已排定的重连、五级退避标称值。
- `stream-pool.test.ts`（**追加** describe，原有用例未动）：文本帧 10 条 + 二进制帧 5 条 →
  `onStreamInfo` 10 次、`onPacket`/`onFrame` 各 5 次、`onStatsUpdate` 最后一次 `fps: 1` 且
  码率只含 5 帧视频字节；后挂订阅者拿到缓存的 `stream_info`；非 JSON / 非 `stream_info` /
  缺 `audio` 静默忽略。

## 2. 实测证据

```bash
cd web && ./node_modules/.bin/vitest run --browser.headless \
  src/features/live/services/g711-decoder.test.ts \
  src/features/live/services/audio-channel.test.ts \
  src/features/live/services/stream-pool.test.ts
# → Test Files 3 passed (3) / Tests 38 passed (38)

cd web && ./node_modules/.bin/tsc -b
# → 无输出（通过）

cd web && ./node_modules/.bin/eslint src/features/live
# → 无输出（通过）

cd web && ./node_modules/.bin/vitest run --browser.headless src/features/live
# → Test Files 30 passed (30) / Tests 175 passed (175)   ← 确认未破坏既有用例
```

`vite.config.ts` 的 proxy 会打 `ECONNREFUSED 127.0.0.1:8000`，是预期的无后端噪声，忽略。

## 3. 与设计稿的偏差

| 设计稿 | 实际实现 | 原因 |
| :--- | :--- | :--- |
| `connect({cameraId, role}) → onInfo / onPacket / onError / onClose` | `connectAudioChannel({cameraId, role}, {onInfo, onFrame, onFatal, onDisconnect})` | 任务书冻结的签名。`onPacket`→`onFrame`（音频只有帧没有包概念）；`onError`→`onFatal`（明确「终止性」语义）；`onClose`→`onDisconnect`（明确「可恢复」语义，终止性走 `onFatal`）。命名差异让「该不该重连」不再靠猜。 |
| 未提及 `droppedFrames` 出口 | `AudioChannel` 多一个 `readonly droppedFrames: number` | 任务书要求「丢帧计数用普通 number 字段」。计数若无法读出就成了不可观测的死代码，测试也没法断言「真的丢了 2 帧」。 |
| 未提及 `hasAudio` 的处理 | `audio_info` 里 `hasAudio === false` 时**不**认作 info（返回 null，继续等关闭码） | 后端 `AudioInfoMessage.HasAudio` 是显式字段。若 `hasAudio=false` 却当 info 推进到 `open`，播放器会拿到一个没声音的 codec 配置并卡在 running 状态；让连接自然走 4404 关闭码，语义单一。 |
| 未提及 `audio_info` codec 的取值域 | `normalizeCodec()` 只认 `'aac'|'pcma'|'pcmu'`，其余返回 null 忽略 | 类型上 `AudioChannelInfo.codec` 是这三值联合。后端 `engine.Codec.String()` 还可能给 `h264` / `codec(99)`，硬塞进联合类型等于对类型撒谎。 |
| 未提及 `WebSocket` 构造抛异常的路径 | 构造失败时 `scheduleReconnect()` 而不是放弃 | 与「非致命关闭也重连」一致。构造失败多是瞬时策略/URL 问题，直接死掉等于 UI 永久无音频。 |
| 未提及 4404 / error 帧之后是否显式 `close()` | `error` 帧后主动 `socket.close()`（4404 由服务端自己关） | 终止性文本帧之后服务端**会**关连接，但前端不主动关的话，中间那段窗口里这条 socket 还占着后端的通道配额。 |

## 4. 切片 C3-b：播放器 + 音画同步

> 本节记录 C3-b（AudioWorklet 播放后端 + 时钟快照 + `onResync`）的落地情况，
> 对应原「留给 C3-b」的前三条（`live-audio-player` + worklet、`clockSnapshot()` /
> `onResync` / `getPresentationClock()`）。**不碰 UI**：`useLiveGrid` / 喇叭按钮 /
> i18n 仍未动。§5 是更新后的未覆盖项清单。

### 4.1 `services/live-audio-worklet.js`（新增，`.js` 而非 `.ts`）

- 环形缓冲 `Float32Array`，容量 2 秒 @ `sampleRate`（worklet 全局，通常 48000）；
- 消息三类：`{type:'push', samples, inputRate, startDueFrame}` / `{type:'flush'}`，
  以及——**省掉了 `{type:'config', gain}`**，音量统一走主线程 `GainNode`
  （任务书要求「两者选一，别重复」）；
- `process()`：从环读、线性插值 `inputRate → sampleRate`、单声道复制到双声道；
- 欠载：输出静音 + 之后 10 ms 线性淡入（`FADE_IN_SECONDS`），并复位读指针语义
  —— **不跳读**，只在缓冲真正有样本时才推进 `readTotal`；
- 无 `console`；
- 时间轴状态 `streaming`：`flush()` 或欠载时复位，下一包的 `startDueFrame` 重新对齐
  输出时间轴（不让缓冲空转时仍按一条追不上墙钟的旧时间轴推进）；
- 必须是 `.js`：`?raw` → `Blob` → `addModule()` 中间无编译步骤（research §3.1）。
  `node --check` 通过；它不在 `tsconfig.app.json` 的 `include: ["src"]` 里参与类型检查
  （`.js` 且未开 `allowJs`），符合 research §3.1 的预期。

### 4.2 `services/live-audio-player.ts`（新增）

导出的公开面与任务书一致，另加 `audioDueMs()`（纯函数）与 `stats.held`：

```ts
export function audioDueMs(clock, ptsUs): number   // clock.wallMs + (ptsUs-clock.ptsUS)/1000 + targetLatencyMs
export function createLiveAudioPlayer(options): LiveAudioPlayer
export interface LiveAudioPlayerStats {
  droppedStale, droppedAhead, rebased, played, held, degraded
}
```

- **`AudioContext` 装配**严格照 research §3.2：`new AudioContext()` → `?raw` 源 →
  `Blob({type:'text/javascript'})` → `createObjectURL` → `addModule` → `finally revokeObjectURL`
  → `new AudioWorkletNode(ctx, AUDIO_WORKLET_PROCESSOR_NAME, {numberOfInputs:0, numberOfOutputs:1, outputChannelCount:[2]})`
  → `connect(GainNode)` → `connect(ctx.destination)`；
- **`resume()` 一次都没调用**（`ctx.resumeCalls === 0` 有单测钉住）；
- `start()` 幂等（重复/并发调用返回同一个 Promise）、`close()` 幂等（重复 close 均 resolve）；
- `ctx.audioWorklet` 缺失或 `addModule` 抛错 → `stats.degraded = true`，`start()` 正常 resolve，
  `push()` 变空操作，且**关掉已建的空上下文**不泄漏；
- 投递策略逐条对齐 design.md §4.2：陈旧 250 ms 丢（`droppedStale`）、超前 400 ms 重锚
  （`droppedAhead` + `rebased`）、`clock === null` 只缓存 100 ms（`UNMAPPED_HOLD_MS`）、
  环深 > 250 ms 重锚；重锚三处（首包 / `AUDIO_FLAG_DISCONTINUITY` / 外部 `flush()`）
  全部收敛到同一条 `reanchor()` + `beginTimeline()` 路径。
- 布局：`startDueFrame` 由 `ctx.currentTime * outputRate + (startDueMs - now)/1000*sampleRate` 折算
  —— worklet 的 `currentFrame` 主线程读不到，`ctx.currentTime` 与它同源。

### 4.3 `services/presentation-scheduler.ts`：只加只读快照

```ts
export interface PresentationClockSnapshot { wallMs; ptsUS; targetLatencyMs }
clockSnapshot(): PresentationClockSnapshot | null
```

`dueOf` / `push` / `takeDue` / `clear` 一行未改；`clock === null` 时返回 `null`
（「队列跑空即作废」的语义保留）。返回**副本**，外部改写不影响内部时钟。

### 4.4 `services/webcodecs-decoder.ts`：加 `onResync`

`StreamDecoderOptions.onResync?: (reason: 'stream_discontinuity' | 'decode_queue_rebuild' | 'frame_skip') => void`，
在既有三个分支旁各调一次（`stream_discontinuity` 日志点、`decode_queue_rebuild` 硬阈值点、
软阈值持续超限点）。回调包在 `notifyResync()` 里 `try/catch`：外部回调抛错不能把解码循环带走。
**没有**重构解码器，也没有改任何现有分支的判定。

### 4.5 `hooks/use-webcodecs-render-loop.ts`：暴露 `getPresentationClock()`

- 返回值新增 `getPresentationClock: () => PresentationClockSnapshot | null`，
  内部 `schedulerRef.current?.clockSnapshot() ?? null`（走 ref，无重渲染、不进依赖数组）；
- `onResync` 从 options 透传给 `StreamDecoder`，走与 `onDecodeError` 相同的 ref 模式；
- 渲染循环行为零改动。

### 4.6 测试

`services/live-audio-player.test.ts`（新增 19 条）：`vi.stubGlobal('AudioContext' | 'AudioWorkletNode', ...)`
+ 注入 `now?: () => number`（不用 fake timers）。覆盖：`due` 计算（`{wallMs:1000, ptsUS:0, targetLatencyMs:100}`,
pts=50_000 → 1150）、陈旧丢弃及其 250 ms 边界、超前重锚、环深超限重锚、`clock === null`
缓存 100 ms 超时丢弃、`flush()` 计数、`DISCONTINUITY` 重锚且该帧不被丢、`addModule` 抛错降级、
`audioWorklet` 缺失降级且关掉空上下文、`close()` 幂等 + close 后 `push()` 不抛、
`setMuted`/`setVolume` 只动 GainNode、未 `start()` 时 `push` 不建上下文。

`services/live-audio-worklet.test.ts`（新增 10 条）：`?raw` 源文本在**主线程**用
`new Function` 装载，用假的 `AudioWorkletProcessor` / `registerProcessor` 与
`sampleRate` / `currentFrame` 全局构造处理器，直接调 `process()` 喂/取 `Float32Array`。
覆盖：注册名与 TS 侧常量逐字一致、8k→48k 线性重采样（1 秒输入铺满 1 秒输出）、
48k 1:1 直通、欠载输出干净静音（不跳读/不循环旧样本）、首包预滚静音、
欠载恢复重新淡入、`flush` 后静音、采样率切换自动重锚、溢出丢最旧、畸形消息不抛。
这样做的原因：worklet 跑在音频线程、测试拿不到它的实例，但它的逻辑全是纯算术；
重采样比例写反或欠载跳读在真实设备上只表现为「声音怪」，无法定位。

`presentation-scheduler.test.ts`（新增 7 条 `clockSnapshot` 用例，**既有 20 条未改**）；
`webcodecs-decoder.test.ts`（新增 3 条 `onResync` 用例，**既有用例未改**）。

```bash
cd web && ./node_modules/.bin/vitest run --browser.headless \
  src/features/live/services/live-audio-player.test.ts \
  src/features/live/services/live-audio-worklet.test.ts \
  src/features/live/services/presentation-scheduler.test.ts \
  src/features/live/services/webcodecs-decoder.test.ts
# → Test Files 4 passed (4) / Tests 62 passed (62)

cd web && ./node_modules/.bin/vitest run --browser.headless src/features/live
# → Test Files 32 passed (32) / Tests 214 passed (214)   ← 未破坏既有用例（C3-a 时是 175）

cd web && ./node_modules/.bin/tsc -b                                  # → 无输出
cd web && ./node_modules/.bin/eslint src/features/live                # → 无输出（exit 0）
cd web && ./node_modules/.bin/prettier --check src/features/live      # → All matched files use Prettier code style!

node --check src/features/live/services/live-audio-worklet.js         # → 通过（worklet 不参与 tsc）
```

### 4.7 与 design.md 的偏差及原因

| 设计稿 | 实际实现 | 原因 |
| :--- | :--- | :--- |
| worklet 用 `new URL('./xxx-worklet.ts', import.meta.url)` | `?raw` + `Blob` + `createObjectURL` | Vite 8 下前者被 `addModule()` 拒绝（research §1/§4，design.md 已同步更正）。 |
| worklet 有 `{type:'config', gain}` 消息 | 不发这条消息，音量全走 `GainNode` | 任务书要求「AudioParam 或 `GainNode` 选一，别重复」。`GainNode` 还能在无音频流时独立生效，且 `setTargetAtTime` 自带短渐变，避免拖滑杆的咔哒声。 |
| 「环深 > 250 ms 丢旧帧」 | 环深 > 250 ms → `flush()` + 重锚 | ①「播放到了哪一帧」在音频线程里，主线程只有估算值，逐帧丢旧估偏就会丢掉还没播的样本 → 断音；②环深超限通常是时间轴整体错位，逐帧丢旧只能缩小偏差，重锚一步到位；③与 `due > now + 400` 复用同一条路径，代码与出错面都小一个数量级。代价是一次约 120 ms（`PRE_ROLL_MS`）的可闻静音。 |
| 「环深 < 40 ms 插 10 ms 淡入」 | 只在 worklet 欠载路径做 10 ms 淡入，player 侧无独立判定 | 任务书已明确「后一条在 worklet 的欠载路径里已覆盖」。player 侧只能靠估算环深，再加一层判定会与 worklet 的真实读指针打架。 |
| 任务书写 `stats: {droppedStale, droppedAhead, rebased, played}` | 多两个字段：`held`（当前暂存帧数）、`degraded`（降级标记） | `degraded` 是任务书 §2 明确要求的（「`stats` 里标记（加一个 `degraded: boolean`）」）。`held` 用于断言「`clock===null` 时真的缓存了」，否则这条契约无法观测。 |
| 任务书写 `clock === null` 时「最多缓存 100 ms」 | 缓存同样受 `close()` / 重锚清空，且 worklet 未就绪（`!attached`）时也走同一条暂存路径 | `start()` 的 `addModule` 有真实异步窗口，首包不该因为「图还没装好」就被丢；两条路径共用 100 ms 预算，语义统一。 |
| 任务书写 `PresentationClockSnapshot` 定义在 player 里 | 定义在 `presentation-scheduler.ts`，player `export type { PresentationClockSnapshot }` 转发 | 快照的形状由 `clockSnapshot()` 的返回类型单点决定。两处各写一份 interface 是结构类型，漂移了编译期不会报警。对外导入路径（`from './live-audio-player'`）不变。 |
| 未提及 `DISCONTINUITY` 帧与「超前」判定的冲突 | `DISCONTINUITY` 帧**跳过**陈旧/超前判定，直接 `beginTimeline()` | 实测发现：跳变帧自己的 `due` 必然远超前（`droppedAhead` 分支会把它当跳变帧处理），若再走一次 `schedule()` 就会被刚触发的那次跳变判成「已过期」而丢掉，等于永远等不到新起点。这与 design.md「以本包为新锚点起播」的意图一致，只是实现上必须显式短路。 |

### 4.8 本切片引入的风险点

- ⚠️ **`startDueFrame` 依赖 `ctx.currentTime`**。无手势时 `ctx.state === 'suspended'`
  且 `currentTime` 不推进，因此解禁前投递的 `startDueFrame` 全部堆在同一个基准上；
  真正出声依赖「首次手势后 `resume()` 让 `currentTime` 开始推进、后续帧按新基准重新排期」。
  这条只能在真实设备上手测 —— 无手势的浏览器里 `resume()` 永久挂起，单测无法覆盖。
- `push()` 直接把调用方传入的 `pcm` 交给 `port.postMessage`（**没有** transfer、也没有拷贝）。
  结构化克隆语义下这是安全的（worklet 拿到的是副本），但调用方若复用一个 `pcm` 缓冲写下一帧
  （`decodeG711` 支持 `out` 复用），投递路径不持有它的引用，也不存在跨帧串音。
  代价是每包一次克隆（160~960 样本），可接受。

## 5. 未覆盖 / 留给 C4

> 原标题是「未覆盖 / 留给 C3-b 与 C4」。C3-b 已完成的部分见 §4，下面保留原始清单
> 以免丢失上下文，并标注本轮的实际状态。
>
> **C4 已全部交付，见 §6**；本节保留为历史对照。

- ~~`live-audio-player.ts` + `live-audio-worklet.js`（环深、`due` 投递、`flush`、`setMuted`、`setVolume`）~~ —— **§4 已完成**；
- ~~`presentation-scheduler.ts` 的 `clockSnapshot()`、`webcodecs-decoder.ts` 的 `onResync`、
  `useWebCodecsRenderLoop` 暴露 `getPresentationClock()`~~ —— **§4 已完成**（只暴露接口，接线待 C4）；
- AAC：`AudioDecoder` + `description = audioInfo.config`、codec 串由 ASC 推导、`isConfigSupported()` 降级；
  ⚠️ 本轮 `AudioChannelInfo.config` 只是把 base64 原样透出，**没有**做 base64 → `Uint8Array` 的转换，
  也没校验 ASC 长度 —— 那是解码器装配时的事；
- `useLiveGrid` 的 `activeCellId` / `volume` / 互斥 / 焦点回收；
- `live-player.tsx` / `live-player-cell.tsx` 接线：`useStreamConnection` 已能返回 `streamInfo`、
  `useWebCodecsRenderLoop` 已能给出 `getPresentationClock()` + `onResync`，但**没有任何组件消费它们**，
  喇叭按钮仍不存在；
- i18n：`unmute` / `mute` / `volume` / `noAudio` / `audioUnsupported`。

---

## 6. 切片 C4：AAC、能力 UI 与单焦点

### 6.1 新增

**`services/aac-decoder.ts`** —— `parseAudioSpecificConfig(base64)` → `{codec, sampleRate, channels, description}`。
ASC 位解析：`audioObjectType` 前 5 bit（31 则 `32 + 后 6 bit`）、`samplingFrequencyIndex` 4 bit（15 = 显式 24 bit）、
`channelConfiguration` 4 bit。AOT 2/5/29 → `mp4a.40.2/5/29`，其余返回 null。
采样率以 ASC 为准（与后端 `11-03-dual-track-native/progress.md` §3 的口径一致：`mpeg4-generic/90000`
会让 `codecpar->sample_rate` 失真）。

**`services/audio-frame-decoder.ts`** —— 帧 → PCM 的分派，`createAudioFrameDecoder({info, player, onDegrade})`。
- G.711：`decodeG711(law, payload)` → `player.push(frame, pcm, 8000)`；
- AAC：`AudioDecoder` + `description = ASC`；每个 `AudioFrame` → `EncodedAudioChunk`（`type: 'key'`，
  `timestamp = frame.ptsUs`）→ `decode()`；`output` 里 `copyTo('f32-planar')` 取声道 0 →
  `player.push(frame, pcm, audioData.sampleRate)` → **`audioData.close()`**（不关会持续占用音频缓冲）；
- ASC 缺失 / `isConfigSupported()` 为 false / 构造抛错 → `onDegrade()`，不抛给 UI。

**`hooks/use-live-audio.ts`** —— 音频生命周期（建 player → 建 WS → 解 ASC → 解码 → 投递）。
组件里那 565 行里抽出 193 行，`live-player.tsx` 回落到 433 行（前者超 300 行 guideline）。
解码器就绪前最多缓存 50 帧（`MAX_PENDING_AUDIO_FRAMES`），按序补投。
卸载顺序固定 **通道 → 解码器 → 播放器**（反了就是往已关闭的播放器里塞帧）。

**测试**：`aac-decoder.test.ts`、`audio-frame-decoder.test.ts`、`live-player-audio.test.tsx`、
`hooks/use-live-audio.test.ts`。

### 6.2 修改

- `use-live-grid.ts`：`setCellMute(id, false)` 互斥（其他格子 + **同一 stream key 的格子**一并静音）；
  `assignCamera` / `clearCell` / `clearAllCells` / `setCellStreamType` 触及当前出声格子时回收焦点。
- `live-player.tsx`：新增 `streamType` / `hasAudio` / `volume` / `onAudioStatusChange` prop；
  `isMuted` 现在**真的生效**；`onResync` → `player.flush()`；保留 `setMute` 命令式接口。
- `live-player-cell.tsx` / `live-player-cell-footer.tsx` / `live-grid.tsx` / `live-toolbar.tsx` / `index.tsx`：
  喇叭按钮、置灰、tooltip、音量透传。
- i18n（三语）：`noAudio` / `audioUnsupported` / `volume` / `audioChecking`。

### 6.3 复核阶段修正的问题（主会话，非实现代理）

| 问题 | 修法 |
| :--- | :--- |
| `streamInfo == null` 与「确定无音频」共用 `noAudio` 文案 —— 能力帧在建连后几帧才到，正常相机在首帧期间会弹「该路无音频」，读起来像设备故障 | 拆成两态：`null` → `audioChecking`（尚不确定），`present=false` → `noAudio`（确定）。三语文案同步。 |
| `use-live-audio.ts` 在 render 期间写 `ref.current`（React 19 eslint `Cannot access refs during render`） | 两个 ref 均改为 `useEffect` 同步。 |
| `use-live-audio.ts` 被重构出来后**没有任何直接测试** | 新增 `use-live-audio.test.ts`（7 例）。其中「卸载顺序」用例经**故意破坏实现验证**：交换 channel/player 的 close 顺序 → 用例失败，确认它真的在测顺序而不是测「都关过」。 |

### 6.4 实测证据

```bash
cd web && ./node_modules/.bin/vitest run --browser.headless
# → Test Files 151 passed (151) / Tests 1078 passed (1078)

cd web && ./node_modules/.bin/tsc -b            # 通过
cd web && ./node_modules/.bin/eslint src/features/live   # 通过
cd web && ./node_modules/.bin/prettier --check src/features/live  # 通过
cd web && ./node_modules/.bin/vite build        # 通过
```

### 6.5 与设计稿的偏差

| 设计稿 | 实现 | 原因 |
| :--- | :--- | :--- |
| 音频生命周期写在 `LivePlayer` 内 | 抽成 `hooks/use-live-audio.ts` | 塞进组件后 `live-player.tsx` 到 565 行，超 300 行 guideline；且三个跨 render 对象（player / channel / decoder）每次重渲染都可能重建，表现为「每次交互声音断一下」 |
| 未规定 `streamInfo` 未到时喇叭如何显示 | 独立 `audioChecking` 状态 | 「还不知道」不等于「确定没有」，见 §6.3 |
| 未规定 `EncodedAudioChunk` 的 `type` | 固定 `'key'` | RTSP 送的裸 AAC AU 都是独立可解帧；标成 `delta` 会让解码器等一个永远不会来的关键帧 |

### 6.6 仍未验证（需要真实硬件 / 真实相机）

- 真实摄像头的 G.711 / AAC 出声（本仓库只有 `rtsp_fixture.py` 桩，无真实设备）；
- 音画同步实测（白闪 + 1 kHz beep @240fps，父任务 AC-3 的稳态 ≤ 80 ms）；
- 两个浏览器会话同时收听的端到端用例（父任务 AC-4）；
- 服务端 48 帧队列 + 慢客户端的真实背压表现（父任务 AC-2 的端到端部分）。

上述属于 D（`11-03-dual-track-soak`）的范围，其中板端与同步测量需要 RK3568 实机。
