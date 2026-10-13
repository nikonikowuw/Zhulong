# 媒体文件服务规范（只读字节层）

> `internal/mediafile`：按媒体根内的**相对路径**提供元信息与 Range 字节传输。纯字节层，不解码、不转封装、无目录列表。

---

## 1. 定位与边界（先读这一节再改代码）

### 1.1 三层职责分离

```txt
【DB 是索引与语义的真源，文件系统是 blob 存储】

列表 / 筛选 / 分页 / 排序  ──➔ 事件证据域（SQL）        ← 不在本包
按路径取字节 / 元信息     ──➔ internal/mediafile（O(1)）← 本包
孤儿文件对账（扫描）      ──➔ 运维工具任务
```

### 1.2 本包**不做**的事（改动前先确认没越界）

- ❌ **目录列表 / 筛选 / 分页 / 排序** —— 归事件证据域，SQL 天然 `O(page)` 且 `total` 免费；
- ❌ **目录扫描** —— 生产代码中不得出现 `ReadDir` / `Readdirnames` / `os.ReadDir`（`resolver_test.go` 有强制断言）；
- ❌ 写入 / 上传 / 删除 / 移动；
- ❌ 解码、demux、转封装、转码、HLS 切片、**缩略图**（服务端只搬运字节，CPU 留给客户端）；
- ❌ 数据库表、迁移、审计记录（见 §6）。

### 1.3 唯一入参形态：媒体根内相对路径

端点**只接受**相对路径（如 `snapshots/cam-01/2026-10-13/evt-1.jpg`），**不接受绝对路径**。

⚠️ **这是与事件域的跨层契约**：事件表应存**相对路径**，既不存绝对路径也不存 URL。

- **存绝对路径**：`UpdateConfig` 热切换 `mediaDir` 后引用**坏死**（把盘插回来路径也是死的）；
- **存 URL**：端点改名 / API 升版 / 反代加前缀会让**全表失效**，且会诱导消费者用 `strings.TrimPrefix` 截出路径再自行 `os.Open`，**绕过本包的四层防护**；
- **存相对路径**：唯一不变量。响应中如需开箱可用的地址，应在 **DTO 里派生** `contentUrl` 字段，而不是固化进 DB。

⚠️ `MediaDirectory` 的默认值 `"data/media"`（`storage/model.go`）本身就是**相对 CWD** 的路径，这使「绝对路径」与「URL」两个选项同时不可行。

---

## 2. 路径防护：四层，但执行时机不同

```txt
第 1 层：路径段形态校验     —— 每请求（内存判断）
第 2 层：os.Root 内核级约束 —— 每请求（fd 相对 openat）
第 3 层：st_dev 掉盘穿透     —— 仅建/换句柄时（会读 /proc/mounts）
第 4 层：顶层目录白名单     —— 每请求（内存判断）
```

### 2.1 第 1 层：形态校验

- 拒绝空路径、NUL 字节、超长（512）；
- 拒绝 `filepath.IsAbs`；
- 每个段必须匹配 `^[A-Za-z0-9_.-]{1,128}$`；
- ⚠️ **必须显式拒绝 `.` 与 `..` 段**（它们**能匹配**上面的正则）；
- 首段必须在白名单内（`recordings` / `snapshots` / `exports`）。

⚠️ **不得用 `filepath.Clean` 归一化后放行**：那等于放行 `./` 与 `//`，且退回「拼接后再 `os.Open`」的有 TOCTOU 方案。

⚠️ **空路径必须真的拒绝**：`GET /media/content/` 的 `c.Param("path")` 为 `"/"`，剥前导 `/` 后是空串。**路由层拦不住它**，必须由第 1 层拒绝为 `422 MEDIA_PATH_INVALID`。

### 2.2 第 2 层：`os.Root`（Go 1.24+ 标准库）

```go
root, err := os.OpenRoot(rootPath)   // 根本身允许是符号链接
f, err := root.Open(relPath)         // 返回 *os.File，支持 Seek，可直接喂 http.ServeContent
```

**机制**：`root_unix.go` 逐组件 `unix.Openat` + `O_NOFOLLOW` + `checkSymlink`，以 fd 相对遍历消除 TOCTOU。**安全强度与 `openat2(RESOLVE_BENEATH)` 等价**，但无需 `//go:build linux` 双实现，也不需要直接依赖 `x/sys`。

#### ⚠️ 符号链接口径（实测，与直觉不同）

| 链接形态 | 结果 |
| --- | --- |
| **相对**目标，最终落在根内（文件 / 目录 / 链接链） | ✅ 放行 |
| **绝对**目标，**即使最终落在根内** | ❌ 拒绝 |
| 相对目标，逃逸出根 | ❌ 拒绝 |

⚠️ **「根内」不等于「放行」**。运维若在 `mediaDir` 下用软链部署，**目标必须写成相对路径** —— 这必须写入部署文档，否则运维会遭遇「文件明明在却一直 404/422」的假故障。

⚠️ **测试夹具必须用相对目标造「合法链接」**：用 `filepath.Join(root, ...)` 造出的绝对目标链接会被拒，会让「应当放行」的用例误报失败。

#### 逃逸错误有两种形态，归类不同

| 请求 | 错误 | 归类 |
| --- | --- | --- |
| 绝对目标链接 / 逃逸相对链接 | `path escapes from parent` | `422 MEDIA_PATH_ESCAPE` |
| 逃逸序列经过 `..` 组件 | `no such file or directory` | `404 MEDIA_ENTRY_NOT_FOUND`（**可接受**） |

⚠️ 标准库**未导出** `ErrPathEscapes` 哨兵（仅在 `export_test.go` 里以 `os.ErrPathEscapes` 暴露给标准库自身测试），只能比对 `Err` 文本。这是**脆弱点**：必须 fail-closed，并用测试**显式断言该文案**，Go 升级改文案时立即失败。

⚠️ `ENOENT` 型归 404 是**刻意的**：不向调用者泄露「该路径被识别为逃逸」。因此**断言口径是「绝不 2xx」**，而不是「所有逃逸都 422」。

#### 两个必须知道的 `os.Root` 行为

1. **`Root.Open` 对目录会成功返回** → 「必须是文件」要用 `info.IsDir()` **显式**判定，不能指望 `Open` 报错；
2. **已打开的子文件 fd 不随 `Root.Close()` 失效** → 这是热切换（§4）成立的基础。

#### 已知边界

`os.Root` **不阻止**跨越挂载点、Linux bind mount、`/proc` 特殊文件与 Unix 设备文件。掉盘穿透由独立的第 3 层承担。

### 2.3 第 3 层：`st_dev` 掉盘穿透（读侧）

⚠️ **完整规则见 [storage-guidelines.md §5](./storage-guidelines.md)**。要点：

- 判定用**媒体根自采快照** `selfDev`，**不得**用 `storage.Service.expectedDeviceID`（未导出，且默认配置下恒为 0 → 全盘误拒）；
- **空间熔断 / 只读挂载 / 媒体根就在根文件系统 → 读取必须放行**；
- **只在建/换句柄时校验**（`InspectFS` 会读 `/proc/mounts`，不能在 Range 热路径上）；
- **非 Linux 宿主必须跳过**（`storage` 的 fallback inspector 返回合成常量，与真实 `st_dev` 相比会产生假掉盘）。

### 2.4 第 4 层：顶层目录白名单

只允许 `recordings` / `snapshots` / `exports`。⚠️ 因此**媒体根下的顶层符号链接会被本层拒绝**（首段不在白名单），要测第 2 层的逃逸拒绝，夹具必须把链接放在白名单目录**内部**。

---

## 3. HTTP 端点契约

### 3.1 三条路由，无列表

| 方法 | 路径 | 返回 |
| --- | --- | --- |
| GET | `/api/v1/media/roots` | JSON 信封：媒体根可用性、设备信息、三个子目录存在性 |
| GET | `/api/v1/media/entries/*path` | JSON 信封：`path` / `name` / `sizeBytes` / `modifiedAt` |
| GET / **HEAD** | `/api/v1/media/content/*path` | **裸字节流**（Range / 206） |

⚠️ **HEAD 必须显式注册**：Gin **不会**为 GET 路由派生 HEAD，缺失时 HEAD 落到 `NoMethod` 返回 **404**。浏览器与媒体客户端用 HEAD 探测大小与 Range 支持，缺失会让它们误判文件不存在。

⚠️ 元信息**不返回**时长 / 分辨率 / 编码（需解析容器，违反纯字节层定位）。

### 3.2 错误契约

| Code | HTTP | Kind | 场景 |
| --- | --- | --- | --- |
| `MEDIA_ROOT_UNAVAILABLE` | **503** | `KindInternal` | 未配置 / 无法打开 / 掉盘 |
| `MEDIA_KIND_INVALID` | 422 | `KindInvalid` | 首段不在白名单 |
| `MEDIA_PATH_INVALID` | 422 | `KindInvalid` | 形态非法（NUL / 绝对 / `.` / `..` / 空 / 超长 / 非法字符） |
| `MEDIA_PATH_ESCAPE` | 422 | `KindInvalid` | 逃逸出媒体根（`EXDEV` 型） |
| `MEDIA_ENTRY_NOT_FOUND` | 404 | `KindNotFound` | 文件不存在（含 `ENOENT` 型拒绝） |
| `MEDIA_ENTRY_UNREADABLE` | 500 | `KindInternal` | 未识别的读取失败（**fail-closed**） |
| `MEDIA_NOT_A_FILE` | 422 | `KindInvalid` | `content` / `entries` 目标是目录 |

⚠️ **503 必须显式构造**：`httputil.KindToHTTPStatus` **没有 `ServiceUnavailable` 分支**，`KindInternal` 会映射为 **500**。用 `httputil.NewError(http.StatusServiceUnavailable, ...)`（参考 `writeMediaError`）。

⚠️ **`MEDIA_PATH_ESCAPE` 可达性**：第 1 层已拒绝文本形式的 `.`/`..`，因此该码的可达路径是**符号链接**（链接名字符集合规，解析时逃逸）。两者职责不重叠，**不是死码**。

⚠️ **错误信息不泄露根路径**：文案中不插值 `mediaDir`，内部原因只进 Zap。

### 3.3 裸字节例外与 Range 行为

⚠️ **完整规范见 [http-api-guidelines.md §7](./http-api-guidelines.md)**。要点：

- 成功体是裸字节，**失败体不是**；**416 是 `text/plain`**（`net/http` 直接写），且不回写 `Accept-Ranges`。前端**不得假定成功与失败同构**；
- **错误必须先于 `ServeContent` 判定完毕**（它一旦开始写就接管状态行与 `Content-Length`）；
- 用 `http.ServeContent`，**禁止 `c.File()`**（后者接受路径字符串并自行 `os.Open`，绕过第 2 层）；
- 按扩展名**显式**设置 `Content-Type`（避免 `DetectContentType` 嗅探与类型混淆）；
- **不加** `Content-Disposition: attachment`（会阻止内联预览）；
- **不加**长 `Cache-Control`（`exports/` 会被 TTL 清理，长缓存会让前端拿到已删文件的副本）。

#### Range 边界（标准库实测，与常见预期不同）

| Range | 状态 | 说明 |
| --- | --- | --- |
| `bytes=abc` | **416** | **不是**忽略 Range 返回 200 |
| `bytes=-0` | **206** + **零字节体** | 不是 416；前端循环必须容忍 |
| `bytes=0-16`（上界越界 1） | **206** 截断为 `bytes 0-15/16` | 上界越界**静默截断** |
| `bytes=16-`（起始 == 长度） | **416** | 起始越界才 416 |
| 多 Range | 206 | `multipart/byteranges`，**不走 sendfile** |
| `If-Range` 不匹配 | **200** | 降级为全量，**不是 412** |

---

## 4. 配置热切换与并发

```txt
配置变更后：
  新请求         ──➔ 用新根的 *os.Root 句柄
  已在传输中的请求 ──➔ 继续用旧句柄完成（文件已打开，fd 有效）
```

- 媒体根的真源是 `storage` 配置：每次请求经 `MediaRootProvider` 读取，**不得在本包缓存配置值**；
- `resolver` 缓存 `(rootPath, *os.Root, selfDev)`，不一致则重建；
- **换根时必须一起重采 `selfDev`**，否则会拿旧盘设备号校验新盘；
- 用 `sync.RWMutex`：读路径持 `RLock` 完成 `Open`，切换时持 `Lock` 替换并关闭。**不能**在替换时无锁 `Close()`（会让在途 `Open` 拿到 `EBADF`）；
- **不迁移旧盘数据**（`storage-guidelines.md` §1.3 禁止 TB 级跨盘拷贝）。旧盘文件在热切换后**不再可见**，属已知限制。

---

## 5. 依赖约束：禁止引入 CGO

⚠️ **本包严禁 import `internal/camera`**：`camera` 的 `probe.go` / `stream_media.go` / `stream_hub.go` / `wire.go` 均 import `internal/engine`（CGO），一旦引入，本包及其测试将被拖入 CGO 链路，**不能再裸跑 `go test -race`**。

允许的依赖：`internal/storage`（媒体根配置 + 文件系统遥测）、`internal/httputil`、`internal/apperr`、`zap`、`gin`。

⚠️ `internal/storage` 的依赖链里**确实有** `github.com/mattn/go-sqlite3`（CGO 实现），但那是**数据库驱动的 CGO**，与 `internal/engine`（FFmpeg 静态链接）不是同一条链路，**不需要** native wrapper 提供外部归档。实测确认可裸跑 `go test -race`。

验证：

```bash
go list -deps ./internal/mediafile | grep -E "internal/(engine|camera)"   # 必须无输出
go test -race ./internal/mediafile/...                                    # 必须可裸跑
```

---

## 6. 不建表、不审计

- **无数据库迁移**：本服务不解释目录层级语义，也不查 DB。`internal/database/migrations/` 不得新增文件；
- **传输端点不产生审计记录**：全仓只读 GET 端点均未声明审计 action，且审计表上限为 `DefaultMaxEntries = 5000`。Range 传输有**放大器效应**（一次预览可产生上百次 Range 请求），加审计会把关键写操作记录挤出。

⚠️ 若确需回答「谁在何时下载了某个证据文件」，应由**事件证据域**承担（在事件表记录 `reviewed_by` / `reviewed_at`），而**不是**用字节层请求推断审核行为。

⚠️ **审计边界测试必须挂真实路由链**：用 `internal/app` 的 `newRouter` + 真实 `audithttp.Middleware` + 真实 `audit.Service`，断言前先 `Stop(ctx)` **排空**异步队列（`Record` 是非阻塞投递，未排空时「List 为空」是**假绿**）。**必须有写端点对照组**，否则「无记录」无法区分「未审计」与「中间件没挂上」。参考 `internal/app/media_router_test.go`。

---

## 7. 测试与门禁

| 项 | 要求 |
| --- | --- |
| `-race` | 强制。并发 `Open` + 热切换必须有专项竞态测试 |
| 路径防护 | 表驱动，覆盖 `%2F` 编码逃逸（**gin 会解码 `%2F`**）、已解码 `..`（**gin 不归一化**）、NUL、单 `.`/`..` 段、空路径、双斜杠、尾斜杠 |
| 符号链接 | **相对目标必须放行**；**绝对目标（含根内）必须拒绝**；逃逸文案断言（Go 升级守护）；环境不支持软链时 `t.Skip`，但**必须在 Linux CI 真实运行** |
| 无扫描 | 断言生产代码中无 `ReadDir` / `Readdirnames` |
| Range | 按 §3.3 的边界表断言，尤其 `bytes=abc`→416、`bytes=-0`→206 零体、`bytes=0-16`→206 截断、`If-Range` 不匹配→200 |
| 测试夹具 | 仓库**无文件生产方**，必须用 `t.TempDir()` 构造 |
| 门禁 | `gofmt -l cmd internal` 无输出；`make go-check` 通过 |

---

## 8. 常见陷阱速查

| 陷阱 | 后果 | 正确做法 |
| --- | --- | --- |
| `c.File(path)` | 绕过 `os.Root` 防护 | `http.ServeContent(w, r, name, modTime, *os.File)` |
| 错误在 `ServeContent` 之后写 | 响应被破坏（JSON 混进定长体） | 全部校验先于 `ServeContent` |
| 只注册 GET，忘了 HEAD | HEAD 返回 404，客户端误判文件不存在 | 显式 `group.HEAD(...)` |
| `stats.DeviceID != expectedDeviceID` | 默认配置下每次读取都 503 | 与自采 `selfDev` 比对（见 §2.3） |
| `Stat_t` 结构体 `==` | 同一目录两次 Stat 不等 | 只比 `Dev` 字段 |
| 每请求跑 `InspectFS` | 每请求读 `/proc/mounts`，违背省 CPU 目标 | 仅建/换句柄时校验 |
| `filepath.Clean` 后拼接再 `os.Open` | 重新引入 TOCTOU | 统一经 `os.Root` |
| 新包漏加 `Makefile` 的 `api-docs --dir` | Swagger 静默缺项，零 diff 被误读为一致 | 同步 `--dir` 并断言新路径出现在 `swagger.json` |
| 用 `apperr.Internal` 期望 503 | 实际返回 500（无 503 映射分支） | `httputil.NewError(http.StatusServiceUnavailable, ...)` |
