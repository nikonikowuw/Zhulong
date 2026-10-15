# 技术设计：算法包管理前端模块 (10-15-algorithms-frontend)

## 1. 架构目标与文件拓扑

按照 `shadcn-admin` 与项目前端架构规范，实现 `web/src/features/algorithms/` 完整切片：

```txt
web/src/
├── routes/_authenticated/algorithms/index.tsx       # 路由声明包装
└── features/algorithms/
    ├── index.tsx                                    # 特性入口 (AlgorithmsPage)
    ├── api/
    │   ├── algorithms-api.ts                       # API 交互 (RESTful + Axios)
    │   └── algorithms-api.test.ts                  # API 单元测试
    ├── data/
    │   └── schema.ts                               # Zod 模式与 TypeScript 类型
    ├── hooks/
    │   ├── use-algorithms.ts                       # TanStack Query 缓存与 Mutation
    │   └── use-algorithms.test.ts                  # Hook 单元测试
    ├── components/
    │   ├── algorithms-table.tsx                    # 算法列表/卡片看板
    │   ├── algorithm-upload-dialog.tsx             # 算法包安装弹窗 (含拖拽与进度条)
    │   ├── algorithm-delete-dialog.tsx             # 删除安全二次确认弹窗
    │   └── algorithm-status-badge.tsx              # 状态 Outline 徽标
    └── locales/
        ├── en.json
        ├── zh-Hans.json
        └── zh-Hant.json
```

---

## 2. 核心数据契约 (`data/schema.ts`)

```ts
export const algorithmStatusSchema = z.enum([
  'ACTIVE',
  'DISABLED',
  'STARTING',
  'RUNNING',
  'CRASHED',
  'CRASH_LOOP',
  'ROLLING_BACK',
])

export const algorithmTaskTypeSchema = z.enum([
  'detection',
  'classification',
  'person_structure',
])

export const algorithmCategorySchema = z.enum([
  'safety',
  'security',
  'fire',
  'traffic',
  'general',
])

export const algorithmSchema = z.object({
  id: z.string(),
  name: z.string(),
  version: z.string(),
  taskType: algorithmTaskTypeSchema,
  category: algorithmCategorySchema,
  targetArch: z.string(),
  targetDevice: z.string(),
  computeCost: z.number().default(10),
  memoryMb: z.number().default(64),
  description: z.string().default(''),
  manifestJson: z.string().default('{}'),
  configSchemaJson: z.string().default('{}'),
  status: algorithmStatusSchema.default('DISABLED'),
  previousVersion: z.string().default(''),
  createdAt: z.string(),
  updatedAt: z.string(),
})

export type Algorithm = z.infer<typeof algorithmSchema>
```

---

## 3. 上传与交互设计 (`algorithm-upload-dialog.tsx`)

- **交互体验**：
  - 虚线拖拽区域，支持 `.tar.gz` 与 `.zip` 格式；
  - 文件大小限制 500MB，超限立即在前端拦截并红字提示；
  - 上传过程展示进度条（利用 `axios.onUploadProgress` 计算 $0\% \sim 100\%$）；
  - 上传完成后若后端验包报错（如算力超出、架构不匹配），通过 Alert 准确展示后端 `message` 或 `details`；
  - 安装成功后自动关闭弹窗、展示 Toast 并使 Query 缓存失效。

---

## 4. 状态与操作设计

- **启停与删除**：
  - `ACTIVE` 状态允许停用；`DISABLED` 允许启用；
  - 只有非运行中的算法允许执行删除；
  - 删除弹窗采用 `ConfirmDialog`，危险操作二次确认，防止误触。
