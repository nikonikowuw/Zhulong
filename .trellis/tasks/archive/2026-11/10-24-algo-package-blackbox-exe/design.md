# Design: 算法包黑盒化 —— 静态链接运行时 + 独立可执行文件入口

> 本文只写技术设计与取舍。需求与验收见 `prd.md`，执行顺序见 `implement.md`。
>
> **⚠️ 决策变更（覆盖本文以下相关内容）**
> 两处决策变更覆盖本文以下内容：**①「一套源码多平台 + 编译期后端宏 + `InputFacts`/`Session::prepare_input` 契约下沉」已撤销**，改为每 target 一套独立实现目录；**② `cpu_mock` 桩后端退出产品面**（仅作测试夹具）。
>
> | 主题 | 原结论 | 现结论 | 依据 |
> | :--- | :--- | :--- | :--- |
> | 多平台源码 | 一套源码 + 编译期宏（§4.8.4） | `packages/<id>/<target>/` 各自独立，零源码引用，允许重复纯数学；**无共享测试夹具**（每 target 用 `sample.jpg` 出 `result.jpg`） | `prd.md` R11、F21–F23 |
> | 归一化 / 输入契约 | `InputFacts` + `prepare_input` + `models/preprocess.json`（§4.8.6） | **整条撤销**：归一化常量留在各实现内 | `prd.md` R13 |
> | 量化三态 | 每模型 `applied ∈ {converter, graph, none}` | 删除；`.rknn` 内嵌 attrs 只作 drift 检查，`CONVERSION.md` 是唯一来源 | `prd.md` R12 |
> | 资产布局 | 运行期 `targets/<t>/` 候选 + 架构宏兜底 | 出包**摊平**到包根，算法侧不认 target 名 | `prd.md` R11 |
> | target 命名 | 主机档位 `cpu`（实为 ONNX） | **`onnx`**；`cpu` 只指 `cpu_mock` 与视觉 CPU 实现 | `prd.md` R11、F25 |
> | 桩后端 | `cpu` 作为可选后端/默认值/profile 档位 | 仅 `onnx`/`rknn` + `#else #error`；桩入夹具，`test_stub` 拒绝出包 | `prd.md` R14、F26–F28 |
>
> §4.8.4、§4.8.6 保留为决策历史（原文已标注「已被取代」），实现与验收一律以 `prd.md` + `implement.md` 为准。

---

## 1. 核心决策摘要

| 决策 | 选择 | 被否掉的替代方案 |
| :--- | :--- | :--- |
| 黑盒实现手段 | **算法包静态链接 `libzhulong_algo_sdk.a`，产出独立可执行文件** | ① 函数表注入式 `zhulong_runtime_api_v1` + `set_runtime`（仍是「插件依赖宿主提供的服务」）；② SONAME 主版本化（`libZhulongAlgoRuntime.so.1`，防不住符号级 ABI 漂移） |
| 进程拓扑 | **一包一进程不变**，进程内 N 个 Worker 线程不变 | 一任务一进程（NPU 权重会被复制 N 份，RK3568 上不可行） |
| 线程模型 | 固定「一 broker 一 Worker 线程」；不提供线程数参数或策略覆盖 | 固定线程池 + 多 broker 复用等调度策略（后续单独设计） |
| 跨边界契约 | **只传数据**：SHM 帧视图 + IPC 控制消息 + JSON 结果 | 跨边界传 C++ 类型（当前 `shared_ptr`/`unique_ptr`/`std::vector<Tensor>`/虚表，已实测段错误） |
| 底库 | 走既有 `MSG_GALLERY_SYNC` IPC，进程内持有只读快照 | 跨 `.so` 全局单例 |
| 包入口 | 仅包根 `<manifest.id>` 可执行文件 | `plugin.so` + `dlopen` 兼容形态及双形态分派 |
| 桩后端 | `cpu_mock` 从产品面移除：factory 仅 `onnx`/`rknn` + `#else #error`，无默认值；桩移入测试夹具（`ZhulongAlgoRuntimeStub`），`test_stub` 产物拒绝出包（`prd.md` R14、F26–F28） | 保留 `cpu` 作为可选后端/默认值（交叉静默产出不可推理产物；包级测试以 `[SKIP]` 伪装通过） |
| target 命名 | 主机档位 = **`onnx`**（决定它的是 ORT/f32），板端 = `rk3568`；**`cpu` 只指 build 侧 `cpu_mock` 桩与视觉 CPU 实现，不得作 target** | 沿用 `cpu` 作主机档位（与 `cpu_mock` 同名不同义，`[algorithm].target` 默认值误导为「CPU 推理」） |
| 多平台形态 | **每 target 一套独立实现目录**（`packages/<id>/<target>/`），target 间零源码引用、零共享夹具；开发期用包根 `sample.jpg` 跑出 `result.jpg` 看图验收；出包摊平 | 一套源码 + 编译期后端宏（N=2 且第二平台不可测，抽象税 > 收益；见文首「决策变更」） |
| 宿主侧人脸特征提取 | 算法包可执行文件 `--extract-feature` 帧流 + Go 侧热进程缓存（§11） | engine `dlopen` 特征提取 `.so`（新形态下无产物，且 `dlopen` PIE 可执行文件被内核/glibc 拒绝） |

---

## 2. 目标形态

### 2.1 进程与线程拓扑（不变的部分）

```
宿主 Go
  └─ Supervisor: 每个已启用算法包 1 个 RunnerProcess
       └─ exec <pkg>/plate_recognition --broker sockA --broker sockB --broker sockC --algorithm-id X
            ├─ main: 解析参数、runtime::init()、algo.init(config_json)   ← Model Group，进程内 1 份权重
            ├─ thread 0 (worker 0) ← sockA → session 0   ← Worker Context，线程独占
            ├─ thread 1 (worker 1) ← sockB → session 1
            └─ thread 2 (worker 2) ← sockC → session 2
```

当前固定一 broker 一 Worker 线程，每个 Worker 对应一路流及其 task context。按任务帧率和设备容量计算 Worker 数、让一个 Worker 复用多个 broker，均不属于本任务。`rknn_dup_context` 的权重共享发生在**同一进程内**，因此本改造不触碰它。这是本次决策不选择「一任务一进程」的根本原因。

### 2.2 二进制依赖拓扑（改变的部分）

| | 改造前 | 改造后 |
| :--- | :--- | :--- |
| 入口 | 共用 `zhulong_algo_runner` + `dlopen plugin.so` | 每个包自带可执行文件（**包根**，与 `models/` 同级，如 `plate_recognition`） |
| `zhulong` 未定义符号 | 9 个（`nm -D` 实测） | **0 个** |
| `zhulong` 动态依赖 | `NEEDED libZhulongAlgoRuntime.so`（SONAME 无版本号） | **无** |
| 第三方动态依赖 | `libonnxruntime.so.1.21`（由 runtime 传递） | `libonnxruntime.so.1.21`（**仍存在**，来源不变） |
| 宿主人脸特征提取 | engine `dlopen libFaceRecognitionPlugin.so` + `dlsym` | 起包可执行文件（`--extract-feature`），stdin 送像素、stdout 收 JSON |
| 包的构建入口 | `native/CMakeLists.txt` 内 `add_library(... ../packages/.../src/*.cpp)`；`build_package.py` 反向 `cmake --build` 取产物 | `packages/<id>/CMakeLists.txt` 独立 `find_package(ZhulongAlgoSDK)`；`make native-build` 不再编包（§4.5） |
| 插件编译所需头 | `sdk/include` + `native/src`（私有头外溢） | `sdk/include` only |

> ⚠️ **静态链接 ≠ 全静态**。`libonnxruntime` / `librknnrt` 仍是动态依赖，来源与现在一致（onnxruntime 由 `make native-deps` 提供并安装到系统路径；`librknnrt` 来自 BSP）。SDK 分发文档必须把这条写清，否则第三方会误以为可离线全静态构建。

---

## 3. 契约变更

### 3.1 `manifest.json` **不新增字段**：包根入口文件名即契约

manifest 保持现状（`id`/`version`/`name`/`task_type`/`category`/`platform`/`resources`）。唯一入口固定为包根 `<manifest.id>` 可执行文件，与 `sample.jpg` / `models/` / `.env` 同属约定式契约。

| 包根内容 | 合法性 | 宿主行为 |
| :--- | :--- | :--- |
| `<manifest.id>` 是包内普通文件；不存在 `plugin.so` | 合法 | `exec.Command(<pkgDir>/<id>, --broker …)` |
| `<manifest.id>` 缺失或不是普通文件 | — | 拒绝安装（`ErrMissingEntry`） |
| 包根存在 `plugin.so`（不论 `<id>` 是否存在） | — | 拒绝安装（旧入口不受支持） |

`manifest.id` 必须通过现有安全 token 校验，且不得等于 `plugin.so`。解析入口时用 `Lstat` 确认 `<id>` 是包内普通文件并拒绝符号链接；验证器随后将其权限规范为 `0755`。运行期只启动该路径，不做 `.so` 探测或回退。


**可执行文件必须与 `models/` 同级（包根），不得放子目录。** 两个现有包的模型定位都是「取当前模块所在目录 + `/models/…`」：`plate_recognition.cpp:32-53` 与 `face_recognition.cpp:60` 使用 `dladdr`。插件代码静态编入可执行文件后，`dladdr` 返回可执行文件所在目录；放子目录会导致生产环境找不到 `models/`。放包根则维持现有路径语义。验包 CWD 也必须是包目录。

> **实现建议（T2，2026-10-25 补充）**：取包根目录时以 `readlink("/proc/self/exe")` 为主路径、`dladdr` 为兜底。静态可执行文件下 `dladdr` 仍可用（有 `_DYNAMIC`），但 `/proc/self/exe` 不依赖动态链接器与传入的符号地址，且包目录被升级替换后能自然暴露 `" (deleted)"` 后缀（先 `TrimSuffix` 再拼 `models/`）。项目已是 Linux-only，无移植性代价。验收：阶段 3.4 的 `/tmp/pkgroot` 用例与升级后重装各跑一次。

### 3.2 可执行文件的 argv 契约

```
<pkg>/<manifest.id>
  --broker <socket_path>          # 可重复，每个订阅流一个
  --name <algorithm_name>         # ≤ 32 字节，沿用 plugin.h 握手缓冲约束
  --algorithm-id <id>
  --verify --sample <image_path>  # 验包模式
  --extract-feature               # 一次性特征提取，帧流协议见 §11
```

不传 `--plugin` 或 `--max-workers`。Worker 数固定等于实际订阅的 broker 数。

> 注：`--max-workers` 在现状 runner 中**并不存在**（`native/` 全仓无此参数，`ProcessConfig` 也没有 `MaxWorkers`）；本条只是「不得新增」的约束，不是「需要移除」的既有能力。

> ⚠️ **刻意不引入 `--config-file`**。算法参数的载体已经存在于 ABI 中：`model_create(config_json)`（`plugin.h:271`，语义为「权重路径、阈值等」）。当前它被恒传 `"{}"`（`runner.cpp:479`、`:630`），是**下发链路未打通**，不是缺口位置错。若在 argv 里另开一个参数文件通道，会造成两套参数体系并存，且引入 installDir 路径拼接面。参见 §10。「算法参数下发」不在本任务范围内。

### 3.3 Worker 数量与调度

本任务沿用当前 `run_ipc_workers` 策略：每个 broker 建立一路连接，并分配一个 Worker 线程、一个执行 session 和一个 task context。宿主传入全部订阅的 `--broker` 参数，SDK 不截断订阅。

本任务**不**根据 `compute_cost`、CPU 核数或任务帧率计算 Worker 数，也不提供 `max_workers` CLI/API。`compute_cost` 仍是算法包资源预算元数据，不映射为 Worker 数。若要根据目标 AI 帧率、实测单 Worker 吞吐及设备容量调度，或让一个 Worker 多路复用 broker，需另行定义策略、背压/丢帧语义和资源上限。

### 3.4 验包入口

| 形态 | 命令 |
| :--- | :--- |
| 唯一支持的可执行文件形态 | `<pkg>/<manifest.id> --verify --sample sample.jpg` |

验包输出末行 JSON / 非零退出码；动态验包直接执行已解析的包入口。

**验包输出必须带明细（2026-10-25 补充）**：现有实现只报 `count`（`runner.cpp:578`），而按 `result_type` 分派的明细序列化器早就存在（`runner.cpp:115 serialize_inference_result`，IPC 路径 `:867` 在用）。两者应当合并：

| 项 | 约定 |
| :--- | :--- |
| 载荷形状 | 结果 JSON = **IPC 结果载荷的超集**（多 `status` / `sample_decoded` / `width` / `height`）；三个消费者共用（broker 长驻 / 一次性推理 / `algo_probe.py`），**禁止**为某个消费者另起字段名 |
| 演进 | **只允许追加字段**，不改名、不改语义；`count` 与载荷数组并存 |
| 宿主兼容 | `VerificationReport` 以指针/slice 接收明细，缺字段 = nil → 跳过比对并 warn，**不判失败**（新宿主必须能读旧包） |
| 不搬的字段 | `stream_id` / `pts` / `capture_time_ms`（验包场景无意义）；`cost_ms` 保留 |
| `sample` 字段 | Go 侧 `verifier.go:301` 已把它换成解码后的**临时 PPM 路径**，渲染方要用原始 `sample.jpg` 作底图 |

> **一次性推理的语义边界（不要留给用户猜）**：不连 `--broker` ⇒ 收不到 `MSG_GALLERY_SYNC`，`matched` 恒 false、`person_name` / `candidates` 恒空；无连续帧 ⇒ `track_id` 恒 0。验包与「UI 上传图片试用」都受此限，输出里不得伪造这两类字段。正因如此，明细输出在本任务就要定死——否则 UI 试用落地时必然要改包输出格式，而那正是本任务要终结的协议漂移。

> ⚠️ **验包 CWD 必须是包目录。** 模型定位依赖包根可执行文件旁的 `models/`；若 CWD 错位，模型定位可能失败或意外命中源码树候选。验收必须把可执行文件和模型复制到临时包根后再运行。

### 3.5 包入口如何到达 spawn 点

当前宿主固定用 `Runtime.PluginPath()` 拼 `plugin.so`，并由 `zhulong_algo_runner` 作为所有包共用的命令。本任务改为由 `Runtime.PackageExecutablePath(id, version)` 返回 `<packageDir>/<id>`，直接将该路径作为子进程命令。

新增纯函数 `algorunner.ResolveEntry(pkgDir, id string) (path string, err error)`，安装期验证和运行期启动共用：

1. 验证 `id` 符合既有安全 token 规则，且不等于 `plugin.so`；
2. 使用 `Lstat` 确认包根 `<id>` 是普通文件，不接受目录或符号链接；
3. 若包根存在旧入口 `plugin.so`，返回明确的旧形态不支持错误；
4. 安装验证时将该普通文件权限规范为 `0755`；运行时使用同一解析结果启动，不做形态猜测。

本任务不增加 `PackageMeta.ComputeCost` / `ProcessConfig.MaxWorkers` 字段；`compute_cost` 继续保留为算法领域元数据。所有 supervisor 启动路径（启动、升级、回滚、崩溃恢复）都必须使用包自身 executable path，避免只改普通启动路径。

### 3.6 特征提取入口

`<pkg>/<manifest.id> --extract-feature`：不接 `--broker`，从 stdin 读帧流、向 stdout 逐帧输出 JSON。协议、弱符号方案、Go 侧热进程与失败语义见 **§11**。

---

## 4. SDK 结构变更

### 4.1 目录

```
native/sdk/
├── include/zhulong/
│   ├── plugin.h                     # 不动（纯 C ABI，唯一跨边界头）
│   ├── algo.hpp                     # C++ 门面；include 改为 <zhulong/runtime/*.hpp>
│   ├── algo_main.hpp                # 新增：SDK main 骨架（可选使用）
│   └── runtime/                     # 新增：从 native/src/algorithm/runtime 收拢的公共头
│       ├── inference_backend.hpp
│       ├── vision_backend.hpp
│       ├── tensor.hpp
│       ├── gallery_registry.hpp
│       └── runtime_factory.hpp
└── src/
    └── algo_main.cpp                # 新增：broker 客户端 + SHM 收帧 + 线程池（从 runner.cpp 抽出）
└── tools/
    ├── algo_probe.py                # 新增：帧流/验包协议自查（第三方与本仓库共用）
    ├── pack_package.py              # 新增：唯一打包实现（staging/验包/归档）
    └── new_package.py               # 新增：脚手架，生成可编译/可验包/可打包的包骨架
```

产出算法包所需的静态 target：

- `ZhulongAlgoRuntimeObjects`（OBJECT，`POSITION_INDEPENDENT_CODE ON`）—— 只供静态目标组合，避免重复编译；
- `ZhulongAlgoRuntimeStatic`（STATIC，`OUTPUT_NAME ZhulongAlgoRuntime`，产物 `libZhulongAlgoRuntime.a`），并将 ONNX Runtime / RKNN 等所选 backend 依赖传递给最终可执行文件。

本任务的算法包与宿主均不使用 `ZhulongAlgoRuntime` 共享库加载路径。现有 SHARED/runner 目标若在迁移期间为基线或旧测试暂留，仅作内部测试，不作为包格式、宿主启动或兼容验收路径。

#### 4.1.1 静态产物的三个必做项

1. **PIC**：OBJECT 库显式设置 `POSITION_INDEPENDENT_CODE ON`。
2. **唯一归属 `dmabuf_allocator.cpp`**：它当前同时编进 runtime 与 IPC。静态 SDK 目标图必须确保最终可执行文件只链接一份该翻译单元，避免 `DmaBufAllocator` 出现两个单例；以最终算法包可执行文件的符号和运行时行为验收。
3. **隐藏实现符号**：**链接侧**加 `-Wl,--exclude-libs,ALL`（静态归档符号不再导出）；**不要**在 `ZhulongAlgoRuntimeObjects` 上设 `CXX_VISIBILITY_PRESET hidden`（2026-10-25 修正）。
   - 理由：只要旧的 `ZhulongAlgoRuntime`（SHARED）还从同一批对象构建、两个 plugin target 还 `target_link_libraries(... ZhulongAlgoRuntime)`，hidden 会让 `.so` 不再导出 `get_default_inference_backend` / `runtime::GalleryRegistry::instance` / `DmaBufAllocator::{allocate,instance}` 等符号，plugin **链接期直接 `undefined reference`**（基线：face 9 个、plate 3 个未定义符号，见 `prd.md` F1），阶段 1 的验证无法走到。
   - `nm -D <exe> | grep -c zhulong` = 0 由两件事共同满足：可执行文件默认不导出未被动态对象引用的符号（不加 `-rdynamic`）+ `--exclude-libs,ALL`。hidden 对静态链入是**冗余**的，而对 SHARED 是致命的。
   - 若确实需要 hidden，唯一正确做法是引入显式导出宏（`__attribute__((visibility("default")))`）并让 OBJECT 库按「SHARED 用 / STATIC 用」编两遍——等于两份实现，本任务不做。
   - 更彻底且推荐的做法：阶段 1 就删掉旧 SHARED/plugin target（与 R5「只支持一种形态」一致），届时 hidden 才没有副作用；代价是 native 侧三个 plugin 用例必须同步迁走（见 `implement.md` Review gate 3）。

静态可执行文件仍可能依赖第三方动态库；这与 Zhulong 自有符号隐藏是不同问题，依赖来源须在 SDK 文档中列明。

### 4.2 头文件收拢的硬约束

- `sdk/include/**` **不得**出现对 `native/src/**` 的 `#include`。落地后 `native/sdk` 的 CMake target 的 `target_include_directories` 里不得再出现 `${CMAKE_CURRENT_SOURCE_DIR}/src`。
  - ⚠️ 2026-10-25 实测补充：只改包 target 的 include 列表**不生效**——`ZhulongAlgoRuntime` / `ZhulongAlgoRuntimeStatic` 的 `src` 是 `PUBLIC`，经 `target_link_libraries` 传播回包 target（`flags.make` 里仍见 `-I…/native/src`）。正确做法是把运行库产物的用法要求收敛为 `PUBLIC sdk/include` + `PRIVATE src`。
- `runtime_factory.hpp:7-10` 的注释必须改写：算法包只使用静态链接运行时；若仓库保留 SHARED target，只限 native 测试与迁移基线，不作为算法包交付形态。
- 实现细节头（`cpu_inference_backend.hpp`、`cpu_vision_backend.hpp`、`letterbox_geometry.hpp`）**不进** SDK。
- **实际落地布局（2026-10-25，比本节原稿多两个头）**：`zhulong/runtime/{runtime_factory,vision_backend,tensor,gallery_registry,inference_backend}.hpp` 之外，`yuv_sampling.hpp` 进 `zhulong/runtime/`、`dmabuf_allocator.hpp` 进 `zhulong/platform/`。二者都是**生产路径**被包代码直接 include 的头（`plate_recognizer.cpp` 的采样数学、`face detector.{hpp,cpp}` 的 DMA-BUF 零拷贝），不搬就物理上无法移除 `-I native/src`；它们本身只依赖标准库，不会把 `src` 依赖带进分发物。

### 4.3 SDK main 骨架（形状，非最终签名）

```cpp
// include/zhulong/algo_main.hpp
namespace zhulong::sdk {

struct Options {
    std::vector<std::string> brokers;
    std::string client_name, algorithm_id, sample_path;
    bool verify = false;
};

template<class PluginClass, class ResultAdapter>
int run(int argc, char** argv, const char* plugin_name, const char* plugin_version);

} // namespace
```

算法作者只绑定算法类、SDK 结果适配器与包内元数据，不实现 broker、IPC 或 worker 循环：

```cpp
ZHULONG_ALGO_MAIN(MyDetector, zhulong::sdk::DetectionResultAdapter,
                  "MyDetector", "1.0.0")
```

该宏生成唯一 `main` 并调用 `zhulong::sdk::run<PluginClass, ResultAdapter>(argc, argv, PluginName, PluginVersion)`。`run` 的模板包装在 `algo_main.hpp` 中定义，再委托给 SDK 静态库的非模板 runner。内部 `PluginFactory` 是构造现有 C ABI API 表的函数指针，真正的 C 入口签名只有三个参数：`zhulong_status_t (*)(uint32_t requested_version, void* out_api_table, size_t table_size)`（`plugin.h:363`，已实测核对）；`PluginApi<...>::get_api` 多出来的 `plugin_name` / `plugin_version` 是 SDK 静态成员形参，不是函数指针契约。该类型仅在 SDK `detail` 层使用，不是算法作者需要实现的扩展点，也不跨进程/公开 ABI。SDK 不使用 `std::function`。四种公开结果适配器为 `DetectionResultAdapter`、`ClassificationResultAdapter`、`PersonStructureResultAdapter`、`VehicleStructureResultAdapter`，均位于 `zhulong::sdk`。

> 关键：`run()` 内部保留现有 `run_ipc_workers`（`runner.cpp:612`）的全部语义 —— 握手（`name_check`）、`shm_init`、`gallery_sync`、`broker_disconnected` 的 stage token 必须逐字保留，否则 Go 侧 `validRunnerStage` allowlist（`process.go:432`）会把它们降级成 `protocol record rejected`，丢掉排障最需要的信号。

### 4.4 SDK 静态库 `libzhulong_algo_sdk.a` 的源集合（分发物的另一半）

- design 原先只定义了 `ZhulongAlgoRuntimeStatic`，但第三方实际要链接**两个**归档：运行时 + SDK main。`libzhulong_algo_sdk.a` 必须由**已编译好的对象**组成，第三方拿不到 `native/src/**`：
  - `native/sdk/src/algo_main.cpp`（新增）；
  - `native/src/algorithm/ipc/*`（`unix_socket` / `shm_pool` / `ipc_protocol` / `frame_*`）—— 现有 `ZhulongAlgoIpc` 的对象；
  - `native/src/algorithm/runner/gallery_index.hpp` / `gallery_parser.hpp` 的非 inline 部分；
  - `src/common/log.cpp`（`ZL_LOGI` 系列宏）—— **最容易漏**的一项。
- 归入后，`ZhulongLog` / `ZhulongAlgoIpc` 不应再作为独立 `.a` 出现在分发物里（否则第三方要猜「到底链几个」）：它们的目标文件被 `libzhulong_algo_sdk.a` 吸收，或作为 CMake INTERFACE 依赖一并分发。两种做法都行，**但必须在 `abi.json` 与 `templates/CMakeLists.txt` 里写清链接顺序**，否则静态库循环依赖会让第三方卡在 `undefined reference`。
- 门禁：阶段 7.2 的干净环境验证就是这条的验收——只要有一个头还 `#include "algorithm/..."` 或缺一个 `.a`，容器里必失败。

### 4.5 包的独立构建 + SDK 可安装（方案 A）

**问题（F20）**：两个参考包没有自己的构建文件——target 定义住在 `native/CMakeLists.txt`，包级 ctest 住在 `native/tests/`，`build_package.py` 反向调用宿主构建树。这等于「我们的包走 A 路径、第三方走 B 路径」，而 B 路径（`sdk-dist` + 干净容器）只在发布前跑一次，平时无人踩。

**目标形态**：

```
native/CMakeLists.txt          # 只产：engine / runtime(.a) / sdk(.a) / mock 夹具 / native 测试
packages/<id>/<target>/CMakeLists.txt   # 自足：find_package(ZhulongAlgoSDK) → add_executable(<manifest.id>)
packages/<id>/<target>/tests/            # dev-only 开发期测试：sample.jpg → result.jpg（不进归档）
build/sdk/prefix/              # make sdk-install 产出（include/ lib/ cmake/）
```

`make sdk-install` 离线产出 prefix，并由 `ZhulongAlgoSDKConfig.cmake` 导出两个 imported target：

| imported target | 内容 | 链接顺序 |
| :--- | :--- | :--- |
| `Zhulong::AlgoSDK` | `libzhulong_algo_sdk.a`（main + ipc + gallery + log） | **在前** |
| `Zhulong::AlgoRuntime` | `libZhulongAlgoRuntime.a` + 第三方依赖传递（onnxruntime / rknn，用 `find_library` 在使用端解析，不烧构建机路径，§4.5.0） | **在后** |

链接顺序写在 config 里，第三方与本仓库共用同一份——不允许各自猜。

包里只能长这样（禁止 `if(TARGET ...)` 双分支、禁止引用 `../native` 源码路径）：

```cmake
cmake_minimum_required(VERSION 3.20)
project(plate_recognition CXX)
find_package(ZhulongAlgoSDK 1.0 REQUIRED)

add_executable(plate_recognition src/plate_recognition.cpp src/plate_detector.cpp src/plate_recognizer.cpp)
target_link_libraries(plate_recognition PRIVATE Zhulong::AlgoSDK Zhulong::AlgoRuntime)
set_target_properties(plate_recognition PROPERTIES
    OUTPUT_NAME plate_recognition
    INSTALL_RPATH "$ORIGIN/lib"                     # 只用于随包绑定的第三方运行时（§4.5.4）
    LINK_FLAGS "-Wl,--exclude-libs,ALL")
```

**包里不需要（也不允许）手写 SDK 路径**：没有 `include_directories(${SDK}/include)`、没有 `link_directories(${SDK}/lib)`、没有裸 `-lZhulongAlgoRuntime`、没有 `if(TARGET ZhulongAlgoRuntimeStatic)` 回退。SDK 只通过两个 imported target 出现，链接顺序、include 目录、第三方依赖传递全部由 `ZhulongAlgoSDKConfig.cmake` 定义。

**唯一需要开发人填的是 prefix**：`cmake -DCMAKE_PREFIX_PATH=<sdk>`（包 Makefile 里就是 `SDK_PREFIX`）。

#### 4.5.0 SDK config 必须可搬迁

`ZhulongAlgoSDKConfig.cmake` 不能把构建机的绝对路径烧进去（否则 SDK tarball 解包到任何其他路径就失效）：

- `IMPORTED_LOCATION` 用 `${CMAKE_CURRENT_LIST_DIR}/../../..` 推算 prefix（CMake 相对定位）；
- 第三方依赖用 `find_library` 在使用端解析：`find_library(ZHULONG_ORT_LIB onnxruntime HINTS ${ONNXRUNTIME_ROOT}/lib)`，rknn 同理；找不到时报出「需要设 `ONNXRUNTIME_ROOT` 或把 onnxruntime 装到系统路径」而不是一个裸链接错误；
- 链接顺序在 config 里表达：`Zhulong::AlgoSDK` 的 `INTERFACE_LINK_LIBRARIES` 指向 `Zhulong::AlgoRuntime`，最终顺序恒为 **sdk → runtime → 第三方**；
- 验门禁：`tar xf sdk-dist.tar.gz -C /tmp/other-prefix && cmake -DCMAKE_PREFIX_PATH=/tmp/other-prefix ...` 必须能 configure 成功。

**包自己的 Makefile**（每个 `packages/<id>/` 一份，与 SDK 模板同形）：

```make
# packages/plate_recognition/Makefile
SDK_PREFIX ?= $(abspath ../../build/sdk/prefix)   # 第三方：make SDK_PREFIX=/opt/zhulong-algo-sdk
PKG        ?= $(notdir $(CURDIR))                # 与 manifest.id 同名
TARGET     ?= onnx
TOOLCHAIN  ?=

all: build/$(PKG)
build/$(PKG): CMakeLists.txt $(wildcard src/*.cpp)
	@test -f $(SDK_PREFIX)/cmake/ZhulongAlgoSDKConfig.cmake || \
	  { echo "SDK prefix 不在 $(SDK_PREFIX)；先跑根目录的 make sdk-install，或指定 SDK_PREFIX="; exit 1; }
	cmake -B build -S . -DCMAKE_BUILD_TYPE=Release -DCMAKE_PREFIX_PATH=$(SDK_PREFIX) \
	      $(if $(TOOLCHAIN),-DCMAKE_TOOLCHAIN_FILE=$(TOOLCHAIN))
	cmake --build build --target $(PKG)

test: all
	# dev-only：出 result-<id>-<target>.jpg + 终端明细表；可依赖图像库，不进归档
	cmake --build build --target $(PKG)_devtest
	./build/$(PKG)_devtest sample.jpg

package: all
	$(PYTHON) $(SDK_PREFIX)/tools/pack_package.py --package-dir . --build-dir build \
	  --target $(TARGET) --out-dir dist $(if $(TOOLCHAIN),--no-verify,)

verify: all
	./build/$(PKG) --verify --sample sample.jpg
```

三个入口就是你要的 `make` / `make test` / `make package`，而且**在包目录里就能跑完整个生命周期**（编 → 测 → 打包 → 验包），不需要回仓库根。

与 SDK 模板的唯一差异：`SDK_PREFIX` 默认值（仓库内指 `../../build/sdk/prefix`，第三方指 `/opt/zhulong-algo-sdk`）。这个差异要能被机器校验：

```bash
diff -u zhulong-algo-sdk/templates/Makefile packages/plate_recognition/Makefile   # 只允许 SDK_PREFIX 那一行
```

根 Makefile 只做委派（不重写任何编译/打包逻辑）：

```make
packages-build: sdk-install
	@for p in $(PACKAGES); do $(MAKE) --no-print-directory -C packages/$$p || exit 1; done
packages-test: packages-build
	@for p in $(PACKAGES); do $(MAKE) --no-print-directory -C packages/$$p test || exit 1; done
packages-package: packages-build
	@for p in $(PACKAGES); do $(MAKE) --no-print-directory -C packages/$$p package \
	    TARGET=$(PKG_TARGET) $(if $(TOOLCHAIN_FILE),TOOLCHAIN=$(TOOLCHAIN_FILE)) || exit 1; done
	@mkdir -p build/packages && cp packages/*/dist/*.tar.gz build/packages/   # 保留 Go 测试既有路径
```

Make 分工（**根目录只需记 4 个入口**，其余是内部实现细节）：

| 入口 | 做什么 |
| :--- | :--- |
| `make` | 默认目标：前端构建 + native + SDK prefix + 两个包 + api-docs + 宿主二进制（产物齐全，可直接 `make run`） |
| `make test` | 全量测试：`native-test` + `packages-test` + 前端 test + go vet / race test / bridge |
| `make package` | 出算法包归档（= 每个包目录里的 `make package`，根目录只负责聚合到 `build/packages/`） |
| `make check` | 发布门禁：`make test` + 前端 lint/type-check/build + api-docs |

内部分解目标（CI 与脚本用，**不要求开发者记**）：

| 目标 | 内容 |
| :--- | :--- |
| `native-build` / `native-test` | 宿主 + 静态库 + SDK prefix 依赖物 + mock 夹具 + native 测试（**不编包代码**） |
| `sdk-install` | 生成 `build/sdk/prefix`（离线，无下载） |
| `packages-build` / `packages-test` | 逐包 `$(MAKE) -C packages/<id>` / `$(MAKE) -C packages/<id> test`（编译与测试逻辑在包自己的 Makefile + CMakeLists） |
| `packages-package` | 逐包 `$(MAKE) -C packages/<id> package`（调 SDK 里的 `tools/pack_package.py`），再把归档聚合到 `build/packages/`；支持 `PKG=<id>` 单包、`PKG_TARGET=onnx\|rk3568\|rk3588` |
| `frontend-check` / `go-check` / `api-docs` | 原语义不变 |

依赖链（写在 Makefile 里，不靠人记）：

```
make            → go-build → {frontend-build, native-build, packages-build, api-docs}
make test       → {native-test, packages-test, go-check, web test}
make package    → packages-package → packages-build → sdk-install → native-build
make check      → test + frontend-check + api-docs
```

两个必须显式声明的依赖：

1. `go-check: native-build packages-build`——`internal/algorithm/*_pkg_test.go` 会调用打包薄壳，缺 `build/packages/<id>/<id>` 时那批用例只能 skip；
2. `CROSS_PROFILE` 非空时，`packages-package` 自动追加 `--no-verify`（交叉不可执行，AGENTS.md 硬约束）。

**不选 B（顶层 `add_subdirectory(../packages/x)`）**：省一步，但包 CMakeLists 里必须写 `if(TARGET ZhulongAlgoRuntimeStatic)` 双分支，把我们正在消灭的「双形态」原样写进构建文件；且顶层构建仍会编译包，AC16 的「`make native-build` 不产生 `*Recognition*`」就无法成立。

#### 4.5.1 SDK 是构建期依赖，不是拷贝

**明确结论：包目录内不得出现 SDK 头文件/静态库的副本。** SDK 通过 prefix 引用（`find_package`），只在编译期存在；静态链接完成后，**发布物里不含任何 SDK 文件**：

```
构建期（需要 SDK）                    发布物（不含 SDK）
find_package(ZhulongAlgoSDK)        <pkg>/<manifest.id>      ← SDK 代码已静态链入
  └─ $SDK_PREFIX/include/…          <pkg>/manifest.json
  └─ $SDK_PREFIX/lib/*.a            <pkg>/config.schema.json
                                    <pkg>/models/  <pkg>/sample.jpg
```

不复制的原因（每条都是真实的失效模式）：

| 复制一份到每个包 | 后果 |
| :--- | :--- |
| SDK 版本碎片 | 每个包各自 pin 一份，门面/协议一改就静默分叉，出问题无法定位用的是哪版 |
| 修复扩散 | SDK 一个 bug 要改 N 份；第三方那份永远停在发布当天 |
| 等价性门禁失效 | 「仓库包 = 第三方包」靠同一个 prefix 保证；复制后各走各的，AC16/AC17 失去意义 |
| 体积 | 每包多一份头 + 归档副本（SDK 代码已静态链进二进制，再存一份源码纯浪费） |

SDK 的三种获取方式，**只差一个变量**：

| 场景 | 做法 |
| :--- | :--- |
| 仓库内两个参考包 | `SDK_PREFIX ?= $(abspath ../../build/sdk/prefix)`——指向仓库的构建产物，**不是拷贝** |
| 第三方 / 已安装 | `make SDK_PREFIX=/opt/zhulong-algo-sdk`（或放到 CMake 默认可搜路径） |
| 内网 / 离线 / hermetic | 把 SDK tarball 放进对方仓库（如 `third_party/`）解包到某目录，`SDK_PREFIX` 指过去；无需我们写代码支持 |

#### 4.5.2 SDK 版本门禁（静态链接没有 SONAME）

包与 SDK 分开发布，必须能回答「这个包是用哪版 SDK 编的」：

- `make sdk-install` 除 `ZhulongAlgoSDKConfig.cmake` 外，额外产出 `VERSION` 文件与 `ZhulongAlgoSDKConfigVersion.cmake`（`write_basic_package_version_file`）；
- 包 Makefile 声明 `SDK_MIN_VERSION ?= 1.0`，在 configure 前校验，失败信息必须含两者版本号：
  `需要 ZhulongAlgoSDK ≥ 1.0，当前 0.9（$SDK_PREFIX/VERSION）`；
- `abi.json` 里的 `api_version` / `protocol` 与 `VERSION` 同源生成，避免三个地方各写一遍。

静态链接的包里查不到被链接库的 SONAME，因此**版本门禁只能显式做**；漏了就是「编得成功、跑起来行为不一致」——与旧 `.so` 的 SONAME 债同类，只是发生在构建期。

#### 4.5.3 为什么没有 SDK 也能跑

静态链接把 `.a` 里的**目标代码复制进可执行文件**，构建完成后 `.a` 已与运行无关。仓库自己的宿主就是证据（引擎以 `libZhulongEngine.a` 静态链入）：

```bash
ls -lh build/Zhulong          # 67M —— 引擎/FFmpeg 的目标代码都在里面
ldd build/Zhulong             # 只有 libstdc++/libm/libgcc_s/libc；无 libZhulongEngine.so、无 onnxruntime
```

对照「必须携带才能跑」的旧形态：

```bash
readelf -d build/native/host/libFaceRecognitionPlugin.so | grep NEEDED
# NEEDED libZhulongAlgoRuntime.so   ← 少一个就 dlopen 失败
```

所以「包里没带 SDK」的准确含义是：**不带头文件 / `.a` / SDK 源码**；运行所需的机器码已在 `<id>` 里。

**但独立运行 ≧ 零外部依赖**，部署清单是：

| 文件 / 依赖 | 来源 | 说明 |
| :--- | :--- | :--- |
| `<pkg>/<manifest.id>` | 包 | 一个文件，SDK 已静态链入 |
| `manifest.json` / `config.schema.json` / `sample.jpg` | 包 | 约定式文件 |
| `models/*.onnx`（或 `*.rknn`） | 包 | 与入口同级目录 |
| `libonnxruntime.so.1.21` | **我们**（`make native-deps` 固定版 + SHA256） | 装到目标机系统路径或按文档设 `LD_LIBRARY_PATH` |
| `librknnrt.so` + NPU 驱动 | **BSP 厂商** | 板端镜像自带，不随包分发 |
| `libstdc++` / `libc` / `libm` / `libgcc_s` | 系统 | 不需要直传 |

**边界**：不追求「第三方也全静态」。ONNX Runtime 静态库需单独评估体积与许可，`librknnrt.so` 闭源且只有 `.so`，所以该目标不可达；把它写成明确边界，而不是留给人猜。

#### 4.5.4 第三方运行时的三种处置（与「不复制 SDK」的区别）

「不复制 SDK」与「随包带 onnxruntime」不是同一件事：

| | SDK 归档（`libzhulong_algo_sdk.a` / `libZhulongAlgoRuntime.a`） | `libonnxruntime.so.1.21` |
| :--- | :--- | :--- |
| 能否进可执行文件 | 能（自家代码，有 `.a`） | ORT 本可静态，但**闭包不可**：实测依赖 47 个库（`libonnx`、`libprotobuf`、37 个 `libabsl_*`、`libXNNPACK`、`libre2`、`libcpuinfo`…） |
| 包里放什么 | 不放（机器码已链入） | **放**：`lib/` 目录 + `RUNPATH=$ORIGIN/lib` |
| 复制是否有意义 | 无意义，纯重复 | 有意义，这是运行期真实需要的文件 |

**现状比想象更脆**：`build/deps/onnxruntime/lib/libonnxruntime.so.1.21` 是指向 `/usr/lib/x86_64-linux-gnu/` 的符号链接，而 `libZhulongAlgoRuntime.so` **无 RPATH/RUNPATH**——完全依赖系统包。目标机（尤其无包管理的板端）没这个系统包，`dlopen` 就失败。

处置政策：

| 依赖 | 处置 | 理由 |
| :--- | :--- | :--- |
| zhulong SDK / 运行时 | 静态链入，包内不放文件 | 自家控制版本，静态化彻底消除 ABI 债（AC1） |
| onnxruntime（`onnx` 档位） | **随包绑定**：`lib/*.so*` + `RUNPATH=$ORIGIN/lib` + `NOTICE` | 版本随包固定、现场零安装；进程模型下每包一份互不干扰；MIT 许可可再分发 |
| `librknnrt` / NPU 驱动（rknn target） | **不绑定**：声明 `requires BSP ≥ X`，启动时探测 | 闭源、许可通常禁止再分发，且必须与 BSP 固件/驱动一致 |

体积代价（实测）：ORT 闭包 47 个文件、**约 33MB/包**（`libonnxruntime` 16M + `libonnx` 4M + protobuf/absl/XNNPACK 等）；tar.gz 压缩后约为一半，需实测记录。10 包 ≈ 330MB，相对 NPU 权重仍可接受。

**不选的反面方案**：把 ORT 放在 `<dataDir>/algorithms/.runtime/` 让所有包共享——`$ORIGIN/../../` 依赖安装深度，升级时会静默指向旧版本，与旧 SONAME 债同类。

**不改 manifest 字段**（R1）也能自描述：

- 包内有 `lib/` → `RUNPATH=$ORIGIN/lib` 自然命中 → `bundled`；
- 包内无 `lib/` → 退到系统路径 → `system`；
- `--verify` / `algo_probe` 输出报告 `runtime: bundled|system (libonnxruntime.so.1.21 @ <路径>)`，运维一眼可查。
- 验包门禁：onnx target 下 `ldd <id>` 中的 ORT 路径必须指向包内 `lib/`；归档里必须含 `NOTICE`（ORT 及其第三方声明的聚合文件）。

**代价**：开发循环多一步（`make build` 串联）；交叉需要两份 prefix；`build/native/host` 不再是「全产物」目录（Go 测试里的几个固定路径随 exe 形态消失，剩余 mock 仍留 native）。

### 4.6 打包：Make 编排、Python 实现（一份，不抄两遍）

**现状问题**：两个包各有一份 ~150 行的 `build_package.py`（diff 112 行），各自维护 staging、入口命名、归档命名与验包调用。包一旦自己构建，这份重复就是维护债：改归档布局要改两处，而第三方看不到任何一份。

**分工原则**：

| 层 | 职责 | 禁止 |
| :--- | :--- | :--- |
| `Makefile` | 依赖顺序、参数、交叉开关 | 不出现 `cp`/`tar`/`jq` 序列（那是第二个实现） |
| `native/sdk/tools/pack_package.py` | **唯一实现**：staging、`manifest.id` 解析、`0755`、验包、归档命名 | 不得 import 本仓库模块、不读 `native/`（否则 AC7 干净容器必挂） |
| `packages/<id>/scripts/build_package.py` | 10 行薄壳，保持现有 Go 测试的调用路径可用 | 不再实现任何打包逻辑 |

**Make 目标**（打包入口在**包目录**里，根 Makefile 只委派）：

```make
# packages/<id>/Makefile —— 就是上面的三个入口
# 根 Makefile：
packages-package: packages-build
	@for p in $(PACKAGES); do $(MAKE) --no-print-directory -C packages/$$p package \
	    TARGET=$(PKG_TARGET) || exit 1; done
	@mkdir -p build/packages && cp packages/*/dist/*.tar.gz build/packages/
```

开发者视角：

```bash
cd packages/plate_recognition && make && make test && make package   # 包内闭环
make package                                                        # 根目录：两个包都打，归档聚合到 build/packages/
```

关键：`PKG_VERIFY_FLAG` 由 Make 根据 `CROSS_PROFILE` 是否为空自动设为 `--no-verify`，把「交叉不允许执行」这条铁律放进编排层，而不是靠人记得加参数。

`pack_package.py` 的输入契约（自己的参数就是契约）：`--package-dir`（含 `manifest.json` / `targets/<target>/`）、`--build-dir`（已构建的 `<manifest.id>`）、`--models-dir`（可选覆盖）、`--target`、`--no-verify`。模型目录默认 `<package-dir>/targets/<target>/models`。

### 4.7 脚手架：一条命令生成包骨架

每个包都要一份 SDK + 一套同形的构建/打包文件。让第三方（和我们自己）手抄一遍，就是把三个已知的坑（`plugin.so` 残留、入口名 ≠ id、`models/` 不在包根）交给运气。所以 SDK 带一个生成器：

```bash
python3 <sdk>/tools/new_package.py my_detector --target onnx
# 仓库内：make new-package PKG=my_detector     （落到 packages/my_detector/）
```

生成树（全部可跑，不是占位符）：

```
my_detector/
├── CMakeLists.txt          # find_package(ZhulongAlgoSDK) + add_executable(<id>)
├── Makefile                # make / make test / make package / make verify
├── manifest.json           # id/name/version/task_type/category/platform{arch,target}/resources
├── config.schema.json      # 最小合法空 schema（type:object, properties:{}）
├── sample.jpg              # 占位样本（README 要求换成真实图）
├── src/my_detector.cpp     # ZHULONG_ALGO_MAIN + init/create_session/detect 空实现
├── tests/                  # dev-only：dev_infer.cpp + stb_image*（不进归档）
├── scripts/build_package.py# 10 行薄壳
└── README.md               # 三个命令 + 「空实现 = 永远 0 检出」的说明
```

**开箱验收（模板质量的唯一证据）**：

```bash
cd my_detector && make && make test && make verify && make package
# verify 输出 {"status":"passed","count":0}；归档里入口名为 my_detector、无 plugin.so
```

空实现返回零检测框是合法的（`--verify` 只看 status），但模板**绝不允许**合成检测框或模拟特征——那是 `algo-plugin-contract.md` 明令禁止的静默退化。

**参数校验必须复用宿主规则**：生成的 `id` 要过 `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`（`verifier.go:50` 的 `manifestTokenPattern`），并显式拒绝 `plugin.so`。否则会出现「脚手架生成了、装到宿主才被拒」的往返。

**模板防腐**：`make sdk-selftest` 每次都生成到临时目录 → `make` → `make verify` → `make package`，并跑在 `make check` 里。模板一旦腐烂（缺 `log.cpp`、漏 `Makefile` 入口、生成物不可编译），CI 第一次就红，而不是等到发布前的容器测试。

**自己的两个包可以是脚手架生成后改造的**，但必须满足：`Makefile` 与模板只差 `SDK_PREFIX` 一行；`CMakeLists.txt` 与模板只差目标名与源文件列表（AC16 门禁）。

### 4.8 多 target 构建矩阵（一套源码，按 target 分别编）

**结论：源码只有一份，但四个对象必须各自按 target 重编**：宿主、SDK prefix、包可执行文件、包归档。不要把「一套代码」误读成「一产物通吃」。

#### 4.8.1 现状（已实验证）

| 事实 | 证据 |
| :--- | :--- |
| target 由 profile JSON 定义：工具链、sysroot、arch/goarch、CFLAGS、backend | `native/profiles/{rk3568,rk3568-rknn,rk3568-rknn-rga,linux-arm64.example}.json` |
| 每次构建生成独立的 toolchain 与构建目录：`build/native/<mode>`，`mode = host \| cross-<profile>` | `build.py:452`、`build.py:480` |
| 交叉默认**不得**落 onnx 后端（板端要能匹配 sysroot 的实现） | `build.py:233-239` |
| 宿主交叉产物必须在 `build/targets/<name>/` 下，且**只编译不执行测试** | `Makefile` 的 `native-cross-build`、AGENTS.md |
| 包资产本就按 target 分开：模型格式与 manifest 字段不同 | `packages/*/targets/{onnx,rk3568}/` |
| Host/Cross 库混用已有门禁 | `native/tests/build_contract_test.py` |

#### 4.8.2 必须按 target 分开的输入与产物

| 对象 | host（onnx） | 交叉（rk3568 等） | 能否共享 |
| :--- | :--- | :--- | :--- |
| 源码 | 同一份 | 同一份 | ✅ 唯一可共享的 |
| toolchain / sysroot | 系统工具链 | profile JSON（`build/native/cross-<p>/toolchain.cmake`） | ❌ |
| SDK prefix | `build/sdk/prefix/host` | `build/sdk/prefix/<profile>` | ❌ 架构/后端不同，**绝不能跨 target 复用** |
| 推理后端 | onnx | rknn（默认） | ❌ 编译期宏 |
| ORT / RKNN 运行时 | x86_64 闭包 | aarch64 闭包 / BSP `.so` | ❌ |
| 包可执行文件 | `packages/<id>/build/host/<id>` | `packages/<id>/build/<profile>/<id>` | ❌ |
| 模型 | `targets/onnx/models/*.onnx` | `targets/rk3568/models/*.rknn` | ❌ 格式不同 |
| 归档名 | `<id>-<arch>-<target>.tar.gz`（如 `x86_64-onnx`） | 同上，`aarch64-rk3568` | ❌ 名不同才能共存 |

#### 4.8.3 命令矩阵

```bash
# 本机（x86_64 + onnx）
make                                   # 宿主 + 两个包
make package                           # PKG_TARGET=onnx，归档名含 x86_64-onnx

# 交叉（rk3568 + rknn）：宿主与包都必须用同一份 profile
make cross CROSS_PROFILE=rk3568        # → build/targets/rk3568/Zhulong（只编不测）
make package TARGET=rk3568 CROSS_PROFILE=rk3568
                                       # → aarch64-rk3568 归档，自动 --no-verify

# 全矩阵（发布前）
make package-all                       # 对 TARGETS=onnx rk3568 逐个跑上述包流程
```

实现要点（全部落在构建系统里，不靠人记）：

1. **构建目录必须带 target**：`packages/<id>/Makefile` 用 `BUILD_DIR ?= build/$(if $(TOOLCHAIN),$(TARGET),host)`。同一目录混用两个 target 会让 CMake 复用另一个架构的 `CMakeCache`，后果是 `file in wrong format` 或更糟的静默错架构对象。
2. **SDK prefix 自带 target 元数据**：`sdk-install` 在 prefix 里写 `sdk-target.json`（`arch` / `target` / `inference_backend`）；包 configure 时断言与 `TARGET` 一致，不一致直接报错。这是防「host prefix + 交叉工具链」的唯一可靠手段。
3. **交叉时验包只能跳过**：`TOOLCHAIN` 非空 → `pack_package.py --no-verify`。AGENTS 明确「交叉只允许编译，不允许执行测试」，不得用 qemu 假装验证。
4. **替代验证（交叉）**：
   ```bash
   file build/targets/rk3568/Zhulong | grep -q 'ARM aarch64'
   file packages/plate_recognition/build/rk3568/plate_recognition | grep -q 'ARM aarch64'
   readelf -d packages/plate_recognition/build/rk3568/plate_recognition | grep NEEDED  # 期望无 onnxruntime
   nm -D --undefined-only <bin> | grep -c zhulong                                    # 仍为 0
   ```
   真正的行为验证只能在板端做（手工 smoke），并在 `research/baseline.md` 记录「已测 / 未测」。
5. **模型与 manifest 按 target 取**：`pack_package.py --target` 决定 `targets/<target>/models` 与 `targets/<target>/manifest.json`；不得把 `onnx` 的模型打进 `rk3568` 归档（校验：`tar tzf` 里的扩展名与 target 一致）。

#### 4.8.4 为什么一套源码能覆盖多平台（以及它在哪里不成立） —— **已被取代（见文首「决策变更」；`prd.md` R11/R13）**

> **⚠️ 历史设计记录（已废弃）**：本节所描述的「一套源码 + 编译期宏跨平台」方案已被 2026-10-25 评审结论彻底撤销（理由见文首表格及 `prd.md` R11）。保留此节仅用于记录得失分析与为什么否定该路线。**现行方案一律采用 `packages/<id>/<target>/` 独立目录，target 间物理隔离、零源码引用。**

| 层 | 是否按平台分叉 | 仓库证据 |
| :--- | :--- | :--- |
| 算法逻辑（`packages/<id>/src`） | 不分叉（应保持） | 两个包的 src 无 `ZHULONG_INFERENCE_*` 宏 |
| 推理接口 `IInferenceBackend` | 不分叉 | `inference_backend.hpp`：上层只依赖接口，不感知厂商 SDK |
| 视觉接口 `IVisionBackend` | 不分叉 | letterbox 几何由 `letterbox_geometry.hpp` 单一来源，CPU/RGA 共用 |
| 后端实现 | **分叉** | `cpu_/onnx_/rknn_inference_backend.cpp`、`cpu_/rga_vision_backend.cpp` |
| 后端选择 | 编译期宏，非运行期 dlopen | `runtime_factory.cpp:31-49`（`ZHULONG_INFERENCE_RKNN/ONNX` × `ZHULONG_VISION_RGA`，两维正交） |
| 模型资产 / 阈值 | **分叉（资产层）** | `packages/*/targets/{onnx,rk3568}/` |

被接口吸收的差异（写在后端里，算法代码看不见）：runtime API（ORT vs RKNN）、张量 stride/对齐、量化 int8 解量化、dma-buf vs host 内存、RGA 与 CPU 缩放取整差异。

**不能被吸收、必须进资产的差异**：模型格式（`.onnx` vs `.rknn`）、量化参数、阈值（量化误差可能要求不同默认值）、驱动/BSP 版本。

**两条硬规则（写进审查）**：

1. 算法源码里**不得**出现后端选择宏（`ZHULONG_INFERENCE_*` / `ZHULONG_VISION_*`）。出现即说明有逻辑本该进后端层。
2. 架构宏只允许用于「开发期模型路径兜底」（现有两处：`plate_recognition.cpp:80`、`face_recognition.cpp:26` 的 `targets/<target>/` 候选），**不得**用于改变算法语义。

门禁：

```bash
grep -rn 'ZHULONG_INFERENCE_\|ZHULONG_VISION_' packages/*/src   # 期望：空
grep -rn '__aarch64__\|__arm__' packages/*/src                  # 只允许模型路径兜底两处
```

**跨 target 等价性的边界**：量化/取整/RGA 采样使不同 target 的输出**不保证逐位一致**；因此 AC3 的可比性只在「同一 target 的前后版本」之间成立，跨 target 只能给容差 + 板端实测，不得用宿主结果代替。

**封装边界：藏机制，露语义**（这是「一套代码」成立的真实原因）

| 能力 | 封装在 | 算法侧看到的 | 证据 |
| :--- | :--- | :--- | :--- |
| 模型加载（权重/格式/会话共享） | `IInferenceBackend::load_model` / `create_session` | `ModelAsset` / `SessionContext` 基类 | `inference_backend.hpp:37-68`（`create_session` 注释直接引用 `rknn_dup_context` 权重共享） |
| 量化（int8 / zp / scale） | 后端内部 `want_float = 1` 统一转 float32 | 只有 float 张量 | `rknn_inference_backend.cpp:372`；`grep -rn 'zero_point\|quant' packages/*/src` 为空 |
| 推理执行 | `forward(session, inputs, outputs)` | 自描述 `Tensor`（dims / dtype / packed bytes） | `tensor.hpp`（DMA-BUF 零拷贝也在 Tensor 里，同步细节不外泄） |
| 图像前处理 | `ops::letterbox` → `IVisionBackend` | 输入 + 回填的 `scale/pad_left/pad_top` | `algo.hpp:313-330`、`letterbox_geometry.hpp` 单一几何来源 |
| NMS | 共享实现 `zhulong::nms` | `ops::nms(detections, iou)` | `algo.hpp:337`（不按后端分叉） |

**不能藏、必须显式暴露的四条契约**（藏起来就变隐性约定，第三方一定会踩）：

1. **线程独占**：`session` 单线程使用；SDK main 固定「一 broker 一 Worker 线程、调用线程不迁移」——RKNN 会话绑定线程，破坏它封装立刻失效。
2. **借用生命周期**：`Image` 只持 `zhulong_frame_view` 常量引用，仅在 `process` 同步调用期间有效；要留存必须深拷贝。
3. **资产布局**：模型路径与 `targets/<target>/` 由**包**决定，SDK 不猜（所以包入口必须在包根、与 `models/` 同级）。
4. **数值差异**：量化/取整/RGA 采样导致跨 target 不逐位一致，等价性只在同 target 前后版本之间成立。

**这条边界要写进 SDK 文档**：第三方只实现算法语义，不得反向依赖后端类型；一旦他们开始手工反量化或自己 sync dma-buf，说明封装漏了——应该修本仓库，而不是让每个包各写一遍。

#### 4.8.6 后端封装评估：方向对，切口选错了一层 —— **已被取代（见文首「决策变更」；`prd.md` R11/R13）**

> **⚠️ 历史设计记录（已废弃）**：本节所设计的「`InputFacts` + `Session::prepare_input` + `models/preprocess.json` 契约下沉」方案已被 2026-10-25 评审结论彻底撤销（理由见文首表格及 `prd.md` R13）。保留此节仅作为设计演进的历史对比。**现行方案明确不引入任何输入契约类型或 preprocess.json，归一化常量留在各 target 实现目录内。**

| 设计 | 为什么对 |
| :--- | :--- |
| 两个正交抽象轴 `IInferenceBackend` × `IVisionBackend` | 与真实差异维度一致（推理与视觉前处理可独立组合，`runtime_factory.cpp:33-49` 用两个宏表达） |
| `ModelAsset` / `SessionContext` 分离 | 对齐 RKNN 的物理语义：`rknn_dup_context` 进程内共享权重 + 每线程独占会话（`inference_backend.hpp:60-68`） |
| 编译期选后端，不做运行期 `dlopen` 分流 | 避免一条 SONAME/符号级 ABI 债，和本任务的黑盒目标同向 |
| `LetterboxParams` 做成 in-out，几何由 `letterbox_geometry.hpp` 单一来源 | 把「硬件可能改变几何」这件事变成显式契约，而不是隐式假设 |

**问题不在封装本身，在切口位置**：现在的接缝沿着**厂商 API 边界**切（`Ort::Session` vs `rknn_context`），而真正会造成分叉的差异发生在**调用前后**——输入契约、归一化、内存种类、输出量化。它们没有被接缝覆盖，于是全部漏进算法：

| 泄漏的差异 | 在算法侧的重复次数 | 证据 |
| :--- | :--- | :--- |
| 「当前是不是 ONNX 后端」 | `is_onnx_` 共 **18** 处（含 setter/getter） | `grep -rn is_onnx_ packages/*/src` |
| 归一化约定（`/255`、`(x-127.5)/128`、`(x-127.5)/127.5`） | **14** 处常量 | `grep -rn '255.0f\|127.5f' packages/*/src` |
| dma-buf 零拷贝快路 | **3** 处，且条件是 `!is_onnx_` | `detector.cpp:56,318`、`grep -rn dma_buf_fd packages/*/src` |

**判定规则（避免两种极端）**：同一差异在算法侧重复 ≥3 次 → 该进接口；只出现 1 次 → 不要抽象（否则就是为想象的需求写框架）。按这条规则，上面三项都已越线，`want_float=1`（只出现在后端内部 1 处）不该动。

**先划一条阈值线：什么该封、什么不该封**（这条比具体接口更重要）

| 差异的性质 | 处置 | 本次适用 |
| :--- | :--- | :--- |
| 同一段逻辑在算法侧重复 ≥3 次 | **去重**（数据化或抽共享函数），不必新增接口 | `is_onnx_` 18 处、归一化常量 14 处、dma-buf 3 处 → 去重 |
| 差异只影响**参数**（dtype / layout / 常量 / 色序） | 用一个 POD 表达，**不加虚函数、不加框架** | `InputFacts`（后端可查）+ `preprocess.json`（每模型） |
| 差异影响**调用序列或资源所有权**（加载、会话、同步、内存） | 才值得加接口/虚函数 | 已有 `IInferenceBackend` / `IVisionBackend`，正确且够用 |
| 只为「将来可能有第三个后端」而加的字段/钩子 | **不加** | `row_alignment`（零使用）、`OutputSpec`（0 处）、能力协商框架 → 全部删掉/不做 |

所以这次的改动性质是：**1 个 POD（去重）+ 1 个 getter + 1 个 SDK 方法**，不是新抽象层。真正被消灭的是包里的 35 处重复常量与 6 个后端判断点；包里的算法逻辑、`ops::letterbox`/`ops::nms` 等共享算子、`IInferenceBackend` 结构全部不动。

**最小升级（不重写现有结构）**：接口只加一个契约 + 一个复用槽，不加 `OutputSpec`（`Tensor` 已自描述 dtype/shape，输出侧按 rule-of-three 不该抽象）。

```cpp
// 新增 SDK 公共头 runtime/input_facts.hpp
enum class PixelLayout { HWC, CHW };

// 后端只能报告「运行时查得到的事实」，且**与具体模型绑定**
struct InputFacts {
    DataType               dtype;        // rknn_query / ort 查询结果
    PixelLayout            layout;
    uint32_t               width, height;// 模型输入尺寸
    bool                   accepts_uint8;// 能否直吃 uint8（RKNN pass_through 可行；ONNX 取决于图首节点）
    bool                   prefer_dma_buf;
};

struct ModelAsset {
    // …既有字段
    InputFacts facts;                    // 由后端在 load_model 时填好
};

class IInferenceBackend {
    virtual std::shared_ptr<ModelAsset> load_model(const std::string& path_or_config) = 0;  // 填 facts
};

class Session {
    // 按**模型名**准备输入：facts 来自该模型的 asset，归一化参数来自该模型的 preprocess 描述
    const Tensor* prepare_input(const char* model, const Image& image, LetterboxParams& params);
};
```

**归一化（mean/scale）不能放后端，必须跟模型走**

你指出的是关键：转换时 `mean/std` 的填法本来就有很多种，且同一个包里就能不同：

| 常见组合 | 转换时 `mean/std` | 图内是否已归一化 | 应用侧应做什么 |
| :--- | :--- | :--- | :--- |
| ImageNet 系 | `[123.675,116.28,103.53] / [58.395,57.12,57.375]` | 否 | 无（转换已做） |
| `/255` | `[0,0,0] / [255,255,255]` | 否 | 无 |
| SCRFD 系 | `[127.5]*3 / [128]*3` | 否 | 无 |
| ArcFace 系（112） | `[127.5]*3 / [127.5]*3` | 否 | 无 |
| 导出时已带 `Sub`/`Div` 前缀 | **必须留空** | 是 | 无（填了就**双重归一化**） |
| 什么都不做（原始 0–255） | 留空 | 否 | SDK 自己归一化（当前 ONNX 路径） |

而现实证据是：`face_recognition` 一个包里就有 3 个模型、至少两套归一化——det 640 用 `(x-127.5)/128`、extractor 112 用 `(x-127.5)/127.5`（`detector.cpp:105-106`、`extractor.cpp:26-27`）。所以「后端级一个 mean/scale」在结构上就装不下。

正确的归属：**归一化是「模型资产」的属性，不是后端的属性**。后端的 `rknn_query` 只能给出张量 dtype/layout/量化参数，**永远给不出转换时的 mean/std**（那是 provenance，不是 runtime 事实）。

因此加一份随模型走的结构化描述：

```json
// targets/<target>/models/preprocess.json
{
  "models": [
    {"file": "det_500m.rknn",   "input": {"size": 640, "order": "RGB"},
     "normalization": {"applied": "converter", "mean": [127.5,127.5,127.5], "scale": [0.0078125,0.0078125,0.0078125]}},
    {"file": "facelivtv2-l.rknn","input": {"size": 112, "order": "RGB"},
     "normalization": {"applied": "converter", "mean": [127.5,127.5,127.5], "scale": [0.007843137,0.007843137,0.007843137]}}
  ]
}
```

`applied` 三态，直接决定 SDK 行为：

| `applied` | 语义 | SDK 侧 |
| :--- | :--- | :--- |
| `converter` | 转换时填了 mean/std | 只 letterbox，送 uint8 raw（零转换） |
| `graph` | ONNX 图内已有 Sub/Div | 同上（ONNX 仍需 cast+transpose） |
| `none` | 都没做 | SDK 按 `mean/scale` 自己做（当前行为，正确但多一次转换） |

**双重归一化机检（打包期拒绝）**：`applied=converter` 且 ONNX 图内存在 `Sub`/`Div` 前导节点 → 拒绝出包；`applied=none` 但声明了非 0 mean → 警告并要求显式确认。ONNX 图检测用 `onnx` 包直接读 initializer/前 3 个节点即可（离线、无网络）。

四处优化（相对上一版草案）：

1. **`mean + scale` 而不是 `mean + std`**：`(p - mean) * scale` 一个乘加，直接吃掉现有的 `/255`、`/128`、`/127.5` 三种写法，少一次除法，也不必让后端分别声明 std 语义。
2. **`order` 与归一化一起进「每模型描述」，`row_alignment` 暂不进**：色序与 mean/scale 同源（都来自转换配置），天然属于模型资产；`row_alignment` 当前**零使用**（零拷贝未落地），按阈值规则属投机字段，等真做时再加。
3. **复用槽而不是每帧新建 Tensor**：`prepare_input` 返回 `Session` 内部的复用缓冲，直接消掉当前 `plate_detector.cpp:100-112` 每帧 `640×640×3×4B ≈ 4.9MB` 的分配 + HWC→CHW 重排（该注释本身就写了「每帧分配 ~4.9MB」）。ONNX 侧仍需转换，但缓冲复用后只剩转换本身的成本。
4. **不引入 `OutputSpec`/`caps()` 两个额外入口**：dma-buf 意愿并进 `InputFacts.prefer_dma_buf`（后端说「我希望」，SDK 说「我能」——不能就退回 host 内存并打一条 debug 日志），少一个需要保持同步的方法。

迁移映射（6 个站点，逐条替换）：

| 现位置 | 现在 | 改为 |
| :--- | :--- | :--- |
| `plate_detector.cpp:92-112` | `is_onnx_` → 手建 float NCHW + `/255` | `auto* in = session.prepare_input(image, params)` |
| `face/detector.cpp:93-110` | `is_onnx_` → `(x-127.5)/128` NCHW | 同上 |
| `face/detector.cpp:354-370` | 同上（第二处检测） | 同上 |
| `face/extractor.cpp:16-30` | `is_onnx_` → `(x-127.5)/127.5` CHW | 同上（`params.target_* = 112`） |
| `face/detector.cpp:56,318`、`plate:…` | `!is_onnx_ && dma_buf_fd>=0` 快路 | 由 `InputFacts.prefer_dma_buf` 内部决定，算法不再判断 |
| 各头文件 | `set_is_onnx()` 暴露到包外 | 删除，未使用即编译失败（强制清理干净） |

验收（可机检）：

```bash
grep -rn 'is_onnx_\|255.0f\|127.5f\|dma_buf_fd' packages/*/src     # 期望：空
# 复用槽证明：连续两次 prepare_input 返回同一指针（单测断言）
# ONNX 侧：同 sample.jpg 输出与阶段 0.3 基线一致（count/class_id/score 容差内）
# 内存：每帧不再出现 4.9MB 级分配（单测用分配计数或 perf 采样）
```

输出侧顺带收口：`want_float=1` 保持不动（rule-of-three 未越线），但 `Tensor::data<T>()` 的 dtype 校验（阶段 1.1c）保证「假设 float32」一旦不成立就快速失败，而不是读垃圾。

**后端数 M 会增长时的三条加固**（前提：包数 N 与后端数 M 都不是 2，而是会持续增加）

1. **事实与描述只由 SDK/后端/打包工具产生，包只读**。硬规则：`InputFacts` 与 `preprocess.json` 不得出现在 `packages/*/src` 的构造里。这样以后追加字段（带默认值）对第三方包**源码兼容**，不必再次破坏契约——这正是当年 `struct_size` 想解决的问题，只不过这次在源码层提前规避。
2. **字段名按「能力」而不是「当前实现」命名**：所以叫 `preprocess_in_model` 而不是 `normalization_in_model`。它同时覆盖 RKNN 图内 baked mean/std、RKNN AIPP、Ascend DVPP/AIPP 这类「硬件替你做 letterbox + 归一化」的后端；只改名字不加字段，避免新后端来了又要动公共头。
3. **接入成本必须量化**，这是判断抽象是否成立的标准，而不是审美：

| 动作 | 成本 | 说明 |
| :--- | :--- | :--- |
| 新增一个推理后端（CANN / TensorRT / OpenVINO…） | 实现 `load_model` / `create_session` / `forward` + 在 `load_model` 里填 `InputFacts` | **0 行包代码修改** |
| 新增一个视觉后端（RGA / DRM / DVPP…） | 实现 `letterbox` | **0 行包代码修改** |
| 新增一个算法包 | 写算法逻辑 + `ZHULONG_ALGO_MAIN` | **0 行后端代码** |
| 验证「包不感知后端」 | 同一个脚手架包（AC17）在 `onnx` 与 `rknn` 两个 backend 下都能 `--verify` 通过 | 交叉只编译，板端跑 |

如果哪天新增后端必须在 `packages/*/src` 里改代码，说明契约又漏了——把它当成缺陷，而不是接受。

**明确不做**：通用能力协商框架、插件式后端注册、运行期后端 `dlopen` 分流、把三方 SDK 类型暴露到接口上。

#### 4.8.5 量化与数值契约（int8 在哪里、边界为什么是 float） —— **其中 `preprocess.json` / `applied` 三态 / per-target schema 覆盖已撤销（见文首「决策变更」；`prd.md` R12）**

**int8 只存在于两层：模型资产 + 后端内部。** 边界不是「一律 float32」，而是：

| 后端 | 输出 dtype | 证据 |
| :--- | :--- | :--- |
| RKNN | 强制 `FLOAT32`（`want_float=1` + `is_prealloc=1`，反量化在 runtime 内完成，直接把 float 写进预分配 Tensor） | `rknn_inference_backend.cpp:370-374` |
| ONNX | **保留模型原始 dtype**（`map_onnx_tensor_type`）：float32 模型自然是 float32，int64 / int8 输出原样透传 | `onnx_inference_backend.cpp:292-302` |
| CPU 桩 | `FLOAT32` | `cpu_inference_backend.cpp:67` |

| 层 | 量化相关行为 | 证据 |
| :--- | :--- | :--- |
| 模型资产 | `.rknn` 由 RKNN-Toolkit2 转换时量化（int8 / hybrid），仓库已有 `det_500m.rknn`、`facelivtv2-l.rknn`、`scrfd_person_2.5g.rknn` | `packages/face_recognition/targets/rk3568/models/` |
| 后端（输入） | `pass_through = 0` + `in.type = to_rknn_tensor_type(dtype)`：算法给 float32，RKNN 按模型内 baked 的 scale/zp 做输入量化 | `rknn_inference_backend.cpp:330-342` |
| 算法侧 | 只处理浮点（当前两个包假定 float32）；归一化（`/255.0`）由算法自己做，与后端无关 | `plate_detector.cpp:107-109` |
| 资产（阈值） | 量化误差可能要求不同默认阈值 | `config.schema.json`（当前单份） |

**为什么边界是浮点而不是 int8**：后处理（YOLO 解码、NMS、IoU、余弦相似度）与阈值比较都是浮点数学；若边界给 int8，每个算法都要按输出张量维护 `scale/zp` 并写定式定点代码，**「一套代码」立即失效**，且手工反量化极易错（per-channel / per-tensor、非对称范围）。让 RKNN 用 `want_float=1` 反量化，等于把「猜 zp/scale」这件事交回给唯一知道答案的一方。门禁：`grep -rn 'zero_point\|quant\|dequant' packages/*/src` 必须为空。

**这里有一个真实的静默失败面（本任务内修）**：`Tensor::data<T>()` 明确「不校验 T 与 dtype_ 是否一致」（`tensor.hpp:163-175`），而算法侧直接 `output.data<float>()`（`plate_detector.cpp:129`）。只要某个 ONNX 模型输出 int64/int8，读到的就是垃圾位模式——不崩溃、不报错、只是结果错；静态链接后 host 侧走的正是 ONNX 后端，这个面被放大了。

处置：

1. **`Tensor::data<T>()` 加类型校验**：`dtype_` 与 `T` 不匹配时抛 `std::invalid_argument`（ABI 边界已有 `try/catch` 转状态码）。每张量一次分支，不在热循环里；这是唯一能拦住「静默读垃圾」的位置。
2. **SDK 文档写明承诺范围**：算法可以假定**浮点模型输出**为 float32；非浮点输出（整数索引等）属显式例外，算法必须按 `dtype()` 分支处理，不得静默 `reinterpret`。
3. **算法侧不逐处检查**：当前两个包继续假设 float32，这不是「依赖后端实现」，而是「SDK 对浮点输出的承诺」——承诺由 `data<T>()` 的校验兜底，违反时快速失败而不是给错结果。

结论：**float32 是「浮点输出的统一货币」，不是「所有 dtype 的强制归一」**；量化只在后端内部，整数索引输出是显式例外。

三个真实缺口（本任务内处置）：

1. **量化配置不可复现（必做）**：仓库没有任何记录说明哪个 `.rknn` 是 int8 还是 hybrid、校准集是什么、RKNN-Toolkit2 哪个版本。换工具或换校准集后精度变了无法归因，也无法解释跨 target 的阈值差异。
   → 约定：`targets/<target>/QUANTIZATION.md`（模型文件 / 精度模式 / 校准集来源与规模 / 转换工具版本 / 已知偏差），`pack_package.py` 若存在则一并打进归档；**不新增 manifest 字段**。
2. **`want_float = 1` 的性能代价（记录，不改）**：int8 输出必须换成 float，模型输出大时（如检测头 `8400×85`）内存与带宽放大 4×；换来的是零手工反量化。int8 直出属后续优化，本任务不做。
3. **per-target 阈值**：**已定（推翻本条）**——R11 改为每 target 独立实现目录后，阈值直接写在该实现的源码/`config.schema.json` 里，不需要「覆盖」机制，也不需要运行期选择。

**明确不做**：算法层手工反量化；int8 直出 fast path；量化感知训练/校准流程自动化。

**与静态链接的关系**：旧形态下 `IInferenceBackend` 的虚表贴在 `.so` 边界上，加一个虚函数就让旧插件段错误（实测，`inference_backend.hpp:44-52` 注释）；现在包与后端同一次编译，接口可以自由演进，代价是**必须按 target 重编**——这就是 §4.8 矩阵的来源。




---

## 5. 宿主侧影响面

| 文件 | 改动 | 性质 |
| :--- | :--- | :--- |
| `internal/algorunner/process.go` | 直接执行包根 `<manifest.id>`；移除 `--plugin`、通用 runner 路径和 `--max-workers` 参数 | 核心 |
| `internal/algorunner/runtime.go` | 提供包根可执行文件路径；新增单形态入口校验，安装期与运行期共用 | 核心 |
| `internal/algorunner/contracts.go` | 不增加 `ComputeCost` 或 `MaxWorkers` 字段 | 无额外改动 |
| `internal/algorunner/supervisor.go` | 启动、升级、回滚、崩溃恢复均直接执行当前算法包的可执行文件 | 核心 |
| `packages/*/scripts/build_package.py` | 可执行文件拷到**包根**（与 `models/` 同级）并置 `0755`；不产出 `plugin.so`；验包命令使用包自身入口 | 核心 |
| `internal/algorunner/orphan_linux.go` | 以本实例 `AlgorithmsDir` 下的真实 `/proc/<pid>/exe` 识别孤儿，不依赖统一 runner 名 | 核心 |
| `internal/algorithm/module.go` | 将 `algorithmsDir` 传给孤儿清扫逻辑 | 小 |
| `internal/algorithm/verifier.go` | 校验唯一可执行入口、拒绝旧 `plugin.so`、规范权限并直接执行验包；CWD 为包目录 | 核心 |
| `internal/algorithm/uploader.go` | 保持归档安全门禁；入口权限在解析 manifest 并确认普通文件后由 verifier 规范 | 小 |
| `internal/app/app.go` | 移除算法包启动对 `DefaultAlgorithmRunnerPath` 的依赖 | 核心 |
| `internal/algorunner/orphan_linux_test.go` | 新增/调整可执行包孤儿认领用例 | 门禁 |
| `internal/algorunner/runtime.go` | 除 `ResolveEntry` 外，`EntryFileName = "plugin.so"` 常量与 `VerifyRunnerPath` 字段的语义一并收敛（验包改为执行包自身入口，不再有独立的 verify 二进制） | 核心 |
| `native/src/abi/engine.cpp` | 删除 `FacePluginLoader` 与 `Zhulong_engine_extract_face_feature`（约 250 行），宿主不再 `dlopen` 任何算法代码 | **净删除** |
| `internal/engine` | 删除 `ExtractFaceFeature` 与对应测试，`internal/engine` 只保留媒体/解码职责 | 核心 |
| `internal/person` + `internal/app/adapters.go` | 注入「子进程特征提取」实现替掉 engine 直连；错误码映射与现状逐条一致 | 核心 |
| `internal/app/config.go` + `config.toml` | 新增 `[person].extractor_algorithm`（默认 `face_recognition`） | 小 |
| `native/tests/check_plugin_symbols.py` | face 插件白名单回退为 `zhulong_plugin_get_api`；新增 exe 形态「`nm -D --defined-only` 为空」断言 | 门禁 |
| `native/CMakeLists.txt` | 删除 `FaceRecognitionPlugin` / `PlateRecognitionPlugin` 的 target、include、link 段落；新增 `ZhulongAlgoSdkStatic` 与 install/export 规则（§4.5） | 核心 |
| `packages/*/<target>/CMakeLists.txt` | **新增**：只 `find_package(ZhulongAlgoSDK)`，产出 `<manifest.id>` 可执行文件 + `_devtest`（`EXCLUDE_FROM_ALL`） | 核心 |
| `packages/*/<target>/tests/` | **新增**：从 `native/tests/` 迁入包级用例，改写成 dev-only `dev_infer.cpp`（读 `sample.jpg`、画框、写 `result.jpg`）；不进归档 | 中 |
| `packages/<id>/<target>/Makefile` | **新增**：包自己的 `make` / `make test` / `make package` / `make verify`（与 SDK 模板仅差 `SDK_PREFIX` 默认值） | 核心 |
| `native/sdk/tools/new_package.py` + `native/sdk/templates/**` | **新增**：脚手架与模板，生成开箱可编译/可验包/可打包的骨架（§4.7） | 核心 |
| `packages/*/scripts/build_package.py` | 改为 **10 行薄壳**，转发到 `native/sdk/tools/pack_package.py`（保留 Go 测试调用路径）；删除 staging/验包/归档实现与 `cmake --build` 调用 | 核心 |
| `native/sdk/tools/pack_package.py` | **新增**：唯一打包实现（staging、入口命名、`0755`、验包、归档命名），随 SDK 分发 | 核心 |
| `Makefile` | 新增 `sdk-install` / `packages-build` / `packages-test` / `packages-package`；`check` 顺序加入包构建与包测试；交叉 profile 同时驱动两者，并自动给打包传 `--no-verify`（§4.6） | 核心 |

---

## 6. 孤儿认亲新判据（关键设计）

### 6.1 现状

`findOrphanCandidates(runnerBase)`（`orphan_linux.go:201`）**先**按 `filepath.Base(argv[0]) != runnerBase` 过滤，**再**解析 `--broker` / `--algorithm-id` 到 `orphanCandidate`（`:221-241`）。也就是说判据是「二进制名」，而解析出的 `brokerSocks` 只用于后续回收资源、不参与认亲。`parentIsReaper`（`:272`）是第二道闸，只回答「它是否已无主」，回答不了「它是不是本实例的」。

### 6.2 新判据：本实例算法安装根下的真实可执行文件

```
候选 = 满足全部：
  1. pid 不是自己
  2. argv 可读且非空
  3. /proc/<pid>/exe 解析后严格落在 <dataDir>/algorithms/ 之下
  4. parentIsReaper(pid) == true  ← 必要条件，活宿主监督的进程永不触碰
```

**为什么不用 broker socket 目录认亲（原第 3 条，已废弃）**：实测 `brokerResourceDirs()` 返回 `[ZHULONG_FRAME_BROKER_SOCK 或 /tmp, /dev/shm]`（`resource_lock_linux.go:73-77`），socket 名为 `<base>/zhulong_broker_<stream_id>.sock`（`frame_broker.cpp:58`）——**全局、无 per-instance 隔离**。该判据对任意实例恒真，等价于「argv 里有 `--broker /tmp/...`」，零区分力。

**为什么用 `exe` 而不是 `argv[0]`**：`argv[0]` 可被 `exec -a` 任意伪造，`/proc/<pid>/exe` 是内核记录的真身。但 argv 仍要读——`--broker` / `--algorithm-id` 是后续 unlink socket/shm 的输入。

要点：

- `AlgorithmsDir = filepath.Join(dataDir, "algorithms")` 源自本实例数据目录，另一实例的包不在其中。
- `/proc/<pid>/exe` 是内核记录的实际可执行文件路径，避免依赖可伪造的 `argv[0]`。
- 路径比较前对 `AlgorithmsDir` 做 `filepath.Abs` + `filepath.EvalSymlinks`，再用 `filepath.Rel` + `escapesInstallRoot` 判严格后代。
- 对 `/proc/<pid>/exe` 的 ` (deleted)` 后缀先做处理；`EACCES` / `ENOENT` 静默跳过。
- 绝不使用「父进程不是我」式反向判定。

### 6.3 需要 AlgorithmsDir 作为入参

`ReapOrphanRunners(ctx, algorithmsDir, logger)` 以本实例算法安装目录作为可执行文件认领边界；调用侧 `internal/algorithm/module.go` 传入 `algorunner.Runtime.AlgorithmsDir`，`algorunner` 不反向 import `internal/algorithm`。

### 6.4 可选加固：实例标签（默认不实现）

`ProcessConfig.ExtraEnv`（`process.go:71`）已存在。spawn 时注入 `ZHULONG_ALGO_INSTANCE=<instance-id>`，清扫时读 `/proc/<pid>/environ` 作第二判据，可免疫包删除、路径搬迁与符号链接。代价：`environ` 需整读（上限 128KB），且老包没有该变量。仅当 `exe` 路径判据在部署环境出现假阴性时启用。

---

## 7. 升级与回滚

| 场景 | 行为 |
| :--- | :--- |
| 包根只有 `<manifest.id>` 可执行文件 | 安装并直接启动 |
| 旧 `.so` 包或包根仍含 `plugin.so` | 安装期拒绝；升级宿主前须先重建并重新安装算法包 |
| 新可执行包启动失败 | 不自动回退到 `.so`；报告失败并回滚到上一版可执行包，或将需回退的旧版本重建为可执行形态后重新安装 |
| 回退宿主代码 | 必须同时确认回退后的宿主支持当前算法包协议；不承诺旧宿主可启动新包 |
| 算法包升级 / 回滚（含特征提取进程） | 特征提取热进程必须 retire 并重建（§11.4 第 5 条）；否则底库特征与流式识别特征不同源 |

本任务不提供双形态探测、自动 fallback 或包根改名回退。

---

## 8. 风险与缓解

| 风险 | 触发条件 | 缓解 |
| :--- | :--- | :--- |
| **PIC 遗漏** | OBJECT 库未设 `POSITION_INDEPENDENT_CODE ON` | 阶段 1 就用 `ldd`/链接成功作为门禁；RK3568 (aarch64) 上 linker 报错更严格 |
| **stage token 漂移** | `algo_main.cpp` 重写循环时改了 stage 字符串 | 逐字对照 `process.go:432` allowlist；门禁 `TestStreamLogReaderAcceptsBrokerDisconnectEvent` |
| **孤儿误杀** | 认亲判据落在全局路径（如 `/tmp`）上而失去区分力 | 判据锁定本实例 `AlgorithmsDir`（§6.2）；门禁含「同二进制、不同实例的活进程不被触碰」用例 |
| **spawn 线程契约被破坏** | 改 `Start` 时误动 `LockOSThread` 结构 | 本设计**不改** spawn 逻辑；门禁 `TestRunnerProcessSpawnThreadSurvivesWhileChildRuns` 必须过 |
| **体积膨胀** | 每包 +≈2.3MB（当前 `libZhulongAlgoRuntime.so` 实测 2,314,392 字节） | 相对 NPU 权重可忽略；顺带获得包间依赖隔离 |
| **旧包二进制不受 SDK 修复影响** | 每包自带运行时 | 跨边界只有 JSON 协议；后续用 `manifest.protocol` 校验而非 SONAME |
| 旧 `.so` 包未迁移 | 新宿主拒绝安装或无法启动旧包 | 部署前重建并重新安装算法包；拒绝原因需明确可诊断 |
| **误开第二套参数通道** | 实现时顺手加 argv 配置参数 | §3.2 明确禁用 `--config-file`；参数只走 `model_create(config_json)` |
| **模型定位静默退化** | 可执行文件放在子目录，`dladdr` 指向该子目录，`models/` 找不到 | §3.1 硬约束：可执行文件必须在包根；阶段 3.4 必须比对 `count` 与基线一致（不只 `status:passed`） |
| **静态库符号外泄** | OBJECT 库未设 hidden / 无 `--exclude-libs` | 附加门禁 `nm -D <bin> \| grep -c zhulong` = 0（AC1 两条查不到） |
| **DmaBuf 双单例** | `dmabuf_allocator.cpp` 同时编进 runtime 与 ipc 两个归档 | 阶段 1.1b 收敛到 OBJECT 库唯一归属；`nm` 确认可执行文件内只有一个 `DmaBufAllocator::instance()` |
| **入口权限修复顺序错误** | 先要求 execute bit 再由 verifier chmod，导致永远无法修复 | 先用 `Lstat` 验证包内普通文件，再规范权限为 `0755`；拒绝符号链接 |
| **入口解析漂移** | 安装期和运行期各自猜测入口 | 只许用 `algorunner.ResolveEntry`（§3.5），始终解析 `<manifest.id>`；旧 `plugin.so` 明确拒绝 |
| **特征空间漂移** | 热进程复用命中的是包升级前的旧模型，表现为「识别率莫名下降」且无报错 | 缓存 key 含包目录 + exe 身份（§11.4 第 5 条）；AC14 门禁 |
| **响应错配** | 超时/崩溃后重试，把上一帧的特征写进本次登记 | 每帧 `request_id` 回显 + 单帧最多重试 1 次（§11.4 第 1-2 条） |
| **裸 exec 绕过 spawn 契约** | 从 HTTP handler 临时 goroutine 起子进程，Go 回收 M 时 Pdeathsig 误杀健康子进程 | 只允许复用 `runnerSysProcAttr()` + `LockOSThread` helper（§11.4 第 6 条） |
| 热进程内存 | 板端多常驻一份约 80MB 模型 | 空闲 60s 回收、并发上限 1（§11.6） |


---

## 9. 开放问题（实现前需确认）

1. ~~`brokerResourceDirs()` 返回的 base dir 是否按宿主实例隔离？~~ **已定（阶段 0 实测）**：返回 `[ZHULONG_FRAME_BROKER_SOCK 或 /tmp, /dev/shm]`，**全局、无实例隔离** → broker 目录认亲判据废弃，改用 `exe` ∈ `AlgorithmsDir`（§6.2）。
2. ~~`--config-file` 是否必要？~~ **已定：不引入**，理由见 §10。

3. RKNN target 是否在本任务范围内一起迁移？建议**先只做 `onnx` 档位**（`plate_recognition/targets/onnx`；原名 `cpu`，已按 F25 改名），RKNN 迁移作为后续验证项 —— 交叉编译无法在开发机执行测试（AGENTS.md 明确「交叉只允许编译，不允许执行测试」）。

4. **已安装旧 `.so` 包的升级处置 —— 已拍板正式决策（T5）**：R5/AC4 覆盖安装期拒绝；针对升级宿主时数据目录已有的存量旧包：
   - **正式决策**：supervisor 启动前用 `ResolveEntry` 校验每个已启用包，失败则将该包置为 `unavailable`、上报稳定错误码 `ALGO_ENTRY_INVALID`（携带包 id、版本及解析出的入口路径），并**进入「不自动重试」状态**，由运维/用户在前端触发「重建 / 重新安装」；同时记录一条一次性 WARN 日志明确列出受影响的包。
   - **硬性禁止**：不得做成「启动失败 → 退避重试」进入每 5 秒一轮的重试风暴。

---

## 10. 显式非目标：算法参数下发（已知缺口，本任务不修）

### 10.1 现状事实

| 事实 | 证据 |
| :--- | :--- |
| `model_create(config_json)` 语义为「权重路径、阈值等」，是**包级**初始化参数 | `native/sdk/include/zhulong/plugin.h:264-271` |
| runner 两组调用点均**恒传** `"{}"` | `native/src/algorithm/runner/runner.cpp:479`（验包）、`:630`（正式） |
| `process()` **没有参数位置**，帧级参数通道不存在 | `plugin.h:324` |
| `task_open(worker, task_id, out_task)` **没有 config 入参**，任务级参数通道**不存在** | `plugin.h:305` |
| 用户配置存在于 `task_algorithms.config_json`，只被 JSON Schema 校验消费，**无任何代码下发到算法包** | `internal/task/model.go:53`、`internal/task/store.go:266` |

### 10.2 层级错配（关键）

`model_create` 是**包级**（一个算法包进程一次），而 `task_algorithms.config_json` 是**任务级**（相机 × 算法）。二者层级不同，因此：

- 即使把 `config_json` 接到 `model_create`，多任务共用一个算法包时也**无法表达各自不同的阈值**；
- 任务级参数的正确载体是 `task_open`，而它在 ABI v1 里没有 config 位置；
- 既有先例：ROI/MASK 区域策略由宿主侧统一执行（`10-18-roi-mask-region-policy`），算法包零改动，正是因为任务级语义**无处可下**。

### 10.3 本任务的处置

- **不**引入 `--config-file` 或任何 argv 配置通道。
- **不**修改 `model_create("{}")` 的调用值，保持与现状逐字一致 —— 本任务只换交付形态，行为等价是 AC3 的前提。
- **不**给 `task_open` 加参数（那是一次 ABI 破坏性变更，需要 `plugin.h` v2 或 `struct_size` 尾追加，独立评审）。

后续若要打通参数下发，应作为独立任务，并先回答：任务级参数走 `task_open` 尾追加（ABI v1 兼容）还是 `zhulong_plugin_api_v2`（干净但需两版并存）。

### 10.4 更严重的后果：参数变更只能靠**全进程重启**

| 事实 | 证据 |
| :--- | :--- |
| 任务变更触发的是**进程级重启**，不是参数更新 | `internal/algorithm/service.go:349-351`：`scheduleAlgorithmRestart` 注释「对该算法包执行 **StopAlgorithm + 重新解析 + StartAlgorithm**」 |
| `model_create` 在运行期**只调一次**，无重调路径 | `native/src/algorithm/runner/runner.cpp:630`（`run_ipc_workers` 启动路径） |
| 参数语义未分层：`plugin.h:267` 把「权重路径、阈值等」写入同一句 | `native/sdk/include/zhulong/plugin.h:264-271` |

推论：用户改一个阈值 → kill 算法包进程 → 重拉 → **重新加载权重**（ONNX/RKNN 数百 ms 至数秒）→ **该算法所有订阅通道的 AI 同时中断**。

### 10.5 正确的分层（后续任务的目标形态，本任务只留结构）

参数必须按**变更代价**分层，而不是全塞进 `model_create`：

| 变更内容 | 应走的机制 | 中断时长 |
| :--- | :--- | :--- |
| 任务级（阈值 / top_k / 类别过滤 / NMS IoU） | **broker 控制消息 → `task_update(task, json)`** | 0（下一帧生效） |
| 同一后端换模型文件 | 重建 session（不断进程） | 单通道，百 ms 级 |
| 换后端 / 换输入尺寸 | 现状 `ScheduleAlgorithmRestart` | 全算法包，秒级 |

关键设计点：

1. **复用现有控制消息通道**。`native/src/algorithm/ipc/ipc_protocol.hpp:48-51` 已有 `MSG_GALLERY_SYNC/UPSERT/DELETE/ACK` 这类 Broker→Client 控制消息；新增一个 `MSG_TASK_CONFIG` 即可，不需要新协议机制。
2. **由 worker 线程自己应用**，在取到下一帧之前 `task_update(...)` —— 因为是线程内串行，插件**不需要任何锁**。若由宿主从外部线程调用，`process` 与 `task_update` 会并发，逼插件加锁，属于错误设计。
3. **ABI 只需尾部追加**：`zhulong_plugin_api_v1` 末尾加 `zhulong_status_t (*task_update)(zhulong_task_ctx_h, const char* config_json)`，靠现有 `struct_size` 校验实现向后兼容，已发布字段一律不动。

### 10.6 C++ 门面丢失 `task`：比 config 更前置的缺陷

| 事实 | 证据 |
| :--- | :--- |
| C ABI `process(task, frame, out)` 里 `task` 存在 | `native/sdk/include/zhulong/plugin.h:326` |
| 但门面适配器只把 `session` + `image` 交给插件，**`task` 被吞掉** | `native/sdk/include/zhulong/algo.hpp:407`（`return plugin.detect(session, image);`） |
| `TaskContext` **只有 `task_id`**，无任何 config | `algo.hpp:768-772` |
| `result->task_id` 是 SDK 替插件填的，插件自己看不见 | `algo.hpp:853` |

推论：C ABI 的 `process(task, ...)` 带有 task 身份，但当前 SDK 门面没有把它传给算法类。由于调度固定为「一 broker 一 Worker」，这不是实现多路复用的前置条件；仍应在 SDK 首次发布前补齐公开门面，避免把丢失上下文的签名固化成第三方源码契约。当前每个 Worker 持有一路流对应的 `TaskContext`，无需 `unordered_map<stream_id, TaskContext>`，也不改变一 broker 一线程调度。

### 10.7 正确形态：`TaskContext` 同时携带 task 与 config

```cpp
// 插件作者实现
DetectionResult detect(Session&, const Image&, TaskContext&);

namespace zhulong {
struct TaskConfig {};  // 本任务不定义具体配置字段
struct TaskContext {
    uint64_t task_id{0};
    TaskConfig params{};  // 本任务保持默认值，后续控制消息任务再填充
};
} // namespace zhulong
```

热路径成本：`params` 是 `task_ctx` 的成员，`detect` 取引用——**零拷贝、零 JSON 解析**。每帧开销仅一个引用传递。

三层代价区分（关键）：

| 层 | 能否在首发后修改 | 理由 |
| :--- | :--- | :--- |
| C++ 门面 `detect(...)` | **否，一经发布就是源码级契约** | header-only，第三方源码需重编译；参数和类型名称须稳定 |
| `TaskContext` / `TaskConfig` 公共字段 | **按公开 C++ API 管理** | 不跨 Go/C ABI，但静态 SDK 与算法代码需使用一致头文件并重建 |
| C ABI `process(...)` | 尾追加可，改已有字段不可 | `struct_size` 保护 |

### 10.8 本任务对本问题的硬约束（必须遵守）

- 黑盒化**不使**热更新变差：静态链接可执行文件的启动成本仍以权重加载为主；本任务只切换交付与进程边界，不实现任务参数热更新。
- 但 `algo_main.cpp` 的 worker 循环**必须在结构上留出帧边界处理控制消息的位置**：
  ```cpp
  while (client.next_frame(&frame)) {
      drain_control_messages(task_context);  // 本任务为空实现（MSG_TASK_CONFIG 尚未定义）
      auto result = algo.process(session, frame, task_context);
      client.publish(result);
  }
  ```
  理由：若写成纯 `while(next_frame) process()`，后续加 `task_update` 必须重构循环；现在留一个空实现成本几乎为零。此约束已写入 `implement.md` 阶段 3.1。

### 10.9 已定（方案 A）：`TaskContext` 修复纳入本任务

**决策：A。** 黑盒化的核心交付物是 SDK 分发物；若 SDK 发布时 `detect` 签名里没有 task/config，后续修改就是破坏第三方源码契约——与我们要消除的 ABI 债同类，只是从二进制债变源码债。现在第三方为零、两个包在仓库内，是本任务内修复的最低成本窗口。

落地改动清单：

1. `native/sdk/include/zhulong/algo.hpp`：
   - `TaskContext` 是 `zhulong` namespace 下的 SDK 公共类型，包含 `task_id` 与 `TaskConfig params{}`；本任务将 `TaskConfig` 定义为空的值类型，不预设业务字段、不从控制消息填充。
   - **命名冲突须同时处理**：`detail::PluginApi::TaskContext`（`algo.hpp:768-772`，含 `worker` + `task_id`）与新的公共 `zhulong::TaskContext` 重名，实现时把 detail 层的改名为 `detail::TaskHandle`，否则适配器内部的 `reinterpret_cast<TaskContext*>` 会静默指向错误类型。
   - 所有 `ResultAdapter::infer(plugin, session, image, task_context)` 加 `TaskContext&`；
   - `detect` / `classify` / `detect_persons` / `detect_vehicles` 门面签名同步加 `TaskContext&`。
2. `packages/face_recognition/src/*`、`packages/plate_recognition/src/*`：入口类同步改签名，**只加参数不读取**，保证输出与改造前一致（AC3）。
3. `algo_main.cpp`：每个 Worker 创建并持有一路流对应的 `TaskContext`；按帧边界调用 `drain_control_messages(TaskContext&)`，当前为空实现。不得为当前一对一调度引入 `stream_id → TaskContext` 映射表。

验收口径：AC3 由「代码不变」改为「同参数下输出一致」——允许入口签名变更，不允许 `count` / `class_id` / `text` / `score` 语义变化。

后续（不在本任务）：`MSG_TASK_CONFIG` 控制消息填充 `TaskContext::params`；在此之前 `params` 始终为默认值。

---

## 11. 宿主侧人脸特征提取通道（替代 engine `dlopen`）

> 需求见 R10，验收见 AC9-AC15。本章回答两件事：为什么不能在旧通道上打补丁，以及新通道的协议与生命周期归属。

### 11.1 为什么必须在这里改

`native/src/abi/engine.cpp` 的 `FacePluginLoader` 是**第二个**算法代码消费者（F16）：`dlopen` `libFaceRecognitionPlugin.so` → `dlsym("zhulong_face_extract_feature")` → 单次人脸特征提取，供 `internal/person` 的底库登记（`POST /persons/:id/faces`）使用。

两条硬事实决定了它无法在新形态下保留：

1. **包里不再有可 `dlopen` 的产物**（R1）。`dlopen` 一个 PIE 可执行文件会被 glibc 直接拒绝（`cannot dynamically load position-independent executable`），所以「让 exe 再导出一次 `zhulong_face_extract_feature` 给宿主 `dlsym`」不成立。
2. 这条通道不是可选的：`internal/person/service.go:606` 是底库特征向量的**唯一**来源，删了不改就是「登记 100% 失败」。

顺带收益：改完后宿主进程不再加载任何算法二进制，`.trellis/spec/native/cgo-contract.md §6` 那条「跨 `.so` C ABI 例外」可以整体删除，`check_plugin_symbols.py` 的 face 白名单也回到单符号。

### 11.2 形态：包可执行文件的 `--extract-feature`

```
<pkg>/<manifest.id> --extract-feature          # 不接 --broker
```

stdin 是**帧流**（小端，可重复至 EOF）：

| offset | size | 字段 | 约束 |
| :--- | :--- | :--- | :--- |
| 0 | u32 | `magic` | ASCII `"ZLFE"`（小端 u32 读作 `0x45464C5A`） |
| 4 | u32 | `request_id` | 宿主生成，响应必须回显 |
| 8 | u32 | `width` | > 0 |
| 12 | u32 | `height` | > 0 |
| 16 | u32 | `stride` | ≥ `width*3`（字节） |
| 20 | u32 | `format` | `1=BGR24` / `2=RGB24`，与 `engine.cpp:820` 同值 |
| 24 | u32 | `payload_len` | 必须等于 `stride*height` |
| 28 | u8[] | `payload` | 裸像元，无编码 |

stdout 每帧一行 JSON（成功与失败**都在 stdout**，避免双通道解析歧义）：

```json
{"request_id":7,"status":"passed","feature_dim":512,"features":[...]}
{"request_id":7,"status":"failed","code":"NO_FACE_DETECTED","error":"..."}
```

- 日志一律走 stderr（`ZL_LOGI` 已固定为 stderr，`log.cpp:158`）；stdout 只能有协议行。
  注：`--verify` 的失败行历史上走 stderr（`write_runner_stderr`），`--extract-feature` 是新通道，统一走 stdout，不要照搬。
- 退出码：`0` = stdin EOF 正常结束；`2` = argv 错误；`5` = 进程级不可用（符号缺失 / 模型不可用）。
- **帧级错误（无脸、输入非法）不退出进程**——否则一张坏图会毁掉热进程。
- 一次进程处理 N 帧是本协议的内置能力：单张登记 = 发 1 帧后关 stdin；后续若有批量导入，不需要改协议。

> 包侧不需要图像解码器：像素在 Go 侧已由 `decodeToRGB24` 展平（F18），所以 `libjpeg` / `libwebp` / `libpng` 不进包二进制。

> **这是 SDK 公开契约，不是宿主私有约定。** 宿主依赖它做底库登记，所以它必须随 SDK 分发：`abi.json` 声明 `extract_feature` 能力（可选实现）、`tools/algo_probe.py` 提供帧流自查、README 写清「实现 `zhulong_face_extract_feature` 才能被宿主用于底库登记」。把它写进仓库侧的 `native/tests/` 是错误归属：那样第三方拿不到工具，而仓库又多一处协议知识。

### 11.3 谁提供能力：弱符号，不扩张公共门面

```cpp
// native/sdk/src/algo_main.cpp
extern "C" __attribute__((weak)) zhulong_status_t zhulong_face_extract_feature(
    const uint8_t*, int32_t, int32_t, int32_t, int32_t, float*, uint32_t*);

int run_extract_feature() {
    FrameHeader h{};
    while (read_frame_header(std::cin, h)) {
        auto pixels = read_payload(std::cin, h);
        if (&zhulong_face_extract_feature == nullptr) {   // 弱引用未解析
            emit_failure(h.request_id, "UNSUPPORTED");
            return 5;
        }
        float features[512];
        uint32_t dim = sizeof(features) / sizeof(features[0]);
        zhulong_status_t rc = zhulong_face_extract_feature(
            pixels.data(), h.width, h.height, h.stride, h.format, features, &dim);
        emit_result(h.request_id, rc, features, dim);
    }
    return 0;
}
```

要点：

- `face_recognition` 已有 `zhulong_face_extract_feature`（`face_recognition.cpp:570`），静态链入后弱引用自然解析；`plate_recognition` 不提供 → `UNSUPPORTED`，不是构建错误。
- **不新增公共门面**：`detect` / `classify` / `detect_persons` / `detect_vehicles`、四种 `ResultAdapter`、`plugin.h` 全部不动（与 §10.9 的 TaskContext 改动互不干扰）。
- **弱符号必须由包自身对象文件提供（T1，2026-10-25 补充）**：GNU ld **不会**为满足「弱未定义引用」去抽取静态归档成员。因此 `zhulong_face_extract_feature` 的定义必须位于 `add_executable(<id> ...)` 直接编译的源码里（今天 `packages/face_recognition/src/face_recognition.cpp:570` 正是如此）；若某天把该定义移进 `libZhulongAlgoRuntime.a`，face 包会**静默**报 `UNSUPPORTED`（exit 5）——表现为「底库登记功能消失」且无错误日志。门禁：用 face 包喂 1 帧，必须**不**返回 `UNSUPPORTED`（`implement.md` 3.8）。
- **不破 AC1 第三条**：`ZHULONG_PLUGIN_EXPORT` 的 default visibility 在 PIE 可执行文件里不进 `.dynsym`（已实测：不加 `-rdynamic` 时 `nm -D | grep -c zhulong` = 0）。**禁止**在为该目标添加 `-rdynamic` / `--export-dynamic`。

### 11.4 Go 侧热进程（本方案的关键部分）

形态是「Go 侧的进程缓存」，**不是**回到常驻插件：被复用的只是一个已经启动的、包自己的可执行文件，跨边界仍然只有帧头与 JSON。

```
         ┌──────────── request ────────────┐
         ▼                                 │
      [idle] ── 空闲 60s ──▶ [retire] ──▶ (无进程)
         ▲
         └── response ◀── [running] ── 崩溃 / 超时 ──▶ [dead] ──▶ Wait() 回收
```

硬约束（逐条都有对应验收，不允许实现时偷工）：

1. **一问一答 + `request_id` 回显**：重试只能重发同一帧，禁止把响应错配给下一个请求（错配 = 把别人的特征写进底库）。
2. **崩溃即重建，单帧最多重试 1 次**：`stdin` 写失败 / 读到 EOF / 非 JSON → kill 进程组 + `Wait()` → 重建 → 重发该帧；第二次失败返回 `ErrInternal`。
3. **超时必须 kill 进程组**：`syscall.Kill(-pid, SIGKILL)` 后 `Wait()`，默认超时 30s。只 kill 单进程会留下抱着 ONNX 的孙进程。
4. **空闲回收对齐现状**：空闲 60s → `SIGTERM` → 超时兜底 `SIGKILL`；沿用 `ZHULONG_FACE_PLUGIN_IDLE_TIMEOUT_SEC`（默认 60），使内存曲线与改造前一致。
5. **缓存 key 含包身份**：`<pkgDir>` 绝对路径 + exe 的 `(dev, ino, size, mtime)`。包升级/回滚/重装后必须 retire 重建——否则底库新特征由旧模型产生，与流式识别不同源，表现为「识别率莫名下降」且无报错（正确性问题，不是性能问题）。
6. **spawn 只走现有 helper**：`runnerSysProcAttr()`（`Pdeathsig` + `Setpgid`）+ `LockOSThread`（`process.go:210` 与 `TestRunnerProcessSpawnThreadSurvivesWhileChildRuns` 已把契约钉死）。热进程活得比请求久，踩中概率比 runner 更高，禁止新写裸 `exec.Command`。

并发上限固定为 **1**：现状 `FacePluginLoader::extract()` 全程持锁，本来就是串行（`cgo-contract.md §6.3`），单进程串行是**语义等价**而非退步；排队时延由请求超时兜底，不引入无界并发 spawn。

### 11.5 三个实现陷阱

- **半写帧**：`stdin` 写失败或 `Close()` 后不能假设内核缓冲已送达；该请求必须判失败并走第 2 条重试路径。
- **stdout 污染**：任何 `std::cout` 调试输出都会破坏协议解析。SDK 必须保持「日志 → stderr，结果 → stdout」，门禁用一条「stderr 有日志时 stdout 仍可解析」的用例钉死。
- **空闲计时器与请求竞争**：取进程时重置计时器、请求期间挂起计时器，用互斥包住「取/放」，不允许事后补 `Reset()`——否则会把正在推理的进程杀掉。

### 11.6 参数与默认值

| 参数 | 默认值 | 来源 | 理由 |
| :--- | :--- | :--- | :--- |
| 并发上限 | 1 | 常量 | 与现状互斥锁串行等价 |
| 请求超时 | 30s | 常量 | 需覆盖冷启动 + 推理，远小于用户可感知的「卡死」阈值 |
| 空闲回收 | 60s | `ZHULONG_FACE_PLUGIN_IDLE_TIMEOUT_SEC`（沿用，默认 60） | 与现状 TTL 一致，内存曲线不变 |
| 单帧最大重试 | 1 | 常量 | 崩溃重建一次；第二次失败 fail-closed |
| 提供者包 | `face_recognition` | `[person].extractor_algorithm` | 显式来源，不靠相对路径撞运气（F17） |

### 11.7 被否掉的形态

| 形态 | 否决理由 |
| :--- | :--- |
| 每次提取都新建进程、不做复用 | 可行但每次登记都付一次模型加载；既然 Go 侧的「空闲回收」只需百行且不产生跨层契约，没有理由把确定性体验让给不确定性 |
| 常驻 extract 守护进程 + 自定义 IPC | 等于再造一套 broker（帧传输、背压、孤儿看护、健康检查），与本任务「减少一套跨边界机制」的目标相反 |
| 在 `engine.cpp` 内 `posix_spawn` | CGO 库内起进程要自己处理 cwd、信号、超时、日志采集，而且拿不到 `dataDir`（包路径在 Go 侧）；同一套事情 `internal/algorunner` 已经写过三遍 |
| 保留一份「宿主专用特征提取 `.so`」 | 保留一条跨 `.so` C ABI 例外，等于保留本任务要消灭的债；且在板端引入两套模型加载路径 |
| 把提取塞进 `--broker` 通道 | broker 是发布/订阅，同步请求/响应需要新控制消息与新的背压语义，属独立议题（与 §10 同类） |

### 11.8 失败语义（与 `cgo-contract.md §6.2` 逐条对齐）

| 场景 | 子进程输出 | Go 错误 | HTTP |
| :--- | :--- | :--- | :--- |
| 成功 | `status=passed`，`feature_dim=512` | `nil` | 200 |
| 无脸 / 输出无法解码 | `code=NO_FACE_DETECTED` | `ErrNoFaceDetected` | 400 `NO_FACE_DETECTED` |
| 帧头非法 / 尺寸超限 / `payload_len` 不符 | `code=INVALID_INPUT` | `ErrInvalidArgument` | 400 |
| 包未安装 / 入口缺失 / 符号缺失 | `code=UNSUPPORTED`（exit 5） | `ErrUnsupported` | 500 |
| 模型不可用（`model_create` 失败） | `code=MODEL_UNAVAILABLE`（exit 5） | `ErrUnsupported` | 500 |
| 崩溃 / 被信号杀 / 超时 / 非 JSON | 无 | `ErrInternal` | 500 `FEATURE_EXTRACT_FAILED` |
| `feature_dim` 为 0 或 > 512 | `status=passed` 但维度非法 | `ErrInternal` | 500 |

**严禁**任何失败路径写零向量（现状注释已明确：零向量与真实登记在库里不可区分，会静默永不匹配）。

### 11.9 需要同步的文档与门禁

- `.trellis/spec/native/cgo-contract.md §6`：改写为「特征提取经包可执行文件的一次性通道」；删 §6.1 的 dlopen 叙述与 §6.3 的 TTL Reaper（改为 Go 侧进程回收），保留 §6.2 的四类语义表与 §6.4 的验证边界（改测新通道）。
- `.trellis/spec/native/algo-plugin-contract.md:49`：删除「宿主直连专用 C ABI」例外（该符号不再跨 `.so` 边界）。
- `.trellis/spec/native/hardware-backends.md:258`：把「必须通过插件导出的专用 C ABI」改为指向本章。
- `native/tests/check_plugin_symbols.py`：face 白名单回退为只允许 `zhulong_plugin_get_api`；exe 形态新增「`nm -D --defined-only` 为空」断言。
- `config.toml` + `internal/app/config.go`：`[person].extractor_algorithm`。
- **SDK 分发物**（阶段 7.1）：`tools/algo_probe.py`（帧流自查：`--verify` 与 `--extract-feature` 两种模式）与 `abi.json` 里的 `extract_feature` 可选能力声明。协议验证工具归 SDK，**不得**落在 `native/tests/`。

---

## 12. 文档修订记录

> 2026-10-25 文档评审：下面每条都对应一份**已存在的仓库事实或链接期/运行期确定性行为**，不是风格偏好。按日期追加，不删除旧条目。

| # | 位置 | 原内容 | 修正后 | 依据 |
| :--- | :--- | :--- | :--- | :--- |
| D1 | `design.md` §4.1.1 第 3 条、`implement.md` 1.1 / 1.4 | OBJECT 库设 `CXX_VISIBILITY_PRESET hidden`，同时 1.4 又期望旧 plugin 仍有 9 个未定义符号 | **不设 hidden**（静态链入不需要）；符号不入 `.dynsym` 靠「可执行文件默认不导出」+ `-Wl,--exclude-libs,ALL` | `native/CMakeLists.txt:377/:406` 的 plugin 仍 link 旧 SHARED，`native/src/algorithm/runtime` 无任何导出宏 → hidden 会让 plugin 链接期 `undefined reference` |
| D2 | `prd.md` AC21 | 要求脚手架空实现包 `readelf -d` **无** onnxruntime，不区分档位 | 断言限定为 `rknn` / `test_stub` prefix；onnx 档位必然 `NEEDED` ORT，其约束是 AC18 | `algo_main` → `runtime::init()` → `runtime_factory.cpp:36` → `onnx_inference_backend.o`，静态链接下确定拉入 |
| D3 | `prd.md` R7 / AC6、`implement.md` 阶段 6 | PRD 把「底库去单例」写成硬需求，implement 又标「可选、默认跳过」 | R7 降级为「保持现状，不改代码」；AC6 改为回归门禁；阶段 6 标为**不执行** | 静态链接后 `algo_main` 与算法同二进制，`GalleryRegistry::instance()` 天然唯一 |
| D4 | `prd.md` Acceptance Criteria 顶部、`implement.md` 全局约束 | grep 门禁未区分「代码」与「注释」，`cpu_mock` 类断言今天就红 | 新增「门禁通用约定」：`--include=` + 剔除注释行；docstring 需显式豁免与上限；断言收编为单一脚本 | `native/scripts/build.py:229` 保留了解释「为何废除 `cpu_mock`」的 docstring |
| D5 | `implement.md` 0.6 | 阶段 0 基线未采集，而阶段 1.0 已提交 | 新增 0.6：基线为**补录**，须标注采集时间/HEAD/R14 已生效的差异，且必须在 3.0f 改名前采完 | `research/` 目录不存在；commit `7aeb3bb` 已改 profile 与后端判定 |
| D7 | `prd.md` 非目标表、`implement.md` 3.2 / 7.4、`design.md` §3.4 | 验包输出只报 `count`；「UI 上传图片试用算法包」未记录 | 验包输出改为 **IPC 结果载荷的超集**（复用 `serialize_inference_result`），只许追加字段，三个消费者共用一份契约；一次性推理的语义边界（无底库、无跨帧跟踪）写进契约；UI 试用记为独立任务并指向本契约 | 后续要加「上传图片测试」能力，若无明细则必然改包输出格式；而且一次性进程不连 broker，底库/跟踪本就不可用 |
| T1 | `design.md` §11.3、`implement.md` 3.8 | 弱符号方案未说明定义必须先于链接可见 | 写明「定义必须在包自身对象文件，不得只存在于 `.a`」+ face 包不得返 `UNSUPPORTED` 的门禁 | GNU ld 不为满足弱未定义引用抽取静态归档成员 |
| T2 | `design.md` §3.1 | 模型定位只靠 `dladdr` | 主路径 `readlink("/proc/self/exe")`，`dladdr` 兜底；处理 `" (deleted)"` | 不依赖动态链接器与符号地址；包升级替换后路径仍可信 |
| T3 | `implement.md` Review gate 3 | 未写本阶段 `make check` 的预期状态 | 允许短暂为红，但红项须逐条列出并对应到 3.9/4.8 的替代用例 | `native/CMakeLists.txt:523/563/651` 三个 ctest 依赖旧 plugin target |
| T4 | `implement.md` 3.0e | golden 向量只写了「存在」；后经评审**整条撤销** | 改为「每 target 自带 dev-only `tests/`：`sample.jpg` → `result.jpg` + 终端明细表；`tests/` 不进归档，可依赖图像库」。撤销理由：按 target 分叉的向量等于普通单测；不分叉则要求 f32 与 int8 同解，与 F24 矛盾 |
| D6 | `prd.md` R11 / AC3 / AC19、`implement.md` 3.0e / 3.2 / 3.4 / 3.6 / 3.9、`design.md` §4.5 / §4.7 | 跨 target 共享 golden 向量（`tests/vectors/*.json`）作为唯一共享物 | **整条撤销**：每 target 自带 dev-only `tests/`，用包根 `sample.jpg` 跑出 `result.jpg`（画框 + 打标）与终端明细表；`tests/` 不进归档、可依赖图像库；`--verify` 需输出明细（复用 `runner.cpp:115` 的序列化器） | 量化不同 → 跨 target 结果本就不同（F24）；按 target 分叉的向量等于普通单测，不分叉则要求 f32 与 int8 同解 |
| T5 | `design.md` §9.4 第 4 条、`implement.md` 4.6 | 「待确认」但 4.6 未标阻塞 | 标为**阻塞 4.6 的待拍板项**，附建议方案（`ALGO_ENTRY_INVALID` + 不自动重试） | 升级宿主时旧包绕过安装期门禁 |
| T6 | `prd.md` AC13 | 「1s 内消失」 | 改为轮询断言 ≤3s，契约（Pdeathsig + 进程组）不变 | 慢 CI 上 1s 阈值会 flaky |

**未在本轮修正、留待拍板的开放项**：`design.md` §9.4 第 4 条（T5）**已拍板**；`prd.md`「跨边界协议版本校验（`manifest.protocol`）」与「同一进程多算法包」两项已明确延后，不在本任务。
