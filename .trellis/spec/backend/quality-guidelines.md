# Go 后端质量门禁与测试规范

> 质量门禁命令、并发安全（-race）与禁止反模式。

---

## 1. 质量检验命令与门禁标准

```bash
make go-check
```

格式化时仅对本次修改的 Go 文件运行 `gofmt -w`。需要单独测试依赖 `internal/engine` 的包时，使用 native wrapper；例如：

```bash
python3 native/scripts/build.py go test -race ./internal/camera
```

不要裸跑间接依赖 `internal/engine` 的 Go 命令，以免复用陈旧 Engine/FFmpeg 归档。`make go-check` 会只读检查 `cmd/` 与 `internal/` 的格式，并通过 Native 构建脚本运行 vet、race 测试和 Go/native 桥接测试。

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

### 4.2 `*Components` 构造期捕获依赖，测试必须从输入侧注入

**Symptom**：测试里写 `comp.Supervisor = algorithm.NewSupervisor(...)` 或 `comp.Verifier = ...` 后再调用 enable/disable 或 upload，行为没有变化，甚至去 fork 一个不存在的 runner 而报 `no such file or directory`。

**Cause**：`NewAlgorithmComponents` 在构造时把 `supervisor`/`verifier` 传给了 `Service`，`Service` 持有的是**原实例**。事后替换组件包上的字段只改了 `AlgorithmComponents` 的引用，`Service` 仍指向旧对象。

**Fix**：构造函数保持**纯粹**——不读环境变量、不拼路径，把 host 侧输入（`algorunner.Runtime`、`algorithm.HostPlatform`）作为参数传进去。测试想要不同图（静态验证 + 桩 runner）就在构造前把参数造好。

```go
// ❌ 无效：Service 已经捕获了原实例，字段替换对它无效
comp := algorithm.NewAlgorithmComponents(db, rt, host, logger)
comp.Verifier = algorithm.NewVerifier("", host)

// ✅ 正确：从输入侧决定，构造出的图从一开始就是想要的形状
rt := algorunner.Runtime{
	AlgorithmsDir: dataDir,
	RunnerPath:    stubPath, // 只给 supervisor 用
	// VerifyRunnerPath 留空 → 验包只跑静态检查
}
comp := algorithm.NewAlgorithmComponents(db, rt, x86Host(), logger)
```

`Runtime` 把两个 runner 角色分开：`RunnerPath` 是 supervisor 起进程服务的二进制，`VerifyRunnerPath` 是验包时以 `--verify-plugin` 调用的那一个。两者在 `internal/app` 的 `newAlgorithmRuntime` 里解析为同一个值；测试里则常常只需要其一。

**Prevention**：

- 任何在构造期捕获依赖的组件包（`*Components`）都不支持事后替换字段；需要更换协作者就改**构造输入**，不要改字段。
- `Verifier` 构造后不可变（host / runnerPath / timeout 全部私有且一次捕获），因此不存在「改一半字段导致判定不一致」的可能；要不同行为就换实例。
- runner 桩用 `trap 'exit 0' TERM INT; while true; do sleep 1; done`，保证收到 SIGTERM 即退出且不留孤儿进程。

### 4.3 Swagger 文档生成（`make api-docs`）跨包类型解析

**Symptom**: `make api-docs` 报错 `cannot find type definition: <pkg>.<Type>`。

**Cause**: 当响应结构体（例如 `record.RecognitionResponse`）直接引用其他包的公开类型（例如 `aitypes.FaceCandidate`）时，Swag CLI 必须通过 `--dir` 显式扫描该被引用的包目录，否则无法构建完整的 OpenAPI Schema。

**Fix**: 在 `Makefile` 的 `api-docs` 目标的 `--dir` 列表中追加被引用的包路径（如 `internal/aitypes`）。


**Prevention**：任何在构造期捕获依赖的组件包（`*Components`）都不支持事后替换字段；需要替换就先改环境变量/参数再构造。runner 桩用 `trap 'exit 0' TERM INT; while true; do sleep 1; done`，保证收到 SIGTERM 即退出且不留孤儿进程。

### 4.4 多路扇出的过滤/变换必须对每一路都断言

⚠️ **症状**：`StreamHub.consumeAIResults` 这类「一次变换 → N 路分发」的汇聚点，测试只注册了其中一路（如 `eventHub`），另一路（如 `aiConsumer` 落库）的过滤/变换正确性完全未被守护。此时把过滤挪到某一路之后仍会全绿。

**修法**：为**每一路**分发目标注册记录型替身，各自对同一硬编码期望值断言。这比「两路互相比对」鉴别力更强 —— 不仅能发现单路漏过滤，也能发现两路同错。

```go
// 记录型替身：mutex 保护 + 快照深拷贝，避免与分发 goroutine 竞争
type recordingAIResultConsumer struct {
    mu      sync.Mutex
    results []aitypes.InferenceResult
}

func (c *recordingAIResultConsumer) ProcessInferenceResult(_ context.Context, res aitypes.InferenceResult) error {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.results = append(c.results, res)
    return nil
}

// 断言两路各自都只看到过滤后的 1 条（而非只断言 eventHub）
hub.SetAIConsumer(consumer)
// ... 喂入 3 条目标，其中 2 条应被 ROI/MASK 过滤 ...
```

**变更鉴别力自检（推荐做法）**：临时把过滤从某一路挪走（或让某一路拿到未过滤结果），确认该路断言**必定失败**，再还原。只靠「测试存在」无法证明它真的守住了契约。

⚠️ 同理适用于断言前的异步排空（见 §4.1）：消费者调用与广播虽在同一 goroutine 内先后发生，仍应用**有界轮询**等待替身记录，而非依赖调用顺序的隐含假设。

### 4.5 存储层错误分支：用 GORM 回调注入故障，不要 mock ORM

要验证 `fmt.Errorf("...: %w", err)` 这类「驱动失败必须被包装并向上传播」的分支，**不要**引入 `go-sqlmock` 或给 Store 抽 Repository 接口：

- `database.DBProvider.DB()` 返回具体类型 `*gorm.DB`，为了 mock 要么改生产接口，要么拼 `sqlite.Dialector{DriverName: "sqlmock"}` 的 DSN hack；
- 本仓既有范式是**真 SQLite**（`t.TempDir()` + `database.New` + `OpenAndMigrate`），再引一套 mock 等于第二种测试范式并存；
- 驱动是 cgo 的 `mattn/go-sqlite3`，mock 掉 database/sql 层则 SQL 方言差异全被跳过。

正确做法是在测试里给**同一个真实 `*gorm.DB`** 注册回调注入错误（`internal/task/store_fault_test.go` 是参考实现）：

```go
// 让 CREATE 处理器的第 nth 次调用返回 fault；callbacks 由 session/tx 共享，注册在根 db 上即可生效。
hook := func(tx *gorm.DB) { calls++; if calls == nth { tx.AddError(fault) } }
db.Callback().Create().Before("gorm:create").Register("zl:test_fault", hook)
```

要点：

- **处理器要选对**：普通 SELECT 走 `Query()`，但 `(*DB).Scan(...)` 走 `Row()`，`Raw`/`Exec` 走 `Raw()`。选错会出现「注入了故障但用例仍通过」的假绿。
- **精确定位**：优先按 `tx.Statement.Dest` 的类型匹配（如只打中 `*TaskAlgorithm` 的查询），比数第几次更抗后续改动；确实只能计数时，用注释写清该流程的查询顺序。
- **模拟竞态**：用 `db.Callback().Delete().Replace("gorm:delete", func(tx *gorm.DB){ tx.RowsAffected = 0 })` 让「读到了但删不到」的 TOCTOU 分支可复现。
- **断言错误链**：用 `errors.Is(err, fault)` 验证 cause 未被吞掉，用 `errors.As(err, &apperr.Error)` 验证机器码；不要断言 `fmt.Errorf` 的包装文案。
- 纯映射函数（如 `mapWriteError` / `mapTaskError`）根本不需要触库，直接喂合成 error 即可。
