# WebSocket 媒体流分发与按需订阅 (PRD)

## 1. 目标与定位 (Goal)

承接媒体接入管道的核心分发能力，在服务端零转码、低资源开销的前提下，建立 Go 业务层与前端 Web 播放器之间的低延迟 WebSocket 视频传输通道：
1. **按需流转生命周期**：仅在有活跃 Web 客户端连入时拉起底层 CGO 码流借用（`Acquire` + `Subscribe`）；客户端全断开后及时释放底层流，与 Native 8 秒 Grace Period 无缝协同。
2. **零转码紧凑传输协议 (Wire Protocol)**：采用方案 A（固定 24 字节二进制帧头 + H.264/H.265 Annex B 裸流），向前端透明传递 PTS/DTS、关键帧标记与时钟基，配合 WebCodecs 实现 <200ms 超低延迟。
3. **GOP 缓存与首帧秒开**：为每个活跃流维护最近关键帧（含 SPS/PPS/VPS 参数集）缓存，新客户端接入时立即下发关键帧，消除等待下一个 GOP 的起播黑屏。
4. **背压与慢消费者防护**：为每个 WebSocket 会话维持有界缓冲队列（默认 64 包），网络拥塞时优先丢弃非参考帧或主动断开慢客户端，杜绝服务端内存堆积。

---

## 2. 需求规范 (Requirements)

### 2.1 路由、端点与鉴权
- **R1.1 端点地址**：提供 `GET /api/v1/cameras/:id/streams/:role/ws`，支持主流（`role=main`）与子流（`role=sub`）；同时提供 `GET /api/v1/cameras/:id/ws` 默认接入主流。
- **R1.2 会话鉴权**：沿用系统 Cookie Session（`zhulong_session`），未认证或会话已失效直接拒绝握手（HTTP 401）。
- **R1.3 存在性与状态校验**：
  - 摄像机不存在时返回 404；
  - 摄像机处于禁用状态（`enabled=false`）时返回 403 明确错误；
  - 请求的流（如未配置子流）不存在时返回 404。

### 2.2 二进制数据包协议 (Binary Wire Protocol)
每个推送给 WebSocket 客户端的 Binary Message 由两部分组成：
`[24 Bytes Fixed Header] + [N Bytes Annex B Payload]`

- **固定头格式 (Big-Endian)**：
  - `Bytes 0..3`：Magic Token `0x5A 0x4C 0x4D 0x31`（ASCII "ZLM1" - Zhulong Media v1）
  - `Byte 4`：Codec 标识（`0x01` = H.264, `0x02` = H.265）
  - `Byte 5`：Frame Flags（位域）：
    - `Bit 0` (0x01)：`KeyFrame`（1 = 关键帧/IDR，0 = 非关键帧）
    - `Bit 1` (0x02)：`HasPTS`
    - `Bit 2` (0x04)：`HasDTS`
  - `Bytes 6..7`：保留字段（0x0000），供未来帧序列号或扩展使用
  - `Bytes 8..15`：PTS（`int64`）
  - `Bytes 16..23`：DTS（`int64`）
- **Payload**：
  - 原始 Annex B 编码帧切片（以 `00 00 00 01` 或 `00 00 01` 起始码分隔）。关键帧必须包含完整的 SPS/PPS（以及 H.265 的 VPS）。

### 2.3 按需流转与连接池管理 (On-Demand Dispatcher)
- **R3.1 按需激活**：
  - 某个流的 Web 订阅者从 $0 \rightarrow 1$ 时，异步从 CameraStore 读取并解密该流的 RTSP 凭据，向 Native Engine 申请 `Acquire(ConsumerPreview)` 并建立 `Subscribe`。
  - 状态同步：流启动成功后，更新摄像机该流的 `Session` 状态为 `running`，并通过 SSE 广播。
- **R3.2 扇出分发**：
  - 同一摄像机同一路流的多个 Web 订阅者共享同一个底层 `engine.Subscription`，底层数据帧单次深拷贝后并发扇出给所有已连接客户端。
- **R3.3 优雅退出与防抖休眠**：
  - 订阅者数量从 $1 \rightarrow 0$ 时，停止后台 Pump 协程并关闭 `Subscription` 与 `Stream`，底层物理流进入 8 秒 Grace Period；
  - 状态同步：重置流会话状态为 `idle`。

### 2.4 GOP 缓存与秒开 (Instant Playback)
- **R4.1 关键帧缓存**：
  - 广播中心为每个流缓存最近接收到的有效关键帧数据包（必须携带 SPS/PPS 参数集）。
  - 若码流提供独立的 extradata，广播中心需保证首包能正确构造或拼接参数集。
- **R4.2 新客户端起播**：
  - 新 WebSocket 客户端握手成功加入流时，如果缓存中存在最新关键帧，立即将该帧推入新客户端队列；
  - 客户端无需等待下一个 1~3 秒的长 GOP 周期，瞬间启动解码。

### 2.5 背压与流控 (Backpressure & Slow Consumer Eviction)
- **R5.1 有界输出队列**：每个 WebSocket 客户端维护固定容量的发送缓冲 channel（默认 64 包）。
- **R5.2 拥塞降级与断开**：
  - 广播写入若遇 channel 阻塞，如果当前帧是非关键帧（P/B 帧），允许丢弃以维持低延迟；
  - 如果缓冲区持续满载超过阈值或丢包过多，主动关闭该客户端 WebSocket 连接（Status 1008 Policy Violation），杜绝服务端内存无界泄漏。

---

## 3. 验收标准 (Acceptance Criteria)

- [x] **AC-1 (端点与鉴权)**：未携带合法 Session Cookie 访问 WS 端点被拒绝 (HTTP 401)；合法用户能正常升级协议并建立 WebSocket 连接。
- [x] **AC-2 (按需拉流与释放)**：无 Web 客户端时，底层引擎无活动 Preview 订阅；首个客户端连入触发底层拉流；最后一个客户端断开后底层流按需关闭并进入 Grace Period。
- [x] **AC-3 (二进制协议正确性)**：验证客户端收到的二进制消息包含 24 字节固定头部，Magic 为 "ZLM1"，Codec、KeyFrame、PTS、DTS 字段与底层码流严格一致。
- [x] **AC-4 (GOP 秒开)**：流已有数据时，新连入的客户端接收到的第一帧必定为带有参数集的关键帧，无需等待下一个自然 GOP。
- [x] **AC-5 (单源多端扇出)**：同一摄像机同流支持多个并发 WebSocket 客户端同时接收视频帧，底层物理流保持单例复用，不产生额外拉流连接。
- [x] **AC-6 (背压与慢客户端淘汰)**：模拟极慢客户端（不读 socket），验证服务端发送队列不无界堆积，慢客户端被安全主动淘汰，其他正常客户端不受影响。
- [x] **AC-7 (停机与生命周期安全)**：HTTP 优雅停机时，所有活动 WebSocket 连接正常断开，后台 Pump 协程安全退出，底层资源逆序排空释放，`-race` 测试零竞争。
