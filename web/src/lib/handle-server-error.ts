import { AxiosError } from 'axios'
import { toast } from 'sonner'
import { log } from './logger'

export function handleServerError(error: unknown): void {
  if (error instanceof AxiosError) {
    // Report the status and the stable error code only. The response message is
    // server-authored prose and may echo request details, so it is never
    // uploaded; it is still shown to the user through the toast below.
    log('error', 'server.error', {
      status: error.response?.status,
      code:
        typeof error.response?.data?.code === 'string'
          ? error.response.data.code
          : undefined,
    })
  } else {
    log('error', 'server.error')
  }

  let errMsg = 'Something went wrong!'

  if (
    error &&
    typeof error === 'object' &&
    'status' in error &&
    Number(error.status) === 204
  ) {
    errMsg = 'No content.'
  }

  if (error instanceof AxiosError) {
    if (error.response?.status === 304) {
      errMsg = 'Content not modified!'
    } else {
      const message = error.response?.data?.message
      const title = error.response?.data?.title
      if (typeof message === 'string' && message.length > 0) {
        errMsg = message
      } else if (typeof title === 'string' && title.length > 0) {
        errMsg = title
      } else if (error.response && error.response.data instanceof Blob) {
        // `responseType: 'blob'` 的下载类请求（CSV/图片）没有 JSON 信封可用，
        // 响应拦截器会先把 Blob 里的服务端消息抬到 `error.message`。
        // 这里必须取回它，否则用户拿到的是一句无关的通用文案。
        if (error.message.trim().length > 0) errMsg = error.message
      } else if (!error.response && error.message.trim().length > 0) {
        // 根本没拿到响应（断网 / DNS 失败 / 超时）：`Network Error` 这类
        // 文案比「出了点问题」有用得多。
        errMsg = error.message
      }
    }
  }

  toast.error(errMsg, { id: errMsg })
}

/**
 * 取一句可直接展示给用户的错误文案。
 *
 * 调用方已用 `suppressGlobalToast` 接管了提示（需要自己控制行内横幅、批量
 * 部分失败等），但**不该自己拼 `String(err)`** —— 那会拿到 axios 的英文
 * 兜底文案，绕过服务端的本地化消息。这里复用 `handleServerError` 的同一套
 * 取值规则，只把 toast 换给调用方决定。
 */
export function errorMessageOf(error: unknown): string {
  if (error instanceof AxiosError) {
    const message = error.response?.data?.message
    const title = error.response?.data?.title
    if (typeof message === 'string' && message.length > 0) return message
    if (typeof title === 'string' && title.length > 0) return title
    if (error.response?.data instanceof Blob && error.message.trim()) {
      return error.message
    }
    if (!error.response && error.message.trim().length > 0) {
      return error.message
    }
    return 'Something went wrong!'
  }
  if (error instanceof Error && error.message.trim().length > 0) {
    return error.message
  }
  const raw = String(error)
  return raw.trim().length > 0 ? raw : 'Something went wrong!'
}
