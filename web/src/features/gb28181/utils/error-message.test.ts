import { AxiosError, AxiosHeaders } from 'axios'
import { type TFunction } from 'i18next'
import { describe, expect, it } from 'vitest'
import { describeGb28181Error, describeRuntimeDetail } from './error-message'

/** 只把 key 原样返回，断言因此与语种无关。 */
const t = ((key: string) => key) as unknown as TFunction

function httpError(serverMessage: string): AxiosError {
  return new AxiosError(
    serverMessage,
    'ERR_BAD_REQUEST',
    { headers: new AxiosHeaders() },
    undefined,
    {
      status: 422,
      statusText: 'Unprocessable Entity',
      data: { code: 'GB28181_CONFIG_INVALID', message: serverMessage },
      headers: {},
      config: { headers: new AxiosHeaders() },
    }
  )
}

describe('describeGb28181Error', () => {
  it('shows the localized server message verbatim', () => {
    // 拦截器已把信封 message 覆写到 error.message；前端不再维护错误码映射。
    expect(describeGb28181Error(httpError('端口区间无效'), t)).toBe(
      '端口区间无效'
    )
  })

  it('does not leak the transport-level axios message when no response arrived', () => {
    const offline = new AxiosError('Network Error')

    expect(describeGb28181Error(offline, t)).toBe('errors.requestFailed')
  })

  it('falls back to the local copy for non-axios failures', () => {
    expect(describeGb28181Error(new Error('boom'), t)).toBe('boom')
    expect(describeGb28181Error(undefined, t)).toBe('errors.unknown')
  })
})

describe('describeRuntimeDetail', () => {
  it('maps known runtime detail codes', () => {
    expect(describeRuntimeDetail('ingress_start_failed', t)).toBe(
      'runtime.detail.ingressStartFailed'
    )
  })

  it('keeps unknown codes verbatim so a new one is still visible', () => {
    expect(describeRuntimeDetail('some_new_code', t)).toBe('some_new_code')
  })

  it('renders nothing when the runtime reports no detail', () => {
    expect(describeRuntimeDetail('', t)).toBe('')
  })
})
