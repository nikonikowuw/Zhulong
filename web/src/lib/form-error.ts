import { AxiosError } from 'axios'
import { type FieldValues, type Path, type UseFormReturn } from 'react-hook-form'
import { type ApiResponse } from '@/lib/api-client'

export interface FieldDetail {
  field: string
  code: string
  message: string
}

export interface HandleFormErrorOptions<T extends FieldValues> {
  /**
   * 字段名映射表。当后端返回的 JSON 路径与表单字段名存在差异时使用
   * 例如：{ 'mainStream.rtspUrl': 'mainRtspUrl' }
   */
  fieldMap?: Record<string, Path<T>>
  /**
   * 当发生 409 CAS 版本冲突时的回调
   */
  onConflict?: (error: AxiosError<ApiResponse>, serverMessage: string) => void
  /**
   * 当错误未能匹配到任何表单字段，或属于非字段级错误时的回调
   */
  onUnhandled?: (message: string, error: AxiosError<ApiResponse>) => void
  /**
   * 是否自动聚焦首个匹配的错误字段（默认 true）
   */
  shouldFocus?: boolean
}

export interface FormErrorResult {
  /** 是否存在 AxiosError 响应 */
  isAxiosError: boolean
  /** HTTP 状态码 */
  status?: number
  /** 是否为 409 CAS 冲突 */
  isConflict: boolean
  /** 是否成功将至少一个 detail 绑定到当前表单字段 */
  hasFieldErrors: boolean
  /** 成功绑定的字段名列表 */
  matchedFields: string[]
  /** 服务端返回的顶层消息或未匹配的通用消息 */
  serverMessage: string
  /** 原始 details 数组 */
  details: FieldDetail[]
}

/**
 * 工业级表单错误自适应处理器
 * 负责将后端标准响应信封中的 details 自动映射到 React Hook Form 的字段红字与焦点上
 */
export function handleFormError<T extends FieldValues>(
  form: UseFormReturn<T>,
  error: unknown,
  options: HandleFormErrorOptions<T> = {}
): FormErrorResult {
  const { fieldMap = {}, onConflict, onUnhandled, shouldFocus = true } = options

  if (!(error instanceof AxiosError)) {
    const rawMsg = error instanceof Error ? error.message : String(error)
    return {
      isAxiosError: false,
      isConflict: false,
      hasFieldErrors: false,
      matchedFields: [],
      serverMessage: rawMsg,
      details: [],
    }
  }

  const status = error.response?.status
  const respData = error.response?.data as ApiResponse | undefined
  const details: FieldDetail[] = Array.isArray(respData?.details)
    ? respData.details
    : []
  const serverMessage =
    typeof respData?.message === 'string' && respData.message.trim().length > 0
      ? respData.message
      : error.message || 'Operation failed'

  const isConflict = status === 409

  if (isConflict && onConflict) {
    onConflict(error, serverMessage)
  }

  const matchedFields: string[] = []
  const registeredValues = form.getValues()

  if (details.length > 0) {
    let focused = false

    for (const d of details) {
      if (!d.field || !d.message) continue

      const targetField = (fieldMap[d.field] || d.field) as Path<T>

      // 递归检测目标字段是否有效注册在表单中（支持浅层与深层点分路径）
      const isKnownField =
        targetField in registeredValues ||
        String(targetField)
          .split('.')
          .reduce<unknown>((acc, part) => {
            return acc && typeof acc === 'object' && part in acc
              ? (acc as Record<string, unknown>)[part]
              : undefined
          }, registeredValues) !== undefined

      if (isKnownField) {
        form.setError(
          targetField,
          {
            type: 'server',
            message: d.message,
          },
          { shouldFocus: shouldFocus && !focused }
        )
        focused = true
        matchedFields.push(String(targetField))
      }
    }
  }

  const hasFieldErrors = matchedFields.length > 0

  if (!hasFieldErrors && !isConflict && onUnhandled) {
    onUnhandled(serverMessage, error)
  }

  return {
    isAxiosError: true,
    status,
    isConflict,
    hasFieldErrors,
    matchedFields,
    serverMessage,
    details,
  }
}
