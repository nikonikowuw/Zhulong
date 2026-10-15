# 实施计划 (Implement): 人员工号与内部主键解耦

## 1. 实施步骤清单 (Execution Checklist)

### 步骤 1：数据库 Schema 迁移
- [x] 编写 `internal/database/migrations/000009_decouple_person_id.up.sql`
  - 使用 SQLite 表重建语法，解除 `person_id` 的 `NOT NULL` 与 `CHECK` 约束
  - 自动将历史记录中 `person_id = id` 的假数据转换为 `NULL`
  - 建立条件唯一索引 `CREATE UNIQUE INDEX idx_persons_person_id ON persons(person_id) WHERE person_id IS NOT NULL;`
- [x] 编写 `internal/database/migrations/000009_decouple_person_id.down.sql`
  - 使用 SQLite 表重建还原 `NOT NULL CHECK (person_id <> '')`，用 `COALESCE(person_id, id)` 补齐
- [x] 验证迁移生效：运行现有数据库迁移测试

### 步骤 2：Go 后端数据模型与仓储改造 (`internal/person`)
- [x] 修改 `internal/person/model.go`：
  - `Person.PersonID` 改为 `*string`
  - `CreatePersonRequest.PersonID` 与 `UpdatePersonRequest.PersonID` 适配指针支持
- [x] 修改 `internal/person/store.go`：
  - `UpdatePerson` 支持更新 `person_id` 列
  - 冲突探针 probe 适配条件唯一索引
- [x] 调整 `internal/person/store_test.go` 验证持久化行为

### 步骤 3：Go 服务层业务逻辑与底库注销重构 (`internal/person`)
- [x] 修改 `internal/person/service.go`：
  - `CreatePerson`：移除 `if personID == "" { personID = id }` 逻辑，空白一律存为 `nil`
  - `UpdatePerson`：放开限制，当人员未绑定工号时允许写入绑定工号；已有工号且不同时返回 `PERSON_ID_IMMUTABLE`
  - `DeletePerson`：循环所有关联人脸，通过 `BroadcastDelete(ctx, f.ID, "")` 逐一通知原生引擎清空底库；若存在工号同时发送人级别广播
  - `GetGallerySyncData`：未设置工号时传递空字符串 `""`
- [x] 调整并补充 `internal/person/service_test.go` 与 `handler_test.go`
- [x] 验证后端测试：`go test -race ./internal/person/...`

### 步骤 4：算法底库同步与原生引擎校验 (`internal/algorithm`)
- [x] 确保 `internal/algorithm` 中底库广播与解析支持空 `person_id`
- [x] 运行算法测试：`python3 native/scripts/build.py go test -race ./internal/algorithm`

### 步骤 5：React 前端适配与交互优化 (`web/src/features/persons` & `live`)
- [x] 修改 `web/src/features/persons/data/schema.ts`：
  - `personSchema.personId` 支持 `nullable().optional()`
  - `updatePersonSchema` 支持 `personId`
- [x] 修改 `web/src/features/persons/components/persons-columns.tsx`：
  - 列单元格无工号时渲染 `-`
- [x] 修改 `web/src/features/persons/components/persons-action-dialog.tsx`：
  - 编辑态未绑定工号时允许输入并提交补录，已绑定时保持禁用
- [x] 修改 `web/src/features/live/services/ai-result-service.ts`：
  - 抓拍防抖 cooling key 支持空工号回退到姓名
- [x] 运行前端测试：`cd web && pnpm test -- src/features/persons/`

### 步骤 6：全量质量检查 (Make Check)
- [x] 运行 `make check`：覆盖前端 lint/type-check/test、native-test、api-docs、go-check

---

## 2. 检查与验证命令

```bash
# 1. 后端单测与竞争检测
go test -race ./internal/person/...
python3 native/scripts/build.py go test -race ./internal/algorithm

# 2. 前端单测
cd web && pnpm test -- src/features/persons/ src/features/live/

# 3. 最终验收流水线
make check
```
