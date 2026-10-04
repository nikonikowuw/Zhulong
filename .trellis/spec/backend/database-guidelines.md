# SQLite 与 GORM 数据库开发规范

> SQLite 持久化、版本化迁移、连接参数与并发事务规则。

---

## 1. 核心铁律：版本化自动迁移 (Versioned Migrations)

1. **绝对禁止 GORM `AutoMigrate`**：生产环境严禁自动建表改表，避免历史数据破坏。
2. **应用启动自动迁移**：
   - 迁移脚本放置于 `internal/database/migrations/`（如 `000001_init.up.sql`），通过 `//go:embed` 打入二进制。
   - **在 Fx `OnStart` 阶段、对外监听流量和启动 Pipeline 之前执行迁移**。
   - ⚠️ **迁移失败必须阻断启动并退出**，严禁在损坏/未就绪的 Schema 上运行业务。
3. **零手动 SQL**：嵌入式设备禁止要求终端用户手动敲 SQL 脚本升级。

---

## 2. 连接池与 PRAGMA 配置

当前宿主将数据库文件命名为 `<data-dir>/zhulong.db`，DSN 使用 SQLite file URL，并配置 `_busy_timeout=5000`、`_foreign_keys=on` 和 `_journal_mode=WAL`。GORM 连接池固定为一个 open/idle connection，避免本地单进程骨架打开多个写连接；GORM 日志级别为 Silent。

```go
dsn := "file:///absolute/path/zhulong.db?_busy_timeout=5000&_foreign_keys=on&_journal_mode=WAL"
db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
    Logger: gormlog.Default.LogMode(gormlog.Silent),
    NowFunc: func() time.Time { return time.Now().UTC() },
})
sqlDB, err := db.DB()
sqlDB.SetMaxOpenConns(1)
sqlDB.SetMaxIdleConns(1)
```

迁移器另用 `database/sql` 打开迁移连接，并将连接数设为 1；它使用嵌入的 `iofs` SQL migrations 和 `golang-migrate` SQLite driver。迁移成功后才将 GORM Store 标记 Ready。

- **`journal_mode=WAL`**：允许并发读取与单写入者模式。
- **`busy_timeout=5000`**：SQLite 锁冲突等待最长 5 秒。
- **`foreign_keys=on`**：启用外键约束。

---

## 3. 时间与时区规范

- 所有表示绝对时刻的字段（如 `created_at`、`updated_at`）统一按 **UTC** 存储，Go 模型使用 `time.Time`；禁止保存依赖宿主本地时区的墙上时间。
- GORM 配置 `NowFunc` 返回 `time.Now().UTC()`；模型使用 `CreatedAt` / `UpdatedAt` 字段，让 GORM 在常规 Create / Updates 操作中维护时间戳。原始 SQL 写入或更新必须显式提供或更新时间。
- SQLite 的 `DATETIME` 列按 UTC 解释。SQLite `CURRENT_TIMESTAMP` 也是 UTC，但只有秒精度且不带时区后缀；需要亚秒精度时使用 GORM 写入的 UTC `time.Time`，不要依赖该默认值。
- 纯日期使用 `DATE` / `YYYY-MM-DD`，不做时区转换；持续时间使用带明确单位的数值字段（如 `duration_ms`），不要当作时间点存储。

## 4. 持久化规约与防爆边界

1. **业务自管 Store**：数据操作收敛在各模块 `internal/<module>/store.go`，禁止跨模块大表模型。
2. **强制绑定上下文**：所有 GORM 查询必须使用 `.WithContext(ctx)` 支持超时取消。
3. **短事务铁律**：事务内只允许纯数据操作，**绝对严禁在事务内部调用 CGO 推理接口或网络 RTSP 请求**，防止 SQLite 数据库被长时间独占锁死。
