# 轻量 RTSP 依赖核查

## 1. 结论与证据等级

**建议：gortsplib/v4 v4.16.2 可以作为“明确受限的低层 DESCRIBE 适配器”候选，不可直接当作完整、安全、有界的 RTSP 客户端使用，也不可宣称支持完整 Digest。** 本次已完成指定范围的静态源码核查；不是执行测试、安全审计或启动批准。

- 推荐复用 `pkg/base` 的请求/响应表示、`pkg/auth.Sender` 的已支持鉴权计算以及 `pkg/sdp.SessionDescription` 的 SDP 语法解析。项目适配器必须先做有界帧读取、原始鉴权挑战白名单检查，再委托库解析/计算。
- 认证范围仅可先承诺：Basic（明文 RTSP 安全风险须明确接受）、**没有 qop 且没有 opaque 的**旧式 MD5 Digest（algorithm 缺省或 MD5），以及同样无 qop/opaque 的 SHA-256 计算变体。最后一项是源码实现事实，不等于符合 RFC 7616 的完整 SHA-256 Digest。
- `qop=auth`、`auth-int`、任意 qop 字段、opaque（包括空值）、`*-sess`、SHA-512-256/其他算法以及尚未审阅的扩展不得默默退化为旧式 Digest；返回“认证能力不支持”，不是“密码错误”。
- 如果产品必须支持带 qop/opaque 的常见摄像机、严格的 RFC Digest、需要转义的 Digest 用户名，或不能接受下文严格兼容边界，**替代客户端/成熟鉴权依赖仍需另行研究**；本次不提供未经核查的替代库名称，也不建议临时自写 Digest。
- 高层 `Client.Describe` 不选用：自动 OPTIONS、重定向复制凭据、宽松 CSeq、各阶段独立超时均不符合本项目拟定合同。

任务路由：执行 `python3 ./.trellis/scripts/task.py current --source` 得到 `Current task: (none); Source: none`。本次按明确指定的已有规划任务 `.trellis/tasks/10-05-camera-lifecycle` 研究；未创建、start、实现或提交任务。已读取 design.md、prd.md、implement.md 和三个既有研究文件，仅更新本文件。

## 2. 下载范围、固定来源与网络记录

所有下列 gortsplib 文件均由本轮独立 curl 下载成功并读取，不仅转述父会话结论。源码下载并发最多 4；每个请求 `--connect-timeout 5 --max-time 20 --max-filesize 1048576 --fail --proto '=https'`，不自动重试或跟随重定向。目录树请求上限 2 MiB。临时证据目录 `/tmp/zhulong-rtsp-review.adhY1L` 不作为持久交接依赖，关键结果均写在本文。

固定 tag 来源（完整 URL）：

- https://raw.githubusercontent.com/bluenviron/gortsplib/v4.16.2/pkg/base/response.go
- https://raw.githubusercontent.com/bluenviron/gortsplib/v4.16.2/pkg/base/header.go
- https://raw.githubusercontent.com/bluenviron/gortsplib/v4.16.2/pkg/base/body.go
- https://raw.githubusercontent.com/bluenviron/gortsplib/v4.16.2/pkg/base/utils.go
- https://raw.githubusercontent.com/bluenviron/gortsplib/v4.16.2/pkg/base/request.go
- https://raw.githubusercontent.com/bluenviron/gortsplib/v4.16.2/pkg/base/url.go
- https://raw.githubusercontent.com/bluenviron/gortsplib/v4.16.2/pkg/headers/authenticate.go
- https://raw.githubusercontent.com/bluenviron/gortsplib/v4.16.2/pkg/headers/authorization.go
- https://raw.githubusercontent.com/bluenviron/gortsplib/v4.16.2/pkg/headers/keyval.go
- https://raw.githubusercontent.com/bluenviron/gortsplib/v4.16.2/pkg/auth/sender.go
- https://raw.githubusercontent.com/bluenviron/gortsplib/v4.16.2/pkg/sdp/sdp.go
- https://raw.githubusercontent.com/bluenviron/gortsplib/v4.16.2/pkg/description/session.go
- https://raw.githubusercontent.com/bluenviron/gortsplib/v4.16.2/pkg/description/media.go
- https://raw.githubusercontent.com/bluenviron/gortsplib/v4.16.2/pkg/auth/sender_test.go
- https://raw.githubusercontent.com/bluenviron/gortsplib/v4.16.2/pkg/headers/authenticate_test.go
- https://raw.githubusercontent.com/bluenviron/gortsplib/v4.16.2/pkg/base/header_test.go
- https://raw.githubusercontent.com/bluenviron/gortsplib/v4.16.2/pkg/base/body_test.go
- https://raw.githubusercontent.com/bluenviron/gortsplib/v4.16.2/pkg/base/response_test.go
- https://raw.githubusercontent.com/bluenviron/gortsplib/v4.16.2/client.go
- https://raw.githubusercontent.com/bluenviron/gortsplib/v4.16.2/go.mod
- https://raw.githubusercontent.com/bluenviron/gortsplib/v4.16.2/LICENSE

目录索引：https://api.github.com/repos/bluenviron/gortsplib/git/trees/v4.16.2?recursive=1 ，返回 `truncated=false`、tree SHA `cf1e0741373f30a8d8c8642cdf992eb318b01a2d`。这只是本次 GitHub 树快照，不是模块校验和或签名验证。

协议对照：https://www.rfc-editor.org/rfc/rfc7616.txt ，本轮下载成功并读取 §3.3、§3.4/3.4.1/3.4.2、§3.7/3.8 相关段落。用于区分现代 HTTP Digest 与库的旧式计算，不把该 RFC 当作所有 RTSP 摄像机兼容性证明。

本轮 21 个固定 tag 文件、目录树与 RFC 请求全部成功（curl exit 0），没有新的下载失败。保留历史：此前 proxy.golang.org 的 @latest 请求 20 秒超时；此前 fetch_content 读取 authenticate.go 返回 `The operation was aborted`，本地两个缓存目录未找到模块。这些是历史网络/缓存结果，**不是模块不存在或库不支持认证的证据**，authenticate.go 的读取阻塞现已解除。

核心文件内容 SHA-256（只用于本次源码快照复核，不是 Go module h1 校验）：

| 文件 | SHA-256 |
| --- | --- |
| pkg/base/response.go | c3fae6d4f0915c325de22d2e4e882449ddcc3d90e84894fcca7533440565b05e |
| pkg/base/header.go | 351baf9ecf608fb065f1930bf298b6bcf71b8f17ea7f0fca93f5e627b62ef5fc |
| pkg/base/body.go | 29404e397b2e3dc0422b9f97ec6ecc64bc4fccd291879e79f94d22d9c8cdc57e |
| pkg/base/utils.go | ba5c870148775c71adfca114b0760c5740db7c6f1bf37584f8c70d691267b304 |
| pkg/headers/authenticate.go | 173b53853a0404ca1203aad1c96ffbfc7dd97b5092ba5e5d56f5f388057c8188 |
| pkg/headers/authorization.go | 1b2ee801647ea65e6dd01d6f568fe894074b8cdcaa27558bc2a8314b8124c065 |
| pkg/headers/keyval.go | 8cf2656b722ad10cc2b689450bc1ac88b57ea88064882a576e28aa21c016211c |
| pkg/auth/sender.go | 6ce822576f1ad7198fe0a359537066960f20a52f906f35784d5f66432b49b009 |
| pkg/sdp/sdp.go | 4d6ffd8840a6ac0610021b4b166512e95ce609c509c071a1d60211e3fd2c1355 |

## 3. 响应与 Header：已检查的精确边界

行号均指上面固定 tag 原始文件。下表是源码事实，不是项目已实现的防护。

| 层次 | 已检查事实 | 适配器影响 |
| --- | --- | --- |
| `base/utils.go:21–33` | `readBytesLimited` 的 n **包括分隔符**；逐字节 Peek，需 bufio 缓冲能容纳 n | 不能把常量直接当作净字段长度；建议明确使用 4 KiB bufio，过小 reader 可更早 ErrBufferFull |
| `response.go:136–184` | 协议 token 搜索上限 255（含空格），但最终必须恰为 RTSP/1.0；状态 token 上限 4（含空格），ParseUint 允许 1～3 位，不强制标准三位/100～599；reason 上限 255（含 CR），净长度 1～254，随后必须 LF | 不支持 RTSP/2.0；业务须验证状态；没有 CSeq、Content-Type 或 DESCRIBE 语义检查 |
| `header.go:11–15,40–100` | 最多 **255 个物理 header 条目**，重复同名条目也计数；第 256 条失败 | Header 是 map→[]string，不是“最多 255 个不同 key” |
| `header.go:62–69` | 常量 key=512；先读 1 字节，再以 n=511 查找冒号，实际净 key 最多 **511 字节** | 第 512 字节净 key 不接受；不是 512 字节 key |
| `header.go:71–89` | 冒号后先用循环跳过任意数量 ASCII 空格；随后以 n=2048 读到 CR，净 value 最多 **2047 字节** | **跳过的前导空格不计 value 上限，也没有 header 总字节上限**；高速空格流仍可消耗时间/带宽，不能只靠字段常量 |
| `header.go:17–31,62–96` | 规范化部分 header 名并聚合同名值；此层没有独立验证 header token/控制字符；值只以 CR 截断，不主动拒绝裸 LF | 不能把解析成功当作安全的可转发字段；需严格 CRLF、token、控制字符检查 |
| `body.go:16–39` | 恰一个 Content-Length 时 ParseUint(10,64)，分配前拒绝 > **131072 字节（128 KiB）**，ReadFull 读取；0 合法 | 正/负号、非数字或溢出不是合法长度；短 body 返回错误 |
| `body.go:17–21` | Content-Length 缺失或有多个值时，**返回成功且 body=nil**，不是重复长度错误 | 重复长度必须显式拒绝；多轮认证连接若保留未消费字节会失步，错误即关连接 |

另外：无 chunked/Content-Encoding 解码；没有 Content-Length 时不会读取到 EOF 当 body。基于整个响应的 `LimitReader` 不能单独表达“header 16 KiB + body 128 KiB”两个独立限制；事后累加规范化 Header 大小也漏掉被跳过的空格。必须在原始字节读取阶段限制 header。

`base.Request.Marshal`（request.go:106–166）会移除 URL userinfo；但 query 仍在请求和 Digest URI 中，不能因此认为日志 URL 已脱敏。marshal 不验证业务提供的字段/凭据长度，也不转义 header 字段中的危险字符。

## 4. 鉴权：独立核验与兼容矩阵

### 4.1 已检查事实

- `headers/authenticate.go:67–159`：单次 Unmarshal 必须收到恰一个值；方法名只匹配大小写精确的 `Basic`/`Digest`。参数 key 按原文精确匹配，未统一大小写。realm 必须出现；Digest nonce 也必须出现，但空字符串未单独拒绝。
- `authenticate.go:29–39,125–150`：algorithm 缺省由 Sender 按 MD5；显式只接受大小写不敏感的 MD5、SHA-256。`*-sess` 和其它算法报解析错误。
- Authenticate 没有 qop/cnonce/nc 字段，switch 未识别的参数被忽略；**qop 被丢弃，而不是拒绝或支持**。opaque 和 stale 被保留为 `*string`；stale 未验证为布尔，也不会自动刷新 nonce。
- `headers/keyval.go:15–72`：内部未导出的简单 key/value 解析器；逗号在普通双引号值中可保留，但双引号遇下一个 `"` 即结束，没有 quoted-pair 反斜杠转义处理；重复参数覆盖前值；空格只在分隔之后跳过，不作完整语法校验。不适合把解析结果当作完整认证规范化器。
- `auth/sender.go:35–55`：遍历多个 WWW-Authenticate header 值，逐个解析，错误被跳过；首个可解析值可选，当前为 Basic 时后续可替换，SHA-256 总能替换，MD5 不替换已选 SHA-256/MD5。典型效果为 SHA-256 > MD5 > Basic；同等级选择与次序有关（后来的 SHA-256 可以覆盖前一个）。
- Sender 只知道算法，不知道 qop；因此可能优先选中“SHA-256 + qop”并生成错误的旧式响应，而非选择另一个真正兼容的挑战。
- `sender.go:58–88`：Digest 为 `H(H(user:realm:pass):nonce:H(method:URI))`，H 为 MD5 或 SHA-256；URI 为移除 userinfo 后的完整 URL。没有 qop/nc/cnonce。**不把 authHeader.Opaque 复制到 Authorization**，虽然 `headers.Authorization` 本身有 Opaque 字段并能输出。
- `authorization.go:153–179`：Basic 是 Base64(user + ":" + pass)。Digest username/realm/nonce/URI 直接拼进引号，未做 quoted-string 转义；显式 algorithm 被输出成带引号字符串。RFC 7616 §3.4 要求 algorithm 不加引号，此差异是严格服务器的额外兼容风险。
- Sender.Initialize 不清空旧 authHeader；Authenticate.Unmarshal 也不清空所有可选字段。适配器每个挑战/nonce 应新建值对象，不复用有状态对象期待“自动重置”。

### 4.2 兼容矩阵（“支持”仅指静态实现路径存在）

| 场景 | 库的实际行为 | 建议适配器结果 |
| --- | --- | --- |
| Basic realm | 可生成 Basic；明文 RTSP 上只是编码不是保密 | 仅在明确接受安全策略后使用；不能把 Basic 当加密传输 |
| Digest、algorithm 缺省/MD5、无 qop/opaque | 可计算传统摘要 | 受限支持；需桩和设备验证 |
| SHA-256、无 qop/opaque | 可计算同构传统摘要 | 受限兼容变体，不标注完整 RFC 7616；严格 algorithm 引号兼容需验证 |
| qop=auth / auth-int / 列表 / 空 qop | 解析器忽略，Sender 不使用 qop | 发送凭据前返回 unsupported_auth_variant，绝不按后续 401 判错密码 |
| opaque，包括 opaque="" | 解析但 Sender 不回传 | 首版严格判 unsupported_auth_variant；不在本任务补自定义回填/算法 |
| stale=true | 仅保留文本 | 适配器最多一次同源、同 realm/算法的新 nonce 重试；详见下文 |
| MD5-sess、SHA-256-sess、SHA-512-256/其它算法 | Authenticate 报错，Sender 跳过 | 没有另一个政策允许的独立挑战则 unsupported；不是 bad password |
| 多个独立 WWW-Authenticate 行 | Sender 会遍历 | 原始验证后显式选一个兼容挑战传入；不要把未经筛选的整个列表交给 Sender |
| 同一行拼接多个认证方案 | 没有完整 challenge-list 解析，可能忽略或混用参数 | 不宣称支持；拒绝含糊列表，不按逗号简单 Split（qop/realm 可有逗号） |
| charset/userhash/domain/其它扩展 | 多数被忽略，不代表实现了语义 | 首版白名单之外按不支持处理；以后逐项研究，不能静默降级 |
| 大小写变体、重复字段、转义引号 | 方法/字段名严格，重复覆盖，转义不完整 | 原始语法不满足受限合同就协议/能力错误；不伪造密码错误 |

RFC 7616 §3.3/§3.4 明确要求 qop、cnonce、nc，并规定 opaque 的回传及 stale 的意义；库的“SHA-256 路径存在”不能替代这些要求。本次没有运行认证向量。

### 4.3 不可绕过的原始挑战校验

**提案，不是新增自写认证实现：** 在库丢弃未知参数之前，以有界、能区分 token 和 quoted-string 的受限语法校验明确允许的字段。Digest 仅允许 realm、nonce、algorithm、stale；Basic 先仅允许 realm。拒绝重复 key、含糊组合方案、未闭合/转义引号以及控制字符。任何 qop/opaque 都应被识别为能力不支持；不能通过 `strings.Contains("qop=")` 这种遗漏大小写/空白且误命中 realm 的检查来保证安全。

允许在同一响应的多个独立挑战中按确定性政策选择支持项，但**默认不因更强 Digest 变体不支持就静默降级到 Basic/弱算法**；是否允许该兼容性回退须明确审阅。把一个已筛选的完整 header 值传给新 Sender，所有摘要计算仍交给库。本轮未实现扫描器、认证或补丁。

Digest 会插入 header 的 username/realm/nonce/URI 必须不含 CR/LF/NUL/其它危险控制字符、未处理的引号或反斜杠；不安全的值应拒绝而不是原样转发。密码本身在 Digest 中只参与哈希，不应无故禁止密码的 @、%、冒号等字符。Basic 用户名含冒号无法无歧义编码；非 ASCII 凭据的 charset 互操作以及复杂用户名转义仍待验证。如果产品要求这些用户名必须支持，应转入依赖兼容性研究，而不是把它们报为密码错误。

## 5. SDP：已检查事实及边界

- `pkg/sdp/sdp.go:687–753` 是本库自己的 Unmarshal，不是直接调用 Pion 的 Unmarshal。先复制成 string、删除全部 CR、按 LF Split，跳过空行；**没有总输入长度、行长、行数、media 数、attribute 数、format 数的内置配额**。独立调用该包不会自动继承 base/body 的 128 KiB 上限。
- 同段代码允许首行不是 v=0（若出现 v 则只接受 0）；末尾没有完整性检查。**空 body 解析返回 nil**，没有 media 也可语法成功；并不强制 o/s/t 或至少一个视频轨道。
- `sdp.go:427–485`：m= 至少四个字段；接受 video/audio/application/application/*/metadata/text；协议按 `/` 拆 token 检查已知名单，不验证所有组合是否可播放；format 只是字符串追加，没有此层的 RTP payload type 数值范围/codec 校验。
- `sdp.go:52–63,448–463`：媒体端口允许 0～**65536（含）**；端口范围仅 Atoi，不检查正数，超过一个 `/` 的其它段没有完整验证。这是具体宽松行为，不代表适配器应接受端口 65536。
- session/media attributes、bandwidth、timing 等可不断 append；无 context 参数，不能通过取消立即抢占同步解析。`Unmarshal` 没有整体清空已有描述字段，应每次构造新对象。
- `description/session.go:79–140` 会额外拒绝零 media、重复/部分 MID、无效 FEC 引用，并遍历所有媒体；`description/media.go:95–139` 对每个 payload 调 `format.Unmarshal`，可能处理 key-mgmt/MIKEY。ID 检查会线性扫描此前媒体，某些语义处理并非仅一次线性扫描。
- **description/format/MIKEY 的完整递归解析链未审计**；本次读取 Session/Media 仅确认调用与边界，不宣称每种编码参数解析已安全。空闲检查建议只用已检查的 `sdp.SessionDescription` 加受限视频语义检查，不为了“找 video”无条件解析所有 codec/MIKEY 数据。
- `Content-Base`、SDP a=control、u=、c= 等只是本次描述数据；不得用来触发新的连接、下载或携带凭据的跟踪请求。DESCRIBE 成功仅证明控制面/描述有效，不证明 RTP、分辨率、FPS 或可解码。主/子流入库深度探测合同不被此检查替代。

## 6. 建议的安全适配器合同（待规划审阅，尚未实现）

以下数字是项目建议值，**不是库默认值，也不是既成产品决定**。

### 6.1 连接、总预算与请求数

1. 一次探测使用 `min(调用方 deadline, 开始时刻+5秒)` 的绝对截止时刻；起点在 DNS/Dial 之前，覆盖连接、所有写入、所有认证响应、body 与结果校验。使用同一 DialContext 和 Conn.SetDeadline，不能每次响应/重试重置 5 秒。取消时关闭 net.Conn；所有返回路径关闭连接并回收取消监视资源。
2. 单次探测只建立一个控制连接，不因 401/EOF 自动重新拨号，不保留 session/keepalive。EOF 需重连的摄像机暂按兼容限制处理，不在同一调用无界重试；后续调度是新探测。DNS 多地址拨号也受同一总预算。
3. 只发 DESCRIBE，带 `Accept: application/sdp`，独立递增 CSeq。最大 **3 次 DESCRIBE / 3 个响应 / 2 次带 Authorization 的发送**：首次无凭据、一次正常挑战重试、至多一次 stale nonce 刷新。没有 OPTIONS、SETUP、PLAY、TEARDOWN、RTP/RTCP 或后台保活。
4. **所有 3xx 一律拒绝，redirect follow count=0**（包括同源），不读取 Location 后拨号，不迁移凭据。不解析成功就代表重定向获准。407 代理认证不支持；1xx、服务端请求和 `$` interleaved frame 不进入继续读取循环。
5. 每个响应要求一个且仅一个、与当前请求一致的数值 CSeq；缺失、重复或不匹配即协议错误并关闭。状态必须符合三位有效范围，只有 200 可进入 SDP 成功判定；403 是权限拒绝，404 是源路径/资源错误，不统一报密码错。
6. 同步 SDP 解析无法被 context 硬抢占；输入/复杂度预限额、解析前后检查 ctx，超时后绝不发布 online。不能为实现超时而丢下仍在解析的无界 goroutine。严格墙钟 5 秒返回仍需本机性能/调度测试，不能由 SetDeadline 单独证明。

### 6.2 有界原始帧与 SDP 验证

- 原始 status line + headers + 终止空行合计最多 **16 KiB**（包括所有被跳过的前导空格）；物理 header 条目最多 **64**；整个状态/头物理行最多 **4 KiB**，仍受库更小的 token/value 限制。校验 CRLF、合法 header token、不允许折叠行或裸 CR/LF/NUL。
- 在库 body 分配/消费之前确认 Content-Length **最多一个且合法、≤128 KiB**；200 必须恰一个且 >0，非 200 无 Content-Length 可解释为零 body。重复 Content-Length 不论是否值相同一律拒绝。若有 Transfer-Encoding 或非 identity Content-Encoding，则拒绝，不尝试隐式解压/另种 framing。
- 可以先按以上预算读取并校验原始 header/framing，准确读取 body 后交给 `base.Response.Unmarshal`，而不是重写完整 RTSP 协议。后续设计需明确保留同一 bufio 中预读字节、验证 framing 一致性；不得在多轮间丢掉缓存或盲目重建 reader。任何 framing 错误即关闭，不重同步。
- 200 必须有单一 Content-Type，经受限 MIME 解析确认为 application/sdp；body 长度必须恰合 Content-Length。非 200 body 同样有界，不能无界“排空错误消息”。最多三次响应意味着应用层有界帧预算最多 3×(16+128) KiB=**432 KiB**，另有固定 IO 缓冲/解析对象开销，不能将此数声称为整个进程内存上限。
- 调 SDP 解析前限制：body ≤128 KiB；最多 **2048 行**（含空行）；每行净长 ≤**4096 字节**；最多 **16 个 m=**；每媒体 format ≤**32**；session attribute ≤**256**、每 media attribute ≤**128**。这些限制在分配庞大解析树之前预检，并再次检查输出；CR 仅允许出现在 CRLF 行尾，避免库删除嵌入 CR 后改变语义。
- 使用新 SessionDescription，语法成功后必须至少存在一个可用 video 描述；验证 payload type 是 0～127、视频 payload/rtpmap 对应且为本产品允许的 H264/H265（对应 90000 时钟）、媒体协议组合属于批准的支持列表。未知/含糊格式不是 online；m=video 的 port=0 是 RTSP 常见描述方式，不能仅凭 0 判坏，65536 则不能按合法端口接受。
- 不从 SDP 宣称 FPS/分辨率已获得，不解码 SPS/PPS，不把不需要的 key-mgmt/FEC/远端 URL 交给额外解析器。若选择扩大到 description/format 完整解析，须重新覆盖这条依赖与攻击面。

### 6.3 401、stale 与错误分类

- 初次 401 是挑战，不是 bad password；先验证 framing、CSeq、挑战语法与能力，再决定是否发送凭据。没有凭据是 credentials_required；不可解析挑战是协议/认证协商错误。
- 第一次带凭据的请求仍返回 401：只有**已用受支持方式完成尝试**，且返回同一方案/realm/算法的有效挑战、stale 缺省或 false，才归为 `auth_rejected`（对外可映射现有 CAMERA_AUTH_FAILED，但文案是“认证被拒绝”，不声称必然密码打错）。认证数据可能因账户权限等原因被拒绝。
- stale=true（大小写不敏感）且新 nonce 不同、同 realm/算法、全部仍在能力白名单内，允许唯一一次刷新，构造新 Sender。重复 nonce、realm/算法变化、非法 stale 值、更多 stale 挑战或超出请求预算均返回 auth_negotiation_failed/exhausted，不能报密码错。
- 任何轮次出现不支持 qop/opaque/算法/扩展，都返回 `CAMERA_UNSUPPORTED` + 稳定 reason（如 unsupported_auth_variant），不能将 Sender 的旧式计算失败变成 auth_failed。缺失/畸形最终挑战也不据此诊断坏密码。
- 无降级重试、无递归挑战循环、无跨探测 nonce/Authorization 缓存。普通超时/EOF 是网络类失败；取消/配置代次过期不得写回健康结果。
- 所有返回和日志只含本地稳定错误码、camera id/角色/代次；不透传第三方错误（多处错误字符串包含原始 header/SDP/URL），不日志记录 req.String()/res.String()/Authorization/nonce/query token。

### 6.4 源访问与凭据安全边界

本期仅 rtsp，不因库支持 rtsps 自动扩大接口范围。Basic、旧式 Digest 都不提供 RTSP 传输加密，不能对不可信网络承诺安全。沿用局域网设备可达需求，不把 RFC1918 全部禁止；建议默认拒绝 loopback、unspecified、multicast、链路本地/云 metadata 目标，并在实际 Dial 前检查全部解析结果，避免仅验证主机字符串。是否允许特殊部署例外由父任务审阅，本次不新增配置。拒绝所有重定向并不能代替初始源的访问策略；query 中的凭据仍需整体加密和日志脱敏。

## 7. 高层 Client 与依赖元数据的复核

- `client.go:1166–1172` 在首次非 OPTIONS 请求前调用 doOptions；`1225–1244` 仅在 sender=nil 时处理一次初始认证。它不是上述显式 stale 重试状态机。
- `client.go:1372–1471` 的 DESCRIBE 在 301～305 且一个 Location 时 reset、解析新 URL、复制原 URL.User、更新 Host/Scheme 并递归。禁止 rtsps→rtsp 降级，但没有在该递归分支看到 hop 计数或同源限制；本次不据此宣称全部间接终止路径已审计。
- `client.go:870–900` 的 waitResponse 在 CSeq 缺失或值数量不为 1 时也可接受，不能复用为严格匹配证据。每次 waitResponse 新建 ReadTimeout，拨号和写入另有超时；Close（724–727）会取消并等待 done，但这不等于跨所有步骤的一次 5 秒预算。
- LICENSE 为 MIT；go.mod 声明 Go 1.23.0。本仓库 go.mod 声明 Go 1.27.1，从版本文本看不低于该最低版本，但本次没有编译验证。
- 模块直接依赖含 mediacommon/v2 v2.4.1、pion/sdp/v3 v3.0.15、pion RTP/RTCP/SRTP、x/net 等。只导入低层包不意味着整个模块依赖图只有几个文件；最终实际构建依赖、许可证与 MVS 版本须在实施门禁验证。
- **未知**：Go module zip/go.mod 的 h1 与 sumdb 校验链、tag 不可变性/签名或精确发布 commit 可信度、最新维护/弃用状态、撤回版本、漏洞数据库与传递依赖安全状态。未执行 go get/download/verify/vulncheck，未改 go.mod/go.sum；不能因为 GitHub raw 可读而声称这些已通过。

## 8. 必需验证清单（全部是待做测试，不是本轮结果）

1. RTSP 桩统计成功、认证失败、stale、取消所有路径：只发 1～3 次 DESCRIBE，不出现 OPTIONS/SETUP/PLAY/媒体接收；停止后连接/worker/取消监视器全部回收。
2. Basic、隐式/显式 MD5、无 qop SHA-256 的 method=DESCRIBE 摘要向量；URL userinfo 剥离且 path/query 一致；特殊密码 @/%/:、IPv6、非 ASCII 和危险用户名有明确结果，不泄露输入。
3. qop=auth/auth-int/list/empty、大小写/空白参数、opaque 空/非空、sess/未知算法、charset/userhash、多 header 顺序与组合 challenge、重复参数、转义引号、非法控制字符：不能被误分类为 bad password，不能发生隐式弱认证降级或 header 注入。
4. 同 realm 新 nonce stale 成功；重复 nonce、realm/算法变化、无限 stale、已支持方式明确拒绝、最终畸形/缺失挑战、403/404/407/3xx 各有稳定脱敏错误。攻击者 Location/Content-Base/a=control 不触发第二目标连接。
5. 精确阈值：净 key 511/512、value 2047/2048、255/256 library 条目、body 131072/131073；再覆盖项目 16 KiB/64 条 header 配额、无限冒号后空格、慢速分片、截断/超大/重复/缺失 Content-Length、错 CSeq 与请求间预读数据。
6. 空/仅音频/无有效 rtpmap/未知视频/端口 65536/非法 SDP、嵌入 CR、多媒体/attributes/line/format 配额与大量短行； fuzz 与内存/CPU 基准证明预算可接受。不能仅运行上游正常样例代替恶意输入验证。
7. 单个 DNS 慢、连接慢、写阻塞、401 后慢 body、多轮总时间超过 5 秒、取消与读取竞争：不重置截止时刻，不产生过期 online，不遗留 goroutine/FD。纯解析阶段的墙钟预算也要测。
8. 受支持摄像机真机样本验证，特别是只在 SETUP/PLAY 验证权限、401 后主动断 TCP、要求 OPTIONS、qop/opaque、严格 algorithm 引号的设备；当前不承诺这些全部兼容。

已读取上游 sender/authenticate/base 三类解析测试：有 Basic/MD5/SHA-256/多 header 正例及部分 fuzz 入口。Sender 测试实际使用 SETUP 而非 DESCRIBE；Authenticate 的 opaque/stale 测试只证明头部读写，不证明 Sender 回传 opaque 或正确刷新 nonce。**未运行这些测试；其存在不是本适配器的边界/安全/设备验收结果。**

## 9. 剩余启动门禁与本轮残余风险

1. 父会话审阅并批准受限认证矩阵（含 Basic 明文与禁止静默降级）、用户名/编码限制、帧/SDP 配额和错误分类；若目标设备需要 qop/opaque 等，先研究替代依赖，不以“密码错”掩盖不可用性。
2. 确认 v4.16.2 是否最终采用的版本，完成模块来源/校验和、维护/撤回/漏洞及许可证审阅；本次固定 tag 静态检查不能关闭供应链门禁。
3. 将本文件提案收敛到 design/implement 的正式合同并通过整体启动审阅；本次受限于只写研究文件，未修改规划门禁勾选。Native 新 ABI、订阅代次/锁序合同仍按 implement.md 保留独立审阅门禁，不由 RTSP 研究代验收。
4. 用户尚未批准启动实现；必须保持 Phase 1。上节桩测试、fuzz、宿主/真机和竞态检查是后续实施验收，不虚称本轮已经通过；在启动前应先批准其覆盖方案。

本次未实现自定义认证或任何业务代码，未启动任务/提交/暂存文件。既存 `native/src/pipeline/engine.hpp` 未编辑；开始时其 SHA-256 为 `dad479e08adc077e9c5c484c8ebcd87964c4eb6683d669cb838944dc21256148`。只更改本研究文档。

### 本轮手工核验记录

- 文档自动断言通过：21 个唯一固定 tag URL、9 个源码 SHA-256 与下载文件一致、下载日志全部 exit 0；检查明确保留“提案/未运行测试/启动门禁”区分。
- 结束时再次校验 engine.hpp 的 SHA-256，与上述开始值一致，证明未触及既存修改；`git diff --cached --name-only` 为空。
- `git status --short` 的项目路径集合与开始一致（研究目录属于原有未跟踪子任务）；本次工具仅对本研究文件作项目内写入。没有运行 Go/Native 构建、单元测试、fuzz 或设备联调。
