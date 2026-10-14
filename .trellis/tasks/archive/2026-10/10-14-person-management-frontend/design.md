# 技术设计：人员管理前端界面 (Person Management Frontend)

## 1. 架构定位与目录拓扑

本特性切片位于 `web/src/features/persons/`，通过 TanStack Router 在 `web/src/routes/_authenticated/persons/index.tsx` 挂载，并在侧边栏 `sidebarData` 的 `General` 分组中呈现入口。

```text
web/src/
├── routes/_authenticated/persons/
│   └── index.tsx                        # 页面路由：处理 URL 参数校验与视图包装
├── features/persons/
│   ├── api/
│   │   ├── persons-api.ts               # API 客户端封装（基于 src/lib/api-client.ts）
│   │   └── persons-api.test.ts          # API 客户端单元测试
│   ├── data/
│   │   ├── schema.ts                    # Zod Schema 与 TypeScript 类型推断
│   │   └── schema.test.ts               # Schema 校验单元测试
│   ├── hooks/
│   │   └── use-persons.ts               # TanStack Query (Query & Mutation) 封装
│   ├── components/
│   │   ├── persons-provider.tsx         # 弹窗与选中行上下文（基于 useDialogState）
│   │   ├── persons-columns.tsx          # TanStack Table 列定义（姓名、工号、人脸数、时间、操作）
│   │   ├── persons-table.tsx            # 数据表格容器（对接 useTableUrlState）
│   │   ├── persons-primary-buttons.tsx  # 顶部主操作栏（新增人员按钮）
│   │   ├── persons-action-dialog.tsx    # 新增 / 编辑人员表单弹窗
│   │   ├── persons-delete-dialog.tsx    # 删除人员二次确认弹窗
│   │   ├── persons-faces-dialog.tsx     # 一人多照人脸模板管理抽屉/弹窗（列表、录入、删除）
│   │   └── persons-dialogs.tsx          # 统一弹窗装配入口
│   ├── locales/
│   │   ├── zh-Hans.json                 # 简体中文语言包
│   │   ├── zh-Hant.json                 # 繁体中文语言包
│   │   └── en.json                      # 英文语言包
│   └── index.tsx                        # Feature 主视图导出入口
```

---

## 2. 跨层数据契约 (Cross-layer Contracts)

### 2.1 后端 API 映射

| 操作 | HTTP 方法与路径 | 请求载荷 / 参数 | 响应结构 |
| :--- | :--- | :--- | :--- |
| 获取人员分页列表 | `GET /persons` | `?page=1&pageSize=10` | `PaginatedData<Person>`: `{ items: Person[], total, page, pageSize }` |
| 获取人员详情 | `GET /persons/:id` | 无 | `Person` |
| 创建人员档案 | `POST /persons` | `{ name: string, personId?: string }` | `Person` (HTTP 201) |
| 更新人员档案 | `PUT /persons/:id` | `{ name?: string, personId?: string }` | `Person` (HTTP 200) |
| 删除人员档案 | `DELETE /persons/:id` | 无 | `{ deleted: true }` |
| 获取人脸底库列表 | `GET /persons/:id/faces` | 无 | `Face[]` |
| 录入人脸底库 | `POST /persons/:id/faces`| `file` (multipart/form-data) | `Face` (HTTP 201) |
| 获取人脸图片 | `GET /persons/:id/faces/:face_id/image` | 无 | `image/jpeg` 等二进制流 |
| 删除单张人脸 | `DELETE /persons/:id/faces/:face_id` | 无 | `{ deleted: true }` |

### 2.2 Zod 数据模型定义 (`data/schema.ts`)

```typescript
export const faceSchema = z.object({
  id: z.string(),
  ownerRowId: z.string(),
  imagePath: z.string(),
  createdAt: z.string(),
})
export type Face = z.infer<typeof faceSchema>

export const personSchema = z.object({
  id: z.string(),
  personId: z.string(),
  name: z.string(),
  createdAt: z.string(),
  updatedAt: z.string(),
  faces: z.array(faceSchema).optional().default([]),
})
export type Person = z.infer<typeof personSchema>

export const createPersonSchema = z.object({
  name: z.string().trim().min(1, 'name_required').max(64),
  personId: z.string().trim().max(64).optional(),
})

export const updatePersonSchema = z.object({
  name: z.string().trim().min(1, 'name_required').max(64),
})

```

---

## 3. 状态管理与数据流

### 3.1 服务端状态 (TanStack Query)
- Query Keys:
  - `['persons', params]`：人员列表，支持 `page` / `pageSize` / `search`。
  - `['persons', 'detail', id]`：人员详情。
  - `['persons', id, 'faces']`：某人员的人脸底库列表。
- Mutations & Invalidation:
  - `useCreatePerson`: 成功后 invalidate `['persons']`。
  - `useUpdatePerson`: 成功后 invalidate `['persons']`。
  - `useDeletePerson`: 成功后 invalidate `['persons']`。
  - `useAddFace`: 成功后 invalidate `['persons', personId, 'faces']` 并 invalidate `['persons']`（更新列表中的人脸计数）。
  - `useDeleteFace`: 成功后 invalidate `['persons', personId, 'faces']` 并 invalidate `['persons']`。

### 3.2 客户端 UI 状态 (`PersonsProvider`)
- 基于 `useDialogState<PersonsDialogType>(null)` 管理打开的弹窗：
  - `create`: 新增人员
  - `edit`: 修改人员
  - `delete`: 删除人员
  - `faces`: 一人多照人脸底库管理抽屉/弹窗
- `currentRow: Person | null` 记录当前正在操作的人员实体。

### 3.3 表格与 URL 状态
- 使用 `@/hooks/use-table-url-state`：
  - `pagination`: `{ page, pageSize }` 与 URL 同步
  - `globalFilter`: 关键字过滤绑定 `search` 参数

---

## 4. 人脸底库录入与物理落盘

### 架构设计
- 前端在 `PersonsFacesDialog` 中提供直观的原生文件拖拽与选择上传区域（支持 JPG、PNG、WebP，限制 10MB）。
- 提交请求为标准的 `multipart/form-data`，包含 `file` 二进制流。
- 后端 `AddFace` 接收文件流后，安全落盘至数据目录 `dataDir/faces/{personID}_{faceID}.ext`，并写入数据库特征记录。
- 提供 `GET /persons/:id/faces/:face_id/image` 端点，供前端列表直接缩略图只读访问。
- 彻底移除对服务器本地绝对路径 `imagePath` 的输入与引用，杜绝路径穿越与前后端协议割裂。

---

## 5. 三语国际化字典设计 (i18n Keys)

命名空间：`persons`
- 结构化键树：
  - `title`, `description`
  - `actions`: `create`, `edit`, `delete`, `manageFaces`, `addFace`
  - `fields`: `name`, `personId`, `faceCount`, `createdAt`
  - `dialogs`:
    - `createTitle`, `editTitle`, `deleteTitle`, `facesTitle`, `addFaceTitle`, `deleteFaceTitle`
    - `deleteConfirm`, `deleteFaceConfirm`
  - `status`: `emptyFaces`
  - `validation`: `nameRequired`, `fileRequired`, `fileTooLarge`, `invalidFileType`
