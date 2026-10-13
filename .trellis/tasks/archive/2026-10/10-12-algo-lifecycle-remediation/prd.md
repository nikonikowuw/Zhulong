# 修复算法包生命周期 P0 缺陷与规范偏差 (PRD)

## 1. 目标与背景

`10-12-algo-package-lifecycle` 的双轴代码评审（Standards + Spec，基准点 `HEAD` 4dbdf7e）暴露出一批缺陷。本任务收敛其中的**阻断级（P0）**与**必须先澄清的 spec 偏差**，让算法包生命周期能力从「测试绿但生产不可用」变为「端到端可跑通」。

### 核心问题：实现与真实运行路径脱节

评审发现一个共同模式——**单元测试自建了与生产不同的环境，因此全部通过，而生产路径从未被走通**：

| 现象 | 测试为何没抓到 |
| --- | --- |
| 上传接口在真实网关下必然失败 | `handler_test.go` 自建 router，绕过了 `app/router.go` 的中间件 |
| Runner 进程永远起不来 | `process_test.go` 用 `sleep` 桩进程，从不走 `runner.cpp` 的参数解析 |
| 底库热更新从未下发 | 只测了序列化字节，没测「谁把它写进 socket」 |
| 回滚路径不可达 | `UpgradePackage` 无调用方，也无 REST 路由 |

因此本任务的验收标准刻意要求**端到端证据**，而非新增单元测试。

## 2. 核心需求 (Requirements)

### R1. 上传体积门禁与 multipart 流式落盘兼容（P0）

`internal/app/router.go:61` 给整个 `/api/v1` group 套了 `httpmiddleware.MaxBodyLimit()`，其上限为 `MaxAPIRequestBodyBytes = 1 << 20`（1 MiB，`internal/httpmiddleware/http.go:21`）。该中间件同时做两件事（`http.go:93-99`）：

1. `ContentLength > 1MiB` → 直接 `413`；
2. `c.Request.Body = http.MaxBytesReader(w, body, 1MiB)` → 截断所有未知长度的请求体。

而 `internal/algorithm/uploader.go` 的设计上限是 500 MB（`handler.go:76` 的 `FILE_TOO_LARGE` 分支即为此），`internal/app/router.go` 中没有任何针对上传路由的豁免。

**结论**：`POST /api/v1/algorithms/install` 在真实网关下对任何体积超过 1 MiB 的算法包返回 413/400，500 MB 上限与 `SaveUploadStream` 的流式设计在生产中不可达。`gin` 的 `c.FormFile` 走 `ParseMultipartForm`，同样受 `MaxBytesReader` 约束，因此流式落盘路径根本进不去。

**要求**：

- 为上传类路由提供**独立于通用 JSON 上限**的体积策略，不得把整个 `/api/v1` 的 1 MiB 上限一并放宽；
- 保持 multipart 流式落盘语义（不得改为先整体读入内存）；
- 上传体积上限、超限响应码与 `uploader.go` 的 `MaxUploadSize` 必须来自**同一个常量源**，消除两处魔法数字。

### R2. FrameBroker 端点契约不一致，且**没有任何生产宿主**（P0，范围已扩大）

⚠️ **复核结论比初始评审更严重**：这不只是「路径格式不一致」，而是**整条 broker 链路在生产中根本不存在**。

**证据链**：

1. **Go 侧把 broker socket 当成固定文件路径**：`internal/algorithm/module.go:76` 默认 `"/tmp/zhulong_framebroker.sock"`；`internal/algorithm/process.go:162-164` 非空时拼 `--broker <该值>` 传给 runner。
2. **native 侧把同一参数当成目录前缀**：`native/src/algorithm/ipc/frame_broker.cpp:20` 执行 `socket_path_ = base_dir + "/zhulong_broker_" + stream_id + ".sock"`。
3. **`FrameBroker` 类在生产代码中零实例化**：全仓库 `grep` 显示，唯一构造点是 `native/tests/frame_broker_test.cpp`（测试文件）。`native/CMakeLists.txt` 里**没有** broker 宿主可执行目标——只有 `zhulong_algo_runner`（第 186 行，客户端角色）。
4. **Go 侧没有任何帧发布通路**：`internal/` 下搜不到 `publish`，C ABI（`native/include/Zhulong/*.h`）也没有暴露任何 broker/帧分发入口。
5. **前序任务已声明完成但集成为空**：`10-12-algo-framebroker-shm` 的 design.md 第 7 行画的是「主媒体服务进程 (Host: ZhulongEngine / Go)」，但该任务的范围外声明写着「本任务不实现完整的 Go 业务持久化与管理 API（留给阶段三进行 Go 包装与进程编排）」——即把集成推给了**本任务的前身** `10-12-algo-package-lifecycle`，而后者只硬编码了一个路径就宣告完成。

**后果**：即使把路径格式改对，runner 也会连上一个**无人监听的 socket**；且没有任何进程会把视频帧写进共享内存槽位。AC3「一包一进程守护」在当前代码下不可能端到端成立。

**故障模式复核**（纠正初始评审的表述）：初始评审称「runner 打印 usage 并 exit 0」。**这是错的**——`module.go:76` 提供了非空默认值，所以 `--broker` 必然被传递，`runner.cpp:322` 的条件成立，会进入 `run_ipc_worker`，在 `client.connect` 失败后输出 `{"status":"failed","stage":"connect"}` 并 **exit 5**（`runner.cpp:190-196`）。真实故障模式是**静默的 crash loop**（表现为反复重启、状态看起来像普通崩溃），而不是优雅退出——比 exit 0 更难排查。

**范围 A 决策：本任务不实现 broker 宿主，改为如实降级。**

⚠️ **D3 决策已被实现阶段推翻（实测确认）**：初始设计断言「`--broker` 语义 = 目录」，依据是 `frame_broker.cpp:20` 的派生规则。**该依据只适用于 FrameBroker 服务端方法**。

| 侧 | 对 `--broker` 的处理 |
| --- | --- |
| **服务端** `FrameBroker::start(stream_id, base_dir)`（`frame_broker.cpp:20`） | 把入参当**目录**，派生 `<base_dir>/zhulong_broker_<stream_id>.sock` |
| **客户端** `runner.cpp:190` → `UnixSocketClient::connect(socket_path)`（`unix_socket.cpp:359-371`） | 把入参当**字面 socket 路径**，直接 `strncpy(addr.sun_path, path)`，**无任何派生** |

两侧语义**不对称**。`native/tests/frame_broker_test.cpp:356` 之所以能跑通，是因为它把**服务端已派生好的** `broker.socket_path()` 传给 runner。

**独立实测证据**（`build/native/host/zhulong_algo_runner` + `libMockDetector.so`）：

| `--broker` 取值 | 实测结果 |
| --- | --- |
| 目录 `/tmp` | `{"stage":"connect","error":"connect() failed: Connection refused"}` → **exit 5** |
| 字面 socket 路径（不存在） | `{"stage":"connect","error":"connect() failed: No such file or directory"}` → **exit 5** |

**后果**：把 Go 默认值改为目录 `/tmp` **不会**让 runner 连对，且产生 `ECONNREFUSED`（Connection refused）而非原来的 `ENOENT`（No such file or directory）。**ENOENT 更准确地表达了「宿主未实现」，ECONNREFUSED 会误导成「宿主在跑但拒绝连接」**。因此「改为目录」在宿主未实现的前提下是**负向改动**。

**修订后的范围 A 决策**：

- **保留** `module.go:76` 的字面 socket 路径默认值（或改为显式传达「宿主未实现」的形态），**不改为目录**——理由是目录语义与 runner 客户端不符，且错误信号更差；
- **真正的定案**（目录 vs 字面路径）必须与 broker 宿主归属一并决定：若宿主实现为「一 stream 一 socket 文件」并由 Go 派生，Go 应传**字面路径**；若宿主启动接口就是「目录」，则 **runner 客户端必须同步改造**为按目录 + stream_id 派生。该决策随 `10-10-algorithm-plugin-architecture` 做出；
- 在 `native/algo-plugin-contract.md` 中**如实记录该不对称**（已完成，见 §5.1）；
- 让「broker 宿主不存在」这一状态**可观测**（已完成）：runner 的 exit 5 / `stage=connect` 被识别为独立信号；
- 在 `prd.md` / `native/algo-plugin-contract.md` 中**显式记录**「broker 宿主 + 帧发布通路未实现」。

⚠️ 本任务**不得**宣称 AC3 已达成。broker 宿主的实现与真实连接端到端测试随父任务 `10-10` 的架构决策一并处理。

### R3. manifest 字段路径安全校验（P0，收敛后定性）

`internal/algorithm/verifier.go:228` 用归档内 `manifest.json` 的 `id`/`version` 拼装安装路径：

```go
targetDir := filepath.Join(finalInstallDir, manifest.ID, manifest.Version)
```

`verifier.go:98` 仅校验三者非空（`manifest.ID == "" || manifest.Version == "" || manifest.Name == ""`），**没有任何字符集校验**；`ExtractArchive`（`uploader.go:99-234`）的 Zip-Slip 防护只作用于**归档内的条目名**，管不到 manifest 的字段值。

**已验证结论**：`filepath.Join` 的逃逸行为**已实测确认**：

```
filepath.Join("/data/algorithms", "../../../tmp/pwned", "1.0.0")  →  /tmp/pwned/1.0.0
```

即带 `../` 的 `manifest.ID` 确实能突破 `installDir`。本项是**已确认可利用**的缺陷（前置的非空校验不构成防护），而非待验证假设。

**要求**：

- 为 `manifest.ID` / `manifest.Version` 增加显式字符集白名单校验（例如 `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`），并把该规则写入 `native/algo-plugin-contract.md`；
- ⚠️ **`manifest.Name` 不施加路径字符集白名单**：已核实 `Name` **不参与任何路径构造**（`verifier.go:122` 仅做非空校验、`:290` 存库、`handler.go:107` 审计、`service.go:154`/`supervisor.go:74,380` 作为业务标识）。它是人类可读的展示字段（同族 `design.md` 的示例为 `"安全帽佩戴检测算法"`），施加该白名单会拒绝所有非 ASCII 名称。`Name` 仅保留非空校验 + 长度上限（防滥用），不做字符集限制；
- 校验失败必须返回可识别的领域错误码，而不是笼统的 `ALGO_VERIFICATION_FAILED`；
- 增加回归测试：带 `../` 的 id 必须被拒绝，且最终安装路径始终被约束在 `installDir` 之内（不仅依赖字符集白名单，还需在 `targetDir` 拼接后做一次前缀断言，形成纵深防御）。

### R4. IPC 消息类型枚举两侧不一致 + 64KB 门禁缺失（P0）

**4a. 枚举定义冲突**：

| 来源 | 消息类型定义 |
| --- | --- |
| `native/src/algorithm/ipc/ipc_protocol.hpp:14-20` | `MSG_UNKNOWN=0` … `MSG_DISCONNECT=6`（**止于 6**） |
| `internal/algorithm/process.go:28-30` | `MsgGallerySync=10`、`MsgGalleryUpsert=11`、`MsgGalleryDelete=12` |

native 侧**完全没有** gallery 消息类型定义，Go 侧凭空定义了 10/11/12。即使 R6 的热更新链路被实现，native 也会把 `msg_type=10` 当成未知消息。Go 侧 `process.go:36-38` 自定义的 `IpcHeader` 结构也与 native 的 `ipc_protocol.hpp:23-30` 是两个独立定义，存在漂移风险。

**4b. 64KB 门禁缺失**：

`.trellis/spec/native/algo-plugin-contract.md` §6.4 明确要求 `MAX_IPC_PAYLOAD_SIZE = 64KB` 尺寸检查。native 侧定义了该常量（`ipc_protocol.hpp:11`），但 `internal/algorithm/process.go:37-48` 的 `EncodeIpcMessage` 直接 `make([]byte, 16+len(payload))` 并拷贝，无任何长度检查。底库全量同步（`MSG_GALLERY_SYNC`）随人员增长必然突破该上限。

**要求**：

- 消除枚举双份定义：确立单一事实来源（native 头文件为准），Go 侧消息类型必须与 native 逐一对齐，gallery 类型需在 native 侧补齐；
- `EncodeIpcMessage` 超限时返回错误（签名需相应调整），或在分片协议层实现超限拆分——**二选一，需在 design.md 中定案**；
- 明确「单条消息上限」与「超限时的行为」并写入契约文档；
- 底库规模超过单条上限时的行为必须有测试覆盖。

### R5. `.env` 注入为死代码（P0）

PRD R2 与 implement.md 阶段三都要求「启动 Runner 时将 `.env` 变量追加注入至 `cmd.Env`」。`internal/algorithm/process.go:117` 定义了 `EnvFile` 字段，`process.go:176-177` 有读取逻辑，但**全仓库（含测试）没有任何地方给 `EnvFile` 赋值**，也没有任何构造函数把它接到安装目录下的 `.env`。

**要求**：

- 把 `.env` 路径接线到实际安装目录（`model.go` 中已有的 `EnvPath(algoDir)` 约定）；
- 端到端验证：一个依赖 `.env` 覆写变量的 runner 能读到该变量；
- 若决定不做（例如认为 `.env` 由 native 侧自行读取），必须在 PRD/design 中显式删除该需求并说明理由——**不允许静默保留死代码**。

### R6. R3 底库热更新链路的 spec 偏差澄清

PRD R3 要求 Go 通过 UDS 向 Runner 下发 `MSG_GALLERY_SYNC`/`MSG_GALLERY_UPSERT`/`MSG_GALLERY_DELETE`，实现零重启热更新。评审发现：

- `internal/algorithm/process.go:355-378` 只**构造**了消息字节，没有任何代码把它写入 socket；
- implement.md 阶段三承诺的 `GalleryDispatcher` 不存在；
- `internal/person/service.go:189` 的 `GetGallerySyncData` 无调用方；
- native 侧 `ipc_protocol.hpp` 没有 gallery 消息类型；
- 载荷字段名两侧不一致：`algorithm/process.go:333` 用 `features`，`person/model.go:66` 用 `feature_vector`。

**要求**：

- 这是**设计缺口而非编码疏漏**，必须先澄清：底库同步的触发时机、传输通道（复用 broker socket 还是独立 UDS）、消息序列与 ACK 语义、单条上限与分片策略；
- 澄清后同步更新 `prd.md` / `design.md` / `native/algo-plugin-contract.md`，再决定是否在本任务内实现；
- 若本任务只澄清不实现，必须把 PRD R3 标记为未完成，**不得**保留「看起来已实现」的状态。

**范围 A 决策：本任务只澄清，不实现。**

理由：热更新链路的落点依赖 R2 的 broker 宿主决策（复用同一 socket 还是独立 UDS 取决于宿主归谁），而 R2 已确定不在本任务内实现。因此本任务只产出澄清结论，并把 PRD R3 如实标记为未完成。

⚠️ 同时必须处理一个**看起来已实现**的假象：`internal/algorithm/process.go:355-378` 存在三个构造 gallery 消息的函数，但无人调用。这些函数与 `person.GetGallerySyncData` 一起构成「已实现」的错觉。本任务需在代码中以明确注释标注其**未接入线路**的状态（或在契约文档中登记），避免下一个任务再次误判完成度。

### R7. R5 熔断阈值 off-by-one 定案

`internal/algorithm/recovery.go:104` 在 `len(crashHistory) >= MaxCrashes` 时跳闸。PRD R5 写「5 分钟内**最多允许**崩溃 3 次」，design.md 写「**超过**阈值」。两者语义不同：

- 「最多允许 3 次」→ 第 3 次崩溃仍应重启，第 4 次才熔断；
- 当前 `>=` → 第 3 次崩溃即熔断。

副作用：4s/8s 两档退避在默认参数下不可达（1s、2s 之后即熔断），而 PRD R5 明确列出了 `1s ➔ 2s ➔ 4s ➔ 8s` 的完整步长。`recovery_test.go:38` 把当前行为写成了断言。

**要求**：

- 以定案的显式配额（3 次）为准，修正 `>=` 为 `>`；
- 同步修正 `design.md` 的措辞，消除「最多允许」与「超过」的矛盾；
- 更新测试断言以匹配最终语义（第 3 次崩溃重启、第 4 次熔断）。

**定案：以显式配额（3 次）为准。**

修正 `>=` 为 `>`，使「5 分钟内最多允许崩溃 3 次」字面成立：

| 崩溃序号 | 当前行为（`>=`） | 定案行为（`>`） |
| --- | --- | --- |
| 第 1 次 | 重启，退避 1s | 重启，退避 1s |
| 第 2 次 | 重启，退避 2s | 重启，退避 2s |
| 第 3 次 | **熔断**（与 PRD 矛盾） | 重启，退避 4s |
| 第 4 次 | —（不可达） | **熔断** `CRASH_LOOP` |

**如实记录的事实**：8s 档在 3 次配额下**仍不可达**（最多观察到 1s/2s/4s 三档）。退避阶梯描述的是**计算公式的形态**，而非保证每一档都被执行到；`MaxBackoff = 30s` 与 `shift > 10` 的封顶逻辑仍然保证公式在更高配额下正确。该事实写入 `design.md`，不声称 8s 可达。

## 3. 约束

- **不改动** `native/` 下已归档任务（`10-12-algo-framebroker-shm`、`10-12-algo-framebroker-hardening`）已验证的行为，除非 R2 的契约统一必须触及；
- 所有改动必须通过 `make go-check`（含 `gofmt -l`、`go vet`、`go test -race`）与 `native/tests/run_go_bridge_tests.py`；
- **端到端证据优先于新增单元测试**：本任务的存在理由就是「单测全绿但生产不通」，因此每个 P0 都必须有走真实生产路径的验证；
- 遵守 `.trellis/spec/backend/` 全部规范，特别是 `quality-guidelines.md` §3 禁止 `_ = fn()`。

## 4. 验收标准 (Acceptance Criteria)

- [x] **AC1（R1 端到端）**：通过 `app.NewRouter` 构建的**真实路由**（非测试自建 router），以 multipart 上传一个 > 1 MiB（建议 ≥ 8 MiB）的合法算法包归档，返回成功而非 413；同时上传一个超过配置上限的归档，返回 `FILE_TOO_LARGE`。
- [x] **AC2（R2 降级可观测性）**：以**真实** `zhulong_algo_runner` 二进制（非 `sleep` 桩）触发一次启动，验证 exit code 5 / `stage=connect` 被识别为**独立的「broker 宿主缺失」信号**（而非普通崩溃），且该状态在结构化日志中可明确区分。⚠️ **不要求**真实 broker 连接成功——宿主未实现，完整连接测试随父任务 `10-10` 处理。⚠️ `--broker` 的目录/字面路径定案**已推迟**至 `10-10`（服务端与客户端语义不对称，实测两种传法均 exit 5）；本任务只要求如实记录该不对称。
- [x] **AC3（R3 已验证缺陷修复）**：`filepath.Join` 逃逸已实测确认；修复后提交回归测试证明 `id: "../../../tmp/pwned"` 被**拒绝**（返回明确领域错误码），且安装路径始终被约束在 `installDir` 之内。
- [x] **AC4（R4 门禁）**：`EncodeIpcMessage` 超限行为有明确错误返回或分片实现，并有测试覆盖超限与边界（恰好 64KB）两种情况。
- [x] **AC5（R5 .env）**：一个依赖 `.env` 覆写变量的 runner 能读到该变量（端到端）；或需求被显式删除并记录理由。
- [x] **AC6（R6 澄清）**：底库热更新的触发时机、通道、消息序列、上限与分片策略写入 `design.md` 与 `native/algo-plugin-contract.md`；PRD R3 被**如实标记为未完成**；`process.go` 中未被调用的 gallery 构造函数带有明确「未接入线路」注释。
- [x] **AC7（R7 定案）**：熔断阈值语义与 PRD 的显式配额（3 次）一致（`>` 而非 `>=`），第 4 次崩溃熔断；测试断言同步更新；design.md 记录「8s 档在 3 次配额下不可达」的事实。
- [x] **AC8（质量门禁）**：`make go-check` 与 `native-test` 通过。

## 5. 边界与范围外

**范围内**：R1–R7 的实现与澄清；`app/router.go` 上传路由策略；`algorithm`/`person` 两个模块的相关改动；契约文档更新。

**范围外（另行立项）**：

- **broker 宿主实现与帧发布通路**（R2 的架构部分）——移交父任务 `10-10-algorithm-plugin-architecture`；本任务只做契约定案与可观测性降级；
- **底库热更新链路的实现**（R6）——本任务只澄清；
- Swagger 注解缺失（`algorithm/handler.go`、`person/handler.go` 均无 `@Summary`/`@Router`，但 Makefile 已把它们喂给 `swag`）——属文档规范债，不阻断运行；
- 未注册错误码（`INVALID_FILE`、`CANNOT_OPEN_FILE`、`FILE_TOO_LARGE`、`INVALID_REQUEST`、`ALGO_INSTALL_FAILED`、`BIND_FAILED`、`BINDING_NOT_FOUND`）导致 `WriteError` 退化为通用 `REQUEST_FAILED`；
- 绑定失败返回 400 而非规范要求的 422 + `details`；
- 裸数组响应、`time.After` 循环、`os.MkdirAll` 构造函数副作用等 `quality-guidelines.md` 违规；
- R6 回滚路径的 REST 路由与就绪探测（`UpgradePackage` 无调用方）；
- 死代码清理（`IsCircuitBroken`、`MarkStable`、`GetFace`、`ListAllFaces`、`ListBindingsByPackage`、`UpdateBinding`、`IpcHeader.validate` 等）；
- `person/service.go` 的零向量 stub（512 维特征未真实提取）；
- `Makefile` 的 pnpm 分支（与本任务无关的 scope creep）。

> 上述范围外项建议作为后续独立任务处理，本任务不顺手改动——避免 diff 膨胀导致 P0 修复难以复核。
