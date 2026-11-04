# 实施计划：实时预览按需音频（每客户端单通道）

> 修订 3。与修订 2 相比：音频改成独立按需通道，新增"每会话一条"的注册表，删掉配置开关、ZLM2 版本升级与分发器改造。

## 阶段依赖

```
Phase 0 ──> Phase 1 ──> Phase 2 ──> Phase 3 ──> Phase 4 ──> Phase 5
（测试桩）  （Native）   （Go 通道）   （G.711）    （AAC+UI）  （门禁/板端）
```

Phase 3 可以在 Phase 2 的音频帧契约冻结后用 fixture 回放并行开工；Phase 4 依赖 Phase 3 的时钟与播放器骨架。

---

## Phase 0：测试桩与失败用例（前置，不可跳过）

- [x] **0.1 扩展 RTSP 桩**（`native/tests/rtsp_fixture.py`）
  - SDP 按 URL 关键字：默认纯视频、`audio-pcma`、`audio-pcmu`、`audio-aac`、`audio-latm`、`audio-broken`；
  - AAC 档位写全 `a=rtpmap:97 mpeg4-generic/90000` 与 `a=fmtp:97 streamtype=5;profile-level-id=1;mode=AAC-hbr;sizelength=13;indexlength=3;indexdeltalength=3;config=<hex ASC>`（SDP 的 `config` 是十六进制）；
  - SETUP 按 `trackID` 分配交织通道（视频 0-1、音频 2-3）；UDP 模式按轨各开一对端口；
  - 音频 RTP 生成器：G.711 每 20 ms 160 采样 / 8 kHz，AAC 每包 1024 采样 / 90 kHz + AAC-hbr AU header；
  - 新增可断流开关（只停音频 / 只停视频）。
- [x] **0.2 失败用例先行**（`native/tests/rtsp_integration_test.cpp`）
  - 断言当前实现音频轨数为 0（红）；
  - 断言纯视频档位行为与今日一致（绿，作为基线锁）。

**验证**

```bash
make native-deps          # 仅首次
make native-test          # 期望：新增音频用例红，其余绿
```

---

## Phase 1：Native 双轨采集、归一化与 ABI v5

- [x] **1.1 ABI 演进**（`native/include/Zhulong/engine.h`）
  - `ZHULONG_ABI_VERSION` → 5；
  - `ZHULONG_TRACK_VIDEO/AUDIO`、`ZHULONG_TRACK_MASK_VIDEO/AUDIO`、`Zhulong_CODEC_AAC/PCMA/PCMU`；
  - `Zhulong_packet_view` 尾部追加 `track_type` + 3 个 reserved（16 字节，保持 8 字节对齐）；
  - `Zhulong_stream_subscribe_track(..., track_mask, ...)`；旧 `Zhulong_stream_subscribe` 保持"仅视频"语义；
  - `Zhulong_audio_view` + `Zhulong_stream_audio_info`：调用方缓冲区、容量不足返回所需长度、不暴露内部指针；
  - **不新增** `enable_audio` 之类的选项字段。
- [x] **1.2 建连申请音频轨**（`native/src/nodes/capture/rtsp_input.{hpp,cpp}`）
  - 拉流模式 `allowed_media_types = "video+audio"`（⚠️ 实测修正：该 AVOption 是 flags 串，
    只认 `+`/`-`，设计稿写的 `"video,audio"` 会让 `avformat_open_input` 直接失败）；Probe 模式保持 `"video"`；
  - `ProbeSession` 记录 `audio_index`（`related_stream = video_index`），兼容档重试后取**最终** session 的下标；
  - 新增 `Audio` 元数据结构 + `Media{Video, Audio}`；`read_rtsp` 的 `ready` 回调改传 `Media`；
  - extradata 深拷贝（≤256 B）；codec / 采样率 / 声道 / time_base 校验（不通过降级 + WARN）；
  - AAC 的采样率/声道以 **ASC** 为准（`mpeg4-generic/90000` 会让 `codecpar->sample_rate=90000`）；
  - `AAC_LATM` 分支退化为**防御性**：固定 FFmpeg 7.1.5 的 `rtpdec_latm.c` 声明 `AV_CODEC_ID_AAC`
    并把 LATM 剥成裸 AU，实测 MP4A-LATM 相机可直接放行（见 `11-03-dual-track-native/progress.md` §3）；
  - **失败不黑屏**：建连/SETUP 失败 → 关 ctx、以 `"video"` 重开一次；原因缓存到物理流生命周期；只重试一次。
- [x] **1.3 双 sink 读流**（同文件）
  - `read_rtsp` 增加 `consume_audio`；
  - 按 `stream_index` 二选一；音频填 `track_type`、`frame_class = UNKNOWN`、`key_frame = 0`；
  - 音频单包 ≤ 8 KiB；音频不刷新 idle 超时。
- [x] **1.4 音频归一化**（`native/src/pipeline/audio_normalizer.{hpp,cpp}` 新增；`preview_normalizer.hpp` 增只读 getter）
  - `PreviewNormalizer` 暴露 `origin_ready()` / `timestamp_shift_us()` / 单调递增的 discontinuity generation；
  - `Stream` 在原点建立前记一次 `Clock::now()` 作为视频原点到达时刻；
  - `AudioNormalizer`：锚点、每包现读视频 shift、generation 变化置不连续、缺失 PTS 递推 + 合成标志、音频 PTS 回绕展开、单调性保护；纯算术不读钟。
- [x] **1.5 接入流水线与订阅**（`stream.{hpp,cpp}`、`engine.{hpp,cpp}`、`native/src/abi/engine.cpp`）
  - `Stream` 增加 `audio_meta_`（mutex 保护）、`dispatch_audio()`；`dispatch()` 入口断言 VIDEO；
  - 订阅带 `track_mask`：视频订阅只收视频，音频订阅只收音频，各自独立队列；
  - `Engine::audio_info(id)` + `Zhulong_stream_audio_info`（照 `Zhulong_stream_get_status` 的锁序与 `boundary` 包装）；
  - `capture()` 里两条回调分别接到 `dispatch` / `dispatch_audio`。
- [x] **1.6 Native 测试**
  - `audio_normalizer_test.cpp`：原点未建立、锚点、跳变标志、合成 PTS、非单调、回绕；
  - `preview_normalizer_test.cpp`：音频包不改变 `gop_id()` / 视频钟；
  - `rtsp_integration_test.cpp`：四档 + `audio-broken` + 纯视频回归 + ABI 生命周期 + 按轨订阅隔离；
  - ASan / TSan 各跑一遍。

**验证**

```bash
make native-build
make native-test

# ABI 新增导出符号的陈旧产物核验
rm -rf build/native/host
make native-test
nm build/native/host/libZhulongEngine.a | grep -c Zhulong_stream_audio_info        # > 0
nm build/native/host/libZhulongEngine.a | grep -c Zhulong_stream_subscribe_track   # > 0

python3 native/scripts/build.py --sanitizer address test
python3 native/scripts/build.py --sanitizer thread  test
```

**完成判据**：Phase 0 的红用例转绿；纯视频档位视频字段与改造前一致；`audio-latm` 按实测放行或静默降级
（固定 FFmpeg 上它是可播放的裸 AAC，故当前判据是「解封装后能出声」，防线性拒绝分支保留）。

---

## Phase 2：Go 音频通道与能力通告

- [x] **2.1 CGO 桥接**（`internal/engine/{types.go,subscription.go,engine.go,stream.go}`）
  - `Packet.TrackType` / `TrackType` 枚举 / `inboundPacket` 映射；
  - `AudioInfo{HasAudio, Codec, SampleRate, Channels, Config []byte}` + `Engine.AudioInfo(ctx, streamID)`（ABI 写入 Go 提供的 C 缓冲区 → Go 复制 → 释放）；
  - `SubscriptionOptions.TrackMask` + `SubscribeTrack`；现有 `Subscribe` 保持视频语义。
- [x] **2.2 预览 WS 能力帧**（`internal/camera/handler.go`）
  - `/cameras/:id/.../ws` 建连后、启动写协程**之前**同步写一条 `stream_info` 文本帧；
  - `AudioInfo` 查询失败或未就绪 → `present=false` + `reason`，不影响视频；
  - `WritePump` 与 `StreamDispatcher` **不改**。
- [x] **2.3 音频通道注册表**（`internal/camera/audio_channel.go` 新增）
  - 键 = `auth.GetSessionToken(c)` 的哈希前缀；同会话接管旧连接（4001），跨会话独立；
  - 全局硬上限（常量 8），超出拒绝（4002）；
  - 生命周期：`AudioInfo → 无音频则 4404 → SubscribeTrack(AUDIO) → 写协程 → 关闭/溢出 → unsubscribe（同步排空）→ 注册表移除`；
  - 日志：会话哈希前缀、camera、role、建立/关闭原因、接管与溢出计数。**不落 token 原文。**
- [x] **2.4 音频 WS**（`internal/camera/handler.go` 新路由 `/cameras/:id/streams/:role/audio/ws`）
  - 建连先发 `audio_info` 文本帧；随后二进制 20 字节头（magic `ZLA1` / codec / flags / reserved / seq / pts_us）；
  - 队列满 → 关闭通道并记 `backpressure`；写超时 → 关闭；
  - 不做任何视频相关的刷新与统计。
- [x] **2.5 Go 测试**
  - `audio_channel_test.go`：同会话接管、跨会话独立、硬上限、并发 race、`unsubscribe` 后注册表才为空；
  - `wire_test.go`：音频帧头封包/解包往返、discontinuity 标志；
  - 预览 WS 二进制帧 golden 对比（文本帧不改变二进制字节与顺序）；
  - `engine` 侧：关闭流后 `AudioInfo` 返回 NOT_FOUND；按轨订阅互不串包。

**验证**

```bash
make native-build
python3 native/scripts/build.py go test -race ./internal/camera -run 'TestAudioChannel|TestWire|TestStreamDispatcher'
python3 native/scripts/build.py go test -race ./internal/engine -run 'TestAudioInfo|TestSubscribeTrack'
make go-check
```

**完成判据**：不收听音频时，预览链路字节与行为与今日一致；音频通道的任何异常都不改变视频订阅与健康态。

---

## Phase 3：前端 G.711 全链路（先出声）

- [x] **3.1 能力帧与按需连接**（`stream-pool.ts`、`hooks/use-stream-connection.ts`）
  - 文本帧分支 → `onStreamInfo`；`ArrayBuffer` 分支与统计逻辑不动；
  - 新增 `services/audio-channel.ts`：连接/重连/关闭音频 WS、解析 20 字节头、错误帧；
  - `wire-parser.ts` 不动。
- [x] **3.2 G.711 解码**（`services/g711-decoder.ts`）
  - A-law / μ-law 256 项静态表 → Float32；表驱动单测。
- [x] **3.3 呈现时钟快照**（`presentation-scheduler.ts`、`webcodecs-decoder.ts`、`use-webcodecs-render-loop.ts`）
  - `clockSnapshot()`；`onResync` 回调（discontinuity / queue rebuild / 跳帧重同步三处）；
  - `useWebCodecsRenderLoop` 暴露 `getPresentationClock()`。
- [x] **3.4 音频播放器**（`services/live-audio-player.ts` + AudioWorklet）
  - 环形缓冲 + 线性重采样 + `GainNode`；
  - 投递策略：无 clock 缓存 ≤100 ms、陈旧丢弃、超前重锚、环深抖动控制；
  - `flush()` / `setMuted()` / `setVolume()` / `start(clock)`；
  - Worklet 用 `new URL('./xxx-worklet.ts', import.meta.url)` 引入；`addModule()` 就绪后再投递首帧。
- [x] **3.5 接线**（`components/live-player.tsx`）
  - 接 `isMuted` / `hasAudio` / `volume` prop；解禁时建音频通道 + 创建/`resume()` `AudioContext`；
  - 静音、切格、卸载时关闭通道并 `flush()`；
  - 视频 `onResync` → `audio.flush()` + 重锚。
- [x] **3.6 单测**
  - `g711-decoder.test.ts`（静音码、0、满幅）；
  - `live-audio-player.test.ts`（due 计算、陈旧丢弃、大跳重锚、flush）；
  - `audio-channel.test.ts`（20B 头、错误帧、断开重连）；
  - `stream-pool.test.ts`（文本帧不影响统计）。

**验证**

```bash
cd web && pnpm test -- src/features/live/services/g711-decoder.test.ts
cd web && pnpm test -- src/features/live/services/live-audio-player.test.ts
cd web && pnpm test -- src/features/live/services/audio-channel.test.ts
make frontend-check
```

**完成判据**：用 `audio-pcma` / `audio-pcmu` 桩能在浏览器里点喇叭出声；静音/解禁/切格无残留声音；不点喇叭时没有任何音频连接。

---

## Phase 4：AAC、能力 UI 与音频焦点

- [x] **4.1 AAC 解码路径**（`live-audio-player.ts`）
  - `AudioDecoder` + `description = audioInfo.config`；codec 串由 ASC 的 `audioObjectType` 推导；
  - `isConfigSupported()` 失败 → 本路禁用音频 + 文案，视频继续。
- [x] **4.2 能力通告到 UI**（`types`、`use-stream-connection`、`live-player-cell`）
  - `stream_info.audio.present` → cell 状态；无音频/不支持 → 置灰 + tooltip。
- [x] **4.3 音频焦点**（`hooks/use-live-grid.ts`、`live-grid.tsx`、`live-player-cell.tsx`）
  - `activeCellId` / `volume` 进 `useLiveGrid`；
  - 互斥（含同一 stream key）、`muteAll()`、换机/清空/切码流回收；
  - `LivePlayer` 由 prop 驱动。
- [x] **4.4 i18n 文案**（`locales/{zh-Hans,zh-Hant,en}.json`）
  - `unmute` / `mute` / `volume` / `noAudio` / `audioUnsupported`。
- [x] **4.5 测试**
  - `use-live-grid.test.ts`（焦点互斥、同流去重、换机回收、muteAll）；
  - `live-player-cell.test.tsx`（无音频置灰、解禁手势创建 AudioContext、卸载关闭通道）。

**验证**

```bash
cd web && pnpm test -- src/features/live/hooks/use-live-grid.test.ts
cd web && pnpm test -- src/features/live/components/live-player-cell.test.tsx
make frontend-check
```

**完成判据**：G.711 与 AAC 两档都能出声；无音频相机不出现可用喇叭；9 宫格同 tab 最多一路发声；两个登录会话互不影响。

---

## Phase 5：门禁、板端与同步实测

- [ ] **5.1 全量门禁**

```bash
make check
make cross-board
```

- [ ] **5.2 同步实测**（design §6.3：白闪 + 1 kHz beep + 240 fps 采样）
  - 稳态中位数、重同步后 500 ms 回收、10 分钟漂移曲线，三条数据入档。
- [ ] **5.3 板端压测**（RK3568）
  - 16 路连续 30 分钟，其中 1~4 路处于收听状态；
  - 记录 CPU / RSS / 订阅断链 / 音频通道接管与溢出计数；
  - 与"完全不收听"的基线做对照表。
- [ ] **5.4 降级回归**
  - `audio-latm` 档位：无音频 + WARN；
  - `audio-broken` 档位：仍出视频，日志可见"已退回纯视频"；
  - 无音频相机：预览正常、喇叭置灰、不建音频通道；
  - 旧前端（缓存 bundle）收新后端：忽略文本帧，画面不受影响；
  - 新前端收旧后端：无文本帧 → 喇叭置灰，不报错循环。
- [ ] **5.5 内嵌 SPA 新鲜度门禁**
  - `make cross` / `make cross-board` 不重建前端（`Makefile:154` 已有注释）；新增 `webui-dist-check`：`web/dist` 存在且与 `internal/webui/dist` 不一致 → 失败并提示 `make frontend-build`；`web/dist` 不存在时只告警；`cross` / `cross-board` 依赖它。
- [ ] **5.6 文档与 spec**
  - `README` 预览能力说明；`config.toml` 无需新增配置项（本期无开关），若要说明请在 README 写清"音频按需、无配置"；
  - spec 更新：`.trellis/spec/backend/camera-guidelines.md`（音频通道与文本能力帧）、`.trellis/spec/native/ingestion-contract.md`（音频归一化与 ABI v5）、`.trellis/spec/native/cgo-contract.md`（调用方缓冲区元数据契约、按轨订阅）、`.trellis/spec/frontend/*`（播放器时钟契约）；
  - `internal/apidocs` 重新生成。

**完成判据**：AC-1~AC-6 全部有对应证据；spec 与实现一致。

---

## 子任务拆分（已执行）

| 子任务 | 目录 | 范围 | 依赖 |
| :--- | :--- | :--- | :--- |
| **A. 双轨接入**（native） | `11-03-dual-track-native/` | Phase 0 + 1 | 无 |
| **B. 音频通道**（backend） | `11-03-dual-track-protocol/` | Phase 2 | A |
| **C. 前端音频**（frontend） | `11-03-dual-track-web-audio/` | Phase 3 + 4 | B |
| **D. 联调门禁** | `11-03-dual-track-soak/` | Phase 5 | A/B/C |

每个子任务落地后 `dev` 树都必须可用：A 结束时还没有音频出口，B 结束时默认没有任何客户端收听，C 结束时才具备出声能力。

## 未覆盖 / 后续任务

- Probe API 暴露音频能力（创建相机表单直接显示"有/无音频"）；
- AAC-LATM 支持；
- 录像与抓拍的音频混流；
- 音量持久化与按相机记忆；
- 音画固定偏差的按机型校正项（等实测数据）。
