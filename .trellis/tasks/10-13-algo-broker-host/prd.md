# FrameBroker 宿主与多路帧分发通路

父任务：`10-10-algorithm-plugin-architecture`（阶段二遗留缺口）。本任务补齐 `.trellis/spec/native/algo-plugin-contract.md` §5.3 登记的「FrameBroker 宿主进程与帧发布通路未实现」，并定案 §5.1 记录的 `--broker` 语义不对称。

与阶段四两个子任务（`10-13-algo-rknn-inference-backend`、`10-13-algo-rga-vision-backend`）**共享构建文件**（`native/CMakeLists.txt`、`native/scripts/build.py`），**必须串行执行，本任务在前**（本任务建立的归档链接方向是后两者的前置基础）；语义上也**先于它们**：RKNN 后端要跑真链路依赖本任务的帧通路。

## 1. 目标

让解码帧真正从 native 引擎流到算法 runner：在 `Engine` 内实例化 `FrameBroker`，把 AI 消费流的解码帧发布到共享内存槽位，经 Unix Socket 通知 runner；Go 侧按多路相机订阅，runner 支持连接多个 broker。

## 2. 仓库事实（2026-10-13 核实）

### 2.1 零件齐备、缺胶水

| 组件 | 状态 | 证据 |
| --- | --- | --- |
| `FrameBroker` | ✅ 完整 | `native/src/algorithm/ipc/frame_broker.{hpp,cpp}`：`start`/`stop`/`publish_frame`/槽位/代次/lease/慢包丢帧/崩溃回收全有 |
| `ShmPool` | ✅ 完整 | `shm_pool.{hpp,cpp}`：`CREATE_READ_WRITE` / `OPEN_READ_ONLY` |
| IPC 协议 | ✅ 完整 | `ipc_protocol.hpp`：`HANDSHAKE`/`FRAME_DISPATCH`/`FRAME_ACK`，`static_assert` 对齐校验齐全 |
| `UnixSocketServer/Client` | ✅ 完整 | `unix_socket.{hpp,cpp}` |
| runner 客户端 | ✅ 完整 | `runner.cpp:161` `run_ipc_worker` |
| **`FrameBroker` 实例化** | ❌ **零处** | 生产代码无构造点；仅 `tests/frame_broker_test.cpp` 构造 |
| **`publish_frame` 调用** | ❌ **零处** | 同上 |
| **CMake broker 宿主目标** | ❌ 无 | 只有 `ZhulongFrameBrokerTests` |
| Go 侧帧 publish | ❌ 无 | C ABI 未暴露 broker/帧分发入口 |
| 解码帧导出 | ✅ 已齐备 | `ffmpeg_decoder.cpp` 经 `216c30f` 补齐 `host_planes[4]` + `plane_strides[4]` + format（父任务 `design.md` §5.1 记的缺口**已过时**） |
| `Stream::acquire_frame` | ✅ 存在 | `stream.cpp:380+`，供下游取 `HardwareFrame::ConstPtr` |
| `DecodeSession` 按需激活 | ✅ 存在 | `stream.hpp:48`；`pipeline-guidelines.md` 记录「仅在存在 `Zhulong_CONSUMER_AI` 接入时动态拉起」 |

### 2.2 Go 侧管道已就绪，缺两端

| 组件 | 状态 | 证据 |
| --- | --- | --- |
| `ConsumerAI` 枚举 | ⚠️ **定义但零调用** | `internal/engine/types.go:33`、`validation.go:48` 允许；实际 acquire 处**零处** |
| `Supervisor.brokerSock` | ✅ 存在 | `supervisor.go:19,61`；透传到 `ProcessConfig.BrokerSock` |
| runner `--broker` 传参 | ✅ 存在 | `process.go:200-201` |
| 默认值 + 决策注释 | ✅ 存在 | `module.go:82-101`，明确把决策推给本任务 |
| `Stream.id` | ✅ 含 stream_id | `internal/engine/stream.go:19` `id C.Zhulong_stream_id` |
| `StreamHub`/`StreamDispatcher` | ✅ 存在 | `internal/camera/stream_hub.go`、`stream_dispatcher.go`（per-camera 包分发） |
| C ABI broker 路径查询 | ❌ 无 | `engine.h` 无相关符号 |
| **任务变更 → 订阅刷新** | ❌ **完全不存在** | `Service.CreateTask`/`DeleteTask`（`service.go:196,216`）与 `Store.UpdateTask` 只写 DB，不触发任何重新解析；`Supervisor.StartAlgorithm` 仅由 `service.go:145`（`EnableAlgorithm`）与 `module.go:33`（启动 auto-start）调用。**新建/删除 `AnalysisTask` 不会让 runner 重新解析 `--broker`，也不会让 camera 域 acquire AI 流**（缺口 R7 的真实落点） |
| `Supervisor` cfg 构造点 | ⚠️ **4 处，仅 3 处有 ctx** | `supervisor.go:99`（`StartAlgorithm`）、`:182`（`UpgradeAlgorithm`）、`:225`（`rollbackToOldLocked`）有 `ctx`；`:478`（`superviseLoop` 崩溃重启）**无 `ctx`、无解析时机** |
| Go 层停机顺序 | ✅ 已满足，但无守护 | `internal/app/runtime.go:220-260`：HTTP → camera → **algorithm** → person → storage → systemTime → network → **native**。恰好满足父任务 `design.md:306`「先排空子进程和 broker，最后关闭 Native engine」，但**无测试守护，顺序被改动不会失败** |

### 2.3 `module.go:82-101` 的既有注释（本任务必须定案的问题）

```go
// ⚠️ The runner CLIENT treats this value as a LITERAL socket path
// (UnixSocketClient::connect does a bare connect(), no derivation),
// while the FrameBroker SERVER derives
// <base_dir>/zhulong_broker_<stream_id>.sock from its own base_dir
// (frame_broker.cpp:20). The two sides are asymmetric and no broker
// host exists yet, so any value fails with exit 5.
//
// The directory-vs-literal-path decision is deferred to the broker
// host design in 10-10-algorithm-plugin-architecture. See
// .trellis/spec/native/algo-plugin-contract.md §5.1.
//
// The pre-existing value is deliberately kept: Go has no stream_id
// (that is a media-layer concept), so it cannot derive a correct
// per-stream path. Inventing one here would be a guess.
brokerSock = "/tmp/zhulong_framebroker.sock"
```

### 2.4 关键约束：broker 是「一流一实例」

`FrameBroker::start(stream_id, base_dir, ...)` 把 `stream_id` 用在**两处身份派生**：

```cpp
shm_name_    = "/zhulong_shm_" + std::to_string(stream_id_);                       // frame_broker.cpp:19
socket_path_ = base_dir + "/zhulong_broker_" + std::to_string(stream_id_) + ".sock"; // frame_broker.cpp:20
```

因此**一个 broker 实例只能服务一个 stream**。而 `StartAlgorithm(ctx, algo)` 的粒度是**算法包**，与 stream 无关。本任务需解决这个粒度错配。

⚠️ **粒度错配有两个触发面，不要只解一个：**

| 触发面 | 何时发生 | 需要重新解析什么 |
| --- | --- | --- |
| **启动面** | `EnableAlgorithm` / 启动 auto-start / 升级 / 回滚 | 该包的 `--broker` 列表 + camera 域 acquire 对应 AI 流 |
| **变更面** | `AnalysisTask` 增删改（含 `sample_fps` 变化） | 同上——**当前完全不存在**（见 §2.2） |

⚠️ 注意区分：**socket 随 stream 走，不随 runner 实例走。** 一包一进程（`watchers[algo.ID]`，`supervisor.go:81`）已实现；但一个 runner 可能连多个 socket（它订阅的每一路流各一个）。完整实体拓扑与计数关系（N:M）见 `design.md` §1.1。

### 2.5 `engine.h` 无 ABI 版本协商机制

`native/include/Zhulong/engine.h`（340 行）**没有** `struct_size` / `api_version` 字段，也没有版本查询函数。本任务新增 C ABI 函数只能是**追加式**（不改现有签名与结构体布局），无法依赖容量协商。这是既有状况，如实记录，不在本任务引入版本机制。

## 3. 已确认决策

| ID | 决策 |
| --- | --- |
| D1 Broker 宿主位置 | **native `Engine` 内**。`Engine` 持有 broker 池；`DecodeSession` 出帧后直接 `publish_frame`。满足父任务 `design.md`「像素不绕 Go」与 §5.1「Native broker 单一拥有者」。 |
| D2 `--broker` 语义 | **字面 socket 路径**。runner 客户端不改派生逻辑；由 native 派生好完整路径再传给 runner。runner 无需知道 stream 拓扑。 |
| D3 多路订阅 | **首期支持「一个算法包同时订阅多路相机」**。runner 需支持多个 `--broker`。 |
| D4 任务粒度 | **单任务**。native 宿主与多路消费必须一起才能端到端验证；拆开会产生无法独立验收的中间态。 |
| D5 任务变更触发 | **首期用「重启该算法包」承接**。`AnalysisTask` 的增/删/改（含 `enabled`、`sample_fps`）→ 对该包执行既有 `StopAlgorithm` + `StartAlgorithm`。理由：父任务 R7 已定「仅重启对应算法包，宿主和其他包不重启」，粒度天然吻合。**不引入运行期重订阅协议**（首期不做）。 |
| D6 多路连接失败语义 | **任一路 `--broker` 连接失败即整体失败**，退出码保持 `5`（§5.2 表不变），stderr 额外带上失败路径。理由：runner 无法区分「该路暂时无流」与「路径配置错误」；部分连接下继续运行会让 AC4 的「独立投递」退化为静默半瘫。 |
| D7 帧节流位置 | **native 发布侧**（`FramePublisher`），按该 stream 上订阅包的 `sample_fps` 节流。发布率取 **`max(sample_fps)`**（同一 broker 扇出给所有包，发布率只能有一个）；单包更低的 `sample_fps` 由 runner 侧二次丢弃。理由：省掉 5× 无谓的 shm 逐 plane 拷贝与 socket 通知，且让「正常背压」与「过载丢帧」可区分（见 `design.md` §4）。 |
| D8 `ZHULONG_FRAME_BROKER_SOCK` 归属 | **定案为 native 侧的 `base_dir`**（默认 `/tmp`），由 native 自己 `getenv` 读取。**Go 侧不再读取、不再传给 runner**——路径已由 native 派生（D2），Go 传值只会制造第二个真实源。变量名保留（历史原因），语义在 `algo-plugin-contract.md` §5.1 明确为「目录」。 |

## 4. 需求

| ID | 需求 |
| --- | --- |
| R1 broker 宿主 | `Engine` 持有 per-stream 的 `FrameBroker` 实例；AI 消费者 acquire 时创建，release 时销毁。broker 生命周期受 `Engine::start/stop` 管理。 |
| R2 帧发布 | AI 消费流的 `DecodeSession` 出帧后调用 `publish_frame`。⚠️ 发布路径不得在 capture/decode 回调或队列锁内阻塞（`design.md` §5）。⚠️ 发布侧按 D7 节流，不得无条件按解码帧率投递。 |
| R3 帧引用归还 | 发布后**立即 drop** `HardwareFrame` 引用，不得持有至推理完成（`pipeline-guidelines.md` §3「防慢算法拖垮解码器准则」）。 |
| R4 socket 路径查询 | 新增 C ABI 查询函数，由 native 派生并返回该 stream 的 broker socket 路径；Go 不重复实现路径规则（单一真实源）。 |
| R5 Go 多路订阅 | `ProcessConfig.BrokerSock` 扩展为多值；`Supervisor` 按算法包关联的**多路相机**传入多个 broker 路径。⚠️ 解析结果必须在创建 `workerWatcher` 时**固化到 watcher**，供 `superviseLoop` 崩溃重启路径（`supervisor.go:478`，无 ctx）复用——否则每次自动重启都 exit 5 并最终熔断 CRASH_LOOP。 |
| R6 runner 多连接 | runner 支持多个 `--broker`，每路一个连接，独立 lease/ACK 循环。任一路连接失败按 D6 整体失败。 |
| R7 AI 消费接入 | Go 侧真正按 `ConsumerAI` acquire 流（当前零调用），与 `AnalysisTask` 的 (camera, algo) 关联对齐。⚠️ 必须同时覆盖 §2.4 的**启动面**与**变更面**：任务增删改要触发重新解析，否则 AC5 无法闭环。 |
| R8 语义定案 | `module.go:82-101` 的注释更新为定案结论；`.trellis/spec/native/algo-plugin-contract.md` §5.1/§5.3 同步更新。⚠️ 定案范围含 **D8 的环境变量归属**（§5.1 现有「值是目录」的表述在定案后仍需保留，但须删除「Go 侧传目录」的旧说法）。 |
| R9 不破坏既有 | 既有 preview/recording 消费、订阅回调、8 秒宽限期等行为不变；不改 `FrameBroker`/`ipc_protocol` 的既有签名与结构体布局。⚠️ Go 层停机顺序（camera → algorithm → native，`runtime.go:220-260`）是「broker 先于 native 停止」的载体，须显式记录为约束，不得被后续改动静默破坏。 |
| R10 像素不绕 Go | 帧数据全程在 native 内（`DecodeSession` → `FrameBroker` → runner）。Go 只传 socket 路径字符串，不接收像素。 |

## 5. 范围

**包含**：native broker 实例化与生命周期、帧发布通路、C ABI 路径查询、Go 多路订阅接线、runner 多连接、spec 语义定案、端到端验证。

**不包含**：RKNN/RGA 后端（阶段四子任务）、DMA-BUF 零拷贝（`design.md` §5.2 的后续门槛）、gallery 热更新链路（`algo-plugin-contract.md` §7.3 登记，依赖本任务的宿主归属但属独立交付）、`FrameBroker` 内部算法改动（已完整）、昇腾/CANN 与 TensorRT 后端、前端页面。

## 6. 明确不承诺

- 不承诺生产零拷贝：本任务走 `design.md` §5.1 的**有拷贝参考路径**（逐 plane 复制到共享内存）。
- 不承诺跨进程 `HardwareFrame` 指针共享：共享内存槽位是**数据副本**，不是指针传递。
- 不承诺驱动/设备级故障隔离。
- 不承诺 `engine.h` 具备 ABI 版本协商（既有状况，本任务不引入）。
- 不把「broker 能 start」当作帧通路已验收。

## 7. 验收标准

| ID | 关联 | 可验证结果 |
| --- | --- | --- |
| AC1 | R1、R9 | `Engine` 内 broker 实例随 AI 消费者 acquire/release 创建销毁；`make native-test` 既有测试全绿；preview/recording 行为不变。 |
| AC2 | R2、R3 | 真实 loopback RTSP 解码帧经 `publish_frame` 进入共享内存槽位，runner 收到 `FRAME_DISPATCH`；发布后 `HardwareFrame` 引用立即释放（无解码池耗尽）。 |
| AC3 | R4 | 新增 C ABI 查询返回该 stream 的 socket 路径；Go 侧 `Stream` 暴露该方法；路径与 `FrameBroker::socket_path()` 一致（无规则重复）。 |
| AC4 | R5、R6 | 一个算法包同时连接 2 路 broker（2 个 `--broker`），两路帧独立投递、独立 lease/ACK，互不干扰。 |
| AC5 | R7 | Go 侧按 `ConsumerAI` acquire 流；`AnalysisTask` 的 (camera, algo) 关联驱动订阅；`DecodeSession` 被按需拉起。 |
| AC6 | R2、R8 | 慢包（故意 sleep 的 runner）触发 Drop Latest，不阻塞采集/解码与其他包；spec §5.1/§5.3 更新为定案结论。 |
| AC7 | R1、R10 | 宿主进程退出/崩溃后 runner 的 lease 被回收、槽位回归 FREE；Go 侧不接收像素数据。 |
| AC8 | 全部 | 端到端：RTSP → 解码 → broker → runner → 插件 `process` 被调用；`make native-test` + `make go-check` 通过。 |
| AC9 | R7、D5 | **变更面闭环**：算法包 A 已运行且订阅相机 1；运行期新建 `AnalysisTask(camera2, A)` → A 的 runner 以 2 个 `--broker` 重启，相机 2 的 AI 流被 acquire、`DecodeSession` 拉起；删除该任务 → 回到 1 路，相机 2 的 AI 消费者 release。 |
| AC10 | R2、D7 | **节流生效**：`sample_fps=5` 时 25fps 解码流的 `publish_frame` 调用率约为 5fps（而非 25fps）；把 `sample_fps` 改为 25 后调用率随之上升；两个包分别 5/25 订阅同一 stream 时发布率为 25，5fps 包在 runner 侧二次丢弃。 |
| AC11 | R5、D6 | **崩溃重启不自杀**：AI 链路跑通后 `kill -9` runner，`superviseLoop` 自动重启仍带完整 `--broker` 列表（不因缺参 exit 5 而进入 CRASH_LOOP）。 |

## 8. 未决与阻塞

- ~~**`AnalysisTask` → 多路相机 的映射细节未定**~~ → **已由 D5 定案**（重启该包承接变更面）。仍需在 `design.md` §7.4 细化「重启谁、按什么顺序、如何避免抖动」。
- **slot 预算**：`DEFAULT_SLOT_COUNT = 8`、`DEFAULT_SLOT_SIZE = 4MB` → 每 stream 32MB 共享内存。多路相机 + 多算法包时总占用需核算。⚠️ D7 的节流会显著降低稳态背压，但**不改变静态预算**（槽位在 `start` 时一次性分配）。
- **`shm_name_` 冲突**：`/zhulong_shm_<stream_id>` 按 stream 唯一，但 stream_id 是进程内 ID，重启后可能复用；需确认 unlink 时机（`FrameBroker::stop` 是否 unlink）。
- **`--broker` 多值时的客户端身份**：`HandshakeReq.client_name[32]` 定长，多路时命名需区分且 ≤32 字节（`algo-plugin-contract.md` §7.1 已规定 `name` 上限）。
- **多路时的 task 身份**：`runner.cpp` 当前对**所有** `--broker` 连接使用同一个 `task_id=1` 与同一个 `zhulong_task_ctx_h`（`runner.cpp:184`）。D5/D7 定了订阅面，但「同一包的多路 stream 如何在同一 worker 内区分 task state」仍未定（父任务 `design.md:242` 要求 task→worker 亲和）。首期建议一路一 worker，但会放大线程与执行上下文开销，需在 `design.md` §8 给出结论。
