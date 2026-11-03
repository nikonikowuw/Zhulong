# PRD: 前端音频（C · Frontend）

> 父任务：`.trellis/tasks/11-03-dual-track-preview/`（权威来源）。依赖子任务 B（音频 WS 帧格式与能力帧冻结后即可用 fixture 回放并行开工）。

## Goal

浏览器端按需建立音频通道，并与视频共用一条呈现时钟：默认全静音、遵守 Autoplay Policy、每 tab 最多一路出声；任一环节不支持都退化为无声视频。

## Scope（父任务 Phase 3 + Phase 4）

1. 预览 WS 只加文本帧分支（`stream_info` → `onStreamInfo`），`ArrayBuffer` 分支与 `wire-parser` **不动**；
2. 新增 `services/audio-channel.ts`：按需连接/重连/关闭音频 WS、解析 20 字节头、处理 `audio_info` 与 `error` 帧；
3. G.711a/μ 256 项静态查表 → Float32；
4. `LiveAudioPlayer`：AudioWorklet 环形缓冲 + 线性重采样 + `GainNode`；按 `due` 投递、陈旧丢弃、超前重锚、环深抖动控制、`flush()`；
5. AV sync：`PresentationScheduler.clockSnapshot()` + `onResync`；音频 PTS 与视频共用同一映射；
6. AAC：WebCodecs `AudioDecoder` + `description = audioInfo.config`；codec 串由 ASC 推导；`isConfigSupported()` 失败降级；
7. 音频焦点：`activeCellId` / `volume` 进 `useLiveGrid`；互斥、同流去重、换机/清空/切码流回收；`LivePlayer` 由 prop 驱动；
8. i18n 文案、喇叭置灰与 tooltip。

## Out of Scope

- 麦克风采集与反向对讲；
- 音量持久化（父任务未决项）；
- 预览协议、`wire-parser`、`StreamDispatcher`（B 已明确不动）；
- 原生与 Go 改动（A/B）。

## Acceptance Criteria（父任务 AC-3 / AC-4 子集）

- [ ] G.711 查表：A-law / μ-law 的静音码、0、满幅输出符合期望；
- [ ] 时钟单测：`due` 计算、陈旧丢弃、前后大跳重锚；解禁瞬间不播积压音频（起播 ≤ 200 ms）；
- [ ] AAC 有 ASC 能解帧；ASC 缺失或 `isConfigSupported()=false` → 无音频 + 文案，视频继续；
- [ ] 不点喇叭时**不建立任何音频连接**；静音/切格/卸载时连接被关闭；
- [ ] 默认全静音，`AudioContext` 只在首次手势创建/恢复；
- [ ] 单 tab 最多一路出声；同一路流多个格子仍只一路；换机/清空/切码流后焦点回收；
- [ ] 无音频相机按钮置灰并说明"该路无音频"；
- [ ] 文本能力帧不影响 fps/码率统计（统计只算视频）。

## Dependencies

B（音频帧与能力帧契约）→ **C** → D。
