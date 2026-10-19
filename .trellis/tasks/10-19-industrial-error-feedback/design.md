# 工业级前端错误反馈与通知体系 — 技术设计

## 1. 架构拓扑与数据契约

### 1.1 后端响应契约（来源：`.trellis/spec/backend/error-handling.md`）
后端 `internal/httputil` 返回标准信封结构：
```typescript
export interface FieldDetail {
  field: string     // 对应的请求 JSON 字段名（如 rtspUrl, fps, name 等）
  code: string      // 稳定机器错误码（如 REQUIRED, MIN_VALUE）
  message: string   // 按 Accept-Language 本地化后的用户可读错误文本
}

export interface ApiResponse<T = unknown> {
  code: string
  message: string
  data: T | null
  details?: FieldDetail[]
}
```

### 1.2 前端反馈层级架构图

```txt
┌────────────────────────────────────────────────────────────────────────┐
│                        前端网络响应拦截 / 捕获                         │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │
                     ┌──────────────┴──────────────┐
                     ▼                             ▼
             [ HTTP 2xx 成功 ]             [ HTTP 4xx / 5xx 异常 ]
                     │                             │
          是否为异步 Promise 任务?                 分流与判定
             /               \                     │
           Yes                No                   ├─► HTTP 401 ───────► 重定向登录 (auth.reset)
            │                  │                   │
    toast.promise 成功态    直接返回               ├─► HTTP 422 ───────► 是否在表单内?
    (绿色勾选，原地平滑)                           │                     ├─ 是: handleFormError
                                                   │                     │      (挂到字段, suppressToast)
                                                   │                     └─ 否: 全局/局部弹窗兜底
                                                   │
                                                   ├─► HTTP 409 CAS ───► 模态驻留 + Inline Alert
                                                   │
                                                   └─► HTTP 500/离线 ──► 全局 Toast 兜底 (带去重 ID)
```

---

## 2. 核心抽象设计

### 2.1 表单错误适配器：`handleFormError` (`web/src/lib/form-error.ts`)

为了让所有使用 `react-hook-form` 的业务组件能用一行代码完成工业级联动，设计强类型工具函数：

```typescript
import { type FieldValues, type UseFormReturn, type Path } from 'react-hook-form'
import { AxiosError } from 'axios'
import { type ApiResponse } from '@/lib/api-client'

export interface HandleFormErrorOptions<T extends FieldValues> {
  /**
   * 当发生 409 CAS 冲突时的回调（若不传则默认不关闭表单）
   */
  onConflict?: (error: AxiosError<ApiResponse>) => void
  /**
   * 当错误未能匹配到任何表单字段时的回退处理（如设置通用行内错误信息）
   */
  onUnhandled?: (message: string, error: AxiosError<ApiResponse>) => void
  /**
   * 是否自动聚焦首个出错字段（默认 true）
   */
  shouldFocus?: boolean
}

export interface FormErrorResult {
  /** 是否成功将至少一个 detail 绑定到表单字段 */
  hasFieldErrors: boolean
  /** 未能映射到表单字段的通用错误消息 */
  unhandledMessage?: string
  /** 是否为 409 CAS 冲突 */
  isConflict: boolean
}

export function handleFormError<T extends FieldValues>(
  form: UseFormReturn<T>,
  error: unknown,
  options: HandleFormErrorOptions<T> = {}
): FormErrorResult {
  // 1. 类型守卫提取 AxiosError
  // 2. 判定 409 Conflict 并执行 onConflict
  // 3. 遍历 details，通过 form.setError(field as Path<T>, { type: 'server', message }) 绑定
  // 4. 若无法绑定至任何字段，调用 onUnhandled
}
```

### 2.2 状态变形通知：`toastPromise` 模式标准

针对所有耗时操作，淘汰原本散落的 `toast.loading` + `try...catch` 手工改写模式，使用标准化模式：

```typescript
// 规范范式：
toast.promise(diagnoseMutation.mutateAsync(cameraId), {
  loading: t('toast.diagnosing'),
  success: (data) => t('toast.diagnoseSuccess', { latency: data.latencyMs }),
  error: (err) => err instanceof Error ? err.message : t('toast.diagnoseFailed'),
})
```

### 2.3 CAS 冲突模态驻留体验标准

在 `CamerasActionDialog` 等编辑模态中：
1. 捕获到 409 CAS 冲突后，**不执行 `setOpen(null)`**；
2. 保持表单数据不动；
3. 在 Dialog 头部下方渲染 `Alert variant="destructive"`，提供「配置已被他人修改，请刷新并核对后重试」提示与一键重新拉取最新 Revision 的按钮。

---

## 3. 改造范围与兼容性考量

| 模块 | 当前状态 | 目标工业级状态 |
| :--- | :--- | :--- |
| `web/src/lib/form-error.ts` | 不存在 | 新建，提供 `handleFormError` 标准实现与单测 |
| `CamerasActionDialog` | 409 关闭弹窗且无行内字段绑定 | 接入 `handleFormError`，422 自动标红字段，409 模态驻留提示 |
| `PersonsActionDialog` | 通用 catch，无行内字段回填 | 接入 `handleFormError`，姓名冲突或验证失败直接标红 Input |
| `CamerasDiagnoseDialog` | 手动 loading 态与局部 Toast | 接入 `toast.promise` 状态原地演进 |
| `CamerasDataTableBulkActions` | `Promise.allSettled` 手动更新 | 保持 `suppressGlobalToast: true`，完善进度到完成的单例提示 |

---

## 4. 回滚与安全考量

- 所有的改造均不侵入后端 API，保持前后端 HTTP 协议 100% 兼容；
- 若某个表单字段未能在 `details` 中匹配，`handleFormError` 会自动降级调用 `onUnhandled` 或保留错误文本，绝不会发生静默吞错；
- TypeScript 开启完全泛型推断，`form.setError` 强校验表单键名，防止因拼写错误产生类型漏洞。
