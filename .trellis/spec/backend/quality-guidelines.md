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
