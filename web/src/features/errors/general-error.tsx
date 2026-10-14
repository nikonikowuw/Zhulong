import { useEffect } from 'react'
import { useNavigate, useRouter } from '@tanstack/react-router'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'

type GeneralErrorProps = React.HTMLAttributes<HTMLDivElement> & {
  error?: unknown
  reset?: () => void
  minimal?: boolean
}

export function GeneralError({
  className,
  error,
  reset,
  minimal = false,
}: GeneralErrorProps) {
  const navigate = useNavigate()
  const { history } = useRouter()

  useEffect(() => {
    if (error) {
      // 始终在控制台输出真实异常，便于开发与排错，避免被误判为后端 HTTP 500
      // eslint-disable-next-line no-console
      console.error('[Router Error Boundary Caught Exception]:', error)
    }
  }, [error])

  const errorMessage =
    error instanceof Error
      ? error.message
      : typeof error === 'string'
        ? error
        : null

  const handleReset = () => {
    if (reset) {
      reset()
    } else {
      window.location.reload()
    }
  }

  return (
    <div className={cn('h-svh w-full', className)}>
      <div className='m-auto flex h-full w-full flex-col items-center justify-center gap-2 px-4'>
        {!minimal && (
          <h1 className='text-[7rem] leading-tight font-bold'>500</h1>
        )}
        <span className='font-medium'>Oops! Something went wrong {`:')`}</span>
        <p className='text-center text-muted-foreground'>
          We apologize for the inconvenience. <br /> Please try again later.
        </p>

        {import.meta.env.DEV && errorMessage && (
          <div className='my-2 max-h-48 max-w-xl overflow-auto rounded-md border border-destructive/50 bg-destructive/10 p-3 text-left font-mono text-xs text-destructive'>
            <p className='mb-1 font-bold'>[Development Mode Error Trace]</p>
            <p className='break-all whitespace-pre-wrap'>{errorMessage}</p>
          </div>
        )}

        {!minimal && (
          <div className='mt-6 flex flex-wrap justify-center gap-4'>
            {reset && (
              <Button variant='default' onClick={handleReset}>
                Try Again
              </Button>
            )}
            <Button variant='outline' onClick={() => history.go(-1)}>
              Go Back
            </Button>
            <Button
              onClick={() => {
                reset?.()
                navigate({ to: '/' })
              }}
            >
              Back to Home
            </Button>
          </div>
        )}
      </div>
    </div>
  )
}
