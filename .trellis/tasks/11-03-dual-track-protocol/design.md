# 设计摘要：音频通道（B · Backend）

> 完整设计见父任务 `design.md` §3。本文件只列实现时必须钉住的边界。

## 不变量

- **I1**：预览通路（`StreamDispatcher` / `packFrame` / `ZLM2` / GOP / `needsKey` / 活性 / 订阅预算）**零改动**。允许的唯一新增是 `handler` 在建连时同步写一条文本帧。
- **I6**：单通道约束的键是**登录会话**（`auth.GetSessionToken`），不是全局、不是用户 ID。
- 日志只记 token 的哈希前缀，**绝不落原文**。

## 能力帧时序

```
upgrade 成功
  → 查询 AudioInfo（失败/未就绪 → present=false + reason，仍然发帧）
  → conn.WriteMessage(TextMessage, streamInfoJSON)   // 此刻只有一个写者
  → hub.RegisterClient(...) / 启动写协程              // 之后二进制帧才可能出队
```

- 写失败只记日志并继续（能力帧是增强信息，不能因为它断流）；
- JSON 字段：`type / v / cameraId / role / audio{present,codec,sampleRate,channels,config,reason?}`；`config` 为 extradata 的 base64。

## 注册表

```go
type AudioChannelRegistry struct {
    mu        sync.Mutex
    bySession map[string]*audioChannel
    total     int
}
```

| 事件 | 行为 |
| :--- | :--- |
| 新连接，会话已有通道 | 关闭旧连接（4001 `superseded`），再建新的 |
| 新连接，`total >= maxAudioChannels` | 拒绝（4002 `too_many_channels`） |
| 无音频 / `AudioInfo` 失败 | 发 `error` 帧后关闭（4404 `no_audio`） |
| 客户端关闭 / 写超时 / 队列溢出 | `unsubscribe`（同步排空）→ 从注册表删除 → `total--` |
| 服务关闭 | 全部关闭，等写协程退出 |

- 注册表删除顺序**必须**在 `unsubscribe` 之后：否则会出现"注册表说没有、回调还在跑"。
- 通道删除使用 `defer` + 幂等标记，避免重复关闭导致 `total` 变负。

## 音频帧

20 字节头 + 负载，字段见父任务 `design.md` §3.4。Go 侧只做"复制 + 填头 + 入队"，**不做时间戳重算**——`pts_us` 来自 A 的 `AudioNormalizer`。

- 队列 48 帧（≈0.96 s），满则关闭通道并计数；绝不阻塞 native 回调。
- `seq` 单调递增，仅用于前端丢包统计。

## 风险

| 风险 | 处理 |
| :--- | :--- |
| 文本帧与二进制帧乱序 | 写在启动写协程之前，单写者天然有序 |
| 会话 token 泄漏到日志 | 只记 `sha256(token)[:16]` |
| 旧连接关闭与新连接建立的竞态 | 注册表内先 close 旧、再登记新；旧连接的清理路径按"不是当前指针则不动 total"处理 |
| 慢客户端拖垮服务 | 独立队列 + 写超时 + 关通道，不触碰视频 |
