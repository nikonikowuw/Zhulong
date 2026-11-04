# PRD: 实时预览按需音频（每客户端单通道）

> 修订 3。相对修订 2 的核心变化：
> 1. **不引入任何配置开关**。音频能力靠探测发现，失败自动退回纯视频；
> 2. 音频**不再挤进预览数据流**：预览 WebSocket 的二进制帧逐字节不变，音频走独立的按需通道；
> 3. 单通道约束是**每客户端**（登录会话）一条，不是全局一条——多人使用时各听各的；
> 4. 每路流仍然最多一条音轨（`av_find_best_stream` 选一条，不做混音）。

## Goal

为 Zhulong 边缘主机引入现场拾音：用户在某个格子上点开喇叭，就能听到那一路的现场声音，并且音画同步。默认全静音；一个客户端同时只听得见一路；不同客户端互不影响。

## Background 与现状分析

| 编号 | 现有事实 / 现状 | 影响 / 根因 |
| :--- | :--- | :--- |
| **F1** | `rtsp_input.cpp:659` 在 RTSP 建连字典里写死 `allowed_media_types=video` | 音频轨在 **SDP 解析阶段**就被跳过，连 stream 都不会创建 |
| **F2** | C ABI（`engine.h`，ABI v4）没有音频包字段、没有音频元数据出口，订阅接口也没有轨道概念 | Go 无法区分轨道、拿不到 AAC 的 AudioSpecificConfig |
| **F3** | ZLM2（`wire.go`）视频帧头只有 H.264/H.265；前端 `wire-parser.ts:88` 遇未知 codec 抛错 | 若把音频塞进预览流，必须升协议版本并让旧 bundle 失效 |
| **F4** | `StreamDispatcher` 围绕视频 IDR / 视频 DTS 背压设计；订阅队列默认仅 32 包 | 音频与视频合流会挤压视频订阅，超限整条订阅断开 |
| **F5** | 预览已由 `PreviewNormalizer` 把视频 PTS 归一化到会话相对时间轴 | 音频若透传源 PTS，两轨差三个数量级；RTP 音视频基点互不相关，不能相减 |
| **F6** | 前端只有视频解码；静音按钮与 `cells[].isMuted` 已存在，`LivePlayer.setMute` 是空实现 | 缺的是音频管线与接线 |
| **F7** | 测试桩 `rtsp_fixture.py` 只宣告 `m=video`，TCP 交织通道写死 `$\0` | 无法构造带音频的端到端用例 |

## Scope & Technical Requirements

### R0. 业务范围

1. 单向现场拾音：摄像机 → 网页端实时解复用、传输、播放；
2. 两向对讲明确排除（Out of Scope）；
3. 音频只做"听"，不参与录像、抓拍、AI 分析。

### R1. Native 引擎与 C ABI

1. **一律申请音频**：拉流模式固定 `allowed_media_types="video+audio"`，不再有开关。独立探测（Probe）模式保持纯视频，不延长建连耗时。
   > ⚠️ 实测修正：该 AVOption 是 **flags 串**（只认 `+`/`-` 前缀），写成 `"video,audio"` 会让 `avformat_open_input` 直接失败。
2. **探测与可接受性**：`av_find_best_stream(AVMEDIA_TYPE_AUDIO, -1, video_index)`，每路流最多一条音轨；codec ∈ {AAC, PCMA, PCMU}，采样率与声道在白名单内，`time_base` 有效。任一不满足即"该路无音频"。
   > 口径修正（D 阶段落实）：`AAC_LATM` **不再判为不可接受**。固定 FFmpeg 7.1.5 的 `rtpdec_latm.c` 声明 `AV_CODEC_ID_AAC` 并把 LATM 剥成裸 AU，实测 MP4A-LATM 相机可直接放行（见 `11-03-dual-track-native/progress.md` §3）。原「明确拒绝 + WARN」只剩防御性分支：解封装后仍不是可接受的 AAC、或 extradata > 256 B 时判为无音频并 WARN。
3. **失败不得拖垮视频**：申请音频后若建连/SETUP 失败，关闭并以 `allowed_media_types="video"` 重开一次；失败原因在物理流生命周期内缓存，只重试一次。
4. **双 sink 与共享时间轴**：音频与视频走两条独立回调，音频在结构上不经过 `PreviewNormalizer` 与 `DecodeSession`；音频 PTS 锚定到视频输出时间轴（锚点由视频原点到达时刻 + 首枚有效音频包到达时延构造），每包读取当前视频跳变偏移与 discontinuity generation。
5. **音频元数据出口**：一次性查询（codec / 采样率 / 声道 / extradata），由调用方提供缓冲区，ABI 调用期间复制；容量不足返回所需长度。
6. **按轨订阅**：订阅接口增加轨道掩码，音频订阅与视频订阅同构（各自独立队列、独立取消、同步排空语义），音频**不进入**视频订阅队列。
7. **资源边界**：包视图仍只在同步回调期间有效；音频单包 ≤ 8 KiB，extradata ≤ 256 B；音频不刷新空闲超时。

### R2. 传输层

1. **预览 WebSocket 零改动**：视频二进制帧逐字节不变，协议版本不升；只在建连时追加一条**文本**能力帧（旧客户端按 `event.data instanceof ArrayBuffer` 忽略，安全）。
2. **音频独立通道**：新增 `/api/cameras/:id/streams/:role/audio/ws`，只在用户解禁某个格子时连接，静音/切换/离开即断开。
3. **单通道约束 = 每客户端一条**：以登录会话（session token）为键，一个会话同时最多一条音频通道；同一会话开第二条时接管（关闭旧连接），避免刷新/切格留下僵尸。不同会话互不影响。服务端另设总通道数硬上限（防滥用，不作为配置项）。
4. **音频帧格式**：20 字节头（magic `ZLA1`、codec、flags、reserved、seq、pts_us）+ 裸负载（G.711 原始采样 / AAC raw AU）。`pts_us` 与视频 PTS 同一条时间轴。
5. **背压语义**：音频订阅有独立有界队列；队列溢出只影响该音频通道（关闭并上报原因），不触碰视频订阅。
6. **能力通告**：预览 WS 的文本帧与音频通道的 `audio_info` 都携带 `hasAudio / codec / sampleRate / channels / config(base64 ASC)`；v2 之外不引入新的二进制语义。

### R3. Web 前端

1. **解码**：G.711a/μ 用 256 项静态查表还原 16-bit PCM；AAC 用 WebCodecs `AudioDecoder`，必须带 AudioSpecificConfig 作为 `description`（RTSP 送裸 AAC，无 ADTS 头）。
2. **输出**：统一 AudioWorklet 环形缓冲 + 线性重采样 + `GainNode`；主线程只按 `due` 投递，不每包新建节点。
3. **音画同步**：音频与视频共用同一个 PTS→墙钟映射（含视频呈现调度器 100ms 目标延迟）；起播、解禁、视频重同步三处重锚并丢弃陈旧音频；稳态偏差在感知阈值内且不随时间发散。
4. **按需连接**：解禁才建连、才创建/恢复 `AudioContext`；默认全静音，遵守 Autoplay Policy。
5. **焦点**：沿用 `cells[].isMuted` 作为唯一真相源，同一 tab 最多一路出声；换机/清空/切码流后焦点回收。
6. **降级**：无音频、codec 不认识、浏览器不支持、非安全上下文、extradata 缺失 → 无声视频 + 明确文案，不黑屏、不报错循环。

### R4. 无配置项

本期**不新增任何配置项**（没有 `[preview].audio`，也没有 `audio_offset_ms`）。理由：

- 能力靠探测，失败自动降级，关开关解决不了"某机型音频坏"的问题；
- 投递按需，没人解禁就没有音频出网，带宽不是需要开关兜底的风险；
- 音画系统性偏差等实测证明存在某机型固定偏移，再按数据加校正项，不预先猜一个旋钮。

### 关键设计决策

| ID | 决策 | 理由 |
| :--- | :--- | :--- |
| D1 | 双 sink（`consume` / `consume_audio`），音频独立归一化器 | 结构上杜绝音频误入视频整形器 |
| D2 | 音频走独立 WebSocket，不挤预览流 | 预览协议、分发器、GOP、健康态、前端 parser 全部零改动，回归面最小 |
| D3 | 每客户端一条音频通道，以登录会话为键 | 与 UI 单焦点一致；多人各听各的；服务端仍能防单会话开多路 |
| D4 | 音频订阅与视频订阅同构（轨道掩码） | 复用既有有界队列、同步排空与所有权语义，不新造一套生命周期 |
| D5 | 不升 ZLM2 版本，能力走文本帧 | 二进制帧逐字节不变，旧 bundle 不会因升级黑屏 |
| D6 | 同一 RTSP 会话内采音频 | 跨会话无法对齐 RTP 基点，`base = a - V` 是唯一能抵消相机音视频编码延迟差的锚 |
| D7 | 音频失败自动退回纯视频，只重试一次 | 新增失败面不能把视频带黑 |

### 升级与回滚

- 预览协议与 ABI 语义对旧前端透明：旧 bundle 忽略文本帧，看不到音频也不受影响；
- 滚动升级期间新旧前端都能出画；音频只在**新前端**解禁时出现；
- 回滚：前端回退即失去音频入口，后端仍只做解复用与丢弃，视频不受影响；Native 侧若需完全停用，回滚二进制即可（无配置依赖）。

## Out of Scope

- 网页端麦克风采集与反向对讲 / 广播喊话（RTSP Backchannel）；
- 录像文件音频混流存盘；
- 音频 AI 分析；
- GB28181 通道音频；
- Probe API 暴露音频能力（能力走实时流通告）；
- AAC-LATM 的**专门**支持（不裁剪/不转封装，依赖固定 FFmpeg 的解封装行为，不作为承诺）；
- 多音轨混音与声道矩阵。

## Acceptance Criteria

- [x] **AC-1（Native）**
  - 三档音频桩（PCMA / PCMU / AAC）均探测到音轨并派发音频包；`audio-latm` 按裸 AAC 放行（固定 FFmpeg 7.1.5 的解封装结果），无法接受时降级为无音频 + WARN；纯视频桩无音频无报错；
  - `audio-broken` 桩（音频 SETUP 失败）仍能出视频，日志可见降级原因；
  - 音频包不改变 `PreviewNormalizer::gop_id()` 与视频时钟，也不进入 `DecodeSession`；
  - 预览订阅（视频轨道掩码）在音频存在与否时行为与今日逐字节一致；
  - `make native-test` 通过 ASan / TSan；`rtsp_startup_timing` 劣化 ≤ 10%。

- [x] **AC-2（传输与 Go）**
  - 预览 WS 二进制帧与改造前逐字节一致（golden 对比）；文本能力帧只在建连时发送一次；
  - 音频通道：同一会话第二条连接接管旧连接；不同会话互不影响；总数超过硬上限时明确拒绝；
  - 关闭音频通道后 native 订阅同步排空、无泄漏、无遗留回调；
  - 慢客户端导致的音频队列溢出只关闭音频通道，视频订阅与健康态不受影响；
  - 音频帧头封包/解包往返测试通过。

- [x] **AC-3（前端解码与同步）**
  - G.711 查表对 A-law / μ-law 的静音码、0、满幅输出期望 PCM；
  - 时钟单测：`due` 计算、陈旧丢弃、前后大跳重锚；
  - AAC：有 ASC 能解帧；ASC 缺失或 `isConfigSupported()=false` 时降级且视频继续；
  - 同步测量（白闪 + 1kHz beep，240fps）：稳态 ≤ 80ms；视频重同步后 500ms 内回收；10 分钟不发散；
  - 解禁瞬间不播放积压音频，起播 ≤ 200ms。

- [x] **AC-4（交互与合规）**
  - 默认全静音，`AudioContext` 只在用户手势后创建/恢复；
  - 单 tab 最多一路出声；同一路流被多个格子显示时仍只一路；
  - 两个登录会话同时收听互不影响（各自一路）；
  - 拖拽换机 / 清空格子 / 主↔子码流切换后焦点回收、旧通道关闭；
  - 无音频相机按钮置灰并说明。

- [x] **AC-5（回归与门禁）**
  - `make check` 全绿、无数据竞争；`make cross-board` 通过；
  - RK3568 16 路连续 30 分钟，其中 1~4 路音频通道处于收听状态：无断链、无 RSS 持续增长、CPU 与基线有量化对照；
  - 不收听任何音频时，预览链路行为与改造前一致。

- [x] **AC-6（可运维性）**
  - 首次建流日志可见 `hasAudio / codec / sampleRate / 降级原因`；
  - 音频通道建立/关闭/接管/溢出都有日志与计数，能定位"谁在听哪一路"。

### 证据索引（D 阶段收口）

| AC | 直接证据 |
| :--- | :--- |
| AC-1 | `review-fixes.md` §1/§4（native 31/31 + ASan/TSan 各 31/31、`audio_fallback_budget_case` 实测 1010 ms）；板端 `/audio-latm`、`/audio-broken` 演练（`11-03-dual-track-soak/research/rollback-drill.md`） |
| AC-2 | `internal/camera/audio_channel_test.go`（4001/4002/4003、同步排空、并发 race）、`wire_test.go`（ZLA1 往返）、`audio_handler_test.go`（端到端 + 文本帧先于二进制帧）；旧客户端演练解析出 ZLM2 v2 帧且忽略文本帧 |
| AC-3 | `11-03-dual-track-soak/research/sync-measurement.md`（294 对样本：稳态 28.9 ms / 重同步后 30.7 ms / 漂移 4 ms）；`g711-decoder`、`aac-decoder`、`live-audio-player` 单测 |
| AC-4 | `live-player-cell.test.tsx`（无音频置灰、旧后端「未知」不报故障）、`use-live-grid.test.ts`（焦点互斥/同流去重/muteAll）、板端双会话演练 |
| AC-5 | `make check` exit 0；`make cross-board` exit 0；`webui-dist-check` 正/负向各一次 |
| AC-6 | `11-03-dual-track-soak/research/board-soak.md`（16 路 30 min × 2 轮对照，零丢帧、零断链、RSS 无增长、+4.4 pp 单核 / 4 条收听通道） |
