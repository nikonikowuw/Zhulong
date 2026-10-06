# 摄像机业务与四态管理：技术设计（进行中）

状态：规划草案，未通过启动审阅。产品语义调整已获批准；依赖最终选型、安全兼容合同与跨层合同复核尚未完成。

## 1. 决策依据

用户选择采用社区最佳实践。采用分级检查、区分控制面与媒体证据、超时与有界并发、退避与错峰等成熟工程原则。不声称存在适用于所有 NVR 的统一空闲探测协议。参考 research/health-check-strategy.md 中来源及其局限。

## 2. 分级健康检查

- 有活跃媒体会话：使用 Native/桥接可提供的收包时间与故障信息，不注册仅用于探活的包订阅，不引入无人预览的额外逐包 CGO 回调。实现前核查当前 Status 能力；缺少证据时明确补充接口范围，不把已连接误认为持续出流。
- 无消费者：通过 RTSP DESCRIBE 完成必要鉴权并验证视频 SDP，随后关闭控制连接。不 SETUP、不 PLAY、不收 RTP、不解码。
- 入库、修改连接配置及显式诊断：走深度探测。现有 FFmpeg Probe 不直接作为轻量检查实现，必须先核查其网络行为。
- OPTIONS、TCP、Ping 仅作为诊断证据，不作为默认 URL 级健康判定的替代。
- 控制面成功不能覆盖已知的媒体超时；后者需真实媒体恢复或明确配置代次变化后重新评估。

## 3. 调度默认值（工程初值，不是行业标准）

- 健康空闲源：60 秒周期，±20% 抖动；单次探测总预算 5 秒，包含连接、鉴权及响应读取。
- 默认最多 4 个并发空闲探测任务；排队不得创建无界 goroutine，调度容量与可配置设备规模需在完整设计中确定。
- 普通网络失败：连续 3 次失败后判离线，期间保留失败计数与最近尝试结果；不得刷新最近成功时间。检测延迟约为分钟级，不等同于活跃流的 5 秒断流目标。
- 已完成鉴权重试后的明确拒绝：立即报告认证错误，不按普通网络抖动掩盖；首次 401 challenge 不是认证失败。错误源采用低频复查，配置修改立即重置检查代次；具体复查周期待完整设计统一。
- 活跃媒体重连：沿用父需求指数退避与抖动，上限 60 秒；不得因空闲健康检查另开重复媒体会话。
- 调度按规范化物理源和凭据身份去重，不把共享 IP 的主子流或不同凭据混为同一流。
- 停用、删除、服务关闭取消任务；结果携带配置代次，禁止过期结果写回。

## 4. 状态证据

启停配置、健康与媒体会话状态分离。健康返回最近检查时间、最近成功时间和证据类型（控制面/媒体）；过期证据不能永久显示为当前在线。启动恢复时持久化的 online 不能直接当成新鲜结果。

用户已批准用第 9 节独立健康/会话状态替代原单一四态；初次未检查为 unknown，不将历史在线当成当前证据。

## 5. 风险与验证

- 有设备在 SETUP/PLAY 才校验鉴权；DESCRIBE 成功不是可播放承诺。
- 响应读取必须有长度上限和总超时；重定向需限制次数并防止跨来源泄漏凭据。
- 通过 RTSP 测试桩统计请求序列，断言空闲健康检查没有 SETUP/PLAY。
- 模拟 401 challenge、最终鉴权失败、404、畸形/超大 SDP、超时、短暂抖动、停用与改配置竞争。
- Native 证据边界涉及 C 内存时，C 分配与释放；Go 只在允许的调用期借用或复制，具体接口依实现核查。

## 6. 当前规划门禁

业务、密钥、HTTP/SSE、停机与多协议预留已形成下文草案；implement.md 与上下文清单已存在。剩余：RTSP 鉴权替代方案与最终依赖选型、安全兼容合同收敛、Native 锁序与代次合同复核及整体启动审阅；父需求语义变更已批准。上下文路径校验通过不等于规划或代码验收通过。

## 7. 业务与持久化方案（待统一审阅）

- `internal/camera` 内聚 handler/service/store、健康调度与事件广播，业务构造函数不导入 Fx、不启动后台任务；`internal/app` 负责注入与生命周期。
- `cameras` 保存 id、name、enabled、revision、UTC 时间；`camera_streams` 保存 camera_id、role(main/sub)、连接密文、transport 与已验证元数据，(camera_id, role) 唯一，外键级联删除。健康为运行时状态，不按探活频率写 SQLite。
- 主流必填，子流可选；若提交子流则必须通过验证，失败整笔配置不生效，不静默忽略子流。仅名称修改不重复探测；连接字段变化才触发门禁。
- 主/子流并行探测共享一次 5 秒预算；网络操作在事务外，成功后使用 revision 条件更新提交短事务。旧 revision 返回 409，不能覆盖新配置；删除后迟到的探测不能重建对象。
- 当前 Native Probe 不保证真实 FPS 总能获取。用户已批准未知 FPS 保留 null/unknown 并允许入库，展示不伪造；编码与分辨率必须有效。父 PRD 已同步取消实际 FPS 必须可得的限制。
- 结构化输入优先；完整 URL 的歧义不得凭最后一个 @ 无条件猜测（路径/查询也可含 @）。确定性解析失败返回字段错误并要求分字段输入。已编码凭据只解码/编码一次，测试 IPv6、百分号、特殊字符和查询语义。

## 8. 凭据与密钥

- 默认 `<data-dir>/camera.key`，32 字节 CSPRNG，AES-256-GCM；每次加密使用随机 nonce，AAD 绑定密文格式版本、camera id 与流角色。连接材料整体加密，避免用户名、userinfo 或查询 token 散落数据库。
- 首次无加密数据时创建密钥，权限 0600、数据目录限制访问；原子排他创建并保证完整写入，拒绝符号链接/非普通文件与不安全权限。已有密文而密钥缺失/损坏时启动失败，不自动再生成。
- 备份必须包含一致的数据库备份及密钥；同目录密钥只能防止单独数据库泄漏，不能防止整个数据目录或 root 被攻破。首版不提供在线密钥轮换，后续需单独规划重加密事务与恢复。
- 前端业务要求完整显示 RTSP 流地址用于播放与配置展示，不进行脱敏遮蔽；已认证会话可通过列表与详情接口获取完整 RTSP 地址。SSE 不返回流凭据；详情与凭据接口设置 Cache-Control: no-store。日志只写 camera id、角色、稳定错误码；不输出完整源 URL、第三方原始错误或请求体。
- 仅接受 rtsp scheme，拒绝文件/其他协议和控制字符；允许部署所需局域网源，不擅自禁止 RFC1918。访问环回、链路本地及重定向的安全策略需作为 RTSP 依赖评估的一部分，默认不自动跟随跨源重定向。

## 9. 状态模型与聚合

用户已批准以独立健康与会话状态替代父需求中的单一四态，不同时保留另一套含糊摘要枚举，避免 offline 同时承担“未知/未启用/网络失败”的含义：

- `enabled`：用户配置。
- 每个流 `health.status = unknown | online | offline | error`，附 checkedAt、lastSuccessAt、evidence、reason、consecutiveFailures、stale。
- 每个流 `session.state = idle | starting | running | reconnecting | error`。
- 摄像机摘要优先主流健康；额外 `degraded=true` 表示配置的子流异常，不让正常主流掩盖子流错误。API 同时返回主子流各自状态。
- 无消费者：session=idle，health 仍可 online。新启动尚未检查或停用：health=unknown，reason 分别为 pending_check/disabled。
- 空闲健康成功超过 240 秒未刷新则 stale=true，不向调用方承诺新鲜在线；进入 unknown，保留历史 lastSuccessAt。调度过载不能当摄像机离线。
- 连续失败阈值仅适用于已有正常证据的暂态网络故障；首次探测没有成功证据时维持 unknown 并暴露失败原因，达到阈值后 offline。
- 活跃媒体失败立即反映 session=reconnecting/error，不等待空闲三次阈值；有视频证据后才确认媒体恢复。

以上语义拆分已获用户批准并同步父 PRD；旧枚举与新模型并非完全等价。

## 10. HTTP 与 SSE 契约草案

- GET/POST `/api/v1/cameras`，GET/PUT/DELETE `/api/v1/cameras/:id`，POST `/api/v1/cameras/:id/diagnose`，GET `/api/v1/cameras/events`。静态 events 路径与参数路由冲突需在 Gin 路由测试验证。
- 全部使用现有 Cookie Session 认证。创建返回 201；更新/删除成功返回 200 标准 JSON 信封，不用 204 破坏三字段合同。PUT 提交 revision，冲突返回 409。
- 网络探测在 HTTP 请求上下文内异步执行但请求等待结果，不新增持久化任务 API；探测成功才提交配置。前端将来用请求 loading 表示进度。
- 上游鉴权失败用 422 CAMERA_AUTH_FAILED，不能返回本系统 401 导致管理员被登出；连接超时 422 CAMERA_CONNECT_TIMEOUT、能力不支持 422 CAMERA_UNSUPPORTED、忙 429 CAMERA_BUSY、未找到 404 CAMERA_NOT_FOUND。错误统一本地化，不含原始输入。
- SSE 返回 text/event-stream，不套 JSON 响应体信封；每条 data 使用明确事件 DTO，snapshot/change/reset 与进程内单调事件序号。每次重连发送完整快照，不承诺持久化事件回放。
- 在同一状态锁下注册订阅并取得快照/序号，避免快照与增量间隙。每连接有界队列（32），溢出关闭连接让客户端重建快照；15 秒 heartbeat、单次写入超时、默认最多 16 个 SSE 连接。
- 长连接定期重验证会话，登出/过期后关闭；不在 URL 传 Session token。停机先通知 SSE 退出，再执行 HTTP Shutdown，防止无限流阻止排空。

## 11. 调度容量与关闭

- 初始摄像机上限 128，每台最多两路；单次列表分页上限 100。超过上限拒绝新增，不能默默丢弃检查。
- 单调时钟调度一个到期队列，每物理源最多一个待执行和一个运行记录，4 个固定 worker，不按 tick 为每路创建 goroutine。以公平队列避免故障源饿死正常源。
- 256 个独立源全部 5 秒超时的最坏巡检需约 320 秒，超过 60 秒周期和 240 秒 freshness；必须暴露 overdue/stale 而不是保证不存在的 SLA。容量/并发是部署可调整项，验收必须覆盖过载。
- 认证错误 5 分钟低频复查；普通离线源 60 秒±20% 继续检查，避免高频失败握手。手动诊断限流且有全局并发准入，与后台优先级分离。
- 启动：数据库迁移→密钥加载/密文校验→Native→摄像机后台→HTTP；任一步失败逆序释放。
- 关闭：关闭新业务准入、取消 SSE/探测→HTTP 排空→停止并等待摄像机调度/消费者→Native Close→DB Close→日志同步。后台使用独立应用 context，不持有 OnStart 的临时 context。

## 12. Native 扩展与未决依赖门禁

仓库实证见 research/repository-boundaries.md。最小补充方向：独立健康查询返回单调时间计算的最近视频包 age 和已收包标志；共享失败物理流具备受控重启能力，不销毁整个 Engine、不破坏其它源、不产生重复连接。采用独立新增 ABI 而非盲改旧结构布局；重启按物理源串行、使用代次保护并明确原订阅的终止/重新订阅行为。

Native 范围已获用户批准；接口/代次提案见第 13 节。仍须完成合同复核、RTSP 客户端固定版本/鉴权/大小限制核查，不能跳过门禁启动任务。既存 engine.hpp 修改保持不动。

## 13. Native 健康与受控重启合同提案

以下名称是拟新增接口，不是已存在 API；最终须经纯 C ABI 与并发审查。

### 13.1 健康查询

- 拟新增 `Zhulong_stream_get_health`，旧 `Zhulong_stream_status` 保持布局和含义不变。新输出结构采用固定宽度标量，入参含调用方输出大小，校验后写入；无借用指针。
- 输出：stream state/error、capture generation、has_video_packet、last_video_packet_age_ms。generation 从 1 起，每次实际启动新 worker 增加；溢出拒绝启动，不能回绕。
- age 由 Native steady clock 计算，has_video_packet=false 时 age 无效，不传 Native 时钟 epoch 给 Go 当 UTC。Go 只将查询时刻作为 checkedAt，并显式标注 age 为快照。
- 选定视频轨道非空包到达时，在 Native 中更新证据，发生于 dispatch 之前，且与订阅数量无关；不因此新增回调或持有 packet 内存。
- 开始新代次即清空收包证据；FAILED 时即便有历史包也不能返回健康在线。
- 查询是短时内存快照，不新增消费者引用、不刷新宽限期、不做网络 IO。活跃物理流采用每 250ms 控制轮询，业务以包 age 阈值判断异常；具体阈值、Native FAILED 后才允许重启的约束及 5 秒验收口径见第 17 节。

### 13.2 受控重启

- 拟新增 `Zhulong_stream_restart`，入参包含 stream id、当前消费者 id、expected capture generation，输出新的 generation。只允许仍有有效消费者的 FAILED 流；无消费者时不重启，健康调度本身不 acquire 虚构引用。
- 保持 stream id 和已有消费者引用不变，以新 capture generation 区分连接。检查代次失败返回冲突；STARTING/RUNNING 不重复重启。并发同代次请求最多一个成功，其余重新查询，不连续启动多个 worker。
- 旧订阅必须先完成 unsubscribe/drain，且进行中的 drain 也算未排空；否则返回 BUSY，不自动悄悄保留旧 token。Native 在排空计数归零前拒绝 restart 和新代次订阅。
- Go 摄像机流协调器按物理源串行执行：标记失败并停旧订阅→等待 Close 完成→确认需求仍存在→按退避到期 restart→首包后宣布媒体恢复→通知消费者重新建立订阅。旧 Subscription 对象保持终态，绝不复活；所有受管理的预览/录像/AI 需求经过同一协调器，不直接散落 Acquire。
- 采用独立的代次校验订阅入口（拟名 Zhulong_stream_subscribe_generation），旧 ABI 保留。新入口在 Native 同一临界区完成 expected capture generation 校验与订阅注册；代次不匹配返回冲突，不注册 token。Go 新协调器只走新入口，不能用“先查后订阅”规避 Native TOCTOU。此为已批准重启范围内的配套合同提案，仍需统一审阅。
- 重启先在池锁下设置该物理源 restarting 占位，不新增长持有 transition 锁；释放全局池锁后 join 已失败 worker。join 完成后重新检查 engine running、消费者存在和流未退休，再清空 cancellation、重置证据、启动新 worker。停止和退休优先，不能在 Engine Stop 之后复活。锁序详见第 16 节。
- join/drain 不持有全局 control 锁、stream 状态锁或 Go 回调需要的锁。池锁占位与 Engine Stop 的协同需以测试验证；Stop 必须等待所有在途 restart 控制操作退出才释放 Engine。
- 新 worker 创建失败仍保留 FAILED 状态和安全错误，不留下 STARTING 假象；下一次调度可在退避后重试。重启成功仅表示启动成功，不表示 RTSP 已握手或视频已恢复。
- 新调用沿用禁止回调重入、异常隔离、输出失败时清零的规则。任何 ctx 超时都不能使调用方提前释放仍被 C 使用的输出内存。

### 13.3 内存所有权与兼容性

- 健康输出由调用方分配和回收，Native 仅同步写入、不留存指针；Go wrapper 返回值拷贝。
- packet 仍由 C++ 分配和释放；Go 保留数据必须复制。旧 cgo.Handle 仅在 unsubscribe 同步 drain 成功后删除。
- 现有 Status/Acquire/Release 的默认语义不变，受控 restart 是显式新增行为，不自动绕过所有失败流的宽限期。
- 将新查询与重启适配纳入 `internal/engine`，保持 Engine 生命周期代次与 capture generation 是两个不同概念。

### 13.4 必需并发测试

1. 无订阅仍有媒体健康证据、CGO 包回调计数为零；首包前 has=false。
2. 多消费者同时请求同代次重启，只启动一个新 RTSP 连接；另一摄像机继续收包。
3. 旧回调阻塞、unsubscribe 从 map 移除但未 drain 完成时，restart 必须 BUSY；token 不能提前删除。
4. restart 与最后消费者释放、Stop、退休竞争，不复活、不死锁、不重复 join。
5. 新代次首包前清除旧健康证据；旧订阅/队列不能混入新代次数据。
6. 注入 worker 创建失败及订阅重建失败，错误可见、引用可回收，后续受控重试不泄漏。

## 14. 多协议接入预留（用户新增要求）

本节修订前文“所有摄像机等于 RTSP 源”的假设。仅预留 GB/T 28181，当前不引入 SIP/RTP 服务或 ZLMediaKit 依赖，不宣称已有国标能力。具体标准版本（如 2016/2022）和厂商扩展在未来接入任务确认。

### 14.1 身份与配置

- Camera 表示用户管理的逻辑视频通道，不代表一台只能提供单路视频的物理设备；未来独立 Device/接入域管理注册设备及其多个通道，不将 SIP 注册状态复制为所有通道的媒体健康。
- CameraStream 使用稳定 source id + protocol + adapter 管理的身份引用；RTSP URL 和传输选项属于类型化 RTSP 配置，不是所有协议共有的必填字段。当前数据库迁移创建实际使用的公共字段及 RTSP 配置，不预建大量无用 GB 表。
- 对外请求采用带 protocol 判别的类型化配置；首版只接受 rtsp，gb28181 返回明确 unsupported protocol 错误。不能接受任意 JSON 后声称预留已完成。
- RTSP 去重仍按现有规范化 URL（包括凭据身份和路径）及配置冲突规则；未来 GB 稳定身份由接入域/设备/通道/流选择构成。动态 SSRC、RTP 端口、Call-ID、临时中转地址属于会话，不可用作数据库主身份。
- main/sub 是当前 RTSP 产品配置能力；未来 GB 的码流选择需通过能力声明适配，不假设所有设备都能提供两个固定 URL。

### 14.2 控制与媒体边界

- 通用业务协调消费者需求、访问权限、引用计数和会话代次；协议 adapter 负责身份解析、配置验证、健康证据与启动/停止协商。不把 DESCRIBE/restart RTSP worker 放进通用 CameraService。
- RTSP adapter 使用本次 Native 健康/重启扩展；未来 GB adapter 管理 SIP 会话及媒体接收资源。统一的 Release 表达释放消费需求，不强制所有 adapter 都发送 RTSP TEARDOWN。
- 健康证据支持主动探测和事件驱动更新。RTSP 空闲用 DESCRIBE；未来 GB 通过注册有效期、Keepalive、通道状态等控制面证据更新，不能单凭设备心跳断言所有通道视频健康。媒体健康继续由有效视频数据与超时判断。
- “创建/修改时 5 秒 Probe”只适用于本期手工 RTSP 配置。未来 GB 设备注册、目录发现、通道授权与实际拉流验证分阶段，不因未主动 INVITE 就拒绝发现记录。
- GB 常见媒体路径为协商后的 RTP/PS，再进行 RTP 重组和 PS 解复用，不能把 RTP payload 直接当 H.264/H.265 Packet。未来要支持会话结束、端口回收、迟到包/SSRC 映射与代次隔离。
- 统一媒体汇合点是附 codec/参数集、PTS/DTS/timebase、代次与明确所有权的压缩视频包/轨道，不是强行把 GB 改写为 RTSP URL。具体 Native 输入适配 ABI 后续独立设计，本期不空造可用的 PS 接口。
- 后续可以评估内嵌接收器或独立媒体服务；目前不选型，不悄悄改变单二进制部署约束。若采用中转，其临时 RTSP 地址仅为 adapter 内部实现细节。

### 14.3 最小验收

- 通过无 URL 的假源 adapter 测试通用源标识与健康更新，生产仅注册 RTSP adapter。
- 未实现协议请求明确失败、不落库、不启动监听端口；列表/SSE 可表达协议和证据类型，且不泄露协议凭据。
- 验证 RTSP 的 URI 规则仅在 RTSP 配置分支执行；共享引用、配置代次和停机管理不依赖字符串 rtsp:// 判断。
- 未来接入不要求改写 camera id 或消费者 API，但允许新增协议配置表/迁移；不承诺零迁移扩展。

## 15. RTSP 客户端核查进度

固定候选 gortsplib/v4 v4.16.2 的静态源码核查已完成指定范围；高层 Client 会自动 OPTIONS 和跟随重定向，不直接用于本项目轻量探活。低层 Sender 忽略 qop、不回传 opaque，不能直接作为完整 Digest 支持；header 缺少总字节上限，SDP 缺少复杂度配额。证据见 research/rtsp-client-selection.md。

父会话结论：暂不批准该候选作为最终鉴权依赖，也不默认批准研究报告提出的设备兼容性缩减。优先继续研究成熟的鉴权依赖或其它客户端低层能力，避免深度 Probe 成功而空闲检查因更窄的鉴权能力失败。不能临时自写 Digest 或用深度拉流替代空闲检查来绕过选型门禁。

保留为适配器设计方向：一次绝对 5 秒预算、全部重定向拒绝、有限鉴权挑战与 stale 重试、原始 header/body 大小限制、SDP 复杂度预检、严格 CSeq 校验；具体配额仍须整体审阅。不支持的鉴权变体明确归为能力/协商错误，不能报密码错误或静默降级。Basic 明文风险及源地址访问策略仍待安全合同收敛；研究报告建议的环回/链路本地限制尚非已批准需求。

鉴权替代研究已收敛为推荐 `github.com/icholy/digest v1.2.0` 的独立低层 API（不使用 HTTP Transport），见 research/rtsp-auth-alternatives.md。低层 Options.Method/URI 可直接接收 DESCRIBE 与包含 query 的绝对 RTSP 请求 URI，核心计算已有 qop=auth、MD5/SHA-256 与普通 opaque 支持；不再新增第三个候选。

用户已回复“yes”批准的最小适配路线（仅技术路线，不是实现批准）：摘要计算全部交给该依赖；项目协议适配层保留原始参数存在性、规范化参数名与 qop token 空白、拒绝重复/含糊挑战并显式选择算法。对于存在但为空的 opaque，库序列化会省略字段，可在确认输出尚无 opaque 后仅补固定文本 `, opaque=""`；这不包含外部输入拼接，也不改变 A1/A2/response 算法。需以空/缺失/非空 opaque、大小写参数、quoted-pair 与防降级测试验证。此处理方案已获用户技术路线批准，尚未实现或测试；不假设未来一定有上游修复，也不自动批准 fork 或设备兼容性缩减。若不能接受该适配责任，再考虑已有实证但构建成本较高的 libcurl 路线。

调用方用标准 CSPRNG 显式生成 cnonce 并处理错误，避免依赖默认随机失败 panic 路径；认证 URI 与请求行使用同一最终字符串。sess 等未支持算法明确归类为能力错误，不冒充密码错误；兼容性范围仍需整体审阅，不宣称所有摄像机均支持。

适配路线已批准。尚未完成：模块来源/校验链与最终 pin；报告中的 OSV/公开公告查询未返回条目仅为当次快照。桩测试与真实设备验证属于实施验收，本次没有运行。

## 16. Native 控制操作线性化方案（规划复核）

选择池锁保护的操作占位，避免添加会与 Stop 形成反向获取顺序的第二把长持有锁：

- `control` 保护运行状态、消费者、retiring/restarting 标记及在途 restart 计数；确需同时读取状态时顺序固定为 control→stream mutex。worker 仅拿 stream mutex，从不反向取 control。
- restart 在 control 下核验 FAILED/消费者/代次/无订阅及 drain 计数，设置 restarting 并计入在途操作；释放所有锁后 join。重新取 control 后检查 running、retiring、消费者，再在 stream mutex 下更新代次/证据并启动 worker。退出的所有异常路径均清除占位、递减计数和通知条件变量。
- restarting 期间同源 restart、subscribe 返回 BUSY；release 可移除消费者，导致 restart 提交阶段取消启动。acquire 同源可返回 BUSY 让协调器重新调度，不持锁阻塞；其它源操作正常进行。
- Reaper 在 control 下设置 retiring 才获得 join 所有权；跳过 restarting 源。acquire 看到 retiring 必须等待退休完成，不能凭 expiry 刷新把已退休 worker 复活。
- Stop 先在 control 下设置 running=false 并请求取消、唤醒等待者，条件变量等待在途 restart 清零时释放 control。之后按现有顺序等待 Reaper 并接管剩余 worker。Stop 不与 restart/reaper 对同一 worker 并发 join。
- unsubscribe 在 control/stream 锁下移除记录并增加 draining 计数，锁外 disable_and_drain；排空完成后重新获取 control 更新计数。仅 map 为空不能证明已排空。
- 旧订阅入口同样检查 restarting/retiring，不能绕过新入口的占位保护；旧接口不具备 expected generation 校验，旧调用方不得跨重启复用订阅。
- Go wrapper 不在 callback 调用 restart，不持有回调需要的 Go 互斥锁执行 drain/join。Engine.Close 必须等待所有已登记 C 控制调用结束，不能只靠 Go context 超时返回。

以上是父会话依据现有 acquire/reap/stop/subscribe 路径的规划复核，不是独立审查或竞态测试结论；实施时以阻塞回调、并发 Stop/release/restart 的测试验证。

## 17. 已批准产品语义差异

1. 单一四态改为 enabled + health + session；无消费者允许 online，未知与过期不伪装 offline。
2. 主流必填、子流可选；填写子流则整笔配置一起验证，失败不部分入库。摄像机摘要取主流，并标记子流 degraded。
3. FPS 未提供时允许入库并明确 unknown，不猜测；编码/分辨率仍必须有效。父 PRD 已同步更新。
4. 密钥和数据库必须一起备份，已有密文但密钥不可用时拒绝启动，不自动换钥。
5. 活跃流检测建议以最后视频包 age≥4秒触发业务 reconnecting，每250ms检查一轮，在受控测试环境验证≤5秒通知；Native默认5秒idle不变，Native未FAILED前不执行restart。低于0.25fps源可能被误判，需允许配置更大阈值，配置后不承诺5秒检测。该监控独立于空闲60秒探活。

用户对上述统一审阅明确回复“可以”，产品语义已批准；此确认不包含启动实现，RTSP 依赖核查仍未完成。

## 18. 源码复核后的并发合同修订（待整体启动审阅）

本节收敛 research/planning-gates.md 的 N1–N8；与第 13/16 节概括不一致时以本节为准。属于规划修正，未实施、未运行竞态测试，不把源码推导标成已复现故障。既存 engine.hpp 修改仍禁止覆盖；实际实施触及该文件前须与用户协调保留其修改。

1. **唯一 join 所有者**：control 下互斥取得 restarting/retiring；只有占位者可 join/替换 worker。Reaper 同临界区选择并标记 retiring，跳过 restart/drain。restart 全程持强引用和占位；提交时重新校验发起 consumer_id 本身仍有效。restarting 时同源 acquire 返回 BUSY，避免消费者 ID 复用 ABA。FAILED 与 shared_ptr 都不代表可并发 join。
2. **按流与消费者追踪 drain**：删除订阅 map 记录前，在同一临界区准备好 draining_total 与 consumer drain 记录；记账失败不得先丢记录。该消费者 release/重新 subscribe 在 drain 完成前返回 BUSY；其它无订阅消费者仍可 release。Reaper 仅退休消费者为空且无 drain 的流。完成注销不持 Subscription 锁反向取 control。
3. **Native 操作存活期**：所有解锁后仍会访问 Engine 的已准入操作（restart、unsubscribe 完成记账、等待退休的 acquire 等）均用 RAII 登记。Stop 先关闭准入、取消和唤醒，再在释放 control 的等待中允许操作完成；随后等待 Reaper、接管剩余 worker。注销须为操作最后一次访问 Engine 的同步阶段。Destroy 仍要求调用方与所有 API 外部串行化，内部计数不能保护尚未进入注册点的裸指针调用。
4. **Go 句柄借用屏障**：e.mu 下原子完成准入/代次检查及在途登记，覆盖现有 Acquire/status/subscribe/unsubscribe/release 和新增接口；借用直到 C 返回及迟到结果回滚、token/对象发布结束才归还。Stop/Close 先关业务准入并发送 Native 取消，不能在发送 Stop 前无条件等调用清零；所有借用归还与 Native Stop 完成后才能 destroy 或开启下一代。内部 cleanup 有明确通路，等待不持 e.mu/Stream.mu/回调所需锁。私有 Probe 的 stop 快照也必须 pin，Probe destroy 等快照借用归还。
5. **token 完成证明**：Done 只表示逻辑终态。仅同步 unsubscribe 成功、共享的同次 drain 完成结果或对应 Engine 代次的 Native Stop-completed 屏障允许删除 cgo.Handle；NOT_FOUND/NOT_RUNNING 单独不构成证明。Close 持久保存共享完成结果与错误，失败不能经 sync.Once 第二次调用变成假成功；不能忽略 sub.Close 错误继续释放消费者。保留最终可清理路径。
6. **generation 提交点**：旧 worker 包括 FFmpeg 析构已 join 后才能 reset cancel。候选 G+1 在线程成功创建并安装后提交；控制/状态锁或短启动屏障阻止新 worker 先发布旧代次证据。创建失败保持 G、FAILED、stopped=true、has=false、输出代次 0，保留流与消费者，清理占位后允许同 expected G 重试。溢出在改变状态前拒绝。
7. **sized output**：health v1 只接受非空输出且 out_size 恰等于当前目标 ABI 的结构大小；无效尺寸不写任何字节。合法形状先置失败零值，再于成功写快照；不按不可信 out_size memset。结构固定宽度标量和 reserved，C/C++ 布局及 canary 验证；旧 status 布局和错误 sentinel 不变。输出由调用方分配/回收，Native 同步借用、不保留；packet 由 C++ 分配/释放，Go 留存必须复制。
8. **旧 ABI 行为与新冲突码**：保持旧签名、布局、错误码值及普通路径/8 秒宽限期；明确新增重启/退休/排空窗口的 BUSY 行为，不再宣称零行为变化。拟新增 `Zhulong_ERR_GENERATION_CONFLICT = -14`（本轮核对头文件未占用，实施前再核对），Go 映射为独立代次冲突。新控制入口优先级：参数/输出形状校验 → CALLBACK_CONTEXT → NOT_RUNNING → 对象/消费者 NOT_FOUND → 占位/drain BUSY → generation conflict → 操作状态条件；非 FAILED 的 restart 返回 BUSY。冲突不注册 token/不增加引用；旧 subscribe 共享安全检查但不提供代次保证。

测试采用可达时序：active callback 阻塞时单测 RUNNING/drain/Stop；FAILED 重启的 drain 屏障暂停在 callback 已返回但 unsubscribe 完成记账尚未结束，不假设同步 worker 自然同时 FAILED 且卡在 callback。重启首包由无订阅健康查询确认，不等待尚未重建的订阅回调。

上述旧 wrapper 生命周期修正与本次重启安全直接相关，纳入整体范围审阅；不扩展到播放器/无关队列重构。
