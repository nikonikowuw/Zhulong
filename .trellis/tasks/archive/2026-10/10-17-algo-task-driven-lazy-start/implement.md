# 算法包任务驱动按需启停与启用幂等性实施计划

## 实施步骤

- [ ] **Step 1: `internal/algorunner` 哨兵错误与 0-broker 拦截**
  - 在 `supervisor.go` 导出 `ErrAlgorithmAlreadyRunning` 与 `ErrNoBrokerSockets`。
  - 在 `StartAlgorithm` 中检测 `s.resolver != nil && len(brokerSocks) == 0`，若为真返回 `ErrNoBrokerSockets`，不启动子进程。
  - 在 `RestartAlgorithm` 中检测若最新 `brokerSocks` 为 0，优雅停止子进程，保持 DB 状态为 `StatusActive`，平滑回退待命态。
  - 验证：`python3 native/scripts/build.py go test -race ./internal/algorunner -run TestSupervisor`

- [ ] **Step 2: `internal/algorithm` 错误码注册与 `EnableAlgorithm` 幂等支持**
  - 在 `locale.go` 中注册 `ALGORITHM_IN_CRASH_LOOP` 本地化文案。
  - 在 `service.go` 中重构 `EnableAlgorithm`：
    - 对 `CRASH_LOOP` 返回 `409 Conflict`；
    - 对运行中状态返回幂等成功；
    - 捕获 `ErrNoBrokerSockets`，写入 `ACTIVE` 并平滑转入待命态返回成功；
    - 捕获 `ErrAlgorithmAlreadyRunning` 返回幂等成功。
  - 验证：`python3 native/scripts/build.py go test -race ./internal/algorithm -run TestHandler`

- [ ] **Step 3: `internal/algorithm` 待命态动态拉起与生命周期联动**
  - 重构 `restartAlgorithmNow`：对处于 `ACTIVE` 但待命的算法，在任务加入后自动调用 `StartAlgorithm`；
  - 调整 `algorithmLifecycleImpl.Start`：对宿主开机自启且无任务的算法放行待命，不打印 Error。
  - 验证：`python3 native/scripts/build.py go test -race ./internal/algorithm -run TestTaskLifecycle`

- [ ] **Step 4: 编写全流程回归测试**
  - 在 `algorunner/supervisor_test.go` 中新增 0 broker 拦截测试与重启回收测试；
  - 在 `algorithm/service_task_test.go` 中新增端到端任务驱动生命周期测试（无任务启用 -> 待命 -> 增加任务拉起 -> 删除任务回收）。
  - 验证：`python3 native/scripts/build.py go test -race ./internal/algorunner ./internal/algorithm`

- [ ] **Step 5: 全局质量门禁**
  - 运行 `make check` 验证代码规范、C++ 引擎绑定与 Go 构建完整性。

## 验证命令

```bash
# 1. 验证 algorunner 单元测试
python3 native/scripts/build.py go test -race ./internal/algorunner

# 2. 验证 algorithm 单元测试
python3 native/scripts/build.py go test -race ./internal/algorithm

# 3. 运行全套质量门禁
make check
```
