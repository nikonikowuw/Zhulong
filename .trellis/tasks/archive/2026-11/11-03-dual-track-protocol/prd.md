# PRD: 音频通道（B · Backend）

> 父任务：`.trellis/tasks/11-03-dual-track-preview/`（权威来源）。依赖子任务 A（ABI v5：`track_type` / `Zhulong_stream_subscribe_track` / `Zhulong_stream_audio_info`）。

## Goal

把音频从 CGO 送到浏览器，同时**一行不改**预览数据通路：新增按需的音频 WebSocket、每客户端（登录会话）最多一条通道、能力走一次性文本帧。

## Scope（父任务 Phase 2）

1. CGO 桥接：`Packet.TrackType`、`AudioInfo{HasAudio,Codec,SampleRate,Channels,Config}`、`Engine.AudioInfo`（调用期间复制，不保留 C 指针）、`SubscriptionOptions.TrackMask`；
2. 预览 WS 能力帧：建连后、启动写协程**之前**同步写一条 `stream_info` 文本帧；`StreamDispatcher` / `WritePump` / ZLM2 二进制帧不动；
3. 音频通道注册表：以登录会话为键，同会话第二条接管旧的，跨会话独立，全局硬上限；
4. 音频 WS：`/api/cameras/:id/streams/:role/audio/ws`，`audio_info` 文本帧 + 20 字节二进制头（magic `ZLA1`）；
5. 生命周期：`unsubscribe` 同步排空之后才从注册表移除；队列溢出只关音频通道；
6. Go 单测（含 `-race`）与预览字节 golden 对比。

## Out of Scope

- 前端任何改动（子任务 C）；
- 预览协议、分发器、GOP、背压、健康态、订阅预算：**明确不允许改动**（I1）；
- 配置项（本期不存在）；
- 板端压测（子任务 D）。

## Acceptance Criteria（父任务 AC-2 子集）

- [ ] 预览 WS 的二进制帧与改造前逐字节一致（golden 对比），文本能力帧在二进制帧之前只发一次；
- [ ] 同会话开第二条音频通道：旧连接以 4001 关闭，新连接正常出声；跨会话互不影响；
- [ ] 全局通道数超上限时明确拒绝（4002），不泄漏 native 订阅；
- [ ] 关闭音频通道后 native 订阅同步排空、注册表清空、无遗留回调（`-race` 下断言）；
- [ ] 音频队列溢出只关闭音频通道，视频订阅与健康态不受影响；
- [ ] 音频帧头（20 B）封包/解包往返测试通过；`pts_us` 与视频同一条时间轴（用 A 的归一化结果，不重算）；
- [ ] 无音频 / 查询失败时返回 4404 + `error` 帧，前端可据此置灰；
- [ ] `Engine.AudioInfo` 在流关闭后返回 NOT_FOUND，不悬垂。

## Dependencies / 交付顺序

A → **B** → C。B 落地后默认没有任何客户端收听，`dev` 树行为与今日一致。
