# Technical Design: 修复 algorunner 并发死锁与管道竞态缺陷

## 1. 管道读取与进程退出同步模型设计

### 现状与问题
在 `internal/algorunner/process.go` 中：
```go
// Start:
go p.streamLogs(stdoutPipe, zap.DebugLevel, false)
go p.streamLogs(stderrPipe, zap.WarnLevel, true)
go p.watchExit(cmd)

// watchExit:
err := cmd.Wait()
// ... 立即读取 p.failureStage 并 close(p.doneCh)
```
- `cmd.Wait()` 会在进程退出后关闭相关描述符，但在多核高并发调度下，负责 `scanner.Scan()` 的协程尚未将缓冲区的字符串写到 `p.logger`（或尚未解析到 `{"stage":"connect"}` 更新 `p.failureStage`）。
- 外部调用者通过 `<-p.Done()` 恢复执行时，读取到的是不完整的日志和陈旧的 `failureStage`。

### 改造方案
在 `RunnerProcess` 中增加 `logWg sync.WaitGroup`：
```go
type RunnerProcess struct {
    // ...
    logWg sync.WaitGroup
}
```
1. `Start()` 时：
   ```go
   p.logWg.Add(2)
   go func() {
       defer p.logWg.Done()
       p.streamLogs(stdoutPipe, zap.DebugLevel, false)
   }()
   go func() {
       defer p.logWg.Done()
       p.streamLogs(stderrPipe, zap.WarnLevel, true)
   }()
   go p.watchExit(cmd)
   ```
2. `watchExit()` 时：
   ```go
   err := cmd.Wait()
   // 必须等待两个流的 reader 读至 EOF 并退出
   p.logWg.Wait()

   p.mu.Lock()
   defer p.mu.Unlock()
   // 此时 p.failureStage 判定 100% 可靠
   // 并且所有日志已经写入 zap logger / observer
   close(p.doneCh)
   ```

---

## 2. Supervisor 锁粒度与互锁死锁消除设计

### 2.1 `UpgradeAlgorithm` 锁隔离
#### 死锁时序
```
Goroutine A (UpgradeAlgorithm):
  1. 获取 s.mu.Lock()
  2. 阻塞在 <-oldWatcher.doneCh

Goroutine B (oldWatcher.superviseLoop):
  1. 检测到旧进程退出或 crash
  2. 触发 guard.ShouldRollback 或 stateMachine
  3. 执行 w.supervisor.mu.Lock() 试图操作 s.watchers 或回滚
  ===> 死锁！Goroutine A 占着 s.mu 等 doneCh，Goroutine B 占着 doneCh 等 s.mu！
```

#### 解决方案
将生命周期停机阶段移出锁保护区：
```go
func (s *Supervisor) UpgradeAlgorithm(ctx context.Context, newAlgo PackageMeta, oldAlgo PackageMeta) error {
    s.mu.Lock()
    oldWatcher, exists := s.watchers[oldAlgo.ID]
    if exists {
        delete(s.watchers, oldAlgo.ID)
    }
    s.mu.Unlock()

    if exists {
        // 使用内置安全关闭，防止直接 close(ch) 引发 panic
        if stopErr := oldWatcher.stopProcess(3 * time.Second); stopErr != nil {
            s.logger.Warn("failed to stop old algorithm process during upgrade",
                zap.String("id", oldAlgo.ID),
                zap.Error(stopErr),
            )
        }
        <-oldWatcher.doneCh
    }

    s.mu.Lock()
    defer s.mu.Unlock()

    // 二次检查是否在释放锁期间被外部重复注册
    if _, alreadyExists := s.watchers[newAlgo.ID]; alreadyExists {
        return errors.New("algorithm worker already started concurrently during upgrade")
    }

    // 解析 broker socks 并启动新版本...
```

### 2.2 `ResetAlgorithm` 锁隔离
```go
func (s *Supervisor) ResetAlgorithm(ctx context.Context, algoID string) error {
    s.mu.Lock()
    w, exists := s.watchers[algoID]
    if exists {
        w.mu.Lock()
        status := w.status
        w.mu.Unlock()
        if status != StatusCrashLoop {
            s.mu.Unlock()
            return ErrAlgorithmNotInCrashLoop
        }
        s.mu.Unlock() // 释放全局锁

        select {
        case <-w.doneCh:
        case <-ctx.Done():
            return ctx.Err()
        }

        s.mu.Lock()
        if s.watchers[algoID] == w {
            delete(s.watchers, algoID)
        }
        s.mu.Unlock()

        if s.store != nil {
            if err := s.store.UpdateAlgorithmStatus(ctx, algoID, StatusDisabled); err != nil {
                return err
            }
        }
        return nil
    }
    s.mu.Unlock()
    // ...
```

---

## 3. 契约精简与无用代码清理

`internal/algorunner/contracts.go`:
- 移除 `func (p PackageMeta) PreviousInstallDir(algoDir string) string`
- 移除 `func (p PackageMeta) PreviousEntryPath(algoDir string) string`

---

## 4. 单测设计

在 `supervisor_test.go` 中增加测试用例：
1. `TestSupervisorUpgradeAlgorithmSuccess`：验证由 v1.0.0 升级到 v2.0.0 时，旧进程被安全停止，新进程正常拉起，且状态保持 `RUNNING`。
2. `TestSupervisorUpgradeAlgorithmRollbackOnCrash`：验证升级到 v2.0.0 后，新版本在 observation window（短时间）内崩溃，`RollbackGuard` 自动触发回滚并将旧版本（v1.0.0）拉起。
