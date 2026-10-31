# GORM Generics API 迁移技术设计 (Technical Design)

## 1. 架构目标与调用模式映射

本次迁移的目标是将 GORM 底层交互从反射指针传参切换到官方强类型泛型接口 `gorm.G[T]`（GORM >= 1.30.0，Zhulong 现为 v1.31.2），消除 SQL Statement 状态累加污染（SQL pollution），并强化 `context.Context` 生命周期传播。

### 核心方法映射表

| 场景 | 传统反射指针写法 | Generics API 范式 (`gorm.G[T]`) |
| :--- | :--- | :--- |
| **单条查询** | `var m Model`<br>`err := db.Where("id = ?", id).First(&m).Error` | `m, err := gorm.G[Model](db).Where("id = ?", id).First(ctx)`<br>`// 返回 m (值类型 Model)，按需返回 &m` |
| **多条查询** | `var list []Model`<br>`err := db.Where(...).Find(&list).Error` | `list, err := gorm.G[Model](db).Where(...).Find(ctx)`<br>`// 返回 ([]Model, error)` |
| **存在性/计数** | `var count int64`<br>`err := db.Model(&Model{}).Where(...).Count(&count).Error` | `count, err := gorm.G[Model](db).Where(...).Count(ctx, "*")` |
| **单条插入** | `err := db.Create(m).Error` | `err := gorm.G[Model](db).Create(ctx, m)` |
| **批量插入** | `err := db.CreateInBatches(&list, batchSize).Error` | `err := gorm.G[Model](db).CreateInBatches(ctx, &list, batchSize)` |
| **按条件删除** | `err := db.Where("id = ?", id).Delete(&Model{}).Error` | `_, err := gorm.G[Model](db).Where("id = ?", id).Delete(ctx)` |
| **单列更新** | `err := db.Model(&Model{}).Where(...).Update("col", val).Error` | `_, err := gorm.G[Model](db).Where(...).Update(ctx, "col", val)` |
| **实体更新** | `err := db.Model(&m).Updates(m).Error` | `_, err := gorm.G[Model](db).Where("id = ?", m.ID).Updates(ctx, m)` |
| **动态 Map 更新** | `tx.Model(&Model{}).Where(...).Updates(map[string]any{...})` | 保持 `tx.Model(&Model{}).Where(...).Updates(...)` 或使用 `gorm.G[map[string]any](tx).Table("table").Updates(ctx, map)`。对于带 `gorm.Expr` 或动态空值补丁的场景，保持模型定向更新以确保稳定性。 |

---

## 2. 事务与快照一致性设计

### 2.1 事务中的 `gorm.G`
在 `db.Transaction(func(tx *gorm.DB) error { ... })` 闭包中，直接把事务指针 `tx` 传给 `gorm.G[T](tx)`。
- `tx` 是事务作用域的 `*gorm.DB`，`gorm.G[T](tx)` 保证在事务上下文内执行，同时每次生成独立的 `Interface[T]`，杜绝在长事务中链式复用导致 SQL 污染。

### 2.2 快照只读事务（Snapshot Reads）
在 `internal/record/store.go` 中，`listInReadSnapshot` 保证分页与总数在 WAL 快照中原子读取：
```go
// 演进为泛型执行器，同时内部采用 gorm.G[T] 消除 Session 拷贝黑魔法
func listInReadSnapshot[T any](
    ctx context.Context,
    db *gorm.DB,
    build func(g gorm.ChainInterface[T]) gorm.ChainInterface[T],
    window httputil.PageWindow,
) ([]T, int64, error)
```
通过泛型辅助函数，消除原来的 `dest any` 反射断言，直接返回编译期强类型切片 `[]T`。

---

## 3. 错误处理与领域边界

GORM Generics API 在无匹配记录时同样返回 `gorm.ErrRecordNotFound`：
- 在各 Store 实现中，统一使用 `errors.Is(err, gorm.ErrRecordNotFound)` 解包并映射为领域错误（如 `ErrCameraNotFound`, `ErrUserNotFound`）。
- 对外接口返回指针类型（如 `*Camera`, `*User`）时，取 `&m` 返回；未找到时返回 `nil, ErrNotFound`。

---

## 4. 实施策略：分层分批演进

为了规避一次性修改全部模块带来的调试风险，采用分批（Batch）渐进式改造并配合每批单测验证：
1. **Batch 1 (基础领域)**：`auth`, `audit`, `storage`, `asyncjob`。结构清晰、无多表复杂事务，验证基础语法与错误转换。
2. **Batch 2 (轻中度领域)**：`algorithm`, `spatial`, `gb28181`。包含外键关联、枚举与过滤。
3. **Batch 3 (核心业务领域)**：`camera`, `task`, `person`。涉及乐观锁（`Revision`）、父子表事务级联、特征向量与快照回填。
4. **Batch 4 (高吞吐感知领域)**：`record`。涉及百万级事件日志、批量插入、时序索引与读快照分页。
5. **Batch 5 (规范固化)**：更新 `.trellis/spec/backend/database-guidelines.md` 并运行全局检查。
