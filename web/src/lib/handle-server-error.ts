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
      }
    }
  }

  toast.error(errMsg, { id: errMsg })
}
