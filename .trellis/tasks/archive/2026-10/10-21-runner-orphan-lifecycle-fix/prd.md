# Runner 孤儿进程与 broker 资源清扫修复

## Goal

确保算法 runner 在短生命周期调用方返回后仍受宿主可靠监督；启动清扫不得误杀健康 runner、卸载仍被使用的共享资源或破坏并发创建中的 broker；runner 断连原因必须被 Go 日志解析器接受。恢复此次 review 标出的意外项目协作约定删除。

## Confirmed Facts

- `runtime.LockOSThread` 绑定调用它的 goroutine；另一个新 goroutine 调用 `UnlockOSThread` 不会解除原调用方的锁。当前 `RunnerProcess.Start` 锁调用方，随后另起 watcher，测试调用方则一直存活到 runner 被 Stop，未覆盖调用方先返回。
- `ReapOrphanRunners` 按 `/proc/<pid>/maps` 判断共享内存是否使用，但当前实现忽略 maps 无法读取的错误，并只按映射路径判断；POSIX shm 在 `shm_open` 后、`mmap` 前也存在一个文件已出现但暂时无映射的窗口。
- Native `UnixSocketServer::start` 先 `bind` 再 `listen`；清扫器只做 `connect` 时，bind/listen 窗口会返回 `ECONNREFUSED` 并被误当作死 socket。当前 Go 清扫和 native broker 创建没有共同同步协议。
- Runner 新增输出 `status=info, stage=broker_disconnected`，而 Go parser 的 status 与 stage allowlist 未包含这两个值。
- 四个 frontend 文件属于仍在进行的 ROI/MASK 任务；本任务不得回退或修改这些文件，也不把它们并入本任务的交付范围。
- 当前 `AGENTS.md` 工作区差异移除了 `TRELLIS:END` 和 Pi 子代理约定；用户要求修复 review 标出的全部问题，故本任务会恢复这段被删内容。

## Requirements

### R1. Runner spawn-thread 生命周期

- 在专属 runner 生命周期 goroutine 中依次完成 `runtime.LockOSThread`、`cmd.Start` 与 `cmd.Wait`；该 goroutine 是唯一持有创建线程的 goroutine。
- `Start` 通过握手向调用方报告启动成功或错误；启动成功后须在 watcher 进入 `Wait` 前完成 `p.cmd`、状态及 stdout/stderr drain 接线。
- 只有持有锁的 goroutine 可在子进程已回收后解锁。启动失败路径必须由同一 goroutine 解锁并完整关闭管道。
- 不得依赖调用 `Start` 的 HTTP/Fx/worker goroutine 长期存活。

### R2. Orphan resource sweep 安全性

- `/proc` 的权限错误、I/O 错误或无法确认进程是否持有资源时，视为状态未知并 fail closed；不得据此删除 shm。只有明确进程已消失的 `ENOENT` 可忽略。
- 对 shm 同时检查映射与仍打开的文件描述符，覆盖 `shm_open` 到 `mmap` 的窗口；在删除前验证目标路径仍是检查时的同一文件。
- Linux Go 清扫器与 Native `FrameBroker::start` 使用相同的 advisory `flock` 协议保护 broker base directory 与 `/dev/shm`。native 资源创建持锁完成 shm/socket 初始化；清扫器以非阻塞方式取锁，锁忙或锁失败时仅报告错误并保留资源，启动仍继续。
- Socket 活性检查须从 `/proc/net/unix` 确认路径是否已有内核中的 bound endpoint（包括尚未 listen 的 socket）；该表不可读时 fail closed。检查与 shm 判定均须在共享资源锁内进行；路径被替换时不得删除替换后的文件。
- 非 Linux 清扫器继续保持无破坏的 no-op 行为。

### R3. Runner protocol event allowlist

- Go parser 接受 `status=info` 与 `stage=broker_disconnected`，记录事件字段而非计为 invalid；通过测试锁定这条跨层日志契约。

### R4. Restore project instructions

- 仅恢复 `AGENTS.md` 中意外删除的 `TRELLIS:END` 和 Pi 子代理约定，不重写文件其他内容。

## Acceptance Criteria

- [x] 在调用 `Start` 的短生命周期 goroutine 返回后，runner 仍保持运行，直到显式 `Stop`；`cmd.Wait` 后子进程被回收且锁定 goroutine 退出。启动失败也无 OS thread、pipe 或 watcher 泄漏。
- [x] 不可读/不可枚举的 `/proc/<pid>/maps` 或 `/proc/<pid>/fd` 不会导致 shm 被删除；仍有映射或 fd 的 shm 保留，确定无人持有的旧 shm 可回收。
- [x] Go 清扫与 C++ broker 创建遵守同一目录锁协议；锁占用时清扫不删除资源、不阻断宿主启动；并发创建中的 socket/shm 不会被清扫器移除。
- [x] 活 broker socket（包括已 bind 但尚未 listen 的 socket）保留，断开的旧 socket 可回收；`/proc/net/unix` 不可读或检查期间路径被替换时，新对象保留。
- [x] `broker_disconnected` 被 Go parser 接受并带出 `status` / `stage`，不增加 invalid 计数。
- [x] `AGENTS.md` 恢复闭合 Trellis marker 与 Pi 子代理规则；四个 ROI frontend 文件保持不变。
- [x] `gofmt`、目标 Go `-race` 测试、`make native-test`、`make check` 和 `git diff --check` 通过。

## Out of Scope

- 修改 ROI/MASK frontend 测试或 Vite 配置；这些改动归属现有 ROI/MASK 任务。
- 改变 runner CLI/IPC ABI、进程组停止语义、NPU/插件生命周期或数据库结构。
- 非 Linux `/proc` 清扫实现；非 Linux 仍为 no-op，runner 通过 peer hang-up 退出。

## Risks

- 目录 `flock` 是协作式约定；Go 清扫与当前 native build 必须使用相同锁集合和顺序。旧版本不参与锁协议，依赖 `/proc` maps/fd/unix endpoint 检查及 inode 复核；状态不可见时保留资源并告警。

## Outcome

反证验证（每条都先回退实现、确认新测试真的失败，再恢复）：

| 回退的实现 | 失败的新测试 |
| --- | --- |
| 把 spawn 锁交还调用方（旧 `Start`） | `TestRunnerProcessSpawnThreadSurvivesWhileChildRuns` — runner 20ms 内被杀 |
| shm 只查 maps，不查 fd | `TestShmHoldState_DetectsUnmappedButOpenSegment` |
| socket 只 `connect` 探测，不读 `/proc/net/unix` | `TestRemoveStaleBrokerSocket_KeepsBoundButNotListeningSocket` |
| 去掉 `FrameBroker::start` 的目录锁 | `ZhulongFrameBrokerTests::test_frame_broker_start_waits_for_resource_lock` |

验证记录：`make go-check`、`make native-test`（22/22）、`make check`（前端 87 文件 / 525 用例 + native + api-docs + go-check）全绿；`GOOS=darwin` 交叉编译 `internal/algorunner` 通过；`git diff --check` 干净。

## Residual Notes

- **`/proc` 不可读时清扫会整体保守跳过**：多用户开发机上 `/proc/<other-uid>/fd` 通常 `EACCES`，此时 shm 段全部落在 `UnverifiedShm` 并保留。这是有意的 fail-closed 取舍：删掉活段是「流坏掉」，留下死段只是「多一个文件」。目标机（root 单用户）不受影响。
- **Windows 交叉编译失败属既有问题**，与本任务无关：`internal/algorunner/process.go` 一直使用 `syscall.Kill`，未纳入跟踪的 `sysprocattr_other.go` 也无条件设置 `Setpgid`。项目支持面是 Linux 目标 + Darwin 构建校验。
- 交接时工作区的 `internal/algorunner/process.go` 使用了 `runtime.LockOSThread` 却没有 `import "runtime"`（不可编译）；本任务顺手补齐，因此该文件的 diff 同时包含此前的线程契约改动。
