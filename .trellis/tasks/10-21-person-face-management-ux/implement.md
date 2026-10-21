# 实现与验证规划 (Implement & Validation Plan)

## 1. 执行步骤分解 (Implementation Steps)

1. **Step 1: 国际化词条扩充 (i18n)**
   - 补充 `web/src/features/persons/locales/{en,zh-Hans,zh-Hant}.json` 中的新增文案（视图切换、首张人脸上传、抽屉头部、批量上传等）。
2. **Step 2: 新建人员一体化人脸上传 (`PersonsActionDialog`)**
   - 增加本地图片选择与预览、格式/大小校验。
   - 适配提交逻辑：创建人员成功后自动触发人脸上传，错误隔离与全局提示。
   - 补充/更新对应测试。
3. **Step 3: 人员详情与人脸管理抽屉 (`PersonsDetailSheet`)**
   - 采用 `@/components/ui/sheet` 替换原 `PersonsFacesDialog` 内部实现。
   - 扩充批量多选上传功能（`multiple` + 队列上传）。
   - 重构人脸卡片 UI，消除纯 Hover 依赖，提供触屏友好的操作栏与清晰的标签。
   - 保留契约字段与 testid，更新/扩展测试。
4. **Step 4: 卡片网格视图 (`PersonsGrid`) 与视图切换**
   - 新增 `PersonsGrid` 组件，支持分页、加载骨架、多选、卡片操作菜单。
   - 在主界面提供 Table ⇄ Grid 视图切换按钮。
   - 与现有批量删除工具栏联动。
5. **Step 5: 质量校验与自动化测试验证**
   - 运行前端单元测试与组件测试：`pnpm test -- src/features/persons/`
   - 运行全局静态分析与构建：`pnpm lint && pnpm build`
