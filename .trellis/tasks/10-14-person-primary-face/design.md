# 技术设计：人员主人脸设置与前端头像化展示

## 1. 架构边界与职责划分

```txt
┌─────────────────────────────────────────────────────────────────┐
│ 前端 (web/src/features/persons)                                 │
│  - PersonsTable: 姓名列复合展示 Avatar (图片/Fallback) + Name    │
│  - PersonsFacesDialog: 显示 [★ 主人脸] Badge + [设为主人脸] 交互 │
│  - useSetPrimaryFace: 触发 PUT /persons/:id/primary-face       │
└────────────────┬────────────────────────────────────────────────┘
                 │ HTTP REST (JSON Envelope)
┌────────────────▼────────────────────────────────────────────────┐
│ Go 后端 (internal/person)                                       │
│  - Handler: PUT /api/v1/persons/:id/primary-face                │
│  - Service: 校验 faceId 所属权、审计日志、自动主图晋升/退化规则 │
│  - Store: GORM 读写与事务保证                                   │
└────────────────┬────────────────────────────────────────────────┘
                 │ SQLite DDL / SQL
┌────────────────▼────────────────────────────────────────────────┐
│ SQLite 数据库 (internal/database/migrations)                    │
│  - 000008_add_primary_face_id.up.sql                            │
│  - persons.primary_face_id TEXT NULL REFERENCES person_faces(id)│
└─────────────────────────────────────────────────────────────────┘
```

## 2. 数据库模式变更

### 2.1 迁移脚本：`000008_add_primary_face_id.up.sql`
```sql
ALTER TABLE persons ADD COLUMN primary_face_id TEXT REFERENCES person_faces(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_persons_primary_face_id ON persons(primary_face_id);
```

### 2.2 回滚脚本：`000008_add_primary_face_id.down.sql`
```sql
DROP INDEX IF EXISTS idx_persons_primary_face_id;
ALTER TABLE persons DROP COLUMN primary_face_id;
```

*设计考量*：外键约束配置为 `ON DELETE SET NULL`，在底层提供最基本的引完整性兜底，避免悬垂指针。

## 3. 后端业务设计与 API 契约

### 3.1 实体模型拓展 (`internal/person/model.go`)
```go
type Person struct {
    ID            string    `gorm:"primaryKey;column:id" json:"id"`
    PersonID      string    `gorm:"column:person_id;not null" json:"personId"`
    Name          string    `gorm:"column:name;not null" json:"name"`
    PrimaryFaceID *string   `gorm:"column:primary_face_id" json:"primaryFaceId,omitempty"`
    CreatedAt     time.Time `gorm:"column:created_at;not null" json:"createdAt"`
    UpdatedAt     time.Time `gorm:"column:updated_at;not null" json:"updatedAt"`
    Faces         []Face    `gorm:"foreignKey:OwnerRowID;references:ID;constraint:OnDelete:CASCADE" json:"faces,omitempty"`
}

type SetPrimaryFaceRequest struct {
    FaceID string `json:"faceId" binding:"required"`
}
```

### 3.2 业务状态转换规则
1. **添加人脸 (`AddFace`)**：
   - 检查该人员当前 `primary_face_id` 是否为 NULL 或空。
   - 若为空，则自动将新录入的 `face.ID` 设为该人员的 `primary_face_id`。
2. **删除人脸 (`DeleteFace`)**：
   - 在删除指定 face 时，先检查其是否为当前人员的 `primary_face_id`。
   - 若是：
     - 查询该人员除该 face 以外的所有有效 faces（按 `created_at ASC` 排序）。
     - 若还有剩余人脸，自动选取最早的一张晋升为 `primary_face_id`；
     - 若已无剩余人脸，置为 NULL。
3. **显式切换主人脸 (`SetPrimaryFace`)**：
   - 校验：`faceId` 必须存在且其 `person_id` 严格等于目标人员的 `id`（杜绝非法跨人关联）。
   - 更新 `persons.primary_face_id = faceId`，更新 `updated_at = time.Now().UTC()`。
   - 触发审计日志：`person.primary_face.set`。

### 3.3 HTTP 端点
- `PUT /api/v1/persons/:id/primary-face`
  - Request Body: `{"faceId": "face-uuid"}`
  - Response: 遵循统一信封 `{"code": "OK", "message": "success", "data": { ...Person }}`
  - 错误码：`FACE_NOT_FOUND` (404), `PERSON_NOT_FOUND` (404), `INVALID_FACE_OWNER` (400)

## 4. 前端契约与组件设计

### 4.1 Schema 与 API (`web/src/features/persons/`)
- `personSchema` 扩充：`primaryFaceId: z.string().nullable().optional()`
- `personsApi.setPrimaryFace(personId: string, faceId: string): Promise<Person>`
- `useSetPrimaryFace()`：Mutation 成功后触发 `queryClient.invalidateQueries({ queryKey: PERSONS_QUERY_KEY })`。

### 4.2 头像化组件改造 (`PersonsTable` & `PersonsColumns`)
姓名列渲染：
- 使用 `<Avatar>` 组合：
  ```tsx
  const avatarFaceId = row.original.primaryFaceId || row.original.faces?.[0]?.id
  const avatarUrl = avatarFaceId ? personsApi.getFaceImageUrl(row.original.id, avatarFaceId) : undefined
  ```
- 头像左侧对齐（40x40 或 36x36），右侧垂直排列姓名与微缩 ID；
- 无人脸或图片载入失败时，`<AvatarFallback>` 提取姓名首字符（中英兼容），背景浅灰/强调色。

### 4.3 底库弹窗 (`PersonsFacesDialog`)
- 在每一个 `FaceItem` 中：
  - 判断 `isPrimary = face.id === currentPerson.primaryFaceId`；
  - 若 `isPrimary`：渲染 Badge `[★ 主人脸]`（主强调色，高辨识度）；
  - 若非 `isPrimary`：渲染按钮 `设为主人脸`（悬浮或默认显示，图标如 Star / Check）；点击触发 `setPrimaryFace`。

## 5. 兼容性与回滚考量
- 向后兼容：旧数据库已有人脸但 `primary_face_id` 为 NULL 的人员，前端具备自动降级策略（使用 `faces[0]` 兜底渲染头像），老数据无需全量回刷；
- 回滚安全：SQLite 迁移脚本具备完整的 `.down.sql`，可逆撤销列与索引。
