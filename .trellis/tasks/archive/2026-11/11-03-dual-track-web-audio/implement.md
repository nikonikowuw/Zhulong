# 实施计划：前端音频（C · Frontend）

> 对应父任务 `implement.md` Phase 3 + Phase 4。前置：B 的音频帧头与 `audio_info` 契约已冻结。

## Phase C3：先出声（G.711）

- [ ] `stream-pool.ts`：只加 `typeof event.data === 'string'` 分支 → JSON 校验 → `subscriber.onStreamInfo`；`ArrayBuffer` 分支、统计、`wire-parser` 均不动；
- [ ] `audio-channel.ts`：连接/关闭/重连、20 B 头解析、`audio_info` / `error` 处理、丢帧计数；
- [ ] `g711-decoder.ts`：A-law / μ-law 静态表 → Float32；
- [ ] `presentation-scheduler.ts`：`clockSnapshot()`；`webcodecs-decoder.ts` 加 `onResync`（discontinuity / queue rebuild / 跳帧重同步）；`useWebCodecsRenderLoop` 暴露 `getPresentationClock()`；
- [ ] `live-audio-player.ts` + worklet：环深控制、`due` 投递、`flush`、`setMuted`、`setVolume`、`start(clock)`；
- [ ] `live-player.tsx`：接 `isMuted` / `hasAudio` / `volume` prop；解禁时建音频通道 + 创建/`resume()` AudioContext；静音/切格/卸载关闭通道并 `flush`；视频 `onResync` → 重锚；
- [ ] 单测：`g711-decoder.test.ts`、`live-audio-player.test.ts`、`audio-channel.test.ts`、`stream-pool.test.ts`。

## Phase C4：AAC、能力 UI 与焦点

- [ ] AAC：`AudioDecoder` + `description`、codec 串由 ASC 推导、`isConfigSupported()` 失败降级；
- [ ] `stream_info.audio.present` → cell 状态 → 喇叭置灰 + tooltip（区分"该路无音频"/"浏览器不支持"）；
- [ ] `useLiveGrid`：`activeCellId` / `volume`；`setCellMute(id,false)` 互斥（含同 stream key）；`muteAll()`；`assignCamera` / `clearCell` / `clearAllCells` / `setCellStreamType` 回收焦点；
- [ ] i18n：`unmute` / `mute` / `volume` / `noAudio` / `audioUnsupported`；
- [ ] 测试：`use-live-grid.test.ts`、`live-player-cell.test.tsx`（含"卸载后通道已关闭"）。

## 验证

```bash
cd web && pnpm test -- src/features/live/services/g711-decoder.test.ts
cd web && pnpm test -- src/features/live/services/live-audio-player.test.ts
cd web && pnpm test -- src/features/live/services/audio-channel.test.ts
cd web && pnpm test -- src/features/live/hooks/use-live-grid.test.ts
cd web && pnpm test -- src/features/live/components/live-player-cell.test.tsx
make frontend-check
```

## 完成判据

- 父任务 AC-3 / AC-4 的自动化部分全绿；
- 用 `audio-pcma` / `audio-pcmu` / `audio-aac` 桩能点喇叭出声；
- 不点喇叭时没有任何音频连接；9 宫格同 tab 最多一路发声；静音/解禁/切格无残留声音。
