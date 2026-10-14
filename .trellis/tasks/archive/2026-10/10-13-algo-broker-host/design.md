# FrameBroker 宿主与多路帧分发通路：技术设计

依据 `prd.md` R1–R10 与已确认决策 D1–D4。接口名称与参数是实现建议，不是已存在 API。

## 1. 结论与边界

**所有零件已在，只缺胶水。** 本任务不重新实现 broker、IPC、共享内存或解码，而是：

1. 在 `Engine` 内实例化 per-stream 的 `FrameBroker`
2. 从 `DecodeSession::frame_queue_` 取帧并按 `sample_fps` 节流后 `publish_frame`
3. 新增 C ABI 查询把 broker socket 路径交给 Go
4. Go 侧真正按 `ConsumerAI` acquire，并把多路 broker 路径传给 runner；任务增删改驱动重解析
5. runner 支持多 `--broker`

### 1.1 实体拓扑与计数关系（N:M，不是 1:1）

⚠️ **socket 不随 runner 实例走，而是随 stream 走。** 这是理解本任务的关键，也是 `frame_broker.cpp:19-20` 的既定事实。

```text
相机 (camera)  ──RTSP URI──→  Stream (native 物理流，按规范化 URI 去重)
                                    │
                                    │ 一个 stream 一个 broker
                                    ↓
                          FrameBroker 实例
                          ├─ socket:  <base_dir>/zhulong_broker_<stream_id>.sock
                          └─ shm:     /zhulong_shm_<stream_id>   (8 槽位 × 4MB = 32MB)

算法包 (algorithm)  ──→  Runner 进程  (一包一进程)
                          └─ Worker[0..N)  线程 + 独立 Execution
```

| 实体 | 粒度 | 由谁派生 | 证据 |
| --- | --- | --- | --- |
| `Stream` | 按规范化 RTSP URI 去重 | `Engine::acquire` | `ingestion-contract.md` §3 |
| `FrameBroker` | **per-stream** | `stream_id` 派生 socket + shm | `frame_broker.cpp:19-20` |
| socket 文件 | **per-stream** | 同上 | 同上 |
| shm 段 | **per-stream** | 同上 | 同上 |
| Runner 进程 | **per-package** | `watchers[algo.ID]` | `supervisor.go:81,110` |
| Worker 线程 | per-package 内多个 | `worker_create` 调用次数 | `algo-plugin-contract.md` §1 |

**具体例子：2 路相机 × 2 个算法包**（包 A 分析相机 1+2，包 B 只分析相机 1）：

```text
Stream_1 (camera1 RTSP)          Stream_2 (camera2 RTSP)
     │                                 │
  Broker_1                          Broker_2
  socket: ..._1.sock                socket: ..._2.sock
  shm: /zhulong_shm_1 (32MB)        shm: /zhulong_shm_2 (32MB)
     │        │                        │
     │        └────────┐               │
     ↓                 ↓               ↓
  Runner A  ←──── 2 个 socket 连接 ────→ (A 订阅 1+2)
  (包 A 进程)
     │
     └─ Worker[0..N)

     ↓
  Runner B  ←──── 1 个 socket 连接 (B 只订阅 1)
  (包 B 进程)
```

- **broker 数** = 2（per stream）
- **socket 数** = 2
- **shm 数** = 2（共 64MB）
- **runner 进程数** = 2（per package）
- **Runner A 连 2 个 socket**，Runner B 连 1 个

⚠️ 包 A 与包 B 都分析相机 1 时，**共享同一个 `Broker_1` 与 socket**。`publish_frame` 扇出给两个客户端，各自独立 lease（`SlotTracker::active_leases` 是 `unordered_set<client_id>`，`frame_broker.hpp:22`）。

**现状 vs 目标**：

| 项 | 现状（已验证） | 目标（本任务后） |
| --- | --- | --- |
| broker 实例 | ❌ 零处 | ✅ per-stream |
| socket | ❌ 无人监听 | ✅ per-stream |
| runner `--broker` | 单值（`process.go:200`） | 多值（可重复） |
| Runner 能连几路 | 1（且连不上，exit 5） | N（它订阅的所有流） |
| `ConsumerAI` | ⚠️ 定义但零调用 | 按 (camera, algo) 关联 acquire |
| `Supervisor.brokerSock` | 构造时**全局单值** | 按算法包解析出多值 |
| **发布速率** | —（无发布） | **按 `max(sample_fps)` 节流**（见 §4） |
| **任务增删改** | ⚠️ **只写 DB，无任何触发** | 重启该算法包（见 §7.4） |

**粒度错配（本任务需解决）**：`FrameBroker::start(stream_id, base_dir)` 是**一流一实例**，而 `StartAlgorithm(ctx, algo)` 的粒度是**包**。解析链路：

```text
StartAlgorithm(包 A)
    ↓ 查包 A 关联的 enabled AnalysisTask
    ↓ 每个 task 的 CameraID → 对应 Stream → 对应 broker socket
    ↓ ProcessConfig.BrokerSocks = [sock_1, sock_2, ...]
```

⚠️ 但 `internal/algorithm` 目前对 camera 域**零依赖**。解法见 §7.2（窄接口 `BrokerPathResolver`）。

⚠️ **共享内存预算**：默认 32MB/stream（8 槽位 × 4MB）。8 路相机 = **256MB**。RK3568 内存有限，而 `frame_queue_` 只有 3 帧，8 槽位可能偏多。本任务先按默认值实现，把预算核算列为验证项（见 §10）。

⚠️ **发布率与背压的相互作用**（D7 的直接后果，见 §4）：不节流时 25fps 投递 + `max_in_flight_per_client=2` + `lease_timeout=500ms`，在 `in_flight` 达上限后会**持续命中背压**——即 AC6 的「Drop Latest」在默认参数下从异常路径退化为常态。节流到 `sample_fps` 后，正常背压与过载丢帧才可区分。

## 2. 已就绪的通路（核实结论）

```text
Stream::acquire(kind=CONSUMER_AI)                    ← Go 侧零调用（缺口 R7）
        ↓
Stream::sync_decode_pipeline()                       ✅ has_ai_consumer() 驱动
        ↓
DecodeSession  (stream.cpp:359-370)                  ✅ 按需创建/销毁
        ↓  decode_loop → receive_frame → frame_queue_.push()   ✅
frame_queue_ {3, DROP_OLDEST}                        ✅ 有界 3 帧，丢旧
        ↓
Stream::acquire_frame()                              ✅ 已存在（仅 tests 使用）
        ↓
FrameBroker::publish_frame()                         ❌ 零调用（缺口 R2）
        ↓  shm slot + MSG_FRAME_DISPATCH
runner  run_ipc_worker                               ✅ 已存在
```

**关键结论**：`DecodeSession` 已按 `has_ai_consumer()` 自动拉起（`stream.cpp:350-361`），`frame_queue_` 是有界 3 帧 + `DROP_OLDEST`。所以本任务**不需要**改解码逻辑，只需要一个「取帧 → 发布」的消费者。

⚠️ 本节是**单流纵向**视角（一帧从解码到 runner 的路径）；实体的**横向 N:M 计数关系**（多相机 × 多算法包）见 §1.1。两者正交，不互相替代。

## 3. 架构：Engine 持有 broker 池

实体拓扑与计数关系（N:M）见 §1.1；本节只讲实现结构。

```cpp
struct Engine {
    // ... 既有字段 ...

    // 新增：per-stream broker 池
    std::map<Zhulong_stream_id, std::unique_ptr<ipc::FrameBroker>> brokers;  /**< 受 control 锁保护 */
    std::string broker_base_dir{"/tmp"};   /**< 默认；可经环境变量覆写 */
};
```

**生命周期绑定**：

| 事件 | 动作 |
| --- | --- |
| `Stream` 首次出现 `CONSUMER_AI` | 创建 broker，`start(stream_id, base_dir)` |
| 最后一个 AI 消费者 release | `broker->stop()`，从池中移除 |
| `Engine::stop()` | 停止所有 broker（在 join 流 worker 之前） |

`broker_base_dir` 默认 `/tmp`，由 native **自己** `getenv("ZHULONG_FRAME_BROKER_SOCK")` 读取（D8）。Go 侧**不再**读取该变量、**不再**把它传给 runner——路径已由 native 派生（D2/D8）。

⚠️ **顺序要求**（`pipeline-guidelines.md` §4 五步停机）：broker 必须在 `Stream::worker` join **之前**停止，否则帧发布线程可能访问已析构的槽位。broker 停止应放在既有的「锁外停止」阶段。

### 3.1 Go 层停机顺序约束（跨层，必须显式记录）

native 内部的停机顺序只是**后半段**。完整的「broker 先于 native 停止」依赖 Go 层顺序：

```txt
internal/app/runtime.go:220-260（现状，恰好正确）
  HTTP shutdown → camera.Stop → algorithm.Stop → person → storage
    → systemTime → network → native.Close
                    ▲              ▲
              排空 runner      最后关引擎
```

- `algorithm.Stop`（→ `Service.StopAll` → `Supervisor.StopAll`）必须在 `native.Close` **之前**：否则 runner 仍持有 shm 映射，而 broker 随引擎析构。
- `camera.Stop` 必须在 `algorithm.Stop` **之前**：否则 AI 消费者先消失、broker 先销毁，而 runner 还在收帧。

⚠️ **现状满足，但没有守护**：`runtime.go` 的 close 顺序被改动时**没有任何测试会失败**。本任务需在 `design.md` 定案后于 `dependency-injection.md` 记一条约束，并在 `check.jsonl` 加核对项（见 §11）。

⚠️ 父任务 `design.md:306` 的顺序表述是「先禁止重启调度 → 排空子进程和 broker → 最后关闭 Native engine 和数据库」。「禁止重启调度」对应 `Supervisor` 的 `stopCh`/`stopOnce`（`supervisor.go`），应在 `StopAll` 入口先置位，而非逐个 `StopAlgorithm` 时再置位。

## 4. 帧发布：谁取帧、在哪取

**方案：每个 AI 流的专属发布线程**（不是复用既有 worker）。

```text
Stream (capture worker)  ──push_packet──→  DecodeSession (decode worker)
                                                    ↓ frame_queue_
                                          FramePublisher 线程  ← 新增
                                                    ↓ acquire_frame()
                                            broker->publish_frame()
```

理由：

1. `frame_queue_` 是**有界 3 帧 + DROP_OLDEST**，发布必须及时消费，否则帧被丢弃。
2. 发布线程与解码线程解耦，`publish_frame` 的耗时（含 socket 发送，`SO_SNDTIMEO` 50ms）不阻塞解码。
3. 复用 `DecodeSession` 线程会让解码循环与 IPC 写耦合，违反单一职责。

**发布循环伪码**（含 D7 节流）：

```cpp
void FramePublisher::run() {
    const auto interval = frame_interval();   // = 1000ms / max(sample_fps)
    auto next_due = std::chrono::steady_clock::now();

    while (!stop_) {
        HardwareFrame::ConstPtr frame;
        if (!stream_->acquire_frame(frame, std::chrono::milliseconds(50))) {
            continue;  // 超时，检查 stop_
        }

        const auto now = std::chrono::steady_clock::now();
        if (now < next_due) {
            frame.reset();        // 未到期，直接丢弃（不发布，不拷贝）
            continue;
        }
        next_due = now + interval;

        broker_->publish_frame(*frame);
        frame.reset();            // ⚠️ 立即释放，不持有到推理完成
    }
}
```

⚠️ **`frame.reset()` 是硬要求**（R3）。父任务 `pipeline-guidelines.md` §3 明确：「各算法必须在硬件预处理任务派发/完成后立即 drop 掉 `HardwareFrame` 引用，严禁将原帧持有至 NPU 推理阶段」。broker 的 `publish_frame` 是**同步拷贝**到共享内存（`design.md` §5.1 的有拷贝参考路径），所以返回后即可释放。

### 4.1 节流频率从哪来（D7）

| 问题 | 结论 |
| --- | --- |
| 频率值来源 | 该 stream 上所有 enabled `AnalysisTask` 的 `sample_fps`（已存在于 `analysis_tasks` 表，且已用于算力预算：`store.go:230,242,248`） |
| 多个包订阅同一 stream | 发布率取 **`max(sample_fps)`**——一个 broker 只有一个发布率，无法对不同客户端按不同速率扇出 |
| 单包 `sample_fps` 低于发布率 | 由 **runner 侧二次丢弃**（runner 已知自己的 `sample_fps`） |
| 无 AI 任务时的流 | broker 本就不会创建（R1 绑定 `CONSUMER_AI`），无需处理 |
| 频率如何送到 native | **已定案并实现：方案 (a)**。新增追加式 C ABI `Zhulong_stream_set_publish_rate(engine, stream, max_sample_fps)`，与 R4 的追加式 ABI 风格一致；Go 侧封装为 `Stream.SetPublishRate`，经 `StreamHub.SetAIPublishRate` 下发。 |

⚠️ **不节流的后果**（为什么不把它当优化而是当需求）：解码 25fps、`sample_fps=5` → **5 倍无谓开销**：每次 `publish_frame` 都要做逐 plane `memcpy`（1080P NV12 约 3MB）、socket 通知与 runner 侧 `process` 调用。更重要的是它会把 AC6 的「慢包丢帧」变成默认行为（见 §1.1 末段）。

## 5. 链接方向问题（⚠️ 唯一真实构建风险：`build.py`）

**现状**：

| 库 | 类型 | 内容 |
| --- | --- | --- |
| `ZhulongEngine` | **STATIC** | `abi/engine.cpp`、`pipeline/*`、`nodes/capture`、`nodes/decode` |
| `ZhulongAlgoIpc` | **STATIC** | `algorithm/ipc/{shm_pool,unix_socket,frame_broker}.cpp` |
| `ZhulongAlgoRuntime` | SHARED | `algorithm/runtime/{cpu_inference,cpu_vision,runtime_factory}.cpp` |

`build.py:373` 最终只把 `libZhulongEngine.a` + FFmpeg 归档链进 Go：

```python
libraries = [link] + [prefix/"lib"/f"lib{lib}.a" for lib in ("avformat","avcodec","avutil")]
```

**问题**：`Engine` 要用 `FrameBroker`，则 `ZhulongEngine` 必须依赖 `ZhulongAlgoIpc`。三个候选：

| 方案 | 做法 | 优点 | 代价 |
| --- | --- | --- | --- |
| **A. Engine 链接 AlgoIpc** | `target_link_libraries(ZhulongEngine PUBLIC ZhulongAlgoIpc)` | CMake 依赖清晰 | 静态库互链需处理顺序；`build.py` 仍只链 `libZhulongEngine.a`，**归档不会自动合并** |
| **B. build.py 多链一个归档** | `libraries = [link_engine, link_ipc] + ffmpeg` | 显式，符合既有「内容寻址归档」模式 | 需扩展 `build.py`，且顺序敏感（被依赖者靠后） |
| **C. 把 ipc 源文件并入 Engine** | 移动 `frame_broker.cpp` 等到 `ZhulongEngine` 源列表 | 无链接问题 | ⚠️ 破坏 `ZhulongAlgoIpc` 供 runner 测试复用的结构；不推荐 |

**建议 B**：扩展 `build.py` 让它链接 `libZhulongEngine.a` + `libZhulongAlgoIpc.a`，两者都走既有的**内容寻址快照**机制（`digest(archive)` → `go-link/<hash>/`）。

⚠️ **顺序**：GNU ld 对静态库顺序敏感，`ZhulongEngine` 依赖 `ZhulongAlgoIpc`，所以 `-lZhulongEngine` 必须**在** `-lZhulongAlgoIpc` **之前**。

⚠️ **内容寻址要各自独立**：两个归档各有自己的 hash 子目录，不能共用一个（否则改一个会让另一个的缓存身份失效）。

### 5.1 ⚠️ 风险定级修正（2026-10-13 实测）

本设计原先把「静态库互链 + CMake 声明顺序」列为**最大构建风险**，并声称需要「调整声明顺序或使用前向引用」。**该定性过重，已实测推翻：**

```cmake
# 实测：cmake 3.31.6（本机版本）
add_library(A STATIC a.cpp)
target_link_libraries(A PUBLIC B)   # B 尚未定义
add_library(B STATIC b.cpp)
# → configure 通过，build 通过
```

CMake 对尚未定义的目标名做**延迟解析**，`target_link_libraries` 的前向引用**不需要**调整声明顺序。

**因此：**

| 项 | 原定性 | 修正后 |
| --- | --- | --- |
| CMake 前向引用 | 「最大风险，需调整顺序」 | **非风险**。仅当 CMake 实际报错时才调整顺序 |
| `build.py` 归档清单扩展 | 与上者并列 | **本任务唯一的真实构建风险**：内容寻址快照、`-L` 顺序、host/cross 双路径 |

⚠️ 不要为了「预防」前向引用问题而去改 `CMakeLists.txt` 的目标声明顺序——那会产生与需求无关的 diff（项目 `AGENTS.md` §3「精准修改」）。

⚠️ **交叉编译的额外核对点**：`ZhulongAlgoIpc` 在 `UNIX AND NOT APPLE` 下链接 `rt`（`CMakeLists.txt:178-180`）。交叉编译时 `-lrt` 由 `build.py` 注入的 `--sysroot` + `ldflags` 解析；rk3568 sysroot 下 `librt.so`/`librt.a` 均存在，但 `-lrt` 必须排在两个归档**之后**。需在阶段 A 用 `CROSS_PROFILE` 实跑确认（只编译不执行）。

## 6. C ABI 查询（R4）

`engine.h` **无 ABI 版本协商机制**（无 `struct_size`/`api_version`），所以只能**追加式**扩展：

```c
/**
 * @brief 查询指定流关联的 FrameBroker socket 路径
 *
 * 调用前提：该流已通过 Zhulong_stream_acquire 以 Zhulong_CONSUMER_AI 获取。
 *
 * @param engine  引擎句柄
 * @param stream  流 ID
 * @param out_path 输出缓冲（调用方分配）
 * @param inout_len 输入缓冲容量；输出实际写入长度（含 '\0'）
 * @return Zhulong_OK 成功；Zhulong_ERR_INVALID_ARGUMENT 参数非法；
 *         Zhulong_ERR_NOT_FOUND 流不存在或无 AI 消费者（broker 未创建）；
 *         Zhulong_ERR_BUFFER_TOO_SMALL 缓冲不足（out_len 回填所需大小）
 */
Zhulong_status_t Zhulong_stream_get_broker_path(Zhulong_engine_h engine,
                                                Zhulong_stream_id stream,
                                                char *out_path,
                                                size_t *inout_len);
```

**设计理由**：

- **单一真实源**：socket 路径规则（`<base_dir>/zhulong_broker_<sid>.sock`，`frame_broker.cpp:20`）只在 native 一处，Go 不重复实现。
- **不暴露内部布局**：Go 只拿字符串，不接触 `FrameBroker` 或 shm 名称。
- **`inout_len` 双向**：既支持「先问长度再分配」，也让缓冲不足时能报出所需大小。
- ⚠️ 路径长度上限需定义（建议 256，足以容纳 `/tmp` + 64 位 ID + 文件名）。

## 7. Go 侧：多路订阅

### 7.1 `ProcessConfig` 扩展

```go
type ProcessConfig struct {
    // ...
    BrokerSocks []string   // 原 BrokerSock string → 多值
}
```

`process.go:200-201` 改为循环追加：

```go
for _, sock := range p.config.BrokerSocks {
    args = append(args, "--broker", sock)
}
```

### 7.2 `Supervisor` 按算法包关联多路

现状 `Supervisor.brokerSock` 是**构造时的全局单值**（`supervisor.go:19`）。需改为**按算法包解析**：

```text
StartAlgorithm(ctx, algo)
    ↓
查该 algo 关联的 enabled AnalysisTask 列表
    ↓
每个 task 的 CameraID → 该相机的 AI 流 → broker socket 路径
    ↓
ProcessConfig.BrokerSocks = [路径...]
```

⚠️ **依赖顺序问题**：`internal/algorithm` 目前**不依赖** camera 域（`grep engine. internal/algorithm/*.go` 为空）。要拿 camera → stream 的映射，需要新的依赖边。

三个候选：

| 方案 | 做法 | 评价 |
| --- | --- | --- |
| **A. algorithm 依赖 camera** | 直接注入 camera 的查询接口 | ⚠️ 域间耦合；且 camera 不持有「哪个 stream 是 AI」的信息 |
| **B. 引入解析接口** | 定义 `BrokerPathResolver` 接口，由 `internal/app` 注入实现 | ✅ 符合既有 Fx 装配风格；`algorithm` 只依赖接口 |
| **C. 由 camera 侧推送** | camera 在 AI acquire 后主动把路径告知 algorithm | 反向依赖，更绕 |

**建议 B**：在 `internal/algorithm` 定义窄接口，实现放在 `internal/app`（或 camera 域的 adapter），符合 `.trellis/spec/backend/dependency-injection.md` 的 Fx 装配约定与「构造函数必须纯粹」要求。

```go
// internal/algorithm 内定义（窄接口，只依赖自己需要的方法）
type BrokerPathResolver interface {
    // 返回该算法包当前应订阅的全部 broker socket 路径
    ResolveBrokerPaths(ctx context.Context, algorithmID string) ([]string, error)
}
```

⚠️ **解析结果必须固化到 watcher，不能只存在 `ProcessConfig` 里。** `brokerSock` 在 `supervisor.go` 有 **4 个 `ProcessConfig` 构造点**，只有 3 处有 `ctx`：

| 行 | 所在方法 | 有 ctx？ | 解析方式 |
| --- | --- | --- | --- |
| `:99` | `StartAlgorithm` | ✅ | 调 `ResolveBrokerPaths`，结果存入 `w.brokerSocks` |
| `:182` | `UpgradeAlgorithm` | ✅ | 同上（升级时任务绑定可能已变） |
| `:225` | `rollbackToOldLocked` | ✅ | 同上（回滚目标版本的订阅可能不同） |
| `:478` | `superviseLoop` 崩溃重启 | ❌ **无 ctx，无解析时机** | **复用 `w.brokerSocks`** |

⚠️ **`:478` 是冷路径陷阱**：若重启时重新调解析（无 ctx）或退回全局单值，runner 会拿不到正确的 `--broker` → 每轮重启 exit 5 → 退避 → 熔断 `CRASH_LOOP`。即「broker 宿主跑起来了，但任何一次崩溃重启都会自杀」。

**定案**：`workerWatcher` 新增字段 `brokerSocks []string`，在 3 个有 ctx 的构造点解析并固化，`:478` 直接复用。（AC11 验证）

### 7.3 AI 消费接入（R7）

`ConsumerAI` 当前**零调用**。需要：

1. camera 域在「该相机有 enabled 的 AI 分析任务」时，以 `ConsumerAI` acquire 该流
2. 这会让 native 侧 `has_ai_consumer()` 为真 → `DecodeSession` 自动拉起
3. 同时 native 为该 stream 创建 broker

⚠️ 消费者 ID 分配、引用计数、以及「最后一个 AI 任务删除时 release」的时序需要明确。camera 域已有 `StreamHub`/`StreamDispatcher` 的 per-camera 管理，AI 消费应接入同一套生命周期，不另建。

⚠️ **acquire 的粒度是 `(camera, role)`，不是 camera**：`StreamHub` 现有 acquire 走 `ensureDispatcher(cameraID, role)`（`stream_hub.go:174`），`role ∈ {main, sub}`。但 `AnalysisTask` **只有 `camera_id`，没有 role**。首期必须明确 AI 消费固定走哪个 role（建议 `main`），否则 `StreamHub` 会为一台相机拉起两路 AI 流、产生两个 broker 与两倍 shm 预算。

⚠️ **`ConsumerAI` 的 stream 不能再 `Subscribe`**：`Stream.Subscribe` 硬校验 `kind == ConsumerPreview`（`internal/engine/stream.go:44-46`）。AI 消费者只 acquire 不订阅，这是**正确**的（AI 消费走 `acquire_frame` 帧队列，不走 packet 回调），但实施时不要顺手复刻 dispatcher 的订阅步骤。

⚠️ **AI 消费不应挂到 `StreamDispatcher` 上**：dispatcher 的存在意义是「有 WebSocket 观众时才拉流」（`stream_hub.go` 的按需语义）。若把 AI 消费绑在 dispatcher 上，则「有 AI 任务但没人看直播」时不会拉流——恰好与 R7 的需求相反。需为 AI 消费定义**独立的 acquire 入口**（可与 dispatcher 共享 `StreamHub` 的 consumerID 分配与生命周期，但不依赖 dispatcher 的存在）。

### 7.4 任务变更触发（D5，R7 的变更面）

⚠️ **这是当前规划最大的空洞**。实测：`Service.CreateTask`/`DeleteTask`/`UpdateTask`（`service.go:196,216`、`store.go:334`）**只写 DB**，不触发任何重新解析；`Supervisor.StartAlgorithm` 仅有 `EnableAlgorithm`（`service.go:145`）与启动 auto-start（`module.go:33`）两个调用点。

**后果**：任务里新建 `AnalysisTask(camera2, A)` → **什么都不会发生**。A 的 runner 早已启动并固化了 `--broker` 列表，camera2 也不会被 acquire。R7/AC5 无法闭环。

**定案 D5**：`AnalysisTask` 的增/删/改 → 对该包执行既有 `StopAlgorithm` + `StartAlgorithm`。

| 项 | 结论 |
| --- | --- |
| 触发点 | `Service.CreateTask` / `DeleteTask` / `UpdateTask`（需新增 `UpdateTask` 的 Service 层方法；当前只有 Store 层） |
| 动作 | 若该 `algorithm_id` 的包当前为 `ACTIVE`（`watchers` 中存在）→ `StopAlgorithm` → 重新解析 → `StartAlgorithm` |
| 包未运行 | 不做任何事（下次 `EnableAlgorithm` 时自然解析） |
| 分析中断 | **接受**。父任务 R7 已定「仅重启对应算法包，允许该包分析短暂中断」 |
| 抖动防护 | **已实现**：`Service.scheduleAlgorithmRestart` 用 `time.AfterFunc` 做短窗口合并（`defaultRestartDebounce = 200ms`），窗口内同一包的连续变更只触发一次重启；`FlushPendingRestarts` 供测试与停机前收敛。 |
| 失败处理 | 重启失败不阻塞 HTTP 响应？⚠️ **需定案**：返回错误（事务语义）还是仅记录 + 状态转 `CRASH_LOOP`（最终一致）。建议后者，与既有 supervisor 行为一致 |

⚠️ 这与 `Supervisor.UpgradeAlgorithm`（`:146`）不是一回事：升级是换版本，这里是**同一版本换订阅集**。不要复用升级路径（它会写 `previous_version` 并触发回滚守卫）。

## 8. runner 多连接

`runner.cpp:161` 的 `run_ipc_worker(so_path, broker_sock, client_name, max_frames)` 当前单 socket。改为：

```cpp
// 参数收集
std::vector<std::string> broker_socks;
// --broker 可重复
if (argv[i] == "--broker") broker_socks.push_back(argv[++i]);

// 每路一个连接 + 独立 lease/ACK 循环
```

⚠️ **`HandshakeReq.client_name[32]` 是定长**（`ipc_protocol.hpp:37`），多路时需区分命名（如 `<name>#<idx>`）且总长 ≤32 字节（`algo-plugin-contract.md` §7.1 已规定 `name` 上限）。超长必须**显式报错**而非静默截断。

### 8.1 部分连接失败语义（D6）

**定案：任一路 `--broker` 连接失败即整体失败**，退出码保持 `5`（`algo-plugin-contract.md` §5.2 表**不变**），stderr 的 `{"stage":"connect"}` 载荷额外带上失败路径与路序号。

理由：

- runner 无法区分「该路暂时无流」与「路径配置错误」——两者都是 `ENOENT`；
- 部分连接下继续运行会让 AC4 的「独立投递」退化为**静默半瘫**（运维看到 runner 在跑，但某路永远无结果）；
- 符合父任务 R8「有界恢复，不盲目重启」。

⚠️ 相应地，`process.go` 的 `FailureStageBrokerConnect` 检测（`exitCode == 5` → stderr 主通道 + 退出码备通道，`process.go:37-41,89-92`）**不需要改**，这正是选 D6 的附带好处。

### 8.2 多路时的 task 身份隔离（✅ 已定案：方案 A，task_id 含 stream_id）

父任务 `design.md:242` 要求「固定 task→worker 亲和分派，同一任务在途最多一帧」。但现状 `runner.cpp:184` 对所有连接**只创建一次** `worker` 与 `task`（`task_id=1`），且 `run_ipc_worker` 只接受**单个** socket。多路时：

| 候选 | 做法 | 代价 |
| --- | --- | --- |
| **A. 一路一 worker（一路一线程）** | 每路 stream 各自 `worker_create` + `task_open(stream_id)`，各自 ACK 循环 | 资源随路数线性增长（每 worker 一份执行上下文/私有缓冲） |
| **B. 单 worker + stream 作为 task 维度** | 一个 worker 内用 `task_id = stream_id` 开多个 task | ⚠️ 需确认插件 SDK 允许多 task 并发于同一 worker（`plugin.h` 未明示）；若不支持则不可行 |
| **C. 一路一组 worker** | 按 `sample_fps` 或路数分组 | 最复杂，首期不建议 |

**已实现方案 A（一路一 worker）**：每路 `--broker` 各自 `worker_create` + `task_open`，各自独立的 lease/ACK 循环。

✅ `task_id` 已**包含 stream_id**：runner 从 `--broker` 字面路径按
`<base_dir>/zhulong_broker_<stream_id>.sock` 反解出 stream_id 作为 `task_id`
（`runner.cpp` 的 `parse_stream_id_from_socket`），无需扩展 IPC 协议。
⚠️ 反解失败（路径不符合派生规则）时 runner 以 exit 5 显式报错，不静默退化。

⚠️ 仍未实测每 worker 的资源开销（父任务 AC4 要求「验证线程数不等于硬件并发度」）——列为本任务残余验证项。

## 9. socket 路径语义定案（R8）

**定案：字面 socket 路径。**

```text
native Engine 派生：socket_path = <base_dir>/zhulong_broker_<stream_id>.sock
        ↓ Zhulong_stream_get_broker_path
Go 拿到完整路径字符串
        ↓ ProcessConfig.BrokerSocks
runner --broker <字面路径>  →  UnixSocketClient::connect(path)   ✅ 语义一致
```

**`module.go:82-101` 的注释必须更新**：从「决策待定，暂用字面路径以免误导」改为「已定案为字面路径，由 native 派生」。同时 `.trellis/spec/native/algo-plugin-contract.md` §5.1 的「语义不对称」段落与 §5.3 的「未实现登记」段落需同步更新。

### 9.1 `ZHULONG_FRAME_BROKER_SOCK` 归属（D8）

⚠️ 定案为「字面路径」后，该环境变量的归属**必须一并定案**，否则会留下两个真实源。

| 项 | 现状 | 定案 |
| --- | --- | --- |
| `algo-plugin-contract.md` §5.1 声明 | 值是**目录** | 保留「目录」语义 ✅ |
| `module.go:82-101` 实际用法 | 当作**字面路径**传给 runner | **删除该读取** |
| `design.md` 阶段 B 的 `broker_base_dir` | 与「同名环境变量」对齐（语义模糊） | 即此变量，**native 自己 `getenv`** |
| Go 侧职责 | 读取并透传 | **不再读取、不再传参** |

**理由**：路径已由 native 派生（D2），Go 传任何值都只会制造第二个真实源。变量名保留（历史原因），语义在 §5.1 明确为「native 的 base_dir，默认 `/tmp`」。

⚠️ `module.go` 的默认值 `/tmp/zhulong_framebroker.sock` 及其全部注释一并删除——它们的存在前提（「Go 无法派生 per-stream 路径」）仍然成立，但结论已从「传一个占位值」变成「不传」。

## 10. 共享内存命名与清理

`shm_name_ = "/zhulong_shm_" + std::to_string(stream_id_)`（`frame_broker.cpp:19`）。

| 项 | 现状 | 结论 |
| --- | --- | --- |
| `start` 前 unlink 残留 | ✅ `shm_pool.cpp:61-62` `shm_unlink` 预防残留 | 无需改动 |
| `stop` 时 unlink | ✅ `shm_pool.cpp:140-142`（`is_owner_` 时） | 无需改动 |
| socket 文件 unlink | ✅ `unix_socket.cpp:100,174` | 无需改动 |
| stream_id 复用 | `Engine::next_stream` 单调递增（`engine.hpp:35`） | ⚠️ 进程内不复用，但**跨进程重启后从 1 开始**——若前次进程崩溃未 unlink，残留会被 `start` 的预防性 unlink 清掉 |

**结论**：清理机制已完备，本任务不需要改动。但需在测试中验证「宿主崩溃后重启能正常 start」。

⚠️ **slot 预算**：`DEFAULT_SLOT_COUNT=8` × `DEFAULT_SLOT_SIZE=4MB` = **32MB/stream**（每 stream 一份，见 §1.1）。多路相机时线性增长：1 路 32MB、4 路 128MB、8 路 256MB。

⚠️ 这个预算需要确认是否可接受（尤其 RK3568 内存有限）。若过大，应通过 `BrokerOptions` 调小 `slot_count`（帧队列只有 3 帧，8 个槽位可能偏多）。**本任务先按默认值实现，把预算核算列为验证项**。

⚠️ 补充：D7 节流**不改变静态预算**（槽位在 `start` 时一次性分配），但会降低稳态的 `LEASED` 驻留时间，从而让 `find_or_evict_free_slot`（`frame_broker.cpp:70-114`）更多走「有空闲槽位」而非「驱逐超时槽位」路径。预算核算时请按**最坏情况**（`slot_count` 全部在途）而非稳态估算。

## 11. 验证路径

| 层 | 命令/方法 | 说明 |
| --- | --- | --- |
| native 单元 | `make native-test` | 既有 `frame_broker_test.cpp` 回归；新增 broker 池生命周期测试 |
| 端到端 | 真实 loopback RTSP + runner | RTSP → 解码 → broker → runner → `process` 被调用（AC8） |
| ABI | `readelf -d` + C 编译测试 | 新增函数不破坏既有 ABI |
| Go | `make go-check` | `-race` 强制；`ipc_contract_test.go` 的跨语言常量比对 |
| 多路 | 2 路 loopback + 2 个 `--broker` | 独立 lease/ACK（AC4） |
| 慢包 | sleep runner | Drop Latest，不阻塞采集（AC6） |
| 崩溃 | kill runner / kill 宿主 | lease 回收、槽位回 FREE、重启可 start（AC7） |
| **变更面** | 运行期增/删 `AnalysisTask` | 该包重启且 `--broker` 列表随之变化（AC9） |
| **节流** | 统计 `publish_frame` 调用率 | 与 `max(sample_fps)` 一致（AC10） |
| **重启路径** | `kill -9` runner | 自动重启仍带完整 `--broker`（AC11） |
| **跨层停机顺序** | 停机时观察日志/`dmesg` | camera → algorithm → native；无 use-after-free |

## 12. 残余风险

- ⚠️ **`build.py` 归档清单扩展**是本任务唯一的真实构建风险（host + cross 双路径、内容寻址快照、`-L` 顺序、`-lrt` 位置）。
  ~~CMake 静态库互链声明顺序~~ → **已实测推翻，非风险**，见 §5.1。
- ⚠️ **域间耦合**（§7.2）：`algorithm` 需知道 camera→stream 映射，方案 B 的接口边界需 review。
- ⚠️ **任务变更触发**（§7.4）：新增「任务 CRUD → 重启包」的依赖边，需防抖动合并；错误语义需定案。
- ⚠️ **多路 task 状态隔离**（§8.2）：已定案并实现（一路一 worker，task_id 含 stream_id）；**每 worker 的资源开销仍未实测**。
- ⚠️ **共享内存预算**（§10）：多路时 32MB/路 可能过大。
- ⚠️ **`engine.h` 无 ABI 版本协商**：新增函数只能追加，无法协商，既有状况。
- ✅ **AI 消费的 role 已定案**（§7.3）：固定 `main`（`StreamRoleMain`），不会翻倍流数与 shm 预算。
- ✅ **`sample_fps` 如何送到 native 已定案**（§4.1）：追加式 ABI `Zhulong_stream_set_publish_rate`。
- ✅ **Go 层停机顺序已有守护**（§3.1）：`internal/app/runtime_test.go` 的 `TestAIShutdownOrderPrecedesNativeClose` 断言 camera → algorithm → native 的相对顺序。
- ⚠️ 端到端验证依赖真实 RTSP 源（loopback 桥接已有，见 `ingestion-contract.md`）。
- ⚠️ **与阶段四任务共享构建文件**（`native/CMakeLists.txt`、`native/scripts/build.py`），必须串行执行（本任务在前）。语义上也先于它们：`10-13-algo-rknn-inference-backend` 的 AC4（1/2/4 worker 真机并发）需要真实帧通路，本任务未完成时它只能降级为后端 API 级并发测试。
