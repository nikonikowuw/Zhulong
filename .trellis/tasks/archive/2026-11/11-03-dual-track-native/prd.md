# PRD: 双轨接入（A · Native）

> 父任务：`.trellis/tasks/11-03-dual-track-preview/`。需求、设计、阶段清单均以父任务 `prd.md` / `design.md` / `implement.md` 为权威来源，本文件只界定子任务边界。

## Goal

让 Native 引擎真正拿到音频轨、把音频包按轨道送到 C ABI，并把音频 PTS 归一化到**与视频同一条输出时间轴**；同时保证音频关闭、缺失或失败时视频路径零回归。

## Scope

包含父任务 **Phase 0 + Phase 1** 的全部内容：

1. RTSP 测试桩扩档：`audio-pcma` / `audio-pcmu` / `audio-aac` / `audio-latm` / `audio-broken`（音频 SETUP 失败），音频 RTP 生成器与按 `trackID` 分配交织通道；
2. 失败用例先行：断言当前实现音频轨数为 0，纯视频档位作为基线锁；
3. `rtsp_input`：拉流模式固定 `allowed_media_types="video,audio"`（无开关）、`av_find_best_stream(related_stream)`、失败自动退回纯视频（只重试一次）、`Media{Video, Audio}` 与 extradata 深拷贝；
4. 双 sink 读流（`consume` / `consume_audio`），音频包固定 `track_type=AUDIO`、`frame_class=UNKNOWN`、`key_frame=0`、单包 ≤ 8 KiB、不刷新 idle 超时；
5. `AudioNormalizer`：锚点 = 视频原点到达时刻 + 首枚有效音频包到达时延；每包现读视频 `timestamp_shift_us` 与 discontinuity generation；源缺失 PTS 递推并置合成标志；
6. ABI v5：`track_type` + 3 个 reserved、`ZHULONG_TRACK_MASK_*`、`Zhulong_CODEC_AAC/PCMA/PCMU`、`Zhulong_stream_subscribe_track`（按轨订阅）、`Zhulong_stream_audio_info`（调用方缓冲区复制，容量不足返回所需长度）；**无** `enable_audio` 字段；
7. Native 单测与 ASan / TSan。

## Out of Scope

- Go 侧 `TrackType` / `AudioInfo` 映射、独立音频通道与能力帧（子任务 B）；
- 任何配置项——本期不存在音频开关；本任务结束时音频还没有对外的传输出口（B 才加），因此生产上不可达；
- 前端任何改动（子任务 C）。

## Dependencies

- 无前置子任务。B 依赖本任务的 ABI v5 与 `AudioInfo` 出口。

## Acceptance Criteria（父任务 AC-1 的子集）

- [ ] 四档音频桩（PCMA / PCMU / AAC / LATM）行为符合预期：前三档探测到音轨并派发音频包，`audio-latm` 无音频 + 一条独立 WARN；
- [ ] `audio-broken` 桩（仍按“永远申请音频”的路径）仍能出视频，日志可见降级原因；若实测 FFmpeg 在该失败下本就能完成 open 并只丢音轨，则保留该行为并删掉重开逻辑（以实测为准，不写无用代码）；
- [ ] 纯视频桩上，视频包 `pts_us/dts_us/gop_id/frame_class/flags` 与改造前逐字节一致；视频订阅（`track_mask=VIDEO`）收不到任何音频包；
- [ ] 音频包不会改变 `PreviewNormalizer::gop_id()`、视频时钟，也不会进入 `DecodeSession`（单测钉死）；
- [ ] `Zhulong_stream_audio_info` 生命周期：锁释放后调用方只读自有内存；容量不足返回明确状态与所需长度；流关闭后返回 NOT_FOUND；
- [ ] `make native-test` 在 ASan 与 TSan 下均通过，含扩桩后的循环回路用例；
- [ ] `rtsp_startup_timing` 的 `info_ms` / `first_packet_ms` 相对改造前劣化 ≤ 10%；
- [ ] ABI 陈旧产物核验：`rm -rf build/native/host` 重建后 `nm` 能看到 `Zhulong_stream_audio_info`。
