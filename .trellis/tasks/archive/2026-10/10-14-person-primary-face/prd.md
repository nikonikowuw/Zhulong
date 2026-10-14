# 支持人员主人脸设置与前端头像化展示

## Goal

在人员与人脸底库管理体系中引入「主人脸（Primary Face / Avatar）」概念。支持人员档案关联主人脸标识，实现录入首张底库自动设为主图、支持手动切换主人脸，并在前端列表提供直观的头像（Avatar）展示，提升人员检索与底库辨识体验。

## Requirements

### 1. 数据模型与迁移
- 数据库 `persons` 表新增 `primary_face_id TEXT NULL`，外键引用 `person_faces(id) ON DELETE SET NULL`，并建立对应索引。
- 遵循版本化迁移规范：提供编号为 `000008_add_primary_face_id.up.sql` 与 `000008_add_primary_face_id.down.sql`。

### 2. 后端业务逻辑与 API
- `Person` 结构体及 DTO 扩展 `primaryFaceId`（可为 `null` 或字符串）。
- 自动设主逻辑：
  - 当为人员录入第一张人脸（或当前 `primary_face_id` 为空）时，后端自动将新增的 face 设置为该人员的 `primary_face_id`。
  - 当删除人脸底库时：若被删除的人脸正好是当前 `primary_face_id`，且该人员仍有剩余人脸，则自动选取最早创建的一张剩余人脸晋升为主人脸；若已无剩余人脸，则置为 `null`。
- 主人脸切换 API：
  - 新增接口 `PUT /api/v1/persons/:id/primary-face`，请求体 `{ "faceId": "<face-id>" }`。
  - 校验 `faceId` 必须属于当前人员（否则返回 404 或 400），设置成功后更新 `persons.updated_at` 并记录审计日志。

### 3. 前端界面与交互
- 领域契约与 API：
  - `data/schema.ts` 中 `personSchema` 支持 `primaryFaceId`。
  - `api/persons-api.ts` 提供 `setPrimaryFace(personId, faceId)`。
  - `hooks/use-persons.ts` 提供 `useSetPrimaryFace` Mutation，成功后自动使 `['persons']` 缓存失效。
- 列表表格展示（Avatar 头像化）：
  - 改造 `PersonsTable` 姓名列：集成 shadcn `<Avatar>` 组件。
  - 展示优先级：优先取 `primaryFaceId` 对应的人脸图片；若未显式指定但存在 `faces` 则取首张兜底；若无人脸或加载失败，则展示姓名首字符 Fallback。
- 底库管理弹窗（`PersonsFacesDialog`）：
  - 底库列表中高亮标记主人脸（显示 `[★ 主人脸]` 徽标）。
  - 对非主人脸项提供「设为主人脸」操作按钮，点击即刻切换并给予 Toast 提示。
- 国际化支持：
  - 补充中、英、繁三语字典中的主人脸操作文案与通知。

## Acceptance Criteria

- [ ] 运行数据库迁移，`000008_add_primary_face_id.up.sql` 与 `down.sql` 正常通过。
- [ ] Go 单测覆盖：录入首张人脸自动设为主人脸、删除当前主人脸自动选替补、手动切换主人脸、跨人员非法 faceId 校验。
- [ ] 前端列表表格能够正常渲染头像缩略图及姓名首字符 Fallback。
- [ ] 在 `PersonsFacesDialog` 中可清晰查看主人脸徽标，并能成功切换主人脸。
- [ ] 前端浏览器测试（Vitest Playwright）与 Go 测试全量通过（无 race、无报错）。
