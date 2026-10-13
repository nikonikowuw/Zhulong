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
    GetRootDeviceID() (uint64, error)                 // 取根文件系统 st_dev，用于掉盘判定
}
```

⚠️ 注意：`DeviceInspector` 的签名引用了 `storage.FSStats`，因此仍会 import `internal/storage`。

⚠️ **`MediaRootProvider` 当前没有现成实现，必须新增**。已核实：`storage.Service` 的 `activeMediaDir` 与 `expectedDeviceID` 都是**未导出字段**，且没有任何 `MediaRoot()` / `ActiveMediaDir()` 访问器（全仓检索确认）。两个可选做法：

| 做法 | 说明 | 推荐 |
| --- | --- | --- |
| **A. 在 mediafile 内用 `GetConfig(ctx)` 取 `MediaDirectory`** | 已导出、无需改 `storage`；代价是每次建句柄多一次 DB 读 | ✅ 推荐 |
| B. 给 `storage.Service` 新增 `MediaRoot(ctx)` 访问器 | 与设计原文一致；但需改 `storage`，而 implement.md 的边界写着「不修改 storage 现有行为」 | ⚠️ 需先放宽边界 |

⚠️ **`DeviceInspector` 不得依赖 `storage.Service.expectedDeviceID`**（它未导出，且语义不适用于本场景）。掉盘判定必须用 mediafile **自采的快照**，见 §3.4。

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
第 1 层：标识符形态校验   —— 字符集、长度、NUL、空路径/空段、显式拒 `.`/`..`
第 2 层：os.Root 内核级约束 —— 逃逸拒绝（无 TOCTOU）
第 3 层：st_dev 掉盘穿透校验 —— 外挂盘脱机后拒绝服务（⚠️ 只在建/换句柄时，不在请求热路径）
第 4 层：类型白名单        —— 只允许 recordings/snapshots/exports 三个顶层名
```

⚠️ 第 1 层与第 3 层的**执行时机不同**：第 1/2/4 层在**每个请求**都跑（都是内存判断 + 一次 `openat`，开销极小）；第 3 层只在**建/换句柄**时跑（它会读 `/proc/mounts`，见 §3.4）。

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
4. 拒绝 NUL 字节；5. 拒绝空路径；6. 拒绝总长 > 512（与事件域 `path` 形态契约一致）。

⚠️ **空路径必须真的拒绝**（实测确认）：`GET /api/v1/media/content/` 会**命中 handler**，`c.Param("path")` 为 `"/"`，剥掉前导 `/` 后是**空串**。路由层拦不住它，必须由本层拒绝并返回 `422 MEDIA_PATH_INVALID`。

⚠️ **另需注意（实测）**：gin 不会归一化路径 —— `recordings//a.mp4`（双斜杠）与 `recordings/./a.mp4` 都会**原样透传**给 handler。若按段切分，会得到**空段**与 **`.` 段**；前者必须在规则 5 被拒，后者在规则 3 被拒。**不得**用 `filepath.Clean` 先归一化再放行（那等于放行 `./`，且退回有 TOCTOU 的手工拼接方案）。

**为什么不校验具体标识符格式**（如 `camera_id` 必须是数字）：本服务不解释层级语义，且 DB 里的 `path` 是**写入时生成**的，不是用户输入。若将来需要校验，那是事件域的职责。

⚠️ **不校验则后果严重**：`camera_id = "../../etc"` 会被 `filepath.Join` 拼成逃逸路径。虽然第 2 层 `os.Root` 会兜住，但第 1 层让攻击在**进入文件系统调用之前**就被拒绝，减少信息泄露面。

### 3.3 第 2 层：`os.Root`（实测验证，取代原定 `openat2`）

```go
// resolver 持有媒体根的 *os.Root 句柄
//
// selfDev 是「建句柄那一刻」媒体根的 st_dev，由 resolver 自己采集。
// 它**不用** storage.Service 的 expectedDeviceID —— 那个字段未导出，且语义不适用（见 §3.4）。
type resolver struct {
    mu       sync.RWMutex
    rootPath string      // 媒体根路径（可能非绝对，见 §6.1）
    root     *os.Root    // 内核级目录约束句柄
    selfDev  uint64      // 建句柄时媒体根的 st_dev
}

func newResolver(rootPath string, inspector DeviceInspector) (*resolver, error) {
    root, err := os.OpenRoot(rootPath)  // 根本身允许是符号链接（实测可用）
    if err != nil {
        return nil, fmt.Errorf("open media root %s: %w", rootPath, err)
    }

    // 第 3 层：建句柄时做一次掉盘穿透校验（不在请求热路径上，见 §3.4）
    selfDev, err := deviceIDOf(root, rootPath)
    if err != nil {
        root.Close()
        return nil, err
    }
    if err := verifyNotFallthrough(inspector, rootPath, selfDev); err != nil {
        root.Close()
        return nil, err
    }

    return &resolver{rootPath: rootPath, root: root, selfDev: selfDev}, nil
}

// deviceIDOf 取媒体根自身的 st_dev。
//
// ⚠️ 不要用 root.Stat(".").Sys() == os.Stat(rootPath).Sys() 做相等比较：
// *syscall.Stat_t 是含嵌套数组的结构体，Go 的 == 对它们是合法的但会**比较全部字段**
// （含 Atim 的纳秒字段等），实测同一目录两次 Stat 的结构体 == 为 false。
// 必须只比 Dev 字段（Linux 下为 *syscall.Stat_t.Dev）。
func deviceIDOf(root *os.Root, rootPath string) (uint64, error) { ... }
```

**实测结论**（`go1.27.1`，测试根含逃逸符号链接与各类根内链接）：

| 请求路径 | 结果 |
| --- | --- |
| 正常嵌套文件 | ✅ ALLOW |
| **相对**目标、落在根内的文件链接（`recordings/rel-in.mp4`） | ✅ ALLOW |
| **相对**目标、落在根内的目录链接（`dirlink/a.mp4`） | ✅ ALLOW |
| 链接链（链接 → 链接 → 根内） | ✅ ALLOW |
| `../etc/passwd` / 相对链接逃逸 / 绝对链接指向 `/etc` | ✅ DENY `path escapes from parent` |
| **绝对**目标链接，**即使目标在根内**（`abs-in.mp4`） | ✅ DENY `path escapes from parent` |
| 逃逸序列经过 `..`（如 `snapshots/up/etc/passwd`） | ✅ DENY，但文案是 **`no such file or directory`** |

⚠️ **「根内符号链接必须放行」的措辞已收窄**：`os.Root` 在遍历中对每个组件施加 `O_NOFOLLOW` + `checkSymlink`。结果是**绝对目标一律拒绝**，哪怕最终落在根内 —— 因为在 fd 相对语义下无法证明安全。

| 链接形态 | 结果 |
| --- | --- |
| 相对目标，最终落在根内（文件 / 目录 / 链接链） | ✅ 放行 |
| 绝对目标（即使最终落在根内） | ❌ 拒绝 |
| 相对目标，逃逸出根 | ❌ 拒绝 |

⚠️ **运维约束**：`mediaDir` 下若用软链部署，**目标必须写成相对路径**。写绝对路径会被拒绝。这必须进入部署文档，否则运维会遭遇「文件明明在，却一直 404/422」的假故障。

**机制**：标准库 `root_unix.go:60-130` 逐组件 `unix.Openat` + `O_NOFOLLOW` + `checkSymlink`，以 fd 相对遍历消除 TOCTOU。GOROOT 全量检索确认 `os` 包**未调用 `openat2`**（仅 `cmd/vendor` 下有该符号）。

⚠️ **补充实测：已打开的子文件 fd 不随 `Root.Close()` 失效**。`rt.Open(name)` 得到 `*os.File` 后即使 `rt.Close()`，该 fd 仍可正常 `ReadAt`。这正是 §7 热切换方案成立的基础。

⚠️ **补充实测：`Root.Open` 对目录会成功返回**。因此「目标是目录」必须在 service 层用 `info.IsDir()` 显式判定（→ `422 MEDIA_NOT_A_FILE`），不能指望 `Open` 报错。

**逃逸错误识别（两种形态）**：

```go
var pathErr *os.PathError
escaped := errors.As(err, &pathErr) && strings.Contains(pathErr.Err.Error(), "path escapes")
if escaped {
    return apperr.Invalid("MEDIA_PATH_ESCAPE", "Media path escapes the media root", err)
}
// 其余错误（含 `..` 组件导致的 ENOENT）归入 not-found 路径
```

⚠️ 标准库未导出 `ErrPathEscapes` 这类哨兵错误（仅在 `export_test.go` 中以 `os.ErrPathEscapes` 暴露给标准库自己的测试），只能比对 `Err` 文本。**这是脆弱点**：Go 升级若改文案，判定会失效。缓解：判定**失败时按「拒绝」处理**（fail-closed）；并在 `resolver_test.go` 中**显式断言该文案**，一旦文案变化测试立即失败。

⚠️ **`ENOENT` 型拒绝的归类（需实现者注意）**：当逃逸序列包含 `..` 组件时，标准库报的是 `no such file or directory`，与「文件真的不存在」**无法区分**。因此：

| 错误形态 | 映射 | 可接受性 |
| --- | --- | --- |
| `path escapes from parent` | `422 MEDIA_PATH_ESCAPE` | 必须 |
| `no such file or directory`（含 `..` 逃逸） | `404 MEDIA_ENTRY_NOT_FOUND` | ✅ **可接受**（不泄露「该路径被识别为逃逸」这一信息） |

⚠️ 因此 AC12 的断言口径是「**绝不 2xx**」，而不是「所有逃逸都 422」。

### 3.4 第 3 层：`st_dev` 掉盘穿透校验

#### ⚠️ 原判定式是错的（已修正）

设计初稿写的是：

```go
if stats.DeviceID != r.expectedDeviceID { /* 拒绝服务 */ }   // ❌ 错误
```

它有三个连锁缺陷，任何一个都会导致**全盘误拒**：

| # | 缺陷 | 证据 |
| --- | --- | --- |
| 1 | `expectedDeviceID` **未导出**，且 `storage.Service` 没有任何 `MediaRoot()` / `ActiveMediaDir()` 访问器 | `internal/storage/service.go:26`；全仓检索无访问器 |
| 2 | `expectedDeviceID` **只在外挂盘场景才被赋值**；媒体根位于根文件系统时它保持 `0` | `service.go:229-233`（仅 `isExternal && expectedDeviceID == 0` 时写入）；`service.go:312-314`（根设备时显式置 0） |
| 3 | `MediaDirectory` 的**默认值就在根文件系统上** | `internal/storage/model.go:40`：`"data/media"` |

叠加结果：默认配置下 `expectedDeviceID == 0`，而 `stats.DeviceID` 是真实设备号，于是 `!=` 恒真 → **每一次读取都 503**。AC13/AC14 这两个关键区分点根本无法实现。

#### 修正后的口径：与媒体根自采快照比对

```go
// selfDev：建/换句柄时媒体根自身的 st_dev（resolver 自采，见 §3.3）
// rootDev：根文件系统 st_dev（inspector.GetRootDeviceID()）
//
// 判定：只有「媒体根原本不在根设备上，而现在解析到了根设备」才是掉盘穿透。
// 媒体根本来就在根设备上 → 不做掉盘判定。
func verifyNotFallthrough(inspector DeviceInspector, mediaRoot string, selfDev uint64) error {
    stats, err := inspector.InspectFS(mediaRoot)
    if err != nil {
        return apperr.Internal("MEDIA_ROOT_UNAVAILABLE", "Media root is unavailable", err)
    }
    rootDev, err := inspector.GetRootDeviceID()
    if err != nil {
        return apperr.Internal("MEDIA_ROOT_UNAVAILABLE", "Media root is unavailable", err)
    }
    if selfDev != rootDev && stats.DeviceID == rootDev {
        return apperr.Internal("MEDIA_ROOT_UNAVAILABLE", "Media root device changed", errDismounted)
    }
    return nil
}
```

| 场景 | `selfDev` | 当前 `DeviceID` | 判定 |
| --- | --- | --- | --- |
| 媒体根在根文件系统（默认配置） | `== rootDev` | `== rootDev` | ✅ 放行（**不触发掉盘**） |
| 外挂盘正常挂载 | `!= rootDev` | `== selfDev` | ✅ 放行 |
| 外挂盘掉线，路径落到根分区 | `!= rootDev` | `== rootDev` | ❌ 503 拒绝 |
| `InspectFS` 报错 | — | — | ❌ 503 拒绝 |

⚠️ 这与 `storage` 的 dismount 规则**同构**（`service.go:229`：`expectedDeviceID != 0 && expectedDeviceID != rootDev && stats.DeviceID == rootDev`），但**不依赖它的私有状态**。

#### ⚠️ 校验时机：只在建/换句柄时，不在请求热路径

第 3 层校验**不得每请求执行**。理由：

1. `storage.DefaultPathInspector.InspectFS`（`inspector_linux.go:41`）会做 `MkdirAll` + `Stat` + `Statfs`，并调用 `findMountInfoLinux`（`inspector_linux.go:115`）**每次 `os.Open("/proc/mounts")` 并全行 Scanner 扫描**。
2. 一次 mp4 预览会产生**数十至数百次 Range 请求**（本设计在论证「不加审计」时用了同一推理，见 D6）。
3. 在 RK3568 上每请求解析一遍 `/proc/mounts` 是真实的 CPU 浪费，**正好违背 D8**。

**正确时机**：建句柄 / 热切换句柄时校验一次。理由是媒体根的设备号只在挂载/卸载事件中变化，而挂载/卸载必然导致 `os.Root` 句柄失效（fd 指向已分离的旧 superblock）。因此「句柄仍可用」本身就是「设备未变」的强证据。

⚠️ **一个值得记录的推论**：`os.Root` 持有的是**目录 fd**，路径遍历是 fd 相对 `openat`。外挂盘掉线后，fd 指向的是**已分离的旧 superblock**，**不会**重新指向同路径的新挂载点。所以「掉盘穿透」在本设计中主要发生在**句柄（重）建立**那一刻 —— 这也解释了为什么第 3 层是「建句柄守卫」而非「每请求守卫」。

#### 与 `EmergencyGate` 的关系

`storage.Service.RefreshStatus` 在检测到掉盘时会把健康状态置为 `StatusError` 并置 `CanWrite=false`（`gate.go:84-95`）。但**读取路径不能只看 `CanWrite`**——熔断（空间不足）时 `CanWrite=false`，而**读取依然应该可用**（用户正是要审核旧证据）。因此：

| 场景 | `CanWrite` | `selfDev` vs `rootDev` | 读取是否放行 |
| --- | --- | --- | --- |
| 空间不足熔断 | `false` | 媒体根本就在根设备上 | ✅ **放行**（旧文件仍可审核） |
| 只读挂载 | `false` | 媒体根本就在根设备上 | ✅ 放行（只读传输本就只读） |
| 外挂盘正常 | `true` | `selfDev != rootDev`，`DeviceID == selfDev` | ✅ 放行 |
| **掉盘穿透** | `false` | `selfDev != rootDev`，但 `DeviceID == rootDev` | ❌ **拒绝**（读到的是根分区，内容错误） |

判定依据用**独立的 `st_dev` 比较**，**不复用 `CanWrite()`**。这是本设计的一个关键区分点，也是 AC14 存在的理由。

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

// ⚠️ Root.Open 对目录会成功返回（实测）。必须在 service 层显式判定：
if info.IsDir() {
    return apperr.Invalid("MEDIA_NOT_A_FILE", "Media path is not a file", nil)   // 422
}

// ServeContent 自动处理：Range、Accept-Ranges、206、If-Range、
// If-Modified-Since、Last-Modified、ETag 协商、Content-Length
http.ServeContent(c.Writer, c.Request, info.Name(), info.ModTime(), f)
```

**为什么用 `http.ServeContent` 而不是 `c.File()`**：`c.File()` 接受**路径字符串**，会**绕过** `os.Root` 防护（它自己再 `os.Open` 一次，走普通路径解析）—— ⚠️ **必须避免的陷阱**；而 `ServeContent` 接受 `io.ReadSeeker`，正好吃 `*os.File`，且完整实现 RFC 7233 Range 语义。

⚠️ **错误响应不要穿过 `ServeContent`**：应先完成「resolver 解析 → 打开 → `IsDir` 判定」并返回 JSON 信封错误，**只在全部校验通过后才**调用 `ServeContent`。否则会把 JSON 写进一个已被 `ServeContent` 设置了 `Content-Length` 的响应。

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
| `bytes=0-0` | 206 | `bytes` | `bytes 0-0/16` | 1 字节 |
| `bytes=0-15`（正好全文件） | 206 | `bytes` | `bytes 0-15/16` | 16 字节 |
| **`bytes=0-16`（上界越界 1）** | **206** | `bytes` | `bytes 0-15/16` | 16 字节（**截断，不报错**） |
| **`bytes=-0`（suffix 长度 0）** | **206** | `bytes` | `bytes 16-15/16` | **0 字节（空体）** |
| `bytes=999-` | **416** | — | `bytes */16` | `invalid range: failed to overlap` |
| `bytes=16-`（起始==文件长） | **416** | — | `bytes */16` | `invalid range: failed to overlap` |
| `bytes=1-0`（start > end） | **416** | — | — | `invalid range` |
| `bytes=abc`（语法错误） | **416** | — | — | `invalid range` |
| `bytes=0-1,5-6`（多 Range） | 206 | — | — | `multipart/byteranges`（**不走 sendfile**） |
| `If-Range` 不匹配 | **200** | `bytes` | — | 完整体（降级为全量） |
| `HEAD` | 200 | `bytes` | — | 空，`Content-Length: 16` |

⚠️ **四处必须注意（实测发现，与常见预期不同）**：

1. **`Range: bytes=abc`（语法错误）返回 416，不是「忽略 Range 返回 200」**。标准库 `parseRange` 对语法错误直接报错，走 416。测试用例必须按 **416** 断言。
2. **416 的响应体是 `net/http` 的纯文本错误，不是统一 JSON 信封**。数据面（`content`）**有意**不走信封（它返回裸字节流），其错误响应也是裸的。Swagger 与前端契约需**明确说明**：解析 `content` 响应时**不能**假定 JSON。⚠️ 注意 416 时**不会**回写 `Accept-Ranges: bytes`（实测），而 200/206 会。
3. **`bytes=-0` 返回 206 且零字节体**（`Content-Range: bytes 16-15/16`），**不是 416**。这是 RFC 7233 语义下「suffix-length 为 0」的合法但无用的请求。前端的 Range 循环必须能处理「206 但零字节」而不空转。若希望在 handler 层前置拒绝为 416，需自己写逻辑（标准库不给）。
4. **上界越界（`bytes=0-16`）被静默截断为 `bytes 0-15/16`**，而**起始越界（`bytes=16-`）才 416**。两者不对称，测试不可混用。

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

| Code | HTTP | Kind | 场景 | 可达路径（⚠️ 防死码） |
| --- | --- | --- | --- | --- |
| `MEDIA_ROOT_UNAVAILABLE` | 503 | `KindInternal` | 媒体根不可用（未配置/无法打开/掉盘） | 配置为空；`os.OpenRoot` 失败；`InspectFS` 报错；掉盘穿透 |
| `MEDIA_KIND_INVALID` | 422 | `KindInvalid` | 首段不在白名单（`recordings`/`snapshots`/`exports`） | `evil/x.mp4` |
| `MEDIA_PATH_INVALID` | 422 | `KindInvalid` | 路径形态非法（NUL、非相对、`.`/`..` 段、空路径、超长、非法字符） | `/etc/passwd`；`a b.mp4`；空路径；`a%00b` |
| `MEDIA_PATH_ESCAPE` | 422 | `KindInvalid` | 路径逃逸出媒体根（`EXDEV` 型） | 绝对目标链接（如 `esc-abs`）；逃逸相对链接（`esc-rel`） |
| `MEDIA_ENTRY_NOT_FOUND` | 404 | `KindNotFound` | 文件不存在（**含 `..` 组件导致的 `ENOENT` 型拒绝**） | `nope.mp4`；`snapshots/up/etc/passwd` |
| `MEDIA_ENTRY_UNREADABLE` | 500 | `KindInternal` | 未识别的读取失败（**fail-closed**） | 打开后 stat 失败或未分类的文件系统底层错误 |
| `MEDIA_NOT_A_FILE` | 422 | `KindInvalid` | `content` 请求目标是目录 | `recordings/cam-01` |

⚠️ **`MEDIA_PATH_ESCAPE` 与第 1 层的关系（可达性澄清）**：第 1 层已显式拒绝 `.`/`..` 段，因此**纯文本逃逸路径在第 1 层就被拦为 `MEDIA_PATH_INVALID`**。`MEDIA_PATH_ESCAPE` 的可达路径是**符号链接**：链接名本身字符集合法（如 `esc-abs`），但解析时目标逃逸。两者职责不重叠，**不是死码**。

⚠️ **`ENOENT` 型拒绝归 `MEDIA_ENTRY_NOT_FOUND`（404）而非 `MEDIA_PATH_ESCAPE`（422）**：当逃逸序列经过 `..` 组件时，标准库报的是 `no such file or directory`，与「文件真的不存在」**无法区分**（见 §3.3）。归为 404 是**刻意的**——不向调用者泄露「该路径被识别为逃逸」这一信息，也与「文件不存在」的实际用户体验一致。

⚠️ **`MEDIA_PATH_ESCAPE` 用 422 而非 403**：这是**请求参数形态非法**（客户端构造了非法路径），不是权限不足。单用户系统里 403 会误导为「认证问题」。

⚠️ **503 不在 `KindToHTTPStatus` 的映射表里**：`internal/httputil/errors.go` 的 `KindToHTTPStatus` **没有 `ServiceUnavailable` 分支**（已核实）。`KindInternal` 会映射到 **500**。因此 503 必须用 `httputil.NewError(http.StatusServiceUnavailable, ...)` 显式构造，不能靠 `apperr.Internal(...)` 的 Kind 推导。⚠️ 三语文案注册按 **Code** 查询（`lookupMessage(code, locale)`），与 Kind 无关，因此不受影响。

⚠️ **错误信息不泄露根路径**：`MEDIA_ENTRY_NOT_FOUND` 的消息不得包含 `mediaDir` 的绝对路径（`locale.go` 文案中不插值路径），内部原因只进 Zap 日志。

### 5.1 `content` 端点的响应形态矩阵（前端契约）

| 情形 | 状态 | 响应体 |
| --- | --- | --- |
| 成功（全量） | 200 | **裸字节流** |
| 成功（Range） | 206 | **裸字节流** |
| Range 语法/范围错误 | 416 | **`text/plain`**（`net/http` 直接写，非 JSON 信封） |
| 未鉴权 | 401 | JSON 信封（`RequireAuth` 写） |
| 路径非法 / 非白名单 / 目录 | 422 | JSON 信封 |
| 文件不存在 | 404 | JSON 信封 |
| 掉盘 | 503 | JSON 信封 |

⚠️ **前端不得假定成功与失败同构**。最稳妥的做法是先看 `Content-Type`，再决定是否解析 JSON。

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

**实现**：`MediaRootProvider.MediaRoot(ctx)` 每次调用返回当前配置值（⚠️ 见 §1：当前无现成实现，建议用 `GetConfig(ctx).MediaDirectory`）；`resolver` 缓存 `(rootPath, *os.Root, selfDev)`，若与缓存不一致则重建句柄并关闭旧句柄。

⚠️ **旧句柄关闭的竞态**：进行中的 `Open` 可能持有旧 `*os.Root`。必须用 `sync.RWMutex`：读路径持 `RLock` 完成 `Open`，切换时持 `Lock` 替换并关闭。**不能**在替换时立即 `Close()` 旧句柄（会让在途请求 `EBADF`）。

⚠️ **两个实测事实使该方案成立**：

1. `rt.Open(name)` 得到的 `*os.File` 在 `rt.Close()` 之后**仍可正常读写**（实测）。因此「关闭旧根句柄」不会杀掉已在传输中的请求 —— 它们持有的是文件自己的 fd。
2. `*os.Root` 的 `Stat` / `Open` 等方法**文档明确可并发**（`Methods on Root are safe to be used from multiple goroutines simultaneously`），因此 `RLock` 下并发 `Open` 是安全的。

⚠️ **换根后 `selfDev` 必须一起重采**，否则会拿旧盘的设备号去校验新盘（反之亦然），导致热切换后立刻误判掉盘。

**不做的**：不迁移旧盘数据、不索引旧路径（`storage-guidelines.md` §1.3 禁止 TB 级跨盘拷贝）。旧盘文件在热切换后**不再可见** —— 已知限制，需在 PRD/文档写明。

---

## 8. 测试策略

### 8.1 路径防护专项（`resolver_test.go`，表驱动）

必须覆盖的攻击向量（⚠️ 夹具必须用**相对目标**造合法链接，否则会因 P1-3 的收窄而误报）：

| 用例 | 期望 |
| --- | --- |
| `../../etc/passwd` | `MEDIA_PATH_INVALID`（第 1 层显式拒绝 `..` 段） |
| `recordings/../../etc/passwd` | `MEDIA_PATH_INVALID`（同上） |
| `/etc/passwd`（绝对路径） | `MEDIA_PATH_INVALID` |
| `recordings/cam/2026-01-01/../../../etc` | `MEDIA_PATH_INVALID` |
| 含 NUL 字节 | `MEDIA_PATH_INVALID` |
| `%2F` 编码的逃逸（`..%2F..%2Fetc`，gin 解码后为 `../../etc`） | 拒绝（**gin 会解码 `%2F`**，实测确认） |
| 已解码的 `recordings/../..` | 拒绝（gin 不归一化，原样传递，实测确认） |
| 单个 `..` 段（可匹配路径段正则，**必须显式拒绝**） | `MEDIA_PATH_INVALID` |
| 单个 `.` 段（含 `recordings/./a.mp4`） | `MEDIA_PATH_INVALID` |
| **空路径**（`GET /media/content/`，`path` 为 `/`） | `MEDIA_PATH_INVALID`（⚠️ 路由层拦不住，必须 resolver 拒） |
| **双斜杠**（`recordings//a.mp4` → 空段） | `MEDIA_PATH_INVALID` |
| **尾斜杠**（`recordings/a.mp4/`） | 拒绝（实测为 `ENOTDIR`） |
| 含非法字符的段（`cam 01` 带空格） | `MEDIA_PATH_INVALID` |
| **绝对目标**符号链接指向 `/etc` | `MEDIA_PATH_ESCAPE` |
| **绝对目标**符号链接指向**根内**（`abs-in.mp4`） | `MEDIA_PATH_ESCAPE`（⚠️ 关键：根内也拒） |
| **相对目标**符号链接逃逸（`esc-rel`） | `MEDIA_PATH_ESCAPE` |
| 逃逸序列经 `..`（`snapshots/up/etc/passwd`） | `MEDIA_ENTRY_NOT_FOUND`（404，`ENOENT` 型） |
| **相对目标、根内文件链接** | ✅ 放行 |
| **相对目标、根内目录链接** | ✅ 放行 |
| 链接链（链接 → 链接 → 根内） | ✅ 放行 |
| `evil`（非白名单顶层名） | `MEDIA_KIND_INVALID` |
| 目标不存在 | `MEDIA_ENTRY_NOT_FOUND`（404） |
| **文件被删后请求（模拟 `exports/` TTL 清理）** | `MEDIA_ENTRY_NOT_FOUND`（404）——正常语义 |
| **目标是目录**（`recordings/cam-01`） | `MEDIA_NOT_A_FILE`（422）——`Root.Open` 会成功，需 `IsDir` 判定 |
| Go 逃逸错误文案断言 | 断言 `path escapes` 文案存在（Go 升级守护） |
| **「绝不 2xx」总断言** | 所有攻击向量都必须不落在 2xx（AC12 的正式口径） |

### 8.2 Range 语义（`handler_test.go`）

| 用例 | 期望 |
| --- | --- |
| 无 Range | 200 + 完整体 + `Accept-Ranges: bytes` |
| `Range: bytes=0-9` | 206 + `Content-Range: bytes 0-9/N` + 10 字节 |
| `Range: bytes=5-` | 206 + 尾部 |
| `Range: bytes=-10` | 206 + 末 10 字节 |
| **`Range: bytes=-0`** | **206 + 零字节体**（实测，非 416） |
| **`Range: bytes=0-16`（上界越界 1）** | **206 + `bytes 0-15/16`**（截断，非 416） |
| `Range: bytes=999999-` | 416 |
| `Range: bytes=16-`（起始==文件长） | 416 |
| `Range: bytes=abc`（语法错误） | **416**（实测，非 200） |
| **`Range: bytes=0-1,5-6`（多 Range）** | 206 + `multipart/byteranges`（⚠️ 不走 sendfile） |
| `If-Range` 匹配 | 206 |
| `If-Range` 不匹配 | **200**（降级为全量，非 412） |
| HEAD 请求 | 200 + 无体 + `Content-Length` + `Accept-Ranges: bytes` |
| **无 cookie 请求** | **401**（JSON 信封；`content` 是唯一错误体形态不统一的端点） |
| 目录路径请求 `content` | `MEDIA_NOT_A_FILE`（JSON 信封） |

### 8.3 掉盘穿透

用 `DeviceInspector` 桩模拟 `st_dev` 变化（⚠️ 判定依据是 `selfDev` vs `rootDev`，不是 `expectedDeviceID`）：

| 用例 | `selfDev` | 当前 `DeviceID` | 期望 |
| --- | --- | --- | --- |
| 媒体根在根文件系统（**默认配置**） | `== rootDev` | `== rootDev` | ✅ **放行**（⚠️ 原判定式在此会全盘 503） |
| 外挂盘正常 | `!= rootDev` | `== selfDev` | ✅ 放行 |
| 外挂盘掉线（路径落到根分区） | `!= rootDev` | `== rootDev` | `MEDIA_ROOT_UNAVAILABLE`（503） |
| `InspectFS` 返回错误 | — | — | `MEDIA_ROOT_UNAVAILABLE`（503） |
| `GetRootDeviceID` 返回错误 | — | — | `MEDIA_ROOT_UNAVAILABLE`（503） |
| **空间熔断（`CanWrite=false`，`st_dev` 正常）** | 媒体根在根设备上 | `== rootDev` | ✅ **仍放行**（关键区分点） |

⚠️ **AC28 专项**：连续多次 `content` 请求后，断言 `InspectFS` 的**调用次数为 0**（用计数桩），证明校验不在请求热路径上。

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
    //
    // ⚠️ 合法链接必须用**相对目标**：
    //   os.Symlink("cam-01/a.mp4", filepath.Join(root, "recordings", "rel-in.mp4"))
    //   os.Symlink("recordings/cam-01", filepath.Join(root, "dirlink"))
    //   os.Symlink("rel-in.mp4", filepath.Join(root, "recordings", "chain.mp4"))
    // ❌ 不要用 os.Symlink(filepath.Join(root, "..."), ...) 造「合法链接」
    //    —— 绝对目标即使落在根内也会被拒（实测）
    //
    // 逃逸链接：
    //   os.Symlink("/etc/passwd", filepath.Join(root, "esc-abs"))
    //   os.Symlink("../../etc/passwd", filepath.Join(root, "esc-rel"))
    //   os.Symlink("..", filepath.Join(root, "snapshots", "up"))
    ...
}
```

⚠️ **符号链接测试在部分环境不可用**（如无权限的 Windows / 受限容器）。用 `t.Skip` 而非失败，但**必须在 Linux CI 上真实运行**。

### 8.6 审计边界（AC18）

⚠️ **必须复用 `internal/app` 的真实路由范式**，不要自建最小中间件链：

- 参照 `internal/app/upload_router_test.go` 的 `TestUploadRouteProducesAuditRecord`，用 `newRouter(...)` 挂**完整中间件链**（`Recovery → RequestID → AccessLog → MaxBodyLimit → audithttp → RequireAuth`）。
- ⚠️ **必须用真实 `audithttp.Middleware` + 真实 `audit.Service`**（`quality-guidelines.md` §4.1 明确禁止测试替身）。
- ⚠️ **断言前必须 `auditSvc.Stop(ctx)` 排空**（`Record` 是非阻塞投递，未排空时「List 为空」是**假绿**）。可直接复用 `internal/app/router_test.go:273` 的 `drainAuditRouter` 写法。
- ⚠️ **必须有对照组**：同一测试内先打一个**已声明 action 的写端点**（如 `PUT /system/storage/config`）产生记录，证明链路有效，再断言 `/media/content/...` 无记录。否则「无记录」无法区分「未审计」与「中间件没挂上」。

---

## 9. 与既有规范的对照

| 规范要求 | 本设计如何满足 |
| --- | --- |
| `directory-structure.md` §2 单向依赖 | mediafile ➔ storage/httputil/apperr；app ➔ mediafile；无反向、无环 |
| `directory-structure.md` §2.6 C++ 类型不出 engine | 本包**不 import engine**（避免经 camera 间接引入） |
| `http-api-guidelines.md` §2 统一信封 | 控制面用 `httputil.Success`；数据面（content）返回裸字节，属**有意的例外**，需在 Swagger 注明 |
| `http-api-guidelines.md` §3 分页四元组 | ⚠️ **不适用**：本服务无列表端点（R0）。分页由事件证据域承担 |
| `storage-guidelines.md` §1.4 路径收敛 | 第 1 层字符集 + 第 2 层 `os.Root` 内核约束，**强于**「规范化 + 前缀断言」；⚠️ **不适用**于「前缀断言」本身 —— 本方案用内核 fd 约束取代字符串断言 |
| `quality-guidelines.md` §2 `-race` | 测试强制 `-race` |
| `quality-guidelines.md` §3 无 panic | 返回 `apperr.Error` |
| `quality-guidelines.md` §4.1 审计测试接线 | AC18 复用 `internal/app` 真实路由 + 真实 `audithttp` + `Stop` 排空（见 §8.6） |
| `error-handling.md` 三语 i18n | `locale.go` 注册全部新增码；⚠️ 按 **Code** 查询，不涉 Kind |
| `dependency-injection.md` Fx 纯粹性 | `NewMediaFileComponents` 纯构造函数，不打开文件；根句柄**首次请求**惰性建立 |
| `error-handling.md` 5xx 契约 | 503 用 `httputil.NewError(StatusServiceUnavailable, ...)` 显式构造（`KindToHTTPStatus` 无 503 分支） |

⚠️ **一处有意偏离需备案**：`error-handling.md` §4 的矩阵只写了「Internal failure → 5xx」而未细分 500/503。本设计引入 503（掉盘）作为**可恢复的服务不可用**，与 500（编程/未知错误）区分。这是**新增语义**，若评审要求收敛到 500，则 AC13 与错误码表需同步下调。

---

## 10. 需要评审确认的开放点

| # | 开放点 | 建议 |
| --- | --- | --- |
| 1 | `DeviceInspector` 是否允许引用 `storage.FSStats` | 允许。**已实测**：import `storage` 可裸跑 `go test -race`，依赖链不含 engine/camera。若要完全解耦，则在 mediafile 定义等价结构 |
| 2 | 配置热切换后旧盘文件不可见 | 接受，写入文档。不迁移数据 |
| 3 | 是否提供 `roots` 端点 | 提供，便于前端探测可用性与展示根信息 |
| 4 | 元信息字段集 | 只返回 `path`/`name`/`size`/`modTime`；`duration` 需解析容器（**违反 D8**，不做） |
| 5 | `MEDIA_PATH_ESCAPE` 的文案依赖 | 接受脆弱性 + fail-closed + 测试守护 |
| 6 | 缩略图 | **已确认不做**（D9）。硬件化需 RGA 落地 + MPP/jpegd 实测，且 JPEG 编码无硬件 |
| 7 | **`MediaRootProvider` 的实现方式** | ⚠️ **待定**。当前无现成实现（`activeMediaDir` 未导出）。建议用 `GetConfig(ctx).MediaDirectory`（不改 `storage`）；若改为新增访问器，需放宽 implement.md 的「不修改 storage 现有行为」边界 |
| 8 | **503 vs 500** | 见 §9 的偏离备案。建议保留 503（可恢复语义），否则 AC13 需改 |
| 9 | **是否限制多 Range 数量** | 建议**限制**（如最多 2 段）。多 Range 会构造 `multipart/byteranges` + goroutine + `io.Pipe`，且**不走 sendfile**；标准库无内置上限，恶意客户端可用 `Range: bytes=0-0,0-0,0-0,...` 放大开销 |
| 10 | **`bytes=-0` 是否前置拒绝** | 建议**保留标准库行为**（206 + 空体）并在前端契约中说明；若要 416 需自己写逻辑 |
| 11 | **`mediaDir` 的绝对化** | 建议在 `newResolver` 内对 `rootPath` 做 `filepath.Abs`（仅用于日志与 `InspectFS` 入参），**不改变**「DB 存相对路径」契约 |
| 12 | **任务树挂接** | 建议新建父任务承载 R9 的四项下游能力，本任务挂为其子任务（见 `prd.md` R9） |
