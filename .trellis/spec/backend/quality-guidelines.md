# Go 后端质量门禁与测试规范

> 质量门禁命令、并发安全（-race）与禁止反模式。

---

## 1. 质量检验命令与门禁标准

```bash
gofmt -w cmd internal
make go-check
```

*`make go-check` 通过 Native 构建脚本传递静态 FFmpeg 与内容摘要 Engine 归档，随后运行 vet/race。需要单独 Go 命令时使用 `python3 native/scripts/build.py go test -race ./internal/engine`，不要依赖固定路径的 CGO 外部归档缓存。门禁限定在 `cmd/` 和 `internal/` 自有包，避免 `./...` 递归扫描 `web/node_modules` 中第三方包自带的 Go 示例。*

---

## 2. 并发安全铁律

1. **`-race` 强制开启**：本地与 CI 测试必须带 `-race`，数据竞争报错视为阻断 Bug。
2. **Goroutine 必须有确定退出路径**：严禁无终止条件的孤儿协程；长时协程必须监听 `ctx.Done()` 或退出通道。
3. **禁止循环内 `time.After`**：在 `for { select { ... } }` 中严禁使用 `case <-time.After(...)`（Timer 无法提前回收导致内存泄漏），必须复用 `time.NewTimer`。

---

## 3. 明确禁止的反模式

| 严禁模式 | 危害 | 正确替代方案 |
| --- | --- | --- |
| **业务处理中直接 `panic`** | 导致整机 NVR 崩溃停服 | 返回强类型 `error` 并由上层恢复 |
| **空白标识符吞噬错误 `_ = fn()`** | 掩盖严重系统故障 | 始终检查并处理/向上传播 `error` |
| **在 Struct 字段中保存 `Context`** | 破坏上下文传递树与生命周期 | 始终作为函数的首个参数显式传入 `ctx` |
| **在热路径中反复开闭 DB 连接** | 耗尽文件描述符并加剧锁冲突 | 全局复用 `*gorm.DB` 连接池 |
| **全局变量持有业务状态** | 并发竞争与单测交叉污染 | 通过构造函数显式注入依赖 |

---

## 4. 测试接线约定

### 4.1 审计 Handler 测试必须挂真实中间件

审计断言不得用测试替身：声明式 Tag API 与中间件收口是一体的，替身会同时绕过「绑定失败仍留痕」「HTTP ≥400 标失败」等被验证的行为。

```go
router := gin.New()
api := router.Group("/api/v1")
api.Use(audithttp.Middleware(auditSvc)) // 真实中间件 + 真实 Service
comp.Handler.RegisterRoutes(api)
```

⚠️ 断言前必须先排空异步队列：`Record` 是非阻塞投递，响应写出时条目可能仍在队列中，直接 `List` 会随机漏项。统一用 `auditSvc.Stop(ctx)` 排空后再查询（见 `internal/{person,camera,algorithm,systemtime}` 的 `drainAudit` 辅助函数）。

### 4.2 `NewAlgorithmComponents` 在构造时捕获 Supervisor

**Symptom**：测试里写 `comp.Supervisor = algorithm.NewSupervisor(...)` 后调用 enable/disable，得到 `fork/exec build/native/host/zhulong_algo_runner: no such file or directory`。

**Cause**：`Service` 持有的是构造时传入的 supervisor 实例，事后替换 `comp.Supervisor` 字段只改了组件包的引用，`Service` 仍指向原实例。

**Fix**：在构造**之前**用 `t.Setenv("ZHULONG_ALGO_RUNNER", stubPath)` 注入 runner，让 `NewAlgorithmComponents` 自行取到桩。

```go
// ❌ 无效：Service 已经捕获了原 supervisor
t.Setenv("ZHULONG_ALGO_RUNNER", stub)
comp := algorithm.NewAlgorithmComponents(db, dir, "cpu", logger)
comp.Supervisor = algorithm.NewSupervisor(comp.Store, dir, stub, "", opts, logger)

// ✅ 正确：构造前注入，字段无需替换
t.Setenv("ZHULONG_ALGO_RUNNER", stub)
comp := algorithm.NewAlgorithmComponents(db, dir, "cpu", logger)
```

### 4.3 Swagger 文档生成（`make api-docs`）跨包类型解析

**Symptom**: `make api-docs` 报错 `cannot find type definition: <pkg>.<Type>`。

**Cause**: 当响应结构体（例如 `record.RecognitionResponse`）直接引用其他包的公开类型（例如 `aitypes.FaceCandidate`）时，Swag CLI 必须通过 `--dir` 显式扫描该被引用的包目录，否则无法构建完整的 OpenAPI Schema。

**Fix**: 在 `Makefile` 的 `api-docs` 目标的 `--dir` 列表中追加被引用的包路径（如 `internal/aitypes`）。


**Prevention**：任何在构造期捕获依赖的组件包（`*Components`）都不支持事后替换字段；需要替换就先改环境变量/参数再构造。runner 桩用 `trap 'exit 0' TERM INT; while true; do sleep 1; done`，保证收到 SIGTERM 即退出且不留孤儿进程。
