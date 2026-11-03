# 实施计划：双轨接入（A · Native）

> 对应父任务 `implement.md` 的 Phase 0 + Phase 1。顺序不可颠倒：先扩桩写红用例，再动生产代码。

## Phase A0：测试桩与失败用例

- [ ] `native/tests/rtsp_fixture.py` 按 URL 关键字产出 SDP：默认纯视频、`audio-pcma`(RTP/AVP 8)、`audio-pcmu`(0)、`audio-aac`(97, mpeg4-generic)、`audio-latm`、`audio-broken`；
- [ ] AAC 档位写全 `a=rtpmap` 与 `a=fmtp`（`streamtype=5;mode=AAC-hbr;sizelength=13;indexlength=3;indexdeltalength=3;config=<hex ASC>`），SDP 的 `config` 用**十六进制**；
- [ ] SETUP 按 `trackID` 分配交织通道（视频 0-1、音频 2-3）；UDP 模式按轨各开一对端口；
- [ ] 音频 RTP 生成器：G.711 20ms/160 采样/8kHz，AAC 1024 采样/90kHz + AAC-hbr AU header；
- [ ] 新增“只停音频 / 只停视频”开关，供活性用例；
- [ ] `audio-broken` 档位在音频 SETUP 返回失败状态；
- [ ] `rtsp_integration_test.cpp` 先写红用例：当前实现音频轨数为 0；纯视频档位锁基线。

**验证**：`make native-test` —— 新增音频用例红，其余绿。

## Phase A1：双轨采集与归一化

- [ ] `rtsp_input.hpp`：`Audio` 结构、`Media{Video, Audio}`、`ready` 回调改签名；**不新增**开关字段；
- [ ] `allowed_media_types` 固定为 `"video,audio"`（拉流模式）；Probe 模式保持 `"video"`；
- [ ] `av_find_best_stream(AUDIO, -1, video_index)`；兼容档重试后取**最终** session 的下标；
- [ ] codec/采样率/声道/time_base 校验；`AAC_LATM` 单独 WARN；extradata 深拷贝（≤256B）；
- [ ] 失败降级：按实测行为决定是否需要“关 ctx → 纯视频重开一次”，并缓存降级原因；
- [ ] 读流循环按 `stream_index` 二选一；音频固定 `track_type=AUDIO`、`frame_class=UNKNOWN`、`key_frame=0`；单包 ≤ 8 KiB；不刷新 idle 超时；
- [ ] `preview_normalizer.hpp` 暴露 `origin_ready()` / `timestamp_shift_us()` / discontinuity generation；`Stream` 在原点建立前打一次 `Clock::now()`；
- [ ] 新增 `audio_normalizer.{hpp,cpp}`（纯算术、无锁、不读钟）：锚点、每包读 shift、generation 变化置不连续、缺失 PTS 递推+合成标志、非单调保护、PTS 回绕展开；
- [ ] `Stream` 增 `audio_meta_`（mutex 保护）与 `dispatch_audio()`；`dispatch()` 断言 VIDEO；`capture()` 接两条回调；
- [ ] `Engine::audio_info(id)` + C ABI `Zhulong_stream_audio_info`（调用方缓冲区、容量不足返回所需长度）；
- [ ] `Zhulong_stream_subscribe_track(..., track_mask, ...)`：视频订阅只收视频、音频订阅只收音频，各自独立队列；旧 `Zhulong_stream_subscribe` 保持仅视频语义；

## Phase A2：测试与证据

- [ ] `audio_normalizer_test.cpp`：原点未建立、锚点建立、跳变标志、合成 PTS、非单调、回绕；
- [ ] `preview_normalizer_test.cpp`：喂音频包后 `gop_id()` / 视频钟不变；
- [ ] `rtsp_integration_test.cpp`：四档 + `audio-broken` + 纯视频回归 + ABI 生命周期；
- [ ] ASan / TSan 各一遍；
- [ ] ABI 陈旧产物核验（`rm -rf build/native/host` 后重建，`nm | grep Zhulong_stream_audio_info`）；
- [ ] `rtsp_startup_timing` 对照，劣化 ≤ 10%。

## 完成判据

- 父任务 AC-1 全绿；
- 音频路径没有进程配置入口（开关在 B 落地），故本子任务不可能在生产误开；
- 关闭音频（= 默认）时视频字段逐字节不变。
