# GB28181 接入技术设计

> 决策依据见 `prd.md`；本文只描述实现方案、边界与验证点。

## 1. 模块拓扑

```txt
React (GB28181 设备/通道页 + 摄像机列表)
  │ REST / WS
Go internal/gb28181 ── UAS：REGISTER / 入站 MESSAGE ──▶ GB28181 设备
  │                     UAC：目录查询 MESSAGE / 点播 INVITE / ACK / BYE ──▶
  │ 信令、目录、认证、会话编排、容量与观测
  │ 媒体端口池：每会话 RTP+RTCP 一对（RTCP 由 Go 侧读丢弃，不进 ABI）
Native Engine (ABI v4) ── dup(RTP fd) / RTP→PS→ES ──▶ 设备推流
  │
  ▼
PreviewNormalizer → FrameBroker/Decode → StreamHub → WS 预览 / AI
```

职责切分：**Go 只管 SIP 信令、设备/目录持久化与会话/端口编排；Native 只管媒体 socket、RTP 与 PS 解复用，不发 SIP。** Camera 定义与 GB28181 无关的媒体会话接口；GB28181 实现并由 `internal/app` 注入。两者通过已绑定 fd 和会话参数交接。

## 2. 点播时序（关键约束）

```txt
1. SessionProvider 创建会话 ID；端口池在 media.bind_address 上**成对** bind RTP + RTCP UDP socket，
   并保留 fd/端口 lease。RTCP socket 由 Go 侧独占读并丢弃，不交给 Native、不进 ABI。
   原因：SDP 宣告 RTCP 端口却不 bind 时，设备发出的 RTCP 会收到 ICMP port unreachable，
   部分厂商设备随后静默停流，现场表现为「偶发黑屏、重连才恢复」。该选择同时决定容量模型
   （2 端口/会话 → 101 端口池 = 50 路并发，见 §12）。
2. Native Engine dup fd 并创建 GB 输入流，source_key 使用本次会话 ID；Acquire 不等待首包。
3. Go 为 Engine 流建立 packet subscription 和 pump，再由 SIP UAC 发 INVITE；SDP 使用
   media.advertised_ip。Offer 的 payload type、SSRC 和 RTCP 形态依据 S3 核验的标准/合成夹具；目标设备抓包有则纳入，真机兼容性留待 S4 验证。
4. 收到 1xx 时保留事务状态；收到 2xx 后校验 SDP 并发送 ACK；拒绝/超时时按 SIP 对话状态处理，必要时 CANCEL。
5. 设备开始向已就绪的 fd 推 RTP/PS；Native 校验并解复用，状态 STARTING→RUNNING。
6. StreamHub 已有的 subscription 缓冲并转发收到的包，WebSocket 出画。
```

**不得把 `INVITE` 的响应简单描述为“200 OK + SDP”**：实现必须处理出站 UAC 事务及对话生命周期。SDP offer 的 payload type 先于 INVITE 确定；P0 先使用单一、明确映射的 PS payload type，除非标准和设备夹具证明需要多值协商。RTCP 是否接收、是否分配独立端口，必须在 S3 ABI 冻结前定案。

两条不可动摇的顺序约束：

- 媒体 fd 必须先 bind，Engine 读循环与 Go packet subscription 必须就绪后再发 INVITE；设备可能在最终响应/ACK 邻近时立即推流。
- `AcquireGB28181` 不得等待首包。首包等待由 Native 状态、可中断读取和 idle timeout 承担；自定义 AVIO 的 read callback 必须使用有界等待/取消检查，不能无限阻塞。

停止与重连：

```txt
最后一个 Preview/AI consumer 离开 → SessionProvider 进入 8 秒 grace，保留 SIP dialog、原始 fd 和媒体端口
  → grace 内重新 acquire：复用同一 source_key/fd，不重发 INVITE
  → grace 到期：发送 BYE → 请求 Native 输入停止并 join → 确认 dup fd 已关闭
  → 关闭 Go 原始 fd → 归还端口
```

现有 Engine 在最后一个 consumer 释放后也会启动 8 秒 grace。GB28181 不得叠加两个独立 grace 导致会话/端口泄漏：必须选定单一计时所有者，并提供能确认 Native worker 已停止的路径（例如 GB 专用立即释放/完成通知，或将同一 grace 纳入 Engine 会话管理）。在确认 dup fd 已释放之前，端口不得回池。

## 3. Native ABI 变更（草案：v3 → v4，冻结前关闭媒体协商门禁）

下列结构只描述**单 RTP fd**。RTCP 已定案由 Go 侧持有并读丢弃（见 §2 步骤 1），**不进入 ABI**，
因此不需要额外 fd、不需要 Native 侧 RTCP 解析。payload type 与 SSRC 的协商形态仍需 S3 以标准正文
和设备夹具核验后冻结；其余字段在本阶段按草案处理，冻结前不得当最终签名。

```c
typedef struct {
    int32_t  rtp_fd;              /* Go 持有已 bind 的 UDP fd；Engine dup 后仅关闭自己的副本 */
    int32_t  payload_type;        /* INVITE offer 中唯一选定的 PS payload type */
    int32_t  codec_hint;          /* Zhulong_CODEC_H264/H265；0 表示未知，最终以 PS stream_id 为准 */
    uint32_t idle_timeout_ms;     /* 无有效视频包超时，0 取默认 */
    const char *source_key;       /* 必须含本次 GB 会话 ID；C++ 在调用期间复制 */
} Zhulong_gb28181_params;

Zhulong_status_t Zhulong_stream_acquire_gb28181(
    Zhulong_engine_h engine, const char *uri, const Zhulong_gb28181_params *params,
    uint64_t consumer, int32_t kind, Zhulong_stream_id *out_stream_id);
```

- `ZHULONG_ABI_VERSION` 升 4，Go 侧 `expectedABIVersion` 取自头文件宏；`Engine.Start` 校验头文件与静态库一致。
- fd 语义：Engine `dup()` 一份用于自身读循环，**不关闭调用方 fd**。正常释放时 Native 必须先取消读取并 join worker，再关闭 dup；Go 只有在 Native 完成后才关闭原始 fd 和归还端口。
- C++ 不保留 Go 指针；`source_key` 由 Go `C.CString` 分配并在同步 ABI 调用返回后释放，C++ 在返回前复制。
- 自定义 fd 读取要有可唤醒的取消路径和有界 idle 等待；FFmpeg interrupt callback 本身不能替代自定义阻塞 read callback 的取消实现。

## 4. Engine 池改造：去重键必须含会话

现状：`Engine::acquire` 线性扫描 `streams` 并按 `Stream::url` 相等去重（`native/src/pipeline/engine.cpp:196`）。
GB28181 的 URI（`gb28181://<device>/<channel>`）跨点播会话保持稳定，但底层 RTP 会话已换 fd。
仅按 url 去重会让第二次点播错误复用已死的接收任务。

改法：

- `Stream` 增加 `std::string source_key`（RTSP 路径传规范化 `url`，GB28181 路径传 `<uri>#<session_id>`）。
- 去重比较改为 `source_key`；`options` 冲突判定不变。GB 会话的 source key 必须由 SessionProvider 生成且在同一 dialog 生命周期内稳定，不从用户可控 URI 参数生成。
- 同一 GB dialog 的 Preview 与 AI consumer 必须取得相同 session ID/source key，才能共享一次 INVITE 与 Native 输入；不同 SIP dialog 即使 URI 相同也不得复用。
- RTSP 的现有 8 秒宽限期行为保持不变。GB 的 BYE/端口回收必须与 Native grace 协同，不能假设 `Stream::Close()` 已同步关闭输入 fd。

## 5. 原生输入源（`native/src/nodes/capture/`）

新增 `rtp_ps_input.{hpp,cpp}`，与 `rtsp_input` 平级，输出同一个 `Video read_*()` 契约（`on_ready` 回调 + packet 回调）。S3 先完成固定版 FFmpeg + 已绑定 fd 的 PoC，再冻结具体输入实现：

- **RTP 层**：若选择自定义 RTP 输入，解析头部并使用有界重排缓冲处理包序回绕、重复包、乱序与丢包；验证协商 payload type、SSRC 和来源端点。不得仅以“常见包小于 1400 字节”作为完整性假设。
- **PS 层**：优先通过自定义 `AVIOContext` 将完整 PS 字节流送入固定 FFmpeg 的 MPEG-PS demuxer。PRD 中的“FFmpeg rtp + mpegps”与本方案是待 PoC 比较的两条输入路径，不能同时当作已定实现。没有失败证据前不自研 PS 解包。
- **FT 字段**：仅在抓包/标准确认目标设备确实携带对应扩展并确定剥离边界后才处理 `0xC0`/`0xC1`；禁止对任意 RTP payload 按首字节猜测并剥离。
- **时间戳**：优先使用 PS demuxer 输出的 packet PTS/DTS 与 time base，交给 `PreviewNormalizer` 归一；不得把 RTP 90 kHz timestamp 直接当成 ES 的 PTS/DTS。只有缺失时才按既有合成时间戳契约处理。
- **取消与状态**：无首包时保持 STARTING；有界读取超时后失败。取消必须能唤醒 socket 等待并 join worker，Engine 才能关闭 dup fd。
- 输出必须与 RTSP 路径同构：`codec` / `key_frame` / `extradata` 全部交给 `PreviewNormalizer` 补齐，上层不感知来源差异。

**ABI 冻结门禁**：先确认输入路径、PT、SSRC、RTCP fd 数量和关闭通知，再冻结 ABI v4。私有字段和国标版本差异必须有标准条文或目标设备夹具支持。

## 6. Go `internal/gb28181` 域

| 文件 | 职责 |
| --- | --- |
| `module.go` | `NewComponents(...)` 纯粹构造，不直接注册独立 Fx 生命周期 hook；组件交给 `internal/app` 的 `lifecycleRuntime` 管理启停与回滚 |
| `config.go` / `config_store.go` | 单例配置的读取、校验、持久化；进程启动时装载 startup snapshot，HTTP 更新只保存 desired config，不热切换 SIP/RTP socket |
| `sip_server.go` | 固定版本 sipgo 的 SIP UAS + 出站 UAC；UDP/TCP listener 共用入站处理器，保存 TCP 注册流的可用路由状态，并支持出站事务/对话 |
| `digest.go` | REGISTER 两次握手；每设备独立密码；明确 nonce 生命周期、过期和重放校验 |
| `device.go` | 设备 allowlist、REGISTER 状态机与 Keepalive 超时；宿主重启后持久化的旧状态不直接视为 online |
| `catalog.go` | 出站 `MESSAGE` + `CmdType=Catalog` 查询；关联事务、SN、分片总数、乱序/丢片超时与 upsert |
| `keepalive.go` | `CmdType=Keepalive` 与 `CmdType=Alarm` 的接收与应答 |
| `portpool.go` | 在 `media.bind_address` 上成对创建 RTP/RTCP UDP fd lease；显式耗尽错误；对外宣告地址不参与 bind |
| `ratelimit.go` | 入站 REGISTER/事务按来源限速、nonce 防重放窗口、报文大小上限 |
| `capacity.go` | 并发会话上限、每会话接收缓冲上限、fd 预算与耗尽计量 |
| `netchange.go` | 订阅网络变更；媒体/信令地址前提失效时停止接入并置 `state=error` |
| `snapshot.go` | 运行态快照：设备/会话/端口占用/首帧耗时/丢包/失败原因分布 |
| `session.go` | Preview/AI 共用的点播会话状态机：Prepare fd → Engine acquire + subscribe → UAC INVITE/ACK → grace → BYE → Native stop/join → 归还 fd/端口 |
| `store.go` / `model.go` | `gb_devices` / `gb_channels` 仓储（依赖 `database.DBProvider`）；设备预置时允许 remote address 为空 |
| `handler.go` / `requests.go` / `responses.go` | REST：配置 GET/PUT、设备预置/凭据更新、设备列表、通道列表、手动刷新目录、添加为摄像机 |
| `xmldoc.go` | GB28181 XML 编解码，按字节验证 UTF-8，失败时回退 GB18030；不信任 XML encoding 声明 |

### 持久化运行配置（迁移 `000020`）

GB28181 配置以 SQLite 的 `gb28181_configs` 单例行为唯一事实源，默认 `enabled=false`。本功能配置不放入 `config.toml`，避免配置文件与 UI/数据库互相覆盖。设备 Digest 密码仍单独加密存入 `gb_devices.encrypted_password`，不得出现在配置 DTO。

`enabled=true` 时，应用启动先读取并校验持久化配置，再初始化 SIP listener 和媒体端口池；无效配置不得部分启动或悄悄回退到另一份配置。`enabled=false` 时不绑定 SIP 5060。监听地址、transport、SIP 身份、媒体 bind/advertised 地址、端口范围和安全 profile 的任何持久化修改均不热应用。GB28181 是可降级子系统：bind/listen 失败时关闭部分 socket、报告 runtime error，不能导致整个 Web 管理面启动失败；它不改变健康检查仅表示数据库与 Native Engine 就绪的既有语义。

- `GET /api/v1/gb28181/config` 返回 envelope `data.config`（已保存的 desired 配置）、`data.runtime.startupConfig`（本进程启动时读取并尝试应用的配置）、`data.runtime.state`（`disabled` / `running` / `error`）和 `data.runtime.restartRequired`。
- `PUT /api/v1/gb28181/config` 校验整份配置并原子持久化；空 `sip_realm` 先规范化为 `sip_id` 再保存。响应返回保存后的配置和运行状态。成功保存不改变正在工作的 listener/会话。
- `restartRequired` 由规范化后的 desired 与进程启动时 `startupConfig` 比较得出，忽略 `updated_at`，不单独写数据库。重启加载 desired 后两者一致，提示清除；若 listener 启动失败，`state=error` 单独显示，不能仅凭配置相等显示“运行中”。
- SIP listener 启动/绑定失败不应导致 Web 管理面整体退出；关闭部分启动的 socket，设置 `state=error` 并记录脱敏诊断，允许操作员通过 UI 修正配置后再次重启。
- 前端显示“需重启生效”和当前运行值/待生效值；本任务不提供远程重启 API，操作员通过部署服务管理流程重启。
- 配置变更写受保护 audit 事件，只记录安全摘要，不记录 Digest 密码或 SIP Authorization。
- `security_profile` P0 仅接受 `standard`；其他值校验失败。设备单独凭据通过设备管理 UI 更新。

### 数据模型（迁移 `000020`）

```sql
CREATE TABLE gb28181_configs (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    sip_id TEXT NOT NULL DEFAULT '',
    sip_realm TEXT NOT NULL DEFAULT '',
    sip_listen TEXT NOT NULL DEFAULT '0.0.0.0:5060',
    sip_transport TEXT NOT NULL DEFAULT 'both' CHECK (sip_transport IN ('udp', 'tcp', 'both')),
    security_profile TEXT NOT NULL DEFAULT 'standard' CHECK (security_profile = 'standard'),
    media_bind_address TEXT NOT NULL DEFAULT '0.0.0.0',
    media_advertised_ip TEXT NOT NULL DEFAULT '',
    rtp_port_min INTEGER NOT NULL DEFAULT 30000 CHECK (rtp_port_min BETWEEN 1 AND 65535),
    rtp_port_max INTEGER NOT NULL DEFAULT 30100 CHECK (rtp_port_max BETWEEN 1 AND 65535),
    require_registered_device INTEGER NOT NULL DEFAULT 1 CHECK (require_registered_device = 1),
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (rtp_port_min <= rtp_port_max)
);
INSERT OR IGNORE INTO gb28181_configs (id) VALUES (1);

CREATE TABLE gb_devices (
    device_id TEXT PRIMARY KEY NOT NULL CHECK (length(device_id) = 20 AND device_id NOT GLOB '*[^0-9]*'), -- 20 位国标编码
    name TEXT NOT NULL DEFAULT '',
    manufacturer TEXT, model TEXT, firmware TEXT,
    transport TEXT NOT NULL DEFAULT 'udp' CHECK (transport IN ('udp', 'tcp')),
    remote_addr TEXT,                     -- 首次 REGISTER 前为 NULL；之后以实际来源/rport 更新，兼容 NAT
    expires_seconds INTEGER NOT NULL DEFAULT 3600 CHECK (expires_seconds >= 60),
    state TEXT NOT NULL DEFAULT 'offline' CHECK (state IN ('offline', 'online')),
    last_keepalive_at DATETIME,
    registered_at DATETIME,
    encrypted_password BLOB,             -- 复用 camera 的 Cipher，AAD = v1:gb:<device_id>
    created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL
);

CREATE TABLE gb_channels (
    device_id TEXT NOT NULL REFERENCES gb_devices(device_id) ON DELETE CASCADE,
    channel_id TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    manufacturer TEXT, model TEXT, owner TEXT, civil_code TEXT, address TEXT,
    parental INTEGER NOT NULL DEFAULT 0,
    parent_id TEXT,
    safety_way INTEGER, register_way INTEGER, ptz_type INTEGER,
    resolution_width INTEGER, resolution_height INTEGER,
    camera_id TEXT REFERENCES cameras(id) ON DELETE SET NULL,  -- 显式添加后回填
    created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
    PRIMARY KEY (device_id, channel_id)
);
```

`ON DELETE SET NULL` 保证删摄像机后通道不残留失效 camera_id。若设备或通道仍关联摄像机，P0 删除操作应先拒绝并要求显式解绑；不能因 `gb_channels` 级联删除而让摄像机留下无法解析的 GB URI。

端口范围按 **2 端口/会话**（RTP + RTCP）计容量：默认 `30000..30100` = 101 端口 = 50 路并发点播。
`rtp_port_max - rtp_port_min + 1` 为奇数时最后一个端口不使用，不做特殊处理；范围变更属需重启生效配置。
奇数端口场景下的容量计算与校验需在 `portpool.go` 测试中固定。

## 7. camera 域改造

- `uri.go`：拆出 `ParseStreamURI`，按 scheme 分派到 `ParseRTSPURI` / `ParseGB28181URI`。
  GB28181 形态固定为 `gb28181://<deviceId>/<channelId>`，附加参数一律拒绝。
- **接口方向**：`camera` 定义不依赖 `gb28181` 包的 `MediaSessionProvider` / `MediaSessionLease`；`gb28181` 实现并由 `internal/app` 注入。接口表达两阶段生命周期：准备并绑定媒体 socket、Native acquire 与订阅就绪后启动 INVITE；关闭时完成 BYE、Native 停止/join 和端口释放。`camera` 不导入 `gb28181`。
- `stream_media.go`：`MediaEngine` 暴露通用的 GB lease acquire 路径，接收 fd、单一协商 PT、codec hint、session/source key；Engine 只 dup fd，不保留 Go 指针。
- `stream_hub.go`：Preview 分支先准备 lease、acquire Engine、建立 packet subscription，再启动 INVITE；并发获取同一路 GB 会话须复用同一 lease/session ID。
- **AI 路径**：`ResolveAIStreamBrokerPath` 也必须按 protocol 取得同一 GB session，不得继续把 `gb28181://` 交给 RTSP `AcquireStream`；Preview 与 AI 消费者应共享一次 INVITE 和 Native source。
- **健康与诊断**：GB 流跳过空闲 RTSP DESCRIBE。设备在线由 REGISTER/Keepalive 证据决定；流健康由活跃媒体收包证据决定。手动诊断走 GB 注册状态与受控点播/媒体证据路径，不得将 RTSP URI 解析失败记作摄像机故障。
- `service.go`：从 `gb_channels` 添加摄像机时跳过 RTSP 原子探测，读通道元信息建 camera 和一条 `protocol=gb28181` 主流；首帧元信息仅在校验后更新。普通 RTSP 创建/更新门禁不变；通用摄像机编辑不得把 GB 流误写成 RTSP。
- 删除已关联摄像机的设备/通道前必须显式解绑；删除摄像机时清空 `gb_channels.camera_id`。
- 凭据列：`camera_streams.encrypted_uri` 存摄像机身份 URI 的密文，AAD 仍按 camera ID/role；SIP Digest 密码只存 `gb_devices.encrypted_password`，AAD 为 `v1:gb:<device_id>`。启动时密钥丢失检查必须覆盖两类密文。

## 8. REST 与前端

| 端点 | 说明 |
| --- | --- |
| `PUT /api/v1/gb28181/devices/:deviceId` | 预置/更新设备 allowlist 与 Digest 密码；密码写入前加密，GET/日志不得回显 |
| `GET /api/v1/gb28181/devices` | 设备列表（含在线状态、通道数） |
| `GET /api/v1/gb28181/devices/:deviceId/channels` | 通道列表（含是否已添加为摄像机） |
| `POST /api/v1/gb28181/devices/:deviceId/catalog` | 手动触发目录查询 |
| `POST /api/v1/gb28181/devices/:deviceId/channels/:channelId/camera` | 添加为摄像机（复合通道键） |
| `GET /api/v1/gb28181/runtime` | 运行态快照：在线设备数、活动会话数、端口占用/总量、首帧耗时分布、丢包与失败原因计数 |
| `GET /api/v1/gb28181/config` | 读取已保存配置、本进程 startup config、运行状态及 `restartRequired` |
| `PUT /api/v1/gb28181/config` | 校验并原子保存 desired 配置，空 `sip_realm` 规范化为 `sip_id`；本进程不热切换，返回最新 `restartRequired` |

接口遵循现有 JSON 信封和 protected route group；设备密码 DTO 不可被 GET 返回，审计与日志记录不得包含密码/Authorization。配置 API 返回 desired、startup config、runtime state 和重启状态；保存成功不代表当前 SIP listener 已重配。

前端新增 `web/src/features/gb28181/`（设备表、手工预置/更新凭据、通道抽屉 + 添加动作），三语文案同步 `locales/`。系统设置新增 `/settings/gb28181` 入口，由该 feature 的 `index.tsx` 暴露配置页；表单显示“需重启生效”，对比展示当前进程启动配置和待生效配置，重启状态清除前不把 desired 值标成 active，并单独展示 runtime 错误状态。摄像机表单不新增 GB28181 URI 编辑分支，避免双入口。

## 9. 风险与回退

| 风险 | 影响 | 回退 |
| --- | --- | --- |
| `media.advertised_ip` 配置错误 | 注册成功但媒体不可达 | 与本地 bind 地址分离；配置错误给出诊断提示，不因 NAT 公网地址不在网卡上而拒绝启动 |
| Native fd/端口生命周期不一致 | 端口重绑失败或 fd 泄漏 | 验证 Native worker join/dup fd 关闭后才归还端口；测试 grace 内复用、过期回收和失败回滚 |
| RTP/PS 非合规或私有扩展 | 黑屏或花屏 | 先保存受控样本并复现；仅针对证据添加解析兼容，不盲目剥离 payload 字节 |
| ABI v4 与旧静态库错配 | 启动即拒绝 | 现有版本校验覆盖，属期望行为 |
| sipgo API/传输流路由不匹配 | TCP 注册成功但出站请求无法到设备 | S1 固定依赖版本，测试 UDP/TCP 注册、出站 MESSAGE/INVITE 和 TCP 连接断开/恢复路径 |
| SDP 宣告 RTCP 却不 bind → 设备收 ICMP 后停流 | 偶发黑屏，需重连才恢复，极难定位 | 已成对 bind 并由 Go 读丢弃 RTCP（§2 步骤 1）；容量模型按 2 端口/会话计算 |
| DHCP 换址 / 网卡重配 | 注册与媒体地址同时失效，表现为「设备在线但黑屏」 | 订阅 `internal/network` 变更；前提失效时停止接入、清理会话、置 `state=error` 并要求重启 |
| 系统时钟跳变（断电重启漂移） | PS PTS/DTS 全乱，预览抖动或时间轴错乱 | 复用 `internal/systemtime` 状态：未同步或偏移超阈时拒绝点播并返回可诊断错误 |
| 媒体端口池耗尽 | 第 N+1 路点播静默挂起 | 端口池显式返回稳定错误码并计入运行态快照；测试覆盖第 51 路 |
| 并发解码超过 SoC 能力 | 点播全成功但画面卡顿/掉帧 | 容量只承诺会话/解复用路数；解码路数在目标板实测后单独公布，不与会话数混用 |
| 并发目录同步压垮管理面 | 1600 通道入库期间 REST 不可用 | 批量 upsert + 分片限速；容量验收明确「管理 API 不劣化到不可用」 |
| 入站 REGISTER 洪泛 / 畸形 XML | 进程 OOM、panic 或授权探测 | 报文大小上限、XML 禁止外部实体与深度限制、按来源限速、nonce 防重放（P0 交付项，非加固项） |

## 10. 版本与特征兼容策略

不根据设备自报版本串走协议分支。下表是实现假设，不是已验证的完整标准差异清单；S1/S3 逐项核对标准正文与目标设备报文。未验证项不得写成兼容承诺。

| 特征 | 当前实现假设（需核实） |
| --- | --- |
| 信令字符集 | 先验证 UTF-8，失败时尝试 GB18030；保留原始报文用于诊断，不能只看 XML 声明 |
| 视频编码 | 从 PS demux 结果/stream_id 判断 H.264/H.265，不看版本串 |
| 音频 | P0 不向上游输出音频；具体 PS stream type 按标准/设备样本校验 |
| RTP 时间戳 | 以 PS demux 输出的 PTS/DTS 为主；RTP 时间戳不得未经验证地替代视频 packet 时间戳 |
| SIP 传输 | UDP/TCP 入站均需验收；TCP 出站请求必须可路由到注册设备的有效连接 |
| 媒体 TCP 封装 | RFC 4571 不在 P0（前提：设备与盒子同侧局域网）；出现外侧 NAT 设备时重新评估 |
| 高安全模式 | P0 只支持明确声明的 standard profile；high 不得被描述为 TLS/加密/国密支持 |

涉及合规性的实现以 GB/T 28181 标准正文和目标设备报文核验，不以二手摘要替代。

## 11. 容量、资源与可观测

### 11.1 容量模型

并发会话上限由端口池直接决定，不做另外估算：

```txt
target_concurrency = floor((rtp_port_max - rtp_port_min + 1) / 2)   // 2 端口/会话
默认 30000..30100 → 101 端口 → 50 路并发点播
```

| 维度 | P0 目标值 | 说明 |
| --- | --- | --- |
| 注册设备 | 100 台 | 小盒形态上限；超出不拒绝但计入运行态 |
| 目录通道 | 1600 条 | 100 × 16，衡量批量 upsert 与 UI 分页 |
| 并发点播/解复用 | 50 路 | 端口池硬约束；第 51 路返回稳定错误码 |
| 并发解码 | **不承诺** | 受 SoC 硬解能力限制，须目标板实测后单独公布 |
| 首帧时延 | P95 ≤ 3 s（同网段） | 无有效媒体 10 s → FAILED 并释放资源 |

**解码与会话必须分开表述。** 预览链路若由浏览器侧解码，则 50 路会话仅消耗解复用 CPU；
若启用了 Native 解码或 AI，实际可点播路数需按 SoC 实测下调，并在部署文档中与本表并列说明。

### 11.2 资源上限

| 资源 | 约束 |
| --- | --- |
| 每会话接收缓冲 | 硬上限（RTP 重排窗口 + 转发送队列），不得随包无界增长；超限丢包并计数 |
| 每会话 fd | 2（RTP + RTCP）；Native dup 后自有副本 |
| TCP 信令连接 | 每设备最多 1 条活跃 flow，断线即清理；总连接数上限可配 |
| fd 预算 | 100（TCP）+ 100（50 会话 × 2）+ 引擎/SQLite/监听 ≈ 250+，部署需 `LimitNOFILE` ≥ 8192 |
| 入站报文 | SIP 报文上限（UDP 受 datagram 限制，TCP 流需显式上限）；XML 单文档上限并记录超限样本 |

### 11.3 可观测

结构化日志必备关联字段：`device_id`、`channel_id`、`session_id`、`sip_call_id`、`transport`、
`error_code`。`snapshot.go` 暴露运行态快照（经 protected API，不引入外部 TSDB）：

- 设备：总数 / online / offline / 注册失败计数（按原因）
- 会话：活动数 / STARTING / RUNNING / FAILED / 首帧耗时直方图（分位）
- 端口池：已用 / 总量 / 耗尽次数
- 网络：丢包、RTP 序号回绕/重复、PS 解析失败计数
- 时钟与网络：当前时钟偏差、最近一次 IP 变更时间

## 12. 运行时异常与自愈

| 事件 | 检测 | 处理 |
| --- | --- | --- |
| 本机 IP / DHCP 变更 | `internal/network` 变更通知 | 停止接受新会话 → 按序 BYE 既有会话 → 释放端口 → `state=error` + 提示重启（advertised 推导前提已变） |
| 系统时钟未同步 / 跳变 | `internal/systemtime` 状态机 + 偏差阈值 | 新点播请求返回可诊断错误；已建立会话不强制中断，但打点告警 |
| SIP listener 运行期退出 | 读/绑错误返回 | 有限次退避重建，仍失败则 `state=error`；不阻塞 Web 管理面 |
| 设备反复上下线 | REGISTER 频率与状态抖动 | 按来源限速 + 抖动抑制，避免会话风暴 |
| 端口耗尽 | 端口池分配失败 | 稳定错误码 + 计数 + 日志（含当前活动会话数） |
| 引擎/Native 停止超时 | join 超时 | 标记会话 FAILED，不归还端口直到确认 dup fd 关闭；超时后告警并保留诊断快照 |

## 13. GB 35114 与高安全接缝（不实现，仅预留）

P0 不实现 GB 35114 A/B/C，也不实现 2022 高安全（TLS / 媒体加密 / 国密）。为降低将来升级的重构成本，只固定两个插入点：

1. **信令侧（A/B 级）**：SIP 报文编解码走独立 codec 边界（`marshal/unmarshal` 单入口），
   签名封装可作为外层实现接入，不改动业务处理逻辑。证书体系、SM2/SM3 不在 P0 范围。
2. **媒体侧（C 级）**：Native 输入保持「fd → 字节流 → PS 解复用」边界，解密可插在
   RTP payload 与 PS 之间。注意解密会改变可用字节数与边界对齐，因此有界读取与
   AVIO 填充逻辑必须按「输入流长度不确定」设计，不得假设原始包长度即 ES 边界。

⚠️ 上述分级含义只用于工作量量级判定，具体条款（签名覆盖范围、报文封装、密钥协商流程）以标准正文为准；
本仓库未核验条文，不得据此宣称任何等级合规。

## 14. 明确不做

向上一级平台级联（UAC；设备点播所需的出站 UAC 对话仍属 P0）、历史回放、PTZ、报警订阅、语音对讲、注册审批工作流、
RTP over TCP 媒体面（P1，前提见 §10）、GB 35114 A/B/C 与 2022 高安全级别接入（TLS / 媒体加密 / 国密，接缝见 §13）。
