# Batch 1 主会话独立验证记录

日期: 2026-10-17

## 独立复跑（未依赖子代理报告）
- `go test -race -count=1 ./internal/algorunner` → ok (3.5s)
- `go vet ./internal/algorunner` → clean
- `gofmt -l internal/algorunner` → 无输出
- `go test -race ./internal/algorithm ./internal/app` → ok

## 反向验证（Step 9 闸门）— 全部通过

对旧实现逐个回退，确认新测试真的会失败。所有回退均为手工文件替换（非 git stash），验证后已完整恢复。

| 项 | 回退内容 | 旧实现下的失败证据 | 结论 |
| --- | --- | --- | --- |
| A (R2 分片) | `process.go` → HEAD | 4 个分片测试全失败：`shard 0 exceeds shardBudget (58982): 64755 bytes`；`SingleItemExceedsBudget` 报 `expected error to contain face_id ..., got: gallery sync shard 0 exceeds 64KB (66542 bytes)` | ✅ 真能抓 bug |
| B (R5 dotenv) | `process.go` → HEAD | `expected a Warn log about the failed dotenv load; observed: []` | ✅ |
| C (R4 normalize) | `recovery.go` + `supervisor.go` → HEAD | `panic: non-positive interval for NewTicker` at `superviseLoop` → `time.NewTicker(0x0)` | ✅ 正是 R4 描述的 panic |
| D (R1 状态漂移) | 仅把 `StopAlgorithm` 的守卫回退为无条件写入 | `StopAlgorithm must not write a status when it did not retire its own watcher; wrote "DISABLED"` | ✅ |

## 对子代理自报偏差的裁定

子代理主动声明：黑盒并发冒烟测试 `TestSupervisorRollbackThenStopKeepsStatusConsistent`
**抓不住**旧 bug（回滚窗口是微秒级，墙钟调度不可靠命中）。经复核认可该判断：
- R1 的**确定性**证据是白盒 `TestStopAlgorithmDoesNotDisableAReplacedWatcher`（反向验证 D 已证其有效）；
- 黑盒冒烟测试保留为 `-race` 下的不变量交叉校验，**不作为 R1 的唯一回归闸门**。

## 未完成项（交由 Batch 2 / Step 8 门禁）
- Step 5/6/7 未实现（本批次刻意排除）
- `make go-check` 未运行（属 Step 8 最终门禁）

---

# Batch 2 主会话独立验证记录

日期: 2026-10-17

## 独立复跑
- `go vet ./internal/algorunner` → clean
- `gofmt -l internal/algorunner` → 无输出
- `go test -race -count=2 -timeout 300s ./internal/algorunner` → ok (6.1s)
- `go test -race ./internal/algorithm ./internal/app` → ok
- `make go-check` → **exit=0**（全包通过）

## 主会话发现并修复的缺陷（子代理遗漏）

**R7 锁纪律回退**：Batch 2 引入的 `signalRunner` 无锁重读 `p.cmd`：

```go
func (p *RunnerProcess) signalRunner(sig syscall.Signal) error {
    pid := p.cmd.Process.Pid   // ← 无锁读 p.mu 保护的字段
```

而原实现（HEAD）在 `Stop()` 中全程使用锁内捕获的局部 `cmd`。虽然 `Stop` 的调用方
（`stopProcess`/`StopAll`）已用 `stopMu`/`once` 串行化，属潜在而非活跃竞态，但这是对既有
锁纪律的**回退**。已修复：`signalRunner(cmd *exec.Cmd, sig)` 改为显式接收句柄。

## 反向验证（Step 9 闸门）— R3/R6/R7 全部通过

| 项 | 回退内容 | 旧实现下的失败证据 | 结论 |
| --- | --- | --- | --- |
| E (R3 锁) | `supervisor.go` → HEAD | `GetStatus blocked while StartAlgorithm was resolving broker paths`；`only 1 StartAlgorithm call(s) reached the resolver` | ✅ |
| F (R6 重解析) | 移除崩溃重启的 re-resolve 块 | `restart broker flags = [/tmp/zhulong_broker_1.sock], want [broker_2 broker_3]` | ✅ |
| G (R7 进程组) | 移除 `Setpgid` + 单进程信号 | `grandchild 519048 survived Stop; the runner process group was not killed` | ✅ |

**注意**：E 的反向验证未再挂起，证明 steer 要求注入的 `sync.Once` 释放逻辑生效
（原缺陷：`t.Fatalf` 早于 `close(release)` → 永久死锁 → 10 分钟超时）。

## 累计反向验证：7/7 通过
A(R2) B(R5) C(R4) D(R1) E(R3) F(R6) G(R7) — 全部证明测试能在旧实现下失败。

## 其他核实
- `w.brokerSocks` 单写者声明**核实无误**：唯一写点 supervisor.go:671，读点 689，同在 `superviseLoop`。
- `UpgradeAlgorithm` 两条回滚路径均在解锁后调用 `rollbackToOld`（无 `defer` 自死锁）。
- AC13 满足：改动仅 `internal/algorunner/`，未触碰 `internal/algoipc` 与 native。
