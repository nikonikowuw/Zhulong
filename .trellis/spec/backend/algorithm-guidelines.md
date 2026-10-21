# 算法包开发与交付规范 (Algorithm Package Specification)

> Zhulong 视觉计算平台算法包工程标准：物理目录拓扑、`manifest.json` 权威元数据、安全打包与本地黑盒验包准入门禁。

---

## 1. 算法包物理目录拓扑 (Package Topology)

算法包必须采用**扁平化归档**（根目录下直接包含约定文件，**严禁外层包裹随机命名的顶层文件夹**）：

```txt
<package_name>.tar.gz (或 .zip)
├── manifest.json       # [必须] 算法元数据声明与算力资源规格清单 (权威定义见 §2)
├── plugin.so           # [必须] 导出纯 C ABI 的推理动态链接库 (权限必须为 0755)
├── sample.jpg          # [必须] 用于准入黑盒测试的真实前向推理样本图
├── models/             # [可选] 算法只读权重模型 (.onnx, .rknn, .om, .engine 等)
├── config.schema.json  # [可选] 算法超参数配置的 JSON Schema (缺省为零参算法)
└── .env                # [可选] 算法运行时本地环境变量覆写文件 (见 §3)
```

### 1.1 归档与打包铁律 (Packaging Rules)

1. **归档格式**：仅支持 `.tar.gz`（魔数 `0x1F, 0x8B`）或 `.zip`（魔数 `0x50, 0x4B, 0x03, 0x04`）。
2. **扁平根路径**：归档根目录下必须直接暴露 `manifest.json` 与 `plugin.so`。严禁打包为 `dist/` 或 `<package_name>/` 嵌套目录。
3. **文件权限**：动态库 `plugin.so` 在 Linux 下必须具备可执行权限（`chmod 0755 plugin.so`）；Windows 打包 zip 时平台服务会在解压后自动补正为 `0755`。
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
    "target": "cpu"
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
| `task_type` | string | 是 | 枚举：`detection` \| `classification` \| `person_structure` | 算法任务类型，决定推理结果的数据结构 |
| `category` | string | 是 | 枚举：`safety` \| `security` \| `fire` \| `traffic` \| `general` | 应用行业分类 |
| `platform.arch` | string | 是 | 枚举：`x86_64` \| `aarch64` | 目标硬件 CPU 指令集架构 |
| `platform.target` | string | 是 | 枚举：`cpu` \| `rknn` \| `cann` \| `tensorrt` | 硬件推理加速后端 |
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

在向平台交付算法包之前，开发者必须在本地使用平台提供的 `zhulong_algo_runner` CLI 进行黑盒自测：

```bash
zhulong_algo_runner --verify-plugin plugin.so --sample sample.jpg
```

退出码为 `0` 且终端输出 `{"status":"passed", ...}` 时方可通过准入校验。

### 平台准入拦截错误代码对照表

| 平台拦截错误码 | 触发原因 | 开发者整改指引 |
| :--- | :--- | :--- |
| `ZIP_SLIP_ATTACK_DETECTED` | 压缩包内含有 `../` 相对路径或绝对路径 | 重新打包，确保所有文件使用相对根目录路径 |
| `DECOMPRESSION_BOMB_DETECTED` | 文件数 > 1,000 或解压总大小 > 1GB | 精简模型与依赖库体积，移除无用中间文件 |
| `ALGO_PLATFORM_MISMATCH` | `platform.arch`/`target` 与运行宿主配置不符 | 核验交叉编译工具链与 `manifest.json` 平台声明 |
| `INVALID_MANIFEST_TOKEN` | `id` / `version` 包含非法字符，或 `name` 超过 32 字节 | 匹配正则 `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$` 并缩短 `name` |
| `ALGO_VERIFICATION_FAILED` | `zhulong_algo_runner` 验包退出码非 0 | 使用 `sample.jpg` 在本地复现排查崩溃与内存问题 |

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


