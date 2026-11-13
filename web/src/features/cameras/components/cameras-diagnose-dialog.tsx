import { useEffect } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import {
  Activity,
  AlertCircle,
  CheckCircle2,
  Loader2,
  RefreshCw,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import i18n from '@/lib/i18n'
import { statusLevelClass, statusTextClass } from '@/lib/status-palette'
import { cn } from '@/lib/utils'
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
import { type Camera, type CameraListResponse } from '../data/schema'
import { CAMERAS_QUERY_KEY, useDiagnoseCamera } from '../hooks/use-cameras'
import { useCamerasContext } from './cameras-provider'

const checkedTimeFormatters = new Map<string, Intl.DateTimeFormat>()

function formatCheckedTime(raw?: string | null, language?: string): string {
  if (!raw) return '-'
  const date = new Date(raw)
  if (Number.isNaN(date.getTime())) return '-'
  const localeKey = language || 'default'
  let formatter = checkedTimeFormatters.get(localeKey)
  if (!formatter) {
    formatter = new Intl.DateTimeFormat(language || undefined, {
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    })
    checkedTimeFormatters.set(localeKey, formatter)
  }
  return formatter.format(date)
}

interface CamerasDiagnoseDialogBodyProps {
  camera: Camera
  onClose: () => void
}

function CamerasDiagnoseDialogBody({
  camera,
  onClose,
}: CamerasDiagnoseDialogBodyProps) {
  const { t } = useTranslation('cameras')
  const queryClient = useQueryClient()

  const diagnoseMutation = useDiagnoseCamera({ suppressGlobalToast: true })
  const { mutate, mutateAsync, reset } = diagnoseMutation

  useEffect(() => {
    reset()
    mutate(camera.id, {
      onSuccess: () => {
        void queryClient.invalidateQueries({ queryKey: CAMERAS_QUERY_KEY })
      },
    })
  }, [camera.id, mutate, reset, queryClient])

  const runDiagnose = async () => {
    try {
      await toast.promise(
        mutateAsync(camera.id).then((res) => {
          void queryClient.invalidateQueries({ queryKey: CAMERAS_QUERY_KEY })
          return res
        }),
        {
          loading: t('toast.diagnosing'),
          success: (res) =>
            res.state?.health === 'online'
              ? t('toast.diagnoseOnline')
              : t('toast.diagnoseOffline'),
          error: (err) =>
            err instanceof Error ? err.message : t('toast.diagnoseFailed'),
        }
      )
    } catch {
      // toast.promise 统一反馈
    }
  }

  const cachedList = queryClient.getQueryData<CameraListResponse>([
    ...CAMERAS_QUERY_KEY,
    'all',
  ])
  const latestCamera =
    cachedList?.items.find((item) => item.id === camera.id) ?? camera

  const state = diagnoseMutation.data?.state
  const isHealthy = state?.health === 'online'
  const isPending = diagnoseMutation.isPending

  const mainStream = latestCamera.streams?.find((s) => s.role === 'main')
  const codec = mainStream?.codec || '-'
  const resolution =
    mainStream?.width && mainStream?.height
      ? `${mainStream.width}x${mainStream.height}`
      : '-'
  const fps =
    mainStream?.fpsString || (mainStream?.fps ? `${mainStream.fps} fps` : '-')
  const checkedTime = formatCheckedTime(state?.lastCheckedAt, i18n.language)

  return (
    <>
      <DialogHeader className='text-start'>
        <DialogTitle className='flex items-center gap-2'>
          <Activity className='size-5 text-primary' />
          <span>{t('dialog.diagnoseTitle')}</span>
        </DialogTitle>
        <DialogDescription>
          {latestCamera.name} ({latestCamera.id})
        </DialogDescription>
      </DialogHeader>

      <div className='space-y-4 py-2'>
        {isPending ? (
          <div className='flex items-center justify-center gap-2 rounded-lg border bg-muted/40 p-8 text-sm text-muted-foreground'>
            <Loader2 className='size-5 animate-spin text-primary' />
            <span>{t('dialog.diagnosing')}</span>
          </div>
        ) : diagnoseMutation.isError ? (
          <div
            className={cn(
              'flex items-start gap-2.5 rounded-lg border p-3.5 text-xs',
              statusLevelClass('destructive')
            )}
          >
            <AlertCircle className='mt-0.5 size-4 shrink-0' />
            <div className='space-y-1'>
              <p className='font-semibold'>{t('diagnose.requestFailed')}</p>
              <p className='leading-relaxed opacity-90'>
                {diagnoseMutation.error?.message ||
                  t('diagnose.serviceUnavailable')}
              </p>
            </div>
          </div>
        ) : diagnoseMutation.data ? (
          <div className='space-y-3.5'>
            {/* 核心结论横幅 */}
            <div
              className={cn(
                'flex items-center justify-between rounded-lg border p-3.5 text-sm',
                statusLevelClass(isHealthy ? 'active' : 'destructive')
              )}
            >
              <div className='flex items-center gap-2'>
                {isHealthy ? (
                  <CheckCircle2
                    className={cn('size-4', statusTextClass('active'))}
                  />
                ) : (
                  <AlertCircle
                    className={cn('size-4', statusTextClass('destructive'))}
                  />
                )}
                <span className='font-medium'>
                  {isHealthy ? t('diagnose.healthy') : t('diagnose.failed')}
                </span>
              </div>

              {state?.health ? (
                <Badge
                  variant='outline'
                  className='text-xs font-normal capitalize'
                >
                  {t(`status.${state.health}`, {
                    defaultValue: state.health,
                  })}
                </Badge>
              ) : null}
            </div>

            {/* 纯净 Key-Value 规格参数列表 */}
            <div className='divide-y divide-border rounded-lg border border-border text-sm'>
              <div className='flex items-center justify-between px-3.5 py-2.5'>
                <span className='text-muted-foreground'>
                  {t('diagnose.codec')}
                </span>
                <span className='font-mono font-medium uppercase'>{codec}</span>
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

            {state?.reason ? (
              <div className='space-y-1 rounded-lg border border-destructive/20 bg-destructive/5 p-3 text-xs text-destructive'>
                <span className='font-semibold'>{t('diagnose.reason')}</span>
                <p className='font-mono leading-relaxed text-muted-foreground'>
                  {state.reason}
                </p>
              </div>
            ) : null}
          </div>
        ) : null}
      </div>

      <DialogFooter className='gap-y-2 pt-2 sm:justify-between'>
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={() => void runDiagnose()}
          disabled={isPending}
        >
          <RefreshCw
            className={cn('me-1.5 size-3.5', isPending && 'animate-spin')}
          />
          {t('actions.reDiagnose')}
        </Button>
        <Button type='button' size='sm' onClick={onClose}>
          {t('actions.close')}
        </Button>
      </DialogFooter>
    </>
  )
}

export function CamerasDiagnoseDialog() {
  const { open, setOpen, currentRow } = useCamerasContext()
  const isOpen = open === 'diagnose' && Boolean(currentRow)

  return (
    <Dialog
      open={isOpen}
      onOpenChange={(val) => {
        if (!val) {
          setOpen(null)
        }
      }}
    >
      <DialogContent className='sm:max-w-md'>
        {isOpen && currentRow ? (
          <CamerasDiagnoseDialogBody
            key={currentRow.id}
            camera={currentRow}
            onClose={() => setOpen(null)}
          />
        ) : null}
      </DialogContent>
    </Dialog>
  )
}
