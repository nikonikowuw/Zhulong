# 执行计划：算法包管理前端模块 (10-15-algorithms-frontend)

## 门禁与前置检查

- [x] 确认工作区基线通过：`pnpm --dir web test` 与 `pnpm --dir web build`。
- [x] 验证后端算法相关 API 接口契约。

---

## 阶段一：Zod 数据契约与类型定义 (Step 1)

- [x] **1.1 编写模式文件**：
  - 在 `web/src/features/algorithms/data/schema.ts` 中实现：
    - `algorithmSchema` / `algorithmListSchema` / `algorithmStatusSchema` / `algorithmTaskTypeSchema`；
    - 导出类型与安全解析函数。
- [x] **1.2 编写模式测试**：
  - 在 `web/src/features/algorithms/data/schema.test.ts` 覆盖解析与默认值测试。

---

## 阶段二：API 客户端与 TanStack Query 钩子 (Step 2)

- [x] **2.1 编写 API 层**：
  - 在 `web/src/features/algorithms/api/algorithms-api.ts` 中实现：
    - `fetchAlgorithms()`；
    - `installAlgorithm(file, onProgress)`；
    - `deleteAlgorithm(id)`；
    - `enableAlgorithm(id)` / `disableAlgorithm(id)`；
- [x] **2.2 编写 TanStack Query Hook**：
  - 在 `web/src/features/algorithms/hooks/use-algorithms.ts` 中实现：
    - `useAlgorithms()`、`useInstallAlgorithm()`、`useDeleteAlgorithm()`、`useToggleAlgorithm()`；
- [x] **2.3 单元测试覆盖**：
  - 编写 API 与 Hook 测试。

---

## 阶段三：核心 UI 组件实现 (Step 3)

- [x] **3.1 状态徽标与详情展示**：
  - `web/src/features/algorithms/components/algorithm-status-badge.tsx`；
- [x] **3.2 上传安装弹窗**：
  - `web/src/features/algorithms/components/algorithm-upload-dialog.tsx`：
    - 拖拽区域、500MB 大小检查、上传进度条、错误反馈。
- [x] **3.3 删除二次确认弹窗**：
  - `web/src/features/algorithms/components/algorithm-delete-dialog.tsx`；
- [x] **3.4 算法列表视图**：
  - `web/src/features/algorithms/components/algorithms-table.tsx`：
    - 骨架屏、空态引导、卡片/表格展示、启停切换操作。

---

## 阶段四：页面入口、路由与侧边栏接入 (Step 4)

- [x] **4.1 特性主入口**：
  - `web/src/features/algorithms/index.tsx`；
- [x] **4.2 路由声明**：
  - `web/src/routes/_authenticated/algorithms/index.tsx`；
- [x] **4.3 侧边栏导航**：
  - 更新 `web/src/components/layout/data/sidebar-data.ts` 增加「算法管理」。

---

## 阶段五：三语国际化与全量门禁检查 (Step 5)

- [x] **5.1 国际化文案补齐**：
  - `web/src/features/algorithms/locales/{en,zh-Hans,zh-Hant}.json`；
- [x] **5.2 全栈门禁检查**：
  - 执行 `pnpm --dir web lint`、`pnpm --dir web test`、`pnpm --dir web build`、`make check`。
