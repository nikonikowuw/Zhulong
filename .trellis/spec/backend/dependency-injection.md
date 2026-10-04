# 依赖注入与应用生命周期规范 (Uber Fx)

> Uber Fx 依赖注入、生命周期钩子（OnStart/OnStop）编排及依赖图验证。

---

## 1. 核心隔离原则

1. **业务包零 Fx 依赖**：业务模块（`internal/camera` 等）仅暴露纯粹的 Go 构造函数，严禁在业务包中导入 `go.uber.org/fx`。
2. **装配收敛在 `internal/app`**：Fx 的 Provider 声明与应用编排集中在 `internal/app/`。
3. **构造函数无副作用**：构造函数只做内存分配与依赖绑定，**严禁启动 goroutine、发起网络请求或加载大模型**；所有长时任务交由生命周期钩子托管。

---

## 2. 当前骨架生命周期时序

| 阶段 | 严格顺序 |
| --- | --- |
| **启动 (`lifecycleRuntime.Start`)** | 1. 打开 GORM/SQLite 并执行嵌入式版本化迁移 (失败则阻断) ➔ 2. 创建并启动无硬件 C++ 生命周期 stub ➔ 3. 绑定 TCP listener 并启动 Gin HTTP Serve |
| **停止 (`lifecycleRuntime.Stop`)** | 1. 优雅关闭 HTTP 并等待 Serve 退出 ➔ 2. 停止并销毁 C++ opaque handle ➔ 3. 关闭 SQLite pool ➔ 4. Sync Zap |

启动的后续步骤失败时，`lifecycleRuntime.Start` 在返回错误前按逆序清理已打开资源，因为 Fx 不会对失败的 `OnStart` 自动调用该 hook 的 `OnStop`。正常停机时先排空 HTTP 请求，再释放 native 与数据库资源。当前 native stub 不创建 worker thread、媒体流水线或 NPU context；未来增加后台线程时，必须先停止、唤醒并 Join 后才能销毁句柄。

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

// Internal `app` package test.
func TestAppDependencyGraph(t *testing.T) {
    if err := fx.ValidateApp(fx.Supply(app.DefaultConfig()), app.Module, fx.NopLogger); err != nil {
        t.Fatalf("Fx dependency graph validation failed: %v", err)
    }
}
```
