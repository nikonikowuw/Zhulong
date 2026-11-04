# 技术设计方案：实时预览按需音频（每客户端单通道）

> 修订 3。核心：**音频不进入预览数据流**。预览 WebSocket、`StreamDispatcher`、ZLM2 二进制帧、前端 `wire-parser` 全部保持不变；音频走一条按需建立、每客户端一条的独立通道。

---

## 0. 不变量（先看这一节）

| ID | 不变量 | 违反后果 |
| :--- | :--- | :--- |
| **I1** | 不收听音频时，预览 WS 的二进制帧**逐字节不变**，`StreamDispatcher` 的视频状态机（GOP / needsKey / 背压 / 活性）完全不变 | 无法灰度、16 路回归面失控 |
| **I2** | 音频**永不**进入 `PreviewNormalizer`、`DecodeSession`、`gopCache`、视频订阅队列、`recordPacketActivity` | 视频时间轴错乱 / GOP 被撑爆 / 健康态误判 |
| **I3** | 音频输出时间戳锚定到视频输出时间轴（同一条 RTSP 会话内构造），不得透传源 PTS | 音画同步从根上不成立 |
| **I4** | `Zhulong_packet_view` 借用契约不变：只在同步回调期间有效 | 悬垂指针、偶发花屏 |
| **I5** | 任一环不支持都退化为"无声视频"，不黑屏、不报错循环 | 边缘现场无人值守，最怕整格不可用 |
| **I6** | 单通道约束是**每客户端**（登录会话）一条，不是全局一条 | 多人使用时只有一个人能听 |

---

## 1. 架构总览

```
                    ┌──────────────────────────────────────────────┐
                    │            RTSP IPC（allowed_media_types      │
                    │               = "video,audio"）              │
                    └───────────────────┬──────────────────────────┘
                                        │ av_read_frame
                    ┌───────────────────┴──────────────────────────┐
                    │           read_rtsp 双 sink 读流              │
                    └───────┬───────────────────────────┬──────────┘
             video_index    │                           │   audio_index
                            ▼                           ▼
              Stream::dispatch（视频）          Stream::dispatch_audio（音频）
              PreviewNormalizer.normalize       AudioNormalizer.normalize
              （原点 / 跳变 / GOP / 分类）       （只读视频原点与跳变偏移）
                            │                           │
                 视频订阅（track_mask=VIDEO）   音频订阅（track_mask=AUDIO）
                            │                           │
                            ▼                           ▼
              Zhulong_packet_callback × N       Zhulong_packet_callback × N
                            │                           │
                            ▼                           ▼
              internal/camera StreamDispatcher   internal/camera AudioChannel
              （一行不改：GOP/背压/活性）         （每会话最多一条 + 全局硬上限）
                            │                           │
                  /cameras/:id/streams/:role/ws    /cameras/:id/streams/:role/audio/ws
                  ZLM2 v2 二进制帧（不变）           ZLA1 20B 头 + 裸负载
                  + 一次性文本能力帧                 + audio_info 文本帧
                            │                           │
                            ▼                           ▼
                    WebCodecsDecoder              LiveAudioPlayer
                    PresentationScheduler         （AudioWorklet 环形缓冲）
                            │                           │
                            ▼                           ▼
                     <canvas> 渲染                  <AudioContext> 输出
                            └──── 同一条 PTS→墙钟映射 ────┘
```

**为什么不让音频走预览 WS**：那需要升协议版本、在 32 字节头里表达轨道、让 `StreamDispatcher` 同时管理两套背压、让前端 parser 分流双轨，并让每个客户端的视频订阅预算被音频挤占。这些复杂度全部是"把两种生命周期不同的流塞进同一条 FIFO"的税。按需 + 每客户端一路之后，音频不需要和视频共享任何状态，独立通道是更小的改动，也是更小的回归面。

---

## 2. Native 引擎与 C ABI 层

### 2.1 建连与探测（`native/src/nodes/capture/rtsp_input.cpp`）

1. **一律申请音频**（无开关）
   - 拉流模式：`allowed_media_types = "video,audio"`（`rtsp_input.cpp:659`）。
   - 独立 Probe 模式：保持 `"video"`，不延长建连耗时；音频能力来自实时流。
   - 原因：FFmpeg 的 RTSP 解复用器在解析 SDP 时就跳过不允许的 `m=` 段（`libavformat/rtsp.c:492`），不申请就永远没有音轨。

2. **音频轨选择**
   - `av_find_best_stream(ctx, AVMEDIA_TYPE_AUDIO, -1, video_index, nullptr, 0)`，用 `related_stream` 绑定视频轨；每路流最多一条音轨。
   - 兼容档重试：`open_and_probe` 失败会 `input.reopen()` 重来一次，`audio_index` 必须取**重试后最终 session** 的下标。

3. **可接受性判定**（任一不满足 → `audio_index = -1`，静默降级 + 一次 WARN）
   - codec ∈ {`AAC`, `PCM_ALAW`, `PCM_MULAW`}；`AAC_LATM` 明确拒绝并单列一条 WARN；
   - `sample_rate` ∈ {8000, 11025, 16000, 22050, 24000, 32000, 44100, 48000}；
   - `channels` ∈ {1, 2}；
   - 音频流 `time_base` 有效。
   - ⚠️ **不要假设 G.711 的 `time_base` 是 1/8000**：FFmpeg 按 RTP 时钟设置 time_base（AAC 通常 1/90000），换算一律走音频流自己的 `time_base`。

4. **失败不得拖垮视频**
   - 申请音频后 `avformat_open_input` / SETUP 失败 → 关闭 ctx，以 `"video"` 重开一次；成功则按无音频继续并记 WARN（带 camera/URL 与失败原因）。
   - 只重试一次；失败原因缓存到物理流生命周期，重连日志直接引用。
   - 实测若 FFmpeg 本身能在音频 SETUP 失败后完成 open，则不加重开逻辑——以 fixture 行为为准，不写无用代码。

5. **元数据提取**
   - `Audio{codec, sample_rate, channels, extradata}`，extradata **深拷贝**（≤256 B）。AAC 的 ASC 只有 2~5 字节，但不能借用 `AVCodecParameters`。
   - `read_rtsp` 的 `ready` 回调由 `void(const Video&)` 改为 `void(const Media&)`（`Video + Audio`），在同一线程完成，`on_ready` 之前即可查询。

### 2.2 双 sink：结构上隔离

```cpp
Video read_rtsp(const std::string& url, const Options& options, Cancellation& cancel,
                const std::function<void(const Zhulong_packet_view&)>& consume,        // 视频
                const std::function<void(const Zhulong_packet_view&)>& consume_audio,  // 音频
                const std::function<void(const Media&)>& ready,
                uint64_t stream_id);
```

- 读流循环按 `stream_index` 二选一；音频包**只**走 `consume_audio`，为空直接 `av_packet_unref`。
- 音频包固定：`track_type = ZHULONG_TRACK_AUDIO`、`codec = AAC/PCMA/PCMU`、`time_base_*`、`pts/dts`、`key_frame = 0`、`frame_class = Zhulong_FRAME_UNKNOWN`。
- 视频包固定 `track_type = ZHULONG_TRACK_VIDEO`，其余字段与今日一致。
- **不做**"一个回调 + 下游分支"：`Stream::dispatch` 第一行就无条件 `preview_normalizer_.normalize()`，音频会污染视频钟、参与 `++gop_id_`、被前置注入 SPS/PPS。
- `Stream::dispatch()` 入口断言 `track_type == VIDEO`；音频走 `dispatch_audio()`，两者结构上不可能串线。

### 2.3 AudioNormalizer：锚定到视频输出时间轴

音频的**归一化始终运行**（很便宜），与有没有订阅者无关——这样锚点在流启动时就被固定，用户几分钟后才点喇叭也依然对齐。

锚点（每个物理流一次，视频时间轴跳变不重建）：

```
V   = 视频首包到达时刻（Stream::dispatch 在原点建立前打一次 Clock::now()）
A,a = 视频原点建立之后的首枚有效音频包：(媒体绝对微秒, 到达时刻)
base = a - V
```

逐包映射，每包现读视频 `timestamp_shift_us` 与 discontinuity generation：

```
audio_abs_us = audio_pts * 1e6 * tb_num / tb_den     // 128 位中间量，先展开音频自身回绕
audio_out_us = (audio_abs_us - A) + base + video_shift_us
```

| 情况 | 处理 |
| :--- | :--- |
| `origin_ready() == false`（视频首帧未到） | 丢弃并计数 `audio_dropped_before_origin`，不自行建原点 |
| 锚点未建立 | 以本包建立 `(A, a)`，输出从 `base` 开始 |
| 源缺 PTS | 按上一包递推（步长优先 `pkt->duration`，否则 PCMA/PCMU 按 `pkt->size`、AAC 按 `frame_size` 缺省 1024），置 `FLAG_TIMESTAMP_SYNTHETIC` |
| video discontinuity generation 变化 | 用新 shift，置 `FLAG_DISCONTINUITY`；音频侧不自行推算跳变 |
| 输出非单调 | 按跳变处理：置不连续标志，绝不静默回拨 |

- `AudioNormalizer` 不读钟、不加锁（`on_ready` 与两个 dispatch 都在拉流 worker 上）；`(a, V)` 由 `Stream` 传入，单测可确定性复现。
- 已知局限：相机在 PLAY 后积压数秒音频再吐出时 `base` 含积压量，表现为声音滞后；由前端的陈旧丢弃与环深微调兜底，不加额外复杂度。

### 2.4 C ABI 演进（v5，只在尾部追加）

```c
#define ZHULONG_ABI_VERSION 5

enum { ZHULONG_TRACK_VIDEO = 0, ZHULONG_TRACK_AUDIO = 1 };
enum { ZHULONG_TRACK_MASK_VIDEO = 1u << 0, ZHULONG_TRACK_MASK_AUDIO = 1u << 1 };

/* codec 枚举追加（与 H264=1 / H265=2 并存） */
enum { Zhulong_CODEC_AAC = 16, Zhulong_CODEC_PCMA = 17, Zhulong_CODEC_PCMU = 18 };

/* Zhulong_packet_view 尾部追加 16 字节，旧调用方零初始化即「视频」 */
uint32_t track_type;   /* ZHULONG_TRACK_* */
uint32_t reserved0;    /* 恒为 0 */
uint32_t reserved1;
uint32_t reserved2;
```

```c
/* 按轨订阅：旧接口保持「仅视频」语义不变 */
Zhulong_status_t Zhulong_stream_subscribe_track(Zhulong_engine_h engine, Zhulong_stream_id stream,
    uint64_t preview_consumer_id, uint32_t track_mask,
    Zhulong_packet_callback callback, uintptr_t token,
    Zhulong_subscription_id *out_subscription);
```

```c
/* 元数据由调用方缓冲区接收；调用期间复制，不返回内部借用指针 */
typedef struct {
    int32_t  has_audio;
    int32_t  codec;            /* Zhulong_CODEC_AAC / PCMA / PCMU */
    int32_t  sample_rate;
    int32_t  channels;
    uint8_t *extradata;        /* 调用方缓冲区；AAC: AudioSpecificConfig */
    size_t   extradata_capacity;
    size_t   extradata_size;   /* 输出：实际字节数；容量不足时返回所需长度 */
} Zhulong_audio_view;

Zhulong_status_t Zhulong_stream_audio_info(Zhulong_engine_h engine, Zhulong_stream_id stream,
                                           Zhulong_audio_view *out_view);
```

- 实现照 `Zhulong_stream_get_status`（`engine.cpp:275`）的锁序：`boundary` 包裹 → `control` 锁下 `find_stream` → 流 `mutex` 下复制元数据与 extradata；容量不足返回明确状态与所需长度。
- **不允许**返回 `audio_meta_` 内部指针，也不以"立即深拷贝"替代生命周期保证。
- 采样率/声道**不进包视图**：它们是流级常量，走 `Zhulong_stream_audio_info`。
- 不再有 `enable_audio` 选项：音频一律申请，失败降级。

### 2.5 资源与安全边界

- 音频单包 ≤ 8 KiB（G.711 20ms = 160 B，AAC 一帧 < 2 KiB），超限丢包计数；extradata ≤ 256 B。
- 音频分支不触碰 `cancel.expire_after`（空闲超时仍由视频包刷新）。
- 音频回调的异常隔离与视频同路径（`SubscriptionBase::execute_callback`）。
- 音频订阅使用与视频同构的有界队列，但**独立实例**；队列溢出的 `ErrBackpressure` 只关闭该音频订阅。

---

## 3. Go 宿主层

### 3.1 预览路径零改动

- `StreamDispatcher`、`packFrame`、`ZLM2` 头、GOP、`needsKey`、`LagResyncs`、`EvidencePacketActivity`、订阅预算：**一行不改**。
- `internal/engine` 只做两件新增：`TrackType` 映射 + `AudioInfo` 查询 + `SubscribeTrack`。
- 预览 WS 唯一新增：建连时由 handler 在**启动写协程之前**同步写一条文本能力帧（此刻只有一个写者，不需要锁），保证它排在所有二进制帧之前。

### 3.2 能力帧（预览 WS，一次性文本帧）

```json
{
  "type": "stream_info",
  "v": 2,
  "cameraId": "cam-1",
  "role": "main",
  "audio": {
    "present": true,
    "codec": "pcma",
    "sampleRate": 8000,
    "channels": 1,
    "config": ""
  }
}
```

- 旧前端 `event.data instanceof ArrayBuffer` 不成立 → 直接忽略，安全。
- `config` 是 extradata 的 **base64**（G.711 为空串，AAC 为 ASC）；注意与 SDP 里 `a=fmtp` 的 `config=`（十六进制）不是一回事。
- 能力未知（`AudioInfo` 尚未就绪/查询失败）时 `present=false` + `reason`，前端置灰即可。

### 3.3 音频通道管理（`internal/camera/audio_channel.go` 新增）

```go
type AudioChannelRegistry struct {
    mu        sync.Mutex
    bySession map[string]*audioChannel // key: sha256(sessionToken) 前 16 位
    total     int                      // 全局通道数，硬上限
}
```

- **键是登录会话**（`auth.GetSessionToken(c)`），不是用户 ID、不是全局：一个会话一条，多人各自独立（I6）。日志只记 token 的哈希前缀，不落原文。
- **接管语义**：同一会话再开一条 → 关闭旧连接（close code 4001 `superseded`）再建新的。刷新页面/切格不会留下僵尸，也不会两个人抢同一个会话。
- **硬上限**：全局同时存在的音频通道数（常量，例如 8）超出即拒绝（close code 4002 `too_many_channels`），防止脚本刷连接。它不是配置项。
- **生命周期**：`open → 查 AudioInfo（无音频则 close 4404）→ Engine.SubscribeTrack(AUDIO) → 写协程泵帧 → 客户端关闭/溢出 → unsubscribe（同步排空）→ 从注册表移除`。注册表移除必须在 `unsubscribe` 之后，保证不会出现"注册表说没有、但回调还在跑"。
- **音频帧转发**：native 回调里把 `Zhulong_packet_view` 复制成 20 字节头 + 负载，投进该通道的有界队列（48 帧 ≈ 0.96s）；队列满则关闭通道并上报 `backpressure`，**绝不阻塞 native 回调、绝不触碰视频订阅**。

### 3.4 音频 WS 协议

路由：`GET /api/cameras/:id/streams/:role/audio/ws`（与预览 WS 同组、同鉴权）。

建连后先发一条文本帧：

```json
{"type":"audio_info","codec":"pcma","sampleRate":8000,"channels":1,"config":"","hasAudio":true}
```

随后全部是二进制帧，20 字节头：

| 偏移 | 长度 | 字段 |
| :--- | :--- | :--- |
| 0 | 4 | magic `0x5A4C4131`（`ZLA1`） |
| 4 | 1 | codec（同 ABI 枚举） |
| 5 | 1 | flags：bit0 discontinuity，bit1 synthetic pts |
| 6 | 2 | reserved（0） |
| 8 | 4 | seq（音频轨独立计数） |
| 12 | 8 | pts_us（i64，与视频同一条时间轴） |
| 20 | N | payload：G.711 原始采样 / AAC raw AU |

- 不使用 ZLM2 头：那里面的 `frame_class` / `gop_id` 对音频没有意义，复用只会制造歧义。
- 无法提供音频（无音轨、codec 不支持、查询失败）时：发 `{"type":"error","code":"NO_AUDIO","reason":"..."}` 后正常关闭。

### 3.5 背压与订阅预算

- 音频订阅：`MaxPackets: 48`、`MaxPacketBytes: 8 KiB`、`MaxBufferedBytes: 512 KiB`（独立于视频订阅，视频仍是默认 32 包）。
- 音频通道队列满 → 关闭通道，前端按需重连；不影响视频。
- 不修改 `SubscriptionOptions` 默认值：它属于视频路径。

---

## 4. Web 前端

### 4.1 接线

- `stream-pool.ts` 只加一个分支：`typeof event.data === 'string'` → `JSON.parse` → `subscriber.onStreamInfo?.(info)`；`ArrayBuffer` 分支一行不改，统计仍只算视频。
- 新增 `services/audio-channel.ts`：负责连接/重连/关闭音频 WS、解析 20 字节头、把帧交给 `LiveAudioPlayer`；`LivePlayer` 在解禁时创建、静音/切格/离开时关闭。
- `wire-parser.ts` **不改**（v2 语义不变）。

### 4.2 时钟：音频挂在视频的呈现时钟上

`PresentationScheduler` 仍是"什么时候画"的唯一权威（`DEFAULT_TARGET_LATENCY_MS = 100`）。

- 新增只读快照 `clockSnapshot(): { wallMs, ptsUS, targetLatencyMs } | null`；
- `useWebCodecsRenderLoop` 通过 ref 暴露 `getPresentationClock()`，`LivePlayer` 交给 `LiveAudioPlayer`；
- `webcodecs-decoder.ts` 新增 `onResync`（`stream_discontinuity` / `decode_queue_rebuild` / 跳帧重同步三处触发）。

```
videoDueMs(ptsUs) = clock.wallMs + (ptsUs - clock.ptsUS) / 1000 + targetLatencyMs
audioDueMs(ptsUs) = videoDueMs(ptsUs)          // 同一条时间轴，无需额外偏移
```

投递策略：

| 情况 | 行为 |
| :--- | :--- |
| `clock === null` | 最多缓存 100 ms，超时丢弃 |
| `due < now - 250 ms` | 陈旧，丢弃并计数 |
| `due > now + 400 ms` | 视为跳变：`flush()` 后以本包为新锚点，从 `now + preRoll` 起播 |
| 环深 > 250 ms | 丢旧帧 |
| 环深 < 40 ms | 插 10 ms 淡入淡出静音（不硬切） |

重锚三处：解禁/建连后首包、`audio` 帧带 discontinuity、视频 `onResync`。

### 4.3 播放后端

- 统一 AudioWorklet 环形缓冲，**不**每包新建 `AudioBufferSourceNode`（8 kHz/20 ms = 50 节点/秒，低端机上可闻爆音）。
- Worklet 用 `new URL('./xxx-worklet.ts', import.meta.url)` 引入；`addModule()` 异步，首帧投递必须等它就绪。
- G.711：256 项静态表 → Float32，Worklet 内做线性重采样到 `AudioContext.sampleRate`。
- AAC：`AudioDecoder` + `description = audioInfo.config`；codec 串由 ASC 的 `audioObjectType` 推导（2/5/29 → `mp4a.40.2/5/29`）；`isConfigSupported()` 失败 → 本路禁用音频 + 文案，视频继续。
- `AudioWorklet` 不可用 → 退回 `AudioBufferSourceNode` 排期 + WARN。

### 4.4 焦点与 UI

- `activeCellId` / `volume` 放进 `useLiveGrid`（`cells[].isMuted` 已是唯一真相源，**不新建全局 store**）。
- `setCellMute(cellId, false)`：其他格子一并静音，同一 stream key 的其他格子也静音；`setCellMute(cellId, true)`：当前焦点则清空。
- `muteAll()`、`assignCamera` / `clearCell` / `clearAllCells` / `setCellStreamType` 涉及当前焦点流变化时回收焦点。
- `LivePlayer` 由 `isMuted` prop 的 effect 驱动，命令式接口只作为 mock 实现。
- `AudioContext` 只在第一次解禁手势里 `new` + `resume()`；默认静音期间不创建。
- 无音频（`present=false`）或浏览器不支持：喇叭置灰 + tooltip，文案区分"该路无音频"与"浏览器不支持该音频格式"。
- 音频通道按需建连，因此**不要**在页面加载时就为所有格子建连。

---

## 5. 兼容性与降级

| 场景 | 期望 |
| :--- | :--- |
| 不收听音频 | 预览 WS 二进制帧逐字节不变，无音频出网 |
| 相机无音轨 | `present=false`，喇叭置灰，不建音频通道 |
| 相机音频 SETUP 失败 | Native 自动重开为纯视频 + WARN；视频出画 |
| 旧前端 + 新后端 | 忽略文本帧，画面不受影响（协议未变） |
| 新前端 + 旧后端 | 无文本帧 → 未知能力 → 喇叭置灰；音频路由 404 → 降级，不报错循环 |
| 同一会话第二个音频通道 | 接管旧连接（4001），不会出现两路混音 |
| 两个登录会话收听 | 各一条，互不影响 |
| AI 路径 / 录像 / 抓拍 | 不感知音频 |

---

## 6. 测试策略

### 6.1 RTSP 测试桩（前置）

`native/tests/rtsp_fixture.py` 按 URL 关键字产出：

| 路径关键字 | SDP |
| :--- | :--- |
| 默认 | `m=video` |
| `audio-pcma` | `m=video` + `m=audio 0 RTP/AVP 8` |
| `audio-pcmu` | `m=video` + `m=audio 0 RTP/AVP 0` |
| `audio-aac` | `m=audio RTP/AVP 97` + `a=rtpmap:97 mpeg4-generic/90000` + `a=fmtp:97 streamtype=5;...;mode=AAC-hbr;sizelength=13;indexlength=3;indexdeltalength=3;config=<hex ASC>` |
| `audio-latm` | `m=audio ... MP4A-LATM/90000`（验证降级） |
| `audio-broken` | 音频 SETUP 返回失败状态（验证 D7） |

- SETUP 按 `trackID` 分配交织通道（视频 0-1、音频 2-3）；UDP 模式按轨各开一对端口；
- G.711 每 20 ms 160 采样 / 8 kHz，AAC 每包 1024 采样 / 90 kHz，且按 AAC-hbr 生成 AU header；
- 提供"只停音频 / 只停视频"开关。

### 6.2 分层用例

| 层 | 用例 |
| :--- | :--- |
| Native | `audio_normalizer_test`（原点未建立、锚点、跳变、合成 PTS、单调、回绕）；`preview_normalizer_test` 音频隔离断言；`rtsp_integration_test` 四档 + `audio-broken` + ABI v5 生命周期；按轨订阅（音频订阅收不到视频包，反之亦然） |
| Go | 音频通道注册表（同会话接管、跨会话独立、硬上限、并发 race）；`unsubscribe` 排空后注册表才移除；音频队列溢出只关音频；预览 WS 二进制帧 golden 对比；`AudioInfo` 关闭流后 NOT_FOUND |
| 前端 | `g711-decoder.test`；`live-audio-player.test`（due / 陈旧 / 重锚 / flush）；`audio-channel.test`（20B 头解析、错误帧）；`use-live-grid.test`（焦点互斥与回收）；`stream-pool.test`（文本帧不影响统计） |

⚠️ 前端测试跑真实无头 Chromium：`AudioContext` 需要用户手势，用 `userEvent` / `page.click` 触发，不要假设 `resume()` 无需激活即可工作。

### 6.3 同步测量

白闪 + 1 kHz beep，240 fps 采样；稳态中位数 ≤ 80 ms，重同步后 500 ms 内回到 ≤ 80 ms，10 分钟内首尾各 1 分钟均值差 ≤ 20 ms。

---

## 7. 风险与未决项

| 风险 | 影响 | 缓解 |
| :--- | :--- | :--- |
| 相机拒绝音频 SETUP，拖垮整条流 | 全部预览黑屏 | 失败自动重开为纯视频（§2.1.4）+ `audio-broken` 桩常驻回归 |
| 16 路都拉音频 | +64 kbps/路（合计 ~1 Mbps）、~800 pps 解复用 | 纯局域网，量级远低于 16 路视频；无人收听时只 unref 不出网；D 阶段实测 |
| 音频与视频时钟不同源 | 长时间播放缓慢漂移 | 环深为误差信号，±1% 重采样 slew；> 250 ms 才丢帧/补静音 |
| 相机编码缓冲导致系统性偏差 | 某机型恒定偏移 | 同会话锚点已抵消启动时刻的延迟差；仍有固定偏移时再按实测加校正项（本期不加配置） |
| 每个收听客户端多一条 WS | 浏览器连接数、重连抖动 | 每客户端最多一条 + 100 ms~1 s 退避重连；只在解禁时建立 |
| AAC-LATM 相机 | 该路无音频 | 探测阶段拒绝 + WARN，UI 显示无音频 |
| 浏览器 AAC 支持差异 | 部分浏览器无法出声 | `isConfigSupported` 前置判定 + 置灰；G.711 覆盖大多数国产 IPC |

**未决**（不阻塞本期）：音量持久化；同一会话多 tab 是否需要各自独立（当前按会话合并）。

---

## 8. 验收映射

| AC | 主要验证手段 |
| :--- | :--- |
| AC-1 | 扩桩后的 `make native-test`（ASan/TSan）+ 音频隔离单测 + `rtsp_startup_timing` |
| AC-2 | Go 单测（注册表 / 接管 / 溢出 / 排空）+ 预览帧 golden 对比 + 音频帧往返 |
| AC-3 | G.711 表单测 + 时钟单测 + 合成源 240 fps 测量 |
| AC-4 | `use-live-grid` 单测 + 无头浏览器双会话用例 |
| AC-5 | `make check` + `make cross-board` + RK3568 30 分钟压测 |
| AC-6 | 日志与计数断言（接管、溢出、降级原因） |

---

## 9. 决策记录

| ID | 决策 | 备选方案 | 为何不选备选 |
| :--- | :--- | :--- | :--- |
| **D2'** | 音频走独立 WS，不挤预览流 | 双轨合流 + ZLM2 v3 | 合流要付协议升级、分发器双状态机、GOP/背压/活性隔离、前端双轨 parser 的复杂度，且旧 bundle 会黑屏；独立通道让视频路径与协议完全不动 |
| **D3'** | 音频通道按**登录会话**限一条 | 全局一条 / 按用户账号一条 | 全局一条会让多人只有一个能听；按账号一条在单账号系统里等价于全局一条。会话粒度既满足"每个客户端一路"，又不影响别人 |
| **D4'** | 音频订阅与视频订阅同构（`track_mask`） | 单独的音频订阅 API | 复用既有有界队列、同步排空与所有权语义，少一套生命周期代码；`ErrBackpressure` 天然只关音频那条 |
| **D5'** | 不升 ZLM2 版本 | v3 方言 | 二进制帧没有任何变化，升版本只会让旧 bundle/旧缓存出问题；能力用文本帧表达，旧客户端天然忽略 |
| **D6'** | 音频仍在同一条 RTSP 会话内采 | 独立的纯音频 RTSP 会话 | 跨会话的 RTP 基点互不相关，`base = a - V` 是唯一能抵消相机音视频编码延迟差的锚；独立会话会把固定偏差留给标定项 |
| **D7'** | 无任何配置开关 | `[preview].audio` / `audio_offset_ms` | 能力靠探测、投递按需、失败自动降级；配置既解决不了坏机型，又制造"喇叭为什么是灰的"的支持成本。校正项等实测数据再议 |
| **D8'** | 拆 A/B/C/D 四个子任务 | 单任务六阶段直推 | A/B/C 各自可独立跑绿且 `dev` 树始终可用；Phase 5 需要真实硬件与 30 分钟压测，不该阻塞前三个归档 |
