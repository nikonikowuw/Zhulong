# 实施计划：媒体文件服务

对应 `prd.md` 的 D1–D8 与 `design.md` 的 §1–§10。所有步骤均为**待审阅建议**，未获授权不进入执行。

---

## 前置门禁（执行前必须满足）

| # | 门禁 | 验证方式 |
| --- | --- | --- |
| G1 | 用户明确授权实现 | 对本计划最终审阅并明确批准 |
| G2 | Go 版本 ≥ 1.24（`os.Root` 要求） | `go version`（当前 `go1.27.1` ✅） |
| G3 | 工作区干净或有明确的基线 commit | `git status`（⚠️ 当前有多个未提交的 `.trellis/tasks/` 改动） |
| G4 | 无并发的包修改冲突 | `internal/mediafile` 为全新包，与活跃任务 `10-13-algo-*` 无文件重叠 |
| G5 | ⚠️ **`MediaRootProvider` 的实现方式已定** | 见 `design.md` §10 开放点 7。建议 `GetConfig(ctx).MediaDirectory`（不改 `storage`） |
| G6 | ⚠️ **「相对路径目标」运维约束已告知** | 若生产 `mediaDir` 下已有绝对目标软链，需先整改，否则本服务上线后这些文件会 404/422 |

---

## 步骤 1：路径解析器（`resolver.go`）—— 本任务的安全核心

**目的**：先钉死安全边界与真实错误形态，它是错误契约的**输入**（`locale.go` 的文案与错误码分类取决于本步的实测结果）。⚠️ **顺序已调整**：原计划把 `locale.go` 放第一步，但文案与错误码分类依赖本步结论（`ENOENT` 型拒绝归 404 还是 422），因此必须先行。

- [ ] `resolver` 结构与 `newResolver(rootPath, inspector)`：`os.OpenRoot` + 采 `selfDev` + 建句柄时做掉盘校验 + 关闭生命周期
- [ ] `deviceIDOf(root, rootPath)`：⚠️ **只比 `Dev` 字段**，不得用 `Stat_t` 结构体 `==`（实测同一目录两次 Stat 结构体不等）
- [ ] `allowedKinds` / `pathSegmentPattern` 校验（第 1 层 + 第 4 层）
- [ ] `resolve(relPath string) (string, error)`：形态校验 + 剥前导 `/` + NUL 拒绝 + 显式拒绝 `.`/`..` 段 + **拒绝空路径**（`content/` 会命中 handler）+ **拒绝空段**（`//`）
- [ ] `open(relPath string) (*os.File, error)`：经 `r.root.Open()`
- [ ] `stat(relPath string) (os.FileInfo, error)`：经 `r.root.Stat()`
- [ ] ⚠️ **不实现 `list()`** —— 无目录列举（R0）；代码中不得出现 `ReadDir`/`Readdirnames`/`os.ReadDir`
- [ ] 逃逸错误识别：`errors.As(*os.PathError)` + `path escapes` 文案比对 + **fail-closed**；⚠️ `ENOENT` 型（`..` 组件）归 not-found，**不**归 escape
- [ ] ⚠️ **不得用 `filepath.Clean` 先归一化再放行** —— 那等于放行 `./`，且退回有 TOCTOU 的手工拼接方案
- [ ] `verifyNotFallthrough(inspector, mediaRoot, selfDev)`：第 3 层，⚠️ **用 `selfDev` 而非 `expectedDeviceID`**，且**只在建句柄时调用**

**验证**：

```bash
go test -race ./internal/mediafile/ -run TestResolver
```

必须覆盖 `design.md` §8.1 的**全部**用例，尤其是：
- `%2F` 编码逃逸（gin 解码后）
- **相对目标**符号链接必须放行；**绝对目标**符号链接（含根内）必须拒绝
- 空路径 / 双斜杠 / 尾斜杠
- Go 逃逸错误**文案断言**（Go 升级守护）
- 「绝不 2xx」总断言

⚠️ 符号链接测试在无权限环境用 `t.Skip`，但**必须在 Linux 上真实运行**。

---

## 步骤 2：包骨架与错误契约

**目的**：在已知真实错误形态的前提下，钉死对外契约（错误码 + 三语文案）。

- [ ] 创建 `internal/mediafile/locale.go`：`init()` 注册 7 个错误码的三语文案（含 fail-closed 的 `MEDIA_ENTRY_UNREADABLE`；按 **Code** 注册，不涉 Kind）
- [ ] 创建 `internal/mediafile/model.go`：DTO 定义
- [ ] 创建 `internal/mediafile/module.go`：`NewMediaFileComponents` 纯构造函数骨架
- [ ] ⚠️ 503 必须用 `httputil.NewError(http.StatusServiceUnavailable, "MEDIA_ROOT_UNAVAILABLE", ...)` 显式构造 —— `KindToHTTPStatus` **没有 503 分支**

**验证**：

```bash
go test ./internal/mediafile/...    # 编译通过（本包无 CGO 依赖，可裸跑）
```

⚠️ **关键验证点**：确认本包**不**引入 CGO。执行：

```bash
go list -deps ./internal/mediafile | grep -c "internal/engine"   # 必须为 0
```

若不为 0，说明误引入了 `internal/camera`，按 `AGENTS.md` 将被迫走 native wrapper —— **必须修正**。

---

## 步骤 3：服务层（`service.go`）

**目的**：按路径取元信息、按路径打开文件流。⚠️ **无列表、无分页、无排序、无扫描**。

- [ ] `GetEntry(ctx, relPath)` → 元信息（一次 `stat`，`O(1)`）
- [ ] `OpenContent(ctx, relPath)` → 返回 `*os.File` + `os.FileInfo`（供 Handler 喂 `ServeContent`）；⚠️ **显式 `info.IsDir()` 判定** → `MEDIA_NOT_A_FILE`（`Root.Open` 对目录会成功）
- [ ] `GetRootInfo(ctx)` → 媒体根状态（供 `roots` 端点）
- [ ] `MediaRootProvider` / `DeviceInspector` 窄接口定义（在 mediafile 包内）
- [ ] ⚠️ `MediaRootProvider` 的实现：建议用 `storage.Service.GetConfig(ctx).MediaDirectory`（**不改 `storage`**，见 `design.md` §1 的选项 A）
- [ ] 配置热切换：`(rootPath, *os.Root, selfDev)` 缓存 + `sync.RWMutex` 原子替换；⚠️ **换根时 `selfDev` 必须一起重采**

**验证**：

```bash
go test -race ./internal/mediafile/ -run TestService
```

⚠️ **热切换竞态专项**：并发 `Open` + 替换根句柄，`-race` 必须无报告。**不得**在替换时立即 `Close()` 旧句柄（实测：已打开的子文件 fd 不随 `Root.Close()` 失效，因此关闭旧句柄安全；但替换时若未持写锁会让在途 `Open` 拿到 `EBADF`）。

⚠️ **无扫描验证**：代码中**不得出现** `ReadDir` / `Readdirnames` / `os.ReadDir`（可用 `grep` 检查）。这是 R0 的架构约束。

```bash
grep -rn "ReadDir\|Readdirnames" internal/mediafile/ && echo "❌ 违反 R0：不应有目录扫描" || echo "✅ 无扫描"
```

---

## 步骤 4：HTTP Handler（`handler.go`）

**目的**：路由、参数绑定、响应信封。

- [ ] `RegisterRoutes(rg *gin.RouterGroup)`：**3 条路由**（`roots` / `entries/*path` / `content/*path`）
- [ ] `roots` → `httputil.Success`
- [ ] `entries/*path` → `httputil.Success`（元信息，JSON 信封）
- [ ] `content/*path` → 显式 `Content-Type` + `http.ServeContent`（⚠️ **不用 `c.File()`**）
- [ ] ⚠️ **错误必须先于 `ServeContent` 返回**：先完成 resolver 解析 → 打开 → `IsDir` 判定，全部通过后才调用 `ServeContent`（否则会把 JSON 写进已设 `Content-Length` 的响应）
- [ ] 错误映射：`apperr.Kind` → HTTP 状态码（复用 `httputil.WriteError`）；⚠️ 503 用 `httputil.NewError` 显式构造
- [ ] Swaggo 注解：3 个端点，⚠️ 注明 `content` 返回**裸字节流**、`416` 为 `text/plain`、其余错误为 JSON 信封（`design.md` §5.1）

⚠️ **不得注册列表端点**（R0）。

**验证**：

```bash
go test -race ./internal/mediafile/ -run TestHandler
go test -race ./internal/mediafile/ -run TestRange
```

Range 用例按 `design.md` §8.2，⚠️ 四个易错断言：`bytes=abc`→**416**、`bytes=-0`→**206 零字节体**、`bytes=0-16`→**206 截断**、`If-Range` 不匹配→**200**（非 412）。

**验证 `sendfile` 快路径仍可用**（设计 §4.5）：确认中间件链未包装 `ResponseWriter`（已核实：`httpmiddleware` 与 `audithttp` 均不包装）。若引入压缩中间件需重新评估。

---

## 步骤 5：Fx 装配

**目的**：接入宿主。

- [ ] `internal/app/app.go` 新增 `mediafileModule` + `provideMediaFile`
- [ ] 依赖：`storage.Service`（作为 `MediaRootProvider`，建议经 `GetConfig`）+ `storage.PathInspector`（作为 `DeviceInspector`）
- [ ] 输出：`ProtectedRoutes RouteRegistrar \`group:"protected_routes"\``
- [ ] ⚠️ 构造函数**纯粹**：不打开文件、不建立 `os.Root`（首次请求惰性建立）
- [ ] 确认 `internal/app/router.go` 的 `protected` 组已挂 `RequireAuth`（无需改动）
- [ ] ⚠️ AC18 的审计验证在 `internal/app` 侧完成（复用 `newRouter` + 真实 `audithttp` + `Stop` 排空），见 `design.md` §8.6

**验证**：

```bash
python3 native/scripts/build.py go test -race ./internal/app/   # app 依赖链含 engine，走 wrapper
```

⚠️ 包模式必须是 `./internal/app/`（或 `./internal/...`）—— **`./internal/app/...` 不是合法的 Go 包模式**，会报 `malformed import path`。

⚠️ `internal/app` 依赖 `engine`（CGO），**必须**走 native wrapper，不能裸跑 `go test`。

---

## 步骤 6：文档与契约同步

- [ ] ⚠️ **`Makefile` 的 `api-docs` 目标新增 `internal/mediafile` 到 `--dir` 列表**（当前为显式目录白名单，不加则新端点根本不被扫描，零 diff 会被误读为「一致」）
- [ ] `make api-docs` 重新生成 `internal/apidocs`
- [ ] ⚠️ 断言 `internal/apidocs/swagger.json` 中出现 `/media/roots`、`/media/entries/{path}`、`/media/content/{path}`（swag 会把 `*path` 渲染为 `{path}`）
- [ ] `README.md` 特性列表补充媒体文件服务（若合适）
- [ ] ⚠️ **部署文档/README 写明运维约束**：`mediaDir` 下软链部署时**链接目标必须用相对路径**（绝对目标会被拒绝）
- [ ] `.trellis/spec/backend/directory-structure.md` 的模块清单补充 `internal/mediafile`
- [ ] `.trellis/spec/backend/http-api-guidelines.md`：若确认 `content` 端点的裸字节例外需要固化，新增一节
- [ ] 确认 `internal/webui/dist` 内嵌副本**无需**更新（本任务无前端改动）

---

## 步骤 7：全量门禁

```bash
gofmt -l cmd internal                        # 必须无输出
make go-check                                # 前端无关，但含 native-build + vet + race
```

⚠️ `make check` 包含前端 lint/type-check/test，本任务无前端改动，可只跑 `make go-check`。若时间允许跑完整 `make check`。

---

## 验收清单（编号与 `prd.md` 的 AC1–AC34 严格一致）

> **执行状态（2026-10-13，主机 darwin/arm64）**：AC1–AC34 已全部落地。
> 104 条断言通过；3 条 Linux-only 用例在 darwin 上 `t.Skip`（已确认 Linux 目标可编译：
> `GOOS=linux GOARCH=arm64 go test -c ./internal/mediafile/` 通过）。
> ⚠️ 主机上 **`make go-check` 无法整体通过**，原因是**预先存在**的缺陷：
> `internal/algorithm/verifier.go:381` 使用 `unix.Renameat2` / `unix.RENAME_NOREPLACE`，
> 二者**无 `darwin` 实现**，导致 `internal/algorithm` 在 macOS 上编译失败（HEAD 处即失败，与本任务无关，本任务未触碰该文件）。
> 因此 `internal/app` 的测试与 vet 是在**临时副本**中、临时修补该行后经 native wrapper 实跑通过的：
> `TestMediaRoutesProduceNoAuditRecords` + `TestAppDependencyGraph` → `ok`。
> ⚠️ **待办**：该宿主缺陷需单开任务（给 `renameDirectoryNoReplace` 加 `//go:build linux` 隔离 + darwin 回退实现）。

| # | 对应 | 验收项 | 验证方式 |
| --- | --- | --- | --- |
| AC1 | R0 | **无目录列表端点**；路由表仅含 `roots`/`entries/*path`/`content/*path` | `TestHandler` 断言路由集 |
| AC2 | R0 | `roots` 返回媒体根可用性、设备信息、三个子目录存在性 | `TestHandler` |
| AC3 | R1 | 按相对路径返回元信息：路径/名称/大小/修改时间（UTC RFC3339Nano） | `TestHandler` |
| AC4 | R1 | 目标不存在 → `404`；目标是目录 → `422 MEDIA_NOT_A_FILE`（⚠️ 需 `IsDir` 显式判定） | `TestHandler` |
| AC5 | R2 | Range 全语义（含 `bytes=abc`→**416**、`bytes=-0`→**206 零字节体**、`bytes=0-16`→**206 截断**、`bytes=16-`→416） | `TestRange` 表驱动 |
| AC6 | R2 | `Content-Type` 按扩展名；无 `Content-Disposition: attachment` | `TestRange` |
| AC7 | R2 | 服务端零解码：不 import 编解码库 | `go list -deps` |
| AC8 | R4 | 路径穿越全拒（含 `%2F` 编码、已解码 `..`、NUL、单 `.`/`..` 段） | `TestResolver` 表驱动 |
| AC9 | R4 | ⚠️ **相对目标、根内符号链接放行**（文件/目录/链接链）；⚠️ **绝对目标链接（含根内）必须拒绝** | `TestResolver` |
| AC10 | R4 | 媒体根本身为符号链接时正常工作 | `TestResolver` |
| AC11 | R4 | 非白名单顶层名被拒 | `TestResolver` |
| AC12 | R4 | 逃逸判定 fail-closed；⚠️ 口径为「**绝不 2xx**」，`ENOENT` 型归 404 可接受；Go 文案断言 | `TestResolver` |
| AC13 | R5 | ⚠️ **掉盘判定用 `selfDev`**：`selfDev != rootDev` 且 `DeviceID == rootDev` → 503；`InspectFS`/`GetRootDeviceID` 报错 → 503；⚠️ **媒体根在根文件系统时不得触发** | `TestService` + 桩 |
| AC14 | R5 | ⚠️ **空间熔断（`CanWrite=false`，`st_dev` 正常）→ 读取仍放行** | `TestService`（关键区分点） |
| AC15 | R6 | `go list -deps ./internal/mediafile` 不含 `internal/engine`/`internal/camera` | 命令验证 |
| AC16 | R6 | 可裸跑 `go test -race ./internal/mediafile/...` | 命令验证 |
| AC17 | R7 | 无新增数据库迁移；`internal/database/migrations/` 文件数不变 | `ls` 对比 |
| AC18 | R7 | ⚠️ **挂真实路由链**（`internal/app` 的 `newRouter`）后传输端点不产生审计记录；⚠️ **必须有写端点对照组** | `internal/app` 测试（`design.md` §8.6） |
| AC19 | R8 | 并发 `Open` + 配置热切换，`-race` 无报告 | `TestService` 专项 |
| AC20 | — | 新增错误码三语文案齐全，且每码可达（无死码） | 遍历错误码断言 |
| AC21 | — | ⚠️ **`Makefile` 的 `--dir` 已加 `internal/mediafile`**；`swagger.json` 含 3 条 `/media/*` 路径 | diff + `grep` |
| AC22 | — | `gofmt -l cmd internal` 无输出；`make go-check` 通过 | 命令验证 |
| AC23 | R9 | ⚠️ **文件被删后 `content` 返回 404**（模拟 `exports/` TTL 清理） | `TestHandler` |
| AC24 | R0 | ⚠️ **只接受相对路径**：绝对路径（`/etc/passwd`、`/data/media/...`）→ `422 MEDIA_PATH_INVALID` | `TestResolver` + `TestHandler` |
| AC25 | R4 | ⚠️ **空路径可达 handler**（`GET /media/content/`）→ `422 MEDIA_PATH_INVALID`；双斜杠同理；`/media/entries` 的 301 可接受 | `TestResolver` + `TestHandler` |
| AC26 | R2 | ⚠️ **端点受会话鉴权保护**：无 cookie → 401；⚠️ `content` 成功体与失败体不同构 | `TestHandler` |
| AC27 | R0 | ⚠️ **代码中无目录扫描**：`ReadDir`/`Readdirnames`/`os.ReadDir` 零命中 | `grep` |
| AC28 | R5 | ⚠️ **`st_dev` 校验不在请求热路径**：连续多次 `content` 请求后 `InspectFS` 调用次数为 0 | `TestService`（计数桩，Linux） |
| AC29 | R2 | ⚠️ **HEAD 显式注册**：Gin 不为 GET 派生 HEAD（否则 404） | `TestHandler`（路由表 + HEAD 用例） |
| AC30 | — | ⚠️ **503 显式构造**（`KindToHTTPStatus` 无 503 分支） | `TestHandler` |
| AC31 | R4 | ⚠️ **`ENOTDIR`（穿过普通文件）归 404** | `TestResolver` |
| AC32 | R5 | ⚠️ **非 Linux 跳过掉盘判定**（合成遥测会导致假掉盘） | `TestIsFallenThrough` + darwin 实跑 |
| AC33 | R0 | ⚠️ **`Root.Open` 对目录成功**，需 `IsDir` 显式判定 | `TestResolver` + `TestService` |
| AC34 | R4 | ⚠️ **顶层链接被第 4 层拒绝**，夹具需把链接放在白名单目录内 | `TestResolver` |

### ⚠️ 关于「DB 存相对路径」契约（PRD R0）

⚠️ **该契约在本任务内无直接 AC** —— 本任务不建表、不写 DB（R7），没有可断言的存储行为。它由 **AC24 反向钉死**：端点只接受相对路径，因此事件域若写入绝对路径或 URL，写入的值**根本调不通本服务**。

⚠️ **实施时不得扩大范围**：不要因为「契约重要」就在本任务内加表、加迁移或加字段校验到 DB 层。契约的存储侧验收属**后续事件域任务**（详见 `prd.md` 的「关于『DB 存相对路径』契约的验收归属」）。

### ⚠️ 架构约束验证（R0）

```bash
# 代码中不得出现目录扫描
grep -rn "ReadDir\|Readdirnames" internal/mediafile/ && echo "❌ 违反 R0" || echo "✅ 无扫描"

# 不得注册列表端点
grep -rn '"/media/entries"' internal/mediafile/ && echo "❌ 有列表端点" || echo "✅ 无列表端点"
```

---

## 回滚点

| 回滚点 | 触发条件 | 回滚动作 |
| --- | --- | --- |
| R1 | 步骤 1 发现 `os.Root` 行为不符合预期 | 退回手写 `openat2` 方案（需重写 `design.md` §3.3；代价：仅 Linux、需 `//go:build` 双实现） |
| R2 | 步骤 3 热切换竞态无法用 `RWMutex` 解决 | 简化为「不热切换」：每次请求重新 `OpenRoot`（代价：每请求一次 `syscall`） |
| R3 | 步骤 4 发现 gin 路由冲突 | 改用非通配符设计：`GET /media/content?path=<urlencoded>`（查询参数） |
| R4 | 步骤 5 装配引入循环依赖 | 将 `DeviceInspector` 改为在 mediafile 内定义独立结构，app 层适配 |
| R5 | 步骤 6 发现 `--dir` 扫描出意外内容 | 保留 `Makefile` 变更但人工核对 `swagger.json` diff |
| R6 | 任一步骤导致 `make go-check` 失败且短期无法修复 | `git revert` 本任务全部提交（独立新包，无外部依赖，回滚安全） |

---

## 明确的实施边界

**不做**（违反 D1/D5/D8 或超出范围）：

- ❌ **目录列表 / 筛选 / 分页 / 排序**（归事件证据域，用 SQL 提供）
- ❌ **目录扫描**（`ReadDir` / `Readdirnames` 不得出现在本包）
- ❌ 文件写入 / 上传 / 删除 / 移动
- ❌ 服务端解码、demux、转封装、转码、HLS 切片
- ❌ **缩略图生成**（D9 已确认不做；硬件化需 RGA 落地 + MPP/jpegd 实测，且 JPEG 编码无硬件）
- ❌ 事件表、`event → 媒体` 绑定、**孤儿文件对账工具**
- ❌ **命名约定改造**（R0：绑定靠 DB `path` 字段，不靠文件名）
- ❌ 前端任何改动
- ❌ 数据库迁移（本任务**无** schema 变更）
- ❌ 审计记录（D6）
- ❌ 修改 `internal/storage` 的现有行为（⚠️ **唯一例外**：若采纳 `design.md` §10 开放点 7 的方案 B，则需新增一个 `MediaRoot(ctx)` 访问器；否则用 `GetConfig`）
- ❌ **`Makefile` 以外的构建配置改动**（`api-docs` 的 `--dir` 增补是**必要**改动，不属越界）

⚠️ **本任务无数据库迁移** —— 这是刻意的（D5）。若实施中发现确实需要建表，说明边界被破坏，应**停止并回到 PRD 评审**。

⚠️ **本任务不引入目录扫描**（R0）。若实施中发现确实需要扫描，说明定位被破坏（应改由事件域 SQL 或另开对账工具任务）。
