# 算法包开发与交付规范 (Algorithm Package Specification)

> Zhulong 视觉计算平台算法包工程标准：物理目录拓扑、`manifest.json` 权威元数据、安全打包与本地黑盒验包准入门禁。

---

## 1. 算法包物理目录拓扑 (Package Topology)

### 1.1 交付归档拓扑 (Binary Package Topology)

算法包必须采用**扁平化归档**（根目录下直接包含约定文件，**严禁外层包裹随机命名的顶层文件夹**）：

```txt
<package_name>.tar.gz (或 .zip)
├── manifest.json       # [必须] 算法元数据声明与算力资源规格清单 (权威定义见 §2)
├── <manifest.id>       # [必须] 包根可执行文件 (静态链接 ZhulongAlgoSDK 与运行时，权限必须为 0755)
├── sample.jpg          # [必须] 用于准入黑盒测试的真实前向推理样本图
├── models/             # [可选] 算法只读权重模型 (.onnx, .rknn, .om, .engine 等)
├── config.schema.json  # [可选] 算法超参数配置的 JSON Schema (缺省为零参算法)
└── .env                # [可选] 算法运行时本地环境变量覆写文件 (见 §3)
```

> ⚠️ **历史废弃项**：交付形态已由动态库装载升级为独立进程可执行文件。归档内必须直接提供以 `manifest.id` 同名的二进制可执行文件，**归档内严禁再有 `plugin.so`**（检出即触发 `ALGO_ENTRY_INVALID` 准入拦截）。

### 1.2 源码工程与 Stage 目录规范 (Source & Stage Architecture)

对于多模型协同或带有后处理业务逻辑的复杂算法包，源码目录严禁按水平技术分层（如 `preprocess/`、`infer/`、`postprocess/`）切分，**必须按业务流水线阶段（Stage）进行垂直切片**：

```txt
packages/<id>/<target>/
├── CMakeLists.txt      # 依赖纯 SDK 前缀构建，PRIVATE/PUBLIC 暴露 src/ 根
├── Makefile            # 权威开发门禁：all, test, verify, clean
├── manifest.json       # 包权威元信息
├── config.schema.json  # 配置 schema
├── sample.jpg          # 准入黑盒样本图
├── models/             # 预置模型权重
├── tests/
│   └── dev_infer.cpp   # 本地闭环自测与各 Stage 细粒度测试
└── src/
    ├── main.cpp        # [入口] 仅调用 ZHULONG_ALGO_MAIN 绑定流水线插件与 SDK 结果适配器
    ├── common/         # [通用] 跨 Stage 共享基础类型、常量映射表、字典、色彩枚举
    ├── detection/      # [阶段 A: 检测] 封装输入前处理、检测推理、BBox 坐标映射与 NMS
    ├── recognition/    # [阶段 B: 识别] 封装 ROI 仿射变换/切图、识别推理与 Greedy/CTC 解码
    ├── format/         # [阶段 C: 校验] 纯领域规则与结构合法性校验（如车牌号牌正则与长度）
    └── pipeline/       # [流水线] 资源生命周期管理、Worker Session 绑定、跨 Stage 级联与 SDK 结果组装
```

**Stage 垂直切片的核心设计原则**：
1. **防止散弹式修改 (Avoid Shotgun Surgery)**：模型调整（如升级 Detector 输入尺寸或更换 Recognizer 模型）时，改动严格局限在单一 Stage 目录内，不跨层扩散到全局的 `preprocess/`。
2. **异构前处理高内聚**：不同 Stage 可能采用不同前处理硬件（如 Stage 1 使用 RGA 硬件缩放，Stage 2 使用 CPU 仿射对齐）。将前处理与模型推理置于同一 Stage 内，能够天然隔离硬件抽象和生命周期。
3. **阶段独立可测 (Stage Testability)**：每个 Stage 拥有明确且类型安全的输入输出（例如 `recognize(image, box) -> Result`），在 `tests/dev_infer.cpp` 中可直接对其进行单阶段白盒单元测试（如特定字符映射、负步幅异常防卫、非法字符规则拦截）。

### 1.3 归档与打包铁律 (Packaging Rules)

1. **归档格式**：仅支持 `.tar.gz`（魔数 `0x1F, 0x8B`）或 `.zip`（魔数 `0x50, 0x4B, 0x03, 0x04`）。
2. **扁平根路径**：归档根目录下必须直接暴露 `manifest.json` 与 `<manifest.id>` 二进制。严禁打包为 `dist/` 或 `<package_name>/` 嵌套目录。
3. **文件权限**：二进制文件在 Linux 下必须具备可执行权限（`chmod 0755 <manifest.id>`）；Windows 打包 zip 时平台服务会在解压后自动补正为 `0755`。
4. **安全与体积硬门禁**：
   - 压缩包上传体积 ≤ **500MB**（超限触发 HTTP 413 熔断）；
   - 解压后文件总数 ≤ **1,000 个**（防御解压炸弹）；
   - 解压后展开总容量 ≤ **1GB**（防御解压炸弹）；
   - **绝对路径安全 (Zip-Slip 防护)**：归档内所有条目路径严禁包含 `../` 相对路径穿越或根绝对路径，一旦检出直接拒绝并告警。

---

## 2. `manifest.json` 元数据权威规范

算法包根目录必须提供 `manifest.json`，声明算法标识、适用平台、算力预算与任务形态：

```json
{
  "id": "safety_helmet_detector",
  "name": "安全帽佩戴检测算法",
  "version": "1.0.0",
  "description": "基于 Yolov8-Nano 的施工现场安全帽佩戴实时检测",
  "task_type": "detection",
  "category": "safety",
  "platform": {
    "arch": "x86_64",
    "target": "onnx"
  },
  "resources": {
    "compute_cost": 20,
    "memory_mb": 128
  }
}
```

### 2.1 字段权威约束

| 字段 | 类型 | 必填 | 约束与取值范围 | 说明 |
| :--- | :--- | :---: | :--- | :--- |
| `id` | string | 是 | 正则 `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$` | 算法全局唯一机器标识，用于物理路径拼接，严禁空格与路径字符 |
| `name` | string | 是 | 字符串，UTF-8 字节长度 `1 ~ 32` | 展示名称；超过 32 字节将在 IPC 握手包定长缓冲截断并被拒 |
| `version` | string | 是 | 正则 `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$` | 语义化版本号（如 `1.0.0`） |
| `description` | string | 否 | 字符串，≤ 256 字符 | 算法功能简短描述 |
| `task_type` | string | 是 | 枚举：`detection` \| `classification` \| `person_structure` \| `vehicle_structure` | 算法任务类型，决定推理结果的数据结构 |
| `category` | string | 是 | 枚举：`safety` \| `security` \| `fire` \| `traffic` \| `general` | 应用行业分类 |
| `platform.arch` | string | 是 | 枚举：`x86_64` \| `aarch64` | 目标硬件 CPU 指令集架构 |
| `platform.target` | string | 是 | 枚举：`onnx` \| `rk3568` \| `cann` \| `tensorrt` | 目标档位。主机档位是 `onnx`（ONNX Runtime）；`cpu` 已**退役**，安装期以稳定码 `PLATFORM_TARGET_CPU_RETIRED` 拒绝，不做静默映射 |
| `resources.compute_cost` | int | 是 | 整数 `1 ~ 1000` | 单帧基准算力消耗点数（全平台总预算池为 1,000 点） |
| `resources.memory_mb` | int | 是 | 整数 `≥ 16` | 预估最大常驻内存（MB） |

---

## 3. 环境变量 `.env` 配置规范

算法包根目录下可包含可选的 `.env` 配置文件，用于动态调节底层推理驱动（如 RKNN、OpenMP）与模型超参数：

```ini
OMP_NUM_THREADS=4
RKNN_CORE_MASK=all
CONFIDENCE_THRESHOLD=0.45
NMS_THRESHOLD=0.50
```

- 采用标准 `KEY=VALUE` 格式，支持 `#` 注释；
- 平台拉起算法子进程时会自动解析并将变量注入至环境变量，算法代码通过 `std::getenv()` 读取。

---

## 4. 动态库开发契约与 Native 规范索引

`plugin.so` 是遵循纯 C ABI 的共享库，由官方独立沙箱子进程（Runner）加载执行。核心开发规约如下（详细 C/C++ 契约请参阅对应原生规范，本处不重复定义）：

1. **纯 C ABI 与 SDK 门面**：
   - 必须通过 `zhulong_plugin_get_api` 导出纯 C 函数表；
   - 推荐使用 C++17 Header-only SDK（`zhulong/algo.hpp`）宏注册插件；
   - 详见 👉 [算法插件系统契约与 SDK 规范](../native/algo-plugin-contract.md)。
2. **异构硬件加速后端**：
   - RKNN 推理与 RGA 视觉前处理等特定芯片加速规范；
   - 详见 👉 [原生硬件加速后端规范](../native/hardware-backends.md)。
3. **坐标系与结果契约**：
   - 所有检测框坐标必须恒定归一化至 `[0.0, 1.0]`（`[x1, y1, x2, y2]`），严禁使用绝对像素值；
   - 未检出目标时必须返回空数组（`count = 0`），严禁返回空指针；
   - 异常绝对禁止穿越 C ABI 边界（必须全量 `try-catch` 拦截）。

---

## 5. 本地黑盒验包与自测门禁 (Verification Gate)

算法包**就是**一个自带算法类的可执行文件（`<manifest.id>`，静态链接运行时）。交付前必须在本地用**包的工程本身**做黑盒自测——命令与平台准入走的是同一个入口：

```bash
cd packages/<id>/<target>
make SDK_PREFIX=<sdk 前缀>        # 构建包根可执行文件
make verify                       # → ./build/<manifest.id> --verify --sample <ppm>（cwd = 包目录）
make test                         # 开发期自测：sample.jpg 进 → build/out/result-*.jpg 出

# SDK 前缀带来的协议自查工具（同时校验 --verify 与 --extract-feature）
python3 <sdk 前缀>/tools/algo_probe.py --entry build/<manifest.id> --pkg-dir . \
        --all --sample sample.ppm --image sample.ppm
```

`make verify` 退出码为 `0` 且输出 `{"status":"passed", ...}` 时方可通过准入校验。

⚠️ `--sample` 必须是包**能解码**的帧（netpbm `.ppm`/`.pgm`）：包骨架不链接图像解码器，`sample.jpg` 无法被它直接读取，会回退合成帧（报告里 `sample_decoded:false`）。`packages/*/scripts/build_package.py` 会自动用 `ffmpeg` 把 `sample.jpg` 转成 PPM 再验包。

### 5.1 验包报告的诚实语义（不可把「烟测」当成「验过样本」）

- 包骨架**不链接图像解码器**：它原生解码 netpbm（`P6`/`P5`）。`sample.jpg/png` 的解码责任在调用侧：平台准入（`internal/algorithm`）用 Go 标准库解码后以 PPM 交给包入口，并以**包目录**为 CWD 启动（复现「可执行文件与 `models/` 同级」的部署布局）；`packages/*/scripts/build_package.py` 在以 `staging` 目录为 CWD 调用包入口的同时，优先用 `ffmpeg` 把 `sample.jpg` 转 PPM。
- 报告字段 `sample_decoded` 决定这次「通过」到底证明了什么：
  - `true` —— 包里那张样本图真的进了模型，`count` 是该图的真实结果数；
  - `false` —— 包骨架回退到合成灰帧，**只**证明 ABI 生命周期（model_create / worker / task / process / release），平台侧会记一条告警。
- `count == 0` 不阻断安装（样本本身可能确实没有目标），但平台会告警提醒「样本图不具代表性」；**算法准确率由算法包自己的 native 金标测试担保**（如 `ZhulongPlateRecognitionPluginTests` 钉住车牌号/颜色），不依赖验包 CLI。
- `model_create` 失败时日志必带 `backend=<onnx|rknn|test_stub>`：`test_stub` 是夹具构建，按设计拒绝一切真实模型（应先换后端而不是查模型文件）；产品构建只有 `onnx` / `rknn`，不存在静默降级路径。

### 5.2 干净容器验收（「包能脱离仓库独立开发」的唯一可信证据）

开发机上「碰巧能找到仓库里的头/库」是最容易漏的假绿，所以独立开发能力必须在一个**没有仓库、
没有宿主二进制**的环境里证明：

```bash
make sdk-install BUNDLE_RUNTIME=1     # 前缀自带 onnxruntime 依赖闭包
make sdk-dist BUNDLE_RUNTIME=1        # → build/packages/zhulong-algo-sdk-<ver>-<arch>-<target>.tar.gz
make sdk-clean-check                  # 只挂前缀 + 包目录，在 debian 里从零构建两个包 + 脚手架 round-trip
```

`make sdk-dist` 打包的就是 `make sdk-install` 装出来的前缀（含 `tools/algo_probe.py`、
`tools/new_package.py`、`dev/stb`、`abi.json`），**不另建文件清单**：第二份清单必然与
`install()` 规则漂移，漂移的代价是第三方拿到的 SDK 缺头/缺 `.a`。

脚本（`native/scripts/sdk_clean_check.sh`）做五件事：

1. **仓库路径门禁**：grep 包内 `CMakeLists.txt` / `Makefile` / 源码，出现指向仓库的相对路径即失败；
2. 容器内 `rm -rf build && make SDK_PREFIX=/opt/zhulong-sdk`；
3. `make test`：`sample.jpg` 进 → `build/out/result-*.jpg` 出（真实推理，不是合成帧）；
4. **脚手架 round-trip**：`new_package.py` 生成 `scaffold_demo/onnx` → `make` → `make test` →
   `make verify`，证明第三方从零写包也走得通（SDK 缺东西会在这里红，而不是在用户那儿红）；
5. `algo_probe.py --all`：`--verify` 断言 `status=passed` 且 `sample_decoded=true`；
   `--extract-feature` 断言 512 维且 L2 范数 ≈ 1（非人脸包如实 `UNSUPPORTED` 记 `SKIPPED`）。

设备档位（`rk3568/`）的出货包要交叉构建（本机不能执行，验包留到目标设备）：

```bash
python3 packages/face_recognition/scripts/build_package.py --target rk3568 \
    --sdk-prefix <aarch64 前缀> \
    --toolchain build/native/cross-rk3568-rknn/toolchain.cmake \
    --rknn-root native/vendor/rknpu2      # 会自动跳过本机验包并给出提示
```

设备档位还有一条更硬的验收路径：`make board-check`（交叉只产 aarch64 SDK 前缀，
**包在板上用板自己的编译器构建**），细节与实测基线见
[硬件加速后端规范 §板端验证](../native/hardware-backends.md)。

⚠️ **镜像基线必须 ≥ 开发机那一代发行版**：前缀里的静态运行库与 bundled onnxruntime 是开发机
编译的二进制，符号版本不向下兼容。老镜像上报 `undefined reference to __isoc23_strtol@GLIBC_2.38`
是镜像太旧，不是包的问题。

⚠️ `BUNDLE_RUNTIME=1` 必须拷 **NEEDED 闭包**（`native/scripts/bundle_runtime.py`），只拷
`libonnxruntime.so` 会在干净容器里链接失败；导入目标的 `-Wl,--disable-new-dtags` 也不能省
（`DT_RUNPATH` 不沿依赖链继承，三层 NEEDED 只找得到第一层）。

### 5.3 模型不可用 ≠ 没检出人脸

协议层必须能区分这两件事：

| 情形 | 上报 | 宿主 HTTP |
| :--- | :--- | :--- |
| 权重缺失 / 后端初始化失败（包或环境坏了） | `ZHULONG_PLUGIN_ERR_NOT_SUPPORTED` → `MODEL_UNAVAILABLE` | 500 |
| 图里确实没有目标（输入问题） | `ZHULONG_PLUGIN_ERR_NOT_FOUND` → `NO_FACE_DETECTED` | 400 |

⚠️ 初始化失败时**不要**把后端返回的 `NOT_FOUND` 直接透传：协议会把它理解成「这张图没检出人脸」，
把包缺陷伪装成用户照片问题。`face_recognition` 的 `extract` 入口就是这么踩过一次。

### 平台准入拦截错误代码对照表

| 平台拦截错误码 | 触发原因 | 开发者整改指引 |
| :--- | :--- | :--- |
| `ZIP_SLIP_ATTACK_DETECTED` | 压缩包内含有 `../` 相对路径或绝对路径 | 重新打包，确保所有文件使用相对根目录路径 |
| `DECOMPRESSION_BOMB_DETECTED` | 文件数 > 1,000 或解压总大小 > 1GB | 精简模型与依赖库体积，移除无用中间文件 |
| `ALGO_PLATFORM_MISMATCH` | `platform.arch`/`target` 与运行宿主配置不符 | 核验交叉编译工具链与 `manifest.json` 平台声明 |
| `INVALID_MANIFEST_TOKEN` | `id` / `version` 包含非法字符，或 `name` 超过 32 字节 | 匹配正则 `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$` 并缩短 `name` |
| `ALGO_VERIFICATION_FAILED` | 包根可执行文件验包退出码非 0 | 在包目录跑 `make verify` 复现崩溃与内存问题 |
| `ALGO_ENTRY_INVALID` | 包根缺少以 `manifest.id` 命名的可执行文件（旧 `.so` 包，或归档被裁剪） | 用当前 SDK 重新构建/重新打包，归档内不得再有 `plugin.so` |

---

## 6. 控制面分层与模块单一职责 (Module Boundaries & Responsibilities)

1. **核心单一职责**：
   - `internal/algorithm`：专注且仅负责**算法包制品资产管理**（解压、Zip-Slip 与炸弹防御、元数据解析、黑盒验包准入门禁、持久化与 HTTP 控制面 API）。
   - `internal/algorunner`：专注**动态沙箱子进程看护与自愈容灾**（环境变量注入、拉起与优雅停止、指数退避、CrashLoopBackoff 熔断、RollbackGuard 自动回滚守卫及底库广播）。
     - **管道同步契约**：`RunnerProcess` 负责标准输出与标准错误流的完全 Drain 与 `failureStage` 探测，必须保证日志写完后才向外关闭 `doneCh`；
     - **锁隔离铁律**：`Supervisor` 的升级（`UpgradeAlgorithm`）与重置（`ResetAlgorithm`）等涉及等待子进程停机与 Channel 退出的操作，**必须释放 `s.mu` 后再阻塞等待 `<-w.doneCh`**，严禁持全局大锁等待子进程或协程，杜绝与后台自愈或自动回滚产生互锁死锁。
     - **锁外 IO 铁律**：`s.mu` 是 `Supervisor` 的全局大锁，`GetStatus` 以 RLock 参与其中，而 `Service.ListAlgorithms` 会对每个算法调用一次 `GetStatus`。因此**任何会做 DB 查询或订阅同步的操作（`BrokerSubscriptionResolver.SyncBrokerSubscriptions`）都必须在锁外执行**：先在 RLock 内快照 `s.resolver`，解锁后解析，再在写锁内做「二次校验 + 注册」。二次校验失败说明解析期间已有并发注册，直接丢弃本地构建的 watcher 即可——订阅是幂等的集合语义，无需回滚。
       - ⚠️ 由此产生的配套约束：`rollbackToOld` 必须是**不持锁的外层**（锁外解析 → `rollbackToOldLocked` 锁内注册），且 `UpgradeAlgorithm` 的两条回滚早退路径**不得依赖 `defer s.mu.Unlock()`**（否则自死锁），必须显式解锁后再调用。
       - ⚠️ `workerWatcher.mu` 同样不得跨 resolver IO 持有：崩溃重启的重解析必须在取 `w.mu` 之前完成（`w.mu` 会被 `GetStatus`/`ResetAlgorithm`/`stopProcess` 获取）。
     - **状态写入以 watcher 退役为条件**：`StopAlgorithm` 仅当 `retireWatcher(algoID, w)` 返回 `true`（确实移除了自己持有的 watcher）时才允许写 DB `DISABLED`。回滚/升级路径会在同一 `algoID` 下注册**新的活 watcher**，此时无条件写 `DISABLED` 会造成「进程在跑、DB 说已停用」的持久化漂移，且该包在下次宿主机启动时不会自动拉起。
     - **运行器进程组**：runner 必须以独立进程组启动（`SysProcAttr{Setpgid: true}`），停止时对**进程组**发信号（`syscall.Kill(-pid, sig)`，`ESRCH` 视为成功），否则 runner 派生的孙进程会成为孤儿并继续持有 broker socket 与共享内存。
     - **宿主机非正常停机级联（Pdeathsig）**：优雅停机（SIGTERM/SIGINT ➔ fx OnStop ➔ `StopAll`）只覆盖「Go 代码还能跑」的情况。宿主被 `SIGKILL`、被 OOM killer 杀掉、或 native 引擎崩溃时，**没有任何 OnStop 钩子会执行**，`StopAll` 永不运行。因此 Linux 上 `RunnerProcess.Start` 必须额外设置 `Pdeathsig: syscall.SIGKILL`（见 `sysprocattr_linux.go`），由内核在宿主进程死亡时直接回收 runner。
       - ⚠️ **必须用 `SIGKILL` 而非 `SIGTERM`**：runner 自身不装信号处理器，但第三方插件可以装 TERM handler 从而存活，等于重新制造孤儿。
       - ⚠️ **必须遵守 spawn 线程生命周期契约**：`Pdeathsig` 在内核中绑定的是「发起 `fork` 的那个 **OS 线程**」，而非进程（go.dev/issue/27505）。`runtime.LockOSThread` 绑定的是**调用它的那个 goroutine**，所以 `Start` **绝不能锁调用方**，也**绝不能由另一个 goroutine 解锁**——那个 `UnlockOSThread` 在未锁定的 goroutine 上是 no-op，调用方的锁会一直留着，直到调用方 goroutine 退出、runtime 回收该 M 并触发内核向健康 runner 发 SIGKILL。正确形态是：一个专属 goroutine 依次执行 `LockOSThread → cmd.Start → 握手回报 → 等待 Start 完成接线 → watchExit（cmd.Wait 之后 Unlock）`，即 **spawn 与 reap 必须同 goroutine**。`Start` 的调用方（HTTP handler / Fx 钩子 / worker）随时可以返回，绝不能成为 runner 生命的绑定物。回归门禁：`TestRunnerProcessSpawnThreadSurvivesWhileChildRuns`（从短生命周期 goroutine 启动多个 runner，启动 goroutine 返回后逐个校验存活）。
       - 非 Linux 平台无此机制，孤儿由 runner 侧的对端挂断检测（下条）在一个 poll 周期内自行收苹果。
     - **启动孤儿清扫（`ReapOrphanRunners`）**：`algorithmLifecycleImpl.Start` 必须在自动拉起任何算法**之前**调用一次清扫。`Pdeathsig` 只能阻止**新**孤儿的产生，无法处理升级前已存在的孤儿，也永远不会清理它们持有的 socket / shm 文件。清扫器的三条铁律：
       - **只认领真孤儿**：候选进程必须「`argv[0]` 基名 == runner 路径基名」且「已被 init/systemd 收养」（`parentIsReaper`）。⚠️ **绝不可用「父进程不是我们」这种反向判定** —— 那会把另一实例（不同路径/包装器）监督的正常 runner 误杀。实测中该缺陷直接杀掉了活进程。在 systemd user session 下收养者是 `systemd --user`（开发机为 pid 2020），**不是 pid 1**，且 `/proc/<systemd>/exe` 可能不可读，因此要用世界可读的 `comm` 而非 `exe`。
       - **`kill(-pid)` 的 ESRCH 不等于进程已死**：非组长的 runner（被 shell 拉起、或修复前启动的旧进程）会让 `kill(-pid)` 报 ESRCH 而进程仍活着。必须无条件再发一次直接信号，否则静默跳过。回归门禁：`TestReapOrphanRunners_KillsReparentedRunner`。
       - **文件回收以「无人持有」为准，且必须 fail-closed**：shm 段需同时扫 `/proc/*/maps`（已映射）与 `/proc/*/fd`（已 `shm_open` 但尚未 `mmap` 的窗口），并以内核报告的身份（device major:minor + inode）比对而非路径名（`maps` 的路径列在段被 unlink 后会带 ` (deleted)`，rename 后也会变）。**只有内核证明进程已消失（`ENOENT`/`ESRCH`）才跳过；`EACCES`/`EPERM`/I/O 错误一律视为「状态未知」，不得据此删除**——把未知当成「无人持有」会删掉活 runner 仍映射的段，表现为「流突然坏掉」而不是「残留一个文件」。未知项写入 `OrphanCleanupReport.UnverifiedShm` 并 `Warn`，让运维看得到为什么没扫。
         - socket 不能只靠 `connect` 探测：`UnixSocketServer::start` 先 `bind` 后 `listen`，中间态同样是 `ECONNREFUSED`。活性以 `/proc/net/unix` 中是否存在该路径的 endpoint 为准（`bind` 即入表），`connect` 仅作为「另一 netns 里的 broker」的兜底；该表不可读时同样不得删除。
         - unlink 前必须重取 inode 比对（`removeIfUnchanged`）：清扫与 `bind` 之间路径可能被换成新 broker 的 socket，按名字删除会让活 broker 失联。
         - ⚠️ **创建与清扫必须共享目录锁**：上述两个窗口（`shm_open→mmap`、`bind→listen`）在 `/proc` 看来与废弃资源同形，仅靠探测无法消除竞态。因此 Go 清扫与 native `FrameBroker::start` 对 `{broker base dir, /dev/shm}` 取同一把 advisory `flock`：两侧都按 `(device, inode)` 去重并升序排序（否则别名路径会变成两把锁或交叉加锁死锁）；创建侧**阻塞**取锁并覆盖 shm 初始化与 socket bind/listen 全程，清扫侧**非阻塞**取锁、锁忙即整体跳过（资源保留、启动继续）。
         - 僵尸进程（`Z`/`X`）视为已释放——它仍然响应 `kill(pid, 0)`，等 init 回收会白等满超时。回归门禁：`TestRemoveUnheldShmSegments_SparesMappedSegment`、`TestShmHoldState_DetectsUnmappedButOpenSegment`、`TestRemoveUnheldShmSegments_ReportsUnknownInsteadOfDeleting`、`TestRemoveStaleBrokerSocket_KeepsBoundButNotListeningSocket`、`TestReapOrphanRunners_SkipsCleanupWhenLockIsBusy`、`ZhulongFrameBrokerTests::test_frame_broker_start_waits_for_resource_lock`。
       - 清扫失败绝不阻断启动（仅 `Warn`），因为残留资源不影响宿主可用性。
     - **对端挂断必须“毒死”连接状态**：`UnixSocketClient::is_connected()` 不能只判断 `fd_ >= 0`。宿主进程消失后，内核只把对端置 `POLLHUP`，**本地 fd 依然有效**；若 `is_connected()` 仍为真，runner 主循环的 `continue` 会退化为**永久忙等死循环**（实测：被 kill -9 的宿主的 runner 在 20s 内烧满 19.98s CPU，持续 100% 占用单核且长期不被回收）。因此：
       - `peer_closed_` 原子标志必须在**不可恢复路径**置位：`poll()` 返回 `POLLHUP`/`POLLERR`/`POLLNVAL`、`recvmsg` 返回 EOF、或头/负载出现半包脱节（`read_all != SUCCESS`）。
       - ⚠️ **空闲 poll 超时绝不能置位**，否则正常的「暂无帧」会被误判为掉线。
       - `connect()` 必须显式复位 `peer_closed_`，否则同一对象重连后永久假死。
       - runner 主循环在 `!is_connected()` 时必须 `break` 并输出 `{"status":"info","stage":"broker_disconnected"}`，绝不可无条件 `continue`。⚠️ 该事件的 `status`/`stage` token 必须同时存在于 Go 侧 allowlist（`validRunnerStatus` / `validRunnerStage`）：漏一个就会把「宿主已死」降级成 `runner protocol record rejected` 并计入 invalid 计数，正是排障最需要的那个信号反而消失。回归门禁：`TestStreamLogReaderAcceptsBrokerDisconnectEvent`。
       - 回归门禁：`ZhulongIpcSocketFdTests` 第 7（对端崩溃必须收敛）与第 8（空闲超时不得误判 + 重连复位）段。
2. **分析任务解耦**：分析任务（CRUD、配置校验、算力预算检查与调度）完全归属 `internal/task` 模块。`algorithm` 仅实现 `task.AlgorithmSpecProvider`（供任务校验算法规格）与 `task.TaskChangeListener`（接收任务变更通知以触发防抖热重启）。兼容路由 `/api/v1/algorithms/tasks` 由 `task.Handler` 统一委托承载。
3. **推理数据与协议下沉**：
   - 通用 AI 推理模型（`InferenceResult`、`DetectionItem`、`PersonItem` 等）收敛于 `internal/aitypes`，解耦 `camera` 与 `record` 对 `algorithm` 的依赖；
   - 16 字节定长二进制帧头编解码与协议常量收敛于 `internal/algoipc`。
4. **错误处理与 apperr 闭环**：`Service` 统一使用 `apperr.Error` 分类暴露错误（`KindInvalid` 对应 422 校验失败，`KindConflict` 对应 409 活跃冲突，`KindNotFound` 对应 404），由 `httputil.WriteError(c, err)` 统一写回并自动完成多语言翻译。
5. **任务驱动按需惰性启停与待命态 (Task-Driven Lazy Startup & Standby State)**：
   - 算法的持久化状态 `ACTIVE` 仅代表管理员授权启用与调度准入，并不强行要求立即拉起底层 Runner 子进程；
   - 当算法无已启用的分析任务（`len(brokerSocks) == 0`）时，`Supervisor.StartAlgorithm` 返回 `ErrNoBrokerSockets`，算法在 DB 保持 `ACTIVE` 并进入待命态（Standby，0 进程、0 内存与 NPU 占用，Supervisor 状态为 `DISABLED`），杜绝因缺少 `--broker` 导致的秒退与虚假熔断；
   - 任务生命周期双向联动：新分析任务加入（`OnTaskChanged`）时，若算法处于待命态则自动唤醒并拉起 Runner 子进程；若全部任务被删除/停用，Runner 子进程优雅停止回收资源，算法在 DB 保持 `ACTIVE` 并平滑退回待命态；
   - 接口幂等性：对已在运行中的算法调用 `EnableAlgorithm` 必须幂等返回 `200 OK`；对处于 `CRASH_LOOP` 熔断态的算法调用 `EnableAlgorithm` 返回 409 Conflict（机器码 `ALGORITHM_IN_CRASH_LOOP`），引导用户先重置。


6. **卸载前必须让操作者看到影响面 (Deletion Impact Preview)**：
   - 卸载算法包**不可撤销**，且会带来两类截然不同的后果，弹窗必须分开陈述：
     - **会失去的能力**：`task_algorithms` 中该算法的条目被删除（任务本身保留，但对应相机在这条算法上停止分析）；算法包与模型文件从磁盘移除；
     - **不会丢失的东西**：历史证据（`recognitions` / `alarms` / `captures`）**保留**，只把 `algorithm_id` 置 NULL，`algorithm_name` / `algorithm_version` 快照仍在（迁移 `000015`/`000016`）。失去的是**按算法筛选**的能力，不是数据。
   - ⚠️ 文案铁律：**绝不可**写「将删除 N 条记录」。用户会以为证据没了，从而不敢卸载或误以为需要先备份。
   - 接口形态：`GET /api/v1/algorithms/{id}/deletion-impact`（只读、无副作用、不打审计），前端在弹窗打开时调用；**不要**让 `DELETE` 返回 409 + 载荷来表达影响面 —— 那会把「预览」与「执行」混在一个非幂等方法里，前端无法在用户确认前安全地预览后果。
   - 影响面取不到（500/超时）时前端必须**显式警告**「无法获取统计，请谨慎确认」，静默失败会让用户误以为「没有影响」。
   - `DELETE` 执行时后端会再取一次影响面并写入审计详情：卸载后 tasks 条目已删、记录已失去 algorithm_id，这些关联信息事后不可重建，只能靠这条审计追溯。
   - ⚠️ 刻意**不统计**抓拍图片字节数：扫描 `snapshots/` 需要目录遍历，几千个文件在 RK3568 上会明显拖慢接口，而图片不会因卸载消失，字节数不影响决策。
   - 回归门禁：`TestDeletionImpactReportsBindingsAndEvidence`（含「查询无副作用」断言）、`TestDeletionImpactZeroWhenClean`（空数组而非 null）、`TestDeleteAlgorithmKeepsEvidenceAfterImpact`（预览与真实后果一致）、`algorithm-delete-dialog.test.tsx` 的 7 个用例。
