# 技术设计：人员批量导入

## 1. 方案概览

新增 `internal/person` 内聚的 CSV 解析、预校验和批量确认导入能力，不新增数据库表和迁移。前端在 `web/src/features/persons` 增加导入对话框，由 TanStack Query mutation 管理预览和确认结果。

流程：

```text
CSV 文件
  -> POST /api/v1/persons/import/preview（multipart，上传组）
  -> 服务端解析 + 业务校验 + 查询现有工号
  -> 返回规范化 rows、行结果和统计（不写库）
  -> 前端保留原始 File，用户确认
  -> POST /api/v1/persons/import/commit（multipart，上传组）
  -> 服务端重新解析同一 CSV、重新校验最新数据库状态
  -> 单事务写入有效行，唯一冲突按行失败
  -> 返回成功/失败逐行结果与统计
```

本期不持久化导入任务，也不依赖服务端内存 token。前端在对话框中保留原始 `File`；预览和确认都上传该文件，确认端点重新解析、校验并基于最新数据库状态决定每行结果。服务端不接收客户端拼装的人员数据，也不信任任何客户端有效标志。

## 2. 后端边界与契约

### 2.1 类型与服务

导入能力完全内聚并整合于 `internal/person` 的标准分层文件中，不新增独立的 `import_*` 零散文件：

- `responses.go`：定义导入结果相关 HTTP 响应类型（`PersonImportPreviewResponse`、`PersonImportCommitResponse` 等）。
- `service.go`：包含 CSV 有界读取与解析、行级业务校验，以及 `PreviewPersonImport` / `CommitPersonImport` 业务编排。
- `store.go`：提供事务内批量创建及工号去重查询能力（`CreatePersonImport` / `ListPersonIDs`）。
- `handler.go`：承载 `PreviewImport` 与 `CommitImport` 端点，注册至上传路由组。
- `locale.go`：统一注册导入错误稳定码的三语本地化文案。
- `service_test.go`、`handler_test.go`：覆盖 CSV 解析、校验、事务及 API 端点测试。

领域接口应区分：

- `PreviewPersonImport(ctx, reader) -> PreviewResponse`：解析并校验，只读数据库。
- `CommitPersonImport(ctx, reader) -> ImportResponse`：重新解析同一 CSV 并校验，事务写入有效行。

每个输入行保存原始 CSV 行号、规范化后的 name/personId 和 issues（稳定 error code + 字段路径）。错误响应不是 HTTP 422 字段绑定错误，而是正常的 200 预览业务结果，避免把多行业务错误塞进统一 `details`。前端通过三语 locale 翻译稳定错误码；文件级结构错误使用后端 `apperr` 错误码和后端 i18n。

### 2.2 CSV 规则

- 使用 `encoding/csv`，允许 BOM，仅去除第一字段开头的 BOM。
- 首行必须包含且只包含 `name`、`personId` 两列；顺序任意。
- 空白行按忽略处理，空文件或只有表头按文件级错误处理。
- name 和 personId Trim；空 personId 归为 nil。
- 复用 `normalizeName` 规则；personId 长度限制 64，业务键按规范保留大小写，不进行隐式大小写折叠。
- 预览阶段先建立文件内 personId 集合，再查询数据库已有工号，给重复行稳定结果。
- 最大 CSV 文件字节数固定为 2 MiB，最大数据行数固定为 1000；解析器使用 `io.LimitReader`/有界读取并拒绝超限。

### 2.3 提交事务与冲突

- 服务端不能信任预览结果，确认时重新解析和校验同一 CSV。
- 先在事务内读取已有工号快照，再按输入顺序创建有效行。
- 业务唯一冲突只标记该行失败；其余行继续写入。
- 其他数据库错误立即返回并回滚整个事务。
- 为区分 SQLite 唯一冲突与系统故障，Store 提供批量写入辅助，统一把 `ErrDuplicatePersonID` 转成行结果；不得吞掉非唯一错误。
- 事务内只做数据库操作，不调用人脸/原生引擎。
- 成功创建人员不需要底库广播，因为导入不包含人脸。

### 2.4 路由与审计

- `POST /persons/import/preview` 与 `POST /persons/import/commit` 均注册到 `UploadRoutes`，使用 multipart 与上传组限额；Handler 和 CSV parser 均实施 2 MiB 文件上限。
- 上传组继续使用认证、AccessLog、Audit 和 HTTP 请求体最大限制；导入业务文件限制远小于算法包上传组的 500 MiB 最大请求体。
- Handler 仅解析请求、调用 Service 和写统一信封；审计在 Handler 标记 `person.import.preview` / `person.import.commit`，不记录姓名、工号全文。
- 更新 `Makefile` Swagger 目录若需要，并运行 `make api-docs` 生成产物。

## 3. 前端设计

- `persons-primary-buttons.tsx` 增加带 Upload 图标的批量导入按钮。
- 新增 `persons-import-dialog.tsx`，按状态渲染选择文件、预览表格、错误详情、确认结果和关闭/重置操作；使用现有 Dialog/ScrollArea/Table/Button 组件。
- `persons-provider.tsx` 增加 `import` dialog 状态，或把导入对话框局部化，避免扩张现有上下文超过职责。
- `persons-api.ts` 增加 `previewImport(file)`、`commitImport(file)`，两者均构造 multipart FormData 并上传原始 File；响应经 Zod schema 解析。
- `data/schema.ts` 增加 import row/result/summary schema；外部数据先 parse。
- `hooks/use-persons.ts` 增加 mutation 和成功后的列表失效。
- 三语 locale 增加所有标题、按钮、状态、校验和下载文案。
- 失败行下载使用前端 `Blob` + `URL.createObjectURL`，通过 `csv` 转义函数生成 CSV，不把用户输入直接拼成公式；必要时对以 `=`, `+`, `-`, `@` 开头的字段加前缀或在失败报告中只导出原始值并提示安全处理。
- 页面遵循 loading/error/empty 三态，长操作使用 `toast.promise`，避免重复全局 toast。

## 4. 兼容与安全

- 不修改既有迁移、人员 CRUD、人脸上传和外部工号不可变规则。
- 预览/确认均受认证保护；上传组保留 AccessLog、Audit 和 Auth。
- 严禁记录完整 CSV、完整姓名/工号列表和原始文件内容到日志。
- 解析器必须限制文件字节数、记录数和单行字段长度，避免内存膨胀。
- 错误行展示可包含用户输入，但后端错误文案不回显底层 SQL/驱动错误。

## 5. 关键取舍

- 不引入 xlsx 依赖：CSV 足够覆盖 Excel 导出，降低边缘设备包体和安全维护成本。
- 不增加导入历史表：本期只需要一次性导入结果；后续若要断点续传/审计查询，再单独设计持久化任务域。
- 部分成功只针对业务行错误；数据库故障整体回滚，保证系统故障可诊断、可重试。
- 预览与确认之间不保证数据库快照一致；确认重新校验并依赖唯一约束应对并发。

## 6. 回滚

删除新增 API/前端文件及路由注册即可回滚，不需要数据库 down migration。若 Swagger 或嵌入前端产物已生成，应随源码版本一起回退/重建。
