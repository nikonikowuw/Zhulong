# 工业级前端错误反馈与通知体系 — 执行计划

## 实施顺序与检查点

- [x] **1. 基础设施建设：封装与验证 `handleFormError`**
  - [x] 1.1 在 `web/src/lib/form-error.ts` 实现 `handleFormError`，支持 details 解析、字段映射、409 冲突回调与未匹配回退；
  - [x] 1.2 在 `web/src/lib/form-error.test.ts` 编写完整单元测试，覆盖单字段、多字段、未知字段、409 冲突及非 Axios 异常场景；
  - [x] 1.3 验证单测全部绿灯：`npx vitest run src/lib/form-error.test.ts`。

- [x] **2. 核心编辑弹窗升级：表单字段级联动与 409 模态驻留**
  - [x] 2.1 改造 `web/src/features/cameras/components/cameras-action-dialog.tsx`：接入 `handleFormError`，409 CAS 模态驻留消解；
  - [x] 2.2 改造 `web/src/features/persons/components/persons-action-dialog.tsx`：接入 `handleFormError`，姓名/工号字段自动标红；
  - [x] 2.3 改造 `web/src/features/settings/storage/components/storage-config-form.tsx`：接入 `handleFormError` 与存储路径读写测试 `toast.promise`；
  - [x] 2.4 改造 `web/src/features/settings/network/components/interface-edit-dialog.tsx`：接入 `handleFormError` 网卡参数校验；
  - [x] 2.5 改造 `web/src/features/settings/network/components/ping-dialog.tsx`：接入 `handleFormError` 与网络探测 `toast.promise`；
  - [x] 2.6 改造 `web/src/features/settings/time/components/time-form.tsx`：接入 `handleFormError` 与 NTP 校时、RTC 写入 `toast.promise`；
  - [x] 2.7 改造 `web/src/features/auth/components/login-form.tsx` 与 `init-form.tsx`：接入 `handleFormError` 凭据校验；
  - [x] 2.8 改造 `web/src/features/persons/components/persons-faces-dialog.tsx`：接入人脸特征提取 `toast.promise`；
  - [x] 2.9 改造 `web/src/features/algorithms/components/algorithm-upload-dialog.tsx`：接入算法包验包与安装进度反馈。

- [x] **3. 异步耗时操作升级：`toast.promise` 状态变形通知**
  - [x] 3.1 改造 `web/src/features/cameras/components/cameras-diagnose-dialog.tsx`：`toast.promise` 原地状态流转；
  - [x] 3.2 优化 `web/src/features/cameras/components/cameras-data-table-bulk-actions.tsx`：批量聚合状态变形流转。

- [x] **4. 质量审查与回归验证**
  - [x] 4.1 运行前端 Lint：`cd web && pnpm lint` (0 警告 0 报错)；
  - [x] 4.2 运行前端全量生产构建：`cd web && pnpm build` (生产产物编译正常)；
  - [x] 4.3 运行全量单元测试并确认无破损 (35 个单测全绿)；
  - [x] 4.4 规范更新：`.trellis/spec/frontend/development-guidelines.md` 固化 2.5 节规范。
