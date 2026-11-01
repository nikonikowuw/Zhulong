import { AxiosError } from 'axios'
import { type TFunction } from 'i18next'

/**
 * 运行态诊断码 → 文案。
 *
 * 这些码是界面自己的词汇：后端 `runtime.detail` 直接存机器码（`internal/gb28181`），
 * 不带本地化文案，因此映射归界面所有。
 */
const RUNTIME_DETAIL_KEYS: Record<string, string> = {
  config_unreadable: 'runtime.detail.configUnreadable',
  media_address_unresolvable: 'runtime.detail.mediaAddressUnresolvable',
  config_invalid: 'runtime.detail.configInvalid',
  ingress_unavailable: 'runtime.detail.ingressUnavailable',
  ingress_start_failed: 'runtime.detail.ingressStartFailed',
  ingress_stop_failed: 'runtime.detail.ingressStopFailed',
  ingress_serve_failed: 'runtime.detail.ingressServeFailed',
  registration_reset_failed: 'runtime.detail.registrationResetFailed',
}

/**
 * 把任意异常转成一句可展示的话。
 *
 * 业务错误的文案由后端按 `Accept-Language` 本地化后放进信封 `message`
 * （`internal/httputil/locale.go` 是唯一真值），前端**不维护**「错误码 → 文案」的
 * 第二份映射 —— 见 `.trellis/spec/frontend/theme-and-i18n.md` §3.3。
 * 这里只做两件事：取服务端 message；拿不到时给本地兜底。
 *
 * 「拿不到」有两种：请求根本没到达服务端（无 `response`），或信封没有 message。
 * 两者的 `error.message` 都是 axios 的英文串（`Network Error` 等），
 * 直接展示会把英文塞进中文界面，所以一律换成本地文案。
 */
export function describeGb28181Error(error: unknown, t: TFunction): string {
  if (error instanceof AxiosError) {
    const serverMessage = error.response ? error.message.trim() : ''
    if (serverMessage !== '') return serverMessage
    return t('errors.requestFailed')
  }
  if (error instanceof Error && error.message.trim() !== '') {
    return error.message
  }
  return t('errors.unknown')
}

/** 运行态 detail 的描述；空 detail 表示没有异常。 */
export function describeRuntimeDetail(detail: string, t: TFunction): string {
  if (detail === '') return ''
  const key = RUNTIME_DETAIL_KEYS[detail]
  return key ? t(key) : detail
}
