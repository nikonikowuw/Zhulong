# 算法包管理前端模块 (10-15-algorithms-frontend)

## Goal

在 Web 前端实现完整的「算法包管理」业务模块：提供算法包安装上传、列表与详情卡片查看、启停与重置调度、以及安全卸载删除能力，消费后端 `/api/v1/algorithms` 系列已就绪的工业级 API。

## Requirements

- **R1 路由与导航集成**：
  - 在侧边栏导航（`sidebar-data.ts`）的 `General` 分组中增加「算法管理 / Algorithms」（`/algorithms`）；
  - 遵循 TanStack Router 约定，创建文件路由 `src/routes/_authenticated/algorithms/index.tsx`。
- **R2 算法包列表展示**：
  - 展示所有已安装的算法包；
  - 核心展示维度：算法名称与机器 ID、版本号、任务类型徽标（目标检测 / 图像分类 / 结构化人脸）、目标平台（架构与加速后端，如 `x86_64 cpu` / `aarch64 rknn`）、预估算力（`computeCost`）与内存消耗（`memoryMb`）、运行状态徽标（`ACTIVE` / `DISABLED` / `RUNNING` / `CRASHED` 等）；
  - 支持按算法名称或 ID 进行快速客户端搜索筛选。
- **R3 算法包上传安装**：
  - 提供标准的「上传算法包」弹窗（`algorithm-upload-dialog.tsx`）；
  - 支持拖拽或点击选择 `.tar.gz` 或 `.zip` 归档文件；
  - 前端体积与格式预检（≤ 500MB，非归档文件拦截）；
  - 采用 `multipart/form-data` 流式提交至 `POST /api/v1/algorithms/install`；
  - 包含上传进度条，并在安装失败时精准映射错误信封（如平台不匹配、算力超预算、解压炸弹防御拒绝等）。
- **R4 算法包安全卸载**：
  - 提供删除二次确认弹窗（基于 `ConfirmDialog`），防止误触；
  - 调用 `DELETE /api/v1/algorithms/:id`，卸载成功后自动使 TanStack Query 缓存失效并触发列表刷新。
- **R5 算法启停与调度控制**：
  - 支持对算法包进行一键「启用」（`POST /:id/enable`）与「停用」（`POST /:id/disable`）；
  - 状态切换时提供明确的乐观更新或加载指示。
- **R6 规范化特性切片拓扑**：
  - 遵循 `src/features/algorithms/{api,hooks,components,data,locales}` 结构规范；
  - 路由仅负责视图包装，状态由 TanStack Query 独占管理；不可信输入通过 Zod 解析，严禁 `any`。
- **R7 完备异步三态 (Loading / Empty / Error)**：
  - 加载态：标准 Table/Card 骨架屏；
  - 空数据态：未安装任何算法包时的插画与「上传第一个算法包」行动点引导；
  - 错误态：网络异常或服务端故障时的重试按钮与错误说明。
- **R8 三语国际化 (i18n)**：
  - 覆盖 `zh-Hans`、`zh-Hant`、`en` 3 种语言，JSX 中严禁任何硬编码字符串。
- **R9 真实无头浏览器质量门禁**：
  - 编写 Vitest 真实 Chromium 交互测试，通过 `make check`。

## Acceptance Criteria

- [x] 侧边栏与 TanStack Router 路由正常联通，无类型报错。
- [x] 算法列表能正确拉取并渲染后端算法包（包含任务类型、平台架构、算力点数）。
- [x] 上传弹窗支持拖拽与文件选择，上传时显示进度，成功后自动回显在列表中。
- [x] 上传非法文件或超限时，展示准确的错误提示。
- [x] 启停切换与删除流程正常，删除弹窗二次确认有效。
- [x] 异步三态完备（骨架屏、空态引导、错误重试）。
- [x] 三语国际化完整，无缺失翻译。
- [x] `pnpm --dir web lint`、`pnpm --dir web test`、`pnpm --dir web build` 全部绿灯。
