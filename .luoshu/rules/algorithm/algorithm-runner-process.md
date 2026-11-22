---
trigger: glob
globs:
  - 'internal/algorunner/**'
priority: 100
description: 算法 runner 的进程组、Pdeathsig、spawn/reap 线程生命周期、孤儿清扫与 watcher 退役契约。
---

# 算法 Runner 进程纪律

- runner 必须以独立进程组启动并带 `Pdeathsig: SIGKILL`；包进程统一调用 `algorunner.PackageSysProcAttr()`，不在别处重新推导（对 Pdeathsig 的第二种理解就是孤儿存活的原因）。必须是 `SIGKILL` 而非 `SIGTERM` —— runner 自身不装信号处理器，但第三方插件可以装 TERM handler 从而存活。停止时对**进程组**发信号（`syscall.Kill(-pid, sig)`，`ESRCH` 视为成功），否则 runner 派生的孙进程会成为孤儿并继续持有 broker socket 与共享内存。见 [SysProcAttr 契约](../../../internal/algorunner/sysprocattr_linux.go#L35-L40)、[进程组信号](../../../internal/algorunner/process.go#L542-L550)。

- spawn 与 reap 必须同 goroutine：由一个专属 goroutine 依次执行 `LockOSThread → cmd.Start → 等待调用方接线 → watchExit`，`UnlockOSThread` 只在 `cmd.Wait` 返回之后发生。`Start` 的调用方（HTTP handler / Fx 钩子 / worker）绝不能成为 pin 的持有者——那个 goroutine 退出时 runtime 回收 M 会触发内核 SIGKILL 一个健康的 runner；也不能由另一个 goroutine 代解，那在未锁定的 goroutine 上是 no-op。见 [专属 spawn 协程](../../../internal/algorunner/process.go#L368-L381)、[唯一 unlock 点](../../../internal/algorunner/process.go#L432-L440)。

- 孤儿清扫先于任何自动拉起，且只认领真孤儿：候选必须「`argv[0]` 基名 == runner 路径基名」且「已被 init/systemd 收养」，绝不可用「父进程不是我们」这类反向判定（会误杀另一实例监督的正常 runner）。`kill(-pid)` 的 `ESRCH` 不等于进程已死（非组长的 runner 会让它静默跳过），必须再发一次直接信号；文件回收以内核证明「无人持有」为准且 **fail-closed** —— `EACCES`/`EPERM`/I/O 错误一律记为 `UnverifiedShm` 并保留，unlink 前重取 inode 比对。见 [收养判定](../../../internal/algorunner/orphan_linux.go#L132-L140)、[收养者识别](../../../internal/algorunner/orphan_linux.go#L457-L470)、[按 inode 删除](../../../internal/algorunner/orphan_resources_linux.go#L75-L95)、[未验证项上报](../../../internal/algorunner/orphan_report.go#L20-L25)。

- `StopAlgorithm` 仅当 `retireWatcher(algoID, w)` 返回 `true`（确实移除了自己持有的 watcher）时才允许写 DB `DISABLED`；回滚/升级路径会在同一 `algoID` 下注册新的活 watcher，无条件写会造成「进程在跑、DB 说已停用」的持久化漂移。见 [退役判定](../../../internal/algorunner/supervisor.go#L470-L490)、[条件写入点](../../../internal/algorunner/supervisor.go#L604-L615)。

- 依赖不可用不是包缺陷：broker 连不上（退出码 5）或运行中断开按 `dependencyBackoff` 的 5s→10s→20s→`MaxBackoff` 阶梯重启，**不消耗崩溃配额、不触发 RollbackGuard 回滚**；一次网络抖动不该把算法固化成 `CRASH_LOOP`。见 [重启阶梯](../../../internal/algorunner/watcher.go#L266-L300)、[依赖阶段判定](../../../internal/algorunner/watcher.go#L262-L266)。
