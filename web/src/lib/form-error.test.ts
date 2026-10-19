import { AxiosError } from 'axios'
import { type UseFormReturn } from 'react-hook-form'
import { describe, expect, it, vi } from 'vitest'
import { handleFormError } from './form-error'

function createMockForm<T extends Record<string, unknown>>(initialValues: T) {
  const values = { ...initialValues }
  const setError = vi.fn()
  const getValues = vi.fn(() => values)

  return {
    getValues,
    setError,
  } as unknown as UseFormReturn<T>
}

function createAxiosError(
  status: number,
  data?: Record<string, unknown>,
  message = 'Request failed'
): AxiosError {
  const error = new AxiosError(message)
  error.response = {
    status,
    statusText: String(status),
    headers: {},
    config: {} as AxiosError['config'],
    data: data || {},
  } as AxiosError['response']
  return error
}

describe('handleFormError', () => {
  it('handles non-Axios errors gracefully', () => {
    const form = createMockForm({ name: '' })
    const result = handleFormError(form, new Error('Something broke'))

    expect(result.isAxiosError).toBe(false)
    expect(result.hasFieldErrors).toBe(false)
    expect(result.serverMessage).toBe('Something broke')
    expect(form.setError).not.toHaveBeenCalled()
  })

  it('binds single field validation error to the form control', () => {
    const form = createMockForm({ name: '', rtspUrl: '' })
    const error = createAxiosError(422, {
      code: 'VALIDATION_FAILED',
      message: '参数校验失败',
      details: [{ field: 'name', code: 'REQUIRED', message: '名称不能为空' }],
    })

    const result = handleFormError(form, error)

    expect(result.isAxiosError).toBe(true)
    expect(result.status).toBe(422)
    expect(result.hasFieldErrors).toBe(true)
    expect(result.matchedFields).toEqual(['name'])
    expect(form.setError).toHaveBeenCalledWith(
      'name',
      { type: 'server', message: '名称不能为空' },
      { shouldFocus: true }
    )
  })

  it('binds multiple fields and focuses only the first one', () => {
    const form = createMockForm({ name: '', rtspUrl: '' })
    const error = createAxiosError(422, {
      code: 'VALIDATION_FAILED',
      message: '多个字段错误',
      details: [
        { field: 'name', code: 'REQUIRED', message: '名称必填' },
        { field: 'rtspUrl', code: 'INVALID_URL', message: 'RTSP 地址无效' },
      ],
    })

    const result = handleFormError(form, error)

    expect(result.matchedFields).toEqual(['name', 'rtspUrl'])
    expect(form.setError).toHaveBeenNthCalledWith(
      1,
      'name',
      { type: 'server', message: '名称必填' },
      { shouldFocus: true }
    )
    expect(form.setError).toHaveBeenNthCalledWith(
      2,
      'rtspUrl',
      { type: 'server', message: 'RTSP 地址无效' },
      { shouldFocus: false }
    )
  })

  it('supports nested object paths', () => {
    const form = createMockForm({
      mainStream: {
        rtspUrl: '',
      },
    })
    const error = createAxiosError(422, {
      details: [
        {
          field: 'mainStream.rtspUrl',
          code: 'INVALID_URL',
          message: '主码流地址格式不正确',
        },
      ],
    })

    const result = handleFormError(form, error)

    expect(result.matchedFields).toEqual(['mainStream.rtspUrl'])
    expect(form.setError).toHaveBeenCalledWith(
      'mainStream.rtspUrl',
      { type: 'server', message: '主码流地址格式不正确' },
      { shouldFocus: true }
    )
  })

  it('uses fieldMap when backend field path diverges from frontend form path', () => {
    const form = createMockForm({ mainRtspUrl: '' })
    const error = createAxiosError(422, {
      details: [
        {
          field: 'mainStream.rtspUrl',
          code: 'REQUIRED',
          message: '请填写主码流地址',
        },
      ],
    })

    const result = handleFormError(form, error, {
      fieldMap: {
        'mainStream.rtspUrl': 'mainRtspUrl',
      },
    })

    expect(result.matchedFields).toEqual(['mainRtspUrl'])
    expect(form.setError).toHaveBeenCalledWith(
      'mainRtspUrl',
      { type: 'server', message: '请填写主码流地址' },
      { shouldFocus: true }
    )
  })

  it('triggers onUnhandled when details do not match any known form fields', () => {
    const form = createMockForm({ name: '' })
    const onUnhandled = vi.fn()
    const error = createAxiosError(400, {
      code: 'DEVICE_UNREACHABLE',
      message: '目标网关无法连通',
      details: [{ field: 'nonExistent', code: 'ERR', message: '未知错误' }],
    })

    const result = handleFormError(form, error, { onUnhandled })

    expect(result.hasFieldErrors).toBe(false)
    expect(form.setError).not.toHaveBeenCalled()
    expect(onUnhandled).toHaveBeenCalledWith('目标网关无法连通', error)
  })

  it('identifies 409 CAS conflict and calls onConflict', () => {
    const form = createMockForm({ name: '', revision: 1 })
    const onConflict = vi.fn()
    const error = createAxiosError(409, {
      code: 'CAS_CONFLICT',
      message: '摄像头已被其他管理员更新，当前版本已失效',
    })

    const result = handleFormError(form, error, { onConflict })

    expect(result.isConflict).toBe(true)
    expect(onConflict).toHaveBeenCalledWith(
      error,
      '摄像头已被其他管理员更新，当前版本已失效'
    )
  })
})
