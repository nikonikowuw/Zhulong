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

```go
// 必须配置的关键 PRAGMA 参数
dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON"
db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{PrepareStmt: true})

sqlDB, _ := db.DB()
sqlDB.SetMaxOpenConns(10)
sqlDB.SetMaxIdleConns(5)
sqlDB.SetConnMaxLifetime(time.Hour)
```

- **`journal_mode=WAL`**：写前日志，并发读不阻塞写，写不阻塞读。
- **`busy_timeout=5000`**：锁冲突时自动重试最长 5 秒，消除并发写锁偶发报错。
- **`foreign_keys=ON`**：强制外键完整性约束。

---

## 3. 持久化规约与防爆边界

1. **业务自管 Store**：数据操作收敛在各模块 `internal/<module>/store.go`，禁止跨模块大表模型。
2. **强制绑定上下文**：所有 GORM 查询必须使用 `.WithContext(ctx)` 支持超时取消。
3. **短事务铁律**：事务内只允许纯数据操作，**绝对严禁在事务内部调用 CGO 推理接口或网络 RTSP 请求**，防止 SQLite 数据库被长时间独占锁死。
