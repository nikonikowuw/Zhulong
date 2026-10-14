# 实现人员管理前端界面

## Goal

在 Web 前端实现完整的「人员管理（Persons）」业务切片，提供人员档案维护（CRUD）、一人多照人脸模板管理、数据表格（URL 同步）、路由挂载与侧边栏导航，严格遵循前端架构规范与三语国际化契约。

## Requirements

### 1. 人员档案管理 (Persons CRUD)
- **R1.1 分页列表展示**：支持表格展示姓名、工号（`personId`）、关联人脸数量、创建时间。
- **R1.2 URL 状态同步**：基于 `useTableUrlState` 同步当前分页（`page`、`pageSize`）与搜索关键字至浏览器 URL Search 参数。
- **R1.3 创建人员**：支持通过 Dialog 新增人员。姓名（必填）、工号（可选，若不填则后端默认使用系统生成 ID）。
- **R1.4 编辑人员**：支持通过 Dialog 修改人员姓名；工号（`personId`）展示为不可变（禁用态输入框），符合后端 `PERSON_ID_IMMUTABLE` 约束。
- **R1.5 删除人员**：支持通过 Confirm Dialog 单项删除人员档案，删除前警示将级联删除该人员的所有人脸特征底库。

### 2. 人脸模板管理 (One-to-Many Faces)
- **R2.1 查看人脸列表**：在人员行操作项或卡片中支持点击打开「人脸管理」弹窗/抽屉，调用 `GET /persons/:id/faces` 实时拉取底库照片列表，并展示真实人脸缩略图（通过 `GET /persons/:id/faces/:face_id/image` 只读访问）。
- **R2.2 添加人脸模板**：支持拖拽或点击选择本地 JPG、PNG、WebP 图片（最大 10MB），前端实时缩略图预览；通过 `multipart/form-data` 直传文件流到 `POST /persons/:id/faces`，服务端自动落盘至数据目录并写入特征记录。
- **R2.3 删除人脸模板**：支持在弹窗内删除单张人脸，调用 `DELETE /persons/:id/faces/:face_id`，服务端级联清理磁盘物理文件，完成后刷新人脸列表与人员行的人脸计数。

### 3. 路由与全局导航 (Routing & Navigation)
- **R3.1 路由定义**：新建 `web/src/routes/_authenticated/persons/index.tsx` 页面路由，接入认证守卫。
- **R3.2 侧边栏菜单**：在 `web/src/components/layout/data/sidebar-data.ts` 的 `General` 分组中新增人员管理条目（图标使用 Lucide `UserCheck`，路由指向 `/persons`）。

### 4. 国际化与视觉体验 (i18n & UX)
- **R4.1 三语国际化**：在 `web/src/features/persons/locales/` 提供 `zh-Hans.json`、`zh-Hant.json`、`en.json`，并注册到 `web/src/lib/i18n.ts`。所有页面文案、表单标签、错误提示和提示弹窗均采用 `t(...)`，严禁 JSX 中出现硬编码文本。
- **R4.2 异步三态**：完备处理加载骨架屏（Skeleton）、接口异常错误重试（Error Alert）与无数据空态（Empty State）。

## Acceptance Criteria

- [x] `web/src/features/persons/` 切片结构完备，严格包含 `api/`, `components/`, `data/`, `hooks/`, `locales/`, `index.tsx`。
- [x] 严格零 `any`，不可信数据通过 Zod Schema 解析验证。
- [x] 服务端状态 100% 由 TanStack Query 接管，无手动数据冗余同步。
- [x] 路由 `/persons` 正常访问，侧边栏显示人员管理项并能正常导航。
- [x] 人员新增、编辑、删除与人脸新增、删除交互完整，弹窗与表单状态管理优雅。
- [x] 三语（简中、繁中、英文）文案无缺失。
- [x] 编写核心单元测试（API、Zod Schema、Table/Dialog 组件测试），覆盖关键交互分支。
- [x] 运行 `pnpm lint`、`pnpm build` 与 `pnpm test` 全部通过。
