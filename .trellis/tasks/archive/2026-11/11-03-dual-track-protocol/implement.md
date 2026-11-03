# 实施计划：音频通道（B · Backend）

> 对应父任务 `implement.md` Phase 2。前置：子任务 A 已合入（ABI v5 可用）。

- [x] **2.1 CGO 桥接**：`TrackType` / `inboundPacket` 映射；`AudioInfo` + `Engine.AudioInfo`（C 缓冲区 → Go 复制 → 释放）；`SubscriptionOptions.TrackMask` + `SubscribeTrack`；
- [x] **2.2 预览能力帧**：`handler.StreamWS` 在 `RegisterClient`/写协程之前写一条 `stream_info`；查询失败降级为 `present=false` + `reason`；`WritePump` 与 `StreamDispatcher` 不动；
- [x] **2.3 注册表**：`audio_channel.go`，键 = session hash；同会话接管（4001）、全局硬上限（4002）、无音频（4404）；
- [x] **2.4 音频 WS**：新路由、`audio_info` 文本帧、20 B 头（`ZLA1`）、独立有界队列、写超时、关闭原因；
- [x] **2.5 生命周期**：`unsubscribe` 同步排空后移除；`defer` 幂等；服务关闭时等待写协程退出；
- [x] **2.6 测试**：注册表（接管/独立/上限/race）、帧头往返、预览 golden、`AudioInfo` 关闭流后 NOT_FOUND、按轨订阅互不串包。

## 验证

```bash
make native-build
python3 native/scripts/build.py go test -race ./internal/camera -run 'TestAudioChannel|TestWire|TestStreamDispatcher|TestStreamWS'
python3 native/scripts/build.py go test -race ./internal/engine -run 'TestAudioInfo|TestSubscribeTrack'
make go-check
```

## 完成判据

- 父任务 AC-2 全绿；
- 不收听音频时，`make run` 的预览与今日逐字节一致；
- 任何音频异常都不改变视频订阅、GOP、健康态。
