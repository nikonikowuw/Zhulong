# 设计与实现前端感知记录模块 (records feature)

## Goal

在前端 (`web/src/features/records`) 设计并实现感知记录管理功能，对接后端 `internal/record` 提供的识别记录（Recognitions）、告警记录（Alarms）和抓拍记录（Captures）接口，提供结构化的时间与空间多维检索、图文结合展示、目标框（Bounding Box）高保真预览与全量中英国际化支持。

## Background & Evidence

1. **后端能力就绪**：
   - 识别记录：`GET /api/v1/records/recognitions` & `GET /api/v1/records/recognitions/:id`
   - 告警记录：`GET /api/v1/records/alarms` & `GET /api/v1/records/alarms/:id`
   - 抓拍记录：`GET /api/v1/records/captures` & `GET /api/v1/records/captures/:id`
   - 媒体资源：静态切片与快照通过 `GET /api/v1/media/content/{path}` 访问。
2. **前端架构模式**：
   - 特性切片：`src/features/records/{api, components, data, hooks, locales, index.tsx}`
   - 路由挂载：基于 TanStack Router，单路由 `/_authenticated/records/index.tsx`
   - 状态保全：状态通过 URL Search Params 保留（`tab`, `page`, `pageSize`, `startTime`, `endTime`, `cameraId`, `view` 等）
   - 数据获取与验证：TanStack Query + Zod 运行时安全解析
   - 国际化：`web/src/locales/` 与 `web/src/features/records/locales/{zh-Hans, zh-Hant, en}.json`

## Requirements

### 1. 页面架构与路由决策 (Page & Navigation)
- **采用单页面 + 顶部 Tab 切换（方案 A）**：
  - 路由：`/_authenticated/records`，侧边栏 General 下新增一级菜单「Records（感知记录）」。
  - 顶部 Tabs：`recognitions`（识别记录，默认）、`alarms`（告警记录）、`captures`（抓拍记录）。
  - 上方公共工具栏共享通用过滤项：摄像头选择器、时间范围选择器（支持快速预设与自定义起止时间）。
  - 切换 Tab 时保留已选定的时间与摄像头过滤上下文，重置各自的细分属性与页码。

### 2. 业务功能 (Core Features)
- **多维检索与过滤**：
  - 公共过滤：
    - 摄像头选择（所有摄像头 / 指定摄像头下拉单选）。
    - 时间范围（最近 1 小时、24 小时、7 天、30 天预设快捷键，或自定义 RFC3339 时间范围）。
  - 专属过滤：
    - 识别记录：比对状态（全部 / 命中目标 / 未匹配陌生人）、目标人员名称/ID 快速过滤。
    - 告警记录：告警类型（如侵入、越界等，支持手动输入或下拉选择）。
    - 抓拍记录：目标类型（如 face、body 等）。
- **双视图呈现 (Dual View Modes)**：
  - **表格视图 (Table View)**：适合高密度快速审阅，展示微缩图、抓拍时间、摄像头、置信度/相似度、目标类型、操作列。
  - **卡片画廊视图 (Card/Grid View)**：适合直观审片，采用图片为主视觉的大卡片流，悬浮/底部显示关键元数据及标注状态。
  - 视图切换持久化保存在 URL (`view=grid` 或 `view=table`)。
- **详情审片与研判 (Detail Dialog / Drawer)**：
  - 视觉原图与画框渲染：展示大图，并在其上叠加 Canvas/SVG 绘制原图 Bounding Box（人脸框、人体框，支持归一化坐标自适应缩放）。
  - 识别详情：展示现场人脸切片 vs 匹配的底库照片左右对比、相似度百分比、Top-N 候选人列表（含候选人底库照、姓名、置信度）。
  - 告警与抓拍详情：展示告警类型/目标类型、置信度分值、发生时间、摄像头，以及结构化扩展属性（Extra / Attributes JSON 树状展示）。
- **图片加载体验与性能**：
  - 列表图片采用原生懒加载 (`loading="lazy"`) 与图片预加载加载失败 Fallback。
  - 封装统一的媒体图片 URL 构造工具（自动映射 `/api/v1/media/content/{path}`），保证路径安全。

### 3. 非功能性需求 (Non-Functional Requirements)
- **代码规范**：符合 Zhulong 前端代码规范，无 `any` 断言，所有 API 响应经 Zod safeParse，通过 `src/lib/api-client.ts` 请求。
- **国际化**：中英三语（`zh-Hans`, `zh-Hant`, `en`），所有 UI 文本、字段名、枚举文案必须走 i18n。
- **测试覆盖**：覆盖 API 调用测试、Zod Schema 校验测试、核心表格与卡片渲染测试。

## Acceptance Criteria

- [x] 在侧边栏导航和路由中成功接入 `/_authenticated/records`，支持 `tab=recognitions|alarms|captures` 切换。
- [x] 识别、告警、抓拍三类数据的分页、过滤（摄像头、时间、细项条件）全部工作正常，URL 参数实时同步。
- [x] 支持 Table / Grid 两种视图切换，并在切换 Tab 时保持稳定。
- [x] 点击任意项可弹出详情弹窗，原图、标注框（Bounding Box）、对比信息与额外属性正确渲染。
- [x] 国际化文案覆盖完整（zh-Hans, zh-Hant, en）。
- [x] 编写并通过对应的自动化测试（API 测试、Schema 测试、组件测试）。
- [x] `make check` 通过，无类型错误，无 ESLint 警告，无测试失败。

## Out of Scope

- 历史记录导出文件（CSV/Excel/ZIP 导出），等待后端支持导出接口后再行追加。
- 历史记录物理删除（由边缘存储清理策略自动回收）。
