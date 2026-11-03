# 实施记录：音频通道（B · Backend）

> 对应父任务 `implement.md` Phase 2。前置 A（ABI v5）已合入。B 落地后默认没有任何客户端收听，
> `dev` 树行为与改造前一致（只有一处可见变化：预览 WS 多了一条一次性文本能力帧）。

## 1. 已完成

### 2.1 CGO 桥接（`internal/engine/`）

- `TrackType`（video/audio）、`TrackMask`（`TrackMaskVideo/Audio`，**零值 = 仅视频**，保持 ABI v4 语义）、
  `Codec` 增加 `CodecAAC/CodecPCMA/CodecPCMU` + `IsAudio()`；
- `Packet.TrackType`、`inboundPacket.TrackType` 映射；
- `SubscriptionOptions.TrackMask`：零值走视频，`Video|Audio` 混订直接 `ErrInvalidArgument`
  （要两条就开两条订阅，混订等于把「音频挤掉视频」重新引回来）；
- `Subscription.mask` 在注册**之前**赋值 —— 注册返回后原生回调可能立刻开始读它，
  写在后面就是一个被 `-race` 当场抓到的数据竞争（已实测踩到并修掉）；
- `Engine.getAudioInfo` + `Stream.AudioInfo`：`C.malloc` 缓冲区 → `Zhulong_stream_audio_info` →
  `C.GoBytes` 复制 → `C.free`；`ERR_BUFFER_TOO_SMALL` 按回报长度重试一次；未知流 `ErrNotFound`，
  旧代次 `ErrStaleResource`，关流后 Go 句柄 `ErrClosed`（无悬垂）。

### 2.2 预览能力帧（`internal/camera/handler.go`）

- `StreamWS` 在 `RegisterClient` 与写协程启动**之前**同步写一条 `stream_info`：
  此刻连接只有一个写者，不与 GOP 补发交错，也不需要锁；
- 查询失败只降级为 `present=false` + `reason`，写失败只记日志 —— 能力帧丢失不能把「没声音」
  升级成「黑屏」；
- `StreamDispatcher` 只新增一个只读的 `AudioCapability()`，`WritePump` / `Broadcast` / GOP / 背压零改动。

### 2.3/2.4 音频通道（`internal/camera/audio_channel.go` + `audio_wire.go`）

- 注册表键 = `sha256(sessionToken)` 前 16 位十六进制，日志只出现哈希前缀；
- 同会话第二条接管（旧连接 **4001**）、跨会话独立、全局硬上限 8（**4002**，且**已有会话的接管不受上限影响**，
  否则刷新页面就永远接不回来）；
- ZLA1 20 字节头 + 裸负载；`seq` 音频独立计数；`pts_us` 直接透传原生归一化结果，Go 侧不重算；
- 写队列 48 帧；溢出只关本通道（**4003**），不阻塞原生回调、不触碰视频订阅；
- 关闭顺序固定 `连接 → 订阅（同步排空）→ 流引用 → 注册表`；注册表 `Remove` 只在登记实例仍是自己时生效，
  防止「旧通道延迟关闭删掉新表项」的顶替时序；
- 无音轨时先发 `{"type":"error","code":"NO_AUDIO"}` 再以 **4404** 关闭；订阅失败 **4500**；
- `Handler.Close()` 先 `CloseAll()` 音频通道，再关事件总线。

**音频用独立消费者 + 独立 `engine.Stream` 句柄**（`StreamHub.AcquireAudioStream`）：原生按 URL 去重所以
不会多开 RTSP 连接，但音频订阅的任何故障都碰不到视频订阅与健康态。为此把「查相机 → 选 role → 解密 URI →
定传输协议」抽成 `resolveStreamTarget`，视频与音频共用同一段解析。

## 2. 与设计稿的偏差

| 设计稿 | 实现 | 原因 |
| :--- | :--- | :--- |
| `AudioInfo` 用 Go `[]byte`（`&buf[0]`）+ `runtime.KeepAlive` | `C.malloc` + `C.GoBytes` | cgo 运行时禁止「内含 Go 指针的结构体指针交给 C」，会 panic：`argument of cgo function has Go pointer to unpinned Go pointer`。已实测踩到，spec 已更正 |
| 订阅失败/无音频的错误码未指定 | 无音频 4404、订阅失败 4500、背压 4003 | 关闭码是前端唯一能区分「置灰 / 不重试 / 重连」的信号 |
| `Stream` 只有单订阅槽 | 音频另开消费者与句柄 | 不复用视频分发器的 `MediaStream`：单句柄单订阅是既有不变量，破它要改 `Stream` 的全部并发假设 |
| 预览 WS 错误映射（原内联） | 抽出 `writeStreamAcquireError` | 音乐与视频共用；顺带把 `ErrStreamHubClosed` 映射为 503（原先落到 500） |

## 3. 验证

```bash
make go-check                                                    # 通过（vet / gofmt / -race / bridge 集成）
python3 native/scripts/build.py go test -race ./internal/camera/ # 通过
python3 native/scripts/build.py go test -race ./internal/engine/ # 通过
make api-docs                                                    # 无 diff
```

新增用例：

- `internal/engine/audio_info_test.go`：未知流 `ErrNotFound`、旧代次 `ErrStaleResource`；
- `internal/engine/integration_test.go`：`TestBridgeIntegration_AudioTrack`（真实 RTSP 桩下元数据、按轨订阅
  只收音频、音频包不带视频帧语义、关流后 `ErrClosed`）、`TestBridgeIntegration_AudioAbsent`；
- `internal/camera/audio_wire_test.go`：ZLA1 往返、标志位、视频帧被拒、能力帧字段投影；
- `internal/camera/audio_channel_test.go`：同会话接管、跨会话独立、硬上限（含「上限下仍可接管」）、
  并发注册/移除（`-race`）、泵帧、背压只关本通道且排空订阅；
- `internal/camera/audio_handler_test.go`：无会话 401、无音频 4404 + error 帧、端到端 ZLA1 帧、
  同会话接管 4001；
- `internal/camera/handler_test.go`：预览首帧必须是文本能力帧，且随后的二进制帧**逐字节黄金对比**
  （32 字节头写死期望值，不调用 `PackPacket` 反推自己）。

## 4. 未覆盖（留给 C / D）

- 前端解码、AudioWorklet、音画同步（C）；
- 16 路板端压测与真实相机音频（D）；
- 两个浏览器会话同时收听的端到端浏览器用例（C 阶段的真实无头浏览器测试）；
- 音频通道与预览 WS 共用同一路相机时「视频断流但音频仍在」的端到端联调（D 的降级回归）。
