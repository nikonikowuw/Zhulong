# 前端审计日志视图与溯源管理技术设计 (Technical Design)

## 1. 架构与目录拓扑

依据 `.trellis/spec/frontend/directory-structure.md` 规范，所有审计日志相关的数据模型、接口调用、私有 Hook、UI 组件及国际化资源统一切片至 `web/src/features/audit/`：

```txt
web/src/
├── features/
│   └── audit/
│       ├── api/
│       │   └── audit-api.ts         # REST API 客户端封装 (GET / DELETE)
│       ├── components/
│       │   ├── AuditClearDialog.tsx       # 清空/清理日志确认对话框
│       │   ├── AuditColumns.tsx           # TanStack Table 列定义 (Badge, 操作按钮)
│       │   ├── AuditColumnsCells.tsx      # 单元格与状态徽章
│       │   ├── AuditDateRangePicker.tsx   # RFC3339 时间范围选择器
│       │   ├── AuditDetailDialog.tsx      # 日志明细抽屉/弹窗 (JSON 高亮查看器)
│       │   ├── AuditPrimaryButtons.tsx    # 顶部操作区 (刷新、清理日志入口)
│       │   ├── AuditProvider.tsx          # 局部 UI 状态 Context
│       │   ├── AuditTable.tsx             # 表格核心容器 (分页、加载骨架、空状态)
│       │   └── AuditToolbar.tsx           # 过滤器工具栏 (动作、状态、时间范围)
│       ├── hooks/
│       │   └── use-audit-logs.ts    # TanStack Query 封装 (列表查询与清理 Mutation)
│       ├── locales/
│       │   ├── en.json              # 英文语言包
│       │   ├── zh-Hans.json         # 简体中文语言包
│       │   └── zh-Hant.json         # 繁体中文语言包
│       ├── data/
│       │   └── schema.ts            # Zod 模式与 TypeScript 类型声明
│       └── index.tsx                # 特性主页面导出
├── routes/
│   └── _authenticated/
│       └── audit/
│           └── index.tsx            # TanStack Router 路由包装与 Search 参数校验
└── components/
    └── layout/
        └── data/
            └── sidebar-data.ts      # 侧边栏导航条目挂载
```

---

## 2. 数据流与状态管理设计

### 2.1 服务端状态独占原则 (Server State)
- 使用 `@tanstack/react-query` 统一接管服务端状态，Query Key 格式：`['audit-logs', queryParams]`。
- 严禁将返回的列表数据复制存储到 Zustand 或全局客户端状态中。
- `clearAuditLogs` 触发成功后，通过 `queryClient.invalidateQueries({ queryKey: ['audit-logs'] })` 自动触发失效并拉取最新数据。

### 2.2 表格状态与 URL Search 参数双向同步
- 使用项目内置的 `hooks/use-table-url-state.ts`，将表格分页（`page`, `pageSize`）、动作过滤（`action`）、状态过滤（`status`）与 URL 同步。
- 筛选状态直接由当前 Router Search 派生，浏览器前进/后退、刷新或分享链接都恢复相同状态。
- Search 参数采用 Zod 在路由文件 `routes/_authenticated/audit/index.tsx` 的 `validateSearch` 中进行强类型校验和容错兜底：
  ```ts
  const auditSearchSchema = z.object({
    page: z.number().int().positive().optional().catch(1),
    pageSize: z.number().int().positive().max(100).optional().catch(20),
    action: z.union([z.array(z.string()), z.string()]).optional().catch([]),
    status: z.union([z.array(z.enum(['success', 'failed'])), z.enum(['success', 'failed'])]).optional().catch([]),
    preset: z.enum(['15m', '30m', '1h', '6h', '24h', 'today', '7d', '30d']).optional().catch(undefined),
    startTime: z.iso.datetime({ offset: true }).optional().catch(''),
    endTime: z.iso.datetime({ offset: true }).optional().catch(''),
  })
  ```
- Search 参数不包含用户/目标/IP 全局搜索；action 选项内的模糊查找由筛选控件完成。
- URL 用 `action` 保存多选值，API 客户端将其映射为重复的 `actions` query key；只传旧版单值 `action` 的客户端继续兼容。若请求同时带非空 `actions` 和 `action`，后端以 `actions` 为准。
- `pageSize` 仅接受 `1..100`；URL 值超出范围时前端回退到默认 `20`，与 API 默认页大小一致。

### 2.3 服务端多 action 过滤
- 请求 `action=<value>` 保持现有单值语义；多选使用重复的 `actions=<value>` 查询参数，多个值按 OR 组合，再与 status 和时间条件按 AND 组合。计数、排序与分页全部在后端同一查询中完成，前端不再对当前页做筛选。
- 新的 `actions` 参数是可选扩展，既有单值客户端和响应结构保持不变；不增加数据库字段或迁移。

---

## 3. UI 交互与组件规范

### 3.1 状态徽章 (Status Badge) 严格规范
遵循 `.trellis/spec/frontend/theme-and-i18n.md` 的 **Dot-Indicator Outline** 标准：
- **执行成功 (`success`)**：
  - 外框与底色：`border-teal-200 bg-teal-100/30 text-teal-900 dark:border-teal-800 dark:bg-teal-950/30 dark:text-teal-200`
  - 指示圆点：`bg-emerald-500`
- **执行失败 (`failed`)**：
  - 外框与底色：`border-destructive/20 bg-destructive/10 text-destructive dark:border-destructive/40 dark:bg-destructive/20 dark:text-destructive`
  - 指示圆点：`bg-destructive`

### 3.2 详情查看 (`AuditDetailDialog`)
- 支持查看操作日志的完整元数据：ID、时间、操作者、Action、Target、IP、执行结果。
- 详情内容（`detail`）多为 JSON 字符串（如修改前后的配置对比），支持自动 JSON 解析、缩进排版和语法高亮，提供「一键复制」功能。
- 若有 `errorMsg`，采用 Destructive 警示卡片醒目呈现。

### 3.3 日志清理 (`AuditClearDialog`)
- 提供破坏性操作二次确认：
  - 清理全部历史日志
  - 或清理 7 天前 / 30 天前日志（调用 `DELETE /api/v1/audit/logs?before=...`）
- 操作成功后调用 `toast.success` 并刷新列表。

---

## 4. 三语国际化与本地化 (i18n)

- 文本完全抽取至 `audit` 命名空间（`en.json`, `zh-Hans.json`, `zh-Hant.json`），在 `src/lib/i18n.ts` 中完成挂载。
- 时间展示：后端统一返回 UTC RFC3339 字符串，前端使用 `Intl.DateTimeFormat` 转换为用户当前本地时区格式（年月日 时分秒）。
- 逻辑属性：所有外内边距强制采用 `ps-*`, `pe-*`, `ms-*`, `me-*`，确保 RTL 布局自然翻转。

---

## 5. 质量保证与构建验证

- **类型安全**：零 `any`，严格遵循后端 `AuditLogDTO`。
- **构建测试**：
  - `pnpm lint` 校验无语法或未适配规则。
  - `pnpm build` 运行 `tsc -b` 并打包。
