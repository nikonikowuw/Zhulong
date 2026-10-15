# 执行计划：实现识别结果前端叠加与记录 (10-14-face-result-frontend)

## 门禁与前置检查

- [ ] 确认工作区基线通过：`pnpm --dir web test` 与 `pnpm --dir web build`。
- [ ] 检查 `web/src/features/live/` 既有结构与组件职责。

---

## 阶段一：Zod 数据契约与类型定义 (Step 1)

- [ ] **1.1 定义 Zod Schema 与 TypeScript 类型**：
  - 在 `web/src/features/live/types/ai-result.ts` 中实现：
    - `FaceCandidateSchema` / `FaceItemSchema` / `PersonItemSchema` / `AIResultEventSchema`；
    - 导出类型与安全解析函数 `parseAIResultEvent`；
- [ ] **1.2 单元测试覆盖**：
  - 在 `web/src/features/live/types/ai-result.test.ts` 中编写 Schema 校验测试（有效合法载荷、畸形字段防御、默认值填充）。

---

## 阶段二：SSE 数据订阅 Hook 实现 (Step 2)

- [ ] **2.1 编写 `useAIResults` Hook**：
  - 在 `web/src/features/live/hooks/use-ai-results.ts` 中实现：
    - 监听 `/api/v1/cameras/events` SSE 产生的 `ai_result` 事件；
    - 支持按 `cameraId` 过滤当前视频单元格对应的感知事件；
    - 维持最新人脸项列表，带 300ms 自动消退计时器（避免画面静止或无目标时幽灵框残留）；
- [ ] **2.2 单元测试覆盖**：
  - 编写 Hook 单元测试，验证事件过滤与消退时序。

---

## 阶段三：Canvas 高性能人脸框叠加层 (Step 3)

- [ ] **3.1 编写 `LiveFaceOverlay` 组件**：
  - 在 `web/src/features/live/components/live-face-overlay.tsx` 中实现：
    - 绝对定位与下层视频画面 1:1 对齐的 Canvas 叠加层；
    - 响应容器 ResizeObserver 动态同步宽度与高度；
    - 绘制归一化人脸框（已匹配绿色、未匹配琥珀黄色）、姓名/相似度标牌、5 点关键点微点；
- [ ] **3.2 集成至播放单元格**：
  - 在 `web/src/features/live/components/live-player-cell.tsx` 中挂载叠加层；
  - 仅在对应单元格分配了摄像机且开启 AI 叠加时生效。

---

## 阶段四：实时抓拍流侧边栏与工具栏开关 (Step 4)

- [ ] **4.1 编写 `LiveRecognitionFeed` 组件**：
  - 在 `web/src/features/live/components/live-recognition-feed.tsx` 中实现可折叠抽屉/面板；
  - 实时展示进画面的抓拍记录卡片（姓名、相似度、时间、摄像机名、命中状态徽标）；
  - 支持按人员姓名搜索与按命中状态筛选；
- [ ] **4.2 工具栏联动与状态管理**：
  - 在 `web/src/features/live/components/live-toolbar.tsx` 中增加“AI 框叠加”一键开关与“实时抓拍”抽屉呼出按钮；
  - 在 `useLiveGrid` 中管理 `showAIOverlay` 与 `isFeedOpen` 状态。

---

## 阶段五：三语国际化与全量门禁检查 (Step 5)

- [ ] **5.1 国际化文案补齐**：
  - 更新 `web/src/features/live/locales/{zh-Hans,zh-Hant,en}.json`，添加所有新增 UI 文案；
- [ ] **5.2 全栈门禁检查**：
  - 运行：
    ```bash
    pnpm --dir web lint
    pnpm --dir web test
    pnpm --dir web build
    make check
    ```
