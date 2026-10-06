# RTSP DESCRIBE 鉴权替代方案：有界源码研究

## 1. 推荐结论

**下一步优先选择 `github.com/icholy/digest v1.2.0` 的独立低层 API，替换此前拟用的 gortsplib auth.Sender；RTSP framing/SDP 仍可复用已核查的 gortsplib 低层能力。不要使用 icholy 的 HTTP Transport。**

这不是批准缩减产品兼容范围，也不是声称该版本所有认证边界都已通过验收。该库已存在真正独立的 `ParseChallenge → Digest(Options) → Credentials.String` 路径：支持 qop=auth、非空 opaque、无 qop MD5、SHA-256、quoted-pair、显式 nonce count/cnonce，以及任意调用方提供的 Method/URI；无需自写摘要算法，也不需要新增 Native 网络栈。下面列出必须关闭的少量、具体的参数解析/序列化缺口，尤其 **opaque="" 的存在性丢失**。不能将这些缺口改写为产品不支持 qop/opaque。

第二候选是 **libcurl 8.22.0（tag `curl-8_22_0`）的正式 RTSP API**：所查实现覆盖空/非空 opaque、MD5/SHA-256 及 sess 变体，确实用 DESCRIBE 和完整 Stream-URI 计算摘要；但需要 C/CGO、额外静态构建与生命周期集成，而且也存在需要防护的 unknown-qop 降级与“首个 Digest 挑战”行为。它是可行备选，不是“无缺点所以直接替换”的答案。

选择 Go 方案的理由是公开低层接口与当前 Go 控制连接设计吻合、没有第三方生产运行时依赖、所需核心算法已实现，且无需把 curl 的连接/重试状态机带入已有 Native ABI 工作。**最终采用门禁聚焦于几个已知兼容缺口和标准依赖校验，不要求无限扩大的安全审计。** 若不愿接受针对上游解析/序列化缺口的修复路线，libcurl 是本轮已有实证的退路，而不是另起无限选型。

本轮只研究 **2 个**候选；找到真实独立 API 后停止扩展。没有把新版 gortsplib 算作候选，没有推测其最新版本已修复 qop。

## 2. 范围、任务路由与来源可信度

- 已重新读取 design.md 第 15 节及前次报告。父会话明确：前次 qop/opaque 排除方案未获批准；本报告遵守该结论。
- `python3 ./.trellis/scripts/task.py current --source` 仍返回 `Current task: (none); Source: none`；按明确指定的已有 `.trellis/tasks/10-05-camera-lifecycle` 任务研究，不创建或 start。
- 只新增本文件；没有改前次报告、设计、代码、go.mod/go.sum 或 engine.hpp。未编译/执行任何第三方 Go/C 代码；本地 Python 仅用于下载、JSON/base64 解码和哈希比对。
- 网络研究止于 **44 次有界 curl 请求：33 次成功、11 次 exit 28 超时**；最多 4 个并发，每请求 `--connect-timeout 5 --max-time 20 --max-filesize 2097152 --fail --proto '=https'`，没有无限重试或自动跟随重定向。少数 raw 下载失败后各用一次固定 ref 的 GitHub contents API 获取；请求清单见第 8 节。
- 临时下载目录 `/tmp/zhulong-auth-alternatives.y8vU5Q` 不作为长期交接依赖；关键事实与失败记录都保存在本文。

### 已发现的真实版本

| 候选 | 可复核固定版本 | 本轮元数据实证 |
| --- | --- | --- |
| icholy/digest | tag `v1.2.0` → commit `104e41263110fbc4690a1c0d6b309cac6e597e56` | GitHub tags API 返回该 tag/commit，同时列出 v1.1.0、v1.0.1 等历史标签；commit 时间 2026-07-28，GitHub 报告 SSH 签名 verified=true；不是本机独立验签 |
| curl | tag `curl-8_22_0` → annotated tag object `d0386aa3e805543ce464c993e1eb31b4d92c6356` → commit `01346829096c61b372692f6dc43ffa778c6caccd` | releases/latest 返回该发布，发布时间 2026-09-02；tag API 返回 PGP 签名及 GitHub verified=true；不是本机独立验签 |

通过本地 Git blob SHA-1 算法，将 icholy 已下载的 12 个源码/测试/许可证/模块文件逐一与固定 ref 的 tree 条目比对，全部一致。这个比对证明读取的 tag 文件与所查树快照一致，**不替代 Go module zip 的 h1/sumdb 校验**。

## 3. 候选 A：icholy/digest v1.2.0（推荐的下一步）

下面行号来自本轮固定版本原文件，不是 README 印象或上游测试运行结果。

### 3.1 独立 API 与 RTSP URI 适用性

| API | 已检查合同 | RTSP 适配方式 |
| --- | --- | --- |
| `ParseChallenge(string) (*Challenge, error)` | challenge.go:33–65 调内部参数解析器，输出 Realm/Nonce/Opaque/Stale/Algorithm/QOP/Charset/Userhash | 对有界 WWW-Authenticate 值使用，不经 HTTP RoundTripper |
| `CanDigest(*Challenge) bool` | digest.go:51–59 检查算法、无 qop 或 auth/auth-int；不是完整语法/必需字段校验 | 可作能力判断，但不能用它证明挑战完整、没有被丢弃参数 |
| `Digest(*Challenge, Options) (*Credentials, error)` | digest.go:35–49 的 Options 暴露 Method、URI、GetBody、Count、Username、Password、A1、Cnonce；63–145 原样使用 Method/URI | Method 传 **DESCRIBE**，URI 传与 RTSP 请求行完全相同、移除 userinfo 的 **absolute RTSP Request-URI** |
| `Credentials.String() string` | credentials.go:72–141 序列化 Authorization 参数 | 返回值放入 RTSP Authorization header；不经过 http.Client |

**没有强制 HTTP path-only 的低层限制：** digest.go:66 保存 `o.URI`；104/120/140 将 `o.Method` 与 `o.URI` 直接用于 A2；不调用 net/url，不重新解析或截短 scheme/host/query。它导入 net/http 是为了 auth-int 的 `http.NoBody`，不意味着必须启动 HTTP 传输。

例如调用方的线缆 URI 若为 `rtsp://cam.example:554/live/%2Fmain?token=a%2Bb&channel=1`，应将该字符串原样交给 Options.URI，保持 RawPath/RawQuery 的最终线缆表示。不能另用 `URL.Path`、`EscapedPath` 或 `URL.RequestURI()` 得到 `/live/...` 来算摘要；也不能先散列绝对 URI，再把输出的 uri 参数改成相对路径。

**容易误用之处：** transport.go:91–103 的 HTTP Transport 默认填 `req.URL.RequestURI()`，README 的 Low Level API 示例也采用 HTTP 路径；这些示例不适合照搬 RTSP。低层 API 则没有此限制。digest_test.go 中已有 `Method: REGISTER, URI: sip:182.82.132.122` 的非 HTTP 用例，进一步支持接口不绑定 HTTP URL 的事实；本轮未执行这个用例。

### 3.2 核心认证能力（源码事实）

- **无 qop MD5：** digest.go:77–88 的缺省/MD5 分支与 100–105 的无 qop 分支实现传统 H(H(A1):nonce:H(A2))。
- **qop=auth：** 106–121 使用 nc、cnonce、qop；不会因正常 `qop="auth"` 忽略 qop。Count=0 时默认 1，输出 nc 用 `%08x`；调用方重用 nonce 必须增加 Count。
- **qop=auth-int：** 122–143 支持请求 body hash；优先 auth，只有没有 auth 且存在 auth-int 时才用后者。GetBody=nil 会 hash 空 body，符合无请求体 DESCRIBE 的使用方向。不是对未来任意请求体的无条件保证。
- **算法：** 缺省/MD5、SHA-256、SHA-512、SHA-512-256 有计算路径；unknown algorithm 返回错误。**MD5-sess/SHA-256-sess 没有支持路径**，不能因为 Challenge 能解析 algorithm 字符串就宣称会算。
- **opaque：** digest.go:72 复制 chal.Opaque；credentials.go:109–115 在非空时回传并加引号。这已经解决旧 Sender 完全不回传普通 opaque 的问题；空 opaque 的已知差异见后文。
- **nonce/stale：** nonce 来自 Challenge；ParseChallenge 将 stale（值大小写不敏感）映射 bool。低层 Digest 不管理重试、连接、nonce 过期或缓存；业务必须管理挑战代次与次数。
- **cnonce：** digest.go:176–181 默认使用 crypto/rand 的 8 字节并 hex 编码；每次未提供 Cnonce 的 Digest 调用会重新生成。Options.Cnonce 可由调用方固定在一个 nonce 代次内，便于明确 nc/cnonce 生命周期和测试。不得在生产中使用固定测试 cnonce。源码随机失败分支 panic，适配器若自行提供 CSPRNG 生成的 Cnonce，可显式控制失败路径；这不是自写 Digest 算法。
- **quoted strings：** internal/param/param.go:96–124 识别反斜杠 quoted-pair，保留引号内逗号；19–23 用 `strconv.Quote` 输出，普通双引号/反斜杠能正确转义。算法、qop、nc 按 token 输出，不重现旧库把 algorithm 输出成 quoted string 的问题。
- **userhash/charset：** userhash 计算路径存在；Charset 仅被保存，没有基于 Charset 做编码转换。非 ASCII/非法 UTF-8/非打印字符与 RFC quoted-string 语义不能仅由 strconv.Quote 推定全面兼容。

### 3.3 必须明示的已知缺口：不是产品排除清单

1. **空 opaque 被省略（已知事实，不是未知）：** Challenge.Opaque 与 Credentials.Opaque 为 string，没有 presence 位；`opaque=""` 与没有 opaque 都变成空串，String() 省略该字段。若验收要求回传存在但为空的 opaque，原版 v1.2.0 不能直接通过。应走上游序列化/存在性修复并固定实际修复 commit，或者改用已核查 libcurl；不能宣布该设备“不受支持”，也不能把失败算坏密码。本文不发明尚不存在的修复版本，不写补丁。
2. **参数名大小写与必需字段：** challenge.go 的 key switch 区分大小写；ParseChallenge 不要求 realm/nonce 出现，重复参数后者覆盖前者。大写 `QOP`/`ALGORITHM` 可能被忽略，结果误入无 qop/缺省 MD5。需要保留原始参数存在性并做协议级验证/规范化，或通过上游解析器修复；绝不能把“CanDigest=true”当作可以安全降级的证据。
3. **qop 列表空白：** `strings.Split(value, ",")` 后不 TrimSpace，SupportsQOP 精确匹配。`"auth, auth-int"` 可选 auth，但 `"auth-int, auth"` 会漏掉带前导空格的 auth，转用 auth-int；`" auth"` 不会识别。应规范化列表 token 并加测试，不把可规范化空白变成用户配置限制。
4. **挑战选择不自动选最强：** FindChallenge 返回第一个可支持的独立 WWW-Authenticate 值，可能跳过不支持的强挑战而选择较弱挑战。它不是“优先 SHA-256”选择器，也不是合并多个方案的一行 header 的完整解析器。项目应明确选择过程，不能直接套 FindChallenge 后声称没有降级。
5. **语法/字符边界：** param 的空白处理仅 ASCII space，unquoted token 仅字母/数字/连字符；quoted parse 以 rune 读值，非法 UTF-8 不保证按原字节往返；序列化用 Go 字符串转义，控制字符不等同 HTTP quoted-pair。已有安全合同应拒绝 CR/LF/NUL 等注入输入，但不因此新增“用户名不能包含普通引号/反斜杠”的产品限制——普通转义已由此库支持。
6. **unsupported ≠ wrong password：** 正确识别的未知 qop/algorithm 会返回错误，不降为 MD5；风险来自参数先被忽略。适配器必须把解析、能力、随机源、计数错误与最终认证拒绝分开。`Options.A1` 不是让本项目绕过算法限制自写 -sess 的后门。

这些是可以定位到源码行的具体工作，不是要求先证明每个设备/所有网络输入都完美才做下一步。核心计算已由成熟依赖提供；下一步只应围绕解析/序列化正确性与有限事务做验证和必要上游修复，**不 fork 一套自写 Digest hash/response 实现**。

### 3.4 许可证、维护与安全快照

- LICENSE 是 MIT（版权起于 2020）；go.mod 声明 `module github.com/icholy/digest`、`go 1.22`，与项目 go 1.27.1 的文本最低版本不冲突。未编译验证。
- 已查生产包 imports 只有标准库与自身 internal/param；go.mod 的 `gotest.tools/v3 v3.5.1`、`google/go-cmp v0.5.9` 用于测试依赖。不要把模块图与实际生产 imports 混为一谈。
- GitHub 仓库元数据显示未 archived、最近 push 2026-07-28；固定版本 CI 文件配置 Go 1.22 的 go build/go test，源码包含 AXIS challenge、quoted realm、SIP URI、MD5/SHA-256/auth-int 等测试。**这是维护和用例存在的证据，不是执行通过或庞大装机量证明**；规模明显小于 curl。
- GitHub 公开 security-advisories 请求返回 `[]`；OSV 对 Go 包 `github.com/icholy/digest`、version `v1.2.0` 的查询返回 `{}`。只能表述为“本次查询未返回已知条目”，不是没有漏洞或完成审计。
- proxy.golang.org 的 `.info`/`.mod` 与 sum.golang.org lookup 均约 5 秒连接超时。**模块 h1 校验、sumdb 链与代理内容尚未核对**；GitHub 来源可读，不能由超时推断模块不存在。可在受信网络完成一次标准下载/verify 后关闭这一常规依赖门禁，而不是继续无限选型。

## 4. 候选 B：libcurl 8.22.0 RTSP（可行但较重）

### 4.1 这是 RTSP 传输 API，不是可直接抽走的 Go Digest 函数

公开 API：`curl_easy_setopt` 设置 `CURLOPT_URL`（连接目标）、`CURLOPT_RTSP_STREAM_URI`（RTSP 请求行的 URI）、`CURLOPT_RTSP_REQUEST=CURL_RTSPREQ_DESCRIBE`、认证方式和凭据，再以 libcurl easy/multi 执行。`Curl_auth_*` 是内部函数，不建议从 Go 链接这些非公共 ABI 或抽取 C 文件拼成新认证实现。

**URI/Method 的完整调用链已查：**

1. lib/rtsp.c:278–301 用 DESCRIBE 枚举选出文字 `DESCRIBE`。
2. rtsp.c:341–345 从 CURLOPT_RTSP_STREAM_URI 取原串（未设置则是 `*`，所以必须显式设置）。
3. rtsp.c:384–385 将实际 request 字符串和完整 stream_uri 传入 `Curl_http_output_auth(..., HTTPREQ_GET, stream_uri, NULL, FALSE)`；其中 HTTPREQ_GET 是内部请求种类，不会把文字 method 改为 GET。
4. lib/http.c:799–877 保留 path 字符串，query 参数为空就不拼接；转交相同 request/path 给 output_auth_headers。715–723 再转交 Curl_output_digest。
5. lib/http_digest.c 调 Curl_auth_create_digest_http_message；lib/vauth/digest.c:804–810 对 `request + ":" + uripath` 计算 A2，**没有要求 uripath 必须以 `/` 开头或去掉 query**。
6. rtsp.c:476–480 使用同样的 block.request/block.stream_uri 构造 `DESCRIBE <absolute-uri> RTSP/1.0`。

因此只要 URL/Stream-URI 由项目统一构造并移除 userinfo，**该公开路径确实支持含 query 的完整 RTSP URI**。CURLOPT_RTSP_STREAM_URI 与 CURLOPT_URL 的目标不自动相等，适配器不能随意让二者跨来源。

### 4.2 认证能力和限制

- vauth/digest.c:520–666 解析 nonce、realm、opaque、stale、qop、algorithm；字段名/主要值大小写不敏感。无 qop 用旧式摘要，auth/auth-int 优先 auth。
- MD5、MD5-sess、SHA-256、SHA-256-sess 存在路径；SHA-512-256 及 sess 受 CURL_HAVE_SHA512_256 编译能力限制。当前非 Windows 路径已查；Windows SSPI 不是本项目宿主/目标的默认路线，也未独立审查。
- nonce 代次清理、stale 重置 nc、新 cnonce（12 个随机字节的 Base64）以及认证后递增 nc 均在代码中；不是项目重新实现。
- opaque 用指针保存存在性，934–942 只要指针非空就回传，**包括 opaque=""**。quoted-pair 读写处理双引号/反斜杠。非 ASCII/控制字节会在输出 helper 中百分号编码，因此不能无测试地承诺所有 charset 情况。
- **未知 qop 会退成无 qop：** 566–598 找不到 auth/auth-int 时没有报错，仅保持 qop=nil；之后走旧式摘要。项目需要在允许凭据重发前识别此类不支持挑战，返回能力/协商错误，不能容忍默默降级。此防护是解析/策略边界，不是新增摘要算法。
- **并不自动偏好 SHA-256 Digest：** lib/http.c:993–1021 已看到“本响应 Digest 已可用则忽略重复 Digest header”的逻辑。HTTPAUTH 文档说选择 best method，不能推导出多个 Digest 算法里选择最强。两个 Digest 行顺序及第一个不支持算法的行为必须实测。
- bodyless DESCRIBE 可走 auth-int 的空 body 计算；该实现注明不支持 PUT/POST auth-int，不能把结论扩展到其它业务请求。
- 401、unsupported algorithm、重复 nonce 等错误路径不能全映射为“密码错”；必须结合挑战和请求阶段分类。

### 4.3 RTSP 与工程代价

- CURLOPT_RTSP_REQUEST 文档明确 DESCRIBE 不需要 session，默认加 Accept: application/sdp；源码方法选择直接发送该请求，没有看到此入口先强制 OPTIONS 的逻辑。没有 SETUP/PLAY 是拟集成路径，不是抓包验收结果。
- libcurl 内部比较发出/收到的 CSeq；这不自动证明所有重复 CSeq/畸形 framing 场景符合本任务更严格合同。
- CURLOPT_TIMEOUT_MS 文档提供单个 transfer 的总毫秒预算；多次 easy 调用不能各给完整五秒，必须用外层绝对截止时刻的剩余预算。取消、多轮认证上限、header/body 配额需通过回调/事件循环管理和桩测试证明；不能只写“设超时”就声称全部关闭。
- 集成需要新增正式 C wrapper 或 Go/CGO binding、静态 libcurl 构建/许可材料、宿主与目标板构建能力检查、全局 init/cleanup、每任务 handle 所有权以及取消 drain。不能偷偷复用系统 curl 二进制或启动 ffmpeg 深度拉流。
- 如果选择它，宜裁剪不使用协议和认证后端，但必须保留 RTSP 使用的 HTTP auth/相关构建能力；不能想当然“禁 HTTP 即仅保留 RTSP”。DNS 后端的可取消性/超时、回调在重试前可否可靠截断仍是具体集成验证项。

### 4.4 发布、许可证与安全证据

- COPYING 是 curl 自身宽松许可证（源码 SPDX 为 `curl`），不是把它直接标为 MIT；实际静态链接的 TLS/DNS 等依赖仍需列许可证。
- 固定 tag/commit 与签名元数据见第 2 节；仓库未 archived，发布和 push 记录可达。
- 官方 `https://curl.se/docs/vuln-8.22.0.html` 本次返回“8.22.0，2026-09-02 发布，0 published security problems”。这是该页面当时的已公布漏洞快照，不是未来保证、完整依赖图结论或本机验证。
- 未下载 release tarball/校验签名，未探测本机系统 libcurl 是否有 RTSP，未构建任何代码；不能把 GitHub 可验证签名元数据当作最终分发产物验签。

## 5. 比较与推荐落点

| 维度 | icholy/digest v1.2.0 | libcurl 8.22.0 |
| --- | --- | --- |
| 独立 API | 公开纯 Go Digest 函数；非常贴合现有 RTSP adapter | 正式 RTSP easy/multi API；Digest 内部函数不是稳定公共 ABI |
| absolute RTSP URI + DESCRIBE | 原样 Options.URI/Method，已有非 HTTP SIP 单测源码 | Stream-URI 和实际 DESCRIBE 文字一路传到摘要，已追调用链 |
| 无 qop MD5 / qop auth / SHA-256 | 有 | 有 |
| opaque | 非空有；空值存在性丢失，须解决 | 空/非空均有 |
| 引号与反斜杠 | 有 quoted-pair；Go quote 的非打印字符语义需测 | 有 quoted-pair；非 ASCII 输出策略需测 |
| sess | 未实现，不允许借 A1 自写补齐 | MD5/SHA-256 sess 已实现 |
| 无静默降级 | 正常参数的未知值报错；参数 key/选择需防护 | unknown-only qop 会无 qop；重复 Digest 行忽略后者，需防护 |
| nonce/cnonce/nc 生命周期 | 调用方显式持有，易与探测代次绑定 | curl 内部持有，外层管理 handle 与轮数 |
| 模块/交叉构建 | 新增小型 Go 模块；生产 imports 标准库 | 新增 C 静态库及 CGO 集成，成本明显更高 |
| 常规安全证据 | API 签名元数据；OSV/公开 GH advisories 未返回条目；sumdb 网络失败 | 正式签名发布元数据、官方版本漏洞页；未验分发包 |

**推荐落点是 A，不并行引入两套认证。** 相比原 gortsplib Sender，A 的缺口已从缺失核心 qop 算法收敛为特定语法/序列化边界；不应为这些可定位问题立即扩大 Native 网络栈。与此同时，v1.2.0 的空 opaque 和参数忽略问题是事实，正式 pin/启动审阅不能把它们隐去。下一步应以最小契约测试确认需要的修复，优先采用上游正式修复或可审阅的确切上游 commit；如坚持完全不接受该修复路线，就使用 B 的正式 RTSP API，而不是修改产品验收排除 qop/opaque。

研究没有批准 Basic 明文策略、源地址允许列表、禁止某种设备/编码字符或扩大 Native 依赖。sess/特殊 charset 的实际设备需求应在兼容性样本中核验，不能由本报告自动缩小已批准产品范围。

## 6. 推荐 Go 适配边界（设计方向，不是实现）

1. **网络由项目 RTSP adapter 控制。** 低层 base 用于 marshal/unmarshal，响应原始字节先受 header/body 配额；SDP 先受复杂度预检。继续采用 design §15 的方向：一次绝对五秒预算、严格 CSeq、零重定向、有限挑战与 stale 重试。具体数值/策略由整体设计审阅，不由本文批准。
2. **只有认证计算/参数序列化交给成熟依赖。** 不使用 digest.Transport/FindChallenge 作为默认全套行为，不启动 HTTP 请求；也不再经过 gortsplib Authenticate 预解析（它会丢弃 qop）。对于多个独立挑战显式解析、验证、按批准策略选择；保留 qop/algorithm/opaque 等字段是否出现的事实，防止解析遗漏触发默认算法。
3. **保持 URI 单一事实来源。** RTSP 请求行与 Digest Options.URI 必须来自同一个已确定、无 userinfo 的 absolute URI；包含 query，不能在认证前后重新编码/规范化或自动改成 path-only。Basic 和 Digest 的凭据均与目标来源、配置代次绑定，不凭共享 hostname 跨端口/凭据缓存。
4. **nonce/cnonce/count 有限生命周期。** 每次探测新认证状态；同 nonce 复用时 count 单调递增、不得为负/回绕；更换 nonce 后换新的 cnonce 并重置 count。有限 stale=true 重试不当成坏密码；新 realm/算法变化或无限挑战不得偷偷重开总预算。默认由库生成随机 cnonce 或用标准 CSPRNG 提供，不自行计算 A1/A2/response。
5. **分类优先于密码诊断。** malformed challenge、被丢弃/无法解释的参数、unsupported qop/algorithm、stale 耗尽、网络超时、最终合法的认证拒绝分开。只有已执行受支持、格式正确的认证请求后得到明确拒绝，才映射 CAMERA_AUTH_FAILED；对外仍是“认证被拒绝”，不声称一定密码录错。
6. **已知序列化缺口显式关闭。** 空 opaque、字段大小写、qop token 空白、重复/组合挑战放进最小验证清单；不能为了使 v1.2.0 看起来可用而删除 qop/opaque、固定 MD5、手写 -sess 或默认回落 Basic。优先通过上游修复获得真实固定版本/commit；这是一项具体依赖工作，不是新产品限制。
7. **不从轻量认证推出媒体成功。** 成功仍只代表 200 + 有效视频 SDP 控制面证据；不 SETUP/PLAY，不取得真实 FPS/分辨率，不刷新已知失效媒体证据。鉴权错误与网络调度、取消/代次写回隔离。
8. **秘密与输入边界。** 禁止泄漏 raw header、Authorization、query token 或第三方原始错误；校验危险控制字符和 bounded input/output。普通合法 quoted strings 不因为旧 Sender 的缺陷而禁用。

## 7. 最少的后续验证与具体门禁

### 必需实现验证（本轮全部未执行）

- 用固定 nonce/cnonce 的独立参考向量验证 DESCRIBE：无 qop MD5、MD5+auth+opaque、SHA-256+auth+opaque、qop 列表和空请求体 auth-int；同时检查请求行 URI、Authorization uri、摘要输入三者相同，覆盖绝对 URL、IPv6、% 编码与 query。
- Header/凭据包含逗号、`\"`、`\\`、普通非 ASCII 字符；opaque 非空/空/缺失分别验证；无空 opaque 正确回传不得把该项标通过。测试不是拿被测库同时生成“独立预期摘要”。
- 同响应多个 SHA-256/MD5/Basic 挑战顺序、unknown qop/algorithm、大小写 key、空白 token、重复参数、畸形/合并挑战：不得静默改用旧式摘要或报告坏密码；明确配置/协商结果。
- 相同 nonce 的 nc/cnonce 复用、新 nonce stale 刷新、非法/重复 stale、轮数耗尽、并发两个不同凭据探测隔离；没有跨源/配置代次缓存泄漏。
- RTSP 桩抓取全部请求序列：只发必要的 DESCRIBE；认证重试和取消计入同一绝对截止时刻；无重定向目标连接、无 OPTIONS/SETUP/PLAY、无 FD/goroutine 泄漏。既有 framing/SDP 恶意输入配额覆盖保持不变。
- 一批实际摄像机样本，至少包含带 qop/opaque 的源；深度 Probe 能成功的源若轻量验证失败，先归因 URI、编码或认证兼容，而不是缩小支持范围或套用三次网络失败规则。

### 可以关闭规划、不能由本次伪称完成的事项

1. 父会话接受 **A 的低层 API 路线**，并明确采用上游小范围修复处理空 opaque/参数规范化，或基于实际需求决定切到已查的 B。**当前确切阻塞是 v1.2.0 空 opaque 存在性丢失和参数忽略可导致隐式默认值，不是整个 Digest 选型毫无可用候选。** 不需要再无限比较第三、第四个库。
2. 在可达的受信网络完成一次固定依赖下载的 h1/sumdb 校验，记录实际 pin 与许可证；若采用新的上游修复 commit，重新核对该差异。OSV/公告查询可在 pin 时更新一次，不要求先完成所有传递代码的形式化安全审计。
3. 把接口合同、最小测试和已知缺口处理方案纳入父会话正式设计/实施计划审阅。本文没有修改那些文件，也不替代 Native ABI/代次合同审阅。
4. 用户实现批准与 task start 仍未发生；上述实验属于之后获批的实施验证，不在本次研究里执行。

## 8. 完整证据索引与网络结果

### 8.1 版本、维护、供应链与安全端点

以下除标明失败外均成功读取；元数据端点的 latest/tags 用于发现，代码结论绑定固定 tag/commit。

- https://api.github.com/repos/icholy/digest/tags?per_page=5
- https://api.github.com/repos/icholy/digest
- https://api.github.com/repos/icholy/digest/git/trees/v1.2.0?recursive=1
- https://api.github.com/repos/icholy/digest/commits/104e41263110fbc4690a1c0d6b309cac6e597e56
- https://api.github.com/repos/icholy/digest/security-advisories （返回 []）
- https://api.osv.dev/v1/query （POST：package.name=github.com/icholy/digest，ecosystem=Go，version=v1.2.0；返回 {}）
- https://proxy.golang.org/github.com/icholy/digest/@v/v1.2.0.info （exit 28，连接超时）
- https://proxy.golang.org/github.com/icholy/digest/@v/v1.2.0.mod （exit 28，连接超时）
- https://sum.golang.org/lookup/github.com/icholy/digest@v1.2.0 （exit 28，连接超时）
- https://api.github.com/repos/curl/curl/releases/latest
- https://api.github.com/repos/curl/curl
- https://api.github.com/repos/curl/curl/git/ref/tags/curl-8_22_0
- https://api.github.com/repos/curl/curl/git/tags/d0386aa3e805543ce464c993e1eb31b4d92c6356
- https://curl.se/docs/vuln-8.22.0.html

### 8.2 推荐候选的精确固定源码 URL

raw 成功的文件：

- https://raw.githubusercontent.com/icholy/digest/v1.2.0/digest.go
- https://raw.githubusercontent.com/icholy/digest/v1.2.0/transport.go
- https://raw.githubusercontent.com/icholy/digest/v1.2.0/credentials_test.go
- https://raw.githubusercontent.com/icholy/digest/v1.2.0/digest_test.go
- https://raw.githubusercontent.com/icholy/digest/v1.2.0/internal/param/param_test.go
- https://raw.githubusercontent.com/icholy/digest/v1.2.0/README.md
- https://raw.githubusercontent.com/icholy/digest/v1.2.0/LICENSE
- https://raw.githubusercontent.com/icholy/digest/v1.2.0/go.mod

以下四个 raw 请求均 exit 28、20 秒未收到数据；同文件随后通过下方固定 commit API 成功读取，不是文件不存在：

- https://raw.githubusercontent.com/icholy/digest/v1.2.0/challenge.go
- https://raw.githubusercontent.com/icholy/digest/v1.2.0/credentials.go
- https://raw.githubusercontent.com/icholy/digest/v1.2.0/internal/param/param.go
- https://raw.githubusercontent.com/icholy/digest/v1.2.0/challenge_test.go

成功的固定 commit API 来源（JSON content 为 base64 原文）：

- https://api.github.com/repos/icholy/digest/contents/challenge.go?ref=104e41263110fbc4690a1c0d6b309cac6e597e56
- https://api.github.com/repos/icholy/digest/contents/credentials.go?ref=104e41263110fbc4690a1c0d6b309cac6e597e56
- https://api.github.com/repos/icholy/digest/contents/internal/param/param.go?ref=104e41263110fbc4690a1c0d6b309cac6e597e56
- https://api.github.com/repos/icholy/digest/contents/challenge_test.go?ref=104e41263110fbc4690a1c0d6b309cac6e597e56
- https://api.github.com/repos/icholy/digest/contents/.github/workflows/go.yml?ref=104e41263110fbc4690a1c0d6b309cac6e597e56

核心原文 SHA-256（与 Go h1 无关）：

| 文件 | SHA-256 |
| --- | --- |
| digest.go | 1f2b0b1a9a34d170bbda10db5378d2029f87346f68c0f2d051d9cb3b577c957e |
| challenge.go | b513d24ae9aae39a076ac4c8c74b75b23121706ecbc530dd8aac4f95577db907 |
| credentials.go | b88de0cebc33e2fb9a99158e544efd01e9426e861f62ade070db25f5f51fca20 |
| internal/param/param.go | 2d478b782f15ad255a5255970d23256777cf9a5abc256b45a85bc87c6d538dc2 |

### 8.3 curl 的精确固定源码 URL

raw 成功：

- https://raw.githubusercontent.com/curl/curl/curl-8_22_0/lib/http_digest.c
- https://raw.githubusercontent.com/curl/curl/curl-8_22_0/COPYING
- https://raw.githubusercontent.com/curl/curl/curl-8_22_0/docs/libcurl/opts/CURLOPT_RTSP_REQUEST.md
- https://raw.githubusercontent.com/curl/curl/curl-8_22_0/docs/libcurl/opts/CURLOPT_TIMEOUT_MS.md
- https://raw.githubusercontent.com/curl/curl/curl-8_22_0/docs/libcurl/opts/CURLOPT_HTTPAUTH.md

以下 raw 请求均 exit 28、约 5 秒连接超时：

- https://raw.githubusercontent.com/curl/curl/curl-8_22_0/lib/vauth/digest.c
- https://raw.githubusercontent.com/curl/curl/curl-8_22_0/lib/rtsp.c
- https://raw.githubusercontent.com/curl/curl/curl-8_22_0/docs/libcurl/opts/CURLOPT_RTSP_STREAM_URI.md
- https://raw.githubusercontent.com/curl/curl/curl-8_22_0/lib/http.c

随后成功读取的固定 ref API：

- https://api.github.com/repos/curl/curl/contents/lib/vauth/digest.c?ref=curl-8_22_0
- https://api.github.com/repos/curl/curl/contents/lib/rtsp.c?ref=curl-8_22_0
- https://api.github.com/repos/curl/curl/contents/docs/libcurl/opts/CURLOPT_RTSP_STREAM_URI.md?ref=curl-8_22_0
- https://api.github.com/repos/curl/curl/contents/lib/http.c?ref=01346829096c61b372692f6dc43ffa778c6caccd

## 9. 本轮手工验证与残余风险

已检查源码调用链、低层 API 与测试源码；未运行 Digest 向量、设备测试、fuzz 或构建。网络超时、模块校验未完成、版本特定兼容缺口均如实区分，未据此推断库不存在，未称“零漏洞”。

项目内唯一写入为本新研究文件。engine.hpp 的保护基线为 `dad479e08adc077e9c5c484c8ebcd87964c4eb6683d669cb838944dc21256148`；前次研究、design.md、go.mod/go.sum 同样列入结束检查。未暂存、启动任务或提交。

结束核验通过：44 个请求 URL 均已持久化，33 成功/11 超时统计与日志一致，4 个核心源码 SHA-256 与原文一致；前次报告、design.md、go.mod、go.sum、engine.hpp 共 5 个受保护文件哈希未变。`git diff --cached --name-only` 为空，工作树项目路径集合与开始一致（新文档位于既有未跟踪子任务目录）。这些只是文档/来源/写入范围检查，不是运行时功能测试。
