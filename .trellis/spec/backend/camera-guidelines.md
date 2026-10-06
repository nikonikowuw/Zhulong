# 摄像机业务与生命周期规范

> 摄像机主子流建模、凭据加密、双流原子门禁、正交状态机与探活调度规约。

---

## 1. 核心设计原则

1. **凭据安全与密文隔离**：
   - 摄像机流凭据（RTSP URL）在 SQLite 中采用 AES-256-GCM 密文存储，使用关联数据 AAD（`v1:<camera_id>:<role>`）防止密文重放或跨流窜改。
   - 密钥文件必须设为 `0600` 严格权限。若数据库中存在密文而密钥丢失，系统**必须阻断启动并退出**，严禁自动生成替代密钥静默覆盖旧数据。
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

---

## 3. 探活与调度架构

| 流状态 | 探活方式 | 频次 / 阈值 | 失败判定行为 |
| --- | --- | --- | --- |
| **活跃流 (Session=Running)** | 媒体包到达监听 | 每 250ms 轮询检测 | 连续 4s 无视频包判超时，触发 `reconnecting` 与 `error` 状态 |
| **空闲流 (Session=Idle)** | 轻量 RTSP DESCRIBE | 60s 周期，±20% 随机抖动 | 连续 1～2 次失败标记 `error`（瞬态）；连续 3 次失败标记 `offline` |

1. **轻量 DESCRIBE 铁律**：空闲探活仅发送 DESCRIBE 校验状态码 200 与视频 SDP，**绝对禁止发送 SETUP、PLAY 或拉取 RTP 数据**，避免无谓消耗网络与摄像机编码器资源。
2. **Digest 认证适配**：集成 `icholy/digest` 低层 API，支持 MD5/SHA-256、`qop=auth` 与参数规范化，单连接完成挑战与重试，限制单次探测总预算 ≤5s。
3. **有界调度与并发控制**：后台调度器使用带权信号量（默认并发上限 4～8），对同一摄像机的同一流去重避免并发重复探测。
4. **收包证据节流与活跃判定**：活跃流推流期间，分发器在遇到关键帧或时间间隔 ≥1s 时节流上报 `EvidencePacketActivity` 刷新 `LastCheckedAt`，保证 250ms 轮询检测准确反映物理收包真实性，避免误判超时。
5. **调度器快速优雅停机**：调度器内部维护随 `Stop()` 级联取消的根 Context；轻量 DESCRIBE 客户端通过监听上下文取消触发 `conn.SetDeadline(time.Now())` 立即打断网络阻塞，实现停机时延 <100ms。

---

## 4. SSE 事件流与背压保护

1. **路由防冲突**：SSE 路由挂载在 `/api/v1/cameras/events`，在路由树中必须先于 `/:id` 参数路由注册，避免路由碰撞。
2. **原子初始快照**：客户端订阅成功后，首条消息推送 `event: snapshot`，携带当前所有摄像机完整状态快照与单调自增 `sequence`，消除快照与增量事件间的时序间隙。
3. **有界队列与慢消费者驱逐**：单个 SSE 连接维护有界缓冲通道（默认 32），广播塞满时主动断开该客户端（Eviction），防止慢客户端引起服务端内存泄漏。
4. **会话时效校验**：SSE 维持长连接期间，每 30 秒主动校验 Cookie 中的会话 Token，会话撤销或过期时立即断开连接。

---

## 5. WebSocket 实时推流与 ZLM1 协议规约

1. **二进制封包协议 (Wire Protocol: ZLM1)**：
   每个推送到 WebSocket 客户端的二进制消息包含 24 字节大端序固定头：
   - `Bytes 0..3`：Magic `0x5A4C4D31` ("ZLM1")
   - `Byte 4`：Codec（`0x01` = H.264, `0x02` = H.265）
   - `Byte 5`：Flags（位掩码：Bit 0 = KeyFrame, Bit 1 = HasPTS, Bit 2 = HasDTS）
   - `Bytes 6..7`：Reserved（`0x0000`）
   - `Bytes 8..15`：PTS（`int64` 大端序，90kHz 时钟基）
   - `Bytes 16..23`：DTS（`int64` 大端序）
   - `Bytes 24..N`：Annex B NALU 载荷（以 `00 00 00 01` 或 `00 00 01` 起始码分隔）
2. **按需生命周期与单例拉流**：
   - 路由：`GET /api/v1/cameras/:id/streams/:role/ws`（及 `/api/v1/cameras/:id/ws` 默认主流），严格受 `auth.RequireAuth` 保护。
   - $0 \rightarrow 1$ 激活：首个 Web 客户端连入时，解密 RTSP 凭据并向 Native 引擎发起 `Acquire(ConsumerPreview)` 与 `Subscribe`，状态切为 `SessionStateRunning`，并立即向 `/api/v1/cameras/events` 广播状态变更。
   - 并发 Singleflight 保护：StreamHub 采用 `singleflight.Group` 协同合并对同一路流的并发获取请求，彻底避免击穿造成对摄像机重复发起物理 RTSP 握手与 C++ 句柄开销。
   - $1 \rightarrow N$ 扇出：同一路流多个 Web 观众共享单例物理拉流，单次封包并发扇出。
   - $1 \rightarrow 0$ 释放：所有客户端断开后，注销拉流，进入 Native 8s Grace Period 防抖休眠，状态切为 `SessionStateIdle`，并向 SSE 广播状态变更。
3. **GOP 秒开缓存 (Instant Playback)**：
   - 分发器持续缓存最近的关键帧（包含 SPS/PPS/VPS）。新连入的客户端立即收到该帧，消除等待长 GOP 造成的黑屏。
4. **背压与慢客户端淘汰**：
   - 每个客户端分配 64-slot 有界缓冲。拥塞时优先丢弃非关键帧；持续阻塞则主动断开连接（Close 1008 Policy Violation），保护服务端内存。

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

1. **协议解包 (`wireParser.ts`)**：
   - 严格解析 24 字节大端序 ZLM1 帧头，校验魔数 `0x5A4C4D31`、Codec（H.264 / H.265）、Flags（KeyFrame/PTS/DTS）以及 90kHz PTS/DTS；
   - 提取 Annex B NALU 载荷；解析错误主动忽略保护。
2. **连接池复用与防抖释放 (`streamPool.ts`)**：
   - `FrontendStreamPool` 针对每个 `${cameraId}:${role}` 维护 WebSocket 单例与 `refCount`；
   - 多个视口绑定同路流时共享同一条物理 WebSocket，单包广播分发；
   - 视口全部卸载（`refCount == 0`）后启动 3 秒 Grace Period 防抖，超时才真正关闭 WebSocket，避免布局切换时频密握手。
3. **WebCodecs 硬件加速渲染 (`LivePlayer.tsx`)**：
   - 优先通过 `VideoDecoder` 进行低延迟解码并绘制至 `<canvas>`；
   - 队列堆积超过 6 帧时丢弃非关键帧实现快速追帧（端到端延迟控制在 150ms 以内）；
   - 收到帧后及时调用 `frame.close()` 释放 GPU 显存。
4. **1 / 4 / 9 宫格监控看板 (`LiveDashboard.tsx`)**：
   - 支持 1、4、9 宫格响应式排布；支持视口分配摄像机、主/子流切换、单视口独立全屏；
   - 视口绑定与布局模式持久化存储在 `localStorage`。



