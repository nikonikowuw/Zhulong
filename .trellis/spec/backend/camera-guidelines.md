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

---

## 4. SSE 事件流与背压保护

1. **路由防冲突**：SSE 路由挂载在 `/api/v1/cameras/events`，在路由树中必须先于 `/:id` 参数路由注册，避免路由碰撞。
2. **原子初始快照**：客户端订阅成功后，首条消息推送 `event: snapshot`，携带当前所有摄像机完整状态快照与单调自增 `sequence`，消除快照与增量事件间的时序间隙。
3. **有界队列与慢消费者驱逐**：单个 SSE 连接维护有界缓冲通道（默认 32），广播塞满时主动断开该客户端（Eviction），防止慢客户端引起服务端内存泄漏。
4. **会话时效校验**：SSE 维持长连接期间，每 30 秒主动校验 Cookie 中的会话 Token，会话撤销或过期时立即断开连接。
