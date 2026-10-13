# 技术设计：媒体文件服务（按路径取字节与元信息）

本文落实 `prd.md` 的已确认决策 D1–D9。所有接口名、字段名、目录名与参数均为**待审阅建议**，不是已存在 API。

---

## 1. 包边界与依赖方向

新增单一业务包 `internal/mediafile`，遵循 `directory-structure.md` 的分包与单向依赖铁律。

```text
internal/mediafile/
  module.go        # NewMediaFileComponents：组件装配 + RouteRegistrar
  model.go         # DTO：MediaEntry、MediaRootInfo
  service.go       # 业务编排：按路径取元信息、按路径打开文件流
  resolver.go      # 路径解析与防护（os.Root 封装 + 路径段校验）
  resolver_test.go # 路径穿越/逃逸专项测试
  service_test.go  # 元信息、流打开、热切换
  handler.go       # Gin Handler：路由、参数绑定、响应信封
  handler_test.go  # 端到端：含 Range 语义
  locale.go        # 三语错误文案注册
```

### 依赖清单（全部为已有包，不引入新依赖）

| 依赖 | 用途 | 方向 |
| --- | --- | --- |
| `internal/storage` | 读取 `StorageConfig.MediaDirectory`、`PathInspector.InspectFS` 做 `st_dev` 校验 | mediafile ➔ storage ✅ |
| `internal/httputil` | 统一信封、错误映射 | mediafile ➔ httputil ✅ |
| `internal/apperr` | 强类型错误 | mediafile ➔ apperr ✅ |
| `internal/app` | 装配（仅 app 导入 mediafile，不反向） | app ➔ mediafile ✅ |
| `go.uber.org/zap` | 结构化日志 | 已有 |

### 明确不引入的依赖

| 不引入 | 原因 |
| --- | --- |
| `internal/camera` | ⚠️ **该包间接依赖 `internal/engine`（CGO）**：`probe.go:12`、`stream_media.go:6`、`stream_hub.go:10`、`wire.go:7` 均 import engine，而 `engine` 下 4 个文件 `import "C"`。一旦引入，本包（及其测试）将被拖入 CGO 链路，按 `AGENTS.md` 不能再裸跑 `go test`，必须走 native wrapper。 |
| `golang.org/x/sys/unix` | 改用标准库 `os.Root`（见 §3），无需 `openat2` 直接调用。 |
| `internal/audit` / `internal/audit/http` | D6 决定：只读端点不审计。 |
| `internal/database` | D5 决定：不建索引表、不查 DB。 |

### 为什么不复用 `internal/storage`

`internal/storage` 的职责是**配置、水位、清理、熔断**（见 `storage-guidelines.md`）。按路径取字节与元信息是**读取路径**，与它的**写入/清理路径**语义不同：

- `storage.Service` 有后台 `runLoop`、`CleanerEngine`、`EmergencyGate` 状态机；把传输塞进去会让该包承担两类不相关职责。
- 但**必须复用**它的两样东西：`MediaDirectory` 配置来源（避免路径真源分裂）与 `PathInspector`（`st_dev` 校验）。
- 采用**窄接口依赖**，接口定义在 mediafile 包内，由 app 装配层注入 `storage.Service`：

```go
// 定义在 internal/mediafile 包内，避免直接依赖 storage.Service 的具体类型
type MediaRootProvider interface {
    MediaRoot(ctx context.Context) (string, error)
}

type DeviceInspector interface {
    InspectFS(path string) (*storage.FSStats, error)  // 复用已导出的 FSStats
}
```

⚠️ 注意：`DeviceInspector` 的签名引用了 `storage.FSStats`，因此仍会 import `internal/storage`。

**实测结论（已验证）**：import `internal/storage` **不会**把本包拖入 engine/CGO 链路，且**可以裸跑 `go test -race`**：

```bash
$ go list -deps ./internal/storage | grep -E "internal/(engine|camera)"
(无输出)                    # ✅ 不含 engine/camera

$ go test -race ./internal/<import-storage-probe>/
PASS                         # ✅ 裸跑 -race 成功
```

⚠️ **但必须说清楚一个易混点**：`storage` 的依赖链里**确实有** `github.com/mattn/go-sqlite3`（经 `internal/database`），而该库是 **CGO** 实现。这不影响本包，因为：

1. 它是**数据库驱动的 CGO**，与 `internal/engine`（FFmpeg 静态链接）**不是同一条链路**；
2. 它**不需要** native wrapper 提供的外部归档——`AGENTS.md` 要求走 wrapper 的原因是「固定 `-L` 路径会静默复用陈旧库」，那是针对 engine 的 FFmpeg 归档；
3. 实测确认裸跑 `go test -race` 成功（`-race` 本身要求 `CGO_ENABLED=1`，而宿主默认即为 1）。

⚠️ **仍然存在的真实约束**：`CGO_ENABLED=0` 时 `-race` 不可用（`go: -race requires cgo`）。因此**交叉编译到无 CGO 环境时，本包测试无法带 `-race`**——但这与现状一致（`storage` 本身也是如此），非本任务引入。

若评审认为连 `storage` 也要切断，可改为在 mediafile 内定义等价的 `DeviceInfo` 结构，并在 app 层做适配。

---

## 2. 目录布局与 URL 形态

### 2.1 三个子目录的形态（保持原约定，不改命名）

| 子目录 | 磁盘层级 | 顶层键 | 中间键 | 叶子 |
| --- | --- | --- | --- | --- |
| `recordings/` | 3 层 | `camera_id` | `YYYY-MM-DD` | `HH-MM-SS.mp4` |
| `snapshots/` | 3 层 | `camera_id` | `YYYY-MM-DD` | `{event_id}.jpg` |
| `exports/` | 1 层扁平 | — | — | `{task_id}.mp4` |

⚠️ 本服务**不依赖**这些层级结构：只把路径当作「mediaDir 内的相对路径」做安全校验，**不做层级语义解释**。层级语义（哪个相机、哪一天、属于哪个事件）属事件域的 DB 记录。

### 2.2 路由设计（仅 3 条）

```text
GET /api/v1/media/roots
    媒体根状态：根路径、可用性、设备信息、各子目录是否存在
    用途：前端探测可用性 + 运维诊断

GET /api/v1/media/entries/*path
    按相对路径取单文件元信息（JSON 信封）

GET /api/v1/media/content/*path
    按相对路径取文件字节（Range / 206；裸字节流）
```

⚠️ **没有列表端点**（D5 / R0 决定）。列表由**事件证据域**用 SQL 提供：

```txt
前端列表页 ──➔ GET /api/v1/events?...  ──➔ SQL LIMIT/OFFSET  → 拿 items[].path
前端播放   ──➔ GET /api/v1/media/content/{path}  ──➔ 本服务吐字节
```

**为什么 `content` 与 `entries` 分开**：`content` 必须返回**裸字节流**（不能套 JSON 信封），而 `entries` 返回 JSON 信封。分开后语义清晰：`entries` 是控制面，`content` 是数据面。

**Gin 通配符冲突验证**：已实测 `GET /media/entries/*path` 与 `GET /media/content/*path` 共存注册**不 panic**（gin 1.x 下 `/*path` 与静态段可共存）。

⚠️ **通配符的转义陷阱（已实测，gin 1.x）**：

| 请求 URL | `c.Param("path")` | 说明 |
| --- | --- | --- |
| `.../content/recordings/cam-01/x.mp4` | `/recordings/cam-01/x.mp4` | 带前导 `/` |
| `.../content/recordings%2Fcam-01%2Fx.mp4` | `/recordings/cam-01/x.mp4` | **`%2F` 被解码为 `/`** |
| `.../content/recordings/..%2F..%2Fetc/passwd` | `/recordings/../../etc/passwd` | **解码后产生逃逸序列** |
| `.../content/recordings/cam/../../etc/passwd` | `/recordings/cam/../../etc/passwd` | gin 不归一化，原样传递 |

**结论与必须的处理**：

1. **剥掉前导 `/`**；2. **拒绝 NUL 字节**；
3. **不得**仅靠字符串前缀断言 —— `%2F` 解码后能构造出任意逃逸序列。必须交给 §3 的 `resolver`，由 `os.Root` 做内核级约束；
4. ⚠️ 不要对路径做 `filepath.Clean` 后自行拼接再 `os.Open` —— 那就退回了有 TOCTOU 的方案。**统一经 `os.Root`**。

### 2.3 无分页、无排序、无扫描

⚠️ 本服务**不做分页、排序、目录扫描**。`entries` 与 `content` 都是**单路径查询**，`O(1)`。

分页、排序、稳定排序全部由**事件证据域的 SQL** 承担（`ORDER BY` + `LIMIT/OFFSET`，`total` 由 `COUNT(*)` 给出）。这消除了一整类问题（清单见 §6.1「被消除的问题清单」）。

---

## 3. 路径解析与防护（核心安全设计）

### 3.1 分层防护

```text
第 1 层：标识符形态校验   —— 字符集、长度、NUL
第 2 层：os.Root 内核级约束 —— 逃逸拒绝（无 TOCTOU）
第 3 层：st_dev 掉盘穿透校验 —— 外挂盘脱机后拒绝服务
第 4 层：类型白名单        —— 只允许 recordings/snapshots/exports 三个顶层名
```

### 3.2 第 1 层：路径段形态校验

由于本服务**不做层级语义解释**（不解析 `camera_id` / `date` / `event_id`），校验只需保证路径段本身**不含危险字符**：

```go
// 顶层子目录白名单（第 4 层）
var allowedKinds = map[string]bool{"recordings": true, "snapshots": true, "exports": true}

// 每个路径段：严格字符集 + 长度上限（字母/数字/下划线/连字符/点）
var pathSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)
```

⚠️ **校验规则**：

1. 路径必须以 `allowedKinds` 中的一项作为**第一个段**；
2. **每个段**必须匹配 `pathSegmentPattern`；
3. ⚠️ **必须显式拒绝 `.` 与 `..` 段**（它们能匹配上面的正则）；
4. 拒绝 NUL 字节；5. 拒绝空路径。

**为什么不校验具体标识符格式**（如 `camera_id` 必须是数字）：本服务不解释层级语义，且 DB 里的 `path` 是**写入时生成**的，不是用户输入。若将来需要校验，那是事件域的职责。

⚠️ **不校验则后果严重**：`camera_id = "../../etc"` 会被 `filepath.Join` 拼成逃逸路径。虽然第 2 层 `os.Root` 会兜住，但第 1 层让攻击在**进入文件系统调用之前**就被拒绝，减少信息泄露面。

### 3.3 第 2 层：`os.Root`（实测验证，取代原定 `openat2`）

```go
// resolver 持有媒体根的 *os.Root 句柄
type resolver struct {
    rootPath string      // 规范化的媒体根绝对路径
    root     *os.Root    // 内核级目录约束句柄
    rootDev  uint64      // 根的 st_dev
    mu       sync.RWMutex
}

func newResolver(rootPath string) (*resolver, error) {
    root, err := os.OpenRoot(rootPath)  // 根本身允许是符号链接（实测可用）
    if err != nil {
        return nil, fmt.Errorf("open media root %s: %w", rootPath, err)
    }
    return &resolver{rootPath: rootPath, root: root}, nil
}

```

**实测结论**（`go1.27.1 linux/amd64`，测试根含逃逸符号链接与根内合法链接）：

| 请求路径 | 结果 |
| --- | --- |
| 正常嵌套文件 / **根内合法符号链接** | ✅ ALLOW |
| `../etc/passwd` | ✅ DENY `path escapes from parent` |
| 绝对符号链接指向 `/etc` / 相对符号链接逃逸 / `..` | ✅ DENY |

**机制**：标准库 `root_unix.go:60-130` 逐组件 `unix.Openat` + `O_NOFOLLOW` + `checkSymlink`，以 fd 相对遍历消除 TOCTOU。GOROOT 全量检索确认 `os` 包**未调用 `openat2`**（仅 `cmd/vendor` 下有该符号）。

**逃逸错误识别**：

```go
var pathErr *os.PathError
if errors.As(err, &pathErr) && strings.Contains(pathErr.Err.Error(), "path escapes") {
    return apperr.Invalid("MEDIA_PATH_ESCAPE", "Media path escapes the media root", err)
}
```

⚠️ 标准库未导出 `ErrPathEscapes` 这类哨兵错误，只能比对 `Err` 文本。**这是脆弱点**：Go 升级若改文案，判定会失效。缓解：判定**失败时按「拒绝」处理**（fail-closed）；并在 `resolver_test.go` 中**显式断言该文案**，一旦文案变化测试立即失败。

### 3.4 第 3 层：`st_dev` 掉盘穿透校验

复用 `storage.PathInspector.InspectFS`（`inspector_linux.go:41`）：

```go
stats, err := r.inspector.InspectFS(mediaRoot)
if err != nil { /* 拒绝服务 */ }
if stats.DeviceID != r.expectedDeviceID { /* 拒绝服务 */ }
```

**与 `EmergencyGate` 的关系**：`storage.Service.RefreshStatus` 在检测到掉盘时会把健康状态置为 `StatusError` 并置 `CanWrite=false`（`gate.go:84-95`）。但**读取路径不能只看 `CanWrite`**——熔断（空间不足）时 `CanWrite=false`，而**读取依然应该可用**（用户正是要审核旧证据）。因此：

| 场景 | `CanWrite` | 读取是否放行 |
| --- | --- | --- |
| 空间不足熔断 | `false` | ✅ **放行**（旧文件仍可审核） |
| 只读挂载 | `false` | ✅ 放行（只读传输本就只读） |
| **掉盘穿透** | `false` | ❌ **拒绝**（读到的是根分区，内容错误） |

判定依据用**独立的 `st_dev` 比较**，不复用 `CanWrite()`。这是本设计的一个关键区分点。

### 3.5 第 4 层：类型白名单

只允许 `recordings` / `snapshots` / `exports` 三个顶层目录，拒绝其他顶层名（防止读到 `mediaDir` 下未来可能出现的其他文件，如 `.lock` 标记、索引文件等）。

## 4. 传输实现（Range 语义）

### 4.1 使用 `http.ServeContent`

`os.Root.Open()` 返回 `*os.File`，**实测支持 `Seek()`**，因此可直接喂给标准库：

```go
f, err := r.root.Open(relPath)   // 已通过 4 层防护
if err != nil { /* 映射错误 */ }
defer f.Close()

info, err := f.Stat()
if err != nil { /* ... */ }

// ServeContent 自动处理：Range、Accept-Ranges、206、If-Range、
// If-Modified-Since、Last-Modified、ETag 协商、Content-Length
http.ServeContent(c.Writer, c.Request, info.Name(), info.ModTime(), f)
```

**为什么用 `http.ServeContent` 而不是 `c.File()`**：`c.File()` 接受**路径字符串**，会**绕过** `os.Root` 防护（它自己再 `os.Open` 一次，走普通路径解析）—— ⚠️ **必须避免的陷阱**；而 `ServeContent` 接受 `io.ReadSeeker`，正好吃 `*os.File`，且完整实现 RFC 7233 Range 语义。

### 4.2 Content-Type

`http.ServeContent` 通过文件扩展名 + `http.DetectContentType` 推断。但需**显式覆盖**以保证一致性：

| 扩展名 | Content-Type |
| --- | --- |
| `.mp4` | `video/mp4` |
| `.jpg` / `.jpeg` | `image/jpeg` |
| `.png` | `image/png` |
| 其他 | `application/octet-stream` |

在调用 `ServeContent` 前 `c.Header("Content-Type", ...)`，避免嗅探带来的不确定性与潜在的类型混淆风险。

⚠️ **不添加 `Content-Disposition: attachment`**：那会强制下载而非内联播放，与「前端可预览」目标冲突。浏览器用 `<video src>` 或 `fetch` + Range 均不需要该头。

### 4.3 Range 行为实测结果（`httptest` 验证）

| 请求 | 状态 | `Accept-Ranges` | `Content-Range` | 体 |
| --- | --- | --- | --- | --- |
| 无 `Range` | 200 | `bytes` | — | 完整 16 字节 |
| `bytes=0-9` | 206 | `bytes` | `bytes 0-9/16` | 10 字节 |
| `bytes=5-` | 206 | `bytes` | `bytes 5-15/16` | 11 字节 |
| `bytes=-4` | 206 | `bytes` | `bytes 12-15/16` | 4 字节 |
| `bytes=999-` | **416** | — | `bytes */16` | `invalid range: failed to overlap` |
| `bytes=abc` | **416** | — | — | `invalid range` |
| `HEAD` | 200 | `bytes` | — | 空，`Content-Length: 16` |

⚠️ **两处必须注意（实测发现，与常见预期不同）**：

1. **`Range: bytes=abc`（语法错误）返回 416，不是「忽略 Range 返回 200」**。标准库 `parseRange` 对语法错误直接报错，走 416。测试用例必须按 **416** 断言。
2. **416 的响应体是 `net/http` 的纯文本错误，不是统一 JSON 信封**。数据面（`content`）**有意**不走信封（它返回裸字节流），其错误响应也是裸的。Swagger 与前端契约需**明确说明**：解析 `content` 响应时**不能**假定 JSON。

### 4.4 不需要 `Cache-Control` 强缓存

录像文件**不可变**（写入完成后不再修改），因此 `ServeContent` 基于 `ModTime` 的 `Last-Modified` + `If-Modified-Since` 已足够。⚠️ 但**不加** `immutable` 或长 `max-age`：`exports/` 会被清理协程删除，长缓存会让前端拿到已删文件的缓存副本，与「文件可能突然消失」的语义冲突。保持默认（无 `Cache-Control`），让浏览器用条件请求。

### 4.5 传输性能（对齐 D8）

| 机制 | 说明 |
| --- | --- |
| `net/http` 的 `sendfile` 快路径 | `ServeContent` 最终 `io.CopyN(w, sendContent, sendSize)`；`w` 为 `*http.response` 时 `ReadFrom` 会尝试 `sendfile(2)`（Linux）。单 Range 下 `sendContent` 就是 `*os.File`，满足 `syscall.Conn` 断言，快路径可用 |
| 无解码 | 不触碰任何编解码库，不 import CGO |
| 无中间缓冲 | 不把整个文件读进内存 |
| 单请求单 fd | 每个 Range 请求打开一次 fd，`defer Close` 立即释放 |

⚠️ **`sendfile` 生效的三个前提**（实现时必须复核）：

1. **响应体未被中间件包装**。当前 API 中间件链（`AccessLog`、`MaxBodyLimit`、审计、鉴权）均不包装 `ResponseWriter`，快路径可用。若引入 gzip 等压缩中间件，会失效并退化为用户态拷贝。
2. **单 Range 或全量请求**。`net/http` 的 `sendFile` 仅处理 `*io.LimitedReader` 包装（单 Range 走 `io.CopyN` 时成立）；**多 Range**（`Range: bytes=0-1,5-6`）走 `io.Pipe` + `multipart`，**不走 `sendfile`**。
3. **必须 `ServeContent` 而非 `c.File()`**（见 §4.1）。

⚠️ **多 Range 的额外开销**：会构造 `multipart/byteranges` 响应并启动 goroutine + `io.Pipe`。浏览器正常播放**不会**发多 Range，但恶意客户端可以。需评估是否限制 Range 数量（标准库无内置上限）。

---

## 5. 错误契约

新增错误码与三语文案（`locale.go` 注册到 `httputil.RegisterMessages`）：

| Code | HTTP | Kind | 场景 |
| --- | --- | --- | --- |
| `MEDIA_ROOT_UNAVAILABLE` | 503 | `KindInternal` | 媒体根不可用（未配置/无法打开/掉盘） |
| `MEDIA_KIND_INVALID` | 422 | `KindInvalid` | 首段不在白名单（`recordings`/`snapshots`/`exports`） |
| `MEDIA_PATH_INVALID` | 422 | `KindInvalid` | 路径形态非法（NUL、非相对、含非法组件） |
| `MEDIA_PATH_ESCAPE` | 422 | `KindInvalid` | 路径逃逸出媒体根 |
| `MEDIA_ENTRY_NOT_FOUND` | 404 | `KindNotFound` | 文件/目录不存在 |
| `MEDIA_NOT_A_FILE` | 422 | `KindInvalid` | `content` 请求目标是目录 |

⚠️ **`MEDIA_PATH_ESCAPE` 用 422 而非 403**：这是**请求参数形态非法**（客户端构造了非法路径），不是权限不足。单用户系统里 403 会误导为「认证问题」。

⚠️ **错误信息不泄露根路径**：`MEDIA_ENTRY_NOT_FOUND` 的消息不得包含 `mediaDir` 的绝对路径（`locale.go` 文案中不插值路径），内部原因只进 Zap 日志。

---

## 6. 架构决策与已知限制

### 6.1 为什么本服务不做列表（扫描问题已被设计消除）

#### 演进过程（记录以避免重复讨论）

本设计曾考虑过「文件服务提供目录列表」，并围绕扫描成本做了实测（10000 个 `HH-MM-SS.mp4`，开发机）：仅 `ReadDir(-1)` 冷 3.26 ms；`ReadDir`+全量 `stat`+排序 冷 14.0 / 热 12.95 ms；命名排序优化（名排序 + 只 `stat` 当前页）冷 2.87 / 热 2.72 ms。当时的结论是「14 ms 可接受」。**但这个讨论本身就是多余的** —— 见下。

#### 用户指出的架构判断（已采纳）

> 「我数据库存储了对于的对象，不能只访问查询的对象吗」
> 「后续文件落库的时候，肯定是需要在这个记录中声明对应的文件在哪里才对」

**结论：列表应由 DB（事件证据域）提供，文件服务只按路径取文件。**

| 能力 | 归属 | 复杂度 |
| --- | --- | --- |
| 列表 / 筛选 / 分页 / 排序 | **事件证据域**（SQL） | `O(page)`，`total` 免费 |
| 按路径取字节（Range）/ 元信息 | **本任务** | `O(1)` |

**因此本服务不存在目录扫描** —— 不是优化掉，是**设计上不需要**。上面那些毫秒数对比已不适用。

#### 被消除的问题清单

| 问题 | 原本的代价 |
| --- | --- |
| `ReadDir` 全量扫描 | 万文件约 3.3 ms |
| 全量 `stat()` 取 `ModTime` | 万文件额外约 10 ms |
| 排序 + 稳定排序保证（分页不重不漏） | O(n log n) + 边界测试 |
| 命名约定与非约定文件混排导致排序错乱 | 正确性风险 |

#### ⚠️ 扫描的唯一正当用途：孤儿对账（不属本任务）

DB 与文件系统**无原子性**（产品 PRD `prd-v1.0.md:196`：不支持单个普通事务共同原子提交），因此：

| 情况 | 后果 | 处理 |
| --- | --- | --- |
| DB 有记录，文件被清理协程删了 | 播放 `404` | ✅ 本任务保证 `404` 语义正确；前端处理 |
| 文件在，DB 无记录（写记录失败） | 文件成为**孤儿**，不可见 | ❌ 需**孤儿对账工具**（后续任务） |

⚠️ **扫描能力并非完全无用**，但它是**运维对账工具**（周期性、离线、可慢），不是 UI 列表路径。**另开任务**。

#### 命名约定无需修改

绑定关系靠 DB 记录的 `path` 字段，**不靠文件名**。因此：

- `recordings/{camera_id}/{date}/{HH-MM-SS}.mp4` — 保持原样
- `snapshots/{camera_id}/{date}/{event_id}.jpg` — 保持原样
- `exports/{task_id}.mp4` — 保持原样

⚠️ **关于 `person_faces.image_path` 先例的更正**：该字段存的是服务端**绝对路径**，且自身带有待重构 TODO（`internal/person/model.go:75-80`：「当前接收服务端绝对路径 ImagePath 仅为阶段性测试桩实现」）。因此它**不能**作为「存绝对路径」的先例；只能作为「DB 存服务端不透明引用」这一抽象方向的先例 —— 注意其方案 B 用的是 `storageKey`，**不是裸 URL**。

#### 关键契约：DB 存相对路径，不存 URL

本服务对外只接受 **`mediaDir` 内的相对路径**（无前导 `/`，如 `snapshots/cam-01/2026-10-13/evt-123.jpg`）。因此事件域的表应存**相对路径**，而非绝对路径或访问地址。

**原则**：URL = 前缀 + 相对路径（`/api/v1/media/content/` + `recordings/cam-01/...`）。前缀含 API 版本、端点名与反代部署前缀，全属 **presentation 层**，独立于数据变化；写进 DB 等于让 API 形态污染持久化层。

| 变更事件 | 存绝对路径 | 存 URL | **存相对路径** |
| --- | --- | --- | --- |
| `UpdateConfig` 热切换 `mediaDir` | ❌ 引用**坏死**（指向旧盘） | ✅ 相对部分有效 | ✅ 引用有效，仅 blob 不在（`404`） |
| 端点改名 / API 升版 / 反代加前缀 / 上 CDN | ✅ 不受影响 | ❌ **全表失效** | ✅ 不受影响 |
| 新增对象存储后端（S3 / 远程节点） | ❌ 需迁移 | ❌ 需迁移且语义更乱 | ✅ 可映射为 `backend + key` |

⚠️ 热切换那行的关键区别：存绝对路径 → **引用坏了**（把盘插回来路径也是死的）；存相对路径 → **引用没坏，只是 blob 不在**（`404`，与「被清理协程删掉」语义一致）。

##### ⚠️ `MediaDirectory` 默认值就是相对路径

```go
// internal/storage/model.go:40
MediaDirectory: "data/media",   // 相对 CWD，不是绝对路径
```

这把「绝对路径」与「URL」**同时堵死**：`mediaDir` 自身都可能非绝对，而 URL 内嵌的路径段仍相对 `mediaDir`，前缀只是额外负债。**跨 `mediaDir` 变化唯一的不变量就是「`mediaDir` 内的相对路径」** —— 不是偏好，是唯一可行选择。

##### 安全：URL 入库会诱导消费者绕过防护

存 URL 等于把「解析 URL」下推到**每个消费者**：`strings.TrimPrefix(record.URL, "/api/v1/media/content/")` 截出路径再自行 `os.Open`，**§3 四层防护被绕过**。相对路径是结构化字段，取用时必然走 resolver。另：URL 路径段**已 escape**，存 URL 需额外定义「存 escape 前还是后」并要求写入方一致 —— 多一个不变量要守。

##### 「零拼接」靠派发，不靠入库

正确做法是在**响应里派生**，而非在 DB 固化：

```json
{ "path": "snapshots/cam-01/2026-10-13/evt-123.jpg",
  "contentUrl": "/api/v1/media/content/snapshots/cam-01/2026-10-13/evt-123.jpg" }
```

| 字段 | 性质 | 说明 |
| --- | --- | --- |
| `path` | **持久化字段** | DB 真源；本服务端点入参 |
| `contentUrl` | **派生字段** | 服务端拼接；端点改名只改一处代码，DB 零迁移 |

⚠️ 派生字段是**事件域响应 DTO 的建议**，不属本任务范围（本任务只有 `roots`/`entries`/`content`）。

##### 事件域 `path` 形态契约

| 项 | 约定 |
| --- | --- |
| 字段名 | `path`（**不是** `filePath` / `url` / `uri`） |
| 形态 / 分隔符 | `mediaDir` 内相对路径，**无前导 `/`**；固定 POSIX `/`（DB 内不存 `\`） |
| 首段 | 必须是 `recordings` / `snapshots` / `exports` 之一 |
| 字符集 | 与 resolver 的 `pathSegmentPattern` **一致**：`^[A-Za-z0-9_.-]{1,128}$`（逐段） |
| 长度上限 | 512（对齐 `person` 的 `maxImagePathLength`） |
| 多后端预留 | 加 `storage_backend TEXT NOT NULL DEFAULT 'local'`，**不要**把后端编进 `path` |

⚠️ **写入时必须用同一套字符集校验**，否则 DB 会出现 resolver 拒绝的路径 → 「DB 有记录但取不到」的**假故障**（看似文件被删，实为路径形态非法）。

⚠️ **何时 URL 才对**：仅当记录需**跨主机 / 跨 CDN 分发且 URL 不由本服务派生**时（第三方推流地址、外部 Webhook）。即便如此，正确建模也是 `storage_backend ('local'|'s3'|'http') + key` —— URL 构造规则属于**该后端的配置**，不属于记录。本任务媒体文件全在本地 `mediaDir` 下，不适用。

### 6.2 无缩略图（D9）

抓拍 JPEG 可能数 MB，列表页直接加载原图会很慢。

**本任务的立场（用户已确认）**：不提供缩略图端点。

**为什么不硬件化**（详见 `prd.md` 的 D9 节）：

| 缺口 | 说明 |
| --- | --- |
| JPEG 编码无硬件 | 板端有 `fded0000.jpegd`（**解码器**），**无 `jpege`**；`vepu` 是视频编码器。缩略图若要 `.jpg`，最后一步必须回 CPU |
| MPP / jpegd 用户态无证据 | 板端快照中 `grep -iE "mpp\|jpegd\|vepu"` 零命中，仅有 RGA 用户态证据 |
| RGA 尚未接入 | `native/src/backends/` 不存在，`runtime_factory.cpp` 硬编码 `CpuVisionBackend` |

且硬件方案会**推翻 D1**（需扩展 `engine.h`）并**部分违反 D8**。

**后续任务的技术方向**（前置：RGA 落地 + MPP/jpegd 实测）：

```
jpegd 硬件解码原图（贵的那步） → RGA 硬件缩放 → CPU 编码小缩略图（便宜的那步）
```

⚠️ **已知体验代价**：无缩略图时，证据列表页首屏需加载原图，**可能很慢**。需在后续前端任务中评估懒加载策略。

### 6.3 并发与资源

- `os.Root` 文档明确**可并发使用**（`Methods on Root are safe to be used from multiple goroutines simultaneously`）。
- 句柄的热切换原子替换见 §7。

---

## 7. 配置热切换的语义

`storage.Service.UpdateConfig` 会改变 `MediaDirectory`（`service.go:285-315`）。本服务的语义：

```text
配置变更后：
  新请求 ──➔ 使用新根的 *os.Root 句柄
  已在传输中的请求 ──➔ 继续用旧句柄完成（文件已打开，fd 有效）
```

**实现**：`MediaRootProvider.MediaRoot(ctx)` 每次调用返回当前配置值；`resolver` 缓存 `(rootPath, *os.Root)`，若与缓存不一致则重建句柄并关闭旧句柄。

⚠️ **旧句柄关闭的竞态**：进行中的 `Open` 可能持有旧 `*os.Root`。必须用 `sync.RWMutex`：读路径持 `RLock` 完成 `Open`，切换时持 `Lock` 替换并关闭。**不能**在替换时立即 `Close()` 旧句柄（会让在途请求 `EBADF`）。

**不做的**：不迁移旧盘数据、不索引旧路径（`storage-guidelines.md` §1.3 禁止 TB 级跨盘拷贝）。旧盘文件在热切换后**不再可见** —— 已知限制，需在 PRD/文档写明。

---

## 8. 测试策略

### 8.1 路径防护专项（`resolver_test.go`，表驱动）

必须覆盖的攻击向量：

| 用例 | 期望 |
| --- | --- |
| `../../etc/passwd` | `MEDIA_PATH_ESCAPE` 或 `MEDIA_PATH_INVALID` |
| `recordings/../../etc/passwd` | 拒绝 |
| `/etc/passwd`（绝对路径） | `MEDIA_PATH_INVALID` |
| `recordings/cam/2026-01-01/../../../etc` | 拒绝 |
| 含 NUL 字节 | `MEDIA_PATH_INVALID` |
| `%2F` 编码的逃逸（`..%2F..%2Fetc`） | 拒绝（**gin 会解码 `%2F`**，实测确认） |
| 已解码的 `recordings/../..` | 拒绝（gin 不归一化，原样传递，实测确认） |
| 单个 `..` 段（可匹配路径段正则，**必须显式拒绝**） | `MEDIA_PATH_INVALID` |
| 单个 `.` 段 | `MEDIA_PATH_INVALID` |
| 空路径 | `MEDIA_PATH_INVALID` |
| 含非法字符的段（`cam 01` 带空格） | `MEDIA_PATH_INVALID` |
| 绝对符号链接指向 `/etc` | 拒绝 |
| 相对符号链接逃逸 | 拒绝 |
| **根内合法符号链接** | ✅ 放行 |
| `evil`（非白名单顶层名） | `MEDIA_KIND_INVALID` 或 `MEDIA_PATH_INVALID` |
| 目标不存在 | `MEDIA_ENTRY_NOT_FOUND`（404） |
| **文件被删后请求（模拟 `exports/` TTL 清理）** | `MEDIA_ENTRY_NOT_FOUND`（404）——正常语义 |
| Go 逃逸错误文案断言 | 断言 `path escapes` 文案存在（Go 升级守护） |

### 8.2 Range 语义（`handler_test.go`）

| 用例 | 期望 |
| --- | --- |
| 无 Range | 200 + 完整体 + `Accept-Ranges: bytes` |
| `Range: bytes=0-9` | 206 + `Content-Range: bytes 0-9/N` + 10 字节 |
| `Range: bytes=5-` | 206 + 尾部 |
| `Range: bytes=-10` | 206 + 末 10 字节 |
| `Range: bytes=999999-` | 416 |
| `Range: bytes=abc`（语法错误） | **416**（实测，非 200） |
| `If-Range` 匹配/不匹配 | 206 / 200 |
| HEAD 请求 | 200 + 无体 + `Content-Length` |
| 目录路径请求 `content` | `MEDIA_NOT_A_FILE`（控制面 JSON 信封） |

### 8.3 掉盘穿透

用 `DeviceInspector` 桩模拟 `st_dev` 变化：

| 用例 | 期望 |
| --- | --- |
| `st_dev` 与记录值一致 | 放行 |
| `st_dev` 变为根设备号 | `MEDIA_ROOT_UNAVAILABLE`（503） |
| `InspectFS` 返回错误 | `MEDIA_ROOT_UNAVAILABLE`（503） |
| **空间熔断（`CanWrite=false`，`st_dev` 正常）** | ✅ **仍放行**（关键区分点） |

### 8.4 并发

- `-race` 强制（项目铁律）。
- 并发 `Open` + 配置热切换的竞态测试。

### 8.5 测试夹具

⚠️ 因为仓库**无文件生产方**（PRD D1），测试必须用 `t.TempDir()` 构造：

```go
func fixtureMediaRoot(t *testing.T) string {
    root := t.TempDir()
    // recordings/{cam}/2026-01-01/10-00-00.mp4
    // snapshots/{cam}/2026-01-01/{event}.jpg
    // exports/{task}.mp4
    // 以及逃逸符号链接（需检查 os.Symlink 是否可用，不可用则 skip）
    ...
}
```

⚠️ **符号链接测试在部分环境不可用**（如无权限的 Windows / 受限容器）。用 `t.Skip` 而非失败，但**必须在 Linux CI 上真实运行**。

---

## 9. 与既有规范的对照

| 规范要求 | 本设计如何满足 |
| --- | --- |
| `directory-structure.md` §2 单向依赖 | mediafile ➔ storage/httputil/apperr；app ➔ mediafile；无反向、无环 |
| `directory-structure.md` §2.6 C++ 类型不出 engine | 本包**不 import engine**（避免经 camera 间接引入） |
| `http-api-guidelines.md` §2 统一信封 | 控制面用 `httputil.Success`；数据面（content）返回裸字节，属**有意的例外**，需在 Swagger 注明 |
| `http-api-guidelines.md` §3 分页四元组 | ⚠️ **不适用**：本服务无列表端点（R0）。分页由事件证据域承担 |
| `storage-guidelines.md` §1.4 路径收敛 | 第 1 层字符集 + 第 2 层 `os.Root` 内核约束，**强于**「规范化 + 前缀断言」 |
| `quality-guidelines.md` §2 `-race` | 测试强制 `-race` |
| `quality-guidelines.md` §3 无 panic | 返回 `apperr.Error` |
| `error-handling.md` 三语 i18n | `locale.go` 注册全部新增码 |
| `dependency-injection.md` Fx 纯粹性 | `NewMediaFileComponents` 纯构造函数，不打开文件；根句柄**首次请求**惰性建立 |

---

## 10. 需要评审确认的开放点

| # | 开放点 | 人家建议 |
| --- | --- | --- |
| 1 | `DeviceInspector` 是否允许引用 `storage.FSStats` | 允许。**已实测**：import `storage` 可裸跑 `go test -race`，依赖链不含 engine/camera。若要完全解耦，则在 mediafile 定义等价结构 |
| 2 | 配置热切换后旧盘文件不可见 | 接受，写入文档。不迁移数据 |
| 3 | 是否提供 `roots` 端点 | 提供，便于前端探测可用性与展示根信息 |
| 4 | 元信息字段集 | 只返回 `path`/`name`/`size`/`modTime`；`duration` 需解析容器（**违反 D8**，不做） |
| 5 | `MEDIA_PATH_ESCAPE` 的文案依赖 | 接受脆弱性 + fail-closed + 测试守护 |
| 6 | 缩略图 | **已确认不做**（D9）。硬件化需 RGA 落地 + MPP/jpegd 实测，且 JPEG 编码无硬件 |
