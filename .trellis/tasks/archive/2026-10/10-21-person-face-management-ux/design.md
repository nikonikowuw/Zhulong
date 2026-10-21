# 技术设计方案: 人员与人脸管理交互与 UI 现代化升级 (方案 B)

## 1. 架构与状态流 (Architecture & State Flow)

```txt
┌──────────────────────────────────────────────────────────────────┐
│                         PersonsContent                           │
│  ┌─────────────────────────────────┐  ┌───────────────────────┐  │
│  │ View Mode Toggle (Table / Grid) │  │  PersonsPrimaryButtons│  │
│  └─────────────────────────────────┘  └───────────────────────┘  │
│                                │                                 │
│           ┌────────────────────┴───────────────────┐             │
│           ▼                                        ▼             │
│     PersonsTable (表格视图)             PersonsGrid (网格视图)   │
│           │                                        │             │
│           └────────────────────┬───────────────────┘             │
│                                ▼                                 │
│                     usePersonsContext (open, currentRow)        │
│                                │                                 │
│   ┌────────────────────────────┼─────────────────────────────┐   │
│   ▼                            ▼                             ▼   │
│ PersonsActionDialog   PersonsDetailSheet (原Faces)  PreviewDialog│
│ (支持直接附带人脸)      (右侧滑出抽屉 + 批量人脸上传)  (大图沉浸式)  │
└──────────────────────────────────────────────────────────────────┘
```

### 1.1 Context 状态扩展 (`PersonsProvider`)
- `viewMode`: `'table' | 'grid'`（可持久化在 `localStorage`，默认 `'table'` 或 `'grid'`）。
- `open`: `'create' | 'edit' | 'delete' | 'faces'`，保持向后兼容。`'faces'` 渲染抽屉 `PersonsDetailSheet`。
- `currentRow`: 当前正在查看/操作的 `Person` 实体。
- `selectedIds`: 用于跨视图共享选中的卡片/行 ID，支持批量删除操作。

### 1.2 新建人员直接附带首张底库照片
- 在 `PersonsActionDialog` 内部引入 `avatarFile: File | null` 状态。
- 使用受控拖拽区（`FaceDropzone`），支持点击浏览、拖拽放图、格式大小校验（JPEG/PNG/WebP <= 10MB）。
- 提交链路：
  ```ts
  const created = await createMutation.mutateAsync({ name, personId })
  if (avatarFile) {
    try {
      await addFaceMutation.mutateAsync({ personId: created.id, data: avatarFile })
    } catch (faceErr) {
      // 捕获并提示人脸报错，但不回滚人员创建，提示可在详情中补录
    }
  }
  ```

### 1.3 人员详情抽屉 (`PersonsDetailSheet`)
- 使用 `@/components/ui/sheet`：
  - `side="right"`
  - 响应式宽度：`className="w-full sm:max-w-xl md:max-w-2xl overflow-y-auto p-0"`
- 分区结构：
  - **头部 Banner / 基础信息**：
    - 主图头像（大尺寸 64px 配合圆形边框与放大镜预览）。
    - 姓名、工号（可一键复制）、创建时间。
    - 右上角快捷操作菜单（编辑姓名工号、删除人员）。
  - **人脸画廊主体**：
    - 多图上传常驻拖拽区，支持 `<input type="file" multiple accept="...">`。
    - 批量上传支持队列上传与动态 Progress / Loader 反馈。
    - 卡片列表：
      - `data-testid="face-card"` 保留以满足测试用例。
      - 图片上方：主图徽章（绿色/青色）。
      - 图片下方：常驻信息区（录入时间、设为主图按钮、删除按钮），在宽屏/触屏下均有良好可见度。
      - 点击卡片调用 `setPreviewImage` 打开大图预览。

### 1.4 卡片画廊视图 (`PersonsGrid`)
- 复用 `data`, `total`, `isLoading`, `isError`, `onRetry`, `pagination`。
- 多选状态与 TanStack Table / Context 的 `rowSelection` 对接，复用现有的 `PersonsDataTableBulkActions` 批量删除组件。
- 骨架屏：8 个优雅的卡片骨架（包含头像、姓名占位条、徽章占位）。
- 空状态与错误状态与表格保持一致的风格与重试按钮。

## 2. 国际化与样式契约 (i18n & Theme)
- 在 `en.json`, `zh-Hans.json`, `zh-Hant.json` 补全新增的 key：
  - `views.table`, `views.grid`
  - `fields.initialFace`, `fields.initialFaceDesc`, `fields.batchUpload`, `fields.batchUploadDesc`
  - `status.noFacesBadge`, `status.hasFacesBadge`
  - `actions.copyId`, `notifications.copySuccess`
- 遵循 Tailwind v4 语义 Token，避免写死任何十六进制色值，全量支持 Dark / Light 模式。
