# 统一审计动作命名空间设计文档

## 1. 背景与问题定义

当前项目中，大部分模块的审计事件均以顶级业务领域作为命名空间前缀（例如 `camera.create`, `person.create`, `auth.login`, `algorithm.install`）。
然而，`systemtime` 与 `storage` 模块在早期实现中，误将 HTTP URL 前缀（`/system/...`）混入动作名，生成了 `system.time.*` 和 `system.storage.*`。

这种分歧导致了以下问题：
1. **语义冗余与不对称**：存储与时间的人工配置操作被误标记为系统内部命名空间，与其他业务域不协调。
2. **前后端契约断裂**：前端 i18n 资源和筛选组件最初按标准领域命名 `time.*` 和 `storage.*` 开发，导致历史日志在详情和表格中无法查表（触发 fallback 显示 raw key），且在按动作筛选时无法匹配到数据库中的记录。

## 2. 契约重构映射矩阵

### 时间模块 (`internal/systemtime`)
| 现常量名 | 原动作名 (`Action`) | 规范动作名 (`New Action`) |
| :--- | :--- | :--- |
| `ActionTimeUpdateConfig` | `system.time.update_config` | `time.update_config` |
| `ActionTimeManualSet` | `system.time.manual_set` | `time.manual_set` |
| `ActionTimeNTPSync` | `system.time.ntp_sync` | `time.ntp_sync` |
| `ActionTimeRTCHeal` | `system.time.rtc_heal` | `time.rtc_heal` |

### 存储模块 (`internal/storage`)
| 现常量名 | 原动作名 (`Action`) | 规范动作名 (`New Action`) |
| :--- | :--- | :--- |
| `ActionStorageUpdateConfig` | `system.storage.update_config` | `storage.update_config` |
| `ActionStorageManualCleanup` | `system.storage.manual_cleanup` | `storage.manual_cleanup` |
| `ActionStoragePathTest` | `system.storage.test_path` | `storage.test_path` |
| `ActionStorageEmergencyStop` | `system.storage.emergency_stop` | `storage.emergency_stop` |
| `ActionStorageEmergencyResume` | `system.storage.emergency_resume` | `storage.emergency_resume` |

## 3. 架构设计与实现方案

### 3.1 数据库迁移 (`000010_normalize_audit_actions`)
采用 `golang-migrate` 机制，在 `internal/database/migrations/` 下添加两段式 SQL：

**`up.sql`**：
```sql
UPDATE audit_logs SET action = 'time.update_config' WHERE action = 'system.time.update_config';
UPDATE audit_logs SET action = 'time.ntp_sync' WHERE action = 'system.time.ntp_sync';
UPDATE audit_logs SET action = 'time.manual_set' WHERE action = 'system.time.manual_set';
UPDATE audit_logs SET action = 'time.rtc_heal' WHERE action = 'system.time.rtc_heal';

UPDATE audit_logs SET action = 'storage.update_config' WHERE action = 'system.storage.update_config';
UPDATE audit_logs SET action = 'storage.manual_cleanup' WHERE action = 'system.storage.manual_cleanup';
UPDATE audit_logs SET action = 'storage.test_path' WHERE action = 'system.storage.test_path' OR action = 'system.storage.path_test';
UPDATE audit_logs SET action = 'storage.emergency_stop' WHERE action = 'system.storage.emergency_stop';
UPDATE audit_logs SET action = 'storage.emergency_resume' WHERE action = 'system.storage.emergency_resume';
```

**`down.sql`**：
逆向将规范名称恢复为 `system.` 前缀。

### 3.2 后端 Go 变更
1. `internal/systemtime/model.go`：修改 `ActionTime*` 常量值。
2. `internal/storage/model.go`：修改 `ActionStorage*` 常量值。
3. `internal/audit/` 及相关测试：同步测试中的固定断言字符串。

### 3.3 前端变更
1. 语言包字典（`web/src/features/audit/locales/{zh-Hans,en,zh-Hant}.json`）：
   统一为 `time.*` 和 `storage.*`（以及 `storage.test_path` / `audit.clear`）。
2. 表格组件（`web/src/features/audit/components/AuditTable.tsx`）：
   `actionOptions` 中的 `value` 与 i18n key 统一使用规范形式。

## 4. 权衡与兼容性 (Tradeoffs & Compatibility)

- **为什么不保留双写/双查？**
  数据迁移已通过版本化迁移脚本在应用启动时自动原子执行，SQLite 单机模式下无需复杂的双写过渡。清理冗余别名有助于避免将来新增动作时产生心智负担。
- **对已发布 API 的影响**：
  审计日志查询接口 `GET /api/v1/audit/logs?action=...` 查询参数的值将与新的动作名保持一致。
