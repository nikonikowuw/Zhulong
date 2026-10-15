# 技术设计：实现识别结果前端叠加与记录 (10-14-face-result-frontend)

> 任务：`10-14-face-result-frontend`
> 父任务：`10-14-face-recognition-e2e`

---

## 1. 架构目标与数据链路

打通前端对算法推理结果的实时渲染与交互展示：
```txt
┌──────────────────────────────── 后端系统 ────────────────────────────────┐
│ Runner ➔ FrameBroker ➔ ZhulongEngine ➔ Go StreamHub.consumeAIResults    │
│                                                │                          │
│ /api/v1/cameras/events (SSE)                   ▼                          │
│ EventTypeAIResult ("ai_result") ──────────────────────────────────────────┼──┐
└───────────────────────────────────────────────────────────────────────────┘  │
                                                                               │ SSE Stream
┌──────────────────────────────── 前端系统 ────────────────────────────────┐   │
│ 1. 数据订阅层 (Hook):                                                        │   │
│    useAIResults(cameraId) ◄──────────────────────────────────────────────────┴───┘
│      ├─ Zod Schema 校验与不可信输入反序列化
│      ├─ cameraId 分流过滤
│      └─ 派发至 Canvas 渲染时钟与抓拍记录缓冲池
│
│ 2. 视频叠加层 (LiveFaceOverlay / Canvas 2D):
│    Canvas rAF 调度渲染 (零 React 重渲染):
│      ├─ 坐标映射: [x1, y1, x2, y2] ∈ [0.0, 1.0] ➔ 像素坐标
│      ├─ 命中底库: 翠绿边框 + 标牌 [姓名 相似度%]
│      ├─ 未命中: 琥珀黄边框 + 标牌 [未登记目标 相似度%]
│      ├─ 5 点关键点微点
│      └─ 工具栏一键开/关 AI 框叠加
│
│ 3. 实时抓拍流侧边栏 (LiveRecognitionFeed):
│    可折叠抽屉/面板:
│      ├─ 实时抓拍卡片（头像占位、姓名、相似度徽标、时间、摄像机名）
│      ├─ 状态筛选（全部 / 已识别 / 未登记）与姓名检索
│      └─ 会话级环形缓冲池（最大 50 条）
└───────────────────────────────────────────────────────────────────────────┘
```

---

## 2. 关键技术契约

### 2.1 Zod 数据契约 (`web/src/features/live/types/ai-result.ts`)
```ts
export const FaceCandidateSchema = z.object({
  person_id: z.string(),
  person_name: z.string(),
  similarity: z.number(),
})

export const FaceItemSchema = z.object({
  box: z.tuple([z.number(), z.number(), z.number(), z.number()]),
  quality: z.number().default(0),
  landmarks: z.array(z.tuple([z.number(), z.number()])).optional(),
  matched: z.boolean(),
  similarity: z.number(),
  is_fused: z.boolean().optional(),
  person_id: z.string().default(''),
  person_name: z.string().default(''),
  candidates: z.array(FaceCandidateSchema).optional(),
})

export const PersonItemSchema = z.object({
  track_id: z.number(),
  body_box: z.tuple([z.number(), z.number(), z.number(), z.number()]),
  body_score: z.number(),
  faces: z.array(FaceItemSchema).default([]),
})

export const AIResultEventSchema = z.object({
  camera_id: z.string(),
  task_id: z.string().optional(),
  task_type: z.string(),
  pts: z.number(),
  cost_ms: z.number(),
  persons: z.array(PersonItemSchema).default([]),
})
```

### 2.2 坐标转换与 Canvas 绘制规则
- **归一化坐标还原**：
  ```ts
  const x = box[0] * canvasWidth;
  const y = box[1] * canvasHeight;
  const w = (box[2] - box[0]) * canvasWidth;
  const h = (box[3] - box[1]) * canvasHeight;
  ```
- **视觉样式规范**：
  - `matched = true`：线宽 2px，颜色 `oklch(0.65 0.22 142)`（翠绿）；标牌文字白色，标牌底色半透明翠绿；
  - `matched = false`：线宽 2px，颜色 `oklch(0.75 0.18 55)`（琥珀黄），虚线 `[4, 4]`；
  - 5 点关键点：半径 3px 实心圆点；
  - 自动消退：超过 250ms 无新目标或收到空退出信标时立即擦除，杜绝幽灵框。

---

## 3. UI 交互设计

1. **监控工具栏（`LiveToolbar`）**：
   - 增加 `Bot` / `ScanFace` 图标按钮：“AI 人脸叠加”，支持一键开启/关闭；
   - 增加“实时抓拍”抽屉呼出按钮，带未读新识别动态微标；
2. **抓拍记录流（`LiveRecognitionFeed`）**：
   - 位于监控界面右侧，支持折叠与展开；
   - 包含快速搜索输入框、状态筛选（全部 / 已识别 / 未登记）与清空按钮；
3. **三语国际化**：
   - `zh-Hans`、`zh-Hant`、`en` 完善所有新增文案。
