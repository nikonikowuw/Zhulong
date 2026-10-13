# 修复算法包生命周期 P0 缺陷与规范偏差 — 技术设计 (Design)

> 本设计仅覆盖 PRD 范围内的 R1–R7。R2 的 broker 宿主实现与 R6 的热更新实现均已移交，不在本设计内。

## 0. 关键决策摘要

| 编号 | 决策 | 依据 |
| --- | --- | --- |
| D1 | 上传路由挂载到**独立的 value group**，使用可配置体积上限；通用 JSON 组保持 1 MiB | `middleware-guidelines.md` §3「非文件接口」 |
| D2 | `Install` 改用 `c.Request.MultipartReader()` 单遍流式，**弃用 `c.FormFile`** | 只有 `MultipartReader` 能做到单遍流式 |
| D3 | ~~broker 端点语义定案为**目录**~~ ⚠️ **已推翻**（实测）：服务端当目录、客户端当字面路径，两侧不对称。定案推迟至 `10-10` | `unix_socket.cpp:359-371` 实测：目录 → ECONNREFUSED，字面路径 → ENOENT，两者均 exit 5 |
| D4 | 增加 `manifest` **路径承载字段**（仅 id/version）字符集白名单 + `targetDir` 前缀断言（纵深防御）；`name` 只做非空 + 32 字节上限 | `filepath.Join` 逃逸已实测确认；`name` 不参与路径构造，但受 `client_name[32]` 限制 |
| D5 | `EncodeIpcMessage` 超限**返回错误**，不做分片 | 分片需 native 侧协议支持，当前无消费方 |
| D6 | `EnvFile` 接线到 `pkg.EnvPath(algoDir)` | `model.go:81` 已有约定 |
| D7 | 熔断改为 `>`，第 4 次崩溃熔断；8s 档如实标注不可达 | 用户定案：显式配额优先 |
| D8 | 新增**跨语言常量一致性检查**，防止 IPC 契约再次漂移 | 枚举已经漂移过一次 |

---

## 1. R1：上传体积门禁

### 1.1 现状与根因

`internal/app/router.go:61` 对 `/api/v1` 组无条件套用 `httpmiddleware.MaxBodyLimit()`，该中间件把请求体包成 `http.MaxBytesReader(w, body, 1<<20)`（`httpmiddleware/http.go:21,99`）。上传接口因此被截断。

**两层问题**：

1. **路由层**：文件上传路由不该受 1 MiB 约束（违反 `middleware-guidelines.md` §3 的「非文件接口」限定）；
2. **处理器层**：`handler.go:59` 用 `c.FormFile`，其内部走 `c.Request.ParseMultipartForm(c.engine.MaxMultipartMemory)`（gin v1.12.0 `context.go`），默认 32 MB 内存缓冲，超出部分由 gin 落到**自己的临时文件**；随后 `fileHeader.Open()` 再读一遍，`SaveUploadStream` 写入**第二个临时文件**。即：

   ```
   网络 → [gin 临时文件] → [SaveUploadStream 临时文件] → 验包解压目录
   ```

   峰值磁盘占用约为上传体积的 2 倍，且 `handler.go:72` 注释宣称的 "Stream directly to disk" 名不副实。**`c.FormFile` 在 API 上无法做到单遍流式**。

### 1.2 设计

**路由层**——新增 value group，与既有 `public_routes`/`protected_routes` 模式一致：

```go
// internal/app/router.go
ProtectedRoutes []RouteRegistrar `group:"protected_routes"`
UploadRoutes    []RouteRegistrar `group:"upload_routes"`   // 新增
```

`newRouter` 中建立**同级**（非子级，避免继承 1 MiB 限制）上传组：

```go
api := router.Group("/api/v1")
api.Use(httpmiddleware.AccessLog(p.Logger), httpmiddleware.MaxBodyLimit())

// 上传组：与 api 同级，不继承 1 MiB 限制
upload := router.Group("/api/v1")
upload.Use(httpmiddleware.AccessLog(p.Logger),
    httpmiddleware.MaxBodyLimitWithLimit(algorithm.MaxUploadSize))
if p.AuthService != nil {
    upload.Use(auth.RequireAuth(p.AuthService))
}
for _, r := range p.UploadRoutes {
    r.RegisterRoutes(upload)
}
```

⚠️ **`upload` 必须是 `router.Group` 而非 `api.Group`**——后者会继承已注册的 1 MiB 中间件，这正是当前缺陷的成因。

⚠️ **关键约束：路由不得重复注册。** 若 `algorithm.Handler.RegisterRoutes` 同时被挂到 `protected_routes` 与 `upload_routes` 两个 value group，所有路由会被注册两次，gin 直接 panic。因此必须**拆分注册入口**：

```go
// internal/algorithm/handler.go

// RegisterRoutes mounts all control-plane routes except file upload.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
    group := rg.Group("/algorithms")
    group.GET("", h.List)
    group.GET("/:id", h.Get)
    group.POST("/:id/enable", h.Enable)
    group.POST("/:id/disable", h.Disable)
    group.POST("/:id/reset", h.Reset)
    group.DELETE("/:id", h.Delete)
    group.GET("/bindings", h.ListBindings)
    group.POST("/bindings", h.CreateBinding)
    group.DELETE("/bindings/:id", h.DeleteBinding)
}

// RegisterUploadRoutes mounts only the large-body file upload endpoint.
// It is mounted on a separate route group whose body limit is not the
// 1 MiB JSON default. See internal/app/router.go.
func (h *Handler) RegisterUploadRoutes(rg *gin.RouterGroup) {
    rg.POST("/algorithms/install", h.Install)
}
```

`app.go` 的 `algorithmOut` 因此需要**两个** `RouteRegistrar` 输出，且两者类型相同。

✅ **已实盘验证：Fx 支持同类型多值输出到不同 group。** 既有 `networkOut`（`internal/app/app.go:193-197`）已在同一个 `fx.Out` 结构体中放置两份同类型 `RouteRegistrar`、分属 `protected_routes` 与 `public_routes`，并在 `provideNetwork`（`:199-212`）中**两者都填充**；`TestAppDependencyGraph`（`fx.ValidateApp`）在真实链接环境下实测通过。

因此**直接照抄该模式即可，无需 `fx.Annotate` 包装**：

```go
type algorithmOut struct {
    fx.Out

    Store      *algorithm.Store
    Verifier   *algorithm.Verifier
    Supervisor *algorithm.Supervisor
    Service    *algorithm.Service
    Handler    *algorithm.Handler
    Lifecycle  algorithmLifecycle

    ProtectedRoutes RouteRegistrar `group:"protected_routes"`
    UploadRoutes    RouteRegistrar `group:"upload_routes"`
}
```

⚠️ 关键点是 `provideAlgorithm` 必须**两个字段都赋值**（均为 `comp.Handler`）——只赋值一个会静默丢失路由组。`networkOut` 正是这么做的。

`httpmiddleware` 增加参数化版本，保持既有调用点不变：

```go
// MaxBodyLimitWithLimit caps API request bodies at the supplied limit.
func MaxBodyLimitWithLimit(limit int64) gin.HandlerFunc { /* 原逻辑，limit 参数化 */ }

func MaxBodyLimit() gin.HandlerFunc { return MaxBodyLimitWithLimit(MaxAPIRequestBodyBytes) }
```

**处理器层**——`Install` 改为流式解析：

```go
reader, err := c.Request.MultipartReader()   // 不经过 ParseMultipartForm
if err != nil { /* INVALID_FILE */ }
for {
    part, err := reader.NextPart()
    if errors.Is(err, io.EOF) { break }
    if part.FormName() != "file" { continue }
    tmpPath, err = SaveUploadStream(part, "")
    break
}
```

收益：单遍流式、无 gin 内存缓冲、无 gin 临时文件、磁盘峰值降为 1 倍。

### 1.3 常量单一来源

`algorithm.MaxUploadSize`（`uploader.go:19`，500 MB）为**唯一事实来源**。路由组上限与 `SaveUploadStream` 的 `io.LimitReader` 均引用它，`handler.go:76` 的 `FILE_TOO_LARGE` 响应与之对应。

`internal/app` 已导入 `internal/algorithm`（`app.go:6`），`router.go` 导入不产生循环（`algorithm` 不导入 `app`）。

### 1.4 验证

- 新增测试直接构造 `newRouter(RouterParams{...})`（真实路由，含中间件），上传 ≥ 8 MiB 归档 → 期望非 413；
- 超限用例：临时把上限调小（可测试性：`MaxUploadSize` 为常量，需通过 `SaveUploadStream` 注入或构造一个超过 500 MB 的稀疏文件——采用后者会太慢，故设计为**在 `app` 包测试中构造 `UploadRoutes` 桩 + 小上限**，或在 `algorithm` 包内以 `MaxUploadSize` 为界做单元测试）。

> ⚠️ 待实现时定夺：AC1 要求「上传超过配置上限的归档返回 `FILE_TOO_LARGE`」。500 MB 实际文件不可行，因此需要让上限可注入（例如 `SaveUploadStream(r, tempDir, limit)` 或包级变量）。**这是对既有签名的改动，需在 implement.md 中显式列出。**

---

## 2. R2：broker 端点契约定案 + 可观测性降级

### 2.1 契约定案（D3）— ⚠️ 已推翻，改为「如实记录不对称」

**初始设计断言**：`frame_broker.cpp:20` 显示 `socket_path_ = base_dir + "/zhulong_broker_" + stream_id + ".sock"`，因此 `--broker` 语义是「目录」。

**该断言只适用于服务端**。实现阶段实测发现两侧语义**不对称**：

| 侧 | 对 `--broker` 的处理 |
| --- | --- |
| 服务端 `FrameBroker::start(stream_id, base_dir)`（`frame_broker.cpp:20`） | 当**目录**，派生 `<base_dir>/zhulong_broker_<stream_id>.sock` |
| 客户端 `runner.cpp:190` → `UnixSocketClient::connect(socket_path)`（`unix_socket.cpp:359-371`） | 当**字面 socket 路径**，直接 `strncpy(addr.sun_path, path)`，**无派生** |

`native/tests/frame_broker_test.cpp:356` 能跑通是因为它把**服务端已派生好的** `broker.socket_path()` 传给 runner。

**实测证据**（`zhulong_algo_runner` + `libMockDetector.so`）：

| `--broker` 取值 | stderr | exit |
| --- | --- | --- |
| 目录 `/tmp` | `connect() failed: Connection refused` | 5 |
| 字面路径（不存在） | `connect() failed: No such file or directory` | 5 |

**因此定案**：

- ⚠️ **不**把 `module.go` 默认值改为目录。ENOENT 诚实表达「宿主不存在」，ECONNREFUSED 会误导成「宿主在跑但拒绝连接」——改目录是负向改动；
- 保留原有字面路径默认值，**零语义改动**，仅补充诚实注释；
- Go 算法层**没有 stream_id**（那是媒体层概念），无法自行派生正确的 per-stream 路径，因此不在 Go 侧发明路径；
- 目录 vs 字面路径的**真正定案**随 `10-10` 的 broker 宿主归属做出：若宿主为「一 stream 一 socket 文件」由 Go 派生，则传字面路径；若宿主启动接口是目录，则 **runner 客户端必须同步改造**。

### 2.2 宿主缺失的可观测性（核心交付）

当前故障模式：runner `client.connect` 失败 → stderr 输出 `{"status":"failed","stage":"connect"}` → **exit 5** → `watchExit` 记为普通 `StatusCrashed` → 看门狗按指数退避重启 → 熔断。运维视角下与「插件崩溃」无法区分。

**设计**：让「宿主缺失」成为一等公民信号。

`RunnerProcess` 增加失败阶段识别（**双通道**，因退出码脆弱而 stderr 标记更可靠）：

```go
// FailureStageBrokerConnect 表示 runner 无法连接 broker（宿主未启动）。
const FailureStageBrokerConnect = "connect"

// runnerExitBrokerUnavailable 是 runner.cpp 连接失败时的退出码。
const runnerExitBrokerUnavailable = 5
```

- **主通道**：`streamLogs` 扫描 stderr 行中的 `"stage":"connect"`，记录 `p.failureStage`；
- **备通道**：`watchExit` 中 `exitCode == runnerExitBrokerUnavailable` 时同样判定。

随后在 `watchExit` 的日志中区分：

```go
if p.failureStage == FailureStageBrokerConnect {
    p.logger.Error("runner exited because the frame broker host is not running",
        zap.Int("exit_code", p.exitCode),
        zap.String("broker_dir", p.config.BrokerSock),
        zap.String("reason", "broker_host_unavailable"),
        zap.String("package", p.config.PackageID),
    )
}
```

⚠️ **不做的事**：不改变看门狗的重启/熔断行为（仍是崩溃），不伪造成功。仅让日志与状态可区分。

### 2.3 契约文档更新

`native/algo-plugin-contract.md` 增加：

- `--broker` 参数语义 = **目录**；
- runner 退出码表（0/1/2/3/4/5 及各自含义）；
- **明确记录**：「FrameBroker 宿主进程与帧发布通路当前**未实现**，`FrameBroker` 仅在测试中被实例化」，并标注该缺口由 `10-10-algorithm-plugin-architecture` 承接。

---

## 3. R3：manifest 字段路径安全

### 3.1 确认的缺陷

`filepath.Join("/data/algorithms", "../../../tmp/pwned", "1.0.0")` → `/tmp/pwned/1.0.0`（**已实测**）。

影响面（`verifier.go`）：

- `:228` `targetDir` 拼接 → 安装目录逃逸；
- `:238` `_ = os.RemoveAll(targetDir)` → 配合逃逸可**删除任意目录**；
- 写入的文件来自攻击者控制的归档内容。

### 3.2 纵深防御（两层）

**第一层：字符集白名单**（`verifier.go` 新增校验，位于非空校验之后）——**仅作用于路径承载字段 `id` / `version`**：

```go
// manifestTokenPattern 限定 manifest 标识字段为安全字符集。
// 禁止路径分隔符、点段与首字符为点，杜绝 filepath.Join 逃逸。
var manifestTokenPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
```

对 `manifest.ID`、`manifest.Version` 校验。失败返回**新领域错误**：

```go
ErrInvalidManifestToken = errors.New("manifest field is invalid")
```

⚠️ 注意：`[A-Za-z0-9._-]` 允许 `..` 作为**子串**（如 `a..b`），但首字符强制为字母数字，因此 `..` 与 `../` 均不可能作为整体出现。仍需第二层。

⚠️ **`manifest.Name` 不施加该白名单**（实现阶段修正）：`Name` **不参与任何路径构造**（仅 `verifier.go:290` 存库、`handler.go:107` 审计、`service.go:154`/`supervisor.go:74,380` 作为业务标识），且是面向人的展示字段。施加白名单会拒绝 `"安全帽佩戴检测算法"` 这类合法名称。

但 `Name` 会流向 `--name` → `HandshakeReq.client_name[32]`（`ipc_protocol.hpp:37`，`runner.cpp:201` 的 `strncpy`）。**`strncpy` 静默截断**会导致两个算法包共享同一 client name，因此改为**显式拒绝超过 32 字节的名字**：

```go
const manifestNameMaxBytes = 32
if len(manifest.Name) > manifestNameMaxBytes { /* ErrInvalidManifestToken */ }
```

**第二层：路径前缀断言**（`StageAndVerify`）：

```go
targetDir := filepath.Join(finalInstallDir, manifest.ID, manifest.Version)
absBase, err := filepath.Abs(finalInstallDir)
absTarget, err := filepath.Abs(targetDir)
if err != nil || !strings.HasPrefix(absTarget, absBase+string(filepath.Separator)) {
    return nil, fmt.Errorf("%w: resolved install path escapes the install root", ErrInvalidManifestToken)
}
```

与 `uploader.go:179` 的 Zip-Slip 断言写法保持一致（同样的 `HasPrefix` + `Separator` 模式）。

### 3.3 契约与测试

- 字符集规则写入 `native/algo-plugin-contract.md`（manifest 字段约束章节）；
- 回归测试：`id` 分别为 `../../../tmp/pwned`、`..`、`/abs/path`、`a/b`、`""`、超长（65 字符）→ 全部拒绝；合法 `yolov8-person-v2`、`1.0.0` → 通过。

---

## 4. R4：IPC 契约对齐与 64KB 门禁

### 4.1 64KB 门禁（D5）

**决策：返回错误，不做分片。**

理由：分片需要 native 侧接收端的重组逻辑与协议字段（分片序号、总片数），而 `ipc_protocol.hpp:14-20` 连 gallery 消息类型都还没有。在没有消费方的情况下实现分片属**投机性设计**（`AGENTS.md` §2）。返回错误是正确的最小实现。

```go
// MaxIpcPayloadSize 镜像 native 侧 ipc_protocol.hpp 的 MAX_IPC_PAYLOAD_SIZE。
const MaxIpcPayloadSize = 64 * 1024

var ErrIpcPayloadTooLarge = errors.New("IPC payload exceeds the 64KB protocol limit")

func EncodeIpcMessage(msgType uint16, seq uint32, payload []byte) ([]byte, error) {
    if len(payload) > MaxIpcPayloadSize {
        return nil, fmt.Errorf("%w: got %d bytes", ErrIpcPayloadTooLarge, len(payload))
    }
    // ... 原逻辑
}
```

三个 gallery 构造器（`process.go:355-378`）已返回 `error`，仅需透传。

⚠️ `DecodeIpcHeader`（`process.go:57`）也应对 `PayloadSize > MaxIpcPayloadSize` 做校验——接收侧同样需要门禁（native `ipc_protocol.hpp` 已在 `handle_client_message` 侧有此防御）。

### 4.2 枚举对齐

| 值 | native (`ipc_protocol.hpp:14-20`) | Go (`process.go:28-30`) | 处置 |
| --- | --- | --- | --- |
| 0–6 | 已定义 | 未定义（仅 10/11/12） | Go 侧**不需要**——Go 是发送方，只发 gallery |
| 10/11/12 | **不存在** | `MsgGallerySync/Upsert/Delete` | 保留为**临时预留**，标注未对齐 |

**决策**：本任务**不在 native 侧新增 gallery 类型**（那属于 R6 的实现，已移交）。改为：

1. 在 `process.go` 的常量块加显式注释，说明 10/11/12 在 native 侧**尚未定义**，实现 R6 的任务必须先在 `ipc_protocol.hpp` 补齐；
2. 在 `native/algo-plugin-contract.md` 登记该未对齐项；
3. **不修改** 10/11/12 的数值（保持既有测试与序列化稳定）。

### 4.3 跨语言常量一致性检查（D8）

枚举已经漂移过一次（Go 定义了 native 不存在的 10/11/12）。加一道廉价护栏：

`internal/algorithm/ipc_contract_test.go` 新增测试，**读取** `native/src/algorithm/ipc/ipc_protocol.hpp`，用正则提取：

- `IPC_MAGIC`
- `IPC_VERSION`
- `MAX_IPC_PAYLOAD_SIZE`

并与 Go 侧 `IpcMagic`/`IpcVersion`/`MaxIpcPayloadSize` 逐一比对，不一致即失败。

⚠️ 测试需在找不到该文件时 `t.Skip`（交叉编译/无 native 源码的环境），避免成为脆弱依赖。

---

## 5. R5：`.env` 接线

`ProcessConfig.EnvFile`（`process.go:117`）从未被赋值。`model.go:81` 已有 `EnvPath(algoDir)` 约定。

**改动**：`supervisor.go` 两处 `ProcessConfig` 构造（`:72` `StartPackage`、`:147` `UpgradePackage`）补上：

```go
cfg := ProcessConfig{
    // ...既有字段
    EnvFile: pkg.EnvPath(s.algoDir),
}
```

`process.go:176` 的读取逻辑已存在且正确：文件不存在时 `LoadDotEnv` 返回错误被忽略（`if loaded, err := ...; err == nil`），`.env` 为可选，符合 PRD。

⚠️ **注意 `.env` 为可选**：`model.go:80` 注释写明 optional，因此**不得**因缺失而报错。

### 5.1 端到端验证

在测试临时目录写入可执行 shell 脚本作为 `RunnerPath`：

```sh
#!/bin/sh
echo "ZHULONG_ENV_PROBE=$ZHULONG_ENV_PROBE"
sleep 0.3
```

配合 `EnvFile` 指向含 `ZHULONG_ENV_PROBE=injected` 的 `.env`，通过 `RunnerProcess.Start()` 启动，用 `zaptest/observer` 断言捕获到的 stdout 日志包含 `ZHULONG_ENV_PROBE=injected`。

这走的是**真实** `os/exec` 环境注入路径（`process.go:187-190`），而非直接测 `LoadDotEnv`——后者正是「单测绿但接线断」的成因。

---

## 6. R6：热更新链路澄清（不实现）

### 6.1 澄清结论

| 维度 | 结论 |
| --- | --- |
| **触发时机** | 人员/人脸 CRUD 成功后（`person.Service` 的 Create/Update/Delete/AddFace/DeleteFace）；冷启动时在 runner 就绪握手后全量同步 |
| **传输通道** | **待定** —— 依赖 R2 的 broker 宿主归属。若宿主在 native engine 内，gallery 通道宜与 broker 复用同一 socket（同一条连接上多路复用消息类型）；若宿主在 Go 内，则 Go 可直接作为服务端。**该决策随 `10-10` 一并做出。** |
| **消息序列** | 全量 `SYNC`（冷启动）→ 增量 `UPSERT`/`DELETE`（运行期）；`SYNC` 后须有 `ACK`，增量可异步 |
| **ACK 语义** | 复用既有 `MSG_FRAME_ACK` 的序号机制，或新增 `MSG_GALLERY_ACK`（待 R2 定案） |
| **单条上限与分片** | 受 64KB 限制（R4）。全量同步在人员规模增长后必然超限，**分片是必须的**——但分片协议需 native 侧支持，随 R6 实现 |
| **载荷字段名** | 两侧不一致：`algorithm/process.go:333` 用 `features`，`person/model.go:66` 用 `feature_vector`。**统一为 `features`**（float32 数组语义更准确，且 `GallerySyncItem` 已使用该名） |

### 6.1.1 澄清结论已写入契约文档

上述结论（触发时机、通道待定、消息序列、ACK 语义、分片必要性、字段名映射）已登记至
`.trellis/spec/native/algo-plugin-contract.md` §7.2，作为跨语言权威依据。`person/model.go` 的
`feature_vector` 属 person 领域 DTO 命名，**不强制改名**，仅记录 `feature_vector → features` 映射关系。

### 6.2 防止「看起来已实现」

`process.go` 中 `CreateGallerySyncMsg` / `CreateGalleryUpsertMsg` / `CreateGalleryDeleteMsg` 与 `person.GetGallerySyncData` 均为**未被调用的死代码**，但形态完整，极易被后续任务误判为已完成。

**改动**：为这四处添加明确注释：

```go
// CreateGallerySyncMsg serializes a full gallery sync message into framed IPC bytes.
//
// ⚠️ NOT WIRED: no caller exists. The gallery hot-update channel (transport,
// ACK semantics, and sharding) is not implemented — see
// .trellis/tasks/10-12-algo-lifecycle-remediation/prd.md R6. Native has no
// MSG_GALLERY_* message types yet (ipc_protocol.hpp).
```

同时在 `10-12-algo-package-lifecycle/prd.md` 的 R3 条目标注「未完成」。

---

## 7. R7：熔断阈值（D7）

`recovery.go:104`：

```go
// 现状
if len(sm.crashHistory) >= sm.opts.MaxCrashes {   // 第 3 次即熔断

// 定案
if len(sm.crashHistory) > sm.opts.MaxCrashes {    // 第 4 次熔断
```

**语义**：`MaxCrashes = 3` 表示「窗口内最多允许 3 次崩溃」，第 4 次超出配额。

| 崩溃序号 | 退避 | 是否熔断 |
| --- | --- | --- |
| 1 | 1s | 否 |
| 2 | 2s | 否 |
| 3 | 4s | 否 |
| 4 | — | **是** |

⚠️ **如实记录**：8s 档（`shift=3`）在 3 次配额下**仍不可达**。退避阶梯描述公式形态，非保证执行到每一档。该事实写入 `design.md` 与本设计，**不声称 8s 可达**。

同步更新：

- `recovery_test.go:38-46` 的断言（第 3 次崩溃应重启，第 4 次熔断）；
- `10-12-algo-package-lifecycle/design.md` 中「超过阈值」的措辞（与 PRD「最多允许」对齐）。

---

## 8. 变更清单

| 文件 | 改动 |
| --- | --- |
| `internal/httpmiddleware/http.go` | 新增 `MaxBodyLimitWithLimit(limit)`；`MaxBodyLimit()` 委托之 |
| `internal/app/router.go` | 新增 `UploadRoutes` value group；建立同级 `upload` 组 |
| `internal/app/app.go` | `algorithmOut` 增加 `UploadRoutes` 输出（照抄 `networkOut` 双 group 模式） |
| `internal/algorithm/handler.go` | `Install` 改用 `MultipartReader`；拆分 `RegisterUploadRoutes` |
| `internal/algorithm/uploader.go` | 上限可注入（为可测试性）；`SaveUploadStream` 签名调整 |
| `internal/algorithm/module.go` | `brokerSock` 默认值改为目录 `/tmp` |
| `internal/algorithm/process.go` | `MaxIpcPayloadSize` + `EncodeIpcMessage`/`DecodeIpcHeader` 门禁；失败阶段识别；gallery 死代码注释 |
| `internal/algorithm/verifier.go` | 字符集白名单 + `targetDir` 前缀断言 + 新错误码 |
| `internal/algorithm/supervisor.go` | 接线 `EnvFile`；broker 缺失日志 |
| `internal/algorithm/recovery.go` | `>=` → `>` |
| `internal/algorithm/locale.go` | 新错误码注册 |
| `internal/algorithm/*_test.go` | 各 AC 对应测试 |
| `.trellis/spec/native/algo-plugin-contract.md` | broker 目录语义、退出码表、manifest 字符集、宿主未实现登记 |
| `10-12-algo-package-lifecycle/prd.md` / `design.md` | R3 未完成标注；熔断措辞修正 |

## 9. 风险与权衡

| 风险 | 说明 | 缓解 |
| --- | --- | --- |
| ⚠️ `SaveUploadStream` 签名变更 | 为可测试性需注入上限，触及既有调用点与测试 | 保留旧签名为薄封装，新增带 limit 的变体 |
| ⚠️ 上传组与通用组同为 `/api/v1` 同级 | 两组独立挂载，需确保 auth 中间件在两组都生效 | 显式在两组各自 `Use(auth.RequireAuth)`；测试覆盖未认证上传返回 401 |
| ⚠️ Fx value group 同类型多值歧义 | 已排除：`networkOut` 为既有先例，实测通过 | 照抄 `networkOut` 模式，两字段均需赋值 |
| ⚠️ 路由重复注册 panic | 若两个 group 都调用完整 `RegisterRoutes`，gin 直接 panic | 拆分 `RegisterRoutes` / `RegisterUploadRoutes`（见 §1.2） |
| ⚠️ 跨语言常量测试读 native 源码 | 无 native 源码的环境会失败 | 文件缺失时 `t.Skip` |
| ⚠️ exit code 5 依赖 native 实现细节 | native 改动退出码会静默失效 | 双通道（stderr `stage=connect` 为主）；退出码表写入契约文档 |
| ⚠️ `module.go` broker 默认值 | D3 被推翻：改目录会让错误信号从 ENOENT 变 ECONNREFUSED，更难排查 | 保留原字面路径默认值（零语义改动）+ 诚实注释；定案移交 `10-10` |
| ⚠️ `manifest.Name` 过度校验 | 初版对 `Name` 也施加路径白名单，会拒绝所有非 ASCII 展示名 | 已修正：仅 id/version 受白名单；`Name` 改为非空 + 32 字节上限 |
| ⚠️ 本任务不解决 broker 宿主 | AC3 仍不可达，算法插件无法真正消费视频流 | 契约文档显式登记；移交 `10-10`；**不谎报完成** |

## 10. 范围外复核

以下项已在 PRD §5 列为范围外，本设计**不触碰**：Swagger 注解、未注册错误码、422 绑定校验、裸数组、`time.After` 循环、构造函数副作用、回滚 REST 路由、死代码清理、零向量 stub、Makefile pnpm 分支。

⚠️ 特别说明：`quality-guidelines.md` §3 禁止 `_ = fn()`，而本次改动会**新增**若干错误处理路径。实现时必须避免引入新的 `_ = fn()`——即改动的代码不得加重既有违规。
