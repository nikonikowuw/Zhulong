# 媒体文件服务（按路径取字节与元信息）

## Goal

为 `mediaDir` 下的媒体文件提供受控只读访问能力：**按相对路径**返回文件字节（Range 传输）与元信息。
服务范围限定在媒体目录白名单内，含路径穿越防护与外挂盘掉线穿透校验。

服务端**只做字节传输**：不解码、不 demux、不转封装、不转码、不切片。
浏览器端承担 demux 与解码，以节约边缘设备 CPU。

**定位**：本服务是**纯字节层**能力——接受一个媒体目录内的相对路径，安全地返回文件内容与元信息。

```txt
【DB 是索引与语义的真源，文件系统是 blob 存储】

列表 / 筛选 / 分页 / 排序  ──➔ 事件证据域（SQL）           ← 不在本任务
按路径取字节 / 元信息     ──➔ 本服务（O(1)）            ← 本任务
```

⚠️ **本服务不提供目录列表**，因此**不存在目录扫描**（见 R0）。

## 已确认的产品决策

| ID | 决策点 | 决定 | 备注 |
| --- | --- | --- | --- |
| D1 | 交付边界 | **仅只读消费侧** | 不含文件生产方（录像 muxer / 抓拍落盘） |
| D2 | 是否包含前端页面 | **不做前端 UI 页面**，但后端必须提供前端可预览的媒体传输能力 | 用户原话：「不需要前端的，但是我有需求的，这个文件服务，我需要的是一个媒体服务，也就是前端可以预览的」 |
| D7 | 预览的媒体传输形态 | **T1：HTTP Range 直传**。本轮只交付后端传输层；前端客户端 demux（T4）另开任务 | 用户确认 |
| D8 | 性能约束 | **服务端只做字节传输**，禁止解码 / demux / 转封装 / 转码 / 切片 | 用户原话：「部署到边缘设备上，因此不能浪费开发板的性能，应该用浏览器消耗客户端的 CPU」 |
| D4a | 服务哪些子目录 | **三个都服务**：`recordings/`、`snapshots/`、`exports/` | 作为路径白名单。⚠️ 本服务**不解释**目录层级语义，层级由事件域 DB 记录承担 |
| D4b | 路径防护机制 | **`os.Root`（Go 1.24+ 标准库）**，经实测验证；取代原定的手写 `openat2` 方案 | 用户确认改用 `os.Root`。见下节实测证据 |
| D4c | 路径段校验 | **路径段字符集校验 + 显式拒绝 `.`/`..`，不查数据库** | 本服务不解释层级语义（D5 选项 A 之后重新定义） |
| D5 | 列表与索引归属 | **本服务不提供列表**（选择 A）：列表归事件证据域用 SQL 提供；本服务只按路径取字节与元信息 | 用户确认选项 A。见「D4c 与 D5 的重新定义」与 R0 |
| D3 | 是否暴露手动删除 | **不暴露删除**（选择 A） | 用户确认。空间管理已由 `storage` 的保留天数 + `POST /system/storage/cleanup` 承担；避免与 `CleanerEngine` 的 `os.Remove` 竞态及审计纠缠 |
| D6 | 传输端点是否进入审计 | **不加审计**（选择 A） | 用户确认。依据见下节 |
| D9 | 缩略图 | **本任务不做缩略图**（选择 A）。另开任务，前置依赖 RGA 落地 + MPP/jpegd 实测 | 用户确认。见下节调研 |

## 背景与仓库事实（已验证）

以下为代码检索结论，不是推测：

| 事实 | 证据位置 |
| --- | --- |
| `internal/storage` 是**存储配置/水位/清理**模块，不是文件浏览服务 | `internal/storage/handler.go` 仅暴露 `GET /system/storage/{status,config}`、`PUT /config`、`POST /test`、`POST /cleanup` |
| 媒体目录约定已存在：`mediaDir/{recordings,snapshots,exports}` | `internal/storage/cleaner.go:196,229`、`internal/storage/inspector.go:92-104` |
| **全仓库没有任何代码向 `recordings/` 写入录像** | `grep -rn "recordings"` 仅命中 storage 的统计与清理；Native `engine.h` 只有 probe/subscribe/decode，无 muxer |
| **全仓库没有任何代码向 `snapshots/` 写入抓拍** | `snapshots` 仅出现在 `storage` 的保留天数配置与目录体积统计 |
| 外挂盘掉盘穿透防护已实现 | `internal/storage/gate.go`（`EmergencyGate`）、`inspector.go` 的 `st_dev` 采集与 `GetRootDeviceID` |
| 清理协程会删除 `recordings/` 下超期或最旧文件（跳过 `.lock` / `_locked`） | `internal/storage/cleaner.go:229-275` |
| 清理协程**不碰数据库**，只做文件系统 `os.Remove` | `internal/storage/cleaner.go:155-185`（`deleteBatch` 只用 `fileRemover`） |
| `upload_routes` 路由组已存在，目前只有算法包上传在用 | `internal/app/router.go:115-128`、`internal/algorithm/handler.go:57` |
| 统一响应信封与错误映射已就绪 | `internal/httputil/{response,errors}.go`、`internal/apperr/error.go` |
| 审计为声明式中间件，**未声明 action 的请求不产生审计记录** | `internal/audit/http/scope.go` 注释与 `scope` 结构 |
| 无媒体文件相关的数据库表 | `internal/database/migrations/` 仅 `000001`–`000007` |
| `jessibuca` 静态资源仍在仓库，但**已无引用**（`d4c5cc0` 起被 WebCodecs 替换） | `web/public/vendor/jessibuca/` |

**结论**：目录骨架存在，但既无生产方也无消费方；本任务只补消费方（读取）一侧。

## D4c 与 D5 的重新定义

### 用户的原始诉求

> 「这些数据是服务业务的，我数据库需要记录这些事件，因此需要把事件 id 和这个媒体绑定的啊」

### 检索发现：三个缺口

| # | 缺口 | 证据 |
| --- | --- | --- |
| 1 | **无事件生产者** —— 谁生成 `event_id` 并写 DB？ | 全仓库无推理结果落库逻辑 |
| 2 | **无事件表** —— 没有可绑定的目标 | `internal/database/migrations/` 仅 `000001`–`000007`，建表清单为 `system_metadata` / `users` / `cameras` / `camera_streams` / `audit_logs` / `system_time_configs` / `persons` / `person_faces` / `algorithms` / `analysis_tasks`。**无告警表、无抓拍表、无识别记录表**（产品 PRD `docs/prd/prd-v1.0.md:292-293` 列为规划项） |
| 3 | ~~录像命名不支持绑定~~ → **已推翻** | 之前认为需要把 `event_id` 写进文件名。**用户指出：绑定关系应存在 DB 记录的 `path` 字段里，不靠文件名。** 因此命名约定**无需修改**。仓库已有先例：`person_faces.image_path` |

另：`internal/algorithm/result.go` 的 `InferenceResult` 含 `CameraID`、`AlgorithmID`、`PTS`、`Timestamp`，但**无 `EventID` 字段**，且 `algorithm/store.go` 只持久化算法与任务。

### 决定（选项 A）：职责分离

**本任务（媒体文件服务）= 字节层**：按 mediaDir 内的**相对路径**提供安全的文件访问。
**后续任务（事件证据域）= 语义层**：事件表、`event → 媒体相对路径` 绑定、列表查询（SQL）。⚠️ **命名约定无需改造**（见下）。

**关键契约**（为将来绑定预留，本任务即生效）：

- 本服务对外只接受 **mediaDir 内的相对路径**（如 `snapshots/cam-01/2026-10-13/evt-123.jpg`），**不接受绝对路径**。
- 因此将来事件表应存**相对路径**而非绝对路径——否则 `UpdateConfig` 热切换 `MediaDirectory` 后旧绝对路径会失效。

**代价（必须接受）**：本轮交付后，**还不能「按事件查媒体」**。前端拿到事件记录里的相对路径后，可直接调用本服务的传输端点播放。

## D4a 的目录层级（仅作白名单，不作语义解释）

三个子目录形状不同：

| 目录 | 约定路径 | 层级 |
| --- | --- | --- |
| `recordings/` | `recordings/{camera_id}/{YYYY-MM-DD}/{HH-MM-SS}.mp4` | 3 层 |
| `snapshots/` | `snapshots/{camera_id}/{YYYY-MM-DD}/{event_id}.jpg` | 3 层 |
| `exports/` | `exports/{task_id}.mp4` | 1 层扁平 |

证据：`internal/storage/inspector.go:92-94`、`cleaner.go:196,229`、`.trellis/spec/backend/storage-guidelines.md`。

⚠️ **本服务不解释这些层级**：它只校验「首段在白名单内 + 每段字符集合法」，不解析 `camera_id` / `date` / `event_id`。层级语义由事件域的 DB 记录承担。

⚠️ `exports/` 由清理协程按 48h TTL 随时删除，元信息查询与传输均可能返回 `404`——这是预期行为，不是 bug。

## D9 决定：缩略图不在本任务范围

### 用户的提议

> 「缩略图可以用硬件实现吧」

### 硬件确实存在（板端实测证据）

来自 `.trellis/tasks/10-10-algorithm-plugin-architecture/research/rk3568-board-evidence.md:103`，`rga` 电源域内实测含：

```
fdeb0000.rk_rga    ← RGA 2D 加速器
fdee0000.vepu      ← 视频编码器（H.264/H.265）
fdef0000.iep       ← 图像增强处理器
fded0000.jpegd     ← JPEG 解码器
```

⚠️ **注意是 `jpegd`（DECODER），没有 `jpege`（ENCODER）。**

### 但硬件化链路存在三个硬缺口

| # | 缺口 | 说明 |
| --- | --- |
| 1 | **JPEG 编码无硬件** | `jpegd` 只能解码；`vepu` 是**视频**编码器，输出不是 JPEG。缩略图若要 `.jpg` 格式，**最后一步必须回 CPU** |
| 2 | **MPP / jpegd 用户态完全无证据** | 板端快照中 `grep -iE "mpp\|rkvdec\|vdpu\|rkvenc\|jpege"` **零命中**。证据里只有 RGA 用户态（`librga.so` 1.10.1 + `/usr/include/rga/`）。走硬件 JPEG 解码需要 Rockchip MPP 或直接 ioctl `jpegd`，**是否有、版本、API 均未实测** |
| 3 | **RGA 尚未接入** | `native/src/backends/` **不存在**；`runtime_factory.cpp` 硬编码返回 `CpuVisionBackend`。`10-13-algo-rga-vision-backend` 仍为 `planning`，未实现 |

### 与已确认决策的冲突

| 已确认决策 | 硬件缩略图的冲突 |
| --- | --- |
| **D1：仅只读消费侧** | ❌ 必须走 CGO → Native C ABI 扩展 → `engine.h` 新增接口 |
| **D8：服务端只做字节传输** | ⚠️ 部分违反（JPEG 编码那一步仍要 CPU） |

另：板端磁盘 `/` 已用 **90%，仅剩 2.9G**（同证据文件 §1）。缩略图若需缓存到磁盘，则又引入写入方，再违 D1。

### 决定与后续

**本任务（选择 A）**：不提供缩略图端点。

**后续独立任务的前置条件**：

1. `10-13-algo-rga-vision-backend` 落地（RGA 可用）；
2. **实测 MPP / `jpegd` 可用性与 API**（当前完全无证据）；
3. 确定 JPEG 编码路径。

**关键技术洞察（供后续任务参考）**：即使全部硬件化，**JPEG 编码这一步仍要 CPU**。但缩略图尺寸很小（如 320×180），CPU 编码成本低；真正昂贵的是**从原图解码**。因此最优组合可能是：

```
jpegd 硬件解码原图（贵的那步） → RGA 硬件缩放 → CPU 编码小缩略图（便宜的那步）
```

这样既省 CPU 又无需 `jpege`，但**依赖 MPP/jpegd 实测**。

**本任务的已知体验代价**：证据列表页首屏需加载原图（抓拍 JPEG 可能数 MB），**可能很慢**。前端可用懒加载 + 浏览器缩放缓解，需在后续前端任务中评估。

## 路径防护原语（已核实）

- 现有范式：`internal/person/service.go:55` `validateImagePath`（NUL 拒绝 + `filepath.IsAbs` + `filepath.Clean` 幂等断言）。
- 规范要求：`.trellis/spec/backend/storage-guidelines.md` §1.4 —— 规范化之后必须追加**前缀断言** `strings.HasPrefix(cleanPath, cleanRoot)`，并与 native 侧 Zip-Slip 防御保持同一纵深防御标准。
- `golang.org/x/sys v0.48.0` 已提供 `unix.Openat2` 与 `unix.OpenHow`（`syscall_linux.go:129`），但**本任务不再使用**（见下）。

### D4b 实测验证：选用 `os.Root` 取代手写 `openat2`

原定方案为手写 `unix.Openat2` + `RESOLVE_BENEATH`。调研中发现 Go 1.24+ 标准库已提供 `os.Root`，实测行为完全满足原方案的语义要求，且无需双实现。

#### 实测环境

`go version go1.27.1`（另在 `darwin/arm64` 与 `linux/amd64` 上复核）。测试根为 `/tmp/roottest/media`，预置：正常嵌套文件、指向 `/etc` 的绝对目标符号链接、相对逃逸符号链接、**相对目标**的根内文件链接与目录链接、链接链、以及**绝对目标但落在根内**的链接。

#### 实测结果

| 请求路径 | 预期 | 实测结果 |
| --- | --- | --- |
| `recordings/cam-01/2026-10-13/10-00-00.mp4` | 放行 | ✅ `ALLOW` |
| `../etc/passwd` | 拒绝 | ✅ `path escapes from parent` |
| `evil-link/passwd`（符号链接指向 `/etc`） | 拒绝 | ✅ `path escapes from parent` |
| `recordings/evil-rel/passwd`（相对路径逃逸） | 拒绝 | ✅ `path escapes from parent` |
| `recordings/rel-in.mp4`（**相对**目标、根内文件链接） | 放行 | ✅ `ALLOW` |
| `recordings/chain.mp4`（链接 → 链接 → 根内） | 放行 | ✅ `ALLOW` |
| `dirlink/a.mp4`（**相对**目标、根内目录链接） | 放行 | ✅ `ALLOW` |
| `recordings/inlink/ok.txt`（**相对**目标、根内合法符号链接） | 放行 | ✅ `ALLOW` |
| `abs-in.mp4`（**绝对**目标，即使落在根内） | 拒绝 | ✅ `path escapes from parent` |
| `..` | 拒绝 | ✅ `path escapes from parent` |
| `recordings/cam-01/2026-10-13/../../../etc/passwd` | 拒绝 | ✅ 拒绝（`no such file or directory`） |
| `snapshots/up/etc/passwd`（`up` → `..`） | 拒绝 | ✅ 拒绝（`no such file or directory`） |

⚠️ **上表后两行是重要发现**：「拒绝」不总是以 `path escapes from parent` 呈现。当逃逸序列经过 `..` 组件时，标准库报的是 `ENOENT`（`no such file or directory`），**仍然是拒绝**，但错误形态不同。这直接影响 §5 错误契约与 AC12 的断言方式（见 `design.md` §3.3）。

⚠️ **「根内」不等于「放行」**：`abs-in.mp4` 的最终目标确实在媒体根内，但因为是**绝对目标**，`os.Root` 一律拒绝。因此「运维软链部署」的可用形态被收窄为：**目标必须以相对路径书写**。

#### 能力验证

| 能力 | 实测结果 |
| --- | --- |
| `Root.Stat(".")` 取根 `st_dev` | ✅ 可用（⚠️ 只比 `Dev` 字段；`Stat_t` 结构体整体 `==` 实测为 `false`） |
| `Root.Stat(name)` 取子文件元信息 | ✅ 可用 |
| `Root.Open(name)` 返回 `*os.File` | ✅ **支持 `Seek()`**，可直接喂 `http.ServeContent` |
| `Root.Open(dir)` + `ReadDir()` / `Readdirnames()` | ✅ 可列举（⚠️ 本服务**不使用**，R0 禁止目录列举） |
| 根路径**本身是符号链接** | ✅ `OpenRoot` 可用，`Name()` 返回原路径 |
| 逃逸错误可识别性（`EXDEV` 型） | ✅ `errors.As(err, *os.PathError)`，`Op = "openat"`，`Err` 文本 `path escapes from parent` |
| 逃逸错误可识别性（`ENOENT` 型） | ⚠️ `Op = "openat"`，`Err` 文本 `no such file or directory` —— **与普通文件不存在无法区分** |
| 子文件 fd 在 `Root.Close()` 之后 | ✅ 仍可读（已打开 fd 不随根句柄关闭而失效）—— 热切换方案（R8）依赖此行为 |
| `Root.Open()` 打开的是目录 | ✅ **成功返回**，需调用方用 `Stat().IsDir()` 自行判定（→ `MEDIA_NOT_A_FILE`） |

#### 机制说明（已核实标准库实现）

GOROOT 全量检索确认 `os` 包**未调用 `openat2`**（仅 `cmd/vendor` 下有该符号）。实际实现为 `root_unix.go:60-130` 的**逐组件 `unix.Openat` + `O_NOFOLLOW` + `checkSymlink`**，以 fd 相对遍历消除 TOCTOU。**安全强度与 `RESOLVE_BENEATH` 等价**，仅不使用该 syscall。

#### 方案对比

| | 手写 `openat2` | **`os.Root`** |
| --- | --- | --- |
| TOCTOU 安全 | ✅ 内核原子 | ✅ 逐组件 `openat` + `O_NOFOLLOW`，同样无窗口 |
| 平台覆盖 | ❌ 仅 Linux，需 `//go:build linux` 双实现 | ✅ Unix 全覆盖（含 macOS 开发机），**零 build tag** |
| 维护方 | 自行维护 | Go 标准库 |
| 需 `x/sys` 直接依赖 | 是 | 否 |
| 逃逸错误识别 | 自定义 errno 映射 | `errors.As(*os.PathError)` |

**已知边界（标准库文档明示）**：`os.Root` **不阻止**跨越挂载点、Linux bind mount、`/proc` 特殊文件与 Unix 设备文件。这不削弱本任务安全性——掉盘穿透防护由独立的 `st_dev` 校验承担（复用 `PathInspector`），属于另一层。

**已知边界 2（符号链接形态）**：`os.Root` 在遍历中对每个组件施加 `O_NOFOLLOW` + `checkSymlink`。结论是：

| 符号链接形态 | 结果 |
| --- | --- |
| 相对目标，最终落在根内（文件 / 目录 / 链接链） | ✅ 放行 |
| 绝对目标（即使最终落在根内） | ❌ 拒绝 |
| 相对目标，逃逸出根 | ❌ 拒绝 |

⚠️ **运维约束（必须写入部署文档）**：`mediaDir` 下若使用软链部署，**目标必须写成相对路径**。绝对目标软链会被本服务拒绝。

**附带确认的语义**：`mediaDir` 根路径**允许**是符号链接（运维常把挂载点做软链），`os.OpenRoot` 对符号链接根路径实测可用。

### D6 决定的依据：传输端点不加审计

全仓库检索确认：**没有任何只读 GET 端点声明审计**。所有 `audithttp.Tag()` 均挂在写操作上：

| 模块 | 声明审计的动作 |
| --- | --- |
| `camera` | Create / Update / Delete |
| `person` | Create / Update / Delete / FaceAdd / FaceDelete |
| `algorithm` | Install / Enable / Disable / Reset / Delete / TaskCreate / TaskDelete |
| `systemtime` | UpdateConfig / NTPSync / ManualSet |
| `storage` | UpdateConfig / ManualCleanup / TestPath / Emergency* |

且 `internal/audit/http/scope.go` 注释明确：未声明 action 的请求**完全不审计**，只读端点与健康检查直接放行。

**不加审计的直接理由**：审计表上限为 `DefaultMaxEntries = 5000`（`internal/audit/model.go:27`），超出即 `Prune` 删除最旧记录。Range 传输具有**放大器效应**——浏览器播放一个 mp4 会发出数十至数百次 Range 请求（初始化读 `moov`、每次 seek、预读缓冲），**一次预览即可产生上百条记录**，足以把审计日志中的关键写操作记录挤出。

**代价（必须接受）**：无法回答「谁在何时下载了某个证据文件」。若该合规需求成立，应由**事件证据域**承担（如事件表记录 `reviewed_by` / `reviewed_at`），而非用字节层请求推断审核行为。

## D2 / D7 / D8 的推导结果

**服务端职责**：仅提供 `Accept-Ranges`、`Range`、`206 Partial Content`、`If-Range`、`ETag`、`Last-Modified`、正确的 `Content-Type`。**不解码、不 demux、不转封装、不转码、不切片。**

**为什么客户端解码是可行的（已核实）**：

- 前端已有完整的客户端解码栈：`wire-parser.ts`（ZLM1 解析）→ `webcodecs-decoder.ts`（`VideoDecoder`，**同时支持 H.264 与 H.265**）→ `live-player.tsx`（Canvas 渲染）。
- `StreamDecoder.decode()` 消费的是 `EncodedVideoChunk`（**压缩帧**，非像素），因此 demux 的位置是自由的：可放服务端，也可放浏览器。
- 实时预览走的是 **WebCodecs `VideoDecoder`**，而非 `<video>` 元素解码器。两条路径的 HEVC 能力**不同**，不可互相推论。
- 因此**客户端 demux（后续 T4）与实时预览共享同一个解码器**，HEVC 能力一致，不受 `<video>` 元素的 HEVC 限制影响。

**后续任务（不在本轮范围）**：

1. **事件证据域**：事件表（含 `path` 字段存相对路径）、事件生产者（算法结果落库 / 抓拍落盘）、列表查询用 SQL 提供。⚠️ **命名约定保持原样**。
2. **前端客户端 demux**：引入 mp4 解析库（如 `mp4box.js`），把 mp4 样本封装成 `EncodedVideoChunk` 喂给现有 `StreamDecoder`；按时间 seek 时用 HTTP Range 只取所需字节。
3. **前端证据中心页面**：列表、筛选、预览、人工审核操作。
4. 若最终仍选择 `<video src>` 直传，需接受 **HEVC 在 Chrome/Linux 上受限**（依赖 VAAPI 硬件解码与特定 Chromium 构建），或另做服务端转封装。

**生产侧约束（不在本轮范围，但必须传递给后续 muxer 任务）**：

⚠️ 客户端 demux 需要 mp4 的 `moov` atom。边写边生成的 mp4 若把 `moov` 放在文件**末尾**（非 faststart），浏览器必须读完文件尾部再回头读取，首帧延迟与 seek 体验会明显变差。**生产侧 muxer 落盘时必须写 faststart（`moov` 前置）**，或提供可被快速定位的索引。

## D1 决定的直接后果（必须接受）

当前仓库**没有任何文件生产方**，因此本任务交付后：

- `mediaDir/{recordings,snapshots,exports}` 在真实设备上仍为空目录，除非运维手工投放文件或后续生产侧任务落地。
- 验收必须使用**临时目录 + 测试夹具**构造文件，不能依赖真实录像数据。
- 本任务的价值是**先把契约钉死**（路径安全、Range 语义、鉴权与审计边界），使生产侧与前端接入时不返工。

## Requirements

### R0 定位：纯字节层，不提供列表

本任务**不提供目录列表端点**。理由：

- **列表属于语义层**（哪些证据属于哪个事件、如何排序筛选），应由**事件证据域**用 SQL 提供。SQL 天然 `O(page)`，`total` 免费。
- 媒体文件服务只负责两件文件系统才能做的事：**按路径吐字节**（Range 传输）与**按路径取元信息**（`stat` 一次，`O(1)`）。
- 因此本任务**不存在目录扫描**，也不存在分页、排序、稳定排序等一整套问题。

```txt
【DB 是索引与语义的真源，文件系统是 blob 存储】

落盘时：  写入 mediaDir/.../xxx.mp4  →  DB 记录里存 path = "recordings/cam-01/2026-10-13/10-00-00.mp4"
列表时：  SELECT ... FROM events → LIMIT/OFFSET  → O(page)，无扫描   ← 事件证据域（后续任务）
播放时：  取记录里的 path → 文件服务按路径安全吐字节  → O(1)          ← 本任务
```

仓库已有「DB 存服务端不透明引用」的先例：`person_faces.image_path`（`migrations/000006`）。

⚠️ **但该先例需要更正**：它当前存的是服务端**绝对路径**，且自身带待重构 TODO（`internal/person/model.go:75-80`：「当前接收服务端绝对路径 ImagePath 仅为阶段性测试桩实现」）。所以它**不能**作为「存绝对路径」的先例，只能作为「DB 存服务端引用」这个抽象方向的先例 —— 其方案 B 用的是 `storageKey`，**不是裸 URL**。

#### ⚠️ 命名约定无需修改

绑定关系**不靠文件名**，而靠 DB 记录里的 `path` 字段。因此 `recordings/{camera_id}/{date}/{HH-MM-SS}.mp4`、`snapshots/{camera_id}/{date}/{event_id}.jpg`、`exports/{task_id}.mp4` 全部**保持原样**，不改命名。

#### 关键契约：DB 存**相对路径**，不存 URL

DB 记录中的 `path` 必须是 **`mediaDir` 内的相对路径**（如 `recordings/cam-01/2026-10-13/10-00-00.mp4`），**既不是绝对路径，也不是访问地址（URL）**。

**一句话原则**：URL = 前缀 + 相对路径。前缀（`/api/v1`、端点名 `/media/content`、反代部署前缀）属于 presentation 层，会独立于数据变化；写进 DB 等于让 API 形态污染持久化层。

三个候选的失效矩阵（详细论证见 `design.md` §6.1）：

| 变更事件 | 存绝对路径 | 存 URL | **存相对路径** |
| --- | --- | --- | --- |
| `UpdateConfig` 热切换 `mediaDir` | ❌ 引用坏死 | ✅ 相对部分有效 | ✅ 有效，仅 blob 不在（`404`） |
| 端点改名 / API 版本升级 / 反代加前缀 | ✅ 不受影响 | ❌ **全表失效** | ✅ 不受影响 |
| 新增对象存储后端 | ❌ 需迁移 | ❌ 需迁移且语义更乱 | ✅ 可映射为 `backend + key` |

⚠️ 还有一条硬约束：`MediaDirectory` 的**默认值就是相对路径**（`internal/storage/model.go:40`：`"data/media"`，相对 CWD）。这使「绝对路径」与「URL」两个选项**同时不可行** —— 跨 `mediaDir` 变化唯一的不变量，就是「`mediaDir` 内的相对路径」。

⚠️ 安全副作用：存 URL 会诱导消费者用 `strings.TrimPrefix` 截出相对路径再自行 `os.Open`，从而**绕过本服务的四层防护**。存相对路径则天然强制走 resolver。

**「消费者零拼接」不靠入库，靠派发**：若事件域希望响应开箱可用，应在**响应 DTO 中派生** `contentUrl` 字段，而**不是**把 URL 固化进 DB：

```json
{
  "data": {
    "path": "snapshots/cam-01/2026-10-13/evt-123.jpg",
    "contentUrl": "/api/v1/media/content/snapshots/cam-01/2026-10-13/evt-123.jpg"
  }
}
```

| 字段 | 性质 | 说明 |
| --- | --- | --- |
| `path` | **持久化字段** | DB 真源；本服务端点入参 |
| `contentUrl` | **派生字段** | 服务端拼接；端点改名只改一处代码，DB 零迁移 |

⚠️ 派生字段是**事件域响应 DTO 的建议**，不属本任务实现范围。

事件域 `path` 字段的形态契约（完整表见 `design.md` §6.1）：相对路径无前导 `/`；固定 POSIX `/`；首段必须命中白名单；**字符集与 resolver 的 `pathSegmentPattern` 一致**（`^[A-Za-z0-9_.-]{1,128}$` 逐段）；长度上限 512；多后端预留用独立 `storage_backend` 列。

⚠️ **写入时必须用同一套字符集校验**，否则 DB 会出现 resolver 拒绝的路径 → 「DB 有记录但取不到」的**假故障**（看起来像文件被删，实际是路径形态非法）。

### R1 按路径取元信息（`GET /api/v1/media/entries/*path`）

- 入参为 `mediaDir` 内的**相对路径**。
- 返回：相对路径、名称、字节大小、修改时间（UTC RFC3339Nano）。
- ⚠️ **不返回**时长、分辨率、编码等需解析容器的字段（会违反 D8）。
- 目标不存在 → `404`；目标是目录 → `422 MEDIA_NOT_A_FILE`。
- ⚠️ 空路径（`GET /media/entries/`）→ `422 MEDIA_PATH_INVALID`（必须由 resolver 拦截，不能依赖路由）。

### R2 按路径取字节（`GET /api/v1/media/content/*path`）

- 完整支持 RFC 7233 Range 语义：`Accept-Ranges`、`206 Partial Content`、`Content-Range`、`If-Range`、`Last-Modified`/`If-Modified-Since`、`416`。
- 按扩展名返回确定的 `Content-Type`（`video/mp4` / `image/jpeg` / `image/png` / 其他 `application/octet-stream`）。
- ⚠️ **不添加** `Content-Disposition: attachment`（会阻止内联预览）。
- 服务端**只搬运字节**：不解码、不 demux、不转封装、不转码、不切片、不生成缩略图。
- 目标不存在 → `404`（⚠️ **这是正常语义**，`exports/` 48h TTL 会真实发生）。
- ⚠️ **成功响应是裸字节流；错误响应不是**：`416` 由 `net/http` 直接写出纯文本（`invalid range`），鉴权与领域错误是 JSON 信封。前端不得假定成功与失败同构（详见 `design.md` §4.3）。
- ⚠️ **Range 边界**（实测，必须固定）：`bytes=-0` → `206` 且零字节体（不报错）；`bytes=0-16`（超出文件长度 16）→ `206` 截断为 `bytes 0-15/16`；`bytes=16-` → `416`。

### R3 媒体根状态（`GET /api/v1/media/roots`）

- 返回媒体根可用性、设备信息、三个子目录是否存在。
- 用途：前端探测可用性、展示根信息；以及**运维诊断**。

### R4 路径安全（四层防护）

1. **标识符形态校验**：顶层子目录白名单；路径段字符集校验（显式拒绝 `.` 与 `..` 段）；拒绝 NUL 字节；拒绝空路径（⚠️ 纯字节层不解析或校验 `YYYY-MM-DD` 等目录层级语义，层级与日期由事件域 DB 记录承担，见 D4c/D5）。
2. **`os.Root` 内核级约束**：拒绝一切逃逸出媒体根的路径，无 TOCTOU 窗口。
3. **`st_dev` 掉盘穿透校验**：媒体根设备号与**建句柄时自采快照**不符时拒绝服务（⚠️ 不在请求热路径上执行，见 R5）。
4. **顶层目录白名单**：只允许 `recordings`/`snapshots`/`exports`。

⚠️ **符号链接的放行口径（已收窄）**：只有「**相对目标且落在根内**」的链接被放行。绝对目标链接即使指向根内也会被拒。这是 `os.Root` 的既定行为，不是可配置项；必须写入部署文档（见下）。

⚠️ **部署约束（必须写入运维文档）**：`mediaDir` 下使用软链部署时，**链接目标必须写成相对路径**。

### R5 掉盘与熔断的区分

⚠️ **空间熔断（`CanWrite=false`）时读取仍必须放行**——用户正是要审核旧证据。
只有**掉盘穿透**（`st_dev` 变为根设备号）才拒绝读取。

**掉盘穿透的判定口径（已修正）**：不得复用 `storage.Service` 的私有状态，也不得写成 `stats.DeviceID != expectedDeviceID`。正确口径是**与媒体根自身的历史快照比对**：

```
建/换句柄时记录媒体根当时的 st_dev 为 selfDev
请求期校验：若 InspectFS 当前 DeviceID == 根文件系统 st_dev 且 selfDev != 根文件系统 st_dev → 拒绝
媒体根本来就位于根文件系统（selfDev == 根设备）→ 不做掉盘判定，读取放行
```

⚠️ 这是关键修正。原表述（`DeviceID != expectedDeviceID`）在默认配置下（`MediaDirectory = "data/media"`，即根文件系统）会把**每一次读取都判为掉盘**，导致全盘误拒。详见 `design.md` §3.4。

⚠️ **校验时机**：`st_dev` 校验**只在建/换句柄时执行**，不在请求热路径上。理由：媒体根设备号只会在挂载/卸载事件中变化，而 Range 传输会产生数十至数百次请求（见 D6 的同一推理）。详见 `design.md` §3.4。

### R6 不引入 CGO

本包**不得**引入 `internal/engine`（含经 `internal/camera` 的间接引入），以保持可裸跑 `go test -race`。

### R7 不建表、不审计

- 无数据库迁移，无索引表（D5）。
- 传输端点不产生审计记录（D6）。

### R8 配置热切换

- `mediaDir` 变更后，新请求使用新根；在途请求用旧 fd 完成。
- ⚠️ 旧盘文件在热切换后**不再可见**，不迁移数据（`storage-guidelines.md` §1.3 禁止跨盘拷贝）。

### R9 不提供的能力（明确划归其他任务）

| 能力 | 归属 | 理由 |
| --- | --- | --- |
| **目录列表 / 筛选 / 分页 / 排序** | **事件证据域**（后续任务） | SQL 天然 `O(page)`，且列表语义属于事件域 |
| **事件表 + `path` 绑定** | **事件证据域**（后续任务） | 需要生产者（算法推理结果落库 / 抓拍落盘） |
| **孤儿文件对账（扫描）** | **后续运维工具任务** | ⚠️ 扫描的**唯一正当用途**。DB 与文件系统无原子性（产品 PRD `prd-v1.0.md:196`），文件可能落盘而 DB 写记录失败 |
| **事件表 + 生产者 + 列表 SQL + 孤儿对账 + 前端证据中心** | **后续父任务（建议新建）** | 上表四项是本服务的**下游语义层**，它们共同依赖本任务钉死的「相对路径」契约。建议建一个父任务（如 `10-13-evidence-domain`）承载这组源需求，本任务作为其唯一先行子任务 |

⚠️ **任务树现状（待挂接）**：本任务当前 `parent: null`，而 R9 列出的四项后续任务**没有任何任务承载**，只存在于本文档的散文里。建议在进入实现前完成挂接，否则契约的消费方会再次丢失上下文。

#### ⚠️ 一致性缺口（本任务无法解决，需明确告知）

| 情况 | 后果 | 处理 |
| --- | --- | --- |
| DB 有记录，文件被清理协程删了 | 播放 `404` | ✅ 本任务保证 `404` 语义正确；前端处理 |
| 文件在，DB 无记录（写记录失败） | 文件成为**孤儿**，不可见 | ❌ 需**孤儿对账工具**（后续任务） |

## Acceptance Criteria

| # | 对应 | 验收项 |
| --- | --- | --- |
| AC1 | R0 | **无目录列表端点**；路由表仅含 `roots` / `entries/*path` / `content/*path` |
| AC2 | R0 | `roots` 返回媒体根可用性、设备信息、三个子目录存在性 |
| AC3 | R1 | 按相对路径返回元信息：相对路径/名称/大小/修改时间（UTC RFC3339Nano） |
| AC4 | R1 | 目标不存在 → `404`；目标是目录 → `422 MEDIA_NOT_A_FILE`（⚠️ `Root.Open` 对目录**会成功**，必须用 `Stat().IsDir()` 显式判定） |
| AC5 | R2 | Range 全语义：无 Range→200；`bytes=0-9`→206；`bytes=5-`→206；`bytes=-4`→206；`bytes=999-`→416；**`bytes=abc`→416**（实测）；**`bytes=-0`→206 且零字节体**（实测，边界）；**`bytes=0-16`（上界越界）→206 截断到 `bytes 0-15/16`**；`bytes=16-`→416；`If-Range` 匹配→206 / 不匹配→200；HEAD 无体且有 `Content-Length` |
| AC6 | R2 | `Content-Type` 按扩展名确定；无 `Content-Disposition: attachment` |
| AC7 | R2 | 服务端零解码：不 import 任何编解码库 |
| AC8 | R4 | 路径穿越全部拒绝：`../`、绝对路径、**`%2F` 编码逃逸**、已解码的 `recordings/../..`、NUL 字节 |
| AC9 | R4 | ⚠️ **「根内符号链接」的判定收窄为「相对目标且落在根内」**：相对目标文件链接 / 相对目标目录链接 / 链接链**必须放行**（不误伤运维软链部署）；⚠️ **绝对目标链接（即使最终落在根内）必须拒绝** |
| AC10 | R4 | 媒体根本身为符号链接时正常工作 |
| AC11 | R4 | 非白名单顶层名（如 `evil`）被拒绝 |
| AC12 | R4 | 逃逸判定 **fail-closed**；⚠️ 断言口径为「**绝不 2xx**」，且区分两种错误形态：`path escapes from parent` 型 → `422 MEDIA_PATH_ESCAPE`；`ENOENT` 型（`..` 组件导致）→ `404 MEDIA_ENTRY_NOT_FOUND` **可接受**（信息不泄露优先）。Go 版本升级导致 `path escapes` 文案变化时测试立即失败 |
| AC13 | R5 | ⚠️ **掉盘判定用媒体根自采快照 `selfDev`**（非 `storage` 的私有 `expectedDeviceID`）：`selfDev != 根设备` 且当前 `DeviceID == 根设备` → 503 拒绝；`InspectFS` 报错 → 503 拒绝；⚠️ **媒体根本就位于根文件系统（`selfDev == 根设备`）→ 不触发掉盘判定，读取放行**（防默认配置全盘误拒） |
| AC14 | R5 | ⚠️ **空间熔断（`CanWrite=false`，`st_dev` 正常）→ 读取仍放行** |
| AC15 | R6 | `go list -deps ./internal/mediafile` 不含 `internal/engine` 与 `internal/camera` |
| AC16 | R6 | 可裸跑 `go test -race ./internal/mediafile/...`（不经 native wrapper） |
| AC17 | R7 | 无新增数据库迁移；`internal/database/migrations/` 文件数不变 |
| AC18 | R7 | ⚠️ **挂真实路由链**（复用 `internal/app` 的 `newRouter` 范式）后访问传输端点，排空后 `audit.Service.List` 不产生记录；⚠️ 同一测试必须先经一个**已声明 action 的写端点**产生对照记录，证明链路有效（否则「无记录」无法区分「未审计」与「中间件没挂上」） |
| AC19 | R8 | 并发 `Open` + 配置热切换，`-race` 无报告 |
| AC20 | — | 新增错误码三语文案（`en`/`zh-Hans`/`zh-Hant`）齐全，且每个码都有可达路径（不得注册死码） |
| AC21 | — | ⚠️ `Makefile` 的 `api-docs` 目标**新增 `internal/mediafile` 到 `--dir`**（否则零 diff 是假绿）；`make api-docs` 后 `internal/apidocs/swagger.json` 中出现 `/media/roots`、`/media/entries/{path}`、`/media/content/{path}`，且 `content` 标注返回裸字节流、错误响应不是 JSON 信封 |
| AC22 | — | `gofmt -l cmd internal` 无输出；`make go-check` 通过 |
| AC23 | R9 | ⚠️ **文件被删后 `content` 返回 404**（模拟 `exports/` TTL 清理）——这是正常语义，不是 bug |
| AC24 | R0 | ⚠️ **本服务只接受相对路径**：绝对路径（`/etc/passwd`、`/data/media/...`）被 `422 MEDIA_PATH_INVALID` 拒绝 |
| AC25 | R4 | ⚠️ **空路径可达 handler**：`GET /media/content/` 的 `c.Param("path")` 为 `/`，剥前导 `/` 后为空串 → 必须 `422 MEDIA_PATH_INVALID`（不能依赖路由拦截，也不能 500）；`GET /media/entries`（无尾斜杠）的 301 重定向到 `/entries/` 属可接受行为 |
| AC26 | R2 | ⚠️ **端点受会话鉴权保护**：无 `zhulong_session` cookie → 401。⚠️ `content` 的**成功**响应是裸字节流，**错误**响应（含 401/404/416）仍是 JSON 信封或标准库纯文本 —— 前端不得假定成功与失败同构 |
| AC27 | R0 | ⚠️ **代码中不得出现目录扫描**：`ReadDir` / `Readdirnames` / `os.ReadDir` 在 `internal/mediafile/` 零命中（架构约束，不只是路由表） |
| AC28 | R5 | ⚠️ **`st_dev` 校验不在请求热路径**：连续多次 `content` 请求不触发 `/proc/mounts` 读取（`InspectFS` 只在建/换句柄时调用） |
| AC29 | R2 | ⚠️ **HEAD 必须显式注册**：Gin **不会**为 GET 路由派生 HEAD（实现期实测：HEAD 落到 `NoMethod` 返回 **404**）。`HEAD /media/content/*path` → 200 + 空体 + `Content-Length` + `Accept-Ranges: bytes` |
| AC30 | — | ⚠️ **503 不得靠 Kind 推导**：`KindToHTTPStatus` 无 `ServiceUnavailable` 分支，`KindInternal` → **500**（实现期实测）。`MEDIA_ROOT_UNAVAILABLE` 必须显式构造为 **503** |
| AC31 | R4 | ⚠️ **穿过普通文件（`ENOTDIR`）归 404**，不得归 500（实现期实测：曾误归 `MEDIA_ENTRY_UNREADABLE`）：`.../10-00-00.mp4/nested.mp4` → `404 MEDIA_ENTRY_NOT_FOUND` |
| AC32 | R5 | ⚠️ **非 Linux 宿主必须跳过掉盘判定**：`storage` 的非 Linux inspector 返回**合成常量**（device=1 / fstype=simulated），与真实 `st_dev` 相比会产生**假掉盘**并拒绝一切读取（实现期实测：macOS 上返回 503）。用编译期常量门控，并把判定抽成纯函数以便各平台测试 |
| AC33 | R0 | ⚠️ **`os.Root.Open` 对目录会成功返回**：「目标是目录」必须用 `info.IsDir()` **显式**判定（不能指望 `Open` 报错） |
| AC34 | R4 | ⚠️ **媒体根下的顶层符号链接被第 4 层拒绝**（首段不在白名单）：要测第 2 层的内核级逃逸拒绝，夹具必须把链接放在白名单目录**内部** |

### 关于「DB 存相对路径」契约的验收归属

R0 的「DB 存相对路径」契约**在本任务内不可直接验收**，原因：本任务**不建表、不写 DB**（R7），因此没有可断言的存储行为。它的验收方式是：

| 时机 | 验收方式 |
| --- | --- |
| **本任务** | AC24：端点**只接受相对路径**，绝对路径一律 `422`。这反向钉死了 DB 里的 `path` 必须存相对路径 —— 否则事件域写入的值根本调不通本服务 |
| **后续事件域任务** | 断言写入的 `path` 满足形态契约（无前导 `/`、首段白名单、字符集一致、长度 ≤ 512），且能用该值直接调通 `content` 端点 |
| **后续孤儿对账任务** | 断言 DB 中不存在 resolver 会拒绝的路径（防「假故障」） |

⚠️ 若将来发现该契约需要放宽（例如确实要支持绝对路径），必须**同时**重新评估 `UpdateConfig` 热切换语义与 §3 四层防护，不得单方面修改端点入参形态。

## 明确不在本任务范围

- **目录列表 / 筛选 / 分页 / 排序**（归事件证据域，用 SQL 提供）。
- **事件表、`path` 绑定、事件生产者**（归事件证据域）。
- **孤儿文件对账（扫描）**（归后续运维工具任务）。
- 文件生产：录像封装（muxer）、抓拍 JPEG 落盘。仓库中不存在生产方。
- 前端 UI 页面（列表 / 筛选 / 播放器 / 审核操作）。
- 前端 mp4 demux 与 WebCodecs 播放集成。
- 服务端解码、转封装、转码、HLS 切片、缩略图生成。
- 硬件编码 / NPU 相关能力。
- 存储水位、保留策略、清理引擎的修改（已由 `internal/storage` 承担）。
- **命名约定改造**（R0 已说明：绑定靠 DB `path` 字段，不靠文件名）。

## Notes

- Keep `prd.md` focused on requirements, constraints, and acceptance criteria.
- Lightweight tasks can remain PRD-only.
- For complex tasks, add `design.md` for technical design and `implement.md` for execution planning before `task.py start`.
