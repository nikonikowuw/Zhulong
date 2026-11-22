---
trigger: glob
globs:
  - 'internal/database/**'
  - 'internal/**/*store*.go'
priority: 100
description: 保持 SQLite 迁移可升级、可回滚，并避免领域 store 绑定具体连接。
---

# SQLite 持久化

- Schema 变更追加同一版本号的 `.up.sql` / `.down.sql`，不修改已应用迁移，不使用 GORM `AutoMigrate`。见 [嵌入式迁移入口](../../../internal/database/migrations.go#L16-L56)和[最近的迁移上行](../../../internal/database/migrations/000038_create_event_clips.up.sql)、[下行](../../../internal/database/migrations/000038_create_event_clips.down.sql)。
- 领域 store 的构造依赖 `database.DBProvider`，不是直接注入 `*gorm.DB`。池只有一个活动连接，事务内的查询须复用同一个 `tx`，不能调用会从池重新取连接的外层方法。显式 `false` 等零值必须落库；列更新用 map 或显式选择字段。见 [摄像头 store 构造](../../../internal/camera/store.go#L39-L48)、[连接池](../../../internal/database/store.go#L141-L181)、[读事务示例](../../../internal/record/store.go#L48-L115)和[显式布尔值](../../../internal/task/store.go#L359)。
