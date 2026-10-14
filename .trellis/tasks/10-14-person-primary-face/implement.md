# 实施计划：人员主人脸设置与前端头像化展示

## ordered checklist

### Step 1: 数据库迁移与模型层
- [x] 在 `internal/database/migrations/` 新增 `000008_add_primary_face_id.up.sql` 与 `000008_add_primary_face_id.down.sql`
- [x] 在 `internal/person/model.go` 中为 `Person` 添加 `PrimaryFaceID` 字段，并新增 `SetPrimaryFaceRequest`
- [x] 验证：运行 `go test ./internal/database/...` 确保迁移加载解析无语法错误

### Step 2: 后端 Store 与 Service 逻辑
- [x] 在 `internal/person/store.go` 中实现 `SetPrimaryFace` 方法，更新 `AddFace`（首图自动设主）与 `DeleteFace`（自动退化/替补选主）
- [x] 在 `internal/person/service.go` 中暴露 `SetPrimaryFace` 业务方法，增加权限校验与审计日志记录
- [x] 在 `internal/person/handler.go` 中注册路由 `PUT /persons/:id/primary-face` 并编写入参校验
- [x] 编写并补充单元测试：`store_test.go`, `service_test.go`, `handler_test.go`
- [x] 验证：运行 `go test -race ./internal/person/...`

### Step 3: 前端数据与契约层
- [x] 在 `web/src/features/persons/data/schema.ts` 中更新 `personSchema`，增加 `primaryFaceId`
- [x] 在 `web/src/features/persons/api/persons-api.ts` 中新增 `setPrimaryFace`
- [x] 在 `web/src/features/persons/hooks/use-persons.ts` 中新增 `useSetPrimaryFace` Mutation
- [x] 更新 `api/persons-api.test.ts` 与 `data/schema.test.ts`
- [x] 验证：在 `web/` 下运行相关单元测试

### Step 4: 前端 UI 与交互改造
- [x] 表格结构优化与多选/批量操作闭环：
  - 新增标准行选择复选框列（`select`），支持表头全选与单选；
  - 姓名列净化：移除与「工号 / 唯一标识」列重复显示的工号信息，保持姓名列整洁纯粹；
  - 人脸照片独立列（`avatar`）：居中展示主人脸缩略图，点击调起全屏大图预览灯箱（`PersonsImagePreviewDialog`）；
  - 全屏沉浸式照片预览器（Lightbox）：支持原生全屏模式切换、多级缩放（50%~300%）、滚轮缩放、双击快速放大/还原、在新标签页打开原图、ESC 退出；
  - 批量操作浮动工具栏（`PersonsDataTableBulkActions`）：支持勾选多条记录后在底部浮现批量删除操作，并具备高危输入 `DELETE` 确认弹窗（`PersonsMultiDeleteDialog`）；
- [x] 改造 `web/src/features/persons/components/persons-faces-dialog.tsx`，将旧版长条文件列表彻底重构为响应式相册画廊（Gallery Grid）模式：
  - 每张人脸以高清正方形卡片呈现，主人脸具有高亮边框和左上角精致 `[★ 主人脸]` 角标；
  - 鼠标悬浮呈现半透明浮层操作栏（非主图一键设为主图、右上角圆角快捷删除）；
  - 支持点击卡片调起全尺寸 Lightbox 大图预览；
  - 上传区域无缝内嵌于画廊末尾，未选图时为 `[ + 点击或拖拽上传 ]` 相框，选图后即时切换为待确认预览卡片；
  - 根除长文件名横向撑爆容器问题，文件名以微缩或 Tooltip 形式展示；
- [x] 更新对应组件单测（`persons-table.test.tsx`, `persons-faces-dialog.test.tsx`）
- [x] 验证：运行 `cd web && npx vitest run --browser.headless src/features/persons`

### Step 5: 全量回归与构建检查
- [x] 运行 `make go-check`
- [x] 运行 `cd web && pnpm test -- src/features/persons`
- [x] 确认全链路编译与单测 100% 通过
