import { AxiosError } from 'axios'
import { toast } from 'sonner'
import i18n from './i18n'
import { log } from './logger'

/** 网关类状态码：后端没起来时 Vite/反代会返回 HTML，拿不到信封 message。 */
const GATEWAY_STATUSES = new Set([502, 503, 504])

interface HandleServerErrorOptions {
  /**
   * 只记录日志、不弹 toast。调用方要自己渲染常驻错误横幅时使用
   * （例如初始化表单：toast 5 秒消失，且离表单太远）。
   */
  silent?: boolean
}

/**
 * 从任意错误里取一句可直接展示的本地化文案。
 *
 * 优先级：服务端信封 message/title → blob 里抬上来的 message →
 * 网络层 message → 网关/通用兜底。
 */
function resolveMessage(error: unknown): string {
  if (error instanceof AxiosError) {
    if (error.response?.status === 304) {
      return i18n.t('serverError.notModified', {
        defaultValue: 'Content not modified!',
      })
    }

    const message = error.response?.data?.message
    const title = error.response?.data?.title
    if (typeof message === 'string' && message.length > 0) return message
    if (typeof title === 'string' && title.length > 0) return title

    if (error.response?.data instanceof Blob) {
      // `responseType: 'blob'` 的下载类请求（CSV/图片）没有 JSON 信封可用，
      // 响应拦截器会先把 Blob 里的服务端消息抬到 `error.message`。
      // 这里必须取回它，否则用户拿到的是一句无关的通用文案。
      if (error.message.trim().length > 0) return error.message
    }

    if (!error.response) {
      // 根本没拿到响应（断网 / DNS 失败 / 超时）：`Network Error` 这类
      // 文案比「出了点问题」有用得多。
      if (error.message.trim().length > 0) return error.message
    } else if (GATEWAY_STATUSES.has(error.response.status)) {
      // 有响应但不是信封（典型：后端未启动，Vite 代理回 502 HTML）。
      // 「请求处理失败」会让人以为是密码错了，这里要明确是服务不可用。
      return i18n.t('serverError.badGateway', {
        defaultValue: 'Cannot reach the service, please try again later.',
      })
    }
  }

  return i18n.t('serverError.generic', {
    defaultValue: 'Something went wrong!',
  })
}

export function handleServerError(
  error: unknown,
  options: HandleServerErrorOptions = {}
): void {
  if (error instanceof AxiosError) {
    // Report the status and the stable error code only. The response message is
    // server-authored prose and may echo request details, so it is never
    // uploaded; it is still shown to the user through a toast or caller-rendered banner.
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

  let errMsg = resolveMessage(error)

  if (
    error &&
    typeof error === 'object' &&
    'status' in error &&
    Number(error.status) === 204
  ) {
    errMsg = i18n.t('serverError.noContent', {
      defaultValue: 'No content.',
    })
  }

  if (options.silent) return

  toast.error(errMsg, { id: errMsg })
}

/**
 * 取一句可直接展示给用户的错误文案。
 *
 * 调用方已自己接管提示（行内横幅、批量部分失败等），但**不该自己拼
 * `String(err)`** —— 那会拿到 axios 的英文兜底文案，绕过服务端的本地化消息。
 * 这里复用 `handleServerError` 的同一套取值规则，只把 toast 换给调用方决定。
 */
export function errorMessageOf(error: unknown): string {
  if (error instanceof AxiosError) {
    return resolveMessage(error)
  }
  if (error instanceof Error && error.message.trim().length > 0) {
    return error.message
  }
  const raw = String(error)
  return raw.trim().length > 0
    ? raw
    : i18n.t('serverError.generic', {
        defaultValue: 'Something went wrong!',
      })
}
