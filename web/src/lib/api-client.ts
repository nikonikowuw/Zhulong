import axios, { AxiosError, type AxiosResponse } from 'axios'

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

// 响应拦截器：自动解包信封与错误信息转换
apiClient.interceptors.response.use(
  (response: AxiosResponse<ApiResponse>) => {
    // 后端返回标准 envelope 结构 { code, message, data }
    return response
  },
  (error: unknown) => {
    if (error instanceof AxiosError) {
      const serverMessage = error.response?.data?.message
      if (typeof serverMessage === 'string' && serverMessage.trim().length > 0) {
        // 将后端返回的国际化错误消息覆写到 error.message，方便上层组件捕获展示
        error.message = serverMessage
      }
    }
    return Promise.reject(error)
  }
)
