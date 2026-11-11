import { z } from 'zod'
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
 * multipart 上传必须在**请求转换前**覆盖掉默认的 `application/json`。
 *
 * Axios 1.15 的 `transformRequest` 见到 `isFormData(data)` 且 contentType 含
 * `application/json` 时，会执行 `JSON.stringify(formDataToJSON(data))` ——
 * `file` 字段变成一个空的 `{}`，文件字节整段丢掉。不显式覆盖头的后果是：
 * 前端明明选了文件，后端 `c.FormFile("file")` 却报 422（见规范 §2.8）。
 *
 * 这里只覆盖到「转换前」；浏览器适配器随后会把这个头移除，让浏览器自己写出
 * 带 boundary 的真实 `multipart/form-data; boundary=…`。**不要手工拼 boundary。**
 */
export const MULTIPART_HEADERS = {
  'Content-Type': 'multipart/form-data',
} as const

/** 错误信封的运行时形状。外部输入一律经 Zod 解析，不做 `as` 断言。 */
const errorEnvelopeSchema = z.object({
  code: z.string(),
  message: z.string(),
  data: z.unknown().optional(),
})

/**
 * 把一个 `responseType: 'blob'` 响应体里意外出现的 JSON 错误信封还原成错误。
 *
 * Axios 在 blob 模式下不会反序列化响应体，`error.response.data` 是个 Blob，
 * 于是 `data.message` 恒为 `undefined`，全局 toast 会退化成
 * `Request failed with status code 500`。
 *
 * ⚠️ 只在**错误路径**调用：2xx 响应才会落进调用方的正常分支，而错误信封
 * 只可能出现在 reject 分支的 `error.response.data` 上。
 */
async function readBlobErrorEnvelope(
  body: unknown
): Promise<{ code: string; message: string } | null> {
  if (!(body instanceof Blob)) return null
  try {
    const parsed = errorEnvelopeSchema.safeParse(JSON.parse(await body.text()))
    if (parsed.success) {
      return { code: parsed.data.code, message: parsed.data.message }
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
  // 响应拦截器：错误路径抬升服务端消息（含 blob 错误体）
  async (error: unknown) => {
    if (error instanceof AxiosError) {
      const serverMessage = error.response?.data?.message
      if (
        typeof serverMessage === 'string' &&
        serverMessage.trim().length > 0
      ) {
        // 将后端返回的国际化错误消息覆写到 error.message，方便上层组件捕获展示
        error.message = serverMessage
        return Promise.reject(error)
      }

      // blob 响应（CSV/图片下载）的错误体是 Blob，`data.message` 恒为
      // undefined。在这里读回文本并把服务端消息抬到 `error.message`，
      // 所有下载类调用的错误提示就自动正确了，无需各自处理。
      if (error.response?.data instanceof Blob) {
        const envelope = await readBlobErrorEnvelope(error.response.data)
        if (envelope) {
          error.message = envelope.message
        }
      }
    }
    return Promise.reject(error)
  }
)
