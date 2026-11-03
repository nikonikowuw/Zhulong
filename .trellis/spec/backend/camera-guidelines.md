# 摄像机业务与生命周期规范

> 摄像机主子流建模、凭据加密、双流原子门禁、正交状态机与探活调度规约。

---

## 1. 核心设计原则

1. **凭据安全与密文隔离**：
   - 摄像机流凭据（RTSP URL）在 SQLite 中采用 AES-256-GCM 密文存储，使用关联数据 AAD（`v1:<camera_id>:<role>`）防止密文重放或跨流窜改。
   - 密钥文件必须设为 `0600` 严格权限。若数据库中存在密文而密钥丢失，系统**必须阻断启动并退出**，严禁自动生成替代密钥静默覆盖旧数据。
   - **跨域密钥持有者必须注册**：`camera.key` 不是 camera 域私有的。任何新域把凭据密文写入本库（当前为 GB28181 设备 Digest 密码，`gb_devices.encrypted_password`，AAD = `v1:gb:<device_id>`），必须在 `internal/app` 装配期调用 `camera.LifecycleManager.AddEncryptedDataProbe`。
     漏注册的后果是**启动时的密钥丢失检查只看摄像机表**、在仅有新域密文的库上静默生成新密钥，旧密文从此无法解密且没有任何报错。`internal/camera/lifecycle_test.go` 与 `internal/gb28181/store_test.go` 各自守住一侧：前者断言 probe 能阻止重新生成，后者断言 GB 密文能被探测到。
   - 日志与公开异常信息中必须强制脱敏凭据；已认证管理员会话的接口允许返回完整明文 RTSP URL 供前端展示与取流播放。
2. **原子双流门禁（Dual-Stream Atomic Gate）**：
   - 主流必填，子流可选。创建或更新流配置时，在 3～5 秒绝对超时预算内执行原子探测门禁。
   - 填写子流时主流与子流并发探测，任一路失败则整笔事务回滚不予入库，严禁部分生效。
   - 编码（H.264/H.265）与分辨率必须有效；FPS 缺失时保存为未知，禁止猜测或伪造。
3. **正交状态模型与证据分离**：
   - 严格解耦为三个正交维度：`enabled`（启用开关）、`health`（健康状态：unknown/online/offline/error）、`session`（会话维度：idle/starting/running/reconnecting/error）。
   - 无消费者不代表离线；活跃拉流优先复用首包/收包证据，空闲流采用定时轻量 DESCRIBE 探活。
   - 摄像机整体健康以主流为主，子流异常单独标记为 `degraded: true`；探活过期（>240s）标记为 `stale: true`。

---

## 2. 数据库与领域模型

```sql
-- cameras: 核心实体与乐观锁
CREATE TABLE cameras (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT 1,
    revision INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

-- camera_streams: 主/子流配置与密文
CREATE TABLE camera_streams (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    camera_id TEXT NOT NULL REFERENCES cameras(id) ON DELETE CASCADE,
    role TEXT NOT NULL, -- 'main' | 'sub'
    protocol TEXT NOT NULL DEFAULT 'rtsp',
    encrypted_uri BLOB NOT NULL,
    transport TEXT NOT NULL DEFAULT 'tcp',
    codec TEXT NOT NULL,
    width INTEGER NOT NULL,
    height INTEGER NOT NULL,
    fps_numerator INTEGER NOT NULL DEFAULT 0,
    fps_denominator INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    CONSTRAINT uq_camera_stream UNIQUE (camera_id, role)
);
```

- **Revision CAS 乐观并发控制**：更新摄像机（名称、开关、流配置）必须提供 `revision`。Store 更新时执行原子 CAS（`WHERE id = ? AND revision = ?` 并递增 `revision = revision + 1`），冲突时返回 `CAMERA_REVISION_CONFLICT`（HTTP 409）。
- **更新流保留与清空契约**：`PUT /api/v1/cameras/:id` 接收更新请求时，若 `subStream` 为 `nil` / 未传递，后端将对称保留原有的子流不变；若需移除/清空既有子流，客户端必须显式传递 `subStream: { role: "sub", rtspUrl: "" }`（即空 URL），后端识别后跳过探测并将子流移出入库列表，实现平滑解绑。

---

## 3. 探活与调度架构

| 流状态 | 探活方式 | 频次 / 阈值 | 失败判定行为 |
| --- | --- | --- | --- |
| **活跃预览流 (Session=Running 且有分发器)** | 媒体包到达监听（Go 侧逐包可见） | 每 250ms 轮询检测 | 连续 4s 无视频包判超时，触发 `reconnecting` 与 `error` 状态 |
| **活跃 AI 分析流 (Session=Running，无分发器)** | Native 流状态（read-idle 失败 → `FAILED`） | Native 侧 5s 读空闲 | 订阅报错后进入 `reconnecting`，由 `StreamHub` 的指数退避重连接管 |
| **空闲流 (Session=Idle)** | 轻量 RTSP DESCRIBE | 60s 周期，±20% 随机抖动 | 连续 1～2 次失败标记 `error`（瞬态）；连续 3 次失败标记 `offline` |
1. **轻量 DESCRIBE 铁律**：空闲探活仅发送 DESCRIBE 校验状态码 200 与视频 SDP，**绝对禁止发送 SETUP、PLAY 或拉取 RTP 数据**，避免无谓消耗网络与摄像机编码器资源。
2. **Digest 认证适配**：集成 `icholy/digest` 低层 API，支持 MD5/SHA-256、`qop=auth` 与参数规范化，单连接完成挑战与重试，限制单次探测总预算 ≤5s。
3. **有界调度与并发控制**：后台调度器使用带权信号量（默认并发上限 4～8），对同一摄像机的同一流去重避免并发重复探测。
4. **收包证据节流与活跃判定**：活跃流推流期间，分发器在遇到关键帧或时间间隔 ≥1s 时节流上报 `EvidencePacketActivity` 刷新 `LastCheckedAt`，保证 250ms 轮询检测准确反映物理收包真实性，避免误判超时。
5. ⚠️ **4 秒包超时只能用于 Go 侧逐包可见的流**（`PacketActivitySource`）：AI 分析流在 Go 侧只暴露**推理结果**，而结果会被运动门控吞掉（画面没动静就没有结果）。把「结果到达」当成收包证据等于给完全健康的流定时判死刑——实测表现是每 4 秒一次假超时、`session` 永久钉在 `reconnecting`，而同一时刻 broker 仍在按 `sample_fps` 稳定投递帧。AI 流的活性以 Native 流状态为准：read-idle 失败会让流进入 `FAILED`，订阅随即报错，那是可复现的证据。
6. **失败必须能自愈**：Native 契约里没有隐式重连（`native/README.md`），所以业务层必须接手。失败后的重连节奏受 Native 8 秒宽限期约束：宽限期内重新 acquire 会原样交回**同一具**失败流，且每次 acquire 都会把它的 `expiry` 推回 max（等于续命）。因此宽限期内禁止任何 acquire，`ResolveAIStreamBrokerPath` 必须先等 `recoveryGrace` 过去；连算法包重启路径也不例外，否则会拿到死 socket → runner 退出 5 → CRASH_LOOP，视频流恢复了算法却停了。
6. **调度器快速优雅停机**：调度器内部维护随 `Stop()` 级联取消的根 Context；轻量 DESCRIBE 客户端通过监听上下文取消触发 `conn.SetDeadline(time.Now())` 立即打断网络阻塞，实现停机时延 <100ms。

---

## 4. SSE 事件流与背压保护

1. **路由防冲突**：SSE 路由挂载在 `/api/v1/cameras/events`，在路由树中必须先于 `/:id` 参数路由注册，避免路由碰撞。
2. **原子初始快照**：客户端订阅成功后，首条消息推送 `event: snapshot`，携带当前所有摄像机完整状态快照与单调自增 `sequence`，消除快照与增量事件间的时序间隙。
3. **有界队列与慢消费者驱逐**：单个 SSE 连接维护有界缓冲通道（默认 32），广播塞满时主动断开该客户端（Eviction），防止慢客户端引起服务端内存泄漏。
4. **会话时效校验**：SSE 维持长连接期间，每 30 秒主动校验 Cookie 中的会话 Token，会话撤销或过期时立即断开连接。

---

## 5. WebSocket 实时推流与 ZLM2 协议规约

1. **二进制封包协议 (Wire Protocol: ZLM2)**：
   每个推送到 WebSocket 客户端的二进制消息包含 32 字节大端序固定头：

   | 偏移 | 长度 | 字段 | 说明 |
   | --- | --- | --- | --- |
   | 0 | 4 | Magic | `0x5A4C4D32` ("ZLM2") |
   | 4 | 1 | Version | `2` |
   | 5 | 1 | Codec | `0x01` = H.264, `0x02` = H.265 |
   | 6 | 1 | FrameClass | `Zhulong_FRAME_*` 取值 |
   | 7 | 1 | Flags | Bit0 KeyFrame / Bit1 Discontinuity / Bit2 ParamInjected / Bit3 PtsSynthetic / Bit4 DtsSynthetic |
   | 8 | 8 | `pts_us` | **显示时间戳微秒值**，保留 B 帧重排，不保证按包序单调 |
   | 16 | 8 | `dts_us` | **解码时间戳微秒值**，按解码包序单调；缺失时由原生层合成 |
   | 24 | 4 | `seq` | 流级帧序号，接收方据此统计真实丢帧 |
   | 28 | 4 | `gop_id` | 关键帧序号，变化即代表可重建解码器 |
   | 32 | N | Payload | Annex B NALU |

   - 载荷时基归一化责任固定在 Native `PreviewNormalizer`；前端不做时基换算。PTS 表示显示时间，可能因 B 帧重排回退；
     WebCodecs 按收到顺序解码，并将原始 `pts_us` 作为 chunk timestamp。

2. **按需生命周期与单例拉流**：
   - 路由：`GET /api/v1/cameras/:id/streams/:role/ws`（及 `/api/v1/cameras/:id/ws` 默认主流），严格受 `auth.RequireAuth` 保护。
   - $0 \rightarrow 1$ 激活：首个 Web 客户端连入时，解密 RTSP 凭据并向 Native 引擎发起 `Acquire(ConsumerPreview)` 与 `Subscribe`，状态切为 `SessionStateRunning`，并立即向 `/api/v1/cameras/events` 广播状态变更。
   - 并发 Singleflight 保护：StreamHub 采用 `singleflight.Group` 协同合并对同一路流的并发获取请求，彻底避免击穿造成对摄像机重复发起物理 RTSP 握手与 C++ 句柄开销。
   - $1 \rightarrow N$ 扇出：同一路流多个 Web 观众共享单例物理拉流，单次封包并发扇出。
   - $1 \rightarrow 0$ 空闲热保留：所有客户端断开后立即把状态切为 `SessionStateIdle` 并向 SSE 广播，
     但**分发器不立即销毁**，而是在 `idleCloseDelay`（默认 10min）内保住底层订阅与 GOP 缓存。
     窗口内回来的客户端复用同一分发器与热 GOP，首帧只需一个目标延迟，而不是一次 RTSP 建连 + 等 IDR；
     窗口到期才真正 Close，进入 Native 8s Grace Period 并释放物理流。
   - ⚠️ **不要把「无观看者」和「不占资源」当成同一件事**：状态是语义信号（有没有人在看），
     资源回收是延迟的。两者的解耦正是秒开的前提；立刻 Close 会让每次切页/刷新都付一次冷启动代价。
   - ⚠️ **热保留必须有路数上限**（`defaultMaxWarmStreamDispatchers`，默认 4）：每条热流都还在拉 RTSP、跑包泵、
     占着 4 MiB GOP 缓存。超上限时按「最早进入空闲」的顺序立即释放，绝不无限累积；
     否则「看过 16 个机位」等于「16 条流常驻 10 分钟」，在 RK3568 上是实打实的带宽与内存。
   - ⚠️ **`running` / `idle` 的写入必须串行**：分发器持有 `stateMu` 包住「写状态 + SSE 广播」，
     并在锁内重新确认订阅者数。只在解锁后重新读一次 `len(subscribers)` 不够——
     读与写之间仍可插进另一个方向的转换，最终状态会写反（有人观看却长期 idle，包超时监测随之失效）。

3. **GOP 完整补发 (Instant Playback)**：
   - 分发器缓存**从最近 IDR 到当前时刻的完整帧窗口**（`gopCache`，字节上限 `gopCacheMaxBytes = 4 MiB`），新客户端在 `RegisterClient` 内**锁内**逐帧补齐。
   - ⚠️ **不得退化为「只缓存关键帧单帧」**。只补 IDR 然后接实时帧，中间缺掉的 P 帧会让参考链断裂 ——
     外部表现就是「刚开始播放卡几帧、花一下才正常」。这是已修复的真实缺陷，不要重新引入。
   - 补发必须在 `d.mu` 内完成：与 `Broadcast` 互斥保证「历史帧 → 实时帧」顺序不乱；且此时客户端尚未进入
     `subscribers`，不会与 `Broadcast` 重复投递同一个 GOP 起点帧。
   - 补发成功后 `client.lastProgressDTSUS` 必须落在 GOP 末帧；若停留在 GOP 起点，第一帧实时帧就会被时间预算
     判定为「落后」并丢弃刚补发的 IDR，秒开直接失效。
   - 队列装不下完整 GOP 时**整段放弃**并置 `needsKey`，等下一个 IDR —— 半截 GOP 等于断链，比不补更糟。
   - GOP 超过字节上限时 `gop.complete = false` 并告警，退化为「等下一个 IDR」。此时应在相机侧调小 GOP，
     而不是放宽缓存上限：相机 GOP 配到十几秒是相机配置问题，边缘设备不能为此无限吃内存。

4. **背压以 DTS 写出进度预算为准，不按包数**：
   - 判据是 `最新输入帧.dtsUS - 最近一次成功 WebSocket 写入帧.dtsUS > maxClientLagUS(500ms)`，不是队列长度；
     基准仅在 WebSocket 二进制帧写成功后推进，首次入队时用最早待发帧初始化。
   - PTS 是显示时间戳，B 帧可能按解码包序回退，**不可用于背压差值**。
   - 超预算时整段丢弃该客户端积压并置 `needsKey`，**只接受下一个 IDR**。
   - ⚠️ **严禁逐帧挑着丢非关键帧**。H.264 的 P 帧绝大多数是参考帧，丢一帧后面全花直到下一个 IDR。
   - ⚠️ **严禁因慢而踢掉客户端**（旧实现的 `ClosePolicyViolation` 已移除）。弱网下踢连接等于制造重连风暴，
     边缘 A55 的协程池撑不住。改为降级为「只跟关键帧」。
   - GOP 缓存和客户端队列共享 128 帧硬上限及 4 MiB 字节上限；超过任一限制就放弃完整 GOP 并告警，
     避免缓存保留了客户端队列无法补发的半截 GOP。

5. **观测计数**：`StreamDispatcher.Counters()` 暴露 `KeyframeInjected / LagResyncs / QueueResyncs / ResyncSkips /
   GOPTruncations / Subscribers`。现场排障优先看 `GOPTruncations`（相机 GOP 过长）与 `LagResyncs`（客户端跟不上）。

6. **起播耗时必须可拆分测量**：起播 1~2 秒是典型投诉，它可能来自四个完全不同的阶段，
   不拆开就只能猜。固定两端各一条日志：
   - Native `rtsp_startup_timing open_ms=… info_ms=… first_packet_ms=… analyzeduration_us=…`；
   - Go `preview first frame written since_subscribe_ms=… skipped_before_first_frame=… first_frame_is_idr=…`。

   两者相减即可定位：`open_ms` 大 → 网络/凭证；`info_ms` 大 → `analyzeduration` 探测窗口过长
   （RTSP 的 SDP 已带 codec/分辨率/参数集，1s 探测纯属浪费）；`skipped_before_first_frame` 大 →
   在等相机下一个 IDR（相机 GOP 配置问题，改代码没用）。

   ⚠️ **缩短探测窗口（`probesize` 128 KiB / `analyzeduration` 300ms）必须有兼容档兜底**：
   SDP 不带 `sprop-parameter-sets`、首包不是 IDR、低码率 H.265 等情况下，快速档可能读不到完整参数集，
   `avformat_find_stream_info` 返回负值。此时**不得直接判流不可用**，而是关闭并重连一次、
   用旧上限（1 MiB / 1s）重试，日志记 `rtsp_probe_fallback`。把「探测不足」当成「相机坏了」是现场不可接受的误判。

---

## 5.1 按需音频通道与 ZLA1 协议

音频是**独立通道**，不是预览流的一部分：预览 WS 的二进制帧逐字节不变，协议版本仍是 ZLM2 v2。

1. **能力帧（预览 WS，一次性文本帧）**：建连后、注册客户端与启动写协程**之前**同步写一条
   `{"type":"stream_info","v":2,"cameraId":…,"role":…,"audio":{present,codec,sampleRate,channels,config,reason?}}`。
   - `config` 是 AudioSpecificConfig 的 **base64**（G.711 为空串）；它不是 SDP `fmtp` 里那个十六进制 `config=`。
   - 为什么必须是文本帧且在二进制帧之前：旧 bundle 用 `event.data instanceof ArrayBuffer` 判据，
     会天然忽略它；一旦排在二进制帧之后，老前端会把文本当帧解析，直接黑屏。
   - 查询失败不产生错误路径，只写 `present=false` + `reason`：一次探测失败不能影响视频建连。

2. **音频通道**：`GET /api/v1/cameras/:id/streams/:role/audio/ws`（同受 `auth.RequireAuth` 保护）。
   - 帧格式 ZLA1，20 字节大端头 + 裸负载：

     | 偏移 | 长度 | 字段 | 说明 |
     | --- | --- | --- | --- |
     | 0 | 4 | Magic | `0x5A4C4131` ("ZLA1") |
     | 4 | 1 | Codec | 与 C ABI 一致（`0x10`=AAC, `0x11`=PCMA, `0x12`=PCMU） |
     | 5 | 1 | Flags | Bit0 Discontinuity / Bit1 PtsSynthetic |
     | 6 | 2 | Reserved | 恒为 0 |
     | 8 | 4 | `seq` | 音频轨独立计数 |
     | 12 | 8 | `pts_us` | 与视频**同一条**输出时间轴（Native 已归一化，前端不得重算） |
     | 20 | N | Payload | G.711 原始采样 / AAC raw AU |

   - 不复用 ZLM2 头：`frame_class` / `gop_id` 对音频没有语义，塞进去只会制造歧义。

3. **每客户端（登录会话）一条**，不是全局一条：
   - 键是 `sha256(sessionToken)` 的前 16 位十六进制；**日志中绝不允许出现 token 原文**。
   - 同一会话开第二条 → 旧连接以 **4001 superseded** 关闭；不同会话互不影响。
   - 全局硬上限 `AudioChannelMaxCount = 8`，超限 → **4002** 拒绝。它是防滥用常量，不是配置项：
     已有会话的「接管」不受上限影响，否则刷新页面就再也接不回来。

4. **隔离是硬约束**：
   - 音频用自己的消费者 ID + 自己的 `engine.Stream` 句柄（Native 按 URL 去重，不多开 RTSP 连接），
     因此音频订阅的失败、背压、关流都碰不到视频订阅、GOP 与健康态。
   - `Stream.Subscribe` 拒绝同时订阅两条轨（`TrackMaskVideo|TrackMaskAudio`）：要两条就开两条订阅。
     「混订」等于把「音频挤掉视频」这个必须排除的故障模式重新引回来。
   - 音频不刷新 Native 空闲超时：相机画面卡死但音频还在流时，必须照常判 `TIMEOUT`。

5. **关闭与背压**：写队列 48 帧（≈0.96 s）。队列满 → 关闭该通道并上报 **4003 backpressure**，
   绝不阻塞原生回调、绝不触碰视频订阅。关闭顺序固定为
   `连接 → 订阅（同步排空）→ 流引用 → 注册表`；注册表必须在 `unsubscribe` 返回后才移除，
   否则会出现「注册表里没有、原生回调还在跑」的窗口。无音轨时先发 `{"type":"error","code":"NO_AUDIO"}`
   再以 **4404** 关闭，前端据此置灰而不是重试。

6. **可观测性**：`AudioChannelRegistry.Stats()` 给出 opened / superseded / rejected；
   通道建立、接管、溢出、关闭各一条日志（含 session 哈希前缀、camera、role、codec、seq、dropped）。

---

## 6. 前端摄像机配置与状态大盘规范

1. **业务域分层架构 (`web/src/features/camera/`)**：
   - `api/cameraApi.ts`：纯函数封装 REST API，由 Zod Schema 强类型约束出入参；
   - `hooks/useCameras.ts`：TanStack Query 封装 CRUD 与诊断 Mutation；
   - `hooks/useCameraEvents.ts`：基于 `EventSource` 订阅 `/api/v1/cameras/events`，通过 `snapshot` 全量初始化与 `change` 增量更新实现原地无感刷新，断网内置指数退避重连；
   - `components/`：大盘卡片、设备卡片、新增/编辑弹窗、诊断报告对话框与删除二次确认；
   - `utils/urlHelper.ts`：RTSP 密码掩码（`••••••••`）与跨环境安全剪贴板复制。
2. **原子探测门禁交互**：提交新增/更新表单时，按钮进入 3～5 秒探测 Loading，禁用输入并展示进度微动；探测失败时保留表单输入并精准回显业务错误码与说明。
3. **CAS Revision 乐观锁处理**：更新摄像机携带当前 `revision`；遇 409 冲突弹出防覆写提示并引导刷新。
4. **全量国际化 (i18n)**：所有标签、状态徽标、表单校验与错误信息必须完整覆盖 `en` / `zh-Hans` / `zh-Hant`。

---

## 7. 前端实时播放器与多路宫格监控规范 (`features/live/`)

1. **协议解包 (`wire-parser.ts`)**：
   - 严格解析 32 字节大端序 ZLM2 帧头：校验魔数 `0x5A4C4D32`、版本号 `2`、Codec、FrameClass、Flags、
     `pts_us`、`seq`、`gop_id`；版本或魔数不符必须抛错，不得静默容错（旧协议帧若被误解析会产出错误时序）；
   - ⚠️ **不得对 `pts_us` 做任何时基换算**（ZLM1 的 `/90` 已删除）。归一化责任在 Native 层。
   - 提取 Annex B NALU 载荷（零拷贝视图）；解析错误主动忽略保护。
2. **连接池复用与防抖释放 (`stream-pool.ts`)**：
   - `StreamConnectionPool` 针对每个 `${cameraId}:${role}` 维护 WebSocket 单例与 `refCount`；
   - 多个视口绑定同路流时共享同一条物理 WebSocket，单包广播分发；
   - 视口全部卸载（`refCount == 0`）后启动 3 秒 Grace Period 防抖，超时才真正关闭 WebSocket，避免布局切换时频密握手；
   - **重连永不放弃**：指数退避（1s→16s 封顶）+ 抖动（0.5~1.0 倍），抖动是必需的 ——
     16 路同时断线后无抖动重连等于对设备发起一次小型 DDoS。重试次数只用于改变 UI 文案，不作为停止条件；
   - `refCount 0→1` 时，除 `connected` / `connecting` 外的一切状态都必须重连。尤其 `error` / `closed`：
     否则 React 严格模式重挂载或路由往返会让流永久卡在错误态。
3. **WebCodecs 硬件加速渲染 (`live-player.tsx` + `webcodecs-decoder.ts`)**：
   - 优先通过 `VideoDecoder` 进行低延迟解码并绘制至 `<canvas>`；
   - ⚠️ **configure 时必须显式声明 `avc: { format: 'annexb' }` / `hevc: { format: 'annexb' }`**。
     规范中 `AvcDecoderConfig.format` 默认是 `"avc"`（AVCC 长度前缀），而载荷是 Annex B；
     Chrome 有启发式容错，Safari 严格实现会直接 `DecodingError`。
   - 追帧采用**整段跳到下一个 IDR**（`skipping`），不得逐帧挑着丢非关键帧 —— 丢参考帧会让画面花到下一个 IDR。
   - 触发跳帧需同时满足「已过 `WARMUP_FRAMES` 预热帧」与「连续 `CONGESTION_STREAK_THRESHOLD` 次超阈值」。
     硬件解码器首帧初始化要 100~300ms，不加预热窗口会在上电瞬间误判拥塞，表现正是「刚开始播放卡几帧」。
   - `H.265` codec string 的 compatibility 字段必须做 32 位**位反转**（ISO/IEC 14496-15）。
   - ⚠️ **`restartKey` 不得绑定 `showAIOverlay`**。restartKey 变化会销毁并重建解码器，而重建后必须等下一个
     IDR 才能出画；叠加层开关只影响绘制，`onFrameDrawn` 走 ref 传递，根本不需要重建解码器。
   - 收到帧后及时调用 `frame.close()` 释放 GPU 显存（`use-webcodecs-render-loop.ts` 统一负责）。
4. **1 / 4 / 9 宫格监控看板 (`LiveDashboard.tsx`)**：
   - 支持 1、4、9 宫格响应式排布；支持视口分配摄像机、主/子流切换、单视口独立全屏；
   - 视口绑定与布局模式持久化存储在 `localStorage`。



