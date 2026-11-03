# 设计摘要：双轨接入（A · Native）

> 完整设计见父任务 `design.md` §2（不变量 I1/I2/I3/I4、双 sink、AudioNormalizer、ABI v5）。本文件只记录子任务边界与实施时必须钉住的点。

## 契约（冻结项）

1. **两条 sink，不是一条回调加分支**：`read_rtsp(..., consume, consume_audio, ready, stream_id)`。音频包只能走 `consume_audio`；`Stream::dispatch()` 入口断言 `track_type == VIDEO`。
2. **时间轴**：`audio_out_us = (audio_abs_us - A) + base + video_shift_us`，`base = a - V`。`A/a` 来自「视频原点建立后的首枚有效音频包」；`V` 是视频原点建立前的 `Clock::now()` 快照。`AudioNormalizer` 不读钟、不加锁（同线程）。
3. **ABI**：只在尾部追加；`track_type` 默认 0 = VIDEO，旧调用方零初始化即兼容。`Zhulong_stream_audio_info` 照 `Zhulong_stream_get_status` 的锁序实现，元数据与 extradata 在锁内复制到调用方缓冲区。
4. **音频一律申请，无选项字段**：`Options` 不再新增 `enable_audio`；物理流能力靠探测，失败自动降级。`Zhulong_stream_subscribe_track` 的 `track_mask` 决定每个订阅收哪些轨道。

## 本子任务新增的决策

- **失败降级只重试一次**：`audio-broken` 桩用于实测 FFmpeg 行为。若 SETUP 失败直接导致 `avformat_open_input` 失败，则关闭 ctx、以 `allowed_media_types="video"` 重开一次；若 FFmpeg 本身能降级完成 open，则不加重开逻辑（避免无意义双握手）。
- **降级原因缓存**：物理流生命周期内记住“该相机音频不可用 + 原因”，重连日志直接引用，避免每 30s 刷一条同样的 WARN。
- **音频单包 8 KiB / extradata 256 B** 上限；超限丢包计数，不扩大订阅拷贝。
- **合成 PTS 步长**：优先 `pkt->duration`，否则 PCMA/PCMU 按 `pkt->size`，AAC 按 `codecpar->frame_size`（缺省 1024）。

## 已知风险

| 风险 | 处理 |
| :--- | :--- |
| 相机在 PLAY 后积压数秒音频再吐出，`base` 含积压量 | 不引入额外复杂度；前端陈旧丢弃 + 环深微调兜底，日志可观测 |
| G.711 的 `time_base` 不是 1/8000 | 一律走音频流自身 `time_base` 换算，不假设 |
| `audio-broken` 桩的失败形态与真实相机不同 | 至少覆盖“SETUP 被拒”这一最坏路径；真实机型问题在 D 阶段用现场日志补 |

## 验证命令

```bash
make native-build && make native-test
python3 native/scripts/build.py --sanitizer address test
python3 native/scripts/build.py --sanitizer thread  test
```
