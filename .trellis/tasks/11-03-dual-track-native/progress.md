# 实施记录：双轨接入（A · Native）

> 对应父任务 `11-03-dual-track-preview` 的 Phase 0 + Phase 1。
> 本文记录**已完成项、实测证据、与设计稿的偏差**，供 B（协议层）与 D（联调）直接引用。

## 1. 已完成

### Phase 0（测试桩）

- `native/tests/rtsp_fixture.py`
  - SDP 按 URL 关键字扩档：默认纯视频、`audio-pcma`、`audio-pcmu`、`audio-aac`、`audio-latm`、`audio-broken`；
  - SETUP 按 `trackID` 分配交织通道（视频 0-1、音频 2-3），UDP 模式按轨各开一对端口；
  - 音频 RTP 生成器：G.711 每 20 ms 160 采样 / 8 kHz；AAC 每包 1024 采样 / 90 kHz，按 AAC-hbr 加 AU header；
  - 断流开关：`/stop-audio`、`/stop-video`；
  - `main()` 新增两条物理证据断言：`/audio-broken` 必须**恰好 2 次连接**（先 video+audio 失败、再纯视频重开一次），
    `/audio-pcmu` 必须**仍是 1 次**（音频正常时不得多付一次重连）。

> ⚠️ 偏差 1：Phase 0.2「失败用例先行」没有真的先红后绿。音频相关断言是在实现之后一次性补齐的，
> 因此「测试先于实现」这条在本次没有交付证据；对应用例的失败形态靠下面的边界用例补偿
> （`audio-broken` 连接计数、`audio-pcma-stop-video` 空闲超时、AAC extradata 容量不足）。

### Phase 1（Native）

- ABI v5（`native/include/Zhulong/engine.h`）
  - `ZHULONG_ABI_VERSION 5`；
  - `Zhulong_packet_view` 尾部追加 `track_type` + 3 个 reserved（16 B，8 字节对齐不变）；
  - `ZHULONG_TRACK_VIDEO/AUDIO`、`ZHULONG_TRACK_MASK_VIDEO/AUDIO`、`Zhulong_CODEC_AAC/PCMA/PCMU`；
  - `Zhulong_stream_subscribe_track(..., track_mask, ...)`，旧 `Zhulong_stream_subscribe` 退化为
    `subscribe_track(..., TRACK_MASK_VIDEO, ...)`，语义与今日逐字节一致；
  - `Zhulong_audio_view` + `Zhulong_stream_audio_info`：调用方缓冲区、容量不足返回
    `Zhulong_ERR_BUFFER_TOO_SMALL` 并写入所需长度；**不**暴露内部指针。
- `rtsp_input`
  - 拉流模式 `allowed_media_types="video+audio"`，Probe 模式保持 `"video"`；
  - **注意**：FFmpeg 的该选项是 AVOption flags 串，分隔符是 `+`，写 `"video,audio"` 会在
    `avformat_open_input` 阶段直接失败（`Invalid chars ',audio'`）——设计稿里的逗号写法是错的；
  - `av_find_best_stream(AUDIO, -1, video_index)`；兼容档重试后取**最终 session** 的 `audio_index`；
  - 可接受性：codec ∈ {AAC, PCMA, PCMU}、采样率白名单、声道 ∈ {1,2}、time_base 有效；
  - AAC 的采样率/声道以 **ASC** 为准（`mpeg4-generic/90000` 会让 `codecpar->sample_rate=90000`，
    直接下发会让前端 `AudioDecoder` 按 90 kHz 重采样）；
  - 建连失败 → 关 ctx、`"video"` 重开一次（实测该路径确实触发，见 `audio-broken` 连接计数），
    失败原因以 `rtsp_audio_open_failed rc=` 落一条 WARN；
  - 双 sink：音频固定 `track_type=AUDIO`、`key_frame=0`、`frame_class=UNKNOWN`、单包 ≤ 8 KiB、
    **不刷新** idle 超时；
  - 首次建流能力日志：`rtsp_audio_detected codec= rate= channels= tb= extradata=`（AC-6 证据）。
- `AudioNormalizer`（`pipeline/audio_normalizer.{hpp,cpp}`）
  - 纯算术、不读钟、不加锁；`(a, V)` 由 `Stream` 传入；
  - 锚点 `base = a - V`（**允许为负**：音频包可能比视频原点更早到达，夹到 0 会变成最多半帧的系统性滞后）、
    逐包读视频 `timestamp_shift_us` 与 discontinuity generation；
  - 视频原点未建立即丢弃并计数；缺失 PTS 递推 + `TIMESTAMP_SYNTHETIC`；
  - 32 位 RTP 回绕展开；非单调输出强制截平并置 `DISCONTINUITY`（绝不回拨）。
- `Stream`
  - `audio_meta_`（`mutex` 保护）、`dispatch_audio()`、`dispatch()` 入口断言 VIDEO；
  - `video_origin_arrival_us_` 在原点建立前由 `dispatch` 打一次 `Clock::now()`；
  - 订阅按 `track_mask` 过滤，视频/音频**结构上不串线**（音频不进 `PreviewNormalizer`、不进 `DecodeSession`）。
- `Engine` / `abi/engine.cpp`
  - `audio_info()` 照 `get_status` 的锁序；`subscribe(..., track_mask)`；
  - **订阅去重粒度改为「消费者 + 轨道掩码」**：同一预览消费者可同时订阅视频与音频，
    同一轨道集重复订阅仍 `ERR_DUPLICATE`（原实现按消费者去重，会让音频订阅必然失败）。

## 2. 实测证据

```bash
make native-test                                   # 31/31 通过
python3 native/scripts/build.py --sanitizer address test   # 31/31 通过
python3 native/scripts/build.py --sanitizer thread  test   # 31/31 通过
rm -rf build/native/host && make native-build      # 陈旧产物核验
nm build/native/host/libZhulongEngine.a | grep -c "T Zhulong_stream_audio_info"        # 1
nm build/native/host/libZhulongEngine.a | grep -c "T Zhulong_stream_subscribe_track"   # 1
make go-check                                      # 通过（ABI v5 与 Go 桥接一致）
```

- 起播开销（回环、单次）：`video-only=50ms`、`video+audio=30ms`（抖动量级，音频轨未带来可测劣化）。
  门限写成 `video+audio <= video-only + 50ms`，刻意比 AC-1 的 10% 宽，避免把功能测试做成抖动测试；
  真要核 10% 请看 `rtsp_startup_timing` 的 `info_ms/first_packet_ms`（本机回环均为 0~1ms，无劣化空间）。
- 音频活动性：`/audio-pcma` 与 `/audio-pcmu` 各 ≥5 包派发；AAC ≥5 包；LATM ≥5 包。
- 单测覆盖：`ZhulongAudioNormalizer`（原点未建立、锚点、负 base、视频跳变传播、合成 PTS、非单调截平、32 位回绕）。
- 空闲超时：`/audio-pcma-stop-video`（只推音频）在 800ms deadline 上判 `TIMEOUT`，
  且音频回调计数为 0（视频原点未建立 → 音频不下发）。这是「音频不刷空闲超时」的正向证据。

## 3. 与设计稿的偏差（需要 D 阶段同步 spec/PRD）

| 设计稿 | 实测（固定 FFmpeg 7.1.5） | 处理 |
| :--- | :--- | :--- |
| `allowed_media_types="video,audio"` | AVOption 是 flags 串，只接受 `+`/`-`；逗号写法直接 open 失败 | 用 `"video+audio"` |
| AAC-LATM 一律拒绝 + WARN | `libavformat/rtpdec_latm.c` 的 `ff_mp4a_latm_dynamic_handler` 声明 `AV_CODEC_ID_AAC`，`latm_parse_packet` 把 LATM 帧剥成裸 AU，`parse_fmtp_config` 把 ASC 写进 extradata ⇒ **该路相机真实可播** | 保留 `AV_CODEC_ID_AAC_LATM` 防御分支（这样报就说明是不可解封装，明确拒绝），但主路径把它当 AAC 正常放行；`rtsp_integration_test.cpp: audio_latm_depacketized_case` 钉住当前行为 |
| 音频包“每路最多一条音轨” | 与实现一致 | 无需改动 |

> 结论：**AAC-LATM 从「Out of Scope / 明确拒绝」升级为「由 FFmpeg 解封装后当裸 AAC 支持」**，
> 前提仍是 `config=` 里带 ASC（媒体层能解析出采样率/声道）。PRD 的 Out of Scope 与风险表应随 D 阶段一并修正。

## 4. 未覆盖（留给 B/C/D 或后续）

- Go 侧 `TrackType` / `AudioInfo` / `SubscribeTrack` 映射（B）；
- 音频通道注册表、能力帧、ZLA1 帧头（B）；
- 前端解码与同步（C）；
- 16 路板端压测与音画同步实测（D）；
- `rtsp_audio_packet_too_large`（>8 KiB 丢包）只有代码路径，没有桩位与单测；
  G.711/AAC 的实测包长都远小于 8 KiB，暂无构造必要。
