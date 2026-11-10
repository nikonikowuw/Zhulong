import axios, { AxiosError, type AxiosResponse } from 'axios'
import i18n from '@/lib/i18n'

export interface ApiResponse<T = unknown> {
  code: string
  message: string
  data: T
  details?: Array<{
    field: string
    code: string
    message: string
  }>
}

export const apiClient = axios.create({
  baseURL: '/api/v1',
  withCredentials: true,
  headers: {
    'Content-Type': 'application/json',
  },
})

/**
 * 把一个 `responseType: 'blob'` 请求体里意外出现的 JSON 错误信封还原成错误。
 *
 * Axios 在 blob 模式下不会反序列化响应体，`error.response.data` 是个 Blob，
 * 于是 `data.message` 恒为 `undefined`。调用方需要显式识别并读回文本。
 */
export async function readBlobErrorEnvelope(
  blob: Blob
): Promise<ApiResponse<unknown> | null> {
  try {
    const parsed: unknown = JSON.parse(await blob.text())
    if (
      parsed &&
      typeof parsed === 'object' &&
      'code' in parsed &&
      'message' in parsed
    ) {
      return parsed as ApiResponse<unknown>
    }
  } catch {
    // 不是 JSON：保持原样返回 null，由调用方决定回落文案。
  }
  return null
}

// 请求拦截器：自动注入当前语言偏好 Accept-Language
apiClient.interceptors.request.use((config) => {
  if (i18n.language) {
    config.headers['Accept-Language'] = i18n.language
  }
  return config
})

// 响应拦截器：自动解包信封与错误信息转换
apiClient.interceptors.response.use(
  (response: AxiosResponse<ApiResponse>) => {
    // 后端返回标准 envelope 结构 { code, message, data }
    return response
  },
  (error: unknown) => {
    if (error instanceof AxiosError) {
      const serverMessage = error.response?.data?.message
      if (
        typeof serverMessage === 'string' &&
        serverMessage.trim().length > 0
      ) {
        // 将后端返回的国际化错误消息覆写到 error.message，方便上层组件捕获展示
        error.message = serverMessage
      }
    }
    return Promise.reject(error)
  }
)
