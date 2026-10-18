# PRD: 算法配置 Schema 动态表单组件 (Dynamic Algorithm Config Form)

## Goal

升级分析任务配置弹窗（`ChannelTaskDialog`）中的算法参数配置交互：将原本单一、生硬的手写 JSON 文本框（`Textarea`），升级为**「基于算法包 `config.schema.json` 的动态表单渲染组件 + 高级 JSON 源码模式」**的双模态交互体验。使得非技术用户可以通过直观的滑块、开关和下拉框完成调参，同时保留高级技术人员直接编辑、复制和粘贴 JSON 配置的能力。

---

## Requirements

### R1. 动态 Schema 属性解析与 UI 映射 (Dynamic Schema Field Mapping)
- 解析算法元数据中的 `configSchemaJson`（遵循 JSON Schema Draft-07 标准规范），支持以下常见数据类型的自动组件映射：
  1. **数值范围型（`type: "number" | "integer"` 且包含 `minimum` / `maximum`）**：
     - 渲染为 **Slider 滑块 + 数字输入框联动**；
     - 支持 `step` 步长（如浮点数步长 0.01 或 0.05，整数步长 1）；
     - 如人脸识别算法的置信度阈值 `threshold`（0.0 ~ 1.0）。
  2. **普通数值型（`type: "number" | "integer"` 无边界限制）**：
     - 渲染为数值输入框（`type="number"`），整数自动过滤浮点。
  3. **布尔型（`type: "boolean"`）**：
     - 渲染为优雅的 **Switch 开关**；
     - 如 `enable_mask_filter`、`save_crop` 等开关。
  4. **枚举型（声明了 `enum: [...]`）**：
     - 渲染为 **Select 下拉选择框**；
     - 优先展示枚举对应的值。
  5. **字符串型（`type: "string"` 无 enum）**：
     - 渲染为标准 **Input 文本框**。
  6. **说明与提示（`description` / `title`）**：
     - 字段标题优先使用 `title`，无 `title` 时使用属性名（Key）；
     - 若包含 `description`，提供 Tooltip 悬浮提示或下方次级文本说明。

### R2. 零参算法友好空态 (Graceful Zero-Config State)
- 当算法未声明 `config.schema.json`、或 Schema 为 `{}`、或 `properties` 为空对象时：
  - 不展示空白表单或裸 JSON 框；
  - 友好展示“该算法无需额外参数配置（零参算法）”状态标识，降低用户认知负担。

### R3. 「表单模式 ⇋ JSON 源码模式」双向同步 (Dual-Mode Bi-Directional Sync)
- 提供明显的切换入口（如「表单配置 / JSON 源码」Segmented Toggle 或按钮）：
  - **表单模式**：面向普通用户，修改字段实时触发 JSON 序列化并更新任务草稿；
  - **JSON 模式**：面向算法工程师/运维人员，提供带语法高亮/格式化的代码编辑体验，方便整段复制、备份与脚本粘贴；
  - **切模容错**：当在 JSON 模式下输入了不合法的 JSON 时，尝试切回表单模式时应阻断并友好提示“JSON 格式不合法，请先修正语法”。

### R4. 默认值预填充与一键重置 (Default Value Seeding & Reset)
- 初次勾选未配置过的算法时，自动从 `config.schema.json` 中提取各字段的 `default` 声明作为初始值；
- 在参数配置区提供「重置为默认值」快捷操作。

### R5. 视觉与国际化契约 (UI System & i18n)
- 100% 遵从项目 Shadcn/UI 与 Apple-style 设计系统，支持深色（Dark）和浅色（Light）模式自适应；
- 支持中、繁、英（`zh-Hans` / `zh-Hant` / `en`）三语国际化字典，文案无硬编码。

### R6. 质量与测试保障 (Testing & Quality)
- 为动态表单组件编写独立的 Vitest 单元测试；
- 覆盖不同字段类型渲染、双向同步、默认值重置、非法 JSON 切换拦截、零参算法等测试场景；
- 保证 `pnpm test` 与 `pnpm run build` 全部无警告/无报错通过。

---

## Acceptance Criteria

- [x] **AC1**: 实现 `AlgoConfigDynamicForm` 独立组件，正确解析 `configSchemaJson` 并映射为 Slider、Switch、Select、Input 等对应控件。
- [x] **AC2**: 在 `ChannelTaskDialog` 中完成集成，勾选算法后优先展示动态表单，支持切换至 JSON 高级模式。
- [x] **AC3**: 表单模式修改数据与 JSON 文本实时双向同步，切回表单时能阻断非法 JSON。
- [x] **AC4**: 零参算法（空 Schema 或无 properties）展示清晰的“无需配置”占位提示。
- [x] **AC5**: 提供「重置默认值」按钮，可一键复原至 Schema 中定义的 `default` 设定。
- [x] **AC6**: `tasks` 模块中/繁/英语言包补充完备，布局支持 RTL 逻辑属性。
- [x] **AC7**: 前端单元测试 100% 通过，无类型与构建错误。
