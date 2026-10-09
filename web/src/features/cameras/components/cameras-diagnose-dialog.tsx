import { useEffect } from 'react'
import {
  Activity,
  AlertCircle,
  CheckCircle2,
  Loader2,
  RefreshCw,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { useDiagnoseCamera } from '../hooks/use-cameras'
import { useCamerasContext } from './cameras-provider'

export function CamerasDiagnoseDialog() {
  const { t } = useTranslation('cameras')
  const { open, setOpen, currentRow } = useCamerasContext()
  const isOpen = open === 'diagnose' && Boolean(currentRow)

  const diagnoseMutation = useDiagnoseCamera()

  const runDiagnose = () => {
    if (currentRow?.id) {
      diagnoseMutation.mutate(currentRow.id)
    }
  }

  useEffect(() => {
    if (isOpen && currentRow?.id) {
      diagnoseMutation.mutate(currentRow.id)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isOpen, currentRow?.id])

  const state = diagnoseMutation.data?.state
  const isHealthy = state?.health === 'online'
  const isPending = diagnoseMutation.isPending

  const mainStream = currentRow?.streams?.find((s) => s.role === 'main')
  const codec = mainStream?.codec || 'H.264'
  const resolution =
    mainStream?.width && mainStream?.height
      ? `${mainStream.width}x${mainStream.height}`
      : '1920x1080'
  const fps =
    mainStream?.fpsString ||
    (mainStream?.fps ? `${mainStream.fps} fps` : '25 fps')
  const checkedTime = state?.lastCheckedAt
    ? new Date(state.lastCheckedAt).toLocaleTimeString()
    : new Date().toLocaleTimeString()

  return (
    <Dialog open={isOpen} onOpenChange={(val) => !val && setOpen(null)}>
      <DialogContent className='sm:max-w-md'>
        <DialogHeader className='text-start'>
          <DialogTitle className='flex items-center gap-2'>
            <Activity className='size-5 text-primary' />
            <span>{t('dialog.diagnoseTitle')}</span>
          </DialogTitle>
          <DialogDescription>
            {currentRow?.name} ({currentRow?.id})
          </DialogDescription>
        </DialogHeader>

        <div className='space-y-4 py-2'>
          {isPending ? (
            <div className='flex items-center justify-center gap-2 rounded-lg border bg-muted/40 p-8 text-sm text-muted-foreground'>
              <Loader2 className='size-5 animate-spin text-primary' />
              <span>{t('dialog.diagnosing')}</span>
            </div>
          ) : diagnoseMutation.isError ? (
            <div className='flex items-start gap-2.5 rounded-lg border border-destructive/30 bg-destructive/10 p-3.5 text-xs text-destructive'>
              <AlertCircle className='mt-0.5 size-4 shrink-0' />
              <div className='space-y-1'>
                <p className='font-semibold'>{t('diagnose.requestFailed')}</p>
                <p className='leading-relaxed opacity-90'>
                  {diagnoseMutation.error?.message ||
                    'Network error or service unavailable'}
                </p>
              </div>
            </div>
          ) : diagnoseMutation.data ? (
            <div className='space-y-3.5'>
              {/* 核心结论横幅 */}
              <div
                className={`flex items-center justify-between rounded-lg border p-3.5 text-sm ${
                  isHealthy
                    ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-800 dark:text-emerald-300'
                    : 'border-destructive/30 bg-destructive/10 text-destructive'
                }`}
              >
                <div className='flex items-center gap-2'>
                  {isHealthy ? (
                    <CheckCircle2 className='size-4 text-emerald-600 dark:text-emerald-400' />
                  ) : (
                    <AlertCircle className='size-4 text-destructive' />
                  )}
                  <span className='font-medium'>
                    {isHealthy ? t('diagnose.healthy') : t('diagnose.failed')}
                  </span>
                </div>

                {state?.health && (
                  <Badge
                    variant='outline'
                    className='text-xs font-normal capitalize'
                  >
                    {t(`status.${state.health}`, {
                      defaultValue: state.health,
                    })}
                  </Badge>
                )}
              </div>

              {/* 纯净 Key-Value 规格参数列表 */}
              <div className='divide-y divide-border rounded-lg border border-border text-sm'>
                <div className='flex items-center justify-between px-3.5 py-2.5'>
                  <span className='text-muted-foreground'>
                    {t('diagnose.codec')}
                  </span>
                  <span className='font-mono font-medium uppercase'>
                    {codec}
                  </span>
                </div>
                <div className='flex items-center justify-between px-3.5 py-2.5'>
                  <span className='text-muted-foreground'>
                    {t('diagnose.resolution')}
                  </span>
                  <span className='font-mono font-medium'>{resolution}</span>
                </div>
                <div className='flex items-center justify-between px-3.5 py-2.5'>
                  <span className='text-muted-foreground'>
                    {t('diagnose.fps')}
                  </span>
                  <span className='font-mono font-medium'>{fps}</span>
                </div>
                <div className='flex items-center justify-between px-3.5 py-2.5'>
                  <span className='text-muted-foreground'>
                    {t('diagnose.lastChecked')}
                  </span>
                  <span className='text-xs text-muted-foreground'>
                    {checkedTime}
                  </span>
                </div>
              </div>

              {state?.reason && (
                <div className='space-y-1 rounded-lg border border-destructive/20 bg-destructive/5 p-3 text-xs text-destructive'>
                  <span className='font-semibold'>{t('diagnose.reason')}</span>
                  <p className='font-mono leading-relaxed text-muted-foreground'>
                    {state.reason}
                  </p>
                </div>
              )}
            </div>
          ) : null}
        </div>

        <DialogFooter className='gap-y-2 pt-2 sm:justify-between'>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={runDiagnose}
            disabled={isPending}
          >
            <RefreshCw
              className={`me-1.5 size-3.5 ${isPending ? 'animate-spin' : ''}`}
            />
            {t('actions.reDiagnose')}
          </Button>
          <Button type='button' size='sm' onClick={() => setOpen(null)}>
            {t('actions.close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
