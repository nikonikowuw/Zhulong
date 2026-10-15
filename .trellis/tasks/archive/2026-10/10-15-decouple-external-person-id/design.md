# 技术设计 (Design): 人员工号与内部主键解耦

## 1. 架构拓扑与设计原则

```txt
React 前端 (web/src/features/persons)
   │
   │  REST API (POST/PATCH/GET /api/v1/persons)
   ▼
Go 服务与控制器 (internal/person)
   │
   ├── GORM / SQLite (internal/database/migrations/000009_decouple_person_id)
   │     persons.person_id NULLABLE, Partial UNIQUE INDEX
   │
   └── IPC 广播 (GalleryNotifier / AIMessageBroadcaster)
         │
         ▼
       C++ 原生引擎 (native/src/algorithm/runner & runtime)
         原子按 FaceID 擦除底库，person_id 为元数据
```

### 设计原则
1. **单一事实源与职责分离**：
   - `persons.id`（内部代理主键）：用于实体寻址、表间级联外键（`person_faces.person_id REFERENCES persons(id)`）。
   - `persons.person_id`（外部业务工号）：可选、可空、用于外部系统（HR/门禁）对接与对账，空值绝不回退至 `persons.id`。
2. **渐进式数据迁移 (Non-destructive Migration)**：
   - 追加 `000009` 版本迁移，执行标准 SQLite 表重建流程。
   - 迁移脚本自动将 `person_id = id` 的历史遗留虚假工号转换为 `NULL`。
3. **C++ 底库原子安全**：
   - 底库清退以 `FaceID` 为原子单元，即使人员未设置工号，注销时也能精准擦除特征向量，绝无内存底库泄漏。

---

## 2. 详细设计

### 2.1 数据库迁移 (`000009_decouple_person_id`)

SQLite 不支持直接 `ALTER COLUMN DROP NOT NULL`，采用 SQLite 规范的表重建流程：

#### `000009_decouple_person_id.up.sql`
```sql
PRAGMA foreign_keys = OFF;

CREATE TABLE persons_dg_tmp (
    id TEXT PRIMARY KEY NOT NULL,
    person_id TEXT,
    name TEXT NOT NULL,
    primary_face_id TEXT REFERENCES person_faces(id) ON DELETE SET NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO persons_dg_tmp (id, person_id, name, primary_face_id, created_at, updated_at)
SELECT
    id,
    CASE WHEN person_id = id THEN NULL ELSE person_id END,
    name,
    primary_face_id,
    created_at,
    updated_at
FROM persons;

DROP TABLE persons;

ALTER TABLE persons_dg_tmp RENAME TO persons;

CREATE UNIQUE INDEX IF NOT EXISTS idx_persons_person_id ON persons(person_id) WHERE person_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_persons_primary_face_id ON persons(primary_face_id);

PRAGMA foreign_keys = ON;
```

#### `000009_decouple_person_id.down.sql`
```sql
PRAGMA foreign_keys = OFF;

CREATE TABLE persons_dg_tmp (
    id TEXT PRIMARY KEY NOT NULL,
    person_id TEXT NOT NULL CHECK (person_id <> ''),
    name TEXT NOT NULL,
    primary_face_id TEXT REFERENCES person_faces(id) ON DELETE SET NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO persons_dg_tmp (id, person_id, name, primary_face_id, created_at, updated_at)
SELECT
    id,
    COALESCE(person_id, id),
    name,
    primary_face_id,
    created_at,
    updated_at
FROM persons;

DROP TABLE persons;

ALTER TABLE persons_dg_tmp RENAME TO persons;

CREATE UNIQUE INDEX IF NOT EXISTS idx_persons_person_id ON persons(person_id);
CREATE INDEX IF NOT EXISTS idx_persons_primary_face_id ON persons(primary_face_id);

PRAGMA foreign_keys = ON;
```

---

### 2.2 Go 领域模型与服务设计 (`internal/person`)

#### 1. 数据模型 (`model.go`)
```go
type Person struct {
    ID            string    `gorm:"primaryKey;column:id" json:"id"`
    PersonID      *string   `gorm:"column:person_id" json:"personId"` // 指针类型，支持 NULL
    Name          string    `gorm:"column:name;not null" json:"name"`
    PrimaryFaceID *string   `gorm:"column:primary_face_id" json:"primaryFaceId,omitempty"`
    CreatedAt     time.Time `gorm:"column:created_at;not null" json:"createdAt"`
    UpdatedAt     time.Time `gorm:"column:updated_at;not null" json:"updatedAt"`
    Faces         []Face    `gorm:"foreignKey:OwnerRowID;references:ID;constraint:OnDelete:CASCADE" json:"faces,omitempty"`
}

type CreatePersonRequest struct {
    PersonID *string `json:"personId"`
    Name     string  `json:"name" binding:"required"`
}

type UpdatePersonRequest struct {
    PersonID *string `json:"personId,omitempty"`
    Name     *string `json:"name,omitempty"`
}
```

#### 2. 服务层逻辑 (`service.go`)
- **`CreatePerson`**：
  ```go
  var personID *string
  if req.PersonID != nil {
      trimmed := strings.TrimSpace(*req.PersonID)
      if trimmed != "" {
          personID = &trimmed
      }
  }
  // 彻底移除 personID = id 回退逻辑
  p := &Person{
      ID:       id,
      PersonID: personID,
      Name:     req.Name,
  }
  ```
- **`UpdatePerson`**：
  ```go
  // 支持从未绑定工号状态向有工号状态补录
  if req.PersonID != nil {
      trimmed := strings.TrimSpace(*req.PersonID)
      if p.PersonID == nil || *p.PersonID == "" {
          if trimmed != "" {
              p.PersonID = &trimmed
          }
      } else if trimmed != *p.PersonID {
          return nil, apperr.Conflict("PERSON_ID_IMMUTABLE", "personId cannot be changed once assigned", nil)
      }
  }
  ```
- **`DeletePerson`**：
  ```go
  if s.galleryNotifier != nil && p != nil {
      for _, f := range faces {
          _ = s.galleryNotifier.BroadcastDelete(ctx, f.ID, "")
      }
      if p.PersonID != nil && *p.PersonID != "" {
          _ = s.galleryNotifier.BroadcastDelete(ctx, "", *p.PersonID)
      }
  }
  ```
- **`GetGallerySyncData`**：
  `PersonID` 为空时输出空字符串 `""`，与 C++ ABI 规范（未设置或未命中为空串）契合。

#### 3. 仓储层逻辑 (`store.go`)
- `CreatePerson`：当 `p.PersonID == nil` 时，GORM 会将对应字段写入 `NULL`。条件唯一索引不会对多个 `NULL` 报冲突。
- `UpdatePerson`：更新映射包含 `person_id`（当 `p.PersonID` 有赋值时）：
  ```go
  updates := map[string]any{
      "name":       p.Name,
      "updated_at": now,
  }
  if p.PersonID != nil {
      updates["person_id"] = *p.PersonID
  }
  ```

---

### 2.3 前端设计 (`web/`)

1. **类型与 Schema (`data/schema.ts`)**：
   - `personSchema` 中的 `personId` 改为 `z.string().nullable().optional()`。
   - `updatePersonSchema` 增加可选的 `personId`。
2. **列表展示 (`components/persons-columns.tsx`)**：
   - 列渲染器中：若 `row.original.personId` 为空串或 null/undefined，展示 `-`。
3. **弹窗交互 (`components/persons-action-dialog.tsx`)**：
   - 编辑态下：判断 `const isBound = Boolean(currentRow?.personId && currentRow.personId.trim())`。
   - 若未绑定（`!isBound`），允许输入工号；提交时若填写了工号，则附加在 `updateMutation` 中。
   - 若已绑定（`isBound`），输入框置灰禁用并显示提示。
4. **实时识别去重与显示 (`live/`)**：
   - `ai-result-service.ts` 的冷却键优化为 `${event.camera_id}:${face.person_id || face.person_name || 'unknown'}`。

---

## 3. 回滚预案 (Rollback Plan)

- 数据库：执行 `000009_decouple_person_id.down.sql`，将所有 `person_id` 为 `NULL` 的行还原为 `person_id = id`，恢复非空与全表唯一索引。
- 代码：回退 Git commit 即可。
