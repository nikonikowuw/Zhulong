# FrameBroker 宿主与多路帧分发通路：实施计划

前置：父任务 `10-10-algorithm-plugin-architecture` 阶段二遗留缺口。**语义上先于阶段四两个任务**（RKNN 真链路依赖本任务的帧通路）。

⚠️ **与阶段四任务存在文件共享**：三者都要改 `native/CMakeLists.txt` 与 `native/scripts/build.py`。本任务在这两个文件上的改动（归档清单、`CMAKE_FIND_ROOT_PATH`）是阶段四后端开关的**前置基础**，因此**必须串行，本任务在前**。

⚠️ 本任务内部也有强顺序：**阶段 A → B/C → D/E → F → G**。其中：

- A（链接方向）失败则 B/C 无法编译，必须先验；
- **阶段 E 的 `workerWatcher.brokerSocks` 固化必须在阶段 F 之前**，否则 F 验证变更面时重启路径会 exit 5，无法区分是 F 的错还是 E 的错；
- **D7 的「频率如何送到 native」未定案前不要开工阶段 C**（见 `design.md` §4.1）。

```txt
推荐执行顺序：
  broker-host  →  rknn-inference-backend  →  rga-vision-backend
```

| 任务 | 共享文件 | 各自职责 |
| --- | --- | --- |
| 本任务 | `CMakeLists.txt`、`build.py` | 归档链接方向（`ZhulongEngine` + `ZhulongAlgoIpc`） |
| `rknn-inference-backend` | `CMakeLists.txt`、`build.py`、`runtime_factory.cpp` | 后端开关机制 + RKNN 前缀校验 |
| `rga-vision-backend` | `CMakeLists.txt`、`build.py`、`runtime_factory.cpp` | 视觉侧开关分支（复用 RKNN 建立的机制） |

⚠️ 本任务还改 `engine.h`（新增 C ABI 查询）；经核实，阶段四两个任务均**不改** `engine.h`，无冲突。

## 1. 实施清单

### 阶段 A：构建链接方向（先做）

- [ ] 扩展 `native/scripts/build.py`：最终 Go 链接从「单个 `libZhulongEngine.a`」改为「`libZhulongEngine.a` + `libZhulongAlgoIpc.a`」。
  - ⚠️ **顺序**：`ZhulongEngine` 依赖 `ZhulongAlgoIpc`，`-lZhulongEngine` 必须在 `-lZhulongAlgoIpc` **之前**（GNU ld 顺序敏感）。
  - ⚠️ **内容寻址各自独立**：两个归档各有自己的 `digest(archive)` → `go-link/<hash>/` 子目录，不共用。
  - 验证：`build/native/host/go-link.json` 的 `CGO_LDFLAGS` 含两个归档路径。
- [ ] `native/CMakeLists.txt`：`target_link_libraries(ZhulongEngine PUBLIC ZhulongAlgoIpc)`。
  - ⚠️ **不需要调整声明顺序**。`design.md` §5.1 已实测：CMake 对未定义目标名做延迟解析，`ZhulongAlgoIpc` 定义在 `ZhulongEngine` 之后**可直接前向引用**（cmake 3.31.6 实测 configure + build 均通过）。
  - ⚠️ **不要为了「预防」而重排目标声明顺序**——那会产生与需求无关的 diff。仅当 CMake 实际报错时才动。
  - 验证：`cmake` 配置无循环依赖错误；`make native-test` 通过。
- [ ] 确认 CPU/host 与 cross/rk3568 两条路径都能链接。
  - ⚠️ **`-lrt` 位置**：`ZhulongAlgoIpc` 在 `UNIX AND NOT APPLE` 下链 `rt`（`CMakeLists.txt:178-180`）。交叉时 `-lrt` 必须排在两个归档**之后**（由 `build.py` 的 `ldflags` + `--sysroot` 解析；rk3568 sysroot 下 `librt.so`/`librt.a` 均存在）。
  - 验证：`make native-build` + `CROSS_PROFILE=native/profiles/rk3568.json CROSS_OUTPUT=build/targets/rk3568/Zhulong make native-build`（**只编译，不执行测试**）。

### 阶段 B：Engine 持有 broker 池

- [ ] `Engine` 新增 `brokers` 池（`std::map<Zhulong_stream_id, std::unique_ptr<ipc::FrameBroker>>`）与 `broker_base_dir`，受既有 `control` 锁保护。
- [ ] AI 消费者首次出现时创建 broker；最后一个 AI 消费者 release 时停止并移除。
  - 复用既有 `has_ai_consumer()`（`stream.cpp:350`）的判定逻辑，不新增判定。
  - 验证：单元测试断言 acquire/release 时 broker 创建/销毁。
- [ ] `Engine::stop()` 中停止所有 broker。
  - ⚠️ **顺序**：broker 必须在 `Stream::worker` join **之前**停止（`pipeline-guidelines.md` §4 五步停机）；放在既有「锁外停止」阶段。
  - 验证：停机无 use-after-free（ASan）。
- [ ] `broker_base_dir` 由 **native 自己** `getenv("ZHULONG_FRAME_BROKER_SOCK")` 读取（默认 `/tmp`）。
  - ⚠️ **D8 定案**：Go 侧**不再**读取该变量、**不再**传给 runner。
  - 验证：清空该变量时 broker 落 `/tmp`；设为目标目录时落到该目录；Go 侧不再出现对该变量的读取。

### 阶段 C：帧发布线程（含 D7 节流）

⚠️ **前置**：D7 的「`sample_fps` 如何送到 native」必须先定案（`design.md` §4.1，建议新增追加式 ABI `Zhulong_stream_set_publish_rate`）。未定案不要开工。

- [ ] 新增 `FramePublisher`（每 AI 流一个线程）：循环 `stream_->acquire_frame(frame, 50ms)` → **按 `max(sample_fps)` 节流** → `broker_->publish_frame(*frame)`。
  - ⚠️ **节流在发布侧，不是消费侧**：未到期的帧直接 `frame.reset()` 丢弃，**不进 `publish_frame`**（否则 shm 拷贝与 socket 通知的开销照旧）。
  - ⚠️ **发布后立即 `frame.reset()`**，不持有到推理完成（`pipeline-guidelines.md` §3「防慢算法拖垮解码器准则」）。
  - ⚠️ 发布路径不得在 capture/decode 回调或队列锁内（父任务 `design.md` §5）。
  - ⚠️ 不阻塞：`publish_frame` 的 socket 发送已有 `SO_SNDTIMEO` 50ms（`unix_socket.cpp:201`）。
  - 验证：AC2（帧到达 runner）+ AC10（`sample_fps=5` 时 25fps 流的 `publish_frame` 调用率约 5fps；改为 25 后随之上升）。
- [ ] 线程生命周期与 `DecodeSession` 绑定（AI 消费者消失时随会话一起停）。
  - 验证：AI 消费者增删循环无线程泄漏。

### 阶段 D：C ABI 查询

- [ ] `engine.h` 追加 `Zhulong_stream_get_broker_path`（**追加式**，不改既有签名与结构体布局）。
  - 参数：`engine`、`stream`、`char *out_path`、`size_t *inout_len`（双向）。
  - 错误：`INVALID_ARGUMENT` / `NOT_FOUND`（无 AI 消费者 → broker 未创建）/ `BUFFER_TOO_SMALL`（回填所需大小）。
  - ⚠️ 路径长度上限定义（建议 256）。
  - ⚠️ `engine.h` 无 ABI 版本协商机制（既有状况），如实记录，不引入版本机制。
- [ ] `src/abi/engine.cpp` 实现，全异常 `try/catch(...)` 包裹（`directory-structure.md` §2 铁律 2）。
  - 验证：C 编译测试 + `readelf -d` 确认既有 ABI 未破坏。
- [ ] Go `internal/engine` 暴露 `Stream.BrokerPath(ctx) (string, error)`。
  - 验证：Go 单元测试（真实 loopback RTSP）。

### 阶段 E：Go 多路订阅

- [ ] `ProcessConfig.BrokerSock string` → `BrokerSocks []string`；`process.go:200-201` 循环追加 `--broker`。
  - 验证：单元测试断言 N 个 `--broker` 参数。
- [ ] `internal/algorithm` 定义窄接口 `BrokerPathResolver`（`ResolveBrokerPaths(ctx, algorithmID) ([]string, error)`）。
  - ⚠️ **不直接依赖 camera 域**（当前 `internal/algorithm` 对 camera 零依赖）。
  - 实现放 `internal/app` 或 camera 域 adapter，符合 Fx 装配约定（`dependency-injection.md`）。
- [ ] **`workerWatcher` 新增 `brokerSocks []string`**，在 3 个有 ctx 的构造点（`supervisor.go:99,182,225`）解析并**固化**。
  - ⚠️ **这是本阶段最关键的一步**。`supervisor.go:478`（`superviseLoop` 崩溃重启）**无 `ctx`、无解析时机**，必须直接复用 `w.brokerSocks`。
  - ⚠️ 若漏了这一步：broker 宿主跑起来后，任何一次 runner 崩溃重启都会因缺 `--broker` 而 exit 5 → 退避 → 熔断 `CRASH_LOOP`（即「能启动但不能恢复」）。
  - 验证：AC11——`kill -9` runner，自动重启仍带完整 `--broker` 列表。
- [ ] `Supervisor` 从构造时全局单值改为**按算法包解析**（`supervisor.go:19,99,182,225,478` 全部涉及）。
  - 验证：一个算法包关联 2 个 task 时得到 2 个路径。
- [ ] `module.go:82-101` 的注释与默认值**一并删除**（D8）：Go 侧不再读取 `ZHULONG_FRAME_BROKER_SOCK`、不再传值。
  - ⚠️ 保留「Go 无 stream_id」这一事实说明（它仍未变），但结论从「传一个占位值」变为「不传」。
  - 验证：`grep -rn ZHULONG_FRAME_BROKER_SOCK internal/` 无命中。

### 阶段 F：AI 消费接入与任务变更触发

- [ ] camera 域在「该相机有 enabled AI 分析任务」时以 `ConsumerAI` acquire 流。
  - ⚠️ `ConsumerAI` 当前**零调用**（`types.go:33` 定义、`validation.go:48` 允许）。
  - ⚠️ **acquire 粒度是 `(camera, role)`，不是 camera**：现有入口是 `ensureDispatcher(cameraID, role)`（`stream_hub.go:174`），而 `AnalysisTask` **只有 `camera_id`、无 role**。首期必须固定一个 role（建议 `main`），否则会翻倍流数与 shm 预算。
  - ⚠️ **不要复用 `StreamDispatcher`**：dispatcher 的存在语义是「有 WebSocket 观众才拉流」，与「有 AI 任务就拉流」相反。需定义**独立的 AI acquire 入口**（可共享 `StreamHub` 的 consumerID 分配与生命周期，但不依赖 dispatcher 存在）。
  - ⚠️ **AI 消费者不得 `Subscribe`**：`Stream.Subscribe` 硬校验 `kind == ConsumerPreview`（`internal/engine/stream.go:44-46`）。AI 消费走 `acquire_frame` 帧队列，不走 packet 回调——这是正确的，不要顺手复刻订阅步骤。
- [ ] 最后一个 AI 任务删除时 release。
  - 验证：AC5——`DecodeSession` 按需拉起/销毁可观测。
- [ ] 消费者 ID 分配与引用计数（复用既有 `consumerID` 机制，不新造）。
- [ ] **任务变更触发（D5，本阶段的核心缺口）**：`Service.CreateTask`/`DeleteTask`/`UpdateTask` 中，若该 `algorithm_id` 的包处于 `ACTIVE` → `StopAlgorithm` → 重新解析 → `StartAlgorithm`。
  - ⚠️ 当前 `Service` **没有** `UpdateTask` 方法（只有 `Store.UpdateTask`），需新增。
  - ⚠️ **不要复用 `UpgradeAlgorithm`**（`:146`）：它会写 `previous_version` 并触发回滚守卫，而这里只是「同版本换订阅集」。
  - ⚠️ 需**防抖动合并**（批量建 N 个任务 → N 次重启），建议 Service 层短窗口合并；阈值待定。
  - ⚠️ 需定案**重启失败的响应语义**（返回 HTTP 错误 vs 仅记录 + 转 `CRASH_LOOP`），建议后者。
  - 验证：AC9——运行期新建 `AnalysisTask(camera2, A)` → A 的 runner 以 2 个 `--broker` 重启；删除 → 回到 1 路。

### 阶段 G：runner 多连接

- [ ] `runner.cpp` 支持 `--broker` 重复（`std::vector<std::string>`），每路独立连接 + 独立 lease/ACK 循环。
  - ⚠️ **`HandshakeReq.client_name[32]` 定长**：多路命名需区分且总长 ≤32 字节；超长**显式报错**，不静默截断（`algo-plugin-contract.md` §7.1）。
  - ⚠️ **任一路连接失败即整体失败**（D6），退出码保持 `5`，stderr 带上失败路径。**不要**定义新的「部分失败」退出码——那会让 `process.go` 的 `exitCode == 5` 检测失效。
  - ⚠️ **task 身份隔离（§8.2，实施前必须定案）**：现状 `runner.cpp:184` 对所有连接只创建一次 `worker`/`task`（`task_id=1`），多路下会冲突。首期建议**一路一 worker**，`task_id` 必须含 `stream_id`。
  - 验证：AC4——2 路 broker 独立投递、独立 lease/ACK。
- [ ] `algo-plugin-contract.md` §5.2 退出码表**保持 5 的语义不变**（D6 定案），仅补充「多路时任一路失败即整体失败」的说明。

### 阶段 H：spec 更新与验收

- [ ] 更新 `.trellis/spec/native/algo-plugin-contract.md`：
  - §5.1 「语义不对称」→ 定案为字面路径；`ZHULONG_FRAME_BROKER_SOCK` 明确为 **native 的 base_dir**（D8）
  - §5.2 退出码表 → 补充「多路任一路失败即整体失败」，**数值不变**
  - §5.3 「未实现登记」→ 移除或改写为已实现
- [ ] 更新 `.trellis/spec/architecture.md`「尚未交付」清单（移除 FrameBroker 宿主相关项，若在其中）。
- [ ] 更新 `.trellis/spec/backend/dependency-injection.md`：记录 **Go 层停机顺序约束**（camera → algorithm → native），说明其存在理由是「broker 先于 native 停止」。
- [ ] 更新 `.trellis/spec/native/pipeline-guidelines.md`（若适用）：记录发布侧节流与「AI 消费不走 Subscribe」两条约束。
- [ ] AC1–AC11 逐项核对。
- [ ] 共享内存预算核算（8 槽位 × 4MB = 32MB/路，按最坏情况估算）并记录结论。
- [ ] 记录残余风险。

## 2. 验证命令

```bash
# native 全量（含既有 frame_broker_test）
make native-test

# Go 侧（-race 强制）
make go-check

# 交叉编译（只编译不执行）
CROSS_PROFILE=native/profiles/rk3568.json CROSS_OUTPUT=build/targets/rk3568/Zhulong make native-build

# 链接方向核对（应同时出现 ZhulongEngine 与 ZhulongAlgoIpc，且 Engine 在前）
grep -o 'libZhulong[A-Za-z]*\.a' build/native/host/go-link.json

# D8：Go 侧不应再读取该环境变量
grep -rn ZHULONG_FRAME_BROKER_SOCK internal/ || echo "OK: no Go-side reader"

# 收工门禁
make check
```

⚠️ **只跑 `make go-check` 是不够的**：阶段 A 改了 Go 链接参数，任何依赖 `internal/engine` 的包都必须走 wrapper（项目 `AGENTS.md`：「绝不直接跑裸 `go build`/`go test`/`go vet`」）。

## 2.1 验收命令对照

| AC | 验证手段 |
| --- | --- |
| AC1 | `make native-test` + broker 池生命周期单测 |
| AC2 | 真实 loopback RTSP → runner 收 `FRAME_DISPATCH`；发布后解码池未耗尽 |
| AC3 | C 编译测试 + `Stream.BrokerPath` 单测；路径与 `FrameBroker::socket_path()` 一致 |
| AC4 | 2 路 loopback + 2 个 `--broker`，各自 lease/ACK 独立 |
| AC5 | 运行期增删 `AnalysisTask`，观测 `DecodeSession` 拉起/销毁 |
| AC6 | sleep runner 触发 Drop Latest，采集/其他包不受阻 |
| AC7 | kill runner / kill 宿主 → lease 回收、槽位回 FREE |
| AC8 | 端到端 + `make native-test` + `make go-check` |
| AC9 | 运行期新建/删除 `AnalysisTask` → 该包以新 `--broker` 列表重启 |
| AC10 | 统计 `publish_frame` 调用率 vs `max(sample_fps)` |
| AC11 | `kill -9` runner → 自动重启仍带完整 `--broker`（不进入 CRASH_LOOP） |

## 3. 回退点

- 阶段 A 失败（`build.py` 归档清单）：回到单归档链接，broker 宿主改用方案 C（源文件并入 `ZhulongEngine`），接受结构代价。
- 阶段 B/C 失败：broker 池与发布线程可独立禁用，既有 preview/recording 路径不受影响。
- 阶段 D 失败：C ABI 新函数可单独回退，不影响既有 ABI。
- 阶段 E 失败：Go 侧多路订阅可回退为单路（保持 `BrokerSocks` 单元素），端到端仍可跑一路。
- 阶段 F 失败（任务变更触发）：可先交付「仅启动面解析」的版本（AC1–AC8、AC10、AC11 仍可验），把 AC9 标为未达成并拆出后续任务。⚠️ 不得把「AC9 未验」当作 AC9 已通过。
- 阶段 G 失败：runner 可保持单 `--broker`，多路降级为「一包一路」。
- 任何阶段：不改 `FrameBroker` / `ipc_protocol` / `shm_layout` 的既有签名与结构体布局；不改 `IInferenceBackend`/`IVisionBackend`；不改 preview/recording 行为。

## 4. 已知风险

- ⚠️ **`build.py` 归档清单扩展**（design §5）——本任务**唯一**的真实构建风险（host + cross 双路径、内容寻址快照、`-L` 顺序、`-lrt` 位置）。
  - ~~静态库互链的 CMake 声明顺序~~ → **已实测推翻，非风险**（design §5.1）。
- ⚠️ **`internal/algorithm` → camera 的域间耦合**——用窄接口隔离，边界需 review。
- ⚠️ **任务变更触发（D5）**——新增「任务 CRUD → 重启包」的依赖边，需防抖动合并；失败响应语义待定。
- ⚠️ **多路 task 状态隔离**（design §8.2）——**未定案**，一路一 worker 的资源开销需实测。
- ⚠️ **共享内存预算** 32MB/路，多路时可能过大（RK3568 内存有限）。
- ⚠️ **AI 消费的 role 未定**（design §7.3）——`AnalysisTask` 无 role 字段，若不定会翻倍流数与 shm 预算。
- ⚠️ **`sample_fps` 如何送到 native 未定**（design §4.1）——阻塞阶段 C。
- ⚠️ **Go 层停机顺序无守护**（design §3.1）——现状正确但无测试，改动不会失败。
- ⚠️ **`engine.h` 无 ABI 版本协商**——只能追加。
- ⚠️ **停机顺序**——broker 必须在流 worker join 前停止。
- ⚠️ 端到端依赖真实 RTSP（loopback 桥接已有）。
- ⚠️ **gallery 热更新链路**（`algo-plugin-contract.md` §7.3）依赖本任务的宿主归属，但属独立交付，不在本任务范围。
