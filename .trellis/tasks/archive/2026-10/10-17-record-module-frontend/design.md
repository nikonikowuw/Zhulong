# 技术设计：前端感知记录模块 (records feature)

## 1. 架构拓扑与层级边界

```txt
浏览器 (TanStack Router)
  │ URL: /_authenticated/records?tab=recognitions&view=grid&page=1&...
  ▼
web/src/routes/_authenticated/records/index.tsx (参数校验 validateSearch via Zod)
  │
  ▼
web/src/features/records/index.tsx (主容器 Records)
  ├── RecordsHeader / RecordsToolbar (摄像头下拉、时间范围选择、视图切换、刷新)
  ├── Tabs (Recognitions | Alarms | Captures)
  │    ├── RecognitionsView (Table / Grid)
  │    ├── AlarmsView (Table / Grid)
  │    └── CapturesView (Table / Grid)
  └── RecordDetailDialog (原图 + BoundingBoxCanvas + 比对详情/属性 Viewer)
       │
       ▼
  Hooks & API 访问层 (TanStack Query)
  ├── useRecognitions / useAlarms / useCaptures
  └── api/records-api.ts ⇄ src/lib/api-client.ts
       │
       ▼ (HTTP GET /api/v1/records/* 与 /api/v1/media/content/*)
Go 宿主 (internal/record 与 internal/mediafile)
```

## 2. 后端数据契约与前端模型映射

### 2.1 识别记录 (Recognitions)
- 后端响应契约：
  - `id`: string
  - `cameraId`: string
  - `algorithmId`: string
  - `pts`: int64
  - `trackId`: int64
  - `personId`: string | null
  - `personName`: string
  - `matchedFaceId`: string
  - `matched`: boolean
  - `similarity`: number (0.0 ~ 1.0)
  - `candidates`: Array<{ faceId: string, personId: string, personName: string, similarity: number, imagePath: string }>
  - `faceImagePath`: string (现场人脸切片)
  - `galleryImagePath`: string (底库对标照片)
  - `imagePath`: string (全景大图)
  - `box`: [number, number, number, number] (原图归一化/像素坐标 [x1, y1, x2, y2])
  - `bodyBox`: [number, number, number, number] (人体目标框)
  - `attributes`: Record<string, any> (年龄、性别、表情等结构化属性)
  - `capturedAt`: string (RFC3339)
  - `createdAt`: string (RFC3339)

### 2.2 告警记录 (Alarms)
- 后端响应契约：
  - `id`: string
  - `cameraId`: string
  - `algorithmId`: string
  - `alarmType`: string (如 tripwire, invasion, loitering 等)
  - `score`: number
  - `box`: [number, number, number, number]
  - `imagePath`: string (全景大图)
  - `cropPath`: string (目标切片小图)
  - `extra`: Record<string, any>
  - `pts`: int64
  - `capturedAt`: string
  - `createdAt`: string

### 2.3 抓拍记录 (Captures)
- 后端响应契约：
  - `id`: string
  - `cameraId`: string
  - `targetType`: string (如 face, body, motor, non_motor 等)
  - `score`: number
  - `box`: [number, number, number, number]
  - `imagePath`: string (全景大图)
  - `cropPath`: string (切片小图)
  - `extra`: Record<string, any>
  - `pts`: int64
  - `capturedAt`: string
  - `createdAt`: string

### 2.4 媒体图片链接生成策略
- 统一辅助函数 `getMediaUrl(path: string | undefined): string`:
  - 空值处理：返回占位图（SVG Data URL 或内置占位图）。
  - 若已是绝对 URL（`http://` 或 `https://`）或以 `/` 开头则直接规范化。
  - 相对路径（如 `faces/alice.jpg` 或 `snapshots/2026/...`）：自动拼接 `/api/v1/media/content/${encodeURI(path)}`。

## 3. URL 状态机与搜索参数 (URL Search Params)

使用 TanStack Router 的 `validateSearch` 保证强类型驱动：

```ts
export const recordsSearchSchema = z.object({
  tab: z.enum(['recognitions', 'alarms', 'captures']).catch('recognitions'),
  view: z.enum(['table', 'grid']).catch('grid'),
  page: z.number().int().positive().optional().catch(1),
  pageSize: z.number().int().positive().max(100).optional().catch(20),
  cameraId: z.string().optional().catch(undefined),
  startTime: z.string().optional().catch(undefined),
  endTime: z.string().optional().catch(undefined),
  preset: z.string().optional().catch(undefined),
  // 识别专属
  matched: z.enum(['all', 'true', 'false']).optional().catch(undefined),
  personId: z.string().optional().catch(undefined),
  // 告警专属
  alarmType: z.string().optional().catch(undefined),
  // 抓拍专属
  targetType: z.string().optional().catch(undefined),
})
```

- **参数联动**：
  - 当切换 Tab 时：保留 `cameraId`、`startTime`、`endTime`、`preset`、`view`；重置各自细项过滤与 `page=1`。
  - 当改变时间或摄像头时：重置 `page=1`，重新触发当前 Tab 对应 Query。

## 4. 关键组件与交互设计

### 4.1 核心工具栏 (`RecordsToolbar`)
- 包含摄像头选择器（异步拉取摄像头列表）、时间预设（1h, 24h, 7d, 30d）与自定义日期时间选择器（复用现有 DateRangePicker 逻辑）。
- 视图切换按钮组：Table 与 Grid 视图切换。
- Tab 专属过滤项：
  - 识别 Tab：匹配状态切换（全部 / 命中白名单 / 陌生人）。
  - 告警 Tab：输入或选择告警类型。
  - 抓拍 Tab：输入或选择目标类别。

### 4.2 双视图渲染器 (Dual View)
1. **Grid / Card View (网格卡片画廊模式)**:
   - 响应式自适应网格（`grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6 gap-4`）。
   - 卡片核心要素：
     - 顶部/主视觉：切片小图（带加载占位和缩略高宽比 `aspect-square`）。
     - 底部信息区：状态 Badge（已匹配 / 陌生人 / 告警类型）、抓拍时间（格式化本地时间）、摄像头名称/ID、置信度进度条或百分比。
   - 点击整卡触发 `RecordDetailDialog`。
2. **Table View (密集表格模式)**:
   - 使用 `@tanstack/react-table`。
   - 列定义：缩略图、抓拍时间、关联摄像头、类型/人员姓名、相似度/置信度分值、属性预览、操作（查看详情）。

### 4.3 详情研判弹窗 (`RecordDetailDialog`)
- **原图与 Bounding Box Canvas 标注组件 (`BoundingBoxCanvas`)**：
  - 支持渲染全景原图 (`imagePath`)。
  - 在全景原图上叠加 SVG / Canvas 矩形框，并支持鼠标 Hover 高亮框体与显示标签。
  - 坐标适配：兼容 `[x1, y1, x2, y2]` 归一化（0~1）或绝对像素坐标自适应换算。
- **比对研判区（针对识别记录）**：
  - 现场人脸切片 vs 底库大头照左右对比（双图同高，显示比对分值与阈值判定）。
  - Top-N 候选人列表卡片（显示备选匹配人脸、姓名、相似度）。
- **属性与元数据区**：
  - 摄像头名称、抓拍时间、PTS 帧戳、算法包标识。
  - 扩展属性（`attributes` / `extra`）结构化折叠浏览。

## 5. 国际化 (i18n) 设计

在 `src/features/records/locales/` 提供完整的英/简/繁三语包，并在 `src/lib/i18n.ts` 中注册 `records` 命名空间：
- 键位设计：
  - `title`: "感知记录" / "Perception Records" / "感知記錄"
  - `tabs.recognitions`: "识别记录" / "Recognitions" / "識別記錄"
  - `tabs.alarms`: "告警记录" / "Alarms" / "告警記錄"
  - `tabs.captures`: "抓拍记录" / "Captures" / "抓拍記錄"
  - `views.grid`: "卡片视图" / "Card View" / "卡片視圖"
  - `views.table`: "表格视图" / "Table View" / "表格視圖"
  - `filters.*`: 摄像头、时间范围、比对状态等
  - `details.*`: 现场抓拍、底库照片、相似度、候选人等

## 6. 测试与验证策略

1. **单元测试 (Unit Tests)**:
   - `schema.test.ts`: 验证 Zod schema 校验各种合法/非法后端响应和 URL 搜索参数。
   - `records-api.test.ts`: 验证 API 请求构造与参数序列化。
2. **组件测试 (Component Tests - Vitest Browser)**:
   - `records-table.test.tsx` & `records-grid.test.tsx`: 验证数据加载、空状态、卡片渲染与点击详情事件。
   - `record-detail-dialog.test.tsx`: 验证详情弹窗开启、比对信息展示及 Canvas/SVG Box 渲染。
3. **全流程验证 (Quality Baseline)**:
   - `pnpm lint`, `pnpm test`, `pnpm build`, `make check`。
