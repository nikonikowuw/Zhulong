# 执行计划：人员管理前端界面 (Person Management Frontend)

## 执行步骤清单

### 步骤 1：数据模型与 API 层实现
- [x] 1.1 创建 `web/src/features/persons/data/schema.ts`（定义 Person, Face, Create/Update/Face DTO 及 Zod Schemas）
- [x] 1.2 编写 `web/src/features/persons/data/schema.test.ts`（验证 Schema 解析与错误约束）
- [x] 1.3 创建 `web/src/features/persons/api/persons-api.ts`（封装与后端 `/persons` 对接的 API 方法）
- [x] 1.4 编写 `web/src/features/persons/api/persons-api.test.ts`（验证 API 客户端调用与参数映射）
- [x] 1.5 创建 `web/src/features/persons/hooks/use-persons.ts`（封装 TanStack Query hooks 与 mutation invalidation）
- 验证命令：`cd web && pnpm test -- src/features/persons/`

### 步骤 2：国际化语言包与全局挂载
- [x] 2.1 创建 `web/src/features/persons/locales/zh-Hans.json`、`zh-Hant.json`、`en.json`
- [x] 2.2 在 `web/src/lib/i18n.ts` 中注册 `persons` 命名空间资源与三语映射
- 验证命令：`cd web && pnpm test -- src/lib/i18n.test.ts`（或类型检查 `pnpm tsc --noEmit`）

### 步骤 3：UI 组件切片实现
- [x] 3.1 创建 `web/src/features/persons/components/persons-provider.tsx`（上下文状态与 dialog state）
- [x] 3.2 创建 `web/src/features/persons/components/persons-columns.tsx` 与 `persons-columns-cells.tsx`（表格列定义、人脸数量 Badge、行操作下拉菜单）
- [x] 3.3 创建 `web/src/features/persons/components/persons-action-dialog.tsx`（新增/编辑人员 Dialog 表单，带不可变 personId 处理）
- [x] 3.4 创建 `web/src/features/persons/components/persons-delete-dialog.tsx`（删除人员确认弹窗）
- [x] 3.5 创建 `web/src/features/persons/components/persons-faces-dialog.tsx`（一人多照人脸底库管理弹窗，查看底库列表、新增人脸表单、删除人脸）
- [x] 3.6 创建 `web/src/features/persons/components/persons-dialogs.tsx` 与 `persons-primary-buttons.tsx`
- [x] 3.7 创建 `web/src/features/persons/components/persons-table.tsx`（集成 TanStack Table 与 useTableUrlState）
- [x] 3.8 创建 `web/src/features/persons/index.tsx`（导出 Persons 主页面模块）
- [x] 3.9 编写组件单元测试 `persons-action-dialog.test.tsx`、`persons-table.test.tsx` 与 `persons-faces-dialog.test.tsx`
- 验证命令：`cd web && pnpm test -- src/features/persons/components/`

### 步骤 4：页面路由与全局侧边栏导航挂载
- [x] 4.1 创建 `web/src/routes/_authenticated/persons/index.tsx` 路由文件
- [x] 4.2 更新 `web/src/components/layout/data/sidebar-data.ts`，在 General 组中注入 Persons 菜单项
- 验证命令：`cd web && pnpm build` 确认 Vite 路由代码生成与生产构建正常

### 步骤 5：全量门禁检验与回归
- [x] 5.1 运行前端静态检查：`cd web && pnpm lint`
- [x] 5.2 运行前端构建：`cd web && pnpm build`
- [x] 5.3 运行前端测试集：`cd web && pnpm test`
- [x] 5.4 验证全系统检查：`make check`

### 步骤 6：人脸底库存储配置化与可迁移性重构
- [x] 6.1 `config.toml` 新增 `[person] faces_directory` 配置项，允许显式指定人脸底库存储目录；
- [x] 6.2 `cmd/Zhulong/config.go` 与 `internal/app/config.go` 接入 `FacesDir`，未指定时平滑默认继承 `<data.directory>/faces`；
- [x] 6.3 `internal/person/service.go` 持久化改造为存储相对文件名，并兼容已有绝对路径，实现底库资产环境无关与无感迁移；
- [x] 6.4 编写 `cmd/Zhulong/config_test.go` 与 `service_test.go` 覆盖显式配置、默认继承与动静态路径解析；
- [x] 6.5 更新存储规范 `.trellis/spec/backend/storage-guidelines.md`；全量 `make check` 验证通过。
