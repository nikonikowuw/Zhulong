# 统一审计动作命名空间并迁移历史记录

## Goal

消除审计日志中动作名称的命名空间不一致性，将存量的 `system.time.*` 和 `system.storage.*` 重构为符合项目整体标准的纯领域命名空间 `time.*` 和 `storage.*`，并通过 SQLite 版本化数据库迁移平滑更新历史记录，实现前后端契约统一。

## Requirements

1. **命名空间规范化**：
   - 将 `internal/systemtime` 审计动作从 `system.time.*` 规范为 `time.*`（`time.update_config`, `time.ntp_sync`, `time.manual_set`, `time.rtc_heal`）。
   - 将 `internal/storage` 审计动作从 `system.storage.*` 规范为 `storage.*`（`storage.update_config`, `storage.manual_cleanup`, `storage.test_path`, `storage.emergency_stop`, `storage.emergency_resume`）。
   - 保证审计动作格式与 `camera.*`, `person.*`, `auth.*`, `algorithm.*` 严格保持一致（即 `<domain>.<resource>.<verb>` 或 `<domain>.<verb>`）。

2. **数据库历史数据平滑迁移**：
   - 新增编号数据库迁移 `000010_normalize_audit_actions.up.sql` 和 `000010_normalize_audit_actions.down.sql`。
   - `up.sql` 将 `audit_logs` 表中历史的 `system.time.%` 和 `system.storage.%` 批量更新为新命名。
   - `down.sql` 提供完整的反向回滚能力。

3. **前后端契约完全对齐**：
   - 前端国际化字典（`zh-Hans.json`, `en.json`, `zh-Hant.json`）清理冗余前缀，统一使用规范动作 key。
   - 前端审计日志表格筛选器（`AuditTable.tsx`）的选项值与后端落库动作保持 100% 一致。

4. **零破坏与高质量验证**：
   - 所有依赖上述动作常量的后端单元测试与跨模块测试全部对齐并通过。
   - 前端组件测试、lint 与 build 全部通过。

## Acceptance Criteria

- [x] 数据库迁移 `000010_normalize_audit_actions.up.sql` 与 `down.sql` 可在 SQLite 环境下正确应用和回滚。
- [x] 后端 `internal/systemtime` 与 `internal/storage` 导出的审计动作常量不包含 `system.` 前缀。
- [x] 后端全量测试通过（`go test -race ./internal/audit/... ./internal/systemtime/... ./internal/storage/... ./internal/app/...`）。
- [x] 前端测试与构建通过（`pnpm test -- src/features/audit`、`pnpm build`）。
- [x] 在前端审计日志表格中，对时与存储相关事件正常展示翻译文本，且按“NTP 对时同步”与“更新存储配置”等筛选时能精准命中目标记录。
