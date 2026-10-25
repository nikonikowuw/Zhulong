# PRD: 算法包黑盒化 —— 从 dlopen `.so` 迁移到独立可执行文件

## Goal

让算法包对宿主成为**真正的黑盒**：算法包以独立可执行文件交付，**静态链接**自身所需的算法运行时；宿主只负责「喂一个帧对象 + 推理参数、收结构化结果」，不向算法包暴露任何实现符号、私有头文件或动态库。

同时**严格保留**现有的「一包一进程、进程内多 Worker 线程、进程内共享模型权重」模型 —— 后者是 RKNN `rknn_dup_context` 共享物理权重的前提，不可退化为「一线程一进程」。

**核心价值**：彻底消除插件与宿主之间的 C++ ABI 耦合，使算法包可以独立编译、独立分发、独立验证，甚至用非 C++ 语言实现；宿主不必再承诺任何二进制兼容，也就不再需要 SONAME 版本化、注入式函数表、`struct_size` 握手这一整套 ABI 债。

> 决策背景（不必在实现中复述）：本需求经过与「函数表注入式」「SONAME 主版本化」两条路线的对比。注入式仍是「插件依赖宿主提供的服务」，只是把链接期耦合挪到运行期；静态链接才是黑盒的彻底形态。参考先例：LSP（VS Code ↔ 语言服务器）用「独立进程 + 协议」获得零 ABI 风险与语言无关性；Frigate 用「独立 detector 进程 + 共享内存」获得同样的隔离，其官方架构目标与本系统的「一包一进程」逐字同构。

---

## Background：已确认的仓库事实

| 编号 | 事实 | 证据 |
| :--- | :--- | :--- |
| F1 | `plugin.so` 存在 **9 个指向宿主库的未定义符号**，即「黑盒被捅穿」的实证 | `nm -D --undefined-only build/native/host/libFaceRecognitionPlugin.so`：`get_default_inference_backend` / `get_default_vision_backend` / `runtime::GalleryRegistry::instance` / `GalleryRegistry::snapshot` / `platform::DmaBufAllocator::{allocate,instance}` / `platform::DmaBuffer::{sync_start,sync_end,~}` |
| F2 | 插件 CMake 目标显式链接动态运行时库 | `native/CMakeLists.txt:355-386`（`FaceRecognitionPlugin` / `PlateRecognitionPlugin` 均 `target_link_libraries(... ZhulongAlgoRuntime)`） |
| F3 | SDK 门面**反向** `#include` 宿主私有头，第三方编译必须 `-I native/src` | `native/sdk/include/zhulong/algo.hpp:21-24`（`<algorithm/runtime/runtime_factory.hpp>` 等 4 个）；被包含头位于 `native/src/algorithm/runtime/` |
| F4 | 仓库已自行记录该耦合不可持续 | `native/src/algorithm/runtime/runtime_factory.hpp:7-10`：「插件通过 `#include` 直接依赖本私有头，因此具体后端实现**必须**编译进 `libZhulongAlgoRuntime.so`」 |
| F5 | C++ 虚表跨 `.so` 边界已**实测段错误** | `native/src/algorithm/runtime/inference_backend.hpp:46-52`：「增删虚函数会让已编译的旧插件在新运行库上调用错槽位（实测：新增一个虚函数后，旧 face 插件在 forward 槽位上直接段错误）」 |
| F6 | 运行时库 SONAME **无版本号**，无法靠 SONAME 拦住 ABI 漂移 | `readelf -d build/native/host/libZhulongAlgoRuntime.so` → `SONAME: libZhulongAlgoRuntime.so` |
| F7 | 安装后的算法包目录**不含**运行时库，靠 runner 进程已加载的同名对象满足 | `data/algorithms/face_recognition/1.0.0/` = `manifest.json` + `config.schema.json` + `models/` + `plugin.so` + `sample.jpg` |
| F8 | runner 线程模型为**一 broker 一线程**，每个线程一个 Worker 会话 | `native/src/algorithm/runner/runner.cpp:968-974`（`threads.reserve(broker_socks.size())`，逐 broker `run_single_worker`） |
| F9 | 三层资源生命周期已在 ABI 中显式建模：Model Group（进程内共享）→ Worker Context（线程独占）→ Task State（单路时序） | `native/sdk/include/zhulong/plugin.h:264-312`（`model_create` / `worker_create` / `task_open`） |
| F10 | 宿主通过 argv 告知插件路径与订阅集 | `internal/algorunner/process.go:142`（`"--plugin", p.config.PluginPath`）、`:147`（`--broker`）、`:152`（`--name`）、`:155`（`--algorithm-id`）、`:158`（`exec.Command(p.config.RunnerPath, args...)`） |
| F11 | 孤儿清扫按「`argv[0]` 基名 == runner 路径基名」认亲，并刻意避免反向判定 | `internal/algorunner/orphan_linux.go:80`（`runnerBase := filepath.Base(runnerPath)`）、`:221`（`filepath.Base(argv[0]) != runnerBase`）；调度点 `internal/algorithm/module.go:36-38` |
| F12 | runner 路径是全局单一常量，所有算法包共用同一二进制 | `internal/app/app.go:590` `DefaultAlgorithmRunnerPath` |
| F13 | 算力预算以 `compute_cost` 全平台累加 ≤ 1000 点表达，与进程/线程数无耦合 | `.trellis/spec/backend/algorithm-guidelines.md` §2.1 |
| F14 | 黑盒边界上流动的**只有数据**：SHM 帧视图（POD，无 C++ 类型）+ JSON 参数 + JSON 结果 | `native/sdk/include/zhulong/plugin.h`（`zhulong_frame_view`）、`internal/aitypes/model.go`（`RawInferencePayload`） |
| F15 | 验包 CLI 已存在且为黑盒形态（`--verify-plugin <so> --sample <img>`） | `.trellis/spec/backend/algorithm-guidelines.md` §5 |
| F16 | 宿主侧还有**第二个**算法代码消费者：native engine 通过 `dlopen` 加载 `libFaceRecognitionPlugin.so`、`dlsym` 取 `zhulong_face_extract_feature`，是人员底库登记的唯一特征来源 | `native/src/abi/engine.cpp:516-730`、`internal/engine/engine.go:823`、`internal/person/service.go:606` |
| F17 | 该通道今天没有指向安装目录的寻址路径（候选为 `ZHULONG_FACE_PLUGIN_PATH` / `ZHULONG_PLUGIN_DIR` / 构建产物目录 / `dladdr` 同目录，Go 与 TOML 都不设置这两个变量），但 `make check` 的 `internal/engine` 人脸用例依赖 `build/native/host/libFaceRecognitionPlugin.so` | `engine.cpp:591-650`、`internal/engine/engine_face_test.go:79` |
| F18 | 特征提取的输入在 Go 侧已解码为 RGB24，C ABI 只收裸像素，**包侧不需要图像解码器** | `internal/engine/engine.go:838`（`decodeToRGB24`）→ `engine.cpp:820` |
| F19 | 「一次性运行、读图、输出 JSON、退出」已是既存形态（验包模式） | `native/src/algorithm/runner/runner.cpp:458-580` |
| F20 | 两个参考包**没有自己的构建文件**：target 定义住在宿主构建树（`add_library(... ../packages/.../src/*.cpp)`），包级 ctest 住在 `native/tests/`，打包脚本反向调用宿主构建树取产物 | `find packages -name CMakeLists.txt` 为空；`native/CMakeLists.txt:355-400`；`packages/plate_recognition/scripts/build_package.py:47-63` |
| F21 | **产物本来就是「一个 target 一个包」**：同一 `manifest.id`，`platform.{arch,target}` 不同，宿主按 `[algorithm].target` 匹配。源码层是唯一「一套」的地方 | `packages/face_recognition/targets/cpu/manifest.json` → `x86_64/cpu`；`targets/rk3568/manifest.json` → `aarch64/rk3568` |
| F22 | 「一套源码」已经产出实证缺陷：`resolve_model_path` 用架构宏排出**跨 target 资产回退链**，rk3568 包在找不到本 target 模型时会静默去试另一 target 的路径 | `packages/face_recognition/src/face_recognition.cpp:26-40`（`targets/rk3568/` ↔ `targets/cpu/` 互为首选），`packages/plate_recognition/src/plate_recognition.cpp:79-82`（三个 target 字面量进算法源码） |
| F23 | target 枚举先行、资产滞后：打包脚本声明 `rk3568`/`rk3588`，但 `plate_recognition` 只有 `cpu` 资产（`--target rk3568` 直接 `sys.exit`） | `packages/plate_recognition/scripts/build_package.py:20`、`:35`；`packages/plate_recognition/targets/` 仅 `cpu/` |
| F24 | int8 量化颗粒足够大，使「跨 target 共用一个阈值」不成立：score 输出量化区间 `[0.0002, 0.858]`，256 级 → 颗粒约 `0.0034` | `packages/face_recognition/targets/rk3568/models/det_500m.rknn` 的 `quant_tab:443` |
| F25 | **`cpu` 一词在两处含义不同，且默认值让它看起来像「CPU 推理」**：build 侧的 `cpu` = `cpu_mock` 桩后端（拒绝一切真实模型路径）；包资产侧的 `targets/cpu` 实际是 **ONNX f32 / x86_64** 档位（模型全是 `.onnx`）；`[algorithm].target` 默认值还写着 `"cpu"` | `runtime_factory.cpp:31-49`（`cpu_mock` / `onnx` / `rknn` 三选一）；`native/scripts/build.py:233-247`（host 有 ONNX 前缀默认 `onnx`，交叉默认 `cpu`）；`cpu_inference_backend.cpp:26`（`cpu_mock:` 夹具标记，真实模型一律硬失败）；`packages/plate_recognition/targets/cpu/models/*.onnx`；`config.toml:31` `target = "cpu"` |
| F26 | **`cpu_mock` 是产品代码里的假推理实现，且会被静默选为默认**：桩后端产出合成张量（1MB `0x5A` 权重 + 1024 个 0 的 workspace），唯一屏障是 `cpu_mock:` 前缀约定；交叉 profile 未声明后端时**默认就是它**，于是"编译成功的板端产物"根本无法加载任何真实模型 | `cpu_inference_backend.cpp:36-60`；`native/scripts/build.py:233-247`（交叉一律 `cpu`、host 无 ORT 前缀也 `cpu`）；`native/profiles/rk3568.json` / `linux-arm64.example.json` 未声明后端 → 落到桩；`rk3568-rga.json` 显式 `inference_backend: cpu` |
| F27 | **桩后端让"包级行为测试"在部分机器上 0 覆盖但 CI 是绿的**：host 落到桩时，包生命周期 / 黄金样本 / 特征提取用例全部走 `[SKIP]` 分支 | `face_recognition_plugin_test.cpp:51-53`、`plate_recognition_plugin_test.cpp:78,216,256`、`face_feature_extract_test.cpp:35`、`algo_runtime_test.cpp:76` |
| F28 | 板端确实有"无真实推理也要跑 broker/进程回归"的需求，但它被表达成了产品构建档位而非夹具 | `.trellis/workspace/nikoniko/journal-1.md:1698`（保留 cpu profile 的理由）；`native/profiles/rk3568.json` 的用途注释 |

---

## Requirements

### R1. 算法包以独立可执行文件交付

- 算法包以包自身的可执行文件交付，入口文件名为 `<manifest.id>`；本任务只支持这一种形态，不保留 `plugin.so` / `dlopen` 兼容路径。
- **manifest 不新增字段**：入口文件名由 `manifest.id` 唯一确定，与 `sample.jpg` / `models/` / `.env` 同属约定式契约。`manifest.id` 不得为 `plugin.so`。
- 包根必须包含 `<manifest.id>` 可执行文件，不得包含作为旧入口的 `plugin.so`；缺少新入口或仍包含旧入口均拒绝安装。已安装的旧 `.so` 算法包必须先重建并重新安装，不能依赖新宿主兼容启动。
- **可执行文件必须位于包根、与 `models/` 同级**：现有两个包的模型定位靠 `dladdr` 取模块目录再拼 `models/`（`plate_recognition.cpp:32-53`、`face_recognition.cpp:60`），放子目录会让 `model_create` 在生产环境失败。
- 可执行文件权限 `0755`；归档安全门禁（500MB / 1000 文件 / 1GB / Zip-Slip）不变。

### R2. 运行时静态链接，零 `zhulong` 未定义符号

- 算法包二进制**不得**在动态依赖或未定义符号中出现任何 `zhulong` 符号。
- `ZhulongAlgoRuntime` 必须能作为**静态库**产出并被算法包链接。
- 验收判据（硬性）：
  ```bash
  nm -D --undefined-only <算法包可执行文件> | grep -c zhulong   # 期望 0
  ldd <算法包可执行文件> | grep -ci zhulong                      # 期望 0
  ```
- 允许残留的第三方动态依赖，但处置必须按类别固定，不得默认丢给运维（`design.md` §4.5.4）：
  - **`libonnxruntime.so.1.21` 及其闭包（`onnx` 档位）：随包绑定**——放在包内 `lib/`，可执行文件用 `RUNPATH=$ORIGIN/lib` 解析，归档带 `NOTICE`；
  - **`librknnrt.so` / NPU 驱动（rknn target）：不绑定**——声明 `requires BSP ≥ X` 并在启动时探测，不符即报明确错误；
  - 验包输出必须报告 `runtime: bundled|system (<库名> @ <路径>)`，使运维可确认这台机器到底用的哪份；
  - 来源与版本以 **README / `abi.json` / 验包输出** 声明（**不新增 manifest 字段**，见 R1）。

### R3. 保留一包一进程与进程内多 Worker 线程

- **进程数 = 已启用算法包数**，不是任务数、不是相机路数。16 路相机跑 3 个算法 → 3 个进程。
- 单个算法包进程内必须支持 N 个 Worker 线程并发，且：
  - 模型权重在进程内**共享一份**（RKNN 物理权重共享不得丢失）；
  - 每个 Worker 线程持有**独占**的执行会话；
  - 每路流（`stream_id`）的时序状态（跟踪器等）**线程内独占**。
- 本任务固定保留现状调度：「一 broker 一 Worker 线程」，不在算法包间或 broker 间复用 Worker；Worker 数由该算法包实际订阅的 broker 数决定。
- C++ 门面 `detect(session, image)` 丢失了 C ABI 的 `task`（`algo.hpp:407`），`TaskContext` 也只有一个 `task_id`（`algo.hpp:767-770`）；本任务按 R9 补齐 SDK 公共门面上下文，但不借此引入多 broker 共用 Worker 的调度。

### R4. 保持现有 Worker 调度

- SDK 固定采用「一 broker 一 Worker 线程」，行为与现有 runner 一致；所有订阅的 broker 均须建立连接。
- 本任务不新增 `max_workers` 参数，不从 `compute_cost` 推导线程数，也不实现固定 N Worker 服务多路 broker、批处理或其他策略覆盖。
- `compute_cost` 继续作为算法包资源预算元数据使用，不赋予 Worker 数量语义。若以后需要按任务目标帧率、实测吞吐和设备容量调度 Worker，应另行设计调度器及过载行为。

### R5. 单一可执行文件形态

- 宿主只启动包根 `<manifest.id>` 可执行文件，不检测或分派 `.so` 形态，不提供旧包回退路径。
- 本任务部署前须将需继续使用的算法包重建为可执行文件形态并重新安装；旧 `.so` 包必须在安装期明确拒绝，不能进入启动后的反复重启状态。

### R6. 孤儿清扫适配新形态

- 现有清扫规则依赖「`argv[0]` 基名 == runner 路径基名」（F11），新形态下**每个包的可执行文件名不同**，该规则会失效（漏杀或误杀）。
- 必须改为不依赖二进制名的认领依据，且该依据必须**按宿主实例可区分**：采用本实例算法安装根下的真实可执行文件路径判定（`/proc/<pid>/exe` ∈ `<dataDir>/algorithms/`，见 `design.md` §6.2）。`--broker` 的 socket 目录不可用作依据——它全局落在 `/tmp`，对任意实例恒真。**绝不可**退化为「父进程不是我」这类反向判定（`algorithm-guidelines.md` 记录该缺陷实测杀过活进程）。
- 新建与清扫共享的目录锁语义（`BrokerResourceLock`）不受影响，但 Pdeathsig / 进程组语义必须在新形态下重新验证。

### R7. 底库跨进程传递（**已降级为「保持现状，不改代码」**）

> 决策变更（2026-10-25 文档审阅）：原 R7 要求「去掉 `GalleryRegistry::instance()` 进程内单例，改为注入式只读快照」。在「可执行文件 + 静态链接运行时」形态下，`algo_main` 与算法代码在**同一二进制内**，`GalleryRegistry::instance()` 天然只有一份（`GalleryIndex` 写入侧与 `face_recognition.cpp:184` 读取侧是同一对象），原 R7 要解决的问题不存在；`MSG_GALLERY_SYNC` 本来就是现有唯一入口，无需新增机制。

- **不新增机制、不改代码**：底库继续走既有 `MSG_GALLERY_SYNC` 广播，进程内持有只读快照；本任务**不**把 `GalleryRegistry` 改为可注入实例，也**不**引入 `zhulong::gallery()`。
- 现有的分片（`shard_index` / `shard_total`）与 64KB 载荷上限语义不变，`accumulate_sync_shard` 逻辑不动。
- `implement.md` 阶段 6.1–6.4 的「注入式快照」方案保留为历史记录，**不在本任务执行**（阶段 6 标题已同步标为「不执行」）。若将来出现「同一进程加载两份算法代码」的形态（本任务明确排除），再回到该方案讨论。

### R8. SDK 分发物自足

- SDK 需能作为**独立发行物**交付，使第三方在**没有本仓库**的环境中完成：编译算法包 → 本地验包 → 产出归档。
- 分发物不得包含 `native/src/**` 私有实现头。
- 必须有「干净环境」回归用例证明其自足性（详见 `design.md` 与 `implement.md`）。
- **仓库内的两个参考包必须与第三方走同一条构建路径**：`packages/<id>/<target>/CMakeLists.txt` 自己的 `find_package(ZhulongAlgoSDK)` + `make sdk-install` 产出的 prefix；`make native-build` **不得**再编译任何包代码，包级测试随包走。不允许出现「我们的包走 A 路径、第三方走 B 路径」。
- **打包只有一份实现**：`native/sdk/tools/pack_package.py`（随 SDK 分发），包的 `scripts/build_package.py` 仅为薄壳；`Makefile` 只负责编排（依赖顺序、参数、交叉自动 `--no-verify`），不得用 `cp`/`tar` 重写一遍打包。
- **每个 target 目录自带 Makefile**：`cd packages/<id>/<target> && make / make test / make package / make verify` 即可完成「编译 → 测试 → 验包 → 归档」闭环；该 Makefile 与 SDK 模板**只允许差 `SDK_PREFIX` 默认值一行**，根目录 Makefile 仅逐包委派并聚合归档。
- **一条命令生成包骨架**：SDK 自带 `tools/new_package.py`，生成开箱能 `make` / `make test` / `make verify` / `make package` 的**单 target 实现目录** `packages/<id>/<target>/`（同一 `<id>` 可再生成其它 target 目录；含 `manifest.json`、`CMakeLists.txt`、`Makefile`、`src/<id>.cpp` 空实现、`tests/`、薄壳脚本、README）；`id` 校验复用宿主 `manifestTokenPattern` 并拒绝 `plugin.so`；`make sdk-selftest` 在 `make check` 内每次重新生成并走完全链，防模板腐烂。
- **SDK 是构建期依赖，不是拷贝**：包的开发与发布**不需要**携带 `native/sdk/`；包目录内不得出现 SDK 头/库副本，一律经 `SDK_PREFIX` + `find_package` 引用，发布归档里不含任何 SDK 文件（静态链入）。SDK 以带版本的独立发行物交付，包声明 `SDK_MIN_VERSION`，版本不匹配必须在 configure 阶段报出双方版本号。

### R9. SDK 门面补齐 task/config 上下文（方案 A，已拍板）

- `zhulong::TaskContext` 必须同时携带 `task_id` 与值初始化的 `TaskConfig params{}`；本任务定义 `TaskConfig` 为空值类型，不预设业务字段、不从控制消息填充。
- 门面 `detect` / `classify` / `detect_persons` / `detect_vehicles` 必须接受 `TaskContext&`；当前每个 Worker 持有一路流对应的 context，不引入 task map。
- 算法入口由 `ZHULONG_ALGO_MAIN(PluginClass, ResultAdapter, PluginName, PluginVersion)` 生成；`run<PluginClass, ResultAdapter>` 在头文件模板中编译期绑定并委托 SDK runner。`PluginFactory` 是 SDK detail 层的 C ABI API-table 构造函数指针，不由算法作者实现或作为扩展点发布。SDK 提供四种公开结果适配器。
- 现有 `face_recognition` / `plate_recognition` 入口类同步改签名，**只加参数不读取**。
- 理由：SDK 分发物是本任务核心交付，门面签名一经发布即成为第三方源码契约；现在修复成本最低（第三方为零），发布后再改会破坏所有第三方。

### R10. 宿主侧人脸特征提取改走包可执行文件（一次性提取通道）

- 宿主 engine **不得**再 `dlopen`/`dlsym` 任何算法二进制：删除 `FacePluginLoader` 与 `Zhulong_engine_extract_face_feature` C ABI，宿主进程从此不加载任何算法代码。
- 特征提取由算法包可执行文件的 `--extract-feature` 模式提供：宿主经 stdin 送入**帧流**（帧头 + RGB24/BGR24 裸像素），逐帧从 stdout 收一行 JSON。一次进程可处理 N 帧；单张登记 = 发 1 帧后关 stdin。
- SDK main 以**弱符号**引用 `zhulong_face_extract_feature`：提供该符号的包（`face_recognition`）自动支持；未提供的包（`plate_recognition`）返回 `UNSUPPORTED`。不新增公共门面、不改四种结果适配器、不改 `plugin.h`。
- Go 侧实现「热进程缓存」：单进程串行、并发上限 1、请求超时 30s（到点 kill 进程组）、空闲 60s 回收、单帧最多重试 1 次。缓存 key 含包目录绝对路径与可执行文件身份，包升级/回滚后必须 retire 重建（否则特征空间漂移）。
- 提供该能力的包由配置显式指定（`[person].extractor_algorithm`，默认 `face_recognition`），不依赖相对路径撞运气。
- **语义等价硬要求**：成功 / `NO_FACE_DETECTED` / 输入非法 / 能力不可用 四类结果及其 HTTP 400/500 映射与现状逐条一致（见 `design.md` §11.8）。

### R11. 每 target 一套独立实现与独立产物（**撤销「一套源码多平台」**）

> 决策变更：原 R11「一套源码 + 编译期宏 + 零后端宏门禁」被撤销。理由见本节末尾与 F21–F23；`design.md` §4.8.4 已标注为被取代。

- **源码结构与产物结构对齐**：每个 target 一个独立实现目录 `packages/<id>/<target>/`，各自带 `CMakeLists.txt`、`Makefile`、`src/`、`manifest.json`、`config.schema.json`、`models/`、`sample.jpg`。`manifest.id` 相同、`platform.{arch,target}` 不同；宿主的安装、回滚、孤儿清扫、`[algorithm].target` 匹配语义**不变**。
- **target 命名口径（一次性改名，永久生效）**：主机档位叫 **`onnx`**——决定它的是推理后端（ORT / f32），不是「CPU」这件事；板端档位按芯片叫 `rk3568`（将来 `rk3588`）。**`cpu` 只允许指 build 侧的 `cpu_mock` 桩后端与视觉后端的 CPU 实现，不得作为 package target、目录名或 `platform.target` 的取值**（见 F25）。
  - 迁移动作：`targets/cpu/` → `targets/onnx/`；`manifest.platform.target` `"cpu"` → `"onnx"`；`[algorithm].target` 默认值与 `app.DefaultConfig.AlgorithmTarget` 同步改为 `"onnx"`；`normalizeHostTarget("")` 返回 `"onnx"`。
  - 安装期门禁：`platform.target == "cpu"` 返回稳定错误码 `PLATFORM_TARGET_CPU_RETIRED`，提示重建为 `onnx` 档位；**不做静默映射**（静默映射会把 mock 与 onnx 两个后端继续混在一起）。
- **不共享源码、也不共享测试夹具**：两个 target 的实现目录之间不得互相引用——CMake 路径（`../onnx`、`../rk3568`）、`#include "../"`、`if(TARGET …)` 回退一律禁止。**允许并预期重复**：decode / NMS / 对齐等逐字相同的纯数学各存一份。
- **开发期测试只服务本 target，不进归档**（2026-10-25 决策变更，取代「共享 golden 向量」）：
  - 每个 target 自带 `tests/`，`make test` 拼一个 **dev-only** 可执行文件（`EXCLUDE_FROM_ALL`、不进安装规则），它自己读**包根 `sample.jpg`** → 直接调本 target 的产品代码 → 把框 / label / score / landmark 画回图上，写出 **`result-<id>-<target>.jpg`**，同时在终端打一张明细表；
  - 这层**允许**依赖图像库（`stb_image` / `stb_image_write` header-only 即可，或系统 libjpeg）：它是开发期工具，不受「交付包 `ldd` 干净」约束。受该约束的是归档里那个 `<manifest.id>`，它仍只吃宿主喂的裸像素；
  - **不进归档**：`pack_package.py` 只拷白名单，且断言 `tar tzf | grep -cE 'tests/|result\.jpg|stb_'` = 0；
  - **跨 target 不比结果**：onnx（f32）与 rk3568（int8）推理本就不同（F24），两张 `result.jpg` 只各自对照自己的基线，不存在「两张图应当一致」这种要求；
  - 交叉构建的 dev-only 可执行文件**可以在板端执行**（这是 RKNN 档位唯一可行的行为验收方式，不违反 AGENTS.md 的「交叉只许编译」——那条禁止的是在**宿主**执行目标架构产物），回传 `result.jpg` 即验收证据；
  - **刻意不做**：张量级 golden 向量（`vectors/*.json` / base64 / regen 脚本 / tolerance）。按 target 分叉的向量等于普通单测；不分叉则捆绑两份实现必须同解——而真正需要在同 target 内防回退的东西，看 `result.jpg` 比看数字更直接。
- 每 target 的归一化常量、默认阈值、NMS 参数写在**本 target 的实现目录内**；不跨 target 共用，也不做「per-target 覆盖」机制。
- **构建隔离**（沿用原 R11，仍然有效）：`SDK_PREFIX` 按 target 各产一份并写 target 元数据，包 configure 断言与 `TARGET` 一致；构建目录 `build/<target>/` 不得共用（CMakeCache 会静默跨架构污染）；归档名必须含 `arch` 与 `target`。
- **资产摊平**：出包时把本 target 的 `models/`、`manifest.json`、`config.schema.json`、`QUANTIZATION.md` 落到**包根**，归档内不存在 `targets/` 目录。算法侧不得出现 `targets/<t>/` 路径候选或 `__aarch64__` 兜底（见 F22 的回退链缺陷）：模型只在包根 `models/`，找不到即失败。
- **target 资产存在性门禁（fail-closed）**：`<id>/<target>/` 必须同时具备 `manifest.json` + 该 target 应有的模型 + `QUANTIZATION.md`（含 `.rknn` 时）；缺任何一项，`make package TARGET=<t>` 在 configure 阶段失败并列出缺失项。`packages/<id>/scripts/build_package.py`（**每 package 一份 10 行薄壳**，按 `--target` 委派给 SDK 的 `pack_package.py`；它是工具不是算法代码，不违反「不共享源码」）的 `--target` 取值收敛为「目录实际存在的 target」，不得再枚举未来平台（见 F23）。
- **验收分级**：host 上跑 `make check`；交叉产物只做静态检查（`file` 架构 / `readelf NEEDED` / 无 zhulong 符号 / 归档不混模型）；行为正确性只能板端做，`research/baseline.md` 必须标「已测 / 未测」，不得以宿主结果代替。本任务不为 RKNN 建立实测基线。
- **决策理由（写进 review）**：平台数 N=2、第二个平台在本机不可执行、差异已泄漏 18 处 `is_onnx_`。此条件下编译期宏抽象的成本（`runtime_factory` 强制把后端拖进包、跨 target 资产回退、公共 ABI 面）全部实付，收益趋零；整包独立换来的是**爆炸半径隔离**：host 侧改动在物理上不可能波及板端。若将来板端具备 smoke 回归，可再把逐字相同的纯数学收敛为共享目录——那是独立任务，不是本任务的前置。
- **不欢迎的复辟**：在实现目录内重新引入 `ZHULONG_INFERENCE_*` / `ZHULONG_VISION_*` 宏或 `is_onnx_` 式运行时判断，等同于把「一套源码」重建一遍，review 直接拒绝（每份实现只服务一个平台，没有分支可写）。

### R12. 量化与数值契约

- **int8 只存在于模型资产与后端内部**：`.rknn` 由 RKNN-Toolkit2 量化；RKNN 后端以 `pass_through=0` 做输入量化、`want_float=1` 做输出反量化（强制 float32）。**ONNX 后端保留模型原始 dtype**（`onnx_inference_backend.cpp:292`），因此边界承诺的是「浮点输出统一 float32」，不是「所有 dtype 归一」；整数索引输出属显式例外，算法必须按 `dtype()` 分支。
- **`Tensor::data<T>()` 必须校验 dtype**（`tensor.hpp:163-175` 当前不校验且算法直接 `data<float>()`）：不匹配即抛 `std::invalid_argument`，由 ABI 边界的 `try/catch` 转状态码——否则 int64/int8 输出会被静默读成垃圾。
- 归一化（`/255.0`）由算法自己做，与后端无关。
- **算法源码不得接触量化参数**：`zero_point` / `scale` / `quant` / `dequant` 在 `packages/*/*/src` 中出现即视为越层。
- **量化配置必须可复现**：每个 target 目录内提交**转换配置**（`CONVERSION.md` 或转换脚本，含 `mean_values` / `std_values` / 量化模式 / 校准集来源与规模 / 工具版本），并提供 `QUANTIZATION.md`（模型文件、**每模型的**精度模式、校准集、工具版本、已知偏差）；出包时摊平到包根随包分发。不新增 manifest 字段。
- **精度模式是每模型的属性，不是每 target 的**：同一 target 内可混用（实测 `facelivtv2-l.rknn` 为 fp16 未量化，`det_500m.rknn` / `scrfd_person_2.5g.rknn` 为 int8 layer-asym），`QUANTIZATION.md` 必须逐模型写明。
- **`.rknn` 内嵌元数据只作第二意见**：Toolkit2 会把转换时的 `mean/std/rgb2bgr/version` 以未文档化的明文结构写进模型文件；它可用于 **drift 检查**（配置与产物不一致时报警），**不得**作为唯一来源，更不得作为任何运行时配置表的生成来源（私有格式无版本保证）。
- **默认阈值按 target 各自维护**（不再是「待确认 / 先按不覆盖」）：量化颗粒使低分候选筛选不成立跨 target 共用（F24：颗粒约 `0.0034`），阈值写在各自实现目录，取值属板端标定项，未标定前在 `research/baseline.md` 标「未测」。
- `want_float=1` 的换算开销属已知代价（输出 float 化，模型输出大时内存与带宽放大 4×）；int8 直出不属本任务。
- **删除**：`models/preprocess.json`、`applied ∈ {converter, graph, none}` 三态、双重归一化机检。归一化在每份实现里只有一处常量，不存在「双重」这个失败模式。

### R13. 平台事实留在各自实现目录（**撤销原「后端契约下沉」**）

> 决策变更：原 R13（`InputFacts` + `Session::prepare_input` + `models/preprocess.json` + 能力协商）**整条撤销**。它在 N=2 的条件下不减少任何语义分支，只把分支从算法搬到 SDK，代价是新增公共 ABI 面 + 一张人写的表 + 一套机检。

- **不做公共表**：归一化（mean/scale/色序）、输入布局、DMA-BUF 选择、阈值，全部写在本 target 的实现目录里（各一处常量 + 一次调用）。没有第二份副本，就没有漂移。
- **仍然移除**对包外暴露的后端选择入口 `set_is_onnx`（`algo.hpp` 及相关）：理由是每份实现只服务一个平台，**分支不存在宿主**，而不是「统一收进 SDK」。
- **不新增** `OutputSpec` / `caps()` / `row_alignment` / 复用槽 / 能力协商框架。
- **顺带修一个显式缺陷**：`native/src/backends/rknn/rknn_inference_backend.cpp:298` 的 `io_attr.pass_through = (io_attr.type == attr.type) ? 1 : 0` 用 dtype 相等**隐式**决定是否让 converter 做归一化。今天靠「永不相等」侥幸成立，模型输入换成 uint8/int8 直连时会静默跳过 `mean/std`。改为显式参数 `convert_input`（rknn 实现恒 `true`；如将来做 int8 直连，由该实现显式选择并配板端标定）。
- **移出本任务**：`Session::prepare_input` 复用槽带来的性能收益（消除每帧 `~4.9MB` 分配与 HWC→CHW 重排）作为独立性能任务，不与交付形态迁移捆绑。

### R14. 删除 `cpu_mock` 产品后端，桩降级为测试夹具

> 决策：`cpu_mock` **不得**再作为可选的后端、构建档位或默认值存在（F26/F27）。它承载的"板端无真实推理也要跑 broker/runner 回归"的需求是真实的（F28），但载体错了——那是夹具，不是产品后端。

- **产品后端只有两个**：`onnx`、`rknn`。`runtime_factory` 的 `#else` 分支改为 `#error "no inference backend selected: pass --inference-backend onnx|rknn"`，**不留任何默认后端**。
- **删除默认值推导**：`native/scripts/build.py` 的 `default_inference_backend()` 移除。交叉 profile **必须**显式声明 `inference_backend`，缺失即 configure 失败（不再"编译成功但不可推理"）；host 固定 `onnx`（R11 的 `onnx` 档位），ORT 前缀缺失即失败并提示 `make native-deps`。
- **桩移入夹具**：`native/tests/fixtures/stub_inference_backend.cpp`，只由 `ZhulongAlgoRuntimeStub`（OBJECT 复用 + 该源）与 `MockDetector` / `MockClassifier` / `MockPersonStructure` 等夹具链接；构建开关 `ZHULONG_INFERENCE_TEST_STUB` 只在夹具目标定义。产品 `ZhulongAlgoRuntime`（`.a` / `.so`）、SDK 分发物、任何包产物**不得**包含桩实现（`ar t` 与 `readelf` 可验）。
- **板端回归改走夹具产物**：原 `rk3568.json` 的用途由夹具交叉产物承担；target 名必须含 `stub`，profile 写 `inference_backend: test_stub`；`pack_package.py` 遇到 `runtime: test_stub` **拒绝出包**（桩产物永不进入安装/分发链路）。
- **删除所有"因为后端是桩所以跳过"的分支**：`[SKIP] ... cpu_mock ... rejects real models` 一律移除。需要真实模型就用真后端；不需要就用夹具 + mock 模型标记。不允许存在"看起来在测真实模型、实际被跳过"的用例（F27）。
- **诊断口径**：`default_inference_backend_name()` 返回 `onnx` / `rknn`（夹具构建为 `test_stub`）；runner 失败日志里"host 推理后端选错"的区分能力由 `#error` 在编译期提供，不再依赖运行期字符串。

---

## 非目标（明确排除）

| 项目 | 原因 |
| :--- | :--- |
| 声明式内容包（`manifest.pipeline` + 具名解码头枚举） | 独立的架构议题；本任务只做交付形态黑盒化，不引入新的算法表达方式 |
| 函数表注入式（`zhulong_runtime_api_v1` + `set_runtime`） | 与静态链接互斥的替代路线；静态链接达成同样的黑盒目标且工作量小一个数量级 |
| 算法包支持非 C++ 语言 | 本任务使该能力**成为可能**（协议边界无 ABI），但不在本任务交付实现 |
| 跨 target 源码复用（`core/` + `platforms/` 抽象层） | 双平台、且第二个平台本机不可执行时，抽象是负收益（见 R11 决策理由）；待板端 smoke 回归就绪后另立任务收敛重复 |
| **算法参数下发与热更新** | 本任务只换交付形态，`model_create` 继续传 `"{}"` 以保持行为等价。参数下发是独立议题：`model_create` 是**包级**、`task_open` 无 config 入参，且任务变更目前走**防抖全进程重启**（`ScheduleAlgorithmRestart` → Stop + Start），改一个阈值会让该算法所有通道 AI 中断。真正的修复需要新增 broker 控制消息 + `task_update` 尾追加，属独立任务（`design.md` §10） |
| `GalleryRegistry` 的只读快照数据格式重定义 | 复用现有 `MSG_GALLERY_SYNC` 载荷，仅调整消费侧位置 |
| 前端 / 其余 Go 业务层改动 | 除 `internal/algorunner`、`internal/algorithm` 的进程管理，以及 R10 涉及的 `internal/engine`（删加载器）、`internal/person`（提取器装配）外不触碰 |
| **算法包 UI 试用（上传图片 → 前端画框）** | 本任务只把它的**前置契约**准备好：`--verify` 输出明细 + 结果 JSON 定为唯一跨边界契约（只许追加字段）。HTTP 端点、上传校验与限额、一次性推理的并发/超时/孤儿看护、前端 canvas 画框（含 EXIF 方向与 `object-fit` letterbox 换算）、以及「本测试不含底库与跨帧跟踪」的提示，属独立任务 |
| 特征提取的批处理与常驻服务化 | 帧流协议已允许一个进程处理 N 帧，但不新增批量导入接口、不做进程池扩展、不做多后端编排；N 帧一次处理属于协议能力，不是本任务的功能交付 |

---

## Acceptance Criteria

> **门禁通用约定（D4）**
>
> 1. 本节所有 `grep` 断言只针对**代码、构建脚本与配置值**，不针对注释 / docstring / Markdown。统一写法：
>    ```bash
>    grep -rn --include='*.c' --include='*.cc' --include='*.cpp' --include='*.h' --include='*.hpp' \
>         --include='*.py' --include='*.cmake' --include='CMakeLists.txt' --include='*.go' \
>         '<pattern>' <dirs> | grep -vE '^[^:]+:[0-9]+:[[:space:]]*(//|#|\*)'
>    ```
> 2. Python docstring 无法用行首规则剔除，因此**必须显式给豁免**：在 AC 里写明「允许在 `<文件>` 内出现 N 处，用于说明历史」。命中数超出豁免上限即失败——既有豁免也有上限。
> 3. 理由：`native/scripts/build.py:229` 保留了解释「为什么废除 `cpu_mock`」的 docstring。那是**知识**，不是**用法**。若断言把「写下禁令」判成「违反禁令」，门禁会先被当成噪音，然后被人关掉。
> 4. 这些断言在 `implement.md` 与 CI 中会重复出现，应收编为单一脚本（建议 `native/tests/check_layering.py`，规则表 + 豁免表两张纸）并挂进 `make check`，不在 Makefile 里散落 grep。

### AC1. 黑盒判据（硬性，可命令行验证）

对迁移后的至少一个算法包（建议 `plate_recognition`，代码量小、`onnx` 档位）：

```bash
nm -D --undefined-only <pkg>/plate_recognition | grep -c zhulong   # = 0
ldd <pkg>/plate_recognition | grep -ci zhulong                     # = 0
nm -D <pkg>/plate_recognition | grep -c zhulong                    # = 0（导出表也不得泄露）
```

### AC2. 线程与进程模型不回退

- 单算法包订阅 3 个 broker，3 路订阅全部建立，且 SDK Worker 数 = 3（一 broker 恰对应一个 Worker）；`ps -L` 用于确认 Worker 线程实际运行，不把主线程或运行时辅助线程计入 Worker 数；
- 进程总数 = 已启用算法包数（不是 broker 数）；
- RKNN target 下（若可测）权重共享行为与改造前一致（通过显存/内存占用对比或 `rknn_dup_context` 调用日志证明）。

### AC3. 行为等价

- 同一模型、同一 `sample.jpg`，改造前后推理结果的 `count`、`label` / `class_id`、`text`、`score`、`box` 在容差内一致（**同 target 内前后版本对比**）；
- **证据形式**：`make verify` 的终端明细表（机器比对） + `make test` 产出的 `result.jpg`（人看框位置与标码）；两者都要，不能只靠 `count`；
- 实时流路径：改造后的算法包在真实 broker 上收到的帧数、丢弃帧数、端到端时延与改造前同量级；
- **可比性限定在同一 target 的前后版本之间**：量化/取整/RGA 采样使跨 target 不保证逐位一致，跨 target 不给数值结论（只各自对照自己的基线）——量化颗粒见 F24（约 `0.0034`）。

### AC4. 单一形态端到端运行

- `plate_recognition` 与 `face_recognition` 均以 `<manifest.id>` 可执行文件形态安装、启动、停止、重启及回滚；
- 旧 `.so` 形态包安装时明确失败，不尝试 `dlopen` 或自动回退；
- `make check` 全绿（前端 + native-test + api-docs + go-check）。

### AC5. 孤儿清扫不误杀不误漏

- 回归用例覆盖：新形态 runner 被 `SIGKILL` 宿主后成为孤儿 → 被正确回收；
- 同名不同路径的另一实例的正常进程**不被误杀**；
- 现存门禁 `TestReapOrphanRunners_KillsReparentedRunner` / `TestReapOrphanRunners_SkipsCleanupWhenLockIsBusy` 仍通过。

### AC6. 底库同步不因交付形态变化而回退

> 依赖已降级的 R7：本任务不动底库代码，因此本条是**回归门禁**，不是新功能验收。

- `make native-test` 中底库分片边界用例（64KB / `shard_index` / `shard_total`）全部通过；
- `--verify --sample` 与真实 broker 路径下，人脸包收到的底库快照与改造前同源（同一份 `MSG_GALLERY_SYNC` 载荷，无单例改写、无注入式替换）；
- **不要求**「10k 底库命中率」实测：该指标在改造前后由同一份代码产出，本任务无从产生差异，测了也无法归因。若板端需要精度回归，记入独立的精度任务，不属于本任务验收。

### AC7. SDK 自足性

- 在**不含本仓库**的干净环境中，仅用 SDK 分发物完成：编译示例算法包 → 使用包自身 `--verify --sample` 验包 → 用 SDK 自带的 `tools/pack_package.py` 产出合规归档（不得依赖仓库内脚本）；退出码 0 且输出 `{"status":"passed"}`。

### AC8. 门面 task/config 上下文已补齐（R9）

- `TaskContext` 是 `zhulong` namespace 下 SDK 公共类型，包含 `task_id` 与 `TaskConfig params{}`；`TaskConfig` 存在于 public API 但当前为空值类型。
- `ZHULONG_ALGO_MAIN(PluginClass, ResultAdapter, PluginName, PluginVersion)` 与 `run<PluginClass, ResultAdapter>` 编译通过；SDK 的四种结果适配器可由示例算法包使用，`PluginFactory` 仅为 SDK detail 层函数指针，不要求算法作者实现；
- 各 `ResultAdapter::infer()` 与 `detect` / `classify` / `detect_persons` / `detect_vehicles` 门面签名均接受 `TaskContext&`；
- 两个现有包已同步改签名且**不读取** `params`（代码审阅确认）；
- `make native-test` 全绿，且验包输出与阶段 0 基线一致（AC3）。

### AC9. 特征提取语义等价（R10）

- 同一张样本、同一模型，改造前后 512 维特征向量余弦相似度 ≥ 0.999（同后端应逐位接近）；
- 成功 / 无脸（400 `NO_FACE_DETECTED`）/ 输入非法（400）/ 能力不可用（500）四类 HTTP 语义与现状逐条一致；
- `internal/person` 现有登记用例（含 `FEATURE_EXTRACTOR_UNAVAILABLE`、`FEATURE_DIM_MISMATCH`、超大文件与格式白名单）全部仍通过。

### AC10. 特征提取失败可诊断

- 包未安装 / 入口缺失 → `ErrUnsupported`（500），日志包含包 id 与解析出的入口路径；
- 子进程崩溃、请求超时、stdout 非法 JSON、`feature_dim` 越界各有一条用例，均不 panic、**不写零向量**（fail-closed 不得退化）。

### AC11. 宿主不再加载算法代码

```bash
grep -n "dlopen\|dlsym" native/src/abi/engine.cpp        # 期望：无输出
nm -D <zhulong 二进制> | grep -c zhulong                  # 期望：0
```

- `zhulong_face_extract_feature` / `zhulong_face_extract_release` 从 `native/tests/check_plugin_symbols.py` 的例外白名单与 `.trellis/spec` 的跨 `.so` 例外条款中移除。

### AC12. 帧流协议

- 一个进程连续处理 3 帧（第 2 帧无脸）→ 3 行 JSON、`request_id` 一一对应、进程不退出；
- 单张登记路径只发 1 帧；
- 包侧不因该能力新增图像解码依赖（`ldd` 中不出现 `libjpeg` / `libwebp` / `libpng`）。

### AC13. 热进程复用与回收

- 连续 5 次登记只产生 1 次 spawn；空闲 60s 后进程数回到 0；
- `kill -9` 宿主后子进程 **≤3s** 内消失（Pdeathsig；轮询断言，不写死 1s——慢 CI 上 1s 阈值会 flaky，而契约本身不变）；
- 请求超时 → 进程组被 SIGKILL 且 `Wait()` 回收，无僵尸；下一次请求重建成功；
- 并发 5 个登记请求全部成功或明确失败，按 `request_id` 断言无响应错配。

### AC14. 包版本失效

- 替换或重装包目录后，首个特征提取请求必须起新进程，且日志记录 retire 原因；不得复用仍持有旧模型的进程。

### AC15. 现有 spawn 契约不回退

- `TestRunnerProcessSpawnThreadSurvivesWhileChildRuns` 仍通过（新子进程复用同一 spawn helper）；
- `make check` 全绿。

### AC16. 包独立构建（R8，方案 A）

```bash
make native-build && find build/native/host -name '*Recognition*' | grep -v '^$'   # 期望：空
make sdk-install && make packages-build && make packages-test                       # 期望：全绿
grep -rn 'packages/' native/CMakeLists.txt                                         # 期望：空（不再 include 包源码）
```

- `packages/<id>/<target>/CMakeLists.txt` 只依赖 `find_package(ZhulongAlgoSDK 1.0)`，不得出现 `if(TARGET ZhulongAlgoRuntimeStatic)` 式双分支、不得引用 `../native` 源码路径，也**不得引用同 id 的另一 target 目录**；
- `build_package.py` 只消费该 target 目录的构建产物（`packages/<id>/<target>/build/<target>/<id>`），不再调用 `cmake --build`；
- 包级测试（face / plate / face-onnx）随包所在目录，由 `make packages-test` 驱动；
- 交叉 profile 下 `native` 与 `packages` 各自产出到 `build/targets/<name>/…`，Host/Cross prefix 不混用；
- 打包入口唯一：`grep -rn 'tarfile\|shutil.copytree' packages/*/scripts/` 为空（逻辑在 `native/sdk/tools/pack_package.py`），且交叉时包 Makefile 自动 `--no-verify`；
- target 内闭环：`cd packages/<id>/<target> && make && make test && make package` 三步全部在该目录内完成；`diff templates/Makefile packages/<id>/<target>/Makefile` 只允许 `SDK_PREFIX` 一行差异（`TARGET` 取自目录名，不算差异）；
- **包不携带 SDK**：`find packages -maxdepth 3 -name 'zhulong' -o -name 'libZhulongAlgoRuntime.a' -o -name 'libzhulong_algo_sdk.a'` 为空；包目录内不得出现 `include/zhulong`；归档里也不得出现 SDK 文件；
- **版本门禁有效**：把 `$SDK_PREFIX/VERSION` 改成低于包声明的最小版本后，`make` 必须在 configure 阶段失败并同时报出双方版本号。

### AC17. 脚手架可用（R8）
```bash
# 干净目录内，只用 SDK 分发物
python3 /opt/zhulong-algo-sdk/tools/new_package.py demo --target onnx
cd demo/onnx && make && make test && make verify && make package
```

- 生成物落在 `demo/onnx/`（每 target 一个目录），开箱通过全链，`verify` 输出 `{"status":"passed","count":0}`（空实现），`dist/` 下有归档；
- 生成物不含 `plugin.so`，入口文件名 = `manifest.id` 且 `0755`，`models/` 与入口同级（无模型时目录可为空但存在约定）；
- **可独立运行**：把包目录拷到任何已装第三方运行时的机器上（无需 SDK、无需宿主），`./<id> --verify --sample sample.jpg` 能跑；`ldd <id>` 里无任何 `zhulong`，`readelf -d` 无 `libZhulongAlgoRuntime.so`（`design.md` §4.5.3）；
- 非法 `id`（不符合 `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`、或等于 `plugin.so`）在生成期就被拒绝，不拖到安装期；
- 生成的包能被宿主正常安装并跑 `--verify --sample`；
- `make sdk-selftest` 在 `make check` 中运行且当前为绿（生成 → 构建 → 验包 → 打包）。

### AC18. 第三方运行时处置正确（R2）

```bash
# onnx 档位：随包绑定
ldd <pkgroot>/plate_recognition | grep onnxruntime      # 期望指向 <pkgroot>/lib/libonnxruntime.so.1.21
tar tzf build/packages/plate_recognition-*.tar.gz | grep -E 'lib/libonnxruntime|NOTICE'   # 期望命中

# rknn target：不绑定
ldd <pkgroot>/plate_recognition | grep -c rknnrt         # 期望 0（由系统/BSP 提供）
```

- `verify` / `algo_probe` 输出含 `runtime: bundled|system (<lib> @ <path>)`；
- 包内 `lib/` 之外的任何位置不得出现 `plugin.so`（归档中同名文件一律拒绝）；
- 体积记录：归档体积与解包体积写入 `research/baseline.md`（ORT 闭包实测约 33MB/包）。

### AC19. 每 target 独立实现与独立产物（R11）

```bash
# 1) 目录隔离：实现目录之间零引用，源码不认 target 名
grep -rn '\.\./onnx\|\.\./rk3568\|targets/' packages/*/*/src packages/*/*/CMakeLists.txt   # 期望：空
# 2) 各 target 独立构建，产物互不覆盖
make -C packages/face_recognition/onnx package
make -C packages/face_recognition/rk3568 package CROSS_PROFILE=rk3568
file packages/face_recognition/rk3568/build/rk3568/face_recognition | grep -c aarch64   # = 1
# 3) 资产摊平 + 不混模型
tar tzf build/packages/face_recognition-*-rk3568.tar.gz | grep -c 'targets/'            # = 0
tar tzf build/packages/face_recognition-*-rk3568.tar.gz | grep -cE '\.onnx$'            # = 0
# 4) 交叉产物不得带宿主后端
readelf -d packages/face_recognition/rk3568/build/rk3568/face_recognition | grep -c onnxruntime   # = 0
nm -D --undefined-only packages/face_recognition/rk3568/build/rk3568/face_recognition | grep -c zhulong  # = 0
# 5) target 资产存在性门禁（fail-closed）
python3 packages/plate_recognition/scripts/build_package.py --target rk3588   # 期望：非 0，错误里列出缺失的 manifest.json / models/
# 6) target 命名口径：cpu 不再作为档位
grep -rn '"cpu"' packages/*/*/manifest.json                                     # 期望：空（档位是 onnx / rk3568）
grep -rn 'AlgorithmTarget: *"onnx"\|target *= *"onnx"' internal/app/config.go config.toml   # 期望命中（默认值已改）
```

- 用 host 的 SDK prefix 配 cross 工具链构建包时，configure 必须失败并报出 target 不一致；
- 交叉过程中**未执行**任何目标架构二进制（日志里不得出现 `--verify` 的真实执行）；
- 板端未测项在 `research/baseline.md` 明确标为未测，不得以宿主结果代替；
- 每份实现只服务一个平台，因此不得出现后端选择宏或运行时后端判断（重建「一套源码」的痕迹）：`grep -rn 'ZHULONG_INFERENCE_\|ZHULONG_VISION_\|is_onnx_' packages/*/*/src` 为空；`grep -rn '__aarch64__\|__arm__' packages/*/*/src` 为空（不再有模型路径兜底）；
- **开发期测试只服务本 target**：`make -C packages/<id>/<target> test` 能产出 `result.jpg`（框 + label + score + landmark）与终端明细表；`tests/` 可依赖图像库（stb_image 等 header-only），但**不得进归档**：
  ```bash
  tar tzf build/packages/<id>-<arch>-<target>.tar.gz | grep -cE 'tests/|result\.jpg|stb_'   # 期望：0
  ldd build/packages/<解包>/<id> | grep -ci 'jpeg\|png\|webp'                              # 期望：0（交付物仍无图像库）
  ```
- **不做**张量级 golden 向量：`find packages -name '*.json' -path '*vectors*'` 为空；两个 target 的 `result.jpg` 不要求一致（量化差异本就不同，见 F24）。
- 旧包（`platform.target == "cpu"`）安装期被拒，错误码 `PLATFORM_TARGET_CPU_RETIRED`，提示改用 `onnx`；`normalizeHostTarget("")` 返回 `"onnx"`（单测覆盖）。

### AC20. 量化契约（R12）

```bash
grep -rn 'zero_point\|dequant\|quantize' packages/*/*/src        # 期望：空（算法不接触量化参数）
tar tzf build/packages/face_recognition-*-rk3568.tar.gz | grep QUANTIZATION   # 期望命中
ls packages/face_recognition/rk3568/CONVERSION.md               # 期望存在（mean/std/量化模式/校准集/工具版本）
```

- 每个带 `.rknn` 的 target 都有 `QUANTIZATION.md`，**逐模型**写明文件名与精度模式（同一 target 内可混用 int8 / hybrid / fp16），并含校准集来源与规模、转换工具版本；
- **转换配置入库**：`CONVERSION.md`（或转换脚本）是 `mean/std` 的唯一来源；`.rknn` 内嵌的 `attrs.mean/std/rgb2bgr/version` 只能作为 **drift 检查** 的第二意见——脚本对二者不一致时报错，且**不得**用它生成任何运行时配置；
- 算法侧无手工反量化代码；
- `Tensor::data<T>()` 的 dtype 校验生效：用桩后端返回一个 `INT64` 输出，算法按 float 读取时必须拿到错误状态码（不是垃圾结果）；
- RKNN 后端的输出恒为 `FLOAT32`（有测试或日志可证）；
- 每个 target 的默认阈值写在自己的实现目录，`grep -rn '0\.5f\|threshold' packages/*/*/src` 不存在跨 target 复制带来的同值硬编码（阈值属板端标定项，未标定前在 `research/baseline.md` 标未测）。

### AC21. 平台事实不做公共抽象（R13）

```bash
grep -rn 'InputFacts\|prepare_input\|preprocess\.json' native/ internal/ packages/   # 期望：空（机制未引入）
grep -rn 'set_is_onnx' native/ packages/                                            # 期望：空（后端选择入口已删）
grep -rn 'pass_through = (io_attr.type' native/src/backends/rknn/                   # 期望：空（改为显式 convert_input）
readelf -d <rknn 或 test_stub prefix 下的脚手架空实现包> | grep -c onnxruntime      # 期望：0
```

- 归一化常量只在各 target 实现内出现，且同一实现内只允许**一处定义点**（禁止内联重复）；
- SDK 公共头不出现任何「由后端声明输入契约」的类型；`Tensor` / `Session` / `zhulong::ops` 契约不变；
- **上条 `readelf` 断言只适用于「本不该带 ORT 的档位」（`rknn` / `test_stub` prefix）**（2026-10-25 修正）：`onnx` 档位**必然**出现 `NEEDED libonnxruntime.so.1.21`——`algo_main` 调 `runtime::init()` → 引用 `runtime_factory.cpp:36 get_default_inference_backend()` → 抽入 `onnx_inference_backend.o`，这是静态链接的确定性结果，不是缺陷。「每 target 一份 SDK prefix」也救不了它：onnx prefix 本身就是 ORT 档位。onnx 档位的正确约束是 **AC18**（随包 `lib/` + `RUNPATH=$ORIGIN/lib` + 归档带 `NOTICE` + 验包输出 `runtime: bundled|system`），不是「不许 NEEDED」；
  - 若要在 onnx 档位也不带 ORT，唯一手段是把后端注册推迟到首次 `create_session` 并用 `dlopen` 打开 ORT——与本任务「消灭 dlopen」的方向相反，**明确不做**；
- `pass_through` 改显式参数后，rknn 实现仍始终让 converter 做归一化（`convert_input=true`），并有单测覆盖「模型输入 dtype 与像素 dtype 相等」这一分支不被误判为「已归一化」。

### AC22. 桩后端已退出产品面（R14）

```bash
# cpu_mock 不得作为后端名 / 配置值 / 分支出现；允许在注释与 docstring 中说明历史（见「门禁通用约定」第 2 条）
grep -rn 'cpu_mock' native/src native/sdk native/scripts Makefile          # 期望：仅 native/scripts/build.py 内 1 处 docstring 历史说明，无代码/配置命中
grep -rn 'default_inference_backend' native/scripts/build.py               # 期望：空（不再推导默认后端）
python3 native/scripts/build.py --profile native/profiles/rk3568.json build   # 期望：失败，指出 profile 未声明 inference_backend
ar t build/native/host/libZhulongAlgoRuntime.a | grep -c stub              # 期望：0
find build/sdk/prefix -name '*.a' -exec sh -c 'ar t "$1" | grep -c stub' _ {} \;   # 期望：全 0
grep -rn '\[SKIP\].*cpu_mock\|cpu_mock backend' native/tests             # 期望：空
```

- 未声明 `inference_backend` 的 profile 一律 configure 失败，不存在"编译成功但不可推理"的产物；
- `test_stub` 只能出现在夹具目标的构建里，`pack_package.py` 遇到 `runtime: test_stub` **拒绝出包**；
- 板端 broker/runner 回归仍可完成：用夹具交叉产物（target 名含 `stub`），结果记入 `research/baseline.md` 并标注「夹具：无真实推理」；
- 没有因为后端是桩而跳过的用例：`grep -c '\[SKIP\]'` 在包级/特征提取用例中为 0；
- 宿主 `onnx` 档位构建要求 ORT 前缀存在，缺失时 configure 失败并提示 `make native-deps`（不再静默降级到桩）。

---

## Constraints & Risks

| 风险 | 影响 | 缓解 |
| :--- | :--- | :--- |
| 每个算法包各带一份运行时，代码段内存上升 | 10 包 ≈ 数十 MB 地址空间（实际 RSS 只算 touched 页） | 相对 NPU 权重可忽略；换取零 ABI 债，且天然获得依赖隔离（包间 onnxruntime 版本冲突不再可能） |
| 旧包二进制仍链着旧 SDK 运行时 | 修复无法「一改全受益」，且协议可能漂移 | 跨边界只有 JSON 协议，引入 `manifest.protocol` 版本校验（非 SONAME） |
| `Pdeathsig` / `LockOSThread` spawn 契约 | 改造易引入孤儿或健康子进程被杀 | 该契约位于宿主侧，本任务不改动 spawn 逻辑；但必须回归 `TestRunnerProcessSpawnThreadSurvivesWhileChildRuns` |
| 旧 `.so` 包未重建即升级宿主 | 算法包无法启动 | 部署前重建并重新安装算法包；验证器对旧入口明确报错 |
| 底库单例移除 | 若遗漏某条读取路径，表现为「识别率莫名变低」 | 以 AC6 为门禁；底库读取全部收敛到注入的只读快照访问器 |
| **特征空间漂移** | 热进程缓存命中的是包升级前的旧模型，底库特征与流式识别特征不再同源；表现为「识别率莫名下降」且无报错 | 缓存 key 含包目录 + 可执行文件身份；包变更即 retire 重建；AC14 门禁 |
| **响应错配** | 超时或崩溃后重试，把上一帧的特征写进本次登记 | 每帧 `request_id` 回显 + 单帧最多重试 1 次；AC13 断言 |
| 热进程占内存 | 板端（如 RK3568 / 2GB）多常驻一份约 80MB 模型 | 空闲 60s 回收（与现状 TTL 默认值一致）；并发上限 1；峰值内存纳入实测记录 |
| **spawn 线程契约被绕过** | 为省事从 HTTP handler 临时 goroutine 裸起子进程，Go 回收 M 时 Pdeathsig 误杀健康子进程 | 只允许复用现有 spawn helper（`runnerSysProcAttr` + `LockOSThread`）；AC13/AC15 门禁 |
| 静态链接后模型定位 | `--extract-feature` 找不到 `models/`，表现为登记失败 | 可执行文件仍在包根（R1），`dladdr` 语义与 `--verify` 一致；AC9 用真实样本验收 |
| 误以为「独立包 = 零外部依赖」 | 部署现场缺 `libonnxruntime.so.1.21` 才发现包起不来 | README / `abi.json` 显式列出第三方运行时来源（`design.md` §4.5.3）；AC17 的 `ldd` 断言只保证无 `zhulong` 依赖 |
| **两条构建路径分叉** | 仓库包走 in-tree target、第三方走 SDK prefix，后者只在发布前容器测试里跑，平时无人踩 | 方案 A：包只认 `find_package(ZhulongAlgoSDK)`，`make native-build` 不编包；AC16 门禁 |
| 交叉构建的 prefix 混用 | Host 的头/归档被用作交叉包的输入，产物在板端静默表现为加载失败或 ABI 异常 | 每个 `CROSS_PROFILE` 各产一份 SDK prefix，输出目录分离；`build_contract_test.py` 的 Host/Cross 约束扩展到包 |
| 开发循环多一步 | `make native-build` 后忘记 `make packages-build`，验包引用到旧产物 | `make check` 固定顺序，`make build` 串联两者；`build_package.py` 不再隐式帮你编 |
| **模板腐烂** | `templates/` 与真实包各自演化，第三方拿到的骨架编不过或打不出包 | `make sdk-selftest` 每次重新生成并跑全链（AC17）；AC16 的 diff 门禁把包与模板钉在一起 |
| 第三方运行时未随包 / 许可漏配 | 现场 `dlopen` 失败，或再分发违反 ORT 及其闭包的许可要求 | onnx target 默认绑定 + 归档带 `NOTICE` + 验包输出报告 `bundled\|system`（AC18） |
| 包体积失控 | 每包 +约 33MB，10 包约 330MB | 仅 onnx target 绑定；RKNN 不绑定；归档体积计入 `research/baseline.md` 监控 |
| **跨 target 污染** | host 的 SDK prefix / CMakeCache 被用于交叉包，产物架构错或链到错误后端，现象是「板端加载失败/崩溃」 | prefix 带 target 元数据 + 每 target 独立构建目录 + configure 断言（R11 / AC19） |
| **整包独立的重复代价** | decode / NMS / 对齐约 400–600 行纯数学在每份实现各存一份，修一侧漏另一侧 | 每个 target 的 `make test` 用包根 `sample.jpg` 出 `result.jpg`（看图就能发现框偏/漏检），阈值标定也走同一路；**不引入**跨 target 共享夹具（按 target 分叉的向量等于普通单测） |
| 平台事实重新长成框架 | 有人为「少写两遍」在 SDK 里重新引入 `InputFacts` / `prepare_input` / 配置表，把分支从算法搬到 SDK | R13 已撤销并写明禁止；AC21 的 grep 门禁直接拦；`code-review` 遇到即退 |
| 桩后端仍在产品面 | 交叉/无 ORT 的机器静默产出不可推理的"成功"产物；包级测试以 `[SKIP]` 伪装成绿 | R14 + AC22：`#error` 兜底、profile 必须显式、桩仅在夹具 target、`test_stub` 拒绝出包 |
| 删除桩后无 ORT 的机器编不了 native | 新上手/CI 机器直接 configure 失败 | `make native-deps` 本就是唯一联网步骤；错误信息里直接给出该命令 |
| 空实现包携带第三方运行时 | 脚手架生成的包因 SDK 静态库引用编译期后端工厂而被 `NEEDED libonnxruntime`（onnx 档位） | **onnx 档位是预期行为，不是缺陷**：约束改为 AC18（随包 `lib/` + `RUNPATH=$ORIGIN/lib` + `NOTICE` + 验包报 `bundled\|system`）。AC21 的 `readelf` 断言只适用于 `rknn` / `test_stub` prefix；要为 onnx 档位也去 ORT，只能改回 `dlopen`，与本任务方向相反 |
