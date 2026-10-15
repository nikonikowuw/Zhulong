# 实施计划：统一审计动作命名空间

## 实施检查清单

- [x] **Step 1: 编写数据库迁移脚本**
  - 创建 `internal/database/migrations/000010_normalize_audit_actions.up.sql`
  - 创建 `internal/database/migrations/000010_normalize_audit_actions.down.sql`
  - 验证命令：`go test -race ./internal/database/...`

- [x] **Step 2: 更新后端常量定义**
  - 修改 `internal/systemtime/model.go`：移除 `ActionTime*` 中的 `system.` 前缀
  - 修改 `internal/storage/model.go`：移除 `ActionStorage*` 中的 `system.` 前缀

- [x] **Step 3: 更新后端单元测试与相关断言**
  - 更新 `internal/audit/service_test.go`
  - 更新 `internal/audit/actor_test.go`
  - 更新 `internal/audit/http/scope_test.go`
  - 验证命令：
    ```bash
    go test -race ./internal/audit/...
    go test -race ./internal/systemtime/...
    go test -race ./internal/storage/...
    go test -race ./internal/app/...
    ```

- [x] **Step 4: 对齐前端 i18n 与组件选项**
  - 规范 `web/src/features/audit/locales/zh-Hans.json`、`en.json`、`zh-Hant.json` 中的 `actionNames`
  - 规范 `web/src/features/audit/components/AuditTable.tsx` 的 `actionOptions`
  - 更新 `web/src/features/audit/components/AuditTable.test.tsx` 对应测试用例
  - 验证命令：
    ```bash
    cd web && pnpm test -- src/features/audit
    cd web && pnpm tsc -b && pnpm lint
    ```

- [x] **Step 5: 全量回归与构建检查**
  - 验证命令：
    ```bash
    make go-check
    cd web && pnpm build
    ```

## 回滚计划 (Rollback)

如需回滚代码与数据：
1. 数据库层面应用 `000010_normalize_audit_actions.down.sql` 还原表中的动作名称。
2. Git 回滚相关 commit：`git revert HEAD`。
