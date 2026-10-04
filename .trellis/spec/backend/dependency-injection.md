# 依赖注入与应用生命周期规范 (Uber Fx)

> Uber Fx 依赖注入、生命周期钩子（OnStart/OnStop）编排及依赖图验证。

---

## 1. 核心隔离原则

1. **业务包零 Fx 依赖**：业务模块（`internal/camera` 等）仅暴露纯粹的 Go 构造函数，严禁在业务包中导入 `go.uber.org/fx`。
2. **装配收敛在 `internal/app`**：Fx 的 Provider 声明与应用编排集中在 `internal/app/`。
3. **构造函数无副作用**：构造函数只做内存分配与依赖绑定，**严禁启动 goroutine、发起网络请求或加载大模型**；所有长时任务交由生命周期钩子托管。

---

## 2. 生命周期启停时序

| 阶段 | 严格顺序 |
| --- | --- |
| **启动 (OnStart 追加顺序)** | 1. 打开 SQLite & 配置 PRAGMA ➔ 2. **执行版本化迁移 (失败则阻断)** ➔ 3. 初始化 C++ 原生引擎句柄 ➔ 4. 启动音视频处理流水线 ➔ 5. 启动 Gin HTTP 监听 |
| **停止 (OnStop 逆序执行)** | 1. 停止 Gin HTTP 监听 (排空请求) ➔ 2. 发出停止信号并 Join 汇合 C++ 工作线程 ➔ 3. 释放 C++ 原生句柄与 NPU 硬件资源 ➔ 4. 关闭 SQLite 连接 (安全落盘) |

---

## 3. 协程上下文与停机安全

- **长驻协程上下文**：后台持续运行的协程必须基于**应用根 Context**，**严禁使用 `OnStart` 传入的 `ctx`**（`OnStart` 的 `ctx` 启动完成后会立即取消）。
- **CGO 停机超时**：`OnStop` 传出的 `ctx` 有 Deadline 超时。底层 C++ Pipeline 必须有明确的取消标志位与唤醒机制，并在释放原生内存前真正完成线程 `join()`。

---

## 4. 依赖图静态校验测试

在 `internal/app/app_test.go` 中加入验证测试，防止生产部署因缺少依赖或循环引用崩溃：

```go
package app_test

import (
 "testing"
 "go.uber.org/fx"
 "github.com/nikonikowuw/Zhulong/internal/app"
)

func TestAppDependencyGraph(t *testing.T) {
 if err := fx.ValidateApp(app.Module); err != nil {
  t.Fatalf("Fx dependency graph validation failed: %v", err)
 }
}
```
