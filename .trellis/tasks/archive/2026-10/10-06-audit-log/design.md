# 技术设计：轻量级系统操作与安全审计日志模块 (Design Document)

## 1. 架构总览与分层设计

本项目面向单用户/边缘嵌入式智能盒子，审计日志设计原则为：**高内聚、低开销、异步非阻塞、防爆盘保护**。

```
[前端 React 控制台]
   │  ▲
   │  │ HTTP (REST + Zod 校验)
   ▼  │
[Gin API 层 (/api/v1/audit/logs)] ── RequireAuth 鉴权
   │
[AuditService (Go)] ── 异步 Buffer 通道 (Worker Pool) ──> [GORM AuditStore]
   ▲                                                            │
   │ 依赖注入调用                                                ▼
[AuthHandler / CameraHandler]                           [SQLite Database]
   • auth.login / logout / init                          (000004_create_audit_logs)
   • camera.create / update / delete / toggle            (FIFO 自动滚动淘汰保护)
```

## 2. 后端设计 (internal/audit)

### 2.1 数据库结构与版本化迁移
新增迁移文件：`000004_create_audit_logs_table.up.sql` 与 `000004_create_audit_logs_table.down.sql`。

```sql
CREATE TABLE IF NOT EXISTS audit_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at DATETIME NOT NULL,
    ip TEXT NOT NULL DEFAULT '',
    username TEXT NOT NULL DEFAULT 'admin',
    action TEXT NOT NULL,
    target TEXT NOT NULL DEFAULT '',
    detail TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'success',
    error_msg TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON audit_logs (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_action ON audit_logs (action);
CREATE INDEX IF NOT EXISTS idx_audit_logs_status ON audit_logs (status);
```

### 2.2 存储与防爆盘淘汰 (FIFO Rolling Retention)
- **容量上限**：默认最大保留 `MaxAuditEntries = 5,000` 条记录。
- **淘汰算法**：在异步 Worker 处理批量写入后，若检测到计数超过安全阈值（或每 N 次写入检测一次），执行高效的行淘汰 SQL：
  ```sql
  DELETE FROM audit_logs WHERE id IN (
      SELECT id FROM audit_logs ORDER BY created_at ASC LIMIT ?
  );
  ```
- 提供手动清理 API：`Clear(ctx) error`（删除全部或指定时间之前的日志）。

### 2.3 异步写入管道 (Worker & Buffer)
- `AuditService` 维护容量为 512 的 `chan AuditEntry` 缓冲通道。
- 业务埋点处调用 `Record(entry AuditEntry)` 为纯非阻塞操作（若通道满则静默记录 warning 并丢弃最旧，绝不阻塞主 HTTP 请求）。
- 单个后台 goroutine 批量收集日志并批量写入 SQLite（通过 GORM 事务或批量 Insert），降低嵌入式 Flash/eMMC 写入频次。
- 优雅停机：实现 Fx Lifecycle `OnStop`，确保关机前 drain 缓冲区未写完的日志。

### 2.4 业务模块埋点规范
统一 `action` 命名空间规范：
- `auth.init`: 管理员初始化。
- `auth.login`: 管理员登录（成功记录 IP，失败记录错误原因及尝试用户）。
- `auth.logout`: 管理员登出。
- `camera.create`: 摄像头添加（target: `camera:<id>`, detail: 名称与拉流协议）。
- `camera.update`: 摄像头配置修改（target: `camera:<id>`, detail: 变更字段）。
- `camera.delete`: 摄像头删除（target: `camera:<id>`）。
- `camera.toggle`: 摄像头启用/停用切换。

### 2.5 API 契约
- `GET /api/v1/audit/logs`
  - Query 参数：
    - `page` (默认 1), `pageSize` (默认 20, 最大 100)
    - `action` (可选，模糊或精准动作)
    - `status` (可选，`success` | `failed`)
    - `startTime`, `endTime` (可选，RFC3339 时间)
  - 响应：
    ```json
    {
      "code": "OK",
      "data": {
        "items": [...],
        "total": 128,
        "page": 1,
        "pageSize": 20
      }
    }
    ```
- `DELETE /api/v1/audit/logs`
  - 响应：清空结果或清理数量。

---

## 3. 前端设计 (web/src/features/audit)

### 3.1 模块结构与职责
```
web/src/features/audit/
├── api/
│   └── auditApi.ts           # REST 接口调用封装，Zod schema 解析
├── components/
│   ├── AuditLogPage.tsx      # 主视图容器，包含标题栏、操作按钮、统计与主布局
│   ├── AuditLogFilterBar.tsx # 动作类型、状态下拉框、时间选择及刷新按钮
│   ├── AuditLogTable.tsx     # 结构化表格、状态 Badge、动作高亮与分页器
│   └── AuditLogDetailModal.tsx # 格式化 JSON / 变更明细弹窗
├── locales/                  # 国际化词条 (en.json, zh-Hans.json, zh-Hant.json)
├── types.ts                  # Zod 实体校验与 TypeScript 类型
└── index.ts                  # 特性统一导出
```

### 3.2 导航与交互打磨
- **侧边栏集成**：在 `Sidebar.tsx` 的 `navItems` 中新增 `audit` 选项，使用 `ScrollText` 图标，保持与其他项的视觉节奏一致。
- **页面状态管理**：使用 TanStack Query 模式封装 `useAuditLogsQuery`，支持自动刷新与缓存失效。
- **多语言**：在 `zh-Hans`, `zh-Hant`, `en` 中提供完整的本地化文案，包括动作枚举的友好展示（如 `auth.login` -> "管理员登录"）。

---

## 4. 兼容性与安全性考量
1. **单一写锁与并发安全**：SQLite 采用 WAL 模式并限制单写连接，异步批量写入可有效减少写锁冲突。
2. **密码与敏感信息脱敏**：在记录 `auth.login` 或 `camera.create` 审计详情时，密码与 token 必须在构造 entry 时过滤脱敏，禁止将明文密码写入审计表。
3. **接口鉴权**：除登录接口埋点外，所有审计日志的查询与清理接口均受 `auth.RequireAuth` 保护。
