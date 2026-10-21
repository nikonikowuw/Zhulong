import { AxiosError } from 'axios'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { handleServerError } from './handle-server-error'
import { __testing } from './logger'

const toastError = vi.hoisted(() => vi.fn())

vi.mock('sonner', () => ({
  toast: {
    error: toastError,
  },
}))

beforeEach(() => {
  vi.mocked(toastError).mockClear()
})

describe('handleServerError', () => {
  it('shows a generic message when the error is not recognised', () => {
    handleServerError(new Error('network'))

    expect(toastError).toHaveBeenCalledWith('Something went wrong!', {
      id: 'Something went wrong!',
    })
  })

  it('maps a plain object with status 204 to the no-content message', () => {
    handleServerError({ status: 204 })

    expect(toastError).toHaveBeenCalledWith('No content.', { id: 'No content.' })
  })

  it('prefers the API title when the error is an Axios error with response data', () => {
    const error = new AxiosError('Bad request')
    error.response = {
      status: 422,
      data: { title: 'Validation failed' },
    } as AxiosError['response']

    handleServerError(error)

    expect(toastError).toHaveBeenCalledWith('Validation failed', {
      id: 'Validation failed',
    })
  })

  it('falls back to the generic message when Axios response has no data.title', () => {
    const error = new AxiosError('Request failed')
    error.response = {
      status: 500,
      data: {},
    } as AxiosError['response']

    handleServerError(error)

    expect(toastError).toHaveBeenCalledWith('Something went wrong!', {
      id: 'Something went wrong!',
    })
  })

  it('falls back to the generic message when Axios data.title is an empty string', () => {
    const error = new AxiosError('Bad request')
    error.response = {
      status: 400,
      data: { title: '' },
    } as AxiosError['response']

    handleServerError(error)

    expect(toastError).toHaveBeenCalledWith('Something went wrong!', {
      id: 'Something went wrong!',
    })
  })

  it('reports a stable event id without the raw error message', () => {
    __testing.reset()
    __testing.setProduction(true)
    const err = new Error('secret token=abc123 in message')

    handleServerError(err)

    const entries = __testing.pendingEntries()
    expect(entries).toHaveLength(1)
    expect(entries[0].msg).toBe('server.error')
    // The raw message must never leave the browser.
    expect(JSON.stringify(entries)).not.toContain('abc123')
    __testing.reset()
  })

  it('reports the response status and stable code, not the server message', () => {
    __testing.reset()
    __testing.setProduction(true)
    const error = new AxiosError('boom')
    error.response = {
      status: 503,
      data: { code: 'SERVICE_UNAVAILABLE', message: 'internal detail here' },
    } as AxiosError['response']

    handleServerError(error)

    const [entry] = __testing.pendingEntries()
    expect(entry.fields).toEqual({ status: 503, code: 'SERVICE_UNAVAILABLE' })
    expect(JSON.stringify(entry)).not.toContain('internal detail here')
    __testing.reset()
  })

  it('does not queue anything in development', () => {
    __testing.reset()
    __testing.setProduction(false)

    handleServerError(new Error('not uploaded'))

    expect(__testing.bufferLength()).toBe(0)
    __testing.reset()
  })
})
