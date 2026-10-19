# 工业级前端错误反馈与通知体系

## Goal

为 Zhulong 边缘运维宿主前端构建严谨、符合高水准商业级（如 Stripe/Linear）交互标准的「四级错误反馈与通知体系」，实现：
1. 后端 422 验证信封 `details: [{ field, code, message }]` 与 React Hook Form 的行内自动映射与焦点捕获；
2. 409 CAS 冲突与业务异常的模态框驻留消解，彻底告别「弹窗被关、数据丢失、右上角报错」的断层体验；
3. 异步耗时操作（诊断、人脸上传、状态同步）全面采用单例原生态状态变形通知（`toast.promise`）；
4. 全局网络层错误与基础设施异常的高可靠兜底。

---

## Requirements

### R1. 反馈分级闭环体系 (Feedback Hierarchy)
- **Field 级（行内字段）**：凡由用户输入引发的参数校验错误（HTTP 422 / 400，带 `details`），错误文本必须直接高亮于对应的表单输入框下方，光标自动 focus 首个出错字段，**严禁弹 Toast 干扰**。
- **Container 级（模态驻留与行内警告）**：对于并发版本冲突（HTTP 409 CAS）、探针失败等需要上下文决断的错误，模态框（Dialog）必须保持打开，用户已输入的数据草稿严禁丢失，通过弹窗内的 Inline Alert 展示具体原因与重试/刷新入口。
- **Morphing Toast 级（状态平滑变形通知）**：异步长耗时任务（如 RTSP 探针连通性诊断、人脸特征提取、批量操作）统一采用单个 Toast 状态机（Loading 旋转态 → 原地平滑变为 Success 绿态 / Error 红态），杜绝先弹一个 loading 弹窗再叠一个 error 弹窗。
- **Global 级（基础设施与意外兜底）**：会话过期（401）、权限拒绝（403）、服务端崩溃（500）与网络彻底断开，由全局基础设施层拦截处理。

### R2. 自动化表单错误映射器 (`handleFormError`)
- 提取并封装统一的 `handleFormError<TFieldValues>(form, error, options)` 工具函数：
  - 自动解包 Axios 响应中的 `error.response?.data?.details`；
  - 校验 `detail.field` 是否存在于当前表单注册的字段中；
  - 若存在，自动执行 `form.setError(field, { type: 'server', message: detail.message })` 并聚焦；
  - 若错误中包含非字段级错误（或无匹配字段），返回 `handled: false` 或触发可选的 fallback 逻辑；
  - 与 TanStack Query 的 `MutationCache` 协同，自动抑制针对已行内处理错误的全局 Toast。

### R3. 模态驻留与 CAS 冲突消解
- 在 `CamerasActionDialog` 等高频交互对话框中，当遭遇后端返回的 409 CAS 冲突时：
  - 弹窗不予关闭；
  - 在对话框顶部展现带刷新或确认覆盖选项的警示 Banner；
  - 避免输入草稿被无情清空。

### R4. 声明式 `toast.promise` 落地
- 针对探针主动诊断（`cameras-diagnose-dialog`）、批量启停与批量删除（`cameras-data-table-bulk-actions`）、人脸特征直传（`persons-faces-dialog`）等异步操作，收拢分散的 `toast.loading`、`toast.success`、`toast.error`，统一采用声明式 Promise 状态流转。

---

## Acceptance Criteria

- [ ] **AC1（行内字段回填）**：当 API 返回 422 且包含 `details: [{ field: "name", message: "..." }]` 时，表单对应字段标红并展示后端多语言错误文案，右上角不出现重复的 Toast。
- [ ] **AC2（非字段错误回落）**：当表单遭遇非字段级错误（如 400 业务规则拦截但无 details）时，能够优雅回退至对话框内的全局提示或 Toast，不产生双弹窗。
- [ ] **AC3（409 模态驻留）**：摄像头或核心资源编辑时触发 CAS 409 冲突，弹窗不被强制关闭，表单字段保持用户编辑态。
- [ ] **AC4（状态变形 Toast）**：摄像机诊断与批量操作使用 `toast.promise`，操作过程中屏幕仅有单个 Toast 元素进行图标与状态变形，无叠加与闪烁。
- [ ] **AC5（测试与规范）**：`handleFormError` 具备 100% 单元测试覆盖；前端 `pnpm lint` 与 `pnpm build` 全绿无警告，TypeScript 严格零 `any`。

---

## Non-Goals & Constraints

- 不引入庞大的外部状态库或重型 UI 框架，完全基于现有 `react-hook-form`、`sonner`、`@tanstack/react-query` 构筑；
- 遵循 `backend/error-handling.md` 的后端标准信封契约；
- 遵循 `frontend/development-guidelines.md` 的所有者前端铁律。
