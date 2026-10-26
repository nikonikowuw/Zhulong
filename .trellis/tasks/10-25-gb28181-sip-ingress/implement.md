# 实施计划

> 阶段划分原则：每一阶段都能独立验收，且**不依赖真机**的部分先做完。
> 真机只在 S4 出现，此前所有逻辑都能用单测与合成报文验证。
>
> 原生改动后必须 `make native-build`（Air 不监听 C++）；依赖 `internal/engine` 的 Go 测试必须走
> `python3 native/scripts/build.py go test ...`，禁止裸 `go test`。
>
> 质量目标是「工业可用」（见 `prd.md`「目标形态与质量等级」）。因此安全基线、限流、可观测、
> 容量上限**按阶段前置**，不留到收尾：S0 定预算与日志关联字段，S1 做输入防护，S3 做资源上限，
> S4 做容量与自愈验收，S5 只做 soak 与证据归档。

## S0 · 骨架、配置、凭据引导、迁移与资源预算（已完成，待提交）

- GB28181 运行配置只存 `gb28181_configs`，不扩展 `internal/app.Config` / `cmd/Zhulong/config.go:fileConfig`，也不在 `config.toml` 增加重复配置；严格配置解析仍只管宿主静态配置。
- 迁移 `000020_gb28181.up.sql` / `.down.sql`：单例 `gb28181_configs`（默认禁用并插入默认行）、`gb_devices`、`gb_channels`；设备预置阶段 `remote_addr` 允许 NULL，删除关联 camera 的设备/通道前要求显式解绑。
- `internal/gb28181`：模型、store 和纯构造 `NewComponents(...)`；配置 Repository 使用 `database.DBProvider` 持久化 desired config，运行时服务单独持有启动快照并计算 `restartRequired`；不在域包内注册独立 Fx 生命周期 hook。
- **资源预算门禁（新增）**：`portpool.go` 按 `floor((max-min+1)/2)` 计算并发上限，奇数端口时末端口不用；
  `capacity.go` 定义并发会话上限、每会话接收缓冲上限与 fd 预算常量。这些上限在配置校验阶段就要能算出来，
  不允许运行期才发现端口不够。
- `advertised_ip` 推导：无 NAT 且机器只有一个非回环 IPv4 时允许留空并推导；多个 IPv4 或检测到 NAT 时必须显式配置，
  推导失败返回 422 而不是写一个错误地址进 SDP。
- 增加 protected `GET/PUT /api/v1/gb28181/config`；整份配置校验并原子保存。PUT 不热切换 listener，响应包含 `restartRequired`；运行配置来自进程启动快照。
- 通过 `internal/app` 注入组件和 routes，并把 GB lifecycle 接入 `lifecycleRuntime.Start`、`stopSequence` 与 startup rollback；迁移完成后读取已保存配置，校验成功且 enabled 才启动 SIP。SIP bind/listen 失败时关闭部分启动的 listener，保留 Web 管理面并暴露 runtime error，允许 UI 修正；停机时先停止 SIP 接入/会话，再关闭 camera/Native 依赖。
- 配置无效时拒绝保存：enabled 时校验 `sip_id`、可达的 `media_advertised_ip`、端口范围（至少一对端口）和 `standard` profile；禁用状态不绑定 5060。
- 增加受保护的设备预置/密码更新 API。Digest 密码只加密写入 `gb_devices.encrypted_password`，GET/日志/审计详情不得泄露明文。
- 扩展 `camera.key` 丢失保护：有任一 camera stream 密文或 GB 设备密码密文时都必须拒绝生成替代 key；新增仅含 GB 密文的测试。
- **可观测基线（新增）**：约定结构化日志关联字段 `device_id` / `channel_id` / `session_id` / `sip_call_id` / `transport` / `error_code`，
  建立 `snapshot.go` 快照骨架与 `GET /api/v1/gb28181/runtime`（此时大部分计数为 0）。

**验证**：`make go-check`；配置 Repository 覆盖默认单例行、更新/读回、事务失败；handler 覆盖 GET/PUT、422 校验、审计脱敏、保存后 `restartRequired=true` 且运行快照不变；重建 runtime 并从 DB 加载后 `restartRequired=false`；默认禁用不绑定 5060；listener bind 部分失败时释放已开的 socket、Web 管理 API 仍可读到 `state=error`；DB 迁移 up/down 与空地址设备预置；缺失 key 且仅有 GB 密文时启动失败；Fx graph 和 runtime rollback/stop 顺序测试；端口范围边界（1 对、奇数个）容量计算测试。

## S1 · SIP 注册、鉴权、在线状态与入站防护

**进度：已落地 UDP/TCP UAS 的 REGISTER + MESSAGE(Keepalive)、Digest、入站限速/尺寸上限、设备状态机与在线计数。**
落地时的四项决定（改前请先读，别当默认行为回退）：

1. **nonce 不做一次性消费**：UDP REGISTER 重传会逐字节重放同一个 Authorization，一次性 nonce 会让重传永远吃 401，
   设备随后放弃注册。nonce 改为 HMAC 签名 + TTL（默认 300 s），重放窗口即 TTL；过期为 401 + `stale=true`。
   这是「重放防护」在本协议下的正确形态，不是漏做。
2. **超长 UDP 报文只能丢弃，不能报错**：sipgo 把 read filter 返回的 error 当作传输层故障并**结束整个 UDP 读循环**，
   一个 20 KiB 报文足以让所有设备失联。因此 filter 返回「零字节」丢弃并计数（`OversizedDrops`）；
   TCP 的每报文上限由 `framedConn` 按 CRLF/Content-Length 跨 read 记账后断连，两者语义不同不可互换。
3. **TCP 连接数上限通过 listener 包装实现**：超限连接 Accepted 后立即关闭并计数，而不是让 `Accept` 返回错误
   （那会停掉整个 serve 循环）。
4. **抖动抑制落在日志与计数上**：状态机按设备缓存上一次状态，只有真实转移才 Info 级记账，keepalive 不发日志；
   上下线仍逐次落库，因为「离线」必须由 sweeper 的时钟判定而不是由日志策略决定。

**剩余待办（S1 未完成项）**：TCP flow 断连后的出站路由清理验证、UAC 客户端对象（`sipgo.Client`）随 S2 目录查询一起建立、
真实设备对端的 UDP/TCP 双栈联调。

- 选定并固定 `github.com/emiago/sipgo` 的精确版本；验证实际 UDP/TCP server、事务和 TCP connection routing API，不在计划中依赖未核对的 callback 名称。
- SIP inbound UAS 处理 REGISTER/MESSAGE；UDP 与 TCP listener 共用业务 handler。TCP 注册设备必须保留有效连接路由；断线后清理 flow，禁止只依赖持久化 `remote_addr` 假设 TCP 出站可达。
- **SIP 事务层显式化（新增）**：明确 UDP 重传定时器（T1/T2 与请求超时）、重复请求幂等处理、TCP 连接保活与空闲回收；
  这些是工业现场丢注册的主因，不能依赖库默认值而不写进验收。
- SIP UAC 的设备定向出站事务由同一信令层提供；Catalog MESSAGE 在 S2 使用。明确应用启动/停止顺序接入 `internal/app/runtime.go`，不单独绕过中央生命周期。
- `digest.go`：401 challenge、设备密码验证、nonce 过期/重放处理、`expires=0` 注销。设备必须在已预置 allowlist 内，否则拒绝注册。
- **入站防护（新增，P0 交付项）**：`ratelimit.go` 按来源限速 REGISTER 与出站事务；nonce 过期与重放窗口参数化并测试；
  SIP 报文大小上限（TCP 流必须显式设上限，不能靠 UDP datagram 兜底）；TCP 信令连接数上限与每设备活跃 flow 数上限。
- `xmldoc.go`：严格 UTF-8 验证后回退 GB18030；**禁止外部实体、限制嵌套深度与单文档大小**；
  记录原始测试夹具来源。没有厂商报文时只标注合成测试，不宣称厂商兼容。
- `device.go`：REGISTER refresh、Keepalive 和 expires 状态；启动时旧 online 状态先恢复为非在线，只有收到有效新 REGISTER/Keepalive 后更新；设备反复上下线要有抖动抑制，避免会话风暴。
- `GET /api/v1/gb28181/devices` 与预置/更新设备凭据接口；统一错误信封、审计和脱敏。

**验证**：表驱动覆盖 Digest 正确/错密码/过期与重放 nonce/`expires=0`/未预置设备；本地对端覆盖 UDP/TCP REGISTER、刷新和注销、TCP flow 断开；XML 覆盖合法 UTF-8、GB18030、无效编码和声明与实际字节不一致；**防护用例覆盖**超长报文头、深层嵌套 XML、外部实体（XXE）、超大 Catalog、REGISTER 突发限速、nonce 重放，全部断言无 panic、无挂死、有稳定错误码。

## S2 · 目录同步与通道入库

- `catalog.go`：向设备发出站 `MESSAGE` + `CmdType=Catalog`；事务关联 `SN`，处理 `SumNum` 分片、乱序、重复、丢片超时与重试策略；解析后 upsert `gb_channels`。
- **批量入库（新增）**：单设备 16 通道、100 设备（1600 条）目录同步走批量 upsert 并限速分片；
  同步期间管理 API 不得劣化到不可用（这是容量验收项，不是性能优化项）。
- API 路由使用复合键：`POST /devices/:deviceId/channels/:channelId/camera`；`camera_id` 仅在显式添加时回填。
- 前端 `features/gb28181` 提供设备列表、手工预置/更新凭据、通道抽屉和添加动作；系统设置导航新增 `/settings/gb28181`，文件路由只做页面声明并从 `features/gb28181/index.tsx` 引入配置页。TanStack Query 读取/更新服务端配置，保存后显示“需重启生效”，区分 desired、startup config 和 runtime state。

  **进度（S1 阶段已落地）**：配置表单（含 desired/startup 差异表与 `restartRequired` 横幅）、运行态面板（在线设备／活动会话／端口池／耗尽计数）、设备白名单表与预置/凭据轮换弹窗、`/settings/gb28181` 导航与路由声明。

  **仍缺（随 S2 backend 落地）**：通道抽屉与“添加为摄像机”动作 —— 依赖 `gb_channels` 的目录同步（S2 `catalog.go`）先存在，否则界面只能显示空抽屉。此处刻意不提前实现，不是漏做。
- 前端另需展示 `GET /api/v1/gb28181/runtime` 的关键计数（在线设备、活动会话、端口占用），作为现场排障入口。

**验证**：分片响应乱序/重复/丢片/超时、多个 device 下相同 channel_id、重复添加与删除关联测试；API 用 fake SIP peer 集成；1600 条目录批量入库压测并记录管理 API 时延；真实无头浏览器覆盖配置读取/保存、desired/startup 对比、保存后重启提示与 runtime error 状态，以及「设备预置 → 刷新目录 → 通道显式添加」页面交互。

## S3 · 原生输入源（离线可测，无真机）

**已定案的边界**（不再作为门禁项）：媒体端口**成对租用**，RTCP socket 由 Go 侧持有并读丢弃，**不进 ABI**；
ABI v4 只需承载 RTP fd。仍需在冻结前以标准正文与合成夹具核验的只剩：SDP 中 RTCP 的宣告形态、payload type 与 SSRC 协商。

- `native/include/Zhulong/engine.h`：门禁关闭后才追加 `Zhulong_gb28181_params` 与 `Zhulong_stream_acquire_gb28181`；仅尾部追加并同步 ABI 版本。
- `native/src/nodes/capture/rtp_ps_input.{hpp,cpp}`：按 PoC 结果实现选定 RTP 输入路径；若自定义 RTP 层，处理有界乱序、重复、丢包和序号回绕；优先 FFmpeg MPEG-PS demux，不凭猜测剥 FT 字段，不默认自研 PS parser。
- **资源上限（新增）**：RTP 重排窗口与转发队列必须有硬上限，超限丢包并计数，不允许随包增长（小盒 SoC 内存受限）。
- 自定义 `AVIOContext` read callback 必须轮询/限时并接受取消，确保 Engine stop 会唤醒读取线程并 join；Engine 复制 fd，原 fd 由 Go lease 持有。
- **为 C 级加密预留的读取假设（新增）**：有界读取与 AVIO 填充不得假设「输入流长度 == 原始包长度」，
  解密/变换后长度可变的实现要能接入；此假设只体现在代码结构与注释，不实现任何加密。
- `Stream` 增加 `source_key`，RTSP 继续传规范化 URI；GB 使用稳定的 SIP session ID，区分同 URI 的不同 dialog，同时允许同一 dialog 的 Preview/AI consumer 复用。
- `src/abi/engine.cpp` 导出；`CMakeLists.txt` 加源文件；`PreviewNormalizer` 不新增来源分支。

**验证**：
- 以真实 FFmpeg demuxer 跑合成 PS/RTP 输入，覆盖单包/分片、乱序/丢包/回绕、无 PS 头、错误 PT/SSRC，以及仅有证据时的 FT 扩展。
- 验证自定义读取的 idle timeout、取消唤醒、worker join 和 dup fd 关闭；无首包时 STARTING，超时后 FAILED。
- 重排窗口上限与队列上限：注入超限乱序包流，断言丢包计数增长且内存不增长。
- 断言相同 URI 不同 session 不复用、同 session 的不同 consumer 复用；验证 RTSP 既有去重/宽限期回归。
- C++ 改动后执行 `make native-build`、`make native-test`；按仓库接入合同清理旧 host 产物并核对新 ABI 符号。

## S4 · 端到端出画、容量与自愈

- `session.go`：按 SDP 门禁实现端口**成对** lease 和 SIP dialog；为避免媒体首包丢失，顺序固定为 bind → Native acquire → Go packet subscription/pump → UAC INVITE；处理 1xx、2xx+ACK、拒绝、超时/CANCEL、BYE。
- `internal/engine/gb28181.go`：CGO 封装 fd 生命周期；Engine dup，Go 保留原始 fd；停止时等待 Native worker join/完成确认后再关闭原 fd 与 RTCP socket、归还两个端口。
- Preview 与 AI 共用会话 lease/source key；最后一个消费者离开后仅由一个明确的 grace owner 计时。grace 内恢复复用 dialog；到期发 BYE 并立即停止 Native GB 输入，禁止 Engine 的第二个 8 秒 grace 造成 fd/端口悬挂。
- camera 域按 protocol 分流：预览、AI Broker、健康状态和诊断；GB 通道导入跳过 RTSP probe，RTSP 创建/更新行为不变。
- **自愈路径（新增）**：`netchange.go` 订阅 `internal/network` 变更（IP/DHCP）；
  时钟复用 `internal/systemtime` 状态，未同步或偏差超阈时拒绝点播并返回可诊断错误码；
  listener 运行期退出走有限次退避重建，仍失败则 `state=error`。
- 前端摄像机列表只显示已显式添加的 GB 通道；设备/通道删除需遵循 camera 关联解绑规则。

**验证**：先以本地 SIP UAS/UAC 对端 + 合成 RTP/PS 做端到端自动测试（含响应拒绝、INVITE 超时、ACK/BYE、无首包、断流、grace 内复用和 10 轮端口回收）；**容量验收**：并发 50 路建立并各自出画、第 51 路返回端口耗尽错误码、首帧 P95 抽样达标、1000 轮点播-断开后端口池回到基线；**自愈验收**：模拟 IP 变更、时钟跳变、设备断网 30 s 恢复、listener 退出，断言状态与错误码正确且 Web 管理面可用。唯一需要真实设备的门禁是至少一个厂商型号点播出画。设备断电转 offline，恢复后重新 REGISTER/Keepalive 并可再次点播。

## S5 · 稳定性收尾、互操作证据与文档

- 完成 grace 内复用与超时 teardown 的并发/失败回滚测试；宽限期内不发 BYE，Native 输入确认停止前不释放端口。
- **72 h soak（新增）**：50 路循环点播，跟踪 RSS、fd、goroutine/线程数、端口占用；要求 RSS 增长 < 5%、零泄漏、无累积。
- **互操作证据包（新增）**：型号×固件矩阵（至少 2 个型号）、设备抓包归档、标准条核验表（每条协议实现标注对应章节号或抓包文件名，无法核验的标为「未验证假设」）。
- `internal/httputil/locale.go` 补齐错误码与三语文案（含端口耗尽、时钟未同步、地址不可推导、限流等新增错误码）；更新 Swagger 并运行 `make api-docs`。
- 更新 spec：`.trellis/spec/native/ingestion-contract.md`（ABI v4、GB 输入、source key、Native stop/join 与 fd 所有权）、`.trellis/spec/native/cgo-contract.md`（fd 参数不涉及 Go 指针及调用期字符串所有权）、`.trellis/spec/backend/camera-guidelines.md`（协议分流、GB 添加路径、健康/AI 路径和 grace 语义）。
- 部署说明：SIP 5060 默认不需要 `CAP_NET_BIND_SERVICE`；仅配置低于 1024 的监听端口时需要相应权限。说明 SIP/媒体防火墙、`media.bind_address` 与 `media.advertised_ip` 的 NAT 配置。
  **新增**：`LimitNOFILE` ≥ 8192；端口范围与并发路数的换算表；**并发解码路数实测值**（与并发会话数分开表述）；
  RTP over TCP 不适用的前提（设备须与盒子同侧局域网）。
- `config.toml.example` 不含重复的 `[gb28181]` 配置；README、Swagger 和任务建表文档说明配置在系统设置 UI 持久化、保存后重启生效。

**验证**：`make check` 全绿；72 h soak 报告存档；`GET /api/v1/gb28181/runtime` 在生产形态下可用；`docs/` 更新；README 说明 SIP 和媒体端口、fd 与解码预算。目标板与目标网络 NAT 的可达性单独记录，host 单测不代替硬件/网络验证。

## 无真机条件下的验收策略（当前状态：无厂商设备）

现场没有 GB28181 设备可用，因此**真机门禁不能作为开发推进的前置条件**，但也**不得被悄悄跳过**。
替代方案分三层，每层都要写清覆盖了什么、没覆盖什么。

### 第一层：可替代的（已在 CI，合成夹具）

| 能力 | 覆盖方式 | 证据位置 |
| --- | --- | --- |
| REGISTER + Digest 双栈互通 | 用 sipgo **客户端**（含其自带的 `icholy/digest` 实现）对运行中的 ingress 走完整 401 → 认证 → 200 流程，UDP 与 TCP 各一条 | `sip_server_test.go` `TestPeerDeviceRegistersOverUDP/TCP` |
| 凭据真实来源 | 对端认证成功后轮换密码，同一次握手必须重新被拒（证明每次请求都从库里读密文） | 同上 |
| 来源伪造 | 已注册设备的 MESSAGE 来自其它 IP 时拒绝且不刷新 keepalive | `TestMessageFromUnexpectedSourceIsRejected` |
| nonce 生命周期 | 注入时钟：有效期内可重复使用（UDP 重传）、过期返回 `stale=true`、伪造签名被拒 | `digest_test.go` |
| 文档解析防护 | 尺寸/深度/DOCTYPE/字符集白名单，外加 `-fuzz` 目标（20 s 13 万次执行无 panic） | `xmldoc_test.go`、`xmldoc_fuzz_test.go` |
| TCP 分帧与上限 | 跨 read 拼接、多报文流水、超限断连；连接数上限拒绝而不中断 serve 循环 | `flow_test.go` |
| 状态机与超时 | 时钟注入覆盖 lease 过期、keepalive 静默、进程重启清理旧 online | `device_test.go` |
| 生命周期接缝 | 真实 ingress 经 `LifecycleManager` 启停，socket 释放 | `TestLifecycleStartsTheRealIngress` |

这套替代方案的价值在于：**协议实现互不共享代码**（对端用 sipgo + icholy/digest，服务端用我们自己的 digest/解析/状态机），
所以它抓得住「我们两边一起错」的一类 bug。

### 第二层：不能替代的（残余风险，必须在有设备时补）

1. **SDP 协商**：`m=` 行 RTCP 形态、payload type 取值、SSRC 归属、`y=`/`f=` 行语义。S3 之前一律标为「标准路径假设」。
2. **媒体面真实行为**：RTP 乱序/丢包/序号回绕、PS 封装差异、FT 扩展字段是否出现、关键帧间隔。
3. **设备侧 keepalive 与注册节奏**：周期值、`Expires` 与 Contact `expires` 的优先级、断网重连退避。
4. **非标实现**：厂商自定义 XML 字段、GB18030 变体、大小写与空白差异、只支持 UDP 或只支持 TCP。
5. **真实时序**：UDP 重传与我们的响应竞争、TCP 半开连接、NAT rebinding 的具体表现。

### 第三层：把现场第一台设备变成夹具（不阻塞开发）

- 首台设备接入时，**先抓包再调参**：`tcpdump -i any -s0 -w gb-first.pcap port 5060`，把 REGISTER / Keepalive / Catalog / INVITE 的报文逐条归档到任务目录，作为后续回归夹具来源（脱敏后）。
- 报文转夹具流程：抓包 → 提取 SIP 消息体 → 落成 `internal/gb28181/testdata/` 下的 golden 文件 → 表格测试标注「真实设备 <型号>/<固件>」。
- 诊断入口先于兼容性声明：`GET /api/v1/gb28181/runtime` 已暴露在线设备数/端口占用；S2 补目录查询计数，S4 补首帧耗时与失败原因分布。
  **在拿到真实抓包之前，任何文档/界面不得声称「支持某厂商」。**

### 门禁改写

原「S4 真机出画」门禁改为：**发布前置条件**（不是开发前置条件）。开发/S1–S3 以第一层替代方案推进；
首次真机联调时按第二层清单逐项核销，并把结论写回本文件的「互操作证据包」。

## 进入实现前/阶段门禁

- **配置生效状态**：P0 `gb28181_configs` 为唯一运行配置来源。UI 保存目标配置后不修改本进程 startup snapshot；重启时从 SQLite 读取最新配置，成功启动后 `restartRequired` 清零。
- **S1**：固定 sipgo 精确版本并证明 TCP 注册连接可用于出站 Catalog/INVITE 请求。
- **S3 ABI 冻结前**：以标准正文和合成报文夹具确认 SDP 的 RTCP 宣告形态、payload type 与 SSRC；
  RTCP 端口策略与「RTCP 不进 ABI」已定案，无需再决策。有目标设备抓包时纳入验证；没有真机时记录为标准路径假设，不声称厂商兼容。
- **S4 真机验收前**：确定首批设备型号、固件和网络拓扑（直连/NAT），记录目标设备 SIP 和 RTP 抓包及预处理/端口条件；确认设备是否全部位于盒子同侧局域网（RTP over TCP 前提）。
- **容量承诺门禁**：任何对外文档/界面在给出「并发解码 N 路」之前必须附目标板实测数据；会话数与解码数不得混用。
- 如果 RTP over TCP（因出现外侧 NAT 设备）必须进范围，将其拆成独立子任务并重新评估跨平台 fd 与 RFC 4571 封装，
  不在本任务 S4 内隐式扩张。
